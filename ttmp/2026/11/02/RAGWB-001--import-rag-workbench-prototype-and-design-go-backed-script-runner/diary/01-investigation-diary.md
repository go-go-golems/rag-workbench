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
