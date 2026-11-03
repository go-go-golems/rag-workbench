package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultBaseURL is LunaRoute's gateway.
const DefaultBaseURL = "https://gw.lunaroute.com"

// TierModels maps sample() tiers to chat model ids. Defaults chosen from probes
// (RAGWB-001 diary step 10): GLM models always reason, and reasoning spends max_tokens.
type TierModels struct {
	Quick, Default, Complex string
}

func DefaultTiers() TierModels {
	return TierModels{Quick: "muse-spark-open", Default: "glm-5.3-flash", Complex: "glm-5.3"}
}

// DefaultMaxRounds bounds tool loops. The workbench agent works in many small
// eval steps (a real session used 6 before answering), so allow ten.
const DefaultMaxRounds = 10

// stripToolMarkup removes tool-call syntax some models (GLM) emit as plain text
// when they want a tool but none is offered: everything from the first marker on.
func stripToolMarkup(s string) string {
	for _, m := range []string{"<tool_call>", "<|tool_call|>", "<function_call>"} {
		if i := strings.Index(s, m); i >= 0 {
			s = strings.TrimRight(s[:i], " \n\t")
		}
	}
	return s
}

// tierBudget is max_tokens per tier, sized so reasoning doesn't starve the answer.
var tierBudget = map[string]int{"quick": 2048, "default": 8192, "complex": 16384}

// Client talks to an OpenAI-compatible gateway (LunaRoute): chat, embeddings, rerank.
type Client struct {
	BaseURL string
	Key     string
	Header  string // auth header name; "LUNAROUTE-API-KEY" for LunaRoute, "Authorization" sends "Bearer <key>"
	HTTP    *http.Client
	Tiers   TierModels
	// MaxRounds bounds the tool loop; the last round is forced to answer without tools.
	MaxRounds int
}

