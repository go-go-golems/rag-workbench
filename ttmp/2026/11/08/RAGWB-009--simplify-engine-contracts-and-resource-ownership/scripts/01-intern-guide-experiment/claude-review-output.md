# Newcomer comprehension review: RAGWB-009 V2 intern guide, candidate B

## 0. Scope and method

- **Reviewed:** `ttmp/2026/11/08/RAGWB-009--simplify-engine-contracts-and-resource-ownership/design-doc/03-v2-intern-guide-candidate-b.md`, all 551 lines.
- **Phase 1:** I read the whole guide in one pass and wrote the preliminary assessment (§3 below) before opening any other file.
- **Phase 2:** I read only the source needed to answer specific questions. Every file I opened is in the ledger (§7).
- **Not read:** the original guide, candidates A/C, reviews, diaries, comparison reports, skill guidance, transcripts, the RAGWB-010 intern guide, `pkg/ragjs/src/README.md`, and the GitHub PR pages.
- **Nature of this review:** a model playing a newcomer, not a real intern. I ran no code or tests. Where I describe what code does, that comes from reading it, not from running it.

---

## 1. Overall judgment

Candidate B is a strong design guide on its core subject: lifecycle invariants.
- **What it does well.** It explains the snapshot/lease/registry split, shared-job cancellation, fixed terminal outcomes, bounded SSE writes and reference traversal through concrete sequences rather than slogans. It is careful about how strong its evidence is.
- **Accuracy.** Every claim about current code that I checked held up in substance. One explanation merges two different constants (`LineageKeep`/`LineageSearch`, §6). One proposal hides a behavior change: the direct-query endpoint has no admission cap today (§9).
- **Orientation is the weak part.** It assumes a reader who already knows the project:
  - Many project terms are never defined: *bake*, *signals*, *verb*, *trigger/submit-triggered*, *Pi*, *vault*, *pinned output*, *family factories*, *neighbor preview*.
  - The source map leaves out the files behind two of the twelve findings.
  - A central concept is used but never defined: what one snapshot covers (`plan.buildKey`, "per build identity", "usable published snapshot").
- **Experiment metadata leaks in.** §1's last sentence and all of §15 talk about the documentation experiment, not the system.

An experienced engineer could understand the proposal and write the characterization regressions with this guide plus light source reading. They could not safely start the snapshot cut-over (phase 3) until the snapshot-granularity and query-freshness questions are answered.

## 2. My mental model from the document alone

- **The system.** A JavaScript RAG engine is built from modular sources into one bundle. It runs inside Goja on a Go server and also in browsers.
  - Users declare a **corpus**: ingestion work (source → parse → chunk → index).
  - They also declare a **plan**: per-query work (retrieve → fuse/filter → rerank → pack → answer).
  - A fluent DSL builds both and compiles them into a graph.
- **Builds.** Builds produce outputs (record collections, indexes). Go natives persist them as artifacts on disk, and a lineage history drives incremental "delta" builds.
- **Queries.** Queries run plans against built outputs. They may call LLM providers (sample/decide/rerank) through asynchronous Go work and stream events to HTTP clients over SSE.
- **The proposal** replaces several ad hoc mechanisms with explicit contracts:
  1. **Datasets.** One read-only `Dataset` interface for fresh, stored and delta outputs. Hits carry versioned `{dataset, id}` references.
  2. **Snapshots and results.** Immutable snapshots are published atomically. Queries hold an engine-managed lease through result construction, and results are copied out ("detached") so callers never close anything.
  3. **Resource registry.** One registry owns every native handle and closes it only after its final user releases it.
  4. **Store inventory.** Manifests and descriptors are the store's inventory; lineage becomes a derived lookup. GC is mark/sweep from roots. One process holds a lock on the writable namespace, and publication follows a fixed crash-safe sequence.
  5. **Async operations.** One runner gives each async operation a single fixed terminal outcome.
  6. **Shared builds.** Builds run as jobs with their own lifetime; callers are waiters whose cancellation only detaches them.
  7. **Streaming.** One SSE response helper with per-write deadlines for both endpoint forms.
  8. **Graph references.** One structural-reference API for compile, edit and export.

---

## 3. Preliminary assessment (preserved as written before any code inspection)

> The text below is my Phase 1 assessment, kept as I wrote it in my notes before opening any other file. Lightly condensed; no conclusions from the code have been added.

### 3.1 Reading process actually followed

I read the guide once, in a single read of all 551 lines. While writing notes I went back to specific earlier passages, and these are the only real look-ups:
- In §10 ("artifact dependency"), I went back to the §3 historical anchor "`finalize` … separately discovers input, `uses`, decision and signal references". I inferred that *artifact dependency* means `uses`.
- In §12 phase 5 ("deterministic Pi fallback"), I went back to §3 finding #6 ("Empty enabled-model list blocks a valid fallback"). I inferred that Pi is the provider involved; nothing defines it.
- In §3 ("generation mismatch") and §5 ("retained generation"), I went back to §2's snapshot definition. I could not tell whether *generation* and *snapshot* mean the same thing.
- In §5 (`snapshots.withCurrent(plan.buildKey, …)`), I jumped ahead to §6 ("current-snapshot pointer per build identity"). Neither passage defines the key.
- In §12, I compared the eight phases with §1's "nine implementation tasks". The guide gives no mapping.
- On reaching §9's decision record ("sample/decide/rerank repeat bridge lifecycle"), I went back to §7. That record covers §7 and §9 together.

