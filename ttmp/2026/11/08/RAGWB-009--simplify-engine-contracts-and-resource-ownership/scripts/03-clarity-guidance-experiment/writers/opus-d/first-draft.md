# Consistent datasets, snapshots, operations and graph references: an intern guide to RAGWB-009

## 1. What this guide is for

rag-workbench lets an author describe a search application in a few lines of JavaScript: where documents come from, how they are parsed and split, which indexes to build, and what happens to a query (retrieve, fuse, filter, rerank, pack evidence, answer). The same script runs in a browser with in-memory data and in the Go host (`ragwb serve`, the CLI) with disk-backed records, native indexes and model providers.

RAGWB-009 is a cleanup of the engine's internal contracts. Reviews of the stacked pull requests found twelve bugs. Most of them are not independent: they come from five places where the engine leaves an important question unanswered — *what kind of collection is this*, *which version of the corpus does this query read*, *who closes this native handle*, *whose cancellation stops this shared work*, and *which strings in a graph are references*. This guide explains the current engine well enough to see those gaps, proposes one contract for each, and lays out an implementation order and tests.

Status: **this is a design. Nothing in sections 5–12 is implemented.** Source references are to the current tree (runtime baseline `c267f91`; authored runtime sources under `pkg/ragjs/src/`, generated into `pkg/ragjs/assets/rag.js` by `pnpm build`). Where I describe a failure that I inferred from source rather than reproduced, I say so.

The ground rules come from the project owner:

- Compatibility may break. Callers, fixtures and stored formats are updated together. No adapters, dual code paths or legacy readers.
- Keep what works: the fluent corpus/plan builders, one JavaScript engine shared by browser and Goja, Markdown parsing and section chunking, Bleve/BM25/terms/vector indexes, incremental (delta) builds, provider implementations, evaluation cases.
- Do not build a framework. Each new concept must replace an existing responsibility or a duplicated mechanism.

## 2. How the engine works today

### 2.1 An ordinary use

The reports example (`examples/apps/reports/lib/corpus.js`, `lib/plans.js`) is a good reference point:

```js
const corpus = rag.corpus('reports')
  .from(rag.source.dir({ root: 'corpus', include: ['*.md'] }))
  .parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  .index('text', rag.bm25({ fields: { title: 2, headingPath: 3, text: 1 } }))
  .index('meta', rag.terms({ keys: ['type', 'tags', 'status', 'aliases'] }));

const fused = rag.plan('fused').over(corpus)
  .signals({ q: '', k: 10, filters: {} })
  .retrieve('text', { k: 30 })
  .retrieve('meta', { k: 30 })
  .fuse.rrf({ k: 60 })
  .filter.where({ field: 'filters' })
  .limit();

const result = await rag.run(fused, { q: 'operation identity' });
result.top(3); // [{ id, score, doc, heading }, …]
```

A **corpus** is the work done when documents change. A **plan** is the work done per query. The builders are immutable values; `rag.compile` turns them into an **IR** (intermediate representation): a list of nodes, each with an id, a component type `t`, parameters `p`, inputs `in`, a phase and an output kind. There are three phases:

- `signal` nodes are query inputs (`q`, `k`, `filters`).
- `build` nodes (source, parse, chunk, enrich, index) run when the corpus changes.
- `live` nodes (retrieve, fuse, filter, rerank, pack, answer) run for every query.

The compiler is `finalize` in `pkg/ragjs/src/graph/graph.js`. It type-checks ports, derives edges and computes a topological order.

### 2.2 Build: digests, memo, store and lineage

`Engine.build` (`pkg/ragjs/src/engine/engine.js`) walks the build nodes in order. Each node gets a **digest**: a hash of its type, parameters, its inputs' digests, and either the source files' contents or a component-specific hook (`Engine.digests`). The digest names "this exact output". The engine keeps `memo: digest → value`; an unchanged node is reused.

When the Go host has an artifact store (`--store`), `natives/persist.go` installs `env.artifacts`. A node whose output was saved under its digest becomes a `Lazy` memo entry that is loaded on first read. Docs and chunks are saved as `records2` directories (64 shards read one record at a time); Bleve indexes as directories; others as blobs.

