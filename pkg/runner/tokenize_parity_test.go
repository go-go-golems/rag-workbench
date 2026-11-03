package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/natives"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// natives.Tokenize must equal rag.util.tokenize (goja) on real text, stemmed and unstemmed.
func TestTokenizeParity(t *testing.T) {
	lines := []string{"getHTTPResponse snake_case_name v2Api", "Ünïcödé façade naïve — Ωmega", "日本語のテキスト 한국어", "a I x9 9 __init__ HTTPServer2Go", "they're it's the cats' toys", "sses ies xes running hopped happily"}
	paths, _ := filepath.Glob("../../examples/apps/reports/corpus/*.md")
	home, _ := os.UserHomeDir()
	more, _ := filepath.Glob(filepath.Join(home, "code/wesen/go-go-golems/go-go-parc/Research/KB/*/*.md"))
	for _, p := range append(paths, more...) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Split(string(b), "\n")...)
	}
	factory, err := NewFactory(Config{})
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
	for i, l := range lines {
		for k, stem := range []bool{true, false} {
			got := natives.Tokenize(l, stem)
			if strings.Join(got, "\x00") != strings.Join(js[i][k], "\x00") {
				t.Fatalf("line %d stem=%v %q:\n  js: %v\n  go: %v", i, stem, l, js[i][k], got)
			}
		}
	}
	t.Logf("%d lines tokenized identically", len(lines))
}
