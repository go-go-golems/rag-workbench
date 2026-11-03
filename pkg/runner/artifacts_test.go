package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/natives"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

type nodeStat struct {
	ID     string `json:"id"`
	Reused bool   `json:"reused"`
	Stored bool   `json:"stored"`
	Saved  bool   `json:"saved"`
	Count  int    `json:"count"`
}

type persistRun struct {
	Nodes  map[string]nodeStat `json:"nodes"`
	Dump   string              `json:"dump"`
	Loaded []string            `json:"loaded"` // build nodes whose stored outputs were actually read
}

// persistBuild builds the reports corpus in a fresh runtime (a new engine, as in a
// new CLI process) and runs a few queries. maxTokens varies the plan.
func persistBuild(t *testing.T, store *artifacts.Store, maxTokens int, marker string) persistRun {
	t.Helper()
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	factory, err := NewFactory(Config{Roots: []string{root}, Artifacts: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	v, err := ragjs.Eval(ctx, rt, "persist", func(vm *goja.Runtime) (goja.Value, error) {
		return vm.RunString(fmt.Sprintf(`(async (root, maxTokens, marker) => {
			const rag = require("ragwb");
			const corpus = rag.corpus("p").from(rag.source.dir({ root, include: ["*.md"] }))
				.parse(rag.markdown()).chunk(rag.sections({ depth: 3, maxTokens }))
				.index("text", rag.bm25({ fields: { title: 2, headingPath: 3, text: 1 } }));
			const plan = rag.plan("q").over(corpus).signals({ q: "", k: 5 }).retrieve("text", { k: 20 }).limit();
			const ir = rag.compile(plan);
			const eng = rag.engine();
			const rep = await eng.build(ir);
			if (!rep.ok) throw new Error(rep.errors.join("\n"));
			const art = eng.artifacts(ir);
			const out = [];
			for (const q of ["rebalance law", "protojson int64", "OAuth worked but no actions", "xyzzy"]) {
				const r = await rag.run(plan, { q, k: 5 });
				out.push([q, r.top(5).map(h => [h.id, h.score])]);
			}
			// every chunk and doc, exactly: stored outputs must equal computed ones
			out.push(["chunks", art.chunks.size, rag.util.hash(JSON.stringify([...art.chunks.values()]))]);
			out.push(["docs", art.docs.size, rag.util.hash(JSON.stringify([...art.docs.values()]))]);
			const loaded = Object.keys(rep.nodes).filter(id => { const m = eng.memo.get(rep.nodes[id].digest); return m && m.lazy && m.lazy.done; });
			return JSON.stringify({ nodes: rep.nodes, dump: JSON.stringify(out), loaded });
		})(%q, %d, %q)`, root, maxTokens, marker))
	})
	if err != nil {
		t.Fatal(err)
	}
	var r persistRun
	if err := json.Unmarshal([]byte(v.(string)), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestArtifactsPersistAcrossRuntimes(t *testing.T) {
	store, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false))
	if err != nil {
		t.Fatal(err)
	}
	cold := persistBuild(t, store, 450, "a")
	for id, n := range cold.Nodes {
		if n.Reused {
			t.Errorf("cold run reused %s", id)
		}
	}
	for _, id := range []string{"parse", "chunks", "ix-text"} {
		if !cold.Nodes[id].Saved {
			t.Errorf("cold run did not save %s: %+v", id, cold.Nodes[id])
		}
	}
	// the source stores only a marker: its files are a lazy re-read (see sourcedir.go)

	warm := persistBuild(t, store, 450, "a")
	if warm.Dump != cold.Dump {
		t.Fatalf("results differ after loading from the store:\n cold %s\n warm %s", cold.Dump, warm.Dump)
	}
	for _, id := range []string{"parse", "chunks", "ix-text"} {
		if n := warm.Nodes[id]; !n.Reused || !n.Stored || n.Count != cold.Nodes[id].Count {
			t.Errorf("warm %s = %+v, want stored+reused with count %d", id, n, cold.Nodes[id].Count)
		}
	}
	// nothing reads the stored source: parse's stored docs are what downstream needs
	// (the docs themselves load here only because the test reads art.docs.size)
	for _, id := range warm.Loaded {
		if id == "files" {
			t.Errorf("warm run read the source files; loaded=%v nodes=%+v", warm.Loaded, warm.Nodes)
		}
	}

	// new chunk params: chunks and index recompute from the stored docs, parse is loaded
	changed := persistBuild(t, store, 300, "a")
	if changed.Nodes["chunks"].Reused || changed.Nodes["ix-text"].Reused || !changed.Nodes["parse"].Reused {
		t.Fatalf("after a param change: chunks %+v ix %+v parse %+v", changed.Nodes["chunks"], changed.Nodes["ix-text"], changed.Nodes["parse"])
	}
	if changed.Dump == cold.Dump {
		t.Fatalf("different chunking gave identical results; the test is not discriminating")
	}
}

func TestArtifactsKeyIncludesComponentSource(t *testing.T) {
	store, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false))
	if err != nil {
		t.Fatal(err)
	}
	run := func(marker string) string {
		root, _ := filepath.Abs("../../examples/apps/reports/corpus")
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
		v, err := ragjs.Eval(ctx, rt, "tagged", func(vm *goja.Runtime) (goja.Value, error) {
			return vm.RunString(fmt.Sprintf(`(async (root, marker) => {
				const rag = require("ragwb");
				// same type and params every run; only the component's body changes
				rag.define("chunk.tagged", { phase: "build", in: ["docs"], out: "chunks",
					all: new Function("[docs]", "return docs.slice(0, 2).map((d, i) => ({ id: d.id + '#' + i, docId: d.id, title: d.title, headingPath: [], text: '" + marker + "', blocks: [] }));") });
				const c = rag.corpus("t").from(rag.source.dir({ root, include: ["*.md"] })).parse(rag.markdown()).chunk(rag.chunk.tagged()).index("text", rag.bm25());
				const ir = rag.compile(rag.plan("t").over(c).signals({ q: "", k: 5 }).retrieve("text", { k: 5 }).limit());
				const rep = await rag.engine().build(ir);
				if (!rep.ok) throw new Error(rep.errors.join("\n"));
				return [...rag.engine().artifacts(ir).chunks.values()].map(x => x.text).join(",");
			})(%q, %q)`, root, marker))
		})
		if err != nil {
			t.Fatal(err)
		}
		return v.(string)
	}
	if got := run("one"); got != "one,one" {
		t.Fatalf("first = %q", got)
	}
	if got := run("two"); got != "two,two" {
		t.Fatalf("after editing the component body: %q (a stale stored output was reused)", got)
	}
}

