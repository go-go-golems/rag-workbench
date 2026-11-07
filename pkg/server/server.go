// Package server serves the RAG workbench page with a local window.claude
// runtime (sample, db, user, downloads), presets from a directory and a
// corpus built from a directory of markdown files.
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-go-golems/geppetto/pkg/decision"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
	"github.com/go-go-golems/rag-workbench/pkg/store"
	"github.com/go-go-golems/rag-workbench/web"
)

// Config for the workbench server.
type Config struct {
	LLM        llm.Backend       // nil disables sample (the page shows its own message)
	Decider    decision.Provider // nil disables /_rt/decide (decision nodes fail with decide_unavailable)
	Docs       *DocStore
	PresetsDir string      // presets/index.json + *.js
	CorpusDir  string      // *.md served as corpus.json {files:[{name,text}]}
	Apps       *store.Apps // saved scripts (nil disables /api/apps)
}

type Server struct {
	Config
	calls calls
}

func New(cfg Config) *Server { return &Server{Config: cfg, calls: calls{m: map[string]*sampleCall{}}} }

// Handler returns the ServeMux (Go 1.22 method + wildcard patterns).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /index.html", s.handleIndex)
	for _, f := range []string{"rag.js", "gui.js", "kit.js"} {
		mux.HandleFunc("GET /"+f, s.handleEngineAsset(f))
	}
	for _, f := range []string{"app.js", "agent.js"} {
		mux.HandleFunc("GET /"+f, s.handlePageAsset(f))
	}
	mux.HandleFunc("GET /_rt/claude.js", s.handlePageAsset("claude.js"))
	mux.HandleFunc("GET /corpus.json", s.handleCorpus)
	mux.HandleFunc("GET /presets/{file}", s.handlePreset)
	mux.HandleFunc("POST /_rt/sample", s.handleSample)
	mux.HandleFunc("POST /_rt/sample/{call}/tools/{use}", s.handleSampleTool)
	mux.HandleFunc("POST /_rt/sample/{call}/cancel", s.handleSampleCancel)
	mux.HandleFunc("POST /_rt/decide", s.handleDecide)
	mux.HandleFunc("POST /_rt/db/{op}", s.handleDB)
	s.appsRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "llm": s.LLM != nil, "decide": s.Decider != nil, "apps": s.Apps != nil})
	})
	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	b, err := web.Workbench.ReadFile("workbench/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The runtime shim must exist before app.js boots (it calls window.claude.use).
	b = bytes.Replace(b, []byte("<head>"), []byte(`<head><script src="/_rt/claude.js"></script>`), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) handleEngineAsset(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := ragjs.Source(name) // the same engine code the Go runtime runs
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(b)
	}
}

func (s *Server) handlePageAsset(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := web.Workbench.ReadFile("workbench/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(b)
	}
}

func (s *Server) handleCorpus(w http.ResponseWriter, r *http.Request) {
	type file struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	files := []file{}
	if s.CorpusDir != "" {
		entries, err := os.ReadDir(s.CorpusDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if e.IsDir() || (ext != ".md" && ext != ".markdown" && ext != ".txt") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(s.CorpusDir, e.Name()))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			files = append(files, file{e.Name(), string(b)})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	}
	writeJSON(w, map[string]any{"files": files})
}

func (s *Server) handlePreset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if s.PresetsDir == "" || name != filepath.Base(name) || (!strings.HasSuffix(name, ".js") && name != "index.json") {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(filepath.Join(s.PresetsDir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".json") {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	_, _ = w.Write(b)
}

func (s *Server) handleDB(w http.ResponseWriter, r *http.Request) {
	if s.Docs == nil {
		writeErr(w, http.StatusServiceUnavailable, "not_granted", "no document store")
		return
	}
	var in struct {
		Path string          `json:"path"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxDocBytes+4096)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var (
		out any
		err error
	)
	switch r.PathValue("op") {
	case "get":
		var d json.RawMessage
		d, err = s.Docs.Get(in.Path)
		if d == nil {
			d = json.RawMessage("null")
		}
		out = map[string]any{"data": d}
	case "set":
		err = s.Docs.Set(in.Path, in.Data)
		out = map[string]bool{"ok": true}
	case "delete":
		err = s.Docs.Delete(in.Path)
		out = map[string]bool{"ok": true}
	case "list":
		var docs []DocEntry
		docs, err = s.Docs.List(in.Path)
		out = map[string]any{"docs": docs}
	default:
		writeErr(w, http.StatusNotFound, "invalid_request", "unknown db op")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
