package ragjs

import (
	"context"
	"strings"
	"testing"

	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

func newSampleRuntime(t *testing.T, backend llm.Backend) *engine.Runtime {
	t.Helper()
	factory, err := engine.NewRuntimeFactoryBuilder().WithModules(Modules(Options{Sample: backend})...).Build()
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

// The sample contract through rag's own engine.sampler: env.sample is what ctx.sample wraps.
func TestSampleContractWithFake(t *testing.T) {
	fake := &llm.Fake{
		FakeToolInput: []byte(`{"query":"rebalance law"}`),
		Answer: func(req llm.Request) string {
			if req.JSON {
				return "Here you go:\n```json\n{\"tags\": [\"a\", \"b\"]}\n```"
			}
			return "answer for " + req.Tier + " with " + req.Turns[len(req.Turns)-1].Content
		},
	}
	rt := newSampleRuntime(t, fake)
	var got struct {
		Plain    map[string]any   `json:"plain"`
		Turns    string           `json:"turns"`
		JSON     map[string]any   `json:"json"`
		ToolSeen []map[string]any `json:"toolSeen"`
		ThrowOK  string           `json:"throwOK"`
		Err      map[string]any   `json:"err"`
		Streamed []string         `json:"streamed"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		const s = rag.engine().sampler({ calls: 0 });
		const streamed = [];
		const plain = await s("hello", { modelTier: "quick", onText: ({ text }) => streamed.push(text) });
		const turns = (await s([{ role: "user", content: "a" }, { role: "assistant", content: "b" }, { role: "user", content: "c" }])).text;
		const json = await s.json("give tags as JSON");
		const toolSeen = [];
		await s("use the tool", { tools: [{ name: "search", description: "d", inputSchema: { type: "object" },
			execute: (input) => { toolSeen.push(input); return [{ id: "x" }]; } }] });
		const throwOK = (await s("use the tool", { tools: [{ name: "boom", execute: () => { throw new Error("nope"); } }] })).text;
		let err = null;
		try { await s(42); } catch (e) { err = e; }
		return JSON.stringify({ plain, turns, json, toolSeen, throwOK, err, streamed });
	})()`, &got)

	if got.Plain["text"] != "answer for quick with hello" || got.Plain["modelTierApplied"] != "quick" || got.Plain["truncated"] != false {
		t.Fatalf("plain = %v", got.Plain)
	}
	if got.Turns != "answer for default with c" {
		t.Fatalf("turns = %q", got.Turns)
	}
	if tags, _ := got.JSON["tags"].([]any); len(tags) != 2 {
		t.Fatalf("json = %v (fenced JSON must be extracted)", got.JSON)
	}
	if len(got.ToolSeen) != 1 || got.ToolSeen[0]["query"] != "rebalance law" {
		t.Fatalf("tool input = %v", got.ToolSeen)
	}
	if !strings.HasPrefix(got.ThrowOK, "answer for default") {
		t.Fatalf("a throwing tool must become a tool error result, got %q", got.ThrowOK)
	}
	if got.Err["code"] != "invalid_request" {
		t.Fatalf("err = %v, want plain {code: invalid_request}", got.Err)
	}
	if len(got.Streamed) != 1 || got.Streamed[0] != got.Plain["text"] {
		t.Fatalf("onText = %v", got.Streamed)
	}
}
