package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Fake is a deterministic backend for tests and offline runs. When tools are
// offered it calls the first one once (with FakeToolInput) and then answers.
// Its answer echoes the tier and a digest of the last user turn; requests that
// look like they want JSON (or Request.JSON) get a small JSON object.
type Fake struct {
	// Answer, when set, produces the reply text for a request.
	Answer func(req Request) string
	// FakeToolInput is passed to the first tool, default {}.
	FakeToolInput json.RawMessage
}

func (f *Fake) Sample(ctx context.Context, req Request, onText OnText, exec ExecuteTool) (Result, error) {
	turns, err := NormalizeTurns(req.Turns)
	if err != nil {
		return Result{}, err
	}
	tier := req.Tier
	if tier == "" || tier == "inherit" {
		tier = "default"
	}
	res := Result{TierApplied: tier, ModelApplied: req.Model, Rounds: 1}
	toolNote := ""
	if len(req.Tools) > 0 && exec != nil {
		in := f.FakeToolInput
		if len(in) == 0 {
			in = json.RawMessage(`{}`)
		}
		tr, err := exec(ctx, ToolCall{ID: "fake-1", Name: req.Tools[0].Name, Input: in})
		if err != nil {
			return res, err
		}
		toolNote = fmt.Sprintf(" tool %s → %d bytes", req.Tools[0].Name, len(tr.Value))
		res.Rounds = 2
	}
	last := turns[len(turns)-1].Content
	var text string
	switch {
	case f.Answer != nil:
		text = f.Answer(req)
	case req.JSON || strings.Contains(strings.ToLower(last), "json"):
		b, _ := json.Marshal(map[string]any{"fake": true, "tier": tier, "chars": len(last)})
		text = string(b)
	default:
		text = fmt.Sprintf("[fake %s answer to %d chars%s]", tier, len(last), toolNote)
	}
	res.Text = text
	if onText != nil {
		onText(text, text)
	}
	return res, nil
}
