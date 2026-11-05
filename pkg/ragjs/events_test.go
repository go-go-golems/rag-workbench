package ragjs

import (
	"strings"
	"testing"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

const eventsCorpus = `
  const files = [
    { name: 'a.md', text: '# Alpha\n\n## Rebalance law\n\nThe rebalance law preserves placements of every product.\n\n## Widgets\n\nUnrelated text about widgets and gadgets.' },
    { name: 'b.md', text: '# Beta\n\n## Law notes\n\nAnother rebalance note about the law.\n\n## More widgets\n\nWidgets again, many widgets.' },
  ];
  const corpus = rag.corpus('e').from(rag.source.inline(files)).parse(rag.markdown()).chunk(rag.sections()).index('text', rag.bm25());
  const plan = rag.plan('e').over(corpus).signals({ q: '', k: 3 }).retrieve('text', { k: 10 }).pack().answer();
  const agent = rag.plan('ag').over(corpus).signals({ q: '' }).agentic({ indexes: ['text'] });
`

// RAGWB-004 §6.4: the event stream of one scoped run — node start/end pairs,
// hits capped by detail.top, context, text deltas that add up to the answer,
// run/runEnd — and tool events for an agentic node.
func TestRunEventsInScope(t *testing.T) {
	rt := newSampleRuntime(t, &llm.Fake{Pieces: 4, Delay: time.Millisecond, FakeToolInput: []byte(`{"query":"rebalance"}`)})
	var got struct {
		Types  []string         `json:"types"`
		Text   string           `json:"text"`
		Answer string           `json:"answer"`
		Hits   map[string]any   `json:"hits"`
		Runs   []float64        `json:"runs"`
		Tools  []map[string]any `json:"tools"`
		Late   int              `json:"late"`
		Scope  bool             `json:"scopeAfterAwait"`
		Unscop int              `json:"unscoped"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+eventsCorpus+`
		const evs = [];
		const sc = rag.newScope(ev => evs.push(ev), { top: 2 });
		const r = await rag.withScope(sc, () => rag.run(plan, { q: 'rebalance law' }, { trigger: 'submit' }));
		const tevs = [];
		const sc2 = rag.newScope(ev => tevs.push(ev));
		await rag.withScope(sc2, () => rag.run(agent, { q: 'rebalance' }, { trigger: 'submit' }));
		let scopeAfterAwait = true;
		await rag.withScope(sc, async () => { await null; scopeAfterAwait = rag.scope() !== null; });
		sc.close();
		const n = evs.length;
		sc.emit({ type: 'note', text: 'late' });
		const unscoped = [];
		await rag.run(plan, { q: 'widgets' }); // no scope: nothing reaches sc
		return JSON.stringify({
			types: evs.map(e => e.type + (e.phase ? ':' + e.phase : '') + (e.node ? '@' + e.node : '')),
			text: evs.filter(e => e.type === 'text').map(e => e.delta).join(''),
			answer: r.answer.text,
			hits: evs.find(e => e.type === 'hits'),
			runs: [...new Set(evs.map(e => e.run))],
			tools: tevs.filter(e => e.type === 'tool'),
			late: evs.length - n, scopeAfterAwait, unscoped: evs.length - n,
		});
	})()`, &got)
	joined := strings.Join(got.Types, " ")
	for _, want := range []string{"run", "build@ix-text", "built", "node:start@text", "hits@text", "node:end@text", "context@pack", "node:start@answer", "text@answer", "node:end@answer", "runEnd"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
	if starts, ends := strings.Count(joined, "node:start"), strings.Count(joined, "node:end"); starts != ends || starts < 3 {
		t.Fatalf("node start/end pairs: %d starts, %d ends: %s", starts, ends, joined)
	}
	if strings.Count(joined, "text@answer") < 2 {
		t.Fatalf("answer text was not streamed in deltas: %s", joined)
	}
	if got.Text != got.Answer || got.Answer == "" {
		t.Fatalf("deltas %q do not add up to the answer %q", got.Text, got.Answer)
	}
	if hs, _ := got.Hits["hits"].([]any); len(hs) != 2 || got.Hits["total"].(float64) < 2 {
		t.Fatalf("hits event not capped at detail.top=2: %v", got.Hits)
	} else if h := hs[0].(map[string]any); h["id"] == nil || h["score"] == nil || h["doc"] == nil || h["chunk"] != nil {
		t.Fatalf("hit shape: %v", h)
	}
	if len(got.Runs) != 1 || got.Runs[0] != 1 {
		t.Fatalf("run numbers: %v", got.Runs)
	}
	if len(got.Tools) != 2 || got.Tools[0]["phase"] != "start" || got.Tools[1]["phase"] != "end" || got.Tools[0]["name"] != "search" {
		t.Fatalf("tool events: %v", got.Tools)
	}
	if got.Late != 0 || got.Unscop != 0 {
		t.Fatalf("events after close / without a scope reached the sink: %d %d", got.Late, got.Unscop)
	}
	if got.Scope {
		t.Fatal("the implicit scope survived an await")
	}
}

// Two runs started in their own scopes and interleaving at awaits (a streaming
// fake LLM) only ever see their own events.
func TestInterleavedScopesStayIsolated(t *testing.T) {
	rt := newSampleRuntime(t, &llm.Fake{Pieces: 6, Delay: 2 * time.Millisecond})
	var got struct {
		A, B   []string
		Inter  bool `json:"interleaved"`
		Chunks bool `json:"chunks"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+eventsCorpus+`
		const all = [], a = [], b = [];
		const sa = rag.newScope(ev => { a.push(ev); all.push('a'); }, { chunks: true });
		const sb = rag.newScope(ev => { b.push(ev); all.push('b'); });
		await rag.run(plan, { q: 'x' }); // build first, so both runs are pure queries
		const pa = rag.withScope(sa, () => rag.run(plan, { q: 'rebalance law' }, { trigger: 'submit' }));
		const pb = rag.withScope(sb, () => rag.run(plan, { q: 'widgets' }, { trigger: 'submit' }));
		await Promise.all([pa, pb]);
		const qs = evs => [...new Set(evs.filter(e => e.type === 'run').map(e => e.signals.q))];
		const s = all.join('');
		return JSON.stringify({ a: qs(a), b: qs(b), interleaved: /ab+a|ba+b/.test(s),
			chunks: !!(a.find(e => e.type === 'hits').hits[0].chunk) && !b.find(e => e.type === 'hits').hits[0].chunk });
	})()`, &got)
	if len(got.A) != 1 || got.A[0] != "rebalance law" || len(got.B) != 1 || got.B[0] != "widgets" {
		t.Fatalf("scopes saw other runs: a %v b %v", got.A, got.B)
	}
	if !got.Inter {
		t.Fatal("the runs did not interleave; the test is not discriminating")
	}
	if !got.Chunks {
		t.Fatal("detail.chunks applies per scope")
	}
}
