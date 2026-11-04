package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/natives"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// RAGWB-003 Phase 2: a stored build records, per node, lineage → (storeKey,
// digest); source.dir stores its manifest; editing one file keeps every
// lineage key and changes every digest downstream of the source.
func TestLineageRecordedForStoredNodes(t *testing.T) {
	src, _ := filepath.Abs("../../examples/apps/reports/corpus")
	root := t.TempDir()
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		if b, err := os.ReadFile(filepath.Join(src, e.Name())); err == nil && !e.IsDir() {
			_ = os.WriteFile(filepath.Join(root, e.Name()), b, 0o644)
		}
	}
	store, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false, "bleve"))
	if err != nil {
		t.Fatal(err)
	}
	type node struct {
		Lineage string `json:"lineage"`
		Digest  string `json:"digest"`
		Saved   bool   `json:"saved"`
	}
	run := func() map[string]node {
		factory, err := NewFactory(Config{Roots: []string{root}, Artifacts: store})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rt.Close(context.Background()) }()
		v, err := ragjs.Eval(ctx, rt, "lineage", func(vm *goja.Runtime) (goja.Value, error) {
			return vm.RunString(fmt.Sprintf(`(async (root) => {
				const rag = require("ragwb");
				const corpus = rag.corpus("l").from(rag.source.dir({ root, include: ["*.md"] }))
					.parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
				const ir = rag.compile(rag.plan("l").over(corpus).signals({ q: "" }).retrieve("text", { k: 3 }).limit());
				const eng = rag.engine(), rep = await eng.build(ir);
				if (!rep.ok) throw new Error(rep.errors.join("\n"));
				const lin = eng.lineages(ir), out = {};
				for (const [id, st] of Object.entries(rep.nodes)) out[id] = { lineage: lin.get(id), digest: st.digest, saved: !!st.saved };
				return JSON.stringify(out);
			})(%q)`, root))
		})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]node{}
		if err := json.Unmarshal([]byte(v.(string)), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := run()
	saved := 0
	for id, n := range first {
		if n.Lineage == "" {
			t.Fatalf("%s has no lineage key: %+v", id, first)
		}
		if !n.Saved {
			continue
		}
		saved++
		e, err := store.LineageLatest(n.Lineage)
		if err != nil || e == nil || e.Digest != n.Digest {
			t.Fatalf("%s: lineage entry %+v (%v), want digest %s", id, e, err, n.Digest)
		}
		m, _ := store.Has(e.StoreKey)
		if m == nil {
			t.Fatalf("%s: lineage points at %s, which is not stored", id, e.StoreKey)
		}
		if m.Type == "source.dir" {
			_, b, err := store.Load(e.StoreKey)
			var man natives.Manifest
			if err == nil {
				err = json.Unmarshal(b, &man)
			}
			if err != nil || len(man.Entries) == 0 || man.Entries[0].Hash == "" {
				t.Fatalf("source.dir manifest: %v, %+v", err, man)
			}
		}
	}
	if saved < 4 {
		t.Fatalf("only %d nodes saved: %+v", saved, first)
	}

	// edit one note: lineage keys are stable, digests change, the lineage grows
	f := filepath.Join(root, ents[0].Name())
	b, _ := os.ReadFile(f)
	_ = os.WriteFile(f, append(b, []byte("\n\nAn appended paragraph.\n")...), 0o644)
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(f, future, future)
	second := run()
	for id, n := range second {
		if n.Lineage != first[id].Lineage {
			t.Fatalf("%s: lineage changed with the corpus (%s → %s)", id, first[id].Lineage, n.Lineage)
		}
		if n.Saved && n.Digest == first[id].Digest {
			t.Fatalf("%s: saved again with the same digest", id)
		}
		if n.Saved {
			if e, _ := store.LineageLatest(n.Lineage); e == nil || e.Digest != n.Digest {
				t.Fatalf("%s: latest lineage entry %+v, want digest %s", id, e, n.Digest)
			}
		}
	}
}
