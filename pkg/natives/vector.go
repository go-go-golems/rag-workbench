package natives

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// Embedder is what the vector and rerank components need (llm.Client implements it).
type Embedder interface {
	Embed(ctx context.Context, texts []string, o llm.EmbedOptions) ([][]float32, error)
	Rerank(ctx context.Context, model, query string, docs []string, topN int) ([]llm.RerankResult, error)
}

// Vectors installs index.vector (dense retrieval) and rerank.model (cross-encoder
// reranking). Building and query embedding call the Embedder synchronously from
// the VM goroutine; rerank.model runs its batched requests asynchronously
// (rerank.go, RAGWB-005), so a served runtime keeps answering meanwhile.
type Vectors struct {
	Embedder Embedder
	Cache    *EmbedCache // optional persistent embedding cache
	// Concurrency is the number of embedding batches in flight (default 4).
	Concurrency int
	Log         func(string)
}

func (Vectors) Name() string { return "vectors" }

const vectorsJS = `(function (rag, h) {
  const getPath = (obj, path) => { if (!path) return obj; let cur = obj; for (const k of String(path).split('.')) { if (cur == null) return undefined; cur = cur[k]; } return cur; };
  const show = v => (v == null ? '' : Array.isArray(v) ? v.join(' › ') : typeof v === 'string' ? v : JSON.stringify(v));
  // Text embedded for a chunk: the 'over' fields joined by blank lines (default: title, heading path, text).
  const embedText = (c, over) => over.map(f => show(getPath(c, f))).filter(Boolean).join('\n\n');
  const wrapVec = ix => {
    const o = {
      kind: 'vector', N: ix.n, model: ix.model,
      search: (q, k = 20, o = {}) => ix.search(String(q), Number(k), o.only ? [...o.only] : null),
      searchIn: (docId, q, k = 3) => ix.searchIn(String(docId), String(q), Number(k)),
      toJSON: () => ({ kind: 'vector', chunks: ix.n, model: ix.model, dims: ix.dims }),
    };
    Object.defineProperty(o, '__ix', { value: ix });
    return o;
  };
  rag.define('index.vector', {
    capabilities: ['model'],
    id: () => 'index', phase: 'build', in: ['chunks'], out: 'index',
    describe: p => (p.model || 'emb-granite') + ' · cosine · over ' + [].concat(p.over || ['title', 'headingPath', 'text']).join('+'),
    all: ([dataset], ctx, p) => {
      let chunks = dataset.materialize();
      chunks = chunks || [];
      const over = [].concat(p.over || ['title', 'headingPath', 'text']);
      const ix = h.build(chunks.map(c => c.id), chunks.map(c => c.docId), chunks.map(c => embedText(c, over)),
        String(p.model || 'emb-granite'), Number(p.dimensions || 0));
      ctx.log('embedded ' + ix.n + ' chunks: ' + ix.cached + ' from cache, ' + ix.fresh + ' new, ' + ix.ms + 'ms');
      return wrapVec(ix);
    },
    // artifact store: the Go index travels as its handle (codec "vectors"); restore
    // binds it to this runtime's embedder for query embedding
    persist: { save: v => v.__ix, load: raw => wrapVec(h.restore(raw)) },
  });
  rag.define('rerank.model', {
    capabilities: ['model'],
    id: () => 'rerank', phase: 'live', in: ['hits'], out: 'hits', trigger: 'submit',
    describe: p => (p.model || 'bge-rr-v2-m3') + ' · top ' + (p.k ?? 30),
    // asynchronous and batched (RAGWB-005): the model requests run off the
    // runtime's goroutine, each finished batch reports progress and the ranking
    // so far, and an aborted request cancels the rest
    run: async (s, ctx, hits, p) => {
      hits = hits || [];
      const k = Math.min(hits.length, Number(p.k ?? 30), 99);
      if (!k || !s.q) return hits;
      const head = hits.slice(0, k), rest = hits.slice(k);
      const docs = head.map(x => { const c = ctx.chunk(x.id) || {}; return [c.title, (c.headingPath || []).join(' › '), c.text].filter(Boolean).join('\n\n'); });
      const toHit = r => ({ ...head[r.index], score: +r.score.toFixed(4), reason: 'cross-encoder ' + r.score.toFixed(3) });
      const scored = [];
      const ranked = await h.rerankAsync(String(p.model || 'bge-rr-v2-m3'), String(s.q), docs,
        { batch: Number(p.batch ?? 8), concurrency: Number(p.concurrency ?? 2) },
        part => {
          for (const r of part) scored.push(r);
          if (ctx.progress) ctx.progress(scored.length, docs.length, 'passages');
          if (ctx.partial && scored.length < docs.length) ctx.partial([...scored].sort((a, b) => b.score - a.score || a.index - b.index).map(toHit));
        }, ctx.signal);
      const out = ranked.map(toHit);
      return p.keepTail === false ? out : out.concat(rest);
    },
  });
  // shortcut like rag.bm25 / rag.terms; late-bound because refreshFactories() rebuilds rag.index on every define
  rag.vector = (...a) => rag.index.vector(...a);
})`

type vecIndex struct {
	ids, docIDs []string
	vecs        [][]float32
	dims        int // actual vector length
	reqDims     int // requested `dimensions` (0 = model default), reused for queries
	model       string
	qcache      sync.Map // query → []float32
}

