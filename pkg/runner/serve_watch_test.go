package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
)

// serve --watch (RAGWB-003 Phase 5): Refresh rebuilds a served app incrementally
// after an edit, the next query sees the edit, and superseded handles are closed.
func TestServeRefreshIsIncremental(t *testing.T) {
	src, _ := filepath.Abs("../../examples/apps/reports/corpus")
	root := t.TempDir()
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		if b, err := os.ReadFile(filepath.Join(src, e.Name())); err == nil && !e.IsDir() {
			_ = os.WriteFile(filepath.Join(root, e.Name()), b, 0o644)
		}
	}
	appDir := t.TempDir()
	script := fmt.Sprintf(`const c = rag.corpus("w").from(rag.source.dir({ root: %q, include: ["*.md"] }))
  .parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25()).index("meta", rag.terms());
return { plan: rag.plan("w").over(c).signals({ q: "", k: 5 }).retrieve("text", { k: 10 }).limit() };`, root)
	path := filepath.Join(appDir, "w.wb.js")
	_ = os.WriteFile(path, []byte(script), 0o644)
	store, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false))
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewFactory(Config{Roots: []string{root, appDir}, Base: appDir, Artifacts: store, SourceDigestTTL: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := OpenApp(ctx, factory, AppSpec{Name: "w", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if out, err := a.Refresh(ctx); err != nil || out != "null" {
		t.Fatalf("refresh without changes: %q %v", out, err)
	}
	type refresh struct {
		OK    bool `json:"ok"`
		Nodes []struct {
			ID          string `json:"id"`
			Incremental bool   `json:"incremental"`
		} `json:"nodes"`
		GC struct {
			Closed int `json:"closed"`
		} `json:"gc"`
	}
	closed := 0
	for i := 1; i <= 3; i++ {
		f := filepath.Join(root, ents[i].Name())
		b, _ := os.ReadFile(f)
		marker := fmt.Sprintf("quokka%dmarker", i)
		_ = os.WriteFile(f, append(b, []byte("\n\n## Watched edit\n\n"+marker+" paragraph\n")...), 0o644)
		future := time.Now().Add(time.Duration(i) * time.Minute)
		_ = os.Chtimes(f, future, future)
		out, err := a.Refresh(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var r refresh
		if err := json.Unmarshal([]byte(out), &r); err != nil || !r.OK {
			t.Fatalf("refresh %d: %s %v", i, out, err)
		}
		for _, n := range r.Nodes {
			if !n.Incremental {
				t.Fatalf("refresh %d: %s was rebuilt fully: %s", i, n.ID, out)
			}
		}
		if len(r.Nodes) != 5 {
			t.Fatalf("refresh %d: want 5 rebuilt nodes, got %s", i, out)
		}
		closed += r.GC.Closed
		res, err := a.Run(ctx, json.RawMessage(fmt.Sprintf(`{"q":%q,"k":5}`, marker)), "change", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res.JSON, "watched-edit") {
			t.Fatalf("query after refresh %d does not find the edit: %.300s", i, res.JSON)
		}
	}
	// the first refresh retires version 0, the second closes it, the third closes version 1
	if closed == 0 {
		t.Fatal("no superseded handles were closed")
	}
}
