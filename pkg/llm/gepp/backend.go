// Package gepp is an llm.Backend on Geppetto with Pi's agent directory
// (~/.pi/agent: models, credentials, settings), read by Geppetto's
// providers/pi (RAGWB-006). Each sample() call runs as rounds of one engine
// call each; text deltas stream to onText as the provider sends them, tools
// run between rounds, and the last round is forced to answer. Ported from
// slop-computer pkg/llm/gepp onto rag-workbench's llm interface.
package gepp

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/go-go-golems/geppetto/pkg/steps/ai/providers/pi"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// DefaultTiers are the models of `--llm pi` when no tier flag is given: the
// LunaRoute models the direct client uses, named with their Pi provider.
func DefaultTiers() llm.TierModels {
	return llm.TierModels{Quick: "lunaroute/muse-spark-open", Default: "lunaroute/glm-5.3-flash", Complex: "lunaroute/glm-5.3"}
}

// Config configures New.
type Config struct {
	// AgentDir is Pi's agent directory; "" = $PI_CODING_AGENT_DIR or ~/.pi/agent.
	AgentDir string
	// Tiers maps quick/default/complex to "provider/model[:thinking]" (or a
	// bare model id that names exactly one available model); "" = Pi's
	// default model.
	Tiers llm.TierModels
	// MaxRounds bounds the tool loop (0 = llm.DefaultMaxRounds); the last
	// round is forced to answer without tools.
	MaxRounds int
	// Options are passed to pi.LoadAgentDir (tests: pi.WithLocalProviders()).
	Options []pi.Option
}

// Backend implements llm.Backend.
type Backend struct {
	dir       *pi.AgentDir
	tiers     map[string]choice
	baseline  string // settings.json's defaultThinkingLevel; "" when off
	maxRounds int
}

var _ llm.Backend = (*Backend)(nil)

// choice is a model and the thinking level its reference named, if any.
type choice struct {
	model pi.Model
	level string
}

func (c choice) ref() string {
	if c.level != "" {
		return c.model.Ref() + ":" + c.level
	}
	return c.model.Ref()
}

var tierNames = []string{"quick", "default", "complex"}

// New loads the agent directory and resolves the tier models.
func New(cfg Config) (*Backend, error) {
	dirPath := cfg.AgentDir
	if dirPath == "" {
		dirPath = pi.DefaultDir(os.Getenv(pi.EnvAgentDir))
	}
	dir, err := pi.LoadAgentDir(dirPath, cfg.Options...)
	if err != nil {
		return nil, fmt.Errorf("pi agent directory %s: %w", dirPath, err)
	}
	b := &Backend{dir: dir, tiers: map[string]choice{}, maxRounds: cfg.MaxRounds}
	if b.maxRounds <= 0 {
		b.maxRounds = llm.DefaultMaxRounds
	}
	if l := dir.Settings.DefaultThinkingLevel; l != "off" {
		b.baseline = l
	}
	eligible := b.eligible()
	refs := map[string]string{"quick": cfg.Tiers.Quick, "default": cfg.Tiers.Default, "complex": cfg.Tiers.Complex}
	for _, tier := range tierNames {
		ref := refs[tier]
		if ref == "" {
			m, ok := b.preferred(eligible)
			if !ok {
				return nil, fmt.Errorf("no authenticated Pi model for tier %s (log in with pi, or pass --llm-%s provider/model)", tier, tier)
			}
			b.tiers[tier] = choice{model: m}
			continue
		}
		c, err := lookup(ref, eligible)
		if err != nil {
			return nil, fmt.Errorf("tier %s: %w", tier, err)
		}
		b.tiers[tier] = c
	}
	return b, nil
}

// eligible is every available model (supported API, credential present) by reference.
func (b *Backend) eligible() map[string]pi.Model {
	out := map[string]pi.Model{}
	for _, m := range b.dir.Available() {
		out[m.Ref()] = m
	}
	return out
}

// preferred is Pi's default model when it is available and enabled, else
// the first available enabled model, else none.
func (b *Backend) preferred(eligible map[string]pi.Model) (pi.Model, bool) {
	s := b.dir.Settings
	enabled := func(ref string) bool {
		if len(s.EnabledModels) == 0 {
			return true
		}
		for _, e := range s.EnabledModels {
			if e == ref {
				return true
			}
		}
		return false
	}
	if s.DefaultModel != "" {
		ref := s.DefaultProvider + "/" + s.DefaultModel
		if m, ok := eligible[ref]; ok && enabled(ref) {
			return m, true
		}
	}
	if len(s.EnabledModels) == 0 {
		refs := make([]string, 0, len(eligible))
		for ref := range eligible {
			refs = append(refs, ref)
		}
		sort.Strings(refs)
		if len(refs) > 0 {
			return eligible[refs[0]], true
		}
	}
	for _, ref := range s.EnabledModels {
		if m, ok := eligible[ref]; ok {
			return m, true
		}
	}
	return pi.Model{}, false
}

// lookup resolves "provider/id[:level]", or a bare id that names exactly one
// available model.
func lookup(ref string, eligible map[string]pi.Model) (choice, error) {
	ref = strings.TrimSpace(ref)
	level := ""
	if i := strings.LastIndex(ref, ":"); i > 0 && isLevel(ref[i+1:]) {
		ref, level = ref[:i], ref[i+1:]
	}
	if m, ok := eligible[ref]; ok {
		return choice{model: m, level: level}, nil
	}
	if strings.Contains(ref, "/") {
		return choice{}, fmt.Errorf("no authenticated Pi model %q (pi models are provider/id; is the provider logged in?)", ref)
	}
	var found []pi.Model
	for _, m := range eligible {
		if m.ID == ref {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 1:
		return choice{model: found[0], level: level}, nil
	case 0:
		return choice{}, fmt.Errorf("no authenticated Pi model with id %q", ref)
	}
	refs := make([]string, len(found))
	for i, m := range found {
		refs[i] = m.Ref()
	}
	return choice{}, fmt.Errorf("model id %q is ambiguous (%s): name the provider", ref, strings.Join(refs, ", "))
}

func isLevel(s string) bool {
	switch s {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return true
	}
	return false
}

// pick returns the call's model: an explicit model when it resolves, else the tier's.
func (b *Backend) pick(req llm.Request) (choice, string) {
	tier := req.Tier
	if tier == "" || tier == "inherit" {
		tier = "default"
	}
	if req.Model != "" {
		if c, err := lookup(req.Model, b.eligible()); err == nil {
			return c, tier
		}
	}
	c, ok := b.tiers[tier]
	if !ok {
		tier = "default"
		c = b.tiers[tier]
	}
	return c, tier
}

// level is a call's thinking level: the one its reference named, else off for
// the quick tier, else settings.json's default.
func (b *Backend) level(c choice, tier string) string {
	switch {
	case c.level != "":
		return c.level
	case tier == "quick" || b.baseline == "":
		return "off"
	}
	return b.baseline
}

// Describe names the tier models, for startup lines.
func (b *Backend) Describe() string {
	parts := make([]string, 0, len(tierNames))
	for _, t := range tierNames {
		parts = append(parts, t+"="+b.tiers[t].ref())
	}
	return "pi (" + strings.Join(parts, " ") + ")"
}
