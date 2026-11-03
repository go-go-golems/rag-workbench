---
Title: Prototype analysis and Go backend reuse map
Ticket: RAGWB-001
Status: active
Topics:
    - rag
    - goja
    - backend
    - ui-dsl
DocType: analysis
Intent: long-term
Owners: []
RelatedFiles:
    - Path: abs:///home/manuel/code/gec/coinvault/internal/knowledge/service.go
      Note: production ragkit bundle/hybrid/RRF wiring
    - Path: abs:///home/manuel/code/wesen/go-go-golems/geppetto/pkg/inference/tools/scopedjs/tool.go
      Note: LLM-callable eval tool over a prepared goja runtime
    - Path: abs:///home/manuel/code/wesen/go-go-golems/goja-bleve/pkg/module.go
      Note: require('bleve') module + xgoja provider
    - Path: abs:///home/manuel/code/wesen/go-go-golems/ragkit/rag/components.go
      Note: Chunker/Embedder/Index/Reranker contracts to wrap as a JS module
    - Path: abs:///home/manuel/code/wesen/slop-computer/pkg/server/sample.go
      Note: sample proxy with page-executed tool callbacks
    - Path: repo://ttmp/2026/11/02/RAGWB-001--import-rag-workbench-prototype-and-design-go-backed-script-runner/sources/prototype/app.js
      Note: Host contract (initCaps) and gui↔pipeline seam (guiHost)
    - Path: repo://ttmp/2026/11/02/RAGWB-001--import-rag-workbench-prototype-and-design-go-backed-script-runner/sources/prototype/rag.js
      Note: Grammar, IR, engine, component registry (DOM-free; runs in goja)
ExternalSources: []
Summary: What the claude.ai RAG workbench prototype is, which host APIs it depends on, goja compatibility findings, and which pieces of ragkit, goja-bleve, goja-text, go-go-goja, geppetto, slop-computer and coinvault can back a Go server + standalone script runner.
LastUpdated: 2026-11-02T21:20:48.84504168-05:00
WhatFor: Grounding the design of a Go-backed rag-workbench (server, script store, headless runner)
WhenToUse: Before designing or implementing the backend, runner or UI port
---

# Prototype analysis and Go backend reuse map

> **Superseded in part (2026-11-02):** sections 6–7 (shape and decisions) are refined in the intern guide, `design-doc/01-intern-guide-…`. That guide also covers widget.dsl, jsverbs, and rag-evaluation-system's own `rag` v2 DSL. This document stays as the first-pass evidence record.

## 1. Goal (restated)

Many small RAG applications for browsing software docs, the Obsidian vault (`go-go-parc`) and notes. Each one is a **JS script** that declares an indexing pipeline, a search/answer plan, evals and a UI written in the `gui` DSL. These scripts are:

1. **authored in the workbench**, alone or with the in-page agent, and saved on a Go backend;
2. **run headless** by the same backend or a CLI to index real corpora (a whole vault or a docs tree), using go-go-goja native modules for the heavy parts;
3. **served as ad-hoc apps**: the script's `gui.app(...)` renders in the browser while the plan's runs happen on the backend.

The claude.ai artifact is the prototype of the grammar, the UI and the agent. What's missing is the backend and the runners.

## 2. What the prototype is

Imported to `sources/prototype/` (published artifact v2; 4,992 lines of JS/HTML) plus `sources/textbook/` ("Search You Can See Into", Parts II–X and the appendices).

