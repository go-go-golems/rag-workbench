package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type servedRun struct {
	Result struct {
		Hits []struct {
			ID    string  `json:"id"`
			Score float64 `json:"score"`
		} `json:"hits"`
		Trace  []map[string]any `json:"trace"`
		Errors []string         `json:"errors"`
	} `json:"result"`
	Chunks    map[string]map[string]any     `json:"chunks"`
	Neighbors map[string]map[string]*string `json:"neighbors"`
}

func openReports(t *testing.T, path string) *ServedApp {
	t.Helper()
	dir, _ := filepath.Abs("../../examples/apps/reports")
	factory, err := NewFactory(Config{Roots: []string{dir}, Base: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a, err := OpenApp(ctx, factory, AppSpec{Name: "reports", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// The reports app module (app.js exports only {app}; its plan comes from app.use)
// opens once and answers queries from the kept session, with every hit's chunk body.
func TestServeAppModule(t *testing.T) {
	dir, _ := filepath.Abs("../../examples/apps/reports")
	a := openReports(t, dir)
	var info struct {
		ID    string         `json:"id"`
		Title string         `json:"title"`
		App   map[string]any `json:"app"`
		IR    struct {
			Outputs map[string]string `json:"outputs"`
		} `json:"ir"`
		Stats struct {
			Chunks int `json:"chunks"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(a.Info, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != "fused" || info.App["kind"] != "gui" || info.IR.Outputs["hits"] == "" || info.Stats.Chunks == 0 {
		t.Fatalf("info = %s", a.Info)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ { // the second run reuses the session
		res, err := a.Run(ctx, json.RawMessage(`{"q":"rebalance law","k":5}`), "change", nil)
		if err != nil {
			t.Fatal(err)
		}
		var r servedRun
		if err := json.Unmarshal([]byte(res.JSON), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Result.Hits) == 0 || len(r.Result.Errors) > 0 {
			t.Fatalf("run %d: %s", i, res.JSON[:min(400, len(res.JSON))])
		}
		for _, h := range r.Result.Hits {
			c := r.Chunks[h.ID]
			if c == nil || c["text"] == nil {
				t.Fatalf("hit %s has no chunk body", h.ID)
			}
			nb, ok := r.Neighbors[h.ID]
			if !ok {
				t.Fatalf("hit %s has no neighbors entry", h.ID)
			}
			for _, x := range nb {
				if x != nil && r.Chunks[*x] == nil {
					t.Fatalf("neighbor %s of %s is not packed", *x, h.ID)
				}
			}
		}
	}
	// a cache miss: one chunk with its neighbors
	s, err := a.Chunk(ctx, "nope")
	if err != nil || s != `{"chunks":{},"neighbors":{}}` {
		t.Fatalf("missing chunk: %q %v", s, err)
	}
}

// A workbench script (*.wb.js) returning {plan} without an app gets the default search app.
func TestServeWorkbenchScript(t *testing.T) {
	dir := t.TempDir()
	corpus, _ := filepath.Abs("../../examples/apps/reports/corpus")
	src := `const c = rag.corpus("s").from(rag.source.dir({ root: ` + jsString(corpus) + `, include: ["*.md"] })).parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
return { plan: rag.plan("s").over(c).signals({ q: "", k: 5 }).retrieve("text", { k: 10 }).limit() };`
	p := filepath.Join(dir, "search.wb.js")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	factory, err := NewFactory(Config{Roots: []string{corpus}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenApp(context.Background(), factory, AppSpec{Name: "s", Path: p})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var info struct {
		App struct {
			Kind string `json:"kind"`
			Uses struct {
				Plan string `json:"plan"`
			} `json:"uses"`
		} `json:"app"`
	}
	_ = json.Unmarshal(a.Info, &info)
	if info.App.Kind != "gui" || info.App.Uses.Plan != "s" {
		t.Fatalf("default app: %s", a.Info)
	}
	res, err := a.Run(context.Background(), json.RawMessage(`{"q":"protojson int64"}`), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var r servedRun
	_ = json.Unmarshal([]byte(res.JSON), &r)
	if len(r.Result.Hits) == 0 {
		t.Fatalf("no hits: %s", res.JSON[:min(300, len(res.JSON))])
	}
}

func jsString(s string) string { b, _ := json.Marshal(s); return string(b) }
