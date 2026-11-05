package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-go-golems/go-go-goja/pkg/jsverbs"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
	"github.com/go-go-golems/rag-workbench/pkg/store"
)

// verbApp writes an app directory whose app.js declares verbs (RAGWB-004) and opens it.
func verbApp(t *testing.T, fake llm.Backend) *ServedApp {
	t.Helper()
	src, _ := filepath.Abs("../../examples/apps/reports/corpus")
	dir := t.TempDir()
	app := fmt.Sprintf(`const rag = require("ragwb"), kit = require("ragwb/kit");
const corpus = rag.corpus("v").from(rag.source.dir({ root: %q, include: ["*.md"] })).parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
const plan = rag.plan("v").over(corpus).signals({ q: "", k: 5 }).retrieve("text", { k: 10 }).limit();
const answerPlan = rag.plan("va").over(corpus).signals({ q: "", k: 5 }).retrieve("text", { k: 10 }).pack({ budget: 4000 }).answer();

async function search(q, k) {
  const r = await rag.run(plan, { q, k });
  return r.top(k).map(h => ({ id: h.id, score: h.score }));
}
__verb__("search", { short: "Search", fields: { q: { argument: true }, k: { type: "int", default: 3 } } });

async function answer(q) {
  const r = await rag.run(answerPlan, { q }, { trigger: "submit" });
  return { text: r.answer.text, cited: r.answer.cited };
}
__verb__("answer", { short: "Answer", fields: { q: { argument: true } } });

async function late(q) {
  const scope = rag.scope(); // captured before the first await (guide §6.3)
  await null;
  const r = await rag.run(plan, { q, k: 2 }, { scope });
  scope.emit({ type: "note", text: "custom" });
  return r.hits.length;
}
__verb__("late", { short: "Explicit scope", fields: { q: { argument: true } } });

module.exports = { app: kit.searchApp(plan, { id: "v", title: "Verbs" }) };
`, src)
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := jsverbs.ScanDir(dir, store.VerbScanOptions())
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewFactory(Config{Roots: []string{src, dir}, Base: dir, SourceLoader: reg.RequireLoader(), LLM: fake})
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenApp(context.Background(), factory, AppSpec{Name: "v", Path: dir, Verbs: reg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func verbByName(t *testing.T, a *ServedApp, name string) *jsverbs.VerbSpec {
	t.Helper()
	for _, v := range a.Verbs() {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no verb %s among %d", name, len(a.Verbs()))
	return nil
}

type recorder struct {
	mu  sync.Mutex
	evs []map[string]any
}

func (r *recorder) Emit(e Event) {
	var m map[string]any
	_ = json.Unmarshal(e.Data, &m)
	r.mu.Lock()
	r.evs = append(r.evs, m)
	r.mu.Unlock()
}

func (r *recorder) types() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.evs {
		s := fmt.Sprint(e["type"])
		if p, ok := e["phase"]; ok {
			s += ":" + fmt.Sprint(p)
		}
		if n, ok := e["node"]; ok {
			s += "@" + fmt.Sprint(n)
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func invoke(t *testing.T, a *ServedApp, name string, params map[string]any, sink EventSink, d Detail) (any, int) {
	t.Helper()
	v := verbByName(t, a, name)
	vals, err := ParseVerbParams(a.Registry(), v, params)
	if err != nil {
		t.Fatal(err)
	}
	res, runs, err := a.Invoke(context.Background(), v, vals, sink, d)
	if err != nil {
		t.Fatal(err)
	}
	return res, runs
}

// Verbs of a served app run in its runtime, with their parameters parsed like
// CLI flags, and stream the engine's events of the runs they start.
func TestServedVerbInvokeStreamsEvents(t *testing.T) {
	a := verbApp(t, &llm.Fake{Pieces: 5, Delay: time.Millisecond})
	rec := &recorder{}
	res, runs := invoke(t, a, "search", map[string]any{"q": "rebalance law", "k": float64(2)}, rec, Detail{Top: 3})
	rows, _ := res.([]any)
	if len(rows) != 2 || runs != 1 {
		t.Fatalf("search result %v (runs %d)", res, runs)
	}
	ty := rec.types()
	for _, want := range []string{"run", "node:start@text", "hits@text", "node:end@text", "runEnd"} {
		if !strings.Contains(ty, want) {
			t.Fatalf("missing %s in %s", want, ty)
		}
	}
	// the default k comes from the verb's field
	if res, _ := invoke(t, a, "search", map[string]any{"q": "protojson"}, nil, Detail{}); len(res.([]any)) != 3 {
		t.Fatalf("default k: %v", res)
	}
	// LLM text arrives as deltas that add up to the answer
	rec = &recorder{}
	res, _ = invoke(t, a, "answer", map[string]any{"q": "rebalance law"}, rec, Detail{})
	text := res.(map[string]any)["text"].(string)
	var joined strings.Builder
	for _, e := range rec.evs {
		if e["type"] == "text" {
			joined.WriteString(e["delta"].(string))
		}
	}
	if joined.String() != text || strings.Count(rec.types(), "text@answer") < 2 {
		t.Fatalf("deltas %q vs answer %q (%s)", joined.String(), text, rec.types())
	}
	// a verb that awaits before rag.run passes the scope explicitly, and may emit its own events
	rec = &recorder{}
	if res, _ := invoke(t, a, "late", map[string]any{"q": "law"}, rec, Detail{}); res.(int64) != 2 {
		t.Fatalf("late: %v", res)
	}
	if ty := rec.types(); !strings.Contains(ty, "run") || !strings.Contains(ty, "note") {
		t.Fatalf("late verb events: %s", ty)
	}
}

// Concurrent requests interleave in the one runtime and see only their own events.
func TestServedVerbsConcurrentScopes(t *testing.T) {
	a := verbApp(t, &llm.Fake{Pieces: 8, Delay: 2 * time.Millisecond})
	invoke(t, a, "answer", map[string]any{"q": "warm up"}, nil, Detail{}) // build once
	qs := []string{"rebalance law", "protojson schema", "kafka topics", "product flags"}
	recs := make([]*recorder, len(qs))
	var wg sync.WaitGroup
	for i, q := range qs {
		recs[i] = &recorder{}
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			invoke(t, a, "answer", map[string]any{"q": q}, recs[i], Detail{})
		}(i, q)
	}
	wg.Wait()
	for i, r := range recs {
		seen := map[string]bool{}
		for _, e := range r.evs {
			if e["type"] == "run" {
				seen[e["signals"].(map[string]any)["q"].(string)] = true
			}
		}
		if len(seen) != 1 || !seen[qs[i]] {
			t.Fatalf("request %d (%q) saw runs %v", i, qs[i], seen)
		}
		if !strings.Contains(r.types(), "text@answer") {
			t.Fatalf("request %d has no text events: %s", i, r.types())
		}
	}
}

func TestServedVerbParams(t *testing.T) {
	a := verbApp(t, &llm.Fake{})
	v := verbByName(t, a, "search")
	for _, bad := range []map[string]any{{"q": "x", "k": "many"}, {"q": "x", "nope": 1}, {"q": "x", "k": 2.5}} {
		_, err := ParseVerbParams(a.Registry(), v, bad)
		var pe *ParamError
		if !errors.As(err, &pe) {
			t.Fatalf("%v: want a ParamError, got %v", bad, err)
		}
	}
	// query-string form: strings parsed by the field type
	vals, err := ParseVerbParams(a.Registry(), v, map[string]any{"q": []string{"law"}, "k": []string{"4"}})
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := a.Invoke(context.Background(), v, vals, nil, Detail{})
	if err != nil || len(res.([]any)) != 4 {
		t.Fatalf("query-string params: %v %v", res, err)
	}
	fs, err := DescribeVerb(a.Registry(), v)
	if err != nil || len(fs) != 2 {
		t.Fatalf("describe: %+v %v", fs, err)
	}
}

// slowLLM streams until its context is cancelled and records that it was (RAGWB-004 §6.7).
type slowLLM struct {
	cancelled chan struct{}
}

func (s *slowLLM) Sample(ctx context.Context, req llm.Request, onText llm.OnText, exec llm.ExecuteTool) (llm.Result, error) {
	text := ""
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			close(s.cancelled)
			return llm.Result{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
		text += "word "
		if onText != nil {
			onText(text, "word ")
		}
		if i > 1000 {
			return llm.Result{Text: text}, nil
		}
	}
}

// Cancelling a request's context aborts its scope: the in-flight model call is
// cancelled (not left running on the runtime's lifetime) and Invoke returns.
func TestServedVerbCancelStopsTheModelCall(t *testing.T) {
	slow := &slowLLM{cancelled: make(chan struct{})}
	a := verbApp(t, slow)
	v := verbByName(t, a, "answer")
	vals, err := ParseVerbParams(a.Registry(), v, map[string]any{"q": "rebalance law"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rec := &recorder{}
	errc := make(chan error, 1)
	go func() {
		_, _, err := a.Invoke(ctx, v, vals, rec, Detail{})
		errc <- err
	}()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(rec.types(), "text@answer") {
		select {
		case <-deadline:
			t.Fatalf("no text streamed: %s", rec.types())
		case <-time.After(5 * time.Millisecond):
		}
	}
	t0 := time.Now()
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Invoke returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Invoke did not return after cancel")
	}
	select {
	case <-slow.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the model call was not cancelled")
	}
	t.Logf("model call cancelled %v after the request", time.Since(t0).Round(time.Millisecond))
	// the runtime is still usable
	if res, _ := invoke(t, a, "search", map[string]any{"q": "law", "k": float64(2)}, nil, Detail{}); len(res.([]any)) != 2 {
		t.Fatalf("after cancel: %v", res)
	}
}
