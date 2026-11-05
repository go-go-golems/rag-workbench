# Changelog

## 2026-11-04

- Initial workspace created

## 2026-11-04

trace({details: true}): expandable per-step outputs, live from engine events on submit runs; cached steps now emit hits/context; answer-first preset uses it; RAGWB_FAKE_STREAM for slow fake LLM

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/ragjs/assets/gui.js — foldEvent, stepDetail, traceDetails, live runNow
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/ragjs/assets/rag.js — describe() for ran and cached steps
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/ragjs/trace_fold_test.go — fold and cached-step tests

## 2026-11-04

Real-reranker batching benchmark (order equal 12/12, score drift ≤ 0.0006, 8x2 first progress at ~half the rerank time); vault report pushed (go-go-parc eb7b821)

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/ttmp/2026/11/04/RAGWB-005--per-step-trace-details-in-the-gui-and-an-asynchronous-batched-rerank-model-with-progress/scripts/01-rerank-batching-bench.js — benchmark of rerank.model batch configurations against bge-rr-v2-m3
