package ragjs

import (
	"context"
	"strings"
	"testing"

	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/decide"
	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

func newOptsRuntime(t *testing.T, opts Options) *engine.Runtime {
	t.Helper()
	factory, err := engine.NewRuntimeFactoryBuilder().WithModules(Modules(opts)...).Build()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

const routeCorpus = eventsCorpus + `
  const route = { id: 'route', questions: { strategy: { type: 'choice', instructions: 'Which topic?',
    options: { widgets: 'widgets gadgets', law: 'rebalance law placements' } } } };
  // classify is added last: the when edges must still order it first
  const routed = rag.plan('r').over(corpus).signals({ q: '', k: 3 })
    .retrieve('text', { k: 10, id: 'lawix', when: { decision: 'route.strategy', in: ['law'] } })
    .retrieve('text', { k: 10, id: 'widgetix', when: { decision: 'route.strategy', in: ['widgets'] } })
    .fuse.rrf()
    .classify(route);
  const view = r => ({ order: r.trace.map(t => t.node), status: Object.fromEntries(r.trace.map(t => [t.node, t.status + (t.skippedBy ? ':' + t.skippedBy : '')])),
    notes: Object.fromEntries(r.trace.filter(t => t.notes).map(t => [t.node, t.notes])), hits: r.hits.length, errors: r.errors });
`

// RAGWB-007 §3.3: classify picks a branch; the other retrieve is skipped by
// when, with a note; a change run (classify not run) runs both (fail open).
func TestClassifyAndWhenRoute(t *testing.T) {
	fake := &decide.Fake{}
	rt := newOptsRuntime(t, Options{Decide: fake})
	var got struct {
		Submit, Change struct {
			Order  []string            `json:"order"`
			Status map[string]string   `json:"status"`
			Notes  map[string][]string `json:"notes"`
			Hits   int                 `json:"hits"`
			Errors []string            `json:"errors"`
		}
		Events []map[string]any `json:"events"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+routeCorpus+`
		const evs = [];
		const sc = rag.newScope(ev => { if (ev.type === 'decision' || (ev.type === 'node' && ev.skippedBy)) evs.push(ev); });
		const submit = await rag.withScope(sc, () => rag.run(routed, { q: 'rebalance law' }, { trigger: 'submit' }));
		const change = await rag.run(routed, { q: 'rebalance law' }, { trigger: 'change' });
		return JSON.stringify({ submit: view(submit), change: view(change), events: evs });
	})()`, &got)
	s := got.Submit
	if len(s.Errors) > 0 || s.Status["route"] != "ran" || s.Status["lawix"] != "ran" || s.Status["widgetix"] != "skipped:when" {
		t.Fatalf("submit %+v", s)
	}
	if s.Order[0] != "route" {
		t.Fatalf("classify must run first: %v", s.Order)
	}
	if n := strings.Join(s.Notes["widgetix"], ";"); !strings.Contains(n, "route.strategy = law") {
		t.Fatalf("skip note %q", n)
	}
	if n := strings.Join(s.Notes["route"], ";"); !strings.Contains(n, "strategy → law") {
		t.Fatalf("classify note %q", n)
	}
	if s.Hits == 0 {
		t.Fatal("no hits through the law branch")
	}
	c := got.Change
	if c.Status["route"] != "skipped" || c.Status["lawix"] != "ran" || c.Status["widgetix"] != "ran" {
		t.Fatalf("change run must fail open: %+v", c)
	}
	if len(got.Events) != 2 || got.Events[0]["type"] != "decision" || got.Events[1]["skippedBy"] != "when" {
		t.Fatalf("events %v", got.Events)
	}
	if fake.Calls() != 1 {
		t.Fatalf("decision calls %d", fake.Calls())
	}
}

// Compile-time checks: a when must name an existing decision node; when is part of the digest.
func TestWhenCompile(t *testing.T) {
	rt := newOptsRuntime(t, Options{Decide: &decide.Fake{}})
	var got struct {
		Missing, NotDecision, BadKey string
		DigestMoves                  bool
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+routeCorpus+`
		const err = f => { try { f(); return "compiled"; } catch (e) { return e.message; } };
		const base = rag.plan('w').over(corpus).signals({ q: '' });
		return JSON.stringify({
			missing: err(() => rag.compile(base.retrieve('text', { when: { decision: 'nope.x', in: ['a'] } }))),
			notDecision: err(() => rag.compile(base.retrieve('text', { id: 'a' }).retrieve('text', { id: 'b', when: { decision: 'a.x', min: 0.5 } }))),
			badKey: err(() => base.retrieve('text', { when: { decision: 'route.x', above: 0.5 } })),
			digestMoves: rag.compile(base.classify(route).retrieve('text', { when: { decision: 'route.strategy', in: ['law'] } })).digest
				!== rag.compile(base.classify(route).retrieve('text', { when: { decision: 'route.strategy', in: ['widgets'] } })).digest,
		});
	})()`, &got)
	if !strings.Contains(got.Missing, "which does not exist") || !strings.Contains(got.NotDecision, "not a decision node") || !strings.Contains(got.BadKey, "unknown key above") || !got.DigestMoves {
		t.Fatalf("%+v", got)
	}
}

// Adaptive-RAG's no-retrieval class: retrieval skipped by a noul, answer({direct}) answers without evidence.
func TestAnswerDirectWhenRetrievalSkipped(t *testing.T) {
	rt := newOptsRuntime(t, Options{Decide: &decide.Fake{}, Sample: &llm.Fake{}})
	var got struct {
		Status map[string]string `json:"status"`
		Direct bool              `json:"direct"`
		Text   string            `json:"text"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		`+routeCorpus+`
		const dplan = rag.plan('d').over(corpus).signals({ q: '' })
			.classify({ id: 'gate', questions: { needs: { type: 'noul', instructions: 'Does answering need the rebalance law documents?' } } })
			.retrieve('text', { k: 5, when: { decision: 'gate.needs', min: 0.5 } })
			.pack().answer({ direct: true });
		const r = await rag.run(dplan, { q: 'hello there friend' }, { trigger: 'submit' });
		return JSON.stringify({ status: Object.fromEntries(r.trace.map(t => [t.node, t.status])), direct: !!r.answer.direct, text: r.answer.text });
	})()`, &got)
	if got.Status["text"] != "skipped" || !got.Direct || got.Text == "" {
		t.Fatalf("%+v", got)
	}
}

// The GUI fold keeps decision events and the when-skip reason (RAGWB-007 §3.7).
func TestTraceFoldsDecisions(t *testing.T) {
	rt := newOptsRuntime(t, Options{Decide: &decide.Fake{}})
	var got map[string]map[string]any
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb"), gui = require("ragwb/gui");
		`+routeCorpus+`
		const live = { nodes: {} };
		const sc = rag.newScope(ev => gui.foldEvent(live, ev));
		await rag.withScope(sc, () => rag.run(routed, { q: 'rebalance law' }, { trigger: 'submit' }));
		return JSON.stringify(live.nodes);
	})()`, &got)
	route, skipped := got["route"], got["widgetix"]
	ds, _ := route["decisions"].([]any)
	if len(ds) != 1 || ds[0].(map[string]any)["answers"].(map[string]any)["strategy"].(map[string]any)["value"] != "law" {
		t.Fatalf("route: %v", route)
	}
	if skipped["skippedBy"] != "when" || skipped["status"] != "skipped" {
		t.Fatalf("skipped: %v", skipped)
	}
}

func TestWhenSurvivesInsertedPlanOperation(t *testing.T) {
	rt := newOptsRuntime(t, Options{Decide: &decide.Fake{}})
	var got struct {
		Status string `json:"status"`
		When   bool   `json:"when"`
	}
	evalJSON(t, rt, nil, `(async () => {
  const rag = require("ragwb");
  `+routeCorpus+`
  const ir = rag.compile(routed);
  const patched = rag.applyOps(ir, [{op:'insert', id:'extra', node:{t:'retrieve', p:{index:'text',k:5}, when:{decision:'route.strategy',in:['widgets']}}, in:['q']}]);
  const r = await rag.run(patched, {q:'rebalance law'}, {trigger:'submit'});
  return JSON.stringify({when:!!patched.nodes.find(n=>n.id==='extra').when,status:r.trace.find(n=>n.node==='extra').skippedBy});
 })()`, &got)
	if !got.When || got.Status != "when" {
		t.Fatalf("inserted when not enforced: %+v", got)
	}
}
