package ragjs

import (
	"context"
	"testing"
	"time"

	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/geppetto/pkg/decision"

	"github.com/go-go-golems/rag-workbench/pkg/decide"
)

func newDecideRuntime(t *testing.T, p decision.Provider) *engine.Runtime {
	t.Helper()
	factory, err := engine.NewRuntimeFactoryBuilder().WithModules(Modules(Options{Decide: p})...).Build()
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

// rag.decide reaches the provider with options in JS order and returns camelCase answers.
func TestRagDecideBridge(t *testing.T) {
	fake := &decide.Fake{}
	rt := newDecideRuntime(t, fake)
	var got struct {
		Route  map[string]any `json:"route"`
		Needs  float64        `json:"needs"`
		Hard   map[string]any `json:"hard"`
		Model  string         `json:"model"`
		Noul   float64        `json:"noul"`
		Choice string         `json:"choice"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		const r = await rag.decide({ state: { query: "bleve index rebalance" }, questions: {
			route: { type: "choice", instructions: "Which index?", options: { zebra: "animals", keyword: "bleve index identifiers", semantic: "concepts" } },
			needs: { type: "noul", instructions: "Does the corpus mention rebalance?", true: "rebalance bleve" },
			hard:  { type: "score", instructions: "How hard?", levels: ["easy", "medium", "hard"] } } });
		const noul = await rag.decide.noul({ query: "rebalance" }, "passage about rebalance");
		const c = await rag.decide.choice({ query: "vectors" }, "pick", ["keyword", "vectors"]);
		return JSON.stringify({ route: r.answers.route, needs: r.answers.needs.probability, hard: r.answers.hard, model: r.model, noul, choice: c.choice });
	})()`, &got)
	if got.Route["choice"] != "keyword" || got.Model != "fake" || got.Needs < 0.6 || got.Choice != "vectors" || got.Noul < 0.9 {
		t.Fatalf("answers %+v", got)
	}
	if lp, ok := got.Hard["levelProbabilities"].([]any); !ok || len(lp) != 3 {
		t.Fatalf("score answer %+v", got.Hard)
	}
	reqs := fake.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests %d", len(reqs))
	}
	opts := reqs[0].Questions["route"].Options
	if len(opts) != 3 || opts[0].Name != "zebra" || opts[1].Name != "keyword" || opts[2].Description != "concepts" {
		t.Fatalf("options lost order: %+v", opts)
	}
}

// Without a provider ctx.decide rejects with decide_unavailable; provider
// errors map to codes; an aborted signal cancels the call.
func TestRagDecideErrors(t *testing.T) {
	rt := newDecideRuntime(t, nil)
	var got struct{ Code string }
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		try { await rag.decide({ state: "s", questions: { q: { type: "noul", instructions: "?" } } }); return JSON.stringify({ code: "resolved" }); }
		catch (e) { return JSON.stringify({ code: e.code }); }
	})()`, &got)
	if got.Code != "decide_unavailable" {
		t.Fatalf("no provider: %+v", got)
	}

	rt = newDecideRuntime(t, &decide.Fake{Delay: 5 * time.Second})
	var got2 struct {
		Invalid, Bad, Cancelled string
	}
	start := time.Now()
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		const code = p => p.then(() => "resolved", e => e.code);
		const invalid = await code(rag.decide({ state: "s", questions: { "bad id": { type: "noul", instructions: "?" } } }));
		let bad; try { await rag.decide({ state: "s", questions: { q: { type: "multi", instructions: "?" } } }); bad = "resolved"; } catch (e) { bad = e.name || "error"; }
		// goja has no AbortController; the bridge needs only addEventListener('abort') and aborted
		const sig = { aborted: false, fns: [], addEventListener(type, fn) { this.fns.push(fn); } };
		const p = code(rag.engine().decider({ calls: 0 }, sig)({ state: "s", questions: { q: { type: "noul", instructions: "?" } } }));
		sig.aborted = true; sig.fns.forEach(f => f());
		return JSON.stringify({ invalid, bad, cancelled: await p });
	})()`, &got2)
	if got2.Invalid != "invalid_request" || got2.Bad == "resolved" || got2.Cancelled != "cancelled" {
		t.Fatalf("errors %+v", got2)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("abort did not cancel the call: %s", time.Since(start))
	}
}
