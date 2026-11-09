package natives

import (
	"context"
	"fmt"
	"github.com/go-go-golems/rag-workbench/pkg/rtenv"
	"sort"
	"sync"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/runtimebridge"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// RerankBatched (RAGWB-005 §2) scores docs against query in batches of size,
// with at most conc requests in flight. A cross-encoder scores each (query,
// doc) pair on its own, so the merged result equals one request over all docs.
// onBatch (optional, called serially) receives each finished batch with
// indexes into docs. The result is sorted by score, ties by index. The first
// failing batch cancels the rest.
func RerankBatched(ctx context.Context, e Embedder, model, query string, docs []string, size, conc int, onBatch func([]llm.RerankResult)) ([]llm.RerankResult, error) {
	if size <= 0 {
		size = len(docs)
	}
	if conc <= 0 {
		conc = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		all      = make([]llm.RerankResult, 0, len(docs))
		firstErr error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, conc)
	)
	for lo := 0; lo < len(docs); lo += size {
		hi := min(len(docs), lo+size)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := e.Rerank(ctx, model, query, docs[lo:hi], hi-lo)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			part := make([]llm.RerankResult, len(res))
			for i, r := range res {
				if r.Index < 0 || r.Index >= hi-lo {
					if firstErr == nil {
						firstErr = fmt.Errorf("rerank backend returned index %d for a batch of %d", r.Index, hi-lo)
						cancel()
					}
					return
				}
				part[i] = llm.RerankResult{Index: r.Index + lo, Score: r.Score}
			}
			all = append(all, part...)
			if onBatch != nil && firstErr == nil {
				onBatch(part)
			}
		}(lo, hi)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Index < all[j].Index
	})
	return all, nil
}

func rerankJS(vm *goja.Runtime, rs []llm.RerankResult) goja.Value {
	out := make([]any, len(rs))
	for i, r := range rs {
		o := vm.NewObject()
		_ = o.Set("index", r.Index)
		_ = o.Set("score", r.Score)
		out[i] = o
	}
	return vm.NewArray(out...)
}

// rerankAsync is h.rerankAsync(model, query, docs, {batch, concurrency}, onBatch, signal):
// a promise of the merged ranking. The batches run off the runtime's owner
// goroutine; onBatch and the settlement are posted back to the event loop; an
// abort of signal cancels the remaining requests (rejecting with code 'cancelled').
func (v *Vectors) rerankAsync(vm *goja.Runtime) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		promise, resolve, reject := vm.NewPromise()
		fail := func(code, msg string) goja.Value {
			o := vm.NewObject()
			_ = o.Set("code", code)
			_ = o.Set("message", msg)
			_ = reject(o)
			return vm.ToValue(promise)
		}
		if v.Embedder == nil {
			return fail("rerank_unavailable", "rerank.model: no rerank backend (run with --embed lunaroute)")
		}
		services, ok := runtimebridge.Lookup(vm)
		if !ok {
			return fail("rerank_unavailable", "rerank.model: runtime has no event-loop bridge")
		}
		model, query := call.Argument(0).String(), call.Argument(1).String()
		docs := strsFromJS(vm, call.Argument(2))
		opt := optObject(vm, call.Argument(3))
		size, conc := 8, 2
		if x := opt.Get("batch"); present(x) {
			size = int(x.ToInteger())
		}
		if x := opt.Get("concurrency"); present(x) {
			conc = int(x.ToInteger())
		}
		onBatch, hasOnBatch := goja.AssertFunction(call.Argument(4))
		op := rtenv.NewOperation(vm, services, "ragwb.rerank", call.Argument(5), func(kind string) { fail(kind, "the operation ended: "+kind) })
		callCtx := op.Context()
		op.Start(func(context.Context) {
			var cb func([]llm.RerankResult)
			if hasOnBatch {
				cb = func(part []llm.RerankResult) {
					_ = services.PostWithLifetimeContext("ragwb.rerank.batch", func(_ context.Context, vm *goja.Runtime) {
						_, _ = onBatch(goja.Undefined(), rerankJS(vm, part))
					})
				}
			}
			res, err := RerankBatched(callCtx, v.Embedder, model, query, docs, size, conc, cb)
			cancelled := callCtx.Err() != nil && services.Lifetime().Err() == nil
			op.Complete(func(vm *goja.Runtime) {
				if err != nil {
					o := vm.NewObject()
					if cancelled {
						_ = o.Set("code", "cancelled")
						_ = o.Set("message", "the request was cancelled")
					} else {
						_ = o.Set("code", "rerank_failed")
						_ = o.Set("message", "rerank.model: "+err.Error())
					}
					_ = reject(o)
					return
				}
				_ = resolve(rerankJS(vm, res))
			})
		})
		return vm.ToValue(promise)
	}
}
