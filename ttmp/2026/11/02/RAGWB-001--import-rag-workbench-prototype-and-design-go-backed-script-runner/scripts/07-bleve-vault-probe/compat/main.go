package main

import (
	"context"
	"fmt"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"
	bleve "github.com/go-go-golems/goja-bleve/pkg"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
	"github.com/go-go-golems/rag-workbench/pkg/runner"
)

func main() {
	f, err := runner.NewFactory(runner.Config{})
	_ = f
	if err != nil {
		panic(err)
	}
	b, err := engine.NewRuntimeFactoryBuilder().WithModules(ragjs.Modules(ragjs.Options{})...).Build()
	if err != nil {
		panic(err)
	}
	rt, err := b.NewRuntime()
	if err != nil {
		panic(err)
	}
	_ = bleve.Register
	v, err := ragjs.Eval(context.Background(), rt, "x", func(vm *goja.Runtime) (goja.Value, error) { return vm.RunString(`typeof require("ragwb").plan`) })
	fmt.Println(v, err)
}