### 3.2 The system and the change in my own words

See §2 above; I wrote it then. Additionally:
- The guide is explicit that none of this is implemented. The twelve findings are reported by review, not reproduced by the author.

### 3.3 Explicit / inferred / ambiguous / unknown

**Explicit:**
- Status of the evidence (§1, §3).
- What to keep and what not to build (§1, §11).
- The dataset operations `get/values/size/materialize` and their meaning (§4).
- That a hit carries `{dataset, id}`.
- Fusion must not guess (§4).
- Lease acquisition is one serialized transition (§5).
- Packing lives inside the lease (§5).
- No-op closer fallback removed (§5).
- Mark/sweep GC roots (§6).
- Seven-step publication (§6).
- The cancellation race rule (§7).
- Waiter semantics and the late-C rule (§8).
- Per-write deadlines through `http.ResponseController` (§9).
- Rename/delete/export policies (§10).
- Phase order (§12).
- The test sequences (§13).
- Open details (§14).

**Inferred (general engineering knowledge filling gaps):**
- "Runtime owner" is the single goroutine allowed to touch a Goja VM. Goja is not goroutine-safe; that is general knowledge, not something the guide says.
- "Admission slot" / "execution slot" is a concurrency semaphore.
- An "OS-backed exclusive namespace lock" is likely `flock`-style.
- `errgroup` is `golang.org/x/sync/errgroup`.
- The claim that a write deadline cannot be extended once exceeded matches my memory of the `http.ResponseController.SetWriteDeadline` documentation.
- "Delta path" means incremental builds that produce a new dataset from changed files.
- "Vault" means an Obsidian-style folder of Markdown notes (from "vault-relative path" and "Obsidian rendering").

**Ambiguous:**
- `.filter.where().limit()` with no arguments in the §2 example.
- "signals": DSL inputs, `AbortSignal`, or "signal dependency"?
- "generation" versus "snapshot".
- `scope.Adopt` versus `Registry.Adopt` in the §5 Go sketch.
- Whether `NewChunkDataset(h)` means the dataset is built in Go or in JS.
- "store root" versus "namespace".
- Which PR "PR #8" is.
- "This experiment changes documentation only": which experiment?
- "Use `go run ./...` with the actual command package" (§13).

**Unknown from the text:**
- What a **bake** is. It is used throughout §3–§4 and never defined.
- **Pi**, **verb** endpoints, **pinned output**, **"submit-triggered"**, **family factories**, **post-sync refresh**, **neighbor preview**.
- What a **build identity** / `buildKey` contains, and whether a snapshot is per corpus or per plan.
- How `Dataset.id` is derived, and whether it is the existing node digest.
- What "usable published snapshot" means when sources have changed.
- Whether artifact descriptors already exist ("Extend artifact descriptors" says they do; their contents are not described).
- Which files hold the dotted-root listing and the model fallback.
- What `__serve.run` is ("embedded" in a Go file?).
- How to run "a reports example", and where it lives.
- What `ChunkDataset.document()` does for a missing ID.
- Whether a synchronous `Index.search` works for every index kind (for example, vector search that needs a query embedding).

### 3.4 Preliminary per-area judgment

- **Orientation (§1–§2):** the motivation is clear, but much of the vocabulary assumes prior knowledge. The DSL example is slightly opaque.
- **Evidence/source map (§3):** well calibrated on evidence. The source map is useful but does not map findings to files.
- **Storage (§4 identity, §6):** strong. The inventory argument is convincing. The identity scheme is under-specified.
- **Lifecycle (§5):** the strongest section. Snapshot granularity is undefined.
- **Operations (§7–§9):** precise and testable.
- **Graph (§10):** clear, with an example.
- **Migration (§12):** clear, except the task/phase mismatch and "Pi".
- **Tests (§13):** concrete sequences; weak on the command needed to run an example.
- **Risks (§14):** honest about what is open.
- **§15:** commentary about the document, not useful to an implementer.

---

## 4. Section-by-section comprehension table

The comprehension levels are my qualitative judgment, not measurements. "After code" records what Phase 2 changed.

