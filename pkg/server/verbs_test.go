package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-go-golems/go-go-goja/pkg/jsverbs"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
	"github.com/go-go-golems/rag-workbench/pkg/runner"
	"github.com/go-go-golems/rag-workbench/pkg/store"
	"github.com/go-go-golems/rag-workbench/pkg/timing"
)

// serveVerbs opens dir as a served app with its verbs, behind an httptest server.
func serveVerbs(t *testing.T, dir string, backend llm.Backend, roots ...string) (*httptest.Server, *RagServer) {
	t.Helper()
	reg, err := jsverbs.ScanDir(dir, store.VerbScanOptions())
	if err != nil {
		t.Fatal(err)
	}
	rec := timing.New(time.Now(), "open")
	factory, err := runner.NewFactory(runner.Config{Roots: append(roots, dir), Base: dir, Timing: rec, SourceLoader: reg.RequireLoader(), LLM: backend, SourceDigestTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	name := filepath.Base(dir)
	app, err := runner.OpenApp(ctx, factory, runner.AppSpec{Name: name, Path: dir, Verbs: reg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	rs := NewRagServer(&RagApp{ServedApp: app, Timing: rec})
	ts := httptest.NewServer(rs.Handler())
	t.Cleanup(ts.Close)
	return ts, rs
}

type verbEvent struct {
	Event string
	Data  map[string]any
}

func readVerbSSE(t *testing.T, r io.Reader) []verbEvent {
	t.Helper()
	var out []verbEvent
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	var ev verbEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.Event != "" {
				out = append(out, ev)
			}
			ev = verbEvent{}
		case strings.HasPrefix(line, "event: "):
			ev.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.Data); err != nil {
				t.Fatalf("bad data line %q: %v", line, err)
			}
		}
	}
	return out
}

func postSSE(t *testing.T, url, body string) (*http.Response, []verbEvent) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Accept", "text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	return res, readVerbSSE(t, res.Body)
}

func names(evs []verbEvent) string {
	var s []string
	for _, e := range evs {
		n := e.Event
		if node, ok := e.Data["node"]; ok {
			n += "@" + fmt.Sprint(node)
		}
		s = append(s, n)
	}
	return strings.Join(s, " ")
}

// The reports example's verbs (search.js) as endpoints: discovery, JSON, SSE,
// GET with query strings, chunks=1, events=, and parameter errors.
func TestVerbEndpoints(t *testing.T) {
	dir, _ := filepath.Abs("../../examples/apps/reports")
	ts, _ := serveVerbs(t, dir, nil)
	base := ts.URL + "/api/rag/reports/verbs"

	var list []map[string]any
	if code := getJSON(t, base, &list); code != 200 || len(list) < 2 {
		t.Fatalf("verbs = %d %v", code, list)
	}
	var search map[string]any
	for _, v := range list {
		if v["path"] == "reports search" {
			search = v
		}
	}
	if search == nil || search["url"] != "/api/rag/reports/verbs/reports/search" || len(search["fields"].([]any)) != 4 {
		t.Fatalf("search verb = %v", search)
	}

	// JSON
	res, err := http.Post(base+"/reports/search", "application/json", strings.NewReader(`{"q":"rebalance law","k":3,"plan":"fused"}`))
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	_ = json.NewDecoder(res.Body).Decode(&env)
	res.Body.Close()
	rows, _ := env["result"].([]any)
	runs, _ := env["runs"].([]any)
	if res.StatusCode != 200 || len(rows) != 3 || len(runs) != 1 || env["params"].(map[string]any)["plan"] != "fused" {
		t.Fatalf("JSON call: %d %v", res.StatusCode, env)
	}

	// SSE
	res, evs := postSSE(t, base+"/reports/search?top=4", `{"q":"rebalance law","k":3,"plan":"fused"}`)
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	seq := names(evs)
	if !strings.HasPrefix(seq, "start run ") || evs[len(evs)-1].Event != "result" || !strings.Contains(seq, "hits@fuse") || !strings.Contains(seq, "runEnd") {
		t.Fatalf("SSE sequence: %s", seq)
	}
	for i, e := range evs {
		if int(e.Data["seq"].(float64)) != i+1 {
			t.Fatalf("seq %v at %d", e.Data["seq"], i)
		}
		if e.Event == "hits" {
			hs := e.Data["hits"].([]any)
			if len(hs) > 4 || (len(hs) > 0 && hs[0].(map[string]any)["chunk"] != nil) {
				t.Fatalf("hits event not capped/compact: %v", e.Data)
			}
		}
	}
	if r := evs[len(evs)-1].Data["result"].([]any); len(r) != 3 {
		t.Fatalf("SSE result %v", evs[len(evs)-1].Data)
	}

	// GET, query-string params, chunks=1, events filter
	req, _ := http.NewRequest(http.MethodGet, base+"/reports/search?q=protojson&k=2&chunks=1&events=hits", nil)
	req.Header.Set("Accept", "text/event-stream")
	gres, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	evs = readVerbSSE(t, gres.Body)
	gres.Body.Close()
	for _, e := range evs[1 : len(evs)-1] {
		if e.Event != "hits" {
			t.Fatalf("events=hits let through %s", e.Event)
		}
		if hs := e.Data["hits"].([]any); len(hs) > 0 && hs[0].(map[string]any)["chunk"] == nil {
			t.Fatal("chunks=1 hits have no chunk")
		}
	}
	last := evs[len(evs)-1]
	if last.Event != "result" || len(last.Data["result"].([]any)) != 2 || len(last.Data["chunks"].(map[string]any)) == 0 {
		t.Fatalf("GET result: %v", last.Data)
	}

	// errors
	res, _ = http.Post(base+"/reports/search", "application/json", strings.NewReader(`{"q":"x","k":"lots"}`))
	var e map[string]any
	_ = json.NewDecoder(res.Body).Decode(&e)
	res.Body.Close()
	if res.StatusCode != 400 || e["error"].(map[string]any)["code"] != "bad_params" || e["error"].(map[string]any)["fields"].(map[string]any)["k"] == nil {
		t.Fatalf("bad params: %d %v", res.StatusCode, e)
	}
	if code := getJSON(t, base+"/reports/nope", nil); code != 404 {
		t.Fatalf("unknown verb: %d", code)
	}
}

