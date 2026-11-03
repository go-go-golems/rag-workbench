package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// The page-facing sample protocol (modelled on slop-computer pkg/server/sample.go):
//
//	POST /_rt/sample                      {input, modelTier, model, json, tools[{name, description, inputSchema}]}
//	  → SSE: call {id} · text {text, delta} · tool {use, name, input} · result {text, truncated, modelTierApplied, json?} · error {code, message, text?}
//	POST /_rt/sample/{call}/tools/{use}   {value, isError}   page tool result
//	POST /_rt/sample/{call}/cancel
//
// The LLM loop runs here; tools run in the page (agent.js's eval tool).

// ToolTimeout bounds how long a parked tool call waits for the page.
var ToolTimeout = 3 * time.Minute

type sampleCall struct {
	id     string
	cancel context.CancelFunc
	mu     sync.Mutex
	parked map[string]chan llm.ToolResult
}

type calls struct {
	mu sync.Mutex
	m  map[string]*sampleCall
}

func (c *calls) add(cancel context.CancelFunc) *sampleCall {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	sc := &sampleCall{id: hex.EncodeToString(b), cancel: cancel, parked: map[string]chan llm.ToolResult{}}
	c.mu.Lock()
	c.m[sc.id] = sc
	c.mu.Unlock()
	return sc
}

func (c *calls) get(id string) *sampleCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[id]
}

func (c *calls) remove(id string) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

type sampleBody struct {
	Input     json.RawMessage `json:"input"`
	ModelTier string          `json:"modelTier"`
	Model     string          `json:"model"`
	JSON      bool            `json:"json"`
	Tools     []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
}

func turnsFrom(raw json.RawMessage) ([]llm.Turn, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []llm.Turn{{Role: "user", Content: s}}, nil
	}
	var ts []llm.Turn
	if err := json.Unmarshal(raw, &ts); err != nil {
		return nil, fmt.Errorf("input must be a string or an array of {role, content} turns")
	}
	return ts, nil
}

func (s *Server) handleSample(w http.ResponseWriter, r *http.Request) {
	if s.LLM == nil {
		writeErr(w, http.StatusServiceUnavailable, "sampling_disabled", "start ragwb workbench with --llm lunaroute (or fake)")
		return
	}
	var body sampleBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	turns, err := turnsFrom(body.Input)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req := llm.Request{Turns: turns, Tier: body.ModelTier, Model: body.Model, JSON: body.JSON}
	for _, t := range body.Tools {
		schema := t.InputSchema
		if string(schema) == "null" {
			schema = nil
		}
		req.Tools = append(req.Tools, llm.Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "upstream_error", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	call := s.calls.add(cancel)
	defer s.calls.remove(call.id)
	var wmu sync.Mutex
	emit := func(event string, v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		defer wmu.Unlock()
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}
	emit("call", map[string]string{"id": call.id})
	onText := func(text, delta string) { emit("text", map[string]string{"text": text, "delta": delta}) }
	exec := func(ctx context.Context, tc llm.ToolCall) (llm.ToolResult, error) {
		ch := make(chan llm.ToolResult, 1)
		call.mu.Lock()
		call.parked[tc.ID] = ch
		call.mu.Unlock()
		defer func() { call.mu.Lock(); delete(call.parked, tc.ID); call.mu.Unlock() }()
		var input any
		_ = json.Unmarshal(tc.Input, &input)
		emit("tool", map[string]any{"use": tc.ID, "name": tc.Name, "input": input})
		t := time.NewTimer(ToolTimeout)
		defer t.Stop()
		select {
		case res := <-ch:
			return res, nil
		case <-t.C:
			return llm.ToolResult{Value: json.RawMessage(`"The page tool did not answer in time"`), IsError: true}, nil
		case <-ctx.Done():
			return llm.ToolResult{}, ctx.Err()
		}
	}
	res, err := s.LLM.Sample(ctx, req, onText, exec)
	if err != nil {
		f, ok := err.(*llm.Failure)
		if !ok {
			code := "upstream_error"
			if ctx.Err() != nil {
				code = "cancelled"
			}
			f = &llm.Failure{Code: code, Message: err.Error()}
		}
		emit("error", f)
		return
	}
	out := map[string]any{"text": res.Text, "truncated": res.Truncated, "modelTierApplied": res.TierApplied}
	if body.JSON {
		raw, ok := llm.ParseJSON(res.Text)
		if !ok || res.Truncated {
			emit("error", &llm.Failure{Code: "invalid_json", Message: llm.InvalidJSONMessage(res), Text: res.Text})
			return
		}
		out["json"] = raw
	}
	emit("result", out)
}

func (s *Server) handleSampleTool(w http.ResponseWriter, r *http.Request) {
	call := s.calls.get(r.PathValue("call"))
	if call == nil {
		writeErr(w, http.StatusNotFound, "invalid_request", "no such call")
		return
	}
	var in struct {
		Value   json.RawMessage `json:"value"`
		IsError bool            `json:"isError"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(in.Value) == 0 {
		in.Value = json.RawMessage("null")
	}
	call.mu.Lock()
	ch := call.parked[r.PathValue("use")]
	call.mu.Unlock()
	if ch == nil {
		writeErr(w, http.StatusNotFound, "invalid_request", "no pending tool use with that id")
		return
	}
	ch <- llm.ToolResult{Value: in.Value, IsError: in.IsError}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleSampleCancel(w http.ResponseWriter, r *http.Request) {
	if call := s.calls.get(r.PathValue("call")); call != nil {
		call.cancel()
	}
	writeJSON(w, map[string]bool{"ok": true})
}
