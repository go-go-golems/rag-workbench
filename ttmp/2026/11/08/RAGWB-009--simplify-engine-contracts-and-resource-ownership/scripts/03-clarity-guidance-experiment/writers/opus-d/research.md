# Research notes for the RAGWB-009 replacement guide

Scope: read-only inspection of the working tree on `task/ragwb-009-writing-experiment` (runtime baseline stated by the context packet as `c267f91`; this branch adds documentation only). I read source and tests by name only; **I ran no commands or tests.** Everything labelled "deduction" below is inferred from reading code, not observed behaviour.

## Reader and task

Reader: a developer new to rag-workbench who must implement the 009 cleanup. They need to understand (1) what the engine does for an app author, (2) where today's boundaries cause the twelve review findings, and (3) the target contracts, phase order and tests. Compatibility may break; no adapters.

## System understanding (current)

- **Builders → IR.** `rag.corpus(...)` and `rag.plan(...)` are immutable builders (`pkg/ragjs/src/graph/graph.js`). `compile` → `finalize` produces an IR: nodes `{id, t, p, in, phase, out, uses, when, reads}`, edges and `outputs`. Phases: `signal`, `build` (per corpus change), `live` (per query). Example: `examples/apps/reports/lib/corpus.js`, `lib/plans.js`, `examples/presets/reports.js`.
- **Build.** `Engine.build` (`engine/engine.js:147`) walks build nodes in order. Each node's digest = hash(type, params, input digests, source-file digests or `def.digest`, component source) (`digests`, :58). Memo `this.memo: digest → {value,count,lazy?}`. If an artifact store is configured (`env.artifacts`, installed by `natives/persist.go`), stored outputs become `Lazy` entries loaded on first read (:184–201). Otherwise compute: delta (`tryDelta`, :443), map (per item, with item cache and LLM call budget/deferral → `this.partial`), or `all`. Successful outputs are saved and a lineage entry recorded (:346–364).
- **Lineage** (`pkg/artifacts/lineage.go`): per "this node of this pipeline" key, newest-first list of `{storeKey,digest,inputs}`, truncated to `LineageKeep = 32`; build search considers `LineageSearch = 8`. `Store.GC(keep)` walks lineage files and deletes non-kept entries *listed there*. An output dropped from the 32-list by `LineageRecord` is never listed again → never collected (finding confirmed by reading).
- **Artifacts view.** `Engine.artifacts(ir)` (:556) returns null if any digest is missing (i.e. sources changed). Else builds one cached object `{key, byId, chunks, order, indexes, synonyms, docs, links}`. `chunks` is a union: in-memory arrays put into a `Map` (later stream overwrites earlier on same id), stored streams appear as `RecordView`s consulted only for ids the Map lacks (`unionChunks`, `records.js:30`). So precedence depends on storage: a computed array always beats a stored view regardless of stream order. `order` comes from the first non-enrich chunk node (:608).
- **Record view.** `recordsViewJS` in `natives/persist.go:437`: iterates, `get/has/size/order/toArray/toJSON`, has `length`, no `map`/`filter`. `isView` = has `__records`. `asArray` materialises.
- **Query.** `Session.run` (`engine/session.js:37`) takes `art = engine.artifacts(ir)`; if null it awaits `engine.buildShared(ir, {signal: opts.signal, onEvent: opts.onEvent ...})`. It runs live nodes in order, awaiting providers, with `art` captured across awaits. Hits are `{id, score, why, parts, reason}` — a bare chunk id, no dataset. Result is decorated with non-enumerable `chunk/top/explain/toJSON` closures over `art` (`decorateResult`, :361).
- **Served app.** `pkg/runner/serve.go` harness: `__serve.run` awaits `session.run`, then `collect()` (which calls `eng.gc` if the artifacts key changed), then packs chunk bodies/neighbours from `art()` — the *current* artifacts after the query, not the ones the query used (:141–156). `__serve.refresh` calls `eng.build` directly (not `buildShared`) when `artifacts(ir)` is null. `chunk`/`doc` lookups use current `art()` by bare id.
- **GC of handles.** `Engine.gc` (:410): memo entries not in the live digest set are moved to `this.retired`; values retired by the *previous* gc are closed now. Two rebuilds while one query awaits → its handles closed (finding confirmed by reading: query holds `art`; nothing counts it).
- **Native resource cleanup.** `Persist.InstallEnv` registers `addCloser(r.Close)` for loaded records/records2/bleve (`persist.go:209,227,245`), with a no-op fallback when `env.AddCloser` is nil (:47–50; also `ragjs/module.go:143`, `bm25_native.go:124`). Delta outputs: `markdown_native.go` `parseDelta`/`chunkDelta` return `records2Object(vm, r)` with no `addCloser` → reader never registered (confirmed). `tryDelta`'s `loadBase` pushes opened bases onto `retired` (:466).
- **Bakes.** `rag.bake(fn)` → `{ $bake: src }` (`graph.js:9`). `resolveBakes` (`engine.js:517`) passes `outputs.get(firstDocsNode)` / first chunks node — array when computed, RecordView when stored. Cache key `hash([docs.map(d => d.id + d.sections.length), chunks.length])`: deduction — a RecordView has no `.map`, so a stored docs input would throw; equal ids/section counts with changed text reuse a stale bake (confirmed by reading).
- **Shared builds.** `buildShared` (:695) keys on joined digests, returns the first caller's promise; the build uses the first caller's `signal` (abort → `'build stopped'`, :288) and `onEvent`. A second waiter's cancellation has no effect; the first waiter's cancels everyone (confirmed).
- **Async bridges.** `ragjs/sample.go`, `ragjs/decide.go`, `natives/rerank.go` repeat: `callCtx := WithCancel(Lifetime)`, add abort listener (never removed), goroutine with `defer cancel()`, post settlement to owner. sample/decide classify on the worker before posting; **rerank classifies inside the posted callback** (`rerank.go:156–161`) after `defer cancel()` has run, so a provider failure reads `callCtx.Err() != nil` → reported `cancelled` (confirmed by reading). decide maps `DeadlineExceeded` to `cancelled` too (`decide.go:103`).
- **SSE.** `pkg/server/sse.go`: `streamSink.Emit` waits ≤100 ms on a full 256-entry queue then cancels the request. `pump` ignores `write`/`ping` errors; `Flush` has no error and no deadline. `handleRun` (`rag.go:227`) and `handleVerb` (`verbs.go:276`) each start a pump and then `<-pumped`/`<-done` — deduction: if the client stops reading, `Fprintf` blocks in the pump forever, the handler never returns, and the verb's inflight slot (`acquire/release`, `rag.go:52`) is never released. `handleRun` does not use `acquire` at all (direct queries have no admission control). Verb path writes a further error event after slow-client cancellation, also unbounded. `sink.slow` read without lock.
- **Graph references.** Kinds: data `in`, `uses` derived per component (`retrieve` uses `'ix-' + p.index`, `builtins.js:180`) — a naming convention, not a stored reference; `when: {decision: 'route.needs'}` string parsed by `whenRefs`; lambda `.decision('x')` detected by regex (`graph.js:356`); `outputs`. `applyOp 'rename'` (`graph.js:877`) rewrites `in` and `outputs` only; `when.decision` and `p.index` untouched → `finalize` rejects afterwards ("when reads decision … does not exist"). `remove` checks `uses` users but not `when` users.
- **Export.** `export/html.js`: removes live `submit`-trigger non-answer nodes, then answer/context nodes. For the `adaptive` plan (`plans.js:81`), `classify` (`route`) is submit-triggered and removed while `retrieve` nodes keep `when: {decision: 'route.needs'}` → deduction: `applyOps → finalize` throws, export fails. Export embeds `engine.files()` which for `source.dir` apps may not be the corpus.
- **Identity.** `docIdFor` (`core/identity.ts:29`) = slug of path only; no source identity; case/punctuation variants collide (`notes/A B.md` vs `notes/a-b.md`). `artifacts` keeps first doc on duplicate and logs.
- **Store listing.** `artifacts.List(dir)` (`store.go:272`) skips any directory whose name contains `.` — including the root itself if its basename has a dot (`~/.ragwb-store`) → empty listing (confirmed by reading).
- **Model fallback.** `gepp/backend.go:114` `preferred`: with empty `EnabledModels` and unavailable default, loops over the empty list → no model even though `eligible` is non-empty (confirmed). Fix: deterministic choice from eligible (sorted refs).
- **Browser shell.** `web/app-shell/shell.js` caches chunks by bare id forever and decorates results with id-based `chunk/top`; negative cache `missing` is permanent.
- **010 foundation.** `pkg/ragjs/src/README.md`: authored sources under `pkg/ragjs/src`, `pnpm build` generates `pkg/ragjs/assets/rag.js`, `pnpm check` (TS, Biome, generation, bundle/module-graph tests), Go tests `go test ./pkg/ragjs ./pkg/runner ./web`. Strict TS only for `core/errors.ts`, `core/identity.ts`, `core/text.ts`, `engine/scope.ts`. `scope.ts` signal has `removeEventListener`.
- **No namespace lock** anywhere (`Flock` absent). `ragwb store gc --keep 3` documented as "run while no serve uses an old version".

