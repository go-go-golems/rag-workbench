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

	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// The Go index.terms must answer exactly like rag.js's TermsIndex: same ids,
// scores, why objects and order (ties by localeCompare), and the same values().
// RAGWB_PARITY_ROOT points it at another corpus (e.g. the vault; needs the dir).
func TestTermsIndexParity(t *testing.T) {
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	include := `["*.md"]`
	if r := os.Getenv("RAGWB_PARITY_ROOT"); r != "" {
		root, include = r, `["**/*.md"]`
	}
	factory, err := NewFactory(Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	v, err := ragjs.Eval(ctx, rt, "terms-parity", func(vm *goja.Runtime) (goja.Value, error) {
		return vm.RunString(fmt.Sprintf(`(async (root) => {
			const rag = require("ragwb");
			const keys = ["type", "tags", "status", "aliases"];
			const corpus = rag.corpus("t").from(rag.source.dir({ root, include: %s })).parse(rag.markdown()).chunk(rag.sections({ depth: 3, maxTokens: 450 }))
				.index("meta", rag.terms({ keys }));
			const ir = rag.compile(rag.plan("t").over(corpus).signals({ q: "" }).retrieve("meta", { k: 10 }).limit());
			const eng = rag.engine();
			const rep = await eng.build(ir);
			if (!rep.ok) throw new Error(rep.errors.join("\n"));
			const art = eng.artifacts(ir);
			const goIx = art.indexes.get("meta");
			const jsIx = new rag.util.TermsIndex([...art.chunks.values()], { keys });
			const all = Object.keys(Object.assign({}, ...keys.map(k => jsIx.values(k))));
			// every value is a query on small corpora; on big ones a deterministic sample (TermsIndex
			// search scans all values per query, so all-vs-all is quadratic in goja)
			const stride = Math.max(1, Math.ceil(all.length / 600));
			const vals = all.filter((_, i) => i %% stride === 0);
			const queries = [...vals, ...vals.slice(0, 40).map((v, i) => v + " and " + vals[(i * 7) %% vals.length]),
				...vals.slice(0, 20).map(v => v.toUpperCase() + "?"), "", "x", "nothing matches this", "PROJECT-report, typescript; go!"];
			const diffs = [];
			for (const q of queries) {
				const a = JSON.stringify(jsIx.search(q, 50)), b = JSON.stringify(goIx.search(q, 50));
				if (a !== b) diffs.push({ q, js: a.slice(0, 300), go: b.slice(0, 300) });
			}
			for (const k of keys) {
				const a = JSON.stringify(jsIx.values(k)), b = JSON.stringify(goIx.values(k));
				if (a !== b) diffs.push({ values: k, js: a.slice(0, 300), go: b.slice(0, 300) });
			}
			return JSON.stringify({ kind: goIx.toJSON(), n: queries.length, vals: all.length, diffs: diffs.slice(0, 5), ndiff: diffs.length });
		})(%q)`, include, root))
	})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Kind  map[string]any   `json:"kind"`
		N     int              `json:"n"`
		Vals  int              `json:"vals"`
		Diffs []map[string]any `json:"diffs"`
		NDiff int              `json:"ndiff"`
	}
	if err := json.Unmarshal([]byte(v.(string)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Vals < 10 {
		t.Fatalf("too few metadata values to be a test: %+v", out)
	}
	if out.NDiff > 0 {
		t.Fatalf("%d of %d queries differ (index %v); first: %v", out.NDiff, out.N, out.Kind, out.Diffs)
	}
	t.Logf("%d queries over %d values: identical (%v)", out.N, out.Vals, out.Kind)
}
