# RAGWB-009: consistent datasets, snapshots, operations and graph contracts

An intern's analysis, design and implementation guide.

## 1. What this guide is for

rag-workbench lets an author describe a retrieval pipeline in a short JavaScript program, build it over a folder of Markdown notes, and query it from a CLI, a served web app or a standalone browser page. RAGWB-009 is a refactor of the engine underneath those programs. It adds no user features. It replaces boundaries that currently make correct behavior depend on storage, timing or call order, and it resolves twelve review findings that are symptoms of those boundaries.

The design is **proposed, not implemented**. "Current behavior" sections describe the source tree at runtime baseline `c267f91`, established by reading code; no test was run to produce this guide. "Proposed" sections describe target contracts. Unsettled policies are listed in section 14 with a recommendation and the phase they block. Compatibility is explicitly not required: callers, tests, fixtures and stored formats change together, with no adapters, dual paths or legacy readers.

The authored runtime lives in `pkg/ragjs/src/`. `pnpm build` generates the committed `pkg/ragjs/assets/rag.js`, which both the Go host (Goja) and browsers load; never edit the generated file. `pkg/ragjs/src/README.md` describes the modules and checks. TypeScript and Biome (from RAGWB-010) check types and lint; they do not prove that runtime inputs are valid or that resources are released, so the lifecycle tests in section 16 remain necessary.

## 2. The system in one example

### 2.1 Corpus, plan, build and query

A **corpus** describes the work done when documents change. A **plan** adds the work done for each query. Both are immutable builder values that compile to a graph called the **IR**: nodes with a component type, parameters, inputs and an output kind.

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

Every node has a **phase**. *Build* nodes (source, parse, chunk, enrich, index) depend only on documents and are cached. *Live* nodes (retrieve, fuse, filter, rerank, pack, answer, classify) run per query and read **signals** such as `q`. The compiler, `finalize` in `pkg/ragjs/src/graph/graph.js`, type-checks ports, records edges and orders nodes topologically.

Three hosts run the same `rag.js`:

| Host | Entry points | Record storage |
|---|---|---|
| CLI scripts and verbs | `pkg/runner/runtime.go`, `pkg/runner/script.go` | Go natives, optional artifact store |
| Served app (`ragwb serve`) | `pkg/runner/serve.go` (`serveHarness`), `pkg/server/rag.go`, `pkg/server/verbs.go` | Go natives, artifact store |
| Browser page and HTML export | `web/app-shell/shell.js`, `pkg/ragjs/src/export/html.js` | JavaScript memory |

Go natives (`pkg/natives/`) replace some components with faster implementations: Markdown parsing and chunking, BM25 and Bleve indexes, and disk-backed record collections. Backends may use different algorithms and produce different scores. After this refactor they must not differ in collection, identity or ownership semantics.

### 2.2 The running example

The guide follows one served app, `reports`, which has two chunk streams:

```text
source.dir ─▶ parse.markdown ─▶ chunk.sections ─┬─▶ index 'text'  (bm25 over raw chunks)
                                                 └─▶ enrich ─▶ index 'ctx' (bm25 over enriched chunks)

live:  q ─▶ retrieve('text') ─┐
       q ─▶ retrieve('ctx')  ─┴─▶ fuse ─▶ pack ─▶ answer (LLM, streamed)
```

`enrich` prefixes each chunk with its document title and heading path. Its records have **the same chunk IDs** as those of `chunk.sections` but different text. The app runs with `ragwb serve --watch 30s`, so a watcher calls `refresh` periodically and rebuilds incrementally when a note changes. A user asks a question and the answer streams over Server-Sent Events (SSE) for twenty seconds. Sections 3 and 4 follow this request through current code; sections 7–11 follow it through the proposed design.

## 3. How the current engine works

### 3.1 Build: digests, memo, stored outputs and deltas

`Engine.build` (`pkg/ragjs/src/engine/engine.js`) visits build nodes in order. For each node it computes a **digest**: a hash of component type, parameters, input digests, file contents for file sources and, for components registered with `rag.define`, their source text (`Engine.digests`). Then:

1. If `this.memo` has the digest, the in-memory value is reused.
2. Otherwise, if an artifact store is configured (`env.artifacts`) and holds the node's **store key** (`storeKey` in `engine/records.js`), a `Lazy` memo entry is inserted that loads the payload on first read.
3. Otherwise the engine tries a **delta build** (`tryDelta`). It looks up the node's **lineage** — a key for "this node of this pipeline" that does not change when file contents change (`Engine.lineages`) — finds a stored previous version whose inputs match, and lets the component apply only the changes.
4. Otherwise the node is computed fully with `def.map` (per item) or `def.all` (whole input).

Each finished node is stored in `this.memo` (keyed by digest and global to the engine), saved through `store.save`, and appended to its lineage through `store.lineageRecord`. Saving happens per node as it completes, not at the end of the build.

Build nodes that would call an LLM can be built with calls deferred. Such outputs are kept in `this.partial` instead of `memo`, are not persisted, and are accepted by `artifacts` as a fallback.

### 3.2 "Artifacts": the implicit current generation

There is no snapshot object. `Engine.artifacts(ir)` recomputes digests for the current files and returns `null` if any digest is missing from `memo`/`partial`. Otherwise it builds, and caches under a key derived from the digests, this object:

```js
art = { key, byId, chunks, order, indexes, synonyms, docs, links }
```

Two implicit rules live here.

**Global chunk union.** All chunk-producing nodes are merged into one `art.chunks`. In-memory arrays are written into a `Map`, so a later node overwrites an earlier one. Stored outputs are record **views** that stay on disk, combined by `unionChunks` (`engine/records.js`). `unionChunks.get` consults the `Map` first and then the views in graph order, taking the first view that has the ID. For a chunk ID present in both streams of the running example, the record a query sees is:

| `chunk.sections` | `enrich` | Record returned for a shared ID |
|---|---|---|
| computed (array) | computed (array) | enriched (later array wins) |
| computed (array) | loaded (view) | raw (array beats view) |
| loaded (view) | computed (array) | enriched (array beats view) |
| loaded (view) | loaded (view) | raw (first view wins) |

Restarting the server, which turns computed outputs into loaded ones, can therefore change the text a query cites.

**Primary reading order.** Neighbor navigation (`art.order`) comes from the first chunk node that is not an enrich node.

### 3.3 Arrays versus record views

A stored chunk or document output is loaded as a `RecordView` (`recordsViewJS` in `pkg/natives/persist.go`): an object with `get/has/size/order/values/toArray` and a hidden `__records` Go handle. Engine code tells it apart from an array with `isView` and converts with `asArray`. Build components receive arrays through `asArray`. `resolveBakes` (bake callbacks, `engine.js`) does not convert, so a bake receives an array on a cold build and a view on a warm one. Its cache key hashes only document IDs with section counts and the chunk count, so an edited note with an unchanged section count reuses a stale bake value. From source, a further consequence appears likely but was not run: the key computation calls `docs.map`, which a stored documents view does not have, so a bake over stored documents would throw.

