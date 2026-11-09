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

	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// buildArtifacts builds a source.dir → parse.markdown → chunk.sections corpus and
// returns JSON of its docs and chunks with the Go-only `range` fields removed.
func buildArtifacts(t *testing.T, jsMarkdown bool, root string, include []string, sections map[string]any) (docs, chunks string, ms float64) {
	t.Helper()
	factory, err := NewFactory(Config{Roots: []string{root}, JSComponents: jsMarkdown})
	if err != nil {
		t.Fatal(err)
	}
	timeout := 5 * time.Minute
	if v, err := time.ParseDuration(os.Getenv("RAGWB_PARITY_TIMEOUT")); err == nil {
		timeout = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	// Long opt-in runs compare per-item hashes ("id hash" lines) instead of full JSON to bound memory.
	hashed := os.Getenv("RAGWB_PARITY_INCLUDE") != "" && os.Getenv("RAGWB_PARITY_FULLJSON") == ""
	opts, _ := json.Marshal(map[string]any{"root": root, "include": include, "sections": sections, "hashed": hashed})
	v, err := ragjs.Eval(ctx, rt, "parity", func(vm *goja.Runtime) (goja.Value, error) {
		return vm.RunString(fmt.Sprintf(`(async (o) => {
			const rag = require("ragwb");
			const c = rag.corpus("parity").from(rag.source.dir({ root: o.root, include: o.include }))
				.parse(rag.markdown()).chunk(rag.sections(o.sections));
			const ir = rag.compile(c);
			const t0 = Date.now();
			const rep = await rag.engine().build(ir);
			if (!rep.ok) throw new Error(rep.errors.join("\n"));
			const ms = Date.now() - t0;
			const art = rag.engine().artifacts(ir);
			const noRange = (k, v) => k === "range" ? undefined :
                ((k === "id" || k === "docId") && typeof v === "string" ? JSON.parse(v)[1] : v);
			// Compare values, not object insertion order. Keep array order significant.
			const clean = x => JSON.parse(JSON.stringify(x, noRange));
			const dump = xs => o.hashed
				? xs.map(x => x.id + " " + rag.util.hash(clean(x))).join("\n")
				: rag.util.stable(clean(xs));
			return JSON.stringify({ ms, docs: dump([...art.docs.values()]), chunks: dump([...art.chunks.values()]) });
		})(%s)`, opts))
	})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		MS           float64
		Docs, Chunks string
	}
	if err := json.Unmarshal([]byte(v.(string)), &out); err != nil {
		t.Fatal(err)
	}
	return out.Docs, out.Chunks, out.MS
}

func firstDiff(a, b string) string {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := max(0, i-120)
			return fmt.Sprintf("at byte %d:\n  js: …%s…\n  go: …%s…", i, a[lo:min(len(a), i+120)], b[lo:min(len(b), i+120)])
		}
	}
	return fmt.Sprintf("length js=%d go=%d", len(a), len(b))
}

func checkParity(t *testing.T, root string, include []string) {
	cases := []map[string]any{
		{"depth": 3, "maxTokens": 450},
		{"depth": 2, "maxTokens": 200, "minTokens": 60},
		{"maxTokens": 120, "prefixHeading": true, "overlap": 40, "dropMermaid": false},
	}
	if os.Getenv("RAGWB_PARITY_INCLUDE") != "" {
		cases = cases[:1] // long opt-in runs: default sectioning only
	}
	for i, sec := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			jsDocs, jsChunks, jsMS := buildArtifacts(t, true, root, include, sec)
			goDocs, goChunks, goMS := buildArtifacts(t, false, root, include, sec)
			if jsDocs != goDocs {
				t.Fatalf("docs differ %s", firstDiff(jsDocs, goDocs))
			}
			if jsChunks != goChunks {
				t.Fatalf("chunks differ %s", firstDiff(jsChunks, goChunks))
			}
			t.Logf("sections%v: %d bytes of canonical chunks identical; build js=%.0fms go=%.0fms", sec, len(goChunks), jsMS, goMS)
		})
	}
}

func TestMarkdownParityReports(t *testing.T) {
	root, _ := filepath.Abs("../../examples/apps/reports/corpus")
	checkParity(t, root, []string{"*.md"})
}

// The real vault exercises far more markdown shapes; skipped when it isn't present.
func TestMarkdownParityVault(t *testing.T) {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, "code/wesen/go-go-golems/go-go-parc")
	if _, err := os.Stat(root); err != nil {
		t.Skip("go-go-parc vault not present")
	}
	include := []string{"Research/KB/**/*.md", "Logs/**/*.md"}
	if v := os.Getenv("RAGWB_PARITY_INCLUDE"); v != "" { // e.g. "**/*.md" for the whole vault
		include = strings.Split(v, ",")
	}
	checkParity(t, root, include)
}
