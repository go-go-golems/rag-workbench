package ragjs

import (
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/decide"
)

const enrichDecideCorpus = `
  const files = [
    { name: 'a.md', text: '# Alpha\n\n## Rebalance law\n\nThe rebalance law preserves placements of every product.\n\n## Widgets\n\nUnrelated text about widgets and gadgets.' },
    { name: 'b.md', text: '# Beta\n\n## Law notes\n\nAnother rebalance law note.\n\n## More widgets\n\nWidgets again, many widgets.' },
  ];
  const corpus = rag.corpus('ed').from(rag.source.inline(files)).parse(rag.markdown()).chunk(rag.sections())
    .enrich('kind', rag.enrich.decide({ questions: { topic: { type: 'choice', instructions: 'What is this section about?', options: { law: 'rebalance law placements', widgets: 'widgets gadgets' } } } }))
    .index('text', rag.bm25())
    .index('kinds', rag.terms({ keys: ['kind.topic'] }));
  const plan = rag.plan('ed').over(corpus).signals({ q: '', filters: {} }).retrieve('text', { k: 10 }).filter.where({ key: 'filters' });
`

// RAGWB-007 §3.5: with build calls, every chunk gets kind.topic (and kind.topic_p)
// in chunk.meta, usable by filter.where; without, the node is deferred.
func TestEnrichDecide(t *testing.T) {
	fake := &decide.Fake{}
	rt := newOptsRuntime(t, Options{Decide: fake, BuildCalls: true})
	var got struct {
		Topics  map[string]string `json:"topics"`
		HasP    bool              `json:"hasP"`
		Widgets int               `json:"widgets"`
		All     int               `json:"all"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+enrichDecideCorpus+`
		const all = await rag.run(plan, { q: 'widgets law rebalance' });
		const w = await rag.run(plan, { q: 'widgets law rebalance', filters: { 'kind.topic': 'widgets' } });
		const topics = {};
		let hasP = true;
		for (const h of all.hits) { const c = all.chunk(h.id); topics[c.headingPath.join('/')] = c.meta.kind.topic; hasP = hasP && typeof c.meta.kind.topic_p === 'number'; }
		return JSON.stringify({ topics, hasP, widgets: w.hits.length, all: all.hits.length });
	})()`, &got)
	if got.Topics["Rebalance law"] != "law" || got.Topics["More widgets"] != "widgets" || !got.HasP {
		t.Fatalf("topics %+v", got)
	}
	if got.Widgets == 0 || got.Widgets >= got.All {
		t.Fatalf("filter.where on kind.topic: %d of %d", got.Widgets, got.All)
	}
	if fake.Calls() == 0 {
		t.Fatal("no decision calls")
	}

	deferred := &decide.Fake{}
	rt = newOptsRuntime(t, Options{Decide: deferred})
	var got2 struct {
		Pending []string `json:"pending"`
		HasKind bool     `json:"hasKind"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+enrichDecideCorpus+`
		const r = await rag.run(plan, { q: 'widgets' });
		const c = r.chunk(r.hits[0].id);
		return JSON.stringify({ pending: rag.engine().pending(rag.compile(plan)).map(x => x.node), hasKind: !!(c.meta && c.meta.kind) });
	})()`, &got2)
	if got2.HasKind || len(got2.Pending) != 1 || deferred.Calls() != 0 {
		t.Fatalf("without build calls: %+v calls %d", got2, deferred.Calls())
	}
}
