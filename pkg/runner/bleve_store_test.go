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

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// The bleve backend's index is a directory payload: built under the store's
// namespace, moved in when saved, opened read-only (not rebuilt) by later runtimes.
func TestBleveIndexPersistsAcrossRuntimes(t *testing.T) {
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	store, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false, "bleve"))
	if err != nil {
		t.Fatal(err)
	}
	type out struct {
		Backend string                     `json:"backend"`
		Stored  bool                       `json:"stored"`
		Saved   bool                       `json:"saved"`
		Reused  bool                       `json:"reused"`
		Top     [][]any                    `json:"top"`
		Nodes   map[string]json.RawMessage `json:"nodes"`
	}
	run := func() out {
		factory, err := NewFactory(Config{Roots: []string{root}, Artifacts: store, IndexBackend: "bleve"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		v, err := ragjs.Eval(ctx, rt, "bleve-store", func(vm *goja.Runtime) (goja.Value, error) {
			return vm.RunString(fmt.Sprintf(`(async (root) => {
				const rag = require("ragwb");
				const corpus = rag.corpus("b").from(rag.source.dir({ root, include: ["*.md"] }))
					.parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
				const plan = rag.plan("b").over(corpus).signals({ q: "", k: 5 }).retrieve("text", { k: 10 }).limit();
				const ir = rag.compile(plan), eng = rag.engine();
				const rep = await eng.build(ir);
				if (!rep.ok) throw new Error(rep.errors.join("\n"));
				const r = await rag.run(plan, { q: "rebalance law preserves placements" });
				if (r.errors.length) throw new Error(r.errors.join("\n"));
				const n = rep.nodes["ix-text"];
				return JSON.stringify({ backend: eng.artifacts(ir).indexes.get("text").backend, stored: !!n.stored, saved: !!n.saved, reused: !!n.reused, top: r.top(5).map(h => [h.id, h.score]) });
			})(%q)`, root))
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		var o out
		if err := json.Unmarshal([]byte(v.(string)), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	cold := run()
	if cold.Backend != "bleve" || !cold.Saved || cold.Stored || len(cold.Top) == 0 {
		t.Fatalf("cold: %+v", cold)
	}
	ms, _ := artifacts.List(store.Dir)
	dirs := 0
	for _, m := range ms {
		if m.Kind == artifacts.KindDir && m.Codec == "bleve" {
			dirs++
		}
	}
	if dirs != 1 {
		t.Fatalf("want one stored bleve index, store has %+v", ms)
	}
	warm := run()
	if warm.Backend != "bleve" || !warm.Stored || !warm.Reused {
		t.Fatalf("warm: %+v", warm)
	}
	if fmt.Sprint(warm.Top) != fmt.Sprint(cold.Top) {
		t.Fatalf("results differ after reopening from the store:\n cold %v\n warm %v", cold.Top, warm.Top)
	}
	// no build leftovers: temp areas were moved into the store or removed with the runtime
	entries, _ := os.ReadDir(filepath.Join(store.Dir, store.NS))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp dir %s", e.Name())
		}
	}
}

// Without a store, a bleve index lives in a temp dir that is removed when the runtime closes.
func TestBleveIndexWithoutStoreIsCleanedUp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	factory, err := NewFactory(Config{Roots: []string{root}, IndexBackend: "bleve"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ragjs.Eval(ctx, rt, "bleve-nostore", func(vm *goja.Runtime) (goja.Value, error) {
		return vm.RunString(fmt.Sprintf(`(async () => {
			const rag = require("ragwb");
			const c = rag.corpus("n").from(rag.source.dir({ root: %q, include: ["*.md"] })).parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
			const r = await rag.run(rag.plan("n").over(c).signals({ q: "" }).retrieve("text", { k: 3 }).limit(), { q: "protojson" });
			return String(r.hits.length);
		})()`, root))
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := filepath.Glob(filepath.Join(tmp, ".tmp-bm25-*"))
	if len(before) != 1 {
		t.Fatalf("expected one temp index while the runtime is open, got %v", before)
	}
	_ = rt.Close(ctx)
	after, _ := filepath.Glob(filepath.Join(tmp, ".tmp-bm25-*"))
	if len(after) != 0 {
		t.Fatalf("temp index not removed on runtime close: %v", after)
	}
}
