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

// Empty results must reach JS as [] from the Go components too. Since goja
// 6ace4dd (go-go-goja v0.10.7) a nil Go slice converts to null, so a native
// that returns `var out []T` for "nothing found" would break callers that
// do .length/.map. Covers source.dir with no matching files (empty corpus),
// bm25 search with no hits, an empty query, and searchIn on an unknown doc.
func TestEmptyResultsAreArrays(t *testing.T) {
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	dumps := map[bool]string{}
	for _, jsComponents := range []bool{true, false} {
		t.Run(fmt.Sprintf("jsComponents=%v", jsComponents), func(t *testing.T) {
			factory, err := NewFactory(Config{Roots: []string{root}, JSComponents: jsComponents})
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
			v, err := ragjs.Eval(ctx, rt, "empty-results", func(vm *goja.Runtime) (goja.Value, error) {
				return vm.RunString(fmt.Sprintf(`(async (root) => {
					const rag = require("ragwb");
					const build = async (include) => {
						const c = rag.corpus("e").from(rag.source.dir({ root, include }))
							.parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
						const ir = rag.compile(c);
						const rep = await rag.engine().build(ir);
						if (!rep.ok) throw new Error(rep.errors.join("\n"));
						return rag.engine().artifacts(ir);
					};
					const full = (await build(["*.md"])).indexes.get("text");
					const none = await build(["*.no-such-extension"]);
					const r = {
						noHits: full.search("xyzzy", 5),
						emptyQuery: full.search("", 5),
						stopWordsOnly: full.search("the and of", 5),
						synonymsNoHits: full.search("xyzzy", 5, { synonyms: { xyzzy: ["plugh"] } }),
						searchInUnknownDoc: full.searchIn("no-such-doc", "law", 3),
						emptyCorpusChunks: [...none.chunks.values()],
						emptyCorpusSearch: none.indexes.get("text").search("law", 5),
					};
					const notArrays = Object.entries(r).filter(([, v]) => !Array.isArray(v)).map(([k, v]) => k + "=" + JSON.stringify(v));
					return JSON.stringify({ notArrays, dump: JSON.stringify(r) });
				})(%q)`, root))
			})
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				NotArrays []string
				Dump      string
			}
			if err := json.Unmarshal([]byte(v.(string)), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.NotArrays) > 0 {
				t.Fatalf("results that are not arrays: %v", got.NotArrays)
			}
			dumps[jsComponents] = got.Dump
		})
	}
	if dumps[true] != dumps[false] {
		t.Fatalf("JS and Go components differ on empty results:\n  js: %s\n  go: %s", dumps[true], dumps[false])
	}
}
