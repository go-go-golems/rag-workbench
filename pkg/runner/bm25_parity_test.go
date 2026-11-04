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

	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// searchDump builds a corpus with the given bm25 params and returns JSON of
// search/searchIn results for a query set derived from the corpus itself.
func searchDump(t *testing.T, jsComponents bool, root string, bm25 map[string]any) (string, float64) {
	t.Helper()
	// exact parity is the memory backend's contract (bleve may rank differently: RAGWB-002 D2)
	factory, err := NewFactory(Config{Roots: []string{root}, JSComponents: jsComponents, IndexBackend: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	opts, _ := json.Marshal(map[string]any{"root": root, "bm25": bm25})
	v, err := ragjs.Eval(ctx, rt, "bm25-parity", func(vm *goja.Runtime) (goja.Value, error) {
		return vm.RunString(fmt.Sprintf(`(async (o) => {
			const rag = require("ragwb");
			const c = rag.corpus("p").from(rag.source.dir({ root: o.root, include: ["*.md"] }))
				.parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25(o.bm25));
			const ir = rag.compile(c);
			const t0 = Date.now();
			const rep = await rag.engine().build(ir);
			if (!rep.ok) throw new Error(rep.errors.join("\n"));
			const ms = Date.now() - t0;
			const art = rag.engine().artifacts(ir);
			const ix = art.indexes.get("text");
			const chunks = [...art.chunks.values()];
			const queries = ["rebalance law", "protojson int64", "How are arrays of objects flattened into metadata terms?",
				"why did ChatGPT show no actions after OAuth worked", "typed-nil", "2026 07 28", "", "the and of", "xyzzy"];
			for (let i = 0; i < chunks.length; i += 23) { queries.push(chunks[i].headingPath.join(" ")); queries.push(chunks[i].text.slice(0, 80)); }
			const syn = { "rebalance": ["redistribute", "rebalancing law"], "oauth": "authorization", "eval tool": ["execution"] };
			const out = { json: ix.toJSON(), N: ix.N, res: [] };
			for (const q of queries) {
				out.res.push([q, ix.search(q, 15), ix.search(q, 5, { synonyms: syn })]);
			}
			const docIds = [...new Set(chunks.map(c => c.docId))];
			for (const d of docIds) out.res.push([d, ix.searchIn(d, "workbench engine law", 3)]);
			return JSON.stringify({ ms, dump: JSON.stringify(out) });
		})(%s)`, opts))
	})
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		MS   float64
		Dump string
	}
	if err := json.Unmarshal([]byte(v.(string)), &r); err != nil {
		t.Fatal(err)
	}
	return r.Dump, r.MS
}

func TestBm25Parity(t *testing.T) {
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	for i, p := range []map[string]any{
		{},
		{"fields": map[string]any{"title": 2, "headingPath": 3, "callout": 2, "text": 1}},
		{"fields": map[string]any{"text": 1, "meta.tags": 2.5, "headingPath": 0.5}, "k1": 1.6, "b": 0.4, "stem": false},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			js, jsMS := searchDump(t, true, root, p)
			gs, goMS := searchDump(t, false, root, p)
			if js != gs {
				t.Fatalf("bm25 differs %s", firstDiff(js, gs))
			}
			t.Logf("params %v: %d bytes of results identical; build js=%.0fms go=%.0fms", p, len(gs), jsMS, goMS)
		})
	}
}
