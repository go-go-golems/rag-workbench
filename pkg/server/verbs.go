package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-go-golems/glazed/pkg/cmds/fields"
	"github.com/go-go-golems/glazed/pkg/cmds/values"
	"github.com/go-go-golems/go-go-goja/pkg/jsverbs"

	"github.com/go-go-golems/rag-workbench/pkg/runner"
)

// Verb endpoints (RAGWB-004 §6.2):
//
//	GET  /api/rag/{app}/verbs              → [{path, url, short, long, tags, fields}]
//	POST /api/rag/{app}/verbs/{verb...}    JSON params → envelope, or SSE (Accept: text/event-stream | ?stream=sse)
//	GET  /api/rag/{app}/verbs/{verb...}    params as query strings → same
//
// Query options: chunks=1 (full chunks in hits events and the envelope),
// events=a,b (event allow-list), top=N (hits per hits event).

// controlParams are query keys that configure the response, not the verb.
var controlParams = map[string]bool{"stream": true, "chunks": true, "events": true, "top": true}

// verbURLPath is a verb's path below /verbs/: its full path with spaces as slashes.
func verbURLPath(v *jsverbs.VerbSpec) string { return strings.ReplaceAll(v.FullPath(), " ", "/") }

func exposed(v *jsverbs.VerbSpec) bool {
	for _, t := range v.Tags {
		if t == "internal" {
			return false
		}
	}
	return true
}

func (s *RagServer) handleVerbs(w http.ResponseWriter, r *http.Request, a *RagApp) {
	out := []map[string]any{}
	for _, v := range a.Verbs() {
		if !exposed(v) {
			continue
		}
		fs, err := runner.DescribeVerb(a.Registry(), v)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "verb_schema", err.Error())
			return
		}
		out = append(out, map[string]any{
			"path": v.FullPath(), "url": "/api/rag/" + a.Name + "/verbs/" + verbURLPath(v),
			"short": v.Short, "long": v.Long, "tags": nonNil(v.Tags), "fields": fs,
		})
	}
	writeJSON(w, out)
}

// findVerb resolves a URL path: the verb's full path (app/answer, reports/search),
// or just its name when no other exposed verb of the app has that name.
func (s *RagServer) findVerb(a *RagApp, p string) *jsverbs.VerbSpec {
	p = strings.Trim(p, "/")
	var byName []*jsverbs.VerbSpec
	for _, v := range a.Verbs() {
		if !exposed(v) {
			continue
		}
		if verbURLPath(v) == p {
			return v
		}
		if v.Name == p {
			byName = append(byName, v)
		}
	}
	if len(byName) == 1 {
		return byName[0]
	}
	return nil
}

// requestParams: a POST's JSON object plus query-string values (lists of strings).
func requestParams(r *http.Request) (map[string]any, error) {
	params := map[string]any{}
	if r.Method == http.MethodPost {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			if err := json.Unmarshal(b, &params); err != nil {
				return nil, fmt.Errorf("the body must be a JSON object of parameters: %w", err)
			}
		}
	}
	for k, vs := range r.URL.Query() {
		if controlParams[k] {
			continue
		}
		if _, set := params[k]; !set {
			params[k] = vs
		}
	}
	return params, nil
}

func detailOf(r *http.Request) runner.Detail {
	q := r.URL.Query()
	d := runner.Detail{Chunks: q.Get("chunks") == "1" || q.Get("chunks") == "true"}
	if n, err := strconv.Atoi(q.Get("top")); err == nil && n > 0 {
		d.Top = n
	}
	return d
}

// paramsOf are the parsed values by section ("default" flattened), for the envelope.
func paramsOf(vals *values.Values) map[string]any {
	out := map[string]any{}
	vals.ForEach(func(slug string, sv *values.SectionValues) {
		m := out
		if slug != "default" {
			m = map[string]any{}
			out[slug] = m
		}
		sv.Fields.ForEach(func(_ string, fv *fields.FieldValue) {
			if fv != nil && fv.Definition != nil {
				m[fv.Definition.Name] = fv.Value
			}
		})
	})
	return out
}

// runSummary collects, from the events, what the envelope reports about the
// verb's rag runs (and the chunks its hits events carried, with chunks=1).
type runSummary struct {
	mu     sync.Mutex
	runs   map[int]map[string]any
	chunks map[string]json.RawMessage
	events int
}

func newRunSummary() *runSummary {
	return &runSummary{runs: map[int]map[string]any{}, chunks: map[string]json.RawMessage{}}
}

func (s *runSummary) observe(e runner.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events++
	switch e.Type {
	case "run", "runEnd":
		var m map[string]any
		if json.Unmarshal(e.Data, &m) != nil {
			return
		}
		n := 0
		if f, ok := m["run"].(float64); ok {
			n = int(f)
		}
		r := s.runs[n]
		if r == nil {
			r = map[string]any{"run": n}
			s.runs[n] = r
		}
		for _, k := range []string{"plan", "trigger", "ms", "calls", "errors"} {
			if v, ok := m[k]; ok {
				r[k] = v
			}
		}
	case "hits":
		var h struct {
			Hits []struct {
				ID    string          `json:"id"`
				Chunk json.RawMessage `json:"chunk"`
			} `json:"hits"`
		}
		if json.Unmarshal(e.Data, &h) == nil {
			for _, x := range h.Hits {
				if len(x.Chunk) > 0 {
					s.chunks[x.ID] = x.Chunk
				}
			}
		}
	}
}

