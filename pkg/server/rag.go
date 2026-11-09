package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
	"github.com/go-go-golems/rag-workbench/pkg/runner"
	"github.com/go-go-golems/rag-workbench/pkg/timing"
	"github.com/go-go-golems/rag-workbench/web"
)

// RagServer serves opened apps (`ragwb serve`, guide P7): each app's gui in an
// app shell whose host.rag calls the server-side session.
//
//	GET  /api/rag                       → [{name, id, title}]
//	GET  /api/rag/{app}                 → {id, title, ir, app, build, stats}
//	POST /api/rag/{app}/run             {signals, trigger} → {result, chunks, neighbors, packMs, timing}
//	                                      Accept: text/event-stream: the run's events (sse.go), then event "result"
//	GET  /api/rag/{app}/chunk?snapshotId=...&id= → {chunks, neighbors}   (a cache miss in the shell)
//	GET  /api/rag/{app}/doc?snapshotId=...&id= → doc metadata | null
//	GET  /api/rag/{app}/timings         → recorder entries since the app was opened
//	GET  /api/rag/{app}/verbs           → the app's verbs and their parameters (RAGWB-004, verbs.go)
//	POST /api/rag/{app}/verbs/{verb...} → run a verb: JSON, or SSE events (sse.go)
//	GET  /apps/{app}/                   → the app shell
type RagServer struct {
	apps  map[string]*RagApp
	order []string
	Log   io.Writer // one line per run (nil = quiet)
}

// RagApp is one served app and the recorder its runtime reports into.
type RagApp struct {
	*runner.ServedApp
	Timing *timing.Recorder
	// MaxInflight caps concurrent query and verb requests (0 = DefaultMaxInflight); more get 503 busy.
	MaxInflight int
	once        sync.Once
	inflight    chan struct{}
}

// DefaultMaxInflight is the verb request cap per app when MaxInflight is 0.
const DefaultMaxInflight = 8

func (a *RagApp) acquire() bool {
	a.once.Do(func() {
		n := a.MaxInflight
		if n <= 0 {
			n = DefaultMaxInflight
		}
		a.inflight = make(chan struct{}, n)
	})
	select {
	case a.inflight <- struct{}{}:
		return true
	default:
		return false
	}
}

func (a *RagApp) release() { <-a.inflight }

func NewRagServer(apps ...*RagApp) *RagServer {
	s := &RagServer{apps: map[string]*RagApp{}}
	for _, a := range apps {
		s.apps[a.Name] = a
		s.order = append(s.order, a.Name)
	}
	return s
}

// Routes adds the API and the shell pages to mux (engine assets included).
func (s *RagServer) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rag", s.handleList)
	mux.HandleFunc("GET /api/rag/{app}", s.withApp(s.handleInfo))
	mux.HandleFunc("POST /api/rag/{app}/run", s.withApp(s.handleRun))
	mux.HandleFunc("GET /api/rag/{app}/chunk", s.withApp(s.handleLookup("chunk")))
	mux.HandleFunc("GET /api/rag/{app}/doc", s.withApp(s.handleLookup("doc")))
	mux.HandleFunc("GET /api/rag/{app}/verbs", s.withApp(s.handleVerbs))
	mux.HandleFunc("POST /api/rag/{app}/verbs/{verb...}", s.withApp(s.handleVerb))
	mux.HandleFunc("GET /api/rag/{app}/verbs/{verb...}", s.withApp(s.handleVerb))
	mux.HandleFunc("GET /api/rag/{app}/timings", s.withApp(func(w http.ResponseWriter, r *http.Request, a *RagApp) {
		writeJSON(w, map[string]any{"entries": nonNil(a.Timing.Entries())})
	}))
	mux.HandleFunc("GET /apps/{app}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/apps/"+url.PathEscape(r.PathValue("app"))+"/", http.StatusFound)
	})
	mux.HandleFunc("GET /apps/{app}/{$}", s.withApp(func(w http.ResponseWriter, r *http.Request, a *RagApp) {
		b, err := web.AppShell.ReadFile("app-shell/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}))
	mux.HandleFunc("GET /app-shell.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := web.AppShell.ReadFile("app-shell/shell.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /gui.css", func(w http.ResponseWriter, r *http.Request) {
		b, err := web.GuiCSS()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(b)
	})
}

// Handler is the standalone `ragwb serve` mux: engine assets, the routes, and an
// index that lists the apps (or opens the only one).
func (s *RagServer) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, f := range []string{"rag.js", "gui.js", "kit.js"} {
		mux.HandleFunc("GET /"+f, engineAsset(f))
	}
	s.Routes(mux)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if len(s.order) == 1 {
			http.Redirect(w, r, "/apps/"+url.PathEscape(s.order[0])+"/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var b strings.Builder
		b.WriteString(`<!doctype html><meta charset="utf-8"><title>ragwb apps</title><link rel="stylesheet" href="/gui.css"><main style="max-width:640px;margin:32px auto"><h1 class="ctl-l">ragwb apps</h1><ul>`)
		for _, n := range s.order {
			fmt.Fprintf(&b, `<li><a href="/apps/%s/">%s</a></li>`, url.PathEscape(n), htmlEsc(n))
		}
		b.WriteString(`</ul></main>`)
		_, _ = io.WriteString(w, b.String())
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "apps": s.order})
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return mux
}

func engineAsset(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := ragjs.Source(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(b)
	}
}

func (s *RagServer) withApp(h func(http.ResponseWriter, *http.Request, *RagApp)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := s.apps[r.PathValue("app")]
		if a == nil {
			writeErr(w, http.StatusNotFound, "not_found", fmt.Sprintf("no app %q is served (have: %s)", r.PathValue("app"), strings.Join(s.order, ", ")))
			return
		}
		h(w, r, a)
	}
}

