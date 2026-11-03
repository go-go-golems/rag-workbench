// Package llm implements the claude.ai artifact `sample` contract (see
// slop-computer doc/claude-apis/sample-spec.md) on top of OpenAI-compatible
// chat backends (LunaRoute), plus embeddings and reranking clients.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Turn is one input turn; Role is "user" or "assistant" (there is no system role).
type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Tool is a page/script function the model may call.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema of the single object argument (optional)
}

// ToolCall is one model request to run a tool.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult is what a tool returned (JSON value) or why it failed.
type ToolResult struct {
	Value   json.RawMessage
	IsError bool
}

// ExecuteTool runs a tool call on behalf of the backend's tool loop.
type ExecuteTool func(ctx context.Context, call ToolCall) (ToolResult, error)

// Request is one sample() call.
type Request struct {
	Turns     []Turn
	Tier      string // quick | default | complex ("" = default)
	Model     string // explicit backend model id (overrides the tier mapping)
	Tools     []Tool
	MaxTokens int
	JSON      bool // the caller will parse the final text as JSON (sample.json)
}

// Result mirrors SampleResult.
type Result struct {
	Text         string `json:"text"`
	Truncated    bool   `json:"truncated"`
	TierApplied  string `json:"modelTierApplied"`
	ModelApplied string `json:"modelApplied,omitempty"`
	Rounds       int    `json:"-"`
	Usage        Usage  `json:"-"`
}

type Usage struct {
	PromptTokens, CompletionTokens, ReasoningTokens int
}

// OnText receives streaming progress: text is the whole answer so far, delta the new part.
type OnText func(text, delta string)

// Backend answers sample() calls.
type Backend interface {
	Sample(ctx context.Context, req Request, onText OnText, exec ExecuteTool) (Result, error)
}

// Failure is the SampleError shape: {code, message, text?}.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Text    string `json:"text,omitempty"`
}

func (f *Failure) Error() string { return f.Code + ": " + f.Message }

func fail(code, format string, args ...any) *Failure {
	return &Failure{Code: code, Message: fmt.Sprintf(format, args...)}
}

// NormalizeTurns checks and merges turns like the platform: non-empty, user
// first and last, consecutive same-role turns merged.
func NormalizeTurns(turns []Turn) ([]Turn, error) {
	out := []Turn{}
	for _, t := range turns {
		if t.Role != "user" && t.Role != "assistant" {
			return nil, fail("invalid_request", "turn role must be user or assistant, got %q", t.Role)
		}
		if len(out) > 0 && out[len(out)-1].Role == t.Role {
			out[len(out)-1].Content += "\n\n" + t.Content
			continue
		}
		out = append(out, t)
	}
	if len(out) == 0 || strings.TrimSpace(joinContent(out)) == "" {
		return nil, fail("invalid_request", "input is empty")
	}
	if out[0].Role != "user" || out[len(out)-1].Role != "user" {
		return nil, fail("invalid_request", "turns must start and end with a user turn")
	}
	return out, nil
}

func joinContent(ts []Turn) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(t.Content)
	}
	return b.String()
}

// MaxInputBytes is the platform's input cap (256 KiB of UTF-8 text).
const MaxInputBytes = 262144

// ParseJSON implements sample.json's tolerant parsing: the whole reply, the
// body of one Markdown code fence, or the span from the first { or [ to the last } or ].
func ParseJSON(text string) (json.RawMessage, bool) {
	try := func(s string) (json.RawMessage, bool) {
		s = strings.TrimSpace(s)
		if s == "" || !json.Valid([]byte(s)) {
			return nil, false
		}
		return json.RawMessage(s), true
	}
	if v, ok := try(text); ok {
		return v, true
	}
	if i := strings.Index(text, "```"); i >= 0 {
		rest := text[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			if j := strings.Index(rest[nl+1:], "```"); j >= 0 {
				if v, ok := try(rest[nl+1 : nl+1+j]); ok {
					return v, true
				}
			}
		}
	}
	start := strings.IndexAny(text, "{[")
	end := strings.LastIndexAny(text, "}]")
	if start >= 0 && end > start {
		if v, ok := try(text[start : end+1]); ok {
			return v, true
		}
	}
	return nil, false
}

// InvalidJSONMessage explains an invalid_json failure with an excerpt of the reply,
// so the page and run reports show what the model actually said.
func InvalidJSONMessage(res Result) string {
	why := "the reply contained no complete JSON value"
	if res.Truncated {
		why += " (the answer was cut short at max_tokens)"
	}
	ex := strings.Join(strings.Fields(res.Text), " ")
	if len(ex) > 160 {
		ex = ex[:80] + " … " + ex[len(ex)-60:]
	}
	return fmt.Sprintf("%s; reply: %q", why, ex)
}