// LoadLunaRouteKey returns $LUNAROUTE_API_KEY, else the key Pi uses
// (~/.pi/agent/models.json providers.lunaroute.apiKey). It never logs the key.
func LoadLunaRouteKey() (string, error) {
	if k := strings.TrimSpace(os.Getenv("LUNAROUTE_API_KEY")); k != "" {
		return k, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "models.json"))
	if err != nil {
		return "", fmt.Errorf("no LUNAROUTE_API_KEY and no Pi models.json: %w", err)
	}
	var cfg struct {
		Providers map[string]struct {
			APIKey string `json:"apiKey"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return "", fmt.Errorf("parse Pi models.json: %w", err)
	}
	k := strings.TrimSpace(cfg.Providers["lunaroute"].APIKey)
	if k == "" {
		return "", fmt.Errorf("no LUNAROUTE_API_KEY and Pi models.json has no providers.lunaroute.apiKey")
	}
	return k, nil
}

// NewLunaRoute builds a client from the environment / Pi config.
func NewLunaRoute() (*Client, error) {
	key, err := LoadLunaRouteKey()
	if err != nil {
		return nil, err
	}
	base := DefaultBaseURL
	if v := os.Getenv("LUNAROUTE_BASE_URL"); v != "" {
		base = strings.TrimSuffix(v, "/")
	}
	return &Client{BaseURL: base, Key: key, Header: "LUNAROUTE-API-KEY", HTTP: &http.Client{Timeout: 6 * time.Minute}, Tiers: DefaultTiers(), MaxRounds: DefaultMaxRounds}, nil
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	if c.Header == "Authorization" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	} else {
		req.Header.Set(c.Header, c.Key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return &Failure{Code: "cancelled", Message: ctx.Err().Error()}
		}
		return &Failure{Code: "upstream_error", Message: err.Error()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return &Failure{Code: "upstream_error", Message: err.Error()}
	}
	if resp.StatusCode >= 300 {
		msg := strings.ReplaceAll(string(data), c.Key, "<key>")
		if len(msg) > 400 {
			msg = msg[:400]
		}
		code := "upstream_error"
		switch {
		case resp.StatusCode == 429:
			code = "rate_limited"
		case resp.StatusCode == 400 || resp.StatusCode == 404 || resp.StatusCode == 422:
			code = "invalid_request"
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			code = "not_granted"
		}
		return &Failure{Code: code, Message: fmt.Sprintf("%s %s: HTTP %d: %s", http.MethodPost, path, resp.StatusCode, msg)}
	}
	return json.Unmarshal(data, out)
}

// ── chat (sample)

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          string       `json:"content"`
			ReasoningContent string       `json:"reasoning_content"`
			ToolCalls        []oaToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		ReasoningTokens  int `json:"reasoning_tokens"`
	} `json:"usage"`
}

func (c *Client) model(req Request) (string, string) {
	tier := req.Tier
	if tier == "" || tier == "inherit" {
		tier = "default"
	}
	if req.Model != "" {
		return req.Model, tier
	}
	switch tier {
	case "quick":
		return c.Tiers.Quick, tier
	case "complex":
		return c.Tiers.Complex, tier
	default:
		return c.Tiers.Default, "default"
	}
}

// Sample runs one sample() call, including the tool loop.
func (c *Client) Sample(ctx context.Context, req Request, onText OnText, exec ExecuteTool) (Result, error) {
	turns, err := NormalizeTurns(req.Turns)
	if err != nil {
		return Result{}, err
	}
	if n := len(joinContent(turns)); n > MaxInputBytes {
		return Result{}, fail("prompt_too_large", "input is %d bytes, over %d", n, MaxInputBytes)
	}
	model, tier := c.model(req)
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = tierBudget[tier]
	}
	msgs := make([]oaMessage, 0, len(turns)+4)
	for _, t := range turns {
		msgs = append(msgs, oaMessage{Role: t.Role, Content: t.Content})
	}
	tools := make([]oaTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		var ot oaTool
		ot.Type = "function"
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.InputSchema
		if len(ot.Function.Parameters) == 0 {
			ot.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, ot)
	}
	res := Result{TierApplied: tier, ModelApplied: req.Model}
	var all strings.Builder
	maxRounds := max(1, c.MaxRounds)
	for round := 1; ; round++ {
		lastRound := round >= maxRounds
		if lastRound && len(tools) > 0 && round > 1 {
			// Forced answer: without this, GLM emits its native tool-call markup as text.
			msgs = append(msgs, oaMessage{Role: "user", Content: "(The tool budget for this answer is used up. Answer now from what you have, in plain text, without calling tools.)"})
		}
		body := map[string]any{"model": model, "messages": msgs, "max_tokens": maxTokens}
		if len(tools) > 0 && !lastRound {
			body["tools"] = tools
		}
		var r oaResponse
		if err := c.post(ctx, "/v1/chat/completions", body, &r); err != nil {
			if f, ok := err.(*Failure); ok {
				f.Text = all.String()
			}
			return res, err
		}
		res.Rounds = round
		res.Usage.PromptTokens += r.Usage.PromptTokens
		res.Usage.CompletionTokens += r.Usage.CompletionTokens
		res.Usage.ReasoningTokens += r.Usage.ReasoningTokens
		if len(r.Choices) == 0 {
			return res, &Failure{Code: "upstream_error", Message: "no choices in response", Text: all.String()}
		}
		ch := r.Choices[0]
		content := stripToolMarkup(ch.Message.Content)
		if content != "" {
			delta := content
			if all.Len() > 0 {
				delta = "\n\n" + delta // rounds are separated by a blank line (sample spec)
			}
			all.WriteString(delta)
			if onText != nil {
				onText(all.String(), delta)
			}
		}
		if len(ch.Message.ToolCalls) == 0 || exec == nil || lastRound {
			res.Text = all.String()
			res.Truncated = ch.FinishReason == "length"
			if strings.TrimSpace(res.Text) == "" {
				msg := "model produced no answer text"
				if res.Truncated {
					msg += " (max_tokens spent on reasoning)"
				}
				return res, &Failure{Code: "empty_completion", Message: msg}
			}
			return res, nil
		}
		msgs = append(msgs, oaMessage{Role: "assistant", Content: content, ToolCalls: ch.Message.ToolCalls})
		for _, tc := range ch.Message.ToolCalls {
			input := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(input) {
				input = json.RawMessage(`{}`)
			}
			tr, err := exec(ctx, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: input})
			content := string(tr.Value)
			if err != nil {
				content = fmt.Sprintf(`{"error": %q}`, err.Error())
			} else if tr.IsError {
				content = fmt.Sprintf(`{"error": %s}`, nonEmpty(tr.Value))
			}
			msgs = append(msgs, oaMessage{Role: "tool", ToolCallID: tc.ID, Content: content})
		}
	}
}

func nonEmpty(v json.RawMessage) string {
	if len(v) == 0 {
		return "null"
	}
	return string(v)
}

// ── embeddings and rerank

// EmbedModel describes one embeddings model (from /v1/embeddings/models).
type EmbedOptions struct {
	Model      string
	Dimensions int // only for models that support it
	BatchSize  int // default 64
}

// Embed embeds texts in batches; vectors are returned in input order.
func (c *Client) Embed(ctx context.Context, texts []string, o EmbedOptions) ([][]float32, error) {
	if o.Model == "" {
		o.Model = "emb-granite"
	}
	if o.BatchSize <= 0 {
		o.BatchSize = 64
	}
	out := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += o.BatchSize {
		end := min(len(texts), start+o.BatchSize)
		batch := make([]string, 0, end-start)
		for _, t := range texts[start:end] {
			if strings.TrimSpace(t) == "" {
				t = " " // the API rejects empty strings
			}
			batch = append(batch, t)
		}
		body := map[string]any{"model": o.Model, "input": batch}
		if o.Dimensions > 0 {
			body["dimensions"] = o.Dimensions
		}
		var r struct {
			Data []struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		if err := c.post(ctx, "/v1/embeddings", body, &r); err != nil {
			return nil, err
		}
		for _, d := range r.Data {
			if d.Index < 0 || start+d.Index >= end {
				return nil, fmt.Errorf("embeddings: index %d out of batch range", d.Index)
			}
			out[start+d.Index] = d.Embedding
		}
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("embeddings: no vector returned for input %d", i)
		}
	}
	return out, nil
}

// RerankResult is one reranked document.
type RerankResult struct {
	Index int     `json:"index"`
	Score float64 `json:"relevance_score"`
}

// Rerank scores documents against the query (Cohere v2 shape), best first.
func (c *Client) Rerank(ctx context.Context, model, query string, docs []string, topN int) ([]RerankResult, error) {
	if model == "" {
		model = "bge-rr-v2-m3"
	}
	if len(docs) == 0 {
		return []RerankResult{}, nil
	}
	if topN <= 0 || topN > len(docs) {
		topN = len(docs)
	}
	var r struct {
		Results []RerankResult `json:"results"`
	}
	if err := c.post(ctx, "/v1/rerank", map[string]any{"model": model, "query": query, "documents": docs, "top_n": topN}, &r); err != nil {
		return nil, err
	}
	return r.Results, nil
}
