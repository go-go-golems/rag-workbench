# Changelog

## 2026-11-04

- Initial workspace created

## 2026-11-04

Intern design guide: verbs as serve endpoints, SSE event model, request scopes, cancellation, shell to SSE; phased plan

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/ttmp/2026/11/04/RAGWB-004--query-api-endpoints-with-sse-streaming-of-intermediate-results/design-doc/01-intern-guide-query-api-endpoints-and-sse-streaming.md — the guide

## 2026-11-04

Guide uploaded to reMarkable: /ai/2026/11/04/RAGWB-004/RAGWB-004 Query API endpoints and SSE streaming.pdf

## 2026-11-04

Implemented Phases 1-5: engine events + request scopes, served verbs (Invoke), /verbs endpoints with JSON and SSE, shell /run on SSE (NDJSON removed), LLM cancellation, reports ask verb; vault: SSE adds no measurable latency (9.3 ms median, first hits 8.7 ms); diary steps 3-7

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/runner/verbs.go — Invoke, params, events
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/server/sse.go — SSE writer
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/server/verbs.go — verb endpoints
