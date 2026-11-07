package ragjs

import (
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/decide"
)

// 40 sections: 10 about the rebalance law, 30 about widgets.
const gradeCorpus = `
  const files = [];
  for (let i = 0; i < 40; i++) files.push({ name: 'd' + i + '.md', text: '# Doc ' + i + '\n\n## Part ' + i + '\n\n' +
    (i % 4 === 0 ? 'The rebalance law preserves product placements during reindexing number ' + i + '.' : 'Widgets and gadgets catalogue entry ' + i + ' with widgets.') });
  const corpus = rag.corpus('g').from(rag.source.inline(files)).parse(rag.markdown()).chunk(rag.sections()).index('text', rag.bm25());
  const base = rag.plan('g').over(corpus).signals({ q: '', k: 5 }).retrieve('text', { k: 40, perQuery: false });
  const all = s => s.retrieve(rag.live((s, ctx) => ctx.chunks().map((c, i) => ({ id: c.id, score: 1 / (i + 1) }))));
`

// RAGWB-007 §3.4: filter.decide grades in batches of ≤ batch and keeps p ≥ threshold;
// rerank.decide orders by p and is the plan's hits output.
func TestFilterAndRerankDecide(t *testing.T) {
	fake := &decide.Fake{}
	rt := newOptsRuntime(t, Options{Decide: fake})
	var got struct {
		Filtered   []map[string]any `json:"filtered"`
		Reranked   []map[string]any `json:"reranked"`
		FilterNote []string         `json:"filterNote"`
		HitsFrom   string           `json:"hitsFrom"`
		Calls      []int            `json:"calls"`
		MinKept    int              `json:"minKept"`
		Progress   int              `json:"progress"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+gradeCorpus+`
		const fplan = all(rag.plan('f').over(corpus).signals({ q: '', k: 5 })).filter.decide({ top: 40, batch: 16, threshold: 0.5 });
		const rplan = all(rag.plan('r').over(corpus).signals({ q: '', k: 5 })).rerank.decide({ top: 40, batch: 16, keep: 4 }).limit();
		const mplan = all(rag.plan('m').over(corpus).signals({ q: '', k: 5 })).filter.decide({ top: 40, threshold: 0.99, min: 2 });
		const evs = [];
		const sc = rag.newScope(ev => { if (ev.type === 'progress' && ev.unit === 'passages') evs.push(ev); });
		const f = await rag.withScope(sc, () => rag.run(fplan, { q: 'rebalance law placements' }, { trigger: 'submit' }));
		const calls = [];
		calls.push(f.calls);
		const r = await rag.run(rplan, { q: 'rebalance law placements' }, { trigger: 'submit' });
		calls.push(r.calls);
		const m = await rag.run(mplan, { q: 'rebalance law placements' }, { trigger: 'submit' });
		const fe = f.trace.find(t => t.t === 'filter.decide');
		return JSON.stringify({ filtered: f.hits.map(h => ({ id: h.id, p: h.p })), reranked: r.hits.map(h => ({ id: h.id, score: h.score, reason: h.reason })),
			filterNote: fe.notes, hitsFrom: rag.compile(rplan).outputs.hits, calls, minKept: m.hits.length, progress: evs.length });
	})()`, &got)
	if len(got.Filtered) != 10 {
		t.Fatalf("filter kept %d: %v (note %v)", len(got.Filtered), got.Filtered, got.FilterNote)
	}
	if got.Calls[0] != 3 || got.Calls[1] != 3 {
		t.Fatalf("40 hits in batches of 16 should be 3 decision calls: %v", got.Calls)
	}
	if got.Progress != 3 {
		t.Fatalf("progress events %d", got.Progress)
	}
	if len(got.Reranked) != 4 || got.Reranked[0]["reason"] == nil {
		t.Fatalf("rerank %v", got.Reranked)
	}
	for i := 1; i < len(got.Reranked); i++ {
		if got.Reranked[i]["score"].(float64) > got.Reranked[i-1]["score"].(float64) {
			t.Fatalf("rerank not sorted by p: %v", got.Reranked)
		}
	}
	if got.HitsFrom != "rerank" {
		t.Fatalf("plan hits output %q", got.HitsFrom)
	}
	if got.MinKept != 2 {
		t.Fatalf("min kept %d", got.MinKept)
	}
	for _, r := range fake.Requests() {
		if len(r.Questions) > 16 {
			t.Fatalf("batch of %d questions", len(r.Questions))
		}
	}
}