| File | Lines | Role | DOM? |
|---|---|---|---|
| `rag.js` | 2036 | Grammar + compiler + engine. Builders (`rag.corpus/plan/graph`) → JSON IR DAG, build phase (files→docs→chunks→indexes, memoized by digest) and live phase (signals→queries→hits→context→answer). Lambdas are stored as source and run closed through a `with(guard)` proxy. Ops-based workspace (set/insert/remove/replace/wire/signal/trigger, undo, branches, merge). Evals (hit@k, mrr, ndcg), sweeps, synthetic evals. | **No**, UMD |
| `gui.js` | 721 | UI grammar `gui.app(id).use(plan).state().derive().view(v => …)` → JSON node tree. A runtime tracks which state keys each component reads. ~45 components: layout, text/md, table, chart(svg), inputs, canvas, plus search components `searchBox`, `facets`, `results`, `answer`, `chunk`, `trace`, `dag`. | Spec builder: no. Runtime: yes |
| `agent.js` | 548 | Chat agent with a single `eval` tool (scope `rag, gui, wb, ws, ui, chat, ref`). Run modes auto/ask/read-only, per-step undo, rich parts (tables, charts, live apps, DAGs), conversations persisted. | yes |
| `app.js` | 1044 | Page shell: editor, preset picker, Files tab (uploads), inspector (DAG/Trace/Evals/History/Corpus/IR), wiring. | yes |
| `kit.js` | 53 | Standard corpus, 12 goldens, standard search app. | no |
| `presets/*.js` | 29 files | Runnable `pipeline.js` scripts in 6 groups (Start, Corpus & chunking, Ranking, With Claude, Search experiences, Evaluation). | no |

Component registry (`rag.js:474+`). Build components: `source.files source.inline parse.markdown chunk.sections chunk enrich enrich.llm index.bm25 index.terms synonyms bake`. Live components: `retrieve rewrite rewrite.llm fuse.rrf fuse.max filter filter.where rerank rerank.llm limit expand.neighbors expand.links pack answer agentic map`. `rag.define(type, …)` adds user components, and the fluent API is generated from the registry. **There are no embeddings anywhere**, because the artifact runtime has none.

A pipeline script is the body of an async function with `rag, gui, log, kit` in scope (`app.js:175`). It returns `{ plan, evals, app, variants? }`.

### 2.1 Host contract: the whole surface the backend must provide

| Host API (claude.ai) | Used at | Go-backend replacement |
|---|---|---|
| `window.claude.use('sample')` → `sample(turns, {tools:[{name,description,inputSchema,execute}], modelTier, onText, signal, cache})`, `sample.json`, `sample.limits()` | `app.js:968`, `agent.js:422` (the agent loop with the `eval` tool), `rag.js:516/574/641/726/781/1742` (LLM nodes) | slop-computer's `sample` SSE proxy (tools execute in the page, results POSTed back) on geppetto |
| `use('db')` → JSON doc tree `data/users/<uid>/ragwb` | `app.js:52` (uploads, preset edits, conversations) | SQLite: scripts, script versions, conversations, uploads |
| `use('user')` → `user.id()` | `app.js:975` | single local user / header |
| `use('downloads')` | export | plain HTTP download |
| `fetch('corpus.json' / 'presets/…' / 'rag.js')` | `app.js:258,988,992` | static or embedded assets + `/api/corpora/...` |

### 2.2 The gui↔pipeline seam

The gui runtime only reaches the pipeline through `host.rag` (`app.js:350` `guiHost`): `session().run(signals)`, `ir()`, `chunk(id)`, `doc(id)`, `chunks(pred)`, `docs()`, `neighbors(id)`. If a `RemoteRag` implements these over HTTP, an unchanged `gui.js` can drive a plan that runs in Go. `chunks(pred)` takes a lambda, so it either ships the lambda source to the server (lambdas are already source text) or becomes a query API.

Lambdas fall into two classes, and that gives a natural split:
- **gui runtime lambdas** `(s, ctx) => …` (derive, props) stay in the browser;
- **pipeline lambdas** (chunkers, enrichers, retrieve/rerank/map λ) run where the plan runs, which is the backend.

## 3. goja compatibility: spike results

`scripts/01-goja-smoke` (plain goja, same version as go-go-goja) loads `rag.js`, `gui.js` and `kit.js` **unmodified**, executes presets the way `app.js` does, builds, queries, runs the goldens and compiles the app IR. `scripts/02-goja-regex-probe` isolates one problem.