For changed corpora, a delta-capable component (`parse.markdown`, `chunk.sections` in `natives/markdown_native.go`) can continue from a previous stored version instead of rebuilding from scratch. To find that base, the store keeps a **lineage** per "this node of this pipeline" — a key like the digest but without source contents, so it stays stable across edits (`Engine.lineages`). `pkg/artifacts/lineage.go` stores the newest 32 versions per lineage and the build searches the newest 8.

### 2.3 Query: artifacts and session

`Session.run` (`engine/session.js`) asks `engine.artifacts(ir)` for the current build outputs. If any digest is missing (sources changed), it waits for `engine.buildShared(ir, …)` and asks again. The artifacts object looks like this:

```js
{
  key,        // joined digests
  byId,       // node id → output (lazy for stored)
  chunks,     // id → chunk, one merged view over all chunk streams
  order,      // docId → [chunkId …] from the "primary" chunk stream
  indexes,    // index name → index handle
  docs, links, synonyms,
}
```

The session then runs live nodes in order. Retrieval asks `ctx.index('text').search(q)` and gets hits like `{ id: 'notes-oauth#2', score: 3.1 }`. Later nodes look up chunk bodies through `ctx.chunk(id)`, which reads `art.chunks`. LLM and decision nodes await providers. Finally the session returns a result object with non-enumerable methods `chunk`, `top` and `explain` that close over `art`.

### 2.4 The served app

`pkg/runner/serve.go` evaluates an app once in a long-lived Goja runtime and installs `__serve.run`, `refresh`, `chunk` and `doc`. HTTP handlers in `pkg/server/rag.go` and `pkg/server/verbs.go` call them, either returning JSON or streaming engine events as Server-Sent Events (SSE, `pkg/server/sse.go`). The browser shell (`web/app-shell/shell.js`) caches chunk bodies the server sends and fetches misses by chunk id.

`__serve.run` does three things in order: await `session.run`; call `collect()`, which runs `Engine.gc` if the artifacts changed; then pack the chunk bodies and neighbours mentioned by the result, reading the *current* artifacts.

Native async work (model sampling, decisions, rerank) runs on Go goroutines. Goja values may only be touched on the runtime's owner goroutine, so each bridge (`ragjs/sample.go`, `ragjs/decide.go`, `natives/rerank.go`) posts results back to the owner and settles a JavaScript promise there.

## 3. Where it breaks

The twelve review findings, with my source-reading status:

| # | PR / label | Reported failure | Source status | Root cause (section) |
|---|---|---|---|---|
| 1 | #2 P2 | Stored duplicate chunks select a different version than cold arrays | Mechanism confirmed | A. collections |
| 2 | #2 P2 | Bake callback receives a record view instead of an array | Confirmed | A. collections |
| 3 | #2 P2 | Store listing skips a root whose basename contains a dot | Confirmed | small fix |
| 4 | #3 P2 | Two refreshes close artifacts used by an awaiting query | Mechanism confirmed | B. lifetime |
| 5 | #3 P2 | Versions beyond 32 lineage entries become uncollectable | Confirmed | B. lifetime (durable) |
| 6 | #3 P2 | Delta-created record readers miss runtime cleanup | Confirmed | B. lifetime |
| 7 | #4 P1 | An SSE client that stops reading holds a slot indefinitely | Inferred | D. transport |
| 8 | #4 P2 | Cancelling the first waiter cancels another waiter's build | Confirmed | C. operations |
| 9 | #5 P2 | Deferred context cleanup turns failure into cancellation | Confirmed (rerank) | C. operations |
| 10 | #6 P2 | Empty enabled-model list prevents a valid fallback | Confirmed | small fix |
| 11 | #7 P2 | Lexical export retains references to removed decisions | Inferred | E. references |
| 12 | #7 P2 | Rename fails to update decision references | Confirmed | E. references |

Review sources: PRs #2–#7 in `go-go-golems/rag-workbench`.

### A. A collection's behaviour depends on where it was stored

A chunks output is a JavaScript array when it was just computed and a `RecordView` (`natives/persist.go`, `recordsViewJS`) when it was loaded from the store. The two are interchangeable only in part: a view iterates and has `get/has/size/order/toArray`, but no `map` or `filter`.

`Engine.artifacts` merges all chunk streams into one `art.chunks`. Arrays go into a `Map` (a later stream overwrites an earlier one with the same id); views are consulted only for ids that the Map lacks (`unionChunks`, `engine/records.js`). Consider a corpus that adds an `enrich` step: `enrich` copies each chunk and adds a field, keeping the same id.

