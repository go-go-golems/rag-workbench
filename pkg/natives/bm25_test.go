package natives

import (
	"math"
	"math/rand"
	"strconv"
	"testing"

	"github.com/dop251/goja"
)

// jsToFixed must agree with goja's Number#toFixed (the parity target), including exact binary ties.
func TestToFixedMatchesGoja(t *testing.T) {
	vm := goja.New()
	fn, _ := goja.AssertFunction(vm.Get("Number").ToObject(vm).Get("prototype").ToObject(vm).Get("toFixed"))
	check := func(x float64, d int) {
		v, err := fn(vm.ToValue(x), vm.ToValue(d))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := strconv.ParseFloat(v.String(), 64)
		if got := jsToFixed(x, d); got != want {
			t.Fatalf("toFixed(%v, %d): goja %v, go %v", x, d, want, got)
		}
	}
	for _, x := range []float64{0.125, 0.375, 0.625, 0.875, 1.005, 2.5, 0.5, 1.5, 12.67585, 7.390850, 0.00005, 9.99995, 0.015625, 3.0, 0} {
		for d := 0; d <= 4; d++ {
			check(x, d)
		}
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		x := r.Float64() * math.Pow(10, float64(r.Intn(4)-1))
		check(x, 2)
		check(x, 4)
	}
}

func TestTokenizeSplitsIdentifiers(t *testing.T) {
	// Expected values come from rag.util.tokenize in goja (see runner tokenizer parity test).
	got := Tokenize("getHTTPResponse snake_case_name v2Api", true)
	want := []string{"get", "httprespons", "gethttprespons", "snak", "case", "name", "snakecasenam", "v2", "api", "v2api"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
