package ragjs

import (
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// trace({details: true}) builds its live view by folding engine events
// (RAGWB-005 §1.2); folding a real scoped run's events gives each step's
// status, hits, evidence and text.
func TestTraceFoldsRunEvents(t *testing.T) {
	rt := newSampleRuntime(t, &llm.Fake{Pieces: 3})
	var got map[string]map[string]any
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb"), gui = require("ragwb/gui");
		`+eventsCorpus+`
		await rag.run(plan, { q: 'x' });
		const live = { nodes: {} };
		const sc = rag.newScope(ev => gui.foldEvent(live, ev));
		const r = await rag.withScope(sc, () => rag.run(plan, { q: 'rebalance law' }, { trigger: 'submit' }));
		live.nodes.answer.final = r.answer.text;
		return JSON.stringify(live.nodes);
	})()`, &got)
	text, pack, answer := got["text"], got["pack"], got["answer"]
	if text["status"] != "ran" || len(text["hits"].([]any)) == 0 || text["out"].(float64) != text["total"].(float64) {
		t.Fatalf("retrieve step: %v", text)
	}
	if pack["status"] != "ran" || len(pack["evidence"].([]any)) == 0 {
		t.Fatalf("pack step: %v", pack)
	}
	if answer["status"] != "ran" || answer["text"] != answer["final"] {
		t.Fatalf("answer step: text %q, final %q", answer["text"], answer["final"])
	}
}

// A step served from the memo reports its output like one that ran: a submit
// after a change run of the same query shows the cached retrieve's hits and
// the cached pack's evidence while the answer is still running.
func TestCachedStepsReportTheirOutput(t *testing.T) {
	rt := newSampleRuntime(t, &llm.Fake{Pieces: 3})
	var got struct {
		Nodes map[string]map[string]any `json:"nodes"`
		Top   bool                      `json:"top"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb"), gui = require("ragwb/gui");
		`+eventsCorpus+`
		const session = rag.engine().session(rag.compile(plan)); // the memo lives in a session, as in the gui
		await session.run({ q: 'rebalance law' }, { trigger: 'change' }); // retrieve and pack run, answer is skipped
		const live = { nodes: {} };
		const r = await session.run({ q: 'rebalance law' }, { trigger: 'submit', onEvent: ev => gui.foldEvent(live, ev) });
		const tt = r.trace.find(e => e.node === 'text');
		return JSON.stringify({ nodes: live.nodes, top: !!(tt.top && tt.top.length) });
	})()`, &got)
	text, pack, answer := got.Nodes["text"], got.Nodes["pack"], got.Nodes["answer"]
	if text["status"] != "cached" || len(text["hits"].([]any)) == 0 {
		t.Fatalf("cached retrieve step: %v", text)
	}
	if pack["status"] != "cached" || len(pack["evidence"].([]any)) == 0 {
		t.Fatalf("cached pack step: %v", pack)
	}
	if answer["status"] != "ran" {
		t.Fatalf("answer step: %v", answer)
	}
	if !got.Top {
		t.Fatal("the cached step's trace entry has no top hits")
	}
}
