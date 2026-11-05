// Package gepptest provides a scripted OpenAI Chat Completions server and a
// temporary Pi agent directory pointing at it, for tests of the Geppetto
// backend and of the code paths that stream its text (RAGWB-006).
package gepptest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Reply answers one provider request.
type Reply func(w http.ResponseWriter, r *http.Request)

// Provider is a scripted server: request i gets Script[i]; bodies are kept.
type Provider struct {
	T      testing.TB
	URL    string
	mu     sync.Mutex
	script []Reply
	bodies []map[string]any
}

// NewProvider starts a scripted server (closed at test cleanup).
func NewProvider(t testing.TB, script ...Reply) *Provider {
	p := &Provider{T: t, script: script}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	p.URL = srv.URL
	return p
}

func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	p.mu.Lock()
	p.bodies = append(p.bodies, body)
	i := len(p.bodies) - 1
	p.mu.Unlock()
	if i >= len(p.script) {
		p.T.Errorf("gepptest: unexpected request %d", i)
		http.Error(w, "no script", http.StatusInternalServerError)
		return
	}
	p.script[i](w, r)
}

// Body returns request i's JSON body (nil if there was none).
func (p *Provider) Body(i int) map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.bodies) {
		return nil
	}
	return p.bodies[i]
}

// Requests counts the requests served.
func (p *Provider) Requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bodies)
}

// Chunk is one streamed completion chunk.
func Chunk(delta map[string]any, finish string) string {
	c := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		c["finish_reason"] = finish
	}
	b, _ := json.Marshal(map[string]any{"id": "c", "model": "m", "choices": []any{c}})
	return string(b)
}

// Usage is a final usage chunk.
func Usage(in, out int) string {
	b, _ := json.Marshal(map[string]any{"id": "c", "model": "m", "choices": []any{}, "usage": map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out}})
	return string(b)
}

// SSE writes chunks as a server-sent event stream, flushing each.
func SSE(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range chunks {
		_, _ = io.WriteString(w, "data: "+c+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// Text answers with the given content pieces, then stops.
func Text(parts ...string) Reply {
	return func(w http.ResponseWriter, _ *http.Request) {
		var cs []string
		for _, p := range parts {
			cs = append(cs, Chunk(map[string]any{"content": p}, ""))
		}
		cs = append(cs, Chunk(map[string]any{}, "stop"), Usage(10, 5))
		SSE(w, cs...)
	}
}

// ToolCall asks for one tool call.
func ToolCall(id, name, args string) Reply {
	return func(w http.ResponseWriter, _ *http.Request) {
		SSE(w,
			Chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, ""),
			Chunk(map[string]any{}, "tool_calls"), Usage(10, 7))
	}
}

// Status fails with an HTTP status and body.
func Status(code int, body string) Reply {
	return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, body, code) }
}

// AgentDir writes a Pi agent directory whose providers "lunaroute" (models
// big, small) and "other" (model small) point at baseURL with a test key.
// Settings: default lunaroute/big, thinking medium.
func AgentDir(t testing.TB, baseURL string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("settings.json", map[string]any{"defaultProvider": "lunaroute", "defaultModel": "big", "defaultThinkingLevel": "medium", "enabledModels": []string{"lunaroute/big"}})
	model := func(id string) map[string]any {
		return map[string]any{"id": id, "reasoning": true, "input": []string{"text"}, "maxTokens": 100000}
	}
	write("models.json", map[string]any{"providers": map[string]any{
		"lunaroute": map[string]any{"baseUrl": baseURL + "/v1", "api": "openai-completions", "apiKey": "test-key", "models": []any{model("big"), model("small")}},
		"other":     map[string]any{"baseUrl": baseURL + "/v1", "api": "openai-completions", "apiKey": "test-key", "models": []any{model("small")}},
	}})
	return dir
}
