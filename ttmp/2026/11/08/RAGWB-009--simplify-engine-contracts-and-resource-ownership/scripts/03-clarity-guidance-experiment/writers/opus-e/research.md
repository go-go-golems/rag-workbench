# Research notes for the RAGWB-009 replacement guide

Inspected: working tree of `task/ragwb-009-writing-experiment` (runtime/source baseline c267f91; the branch adds documentation only). All findings below are from reading source. No tests or programs were run.

## Reader and task

Reader: an intern or new engineer who knows Go and JavaScript but not this project. They must understand what the engine does, why the current composition of datasets / artifacts / handles / builds / transport is fragile, and what the 009 refactor changes, well enough to start phase 1 (characterization tests) and then implement phases.

Scope (from original design + context): the twelve review findings and the target contracts: datasets with provenance, snapshots with leases, resource registry, authoritative store inventory + GC, one async-operation bridge, shared build jobs with waiters, one bounded SSE transport, canonical graph references + capability-based export. Compatibility may break; no adapters. New contracts in strict TypeScript (010 established `pkg/ragjs/src`, `pnpm build`).

## How the system works today (observed in source)

### Builders → IR
- `pkg/ragjs/src/graph/graph.js`: `CorpusSpec`, `PlanSpec` build raw node lists; `compile` → `finalize(raw)` validates ids, ports, `uses` edges (artifact), `when` decision edges, signal reads from lambda source; topological order; picks default outputs (`hits`, `context`, `answer`). Throws `RagError('compile', …)` with `problems`.
- Nodes: `{id, t, phase: 'build'|'live'|'signal', in, p, trigger?, when?}`. Build nodes: source/parse/chunk/enrich/index/bake. Live nodes: retrieve/fuse/filter/rerank/pack/answer/classify.
- `applyOps(ir, ops)` edits a raw copy and refinalizes. `rename` (graph.js:877) rewrites `in` and `outputs` only. Misses: `when.decision` strings ('route.needs'), lambda `.decision('route')` refs, `p.index` names used by `uses` (retrieve `uses: p => ['ix-' + p.index]`, builtins.js:180), pinned names are output keys (ok). `remove` (839) checks `uses` users but not `when` readers.
- References are discovered in several places: `n.in`, `def.uses(p)`, `whenRefs(n.when)` (regex over lambda source for `.decision('x')`), `def.reads(p)` + `signalReads(lambda)`; the `retrieve` + `synonyms` implicit use (graph.js:455).

### Build (engine.js `Engine.build`)
- `digests(ir)`: per build node hash of type, params, input digests, extra (file digests for `source.files`, `def.digest`), and `def.src` for rag.define'd components.
- `memo`: digest → `{value, count, lazy?}`. Stored outputs: `store.has(storeKey)` → `Lazy` that loads on first read via `codec.load(store.load(sk))`.
- Delta path: `tryDelta` uses `lineages(ir)` keys and `store.lineageList(lin)` (newest ≤ 8 still stored) to find a base whose recorded input digests equal the current inputs' "from" digests; component `def.delta.apply(base, inputs, ctx, p)`.
- Map components (enrich) run per item with concurrency, item cache `this.items`, `env.cache`, call budget, deferred/partial outputs when calls not allowed.
- After compute, `memo.set`, then `store.save(sk, …)` and `store.lineageRecord(lin, sk, d, inputDigests)`.
- `resolveBakes` (engine.js:517): finds the *first* docs node and first chunks node with outputs; `outputs.get(id)` forces Lazy and may return a RecordView (stored) or an array (cold). Bake key: `hash([docs.map(d => d.id + d.sections.length), chunks.length])` — call `.map` on a RecordView fails (RecordView has no `map`), and equal ids/section counts with changed text give same key → stale bake. Confirms findings #2 (bake receives view) and cache-key gap.
- `buildCtx.firstOf` uses `asArray` (so build components get arrays), inconsistent with bakes.

### Artifacts (engine.js `artifacts(ir)`, line 556)
- Returns null if any digest missing from memo/partial (this is also how "sources changed" is detected: `source.files` digest includes file text hashes, and `source.dir` presumably via def.digest).
- Builds `art = {key, byId, chunks, order, indexes, synonyms, docs, links}` cached by `key` (concatenation of digests).
- Global chunk map: for every chunks build node in order, arrays `chunks.set(c.id, c)` (later stream wins); views collected separately and combined by `unionChunks(map, views)` (records.js:30): `get` prefers map (arrays) then first view; `values()` yields views first (skipping ids in map), then map. So: cold (all arrays) → last stream wins; stored → arrays beat views regardless of position, first view beats later views. Precedence depends on storage. Confirms #2 duplicate-chunk finding.
- `order`: reading order from the "primary" chunk stream = first chunks build node that is not `enrich`/`enrich.llm` (note `enrich.decide` is not excluded — an inconsistency).
- `indexes`: keyed by `n.p.name || id without 'ix-'`. Index `search` returns hits `{id, score, ...}`: chunk id only. Hits are resolved later against `art.chunks` (global union), not against the dataset the index was built from.
- Docs: first doc with an id wins, duplicates logged.