func normalize(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	n = math.Sqrt(n)
	if n == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

func (v *Vectors) embedAll(ctx context.Context, model string, dims int, texts []string) ([][]float32, int, error) {
	vecs, err := v.Cache.Get(model, dims, texts)
	if err != nil {
		return nil, 0, err
	}
	missing := []int{}
	for i, x := range vecs {
		if x == nil {
			missing = append(missing, i)
		}
	}
	const batch = 64
	conc := v.Concurrency
	if conc <= 0 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for start := 0; start < len(missing); start += batch {
		idx := missing[start:min(len(missing), start+batch)]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			in := make([]string, len(idx))
			for j, i := range idx {
				in[j] = texts[i]
			}
			out, err := v.Embedder.Embed(ctx, in, llm.EmbedOptions{Model: model, Dimensions: dims, BatchSize: batch})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			for j, i := range idx {
				vecs[i] = normalize(out[j])
			}
			if err := v.Cache.Put(model, dims, in, out); err != nil && firstErr == nil {
				firstErr = err
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, 0, firstErr
	}
	for i := range vecs {
		vecs[i] = normalize(vecs[i])
	}
	return vecs, len(missing), nil
}

func (v *Vectors) Install(vm *goja.Runtime, rag *goja.Object) error {
	h := vm.NewObject()
	_ = h.Set("build", func(ids, docIDs, texts []string, model string, dims int) (*goja.Object, error) {
		if v.Embedder == nil {
			return nil, fmt.Errorf("index.vector: no embedding backend (run with --embed lunaroute)")
		}
		if len(ids) != len(texts) || len(docIDs) != len(texts) {
			return nil, fmt.Errorf("index.vector: mismatched inputs")
		}
		t0 := time.Now()
		vecs, fresh, err := v.embedAll(context.Background(), model, dims, texts)
		if err != nil {
			return nil, fmt.Errorf("index.vector: %w", err)
		}
		ix := &vecIndex{ids: ids, docIDs: docIDs, vecs: vecs, model: model, reqDims: dims}
		if len(vecs) > 0 {
			ix.dims = len(vecs[0])
		}
		return v.object(vm, ix, len(ids)-fresh, fresh, time.Since(t0).Milliseconds()), nil
	})
	// restore binds a stored index (artifact codec "vectors") to this runtime's embedder
	_ = h.Set("restore", func(raw goja.Value) (*goja.Object, error) {
		ix, ok := raw.Export().(*vecIndex)
		if !ok {
			return nil, fmt.Errorf("index.vector: stored artifact is not a vector index")
		}
		return v.object(vm, ix, len(ix.ids), 0, 0), nil
	})
	_ = h.Set("rerankAsync", v.rerankAsync(vm))
	val, err := vm.RunString(vectorsJS)
	if err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(val)
	if !ok {
		return fmt.Errorf("vectors installer is not a function")
	}
	_, err = fn(goja.Undefined(), rag, h)
	return err
}

// object is the Go side of an index.vector output: counts, search, searchIn, and the
// index itself as a hidden handle for the artifact store.
func (v *Vectors) object(vm *goja.Runtime, ix *vecIndex, cached, fresh int, ms int64) *goja.Object {
	o := vm.NewObject()
	_ = o.DefineDataProperty(goHandleKey, vm.ToValue(ix), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	_ = o.Set("n", len(ix.ids))
	_ = o.Set("cached", cached)
	_ = o.Set("fresh", fresh)
	_ = o.Set("ms", ms)
	_ = o.Set("model", ix.model)
	_ = o.Set("dims", ix.dims)
	_ = o.Set("search", func(q string, k int, only []int64) ([]map[string]any, error) {
		var set map[int]bool
		if only != nil {
			set = map[int]bool{}
			for _, i := range only {
				set[int(i)] = true
			}
		}
		return v.search(ix, q, k, set)
	})
	_ = o.Set("searchIn", func(docID, q string, k int) ([]map[string]any, error) {
		set := map[int]bool{}
		for i, d := range ix.docIDs {
			if d == docID {
				set[i] = true
			}
		}
		return v.search(ix, q, k, set)
	})
	return o
}

func (v *Vectors) search(ix *vecIndex, q string, k int, only map[int]bool) ([]map[string]any, error) {
	if v.Embedder == nil {
		return nil, fmt.Errorf("index.vector query: no embedding backend to embed the query (run with --embed lunaroute)")
	}
	var qv []float32
	if c, ok := ix.qcache.Load(q); ok {
		qv = c.([]float32)
	} else {
		vs, err := v.Embedder.Embed(context.Background(), []string{q}, llm.EmbedOptions{Model: ix.model, Dimensions: ix.reqDims})
		if err != nil {
			return nil, fmt.Errorf("index.vector query: %w", err)
		}
		qv = normalize(vs[0])
		ix.qcache.Store(q, qv)
	}
	type scored struct {
		i int
		s float64
	}
	all := make([]scored, 0, len(ix.vecs))
	for i, vec := range ix.vecs {
		if only != nil && !only[i] {
			continue
		}
		var d float64
		for j := range vec {
			d += float64(vec[j]) * float64(qv[j])
		}
		all = append(all, scored{i, d})
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].s > all[b].s })
	if k < len(all) {
		all = all[:max(0, k)]
	}
	out := make([]map[string]any, len(all))
	for n, x := range all {
		out[n] = map[string]any{"id": ix.ids[x.i], "score": jsToFixed(x.s, 4)}
	}
	return out, nil
}
