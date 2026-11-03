package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

func newTestServer(t *testing.T, backend llm.Backend) *httptest.Server {
	t.Helper()
	docs, err := OpenDocStore(filepath.Join(t.TempDir(), "docs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = docs.Close() })
	s := New(Config{LLM: backend, Docs: docs, PresetsDir: "../../examples/presets", CorpusDir: "../../examples/apps/reports/corpus"})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, url string, body any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	r, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(r.Body).Decode(&out)
	return out
}

type sseEvent struct {
	Type string
	Data map[string]any
}

// playSample acts as the page: stream events, answer tool events via the tools endpoint.
func playSample(t *testing.T, base string, body map[string]any, tool func(name string, input any) any) []sseEvent {
	t.Helper()
	b, _ := json.Marshal(body)
	r, err := http.Post(base+"/_rt/sample", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		raw, _ := io.ReadAll(r.Body)
		t.Fatalf("content-type %q body %s", ct, raw)
	}
	var events []sseEvent
	callID := ""
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	ev := sseEvent{}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			ev.Type = line[7:]
		case strings.HasPrefix(line, "data: "):
			_ = json.Unmarshal([]byte(line[6:]), &ev.Data)
		case line == "":
			events = append(events, ev)
			switch ev.Type {
			case "call":
				callID = ev.Data["id"].(string)
			case "tool":
				res := post(t, base+"/_rt/sample/"+callID+"/tools/"+ev.Data["use"].(string), map[string]any{"value": tool(ev.Data["name"].(string), ev.Data["input"])})
				if res["ok"] != true {
					t.Fatalf("tool result rejected: %v", res)
				}
			case "result", "error":
				return events
			}
			ev = sseEvent{}
		}
	}
	return events
}

func TestSampleProtocolWithPageTool(t *testing.T) {
	ts := newTestServer(t, &llm.Fake{FakeToolInput: []byte(`{"code":"return 1+1"}`)})
	var seen any
	events := playSample(t, ts.URL, map[string]any{
		"input": "run code", "modelTier": "quick",
		"tools": []map[string]any{{"name": "eval", "description": "run JS", "inputSchema": map[string]any{"type": "object"}}},
	}, func(name string, input any) any { seen = input; return map[string]any{"result": 2} })
	types := []string{}
	for _, e := range events {
		types = append(types, e.Type)
	}
	if strings.Join(types, ",") != "call,tool,text,result" {
		t.Fatalf("events = %v", types)
	}
	if in, _ := seen.(map[string]any); in["code"] != "return 1+1" {
		t.Fatalf("tool input = %v", seen)
	}
	last := events[len(events)-1].Data
	if last["modelTierApplied"] != "quick" || !strings.Contains(last["text"].(string), "tool eval") {
		t.Fatalf("result = %v", last)
	}
}

func TestSampleJSONAndErrors(t *testing.T) {
	ts := newTestServer(t, &llm.Fake{})
	ev := playSample(t, ts.URL, map[string]any{"input": "answer in JSON", "json": true}, nil)
	if last := ev[len(ev)-1]; last.Type != "result" || last.Data["json"].(map[string]any)["fake"] != true {
		t.Fatalf("json result = %+v", last)
	}
	ev = playSample(t, ts.URL, map[string]any{"input": []map[string]string{{"role": "assistant", "content": "x"}}}, nil)
	if last := ev[len(ev)-1]; last.Type != "error" || last.Data["code"] != "invalid_request" {
		t.Fatalf("expected invalid_request, got %+v", last)
	}
	noLLM := newTestServer(t, nil)
	r, _ := http.Post(noLLM.URL+"/_rt/sample", "application/json", strings.NewReader(`{"input":"x"}`))
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no-LLM status = %d", r.StatusCode)
	}
}

func TestDocStoreOps(t *testing.T) {
	ts := newTestServer(t, nil)
	base := ts.URL + "/_rt/db/"
	post(t, base+"set", map[string]any{"path": "data/users/local/ragwb", "data": map[string]any{"preset": "minimal"}})
	post(t, base+"set", map[string]any{"path": "data/users/local/ragwb/transcripts/c1", "data": map[string]any{"id": "c1"}})
	post(t, base+"set", map[string]any{"path": "data/users/local/ragwb/transcripts/c2", "data": map[string]any{"id": "c2"}})
	post(t, base+"set", map[string]any{"path": "data/users/local/ragwb/transcripts/c2/parts/p1", "data": map[string]any{"deep": true}})
	if got := post(t, base+"get", map[string]any{"path": "data/users/local/ragwb"}); got["data"].(map[string]any)["preset"] != "minimal" {
		t.Fatalf("get = %v", got)
	}
	list := post(t, base+"list", map[string]any{"path": "data/users/local/ragwb/transcripts"})["docs"].([]any)
	if len(list) != 2 {
		t.Fatalf("list must return direct children only: %v", list)
	}
	post(t, base+"delete", map[string]any{"path": "data/users/local/ragwb/transcripts/c1"})
	if got := post(t, base+"get", map[string]any{"path": "data/users/local/ragwb/transcripts/c1"}); got["data"] != nil {
		t.Fatalf("deleted doc still there: %v", got)
	}
}

func TestPageAssets(t *testing.T) {
	ts := newTestServer(t, nil)
	get := func(p string) string {
		r, err := http.Get(ts.URL + p)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", p, err, r)
		}
		b, _ := io.ReadAll(r.Body)
		return string(b)
	}
	if !strings.Contains(get("/"), `<head><script src="/_rt/claude.js"></script>`) {
		t.Fatal("index.html must load the runtime shim first")
	}
	if !strings.Contains(get("/rag.js"), "const LN = ") {
		t.Fatal("rag.js must be the patched engine (D6)")
	}
	var corpus struct{ Files []struct{ Name string } }
	_ = json.Unmarshal([]byte(get("/corpus.json")), &corpus)
	if len(corpus.Files) != 11 {
		t.Fatalf("corpus files = %d", len(corpus.Files))
	}
	if !strings.Contains(get("/presets/index.json"), `"minimal"`) || !strings.Contains(get("/presets/minimal.js"), "rag.corpus") {
		t.Fatal("presets not served")
	}
	if r, _ := http.Get(ts.URL + "/presets/..%2Fgo.mod"); r.StatusCode != 404 {
		t.Fatalf("path traversal status = %d", r.StatusCode)
	}
}
