package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/store"
)

func newAppsServer(t *testing.T) (*httptest.Server, *store.Apps) {
	t.Helper()
	d := t.TempDir()
	apps, err := store.OpenApps(filepath.Join(d, "apps"), filepath.Join(d, "ragwb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = apps.Close() })
	ts := httptest.NewServer(New(Config{Apps: apps}).Handler())
	t.Cleanup(ts.Close)
	return ts, apps
}

func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rd)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(r.Body).Decode(&out)
	return r.StatusCode, out
}

func TestAppsAPISaveListReadHistory(t *testing.T) {
	ts, apps := newAppsServer(t)
	base := ts.URL + "/api/apps"

	code, out := doJSON(t, "PUT", base+"/notes/files/search.js", map[string]any{
		"source":  "async function search(q) { return [{ q }]; }\n__verb__(\"search\", { fields: { q: { argument: true } } });\n",
		"comment": "first draft",
	})
	if code != 200 {
		t.Fatalf("PUT = %d %v", code, out)
	}
	saved := out["saved"].(map[string]any)
	check := out["check"].(map[string]any)
	if saved["version"].(float64) != 1 || check["ok"] != true || check["role"] != "verbs" {
		t.Fatalf("PUT result = %v", out)
	}
	if verbs := check["verbs"].([]any); len(verbs) != 1 || verbs[0].(map[string]any)["name"] != "search search" {
		t.Fatalf("verbs = %v", check["verbs"])
	}
	// the file is in the apps dir, where `ragwb verbs --dir` scans
	if b, err := os.ReadFile(filepath.Join(apps.Dir, "notes", "search.js")); err != nil || !bytes.Contains(b, []byte("__verb__")) {
		t.Fatalf("on disk: %q %v", b, err)
	}

	code, out = doJSON(t, "PUT", base+"/notes/files/search.js", map[string]any{"source": "async function search(q) { return []; }\n__verb__(\"search\", {});\n", "comment": "v2"})
	if code != 200 || out["saved"].(map[string]any)["version"].(float64) != 2 {
		t.Fatalf("PUT v2 = %d %v", code, out)
	}
	_, out = doJSON(t, "PUT", base+"/notes/files/pipeline.wb.js", map[string]any{"source": "return { plan: null ;\n"})
	if c := out["check"].(map[string]any); c["ok"] != false || c["role"] != "workbench" {
		t.Fatalf("syntax error not reported: %v", out)
	}

	_, out = doJSON(t, "GET", base, nil)
	if list := out["apps"].([]any); len(list) != 1 || list[0].(map[string]any)["name"] != "notes" {
		t.Fatalf("GET /api/apps = %v", out)
	}
	_, out = doJSON(t, "GET", base+"/notes", nil)
	if files := out["files"].([]any); len(files) != 2 {
		t.Fatalf("GET /api/apps/notes = %v", out)
	}
	_, out = doJSON(t, "GET", base+"/notes/files/search.js", nil)
	if out["version"].(float64) != 2 || out["role"] != "verbs" {
		t.Fatalf("GET file = %v", out)
	}
	_, out = doJSON(t, "GET", base+"/notes/files/search.js?version=1", nil)
	if !bytes.Contains([]byte(out["source"].(string)), []byte("[{ q }]")) {
		t.Fatalf("GET version 1 = %v", out)
	}
	_, out = doJSON(t, "GET", base+"/notes/history?path=search.js", nil)
	if vs := out["versions"].([]any); len(vs) != 2 || vs[1].(map[string]any)["comment"] != "first draft" {
		t.Fatalf("history = %v", out)
	}

	if code, _ := doJSON(t, "DELETE", base+"/notes/files/pipeline.wb.js", nil); code != 200 {
		t.Fatalf("DELETE = %d", code)
	}
	if code, _ := doJSON(t, "GET", base+"/notes/files/pipeline.wb.js", nil); code != 404 {
		t.Fatalf("GET deleted = %d", code)
	}
}

func TestAppsAPIRejectsBadInput(t *testing.T) {
	ts, _ := newAppsServer(t)
	base := ts.URL + "/api/apps"
	for _, u := range []string{
		base + "/..%2Fetc/files/x.js",
		base + "/notes/files/..%2F..%2Fx.js",
		base + "/notes/files/.hidden.js",
		base + "/notes/files/run.sh",
	} {
		if code, out := doJSON(t, "PUT", u, map[string]any{"source": "x"}); code != 400 {
			t.Errorf("PUT %s = %d %v, want 400", u, code, out)
		}
	}
	if code, _ := doJSON(t, "PUT", base+"/notes/files/x.js", map[string]any{"comment": "no source"}); code != 400 {
		t.Errorf("PUT without source = %d, want 400", code)
	}
	if code, _ := doJSON(t, "GET", base+"/missing", nil); code != 404 {
		t.Errorf("GET missing app = %d, want 404", code)
	}
	// without a store, the API says how to enable it
	ts2 := httptest.NewServer(New(Config{}).Handler())
	defer ts2.Close()
	if code, out := doJSON(t, "GET", ts2.URL+"/api/apps", nil); code != 503 || out["error"].(map[string]any)["code"] != "apps_disabled" {
		t.Errorf("no store = %d %v", code, out)
	}
}
