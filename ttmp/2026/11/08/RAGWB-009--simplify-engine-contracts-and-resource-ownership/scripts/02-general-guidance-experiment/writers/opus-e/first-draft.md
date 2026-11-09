# RAGWB-009: consistent datasets, snapshots, operations and graph contracts

An intern's analysis, design and implementation guide.

## 1. What this guide is for

rag-workbench lets an author describe a retrieval pipeline in a short JavaScript program, build it over a folder of Markdown notes, and query it from a CLI, a served web app or a standalone browser page. RAGWB-009 is a refactor of the engine underneath that program. It does not add user features. It replaces several boundaries that currently make correct behavior depend on accidents of storage, timing or call order, and it fixes twelve review findings that are symptoms of those boundaries.

The design is **proposed, not implemented**. Statements in "current behavior" sections describe the source tree (runtime source baseline `c267f91`); statements in "proposed" sections describe target contracts. Where a policy is not yet decided, the guide says so and gives a recommendation. Compatibility is explicitly not required: callers, tests, fixtures and stored formats are updated together, and no adapters, dual paths or legacy readers are kept.

The authored runtime lives in `pkg/ragjs/src/`. `pnpm build` generates the committed `pkg/ragjs/assets/rag.js`, which is loaded both by the Go host (Goja) and by browsers. Never edit the generated asset. `pkg/ragjs/src/README.md` describes the module layout and checks.

## 2. The system in one example

### 2.1 Corpus, plan, build and query

A **corpus** describes the work done when documents change. A **plan** adds the work done per query. Both are immutable builder values that compile to a graph (the **IR**): nodes with a component type, parameters, inputs and an output kind.

```js
const corpus = rag.corpus('reports')
  .from(rag.source.dir({ root: '/data/reports' }))   // files
  .parse(rag.markdown())                              // docs
  .chunk(rag.sections({ maxTokens: 450 }))            // chunks
  .index('text', rag.bm25());                         // index over chunks

const plan = rag.plan('search').over(corpus)
  .signals({ q: '', k: 10 })
  .retrieve('text', { k: 30 })                        // hits
  .filter.where()
  .limit();

const result = await rag.run(plan, { q: 'operation identity' });
result.top(5);
```

Nodes have a **phase**. *Build* nodes (source, parse, chunk, enrich, index) depend only on documents and are cached. *Live* nodes (retrieve, fuse, filter, rerank, pack, answer, classify) run per query and read **signals** such as `q`. The compiler (`finalize` in `pkg/ragjs/src/graph/graph.js`) type-checks ports, records edges and orders nodes topologically.

Three hosts run the same `rag.js`:

| Host | Entry point | Record storage |
|---|---|---|
| CLI scripts and verbs | `pkg/runner/runtime.go`, `pkg/runner/script.go` | Go natives, optional artifact store |
| Served app (`ragwb serve`) | `pkg/runner/serve.go` (`serveHarness`), `pkg/server/rag.go`, `pkg/server/verbs.go` | Go natives, artifact store |
| Browser page / HTML export | `web/app-shell/shell.js`, `pkg/ragjs/src/export/html.js` | JavaScript memory |

Go natives (`pkg/natives/`) replace some components with faster implementations: Markdown parsing and chunking, BM25 and Bleve indexes, and disk-backed record collections. Backends may differ in algorithms and scores. After this refactor they must not differ in collection, identity or ownership semantics.

### 2.2 The running example

The guide reuses one served app, `reports`, with two chunk streams:

```text
source.dir ──> parse.markdown ──> chunk.sections ──┬──> index 'text'  (bm25 over raw chunks)
                                                    └──> enrich ──> index 'ctx' (bm25 over enriched chunks)

live:  q ──> retrieve('text') ─┐
       q ──> retrieve('ctx')  ─┴──> fuse ──> pack ──> answer (LLM, streamed)
```

`enrich` prefixes each chunk with its document title and heading path, so it produces records with **the same chunk IDs** as `chunk.sections` but different text. The app is served with `--watch 30s`, so a watcher calls `refresh` periodically and rebuilds incrementally when a note changes. A user asks a question; the answer streams over Server-Sent Events (SSE) for twenty seconds. Sections 3 and 4 follow this request through today's code; later sections follow it through the proposed design.

## 3. How the current engine works

### 3.1 Build: digests, memo, stored outputs and deltas

`Engine.build` (`pkg/ragjs/src/engine/engine.js`) walks build nodes in order. For each node it computes a **digest**: a hash of type, parameters, input digests, file contents for file sources and, for components defined with `rag.define`, their source text (`Engine.digests`). Then:

1. If `this.memo` has the digest, reuse the in-memory value.
2. Else, if an artifact store is configured (`env.artifacts`) and holds the node's **store key** (`storeKey` in `engine/records.js`: digest plus component source), insert a `Lazy` memo entry that loads on first read.
3. Else try a **delta build** (`tryDelta`): find a previous stored version through the node's **lineage** — a key for "this node of this pipeline" that is stable across corpus edits — and let the component apply only the changes.
4. Else compute fully with `def.map` (per item) or `def.all` (whole input).

Successful outputs go into `this.memo` (keyed by digest, global to the engine), are saved through `store.save`, and the lineage gets a new entry through `store.lineageRecord`.

### 3.2 "Artifacts": the implicit current generation

There is no explicit snapshot. `Engine.artifacts(ir)` recomputes the digests for today's files and returns `null` if any is missing from memo. Otherwise it constructs (and caches by a key built from the digests) an object:

```js
art = { key, byId, chunks, order, indexes, synonyms, docs, links }
```

Two implicit rules live here:

- **Global chunk union.** Every chunk-producing node's records are merged into one `art.chunks`. In-memory arrays go into a `Map` (later nodes overwrite earlier ones); stored outputs are record **views** that stay on disk and are combined by `unionChunks` (`engine/records.js`). `unionChunks.get` consults the `Map` first and the views afterwards, whatever their graph order. In the running example, a lookup of a chunk ID returns the enriched record when both streams are cold arrays (enrich is later), but returns the raw record when `enrich` is cold and `chunk.sections` is a stored view — wait, the reverse: the cold array always wins over any view. Which bytes a query sees therefore depends on which nodes happened to be loaded from disk.
- **Primary reading order.** Neighbor navigation uses the first non-enrich chunk node as the "primary" stream.

