// Package decide holds rag-workbench's decision-model helpers: a deterministic
// fake provider for tests and offline demos (RAGWB-007). Real providers come
// from geppetto's pkg/decision (systemone, pi.AgentDir.Decider).
package decide

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-go-golems/geppetto/pkg/decision"
)

// Fake answers decision questions by word overlap, deterministically:
//
//   - noul: the share of the state's query terms found in the instructions
//     and the true-case description, squeezed into [0.02, 0.98];
//   - choice: a softmax over each option's term overlap with the state;
//   - score: the middle level.
//
// Calls counts Decide calls; Delay sleeps before answering (respecting ctx).
type Fake struct {
	Delay time.Duration

	mu    sync.Mutex
	calls int
	last  []decision.Request
}

var _ decision.Provider = (*Fake)(nil)

// Model reports the fake model.
func (f *Fake) Model() decision.Model { return decision.Model{Provider: "fake", Name: "fake"} }

// Calls returns how many Decide calls were made.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Requests returns the requests received, in order.
func (f *Fake) Requests() []decision.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]decision.Request(nil), f.last...)
}

// Decide validates the request like a real provider and answers it.
func (f *Fake) Decide(ctx context.Context, in decision.Request) (decision.Response, error) {
	f.mu.Lock()
	f.calls++
	f.last = append(f.last, in)
	f.mu.Unlock()
	start := time.Now()
	if err := decision.ValidateRequest(in, f.Model()); err != nil {
		return decision.Response{}, err
	}
	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-ctx.Done():
			return decision.Response{}, ctx.Err()
		}
	}
	stateText := text(in.State)
	query := terms(stateText)
	if m, ok := in.State.(map[string]any); ok {
		if q, ok := m["query"].(string); ok && q != "" {
			query = terms(q)
		}
	}
	answers := make(map[string]decision.Answer, len(in.Questions))
	for id, q := range in.Questions {
		switch q.Type {
		case decision.Noul:
			answers[id] = decision.Answer{Type: q.Type, Probability: overlap(query, terms(text(q.Instructions)+" "+q.True))}
		case decision.Choice:
			state := terms(stateText)
			scores := make([]float64, len(q.Options))
			sum := 0.0
			for i, o := range q.Options {
				scores[i] = math.Exp(4 * overlapCount(state, terms(o.Name+" "+o.Description)))
				sum += scores[i]
			}
			probs := make(map[string]float64, len(q.Options))
			best := 0
			for i, o := range q.Options {
				probs[o.Name] = scores[i] / sum
				if scores[i] > scores[best] {
					best = i
				}
			}
			top := probs[q.Options[best].Name]
			conf := 1.0
			if n := float64(len(q.Options)); n > 1 {
				conf = (top - 1/n) / (1 - 1/n)
			}
			answers[id] = decision.Answer{Type: q.Type, Choice: q.Options[best].Name, Probabilities: probs, Confidence: conf}
		case decision.Score:
			mid := (len(q.Levels) - 1) / 2
			lp := make([]float64, len(q.Levels))
			lp[mid] = 1
			answers[id] = decision.Answer{Type: q.Type, Score: float64(mid), Confidence: 0.5, LevelProbabilities: lp}
		}
	}
	ms := time.Since(start).Milliseconds()
	return decision.Response{Provider: "fake", Model: "fake", Answers: answers, Usage: &decision.Usage{InputTokens: len(strings.Fields(stateText))}, DurationMs: &ms}, nil
}

var wordRE = regexp.MustCompile(`[\p{L}\p{N}]{3,}`)

func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func terms(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRE.FindAllString(strings.ToLower(s), -1) {
		out[w] = true
	}
	return out
}

func overlapCount(a, b map[string]bool) float64 {
	n := 0.0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return n
}

func overlap(query, doc map[string]bool) float64 {
	if len(query) == 0 {
		return 0.5
	}
	return 0.02 + 0.96*overlapCount(query, doc)/float64(len(query))
}
