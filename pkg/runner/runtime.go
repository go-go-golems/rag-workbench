// Package runner builds go-go-goja runtimes with the ragwb modules and runs
// workbench scripts and jsverbs app directories headlessly.
package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/dop251/goja_nodejs/require"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/llm"
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
	// JSComponents keeps rag.js's own parse.markdown, chunk.sections and index.bm25
	// instead of the Go ports (natives.Markdown, natives.Bm25Native). Used by parity
	// tests and as an escape hatch (ragwb --js-components).
	JSComponents bool
	// Natives are extra Go-backed components installed into every ragwb module instance.
	Natives []ragjs.Native
	// LLM backs ctx.sample for LLM nodes (nil = LLM nodes unavailable).
	LLM llm.Backend
	// Embedder backs index.vector and rerank.model (nil = those components fail with a hint).
	Embedder natives.Embedder
	// EmbedCache persists embeddings across runs (nil = no cache).
	EmbedCache *natives.EmbedCache
	// BuildCalls allows LLM-costing build nodes to run during build.
	BuildCalls bool
	// Artifacts persists build outputs by digest (docs, chunks, BM25 indexes), so a
	// later run over an unchanged corpus loads them instead of rebuilding. Open it with
	// ArtifactNamespace(JSComponents). Nil keeps everything in memory.
	Artifacts *artifacts.Store
}

// ArtifactNamespace names the artifact namespace for this binary: the rag.js
// source, the Go components' version and the component mode. Anything that can
// change an output without changing its digest belongs here.
func ArtifactNamespace(jsComponents bool) string {
	src, _ := ragjs.Source("rag.js")
	mode := "go"
	if jsComponents {
		mode = "js"
	}
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00", natives.Version, mode)
	_, _ = h.Write(src)
	return hex.EncodeToString(h.Sum(nil))[:16]
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
	nat := []ragjs.Native{&natives.SourceDir{Roots: cfg.Roots, Base: cfg.Base}}
	if !cfg.JSComponents {
		nat = append(nat, natives.Markdown{}, natives.Bm25Native{})
	}
	nat = append(nat, &natives.Vectors{Embedder: cfg.Embedder, Cache: cfg.EmbedCache})
	nat = append(nat, cfg.Natives...)
	if cfg.Artifacts != nil {
		nat = append(nat, natives.Persist{Store: cfg.Artifacts})
	}
	b := engine.NewRuntimeFactoryBuilder().
		WithModules(ragjs.Modules(ragjs.Options{Log: logf, Natives: nat, Sample: cfg.LLM, BuildCalls: cfg.BuildCalls})...).
		UseModuleMiddleware(engine.MiddlewareExclude(excludedModules...))
	if cfg.SourceLoader != nil {
		b = b.WithRequireOptions(require.WithLoader(cfg.SourceLoader))
	}
	return b.Build()
}
