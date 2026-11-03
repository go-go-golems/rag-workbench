package ragjs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/natives"
)

const corpusDir = "../../examples/apps/reports/corpus"

func newRuntime(t *testing.T, opts Options) *engine.Runtime {
	t.Helper()
	factory, err := engine.NewRuntimeFactoryBuilder().WithModules(Modules(opts)...).Build()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func readCorpus(t *testing.T) []map[string]any {
	t.Helper()
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	files := []map[string]any{}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(corpusDir, n))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, map[string]any{"name": n, "text": string(b)})
	}
	return files
}

func evalJSON(t *testing.T, rt *engine.Runtime, globals map[string]any, script string, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	v, err := Eval(ctx, rt, "test", func(vm *goja.Runtime) (goja.Value, error) {
		for k, g := range globals {
			if err := vm.Set(k, g); err != nil {
				return nil, err
			}
		}
		return vm.RunString(script)
	})
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("script should resolve to a JSON string, got %T", v)
	}
	if err := json.Unmarshal([]byte(s), out); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
}

// goja (≥ e1fc788, via go-go-goja ≥ v0.10.7) supports \p{..} under /u, so rag.js
// takes the [\p{L}\p{N}] word class like the browser (D6), and the Go port
// (natives.Tokenize) must tokenize identically, including letters and numbers
// outside the old fallback ranges (x², ℝ, ₁, Devanagari).
func TestTokenizeUsesUnicodeClassesInGoja(t *testing.T) {
	const text = "Rebalance law für Ωmega x² ℝ³ Δx₁ नमस्ते"
	rt := newRuntime(t, Options{})
	var got struct {
		Tokens []string `json:"tokens"`
		Native bool     `json:"native"`
	}
	evalJSON(t, rt, nil, `(async () => {
		const rag = require("ragwb");
		return JSON.stringify({ tokens: rag.util.tokenize(`+"`"+text+"`"+`), native: rag.util.LN === '\\p{L}\\p{N}' });
	})()`, &got)
	if !got.Native {
		t.Fatalf("rag.util.LN is the fallback class: goja lost \\p{L} support (needs goja ≥ e1fc788)")
	}
	want := natives.Tokenize(text, true)
	if strings.Join(got.Tokens, "|") != strings.Join(want, "|") {
		t.Fatalf("JS tokens = %q, Go natives.Tokenize = %q", got.Tokens, want)
	}
	if !slices.Contains(got.Tokens, "x²") {
		t.Fatalf("tokens = %q, want x² kept whole by \\p{N}", got.Tokens)
	}
}

// Parity with the browser engine: the textbook's worked BM25 example ("rebalance law",
// kit.reports() field weights title:2 headingPath:3 callout:2 text:1) scores 12.6758 in
// the browser. (The minimal preset uses default weights and scores 12.5926 instead.)
func TestMinimalPresetParity(t *testing.T) {
	rt := newRuntime(t, Options{})
	var got struct {
		Top []struct {
			ID      string  `json:"id"`
			Score   float64 `json:"score"`
			Heading string  `json:"heading"`
		} `json:"top"`
		Evals map[string]float64 `json:"evals"`
		App   string             `json:"app"`
	}
	evalJSON(t, rt, map[string]any{"__files": readCorpus(t)}, `(async () => {
		const rag = require("ragwb"), gui = require("ragwb/gui"), kit = require("ragwb/kit");
		rag.configure({ files: () => __files });
		const corpus = kit.reports();
		const plan = rag.plan('minimal').over(corpus).signals({ q: 'rebalance law', k: 10 }).retrieve('text', { k: 20 });
		const app = gui.app('minimal').title('Minimal search').use(plan).state({ selected: null })
			.view(v => v.row(r => r.col(c => c.searchBox('q').status().results())));
		const ir = rag.compile(plan);
		const r = await rag.engine().run(ir, { q: 'rebalance law' });
		const ev = await kit.goldens().run(ir, { engine: rag.engine() });
		return JSON.stringify({ top: r.top(3), evals: ev.summary, app: app.compile().kind });
	})()`, &got)

	if len(got.Top) == 0 {
		t.Fatal("no hits: tokenizer or index broken")
	}
	if got.Top[0].Heading != "5. The core's modules › 5.9 The rebalance law" {
		t.Fatalf("top heading = %q", got.Top[0].Heading)
	}
	if got.Top[0].Score < 12.675 || got.Top[0].Score > 12.677 {
		t.Fatalf("top score = %v, want 12.676 (browser parity)", got.Top[0].Score)
	}
	if got.Evals["n"] != 12 || got.Evals["hit@5"] != 1 {
		t.Fatalf("goldens = %v, want n=12 hit@5=1", got.Evals)
	}
	if got.App != "gui" {
		t.Fatalf("app kind = %q", got.App)
	}
}