| Section | Comprehension (doc alone) | Inference / rereading needed | Remaining questions (pre-code) | After code |
|---|---|---|---|---|
| Frontmatter | High, little content | None | `Summary`, `WhatFor`, `WhenToUse`, `RelatedFiles` are empty | Unchanged; minor doc-tooling gap |
| §1 What this design changes | Good on goals and limits | Inferred PR #8 = the 010 PR; compared "nine tasks" with eight phases | Which PR is #8; where the nine tasks are; what "this experiment" is | Not resolved (I did not read PRs or ticket tasks) |
| §2 Follow one document | Good on concepts; partial on the DSL | Inferred meaning of `.signals`, `.filter.where()`, `.limit()` | Signals vs `AbortSignal`; what "bake" is (used later); how to "run a reports example" | `filter.where` reads `s.filters` (a no-op here); signals are graph nodes. Reports example still unresolved |
| §2 diagram + snapshot/lease/registry | High | None | Snapshot granularity | Still open (design decision) |
| §3 Findings table | Good | Needed §4–§9 to decode several rows | Mechanism behind "duplicate chunks choose a different version" | Confirmed in `records.js`/`engine.js` |
| §3 Source table | High | None | Do the historical symbols still exist in `src/`? | Yes, all exist |
| §3 Historical anchors | Good | Used later to decode §10 | — | Confirmed against current symbols |
| §3 Native/app boundaries | Partial | "embedded `__serve.run`" unclear | Files for dotted root and model fallback absent | Found `artifacts/store.go List`, `llm/gepp/backend.go preferred`; a second no-op fallback in `bm25_native.go` |
| §4.1 Fresh and stored records | High | — | Callback argument shape changes (`ctx.chunks`) unstated | Current bake gets `{docs, chunks, tokens, slug}` |
| §4.1 TS sketch | Good | — | `document()` on a missing ID; sync `search` for all index kinds | Current `indexes.js` search is synchronous; missing-ID behavior is a design detail |
| §4.2 Logical ID vs version | High | — | How `Dataset.id` is derived; relation to existing digests | Still open; existing node digests/storeKeys are the obvious basis, but the guide doesn't say so |
| §4 Decision | High | — | — | — |
| §5.1 Refresh vs active query | High, except granularity | Jumped to §6 for "build identity" | `buildKey`; meaning of "usable" A; stale-serve vs wait semantics | Current code waits for a rebuild when digests change; the proposal may change this unannounced |
| §5.2 Registry | Good | `scope` vs `Registry` | Where `Dataset` is built (Go/JS); `ResourceID` derivation | Current views are JS wrappers over Go handles (`persist.go recordsViewJS`); still a design choice |
| §5.3 Detached results | Good | — | "Neighbor preview"; "generation" vs snapshot | Neighbor preview = `NB_HITS = 40` prev/next packing in `serve.go` |
| §5 Decision | High | — | — | — |
| §6.1 History vs inventory | High in concept | — | Do descriptors exist today; store root vs namespace | 32 is `LineageKeep`, not the search list (`LineageSearch = 8`) |
| §6.2 One process per namespace | High | General knowledge for OS locks | Lock mechanism | Fine as a design statement |
| §6.3 Publication sequence | High | — | Orphans after a crash between steps 3–5 | Inferred: swept as unrooted owned artifacts; acceptable |
| §6 Decision | High | — | — | — |
| §7 Async operations | High | Inferred what sample/decide are; "runtime owner" from general knowledge | Where the runner lives; what "streaming tools" are concretely | Rerank mechanism confirmed exactly in `rerank.go` |
| §8 Shared builds | High | — | What "post-sync refresh" is; what a background owner is | `buildShared` confirmed; "post-sync" unresolved |
| §9 HTTP stream | High technically | Inferred "admission slot" is a semaphore | "Verb" undefined; do both endpoints have slots? | Only verbs have admission; direct queries have none |
| §9 Decision | Good | Went back to §7 | Placement (covers §7 and §9) | — |
| §10 Graph references | Good | Went back to §3 for `uses`; "pinned", "submit-triggered" undefined | Where `when` appears in the DSL | Confirmed in `finalize`/`applyOps`/`html.js`; rename also skips `uses`-derived references |
| §10 Decision | High | — | — | — |
| §11 Smaller implementation | High | — | — | — |
| §11 TypeScript foundation | Good | "family factories" unclear | — | Confirmed `tsconfig`, `pnpm` scripts; family = `rag[fam]` from component types |
| §12 Migration | Good | Pi from §3 #6; tasks vs phases | Pi; nine vs eight | Pi resolved (external agent settings) |
| §13 Proof | High for the sequence table; partial for commands | — | Which command package; `lsof-who` | Not resolved |
| §14 Risks | High | — | — | Runner-packing risk confirmed and somewhat worse (GC runs before packing) |
| §15 Author self-check | Clear, but not for implementers | — | Why it's in the guide | Recommend removal |

---

## 5. Strong explanations

