package runner

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// slowRanker is bowEmbedder with a delay per rerank request, and a count of
// requests that ended because their context was cancelled.
type slowRanker struct {
	bowEmbedder
	delay     time.Duration
	cancelled atomic.Int64
}

func (s *slowRanker) Rerank(ctx context.Context, model, q string, docs []string, topN int) ([]llm.RerankResult, error) {
	select {
	case <-ctx.Done():
		s.cancelled.Add(1)
		return nil, ctx.Err()
	case <-time.After(s.delay):
	}
	return s.bowEmbedder.Rerank(ctx, model, q, docs, topN)
}

const rerankEventsScript = `(async () => {
	const rag = require("ragwb");
	const corpus = rag.corpus("v").from(rag.source.dir({ root: ROOT, include: ["*.md"] }))
		.parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25()).index("vec", rag.vector());
	const plan = (batch) => rag.plan("r").over(corpus).signals({ q: "", k: 5 }).retrieve("vec", { k: 20 }).rerank.model({ k: 12, batch, concurrency: 1 }).limit();
	const lexical = rag.plan("l").over(corpus).signals({ q: "", k: 5 }).retrieve("text", { k: 5 }).limit();
	const q = "rebalance law preserves placements";
	await rag.run(lexical, { q }); await rag.run(plan(4), { q }); // build both first
	const evs = [];
	let firstBatch;
	const batchSeen = new Promise(r => { firstBatch = r; });
	const sc = rag.newScope(ev => { evs.push(ev); if (ev.type === 'progress') firstBatch(); });
	const reranked = rag.withScope(sc, () => rag.run(plan(4), { q }, { trigger: "submit" }));
	await batchSeen;
	// the rerank is still running (3 batches of 4, one at a time): the runtime answers another query meanwhile
	const lex = await rag.run(lexical, { q: "protojson" });
	const progressAtLex = evs.filter(e => e.type === 'progress').length;
	const r = await reranked;
	const single = await rag.run(plan(99), { q }, { trigger: "submit" });
	return JSON.stringify({
		progress: evs.filter(e => e.type === 'progress').map(e => e.done + '/' + e.total),
		partial: evs.filter(e => e.type === 'hits' && e.partial).map(e => e.total),
		final: evs.filter(e => e.type === 'hits' && !e.partial && e.node === 'rerank').length,
		lexHits: lex.hits.length, progressAtLex,
		batched: r.hits.map(h => h.id + ':' + h.score), single: single.hits.map(h => h.id + ':' + h.score),
		reason: r.hits[0].reason || "", errors: [...r.errors, ...single.errors],
	});
})()`

// rerank.model (RAGWB-005 §2): batched requests off the runtime's goroutine,
// progress and partial hits per batch, the same ranking as one request.
func TestRerankModelBatchedAsyncWithProgress(t *testing.T) {
	emb := &slowRanker{delay: 60 * time.Millisecond}
	out := runVectorScript(t, emb, nil, rerankEventsScript)
	if errs := out["errors"].([]any); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if p := out["progress"].([]any); len(p) != 3 || p[0] != "4/12" || p[2] != "12/12" {
		t.Fatalf("progress events: %v", p)
	}
	if p := out["partial"].([]any); len(p) != 2 || p[0].(float64) != 4 || p[1].(float64) != 8 {
		t.Fatalf("partial hits events: %v", p)
	}
	if out["final"].(float64) != 1 || !strings.HasPrefix(out["reason"].(string), "cross-encoder") {
		t.Fatalf("final hits / reason: %v %v", out["final"], out["reason"])
	}
	if out["lexHits"].(float64) == 0 || out["progressAtLex"].(float64) >= 3 {
		t.Fatalf("the runtime did not answer during the rerank: lex hits %v, progress events seen by then %v", out["lexHits"], out["progressAtLex"])
	}
	b, s := out["batched"].([]any), out["single"].([]any)
	if len(b) == 0 || len(b) != len(s) {
		t.Fatalf("batched %v single %v", b, s)
	}
	for i := range b {
		if b[i] != s[i] {
			t.Fatalf("batched ranking differs at %d: %v vs %v", i, b, s)
		}
	}
}

// An aborted scope cancels the rerank's remaining requests.
func TestRerankModelCancelled(t *testing.T) {
	emb := &slowRanker{delay: 80 * time.Millisecond}
	out := runVectorScript(t, emb, nil, `(async () => {
		const rag = require("ragwb");
		const corpus = rag.corpus("v").from(rag.source.dir({ root: ROOT, include: ["*.md"] }))
			.parse(rag.markdown()).chunk(rag.sections()).index("vec", rag.vector());
		const plan = rag.plan("r").over(corpus).signals({ q: "", k: 5 }).retrieve("vec", { k: 20 }).rerank.model({ k: 16, batch: 4, concurrency: 1 }).limit();
		await rag.run(plan, { q: "law" });
		let first; const seen = new Promise(r => { first = r; });
		const sc = rag.newScope(ev => { if (ev.type === 'progress') first(); });
		const p = rag.withScope(sc, () => rag.run(plan, { q: "rebalance law" }, { trigger: "submit" }));
		await seen;
		sc.abort("test");
		const r = await p;
		return JSON.stringify({ trace: r.trace.map(t => t.node + ':' + t.status + (t.error ? ':' + t.error : '')) });
	})()`)
	trace := strings.Join(toStrings(out["trace"]), " ")
	if !strings.Contains(trace, "rerank:error:the operation ended: cancelled") {
		t.Fatalf("trace after abort: %s", trace)
	}
	time.Sleep(200 * time.Millisecond)
	if emb.cancelled.Load() == 0 {
		t.Fatal("no rerank request saw its context cancelled")
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

type failingRanker struct{ bowEmbedder }

func (*failingRanker) Rerank(context.Context, string, string, []string, int) ([]llm.RerankResult, error) {
	return nil, errors.New("deliberate provider failure")
}
func TestRerankFailureSurvivesWorkerCleanup(t *testing.T) {
	out := runVectorScript(t, &failingRanker{}, nil, `(async()=>{
 const rag=require("ragwb");
 const c=rag.corpus("failure").from(rag.source.dir({root:ROOT,include:["*.md"]})).parse().chunk(rag.sections()).index("text",rag.bm25());
 const p=rag.plan("failure").over(c).signals({q:"rebalance law"}).retrieve("text").rerank.model();
 const result=await rag.run(p);
 return JSON.stringify({errors:result.errors});
 })()`)
	failures := strings.Join(toStrings(out["errors"]), " ")
	if !strings.Contains(failures, "deliberate provider failure") || strings.Contains(failures, "cancelled") {
		t.Fatalf("terminal outcome: %s", failures)
	}
}
