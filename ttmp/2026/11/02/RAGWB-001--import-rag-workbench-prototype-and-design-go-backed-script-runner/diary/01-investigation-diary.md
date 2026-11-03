---
Title: Investigation diary
Ticket: RAGWB-001
Status: active
Topics:
    - rag
    - goja
    - backend
    - ui-dsl
DocType: diary
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://ttmp/2026/11/02/RAGWB-001--import-rag-workbench-prototype-and-design-go-backed-script-runner/scripts/01-goja-smoke/main.go
      Note: goja smoke test running prototype presets + goldens
    - Path: repo://ttmp/2026/11/02/RAGWB-001--import-rag-workbench-prototype-and-design-go-backed-script-runner/scripts/02-goja-regex-probe/main.go
      Note: isolates the missing \p{L} support in goja
    - Path: repo://ttmp/2026/11/02/RAGWB-001--import-rag-workbench-prototype-and-design-go-backed-script-runner/sources/prototype/app.js
      Note: initCaps (host contract) and guiHost (gui↔pipeline seam)
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-02T21:20:48.93324213-05:00
WhatFor: ""
WhenToUse: ""
---

# Investigation diary — RAGWB-001

## 2026-11-02 · Step 1 — Ticket + import

- Created ticket `RAGWB-001`; added vocab topics `rag`, `goja`, `ui-dsl` and doc types `analysis`, `diary`.
- Imported the claude.ai session `b9cbc13e-…` ("RAG workbench with fluent builder API", 2026-10-16) into `sources/`:
  - `sources/prototype/` — published artifact v2 (index.html, rag.js, gui.js, agent.js, app.js, kit.js, corpus.json, 29 presets + index.json) plus `pipeline.js` from the full zip.
  - `sources/textbook/` — "Search You Can See Into" markdown (contents, Parts II–X, appendices).
  - `sources/corpus-uploads/` — the 11 reports used as the bundled corpus.
  - `sources/conversation.md`, `meta.json`, `rag-workbench-examples.js` (15 pre-implementation API examples), the textbook PDF.
  - Not imported: scratchpad / subagent transcripts / screenshots (still in `~/code/wesen/transcripts/claude/b9cbc13e-…/files/rag-workbench-full.zip`).
- Mistake: zsh didn't expand a `RAGWB-001--*` glob inside a variable assignment and `mkdir -p` created a literal `RAGWB-001--*` directory. Moved `sources/` into the real ticket dir and removed the stray dir.

## Step 2 — Read the prototype's host contract

- `app.js:968` `initCaps()` is the only place the page touches the claude.ai runtime: `window.claude.use('sample'|'db'|'user'|'downloads')`, plus `sample.limits()` and `user.id()`.
- `sample(turns, {tools:[{name,description,inputSchema,execute}], modelTier, onText, signal, cache})` — the **tool loop runs in the page** (`agent.js:445` `evalTool` → `execute` → `runEval`). The agent has a single `eval` tool.
- `rag.js` uses `ctx.sample`/`ctx.sample.json` for enrich.llm, rewrite.llm, rerank.llm, answer, agentic (lines 516, 574, 641, 726, 781) and synthetic evals (1742).
- `db` is used as a JSON doc tree under `data/users/<uid>/ragwb` (app.js:52) for uploads, preset edits, conversations.
- The gui runtime only reaches the pipeline through `host.rag` (app.js:350 `guiHost`): `session().run(signals)`, `ir()`, `chunk(id)`, `doc(id)`, `chunks(pred)`, `docs()`, `neighbors(id)`. **This is the seam for a remote (Go-backed) pipeline.**

## Step 3 — goja smoke test (scripts/01-goja-smoke, scripts/02-goja-regex-probe)

- `go run ./scripts/01-goja-smoke <prototype-dir> presets/X.js` loads rag.js + gui.js + kit.js into plain goja (same version as go-go-goja: `v0.0.0-20251103141225-af2ceb9156d7`), runs a preset the way `app.js:175` does (`new Function('rag','gui','log','kit', …)`), builds, runs one query, runs the goldens, and compiles the app spec.
- Result 1: all three files load, `new Function` + `with(__g)` lambda guard works, build reports ok, app spec compiles to JSON IR (`kind: gui`). **But every query returned 0 hits and evals were all 0.**
- Probe (02): `"rebalance law".match(/[\p{L}\p{N}_]+/gu)` → `null` in goja. **goja silently does not support Unicode property escapes**; lookbehind (`(?<=…)`) works. The tokenizer (`rag.js:166`) therefore emits no tokens.
- Fix for the spike: `RAGWB_COMPAT=1` rewrites `\p{L}\p{N}` to explicit ranges at load time (7 occurrences: 5 in rag.js, 2 in gui.js). With it:

| preset | build | query | hit@5 | mrr | ndcg@10 |
|---|---|---|---|---|---|
| minimal | 3.2 s | 4 ms | 1.0 | 0.933 | 0.847 |
| playground | 4.9 s | 9 ms | 1.0 | 0.958 | 0.879 |
| fusion | 4.4 s | 8 ms | 1.0 | 0.938 | 0.850 |
| custom-rerank | 4.4 s | 7 ms | 1.0 | 1.000 | 0.897 |
| fields | 4.3 s | 5 ms | 1.0 | 0.933 | 0.847 |

