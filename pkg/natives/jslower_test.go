package natives

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// jsLower must equal goja's String.prototype.toLowerCase, including full
// mappings (İ) and the Final_Sigma rule, which strings.ToLower gets wrong.
func TestJSLowerMatchesGoja(t *testing.T) {
	inputs := []string{
		"", "ASCII Only", "Ünïcödé FAÇADE", "İstanbul", "ẞ STRASSE", "ǅemal ǈ ǋ",
		"Σ", "IΣ", "ΣΑΣ", "ΟΔΥΣΣΕΥΣ", "AΣ.", "AΣ'B", "nΣᵧ", "TΣ,W", "ΑΣ Β", "Σ1", "aΣ́", "aΣ́b",
		"𝐾Σ", "ΣΣΣ", "ΩMEGA ΣΟΦΟΣ", "日本語 ＡＢＣ", "Ⅻ Ⓐ",
	}
	vm := goja.New()
	stdlibWrong := 0
	for _, in := range inputs {
		v, err := vm.RunString("(s => s.toLowerCase())")
		if err != nil {
			t.Fatal(err)
		}
		fn, _ := goja.AssertFunction(v)
		res, err := fn(goja.Undefined(), vm.ToValue(in))
		if err != nil {
			t.Fatal(err)
		}
		want := res.String()
		if got := jsLower(in); got != want {
			t.Errorf("jsLower(%q) = %q, goja = %q", in, got, want)
		}
		if strings.ToLower(in) != want {
			stdlibWrong++
		}
	}
	if stdlibWrong < 5 {
		t.Fatalf("only %d inputs where strings.ToLower differs from goja; the cases no longer exercise jsLower", stdlibWrong)
	}
}
