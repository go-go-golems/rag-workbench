# Research notes before drafting (RAGWB-009 replacement guide)

Inspected: working tree on `task/ragwb-009-writing-experiment` (runtime baseline c267f91 per context; this branch adds docs only). All findings are from **reading source**; no tests or reproductions were run. Line numbers are as read today and will drift.

## Reader and decision

Reader: an engineer new to rag-workbench who must implement 009. They need (a) a working model of build → artifacts → query → HTTP, (b) the concrete mechanism behind each of the twelve review findings, (c) target contracts with the open policy choices called out, (d) an order of work with tests. The original guide is proposed design, not implemented; 010 moved sources to `pkg/ragjs/src` and generates `pkg/ragjs/assets/rag.js` via `pnpm build`.

## System understanding (current behavior)

### Build side (`pkg/ragjs/src/engine/engine.js`)
- `Engine.build(ir, opts)` iterates build-phase nodes in IR order. For each node: digest `d` from `digests(ir)` = hash(type, params, input digests, extra, def.src) (engine.js:58-76). `source.files` extra = file digests.
- Persistence: if `env.artifacts` has `storeKey(d, def)` (records.js:86 = hash([d, def.src])) the node becomes a `Lazy` memo entry (engine.js:184-201). Otherwise computed: delta (`tryDelta`), `def.map` per item (with item cache `this.items`, async concurrency, LLM budget/deferral) or `def.all(inputArrays())`.
- `inputArrays()` uses `asArray` → record views are materialized with `toArray()` (records.js:75). Delta builds receive `inputs[i].value` lazily and `base.load()`.
- Outputs saved with `store.save(sk, type, codec.save(...), count)` and `store.lineageRecord(lin, sk, d, inputDigests)` (engine.js:346-364). Save failure only logs.
- `this.memo` (digest → {value,count,lazy}), `this.partial` (deferred-LLM outputs), `this.items`, `this.bakes`, `this.retired`, `this.building`, `this._art` single-slot cache.
- `resolveBakes` (engine.js:517-551): finds the *first* docs and chunks node in IR with outputs; passes `outputs.get(...)` — may be a RecordView, not an array (finding #2 bake). Cache key `key0 = hash([docs.map(d => d.id + d.sections.length), chunks.length])` — equal counts with changed text collide; for a view `docs.map` may not exist (RecordView has no `map`) → likely throws; `chunks.length` exists on view (getter). Called again in `Session.params` with `art.byId`.
- `buildCtx.docs/chunks` getters = first docs/chunks node output (asArray).
- `buildShared(ir, opts)` (engine.js:695-710): key = joined digests; a second caller gets the first caller's promise, built with the first caller's `opts.signal`, `onProgress`, `onEvent`. Second caller only receives `{type:'build', waiting:true}`. Cancel of first aborts the build for both (finding #4 shared build). `calls/force/full/persist` options are not in the key.
- `gc(irs)` (engine.js:410-438): drops memo entries whose digests are not in the given IRs, pushes their values to `retired`; values retired in the **previous** gc call are closed now (via `v.__records.close()` or `v.__ix.close()`). Lazy unloaded entries are never closed. So a value survives exactly one additional gc. Two refreshes during one query → closed under the query (finding #3).
- `tryDelta` loadBase: if base not in memo as live view, opens with `codec.load(store.load(base.storeKey))` and pushes to `retired` (closed at the next-but-one gc).
- `artifacts(ir)` (engine.js:556-673): requires every digest present in memo or partial; builds `byId`, then merges chunk outputs: arrays into a Map with `chunks.set(c.id, c)` (later stream wins), views collected; if views present: single view used directly when no arrays, else `unionChunks(map, views)` where `get` prefers the array map, then the **first** view (records.js:30-61). Values iteration order: views first, then arrays. Thus precedence of duplicate IDs depends on which outputs happen to be stored as record views vs arrays (finding #2 duplicate chunks). Reading order `order` is derived from a "primary" chunks node = first chunks node whose type isn't `enrich`/`enrich.llm` (note: `enrich.decide` is NOT excluded — likely a latent bug). Docs map: first doc with an ID wins; duplicates logged. Indexes keyed by `n.p.name || id without 'ix-'`.
- Enrichment components (`enrich.decide`, builtins.js:93-121) return `{...chunk, ...}` with the same `id`. So two datasets legitimately share IDs with different bytes.

### Persistence in Go (`pkg/natives/persist.go`, `pkg/artifacts`)
- `Persist.InstallEnv`: `addCloser` falls back to no-op when `env.AddCloser` nil (persist.go:47-50); `ragjs/module.go:143` also defaults to no-op outside an engine runtime.
- `load(key)` opens records (v1), records2 or bleve in place and calls `addCloser(r.Close)`; returns RecordView (JS class in `recordsViewJS`, persist.go:437-468) with `__records`, `get/has/order/values/toArray`, `size`/`length`, 4096-entry decode cache.
- Delta outputs: `markdown_native.go` `parseDelta`/`chunkDelta` → `ParseDelta`/`ChunkDelta` → `cowOpen` → `OpenRecords2(dir)` (delta.go:96-102). The JS side wraps with `ctx.store.view(r.records)`. **No addCloser** on this path (finding #3 delta readers). Temp dirs registered for removal at runtime close (persist.go:97-113).
- `save`: bleve relocates dir into store; records2 delta output moved (or COW-copied) in and relocated; Go chunk/doc arrays written as records2; others JSON/binary codecs.
- `artifacts.Store` (store.go): layout `<dir>/<ns>/<key[:2]>/<key>.{bin,json,<codec>}`; `Has` validates payload size/kind; `savePath` rename or copy; `writeAtomic` = temp+rename **without fsync**. `List(dir)` skips any directory whose name contains `.` — including the root itself when its basename has a dot (store.go:282) (finding #2 dotted root). Namespace `runner.ArtifactNamespace` hashes `natives.Version`, mode and the rag.js bytes (runtime.go:77-94) → any generated asset change already creates a new namespace.
- Lineage (lineage.go): per-lineage JSON list newest-first, truncated to `LineageKeep=32`; `LineageList` returns up to `LineageSearch=8` still-stored entries. `GC(keep)` walks lineage files only; deletes stored entries beyond `keep`; anything truncated off the 32-entry list is never seen again → leak (finding #3 >32 versions). Outputs without lineage key are never collected. Temp dirs > 1h removed — could remove a long-running live build's temp dir (source deduction; no lock). `store gc` CLI (cmd/ragwb/cmds/store.go:96-106) opens the namespace with no process lock while `serve` may be running.

### Query side (`session.js`)
- `Session.run`: `art = engine.artifacts(ir)`; if null awaits `buildShared` and re-reads. Keeps `art` across all awaits of live nodes (LLM, decide, rerank). Session `memo` of live node outputs reset when `art.key` changes. Hits normalized by `normHits` to `{id, score, via, ...}` — **no dataset identity**. Components resolve with `ctx.chunk(id)` → `art.chunks.get(id)` (≈15 sites in builtins.js; hits.js viewHit; events.js hitsEvent).
- Result decorated with non-enumerable `chunk/top/explain/toJSON` closing over `art` (session.js:361-392) → hidden live handle use after return.
- `when` failure fails open (`when failed → run`); decision missing → run.

### Served app (`pkg/runner/serve.go` harness)
- `__serve.run`: `session.run(...)`, then `sc.close()` (not in finally), then `collect()` (gc if `art().key` changed), then `pack(art(), ...)` with **current** artifacts — may be newer than the ones the query used; ids from the old generation resolved against new chunks (or missing). `__serve.chunk/doc` look up by bare id in current artifacts.
- `refresh`: `if (eng.artifacts(ir)) return 'null'` else build + collect. Triggered by `watchApp` ticker (cmd/ragwb/cmds/serve.go:169-191) with 10-min timeout, and implicitly by a query that finds artifacts missing.
- Cancellation: Go goroutine on ctx.Done posts `abortScope` to owner (serve.go:276-310).

### HTTP (`pkg/server`)
- `handleRun` (rag.go:227-301): no admission (`acquire`) — only verbs use the inflight semaphore (verbs.go:239). Stream: `newSSEWriter` writes header; pump goroutine; after `a.Run` returns, `sink.close(); <-pumped`.
- `sse.go`: `streamSink.Emit` waits ≤100ms on full queue then cancels request and sets `slow`. `sseWriter.write/ping` ignore flush errors; pump ignores write errors (`_ = s.write`). No write deadline. If the client stops reading, TCP buffer fills, `Fprintf` blocks forever in pump; handler blocks on `<-done`; verb slot never released (finding #4 P1). Coalesced `pending.Delta` grows unbounded while text arrives continuously within 30ms windows (flush happens only on timer, so practically bounded per 30ms but no byte limit).
- `handleVerb`: different terminal logic (slow_client error, cancellation, envelope). Duplicate orchestration with handleRun.

### Async bridges
- `sample.go newSample`: classifies outcome in goroutine **before** posting settle (correct order). Abort listener never removed. Tool exec posts to owner and waits on channel or ctx.
- `decide.go newDecide`: classifies in goroutine before post (`classifyDecideError(err, callCtx, lifetime)`), `defer cancel()` after post — classification already done → correct.
- `rerank.go rerankAsync` (rerank.go:145-171): `defer cancel()` runs when goroutine returns, i.e. right after posting; the posted settle callback later evaluates `callCtx.Err() != nil` → a genuine provider failure is reported as `cancelled` (finding #5). Confirmed by reading.
- Three near-identical lifecycles; differences are what the original calls "different outcome handling".

### Model selection (`pkg/llm/gepp/backend.go:114-139`)
- `preferred`: `enabled(ref)` treats empty `EnabledModels` as "all enabled", but the fallback loop iterates `s.EnabledModels` — empty → no model even though eligible models exist (finding #6). Fix: when empty, iterate eligible refs in sorted order.

### Graph (`pkg/ragjs/src/graph/graph.js`)
- `finalize` (405+) discovers: data inputs `n.in`; artifact uses via `def.uses(p)` (e.g. retrieve `['ix-'+p.index]`, builtins.js:180, 525, 665) plus an implicit `synonyms` special case; `when` decision refs via `whenRefs` (declarative `decision: 'node.q'` string, or regex over lambda source); signal reads via lambda source scan.
- `applyOp 'rename'` (877-884): rewrites `in` and `outputs` only. Misses `when.decision`, `uses`-derived params (`p.index`, `p.indexes`), lambda-source decision refs; no target-collision check (finalize catches duplicates afterwards). Renaming `ix-text` breaks `retrieve('text')` as well — broader than the review's "decision references".
- `applyOp 'remove'` (839-866): rejects removal when `uses` users exist; does **not** check `when` dependents.
- `exportHtml` (export/html.js): removes every live node with `trigger === 'submit'` except answers, then answers/contexts. A classify node (submit-triggered, assumed) removed → retrieve with `when: {decision:'route.needs'}` fails finalize ("when reads decision ... does not exist") (finding #7). Exports `engine.files()` — only `rag.files` sources; `source.dir` corpora are not in it.

### Evaluation (`evaluation/evals.js:80-110`)
- Captures `art` once, then calls `session.run` per case; each run may see a different generation if a refresh lands mid-eval; `allChunks` from the first art. Mixed-generation evaluation is possible.

## Review findings — status of my verification

| # | Finding | Mechanism found in source? |
|---|---|---|
| 1 | duplicate chunk precedence differs stored vs cold | Yes: Map.set last-wins vs unionChunks first-view-wins, arrays beat views (engine.js:580-625, records.js:30-61) |
| 2 | bake receives view | Yes: resolveBakes passes `outputs.get` (engine.js:525-536) |
| 3 | dotted root | Yes: store.go:282 applies to root dir entry |
| 4 | two refreshes close artifacts under awaiting query | Yes: one-gc retirement (engine.js:410-438) + serve collect |
| 5 | >32 versions uncollectable | Yes: lineage.go:110-114 + GC walks lineage only |
| 6 | delta readers miss cleanup | Yes: delta.go:101 / markdown_native.go no addCloser |
| 7 | SSE non-reading client holds slot | Yes by deduction: blocking Fprintf, no deadline, `<-done` |
| 8 | cancelling first waiter cancels build | Yes: buildShared reuses first opts.signal |
| 9 | deferred cleanup turns failure into cancellation | Yes: rerank.go settle reads callCtx after defer cancel |
| 10 | empty enabled list | Yes: backend.go:133 |
| 11 | lexical export dangling decisions | Yes by deduction: export removes classify; remove ignores when refs |
| 12 | rename misses decision refs | Yes: graph.js:877-884 (and also misses `p.index`) |

Additional source-found issues not in the twelve: `__serve.run` packs from current artifacts; `sc.close()` not in finally; evals can mix generations; `enrich.decide` not excluded from "primary" chunk stream; `handleRun` has no admission; store `writeAtomic` has no fsync; GC deletes 1h-old temp dirs without knowing live builders; `buildShared` key omits semantic options.

## Decisions the original leaves underspecified (to raise in the guide)

1. **Dataset identity value**: build digest vs store key. Proposal: dataset id = node build digest (already includes def.src and inputs); store key derived from it.
2. **Fusion over different datasets**: can be decided at compile time because every hits stream's dataset node is statically known (retrieve → index → its chunks input). Custom `retrieve(fn)` lambdas have unknown dataset → must declare `dataset` or emit full refs. Policy for mixed fusion: compile error unless an explicit projection/canonicalization component is inserted. Needs owner decision on default.
3. **Which dataset is "the corpus" for ctx.chunk/neighbors/order and bakes**: today "first chunks node". Proposal: each consumer declares; default = the dataset of the hit's ref; reading order from the dataset that the chunk belongs to (ChunkDataset.order). Enriched datasets preserve order from their input.
4. **Snapshot granularity**: per plan build set. Serve has the app plan plus verb plans (`recentIrs`, max 8). Proposal: snapshot keyed by "build key" = sorted build-node digests of a compiled plan; registry shares handles across snapshots.
5. **Partial (deferred-LLM) outputs**: `artifacts()` accepts `partial` entries. Is a partial snapshot publishable? Proposal: yes, flagged `partial: true`, never durable-published. Open.
6. **Retention policy** for durable GC (how many old snapshots): open; original says deliberate policy including privacy.
7. **Detached result budget** default size and error vs pagination: open.
8. **Stale lookup response**: HTTP status/code for stale generation (e.g. 410 `stale_generation`) and how long retired snapshots stay resolvable for lookups (only while leased? short TTL?). Original says "resolve against retained generation or return stale" — retention for lookups needs a rule. Proposal: lookups resolve only against current or still-leased snapshots; otherwise stale. Simple, no TTL.
9. **Background refresh owner**: watchApp. Who cancels it? Engine shutdown.
10. **Race rule for terminal outcome vs cancel**: original: accepted terminal result first stays terminal. Need arbiter: the owner-goroutine settle. Proposal: arbiter is the single `settle` CAS in Go before posting.
11. **Deletion policy for graph remove with dependents**: reject by default; cascade only explicit. Export projection policy: removing classify → strip `when` that reads it (unconditional). Needs confirmation that this is the desired export semantics (original says yes, documented).
12. **Admission for direct runs**: original says apply same admission scope; current handleRun has none — decide whether direct queries share the verb semaphore. Proposal: one per-app semaphore for both.
13. **Store lock mechanism**: flock on Unix; other platforms? `nlink_other.go` exists, so non-Unix builds exist. Open: Windows lock or fail.

## Plan for the guide structure
Purpose → system walkthrough with one running example (reports corpus + enrich + classify-gated retrieve + serve with --watch) → findings mapped to mechanisms → target contracts (datasets/refs; snapshots/registry/results; store; operations/jobs/SSE; graph refs/export) with decision records → migration and phases → tests → open decisions. Keep Go/TS sketches labelled as proposals.