### 3.4 Query and result

`Session.run` (`engine/session.js`) obtains `art`, awaiting `engine.buildShared` first if `art` is `null`. It runs live nodes, giving each a `ctx` whose `chunk`, `docChunks`, `neighbors` and `index` closures read `art`. A hit is `{ id, score, ... }` with a bare chunk ID. Live outputs are memoized in `Session.memo`, which is cleared when `art.key` changes. `decorateResult` attaches `chunk`, `top`, `explain` and `toJSON` to the result; they close over `art`, so a returned result keeps reading native handles for as long as the caller holds it.

### 3.5 Serving, refresh and handle cleanup

`serveHarness` (`pkg/runner/serve.go`) builds once, then exposes `__serve.run`, `refresh`, `chunk` and `doc`:

- `run` awaits `session.run`, calls `collect()` (which may run `Engine.gc`), then calls `art()` **again** and packs chunk bodies and neighbors from whatever that returns.
- `refresh` calls `eng.build(ir)` directly — not `buildShared` — when `eng.artifacts(ir)` is `null`, then calls `collect()`.
- `chunk` and `doc` read `art()` at request time by bare ID.

`Engine.gc` drops memo entries whose digests are no longer live and puts their values on `this.retired`. The *next* `gc` closes those values' Go handles (`__records.close()` or `__ix.close()`). The intent was to give an in-flight query one rebuild of grace.

### 3.6 Native handles

`Persist.InstallEnv` (`pkg/natives/persist.go`) opens stored payloads in place (`OpenChunkRecords`, `OpenRecords2`, `OpenBleveBm25`) and registers each `Close` with the runtime's closer hook `env.AddCloser`. If the host supplies no hook, `addCloser` silently becomes a no-op (`persist.go`, and the same pattern in `bm25_native.go`). Delta builds create record readers by another path: `parseDelta`/`chunkDelta` (`pkg/natives/markdown_native.go`) call `ParseDelta`/`ChunkDelta`, which open a `Records2` in a temp directory (`pkg/natives/delta.go`) that is wrapped with `ctx.store.view` and never registered with `AddCloser`. `Records2` opens shard files lazily (`Records2.shard`), so a reader may need files on disk long after it was opened.

### 3.7 The store and its collector

`pkg/artifacts/store.go` lays out a namespace as `<dir>/<ns>/<key[:2]>/<key>.json` (metadata, `Meta`) with a payload `<key>.bin` or `<key>.<codec>`. `Has` validates that the payload exists with the recorded size. Writes use temp file plus rename (`writeAtomic`) without `fsync`. `pkg/artifacts/lineage.go` keeps per-lineage history files of at most `LineageKeep = 32` entries; delta builds search the newest `LineageSearch = 8`. `Store.GC(keep)` keeps the newest `keep` versions of each lineage, deletes the other *listed* versions, then deletes `.tmp-*` directories older than an hour. `artifacts.Prune` deletes whole other namespaces. Nothing locks a namespace, so a separate `ragwb store gc` process cannot know which outputs a server is reading.

### 3.8 Native asynchronous calls

LLM sampling (`newSample`, `pkg/ragjs/sample.go`), decisions (`newDecide`, `pkg/ragjs/decide.go`) and reranking (`rerankAsync`, `pkg/natives/rerank.go`) repeat one structure: derive a context from the runtime lifetime, add an abort listener to the JS signal, run the provider on a goroutine, then post the settlement to the Goja owner goroutine with `PostWithLifetimeContext`. Sample and decide classify the outcome on the goroutine before its deferred `cancel()` runs. Rerank classifies inside the posted callback, after the deferred `cancel()` has already run, so `callCtx.Err()` is always set and every provider failure is reported as `cancelled`. None of the three removes its abort listener. `RerankBatched` coordinates batch goroutines with a `sync.WaitGroup` and a manual first-error variable.

### 3.9 Shared builds

`Engine.buildShared` keys in-flight builds by their joined digests. A second caller receives the first caller's promise. That build was started with the first caller's `signal` and `onEvent`, so cancelling the first request cancels the build for everyone, and progress reaches only the first request.

### 3.10 HTTP and SSE

`handleRun` (`pkg/server/rag.go`, direct queries) and `handleVerb` (`pkg/server/verbs.go`, verbs) each create an `sseWriter`, a `streamSink` and a pump goroutine, run the operation, close the sink, wait for the pump and write a terminal event. `streamSink.Emit` runs on the Goja owner goroutine and waits up to 100 ms for queue space before cancelling the request. `sseWriter.write`, `ping` and `pump` ignore write and flush errors, and no write deadline is set. `handleVerb` holds one of the app's in-flight slots (`acquire`/`release`, default 8) for its whole duration; `handleRun` takes no slot.

### 3.11 Graph references

