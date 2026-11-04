# Changelog

## 2026-11-04

- Initial workspace created

## 2026-11-04

Intern guide written and uploaded to reMarkable (/ai/2026/11/04/RAGWB-006); spike shows Geppetto + Pi streams deltas (glm-5.3-flash: 43 deltas, first at 402 ms); geppetto v0.13.13 added (glazed v1.4.3: --output → --format)

## 2026-11-04

Implemented: pkg/llm/gepp (Geppetto + Pi, streaming), --llm pi, streaming tests through engine and SSE; live: 23 SSE text events per answer, browser answer grows in 10 repaints, workbench /_rt/sample 35 text events

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/cmd/ragwb/cmds/roots.go — --llm pi, --pi-agent-dir, tiersOr
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/llm/gepp/backend.go — Pi agent dir, tier lookup, thinking levels
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/llm/gepp/sample.go — sample() on Geppetto: rounds, streaming sink, tools, errors