// An LLM verb over SSE: text deltas (merged by the writer) add up to the answer.
func TestVerbSSEStreamsLLMText(t *testing.T) {
	corpus, _ := filepath.Abs("../../examples/apps/reports/corpus")
	dir := filepath.Join(t.TempDir(), "qa")
	_ = os.MkdirAll(dir, 0o755)
	app := fmt.Sprintf(`const rag = require("ragwb"), kit = require("ragwb/kit");
const corpus = rag.corpus("q").from(rag.source.dir({ root: %q, include: ["*.md"] })).parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
const plan = rag.plan("q").over(corpus).signals({ q: "", k: 5 }).retrieve("text", { k: 10 }).pack({ budget: 4000 }).answer();
async function answer(q) {
  const r = await rag.run(plan, { q }, { trigger: "submit" });
  return { text: r.answer.text };
}
__verb__("answer", { short: "Answer a question", fields: { q: { argument: true } } });
module.exports = { app: kit.searchApp(plan, { id: "q", title: "QA" }) };
`, corpus)
	_ = os.WriteFile(filepath.Join(dir, "app.js"), []byte(app), 0o644)
	ts, _ := serveVerbs(t, dir, &llm.Fake{Pieces: 12, Delay: 3 * time.Millisecond}, corpus)
	// app.js's verbs are "app <name>" (jsverbs names a file's verbs after the file);
	// the bare name works when it is unique
	res, evs := postSSE(t, ts.URL+"/api/rag/qa/verbs/answer", `{"q":"what is the rebalance law"}`)
	if len(evs) == 0 {
		var list []map[string]any
		getJSON(t, ts.URL+"/api/rag/qa/verbs", &list)
		t.Fatalf("no events: HTTP %d %s; verbs %v", res.StatusCode, res.Header.Get("Content-Type"), list)
	}
	var text strings.Builder
	n := 0
	for _, e := range evs {
		if e.Event == "text" {
			text.WriteString(e.Data["delta"].(string))
			n++
		}
	}
	last := evs[len(evs)-1]
	if last.Event != "result" {
		t.Fatalf("last event %s: %s", last.Event, names(evs))
	}
	want := last.Data["result"].(map[string]any)["text"].(string)
	if text.String() != want || n == 0 {
		t.Fatalf("streamed %q (%d events), answer %q", text.String(), n, want)
	}
	t.Logf("12 deltas arrived as %d text events", n)
}
