package gepp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-go-golems/geppetto/pkg/steps/ai/providers/pi"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// provider is a scripted OpenAI Chat Completions server: request i gets
// script[i]; request bodies are kept for assertions.
type provider struct {
	t      *testing.T
	mu     sync.Mutex
	script []func(w http.ResponseWriter, r *http.Request)
	bodies []map[string]any
}

func (p *provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	p.mu.Lock()
	p.bodies = append(p.bodies, body)
	i := len(p.bodies) - 1
	p.mu.Unlock()
	if i >= len(p.script) {
		p.t.Errorf("unexpected request %d", i)
		http.Error(w, "no script", 500)
		return
	}
	p.script[i](w, r)
}

func (p *provider) body(i int) map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.bodies) {
		return nil
	}
	return p.bodies[i]
}

func chunk(delta map[string]any, finish string) string {
	c := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		c["finish_reason"] = finish
	}
	b, _ := json.Marshal(map[string]any{"id": "c", "model": "m", "choices": []any{c}})
	return string(b)
}

func usage(in, out int) string {
	b, _ := json.Marshal(map[string]any{"id": "c", "model": "m", "choices": []any{}, "usage": map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out}})
	return string(b)
}

func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range chunks {
		_, _ = io.WriteString(w, "data: "+c+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// text answers with the given content pieces, then stop.
func text(parts ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		var cs []string
		for _, p := range parts {
			cs = append(cs, chunk(map[string]any{"content": p}, ""))
		}
		cs = append(cs, chunk(map[string]any{}, "stop"), usage(10, 5))
		sse(w, cs...)
	}
}

// toolCall asks for one tool call: {id, name, arguments JSON}.
func toolCall(id, name, args string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		sse(w,
			chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, ""),
			chunk(map[string]any{}, "tool_calls"), usage(10, 7))
	}
}

func status(code int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, body, code) }
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// setup makes an agent directory whose lunaroute provider is the scripted
// server (models "big" and "small"; "small" also exists under provider "other").
func setup(t *testing.T, cfg Config, script ...func(http.ResponseWriter, *http.Request)) (*Backend, *provider) {
	t.Helper()
	p := &provider{t: t, script: script}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "settings.json"), map[string]any{"defaultProvider": "lunaroute", "defaultModel": "big", "defaultThinkingLevel": "medium", "enabledModels": []string{"lunaroute/big"}})
	model := func(id string) map[string]any {
		return map[string]any{"id": id, "reasoning": true, "input": []string{"text"}, "maxTokens": 100000}
	}
	writeJSON(t, filepath.Join(dir, "models.json"), map[string]any{"providers": map[string]any{
		"lunaroute": map[string]any{"baseUrl": srv.URL + "/v1", "api": "openai-completions", "apiKey": "test-key", "models": []any{model("big"), model("small")}},
		"other":     map[string]any{"baseUrl": srv.URL + "/v1", "api": "openai-completions", "apiKey": "test-key", "models": []any{model("small")}},
	}})
	cfg.AgentDir = dir
	cfg.Options = append(cfg.Options, pi.WithLocalProviders())
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}

func user(s string) []llm.Turn { return []llm.Turn{{Role: "user", Content: s}} }

type textLog struct {
	mu     sync.Mutex
	texts  []string
	deltas []string
}

func (l *textLog) on(text, delta string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.texts = append(l.texts, text)
	l.deltas = append(l.deltas, delta)
}

// Text deltas reach onText one by one, with the whole text so far.
func TestSampleStreamsDeltas(t *testing.T) {
	b, p := setup(t, Config{Tiers: llm.TierModels{Default: "lunaroute/big"}}, text("Hello", ", ", "world."))
	var log textLog
	res, err := b.Sample(context.Background(), llm.Request{Turns: user("hi")}, log.on, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello, world." || res.TierApplied != "default" || res.ModelApplied != "lunaroute/big" || res.Rounds != 1 {
		t.Fatalf("result %+v", res)
	}
	if strings.Join(log.deltas, "|") != "Hello|, |world." || log.texts[2] != "Hello, world." {
		t.Fatalf("onText calls: texts %q deltas %q", log.texts, log.deltas)
	}
	if res.Usage.PromptTokens != 10 || res.Usage.CompletionTokens != 5 {
		t.Fatalf("usage %+v", res.Usage)
	}
	if mt := p.body(0)["max_completion_tokens"]; mt != float64(8192) {
		t.Fatalf("default tier budget not sent: %v", p.body(0))
	}
}

// A tool call runs through exec; its result goes back to the model; rounds
// are joined with a blank line.
func TestSampleToolLoop(t *testing.T) {
	b, p := setup(t, Config{Tiers: llm.TierModels{Default: "lunaroute/big"}},
		func(w http.ResponseWriter, _ *http.Request) {
			sse(w, chunk(map[string]any{"content": "Let me look."}, ""),
				chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "search", "arguments": `{"query":"rebalance"}`}}}}, ""),
				chunk(map[string]any{}, "tool_calls"), usage(10, 7))
		},
		text("Found it."))
	var got llm.ToolCall
	exec := func(_ context.Context, c llm.ToolCall) (llm.ToolResult, error) {
		got = c
		return llm.ToolResult{Value: json.RawMessage(`"3 hits"`)}, nil
	}
	var log textLog
	res, err := b.Sample(context.Background(), llm.Request{Turns: user("find"), Tools: []llm.Tool{{Name: "search", Description: "search", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)}}}, log.on, exec)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "call_1" || got.Name != "search" || string(got.Input) != `{"query":"rebalance"}` {
		t.Fatalf("tool call %+v", got)
	}
	if res.Text != "Let me look.\n\nFound it." || res.Rounds != 2 {
		t.Fatalf("result %q rounds %d", res.Text, res.Rounds)
	}
	if _, ok := p.body(0)["tools"]; !ok {
		t.Fatal("round 1 offered no tools")
	}
	msgs, _ := json.Marshal(p.body(1)["messages"])
	if !strings.Contains(string(msgs), "3 hits") || !strings.Contains(string(msgs), "call_1") {
		t.Fatalf("round 2 lacks the tool result: %s", msgs)
	}
}

