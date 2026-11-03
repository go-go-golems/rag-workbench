# Changelog

## 2026-11-02

- Initial workspace created

## 2026-11-02

Imported claude.ai rag-workbench session; goja smoke test (rag.js runs; \p{L} regex unsupported); reuse-map analysis across ragkit, goja-bleve, goja-text, go-go-goja, geppetto, slop-computer, coinvault; go-go-parc survey

## 2026-11-02

Intern guide written: architecture, API adjustments A1–A10, decisions D1–D8 (incl. widget.dsl vs gui, rag v2 DSL relation), phases P0–P9; spike 03 (split files as jsverbs, parity 12.676)

## 2026-11-02

P0+P1: ragwb Go module and CLI; rag.js/gui.js/kit.js vendored as goja modules (D6 regex fix, digest hook); parity test (12.676) and 30-script preset sweep all build ok, reports preset 11/12 goldens as in browser

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/ragjs/module.go — ragwb native modules
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/runner/script.go — headless workbench-script harness

## 2026-11-02

P2+P3: ragwb verbs (jsverbs with ragwb runtime) validated on split-file examples/apps/reports (12.6758 parity) and examples/apps/parc; native rag.source.dir with roots allowlist; vault Research/ subset 572 files/10.7MB builds in 69s/477MB (motivates P4/P8); corrected BM25 baseline (1.1s, not 0.12s)

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/examples/apps/reports/search.js — split-file app verbs
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/sourcedir.go — source.dir native component