### Query (session.js `Session.run`)
- `art = engine.artifacts(ir)`; if null → `engine.buildShared(ir, {signal: opts.signal, onProgress, onEvent})` then re-read artifacts.
- Live nodes run in order with memo per node (digest, identical inputs, signal reads). `ctx.chunk(id)` = `art.chunks.get(id)`. `ctx.index(name)`. Awaits on LLM (`sample`, `decide`, rerank) while holding `art`.
- Result: `{runId, trigger, signals, hits, context, answer, outputs, trace, errors, ms, calls}` decorated with non-enumerable `chunk`, `top`, `explain`, `toJSON` closing over `art` (live handles behind views).
- Cancel: an error with code `cancelled` breaks the loop.

### Shared builds (engine.js:695 `buildShared`)
- key = all digests joined. Second caller gets the same promise; only an event `{type:'build', waiting:true}`. The build runs with the first caller's `signal`, `onProgress`, `onEvent`. First caller aborting aborts the build (map loop checks `opts.signal.aborted`; sample/decide calls also get it via `buildCtx`). Confirms #4 first-waiter finding. Progress events go only to the first caller.

### Engine GC (engine.js:410)
- `gc(irs)`: computes live digests for the given IRs; memo entries not live are dropped and their values pushed to `retired`; values retired at the *previous* gc and not live now are closed (`__records.close` or `__ix.close`). So a handle survives exactly one extra gc. A query that started before two refreshes can see its handles closed. Confirms #3.
- `tryDelta.loadBase` pushes delta-only opened bases to `retired`.
- Called from serve's `collect()` after refresh and after every run when `art.key` changed.

### Go native resources
- `pkg/natives/persist.go` `InstallEnv`: if `env.AddCloser` nil, uses no-op (line 47–50). `load` registers `addCloser(r.Close)` for records/records2/bleve opened from the store. `module.go:143` also defaults AddCloser to a no-op.
- Delta: `markdown_native.go` `parseDelta`/`chunkDelta` return `records2Object(vm, r)` for a newly created reader; JS wraps with `ctx.store.view(...)`. Never passes through `addCloser` → only closed if engine.gc happens to retire it. Confirms #3 delta-reader finding. Temp dirs from `tempDir` removed at runtime close.
- So resources have two owners/closers: runtime closer list and engine gc (possible double close; `Records2.Close` idempotency not verified).
- RecordView (persist.go:437) caches up to 4096 decoded objects; returned objects are mutable JS objects; mutation by a callback would persist in the cache. Memory arrays are shared mutable objects too.

