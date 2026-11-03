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

## 2026-11-02

P4: Go ports of parse.markdown, chunk.sections, index.bm25 under original type names; byte-identical parity tests (markdown, BM25, tokenizer, toFixed); chunk/block ranges (A2); presets sweep identical; whole vault 2282 notes/85880 chunks builds in 17.4s (3.47GB RSS)

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/bm25.go — Go port of tokenize/stem/Bm25Index
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/markdown.go — Go port of parseMarkdown with ranges
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/runner/bm25_parity_test.go — JS-vs-Go parity tests

## 2026-11-02

P9: llm package (sample contract, LunaRoute client with tool loop, embeddings, rerank, fake) bridged as rag env.sample; reports/agentic/doc-cards LLM presets run headless against LunaRoute; --llm/--build-calls flags

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/llm/lunaroute.go — LunaRoute chat/embeddings/rerank client
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/ragjs/sample.go — sample contract bridge (promises across goroutines)

## 2026-11-02

Whole-vault parity (2282 notes) found and fixed the empty-fence token quirk; now byte-identical, JS 280s vs Go 4.4s. P8a: index.vector (LunaRoute emb-granite, SQLite embedding cache) + rerank.model (bge-rr-v2-m3); hybrid plan best on goldens (MRR 1.0, nDCG 0.915)

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/embcache.go — persistent embedding cache
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/vector.go — index.vector + rerank.model components

## 2026-11-02

P5: ragwb workbench serves the ported page with a local window.claude runtime (sample SSE with page tools, SQLite db, user, downloads); browser-verified Ask + 9-step agent session on LunaRoute; tool-loop markup leak fixed; whole-vault dense index built (85,881 chunks, 15.7 min, cached)

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/server/sample.go — SSE sample with parked page tools
- /home/manuel/code/wesen/go-go-golems/rag-workbench/web/workbench/claude.js — window.claude shim
- ttmp/2026/11/02/RAGWB-001--import-rag-workbench-prototype-and-design-go-backed-script-runner/various/p5-workbench-agent-lunaroute.png — browser evidence of agent session

## 2026-11-03

go-go-goja PR #106 (https://github.com/go-go-golems/go-go-goja/pull/106): bump goja to e1fc788 for \p{..} under /u, plus regression test; rag-workbench follows after v0.10.7 (task k4hx)

### Related Files

- /home/manuel/code/wesen/go-go-golems/go-go-goja/pkg/engine/regexp_test.go — regression test for Unicode property escapes in the shared runtime

## 2026-11-03

go-go-goja PR #106 extended: goja/goja_nodejs master, database nil-slice fix, Dependabot #105/#94/#89 + alerts at newest releases, toolchain go1.26.10, gosec ttmp exclusion
