// Package ragjs exposes the RAG workbench grammar (rag.js, gui.js, kit.js) to
// go-go-goja runtimes as the native modules "ragwb", "ragwb/gui" and "ragwb/kit".
//
// The JavaScript sources are vendored from the claude.ai prototype (see
// assets/PROVENANCE.md) and evaluated unmodified apart from the documented
// local changes. They are compiled once per process and run once per runtime;
// require() caches the module objects inside each runtime.
package ragjs

import (
	"embed"
	"fmt"
	"sync"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

//go:embed assets/rag.js assets/gui.js assets/kit.js
var assets embed.FS

// Module names exposed to scripts.
const (
	ModuleRag = "ragwb"
	ModuleGui = "ragwb/gui"
	ModuleKit = "ragwb/kit"
)

// Source returns one of the vendored JavaScript files (rag.js, gui.js, kit.js),
// e.g. for serving the same code to the browser workbench.
func Source(name string) ([]byte, error) {
	return assets.ReadFile("assets/" + name)
}

// Each file is wrapped so it sees a private root object instead of the real
// global object: rag.js takes the CommonJS branch (module.exports), gui.js and
// kit.js publish onto the root we pass in. "globalThis" is a parameter name in
// the wrapper, which shadows the real global inside the vendored code.
const (
	ragWrapperHead  = "(function (module, exports, globalThis) {\n"
	rootWrapperHead = "(function (globalThis) {\n"
	wrapperTail     = "\n})"
)

type programs struct {
	once sync.Once
	rag  *goja.Program
	gui  *goja.Program
	kit  *goja.Program
	err  error
}

var compiled programs

func load() (*programs, error) {
	compiled.once.Do(func() {
		compile := func(file, head string) *goja.Program {
			if compiled.err != nil {
				return nil
			}
			src, err := assets.ReadFile("assets/" + file)
			if err != nil {
				compiled.err = err
				return nil
			}
			p, err := goja.Compile("ragwb/"+file, head+string(src)+wrapperTail, false)
			if err != nil {
				compiled.err = fmt.Errorf("compile %s: %w", file, err)
			}
			return p
		}
		compiled.rag = compile("rag.js", ragWrapperHead)
		compiled.gui = compile("gui.js", rootWrapperHead)
		compiled.kit = compile("kit.js", rootWrapperHead)
	})
	return &compiled, compiled.err
}

// Options configures the host environment installed into rag.configure().
type Options struct {
	// Log receives rag's log lines (engine progress, lambda logs). Nil discards them.
	Log func(line string)
	// Natives are installed into the rag object right after it is created, before
	// any script sees it. Use them to register Go-backed components via rag.define.
	Natives []Native
	// Sample, when set, backs rag's LLM nodes (ctx.sample / ctx.sample.json) with
	// the claude.ai sample contract. Nil leaves LLM nodes failing with sampling_unavailable.
	Sample llm.Backend
	// BuildCalls lets LLM-costing build nodes (enrich.llm, doc cards) run during
	// build; otherwise they are deferred like the workbench's explicit Build button.
	BuildCalls bool
	// Timing receives the engine's timing events (compile, build, load, artifacts, run).
	Timing func(kind, name string, ms float64, detail string)
}

// Native installs Go-backed functionality into a freshly created rag object.
type Native interface {
	Name() string
	Install(vm *goja.Runtime, rag *goja.Object) error
}

// Modules returns the runtime module registrars for ragwb, ragwb/gui and ragwb/kit.
func Modules(opts Options) []engine.RuntimeModuleRegistrar {
	return []engine.RuntimeModuleRegistrar{
		engine.NativeModuleRegistrar{ModuleName: ModuleRag, Loader: ragLoader(opts)},
		engine.NativeModuleRegistrar{ModuleName: ModuleGui, Loader: rootLoader(ModuleGui, "gui", func(p *programs) *goja.Program { return p.gui }, ModuleRag)},
		engine.NativeModuleRegistrar{ModuleName: ModuleKit, Loader: rootLoader(ModuleKit, "kit", func(p *programs) *goja.Program { return p.kit }, ModuleRag, ModuleGui)},
	}
}

func ragLoader(opts Options) func(*goja.Runtime, *goja.Object) {
	return func(vm *goja.Runtime, moduleObj *goja.Object) {
		p, err := load()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		fn := mustFunc(vm, p.rag)
		inner := vm.NewObject()
		innerExports := vm.NewObject()
		_ = inner.Set("exports", innerExports)
		if _, err := fn(goja.Undefined(), inner, innerExports, vm.NewObject()); err != nil {
			panic(err)
		}
		rag := inner.Get("exports").ToObject(vm)
		if err := configureHost(vm, rag, opts); err != nil {
			panic(vm.NewGoError(err))
		}
		for _, n := range opts.Natives {
			if err := n.Install(vm, rag); err != nil {
				panic(vm.NewGoError(fmt.Errorf("install native %s: %w", n.Name(), err)))
			}
		}
		_ = moduleObj.Set("exports", rag)
	}
}

// rootLoader evaluates gui.js / kit.js against a private root that already
// holds the modules they depend on (root.rag, root.gui), and exports root[key].
func rootLoader(name, key string, prog func(*programs) *goja.Program, deps ...string) func(*goja.Runtime, *goja.Object) {
	return func(vm *goja.Runtime, moduleObj *goja.Object) {
		p, err := load()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		root := vm.NewObject()
		req, ok := goja.AssertFunction(vm.Get("require"))
		if !ok {
			panic(vm.NewGoError(fmt.Errorf("%s: require() is not installed in this runtime", name)))
		}
		for _, dep := range deps {
			v, err := req(goja.Undefined(), vm.ToValue(dep))
			if err != nil {
				panic(err)
			}
			_ = root.Set(map[string]string{ModuleRag: "rag", ModuleGui: "gui"}[dep], v)
		}
		fn := mustFunc(vm, prog(p))
		if _, err := fn(goja.Undefined(), root); err != nil {
			panic(err)
		}
		_ = moduleObj.Set("exports", root.Get(key))
	}
}

func mustFunc(vm *goja.Runtime, p *goja.Program) goja.Callable {
	v, err := vm.RunProgram(p)
	if err != nil {
		panic(err)
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		panic(vm.NewGoError(fmt.Errorf("wrapped program did not evaluate to a function")))
	}
	return fn
}

// configureHost calls rag.configure({log, buildCalls}) with Go-backed hooks.
// env.sample is installed only when opts.Sample is set.
func configureHost(vm *goja.Runtime, rag *goja.Object, opts Options) error {
	configure, ok := goja.AssertFunction(rag.Get("configure"))
	if !ok {
		return fmt.Errorf("rag.configure is not a function")
	}
	env := vm.NewObject()
	logf := opts.Log
	if logf == nil {
		logf = func(string) {}
	}
	_ = env.Set("log", func(call goja.FunctionCall) goja.Value {
		line := ""
		for i, a := range call.Arguments {
			if i > 0 {
				line += " "
			}
			line += a.String()
		}
		logf(line)
		return goja.Undefined()
	})
	// Headless runs never spend LLM calls during build unless asked explicitly.
	if opts.BuildCalls {
		_ = env.Set("buildCalls", "auto")
	} else {
		_ = env.Set("buildCalls", "explicit")
	}
	if opts.Timing != nil {
		_ = env.Set("timing", opts.Timing)
	}
	if opts.Sample != nil {
		sample, err := newSample(vm, opts.Sample)
		if err != nil {
			return err
		}
		_ = env.Set("sample", sample)
	}
	_, err := configure(rag, env)
	return err
}
