package cmds

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
	"github.com/go-go-golems/rag-workbench/pkg/natives"
)

var (
	extraRoots   []string
	jsComponents bool
	llmName      string
	llmTiers     = llm.DefaultTiers()
	buildCalls   bool
	embedName    string
)

// AddGlobalFlags registers persistent flags shared by run, presets and verbs.
func AddGlobalFlags(root *cobra.Command) {
	AddRootsFlag(root)
	root.PersistentFlags().BoolVar(&jsComponents, "js-components", false,
		"Use rag.js's own parse.markdown, chunk.sections and index.bm25 instead of the Go ports (parity debugging)")
	f := root.PersistentFlags()
	f.StringVar(&llmName, "llm", "none", "LLM backend for LLM nodes: none, fake, lunaroute (key: $LUNAROUTE_API_KEY or Pi's ~/.pi/agent/models.json)")
	f.StringVar(&llmTiers.Quick, "llm-quick", llmTiers.Quick, "Model for modelTier 'quick'")
	f.StringVar(&llmTiers.Default, "llm-default", llmTiers.Default, "Model for modelTier 'default'")
	f.StringVar(&llmTiers.Complex, "llm-complex", llmTiers.Complex, "Model for modelTier 'complex'")
	f.StringVar(&embedName, "embed", "none", "Embeddings/rerank backend for index.vector and rerank.model: none, lunaroute (cache: ~/.ragwb/cache/embeddings.db)")
	f.BoolVar(&buildCalls, "build-calls", false, "Let LLM-costing build nodes (enrich.llm, doc cards) run instead of deferring them")
}

// LLM returns the backend selected by --llm (nil for "none").
func LLM() (llm.Backend, error) {
	switch llmName {
	case "", "none":
		return nil, nil
	case "fake":
		return &llm.Fake{}, nil
	case "lunaroute":
		c, err := llm.NewLunaRoute()
		if err != nil {
			return nil, err
		}
		c.Tiers = llmTiers
		return c, nil
	}
	return nil, fmt.Errorf("unknown --llm %q (none, fake, lunaroute)", llmName)
}

// Embedder returns the embeddings/rerank backend selected by --embed and the
// persistent embedding cache (both nil for "none").
func Embedder() (natives.Embedder, *natives.EmbedCache, error) {
	switch embedName {
	case "", "none":
		return nil, nil, nil
	case "lunaroute":
		c, err := llm.NewLunaRoute()
		if err != nil {
			return nil, nil, err
		}
		cache, err := natives.OpenEmbedCache(natives.DefaultEmbedCachePath())
		if err != nil {
			return nil, nil, err
		}
		return c, cache, nil
	}
	return nil, nil, fmt.Errorf("unknown --embed %q (none, lunaroute)", embedName)
}

// BuildCalls reports --build-calls.
func BuildCalls() bool { return buildCalls }

// JSComponents reports the --js-components flag.
func JSComponents() bool { return jsComponents }

// AddRootsFlag registers the persistent --root flag (repeatable): directories
// scripts may read through rag.source.dir.
func AddRootsFlag(root *cobra.Command) {
	root.PersistentFlags().StringArrayVar(&extraRoots, "root", nil,
		"Directory that rag.source.dir may read (repeatable; the working directory and the verbs --dir are always allowed)")
}

// Roots returns the allowlist: the working directory, the given dirs, and every --root.
func Roots(dirs ...string) []string {
	out := []string{}
	if wd, err := os.Getwd(); err == nil {
		out = append(out, wd)
	}
	for _, d := range dirs {
		if d != "" {
			out = append(out, d)
		}
	}
	return append(out, extraRoots...)
}
