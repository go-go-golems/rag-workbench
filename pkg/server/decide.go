package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-go-golems/geppetto/pkg/decision"

	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// handleDecide is POST /_rt/decide (RAGWB-007 §3.6): a decision.Request JSON
// body (as rag.js's env.decide sends it) answered with the decision.Response
// JSON. The request context cancels the provider call when the page aborts.
func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	if s.Decider == nil {
		writeErr(w, http.StatusServiceUnavailable, "decide_unavailable", "start ragwb workbench with --decide pi|lunaroute|fake")
		return
	}
	var req decision.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	resp, err := s.Decider.Decide(r.Context(), req)
	if err != nil {
		code, msg, _ := ragjs.DecideErrorCode(r.Context(), err)
		status := http.StatusBadGateway
		switch code {
		case "invalid_request", "prompt_too_large", "model_not_found":
			status = http.StatusBadRequest
		case "not_granted":
			status = http.StatusForbidden
		case "rate_limited":
			status = http.StatusTooManyRequests
		case "cancelled":
			status = 499
		}
		writeErr(w, status, code, msg)
		return
	}
	writeJSON(w, resp)
}
