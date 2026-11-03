// Package runner builds go-go-goja runtimes with the ragwb modules and runs
// workbench scripts and jsverbs app directories headlessly.
package runner

import (
	"fmt"
	"os"

	"github.com/dop251/goja_nodejs/require"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/natives"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// Config is shared by every runtime the CLI creates.
type Config struct {
	// Log receives rag engine log lines; nil writes them to stderr when Verbose.
	Log     func(line string)
	Verbose bool
	// SourceLoader resolves non-native require() paths (jsverbs uses an overlay
	// loader over the scanned files). Nil uses the default filesystem loader.
	SourceLoader require.SourceLoader
	// Roots are the directories rag.source.dir may read (allowlist).
	Roots []string
	// Base resolves relative rag.source.dir roots (app dir for verbs; "" = working directory).
	Base string
	// Natives are extra Go-backed components installed into every ragwb module instance.
	Natives []ragjs.Native
}

// excludedModules are default go-go-goja modules scripts must not get.
var excludedModules = []string{"exec"}

// NewFactory returns a runtime factory with ragwb, ragwb/gui, ragwb/kit and the
// default go-go-goja modules minus excludedModules.
func NewFactory(cfg Config) (*engine.RuntimeFactory, error) {
	logf := cfg.Log
	if logf == nil && cfg.Verbose {
		logf = func(line string) { fmt.Fprintln(os.Stderr, "rag:", line) }
	}
	nat := append([]ragjs.Native{&natives.SourceDir{Roots: cfg.Roots, Base: cfg.Base}}, cfg.Natives...)
	b := engine.NewRuntimeFactoryBuilder().
		WithModules(ragjs.Modules(ragjs.Options{Log: logf, Natives: nat})...).
		UseModuleMiddleware(engine.MiddlewareExclude(excludedModules...))
	if cfg.SourceLoader != nil {
		b = b.WithRequireOptions(require.WithLoader(cfg.SourceLoader))
	}
	return b.Build()
}