```text
chunk (sections)  ->  enrich (adds summary)  ->  index 'text' built over enriched chunks
cold start:  both arrays   -> Map: enriched wins (later stream)
warm start:  both views    -> unionChunks: first view answers -> original wins
mixed:       chunk stored (view), enrich recomputed (array) -> array wins -> enriched
```

The index was built over enriched chunks, but `ctx.chunk(hit.id)` can return the un-enriched bytes depending on what happened to be on disk. The hit `{ id, score }` carries no information about which dataset it came from, so there is no way to resolve it correctly.

Bakes show the same problem from the callback side. `rag.bake(ctx => …)` computes a parameter from the corpus at build time (the reports preset builds a synonyms table from `ctx.docs`). `resolveBakes` passes whatever the first docs/chunks output currently is — array or view — and caches the result under `hash(docs.map(d => d.id + d.sections.length), chunks.length)`. A view has no `map`, so (by reading) a stored docs input would throw; and two corpora with the same ids and section counts but different text reuse a stale bake.

Document identity is also weaker than it looks: `docIdFor` (`core/identity.ts`) is a slug of the path, so `notes/A B.md` and `notes/a-b.md` collide, and two sources with the same relative paths collide too. The engine keeps the first and logs.

### B. Nobody owns the lifetime of a corpus version

Consider a served app and this sequence (finding 4):

1. Query Q starts, takes `art` (version 1) and awaits an LLM rerank.
2. Refresh R1 rebuilds; `collect()` → `Engine.gc` moves version 1's handles to `retired`.
3. Refresh R2 rebuilds; `gc` closes everything in `retired` — including version 1's record reader and Bleve index.
4. Q resumes and reads a closed handle.

`Engine.gc` assumes "one more rebuild" is long enough for any query. Nothing records that Q is still using version 1. Step 4 also happens without refreshes in a milder form: `__serve.run` packs chunk bodies from the *current* artifacts after the query, so a query that ran on version 1 can return chunk bodies from version 2, or omit them.

Cleanup is split between two systems. `Engine.gc` closes some handles; the runtime closes handles registered with `env.AddCloser`. `Persist.InstallEnv` registers loaded readers, but falls back to a no-op when no closer is available, and delta outputs created by `parseDelta`/`chunkDelta` (`natives/markdown_native.go`) are never registered (finding 6).

On disk, `artifacts.Store.GC(keep)` uses the lineage lists as its inventory: it deletes versions that are listed but not kept. `LineageRecord` drops entries beyond 32 when it writes a new one. A version that falls off the list is no longer listed anywhere, so GC never deletes it (finding 5). GC also cannot see in-memory users at all; the CLI help tells users to run it "while no ragwb serve uses an old version".

### C. Shared work is owned by whoever arrived first

`Engine.buildShared` lets a second query wait for a build already in progress. It stores the first caller's promise, and the build runs with the first caller's abort signal and event sink. If the first HTTP request is cancelled, the build stops for every waiter; if the second is cancelled, nothing happens; build progress goes only to the first request's stream (finding 8). `__serve.refresh` does not use `buildShared` at all, so a refresh and a query-triggered build of the same plan can run concurrently.

The three native bridges repeat the same lifecycle with small differences. In `rerank.go` the worker goroutine `defer cancel()`s its context and posts a settlement callback; the callback runs later on the owner and checks `callCtx.Err()` — which by then is always set by the deferred cancel. A provider failure is reported as `cancelled` (finding 9). `sample.go` and `decide.go` classify on the worker before posting and avoid this, but `decide` maps a deadline to `cancelled`, and none of the three removes its abort listener.

### D. The HTTP stream has no bound on writes

`sseWriter.write` and `ping` ignore write errors; `Flush` neither returns an error nor has a deadline. When a client stops reading, the TCP buffer fills and the pump goroutine blocks inside a write. The event queue then fills and `streamSink` cancels the request after 100 ms — but the handler joins the pump with `<-pumped`, which never returns. In the verb handler the `defer a.release()` of the in-flight slot therefore never runs (finding 7; inferred from reading, not reproduced). The direct `/run` handler has no admission control at all. Each handler has its own copy of the pump/terminal-event logic.

### E. References in the graph are found in different ways

A node can refer to other nodes in four ways:

