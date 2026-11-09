package gepp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-go-golems/geppetto/pkg/steps/ai/providers/pi"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
	"github.com/go-go-golems/rag-workbench/pkg/llm/gepp/gepptest"
)

type provider = gepptest.Provider

var (
	chunk    = gepptest.Chunk
	usage    = gepptest.Usage
	sse      = gepptest.SSE
	text     = gepptest.Text
	toolCall = gepptest.ToolCall
	status   = gepptest.Status
)

// setup makes a backend over gepptest's agent directory, whose providers
// point at a scripted server.
func setup(t *testing.T, cfg Config, script ...gepptest.Reply) (*Backend, *provider) {
	t.Helper()
	p := gepptest.NewProvider(t, script...)
	cfg.AgentDir = gepptest.AgentDir(t, p.URL)
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
	if mt := p.Body(0)["max_completion_tokens"]; mt != float64(8192) {
		t.Fatalf("default tier budget not sent: %v", p.Body(0))
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
	if _, ok := p.Body(0)["tools"]; !ok {
		t.Fatal("round 1 offered no tools")
	}
	msgs, _ := json.Marshal(p.Body(1)["messages"])
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
	if _, ok := p.Body(1)["tools"]; ok {
		t.Fatal("the last round offered tools")
	}
	msgs, _ := json.Marshal(p.Body(1)["messages"])
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

func TestPreferredFallbackWithoutEnabledList(t *testing.T) {
	b, _ := setup(t, Config{})
	b.dir.Settings.EnabledModels = nil
	b.dir.Settings.DefaultProvider = "missing"
	b.dir.Settings.DefaultModel = "missing"
	eligible := b.eligible()
	if len(eligible) < 2 {
		t.Fatal("fixture needs multiple authenticated models")
	}
	want := ""
	for ref := range eligible {
		if want == "" || ref < want {
			want = ref
		}
	}
	for i := 0; i < 30; i++ {
		model, ok := b.preferred(eligible)
		if !ok || model.Ref() != want {
			t.Fatalf("fallback=%s %v, want %s", model.Ref(), ok, want)
		}
	}
}