- **§2, three separated questions:** "This separates three questions: which version should a query read, who still needs that version, and who closes its underlying handles." This one sentence gave me the decomposition that the rest of §5 implements.
- **§4.2, provenance by example:** the `plain-v1` / `prefixed-v1` chunk `engine.md#cancellation` example, ending "A global union keyed only by chunk ID cannot express that requirement." It makes a type-system point concrete. Code inspection showed the current union really is keyed only by ID, with inconsistent precedence.
- **§4.2, bake identity:** "Equal record counts do not imply equal input: changing a sentence without changing the number of sections must invalidate dependent bakes." This is exactly right. `Engine.resolveBakes` keys on `d.id + d.sections.length` and `chunks.length` (`engine.js:529`).
- **§5.1, two distinct failures:** "Closing A merely because two refreshes happened can break Q when it resumes. Even if the index remains open, looking up the hit in C may return changed text or no record." Separating lifetime from version selection is the key insight, and it is stated plainly.
- **§5.1, scope of the lease:** "It includes the runner's post-query response packing, not only `Session.run`'s computation." This flags the integration boundary a newcomer would most likely miss. It is confirmed in `serve.go`.
- **§6.1, inventory argument:** "Creating version 33 evicts version 1 from that list, but version 1's files may still exist… Increasing the history limit delays the failure without fixing the ownership error." This explains why the obvious fix is wrong.
- **§7, the race rule:** "a terminal result accepted before the cancellation transition stays terminal; otherwise cancellation wins." Precise enough to test. The description of the rerank bug matches `rerank.go:146–165` line by line.
- **§8:** "Reusing A's promise is useful; reusing A's abort signal is wrong." The late-C rule ("do not attach C to a doomed promise") anticipates an edge case most designs miss.
- **§9:** "A bounded event queue alone does not prevent the writer from blocking forever." This matches the current design exactly. `streamSink` has a queue with slow-client cancellation, but `pump` writes with no deadline. Also strong: "Assert that the admission slot becomes available again, not merely that an error was logged."
- **§11, reuse boundaries:** "Memory and persistent datasets share conformance, not encoding algorithms. Fresh/load/delta paths share adoption, not build algorithms…" These sentences effectively forbid the over-abstraction the guide warns against.
- **§13:** "Hold/release barriers let a test pause a query or build at a precise transition; arbitrary sleeps do not establish the race sequence." This is practical testing guidance.
- **Evidence handling (§1, §3):** "this documentation work did not independently reproduce all twelve" and "The proposed prevention is a design target, not a passing test result." The guide states its evidence level honestly.

---

## 6. Confusing or potentially misleading passages

### 6.1 The §2 DSL example: `.filter.where().limit()`
- **Initial reading:** the empty argument lists were placeholders or truncation.
- **Why the text was insufficient:** the example is labelled "Existing public style to retain", but it never says what these calls do with no arguments.
- **Resolution:** `builtins.js:294` `filter.where` reads `s[p.key || 'filters']` and returns the hits unchanged when that signal is empty. In this plan it is a no-op. One comment would fix this.

### 6.2 "signals"
- **Initial reading:** three possible meanings. The DSL `.signals({q:'', k:10})`, the `request.signal` / `AbortSignal` in §5 and §8, and "signal dependency" in §10.
- **Why insufficient:** the term is never defined, and the `AbortSignal` usage sits right next to it.
- **Resolution:** `graph.js finalize:408–411` turns each declared signal into a `signal` graph node. Lambdas reading `s.x` create signal edges (`:491`, `:499–501`). These are unrelated to abort signals.

### 6.3 "bake" (§3, §4)
- **Initial reading:** a build-step node, perhaps "bake an index".
- **Why insufficient:** the term is never defined, yet §4's main example and one finding depend on it.
- **Resolution:**
  - `graph.js:10`: `rag.bake(f)` returns `{ $bake: source }`, a *parameter placeholder*.
  - `Engine.resolveBakes` (`engine.js:517–551`) finds placeholders in node params and evaluates them over the first docs/chunks outputs. It caches them by a weak key and currently passes `{docs, chunks, tokens, slug}`.
  - The guide's example `ctx => ctx.chunks.materialize()` therefore also changes the callback's argument shape, which the guide doesn't say.

### 6.4 "Pi" (§12 phase 5)
- **Initial reading:** a provider called Pi.
- **Why insufficient:** it is used once, never defined, and no file is named.
- **Resolution:**
  - `llm/lunaroute.go:62–91` shows Pi is an external agent tool with a `~/.pi/agent/models.json` configuration.
  - `llm/gepp/backend.go preferred` (around `:112–135`) treats an empty `EnabledModels` as "all enabled" inside `enabled()`. Its fallback loop, however, iterates `EnabledModels`, so an empty list selects nothing.
  - `eligible` is a Go map. That explains the guide's insistence on a *deterministic* order, which the guide never motivates.

### 6.5 Admission slots for "both execution forms" (§9)
- **Initial reading:** both direct-query and verb endpoints hold slots, and the bug affects both.
- **Why misleading:** "the handler can retain an admission slot" and "Use the same terminal-error vocabulary and admission scope for both" suggest both have admission today.
- **Resolution (contradiction):**
  - Only `handleVerb` calls `a.acquire()` (`verbs.go:239–243`, default 8 in `rag.go:50`).
  - `handleRun` (`rag.go:227–260`) has no cap.
  - Moving both forms to one helper therefore *introduces* admission control for direct queries. That is a user-visible change that needs its own decision.

### 6.6 "the 32-entry lineage history" as a "search list" (§6.1, §13)
- **Initial reading:** builds search the last 32 versions, and GC uses the same list.
- **Why misleading:** `lineage.go:30–33` defines two constants:
  - `LineageKeep = 32`: how many entries a lineage file remembers, which is what `Store.GC` can find.
  - `LineageSearch = 8`: how many versions a build considers as its base.
