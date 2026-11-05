package llm

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseJSONTolerant(t *testing.T) {
	cases := map[string]string{
		`{"a": 1}`:                           `{"a": 1}`,
		"Sure:\n```json\n[1, 2]\n```\nDone":  `[1, 2]`,
		`prefix {"k": [1, {"x": 2}]} suffix`: `{"k": [1, {"x": 2}]}`,
		"```\n{\"plain\": true}\n```":        `{"plain": true}`,
	}
	for in, want := range cases {
		got, ok := ParseJSON(in)
		if !ok || string(got) != want {
			t.Fatalf("ParseJSON(%q) = %s, %v; want %s", in, got, ok, want)
		}
	}
	if _, ok := ParseJSON("no json here"); ok {
		t.Fatal("expected failure")
	}
}

func TestNormalizeTurns(t *testing.T) {
	ts, err := NormalizeTurns([]Turn{{"user", "a"}, {"user", "b"}, {"assistant", "c"}, {"user", "d"}})
	if err != nil || len(ts) != 3 || ts[0].Content != "a\n\nb" {
		t.Fatalf("merge: %v %v", ts, err)
	}
	if _, err := NormalizeTurns([]Turn{{"assistant", "x"}}); err == nil {
		t.Fatal("must start with user")
	}
	if _, err := NormalizeTurns([]Turn{{"system", "x"}}); err == nil {
		t.Fatal("no system role")
	}
}

// Live checks against LunaRoute (opt-in: RAGWB_LIVE_LLM=1; key from env or Pi config).
func liveClient(t *testing.T) *Client {
	if os.Getenv("RAGWB_LIVE_LLM") == "" {
		t.Skip("set RAGWB_LIVE_LLM=1 to run live LunaRoute tests")
	}
	c, err := NewLunaRoute()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLiveSampleWithTool(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	called := 0
	res, err := c.Sample(ctx, Request{
		Turns: []Turn{{"user", "Call the lookup tool with key 'color', then reply with just the value it returns."}},
		Tier:  "default",
		Tools: []Tool{{Name: "lookup", Description: "Look up a value by key", InputSchema: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`)}},
	}, nil, func(ctx context.Context, tc ToolCall) (ToolResult, error) {
		called++
		return ToolResult{Value: json.RawMessage(`"vermilion"`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called == 0 || !strings.Contains(strings.ToLower(res.Text), "vermilion") {
		t.Fatalf("called=%d text=%q", called, res.Text)
	}
	t.Logf("rounds=%d usage=%+v text=%q", res.Rounds, res.Usage, res.Text)
}

func TestLiveEmbedAndRerank(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	vs, err := c.Embed(ctx, []string{"rebalance law", "the rebalance law preserves placements", "cats are mammals"}, EmbedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cos := func(a, b []float32) float64 {
		var d, na, nb float64
		for i := range a {
			d += float64(a[i]) * float64(b[i])
			na += float64(a[i]) * float64(a[i])
			nb += float64(b[i]) * float64(b[i])
		}
		return d / math.Sqrt(na*nb)
	}
	if len(vs[0]) != 768 || cos(vs[0], vs[1]) <= cos(vs[0], vs[2]) {
		t.Fatalf("dims=%d cos(rel)=%.3f cos(unrel)=%.3f", len(vs[0]), cos(vs[0], vs[1]), cos(vs[0], vs[2]))
	}
	rr, err := c.Rerank(ctx, "", "rebalance law", []string{"cats are mammals", "the rebalance law preserves placements"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rr[0].Index != 1 {
		t.Fatalf("rerank = %+v", rr)
	}
	t.Logf("cos(rel)=%.3f cos(unrel)=%.3f rerank=%+v", cos(vs[0], vs[1]), cos(vs[0], vs[2]), rr)
}

func TestStripToolMarkup(t *testing.T) {
	in := "path ranks are null here; let me check res.trace.<tool_call>eval<arg_key>code</arg_key><arg_value>const q = 1"
	if got := StripToolMarkup(in); got != "path ranks are null here; let me check res.trace." {
		t.Fatalf("got %q", got)
	}
	if got := StripToolMarkup("plain answer"); got != "plain answer" {
		t.Fatalf("got %q", got)
	}
}
