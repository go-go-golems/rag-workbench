package gepp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/go-go-golems/geppetto/pkg/events"
	"github.com/go-go-golems/geppetto/pkg/inference/engine"
	"github.com/go-go-golems/geppetto/pkg/inference/tools"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/providers/pi"
	"github.com/go-go-golems/geppetto/pkg/turns"
	"github.com/go-go-golems/geppetto/pkg/turns/toolblocks"
	"github.com/invopop/jsonschema"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// Sample runs one sample() call: rounds of one engine call each, tools
// between them, text deltas streamed to onText as they arrive (RAGWB-006 §10.3).
func (b *Backend) Sample(ctx context.Context, req llm.Request, onText llm.OnText, exec llm.ExecuteTool) (llm.Result, error) {
	ts, err := llm.NormalizeTurns(req.Turns)
	if err != nil {
		return llm.Result{}, err
	}
	if n := inputBytes(ts); n > llm.MaxInputBytes {
		return llm.Result{}, &llm.Failure{Code: "prompt_too_large", Message: fmt.Sprintf("input is %d bytes, over %d", n, llm.MaxInputBytes)}
	}
	ch, tier := b.pick(req)
	res := llm.Result{TierApplied: tier, ModelApplied: ch.model.Ref()}
	resolved, err := b.dir.Resolve(ctx, ch.model.Ref(), pi.ResolveOptions{Thinking: b.level(ch, tier)})
	if err != nil {
		return res, classify(err, "")
	}
	t := &turns.Turn{}
	for _, tu := range ts {
		if tu.Role == "assistant" {
			turns.AppendBlock(t, turns.NewAssistantTextBlock(tu.Content))
		} else {
			turns.AppendBlock(t, turns.NewUserTextBlock(tu.Content))
		}
	}
	registry := tools.NewInMemoryToolRegistry()
	for _, tool := range req.Tools {
		if err := registry.RegisterTool(tool.Name, tools.ToolDefinition{Name: tool.Name, Description: tool.Description, Parameters: schemaOf(tool.InputSchema)}); err != nil {
			return res, &llm.Failure{Code: "invalid_request", Message: "tool " + tool.Name + ": " + err.Error()}
		}
	}
	perRound := req.MaxTokens
	if perRound <= 0 {
		perRound = llm.TierBudget[tier]
	}
	text := &stream{onText: onText}
	for round := 1; ; round++ {
		last := round >= b.maxRounds
		offersTools := len(req.Tools) > 0 && exec != nil && !last
		if last && round > 1 && len(req.Tools) > 0 {
			turns.AppendBlock(t, turns.NewUserTextBlock(llm.ForcedAnswer))
		}
		limit := perRound
		if err := engine.KeyInferenceConfig.Set(&t.Data, engine.InferenceConfig{MaxResponseTokens: &limit}); err != nil {
			return res, err
		}
		eng, err := resolved.Engine()
		if err != nil {
			return res, classify(err, text.all())
		}
		rctx := ctx
		if offersTools {
			rctx = tools.WithRegistry(ctx, registry)
		}
		s := &sink{text: text}
		text.startRound()
		out, err := eng.RunInference(events.WithEventSinks(rctx, s), t)
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if err == nil {
			err = s.failed
		}
		if err != nil {
			return res, classify(err, text.all())
		}
		res.Rounds = round
		res.Usage.PromptTokens += s.in
		res.Usage.CompletionTokens += s.out
		res.Truncated = s.truncated
		t = out
		calls := toolblocks.ExtractPendingToolCalls(t)
		if len(calls) == 0 || !offersTools {
			break
		}
		// Sequential, in call order: every tool runs on the same JS event loop (§10.8 D4).
		for _, c := range calls {
			input, err := json.Marshal(c.Arguments)
			if err != nil || c.Arguments == nil {
				input = []byte("{}")
			}
			r, err := exec(ctx, llm.ToolCall{ID: c.ID, Name: c.Name, Input: input})
			if err != nil {
				return res, err
			}
			out := resultText(r.Value)
			if r.IsError {
				turns.AppendBlock(t, turns.NewToolUseBlockWithError(c.ID, out, out))
			} else {
				turns.AppendBlock(t, turns.NewToolUseBlock(c.ID, out))
			}
		}
	}
	all := text.all()
	clean := llm.StripToolMarkup(all)
	if clean != all {
		text.correct(clean) // streamed markup is withdrawn: ctx.emit turns this into a reset
	}
	if strings.TrimSpace(clean) == "" {
		msg := "model produced no answer text"
		if res.Truncated {
			msg += " (max_tokens spent on reasoning)"
		}
		return res, &llm.Failure{Code: "empty_completion", Message: msg}
	}
	res.Text = clean
	return res, nil
}