- **Assessment:** the argument stays valid because GC only finds outputs through lineage files. The explanation and the §13 phrase "despite the lineage search limit" name the wrong constant.

### 6.7 `plan.buildKey`, "per build identity", "usable published snapshot" (§5, §6)
- **Initial reading:** one snapshot per corpus.
- **Why insufficient:** the key is never defined, and "usable" is never defined.
- **What code showed:**
  - `Engine.artifacts(ir)` is computed per compiled plan from its digest set (`engine.js:556–562`).
  - The server keeps outputs for up to eight recently run plans (`noteIr`, `engine.js:685–690`), because verbs can run other plans.
  - `Session.run` waits for a rebuild when the digests are missing (`session.js:41–51`).
  - `__serve.refresh` exists "so the next query doesn't wait" (`serve.go:123–126`).
- **Still open:** the guide's "A usable A continues serving during a background build of B" therefore may change freshness semantics. Today queries appear to wait; under the proposal they might be served stale. This is an open design decision the guide doesn't mark as one.

### 6.8 "generation" vs "snapshot" (§3, §5, §12)
- **Initial reading:** probably synonyms.
- **Why insufficient:** both terms are used for what seems to be the same thing ("snapshot ID plus record reference … retained generation").
- **Resolution:** none in code; this is a proposed concept. Pick one term.

### 6.9 "embedded `__serve.run`" (§3)
- **Initial reading:** a Go function embedded in a struct.
- **Resolution:** it is a JavaScript harness string (`serveHarness`, `serve.go:51–169`). The sequence there is worse than the guide implies:
  1. `session.run` (`:137`)
  2. `sc.close()`
  3. `collect()`, which may call `eng.gc` (`:142`, `:105`)
  4. `art()` for the *current* artifacts
  5. pack by plain chunk ID (`:144–152`)

  `__serve.chunk` (`:158`) also resolves later GUI lookups by plain ID against the current artifacts. The guide's claim is correct; spelling out this order would make the risk obvious.

### 6.10 The §5 Go sketch: `Registry.Adopt` vs `scope.Adopt`, `NewChunkDataset(h)`
- **Initial reading:** the builder scope implements `Registry`, and datasets are built in Go.
- **What code showed:** today the record view is a JS wrapper built by `recordsViewJS` around a Go `Records2` handle (`persist.go:51–57`).
- **Still open:** whether the new `Dataset` lives in Go, JS or both is an implementation choice and not a defect. The sketch should say which side `NewChunkDataset` is on, or say it is undecided.

### 6.11 Source-map omissions (§3)
- **What the guide names:** `persist.go`'s no-op fallback, and `markdown_native.go` for delta readers.
- **What else exists:**
  - `bm25_native.go:124–126` has an identical `addCloser = func(func() error) {}` fallback.
  - The actual delta opener is in `delta.go:101` (`OpenRecords2`).
  - The dotted-root bug is in `artifacts/store.go List:282`: `strings.Contains(d.Name(), ".")` → `SkipDir`, which also matches the walk root itself.
- **Assessment:** none of these files are named, and "remove the no-op closer fallback" is singular when there are two.

### 6.12 Rename coverage (§10)
- **What the guide says:** rename misses `when.decision`.
- **What code showed:** `applyOps` rename (`graph.js:877–884`) rewrites only `in` arrays and outputs. Parameter-encoded `uses` references are also untouched; for example, a retrieve's index name maps to `ix-<name>` through `def.uses(n.p)` (`:454`). `remove` checks `uses` users but not `when` users (`:841`).
- **Assessment:** the guide's general statement ("separate consumers … rewrite different subsets") covers this, but the example understates it. I did not trace every component's `uses`, so treat the `uses` point as "very likely".

### 6.13 Meta and process text
- §1: "PR #8 is open against the published `task/ragwb-008-vault-ui` parent". Which PR this is has to be inferred.
- §1: "This experiment changes documentation only."
- §1 "nine implementation tasks" vs §12's eight phases, with no mapping.
- §15 is entirely about the writing process.
- §13: "Use `go run ./...` with the actual command package" doesn't name the package. `go run ./...` itself is not a valid way to run one command. The `lsof-who` convention is unexplained.
- §9's decision record covers §7's runner as well as §9's transport.

### 6.14 Unresolved after inspection
- How to "run a reports example" (§2): no path or command given. I did not search for it.
- "Post-sync refresh" (§8).
- PR #8 and the nine tasks.

---

## 7. Code-consultation ledger

