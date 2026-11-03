package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-go-golems/rag-workbench/pkg/store"
)

// The app store API (guide §9.7):
//
//	GET    /api/apps                          {dir, apps:[{name, files, updated}]}
//	GET    /api/apps/{app}                    {app, dir, files:[{path, role, size, updated, version}]}
//	GET    /api/apps/{app}/files/{path...}    {app, path, role, source, version}   (?version=N for an old one)
//	PUT    /api/apps/{app}/files/{path...}    {source, comment} → {saved, check}
//	DELETE /api/apps/{app}/files/{path...}    {ok}
//	GET    /api/apps/{app}/history            {versions}  (?path= for one file)
//
// Files are written to the apps directory, where `ragwb verbs` and `ragwb run` find them.

func (s *Server) appsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/apps", s.withApps(s.handleAppsList))
	mux.HandleFunc("GET /api/apps/{app}", s.withApps(s.handleAppFiles))
	mux.HandleFunc("GET /api/apps/{app}/files/{path...}", s.withApps(s.handleAppRead))
	mux.HandleFunc("PUT /api/apps/{app}/files/{path...}", s.withApps(s.handleAppSave))
	mux.HandleFunc("DELETE /api/apps/{app}/files/{path...}", s.withApps(s.handleAppDelete))
	mux.HandleFunc("GET /api/apps/{app}/history", s.withApps(s.handleAppHistory))
}

func (s *Server) withApps(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Apps == nil {
			writeErr(w, http.StatusServiceUnavailable, "apps_disabled", "the app store is not configured (ragwb workbench --apps <dir>)")
			return
		}
		if app := r.PathValue("app"); app != "" {
			if err := store.CheckApp(app); err != nil {
				writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
		}
		if p := r.PathValue("path"); p != "" {
			if err := store.CheckPath(p); err != nil {
				writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
				return
			}
		}
		h(w, r)
	}
}

func storeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
}

func (s *Server) handleAppsList(w http.ResponseWriter, r *http.Request) {
	apps, err := s.Apps.ListApps()
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"dir": s.Apps.Dir, "apps": apps})
}

func (s *Server) handleAppFiles(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	files, err := s.Apps.Files(app)
	if err != nil {
		storeErr(w, err)
		return
	}
	dir, _ := s.Apps.AppDir(app)
	writeJSON(w, map[string]any{"app": app, "dir": dir, "files": files})
}

func (s *Server) handleAppRead(w http.ResponseWriter, r *http.Request) {
	app, p := r.PathValue("app"), r.PathValue("path")
	var (
		src     []byte
		err     error
		version int
	)
	if v := r.URL.Query().Get("version"); v != "" {
		if version, err = strconv.Atoi(v); err != nil || version < 1 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "version must be a positive integer")
			return
		}
		src, err = s.Apps.ReadVersion(app, p, version)
	} else {
		src, err = s.Apps.Read(app, p)
		if err == nil {
			if vs, herr := s.Apps.History(app, p); herr == nil && len(vs) > 0 {
				version = vs[0].Version
			}
		}
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"app": app, "path": p, "role": store.Role(p), "source": string(src), "version": version})
}

func (s *Server) handleAppSave(w http.ResponseWriter, r *http.Request) {
	app, p := r.PathValue("app"), r.PathValue("path")
	var body struct {
		Source  *string `json:"source"`
		Comment string  `json:"comment"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, store.MaxFileBytes+64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.Source == nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "body must be {source, comment?}")
		return
	}
	if len(*body.Source) > store.MaxFileBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "files are limited to 1 MiB")
		return
	}
	src := []byte(*body.Source)
	saved, err := s.Apps.Save(app, p, src, body.Comment)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"saved": saved, "check": s.Apps.Check(app, p, src)})
}

func (s *Server) handleAppDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Apps.Delete(r.PathValue("app"), r.PathValue("path"), r.URL.Query().Get("comment")); err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleAppHistory(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p != "" {
		if err := store.CheckPath(p); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	vs, err := s.Apps.History(r.PathValue("app"), p)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"versions": vs})
}