### 3.3 Arrays versus record views

A stored chunk or document output is loaded as a `RecordView` (`recordsViewJS` in `pkg/natives/persist.go`), an object with `get/has/size/order/values/toArray` and a hidden `__records` Go handle. Engine code distinguishes it with `isView`/`asArray`. Build components get arrays via `asArray`; `resolveBakes` (bake callbacks, `engine.js`) does not convert, so a bake receives an array on a cold build and a view on a warm one. The bake cache key hashes only document IDs with section counts and the chunk count, so a changed note with the same section count reuses a stale bake.

### 3.4 Query and result

`Session.run` (`engine/session.js`) obtains `art` (awaiting `engine.buildShared` if it is `null`), runs live nodes, and gives each a `ctx` whose `chunk`, `docChunks`, `neighbors` and `index` closures read `art`. Hits are `{ id, score, ... }` with a bare chunk ID. `decorateResult` attaches `chunk`, `top`, `explain` and `toJSON` to the result; these also close over `art`, so a returned result keeps reading live native handles for as long as the caller holds it.

### 3.5 Serving, refresh and handle cleanup

`serveHarness` (`pkg/runner/serve.go`) builds once, then exposes `__serve.run`, `refresh`, `chunk` and `doc`.

- `run` awaits `session.run`, then calls `collect()` (which may run `Engine.gc`), then calls `art()` **again** and packs chunk bodies and neighbors from that result.
- `refresh` calls `eng.build(ir)` directly (not `buildShared`) when `eng.artifacts(ir)` is `null`, then `collect()`.
- `chunk`/`doc` read `art()` at request time by bare ID.

`Engine.gc` drops memo entries whose digests are not live and puts their values on `this.retired`; the *next* `gc` closes those values' Go handles (`__records.close()` or `__ix.close()`). The design intent was to give a query one rebuild of grace.

### 3.6 Native handles

`Persist.InstallEnv` (`pkg/natives/persist.go`) opens stored payloads in place (`OpenChunkRecords`, `OpenRecords2`, `OpenBleveBm25`) and registers each `Close` with the runtime's closer hook `env.AddCloser`, which falls back to a no-op function when the host supplies none. Delta builds create new record readers through a different path: `parseDelta`/`chunkDelta` in `pkg/natives/markdown_native.go` call `ParseDelta`/`ChunkDelta`, which open a `Records2` in a temp directory (`pkg/natives/delta.go`), and wrap it with `ctx.store.view` — without `AddCloser`. `Records2` opens its shard files lazily (`Records2.shard`), so a reader can need files long after it was opened.

### 3.7 The store and its collector

`pkg/artifacts/store.go` lays out one namespace as `<dir>/<ns>/<key[:2]>/<key>.json` (metadata) plus `<key>.bin` or `<key>.<codec>` (payload). Writes are temp-file-plus-rename (`writeAtomic`) without `fsync`. `pkg/artifacts/lineage.go` stores per-lineage history files with at most `LineageKeep = 32` entries; a delta build searches the newest `LineageSearch = 8`. `Store.GC(keep)` keeps the newest `keep` versions of each lineage and deletes the other *listed* entries, then deletes `.tmp-*` directories older than an hour. No lock protects a namespace; a separate `ragwb store gc` process cannot know which outputs a server is reading.

### 3.8 Native asynchronous calls

LLM sampling (`newSample`, `pkg/ragjs/sample.go`), decisions (`newDecide`, `pkg/ragjs/decide.go`) and reranking (`rerankAsync`, `pkg/natives/rerank.go`) each repeat the same structure: derive a context from the runtime lifetime, attach an abort listener to the JS signal, run the provider on a goroutine, post the settlement back to the Goja owner goroutine with `PostWithLifetimeContext`. Sample and decide classify the outcome on the goroutine before its deferred `cancel()`. Rerank classifies inside the posted callback — after the deferred `cancel()` has already run — so `callCtx.Err()` is always set and every provider failure is reported as `cancelled`. None of the three removes its abort listener.

### 3.9 Shared builds

`Engine.buildShared` keys in-flight builds by their joined digests. A second caller gets the first caller's promise. That promise was started with the first caller's `signal` and `onEvent`, so cancelling the first request cancels everybody's build, and build progress goes only to the first request.

### 3.10 HTTP and SSE

`pkg/server/rag.go` `handleRun` (direct queries) and `pkg/server/verbs.go` `handleVerb` (verbs) each construct an `sseWriter`, a `streamSink` and a pump goroutine, run the operation, close the sink, wait for the pump and write a terminal event. `streamSink.Emit` (on the Goja owner goroutine) waits up to 100 ms for queue space and then cancels the request. `sseWriter.write`, `ping` and `pump` ignore write and flush errors, and there are no write deadlines. `handleVerb` holds an in-flight slot (`acquire`/`release`, default 8); `handleRun` takes none.

### 3.11 Graph references