## Review-finding verification summary

| Finding | Status from source reading |
|---|---|
| Stored duplicate chunks pick different version from cold arrays | Confirmed mechanism (`unionChunks` + Map) |
| Bake receives record view | Confirmed (`resolveBakes`); `.map` throw is deduction |
| Dotted store root | Confirmed (`List`) |
| Two refreshes close in-use artifacts | Confirmed mechanism (`gc` retired one-cycle) |
| >32 versions uncollectable | Confirmed (`LineageRecord` truncation + `GC`) |
| Delta readers miss cleanup | Confirmed (`markdown_native.go`) |
| Non-reading SSE client holds slot | Deduction from blocking write + join; plausible |
| First waiter's cancel cancels build | Confirmed (`buildShared`) |
| Deferred cleanup turns failure into cancel | Confirmed for rerank; sample/decide classify earlier |
| Empty enabled list blocks fallback | Confirmed (`preferred`) |
| Export retains removed decisions | Deduction: export throws for `adaptive` |
| Rename misses decision refs | Confirmed (`applyOp rename`) |

Additional observations not in the original list: direct `/run` has no admission control; `refresh` bypasses `buildShared` (a refresh and a query-triggered build can run concurrently for the same plan); `decide` maps deadline to `cancelled`; abort listeners are never removed; `__serve.run` calls `sc.close()` only on success path (deduction: on exception the scope stays open — harmless today).