// index.vector persists its vectors: a warm run (no embedding cache at all) loads the
// index, embeds only the query, and returns identical results.
func TestArtifactsVectorIndex(t *testing.T) {
	store, err := artifacts.Open(t.TempDir(), ArtifactNamespace(false))
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	run := func(emb natives.Embedder) (string, error) {
		factory, err := NewFactory(Config{Roots: []string{root}, Embedder: emb, Artifacts: store})
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
		v, err := ragjs.Eval(ctx, rt, "vecstore", func(vm *goja.Runtime) (goja.Value, error) {
			return vm.RunString(fmt.Sprintf(`(async (root) => {
				const rag = require("ragwb");
				const corpus = rag.corpus("v").from(rag.source.dir({ root, include: ["*.md"] }))
					.parse(rag.markdown()).chunk(rag.sections()).index("vec", rag.vector());
				const plan = rag.plan("v").over(corpus).signals({ q: "", k: 5 }).retrieve("vec", { k: 10 }).limit();
				const rep = await rag.engine().build(rag.compile(plan));
				if (!rep.ok) throw new Error(rep.errors.join("\n"));
				const r = await rag.run(plan, { q: "rebalance law preserves placements" });
				if (r.errors.length) throw new Error(r.errors.join("\n"));
				return JSON.stringify({ stored: rep.nodes["ix-vec"].stored || false, top: r.top(5).map(h => [h.id, h.score]) });
			})(%q)`, root))
		})
		if err != nil {
			return "", err
		}
		return v.(string), nil
	}
	cold, err := run(&bowEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	emb := &bowEmbedder{}
	warm, err := run(emb)
	if err != nil {
		t.Fatal(err)
	}
	var c, w struct {
		Stored bool
		Top    [][]any
	}
	_ = json.Unmarshal([]byte(cold), &c)
	_ = json.Unmarshal([]byte(warm), &w)
	if c.Stored || !w.Stored || fmt.Sprint(c.Top) != fmt.Sprint(w.Top) || len(w.Top) == 0 {
		t.Fatalf("cold %s\nwarm %s", cold, warm)
	}
	if n := emb.calls.Load(); n != 1 {
		t.Fatalf("warm run made %d embed calls; want 1 (the query only)", n)
	}
	// a stored index without an embedder: searching fails with the hint, not a panic
	if _, err := run(nil); err == nil || !strings.Contains(err.Error(), "--embed lunaroute") {
		t.Fatalf("no embedder: err = %v", err)
	}
}
