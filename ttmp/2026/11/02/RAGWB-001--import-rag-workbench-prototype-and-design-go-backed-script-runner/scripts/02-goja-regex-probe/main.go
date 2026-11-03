// 02-goja-regex-probe: isolate how goja treats Unicode property escapes (\p{..}).
// Usage: go run . <prototype-dir>
package main

import (
	"fmt"
	"os"

	"github.com/dop251/goja"
)

func main() {
	vm := goja.New()
	if len(os.Args) > 1 {
		src, _ := os.ReadFile(os.Args[1] + "/rag.js")
		if _, err := vm.RunScript("rag.js", string(src)); err != nil {
			fmt.Println(err)
			return
		}
	}
	probes := []string{
		// what the prototype's tokenizer ran into
		`JSON.stringify("rebalance law".match(/[\p{L}\p{N}_]+/gu))`,
		// \p is treated as a literal 'p': the class becomes [p{L}p{N}_]
		`JSON.stringify("apple {LN} pie".match(/[\p{L}\p{N}_]+/gu))`,
		`JSON.stringify("p{L}".match(/^\p{L}$/u))`,
		`JSON.stringify("é".match(/^\p{L}$/u))`,
		`String(/\p{L}/u.source)`,
		// the spec requires a SyntaxError for an unknown property under /u; goja accepts it
		`(() => { try { new RegExp("\\p{NotAProperty}", "u"); return "accepted"; } catch (e) { return "throws " + e.name; } })()`,
		// without /u (Annex B) \p really is an identity escape for 'p' — correct there
		`JSON.stringify("pp".match(/\p/g))`,
	}
	for _, p := range probes {
		v, err := vm.RunString(p)
		fmt.Printf("%-75s => %v %v\n", p, v, err)
	}
}