## Underspecified decisions in the original (to surface in the guide)

1. **Snapshot key/granularity.** Original sketch uses `plan.buildKey`. Need: what identifies "the same build" across source changes? Proposal: the plan's *build identity* = hash of build nodes' structure (lineage-style keys without source digests). Several plans over one corpus share it if build subgraphs match.
2. **Do queries trigger rebuilds?** Today a query that sees changed sources builds. Original: query waits only if no usable snapshot. Proposal: queries read the current snapshot; changed sources start (or join) a background refresh. Behavioural change to state.
3. **Partial (deferred-LLM) builds.** Are they publishable snapshots? Original says publish only after complete validation; current engine serves partial outputs. Proposal: publishable, flagged `partial`, because deferral is intended behaviour, not invalidity.
4. **Stale generation lookups.** How many old snapshots are kept open for browser lookups? Proposal: current + previous; older returns `stale_generation`.
5. **Fusion across datasets.** Dedup by exact reference; distinct datasets stay distinct; explicit `canonical` option only if needed. Reports preset already fuses sections+failures (different datasets) legitimately.
6. **Duplicate adoption.** Choose: reuse existing entry, close redundant handle.
7. **Removal of referenced nodes.** Choose: reject by default; export uses its own projection.
8. **Result budget values**: implementer default; not blocking.
9. **Retention count** for durable snapshots: default (e.g. 3 published) — implementer choice.
10. **Delta base opened for a delta only** — under registry it is a builder-scope resource released at publish/abort.

## Unresolved / limits

- I did not verify the SSE slot leak with a socket; it is a deduction.
- Whether Goja `toArray()` of a large records2 is acceptable performance — not measured.
- Did not inspect all custom component consumers of `ctx.chunks` / `art.chunks`; the guide asks implementers to enumerate them (`rg "ctx.chunk|art.chunks|asArray|isView"`).
- Fsync/directory-sync behaviour of `writeAtomic` not inspected in detail.