func inputBytes(ts []llm.Turn) int {
	n := 0
	for _, t := range ts {
		n += len(t.Content)
	}
	return n
}

// stream is a call's answer text across rounds, reported to onText.
type stream struct {
	mu      sync.Mutex
	onText  llm.OnText
	b       strings.Builder
	started bool // the current round has produced text
}

func (s *stream) startRound() {
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
}

func (s *stream) add(delta string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started && s.b.Len() > 0 {
		delta = "\n\n" + delta // rounds are separated by a blank line (sample spec)
	}
	s.started = true
	s.b.WriteString(delta)
	if s.onText != nil {
		s.onText(s.b.String(), delta)
	}
}

func (s *stream) correct(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
	s.b.WriteString(text)
	if s.onText != nil {
		s.onText(text, "")
	}
}

func (s *stream) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// sink turns one round's Geppetto events into streamed text, usage and errors.
type sink struct {
	text      *stream
	in, out   int
	truncated bool
	failed    error
}

func (s *sink) PublishEvent(e events.Event) error {
	switch e := e.(type) {
	case *events.EventTextDelta:
		if e.Delta != "" {
			s.text.add(e.Delta)
		}
	case *events.EventProviderCallFinished:
		switch e.StopReason {
		case "length", "max_tokens", "max_output_tokens", "incomplete":
			s.truncated = true
		}
		if e.Usage != nil {
			s.in += e.Usage.InputTokens
			s.out += e.Usage.OutputTokens
		}
	case *events.EventError:
		if s.failed == nil {
			s.failed = stderrors.New(e.ErrorString)
		}
	}
	return nil
}

// resultText is a tool result as the model reads it: a JSON string as its
// text, anything else as JSON.
func resultText(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	if len(v) == 0 {
		return "null"
	}
	return string(v)
}

// schemaOf parses a tool's JSON schema; none (or an invalid one) is an empty object.
func schemaOf(raw json.RawMessage) *jsonschema.Schema {
	s := &jsonschema.Schema{}
	if len(raw) == 0 || json.Unmarshal(raw, s) != nil {
		return &jsonschema.Schema{Type: "object", Properties: jsonschema.NewProperties()}
	}
	return s
}

var (
	authErr    = regexp.MustCompile(`(?i)\b(?:401|403)\b|unauthori[sz]ed|forbidden|authentication|invalid.{0,20}(?:api.?key|token)|(?:expired|missing).{0,20}(?:token|credential)|no.{0,20}(?:api.?key|credential)`)
	rateErr    = regexp.MustCompile(`(?i)\b429\b|rate.?limit|too many requests`)
	largeErr   = regexp.MustCompile(`(?i)\b413\b|context.{0,20}(?:length|window)|too many tokens|prompt is too long|maximum context`)
	invalidErr = regexp.MustCompile(`(?i)\b(?:400|404|422)\b|bad request|not found|unprocessable`)
	secretish  = regexp.MustCompile(`(?i)(bearer\s+|api[-_ ]?key["':= ]+|token["':= ]+)[A-Za-z0-9._\-]{8,}`)
)

// classify maps a resolution or provider error to a sample failure code.
// Messages stay short and are scrubbed of anything that looks like a key.
func classify(err error, text string) error {
	var f *llm.Failure
	if stderrors.As(err, &f) {
		if f.Text == "" {
			f.Text = text
		}
		return f
	}
	if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := err.Error()
	var nc *pi.NoCredentialError
	var ua *pi.UnsupportedAPIError
	code := "upstream_error"
	switch {
	case stderrors.As(err, &nc):
		code = "not_granted"
		msg = nc.Error()
	case stderrors.As(err, &ua):
		code = "invalid_request"
	case authErr.MatchString(msg):
		code = "not_granted"
	case rateErr.MatchString(msg):
		code = "rate_limited"
	case largeErr.MatchString(msg):
		code = "prompt_too_large"
	case invalidErr.MatchString(msg):
		code = "invalid_request"
	}
	msg = secretish.ReplaceAllString(msg, "${1}<redacted>")
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return &llm.Failure{Code: code, Message: msg, Text: text}
}