| Reference | Where it lives | How it is found |
|---|---|---|
| data input | `n.in` | stored list |
| artifact dependency | e.g. `retrieve` uses `'ix-' + p.index` | computed by a per-component `uses(p)` from a naming convention |
| decision | `when: { decision: 'route.needs' }` | string split in `whenRefs` |
| decision inside a lambda | `ctx.decision('route')` in source text | regex over the function source |

`applyOps` `rename` (`graph.js`) rewrites `in` and `outputs` only. Renaming `route` in the `adaptive` plan (`plans.js`) leaves `when: { decision: 'route.needs' }` in three nodes, and `finalize` then rejects the plan (finding 12). The HTML exporter (`export/html.js`) removes every submit-triggered live node except answers. For `adaptive`, that removes the `route` classifier but keeps the retrieves that depend on it; by reading, `applyOps` then throws and export fails (finding 11). The exporter also embeds `engine.files()`, which is not the corpus when the app reads a directory source.

## 4. Target design in one page

```text
 DSL builders / GUI / CLI / HTTP
              |
       Graph compiler  -- one reference visitor (§10)
              |
   Application operations: build · refresh · query · lookup
         |                              |
   Shared build jobs (§8)        Query executor
         |                              |
         +------- Snapshots + leases ---+  (§6)
                        |
               Datasets and indexes (§5)
                        |
          Resource registry + artifact store (§6, §7)

 Provider bridges -> one operation runner (§9) -> typed events -> JSON/SSE (§10)
```

The rules the design enforces:

1. A hit names a record by `{ dataset, id }`, not by id alone.
2. A stored output behaves exactly like a computed one; only cost differs.
3. A query reads one published snapshot from start to the end of result packaging.
4. Each open native resource has one registry entry; it closes after its last user releases it.
5. The durable inventory is descriptors and manifests; lineage is only a search accelerator.
6. A shared job is cancelled only when no one wants it any more.
7. Each async operation has one terminal outcome, fixed before any cleanup.
8. Every network write and flush has a deadline; a failed write ends that request.
9. Every structural graph reference is visited by the same code in compile, edit and export.
10. Application code never calls `close` for ordinary searches.

JavaScript cannot enforce ownership at compile time. These rules are kept by private handles, a single constructor path, checked references and sequence tests.

## 5. Datasets and record references

### 5.1 One collection type

Every docs or chunks output becomes a read-only **dataset**, whatever its storage:

```ts
// Proposed (strict TypeScript, pkg/ragjs/src/engine/dataset.ts)
interface Dataset<R extends { id: string }> {
  readonly id: DatasetId;          // immutable version identity (see 5.3)
  readonly kind: 'docs' | 'chunks';
  readonly size: number;
  get(recordId: string): R | null; // frozen record
  values(): Iterable<R>;           // deterministic order
  materialize(): R[];              // explicit full traversal
}
interface ChunkDataset extends Dataset<Chunk> {
  order(docId: string): RecordRef[];   // reading order inside a document
}
interface Index {
  readonly dataset: DatasetId;     // the chunks it was built from
  search(query: string, opts: SearchOptions): Hit[];
}
```

Access stays synchronous: both the memory and the disk backend answer locally. Records returned to callbacks are frozen (memory) or freshly decoded and frozen (disk), so a callback cannot change the stored data; nested metadata needs a test. Datasets expose no `close`, `__records`, `__ix` or codec details.

Two backends implement it: an array-backed one (browser, cold builds) and a records2-backed one (Go store). One conformance suite runs against both: iteration order, missing ids, frozen records, materialisation, `order`.

### 5.2 Record references and resolution

```js
const hit = { ref: { dataset: 'ds:9f2c…', id: 'notes-oauth#2' }, score: 3.1 };
const chunk = snapshot.resolve(hit.ref);
```

An index stamps every hit with its own dataset id. `pack`, trace entries, context evidence, evaluation and the GUI carry the `ref` unchanged. The enrich example from §3A now resolves correctly: the `text` index was built over the enrich dataset, so its hits resolve against enrich bytes whatever is on disk. There is no global `art.chunks` any more.

**Fusion.** `fuse.rrf` deduplicates by exact reference. Hits from different datasets stay separate entries — the reports preset fuses `text` (sections) and `failures` (table rows), and those are genuinely different records. If an author fuses two indexes over different versions of the same logical records (original and enriched sections), the engine does not guess; the fuse node must name a canonical dataset (`fuse.rrf({ canonical: 'enrich' })`) and the component maps references by logical id onto it. Without that option, such a plan compiles and the hits remain distinct.

