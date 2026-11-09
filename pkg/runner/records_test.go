package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// Stored chunks are a record file read one chunk at a time: a warm runtime's
// art.chunks is a view, and lookups, reading order, neighbors and filters give
// exactly what the cold runtime's in-memory Map gave.
func TestChunkRecordsReplaceTheChunksMap(t *testing.T) {
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	store, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false))
	if err != nil {
		t.Fatal(err)
	}
	type out struct {
		View      bool     `json:"view"`
		Size      int      `json:"size"`
		Probe     string   `json:"probe"`
		Order     []string `json:"order"`
		Neighbors []string `json:"neighbors"`
		Filtered  []string `json:"filtered"`
		AllHash   string   `json:"allHash"`
	}
	run := func() out {
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
		v, err := ragjs.Eval(ctx, rt, "records", func(vm *goja.Runtime) (goja.Value, error) {
			return vm.RunString(fmt.Sprintf(`(async (root) => {
				const rag = require("ragwb");
				const corpus = rag.corpus("r").from(rag.source.dir({ root, include: ["*.md"] }))
					.parse(rag.markdown()).chunk(rag.sections({ depth: 3, maxTokens: 450 }))
					.index("text", rag.bm25()).index("meta", rag.terms({ keys: ["type", "tags"] }));
				const plan = rag.plan("r").over(corpus).signals({ q: "", k: 10, filters: {} })
					.retrieve("text", { k: 20 }).filter.where({ field: "filters" }).expand.neighbors({ after: 1 }).limit();
				const ir = rag.compile(plan), eng = rag.engine();
				const rep = await eng.build(ir);
				if (!rep.ok) throw new Error(rep.errors.join("\n"));
				const art = eng.artifacts(ir);
				const r = await rag.run(plan, { q: "rebalance law", filters: { type: ["project-report"] } });
				if (r.errors.length) throw new Error(r.errors.join("\n"));
				const first = r.hits[0].id, probe = art.chunks.get(first);
				return JSON.stringify({
					view: art.byId.get("chunks").__records !== undefined, size: art.chunks.size,
					probe: JSON.stringify(probe), order: art.order.get(probe.docId),
					neighbors: r.trace.find(x => x.node === "expand") ? r.hits.map(h => h.id) : [],
					filtered: r.hits.map(h => h.id),
					allHash: rag.util.hash(JSON.stringify([...art.chunks.values()])),
				});
			})(%q)`, root))
		})
		if err != nil {
			t.Fatal(err)
		}
		var o out
		if err := json.Unmarshal([]byte(v.(string)), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	cold := run()
	if cold.View {
		t.Fatal("cold run: chunks were just computed, art.chunks should be the in-memory Map")
	}
	recs := 0
	ms, _ := artifacts.List(store.Dir)
	for _, m := range ms {
		if m.Kind == artifacts.KindDir && m.Codec == "records2" {
			recs++
		}
	}
	if recs != 2 { // docs and chunks (RAGWB-003 records2)
		t.Fatalf("want stored docs and chunk records, store has %+v", ms)
	}
	warm := run()
	if !warm.View {
		t.Fatal("warm run: art.chunks should be a record view")
	}
	if warm.Size != cold.Size || warm.Probe != cold.Probe || fmt.Sprint(warm.Order) != fmt.Sprint(cold.Order) ||
		fmt.Sprint(warm.Filtered) != fmt.Sprint(cold.Filtered) || fmt.Sprint(warm.Neighbors) != fmt.Sprint(cold.Neighbors) || warm.AllHash != cold.AllHash {
		t.Fatalf("records differ from the in-memory chunks:\ncold %+v\nwarm %+v", cold, warm)
	}
	if len(cold.Order) < 2 || len(cold.Filtered) == 0 {
		t.Fatalf("test is not discriminating: %+v", cold)
	}
}
