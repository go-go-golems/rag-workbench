# Changelog

## 2026-11-04

- Initial workspace created

## 2026-11-04

trace({details: true}): expandable per-step outputs, live from engine events on submit runs; cached steps now emit hits/context; answer-first preset uses it; RAGWB_FAKE_STREAM for slow fake LLM

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/ragjs/assets/gui.js — foldEvent, stepDetail, traceDetails, live runNow
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/ragjs/assets/rag.js — describe() for ran and cached steps
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/ragjs/trace_fold_test.go — fold and cached-step tests
