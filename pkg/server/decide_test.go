package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/decide"
)

// RAGWB-007 §3.6: /_rt/decide answers a decision.Request; errors carry codes.
func TestDecideEndpoint(t *testing.T) {
	post := func(s *Server, body string) (int, map[string]any) {
		srv := httptest.NewServer(s.Handler())
		defer srv.Close()
		r, err := http.Post(srv.URL+"/_rt/decide", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return r.StatusCode, m
	}
	ok := `{"state":{"query":"rebalance law"},"questions":{"q":{"type":"choice","instructions":"which?","options":[{"name":"widgets"},{"name":"law","description":"rebalance law"}]}}}`
	status, m := post(New(Config{Decider: &decide.Fake{}}), ok)
	if status != 200 || m["model"] != "fake" || m["answers"].(map[string]any)["q"].(map[string]any)["choice"] != "law" {
		t.Fatalf("%d %v", status, m)
	}
	status, m = post(New(Config{}), ok)
	if status != 503 || m["error"].(map[string]any)["code"] != "decide_unavailable" {
		t.Fatalf("no decider: %d %v", status, m)
	}
	status, m = post(New(Config{Decider: &decide.Fake{}}), `{"state":"s","questions":{"bad id":{"type":"noul","instructions":"?"}}}`)
	if status != 400 || m["error"].(map[string]any)["code"] != "invalid_request" {
		t.Fatalf("invalid: %d %v", status, m)
	}
}