`finalize` discovers four kinds of reference independently: data inputs (`n.in`), artifact uses (`def.uses(p)`, e.g. `retrieve` with `p.index = 'text'` uses node `ix-text`), decision references in `when` conditions (`whenRefs`: the string `'route.needs'`, or a regex over a lambda's source for `.decision('route')`) and signal reads. Graph edits (`applyOps`/`applyOp`) handle each kind separately. `rename` rewrites only `in` and `outputs`. `remove` checks `uses` dependents but not `when` dependents. The HTML exporter (`export/html.js`) removes every submit-triggered live node except answers, then answers and contexts; it never rewrites dependents' `when` conditions.

## 4. Following the example through today's code

The user asks a question. The query runs `retrieve`, `fuse` and `pack`, then `answer` begins streaming tokens for twenty seconds.

1. While `answer` awaits the LLM, the watcher's refresh sees an edited note. `refresh` builds new chunk and index versions (incrementally), then `collect()` runs `gc`: the old versions move to `retired`.
2. Thirty seconds later the note changes again (or the user saves twice). The second refresh's `gc` closes everything on `retired` — including the record view and Bleve index the first query is still holding through `art`.
3. When `answer` resolves, `__serve.run` calls `art()` and packs chunk bodies from the **newest** generation. If a cited chunk changed or disappeared, the client receives text that does not match the evidence the model saw. If the query's `ctx.neighbors` or `result.chunk` touches a closed reader, the read fails.
4. Meanwhile the user's browser stopped reading (laptop lid closed). The TCP send buffer fills; `fmt.Fprintf` in the pump blocks with no deadline; `handleVerb` waits for the pump forever; its in-flight slot never returns. After eight such clients, the app answers `503 busy`.
5. Independently, `fuse` combined hits from `text` and `ctx` that share chunk IDs. Which record each hit resolves to depends on whether `chunk.sections` and `enrich` were cold or loaded (section 3.2), so the same query can cite different text after a server restart.

No step is a coding slip in isolation. Each comes from a missing contract: records have no dataset identity, a query has no fixed generation, handles have no owner, and transports have no bounded write.

## 5. Review findings and their causes

The six stacked PRs (#2–#7) have twelve unresolved findings. Priorities are the reviewers' labels. "Source" means this guide confirmed the mechanism by reading code; none was reproduced by running tests, so phase 1 of the implementation must add a failing regression for each.

| # | PR / prio | Finding | Mechanism | Evidence | Cause group |
|---|---|---|---|---|---|
| 1 | #2 P2 | Stored duplicate chunks select a different version than cold arrays | `unionChunks` prefers the array `Map` over views | `engine/records.js` `unionChunks`; `Engine.artifacts` | Datasets |
| 2 | #2 P2 | Bake receives a record view instead of an array | `resolveBakes` passes `outputs.get(...)` unconverted | `engine.js` `resolveBakes` | Datasets |
| 3 | #2 P2 | Store listing skips a root whose basename contains a dot | `List` `SkipDir`s any dotted directory name, including the walk root | `artifacts/store.go` `List` | Local fix |
| 4 | #3 P2 | Two refreshes close artifacts used by an awaiting query | `gc` closes `retired` one cycle later, with no query ownership | `Engine.gc`; `serve.go` `collect` | Snapshots |
| 5 | #3 P2 | Versions beyond 32 lineage entries become uncollectable | `LineageRecord` truncates; `GC` deletes only listed entries | `artifacts/lineage.go` | Store |
| 6 | #3 P2 | Delta-created record readers miss runtime cleanup | delta readers bypass `AddCloser` | `markdown_native.go`, `delta.go` | Resources |
| 7 | #4 P1 | Non-reading SSE client holds a slot indefinitely | no write deadline; ignored errors; unbounded join | `server/sse.go`, `verbs.go` | Transport |
| 8 | #4 P2 | Cancelling the first waiter cancels another waiter's build | shared promise carries the first caller's signal | `Engine.buildShared` | Operations |
| 9 | #5 P2 | Deferred cleanup turns failure into cancellation | rerank classifies after deferred `cancel()` | `natives/rerank.go` `rerankAsync` | Operations |
| 10 | #6 P2 | Empty enabled-model list prevents a valid fallback | empty list means "all enabled" but the fallback loop iterates the empty list | `llm/gepp/backend.go` `preferred` | Local fix |
| 11 | #7 P2 | Lexical export retains references to removed decisions | exporter removes classifiers but not dependents' `when` | `export/html.js` | Graph |
| 12 | #7 P2 | Rename fails to update decision references | `rename` rewrites only `in` and `outputs` | `graph.js` `applyOp` | Graph |

Findings 3 and 10 are local corrections with tests. The rest group into six causes, each addressed by one contract below. The packing-after-await problem in step 3 of section 4 is not a review finding but has the same cause as finding 4; fixing only handle lifetime would leave result packing reading a newer generation.

## 6. Target design overview

### 6.1 Modules

```text
DSL builders, CLI, HTTP, GUI
            │
            ▼
graph: canonical references, compile, edit, project
            │
            ▼
operations: build/refresh (shared jobs) · query · lookup
       │                         │
       ▼                         ▼
   build jobs ──publish──▶ snapshots ◀──lease── query execution
                              │
                      datasets + indexes
                              │
            resource registry (Go) + artifact store

native async bridge ──▶ typed outcome ──▶ JS promise
operation events ──▶ one HTTP responder (JSON or bounded SSE)
```

This is a set of modules with a single direction of dependency, not a workflow framework. No durable scheduler, plugin system, configuration framework or service container is introduced. A shared mechanism is justified only where two existing consumers need it.

### 6.2 Invariants

1. A record reference names both a dataset version and a record ID.
2. Loading an output from disk changes cost, never collection behavior.
3. A query reads exactly one published snapshot, including packaging of its result and events.
4. Every opened native resource has one registry entry and explicit owners.
5. A resource closes only when its last owner releases it.
6. Bounded lineage history is never the authoritative store inventory.
7. One waiter's cancellation cannot cancel a shared job another waiter still needs.
8. Every asynchronous operation settles with exactly one immutable outcome.
9. Every network write and flush has a bounded wait; a transport failure ends that transport.
10. Every structural graph reference is enumerated and rewritten by one visitor.
11. Ordinary searches need no close or release call from application code.
12. Each new mechanism replaces its old path in the same phase.

JavaScript cannot enforce linear ownership at compile time. These invariants are enforced by private handles, a single constructor path, checked references and sequence tests; native code can still have bugs and must be tested.

## 7. Datasets and record references

### 7.1 One collection type

Every document or chunk output becomes a read-only **dataset**, whether computed in memory, loaded from disk or produced by a delta build. Access stays synchronous: both current hosts read memory or local disk. A remote storage boundary would be a separate design.

```ts
// Proposed: pkg/ragjs/src/engine/dataset.ts (strict TypeScript)
export type DatasetId = string; // version identity, see 7.3

export interface RecordRef { readonly dataset: DatasetId; readonly id: string }

export interface Dataset<R extends { id: string }> {
  readonly id: DatasetId;
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): Readonly<R> | null;
  has(id: string): boolean;
  values(): IterableIterator<Readonly<R>>; // deterministic: build order
  materialize(): readonly Readonly<R>[];   // explicit full traversal
}

export interface ChunkDataset extends Dataset<Chunk> {
  order(docId: string): readonly string[]; // chunk ids of one doc, reading order, in this dataset
}
```

Two implementations exist: `MemoryDataset` over an array (browser, cold builds) and `StoredDataset` over a Go record reader (records2). The `RecordView` JS class, `isView`, `asArray`, `__records`, and the `toArray` convention are deleted. Records returned to callers are frozen (memory) or freshly decoded per read (stored); nested `meta` objects included. A conformance suite runs against both implementations: iteration order, missing IDs (`null`), immutability, `materialize`, `order`.

### 7.2 References instead of a union

Hits carry references:

```js
// Proposed hit shape
{ ref: { dataset: 'ds-7f3a…', id: 'q3-report#risks' }, score: 0.82, via: 'retrieve-text' }
```

Each index knows the dataset it was built from:

```ts
export interface Index {
  readonly dataset: DatasetId;
  search(query: string, options: SearchOptions): Hit[]; // refs into `dataset`
}
```

A snapshot (section 8) resolves a reference against exactly the dataset it names. There is no `art.chunks` union and no "primary" stream: neighbors of a hit come from `order` of that hit's dataset.

In the running example, `retrieve('text')` returns refs into the raw chunk dataset and `retrieve('ctx')` returns refs into the enriched one. Same logical ID, different datasets, different bytes. That is what the user's evidence should show, regardless of what was loaded from disk.

**Fusion.** `fuse` deduplicates exact references. Combining two indexes over the same dataset therefore behaves as today. Combining hits from different datasets that share logical IDs needs a stated rule. Proposed default: `fuse` keeps both entries unless given an explicit `canonical` option naming the dataset whose record represents a logical ID; with it, hits are re-pointed to that dataset before scoring, and IDs absent from it are dropped with a trace note. This is an ordinary component option, not an engine mechanism. (Decision D3, section 14.)

**Document identity.** Document IDs are currently derived from paths (`docIdFor` / `core/identity.ts`). With multiple sources, two vaults can contain the same relative path. Proposed: logical document identity is source identity plus vault-relative path; titles and slugs are never assumed unique. This changes IDs and therefore evaluation fixtures. (Decision D4.)

### 7.3 Version identity

A dataset ID is the artifact's store key when stored, or the build digest when memory-only. Both already include the component type, normalized parameters, component source and input digests. An index's identity derives from its component/codec version, parameters and input dataset ID. The native `natives.Version` constant and `runner.ArtifactNamespace` continue to separate incompatible binaries.

### 7.4 Callbacks and bake caching

Bake callbacks and custom components receive datasets:

```js
rag.bake(ctx => ctx.chunks.size);                         // count, no traversal
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id)); // explicit full read
```

The bake cache key becomes a hash of the input dataset IDs, the bake's source text and the component version. Two inputs with equal counts but different content have different dataset IDs, so the stale-bake case disappears. Every in-repository bake and `rag.define` callback is migrated in the same change.

`buildCtx.docs`/`chunks` currently return the first docs/chunks node found. They should name their input explicitly: build components already receive inputs positionally via `n.in`; `ctx.docs`/`ctx.chunks` become datasets of the node's declared inputs, or are removed where a component has none.

## 8. Snapshots, resources and result lifetime

### 8.1 Entities

| Entity | What it is | Owned by | Lifetime |
|---|---|---|---|
| Resource | One opened native handle (records2 reader, Bleve index, in-memory BM25) | Go `Registry` of one runtime | until last owner releases |
| Build scope | A builder's private set of resources for a candidate | the build job | until publish or abort |
| Snapshot | Immutable map: node id → dataset/index, plus snapshot ID and generation number | snapshot table of one engine | until retired **and** leases = 0 |
| Lease | A query's (or lookup's) hold on one snapshot | the query operation | the query, including packaging |
| Result | Detached plain data copied from a snapshot | the caller | caller-controlled; no handles |

A snapshot is published per **build key**: the normalized identity of the build subgraph of a plan (its build-node digests plus options that change semantics, such as whether LLM calls were allowed). Several plans over one corpus share a build key when their build subgraphs match. Snapshots share unchanged resources: the registry counts each snapshot as one owner of each resource it maps.

### 8.2 Transitions

```text
          refresh (job J)                     query Q
                │                                │
                │                       acquire(current) → A (leases 1)
   build B in private scope                      │
   validate B                                    │ awaits LLM …
   publish B: current := B, A retired            │
                │                                │
   (second refresh) publish C, B retired         │
     B leases 0 → release B's resources          │
     (shared ones remain owned by A or C)        │
                                                 │ LLM resolves
                                    materialize result from A
                                    release lease → A leases 0, retired
                                    → registry.release(A's resources)
                                    → resources with 0 owners close
```

Rules:

- `acquire` reads the current snapshot and increments its lease count in one synchronous step on the Goja owner goroutine. Publication also runs on the owner goroutine, so it cannot interleave between those two actions. Go code that touches the registry from other goroutines (shutdown) uses the registry's mutex.
- A failed or cancelled build never replaces the current snapshot. Its build scope releases everything it adopted.
- A retired snapshot releases its resources when its last lease ends. A current snapshot is never released.
- Shutdown: reject new operations; abort active ones; wait for them to settle; release all snapshots; close the registry. If waiting times out, report the timeout and do not close resources that worker goroutines may still read.

### 8.3 Query flow

```ts
// Sketch: pkg/ragjs/src/engine/query.ts
async function query(plan: CompiledPlan, signals: Signals, req: RequestScope): Promise<RunResult> {
  if (!snapshots.has(plan.buildKey)) await builds.join(plan, req); // first use only
  return snapshots.withCurrent(plan.buildKey, async (snap) => {
    const exec = await executor.run(plan, signals, snap, req);
    return materialize(exec, snap, plan.limits);  // copies required records
  });                                             // lease released in finally
}
```

`builds.join` is awaited only when no snapshot exists for the build key. Whether a query should ever start a rebuild when one exists is decision D1: the recommendation is no — a served query uses the current published snapshot, and only the refresh operation (watcher, manual, post-sync) detects changes and publishes new snapshots. `ragwb serve --watch 0` already documents "snapshot at open, restart to pick up edits", which matches.

### 8.4 One path for opening resources

Proposed Go registry, one per runtime:

```go
// Proposed: pkg/natives/resources.go
type ResourceID string // artifact store key + codec, or a build-unique id for unsaved outputs

type Registry struct { mu sync.Mutex; entries map[ResourceID]*entry; closed bool }

// Adopt takes ownership of c for owner. If id exists, c is closed and the existing
// entry gains owner. After Close, Adopt closes c and returns ErrClosed.
func (r *Registry) Adopt(id ResourceID, owner OwnerID, c io.Closer) (Handle, error)
func (r *Registry) Retain(id ResourceID, owner OwnerID) error
func (r *Registry) Release(id ResourceID, owner OwnerID) error // closes at zero owners
func (r *Registry) Close(ctx context.Context) error            // shutdown only
```

Every opener follows one pattern:

```go
rec, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := reg.Adopt(id, scope, rec)
if err != nil { _ = rec.Close(); return nil, err }
return newStoredDataset(vm, h), nil
```

Persistent loads, delta outputs (`ParseDelta`, `ChunkDelta`) and fresh native builds (`bm25_native.go`) all use it. `Persist`, `Markdown` and the BM25 natives require a registry at install time; a nil registry is a construction error, replacing the no-op `addCloser` fallback. The temp-directory list in `Persist.InstallEnv` becomes a build-scope resource as well.

Duplicate IDs are reused (the content is identical by construction of store keys) and the redundant handle is closed. Two registry entries for one underlying handle are impossible because only `Adopt` creates entries.

The browser/memory host implements the same TypeScript `ResourceHost` interface (`retain`/`release`) with no-op closing, so the snapshot code is identical in both hosts.

### 8.5 Detached results

A result returned by `rag.run` must be usable after the lease is released, without a close call. `materialize` therefore copies into the result:

- records for every emitted hit, evidence entry and trace `top` entry;
- configured neighbor previews (today `NB_HITS = 40` in `serve.go`);
- the snapshot ID and generation.

```js
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;   // hit or ref, never a bare id
// no cleanup: result owns plain data
```

`result.chunk`, `result.top` and `result.explain` read only the copied records. A bare string ID is rejected with an error pointing to `rag.ref(dataset, id)`; it cannot silently choose a dataset. Materialization copies the bounded result, not the corpus. If a requested trace or result exceeds a declared byte/record limit, the query fails with a `result_too_large` error rather than returning hits whose records are missing (limit values: decision D6).

Evaluations (`evaluation/evals.js`) and corpus-wide exports iterate whole datasets. They use an internal scoped read, `snapshots.withCurrent(key, snap => …)`, rather than a result.

### 8.6 Generation-aware lookups

The served shell asks `/chunk?id=` and `/doc?id=` when its cache misses. The request becomes `?snapshot=<id>&dataset=<id>&id=<id>`. The server resolves against that snapshot if it is still held (current, or retired with leases), otherwise returns `410 stale_generation`. It never reinterprets an old reference against the newest dataset. The shell keys its caches by snapshot and reference and clears them when a run response reports a new snapshot. Already-delivered text cannot be revoked; this is consistency, not access control.

Keeping retired snapshots resolvable for later lookups requires retention; whether the server keeps the last N retired snapshots alive is decision D7 (recommendation: keep none beyond active leases; the shell refetches by rerunning the query on `410`).

## 9. Durable store ownership and collection

### 9.1 Authority

Keep the existing payload layout, codecs and `Has` validation. Add:

- **Descriptor dependencies.** `Meta` gains `deps []string`: store keys this artifact needs to be readable (e.g. a records2 directory produced copy-on-write from a base links shard files, but each directory is self-contained by hard links, so deps are usually empty; an index's deps are none because it is independently openable). The field exists so the collector never guesses.
- **Snapshot manifests.** `<ns>/snapshots/<id>.json`: build key, created time, and the store keys it maps.
- **Current pointers.** `<ns>/current/<buildKey>.json`: the current snapshot ID, written atomically.

Descriptors, manifests and pointers are the inventory. Lineage files become a derived accelerator for finding a delta base; deleting them loses speed, not ownership.

### 9.2 One writable owner per namespace

On open, a process takes an OS-backed exclusive lock on `<ns>/.lock` (released by the OS if the process dies) and keeps it for its lifetime. The owner performs builds, publication, leases and online GC. A second process trying to open the same namespace for writing fails with a clear "namespace is in use by pid …" error. `ragwb store gc` takes the same lock, so it only runs while no server owns the namespace.

The browser memory store implements the same logical contract without a lock or persistence. Multiple processes sharing a store and cross-machine collection are out of scope.

Consequence: today a user can run `ragwb run` scripts against the store a server is using. After this change that fails until the server stops or the script uses another store. Whether to add a read-only open mode for such scripts is decision D8 (recommendation: not now).

### 9.3 Publication order

```text
1. Builder writes payloads under an owned staging dir (<ns>/.tmp-*).
2. Close writers; validate counts and codec metadata.
3. Rename payloads into place; fsync payload files and parent directory.
4. Write descriptors atomically (temp + fsync + rename + fsync dir).
5. Write the snapshot manifest the same way.
6. Atomically replace current/<buildKey>.json.
7. Update lineage files (derived).
```

A crash before step 6 leaves the old snapshot current; orphaned payloads and descriptors are collected later. A crash after 6 may leave lineage stale, which only costs a full build. `writeAtomic` currently does not fsync; adding file and directory syncs is part of this phase. Rename atomicity alone does not prove power-loss durability; test the sequence on the supported platforms (Linux and macOS; decision D9 for others).

### 9.4 Mark and sweep

Run by the owner, serialized with publication on the owner goroutine:

1. Mark: store keys of current snapshots; of the newest `R` retained manifests per build key (decision D7); of snapshots with active leases; of active build scopes. Follow `deps` transitively.
2. Sweep: every descriptor in the namespace (walk of `<ns>/<shard>/`) not marked is deleted with its payload.
3. Temps: with the lock held at startup, every `.tmp-*` is an orphan and is deleted. During operation, only temps not owned by an active build scope are deleted. This replaces the one-hour age heuristic.
4. Missing or unparsable descriptors or manifests abort the sweep with an actionable error. Nothing is deleted on a guess.

Hard-linked files: deleting one artifact's directory removes only its links (`Store.Delete` already relies on this). Report logical bytes and actually freed bytes (`unlinkedSize`) separately.

Retention of old snapshots has a privacy consequence: a note removed from the vault remains in retained payloads until they are swept. Document it with the retention setting.

### 9.5 Store listing

`List` walks the namespace layout explicitly — namespaces, two-character shard directories, `<key>.json` files — instead of skipping directories with a dot in their name. This fixes finding 3 as a consequence; keep a regression with a root like `x.store`.

## 10. Operations: native async calls and shared builds

### 10.1 One native operation bridge

Extract the shared lifecycle of sample, decide and rerank:

```go
// Proposed: pkg/ragjs/asyncop (or pkg/runtimeop); sketch, not final API.
type Kind int
const ( Succeeded Kind = iota; CallerCancelled; DeadlineExceeded; ProviderFailed; RuntimeClosed )

type Outcome struct {
    Kind    Kind
    Payload []byte        // immutable; JSON for success
    Failure *llm.Failure  // code/message for failures
}

type Spec struct {
    Name     string
    Work     func(ctx context.Context, emit func(Event), owner OwnerCall) ([]byte, error)
    Classify func(payload []byte, err error, cause error) Outcome
}

// Start returns a JS promise. It installs the abort listener before starting work.
func Start(vm *goja.Runtime, signal goja.Value, spec Spec) goja.Value
```

Behavior that must hold:

- An atomic state decides the race between the provider returning and the caller aborting. States: `running → finished` (worker returned first) or `running → cancelled` (abort listener fired first). The first transition wins; the outcome is computed from it **on the worker goroutine, before any deferred cleanup**, and posted once.
- `cause` comes from `context.Cause(ctx)`; the abort listener cancels with a `callerCancelled` cause, runtime lifetime cancellation has its own cause, deadlines have theirs. This distinguishes caller cancellation from shutdown without reading `ctx.Err()` after cleanup.
- Settlement on the owner resolves or rejects the promise and removes the abort listener.
- If the runtime has closed and the post fails, the promise never settles (no JS can observe it), but the worker still releases its resources.
- Streaming text (`onText`) and rerank batches are emitted as immutable Go values through `emit`, which posts to the owner.
- Tool calls during sampling need a request/reply onto the owner (today `exec` in `sample.go`). `OwnerCall` is that hook: post a function, wait for its reply or for `ctx.Done()`. Workers never touch Goja values.

Provider-specific request decoding, JSON validation, tool semantics and error mapping (`classifyDecideError`, `llm.Failure`) stay in their packages as `Work` and `Classify`.

### 10.2 Shared build jobs

Builds run in JavaScript on the owner goroutine, so the job table is TypeScript:

```ts
// Proposed: pkg/ragjs/src/engine/jobs.ts
interface Waiter { signal: CancellationSignal; onEvent?: EventSink }

class BuildJobs {
  join(plan: CompiledPlan, waiter: Waiter, opts: BuildOptions): Promise<BuildReport>;
  refresh(plan: CompiledPlan, opts: BuildOptions): Promise<BuildReport>; // background owner
}
```

- Key: build key plus semantic options (`calls`, `full`, `force`, `persist`). Not `signal` or `onEvent`.
- Each job has its own scope (`newScope` in `engine/scope.ts`), linked to engine shutdown. The job's signal, not any waiter's, is passed to `Engine.build`.
- A waiter's abort detaches that waiter; its promise rejects with `cancelled`. When the last waiter detaches and the job has no background owner (refresh), the job is aborted.
- A waiter arriving while a job is cancelling waits for it to settle and then starts a new job; it never joins a doomed one.
- Progress events go to every attached waiter, tagged with `job` id; each waiter's sink is isolated (`safeEmit`). A failing or detached sink affects only its waiter.
- `__serve.refresh` uses `BuildJobs.refresh` rather than calling `eng.build` directly, so a refresh and a first query for the same key share one job.

On success the job publishes a snapshot (section 8). On failure or abort it releases its build scope; the current snapshot is unchanged.

## 11. HTTP execution and bounded SSE

### 11.1 One responder

Both `/run` and `/verbs/...` call one helper after parsing their parameters:

```go
// Proposed: pkg/server/respond.go
type Operation func(ctx context.Context, sink runner.EventSink) (json.RawMessage, error)

func (s *RagServer) respond(w http.ResponseWriter, r *http.Request, a *RagApp,
    start json.RawMessage, op Operation)
```

`respond` acquires the app's in-flight slot (now for both endpoints), chooses JSON or SSE, runs the operation and writes exactly one terminal response, then releases the slot after the operation returned **and** the writer exited. Error codes are shared: `busy`, `bad_request`, `run_failed`/`verb_failed`, `slow_client`, `cancelled`.

### 11.2 The SSE writer

- One goroutine — the writer — is the only code that writes or flushes the response, including headers, heartbeats and the terminal event. The handler hands the terminal event to the writer over a channel.
- Before each write or flush the writer calls `http.ResponseController(w).SetWriteDeadline(time.Now() + writeTimeout)` and checks the error of `Flush()`. If the controller reports `http.ErrNotSupported`, streaming is refused with a setup error before any event is sent. Middleware response wrappers must implement `Unwrap()` so the controller reaches the real connection.
- On any write or flush error (including an exceeded deadline, which cannot be extended afterward): stop writing, cancel the request context, and exit. The operation observes cancellation through its scope; no terminal event is promised to a failed connection.
- `streamSink.Emit` (on the Goja owner) never blocks: a non-blocking send; on overflow it marks the stream `slow_client`, cancels the request and drops further events. This removes the 100 ms owner stall per event.
- Coalesced text and queued events are bounded by bytes, not only by queue length.
- Per-write deadlines bound each write while allowing a long, healthy stream.

In the running example, the closed laptop stops reading; the next write exceeds its deadline; the writer cancels the request; `abortScope` aborts the query's scope; the LLM call is cancelled through the operation bridge; the query's lease is released; `respond` releases the slot. Capacity returns after one write timeout plus cancellation latency.

The slot is held until the operation returns because the runtime is genuinely still busy until then. An operation that ignores cancellation keeps its slot; that is a bug in the operation, tested separately.

## 12. Graph references and transformations

### 12.1 Canonical references

The DSL keeps its concise forms. The compiler normalizes them once into a canonical IR:

```js
// DSL
when: { decision: 'route.needs', min: 0.5 }
retrieve('text', { k: 30 })

// Canonical IR (sketch)
when: { source: { kind: 'decision', node: 'route', question: 'needs' }, min: 0.5 }
p:    { index: { kind: 'artifact', node: 'ix-text' }, k: 30 }
```

Component definitions declare which parameters hold references (`refs: { index: 'artifact', indexes: 'artifact[]' }`) instead of computing `uses` from names. One module provides:

```ts
// Proposed: pkg/ragjs/src/graph/refs.ts
type GraphRef =
  | { kind: 'data'; from: NodeId; to: NodeId; port: number }
  | { kind: 'artifact'; from: NodeId; to: NodeId; param: string }
  | { kind: 'decision'; from: NodeId; to: NodeId; question: string }
  | { kind: 'signal'; name: string; to: NodeId }
  | { kind: 'output'; name: string; to: NodeId };

function references(ir: RawGraph): GraphRef[];
function rewriteReferences(ir: RawGraph, rename: Map<NodeId, NodeId>): RawGraph;
```

`finalize` uses `references` for existence, port types, phases, edges and cycle detection. `rename` checks that the target is unused, rewrites with `rewriteReferences`, and validates once after the op list. `remove` consults `references` for every dependent: data dependents are rewired as today, artifact and decision dependents cause rejection unless the op states `cascade: true` or a replacement (decision D10; recommendation: reject by default).

### 12.2 Lambdas

Arbitrary JavaScript cannot be renamed by string replacement. A lambda that reads graph values declares its bindings:

```js
when: { uses: { route: 'route' }, fn: (s, { decision }) => decision('route').needs.score > 0.5 }
```

The callback uses the local alias `route`; rename rewrites the binding's target, not the source text. A lambda that calls `ctx.decision('x')` without a declared binding is a compile error. This removes the regex in `whenRefs`.

### 12.3 Export as an explicit projection

Components declare capabilities: `local-index`, `external-model`, `host-filesystem`, `native-only`. The lexical HTML export projects the graph onto a browser-capable set:

1. Remove nodes whose capabilities are unsupported (classifiers, rerankers, answer, LLM enrichment).
2. For each dependent of a removed decision, apply the documented rule: the `when` condition is removed, so retrieval becomes unconditional.
3. For any other dependent whose required reference was removed, refuse to export with a message naming both nodes.
4. Validate the final graph before serialization.

The page currently embeds `engine.files()`, which is empty when sources come from `source.dir` on the server. The export should embed a normalized portable dataset taken from the snapshot (decision D11: documents dataset vs raw source files).

## 13. Module layout and what is deleted

Split modules as contracts land; keep the `rag` facade in `api/index.js`. Browser and Goja load the same generated bundle.

| Concern | Today | Proposed |
|---|---|---|
| Datasets | `engine/records.js` (`Lazy`, `unionChunks`, `isView`, `asArray`) | `engine/dataset.ts` |
| Snapshots, leases | implicit in `Engine.artifacts`, `Engine.gc` | `engine/snapshots.ts` |
| Build jobs | `Engine.buildShared` | `engine/jobs.ts` |
| Query, results | `engine/session.js`, `decorateResult` | `engine/session.ts`, `engine/result.ts` |
| Graph references | spread across `graph.js` | `graph/refs.ts`, used by `graph.js` |
| Export | `export/html.js` heuristics | capability projection in `export/html.js` |
| Native resources | `addCloser` in each native | `natives/resources.go` |
| Async bridges | `sample.go`, `decide.go`, `rerank.go` | one bridge + three specs |
| HTTP | two orchestrations + `sse.go` | `server/respond.go` + bounded `sse.go` |

Deleted as replacements land: `unionChunks`, `RecordView`, `isView`/`asArray`, consumer-side record-view tests, primary-stream order, `Engine.retired` and one-cycle GC, direct `__records.close`/`__ix.close` calls, the no-op `addCloser` fallback, first-waiter `buildShared`, duplicated HTTP pumps, lineage-driven `Store.GC`. Keep memory algorithms used by the browser and parity tests. Remove an obsolete codec (e.g. the older `records` chunk codec) only after confirming no live writer produces it.

Simplification rule for review: each new concept must retire at least one existing responsibility, and no component-specific branch may be added to the executor when a component contract can express it.

## 14. Decisions

Accepted (from the original design and the user's direction):

- **Compatibility may break.** Callers, fixtures and stored formats change together. Serialized IR version and artifact namespace are bumped. Old stores need an explicit cleanup command when no process owns them; directories are never deleted silently.
- **Single writable owner per namespace** (9.2), not a multi-process collector.
- **Synchronous datasets** (7.1).
- **Detached results** instead of closeable results (8.5).
- **New contracts in strict TypeScript** with discriminated unions for outcomes and references (RAGWB-010 foundation).

Proposed, needing confirmation:

| ID | Question | Recommendation | Blocks |
|---|---|---|---|
| D1 | Can a query start a rebuild when a snapshot exists? | No; refresh owns change detection | Phase 3 |
| D2 | Snapshot granularity | Per build key; resources shared via registry | Phase 3 |
| D3 | Fusion across datasets with equal IDs | Keep both unless `canonical` is given | Phase 2 |
| D4 | Document identity across sources | source id + vault path | Phase 2 (fixtures) |
| D5 | Registry duplicate IDs | Reuse, close redundant | Phase 3 |
| D6 | Result size limits and error | `result_too_large`, limits per plan | Phase 3 |
| D7 | Retained snapshots (memory and disk) | 0 in memory beyond leases; small N on disk | Phases 3–4 |
| D8 | Read-only store access for other processes | Not now | Phase 4 |
| D9 | Durability platforms | Linux, macOS | Phase 4 |
| D10 | Remove with decision/artifact dependents | Reject by default; `cascade` explicit | Phase 7 |
| D11 | Export payload | Normalized documents dataset | Phase 7 |
| D12 | Partial builds with deferred LLM items (`engine.partial`) | Publish as `partial` snapshot, never persisted | Phase 3 |

D12 is not covered by the original design. `Engine.build` keeps outputs computed without LLM calls in `this.partial`, and `artifacts` accepts them. The snapshot model must say whether such an output is publishable; the recommendation is yes, flagged in the snapshot, so the UI can show pending items.

## 15. Implementation plan

Each phase is a vertical change: tests, migration of consumers and deletion of the replaced code. Do not extract empty abstractions first. The PR stack (#2–#7) is not merged; choose one integration branch for this refactor rather than fixing divergent copies.

1. **Characterize.** Add deterministic regressions for all twelve findings and record which fail at baseline. Cover cold, stored and delta paths, and an equal-count changed input for bakes. If the stack is used before migration finishes, land the P1 SSE fix early, but in the shape of the final writer (section 11.2), not a temporary path.
2. **Datasets and references.** `dataset.ts`, memory and stored implementations, conformance suite run in Node and Goja. Indexes carry `dataset`. Hits carry `ref`. Migrate `components/builtins.js` (`ctx.chunk(h.id)` call sites), `components/hits.js`, `engine/events.js`, `evaluation/evals.js`, the shell and export. Remove `unionChunks`, views and `asArray`. Bake keys from dataset IDs.
3. **Snapshots and resources.** Go registry; all openers adopt; snapshot table with leases; `materialize`; `__serve.run` packages inside the lease; generation-aware `chunk`/`doc`; delete `Engine.gc`/`retired` and `addCloser` fallbacks.
4. **Store authority.** Namespace lock, descriptors with deps, manifests, current pointers, fsync, mark/sweep, explicit `List`. Lineage becomes derived.
5. **Operations.** Async bridge for sample/decide/rerank; `BuildJobs`; refresh via jobs. Fix the Pi fallback (`preferred`) as a separate small change: when the enabled list is empty and the default is unavailable, choose the first eligible model sorted by reference.
6. **Transport.** `respond.go`, bounded SSE writer, non-blocking sink, admission for `/run`.
7. **Graph.** `refs.ts`, canonical IR, rename/remove, lambda bindings, capability export.
8. **Cleanup and measurement.** Remove dead code and codecs, update examples, help and API docs, run the vault app and reports workflows, record measurements.

Start here: run `pnpm install --frozen-lockfile && pnpm build && pnpm check`, then `go test ./pkg/ragjs ./pkg/runner ./pkg/server ./web`. Read `Session.run`, then `serveHarness` in `serve.go`, then `Engine.artifacts`.

## 16. Tests and acceptance

| Contract | Sequence and assertion |
|---|---|
| Dataset conformance | Same assertions over memory, stored and delta datasets |
| Provenance | Two datasets sharing IDs resolve their own bytes through retrieve, fuse, pack, events and shell |
| Bake inputs | Cold/warm receive a dataset; equal-count changed input invalidates |
| Snapshot leases | Pause a query in a fake provider; publish two snapshots; resume; packaged records equal the original snapshot's |
| Resource lifecycle | Fresh/load/delta adopt; counting closers show exactly one close per handle on error and on shutdown |
| Result ownership | Result readable after its snapshot's resources closed |
| Durable inventory | 40+ versions, retain 3; sweep leaves exactly the reachable set |
| Store lock | Second writer and `store gc` refused while a server holds the lock |
| Crash recovery | Inject failure after each publication step; reopen yields complete old or new snapshot |
| Shared jobs | Cancel A while B waits → B succeeds; cancel both → job aborted, scope released; late waiter starts a new job |
| Async outcome | Provider failure stays failure; caller abort stays cancellation; runtime close releases worker |
| SSE bounds | Real TCP client that stops reading: stream ends within write timeout; slot returns; flush errors handled |
| Graph edits | Rename/remove keep references or reject; no dangling decisions |
| Export | A classified plan exports a working unconditional lexical page |
| Local fixes | Dotted store root lists outputs; empty enabled list picks a deterministic eligible model |

Use synthetic corpora, provider fakes and hold/release barriers instead of sleeps. Use counting closers and, where practical, file-descriptor counts. The SSE test must use a real socket.

Measure cold build, warm start, query latency, changed-note refresh, memory, open file descriptors and store growth on one fixture before and after. Report conditions. Materialization and references add allocations; a regression is investigated, not a reason to drop the contract.

Done means: each finding has a passing regression or a reviewed disposition; the deleted mechanisms in section 13 are gone; one example works in browser and Goja hosts; docs match the public API; shutdown, cancellation and publication sequences are tested.

## 17. Risks and scope

- **Result lifetime.** Releasing a lease before copying records reproduces use-after-close.
- **Shared resources.** Retiring a snapshot must release, not close, its resources.
- **Cross-process GC.** Without the lock, in-memory leases protect nothing.
- **Reference migration** is the largest API change: every trace, evidence, GUI and evaluation consumer moves from IDs to refs.
- **Lifecycle reuse** applies only where semantics match: sampling tools are a request/reply protocol, not a one-shot call.

Out of scope: distributed indexing, remote datasets, multi-process stores, full Obsidian rendering, authentication. Reindex status and manual refresh can later use the refresh operation without a new implementation.