**Document identity.** A document id becomes `sourceId + ':' + vault-relative path` (normalised, not slugged to collisions). Titles and truncated slugs are display values, not identities.

### 5.3 Identities and caches

- Dataset id = the producing node's store key (digest plus component source), which already changes when inputs, parameters or code change.
- Index identity = component/codec version + normalised parameters + input dataset id.
- Bake cache key = bake source + full input dataset ids + component version. Equal-sized inputs with different content therefore produce different keys.

Bake and custom-component contexts receive datasets:

```js
rag.bake(ctx => ctx.docs.materialize().flatMap(d => d.meta.aliases ?? []));
rag.bake(ctx => ctx.chunks.size); // counting needs no traversal
```

The current `ctx.docs`/`ctx.chunks` mean "the first docs/chunks node in the IR". In the new contract a bake names its input when there is more than one (`rag.bake({ from: 'failures' }, ctx => …)`); with one docs and one chunks stream, the default is unambiguous.

## 6. Snapshots, resources and result lifetime

### 6.1 Snapshot and lease

A **snapshot** is an immutable, validated set of build outputs for one plan's build graph: `{ id, buildIdentity, datasets, indexes, synonyms, … }`. The **build identity** is the structural key of the build subgraph (like today's lineage keys: no source contents), so the same corpus definition keeps one "current" snapshot while its data changes. Several plans over the same corpus share a build identity.

A **lease** is a counted claim on a snapshot. A query takes a lease at start and releases it after its result is materialised. A snapshot is **current** until a newer one for the same build identity is published, then **retired**; a retired snapshot with zero leases releases its resources.

```text
t0  S1 current, leases=0
t1  query Q leases S1 (leases=1), awaits LLM
t2  refresh builds S2 privately, validates, publishes: S2 current, S1 retired (leases=1)
t3  refresh builds S3, publishes: S3 current; S2 retired (leases=0) -> S2 released
t4  Q resumes, still reads S1, materialises its result, releases: S1 leases=0 -> S1 released
t5  registry closes handles whose last owner was S1 or S2 (unchanged handles shared with S3 stay open)
```

Acquiring "the current snapshot" and incrementing its lease must happen in one step on the JS runtime owner; publication cannot interleave. The Go registry is reached from several goroutines and needs its own mutex.

```ts
// Proposed
async function query(plan, signals, request) {
  const snapshot = await snapshots.acquire(plan.buildIdentity, request); // see 6.2
  try {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot);
  } finally {
    snapshot.release(); // success, error, cancellation, packaging failure
  }
}
```

### 6.2 When does a query build?

`snapshots.acquire` waits only when no snapshot has ever been published for the build identity (first query on a cold app). Otherwise it leases the current snapshot immediately, even if the sources have changed since. A detected change starts or joins a background refresh job (§8). This deliberately changes today's behaviour, where a query that sees changed sources waits for the rebuild: a query now returns from the last good corpus and the next query sees the new one. A failed or cancelled refresh never replaces the current snapshot.

### 6.3 One path for native resources

A **resource registry** owns every open native handle (records reader, Bleve index, temp directory). Entries are keyed by artifact identity, so two snapshots that share an unchanged index share one entry. A build has a private **build scope**; every opener adopts its handle into that scope immediately:

```go
// Proposed, pkg/natives/registry.go
type ResourceID string

type Registry struct{ /* mutex, entries map[ResourceID]*entry */ }

// Adopt takes ownership of c. If id is already registered, the existing
// entry gains an owner and c is closed (the redundant open is discarded).
func (s *Scope) Adopt(id ResourceID, c io.Closer) (Handle, error)

// Commit transfers the scope's handles to a snapshot; Abort releases them.
func (s *Scope) Commit(snapshot SnapshotID) error
func (s *Scope) Abort()

r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(ResourceID(storeKey), r)
if err != nil { _ = r.Close(); return nil, err }
```

Stored loads, delta outputs and freshly built native indexes all use this path. A runtime with a store but no registry is a construction error; the no-op closer fallback goes away. Delta bases opened only to compute a delta belong to the build scope and are released at commit or abort.

Shutdown order: reject new operations, cancel active ones, wait for them to settle (bounded), release snapshots, close the registry. If the wait times out, report it; do not report a clean close while worker goroutines may still use a handle.