| # | File / symbol | Motivating question | Result | Should the guide have supplied it? |
|---|---|---|---|---|
| 1 | Glob `pkg/ragjs/src/**` | Is it 26 modules with 4 strict TS? | Confirmed: 26 code files + README; 4 `.ts` | No |
| 2 | Grep `src/` for historical symbols | Do the §3 anchors still exist? | All present: `graph.js finalize:405`, `applyOps:700`; `records.js unionChunks:30`; `engine.js gc:410`, `resolveBakes:517`, `artifacts:556`, `buildShared:695`; `html.js exportHtml:4` | Yes: one sentence saying the symbols survive |
| 3 | `engine/records.js` `unionChunks`, `isView`, `Lazy`, `Outputs` | What precedence goes wrong? | Map (cold arrays) beats every view; the first view beats later views | Mechanism: optional |
| 4 | `engine/engine.js artifacts` (556–673) | How cold arrays merge | `chunks.set(c.id, c)`, so the later stream wins. The comment at `:622–623` claims later views win, which the code contradicts | No (detail) |
| 5 | `engine.js gc` (410–438) | "Two refreshes close artifacts" | Confirmed: handles retired by one `gc` are closed on the next. Handles are also registered with the runtime `AddCloser`, so ownership is doubled | Doubled ownership: briefly, yes |
| 6 | `engine.js tryDelta` (443–493) | How the delta base is opened | `this.retired.push(v)`: closed by the next gc | No |
| 7 | `engine.js resolveBakes` (517–551) | The bake key and argument shape | Key `hash([docs.map(d => d.id + d.sections.length), chunks.length])`; callback gets `{docs, chunks, tokens, slug}` | Yes: define bake and the callback shape change |
| 8 | `engine.js buildShared` (695–710), `noteIr` (685–690) | First-waiter ownership; plan scope | Key = digest set; the first caller's `opts` (signal, onEvent) are used; 8 recent IRs kept | Granularity: yes |
| 9 | `api/index.js` (131–164), `graph.js:10`, `core/util.js:54` | What a bake is; what "family factories" are | Bake = `{ $bake }` parameter; families = `rag[fam]` derived from component type prefixes | Yes (bake), optional (families) |
| 10 | `graph.js finalize` (405–570) | Reference kinds; signals; pinned | Signals become graph nodes; edges are `data`/`artifact`/`decision`/`signal`; `pinned` = explicitly named outputs | Yes: short definitions |
| 11 | `graph.js applyOps` rename/remove (839–884) | Rename misses decisions | Confirmed; also skips `uses`-derived parameter references; remove checks `uses` but not `when` | Example could be broader |
| 12 | `export/html.js exportHtml` | Export pruning | Removes live `submit`-triggered non-answer nodes, then answer/context; `when` untouched; uses `engine.files()`; calls `r.chunk(h.id)` by string | Yes: define "submit-triggered" |
| 13 | `natives/persist.go InstallEnv` (43–110) | No-op fallback; registration | Confirmed fallback `:47–50`; loaders register at `:209/227/245` | No |
| 14 | `natives/bm25_native.go` (124–126) | (Found via grep) | Second identical no-op fallback | Yes |
| 15 | `natives/markdown_native.go parseDelta/chunkDelta` (103–174); `delta.go:101` | Delta readers miss cleanup | Confirmed: `records2Object(vm, r)` with no `addCloser` | Name `delta.go`: helpful |
| 16 | `artifacts/lineage.go` `LineageKeep`, `LineageSearch`, `LineageRecord`, `GC` | The 32-entry claim; GC inventory | Mechanism confirmed; two constants, the guide merges them; GC walks only lineage files | Yes: correct the constant |
| 17 | `artifacts/store.go List` (272–304) | Where the dotted-root bug lives | `:282` `strings.Contains(d.Name(), ".")` → `SkipDir` | Yes |
| 18 | `natives/rerank.go rerankAsync` (104–174) | Relabeling mechanism | Confirmed exactly; abort listener never removed | No (guide already precise) |
| 19 | `server/sse.go` `streamSink`, `sseWriter`, `pump` | Write bounds | No deadlines; non-error `http.Flusher`; `pump` discards write errors | No |
| 20 | `server/rag.go` `RagApp.acquire/release`, `handleRun` (227–286) | Admission on both forms? | **Contradiction of the implied state:** direct queries have no admission | Yes |
| 21 | `server/verbs.go handleVerb` (209–299) | Slot retention | Slot held across `<-done`, which waits on a blocked pump | No |
| 22 | `runner/serve.go serveHarness` (51–169) | What `__serve.run` is; packing order | Confirmed; `collect()`/gc runs between run and pack; `__serve.chunk` by plain ID; `NB_HITS = 40` | Yes: state the order |
| 23 | `engine/session.js run` (37–56), `:312` | Do result methods read live artifacts? Freshness | `chunk: id => art.chunks.get(id)`; queries wait when digests are missing | Freshness: yes |
| 24 | `llm/lunaroute.go`, `llm/gepp/backend.go preferred` | What Pi is; fallback location | Resolved (§6.4) | Yes |
| 25 | `package.json` scripts | Do `pnpm build`/`check` exist? | Yes | No |
| 26 | `tsconfig.json` | `allowJs`/`checkJs` | `allowJs: true`, `checkJs: false`, `strict: true` | No |
| 27 | `components/builtins.js filter.where` (294–317) | DSL example semantics | Reads `s.filters`; a no-op if empty; uses `ctx.chunk(h.id)`, another plain-ID consumer | A comment in the example |
| 28 | `components/indexes.js` `search` | Is sync `Index.search` plausible? | Current `search` methods are synchronous | No |