func (s *RagServer) handleList(w http.ResponseWriter, r *http.Request) {
	out := []map[string]any{}
	for _, n := range s.order {
		var info struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal(s.apps[n].Info, &info)
		out = append(out, map[string]any{"name": n, "id": info.ID, "title": info.Title})
	}
	writeJSON(w, out)
}

func (s *RagServer) handleInfo(w http.ResponseWriter, r *http.Request, a *RagApp) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(a.Info)
}

func (s *RagServer) handleLookup(kind string) func(http.ResponseWriter, *http.Request, *RagApp) {
	return func(w http.ResponseWriter, r *http.Request, a *RagApp) {
		id := r.URL.Query().Get("id")
		snapshotID := r.URL.Query().Get("snapshotId")
		if id == "" || snapshotID == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "id and snapshotId are required")
			return
		}
		var out string
		var err error
		if kind == "chunk" {
			out, err = a.Chunk(r.Context(), snapshotID, id)
		} else {
			out, err = a.Doc(r.Context(), snapshotID, id)
		}
		if err != nil {
			if errors.Is(err, runner.ErrStaleGeneration) {
				writeErr(w, http.StatusGone, "stale_generation", err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, "lookup_failed", err.Error())
			return
		}
		_ = writeOperationJSON(w, []byte(out))
	}
}

// runTiming is appended to every run response: where the request's time went.
type runTiming struct {
	EngineMS float64 `json:"engineMs"` // rag Session.run (live nodes)
	PackMS   float64 `json:"packMs"`   // collecting chunk bodies and neighbors
	EvalMS   float64 `json:"evalMs"`   // the whole runtime call (engine + pack + JSON.stringify + scheduling)
	TotalMS  float64 `json:"totalMs"`  // the handler, request decode to response ready
	Bytes    int     `json:"bytes"`    // response payload size
	Chunks   int     `json:"chunks"`   // chunk bodies sent
}

func (s *RagServer) handleRun(w http.ResponseWriter, r *http.Request, a *RagApp) {
	if !a.acquire() {
		writeErr(w, http.StatusServiceUnavailable, "busy", "request capacity exhausted")
		return
	}
	defer a.release()
	t0 := time.Now()
	var req struct {
		Signals json.RawMessage `json:"signals"`
		Trigger string          `json:"trigger"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(req.Signals) == 0 || string(req.Signals) == "null" {
		req.Signals = json.RawMessage("{}")
	}
	stream := wantsSSE(r)
	var response *responseStream
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var sinkArg runner.EventSink
	if stream {
		var err error
		response, err = startResponseStream(w, r, cancel, t0, nil)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "stream_unavailable", err.Error())
			return
		}
		sinkArg = response.sink
	}
	res, err := a.Run(ctx, req.Signals, req.Trigger, sinkArg)
	if err != nil {
		if stream {
			b, _ := json.Marshal(map[string]string{"type": "error", "code": "run_failed", "message": err.Error()})
			_ = response.finish("error", b)
			return
		}
		writeErr(w, http.StatusInternalServerError, "run_failed", err.Error())
		return
	}
	var head struct {
		Result struct {
			MS      float64          `json:"ms"`
			Signals map[string]any   `json:"signals"`
			Hits    []map[string]any `json:"hits"`
			Errors  []string         `json:"errors"`
			Trigger string           `json:"trigger"`
		} `json:"result"`
		Chunks map[string]json.RawMessage `json:"chunks"`
		PackMS float64                    `json:"packMs"`
	}
	_ = json.Unmarshal([]byte(res.JSON), &head)
	tm := runTiming{EngineMS: head.Result.MS, PackMS: head.PackMS, EvalMS: res.EvalMS, Chunks: len(head.Chunks), Bytes: len(res.JSON)}
	tm.TotalMS = float64(time.Since(t0).Microseconds()) / 1000
	tb, _ := json.Marshal(tm)
	body := strings.TrimSuffix(strings.TrimSpace(res.JSON), "}")
	out := body + `,"timing":` + string(tb) + "}"
	if stream {
		_ = response.finish("result", []byte(`{"type":"result",`+strings.TrimPrefix(out, "{")))
	} else {
		_ = writeOperationJSON(w, []byte(out))
	}
	if s.Log != nil {
		errs := ""
		if len(head.Result.Errors) > 0 {
			errs = " errors=" + strings.Join(head.Result.Errors, "; ")
		}
		fmt.Fprintf(s.Log, "%s run %s q=%q hits=%d · engine %.1f ms · pack %.1f ms · eval %.1f ms · total %.1f ms · %d chunks %.1f KB%s\n",
			a.Name, head.Result.Trigger, fmt.Sprint(head.Result.Signals["q"]), len(head.Result.Hits),
			tm.EngineMS, tm.PackMS, tm.EvalMS, tm.TotalMS, tm.Chunks, float64(tm.Bytes)/1024, errs)
	}
}

func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

func htmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
