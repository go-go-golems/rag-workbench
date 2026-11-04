package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/runner"
	"github.com/go-go-golems/rag-workbench/pkg/timing"
)

func ragTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir, _ := filepath.Abs("../../examples/apps/reports")
	rec := timing.New(time.Now(), "open")
	factory, err := runner.NewFactory(runner.Config{Roots: []string{dir}, Base: dir, Timing: rec, SourceDigestTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	app, err := runner.OpenApp(ctx, factory, runner.AppSpec{Name: "reports", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	ts := httptest.NewServer(NewRagServer(&RagApp{ServedApp: app, Timing: rec}).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if v != nil {
		_ = json.NewDecoder(res.Body).Decode(v)
	}
	return res.StatusCode
}

func TestRagServerRoutes(t *testing.T) {
	ts := ragTestServer(t)

	var list []map[string]string
	if getJSON(t, ts.URL+"/api/rag", &list); len(list) != 1 || list[0]["name"] != "reports" || list[0]["title"] != "Project reports" {
		t.Fatalf("list = %v", list)
	}
	var info map[string]any
	if code := getJSON(t, ts.URL+"/api/rag/reports", &info); code != 200 || info["id"] != "fused" {
		t.Fatalf("info %d %v", code, info["id"])
	}
	var e map[string]any
	if code := getJSON(t, ts.URL+"/api/rag/nope", &e); code != 404 {
		t.Fatalf("unknown app: %d", code)
	}

	// plain run
	res, err := http.Post(ts.URL+"/api/rag/reports/run", "application/json", strings.NewReader(`{"signals":{"q":"rebalance law","filters":{"type":["project-report"]}},"trigger":"change"}`))
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		Result struct {
			Hits []struct {
				ID string `json:"id"`
			} `json:"hits"`
		} `json:"result"`
		Chunks map[string]struct {
			Meta map[string]any `json:"meta"`
		} `json:"chunks"`
		Timing runTiming `json:"timing"`
	}
	_ = json.NewDecoder(res.Body).Decode(&run)
	res.Body.Close()
	if len(run.Result.Hits) == 0 || run.Timing.Bytes == 0 || run.Timing.Chunks != len(run.Chunks) {
		t.Fatalf("run: %d hits, timing %+v", len(run.Result.Hits), run.Timing)
	}
	for _, h := range run.Result.Hits {
		if c, ok := run.Chunks[h.ID]; !ok || c.Meta["type"] != "project-report" {
			t.Fatalf("hit %s: chunk %v (the filter must apply and the body must be sent)", h.ID, c)
		}
	}

	// streamed run: Server-Sent Events, the engine's events, then the result
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/rag/reports/run", strings.NewReader(`{"signals":{"q":"protojson"}}`))
	req.Header.Set("Accept", "text/event-stream")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	evs := readVerbSSE(t, res.Body)
	res.Body.Close()
	if res.Header.Get("Content-Type") != "text/event-stream" || len(evs) < 3 {
		t.Fatalf("stream: %s, %d events", res.Header.Get("Content-Type"), len(evs))
	}
	last := evs[len(evs)-1]
	if last.Event != "result" || last.Data["timing"] == nil || last.Data["chunks"] == nil || !strings.Contains(names(evs), "hits@") {
		t.Fatalf("stream: %s; last %v", names(evs), last.Data["type"])
	}

	// lookups
	var miss map[string]map[string]any
	if getJSON(t, ts.URL+"/api/rag/reports/chunk?id=nope", &miss); len(miss["chunks"]) != 0 {
		t.Fatalf("missing chunk: %v", miss)
	}
	id := run.Result.Hits[0].ID
	var hit struct {
		Chunks    map[string]any            `json:"chunks"`
		Neighbors map[string]map[string]any `json:"neighbors"`
	}
	getJSON(t, ts.URL+"/api/rag/reports/chunk?id="+url.QueryEscape(id), &hit)
	if hit.Chunks[id] == nil || hit.Neighbors[id] == nil {
		t.Fatalf("chunk lookup: %v", hit)
	}
	var doc map[string]any
	docID := strings.SplitN(id, "#", 2)[0]
	if getJSON(t, ts.URL+"/api/rag/reports/doc?id="+url.QueryEscape(docID), &doc); doc["id"] != docID || doc["text"] != nil {
		t.Fatalf("doc lookup: %v", doc)
	}
	var tm map[string][]timing.Entry
	if getJSON(t, ts.URL+"/api/rag/reports/timings", &tm); len(tm["entries"]) == 0 {
		t.Fatal("no timings recorded")
	}

	// pages and assets
	for path, want := range map[string]string{"/apps/reports/": "app-shell.js", "/app-shell.js": "RemoteRag", "/gui.css": ".g-result", "/gui.js": "class Runtime"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b := new(strings.Builder)
		_, _ = bufio.NewReader(res.Body).WriteTo(b)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(b.String(), want) {
			t.Errorf("GET %s: %d, want %q in body", path, res.StatusCode, want)
		}
	}
}