**Not consulted:**
- Go files: `pkg/ragjs/sample.go`, `decide.go`, `module.go`, `pkg/runner/runtime.go`, `records2.go` internals.
- JavaScript/TypeScript: `engine/scope.ts`, `recordsViewJS`, `web/app-shell/shell.js`.
- Documentation: `src/README.md`, the 010 guide, the PR pages.
- Any test files.

---

## 8. Changes to my understanding after code inspection

1. **Confirmed findings.** The findings I could trace are real in the current source, not only at `0854c6a`:
   - chunk precedence
   - bake view vs array and the weak bake key
   - dotted root
   - two-gc closure
   - lineage-inventory loss
   - unregistered delta readers
   - verb SSE slot retention
   - first-waiter build options
   - rerank relabeling
   - empty enabled-model fallback
   - export dangling `when`
   - rename missing `when`

   I traced the mechanism for every one of the twelve except the export *runtime* failure, which I inferred from the code, not observed.
2. **Runner packing is worse than described.** GC (`collect()`) runs *before* packing, so a query can trigger closure of handles that its own packing would need. The guide's priority on this boundary in §14 is justified.
3. **Ownership is currently doubled.** Handles are registered with the runtime closer list *and* closed directly by `Engine.gc` through `__records.close()` / `__ix.close()`. The guide's "one registry owns each opened resource" is the right fix, but the guide doesn't say that two owners exist today.
4. **Direct queries have no admission today.** This changes how §9 reads: it proposes a new limit for direct queries, not just a unified one.
5. **Lineage has two limits (32 and 8).** The inventory argument is about the 32.
6. **Freshness semantics are a live decision.** Today a query whose plan digests changed waits for the rebuild. The proposed "usable A continues serving" needs an explicit choice.
7. **Snapshot granularity has an existing basis.** Snapshots can be keyed by plan digest sets, and the server already serves several plans (the app's plan plus those verbs run).
8. **Bakes are parameter placeholders, not nodes.** Migrating them to datasets changes the callback signature as well as the collection type.
9. **There are more plain-ID consumers than the guide's examples show:** `filter.where`, export HTML, `__serve.chunk`, `Session.run`'s `chunk`. This supports the guide's §14 warning.

---

## 9. Classification of issues

**Writing issues:**
- Undefined project terms: bake, signal, verb, trigger/"submit-triggered", Pi, vault, pinned output, family factories, neighbor preview, post-sync refresh, runtime owner, admission slot.
- "Generation" and "snapshot" used interchangeably.
- The §2 DSL example uses argument-less calls without explanation.
- The source map lacks a finding→file mapping and omits `store.go`, `gepp/backend.go`, `bm25_native.go` and `delta.go`.
- Experiment and process text in §1 and §15.
- PR #8 unidentified; nine tasks vs eight phases.
- §13's `go run ./...` and `lsof-who` are unexplained.
- The §9 decision record also covers §7.
- Empty frontmatter metadata.

**Incorrect or imprecise factual claims:**
- §6.1/§13: the 32-entry history is described as the incremental search list. The search limit is `LineageSearch = 8`; 32 is `LineageKeep`. The conclusion still holds.
- §9 implies both endpoint forms hold admission slots today; only verbs do.
- §5: "remove the no-op closer fallback" is incomplete; there are two.
- Nothing else I checked was contradicted.

**Open design choices:**
- *Flagged by the guide:* API names, record schemas, duplicate-adoption policy, output budgets, retention duration, timeouts, filesystem durability, fallback eligibility.
- *Not flagged, but should be:*
  - snapshot granularity / `buildKey` (per plan digest set? per corpus?)
  - query freshness when sources changed (wait vs stale-serve)
  - whether `Dataset.id` is the existing node digest/storeKey
  - whether direct queries get an admission cap, and its size
  - which side (Go/JS) constructs datasets
  - the shape of the "scoped internal snapshot operations" for evals/export
  - how verbs that run other plans map onto snapshots

**Ordinary implementation detail a design guide need not include:**
- exact current line numbers
- `NB_HITS`
- `recordsViewJS` internals
- `runtimebridge` method names
- constant values for queue sizes and timeouts
- test file locations

---

## 10. Could I begin implementing safely?

**Yes, for phase 1 (characterization).** The guide plus the files in §7 are enough to write deterministic regressions for all twelve findings. Each mechanism is visible in the source.

**Mostly yes, for phase 2 (datasets and provenance).** It needs one decision first: whether `Dataset.id` reuses the existing node digest. Converting hits to `{dataset, id}` also needs a full inventory of plain-ID consumers, which the guide asks for and I could find while working.

**Blocked, for phase 3 (snapshot/resource lifetime), until these are decided:**
- What one snapshot covers: the per-plan digest set or a per-corpus build, and how the up-to-eight recently run verb plans share or retain snapshots.
- What a query does when sources changed and B is unpublished: wait (today's behavior) or serve A.
- Whether `collect()`/GC moves out of the request path entirely. The design implies it does; the guide should say so.

**Discoverable while working:**
- the Goja owner/posting API (`runtimebridge`)
- `scope.ts` request capture
- where `Records2` handles are created
- how `rag.engine()` is constructed in `runtime.go`/`module.go`
- test command packages
- the Pi settings structure

**Phases 4–7** are specified well enough to start, given the flagged open details. For phase 6 there is one more decision: an admission limit for direct queries, and what a search-as-you-type client sees when rejected. I infer the as-you-type behavior from the `change` trigger in export HTML; I have not verified it in the served GUI.

---

## 11. Prioritized edit list

1. **Define snapshot granularity and query freshness, or mark them as decisions** (§5). This blocks phase 3.

   > *Decision needed before phase 3.* Today `Engine.artifacts(ir)` is computed per compiled plan from its node digests, and the server keeps outputs for up to eight recently run plans, because verbs can run plans other than the app's. Define `buildKey` as one of these: the plan's build-node digest set, or a corpus-level build identity shared by every plan over that corpus. Also decide what a query does when sources have changed but B is not yet published. Today `Session.run` finds no artifacts for the new digests and waits for the build. "A usable A continues serving" would instead serve A until B publishes. Choose one behavior and test it.

2. **State the current runner packing order** (§3/§5).

   > In the server, `__serve.run` is a JavaScript harness string in `pkg/runner/serve.go`. It calls `session.run`, closes the request scope, calls `collect()` (which may call `eng.gc`), then reads the *current* artifacts through `art()` and packs chunk bodies and neighbor links (`NB_HITS`) by plain chunk ID. `__serve.chunk` serves later GUI lookups the same way. A refresh that finishes during the query therefore changes what the response packs, and the query's own `collect()` can retire handles before packing. The lease must cover packing, and collection must not run inside the request path.

3. **Add a short glossary of project terms** (end of §2). These definitions come from my code reading; the author should confirm them.

   > - **Signal:** a named query input declared with `.signals({...})`. It compiles to a `signal` graph node, and live components read it as `s.name`. Unrelated to `request.signal` (an `AbortSignal`).
   > - **Bake:** a node parameter computed at build time by a callback over built docs/chunks. `rag.bake(f)` stores `{ $bake: source }`, and `Engine.resolveBakes` evaluates and caches it.
   > - **Trigger:** why a query runs (`change` or `submit`). Export currently drops submit-only live nodes.
   > - **Verb:** an app-declared function served as an HTTP endpoint (`handleVerb`) under a per-app inflight cap.
   > - **Pinned output:** a plan output named explicitly, not inferred.
   > - **Pi:** the external agent configuration from which `pkg/llm/gepp` reads the default and enabled models.
   > - **Runtime owner:** the one goroutine allowed to touch a Goja runtime. Workers post callbacks to it.

4. **Correct the lineage explanation** (§6.1, §13).

   > A lineage file remembers the newest `LineageKeep` (32) versions; a delta build searches only the newest `LineageSearch` (8) of those for a base. `Store.GC` finds old outputs only through lineage files. When version 33 is recorded, version 1 drops out of the file while its payload stays in the namespace, so GC can no longer find it. Raising `LineageKeep` delays this; making descriptors and manifests the inventory fixes it.

5. **State the admission change** (§9).

   > Today only verb requests take an admission slot (`RagApp.acquire`, default 8); `handleRun` has none. Both handlers wait for `pump`, which writes without a deadline and discards write errors. Moving both forms to the shared helper gives direct queries an admission scope for the first time. Choose its limit and rejection behavior deliberately.

6. **Map each finding to its current location.** Add a "current location" column to the §3 findings table, including `artifacts/store.go List` (dotted root), `llm/gepp/backend.go preferred` (fallback), `natives/delta.go` (delta opener), and both no-op fallbacks (`persist.go`, `bm25_native.go`). Add one sentence: "The historical symbols still exist under the same names in the modules listed below."
7. **Annotate the §2 example:** `.filter.where() // filters hits by s.filters; no-op when unset` and `.limit() // trims to s.k` (the author should confirm the `limit` behavior; I did not check it). Also say in §4 that bake callbacks change shape from `{docs, chunks, tokens, slug}` to the proposed `ctx`.
8. **Remove experiment and process text:**
   - Delete §15 and "This experiment changes documentation only."
   - Name PR #8.
   - Map the nine tasks to the eight phases, or drop the count.
   - Use "snapshot" consistently instead of "generation".
   - Replace `go run ./...` with the actual command path.

---

## 12. Limitations

- I am a model simulating a newcomer. This review is not evidence of how a real intern would read the guide. My "rereads" are the look-ups I actually made while writing notes, not a timed reading log.
- Source inspection covered the 28 entries in the ledger. Several named boundaries were not inspected: `sample.go`, `decide.go`, `scope.ts`, `runtime.go`, `module.go`, `shell.js`, the record-view JS, and the tests.
- I ran no commands, builds or tests. Statements about runtime behavior come from reading code. The export-dangling-`when` failure and the bake-on-a-view failure are inferences from code paths, not observed failures.
- I did not access the PR pages, so the review findings' original evidence and the PR numbering are unverified.
- General engineering knowledge filled specific gaps, as noted where they occur: Goja's single-owner threading, `http.ResponseController` deadline semantics, `errgroup`, OS file locks, and the meaning of "vault".

REVIEW_COMPLETE