func (s *runSummary) list() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]int, 0, len(s.runs))
	for k := range s.runs {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]map[string]any, len(keys))
	for i, k := range keys {
		out[i] = s.runs[k]
	}
	return out
}

func (s *RagServer) handleVerb(w http.ResponseWriter, r *http.Request, a *RagApp) {
	t0 := time.Now()
	verb := s.findVerb(a, r.PathValue("verb"))
	if verb == nil {
		names := []string{}
		for _, v := range a.Verbs() {
			if exposed(v) {
				names = append(names, verbURLPath(v))
			}
		}
		writeErr(w, http.StatusNotFound, "unknown_verb", fmt.Sprintf("app %s has no verb %q (have: %s)", a.Name, r.PathValue("verb"), strings.Join(names, ", ")))
		return
	}
	params, err := requestParams(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	vals, err := runner.ParseVerbParams(a.Registry(), verb, params)
	if err != nil {
		var pe *runner.ParamError
		if errors.As(err, &pe) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "bad_params", "message": pe.Error(), "fields": pe.Fields}})
			return
		}
		writeErr(w, http.StatusInternalServerError, "verb_schema", err.Error())
		return
	}
	if !a.acquire() {
		writeErr(w, http.StatusServiceUnavailable, "busy", fmt.Sprintf("%s is running %d requests; try again", a.Name, cap(a.inflight)))
		return
	}
	defer a.release()
	detail := detailOf(r)
	sum := newRunSummary()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	envelope := func(res any) map[string]any {
		env := map[string]any{"verb": verb.FullPath(), "params": paramsOf(vals), "result": res, "runs": sum.list(), "ms": ms(time.Since(t0))}
		if detail.Chunks {
			env["chunks"] = sum.chunks
		}
		return env
	}
	status := "ok"
	defer func() { s.logVerb(a, verb, params, sum, status, t0) }()

	if !wantsSSE(r) {
		res, _, err := a.Invoke(ctx, verb, vals, runner.SinkFunc(sum.observe), detail)
		if err != nil {
			status = "error: " + err.Error()
			writeErr(w, http.StatusInternalServerError, "verb_failed", err.Error())
			return
		}
		b, err := json.Marshal(envelope(res))
		if err != nil {
			status = "error: " + err.Error()
			writeErr(w, http.StatusInternalServerError, "verb_failed", "result is not JSON-serializable: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}

	response, err := startResponseStream(w, r, cancel, t0, sum.observe)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "stream_unavailable", err.Error())
		return
	}
	start, _ := json.Marshal(map[string]any{"type": "start", "verb": verb.FullPath(), "params": paramsOf(vals)})
	response.sink.Emit(runner.Event{Type: "start", Data: start})
	res, _, err := a.Invoke(ctx, verb, vals, response.sink, detail)
	switch {
	case response.sink.isSlow():
		status = "slow client"
		b, _ := json.Marshal(map[string]any{"type": "error", "code": "slow_client", "message": "the client did not read the stream; the request was cancelled"})
		_ = response.finish("error", b)
	case err != nil && r.Context().Err() != nil:
		status = "cancelled by the client"
		_ = response.finish("", nil)
	case err != nil:
		status = "error: " + err.Error()
		b, _ := json.Marshal(map[string]any{"type": "error", "code": "verb_failed", "message": err.Error()})
		_ = response.finish("error", b)
	default:
		env := envelope(res)
		env["type"] = "result"
		b, err := json.Marshal(env)
		if err != nil {
			status = "error: " + err.Error()
			b, _ = json.Marshal(map[string]any{"type": "error", "code": "verb_failed", "message": "result is not JSON-serializable: " + err.Error()})
			_ = response.finish("error", b)
			return
		}
		_ = response.finish("result", b)
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func (s *RagServer) logVerb(a *RagApp, verb *jsverbs.VerbSpec, params map[string]any, sum *runSummary, status string, t0 time.Time) {
	if s.Log == nil {
		return
	}
	q := ""
	if v, ok := params["q"]; ok {
		if l, isList := v.([]string); isList && len(l) == 1 {
			v = l[0] // a query-string value
		}
		q = fmt.Sprintf(" q=%q", fmt.Sprint(v))
	}
	runs := sum.list()
	hint := ""
	if len(runs) == 0 && status == "ok" {
		hint = " (no rag runs streamed: call rag.run before the first await, or pass { scope: rag.scope() })"
	}
	fmt.Fprintf(s.Log, "%s verb %s%s · %d run(s) · %d event(s) · %.1f ms · %s%s\n", a.Name, verb.FullPath(), q, len(runs), sum.events, ms(time.Since(t0)), status, hint)
}
