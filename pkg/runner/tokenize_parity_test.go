package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/natives"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// natives.Tokenize must equal rag.util.tokenize (goja) on real text, stemmed and unstemmed.
// RAGWB_PARITY_INCLUDE (e.g. "**/*.md") widens the vault part beyond Research/KB/*/*.md.
func TestTokenizeParity(t *testing.T) {
	lines := []string{"getHTTPResponse snake_case_name v2Api", "Ünïcödé façade naïve — Ωmega", "日本語のテキスト 한국어", "a I x9 9 __init__ HTTPServer2Go", "they're it's the cats' toys", "sses ies xes running hopped happily",
		"x² + y₁ = ℝ³ ⅓ 𝜆ᵀ नमस्ते ไทย Tiếng Việt"}
	paths, _ := filepath.Glob("../../examples/apps/reports/corpus/*.md")
	home, _ := os.UserHomeDir()
	vault := filepath.Join(home, "code/wesen/go-go-golems/go-go-parc")
	more, _ := filepath.Glob(filepath.Join(vault, "Research/KB/*/*.md"))
	if v := os.Getenv("RAGWB_PARITY_INCLUDE"); v != "" {
		more = nil
		for _, pat := range strings.Split(v, ",") {
			ms, err := doublestar.Glob(os.DirFS(vault), pat)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range ms {
				more = append(more, filepath.Join(vault, m))
			}
		}
	}
	seen := map[string]bool{}
	for _, p := range append(paths, more...) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			if !seen[l] {
				seen[l] = true
				lines = append(lines, l)
			}
		}
	}
	factory, err := NewFactory(Config{})
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.Minute
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
	in, _ := json.Marshal(lines)
	v, err := ragjs.Eval(ctx, rt, "tok", func(vm *goja.Runtime) (goja.Value, error) {
		return vm.RunString(`(() => { const rag = require("ragwb"); const ls = ` + string(in) + `;
			return JSON.stringify(ls.map(l => [rag.util.tokenize(l), rag.util.tokenize(l, { stem: false })])); })()`)
	})
	if err != nil {
		t.Fatal(err)
	}
	var js [][2][]string
	if err := json.Unmarshal([]byte(v.(string)), &js); err != nil {
		t.Fatal(err)
	}
	bad := 0
	for i, l := range lines {
		for k, stem := range []bool{true, false} {
			got := natives.Tokenize(l, stem)
			if strings.Join(got, "\x00") != strings.Join(js[i][k], "\x00") {
				if bad++; bad <= 20 {
					t.Errorf("line %d stem=%v %q:\n  js: %q\n  go: %q", i, stem, l, js[i][k], got)
				}
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d (line, stem) pairs differ", bad, 2*len(lines))
	}
	t.Logf("%d lines tokenized identically", len(lines))
}