- Build cost breakdown (minimal, 11 files / 540 KB → 462 chunks): parse 0.5–0.6 s, chunk ~1.07 s, bm25 ~0.12 s. That is roughly 3 s/MB in goja. The go-go-parc vault (~81 MB of real notes) would take ~4 min single-threaded with an all-JS in-memory BM25 holding ~70k chunks → parse/chunk/index for vault-scale runs should be Go natives, with the JS grammar kept as the orchestration layer.
- Not verified: equality with browser scores (the conversation reported 11/12 lexical goldens for the *reports* preset, which I didn't run); LLM nodes (no `sample` shim yet); my `\p` replacement is an approximation (Latin/Greek/Cyrillic/kana/CJK only).

## Step 4: UI DSL prior art, the jsverbs pattern, and a split-file spike

The user pointed at three more pieces of prior art: the JSON→React widget renderer used by the upwork tracker, the goja SQL module, and the jsverbs pattern for exposing JS functions as CLI commands. They also asked whether ragkit should be adapted to the workbench API or the API changed. I surveyed upwork and jsverbs directly, delegated a survey of `rag-evaluation-system` (the repo that owns `widget.dsl`), and built a third spike. It splits a pipeline into `lib/corpus.js` and `lib/plans.js`, plus two verb files `search.js` and `evals.js`, and runs them through go-go-goja's `jsverbs-example` runner.

The spike works end to end. The verbs are discovered, run against the bundled corpus, and emit Glazed rows. The top BM25 score for "rebalance law" (12.676) matches the browser engine's value quoted in the textbook (12.6758), which is the first parity evidence between goja and the browser.

### Prompt Context
**User prompt (verbatim, turn 1, recorded here because Step 1 predates the investigation format):** "Create a newdocmgr ticket, Import rag-workbench from  ~/code/wesen/transcripts/claude/b9cbc13e-53a9-47a0-bcc3-96c5102e2490/ , and analyze how to transform this into a go backed app using ../ragkit and potentially other libs like ~/code/wesen/go-go-golems/geppetto or goja-bleve and look at maybe /home/manuel/code/gec/coinvault  What I want is a version where I can create and save scripts with an agent in the workbench (by porting the UI, you can look at ~/code/wesen/go-go-golems/slop-computer maybe on how to implement the claude sample and other apis to serve the app), and then also run the created scripts as standalone to either index a whole vault like for example ~/code/wesen/go-go-golems/go-go-parc or other documents (potentially by adapting the JS API to support go-go-goja stuff).  SO, I kind of want to run the scripts in the backend, and be able to create UIs still with it to make ad-hoc apps.  Anyway, analyze all this, and come back and repeat back to me what I just said, in your own words and with the details of what you found and might be reused.  My goal is to create many little RAG applications to have different way of browsing my software documentation and obsidian vault and notes and all that. And I want to create these pipelines in JS to index and search, and also use the UI DSL to create the UIs. The RAG workbench from claude.ai is a prototype I did where we now need to fill in the backend / runners."

**User prompt (verbatim, turn 2):** "there;s definitely some UI DSL patterns in like ~/code/wesen/go-go-golems/upwork with like hte JSON react renderer.  We also have goja-text and goja-bleve in the same directory, and we can also use the goja sql module. the whole pipeline + query + app don't need to be in the same file. If a file has a query or so you can run it as a command line jsverb, same if it contains indexing pipelines etc..., look at the jsverb pattern.   would we build adapters for ragkit to the JSAPI used in the workbench? or would we modify the API? I think the API and its concept is great, maybe it needs a bit of adjustment?"

**User prompts (verbatim, mid-turn 2):** "Create  a detailed analysis / design / implementation guide that is for a new intern, explaining all the parts of the system needed to understand what it is, with prose paragraphs and bullet point sand pseudocode and diagrams and api references and file references. It should be very clear and technical. Store in the ticket and the nupload to remarkable." and "Then as you implement (after properly researching and writing the guide), commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill)"

**Assistant interpretation:** Fold widget.dsl, jsverbs and the db module into the design. Answer "adapter vs API change". Write an intern-grade design/implementation guide, upload it to reMarkable, then implement in phases with commits and this diary.

**Inferred user intent:** A concrete, buildable plan whose first slices reuse the existing go-go-golems runtime stack (xgoja, jsverbs, widget.dsl), not a new framework, and which keeps the workbench grammar's concepts.

### What I did
- Read `~/code/wesen/go-go-golems/upwork/XGOJA-JS-GUIDEBOOK.md`, `xgoja.yaml`, `verbs/upwork.js` (lines 1–330) and greps of `verbs/lib/pages.js`.
- Read `go-go-goja/pkg/doc/08-jsverbs-example-overview.md`, `09-jsverbs-example-fixture-format.md`, `cmd/jsverbs-example/main.go`, `pkg/jsverbs/runtime.go` (InvokeInRuntime, waitForPromise).
- Read the `go-go-goja/modules/database/database.go` Loader (line ~221): `configure/query/exec/begin/close`.
- Compared ragkit `rag/types.go` (Chunk = exact byte Range + Representation kinds) with the prototype's `makeChunk`/`blocksText` (`rag.js:309–320`).
- Built `jsverbs-example` from go-go-goja into the scratchpad. Wrote `scripts/03-jsverbs-split/` (`00-setup.sh` generates `app/lib/rag.js` with the `\p{L}` rewrite and `app/lib/corpus-data.js`).

### Why
- Splitting into files is the user's explicit requirement, and jsverbs is the house pattern for "a JS file is a command". I needed to prove that the prototype's immutable builders survive being shared through `require()` across verb files.

### What worked
- `jsverbs-example --dir ./app list` → `reports goldens (evals.js#goldensVerb)`, `reports search (search.js#search)`. The lib files contribute no verbs.
- `reports search "rebalance law" --k 3` → rank 1 `5. The core's modules › 5.9 The rebalance law`, score **12.676** (3.2 s wall, almost all of it the build).
- `reports search "protojson int64" --plan fused --output csv` → `The protobuf payload contract › The three type traps` at rank 1.
- `reports goldens` (4 cases) → lexical hit@5 1 / mrr 1 / ndcg@10 0.845; fused 1 / 1 / 0.899 (3.8 s wall; both plans share one engine, so the build is memoized once).

### What didn't work
- `require("./corpus.json")` inside a jsverbs app failed with `Error: GoError: Invalid module at github.com/dop251/goja_nodejs/require.(*RequireModule).require-fm (native)`. The jsverbs source loader resolves only the scanned `.js/.cjs` files. Recovery: `00-setup.sh` wraps the JSON as `corpus-data.js` (`module.exports = …;`).

### What I learned
- widget.dsl (`rag-evaluation-system` provider `widgetsite`) is **server-driven**: JS builds page JSON per request (`/api/widget/pages/:id`), and actions POST to Express routes, which respond `{ok, refresh, toast}`. The prototype's `gui` is **client-reactive**: state, derive and lambdas run in the browser, and typing re-runs only the live nodes. These are different execution models, and the design has to pick or combine them.
- jsverbs metadata must be static literals (tree-sitter parsed). Builders and plans can't be referenced inside `__verb__`, only from the function body.
- go-go-goja `db` uses mattn/go-sqlite3. FTS5 needs the `sqlite_fts5` build tag.

### What was tricky to build
- Nothing beyond the JSON require. The engine instance is a module-level singleton in `lib/corpus.js`, so every verb in one process shares the build memo. That is good for evals over variants, but it means a long-lived server needs explicit engine lifetimes per corpus.

### What warrants a second pair of eyes
- Each CLI invocation rebuilds the whole corpus (about 3 s here, minutes for the vault). A persisted build artifact (Phase "bundle") is a prerequisite for query verbs to be pleasant.

### What should be done in the future
- Read the rag-evaluation-system survey when it lands, then decide on the gui renderer vs widget.dsl.

### Code review instructions
- `scripts/03-jsverbs-split/00-setup.sh`, then `jsverbs-example --dir scripts/03-jsverbs-split/app list|reports search …|reports goldens`. Build the runner with `go build ./cmd/jsverbs-example` in go-go-goja.

### Technical details
- jsverbs result mapping: object → 1 row, array → rows, primitive → `value`, Promise awaited; `output: "text"` for writer verbs. Bindings: `bind: "all" | "context" | "<section>"`.

## Step 5: Intern guide written and delivered

I wrote the intern-facing design and implementation guide. Its structure is orientation and vocabulary first, then current state with file references, spike evidence, gaps, the proposed architecture and APIs, eight decision records, pseudocode flows, an API reference, phases P0–P9, a test strategy and risks. I then uploaded it to reMarkable as one PDF.

The rag-evaluation-system survey arrived mid-write and changed the guide in two places. That repo already ships its own `require("rag")` (an authoring-only v2 DSL compiling to `rag-pipeline-ir/v2`), so our module is now `ragwb` (D8). Its widget.dsl is server-driven with whole-page refresh and no search widgets, so the UI decision (D3) keeps `gui` for search experiences and uses widget.dsl for server-driven pages, with a custom-renderer convergence path.

### Prompt Context
**User prompt (verbatim):** see Step 4 (the "Create a detailed analysis / design / implementation guide…" and "Then as you implement…" messages).
**Assistant interpretation:** One self-contained guide in the ticket, uploaded to reMarkable; implementation follows.
**Inferred user intent:** Onboard someone (or a future agent) to the whole system and make the build order unambiguous.
**Commit (code):** see the commit after this entry.

### What I did
- `docmgr doc add --doc-type design-doc --title "Intern guide: …"` → `design-doc/01-intern-guide-rag-workbench-on-go-architecture-apis-and-implementation-plan.md` (926 lines).
- Verified before citing: slop `web/runtime/src/sample.ts:60-115`, `pkg/server/sample.go:122-260` (event names, tool parking, timeout); ragkit `indexbundle/types.go:45-200`; goja-text `chunking/module.go:70-110`; upwork `runtimeidentity` provider and `cmd/tracker/main.go:57-67`; rag-evaluation-system `pkg/gojamodules/rag/module.go:45-85` and `examples/rag-v2/01-product.js`; prototype `rag.js` namespace (`:1974-2036`), `define` (`:1917`), `buildCtx` (`:1375`), `Session.ctx` (`:1570`), index classes (`:361-446`).
- Replaced `require("rag")` → `require("ragwb")` throughout the guide after D8.
- `remarquee upload bundle <guide> --name "RAGWB-001 Intern Guide RAG Workbench on Go" --remote-dir /ai/2026/11/02/RAGWB-001 --toc-depth 2 --non-interactive` → `OK: uploaded RAGWB-001 Intern Guide RAG Workbench on Go.pdf -> /ai/2026/11/02/RAGWB-001`.
- `docmgr doctor --ticket RAGWB-001 --stale-after 30` → all checks passed.

### Why
- The "adapter or API change?" answer needed a precise index/component contract. I extracted it from the code (`RagIndex`, `define`) rather than paraphrasing it.

### What worked
- Upload succeeded on the first attempt. That proves cloud delivery only, not sync to the device.

### What didn't work
- No failures in this step.

### What I learned
- The engine's extension surface is small: `rag.define` (with `map`/`all`/`run`), the `RagIndex` object contract, and `rag.configure(env)` with `files`/`sample`/`log`. Every native integration can go through those three.
- xgoja's `runtime-package` artifact plus a hand-written main (upwork) lets Go HTTP commands live next to generated JS commands.

### What was tricky to build
- Keeping the module rename consistent: one `replace` pass, then a grep confirming that only intentional references to rag-evaluation-system's `require("rag")` remain (3 lines).

### What warrants a second pair of eyes
- D3 and D8 are judgment calls about two existing org-wide DSLs. They are flagged as proposed, not accepted.
- §9.8: the gui runtime reads `host.rag.chunk(id)` synchronously. RemoteRag relies on prefetching, which hasn't been verified.

### What should be done in the future
- Implement P0→P2 now (scaffold, `ragwb` module with the regex fix, jsverbs runner, split-file example), then P3 (directory source) against go-go-parc.

### Code review instructions
- Read guide §3–§4 for orientation, §9.3 for the API answer, §10 for the decisions, §13 for the phases.

### Technical details
- Remote: `/ai/2026/11/02/RAGWB-001/RAGWB-001 Intern Guide RAG Workbench on Go.pdf`.

## Step 6: P0 scaffold + P1 `ragwb` module — the prototype grammar as a goja native module

I scaffolded the Go module and CLI, vendored the prototype's `rag.js`, `gui.js` and `kit.js` into `pkg/ragjs/assets/` with two documented local changes, and exposed them as native modules `ragwb`, `ragwb/gui` and `ragwb/kit`. A headless runner executes workbench-form scripts the same way `app.js:175` does. The phase gate is a sweep of all 29 presets plus `pipeline.js`. Every one builds and queries headlessly, and the `reports` preset reproduces the browser's reported 11/12 goldens (hit@5 0.917).

The `verbs` command (jsverbs over an app directory, with our runtime) is written and compiles, but it is **not validated yet**. That happens in Step 7 with the split-file example app.

### Prompt Context
**User prompt (verbatim):** "when you implement, commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill)  diary format is mandatory" (plus "what are the phases of impementation? and open the guide with `md-view view ...`", answered in chat; the guide was opened with `md-view view <guide>`, which started process 2600869). Earlier requests: see Step 4.
**Assistant interpretation:** Implement guide phases in order, with an investigation-format diary entry and a commit at each validated boundary.
**Inferred user intent:** A reviewable trail, where each commit corresponds to a phase with evidence.
**Commit (code):** see the commit made right after this entry ("RAGWB-001 P0/P1: …").

### What I did
- `go.mod` (`github.com/go-go-golems/rag-workbench`, go 1.26.1), pinned to **published** `go-go-goja v0.10.6` (no `replace`). That pulls glazed v1.3.5 and cobra v1.10.2.
- `pkg/ragjs/assets/{rag,gui,kit}.js`: vendored by a one-off Python patcher. Upstream sha256 values and the changes are recorded in `pkg/ragjs/assets/PROVENANCE.md`:
  1. D6: a feature-detected class body `LN` (`'\\p{L}\\p{N}'` when `new RegExp('^[\\p{L}\\p{N}]+$','u').test('é9')`, otherwise explicit ranges covering Latin-1/Latin Extended, Greek, Cyrillic, Hebrew, Arabic, kana, CJK and Hangul). `RE_WORD()`/`RE_NONWORD()` replace the 5 literals in rag.js. gui.js's 2 literals plus the string-built highlight regex (`gui.js:420`) use `rag.util.LN`.
  2. Engine: `Engine.digests` now calls an optional `def.digest(params, engine)` for any build component, so native sources can fold content digests into their node digest. Before, only `source.files` was special-cased (`rag.js:1254`).
- `pkg/ragjs/module.go`: compiles each file once per process (`goja.Compile`) inside a wrapper. rag.js gets `(module, exports, globalThis)`, so its UMD takes the CommonJS branch. gui.js and kit.js get `(globalThis)` bound to a private root that holds `rag`/`gui` fetched via `require`. `ragLoader` then calls `rag.configure({log, buildCalls: 'explicit'})` and installs `Options.Natives`.
- `pkg/ragjs/await.go`: `Eval(ctx, rt, op, fn)` runs on `rt.Owner` and polls promise state with backoff from 100µs to 20ms. It returns `*RejectionError` with the JS stack on rejection.
- `pkg/runner/runtime.go`: `NewFactory(Config)` = `WithModules(ragjs.Modules(...))` + `UseModuleMiddleware(engine.MiddlewareExclude("exec"))` + optional jsverbs source loader.
- `pkg/runner/script.go`: the workbench harness (`new Function('rag','gui','log','kit', …)`), build + run + optional evals, and a JSON report (build nodes, top hits, trace, pending deferred LLM nodes, variants, app kind).
- `cmd/ragwb`: `version`, `run <script.js> [--files dir] [--q] [--k] [--trigger] [--evals]`, `presets --dir`, `verbs --dir` (dir pre-scanned from argv like jsverbs-example).
- `examples/apps/reports/corpus/`: the 11 reports written from `corpus.json` with their **exact** names. The upload copies have underscores, and chunk ids derive from file names. `examples/presets/`: the 29 presets + `index.json` + `pipeline.js`.
- Tests `pkg/ragjs/module_test.go`: `TestTokenizeWorksInGoja`, `TestMinimalPresetParity`.

### Why
- Pinning the published v0.10.6 keeps the build reproducible. The local go-go-goja checkout is 23 commits ahead, but those are OIDC/auth work, irrelevant here.
- Compiling once with per-runtime evaluation lets presets, verbs and later server sessions each get isolated rag registries (`rag.define` mutates the registry) without recompiling 2k lines.

### What worked
- `go test ./pkg/ragjs/` → `ok` (3.5 s). The tokenizer degrades correctly: `["rebalanc","law","für","ωmega"]`, and `LN` is the fallback in goja.
- `node -e 'require("./pkg/ragjs/assets/rag.js").util.LN'` → `"\\p{L}\\p{N}"`, and CJK tokenizes (`日本語`), so the browser path keeps full Unicode.
- `./dist/ragwb presets --dir examples/presets --files examples/apps/reports/corpus` (exit 0, 2m13s wall). All 30 scripts give `BUILD ok`. Selected rows:

| preset | nodes | pending | query ms | hit@5 | mrr |
|---|---|---|---|---|---|
| minimal | 4 | 0 | 1.0 | 1.000 | 0.933 |
| reports | 8 | 0 | 10.6 | **0.917** | 0.926 |
| custom-rerank | 5 | 0 | 5.5 | 1.000 | 1.000 |
| code-blocks | 7 | 0 | 3.6 | 0.583 | 0.583 |
| doc-cards | 8 | **1** | 6.8 | 0.750 | 0.769 |
| agentic | 7 | 0 | 0.2 | **0.000** | 0.000 |

### What didn't work
- First parity test run: `module_test.go:138: top score = 12.5926, want 12.676 (browser parity)`. **The cause was my test, not the engine.** The textbook number (12.6758) uses `kit.reports()` field weights (`title:2, headingPath:3, callout:2, text:1`). My test built the minimal preset's corpus with default weights (`title:2, headingPath:3, text:1`), which scores 12.5926, the same value spike 01 printed (12.59). Fixed by using `kit.reports()` in the parity test, with a comment recording both numbers.
- No other failures. The LSP showed stale "missing go.sum" diagnostics until `go mod tidy` ran; `go build`/`go test` were authoritative.

### What I learned
- `agentic` hit@5 = 0 is expected without `sample`: its retrieval happens inside an LLM tool loop at `submit`. The run uses `trigger: change` and has no sampler, so it returns no hits and no errors. Headless reports should flag "LLM nodes skipped" explicitly (TODO).
- `doc-cards` reports `pending: 1`: its `enrich.llm` node is deferred under `buildCalls: 'explicit'`. That is the prototype's Build-button semantics, preserved.
- A sweep of 30 presets costs 133 s because each preset rebuilds in a fresh runtime (~4 s each). That is the A5 (persistence) motivation, measured.

### What was tricky to build
- **Shadowing `globalThis`.** rag.js's UMD tail is `(typeof globalThis !== 'undefined' ? globalThis : this)`. Making `globalThis` a *parameter* of the wrapper function hands the vendored code our private root without touching the real global. Combined with a `module` parameter, rag.js takes the CommonJS branch and gui.js/kit.js publish onto the private root.
- **Cross-module identity.** `ragwb/gui` must see the *same* rag object as `require("ragwb")` (gui reads `rag.util`, and plans built with one rag must compile with the same registry). The gui/kit loaders call the runtime's own `require()` for their deps, so goja_nodejs's per-runtime module cache guarantees identity.
- **Field-name mapping.** My first harness called `vm.SetFieldNameMapper(TagFieldNameMapper("json"))` to pass `[]File`. I removed it before running, because it changes struct field naming for every module in that runtime. Files are now passed as `[]map[string]any`.

### What warrants a second pair of eyes
- `pkg/ragjs/await.go` polls promise state, it doesn't subscribe. That's fine for CLI runs, but a server with many concurrent sessions should resolve via a then-callback that posts to a Go channel.
- The fallback character ranges (D6) drop letters outside the listed scripts (e.g. Thai, Devanagari) in goja only.
- `engine.MiddlewareExclude("exec")` still exposes `fs`, `database`, `fetch` and `os` to verb files. Roots confinement for `fs` is P3 work.

### What should be done in the future
- Validate `ragwb verbs` with a split-file example app (Step 7). Add `rag.source.dir` (P3).
- Report skipped LLM nodes in `ScriptReport`.
- File an upstream goja issue for `\p{..}` property escapes.

### Code review instructions
- Start at `pkg/ragjs/module.go` (`Modules`, `ragLoader`, `rootLoader`), then `pkg/ragjs/assets/PROVENANCE.md` and `git diff --no-index ttmp/…/sources/prototype/rag.js pkg/ragjs/assets/rag.js`.
- Validate: `go test ./...`; `make presets`.

### Technical details
- Module names: `ragwb`, `ragwb/gui`, `ragwb/kit` (constants in `pkg/ragjs/module.go`). `ragjs.Source(name)` serves the same bytes to the browser later.
- `ScriptReport` JSON: `{build:{ok,ms,errors,nodes[{id,type,count,ms,reused,deferred}]}, run:{trigger,signals,ms,calls,errors,top[],trace[],answer}, evals?, variants?, app?{kind,id,title}, pending[]}`.

## Step 7: P2 jsverbs runner + P3 `rag.source.dir`, and a corrected performance baseline

I validated the `verbs` command with two split-file example apps. `examples/apps/reports` (lib/corpus, lib/plans, search, evals, index, app) reproduces the browser's worked example exactly, **12.6758**, through the new Go-native `rag.source.dir` → parse → chunk → BM25. `examples/apps/parc` indexes and searches subsets of the real Obsidian vault behind a roots allowlist.

Two of my own earlier claims turned out wrong and are corrected here. First, BM25 in goja is not 0.12 s for 462 chunks but about 1.1 s; the 0.12 s figure was measured while the tokenizer was still broken and indexed nothing. Second, the honest goja build cost is about 5–6.5 s/MB, so a full vault build would take minutes and gigabytes in JS.

### Prompt Context
**User prompt (verbatim):** see Step 6 ("when you implement, commit at appropriate intervals and keep a detailed diary …").
**Assistant interpretation:** Continue phases P2/P3 with tests, a diary entry and a commit.
**Inferred user intent:** Saved scripts must run standalone over real corpora, which is the core of the request.
**Commit (code):** see the commit made right after this entry ("RAGWB-001 P2/P3: …").

### What I did
- `pkg/natives/sourcedir.go`: `SourceDir{Roots, Base}` implementing `ragjs.Native`. It registers `rag.define('source.dir', {phase:'build', in:[], out:'files', args, describe, digest, all})`. Behavior:
  - Walk: `filepath.WalkDir`, doublestar include (default `**/*.md`) and exclude (always `.git/** .obsidian/** node_modules/** .trash/**`), excluded directories pruned, symlinks not followed.
  - Skips: `maxBytes` (default 4 MiB), and HTML documents with a text extension (`skipHtml`, default on). Skips are reported through `ctx.log`.
  - Records: `{name: relpath, path, text (ToValidUTF8), size, mtime}`.
  - Digest: sha256 over the resolved params plus `(relpath, size, mtimeNano)` of every match, via the engine digest hook from Step 6.
  - Root resolution: `~` expansion, relative to `Base`, `Abs`, `EvalSymlinks`, and it must lie inside an allowlisted root.
- `runner.Config{Roots, Base}` always installs `SourceDir`. CLI `--root` is a persistent, repeatable flag. `cmds.Roots(dirs...)` = cwd + verbs `--dir` + `--root` values.
- `examples/apps/reports/`: `lib/corpus.js` (kit.reports weights over `source.dir({root: "corpus", include: ["*.md"]})`), `lib/plans.js` (`lexical`, `fused` = text+meta → rrf → filter.where → limit), `search.js` (`search`, `explain`), `evals.js` (`goldens`), `index.js` (`index`), `app.js` (`kit.searchApp(plans.fused)`, no verbs).
- `examples/apps/parc/`: `lib/corpus.js` (`corpus(include)`/`plan(include)` over `~/code/wesen/go-go-golems/go-go-parc` with the survey's excludes), `parc.js` (`index`, `search`, a `subset` section with an `include` stringList).
- `rag.js`: `RE_WORD`/`RE_NONWORD` now return regexes compiled once (`RE_WORD_G`/`RE_NONWORD_G`).
- Tests: `pkg/natives/sourcedir_test.go` (include/exclude/skips, digest changes on edit and on globs, allowlist incl. parent escape and empty-deny, relative root via Base, relative allowlist entry); `cmd/ragwb/cmds/verbs_test.go` (`TestReportsAppVerbs`: real cobra tree → `verbs --dir … reports search "rebalance law" --output json` → top id `…#5-9-the-rebalance-law`, score 12.6758).

### Why
- The relative-root `Base` exists because jsverbs' overlay loader gives modules **virtual** paths. Module path is `/lib/corpus.js`, so `__dirname + "/../corpus"` resolved to `/corpus`. Apps should be relocatable without embedding absolute paths, so roots resolve against the host-known app directory instead.
- The stat-based digest (not content hashes) keeps `digests(ir)` cheap. It is called several times per build (`dirty`, `pending`, `build`). Per-file content hashing already happens in the engine's per-item memo (`hash(item)`).

### What worked
- `ragwb verbs --dir examples/apps/reports list` → 4 verbs (`reports goldens|index|explain|search`).
- `reports search "rebalance law" --k 3` → rank 1 `5. The core's modules › 5.9 The rebalance law` **12.6758**, then 7.3908 and 7.0051 (2.9 s wall).
- `reports index` → files 11 (3 ms) · parse 526 ms · chunks 462 (1065 ms) · ix-text 1143 ms · ix-meta 23 ms.
- `reports goldens` → lexical hit@5 1 / mrr 0.933 / ndcg 0.847; fused 1 / 0.892 / 0.818.
- `reports explain "rebalance law" <id>` → `text` rank 1 12.6758 `{"law":7.08,"rebalanc":5.59}` → filter → limit.
- `parc search "reciprocal rank fusion" --include 'Research/KB/**/*.md'` → rag-ttc textbook series, RAG Evaluation Pipeline Architecture, Bleve guide, goja-bleve (4.7 s).
- Without `--root`: `source.dir: root "/home/manuel/code/wesen/go-go-golems/go-go-parc" is outside the allowed roots [...] (pass --root)`.
- `go vet ./... && go test ./...` → all ok.
- Regression gate after the rag.js regex change: `ragwb presets --dir examples/presets --files examples/apps/reports/corpus` exit 0, 30/30 `ok`. Diffed against the Step 6 sweep with the query-ms column normalized: **identical** (only one alignment-whitespace difference).

Vault build measurements (this machine, commit e1707e9 + this step's changes, goja, single runtime):

| subset | files | md size | chunks | parse | chunk | bm25 | total | max RSS |
|---|---|---|---|---|---|---|---|---|
| `Research/KB/**` | 96 | 834 KB | 1,107 | 0.98 s | 1.79 s | 1.81 s | 4.6 s | — |
| `Research/**` | 572 | 10.7 MB | 11,926 | 12.2 s | 28.2 s | 28.4 s | **69.3 s** | **477 MB** |

### What didn't work
- `ragwb verbs --dir examples/apps/reports reports search …` (first attempt) → `Error: promise rejected: GoError: source.dir: lstat /corpus: no such file or directory`. The cause is the virtual `__dirname` described above. Fixed by `SourceDir.Base` and `root: "corpus"`.
- My sed edit of `lib/corpus.js` put the comment inside the call and dropped `)`: `GoError: SyntaxError: /lib/corpus.js: Line 15:77 Unexpected token ; (and 4 more errors)`. Rewrote the line properly.
- `TestReportsAppVerbs` (first run) → `source.dir: root ".../examples/apps/reports/corpus" is outside the allowed roots [.../cmd/ragwb/cmds ../../../examples/apps/reports]`. **This was a real allowlist bug:** `EvalSymlinks` on a relative allowlist entry returns a relative path, and `filepath.Rel(relative, absolute)` fails, so relative entries never matched. CLI runs only passed because the corpus also sat under the (absolute) cwd entry. Fixed by `filepath.Abs` before `EvalSymlinks`, with a unit test.
- The same test (second run) → `decode "": unexpected end of JSON input`. Glazed writes rows to `os.Stdout`, not cobra's `OutOrStdout`. The test now captures the fd with a pipe.
- **Hypothesis disproven:** I attributed BM25 = 1143 ms (vs. "115 ms" in Step 3) to per-call RegExp compilation in `RE_WORD()`. Caching the regexes changed nothing (1122 ms). Re-running spike 01 with its *own* old rewrite gave ix-text 1141.8 ms, and with the new assets 1607.5 ms. The Step 3 figure came from the broken-tokenizer runs, where nothing was indexed. Corrected the guide (§1 point 3, §7 table, D1 context: "about 5 s/MB") and analysis §3. The regex caching stays as a harmless improvement.

### What I learned
- The real goja cost is roughly parse 1 : chunk 2 : BM25 2, and it grows slightly superlinearly (5.6 s/MB at 0.8 MB, 6.5 s/MB at 10.7 MB). Extrapolated to the 81 MB vault: about 9–10 minutes and around 3–4 GB RSS. **P4 (native parse/chunk) and P8 (persisted Bleve) are required before whole-vault use**, and P8's persistence also matters for queries, because every `search` verb currently rebuilds.
- jsverbs gives modules virtual paths (`/lib/corpus.js`), so file access must go through host-resolved bases, not `__dirname`.

### What was tricky to build
- **Directory exclusion with doublestar.** A pattern like `ttmp/**` matches files *below* `ttmp` but not the directory path `ttmp` itself. The walk therefore tests both `rel + "/"` against the pattern and `rel` against the pattern minus its `/**` suffix, and returns `SkipDir` on either match. Without that, excluded trees (e.g. `.pi/` with 54 skill files) would be walked and stat-ed on every digest.

### What warrants a second pair of eyes
- `source.dir` reads files synchronously on the VM goroutine. That's fine for the CLI, but it blocks a server runtime for about 70 ms per 570 files.
- Two `matches()` walks happen per build (digest + walk), plus extra digest calls from `dirty`/`pending`. A short-lived per-build cache would remove the duplicated stat walks.
- Default-allowing the cwd is convenient for the CLI. The server (P5+) should use explicit roots only.

### What should be done in the future
- P4: goldmark-based `parse.markdown` + `chunk.sections` natives with byte ranges, verified against the JS output on both example corpora.
- P8: persist build outputs so the parc `search` verb doesn't rebuild 69 s per query.

### Code review instructions
- `pkg/natives/sourcedir.go` (`resolveRoot`, `matches`, `Walk`, `Digest`, `sourceDirJS`), `cmd/ragwb/cmds/roots.go`, `examples/apps/*/`.
- Validate: `go test ./...`; `make presets`; `./dist/ragwb verbs --dir examples/apps/reports reports search "rebalance law"`; `./dist/ragwb --root ~/code/wesen/go-go-golems/go-go-parc verbs --dir examples/apps/parc parc index --include 'Research/KB/**/*.md'`.

### Technical details
- `rag.source.dir(o)` params: `root` (required), `include` (string or list, default `**/*.md`), `exclude` (list, added to the defaults), `maxBytes` (default 4194304), `skipHtml` (default true). Node id: `dir`.
- The jsverbs `stringList` field accepts repeated `--include a --include b`.

## Step 8: P4 — Go ports of parse.markdown, chunk.sections and index.bm25 (byte-identical, 28× faster on the vault)

I replaced the three heavy build components with Go ports registered under their **original type names**, so every script and preset gets them without edits. These are faithful line-by-line ports of the prototype's own algorithms, not goldmark or Bleve, because the acceptance bar is that outputs stay identical. Parse and chunk outputs are byte-identical to the JS implementation as JSON across three chunking configurations, on the 11 reports and 144 vault notes. BM25 search results (scores, `why` terms, tie order, synonyms, `searchIn`, `toJSON`) are byte-identical across three index configurations. The tokenizer matches on 20,328 real lines. All 30 presets give identical metrics. The vault's `Research/` subset now builds in 2.5 s instead of 69.3 s, and **the whole vault (2,282 notes, 85,880 chunks) builds in 17.4 s**. Memory (3.47 GB RSS) is now the limiting factor.

Blocks and chunks also gained `range: {byteStart, byteEnd, lineStart, lineEnd}` (API adjustment A2), and a test verifies all 1,168 block ranges in the reports corpus against the source bytes.

### Prompt Context
**User prompt (verbatim):** see Step 6. Mid-step the user added: "we can use embeddings models and reranking from https://docs.lunaroute.com/ and for llm calls, for testing (the same key through pi as the normal hosting)". That is recorded and acted on in Step 9.
**Assistant interpretation:** Execute P4 with parity evidence. BM25 was pulled forward from P8 because after the parse/chunk ports it was 95% of build time.
**Inferred user intent:** Scripts must index real vaults quickly without changing their semantics.
**Commit (code):** see the commit made right after this entry ("RAGWB-001 P4: …").

### What I did
- `pkg/natives/jsstr.go`: JS semantics helpers. `jsWS` (the ECMAScript `\s` set), `jsTrim`, `len16` (UTF-16 length), `tokens` (= ceil(len16/4)), `slice16`, `clip`, `slug`, `jsString`, `jsTruthy`, and `splitSentences` (`split(/(?<=[.!?])\s+/)` without lookbehind, which RE2 lacks).
- `pkg/natives/markdown.go`: `parseScalar`, `parseFrontmatter` (keeps the JS quirk where an unterminated last line yields the whole text as body), `plain`, `ParseMarkdown` with block ranges, an ordered `Meta` (insertion order, deletion of empty lists).
- `pkg/natives/chunk.go`: `ChunkSections` with `splitBlock` (including the JS quirk `tokens(cur.join(' ') + u)`), `blocksText`, `makeChunk` (+ range union), `pickMeta`, `AssignIDs`.
- `pkg/natives/bm25.go`: `Tokenize`/`stem`/stopwords, `NewBm25`, `Search` (stable sort on Map insertion order; `why` pre-ordered with array-index keys first because JS plain objects enumerate them first), `SearchIn`, and `jsToFixed`, which rounds the exact decimal expansion with ties away from zero.
- `pkg/natives/markdown_native.go` and `bm25_native.go`: `rag.define` with explicit `id: () => 'parse' | 'chunk' | 'index'`. Go → JS builds real JS objects in prototype key order. JS → Go reads docs back, with a non-enumerable `__go` handle on natively parsed docs as a fast path. BM25 field texts are extracted in JS (exact `getPath`/join/JSON rules) in one pass.
- `runner.Config.JSComponents` and CLI `--js-components` keep rag.js's implementations (parity tests, escape hatch).
- Tests: `pkg/runner/markdown_parity_test.go` (JS vs Go JSON with `range` stripped; opt-in `RAGWB_PARITY_INCLUDE` / `RAGWB_PARITY_TIMEOUT` for long runs with per-item hashes), `pkg/runner/bm25_parity_test.go`, `pkg/runner/tokenize_parity_test.go`, `pkg/natives/markdown_test.go` (range invariants, frontmatter), `pkg/natives/bm25_test.go` (`jsToFixed` vs goja's `toFixed` on 400k random values + exact ties).

### Why
- A goldmark-based parser would segment blocks differently. Chunk ids, headings and scores would all shift, breaking every preset, eval and textbook number. A faithful port keeps semantics, and the parity tests make "faithful" checkable.
- I ported BM25 ahead of Bleve (P8) because parity is provable and it kills the dominant cost now. Bleve remains the persistence and highlighting path.

### What worked
- `go test ./pkg/runner -run 'Bm25Parity|MarkdownParityReports' -v`:
  - parse/chunk identical: 926,670 / 1,239,443 / 1,977,082 bytes of chunk JSON (three configs), JS 2.3–2.6 s vs Go 27–49 ms.
  - BM25 identical: 156,011 / 156,055 / 153,384 bytes of results, full build JS ~4.0 s vs Go 67–96 ms.
- `TestMarkdownParityVault` (`Research/KB/**` + `Logs/**`, 3 configs): identical; JS 5.1–5.3 s vs Go 101–130 ms.
- `TestTokenizeParity`: 20,328 lines identical (stemmed and unstemmed). `TestToFixedMatchesGoja`: pass. `TestBlockRangesPointAtSource`: 1,168 ranges verified.
- Presets sweep with all Go ports: 30/30 ok and **identical** to the P3 sweep (query-ms normalized). Wall time 133 s → 47 s.
- `ragwb --js-components verbs --dir examples/apps/reports reports index` vs. without: parse 754 → 15 ms, chunks 1853 → 11 ms, ix-text 1678 → 67 ms.
- Vault (this machine; a parity test was running concurrently, so these timings are pessimistic):

| run | files | chunks | parse | chunk | bm25 | terms | total | max RSS |
|---|---|---|---|---|---|---|---|---|
| `Research/**`, Step 7 (all JS) | 572 | 11,926 | 12,155 ms | 28,173 ms | 28,443 ms | 426 ms | 69.3 s | 477 MB |
| `Research/**`, Go parse+chunk only | 572 | 11,926 | 386 | 272 | 28,530 | 544 | 29.9 s | 587 MB |
| `Research/**`, all Go ports | 572 | 11,926 | 366 | 293 | 1,138 | 551 | **2.5 s** | 534 MB |
| **whole vault**, all Go ports | 2,282 | 85,880 | 2,631 | 2,121 | 8,761 | 3,382 | **17.4 s** | **3.47 GB** |

- `parc search "goja module provider xgoja"` over the whole vault (rebuild + query, 22 s) → go-go-goja Protobuf Builders, xgoja provider architecture, XGoja third-party provider rollout, "Designing DSLs with go-go-goja", …

### What didn't work
- First whole-vault parity run: `markdown_parity_test.go:87: runtimeowner parity: runtime call canceled: context deadline exceeded` after 300 s. The JS side of the build exceeded the test's own 5-minute context, so the result is inconclusive, not a mismatch. Fixed by `RAGWB_PARITY_TIMEOUT` and per-item hash comparison for opt-in runs (full JSON of 81 MB would need gigabytes of UTF-16 strings). The rerun is in progress; its result goes in Step 9.
- `TestTokenizeAndStem` (my first unit test) failed: `got [rebalanc law get httprespons gethttprespons snak case name snakecasenam index run v2 api v2api]`. My **hand-written expectation** was wrong: I'd assumed `getHTTPResponse` stays one token and `running` → `runn`. The real rules split at lower→upper boundaries only (`get` | `HTTPResponse`), and `running` loses `ing` (length > 6), then the doubled `n`, giving `run`. I replaced the guesses with JS-derived ground truth (`TestTokenizeParity`).

### What I learned
- After the ports, the whole vault fits in about 17 s of build. The remaining costs are the JS field-text pass for BM25 (inside ix-text), JS `index.terms` (3.4 s), and **memory**: every doc and chunk lives as JS objects plus the hidden Go `__go` structs. That's API adjustment A6 (artifact accessors) and P8 (persisted indexes).
- goja's `Number#toFixed` (its own `ftoa`) matches the ECMAScript tie rule: half away from zero on the exact binary value. Go's `strconv` would round half to even, so I wrote `jsToFixed`.

### What was tricky to build
- **`define()` vs the built-ins.** `rag.define` assigns `d.id = def.id || (() => type.split('.').pop())`. The op layer (`rag.js:1186`, inserting nodes) names new nodes `def.id(p) || family`. Without explicit ids, an agent inserting `index.bm25` would get node id `bm25` instead of `index`. All natives now pass `id: () => '<family>'`. Corpus index ids (`'ix-' + name`, `rag.js:880`) were never affected.
- **Ordering semantics, three of them:** JS `Map` insertion order for scores (first contributing term, then posting order); `Array#sort` stability; plain-object enumeration with array-index keys first (affects which 5 `why` terms survive ties). Each one would have produced rare, data-dependent diffs. The byte-level parity tests are what make me confident they're all handled.
- **FMA.** The Go spec allows fusing `x*y+z`. The score expression wraps products in `float64(...)`, which the spec says forces rounding, so results match goja's (non-fused) arithmetic on any architecture.

### What warrants a second pair of eyes
- `docFromJS` trusts the `__go` handle. A JS lambda that mutates a parsed doc *in place* between parse and chunk would be ignored by the Go chunker. Copies (`{...doc}`) correctly fall back to the reader.
- `jsToFixed` uses `big.Float` with 1100 digits per call. That's fine for ≤ k hits × 5 terms per query, but it's not free in a tight loop.
- RSS went up (477 → 534–587 MB on `Research/`) because docs are held twice. Revisit with A6.

### What should be done in the future
- Step 9: record the whole-vault parity result. LunaRoute embeddings/rerank/LLM (P8/P9).
- Port `index.terms` (3.4 s on the vault) the same way, or back it with SQLite.
- Persist build artifacts (P8) so query verbs don't rebuild for 17 s.

### Code review instructions
- Read `pkg/natives/markdown.go` side by side with `pkg/ragjs/assets/rag.js` lines ~193–293 (parse) and ~306–360 (chunk). Read `bm25.go` with rag.js ~155–190 (tokenize/stem) and ~373–445 (Bm25Index).
- Validate: `go test ./...`; `make presets`; `RAGWB_PARITY_INCLUDE='**/*.md' RAGWB_PARITY_TIMEOUT=45m go test ./pkg/runner -run TestMarkdownParityVault -v -timeout 100m`.

### Technical details
- Range: half-open bytes in the original file; 1-based inclusive lines. Fences include their marker lines. Chunk range = union of its blocks' ranges. Synthetic heading paragraphs (deeper sections folded into a group) have no range.
- Native index object: `{kind: 'bm25', N, fields, search(q, k = 20, {synonyms, only}), searchIn(docId, q, k = 3, {synonyms}), toJSON()}`.

## Step 9: LunaRoute as the LLM, embedding and rerank provider — research, probes, and a credential slip

The user asked to use LunaRoute for embeddings, reranking and LLM calls, with the key Pi already uses. I read the LunaRoute docs, found the key in Pi's config, listed the available models, and probed each endpoint once with a minimal request. That way the client is written against observed responses rather than undocumented assumptions. I also fixed the formatting slip from Step 8.

There was one mistake with consequences. While locating the key I printed a redacted view of `~/.pi/agent/auth.json`. My redaction matched key *names* containing `key|token|secret|auth`, but that file stores the LunaRoute OAuth credential under `access`, so **the token value appeared in this session's local tool output**. It was not written to any file, committed or sent anywhere. All later access reads the key inside the program and prints only metadata. The user was told and may want to rotate it.

### Prompt Context
**User prompt (verbatim):** "we can use embeddings models and reranking from https://docs.lunaroute.com/ and for llm calls, for testing (the same key through pi as the normal hosting)" and "don't forget the dairy".
**Assistant interpretation:** Make LunaRoute the backend for `ctx.sample` (P9), embeddings and rerank (P8), with credentials taken from Pi's existing config. Keep the diary current at every step.
**Inferred user intent:** Test real LLM/embedding behavior now without new accounts or keys.
**Commit (code):** e434fb6 — "RAGWB-001: gofmt bm25 files; make lint checks gofmt" (the research itself has no code).

### What I did
- Fetched `https://docs.lunaroute.com/`, `/api/embeddings-rerank/`, `/models/` and `/api/chat-completions/`. Base URL `https://gw.lunaroute.com`, header `LUNAROUTE-API-KEY`. Endpoints: `POST /v1/chat/completions` (OpenAI-style), `/v1/messages` (Anthropic), `POST /v1/embeddings` (TEI's OpenAI-compatible body, 2 MiB cap, no truncation), `POST /v1/rerank` (Cohere v2), and listings `GET /v1/models`, `/v1/embeddings/models`, `/v1/rerank/models`.
- Key location: `~/.pi/agent/models.json` → `providers.lunaroute` = `{baseUrl: "https://gw.lunaroute.com/v1", api: "openai-completions", apiKey: <67 chars>}`. Pi's `auth.json` also has an OAuth `lunaroute.access` token (the one exposed). The client will prefer `$LUNAROUTE_API_KEY`, then `models.json`.
- Listings (embeddings policy `included`):

| task | id | dims | max tokens | batch/docs |
|---|---|---|---|---|
| embed | `emb-granite` | 768 | 32768 | 64 |
| embed | `emb-qwen3` | 1024 (dimensions settable) | 32768 | 64 |
| embed | `emb-nomic-code` | 3584 | 32768 | 64 |
| embed | `emb-nomic-moe` | 768 (dimensions settable) | 512 | 64 |
| rerank | `bge-rr-v2-m3` | — | 8192 | 99 docs |

  Chat ids: `clef-flash, deepseek-4.1-flash(-background), djev, glm-5.3(-background), glm-5.3-flash(-background), glm-5.3-vision(-background), muse-spark-open(-background), whisper-large-v3`.
- Probes (one request each, key read inside Python, output scrubbed):
  - chat + tools (`glm-5.3-flash`, `max_tokens` 300) → 1.49 s, `finish_reason: "tool_calls"`, `message.tool_calls[0] = {id, type: "function", function: {name: "search", arguments: "{\"query\": \"rebalance law\"}"}}`, separate `message.reasoning_content`, `usage.reasoning_tokens: 23`.
  - plain chat with `max_tokens` 50 → `finish_reason: "length"`, **empty `content`**: the budget was spent on `reasoning_content`.
  - embeddings (`emb-granite`, 2 inputs) → 0.67 s, `data[i] = {object: "embedding", index, embedding: [768 floats]}`, `usage.prompt_tokens: 10`.
  - rerank (`bge-rr-v2-m3`, 2 docs) → 0.45 s, `results: [{index: 0, relevance_score: 0.9896}, {index: 1, relevance_score: 1.6e-05}]`, `meta.billed_units.search_units: 1`.
- gofmt: `gofmt -l .` in the Step 8 commit chain listed `pkg/natives/bm25.go` and `bm25_native.go`, but `&&` only guarded `go vet`, so the commit went ahead. Formatted them in e434fb6, and `make lint` now fails when `gofmt -l` is non-empty.

### Why
- The listings and probes replace guesses. The response fields (`tool_calls`, `reasoning_content`, `finish_reason`) aren't documented on the chat page.

### What worked
- All four endpoints answered with the documented header and lr_ key on the first try. Latencies are 0.45–1.5 s.

### What didn't work
- The redaction described above (security slip). Root cause: redacting by key-name pattern instead of by value. Prevention: never pretty-print credential files. Read the needed field programmatically, and print only lengths or presence.
- `/models/` and `/api/chat-completions/` docs don't list model ids, tool format or response schema. Resolved via the listing endpoints and probes.

### What I learned
- GLM models reason by default, and reasoning consumes `max_tokens`. The `quick` tier needs either a thinking-off control or a generous token budget. `finish_reason: "length"` must map to `truncated: true`, and empty content after reasoning must not be returned as an answer (`empty_completion`).
- `-background` model variants exist for work that can wait (bulk enrichment, synthetic evals).

### What was tricky to build
- N/A (research step). The tricky part ahead is resolving JS promises from HTTP goroutines. `runtimebridge.Lookup(vm).PostWithLifetimeContext` is the path, since native loaders only receive `vm`.

### What warrants a second pair of eyes
- Credential handling in the coming `pkg/llm`: env first, then `models.json`. Never log it, and never put it in error messages (HTTP error bodies are truncated and scrubbed).

### What should be done in the future
- P9: `pkg/llm` (Backend interface, LunaRoute + fake), `env.sample` / `sample.json` in the ragwb host, tier → model config. Then the "With Claude" presets run headless.
- P8: `embed` + `index.vector` + `rerank.model` components on emb-granite / bge-rr-v2-m3.

### Code review instructions
- No code in this step beyond e434fb6 (`Makefile` lint target).

### Technical details
- Rerank request: `{model, query, documents[], top_n, return_documents}`. A long query reduces each document's share of the 8192-token pair limit.
- Embeddings: unknown fields are rejected, so send only `model`, `input`, and optionally `dimensions` / `encoding_format`.

## Step 10: P9 — the claude.ai `sample` contract on LunaRoute; LLM nodes, tool loops and build-time enrichment run headless

I implemented `pkg/llm`: the sample contract types, a LunaRoute/OpenAI-compatible client with a bounded tool loop, embeddings and rerank clients, and a deterministic fake. I then bridged it into the `ragwb` module as `env.sample`, so rag.js's own `ctx.sample`/`ctx.sample.json` work unchanged. Every LLM component in the prototype now runs from the CLI against real models:

- the reports preset's rewrite → rerank.llm → answer (3 calls, 11 s, correctly cited);
- the agentic preset's tool loop (the model calls the JS `search`/`read` tools, 10 s, correct root cause with two citations);
- doc-cards' build-time `enrich.llm` (11 summaries in 21.8 s with `--build-calls`).

All 30 presets also run with `--trigger submit` on the fake backend without errors.

### Prompt Context
**User prompt (verbatim):** see Step 9 (LunaRoute for embeddings/rerank/LLM, the key via Pi).
**Assistant interpretation:** Make LLM nodes executable headless against LunaRoute, keeping the claude.ai `sample` semantics the prototype is written against.
**Inferred user intent:** Test the real LLM behavior of pipelines, not just lexical retrieval.
**Commit (code):** see the commit right after this entry ("RAGWB-001 P9: …").

### What I did
- One more probe before coding: can GLM's reasoning be switched off for the `quick` tier? `thinking: {type: "disabled"}`, `chat_template_kwargs: {enable_thinking: false}` and `reasoning_effort: "none"` are all **accepted but ignored**: each still spent all 60 tokens on `reasoning_content` and returned empty content (`finish_reason: length`). A model comparison for a 12-character JSON reply: `muse-spark-open` 0.58 s (16 reasoning tokens), `deepseek-4.1-flash` 3.15 s (no reasoning), `glm-5.3-flash` 2.37 s (131 reasoning tokens), `glm-5.3` 20.8 s. `clef-flash` → HTTP 400 `CAPABILITY_UNSUPPORTED` (a System One decision model).
  - Tier defaults (overridable with `--llm-quick/--llm-default/--llm-complex`): quick `muse-spark-open`, default `glm-5.3-flash`, complex `glm-5.3`. `max_tokens` budgets 2048 / 8192 / 16384, because reasoning shares the budget.
- `pkg/llm/llm.go`: `Turn`, `Tool`, `ToolCall`, `ToolResult`, `Request`, `Result{text, truncated, modelTierApplied, modelApplied}`, `Failure{code, message, text}`. `NormalizeTurns` (user first and last, same-role merge, no system role). `ParseJSON` (whole reply → code fence → first `{`/`[` … last `}`/`]`). `MaxInputBytes` = 256 KiB.
- `pkg/llm/lunaroute.go`: `LoadLunaRouteKey` (`$LUNAROUTE_API_KEY`, then Pi `models.json`, never logged). `Client.Sample` (OpenAI chat with tools; rounds separated by a blank line for `onText`; the last round is forced to answer without tools; `finish_reason: length` → `truncated`; empty content → `empty_completion` with a note when reasoning ate the budget). `Embed` (batches of 64, empty strings replaced by a space since the API rejects them, reordered by `index`). `Rerank` (Cohere v2). HTTP errors are mapped to sample codes, and the key is scrubbed from bodies.
- `pkg/llm/fake.go`: deterministic answers. It calls the first offered tool once, and JSON-ish prompts get JSON.
- `pkg/ragjs/sample.go`: the JS half normalizes input, wraps tools (`Promise.resolve().then(execute)`, so throwing tools become `{value, isError}` tool results), and parses JSON with `JSON.parse`. The Go half runs `backend.Sample` on a goroutine and settles the promise with `runtimebridge.Lookup(vm).PostWithLifetimeContext`. Tool calls are posted to the loop, their (possibly async) results are awaited through `then`, and they're handed back on a channel. `onText` is posted fire-and-forget.
- `ragjs.Options{Sample, BuildCalls}` → `rag.configure({sample, buildCalls})`. `runner.Config{LLM, BuildCalls}`. CLI: `--llm none|fake|lunaroute`, `--llm-quick|default|complex`, `--build-calls`.
- Tests: `pkg/ragjs/sample_test.go` (through rag's own `engine.sampler`: string, turns, fenced `sample.json`, tool input passed through, throwing tool tolerated, `invalid_request` as a plain object, `onText`). `pkg/llm/llm_test.go` (ParseJSON, NormalizeTurns, plus opt-in live tests under `RAGWB_LIVE_LLM=1`).

### Why
- I kept the sample contract rather than exposing geppetto/OpenAI directly to scripts. rag.js's LLM nodes and the workbench agent are written against it. The same Go `Backend` will serve the browser's `/_rt/sample` in P5, slop-computer style.
- I chose a direct HTTP client over geppetto for this slice: LunaRoute is OpenAI-compatible, the tool loop is about 60 lines, and the probes pinned down the exact response shape. Geppetto profiles/engines can implement `llm.Backend` later. This deviates from guide §9.6 and is recorded here as a proposed amendment.

### What worked
- `go test ./pkg/ragjs -run Sample` → pass. `RAGWB_LIVE_LLM=1 go test ./pkg/llm -run Live -v`: tool loop `rounds=2 usage={PromptTokens:362 CompletionTokens:66 ReasoningTokens:52} text="vermilion"` (1.4 s). Embeddings 768-d, cos(related) 0.945 vs cos(unrelated) 0.744. Rerank `[{Index:1 Score:0.256} {Index:0 Score:1.6e-05}]`.
- `ragwb --llm lunaroute run examples/presets/reports.js --files examples/apps/reports/corpus --trigger submit --q "What does the rebalance law say?"` → calls 3, 11.3 s wall. Trace: rewrite 1657 ms (1→4 queries), text/meta/failures_2 retrieval, fuse 3→41, rerank 4771 ms (41→1), neighbors, pack, answer 2247 ms. The answer explains `preservesPlacements`/`planRebalance` with `[E1]` citations.
- `ragwb --llm lunaroute run examples/presets/agentic.js … --q "Why did the browser ECS service restart in a loop after the shared-image deploy?"` → 10.3 s. The answer identifies the Go typed-nil verifier, citing [E1] and [E2].
- `ragwb --llm lunaroute --build-calls run examples/presets/doc-cards.js … --q "customer segmentation"` → `enrich_llm` 11 items in 21,806 ms, `pending: []`. Top 3 hits come from the TTC customer clustering report.
- `ragwb --llm fake presets --trigger submit` → 30/30 ok, no errors.
- `make lint` and `go test ./...` → all ok.

### What didn't work
- The thinking-off controls (above) have no effect on LunaRoute's GLM. Worked around with tier budgets and model choice.
- No other failures in this step.

### What I learned
- The agentic trace still reports `calls 1` for a multi-round tool loop. That's the prototype's known accounting gap: `engine.sampler` counts one call per `sample()`. `llm.Result.Rounds` and `Usage` now exist on the Go side but aren't surfaced to rag's counter yet.
- `doc-cards` with `--build-calls` produced correct results immediately. The prototype's "stale index after Build" bug is a workbench-session effect (deferred, then built later in the same engine), which a fresh headless build doesn't hit.

### What was tricky to build
- **Promises across goroutines.** Native loaders only receive `vm`. `runtimebridge.Lookup(vm)` provides the owner services, and every touch of the VM from the HTTP goroutine (settle, `onText`, tool calls) goes through `PostWithLifetimeContext`. Tool results can themselves be promises (an async `execute`), so the posted closure calls the returned value's `then` with Go callbacks that write to a buffered channel. The goroutine then selects on that channel and on `ctx.Done()`.
- **Thrown vs. failed.** The prototype's agentic tools *throw* to tell the model about bad ids and budget limits. Those must become tool results the model can read. Only transport and backend errors reject the `sample()` promise.

### What warrants a second pair of eyes
- If the runtime closes while an HTTP call is in flight, the settle `Post` fails silently. That's fine for the CLI, but the server should cancel in-flight calls on session close.
- No response caching yet. The claude.ai platform caches identical calls for 5 min by default, and rag.js passes `cache: {gcTime}` for `enrich.llm`. Repeated headless builds therefore re-spend calls. An item cache (`env.cache`, an engine hook) or a request cache belongs with P8 persistence.
- The tier models are my choice from one tiny probe each. Answer quality per tier hasn't been evaluated.

### What should be done in the future
- Surface `Rounds`/`Usage` in run reports. Add a request cache.
- P8: `embed` + `index.vector` + `rerank.model` components on `emb-granite` / `bge-rr-v2-m3`, with persistence.
- Amend guide §9.6 / D4 to record the direct-HTTP Backend (geppetto optional).

### Code review instructions
- `pkg/ragjs/sample.go` (JS half: `sampleJS`; Go half: `newSample` → `exec`, settle goroutine), `pkg/llm/lunaroute.go` (`Sample` loop, `post` error mapping), `pkg/llm/llm.go` (`ParseJSON`).
- Validate: `go test ./...`; `RAGWB_LIVE_LLM=1 go test ./pkg/llm -run Live -v`; `./dist/ragwb --llm lunaroute run examples/presets/reports.js --files examples/apps/reports/corpus --trigger submit --q "What does the rebalance law say?"`.

### Technical details
- The JS-visible contract: `sample(input, {modelTier, model, onText({text, delta}), tools[{name, description, inputSchema, execute(input, {signal})}], signal})` → `{text, truncated, modelTierApplied, modelApplied?}`. `sample.json(...)` → the parsed value or `{code: 'invalid_json', text}`. `sample.limits()` → `{tools: {maxCount: 16}}`.

## Step 11: Whole-vault parity finds one JS quirk; P8a dense retrieval and cross-encoder rerank on LunaRoute

This step has two parts. First, the whole-vault parity run (opt-in, all 2,282 notes) found a real divergence the subset tests had missed. The cause was a quirk in rag.js's token counting, which I now replicate. After the fix, every doc and chunk in the vault is byte-identical between JS and Go, and the JS build takes 280 s against 4.4 s for Go. Second, I added the first P8 slice: `index.vector` (LunaRoute `emb-granite` embeddings with a persistent SQLite cache) and a `rerank.model` live node (`bge-rr-v2-m3`). With them the reports app gains semantic, hybrid and hybrid+rerank plans. **Hybrid (BM25 + vectors, RRF) is the best plan on the 12 goldens: MRR 1.0, nDCG@10 0.915, against lexical 0.933 / 0.847.**

### Prompt Context
**User prompt (verbatim):** see Step 9 (LunaRoute embeddings/rerank) and Step 6 (commit cadence, diary).
**Assistant interpretation:** Finish validating P4 on the full vault, then build embeddings and rerank into the grammar as components.
**Inferred user intent:** Better retrieval for the vault apps, measured rather than assumed.
**Commit (code):** see the commit right after this entry ("RAGWB-001 P4 fix + P8a: …").

### What I did
- Whole-vault parity, first run: `RAGWB_PARITY_INCLUDE='**/*.md' RAGWB_PARITY_TIMEOUT=45m go test ./pkg/runner -run TestMarkdownParityVault -v -timeout 100m` → FAIL after 1034 s: `chunks differ at byte 5744886` in `transcripts-2026-07-21-chatgpt-transcript-branch-interpreter-con#12-immutable-artifacts-and-defensive-copies`. **Docs were identical** (they're compared first), so the problem was in chunking.
- Narrowed it with the new `RAGWB_PARITY_FULLJSON=1` (full JSON on opt-in runs) plus `RAGWB_PARITY_INCLUDE='Transcripts/2026/07/21/CHATGPT TRANSCRIPT - Branch Interpreter Con*.md'`: JS `"parts":13`, Go `"parts":12` for the same group. A throwaway debug test (deleted afterwards) dumped part sizes: JS p2 436 + p3 83 tokens vs Go p2 **470** tokens, which is over `maxTokens` 450.
- Root cause: rag.js `tokens(t)` for block arrays is `t.map(b => (b && b.text) || b).join('\n')`. A block whose `text` is `''` (an **empty code fence**) is falsy-coalesced to the block *object*, which joins as `"[object Object]"` (15 characters). Go counted 0, so Go packed one more block into the part. Fix: `blocksTokens` substitutes `"[object Object]"` for empty texts, with a comment; regression test `TestBlocksTokensEmptyTextQuirk`.
- Whole-vault parity, second run → **PASS**: `9514966 bytes of chunks identical; build js=279822ms go=4357ms` (825 s total).
- P8a:
  - `pkg/natives/embcache.go`: SQLite cache, table `embeddings(model, dims, hash = sha256(text)[:16], vec BLOB float32 LE)`, at `~/.ragwb/cache/embeddings.db` (`$RAGWB_CACHE_DIR` overrides). WAL mode, batched `IN` lookups.
  - `pkg/natives/vector.go`:
    - `index.vector({model = 'emb-granite', over = ['title', 'headingPath', 'text'], dimensions})` embeds the joined fields of each chunk, misses only, with 4 concurrent batches of 64, and L2-normalizes. Exact cosine search; the query is embedded synchronously and cached per index.
    - `rerank.model({model = 'bge-rr-v2-m3', k = 30, keepTail})` is a `submit`-trigger live node. It reranks the head k hits with the cross-encoder, sets `score` and `reason: 'cross-encoder …'`, and keeps the tail unless `keepTail: false`.
    - `rag.vector(...)` is a late-bound shortcut.
  - `runner.Config{Embedder, EmbedCache}`; CLI `--embed none|lunaroute`. Without a backend, a build fails with `index.vector: no embedding backend (run with --embed lunaroute)`.
  - Examples: `reports` gains `corpusDense` and plans `semantic`, `hybrid`, `hybrid-rerank`; `search --plan … --trigger`; `goldens --names … --trigger`; `index --dense`. `parc` gains `corpus(include, dense)`, `plan(include, mode)` and `search --mode lexical|semantic|hybrid`.
  - Tests `pkg/runner/vector_test.go`: a deterministic bag-of-words embedder and term-overlap reranker. The index is built through rag.js; the `submit` run applies `rerank.model`; and **a cached rebuild in a fresh runtime makes exactly one embed call** (the query). A test also checks the no-backend hint.

### Why
- I kept vectors as rag components (`index.vector`, `rerank.model`) rather than a separate API (D2), so plans compose dense and lexical retrieval with the existing `fuse.rrf`, `filter` and `limit`, and the inspector and evals see them.
- The persistent cache is what makes vector indexes usable from short-lived CLI verbs.

### What worked
- Reports: first `index --dense` embedded 462 chunks in **2,457 ms** (wall 2.6 s); the second took **24 ms** (wall 0.24 s, cache). `sqlite3 … "select model, dims, count(*) …"` → `emb-granite|0|462`.
- Semantic vs lexical on "how do agents get notified about new posts": semantic finds Agentforum "What was built" (the unified event inbox), while lexical's top hit is an unrelated PBUI section.
- Goldens (12 cases, `goldens --names …`, 34.6 s wall for both commands):

| plan | trigger | hit@5 | MRR | nDCG@10 |
|---|---|---|---|---|
| lexical | change | 1 | 0.933 | 0.847 |
| fused (text+meta) | change | 1 | 0.892 | 0.818 |
| semantic | change | 1 | 0.917 | 0.833 |
| **hybrid** | change | 1 | **1.000** | **0.915** |
| hybrid | submit | 1 | 1.000 | 0.915 |
| hybrid-rerank | submit | 1 | 1.000 | 0.861 |

- `go test ./pkg/runner -run Vector` → pass.

### What didn't work
- The first parity run's failure is above (it was real).
- `reports index --dense` piped through `grep` first printed nothing. I'd passed `-v`, which is a flag of `run`, not a global flag, so the command errored inside the pipe. Reran without it.
- `TestVectorIndexAndRerankModel` (first run) failed: `reranked top = [7. Phases 3–7: … 20. Primary source material › CoinVault Testing and validation]`. **My fake was bad**: 64 hash buckets collide too much for cosine, and the fake reranker's length normalization favoured short chunks. With 1024 buckets and a plain overlap score it passes. The components were fine.
- My first edit of `examples/apps/parc/lib/corpus.js` (Python string replace) added the vector index unconditionally plus a meaningless placeholder fragment. Caught on read-back and rewritten.

### What I learned
- The cross-encoder *lowered* nDCG on the goldens (0.915 → 0.861) with MRR unchanged. With 12 questions this could be noise. It could also mean `bge-rr-v2-m3` with the title + heading + text input format reorders within the relevant set. It needs a larger golden set before concluding anything (the prototype textbook notes the same measurement weakness).
- The empty-fence quirk shows why the parity bar needs the whole vault: 1 group in 85,880 chunks diverged, and no subset I'd chosen contained it.

### What was tricky to build
- **Finding a 1-in-85,880 divergence.** Hash-mode parity gives the id but no content. The FULLJSON switch plus an include glob over the three files sharing the truncated id prefix reduced it to a part-size dump. The 470 > 450 token part was the tell: Go believed the block fit because it under-counted.

### What warrants a second pair of eyes
- `index.vector` blocks the VM goroutine during corpus embedding (minutes for the vault) and during query embedding (~0.3–0.7 s on a cache miss). That's fine for CLI verbs, but the server needs per-session runtimes or an async index API (`await ix.search(...)` in `retrieve` would need a small engine change).
- Vectors live in memory as float32: 85,880 × 768 × 4 B ≈ 264 MB for the vault, on top of the JS artifacts.

### What should be done in the future
- Record the whole-vault dense build (running at the time of writing) in Step 12.
- Grow the golden sets (synthetic evals via `rag.evals.generate` on LunaRoute) before tuning rerank.
- P8b: persist BM25/terms/vector artifacts by node digest (A5) so query verbs don't rebuild.

### Code review instructions
- `pkg/natives/chunk.go` `blocksTokens`; `pkg/natives/vector.go` (`vectorsJS`, `embedAll`, `search`); `pkg/natives/embcache.go`; `examples/apps/reports/lib/plans.js`.
- Validate: `go test ./...`; `./dist/ragwb --embed lunaroute verbs --dir examples/apps/reports reports goldens --names lexical,semantic,hybrid`.

### Technical details
- Embedded text per chunk = `[title, headingPath.join(' › '), text].filter(Boolean).join('\n\n')`. Rerank document text uses the same composition.
- LunaRoute limits respected: 64 inputs per request, max 99 docs per rerank (`k` capped at 99), and empty strings replaced by `" "`.

## Step 12: P5 — the workbench served from Go with a local window.claude runtime; the agent runs on LunaRoute

The ported workbench now runs from `ragwb workbench`: the claude.ai page (unchanged `index.html`, `app.js`, `agent.js`), the engine files served from `pkg/ragjs` (so browser and server run identical engine code), and a local `window.claude` runtime. That runtime provides `sample` over SSE with **page-executed tools** (the slop-computer protocol), `db` (a Firestore-style document store in SQLite), `user` and `downloads`. In a real browser, a preset builds, Ask produces a cited answer, and the agent completes a 9-step tool session on LunaRoute that reproduces the original session's analysis of the one golden miss. I also verified the whole-vault dense build, fixed a tool-markup leak in the tool loop that browser testing exposed, and made `invalid_json` failures self-describing after an intermittent rerank failure.

### Prompt Context
**User prompt (verbatim):** see Step 4 (turn 1: "a version where I can create and save scripts with an agent in the workbench (by porting the UI, you can look at … slop-computer … on how to implement the claude sample and other apis to serve the app)") and Step 9 (LunaRoute).
**Assistant interpretation:** Serve the prototype from Go with the capabilities it expects, LLM on LunaRoute, tools still in the page.
**Inferred user intent:** Use the workbench and its agent locally, against a real backend, as the place where scripts get written.
**Commit (code):** see the commit right after this entry ("RAGWB-001 P5: …").

### What I did
- `web/workbench/` (embedded via `web/embed.go`): the prototype's `index.html`, `app.js` and `agent.js` verbatim, plus a new `claude.js` shim:
  - `db.doc/collection` → `POST /_rt/db/{get,set,delete,list}`;
  - `user.id()` → `"local"`;
  - `downloads.save` → a Blob link;
  - `sample` → `POST /_rt/sample`, SSE parse; on a `tool` event it runs `execute` in the page and POSTs `/_rt/sample/{call}/tools/{use}`; `sample.json` and `sample.limits` included.
- `pkg/server/`:
  - `server.go`: ServeMux with Go 1.22 patterns. Index with `<script src="/_rt/claude.js">` injected after the first `<head>`, engine assets from `ragjs.Source`, `/corpus.json` built from `--corpus`, `/presets/{file}` from `--presets` (basename-only, `.js`/`index.json`), db RPC, `/healthz`.
  - `sample.go`: `call` → `text` → `tool` → `result` | `error`. Tool calls are parked on a per-call channel with a 3-min timeout, cancellation via `/cancel` or client disconnect, and `json: true` parsed server-side.
  - `docs.go`: SQLite `docs(path PK, json, updated)`; `List` returns direct children only; 256 KiB per-document cap like the platform.
- `cmd/ragwb workbench --addr 127.0.0.1:8790 --presets examples/presets --corpus examples/apps/reports/corpus --db ~/.ragwb/ragwb.db` (uses the global `--llm`).
- `pkg/server/server_test.go`: a Go client plays the page (SSE + tool POST); JSON and error paths; 503 without an LLM; db semantics (direct children only); assets (shim injected first, patched `rag.js`, 11-file corpus, presets, path traversal → 404).
- Tool-loop fixes in `pkg/llm/lunaroute.go`: `DefaultMaxRounds` 6 → **10** (flag `--llm-max-rounds`). On the forced final round a user note says the tool budget is spent. `stripToolMarkup` cuts `<tool_call>`/`<|tool_call|>`/`<function_call>` from text; test `TestStripToolMarkup`.
- `llm.InvalidJSONMessage`: `invalid_json` errors now include an excerpt of the reply and say whether it was cut off (used by the headless bridge and the server).

### Why
- Serving rag.js/gui.js/kit.js from `pkg/ragjs` rather than a copy keeps one engine source for browser and goja (the D6 regex fix is feature-detected, so the browser keeps `\p{L}`).
- Presets and corpus are files on disk, not embedded. That's the first step toward D7 (the apps directory is the source of truth).

### What worked
- `go test ./pkg/server/ -v`: 4/4 pass.
- Browser (Playwright, Chromium) against `ragwb --llm lunaroute workbench`:
  - Page boots: status "Built · 11 docs · 490 chunks · 125 ms", the default query gives "46 results · lexical · 35.9 ms". The only console error is `/favicon.ico` 404.
  - **Ask** → cited answer (stateful vs. sessionless MCP transport, [E1] [E2], "2 of 18 sources cited").
  - **Agent**, suggestion "Which golden questions does the current plan miss, and why? Show the ranks." (Balanced = `glm-5.3-flash`, Auto mode): 9 eval steps, 125 s. Generative-UI tables show golden ranks (11/12, miss at rank 9) and the want-chunk's stage positions (text 5 → fuse 9 → neighbors 10). The final answer has three causes and a `wb:query` link. No leaked markup. Screenshot: `various/p5-workbench-agent-lunaroute.png`.
- Whole-vault dense index (`parc index --dense` with `--embed lunaroute`, started in Step 11): 85,881 chunks; ix-vec 944,222 ms (15.7 min, 4 concurrent batches); total 961 s; max RSS 3.76 GB. The cache now holds `emb-granite|85393` vectors.

### What didn't work
- **First agent run: leaked tool markup.** The final paragraph read `path ranks are null here; let me check res.trace for per-node outputs instead.<tool_call>eval<arg_key>code</arg_key><arg_value>const q = …`. The agent hit `MaxRounds` (6). My forced final round removed `tools`, and GLM still wanted a tool, so it emitted its native call syntax as content. Fixed by the three measures above; the re-run ended cleanly.
- **Intermittent `rerank: the reply contained no complete JSON value`** in the browser's Ask flow. CLI reproduction: 1 of 3 runs failed (`rerank error 13322.2ms 50→0`), then 5 of 5 passed after the message change. A direct probe (9 calls; `muse-spark-open`, `glm-5.3-flash`, `deepseek-4.1-flash` with a rerank-style prompt over 20 passages) returned valid JSON every time. Notably the two GLM-family models often returned `{"ranking": []}`, while `deepseek-4.1-flash` ranked properly but took 14–32 s. **Root cause not established.** The new message will show the raw reply the next time it happens. Open issue.
- My `pkill -f "dist/ragwb --llm lunaroute workbench"` also matched the shell running it (exit 144) and killed the restart. I used `TaskStop` on the background task instead.
- `browser_take_screenshot` refused the scratchpad path ("outside allowed roots"). Saved into the ticket's `various/` instead, and added `.playwright-mcp/` to `.gitignore`.

### What I learned
- The prototype's agent prompt is large (API reference + state), so first-round latency on `glm-5.3-flash` is 20–80 s. The claude.ai platform "allows a handful of rounds", and this agent routinely needs 6–9 small steps.
- The `quick`-tier choice matters for JSON-producing nodes. `rerank.llm`/`rewrite.llm` default to `quick`, and `muse-spark-open` tends to return empty rankings. Evaluate the tier models on the LLM presets before trusting their numbers.

### What was tricky to build
- **Parked tool calls.** The model's tool request arrives on the server goroutine running `Sample`, but the tool must run in the browser. The handler registers a channel under the tool-use id, emits a `tool` SSE event, and blocks on select(channel, timeout, ctx). The page's POST to `/tools/{use}` looks up the call and sends on the channel. Every SSE write goes through one mutex-guarded `emit`, because `onText` and `tool` events can come from the same loop at different times.

### What warrants a second pair of eyes
- No authentication. Bind to loopback only (the default; documented in the flag help). `db` accepts any path under one store, with no per-user separation (single local user by design).
- `handleSample` holds the HTTP request open for the whole tool loop (minutes). Fine locally; proxies would need SSE-friendly timeouts.
- `rerank.llm` intermittent `invalid_json` (above).

### What should be done in the future
- P6: an apps directory as the script store, with `wb.save`/`wb.load` (and the editor) writing `apps/<name>/*.js` that `ragwb verbs` can run; history in SQLite.
- P7: RemoteRag + `ragwb serve <app>` (the gui runtime driving a server-side session).
- Evaluate the tier models on JSON nodes; consider `deepseek-4.1-flash` for `quick` JSON tasks despite its latency.

### Code review instructions
- `web/workbench/claude.js` (sample SSE client, tool round trip), `pkg/server/sample.go` (parking), `pkg/server/docs.go` (`List`), `pkg/llm/lunaroute.go` (forced final round, `stripToolMarkup`).
- Validate: `go test ./pkg/server ./pkg/llm`; `./dist/ragwb --llm lunaroute workbench`, then open http://127.0.0.1:8790, press Ask, and run an agent suggestion.

### Technical details
- SSE event payloads: `call {id}`, `text {text, delta}`, `tool {use, name, input}`, `result {text, truncated, modelTierApplied, json?}`, `error {code, message, text?}`.

## Step 13: Is the `\p{L}` behavior a goja bug? Yes, and it is fixed upstream after our pin

The user asked whether "goja silently ignores `\p{L}`" is a goja bug. It is, though "ignores" was the wrong word. goja translates JS regex syntax to RE2 in `parser/regexp.go` `scanEscape`, which had no case for `\p`/`\P`. Its default branch treats `p` as an identity escape and drops the backslash, so `[\p{L}\p{N}_]` becomes the literal class `[p{L}{N}_]`. That is correct Annex B behavior without the `u` flag, but with `u` the spec requires a property escape or a SyntaxError. goja did neither and accepted `\p{NotAProperty}`. The fix, goja PR #745 (merged 2026-10-01, commit e1fc788), hands `\p`/`\P` under `u` to regexp2. Our pin, `v0.0.0-20251103141225-af2ceb9156d7`, predates it. On goja master every probe gives the spec answer.

### Prompt Context
**User prompt (verbatim):** "goja silently ignores \p{L} in regexes, which made every search return 0 hits. All 29 presets plus pipeline.js run headless. Scores match the browser: the textbook's 12.6758, and the reports preset finds 11 of 12 golden questions, as in the original session.  --- ^-- is this a bug in goja?"
**Assistant interpretation:** Decide whether the D6 regex problem is a goja defect or expected behavior, with evidence.
**Inferred user intent:** Know whether to work around it permanently, report it upstream, or upgrade.
**Commit (code):** see the commit right after this entry ("RAGWB-001: goja \p probe …").

### What I did
- Read `scanEscape` in the pinned goja (`parser/regexp.go` lines 252–420). There is no `p`/`P` case; the default branch "Unescape[s] the character for re2" via `self.pass()`.
- Found `"regexp-unicode-property-escapes"` in the `featuresBlackList` in `tc39_test.go`: a known, skipped test262 feature at our pin.
- Rewrote `scripts/02-goja-regex-probe/main.go` as a focused probe: 7 expressions (the tokenizer case, literal-`p` evidence, `.source`, an unknown property under `u`, Annex B `\p` without `u`).
- Searched upstream: `gh pr list -R dop251/goja --state all --search property` → #745 "Pass Unicode property escapes on to regexp2 in unicode mode", MERGED. #715 (a different approach) was closed.
- Ran the same probe against goja master in a scratch module (`go get github.com/dop251/goja@master` → `v0.0.0-20261103151521-97b96e1e55f5`).

### What worked
| probe | pinned (af2ceb9) | master (97b96e1) |
|---|---|---|
| `"rebalance law".match(/[\p{L}\p{N}_]+/gu)` | `null` | `["rebalance","law"]` |
| `"apple {LN} pie".match(/[\p{L}\p{N}_]+/gu)` | `["pp","{LN}","p"]` | `["apple","LN","pie"]` |
| `"p{L}".match(/^\p{L}$/u)` | `["p{L}"]` | `null` |
| `"é".match(/^\p{L}$/u)` | `null` | `["é"]` |
| `new RegExp("\\p{NotAProperty}","u")` | accepted | throws SyntaxError |
| `"pp".match(/\p/g)` (no `u`) | `["p","p"]` | `["p","p"]` (correct, Annex B) |

### What didn't work
- `gh search issues --repo dop251/goja "property escapes"` and several variants returned nothing. `gh pr list --search property` found the PR.

### What I learned
- The bug was easy to miss because the same translation is correct without `u` and silently wrong with it. The second probe row shows the mechanism directly: it matches the characters `p`, `{`, `L`, `N`, `}`.
- Per the PR, valid names regexp2 doesn't support yet (e.g. `\p{Script=Latin}`) now throw instead of matching nothing (dlclark/regexp2#113). rag.js only uses `\p{L}` and `\p{N}`.

### What was tricky to build
- Nothing to build. The trap is the parity coupling below.

### What warrants a second pair of eyes
- **Bumping goja changes tokenization.** rag.js feature-detects `\p` (D6), so after a bump it switches to the real Unicode classes. But the Go port's `isLN` (`pkg/natives/bm25.go:28`) mirrors the *fallback* ranges, so JS↔Go parity would break on any letter or number outside them (e.g. Latin Extended Additional, Devanagari, Thai, `²`). The bump must switch `isLN` to `unicode.IsLetter(r) || unicode.IsNumber(r)` in the same change. That also matches the browser, which always had `\p`.

### What should be done in the future
- Task k4hx: no upstream report needed. Bump goja past e1fc788, check go-go-goja v0.10.6 still builds against it, change `isLN`, and re-run the parity suite, including whole-vault hashed parity. The D6 detection can stay as a harmless guard.

### Code review instructions
- `scripts/02-goja-regex-probe/main.go`; run `go run .` in that directory (pinned goja). To compare against master, copy it into a scratch module and `go get github.com/dop251/goja@master`.

### Technical details
- Upstream PR: https://github.com/dop251/goja/pull/745 (files: `parser/regexp.go`, `regexp_test.go`).