### Store (pkg/artifacts)
- Layout: `<dir>/<ns>/<key[:2]>/<key>.json` meta + `.bin` or `<key>.<codec>` file/dir; `lineage/<l[:2]>/<l>.json`; `.tmp-*` dirs.
- `Save`/`savePath`: payload then meta, each via rename; no fsync (writeAtomic does not Sync file or dir).
- `LineageRecord` keeps `LineageKeep = 32` newest; `LineageList` returns ≤ `LineageSearch = 8` still-stored.
- `Store.GC(keep)` (lineage.go:159): walks lineage files only; keeps newest `keep` stored per lineage; deletes others *listed*; versions that dropped off the 32-entry list are never seen → uncollectable. Outputs without lineage are kept. Removes `.tmp-*` older than an hour. No lock; CLI doc says "Run it while no ragwb serve uses an old version, or keep at least 2". Confirms #3 32-entry finding and cross-process risk.
- `List(dir)` (store.go:272): WalkDir skips any directory whose name contains '.', including the root itself if its basename has a dot → lists nothing. Confirms dotted root finding (#2).

### Serve (pkg/runner/serve.go harness)
- Opens app: `eng.build(ir)`, `eng.artifacts(ir)` warm, `session = eng.session(ir)`.
- `refresh`: if `eng.artifacts(ir)` non-null → 'null'; else `eng.build(ir)` then `collect()` → `eng.gc([ir, ...recentIrs])`. Called by `watchApp` ticker (cmd/ragwb/cmds/serve.go:169).
- `run`: new scope, `session.run`, `sc.close()` (not in finally), `collect()`, then `a = art()` (CURRENT artifacts — possibly newer than what the query used) and `pack(a, ids, …)` chunk bodies + neighbors. So result packing reads a newer generation. Confirms original's §4 last observation.
- `chunk(id)`, `doc(id)` lookups use current art; no generation.
- Go `Run`: goroutine aborts scope on ctx.Done via `Owner.Call`.

### Web shell (web/app-shell/shell.js)
- Caches chunks/neighbors/docs by id for the page lifetime; `missing` set is permanent. No generation awareness. `result.chunk(id)` reads cache.

### HTTP/SSE (pkg/server)
- `sse.go`: `streamSink.Emit` queues (256); if full for 100 ms, marks slow and cancels request. `pump` ignores write errors (`_ = s.write(...)`), no write deadline; `Flush()` has no error. If client stops reading, `Fprintf` eventually blocks forever in pump.
- `rag.go handleRun`: no admission control; `sink.close(); <-pumped` waits for pump → blocks forever if pump stuck. `verbs.go handleVerb`: acquires inflight slot (`defer a.release()`), same `<-done` wait → slot never released. Confirms #4 P1.
- Both handlers duplicate: SSE setup, sink, pump goroutine, terminal event writing, with different error vocab (`run_failed`, `verb_failed`, `slow_client`). `handleVerb` reads `sink.slow` without lock.
- Terminal event: "Every stream ends with exactly one result or error" comment, but on client-cancel verb writes none (ok), and run writes result even after slow cancellation.

### Async bridges
- `pkg/ragjs/sample.go newSample`: classifies failure *before* posting settle (ok); abort listener never removed; tool exec posts to owner, waits on done or ctx.
- `pkg/ragjs/decide.go newDecide`: classifies before posting; maps DeadlineExceeded to "cancelled" (conflation).
- `pkg/natives/rerank.go rerankAsync`: goroutine `defer cancel()` runs after `PostWithLifetimeContext` returns; posted callback reads `callCtx.Err()` later on the owner → non-nil because of the deferred cancel → a provider failure reported as `cancelled`. Confirms #5.
- All three: same structure (promise, signal listener, context from `services.Lifetime()`, goroutine, post settle).

### Model selection (pkg/llm/gepp/backend.go:114 `preferred`)
- If default model unavailable and `EnabledModels` empty → loop over empty list → no model, even though `enabled()` treats empty as "all enabled". Confirms #6. Fix: when list empty, pick deterministic first eligible (sorted by ref).

### Export (pkg/ragjs/src/export/html.js)
- Removes live submit-triggered non-answer nodes (classify, rerank.llm, rewrite.llm…) then answer/context. A remaining node with `when: {decision: 'route.x'}` makes `finalize` fail (decision does not exist). Confirms #7. Uses `engine.files()` as corpus — for `source.dir` corpora (server-side) this is empty/incorrect.

### Evaluations
- `evaluation/evals.js` reads `engine.artifacts(ir)` and `art.chunks` directly, iterates all chunks.

## Evidence gaps / not verified
- Did not run the regressions; did not confirm `Records2.Close` idempotency or double-close effects.
- Did not inspect `source.dir` digest implementation in detail (sourcedir.go) — assumed it contributes file stats/TTL.
- Did not inspect go-go-goja `runtimebridge.PostWithLifetimeContext` semantics after runtime close (assume returns error and does not run callback).
- `http.ResponseController` availability through any middleware wrappers not checked (server has no visible wrappers in rag.go).
- Original guide's PR numbers/priorities taken as given (review labels).

## Decisions the guide must surface (underspecified in original)
1. Fusion over differently enriched datasets: which policy (reject by default vs canonical projection). Proposal: reject mixed-dataset fusion unless a declared `project`/canonical dataset; exact-ref dedupe otherwise.
2. Default reading order (`order`) when several chunk datasets exist: proposal — order belongs to each ChunkDataset; neighbors resolved within the hit's dataset.
3. Duplicate-ID registry adoption policy: proposal — reuse existing, close redundant new handle.
4. Result materialization budget values and error code.
5. Stale-generation response shape for lookups; how many retired snapshots stay resolvable (proposal: only snapshots still leased or retained by policy; otherwise `410 stale_generation`).
6. Deletion policy for graph edits (reject by default; cascade explicit).
7. Retention policy for durable snapshots (count N; privacy note).
8. Whether background refresh owner keeps a job alive after all waiters leave (watch refresh = owner).
9. What "decision-only classifier omitted → retrieval unconditional" means for `when` with `not`/`max`: proposal — export projection rewrites `when` referencing removed decisions by dropping the condition (node always runs), documented.
10. Terminal race rule for async ops.
11. Where the namespace lock lives and how browser memory store behaves.
12. Hits shape: `ref: {dataset, id}`; GUI/JSON transport shape.

## Key files/symbols to cite
- graph.js: `finalize`, `applyOps`/`applyOp` rename/remove, `whenRefs`, `normWhen`.
- engine.js: `Engine.build`, `resolveBakes`, `artifacts`, `gc`, `tryDelta`, `buildShared`, `buildCtx`.
- records.js: `unionChunks`, `isView`, `asArray`, `Lazy`, `persistOf`.
- session.js: `Session.run`, `decorateResult`, `ctx`.
- persist.go: `InstallEnv`, `load`, `recordsViewJS`, `saveRecords2`.
- markdown_native.go: `parseDelta`, `chunkDelta`.
- records2_codec.go: `records2Object`.
- lineage.go: `LineageKeep`, `LineageRecord`, `GC`; store.go `List`, `savePath`, `writeAtomic`.
- serve.go: `serveHarness` (`refresh`, `run`, `collect`, `pack`), `ServedApp.Run`.
- sse.go `streamSink`, `pump`; rag.go `handleRun`; verbs.go `handleVerb`, `acquire`.
- sample.go `newSample`; decide.go `newDecide`, `classifyDecideError`; rerank.go `rerankAsync`.
- gepp/backend.go `preferred`.
- export/html.js `exportHtml`.
- shell.js cache.
- cmd/ragwb/cmds/store.go gc; cmds/serve.go `watchApp`.
