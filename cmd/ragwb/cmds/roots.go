package cmds

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
	"github.com/go-go-golems/rag-workbench/pkg/llm/gepp"
	"github.com/go-go-golems/rag-workbench/pkg/natives"
)

var (
	extraRoots   []string
	jsComponents bool
	indexBackend string
	llmName      string
	llmTiers     llm.TierModels // "" per tier: the backend's default
	piAgentDir   string
	buildCalls   bool
	fullBuild    bool
	embedName    string
	maxRounds    int
)

// AddGlobalFlags registers persistent flags shared by run, presets and verbs.
func AddGlobalFlags(root *cobra.Command) {
	AddRootsFlag(root)
	root.PersistentFlags().BoolVar(&jsComponents, "js-components", false,
		"Use rag.js's own parse.markdown, chunk.sections and index.bm25 instead of the Go ports (parity debugging)")
	f := root.PersistentFlags()
	f.StringVar(&indexBackend, "index-backend", "bleve",
		"Default backend of index.bm25 when a plan does not set one: bleve (disk-backed, opens in ms) or memory (exact port of the browser engine's BM25)")
	f.StringVar(&llmName, "llm", "none", "LLM backend for LLM nodes: none, fake, pi (Geppetto with Pi's models and credentials; streams tokens; recommended), lunaroute (direct client, no token streaming; key: $LUNAROUTE_API_KEY or Pi's models.json)")
	lt, pt := llm.DefaultTiers(), gepp.DefaultTiers()
	f.StringVar(&llmTiers.Quick, "llm-quick", "", fmt.Sprintf("Model for modelTier 'quick' (default %s; with --llm pi a Pi reference provider/model[:thinking], default %s)", lt.Quick, pt.Quick))
	f.StringVar(&llmTiers.Default, "llm-default", "", fmt.Sprintf("Model for modelTier 'default' (default %s; with --llm pi, %s)", lt.Default, pt.Default))
	f.StringVar(&llmTiers.Complex, "llm-complex", "", fmt.Sprintf("Model for modelTier 'complex' (default %s; with --llm pi, %s)", lt.Complex, pt.Complex))
	f.StringVar(&piAgentDir, "pi-agent-dir", "", "Pi's agent directory for --llm pi (models, credentials, settings; default $PI_CODING_AGENT_DIR or ~/.pi/agent)")
	f.IntVar(&maxRounds, "llm-max-rounds", llm.DefaultMaxRounds, "Most model rounds per sample() call with tools (the last is forced to answer)")
	f.StringVar(&embedName, "embed", "none", "Embeddings/rerank backend for index.vector and rerank.model: none, lunaroute (cache: ~/.ragwb/cache/embeddings.db)")
	f.BoolVar(&buildCalls, "build-calls", false, "Let LLM-costing build nodes (enrich.llm, doc cards) run instead of deferring them")
	f.BoolVar(&fullBuild, "full", false, "Rebuild changed nodes from scratch instead of continuing from their stored previous version (delta builds)")
	addStoreFlags(f)
	f.Bool("timings", false, "Print where the time went (startup, runtime, build/load/query per node, output) to stderr")
}

// LLM returns the backend selected by --llm (nil for "none").
func LLM() (llm.Backend, error) {
	switch llmName {
	case "", "none":
		return nil, nil
	case "fake":
		// RAGWB_FAKE_STREAM="pieces,delay" (e.g. "20,40ms") streams fake replies slowly, for demos of live output
		f := &llm.Fake{}
		if v := os.Getenv("RAGWB_FAKE_STREAM"); v != "" {
			n, d, _ := strings.Cut(v, ",")
			if p, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				f.Pieces = p
			}
			if dd, err := time.ParseDuration(strings.TrimSpace(d)); err == nil {
				f.Delay = dd
			}
		}
		return f, nil
	case "pi":
		return gepp.New(gepp.Config{AgentDir: piAgentDir, Tiers: tiersOr(gepp.DefaultTiers()), MaxRounds: maxRounds})
	case "lunaroute":
		c, err := llm.NewLunaRoute()
		if err != nil {
			return nil, err
		}
		c.Tiers = tiersOr(llm.DefaultTiers())
		if maxRounds > 0 {
			c.MaxRounds = maxRounds
		}
		return c, nil
	}
	return nil, fmt.Errorf("unknown --llm %q (none, fake, pi, lunaroute)", llmName)
}

// tiersOr fills the tiers not set by --llm-quick/-default/-complex from def.
func tiersOr(def llm.TierModels) llm.TierModels {
	t := llmTiers
	if t.Quick == "" {
		t.Quick = def.Quick
	}
	if t.Default == "" {
		t.Default = def.Default
	}
	if t.Complex == "" {
		t.Complex = def.Complex
	}
	return t
}

// DescribeLLM names a backend for startup lines.
func DescribeLLM(b llm.Backend) string {
	switch v := b.(type) {
	case nil:
		return "none"
	case interface{ Describe() string }:
		return v.Describe()
	case *llm.Client:
		return fmt.Sprintf("lunaroute (quick=%s default=%s complex=%s; no token streaming)", v.Tiers.Quick, v.Tiers.Default, v.Tiers.Complex)
	case *llm.Fake:
		return "fake"
	}
	return fmt.Sprintf("%T", b)
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

// FullBuild reports --full.
func FullBuild() bool { return fullBuild }

// BuildCalls reports --build-calls.
func BuildCalls() bool { return buildCalls }

// IndexBackend reports --index-backend.
func IndexBackend() string { return indexBackend }

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