// The last round offers no tools and is told to answer.
func TestSampleForcesTheLastRound(t *testing.T) {
	b, p := setup(t, Config{Tiers: llm.TierModels{Default: "lunaroute/big"}, MaxRounds: 2},
		toolCall("c1", "search", `{}`), text("Answer."))
	exec := func(context.Context, llm.ToolCall) (llm.ToolResult, error) {
		return llm.ToolResult{Value: json.RawMessage(`"x"`)}, nil
	}
	res, err := b.Sample(context.Background(), llm.Request{Turns: user("q"), Tools: []llm.Tool{{Name: "search"}}}, nil, exec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Answer." {
		t.Fatalf("text %q", res.Text)
	}
	if _, ok := p.body(1)["tools"]; ok {
		t.Fatal("the last round offered tools")
	}
	msgs, _ := json.Marshal(p.body(1)["messages"])
	if !strings.Contains(string(msgs), "tool budget for this answer is used up") {
		t.Fatalf("no forcing message: %s", msgs)
	}
}

// Tier references: provider/id, a unique bare id, a :level suffix, and errors.
func TestTierLookup(t *testing.T) {
	b, _ := setup(t, Config{Tiers: llm.TierModels{Quick: "big", Default: "lunaroute/small:high", Complex: ""}})
	if got := b.Describe(); got != "pi (quick=lunaroute/big default=lunaroute/small:high complex=lunaroute/big)" {
		t.Fatalf("describe: %s", got)
	}
	for _, bad := range []string{"small", "lunaroute/nope", "nope"} {
		_, err := lookup(bad, b.eligible())
		if err == nil {
			t.Fatalf("%q resolved", bad)
		}
	}
	if _, err := lookup("small", b.eligible()); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous id: %v", err)
	}
}

// Thinking: quick is off unless the reference names a level; other tiers use settings' default.
func TestThinkingLevels(t *testing.T) {
	b, _ := setup(t, Config{Tiers: llm.TierModels{Quick: "lunaroute/big", Default: "lunaroute/big", Complex: "lunaroute/big:high"}})
	for tier, want := range map[string]string{"quick": "off", "default": "medium", "complex": "high"} {
		c, tr := b.pick(llm.Request{Tier: tier})
		if got := b.level(c, tr); got != want {
			t.Fatalf("%s: level %q, want %q", tier, got, want)
		}
	}
	if c, _ := b.pick(llm.Request{Tier: "quick", Model: "lunaroute/small"}); c.model.ID != "small" {
		t.Fatalf("explicit model ignored: %s", c.model.Ref())
	}
	if c, _ := b.pick(llm.Request{Tier: "quick", Model: "lunaroute/missing"}); c.model.ID != "big" {
		t.Fatalf("unavailable explicit model did not fall back to the tier: %s", c.model.Ref())
	}
}

// Provider errors become sample failure codes; keys never appear in messages.
func TestSampleFailures(t *testing.T) {
	cases := []struct {
		reply func(http.ResponseWriter, *http.Request)
		code  string
	}{
		{status(401, `{"error":{"message":"invalid api key sk-abcdefghijklmnop"}}`), "not_granted"},
		{status(429, `{"error":{"message":"rate limit"}}`), "rate_limited"},
		{text(""), "empty_completion"},
	}
	for _, c := range cases {
		b, _ := setup(t, Config{Tiers: llm.TierModels{Default: "lunaroute/big"}}, c.reply)
		_, err := b.Sample(context.Background(), llm.Request{Turns: user("q")}, nil, nil)
		var f *llm.Failure
		if !errors.As(err, &f) || f.Code != c.code {
			t.Fatalf("want %s, got %v", c.code, err)
		}
		if strings.Contains(f.Message, "sk-abcdefghijklmnop") || strings.Contains(f.Message, "test-key") {
			t.Fatalf("secret in message: %s", f.Message)
		}
	}
}

// Cancelling the context stops a slow stream promptly.
func TestSampleCancel(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	b, _ := setup(t, Config{Tiers: llm.TierModels{Default: "lunaroute/big"}}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+chunk(map[string]any{"content": "partial"}, "")+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, err := b.Sample(ctx, llm.Request{Turns: user("q")}, func(string, string) { first <- struct{}{} }, nil)
		done <- err
	}()
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("no delta before cancel")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Sample did not return after cancel")
	}
}

// Tool-call markup leaked as text is withdrawn after streaming.
func TestSampleWithdrawsLeakedMarkup(t *testing.T) {
	b, _ := setup(t, Config{Tiers: llm.TierModels{Default: "lunaroute/big"}}, text("The answer.", " <tool_call>search(", "x)</tool_call>"))
	var log textLog
	res, err := b.Sample(context.Background(), llm.Request{Turns: user("q")}, log.on, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "The answer." {
		t.Fatalf("text %q", res.Text)
	}
	if last := log.texts[len(log.texts)-1]; last != "The answer." || log.deltas[len(log.deltas)-1] != "" {
		t.Fatalf("no correction: %q", log.texts)
	}
}