- ✅ UMD load, `new Function`, the `with(Proxy)` lambda guard, lookbehind regex, async/await (microtasks drain without an event loop), the app spec compiles to JSON.
- ❌ **goja does not support Unicode property escapes**. `/[\p{L}\p{N}_]+/gu` matches nothing and doesn't throw an error, so the tokenizer emits zero tokens and every search returns 0 hits. There are 7 occurrences (5 in rag.js, 2 in gui.js). With a load-time rewrite to explicit ranges (`RAGWB_COMPAT=1`):

| preset | build | query | hit@5 | mrr | ndcg@10 |
|---|---|---|---|---|---|
| minimal | 3.2 s | 4 ms | 1.0 | 0.933 | 0.847 |
| playground | 4.9 s | 9 ms | 1.0 | 0.958 | 0.879 |
| fusion | 4.4 s | 8 ms | 1.0 | 0.938 | 0.850 |
| custom-rerank | 4.4 s | 7 ms | 1.0 | 1.000 | 0.897 |
| fields | 4.3 s | 5 ms | 1.0 | 0.933 | 0.847 |

- ⚠️ **Performance** (corrected in diary step 7): about 5 s/MB to parse, chunk and BM25-index in goja (11 files / 540 KB → 462 chunks; parse ~0.5 s, chunk ~1.07 s, BM25 ~1.1 s). The real vault is ~81 MB, so expect minutes plus a large in-JS BM25. → Keep the grammar/IR/lambdas in JS and move the heavy build nodes to Go natives.
- Not verified yet: the LLM nodes (no `sample` shim in the spike), parity with browser scores, and whether the agent's `eval` scope works headless.

## 4. Target corpus: go-go-parc (survey)

- 2,441 `.md` files, about 84 MB. Excluding `.pi/`, `ttmp/`, `.trash/`, `*/_assets/**` and `scripts/` leaves ≈2,285 notes (~81 MB).
- Folders: `Projects/2026/MM/DD/` (1,315), `Research/` (573: Software Architecture Garden, Institute, KB, playbooks), `Transcripts/` (368), `Logs/` daily reports (48).
- Name prefixes act as an implicit type: `ARTICLE -` 566, `PROJ -` 367, `PROJECT REPORT -` 299, `CHATGPT TRANSCRIPT -` 195, `PLAYBOOK -`, `RES -`, `INVENTORY -`.
- Frontmatter in ~82% of notes: `title type tags created status aliases repo` (+ `repository*`, `ticket`, `related_*`). It has inconsistencies: tags written both as block lists and inline, `repo` vs `repository`, and daily reports use `date`. The prototype's `parseFrontmatter` is a small hand-rolled YAML subset, so this argues for a real YAML parser (goja-text `extract.frontmatter`).
- 11.6k wikilinks (3.7k aliased, 2.2k with a `#heading`), so `expand.links` and the link graph are valuable. MOCs and indexes are good for graph expansion but poor as chunks.
- Long notes: 690 are between 30 and 100 KB, 122 are over 100 KB (transcripts up to 546 KB). There are 43k code fences (2.4k mermaid), 1.9k callouts, transcript noise (`<details>` thinking, branch copies), and a few `.md` files that are actually HTML.

## 5. Reuse map

### 5.1 Retrieval and indexing (Go natives behind the JS grammar)

