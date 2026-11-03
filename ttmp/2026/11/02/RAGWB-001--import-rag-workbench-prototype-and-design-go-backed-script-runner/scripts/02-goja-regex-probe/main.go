// 02-goja-regex-probe: isolate which regex/unicode features in rag.js differ in goja.
package main

import (
	"fmt"
	"os"

	"github.com/dop251/goja"
)

func main() {
	vm := goja.New()
	src, _ := os.ReadFile(os.Args[1] + "/rag.js")
	if _, err := vm.RunScript("rag.js", string(src)); err != nil {
		fmt.Println(err)
		return
	}
	probes := []string{
		`JSON.stringify("rebalance law".match(/[\p{L}\p{N}_]+/gu))`,
		`JSON.stringify("rebalance law".match(/[a-zA-Z0-9_]+/g))`,
		`JSON.stringify("fooBar".split(/_+|(?<=[a-z0-9])(?=[A-Z])/))`,
		`JSON.stringify("x".replace(/[^\p{L}\p{N}]+/gu, ' '))`,
		`JSON.stringify(rag.util.tokenize("rebalance law"))`,
		`JSON.stringify(Object.keys(rag.util))`,
	}
	for _, p := range probes {
		v, err := vm.RunString(p)
		fmt.Printf("%-70s => %v %v\n", p, v, err)
	}
}
