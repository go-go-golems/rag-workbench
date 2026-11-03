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