| Need | Reuse | Notes |
|---|---|---|
| Chunk / Document / Hit contracts, exact-slice chunks | **ragkit** `rag/types.go`, `rag/components.go` (Chunker, Embedder, Searcher, Index, Reranker) | No JS bindings: write a `require("ragkit")` adapter |
| Markdown heading chunker | ragkit `rag/chunking` `MarkdownHeading{Max,Min,OverlapRunes}`; **goja-text** `chunking.markdownSections/pack/recursive` (Span carries byte/rune/line ranges) | goja-text is already a JS module |
| Markdown parse, frontmatter | **goja-text** `markdown.parse/walk/textContent` (goldmark), `extract.frontmatter` (yaml.v3) | Replaces the hand-rolled parser for vault scale |
| BM25 / lexical | ragkit `rag/lexical/bm25.go` (memory), `rag/lexical/bleve` (persistent + digest manifest); **goja-bleve** `require("bleve")` (mapping, batches, query types, highlight, hybrid `score('rrf'\|'rsf')`, knn) | goja-bleve doesn't expose facets; the prototype's `index.terms` covers facets anyway |
| Vectors | ragkit `rag/vector/sqliteexact` (persistent), `exact`, `hnswcandidate` (memory); goja-bleve knn needs `-tags=vectors` + FAISS | sqliteexact is the low-friction choice |
| Embeddings | **geppetto** `pkg/embeddings` (OpenAI, Ollama, Cohere; LRU + disk cache); JS `gp.embeddings(settings).embed/embedBatch`; ragkit `rag/provider/geppetto.NewEmbedder`, `embedding.CachedEmbedder` | New grammar node: `index.vector` / `rag.embed()` |
| Fusion, collapse, hydrate | ragkit `rag/retrieval` `WeightedRRF`, `Collapse`, `Hydrate(FromStore)` | Same semantics as `fuse.rrf` |
| Rerank | ragkit `TermOverlap`, `CachedReranker`; geppetto JS `reranker` | |
| Persisted, content-addressed index bundles | ragkit `rag/indexbundle` (bleve + sqlite vectors + sqlite content, atomic publish, manifest) | Lets a vault index be built headless once and opened by apps |
| Evals | ragkit `rag/evaluation` (P/R@k, MRR, nDCG) | The prototype's JS evals already work in goja |
| Production example of all this | **coinvault** `internal/knowledge/service.go` (bundle open, lexical/hybrid, RRF, reranker, synonyms), `internal/knowledgebuild/build.go` (geppetto embedder), `knowledge_search` tool | No goja there; it's a reference for wiring |

### 5.2 JS runtime

| Need | Reuse |
|---|---|
| Runtime factory, require, event loop, owner for goroutine-safe calls | **go-go-goja** `engine.NewRuntimeFactoryBuilder().WithModules(...).Build().NewRuntime(...)` (`pkg/engine/factory.go`), `runtimeowner.Call/Post` |
| Host modules | go-go-goja `fs` (Promise + Sync, `Root` option), `path`, `yaml`, `database`, `fetch`, `exec`, `timer`. **There is no recursive walk or glob**, so a `walk/glob` helper has to be written (needed for `rag.files('**/*.md')` over a vault) |
| Custom binaries | **xgoja** providers (`providerapi.ProviderRegistry`). goja-bleve, goja-text and geppetto already ship providers. `cmd/goja-bleve` is an xgoja-generated binary bundling bleve + geppetto + fs: almost the "standalone runner" shape already |
| LLM calls from scripts | **geppetto** `require("geppetto")` (engines, agent/session/toolLoop, embeddings, reranker, profiles); `pkg/js/runtime.NewRuntime` |
| LLM-callable eval tool over a prepared runtime | geppetto **`scopedjs`**: `{code,input} → {result,console,error}`, waits on promises, captures console. This is the server-side twin of the prototype's `eval` tool |
| Static HTML from scripts | go-go-goja `uidsl` (`ui.div(...)` → HTML string). It isn't reactive, so it's no substitute for `gui`; maybe useful for static exports or reports |

### 5.3 Serving the app and the agent

| Need | Reuse |
|---|---|
| `window.claude` runtime on a local backend | **slop-computer** `web/runtime/src/index.ts` (`claude.use(name)` for db/user/assets/downloads/sample), injected per page (`pkg/server/sandbox.go` `inject`) |
| `sample` with **page-executed tools** (exactly what agent.js needs) | slop `web/runtime/src/sample.ts` (POST + SSE) ↔ `pkg/server/sample.go` `startSample` streams `call/text/thinking/tool/result/error`, parks on a channel until `POST /_rt/sample/{call}/tools/{use}` (`sampleTool`), `cancelSample` |
| LLM backend abstraction | slop `pkg/llm/backend.go` `Backend{Models, Sample(ctx, req, emit, ExecuteTool), Close}`, geppetto impl `pkg/llm/gepp/sample.go` (multi-round, tool registry, event adaptation), tiers quick/default/complex, `fake.go` deterministic backend for tests |
| Server-side scripts in slop | `pkg/actor` (goja actor, 5 s turn limit; `sample()` is supported but **tools are not**, `pkg/actor/sample.go:103`) |
| Profiles and keys | geppetto `engineprofiles` YAML registry (coinvault `profile-registry.yaml` pattern) |
| Tool allowlist per app/profile | coinvault `catalog.BuildRegistry(allowed)` + `application-profiles.yaml` |
| SPA embedding | coinvault `internal/webui` (build tag `embed`, disk fallback, runtime config injection, SPA fallback) |
| Full web chat (later, for server-side agents) | coinvault pinocchio chatapp + sessionstream (WS, submit/stop/snapshot) |
| Saved-script storage | slop `pkg/store` (SQLite docs/files/snapshots/refs) is overkill; a small SQLite `scripts(id, name, kind, source, created, updated)` + `script_versions` is enough, maybe borrowing slop's snapshot/ref idea for branches |

