package runner

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
	"github.com/go-go-golems/rag-workbench/pkg/natives"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// bowEmbedder is a deterministic bag-of-words embedder (1024 hashed buckets) and a
// term-overlap reranker, so vector components can be tested offline.
type bowEmbedder struct{ calls atomic.Int64 }

func (b *bowEmbedder) Embed(_ context.Context, texts []string, _ llm.EmbedOptions) ([][]float32, error) {
	b.calls.Add(1)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 1024)
		for _, w := range natives.Tokenize(t, true) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(w))
			v[h.Sum32()%1024]++
		}
		out[i] = v
	}
	return out, nil
}

func (b *bowEmbedder) Rerank(_ context.Context, _, q string, docs []string, topN int) ([]llm.RerankResult, error) {
	qs := map[string]bool{}
	for _, w := range natives.Tokenize(q, true) {
		qs[w] = true
	}
	res := make([]llm.RerankResult, len(docs))
	for i, d := range docs {
		n := 0
		for _, w := range natives.Tokenize(d, true) {
			if qs[w] {
				n++
			}
		}
		res[i] = llm.RerankResult{Index: i, Score: float64(n)}
	}
	sort.SliceStable(res, func(a, c int) bool { return res[a].Score > res[c].Score })
	return res[:min(topN, len(res))], nil
}

func runVectorScript(t *testing.T, emb natives.Embedder, cache *natives.EmbedCache, script string) map[string]any {
	t.Helper()
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	factory, err := NewFactory(Config{Roots: []string{root}, Embedder: emb, EmbedCache: cache})
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
	v, err := ragjs.Eval(ctx, rt, "vec", func(vm *goja.Runtime) (goja.Value, error) {
		_ = vm.Set("ROOT", root)
		return vm.RunString(script)
	})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(v.(string)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

const vectorScript = `(async () => {
	const rag = require("ragwb");
	const corpus = rag.corpus("v").from(rag.source.dir({ root: ROOT, include: ["*.md"] }))
		.parse(rag.markdown()).chunk(rag.sections()).index("vec", rag.vector());
	const plan = rag.plan("v").over(corpus).signals({ q: "", k: 5 }).retrieve("vec", { k: 20 }).rerank.model({ k: 10 }).limit();
	const ir = rag.compile(plan);
	const change = await rag.run(ir, { q: "rebalance law preserves placements" }, { trigger: "change" });
	const submit = await rag.run(ir, { q: "rebalance law preserves placements" }, { trigger: "submit" });
	return JSON.stringify({
		ix: rag.engine().artifacts(ir).indexes.get("vec").toJSON(),
		change: change.top(3).map(h => h.heading), submit: submit.top(3).map(h => h.heading),
		reason: submit.hits[0].reason || "", errors: [...change.errors, ...submit.errors],
	});
})()`

func TestVectorIndexAndRerankModel(t *testing.T) {
	emb := &bowEmbedder{}
	cache, err := natives.OpenEmbedCache(filepath.Join(t.TempDir(), "emb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	out := runVectorScript(t, emb, cache, vectorScript)
	if errs := out["errors"].([]any); len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	ix := out["ix"].(map[string]any)
	if ix["kind"] != "vector" || ix["chunks"].(float64) != 462 || ix["dims"].(float64) != 1024 {
		t.Fatalf("index = %v", ix)
	}
	if top := out["submit"].([]any); len(top) == 0 || !strings.Contains(top[0].(string), "rebalance law") {
		t.Fatalf("reranked top = %v", top)
	}
	if !strings.HasPrefix(out["reason"].(string), "cross-encoder") {
		t.Fatalf("submit run did not apply rerank.model: reason %q", out["reason"])
	}
	first := emb.calls.Load()

	// A second runtime with the same cache must embed only the two queries, not the corpus.
	emb2 := &bowEmbedder{}
	_ = runVectorScript(t, emb2, cache, vectorScript)
	if emb2.calls.Load() != 1 { // one query embedding (identical query text is cached per index)
		t.Fatalf("cached rebuild made %d embed calls (first build made %d)", emb2.calls.Load(), first)
	}
}

func TestVectorWithoutBackendFailsWithHint(t *testing.T) {
	out := runVectorScriptExpectBuildError(t)
	if !strings.Contains(out, "--embed lunaroute") {
		t.Fatalf("error = %q", out)
	}
}

func runVectorScriptExpectBuildError(t *testing.T) string {
	t.Helper()
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	factory, _ := NewFactory(Config{Roots: []string{root}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rt, _ := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	defer func() { _ = rt.Close(context.Background()) }()
	v, err := ragjs.Eval(ctx, rt, "vec", func(vm *goja.Runtime) (goja.Value, error) {
		_ = vm.Set("ROOT", root)
		return vm.RunString(`(async () => {
			const rag = require("ragwb");
			const c = rag.corpus("v").from(rag.source.dir({ root: ROOT, include: ["*.md"] })).parse(rag.markdown()).chunk(rag.sections()).index("vec", rag.vector());
			const r = await rag.engine().build(rag.compile(c));
			return r.errors.join("; ");
		})()`)
	})
	if err != nil {
		t.Fatal(err)
	}
	return v.(string)
}