`finalize` discovers four kinds of reference by separate code: data inputs (`n.in`); artifact uses (`def.uses(p)`, e.g. `retrieve` with `p.index = 'text'` uses node `ix-text` by naming convention); decision references in `when` conditions (`whenRefs`: the string `'route.needs'`, or a regex over a lambda's source for `.decision('route')`); and signal reads. Graph edits (`applyOps`/`applyOp`) handle each kind separately. `rename` rewrites only `in` and `outputs`. `remove` checks `uses` dependents but not `when` dependents. The HTML exporter (`export/html.js`) removes every submit-triggered live node except answers, then removes answers and contexts, and never rewrites dependents' `when` conditions.

## 4. Following the example through current code

The user asks a question. `retrieve`, `fuse` and `pack` finish, and `answer` streams tokens for twenty seconds.

1. While `answer` awaits the LLM, the watcher's refresh sees an edited note. `refresh` builds new chunk and index versions incrementally; `collect()` runs `gc`, which moves the old versions to `retired`.
2. The note is saved again and the next watcher tick rebuilds. This `gc` closes everything on `retired` — including the record views and Bleve indexes the first query reaches through `art`.
3. When `answer` resolves, `__serve.run` calls `art()` and packs chunk bodies from the **newest** generation. If a cited chunk changed or disappeared, the client receives text that differs from the evidence the model saw. If the query touches a closed reader (`ctx.neighbors`, `result.chunk`), the read fails with `records2 … closed`.
4. Meanwhile the user's browser stopped reading (the laptop lid closed). The TCP send buffer fills; `fmt.Fprintf` in the pump blocks with no deadline; the handler waits for the pump indefinitely. For `/run` this leaks the handler goroutine and keeps the query's runtime work alive. For a verb it also keeps the in-flight slot; after eight such clients the app's verbs answer `503 busy`.
5. Separately, `fuse` combined hits from `text` and `ctx` that share chunk IDs, and every hit was resolved through the union of section 3.2. Which text each citation shows depends on what was loaded from disk.

No step is an isolated coding slip. Each follows from a missing contract: records have no dataset identity, a query has no fixed generation, handles have no owner, and transports have no bounded write.

## 5. Review findings and their causes

The six stacked PRs (#2–#7) carry twelve unresolved review findings. Priorities are the reviewers' labels. For each finding, this guide confirmed the mechanism by reading source; none was reproduced by running a test, so phase 1 of the implementation must add a failing regression for each.

| # | PR / prio | Finding | Mechanism (section) | Cause group |
|---|---|---|---|---|
| 1 | #2 P2 | Stored duplicate chunks select a different version than cold arrays | union precedence (3.2) | Datasets |
| 2 | #2 P2 | Bake receives a record view instead of an array | `resolveBakes` (3.3) | Datasets |
| 3 | #2 P2 | Store listing skips a root whose basename contains a dot | `List` skips every dotted directory, including the walk root (`store.go`) | Local fix |
| 4 | #3 P2 | Two refreshes close artifacts used by an awaiting query | one-cycle `gc` (3.5, 4) | Snapshots |
| 5 | #3 P2 | Versions beyond 32 lineage entries become uncollectable | `LineageRecord` truncates; `GC` only deletes listed entries (3.7) | Store |
| 6 | #3 P2 | Delta-created record readers miss runtime cleanup | delta path bypasses `AddCloser` (3.6) | Resources |
| 7 | #4 P1 | A non-reading SSE client holds a slot indefinitely | no deadline, ignored errors, unbounded join (3.10, 4) | Transport |
| 8 | #4 P2 | Cancelling the first waiter cancels another waiter's build | first caller's signal (3.9) | Operations |
| 9 | #5 P2 | Deferred cleanup turns failure into cancellation | rerank classifies after `cancel()` (3.8) | Operations |
| 10 | #6 P2 | Empty enabled-model list prevents a valid fallback | `preferred` in `pkg/llm/gepp/backend.go`: empty list means "all enabled", but the fallback loop iterates the empty list | Local fix |
| 11 | #7 P2 | Lexical export retains references to removed decisions | exporter heuristics (3.11) | Graph |
| 12 | #7 P2 | Rename does not update decision references | `rename` (3.11) | Graph |

Findings 3 and 10 are corrected locally with tests. The other ten fall into six cause groups, each addressed by one contract below. Step 3 of section 4 — packing after the await from a newer generation — is not a review finding but has the same cause as finding 4: fixing only handle lifetime would leave the response inconsistent.

## 6. Target design overview

### 6.1 Modules

```text
DSL builders, CLI, HTTP, GUI
            │
            ▼
graph: canonical references · compile · edit · project
            │
            ▼
operations: build/refresh (shared jobs) · query · lookup
       │                           │
       ▼                           ▼
  build jobs ──publish──▶ snapshots ◀──lease── query execution
                             │
                     datasets + indexes
                             │
           resource registry (Go) + artifact store

native async bridge ─▶ one immutable outcome ─▶ JS promise
operation events ─▶ one HTTP responder (JSON or bounded SSE)
```

These are modules with one direction of dependency, not a workflow framework. No durable scheduler, plugin system, configuration framework or service container is added. A shared mechanism is justified only where two existing consumers need the same contract.

### 6.2 Invariants

1. A record reference names a dataset version and a record ID.
2. Loading an output from disk changes cost, never collection behavior or identity.
3. A query reads exactly one published snapshot, including the packaging of its result and events.
4. Every opened native resource has one registry entry and explicit owners.
5. A resource closes only when its last owner releases it.
6. Bounded lineage history is never the authoritative store inventory.
7. One waiter's cancellation cannot cancel a shared job another waiter or owner still needs.
8. Every asynchronous operation settles with exactly one immutable outcome.
9. Every network write and flush has a bounded wait; a transport failure ends that transport.
10. Every structural graph reference is enumerated and rewritten by one visitor.
11. Ordinary searches need no close or release call in application code.
12. Each new mechanism replaces its old path in the same phase.

JavaScript cannot enforce linear ownership at compile time. Private handles, one constructor path, checked references and sequence tests make misuse hard to express; native code can still contain bugs and must be tested.

## 7. Datasets and record references

### 7.1 One collection type

Every document or chunk output becomes a read-only **dataset**, whether computed in memory, loaded from disk or produced by a delta build. Access stays synchronous because both current hosts read memory or local disk; a remote storage boundary would be a separate design.

```ts
// Proposed: pkg/ragjs/src/engine/dataset.ts (strict TypeScript)
export type DatasetId = string; // the producing node's build digest (7.3)

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
  order(docId: string): readonly string[]; // this dataset's chunk ids for one document, in reading order
}
```

There are two implementations: `MemoryDataset` over an array (browser and computed outputs) and `StoredDataset` over a Go records2 reader. The `RecordView` class, `isView`, `asArray`, `__records` and the `toArray` convention are deleted. Records returned to callers cannot alter the dataset: memory records are deep-frozen, including nested `meta`; stored records are decoded per read (a cache of immutable decoded records is allowed). Neither the API nor ordinary components see `close`, `__records`, `__ix` or codec choice.

A single conformance suite runs against both implementations in Node and in Goja: iteration order, `null` for missing IDs, immutability of nested fields, `materialize`, `order`, and provenance of hits.

### 7.2 References instead of a union

Hits carry references:

```js
// Proposed hit shape
{ ref: { dataset: 'd-7f3a…', id: 'q3-report#risks' }, score: 0.82, via: 'retrieve-text' }
```

Each index records the dataset it was built from:

```ts
export interface Index {
  readonly dataset: DatasetId;
  search(query: string, options: SearchOptions): Hit[]; // refs into `dataset`
}
```

A snapshot (section 8) resolves a reference against exactly the dataset it names. There is no `art.chunks` union and no primary stream: the neighbors of a hit come from `order` of that hit's own dataset. `pack`, traces, context citations, evaluations and the GUI carry the reference unchanged.

In the running example, `retrieve('text')` returns references into the raw chunk dataset and `retrieve('ctx')` into the enriched one. The logical ID is the same and the bytes differ, and the user sees the text that was actually retrieved, whatever was loaded from disk.

**Fusion.** `fuse` deduplicates exact references, so fusing two indexes over the same dataset behaves as today. Fusing hits from *different* chunk datasets is where shared logical IDs become ambiguous. The compiler can detect this: each `retrieve` names an index node, and each index node names its input chunk node. Proposed rule (decision D3): if a fuse node's inputs resolve to more than one chunk dataset, the fuse must declare `canonical: '<chunk node>'`; hits are re-pointed to that dataset before scoring, and hits whose ID is absent there are dropped with a trace note. Without the option, compilation fails with a message naming the datasets. This is an option of an ordinary component, not an engine merge mechanism, and nothing ever falls back to "last stream wins".

**Document identity.** Document IDs are derived from paths today (`core/identity.ts`). With several sources, two vaults can contain the same relative path. Proposed (decision D4): a logical document ID is source identity plus vault-relative path; titles and slugs are never assumed unique. This changes IDs and therefore evaluation fixtures, which are updated in the same change.

### 7.3 Version identity

A dataset's ID is the build digest of the node that produced it (`Engine.digests`), in every host and whether the value is computed or loaded. The digest already covers component type, normalized parameters, input digests and component source, so equal digests mean equal content. The store key (`storeKey`) remains only a storage address. Using the digest everywhere keeps references identical between cold and warm runs (invariant 2). An index's version derives the same way from its component and codec version, parameters and input dataset. `natives.Version` and `runner.ArtifactNamespace` continue to separate incompatible binaries.

### 7.4 Callbacks and bake caching

Bake callbacks and custom components receive datasets:

```js
rag.bake(ctx => ctx.chunks.size);                         // count without a traversal
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id)); // explicit full read
```

The bake cache key becomes a hash of input dataset IDs, the bake source and the component version. An edited note with an unchanged section count yields a new dataset ID, so the stale value is not reused. `buildCtx.docs` and `buildCtx.chunks`, which today return the first docs or chunks node found anywhere in the graph, become the datasets of the node's own declared inputs. Every bake and `rag.define` callback in the repository is migrated in the same change.

## 8. Snapshots, resources and result lifetime

### 8.1 Entities and identities

Two identities are needed and must not be confused:

- A **build slot** is the stable identity of a plan's build subgraph: component types, parameters, wiring and component source, but not file contents. It is computed like `Engine.lineages`. A slot has at most one *current* snapshot. Plans over the same corpus share a slot when their build subgraphs are identical.
- A **build identity** is the slot plus the current content digests and the build options that change results (`calls`, `full`, `force`, `persist`). It names one concrete build: shared jobs are keyed by it (10.2), and the snapshot it produces is named by it.

| Entity | What it is | Owner | Ends when |
|---|---|---|---|
| Resource | One opened native handle (records2 reader, Bleve index, Go BM25 or vector index) | the runtime's Go `Registry` | its last owner releases it |
| Build scope | A build job's private set of adopted resources and temp directories | the build job | publish (handed to the snapshot) or abort |
| Snapshot | Immutable mapping node id → dataset/index, with snapshot ID, slot, generation number and a `partial` flag | the engine's snapshot table | it is retired **and** its lease count is 0 |
| Lease | A query's or lookup's hold on one snapshot | that operation | the operation finishes, including packaging |
| Result | Plain data copied out of a snapshot | the caller | the caller drops it; holds no handles |

A snapshot built with deferred LLM items (section 3.1) is publishable and carries `partial: true` with the deferred counts so the UI can show pending work; it is never persisted (decision D12). Snapshots share unchanged resources: each snapshot is one registry owner of every resource it maps.

### 8.2 Transitions

```text
        refresh (job J)                         query Q
              │                                    │
              │                         acquire(current) → A, leases 1
   build B in J's private scope                    │
   validate B                                      │ awaiting LLM…
   publish B: current := B, A retired              │
              │                                    │
   (next refresh) publish C, B retired             │
     B has 0 leases → B releases its resources     │
     (those also mapped by A or C stay open)       │
                                                   │ LLM resolves
                                    materialize result from A
                                    release lease → A: 0 leases, retired
                                    → A releases its resources
                                    → resources with 0 owners close
```

Rules:

- `acquire` reads the slot's current snapshot and increments its lease count in one synchronous step on the Goja owner goroutine. Publication also runs on the owner goroutine, so no publication can fall between selecting and retaining a snapshot. The Go registry is also touched at shutdown from another goroutine, so it has its own mutex.
- Publishing is a handoff: the new snapshot first retains every resource it maps, then the build scope releases its ownership, then the slot pointer changes and the previous snapshot is marked retired. If any step fails before the pointer changes, the build scope still owns its resources and releases them when the job aborts; the previous snapshot stays current.
- A failed or cancelled build never replaces the current snapshot.
- A current snapshot is never released. A retired snapshot releases its resources when its last lease ends.
- `Session.memo` (live-node output cache) is keyed by snapshot ID instead of `art.key`.
- Shutdown: reject new operations; abort active operations and jobs; wait for them to settle; release all snapshots; close the registry. If waiting times out, report the timeout and leave resources that worker goroutines may still read open rather than declaring cleanup successful.

### 8.3 Query flow

```ts
// Sketch: pkg/ragjs/src/engine/query.ts
async function query(plan: CompiledPlan, signals: Signals, req: RequestScope): Promise<RunResult> {
  if (!snapshots.hasCurrent(plan.slot)) await builds.join(plan, req); // first use only
  return snapshots.withCurrent(plan.slot, async (snap) => {
    const exec = await executor.run(plan, signals, snap, req);
    return materialize(exec, snap, plan.limits); // copies required records
  });                                            // lease released in finally: success, error, cancel
}
```

A query waits for a build only when its slot has no snapshot yet (decision D1). It never starts a rebuild when one exists; detecting changed sources and publishing new snapshots is the refresh operation's job (watcher, manual or post-sync). `ragwb serve --watch 0` already documents "snapshot at open, restart to pick up edits", which matches this rule. Consequence: today a query can discover changed files itself; after this change a served app shows edits only after a refresh.

### 8.4 One path for opening resources

One Go registry per runtime:

```go
// Proposed: pkg/natives/resources.go (sketch)
type ResourceID string // build digest + codec, or a unique id for an unsaved build output
type OwnerID string    // a snapshot id or a build-scope id

type Registry struct {
    mu      sync.Mutex
    entries map[ResourceID]*entry // closer + owner set
    closed  bool
}

// Adopt records owner as an owner of c. If id already exists, c is closed and owner
// is added to the existing entry. After Close, Adopt closes c and returns ErrClosed.
func (r *Registry) Adopt(id ResourceID, owner OwnerID, c io.Closer) error
func (r *Registry) Retain(id ResourceID, owner OwnerID) error
func (r *Registry) Release(id ResourceID, owner OwnerID) error // closes at zero owners
func (r *Registry) Close(ctx context.Context) error            // shutdown only
```

Every opener uses one pattern and adopts before returning a value to JavaScript:

```go
rec, err := OpenRecords2(path)
if err != nil { return nil, err }
if err := reg.Adopt(id, scope, rec); err != nil { _ = rec.Close(); return nil, err }
return storedDatasetObject(vm, id, rec), nil // the JS object carries its ResourceID
```

Persistent loads, delta outputs (`ParseDelta`, `ChunkDelta`) and freshly built native indexes (`bm25_native.go`, `vector.go`) all go through it, as do the build's temp directories (today a list closed by `AddCloser` in `Persist.InstallEnv`). `Persist`, `Markdown` and the index natives require a registry at install time; a missing registry is a construction error, which replaces the no-op `addCloser` fallback.

Duplicate IDs are reused: equal digests mean equal content, so the redundant handle is closed and the existing entry gains the new owner (decision D5). Because only `Adopt` creates entries, two entries for one handle cannot exist.

JavaScript snapshots reach the registry through a small host interface, which the browser implements with no-op closing so the snapshot code is the same in both hosts:

```ts
// Proposed: provided by the host via rag.configure
export interface ResourceHost {
  retain(owner: string, ids: readonly string[]): void;
  release(owner: string, ids: readonly string[]): void;
}
```

### 8.5 Detached results

A result returned by `rag.run` must remain usable after its lease ends, without a close call. `materialize` copies into the result:

- the records of every emitted hit, evidence entry and trace `top` entry;
- configured neighbor previews (today `NB_HITS = 40` hits in `serve.go`);
- the snapshot ID and generation number.

```js
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null; // a hit or a ref
// no cleanup: the result owns plain data
```

`result.chunk`, `result.top` and `result.explain` read only the copied records. A bare string ID is rejected with an error that points to `rag.ref(dataset, id)`, so it can never silently pick a dataset. Materialization copies the bounded result, not the corpus. If a trace or result exceeds a declared record or byte limit, the query fails with `result_too_large`; it never returns hits whose records were omitted (decision D6).

In the served app, `__serve.run` performs packing inside the same lease, so chunk bodies and neighbors come from the snapshot the query ran on. Evaluations (`evaluation/evals.js`) and corpus-wide exports iterate whole datasets; they use the internal scoped read `snapshots.withCurrent(slot, snap => …)` instead of a result.

### 8.6 Generation-aware lookups

The served shell calls `/chunk?id=` and `/doc?id=` on a cache miss. These requests become `?snapshot=<id>&dataset=<id>&id=<id>`. The server resolves against that snapshot if it still exists (current, or retired with active leases) and otherwise answers `410 stale_generation`. It never reinterprets an old reference against the newest dataset. The shell (`web/app-shell/shell.js`) keys its chunk, neighbor, doc and *missing* caches by snapshot and reference and clears them when a run response reports a new snapshot. Already-delivered text cannot be revoked; this is consistency, not access control.

Recommendation for decision D7a: keep no retired snapshot alive beyond its leases. The user-visible consequence is that, after a refresh, walking to a neighbor that was not prefetched returns `410` and the shell reruns the query.

## 9. Durable store ownership and collection

### 9.1 What is authoritative

Keep the payload layout, codecs and `Has` validation. Add:

- **Snapshot manifests**, `<ns>/snapshots/<id>.json`: slot, build identity, created time, `partial` is never persisted, and the store key of every artifact the snapshot maps.
- **Current pointers**, `<ns>/current/<slot>.json`: the slot's current snapshot ID.

Artifact descriptors (`Meta`), manifests and pointers together are the inventory. Lineage files become a derived accelerator for finding a delta base; deleting them costs a full build, never ownership information.

The original design also asks descriptors to record dependencies on other payloads. Reading the codecs, no current payload needs another to be readable: `CowRecords2` hard-links shard files into a self-contained directory, Bleve indexes are relocated as self-contained directories, and JSON, BM25, terms and vector blobs are standalone (the vector loader binds an embedder, not another payload). Proposal: because manifests list every key directly, omit a `deps` field for now, and add it — with transitive marking in GC — the first time a codec references another payload. Phase 4 must confirm this per codec.

### 9.2 One writable owner per namespace

When a process opens a namespace for writing, it takes an OS-backed exclusive lock on `<ns>/.lock` (released by the OS if the process dies) and holds it for its lifetime. The owner performs builds, publication, leases and online GC. A second process trying to open the same namespace for writing fails with a clear "namespace in use" error. `ragwb store gc` takes the same lock and therefore runs only while no server owns the namespace. `artifacts.Prune` takes each namespace's lock before deleting it and skips (and reports) a namespace it cannot lock.

The browser memory store implements the same logical contract without locks or persistence. Several processes sharing one writable store, and cross-machine collection, are out of scope; they would need a separate design.

Consequence: today a user can run `ragwb run` scripts against the store a server uses. After this change that fails until the server stops or the script uses another store. A read-only open mode is decision D8 (recommendation: not now).

### 9.3 Publication order

Steps 1–4 happen for each node as it finishes, as saving does today; the build scope keeps those artifacts from being collected before publication. Steps 5–7 happen once when the build succeeds.

```text
1. Write the payload under a staging dir owned by the build scope (<ns>/.tmp-*).
2. Close writers; validate counts and codec metadata.
3. Rename the payload into place; fsync it and its parent directory.
4. Write its descriptor atomically (temp, fsync, rename, fsync directory).
5. Write the snapshot manifest the same way.
6. Atomically replace current/<slot>.json.
7. Update lineage files (derived).
```

A crash before step 6 leaves the previous snapshot current; orphaned payloads and descriptors are swept later. A crash after step 6 can leave lineage stale, which only costs a full build. `writeAtomic` does not fsync today; adding file and directory syncs is part of phase 4. Rename atomicity alone does not prove power-loss durability, so the sequence is tested on the supported platforms (decision D9).

### 9.4 Mark and sweep

The owner runs collection on the Goja owner goroutine, serialized with publication, so an artifact cannot be swept between becoming durable and being published.

1. **Mark** the store keys of: current snapshots; the newest `R` retained manifests per slot (decision D7b); snapshots with active leases; active build scopes.
2. **Sweep** every descriptor in the namespace's shard directories that is not marked, together with its payload.
3. **Temps.** At startup, with the lock held, every `.tmp-*` directory is an orphan and is deleted. During operation, only temps not owned by an active build scope are deleted. This replaces the one-hour age heuristic, which could delete a temp directory of a build running longer than an hour.
4. Missing or unparsable descriptors, manifests or pointers abort the sweep with an actionable error. Nothing is deleted on a guess.

Deleting one artifact's directory removes only its own hard links (`Store.Delete` relies on this already). Report logical bytes and actually freed bytes (`unlinkedSize`) separately. Retention is a deliberate count per slot, never derived from how long queries run. Retained snapshots keep notes that were removed from the vault on disk until they are swept; document this privacy consequence next to the retention setting.

### 9.5 Store listing

`List` walks the namespace layout explicitly — namespace directories, two-character shard directories, `<key>.json` files — instead of skipping every directory whose name contains a dot. This fixes finding 3; keep a regression with a root such as `reports.store`.

## 10. Operations: native async calls and shared builds

### 10.1 One native operation bridge

Extract the lifecycle shared by sample, decide and rerank:

```go
// Proposed: pkg/ragjs/asyncop (sketch, names may change)
type Kind int
const (Succeeded Kind = iota; CallerCancelled; DeadlineExceeded; ProviderFailed; RuntimeClosed)

type Outcome struct {
    Kind    Kind
    Payload []byte        // immutable; JSON on success
    Failure *llm.Failure  // code, message, optional text
}

type Spec struct {
    Name     string
    Work     func(ctx context.Context, emit func(any), owner OwnerCall) ([]byte, error)
    Classify func(payload []byte, err error, cause error) Outcome
}

// OwnerCall runs fn on the Goja owner and waits for its reply or ctx.Done().
type OwnerCall func(ctx context.Context, fn func(vm *goja.Runtime) (string, error)) (string, error)

// Start returns a JS promise; it installs the abort listener before starting Work.
func Start(vm *goja.Runtime, signal goja.Value, spec Spec) goja.Value
```

Required behavior:

- **Arbiter.** An atomic state decides the race between the provider returning and the caller aborting: `running → finished` when `Work` returns first, `running → cancelled` when the abort listener fires first. The first transition wins. The worker computes the outcome from that state and `context.Cause(ctx)` **before** any deferred cleanup, then posts it once. A result that arrived before the abort stays a result; an abort that arrived first makes the outcome `CallerCancelled` even if the provider then returns an error.
- **Causes.** The abort listener cancels with a caller-cancelled cause; runtime lifetime cancellation and deadlines carry their own causes. This separates caller cancellation from shutdown without reading `ctx.Err()` after cleanup — the rerank bug.
- **Settlement** on the owner resolves or rejects the promise and removes the abort listener.
- **Runtime closed.** If the post fails because the runtime is closing, the promise never settles (no JavaScript can observe it), but the worker still releases its resources.
- **Events.** Streaming text and rerank batches are immutable Go values emitted through `emit`, which posts them to the owner. After a terminal transition, further events are dropped.
- **Tools.** Sampling tool calls (`exec` in `sample.go`) use `OwnerCall`; worker goroutines never touch Goja values.
- **Goroutines inside `Work`.** Related goroutines, such as rerank batches, use `errgroup` with the operation's context instead of a hand-written first-error variable.

Request decoding, JSON validation, tool semantics and error mapping (`classifyDecideError`, `llm.Failure`) stay in their packages as `Work` and `Classify`.

### 10.2 Shared build jobs

Builds run in JavaScript on the owner goroutine, so the job table is TypeScript:

```ts
// Proposed: pkg/ragjs/src/engine/jobs.ts
interface Waiter { signal: CancellationSignal; onEvent?: EventSink }

class BuildJobs {
  join(plan: CompiledPlan, waiter: Waiter, opts?: BuildOptions): Promise<BuildReport>;
  refresh(plan: CompiledPlan, owner: CancellationSignal, opts?: BuildOptions): Promise<BuildReport>;
}
```

- Jobs are keyed by **build identity** (8.1). `signal` and `onEvent` are not part of the key.
- Each job has its own scope (`newScope` in `engine/scope.ts`), linked to engine shutdown. The job's signal — not any waiter's — is passed to `Engine.build`.
- A waiter's abort detaches only that waiter, whose promise rejects with `cancelled`. When the last waiter detaches and the job has no refresh owner, the job is aborted and its build scope released.
- A refresh owner keeps the job alive independently of waiters. Its signal comes from the refresh operation's context: `watchApp` already passes a context with a ten-minute timeout to `ServedApp.Refresh`, which today is not connected to the JavaScript build. Engine shutdown also aborts it.
- A waiter that arrives while a job is cancelling waits for it to settle and then starts a new job; it never attaches to a job that is being cancelled.
- Progress goes to every attached waiter, tagged with the job ID so each HTTP request can attribute it. Each waiter's sink is isolated (`safeEmit`); a failing or detached sink affects only its waiter, and no events stay bound to the first request's scope.
- `__serve.refresh` calls `BuildJobs.refresh` instead of `eng.build`, so a refresh and a first query for the same content share one job.

On success the job publishes a snapshot (8.2). On failure or abort it releases its build scope and the current snapshot is unchanged.

## 11. HTTP execution and bounded SSE

### 11.1 One responder

Both `/run` and `/verbs/...` parse their parameters and then call one helper:

```go
// Proposed: pkg/server/respond.go (sketch)
type Operation func(ctx context.Context, sink runner.EventSink) (json.RawMessage, error)

func (s *RagServer) respond(w http.ResponseWriter, r *http.Request, a *RagApp,
    start json.RawMessage, op Operation)
```

`respond` acquires an in-flight slot (for both endpoints now), selects JSON or SSE, runs the operation, and produces exactly one terminal response. It releases the slot once the operation has returned **and** the SSE writer has exited. Both endpoints share error codes: `busy`, `bad_request`, `run_failed`/`verb_failed`, `slow_client`, `cancelled`. Domain execution only emits typed events and returns a result; provider events stay independent of HTTP.

### 11.2 The SSE writer

- One goroutine, the writer, is the only code that writes or flushes the response: headers, events, heartbeats and the terminal event. The handler passes the terminal event to the writer over a channel.
- Before each write and flush the writer calls `http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout))` and checks the error returned by its `Flush()`. If the controller reports `http.ErrNotSupported`, streaming is refused as a setup error before any event is sent. Response wrappers in middleware must implement `Unwrap() http.ResponseWriter` so the controller reaches the connection.
- On any write or flush error, including an exceeded deadline (which cannot be extended afterward), the writer stops, cancels the request context and exits. A failed connection is not promised a terminal event.
- `streamSink.Emit`, called on the Goja owner goroutine, never blocks: it does a non-blocking send; on overflow it marks the stream `slow_client`, cancels the request and drops further events. This removes the current 100 ms owner stall.
- The queue, coalesced text and heartbeats are bounded by bytes as well as entry counts.
- Per-write deadlines bound each write without cutting off a long, healthy stream.

In the running example, the closed laptop stops reading; the next write exceeds its deadline; the writer cancels the request; `ServedApp.Run` aborts the query's scope (`abortScope`); the LLM call is cancelled through the operation bridge; the query releases its lease; `respond` releases the slot. The slot is held until the operation returns because the runtime is genuinely busy until then. That the operation returns promptly after the abort depends on `ragjs.Eval` returning once the JavaScript run settles; this guide did not verify that path, so the phase-6 test must assert slot recovery within the write timeout plus a stated cancellation bound, using a real socket.

## 12. Graph references and transformations

### 12.1 Canonical references

The DSL keeps its concise forms; the compiler normalizes them once:

```js
// DSL
when: { decision: 'route.needs', min: 0.5 }
retrieve('text', { k: 30 })

// Canonical IR (sketch)
when: { source: { kind: 'decision', node: 'route', question: 'needs' }, min: 0.5 }
p:    { index: { kind: 'artifact', node: 'ix-text' }, k: 30 }
```

Component definitions declare which parameters hold references (`refs: { index: 'artifact', indexes: 'artifact[]' }`) instead of computing `uses` from names with the `ix-` prefix convention. One module serves the compiler, the editor and the exporter:

```ts
// Proposed: pkg/ragjs/src/graph/refs.ts
type GraphRef =
  | { kind: 'data'; from: NodeId; to: NodeId; port: number }
  | { kind: 'artifact'; from: NodeId; to: NodeId; param: string }
  | { kind: 'decision'; from: NodeId; to: NodeId; question: string }
  | { kind: 'signal'; name: string; to: NodeId }
  | { kind: 'output'; name: string; to: NodeId };

function references(graph: RawGraph): GraphRef[];
function rewriteReferences(graph: RawGraph, rename: ReadonlyMap<NodeId, NodeId>): RawGraph;
```

`finalize` uses `references` for existence, port types, phases, edges and cycles. `rename` checks that the target ID is unused, rewrites through `rewriteReferences` and validates once after the whole op list. `remove` consults `references` for every dependent: data dependents are rewired as today; artifact and decision dependents cause rejection unless the op states `cascade: true` or names a replacement (decision D10).

### 12.2 Lambdas

Arbitrary JavaScript cannot be renamed by string replacement. A lambda that reads graph values declares its bindings:

```js
when: { uses: { route: 'route' }, fn: (s, { decision }) => decision('route').needs.score > 0.5 }
```

The callback uses the local alias `route`; a rename rewrites the binding's target node, not the callback source. A lambda that calls `decision('x')` for an undeclared `x` is a compile error. This removes the regex in `whenRefs` and turns opaque references into an explicit restriction.

### 12.3 Export as an explicit projection

Components declare capabilities such as `local-index`, `external-model`, `host-filesystem` and `native-only`. The lexical HTML export projects the graph onto the browser-supported set:

1. Remove nodes requiring unsupported capabilities (classifiers, model rerankers, answers, LLM enrichment).
2. For a dependent whose `when` reads a removed decision, apply the documented lexical-export rule: drop the condition, so retrieval runs unconditionally.
3. For any other dependent whose required reference was removed, refuse the export with a message naming both nodes.
4. Validate the final graph before serializing it.

The page currently embeds `engine.files()`, which is empty when sources come from `source.dir` on the server. The export should embed a normalized portable dataset taken from the snapshot (decision D11).

## 13. Module layout and deletions

Split modules only as contracts land, and keep the `rag` facade in `api/index.js`. Browser and Goja keep loading one generated bundle; `pkg/ragjs/module.go` and exported pages need no separate loader.

| Concern | Today | Proposed |
|---|---|---|
| Datasets | `engine/records.js` (`Lazy`, `unionChunks`, `isView`, `asArray`) | `engine/dataset.ts` |
| Snapshots and leases | implicit in `Engine.artifacts`, `Engine.gc` | `engine/snapshots.ts` |
| Build jobs | `Engine.buildShared` | `engine/jobs.ts` |
| Query and results | `engine/session.js`, `decorateResult` | `engine/session.ts`, `engine/result.ts` |
| Graph references | spread across `graph/graph.js` | `graph/refs.ts`, used by `graph.js` |
| Export | heuristics in `export/html.js` | capability projection |
| Native resources | `addCloser` in each native | `natives/resources.go` |
| Async bridges | `sample.go`, `decide.go`, `rerank.go` | one bridge plus three specs |
| HTTP | two orchestrations and `sse.go` | `server/respond.go` and a bounded `sse.go` |

Delete as the replacements land: `unionChunks`, `RecordView`, `isView`/`asArray`, consumer-side record-view tests, primary-stream order, `Engine.retired` and one-cycle `gc`, direct `__records.close`/`__ix.close` calls, the no-op `addCloser` fallback, first-waiter `buildShared`, the duplicated HTTP pumps and lineage-driven `Store.GC`. Keep the memory algorithms used by the browser and by parity tests. The older `records` chunk codec is a removal candidate: `Persist.load` still reads it, while `save` writes `records2` for chunks and docs; confirm no writer remains before deleting it.

Review rule: every new concept must retire at least one existing responsibility or duplicated mechanism, and no component-specific branch is added to the executor when a component contract can express the behavior.

## 14. Decisions

**Accepted** (original design and the user's direction):

- Compatibility may break. Callers, fixtures and stored formats change together. The serialized IR version and artifact namespace are bumped. Old stores need an explicit cleanup command when no process owns them; user directories are never deleted silently.
- One writable owner per store namespace (9.2), not a multi-process collector.
- Synchronous datasets (7.1).
- Detached results rather than closeable results (8.5).
- New contracts in strict TypeScript, with discriminated unions for outcomes and references.

**Proposed, needing confirmation:**

| ID | Question | Recommendation | Alternatives | Blocks |
|---|---|---|---|---|
| D1 | May a query start a rebuild when its slot has a snapshot? | No; refresh detects changes | query-triggered rebuild (today) | Phase 3 |
| D2 | Snapshot granularity | Per build slot; resources shared through the registry | per app; per corpus | Phase 3 |
| D3 | Fusion over datasets with shared IDs | Compile error unless `canonical` is declared | keep both; last wins (rejected) | Phase 2 |
| D4 | Document identity across sources | source identity + vault-relative path | path slug (today) | Phase 2 fixtures |
| D5 | Registry duplicate IDs | Reuse entry, close redundant handle | reject | Phase 3 |
| D6 | Over-budget results | `result_too_large` error; limits per plan | pagination contract | Phase 3 |
| D7a | Retired snapshots kept for lookups | None beyond leases; `410` then rerun | keep last N | Phase 3 |
| D7b | Retained snapshots on disk | Small fixed count per slot | time-based | Phase 4 |
| D8 | Read-only access for other processes | Not now | read-only open mode | Phase 4 |
| D9 | Platforms with tested durability | Linux and macOS | add Windows | Phase 4 |
| D10 | `remove` with artifact/decision dependents | Reject; explicit `cascade` or replacement | cascade by default | Phase 7 |
| D11 | Export payload | Normalized documents dataset from the snapshot | raw source files | Phase 7 |
| D12 | Builds with deferred LLM items | Publish with `partial: true`; never persist | refuse to publish | Phase 3 |
| D13 | Write timeout and byte limits for SSE | Configurable; e.g. 10 s per write | — | Phase 6 (values only) |

D1, D2, D3 and D12 shape APIs and should be settled before the phase they block. D6, D7b, D9 and D13 are values an implementer can choose and document.

## 15. Implementation plan

### Where to start

```sh
pnpm install --frozen-lockfile && pnpm build && pnpm check
go test ./pkg/ragjs ./pkg/runner ./pkg/server ./web
```

Then read `Session.run` (`engine/session.js`), `serveHarness` (`pkg/runner/serve.go`) and `Engine.artifacts` (`engine/engine.js`), in that order, with the running example in mind.

### Phases

Each phase is a vertical change: tests, migration of its consumers and deletion of the code it replaces. Do not extract empty abstractions first or leave two engines running side by side. The PR stack (#2–#7) is not merged; choose one integration branch for this refactor instead of fixing divergent copies.

1. **Characterize.** Add deterministic regressions for all twelve findings and record which fail at baseline. Cover cold, stored and delta paths, and an equal-count changed input for bakes. If the stack is used before migration finishes, land the P1 SSE fix early, in the shape of the final writer (11.2) rather than a temporary path.
2. **Datasets and references.** `dataset.ts` with memory and stored implementations; conformance suite in Node and Goja; indexes carry `dataset`; hits carry `ref`; `fuse` gains `canonical`. Migrate `components/builtins.js` (its `ctx.chunk(h.id)` call sites), `components/hits.js`, `engine/events.js`, `evaluation/evals.js`, the shell and the exporter. Remove `unionChunks`, views and `asArray`; derive bake keys from dataset IDs.
3. **Snapshots and resources.** Go registry with every opener adopting; snapshot table with slots, leases and handoff; `materialize`; `__serve.run` packaging inside the lease; generation-aware `chunk`/`doc`. Delete `Engine.gc`, `retired` and the `addCloser` fallbacks.
4. **Store authority.** Namespace lock (also in `Prune`), manifests and current pointers, fsync, mark/sweep, explicit `List`. Lineage becomes derived. Confirm per codec that no payload depends on another.
5. **Operations.** Async bridge for sample, decide and rerank; `BuildJobs`; refresh through jobs with the refresh context as owner. Separately, fix the Pi fallback in `preferred`: when the enabled list is empty and the default is unavailable, choose the first eligible model ordered by reference string, so the choice is deterministic despite map iteration.
6. **Transport.** `respond.go`, the bounded SSE writer, the non-blocking sink and admission for `/run`.
7. **Graph.** `refs.ts`, canonical IR, rename and remove, lambda bindings, capability export.
8. **Cleanup and measurement.** Remove dead code and codecs; update examples, help and API docs; exercise the vault app and the reports workflows; record measurements.

Run focused tests while developing each contract, and the full checks at phase boundaries.

## 16. Tests and acceptance

| Contract | Sequence and assertion |
|---|---|
| Dataset conformance | Identical assertions over memory, stored and delta datasets, in Node and Goja |
| Provenance | Two datasets sharing IDs resolve their own bytes through retrieve, fuse, pack, events and shell; fusing them without `canonical` fails to compile |
| Bake inputs | Cold and warm builds pass a dataset; an equal-count changed input invalidates the bake |
| Snapshot leases | Pause a query in a fake provider; publish two snapshots; resume; packaged records equal the first snapshot's |
| Resource lifecycle | Fresh, loaded and delta resources are adopted; counting closers show exactly one close per handle on build failure and on shutdown |
| Result ownership | A result stays readable after its snapshot's resources are closed |
| Durable inventory | Create 40+ versions, retain three; the sweep leaves exactly the reachable set, independent of lineage length |
| Store lock | A second writer, `store gc` and `store prune` are refused while a server holds the namespace |
| Crash recovery | Fail after each publication step; reopening yields a complete old or new snapshot, never a mix |
| Shared jobs | Cancel A while B waits → B succeeds; cancel both → job aborted and scope released; a late waiter starts a new job; a refresh owner keeps a job alive without waiters |
| Async outcome | Provider failure stays failure after cleanup; caller abort stays cancellation; runtime close releases the worker |
| SSE bounds | A real TCP client that stops reading: the stream ends within the write timeout, the operation is cancelled and the slot returns; flush errors end the stream |
| Graph edits | Rename and remove keep every reference valid or reject; no dangling decisions; undeclared lambda references fail to compile |
| Export | A classified plan exports a working unconditional lexical page |
| Local fixes | A dotted store root lists its outputs; an empty enabled list selects a deterministic eligible model |

Use synthetic corpora, provider fakes and hold/release barriers rather than sleeps. Use counting closers and, where practical, open file-descriptor counts. The SSE test must use a real socket; a fake `ResponseWriter` cannot show that write deadlines work.

Measure cold build, warm start, query latency, changed-note refresh, memory, open file descriptors and store growth on one fixture before and after, and report sizes and conditions. Materialization and references add allocations; passing unit tests do not show performance parity, and a regression is investigated rather than taken as a reason to abandon the contract.

The work is complete when each finding has a passing regression or a reviewed disposition; the mechanisms listed in section 13 are deleted; one example works in browser and Goja hosts; documentation matches the public API; and the shutdown, cancellation and publication sequences are tested. A review thread is not resolved merely because an abstraction with the right name exists.

## 17. Risks and scope

- **Result lifetime.** Releasing a lease before copying the needed records reproduces use-after-close under a new API.
- **Shared resources.** Retiring a snapshot releases its ownership; it must not close resources other snapshots still map.
- **Cross-process GC.** Without the namespace lock, in-memory leases protect nothing from another process.
- **Reference migration** is the largest API change: every trace, evidence, GUI and evaluation consumer moves from bare IDs to references.
- **Lifecycle reuse** applies only where semantics match: sampling tool calls are a request/reply protocol, not a one-shot call, and use the bridge's `OwnerCall` rather than bypassing it.

Out of scope: distributed indexing, remote dataset backends, multi-process shared stores, full Obsidian rendering and production authentication. Reindex status and manual refresh can later be built on the refresh operation without another refresh implementation.

When this work is done, each of these questions has one answer at one boundary: Is this an array? Who closes this reader? Whose cancellation controls this build? Which generation does this hit belong to? Which references does this rename update?