## 6. Shape that falls out of this

```
                ┌──────────────── browser ───────────────────────┐
                │ workbench (ported app.js/agent.js/gui.js)      │
                │  editor ─ presets ─ inspector ─ agent(eval)    │
                │  gui runtime ── host.rag = RemoteRag ──────┐   │
                │  window.claude shim (sample/db/user)       │   │
                └───────┬──────────────────────┬─────────────┼───┘
                 sample SSE + tool cb      /api/scripts   /api/apps/:id/{run,chunk,doc}
                ┌───────▼──────────────────────▼─────────────▼───┐
                │ ragwb server (Go, net/http ServeMux, go:embed) │
                │  llm: geppetto (profiles, tiers, toolloop)     │
                │  store: sqlite scripts/versions/convs/uploads  │
                │  runner: go-go-goja runtime pool               │
                │   ├ rag.js (compat-patched) grammar/IR/engine  │
                │   ├ native nodes: walk, goja-text md/chunk,    │
                │   │  bleve/ragkit bm25, ragkit vectors+embed   │
                │   └ ctx.sample → geppetto                      │
                │  artifacts: ragkit indexbundle per (corpus,dg) │
                └────────────────────────────────────────────────┘
   CLI: ragwb index script.js --root ~/…/go-go-parc   (headless build → bundle)
        ragwb serve script.js                          (ad-hoc app on its own)
        ragwb eval  script.js                          (goldens in CI)
```

## 7. Decisions to make next

1. **The engine stays in JS, accelerated with natives** (recommended). Keep `rag.js` as the grammar, IR, ops, evals and live engine (it runs in goja today) and register Go-native implementations for `source.files` (walk/glob/ignore), `parse.markdown`, `chunk.sections`, `index.bm25`, plus a new `index.vector`. The alternative is to reimplement the whole engine in Go and keep only the builders in JS. That is faster, but it forks the semantics the textbook documents.
2. **Where the agent loop runs.** Phase 1: as in slop, the LLM loop runs on the server and the `eval` tool runs in the page, so `agent.js` is almost unchanged. Phase 2: a server-side agent through `scopedjs` over the runner's runtime, for headless "build me an index/app" jobs.
3. **Same `rag.js` in both places?** The browser could keep running small corpora locally (instant feedback) and switch to `RemoteRag` for real corpora. The decision is either one code path (always remote) or both.
4. **Build artifact persistence**: the prototype memoizes in memory per digest. For a vault, build nodes need on-disk caches (bleve dirs / ragkit bundles keyed by node digest), plus incremental per-file rebuilds (rag.js already digests per file).
5. **Corpus access and sandboxing**: scripts name roots (`rag.files({root:'~/…/go-go-parc', glob:'**/*.md', ignore:[…]})`). go-go-goja host fs explicitly "does not sandbox paths", so the server should allowlist roots.
6. **Fix list from the prototype** (found while writing the textbook; see `sources/conversation.md`): Doc cards stale index after Build, lambdas lost from undo history for function-valued ops, the `failures_2` id mismatch, the playground `k` slider, history display, the dry-run cost estimate, `expect` never firing, read-only mode not sealed, byte vs char limits.