### 6.4 Results do not hold handles

`rag.run` stays simple: the caller never closes a result. Before the lease is released, `materializeResult` copies the records the result needs — hit records, trace detail, evidence, configured neighbour previews — into the result. `result.top()` and `result.chunk(hit)` read those copies.

```js
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null; // hit.ref resolves against the result's copy
```

`result.chunk('notes-oauth#2')` with a bare string is no longer accepted when it is ambiguous; `rag.ref(dataset, id)` builds a reference explicitly.

Materialisation copies a bounded result, not the corpus. A result that would exceed a declared size budget fails with a clear `result_too_large` error instead of silently dropping records. Evaluations and corpus exports that need to iterate everything use a scoped operation (`snapshots.read(buildIdentity, fn)`) that holds a lease for the duration.

**Browser lookups.** A run response carries `snapshot` (its id). Later `chunk`/`doc` requests send `snapshot` plus `ref`; the server resolves against that snapshot if it is still held, or replies `stale_generation`. The shell keys its cache by snapshot and reference and clears it when a response carries a new snapshot. Already-delivered text cannot be revoked; retention is not access control.

## 7. The durable store

### 7.1 Authority

Keep the payload formats and codecs. Add:

- an **artifact descriptor** per stored output, listing its payload and the artifacts it depends on;
- an immutable **snapshot manifest** per published snapshot, listing its artifact roots;
- a small **current pointer** per build identity, replaced atomically.

Descriptors and manifests are the inventory. Lineage files remain, but only to find a delta base; they can be rebuilt or deleted without losing track of anything. This also removes the need to guess payload directories from punctuation in names, which fixes the dotted-root listing bug as a side effect (it is still worth a direct test).

### 7.2 One owner per writable namespace

A process that writes to a namespace holds an exclusive OS lock on it (`flock` on a lock file) for its lifetime. That process owns builds, publication, leases and online GC. A second writer fails clearly. `ragwb store gc` takes the same lock, so it runs only when no server owns the namespace. The browser store implements the same logical contract in memory with no locking. Multiple processes sharing a writable store is out of scope.

### 7.3 Publication and crash recovery

```text
1. Write payloads into an owned temp directory.
2. Close writers; validate counts, checksums and codec metadata.
3. Move complete payloads into the namespace.
4. Write their descriptors atomically.
5. Write the snapshot manifest.
6. Atomically replace the current pointer.
7. Update lineage entries.
```

A crash before step 6 leaves the old snapshot current; orphaned payloads are swept by GC because no manifest reaches them. A crash after 6 may leave lineage stale, which only costs a full build later. Rename is atomic but not durable on its own; sync files and the directory around step 6 and test on the supported platforms.

### 7.4 Garbage collection

Mark artifacts reachable from: the current pointer of every build identity, the configured number of retained snapshots, snapshots with live leases, and open build scopes. Follow descriptor dependencies, then delete unmarked artifacts. The owner serialises publish and mark/sweep. Missing or corrupt descriptors abort collection with an actionable message; never delete a file whose ownership is unknown. Hard-linked payload files survive while another artifact links them; report logical and reclaimed bytes separately. Retention is a count of published snapshots (proposed default: 3), independent of query duration. Retaining old snapshots also retains excluded notes; document that.

## 8. Builds and refresh as shared jobs

`buildShared` becomes a small registry of **jobs** keyed by build identity plus every option that changes the result (`calls`, `full`, `force`). A job has its own context linked to engine shutdown, not to any request. Each requester is a **waiter**:

```text
request A --wait--+
                  +--> job J (own context) --> build into scope --> publish snapshot
request B --wait--+
refresh timer ----+ (background owner)

A cancels   -> A detaches; J continues for B
B cancels   -> no waiters left; cancel J unless a background owner holds it
engine close -> cancel J, reject all waiters, join cleanup
```

A waiter that arrives while J is cancelling waits for J to settle and then starts a new job, rather than attaching to one that will fail. Progress events belong to the job and are fanned out to each waiter's subscription, tagged with job and operation ids; a slow subscriber drops its own events and cannot block the job. Timer, file-watch, manual and query-detected refreshes all go through this registry.

## 9. One bridge for native async operations

Sample, decide and rerank share a lifecycle: start a Go context, connect the JS abort signal, run work off the owner goroutine, post streaming events, post exactly one settlement, release everything. Extract it once:

```go
// Proposed, pkg/ragjs/operation.go. Values crossing goroutines are Go values or bytes.
type Outcome struct {
    Kind    OutcomeKind // Succeeded | Cancelled | DeadlineExceeded | Failed | Shutdown
    Payload []byte
    Failure *Failure
}

type Spec struct {
    Name     string
    Execute  func(ctx context.Context, emit func(Event)) ([]byte, error)
    Classify func(payload []byte, err error, cause error) Outcome // provider-specific errors
}

func Start(vm *goja.Runtime, services Services, signal goja.Value, spec Spec) goja.Value // a promise
```

`Start` installs the abort listener before work begins, runs `Execute` under an owned task, and calls `Classify` *on the worker, before any deferred cancel*. The outcome is then immutable. It posts the outcome to the owner once, removes the abort listener there and settles the promise. If the runtime is closing, the post may never run, but task and resource cleanup still happens.

Race rule: a result accepted before the cancel transition stays the result; otherwise cancellation wins. Caller cancellation, deadline, provider failure and runtime shutdown are different outcomes. Model tool calls (sample) need a request/reply hook onto the owner that this runner provides; workers never touch Goja values.

## 10. One bounded HTTP response path

Both `/run` and verb endpoints call one helper. Domain code emits typed events and returns a terminal outcome; the helper does admission control, JSON or SSE framing and the request lifetime.

- The SSE writer is the only goroutine that writes or flushes. Before each write and flush (headers, events, heartbeats, the terminal event) it sets a deadline with `http.ResponseController.SetWriteDeadline` and uses the error-returning `Flush`. Any wrapper `ResponseWriter` must support `Unwrap`. If setting a deadline is unsupported, fail at setup rather than stream unbounded.
- A write or flush error ends the stream: stop pumping, cancel the operation, join it with a bound, release the slot. Queue overflow has the same effect. An exceeded deadline cannot be extended, so do not try to write a final error after it.
- At most one terminal event is sent; a broken network may receive none.
- Text coalescing and the queue are bounded in bytes as well as entries.
- `/run` and verbs share one admission limit and one error vocabulary.

Per-write deadlines let a healthy long stream continue indefinitely while a stalled one ends within one deadline.

## 11. Graph references and transformations

### 11.1 Canonical references

The DSL stays concise; the compiler normalises once:

```js
// DSL
.retrieve('text', { k: 30, when: { decision: 'route.needs', min: 0.5 } })

// Canonical IR (proposed)
{ id: 'text', t: 'retrieve',
  refs: [ { kind: 'artifact', node: 'ix-text' },
          { kind: 'decision', node: 'route', output: 'needs' } ],
  when: { ref: 1, min: 0.5 } }
```

Every component declares its reference slots in its definition. One visitor serves everything:

```ts
references(node): Ref[]
rewriteReferences(node, mapping: Map<string, string>): Node
```

`finalize` validates existence, types, phase and cycles over `references`. `rename` checks the target is free, rewrites every reference and validates once at the end. `remove` of a referenced node is rejected, listing the dependents.

Lambdas: the regex over `ctx.decision('x')` cannot be renamed safely. A lambda that needs a decision declares it (`{ when: s => …, needs: { route: 'route' } }`) and receives it by binding name; rename changes the binding target, not the source. A structural transformation that would require rewriting an undeclared reference is rejected.

### 11.2 Export as an explicit projection

Components declare capabilities: `local-index`, `external-model`, `host-filesystem`. The exporter keeps components that a static page supports and applies a written rule for the rest; for the lexical page: remove model-dependent nodes, and treat a `when` that depends on a removed decision as unconditional. It validates the result before writing. Instead of `engine.files()`, it bundles a portable dataset built from the current snapshot.

## 12. Small independent fixes

- **Dotted store root** (`artifacts.List`): skip directories by layout (shard dirs, `lineage`, `.tmp-*`, `<key>.<codec>` payloads below the namespace), never the root. Test with a root named `my.store`.
- **Model fallback** (`pkg/llm/gepp/backend.go`, `preferred`): if `EnabledModels` is empty and the default is unavailable, choose the first eligible model in sorted reference order. Test that the choice is stable.

## 13. Modules and deletions

Split code only as contracts land, under `pkg/ragjs/src/`:

| Concern | Current home | Target |
|---|---|---|
| datasets, refs | `engine/records.js` | `engine/dataset.ts` |
| snapshots, leases, jobs | `engine/engine.js` | `engine/snapshots.ts`, `engine/jobs.ts` |
| query execution, results | `engine/session.js` | `engine/session.ts` (executor + `materializeResult`) |
| references | `graph/graph.js` | `graph/references.ts` used by `graph.js` |
| export | `export/html.js` | same file, projection-based |

New contracts are strict TypeScript; consumers they replace are migrated in the same phase. Delete: `unionChunks`, `isView`/`asArray`, `Lazy` exposure in outputs, `Engine.gc` and `retired`, `noteIr/recentIrs` as a GC input, `buildShared`, the no-op closers, per-handler SSE pumps, `RecordView` JS class, id-only `result.chunk`. Keep the memory backend.

## 14. Implementation phases

1. **Regressions first.** Add a deterministic test for each of the twelve findings and record which fail today. Include cold, stored and delta paths. If the stack is used before the full cleanup, land the bounded SSE writer early, already in its final shape.
2. **Datasets and references.** Dataset contract with both backends and conformance suite; index→dataset binding; refs through hits, pack, trace, evals, GUI; bake inputs; remove the union.
3. **Snapshots and registry.** Leases, build scopes, registry adoption on every opener, detached results, generation-aware lookup; delete `Engine.gc`.
4. **Durable store.** Descriptors, manifests, current pointer, namespace lock, mark/sweep; lineage becomes derived; fix listing.
5. **Jobs and operations.** Shared job registry; one async runner for sample/decide/rerank; model fallback fix.
6. **HTTP.** One response helper for `/run` and verbs.
7. **Graph references and export.**
8. **Cleanup and measurement.** Remove dead code and formats; update examples and docs; measure.

Start from `pkg/ragjs/src/README.md`: `pnpm install --frozen-lockfile && pnpm build && pnpm check`, then `go test ./pkg/ragjs ./pkg/runner ./web`.

## 15. Tests and acceptance

| Contract | Test |
|---|---|
| Datasets | memory, stored and delta backends give identical records, order and refs |
| Bakes | one API cold and warm; changed text with equal counts invalidates |
| Provenance | two datasets sharing ids resolve their own bytes through retrieve, fuse, pack, display |
| Leases | query paused in a fake provider; publish two snapshots; resume and package old records |
| Registry | fresh, loaded and delta handles each close exactly once on error and shutdown |
| Results | readable after release and after unused handles close |
| Durable inventory | 40+ versions, retain 3, collect to exactly the reachable set |
| Lock | second writer and standalone gc refused while server owns namespace |
| Crash | fail between each publication step; reopen to complete old or new snapshot |
| Jobs | cancel A while B waits → B succeeds; cancel all → job releases resources |
| Outcomes | provider failure stays failure after cleanup; user cancel stays cancel |
| SSE | real socket, non-reading client → ends within bound, slot recovers |
| Graph | rename/remove/projection keep refs or reject; no dangling decisions |
| Export | `adaptive` exports a working unconditional lexical page |
| Small fixes | dotted root lists outputs; empty enabled list picks deterministic model |

Use synthetic corpora, provider fakes and hold/release barriers, not sleeps. Count closes and check file descriptors. Measure cold build, warm start, query latency, refresh, memory, descriptors and store growth before and after.

Done means: every finding has a passing regression or a reviewed disposition, the deleted mechanisms are gone, the same example works in browser and Goja, and docs match the API.

## 16. Decisions and open questions

| Decision | Choice | Status |
|---|---|---|
| Snapshot key | build identity of the plan's build subgraph | proposed |
| Query vs changed sources | serve current snapshot, refresh in background | proposed, changes behaviour |
| Duplicate adoption | reuse entry, close redundant handle | proposed |
| Removing referenced nodes | reject | proposed |
| Cross-dataset fusion | exact-ref dedupe; optional `canonical` | proposed |
| Store concurrency | one writer per namespace with lock | proposed |

Open:

- Are builds with deferred LLM items (`Engine.partial`) publishable snapshots? Today they serve queries.
- How many old snapshots does the server keep open for lookups?
- Result size budget defaults.

Risks: releasing a lease before materialising recreates use-after-close; closing a retired snapshot must not close handles shared with the current one; record identity migration touches every consumer.
