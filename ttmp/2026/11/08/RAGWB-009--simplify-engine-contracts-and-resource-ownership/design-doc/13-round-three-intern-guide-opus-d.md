---
Title: Round three intern guide Opus D
Ticket: RAGWB-009
Status: active
Topics:
    - rag
    - search
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-08T22:54:10.928274103-05:00
WhatFor: ""
WhenToUse: ""
---

# Consistent datasets, snapshots, operations and graph references: an intern guide to RAGWB-009

## 1. What this guide is for

rag-workbench lets an author describe a search application in a few lines of JavaScript: where documents come from, how they are parsed and split into chunks, which indexes to build, and what happens to a query (retrieve, fuse, filter, rerank, pack evidence, answer). The same script runs in a browser with in-memory data and in the Go host (`ragwb serve`, the CLI) with disk-backed records, native indexes and model providers.

RAGWB-009 cleans up the engine's internal contracts. Reviews of the stacked pull requests found twelve bugs. Most of them share causes. In five places the engine leaves an important question unanswered:

1. What kind of collection is this output?
2. Which version of the corpus does this query read?
3. Who closes this native handle?
4. Whose cancellation stops this shared work?
5. Which values in a graph are references to other nodes?

This guide explains enough of the current engine to show those gaps. It then proposes one contract for each gap and sets out an implementation order and tests.

**Status.** This is a design. Nothing in sections 5–12 is implemented. Source references point to the current tree: runtime baseline `c267f91`, authored runtime sources in `pkg/ragjs/src/`, generated into the committed `pkg/ragjs/assets/rag.js` by `pnpm build`. Failure descriptions come from reading source. Where a failure depends on runtime behaviour that has not been reproduced, the guide says so.

**Ground rules from the project owner.**

- Compatibility may break. Callers, fixtures and stored formats are updated together. There are no adapters, dual code paths or legacy readers.
- Keep what works: the fluent corpus/plan builders, one JavaScript engine shared by browser and Goja, Markdown parsing and section chunking, the Bleve/BM25/terms/vector indexes, incremental (delta) builds, provider implementations and protocol errors, evaluation cases and meaningful integration tests.
- Do not build a framework. Each new concept must retire an existing responsibility or a duplicated mechanism. Do not add a durable job scheduler, a plugin system or a dependency-injection container.

## 2. How the engine works today

### 2.1 An ordinary use

The reports example (`examples/apps/reports/lib/corpus.js`, `lib/plans.js`) makes a good reference point:

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

A **corpus** is the work done when documents change. A **plan** is the work done for each query. The builders are immutable values. `rag.compile` turns them into an **IR** (intermediate representation): a list of nodes, each with an id, a component type `t`, parameters `p`, inputs `in`, a phase and an output kind. There are three phases:

- `signal` nodes are query inputs (`q`, `k`, `filters`).
- `build` nodes (source, parse, chunk, enrich, index) run when the corpus changes.
- `live` nodes (retrieve, fuse, filter, rerank, pack, answer) run for every query.

Each live node also has a **trigger**. A `change` node runs on every query, for example while the user types. A `submit` node runs only on an explicit submit (Enter). LLM nodes, decision nodes (`classify`, `rerank.decide`) and `rerank.model` are `submit` nodes, because they are slow or cost money. The compiler is `finalize` in `pkg/ragjs/src/graph/graph.js`. It type-checks ports, derives edges and computes a topological order.

### 2.2 Build: digests, memo, store and lineage

`Engine.build` (`pkg/ragjs/src/engine/engine.js`) walks the build nodes in order. Each node gets a **digest**. The digest is a hash of the node's type and parameters, its inputs' digests, and either the source files' contents or a component-specific `digest` hook (`Engine.digests`). It names one exact output. The engine keeps `memo: digest → value` and reuses the value for an unchanged node.

When the Go host runs with an artifact store (`--store`), `natives/persist.go` installs `env.artifacts`. A node whose output is already saved becomes a `Lazy` memo entry that loads on first read. How an output is stored depends on the component that produced it. Go-native docs and chunks (from `parse.markdown` and `chunk.sections`) are saved as `records2` directories (sharded, read one record at a time) and load back as a **RecordView**. Bleve indexes are saved as directories. Plain JavaScript arrays, such as the output of an `enrich` lambda, are saved as JSON and load back as arrays.

When the corpus changes, a delta-capable component (`parse.markdown` and `chunk.sections` in `natives/markdown_native.go`) can continue from a previous stored version instead of rebuilding from scratch. To find that base version, the store keeps a **lineage** for each "this node of this pipeline". The lineage key is computed like the digest but without the source contents, so it stays stable across edits (`Engine.lineages`). `pkg/artifacts/lineage.go` remembers the newest 32 versions of each lineage, and a build searches the newest 8.

### 2.3 Query: artifacts and session

`Session.run` (`engine/session.js`) asks `engine.artifacts(ir)` for the current build outputs. If any digest is missing because the sources changed, it waits for `engine.buildShared(ir, …)` and then asks again. The artifacts object looks like this:

```js
{
  key,        // joined digests
  byId,       // node id → output (lazy for stored outputs)
  chunks,     // id → chunk: one merged view over all chunk streams
  order,      // docId → [chunkId …], from the first non-enrich chunk stream
  indexes,    // index name → index handle
  docs, links, synonyms,
}
```

The session runs live nodes in order. `retrieve` looks up an index by name (`ctx.index('text')`) and calls `search(q, k, opts)`. The hits look like `{ id: 'notes-oauth#2', score: 3.1 }`. Later nodes read chunk bodies through `ctx.chunk(id)`, `ctx.neighbors(id)` or `ctx.chunks()`, all of which read `art.chunks`. LLM and decision nodes await providers. The session returns a result object with non-enumerable methods `chunk`, `top` and `explain`, and those methods close over `art`.

### 2.4 The served app

`pkg/runner/serve.go` evaluates an app once in a long-lived Goja runtime and installs `__serve.run`, `refresh`, `chunk` and `doc`. HTTP handlers in `pkg/server/rag.go` (`POST /api/rag/{app}/run`) and `pkg/server/verbs.go` call these functions. **Verbs** are named operations that an app's `app.js` declares (the jsverbs mechanism) and that are exposed as HTTP endpoints. A verb may run other plans on the same engine. Responses are either JSON or a stream of engine events sent as Server-Sent Events (SSE, `pkg/server/sse.go`). The browser shell (`web/app-shell/shell.js`) caches the chunk bodies the server sends and fetches misses by chunk id.

`__serve.run` does three things in order:

1. It awaits `session.run`.
2. It calls `collect()`, which runs `Engine.gc` if the artifacts changed.
3. It packs the chunk bodies mentioned by the result, plus the neighbours of the first 40 hits, reading the artifacts that are current *at that moment*.

Native async work (model sampling, decisions, rerank) runs on Go goroutines. Goja values may only be touched on the runtime's owner goroutine. Each bridge (`ragjs/sample.go`, `ragjs/decide.go`, `natives/rerank.go`) therefore posts its result back to the owner and settles a JavaScript promise there.

## 3. Where it breaks

Here are the twelve review findings (PRs #2–#7 in `go-go-golems/rag-workbench`). The priority labels come from the reviews. In the status column, "confirmed" means the code path that produces the failure can be read directly. "Inferred" means the failure also depends on runtime behaviour, such as socket buffering. No finding has been reproduced by running a test for this guide.

| # | PR / label | Reported failure | Status | Cause |
|---|---|---|---|---|
| 1 | #2 P2 | Stored duplicate chunks select a different version than cold arrays | confirmed | A |
| 2 | #2 P2 | Bake callback receives a record view instead of an array | confirmed | A |
| 3 | #2 P2 | Store listing skips a root whose basename contains a dot | confirmed | local fix |
| 4 | #3 P2 | Two refreshes close artifacts still used by an awaiting query | confirmed | B |
| 5 | #3 P2 | Versions beyond 32 lineage entries become uncollectable | confirmed | B |
| 6 | #3 P2 | Delta-created record readers miss runtime cleanup | confirmed | B |
| 7 | #4 P1 | An SSE client that stops reading holds a slot indefinitely | inferred | D |
| 8 | #4 P2 | Cancelling the first waiter cancels another waiter's build | confirmed | C |
| 9 | #5 P2 | Deferred context cleanup turns failure into cancellation | confirmed (rerank) | C |
| 10 | #6 P2 | Empty enabled-model list prevents a valid fallback | confirmed | local fix |
| 11 | #7 P2 | Lexical export retains references to removed decisions | inferred | E |
| 12 | #7 P2 | Rename fails to update decision references | confirmed | E |

The two local fixes do not justify architecture work. They are described in §12. Causes A to E follow.

### A. A collection's behaviour depends on how it was stored

A chunks output is an array when it was computed or saved as JSON, and a `RecordView` (`recordsViewJS` in `natives/persist.go`) when it was saved as records. The two forms are only partly interchangeable. A view iterates and has `get/has/size/order/toArray`, but it has no `map` or `filter`.

`Engine.artifacts` merges every chunk stream into one `art.chunks` (`unionChunks` in `engine/records.js`). Arrays go into a `Map`, where a later stream overwrites an earlier one that has the same id. Views are consulted only for ids the `Map` lacks, in stream order. Which record wins for a shared id therefore depends on storage:

| Stream A (earlier) | Stream B (later), same ids | Winner |
|---|---|---|
| array | array | B |
| view | view | A |
| view | array | B |
| array | view | A |

Take a corpus with two streams that share ids, where at least one stream is stored as records. Its cold start (everything computed, so all arrays) and its warm start (loaded from the store) can return different bytes for the same hit. Whether a stream ends up as a view depends on whether its component is Go-native, a detail no plan author should need to know. The hit `{ id, score }` does not say which stream it came from, so it cannot be resolved correctly in any case (finding 1).

Bakes have the same problem from the callback side. `rag.bake(ctx => …)` computes a parameter from the corpus at build time. For example, the reports preset (`examples/presets/reports.js`) builds a synonyms table from `ctx.docs`. `resolveBakes` passes whatever the first docs/chunks output currently is, an array or a view (finding 2). It caches the result under `hash(docs.map(d => d.id + d.sections.length), chunks.length)`. Reading the source suggests two problems. First, `synonyms` is not persisted, so a warm start recomputes it with a stored docs input, and a RecordView has no `map`. Second, two corpora with the same ids and section counts but different text reuse a stale bake. The regression test should establish the first behaviour; the second follows directly from the key.

Document identity is weaker than it looks. `docIdFor` (`core/identity.ts`) is a slug of the path, so `notes/A B.md` and `notes/a-b.md` get the same id. Two sources that contain the same relative path also collide. The engine keeps the first document and logs the rest.

### B. Nobody owns the lifetime of a corpus version

Here is finding 4 as a sequence in a served app:

1. Query Q takes `art` (version 1) and awaits an LLM rerank.
2. Refresh R1 builds version 2. `collect()` calls `Engine.gc`, which moves version 1's handles to `retired`.
3. Refresh R2 builds version 3. `gc` closes everything in `retired`, including version 1's record reader and Bleve index.
4. Q resumes and reads a closed handle.

`Engine.gc` assumes that one more rebuild gives every query enough time to finish. Nothing records that Q still uses version 1. A milder form of the same problem needs no second refresh. `__serve.run` packs chunk bodies from the *current* artifacts after the query finishes, so a query that ran on version 1 can return bodies from version 2, or silently omit them (`add` skips ids the current artifacts lack).

Handle cleanup is split between two systems. `Engine.gc` closes some handles. The runtime closes the handles registered through `env.AddCloser`. `Persist.InstallEnv` registers the readers it loads, but it falls back to a no-op when no closer is supplied. The record readers that `parseDelta` and `chunkDelta` create (`natives/markdown_native.go`) are never registered at all (finding 6).

On disk, `artifacts.Store.GC(keep)` uses the lineage lists as its inventory: it deletes versions that are listed but not kept. When `LineageRecord` writes a new entry, it drops every entry beyond the 32nd. A version that falls off the list is no longer listed anywhere, so GC never deletes it (finding 5). GC also cannot see users in another process. The `ragwb store gc` help text tells users to run it "while no ragwb serve uses an old version".

### C. Shared work is owned by whoever arrives first

`Engine.buildShared` lets a second query wait for a build that is already running. It stores the first caller's promise, and the build runs with the first caller's abort signal and event sink. The results:

- If the first HTTP request is cancelled, the build stops for every waiter.
- If the second request is cancelled, nothing happens.
- Build progress goes only to the first request's stream (finding 8).

`__serve.refresh` calls `eng.build` directly, not `buildShared`, so a refresh and a query-triggered build of the same plan can run at the same time.

The three native bridges repeat the same lifecycle with small differences. In `rerank.go`, the worker goroutine runs `defer cancel()` and posts a settlement callback. The callback runs later on the owner goroutine and checks `callCtx.Err()`. By then the deferred cancel has always run, so a provider failure is reported as `cancelled` (finding 9). `sample.go` and `decide.go` classify the error on the worker before posting, which avoids this bug. Two smaller problems remain in all three bridges: `decide` reports a deadline as `cancelled`, and none of the bridges removes its abort listener.

### D. HTTP writes have no bound

`sseWriter.write` and `ping` ignore write errors. `Flush` returns no error and has no deadline. Reading the code gives this sequence for finding 7, which has not been reproduced on a real socket:

1. A client stops reading. The TCP buffer fills, and the pump goroutine blocks inside a write.
2. The event queue fills, and after 100 ms `streamSink` cancels the request.
3. The handler then joins the pump (`<-pumped` / `<-done`), which never returns.
4. In the verb handler, `defer a.release()` therefore never runs, and the in-flight slot stays taken.

The direct `/run` handler has no admission control at all. Each handler has its own copy of the pump and terminal-event logic.

### E. References in the graph are found in different ways

A node can refer to other nodes in four ways:

| Reference | Where it lives | How the compiler finds it |
|---|---|---|
| Data input | `n.in` | a stored list |
| Artifact dependency | `retrieve('text')` depends on node `ix-text` | a per-component `uses(p)` builds `'ix-' + p.index`, a naming convention |
| Decision | `when: { decision: 'route.needs' }` | string split in `whenRefs` |
| Decision inside a lambda | `ctx.decision('route')` in the function source | regex over the source text |

The `rename` operation in `applyOps` (`graph.js`) rewrites only `in` and `outputs`. Renaming `route` in the `adaptive` plan (`plans.js`) leaves `when: { decision: 'route.needs' }` in three nodes, and `finalize` then rejects the edited plan (finding 12).

The HTML exporter (`export/html.js`) removes every submit-triggered live node except answers, and then removes answers and context. For `adaptive`, this removes the `route` classifier but keeps the retrieves whose `when` depends on it. Reading the code suggests that `applyOps` then throws and the export fails (finding 11). The exporter also embeds `engine.files()`, which is not the corpus when the app reads a directory source.

## 4. Target design in one page

```text
 DSL builders / GUI / CLI / HTTP
              |
       Graph compiler  ── one reference visitor (§11)
              |
   Application operations: build · refresh · query · lookup
         |                              |
   Shared build jobs (§8)        Query executor
         |                              |
         +------ Snapshots + leases ----+   (§6)
                        |
               Datasets and indexes (§5)
                        |
         Resource registry + artifact store (§6.3, §7)

 Provider bridges ─> one operation runner (§9) ─> typed events ─> one JSON/SSE path (§10)
```

The design enforces these rules:

1. A hit names its record by `{ dataset, id }`, not by id alone.
2. A stored output behaves exactly like a computed one. Only the cost differs.
3. A query reads one published snapshot from its start to the end of result packaging.
4. Each open native resource has exactly one registry entry and closes after its last owner releases it.
5. The durable inventory consists of descriptors and manifests. Lineage only accelerates the search for a delta base.
6. A shared job is cancelled only when nobody wants it any more.
7. Each async operation has one terminal outcome, fixed before any cleanup runs.
8. Every network write and flush has a deadline, and a failed write ends that request.
9. Every structural graph reference is visited by the same code in compile, edit and export.
10. Application code never calls `close` for ordinary searches.
11. Each new mechanism replaces the old path in the same phase. No parallel legacy implementation remains.

JavaScript cannot enforce ownership at compile time. Private handles, a single constructor path, checked references and sequence tests make misuse hard to express. Native code can still have bugs, so it needs its own tests.

## 5. Datasets and record references

### 5.1 One collection type

Every docs or chunks output becomes a read-only **dataset**, however it is stored:

```ts
// Proposed: pkg/ragjs/src/engine/dataset.ts (strict TypeScript)
interface Dataset<R extends { id: string }> {
  readonly id: DatasetId;          // version identity (5.3)
  readonly kind: 'docs' | 'chunks';
  readonly size: number;
  get(recordId: string): R | null; // frozen record
  values(): Iterable<R>;           // deterministic order
  materialize(): R[];              // explicit full traversal
}
interface ChunkDataset extends Dataset<Chunk> {
  order(docId: string): RecordRef[]; // reading order inside one document
}
```

Access stays synchronous, because both backends answer locally. Do not make `get` asynchronous in anticipation of remote storage. If remote storage ever becomes a requirement, it needs its own bulk-access design.

Records handed to callbacks are frozen: frozen in place by the memory backend, decoded and then frozen by the disk backend. A callback therefore cannot change the stored data. Nested metadata needs a test. Datasets expose no `close`, `__records`, `__ix` or codec details. The implementation may cache decoded records.

Indexes keep their current search call, `ix.search(q, k, opts)`, which is synchronous today (`retrieve` in `components/builtins.js`). Indexes gain two things: each index records the dataset it was built from, and each hit it returns carries that dataset in its reference.

There are two backends. The array backend serves the browser and cold builds. The records2 backend serves the Go store. One conformance suite runs against both and checks iteration order, missing ids, frozen records, materialisation, `order` and references.

### 5.2 Record references and resolution

```js
// A hit after this change
{ ref: { dataset: 'ds:9f2c41…', id: 'notes-oauth#2' }, score: 3.1, via: 'text' }
```

`pack`, trace entries, context evidence, evaluations and the GUI all carry `ref` unchanged. There is no global `art.chunks` any more. Live components resolve through their context:

| Today | Proposed |
|---|---|
| `ctx.chunk(id)` | `ctx.resolve(ref)` |
| `ctx.neighbors(id, o)` | `ctx.neighbors(ref, o)`: same dataset as `ref` |
| `ctx.docChunks(docId)` | `ctx.dataset(name).order(docId)` |
| `ctx.chunks(f)`: every chunk of every stream | `ctx.dataset(name).values()`: one named stream |

With refs, the precedence table from §3A disappears. A hit from the `text` index resolves against the dataset the `text` index was built from, whatever is on disk.

Custom retrieve lambdas that return `{ id }` must also say which dataset the ids belong to. Either the lambda returns refs, or the retrieve node declares a dataset that `normHits` uses to build them.

**Fusion.** `fuse.rrf` deduplicates by exact reference. Hits from different datasets remain separate entries. The reports preset fuses `text` (section chunks) and `failures` (table-row chunks), and these are genuinely different records. The hard case is fusing indexes over two versions of the same logical records, for example original sections and enriched sections. The engine will not guess which version wins. By default the two hits stay distinct, and the trace shows both. An author who wants them merged names a canonical dataset (`fuse.rrf({ canonical: 'enrich' })`), and fusion maps references with the same record id onto it. This is ordinary component behaviour, not an engine-wide merge.

**Document identity.** A document's logical id becomes the source node's id plus the vault-relative path, for example `src:notes/A B.md`. The path is not slugged into collisions. Titles and slugs are display values only. Chunk ids derive from document ids, so they change too, and fixtures are updated with them.

### 5.3 Identities and cache keys

- **Dataset id** is the producing node's store key, `storeKey(digest, def)` in `engine/records.js`. It is a pure hash, so it can be computed with or without a store. It changes whenever inputs, parameters or component code change.
- **Index identity** combines the component/codec version, the normalised parameters and the input dataset id.
- **Bake cache key** combines the bake source, the full input dataset ids and the component version. Inputs of equal size but different content get different keys.

Bake and custom-component contexts receive datasets:

```js
rag.bake(ctx => ctx.docs.materialize().flatMap(d => [].concat(d.meta.aliases ?? [])));
rag.bake(ctx => ctx.chunks.size); // counting needs no traversal
```

Today, `ctx.docs` and `ctx.chunks` mean "the first docs/chunks node in the IR". That default stays when it is unambiguous. When a corpus has several chunk streams, a bake names its input (proposed: `rag.bake({ from: 'failures' }, ctx => …)`). Every in-repository callback is updated in the same cutover.

## 6. Snapshots, resources and result lifetime

### 6.1 Snapshots and leases

A **snapshot** is an immutable, validated set of build outputs for one plan's build graph: `{ id, buildIdentity, datasets, indexes, synonyms, partial }`.

The **build identity** names "this corpus definition", independent of its data. It is a hash over the build nodes' ids, types, normalised parameters, input build identities and component source. It excludes file contents and digest hooks. Do not reuse lineage keys here: `Engine.lineages` returns `null` for `source.files` pipelines. When the sources change, the build identity stays the same, and a new snapshot replaces the old one under it. Plans with identical build subgraphs share snapshots. Plans with different subgraphs have separate snapshots, but those snapshots still share native handles through the registry (§6.3).

A snapshot is **current** until a newer snapshot with the same build identity is published. After that it is **retired**. A **lease** is a counted claim that a query or a scoped read holds on one snapshot. A retired snapshot with zero leases releases its resources.

```text
t0  S1 current, leases=0
t1  query Q leases S1 (leases=1) and awaits an LLM
t2  refresh builds S2 privately, validates, publishes → S2 current; S1 retired (leases=1)
t3  refresh builds S3, publishes → S3 current; S2 retired with leases=0 → S2 released
t4  Q resumes on S1, materialises its result, releases → S1 leases=0 → S1 released
t5  the registry closes handles that no snapshot owns any more;
    handles that S1, S2 and S3 share (unchanged index) stay open for S3
```

Two counters are involved, and they are kept apart:

- **Leases** count users of one JS snapshot. They live in the engine on the JS owner goroutine.
- **Registry owners** count the snapshots and build scopes that hold one native handle. They live in the Go registry (§6.3). Releasing a snapshot removes it as an owner of each of its handles. In the browser, the registry has nothing to close.

Taking "the current snapshot" and incrementing its lease must be one synchronous step on the JS owner, so that no publication can fall between them. Any waiting, such as a cold build, happens before that step:

```ts
// Proposed
async function query(plan, signals, request) {
  const snapshot = await snapshots.acquire(plan.buildIdentity, {
    freshness: request.freshness, signal: request.signal });  // may wait for a build job (§8)
  // acquire returned with the lease already taken
  try {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot);              // §6.4
  } finally {
    snapshot.release(); // on success, error, cancellation and packaging failure
  }
}
```

A cancelled request that is still waiting inside `acquire` detaches from the build job and takes no lease.

Builds write into a private build scope and publish only after validation. A failed or cancelled build never replaces the current snapshot. **Validation** means structural completeness: every node produced its output, the codecs check out, and the indexes match their datasets. A build that deferred LLM items because calls were not allowed (`Engine.partial` today) is complete in this sense. This guide proposes that such a build is publishable, with `partial: true`, because the engine currently serves these builds on purpose. This needs owner confirmation (§16).

### 6.2 When does a query wait for a build?

Today, any change to the source files makes `artifacts(ir)` return null, and the next query waits for a rebuild. That suits an editor: you change a note, press Enter, and see the change. A server wants something else: queries should keep answering from the last good corpus while a refresh runs. `acquire` therefore takes a `freshness` option:

| `freshness` | When a published snapshot exists but sources changed | When none exists |
|---|---|---|
| `'current'` (default for `rag.run`, the browser and the CLI) | Start or join the build job for the current sources, wait, then lease the new snapshot | Build and wait |
| `'published'` (used by `serve`) | Lease the current snapshot at once, and start or join a background refresh | Build and wait |

Detecting a change works as it does today: compute the source digests and compare them with the snapshot's digests.

### 6.3 One path for native resources

A **resource registry** owns every open native handle: record readers, Bleve indexes and temp directories. Entries are keyed by artifact identity, so snapshots that share an unchanged index share one entry. A build has a private **build scope**, and every opener adopts its handle into that scope immediately:

```go
// Proposed: pkg/natives/registry.go (sketch, synchronization omitted)
type ResourceID string
type OwnerID string // a snapshot id or a build-scope id

type Registry struct{ /* mutex; entries map[ResourceID]*entry{closer, owners set} */ }

func (r *Registry) NewScope() *Scope
// Release removes owner from every entry it holds; entries with no owners close.
func (r *Registry) Release(owner OwnerID) error

// Adopt takes ownership of c for this scope. If id is already registered,
// the scope becomes another owner of the existing entry, c is closed,
// and the existing handle is returned — callers must use the returned handle.
func (s *Scope) Adopt(id ResourceID, c io.Closer) (Handle, error)
// Commit makes snapshot the owner of the scope's entries; Abort releases them.
func (s *Scope) Commit(snapshot OwnerID) error
func (s *Scope) Abort()

// Every opener follows this pattern:
rec, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(ResourceID(storeKey), rec)
if err != nil { _ = rec.Close(); return nil, err }
return newChunkDataset(h), nil
```

Stored loads (`Persist.load`), delta outputs (`parseDelta`, `chunkDelta`) and freshly built native indexes (`bm25_native.go`) all take this path. A delta base that is opened only to compute a delta belongs to the build scope and is released at commit or abort; this replaces `tryDelta` pushing it onto `retired`. A runtime that has a store but no registry fails at construction, and the no-op closer fallbacks go away.

Shutdown proceeds in this order:

1. Reject new operations.
2. Cancel active operations.
3. Wait, with a bound, for them to settle.
4. Release snapshots.
5. Close the registry.

If the wait times out, report the timeout. Never report a clean close while worker goroutines may still touch a handle.

### 6.4 Results do not hold handles

`rag.run` stays simple: the caller never closes a result. Before the lease is released, `materializeResult` copies the records the result needs into the result itself: the hit records, the trace detail, the evidence and the configured neighbour previews. The packing that `__serve.run` does today after the query (chunk bodies plus the neighbours of the first 40 hits) moves into this step, inside the lease. A serialised result looks like this:

```json
{
  "snapshot": "snap:51e0…",
  "hits": [ { "ref": { "dataset": "ds:9f2c…", "id": "notes-oauth#2" }, "score": 3.1 } ],
  "records": { "ds:9f2c…/notes-oauth#2": { "title": "OAuth notes", "text": "…" } },
  "neighbors": { "ds:9f2c…/notes-oauth#2": { "prev": null, "next": { "dataset": "ds:9f2c…", "id": "notes-oauth#3" } } }
}
```

```js
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null; // resolves hit.ref in result.records
```

`result.chunk('notes-oauth#2')` with a bare string is rejected, because the id may exist in several datasets. If a low-level caller needs one, `rag.ref(dataset, id)` builds a reference explicitly.

Materialisation copies a bounded result, not the corpus. A result that would exceed its declared size budget fails with a clear `result_too_large` error rather than silently dropping records. The default budget is an implementation choice. Evaluations and corpus exports that must iterate everything use a scoped read, `snapshots.read(buildIdentity, fn)`, which holds a lease for the duration of `fn`.

**Browser lookups.** The shell sends `snapshot` and `ref` with every `chunk`/`doc` request. The server resolves the request against that snapshot if it is still held, and otherwise replies `stale_generation`; the shell then re-runs the query. The shell keys its cache by snapshot and reference and clears it when a response arrives with a new snapshot. How many retired snapshots the server keeps open for lookups is a policy choice (§16). Text that has already been delivered cannot be revoked, and retention is not access control.

## 7. The durable store

### 7.1 Which records are authoritative

Keep the existing immutable payloads and codecs. Add three kinds of record:

- an **artifact descriptor** for each stored output, listing its payload and the artifacts it depends on;
- an immutable **snapshot manifest** for each published snapshot, listing its artifact roots;
- a small **current pointer** for each build identity, replaced atomically.

Descriptors and manifests form the inventory. The lineage files stay, but they are used only to find a delta base. They can be rebuilt or deleted without losing track of anything. Do not add a database for an inventory that descriptors already express.

### 7.2 One owner per writable namespace

The process that writes a namespace holds an exclusive OS lock on it (`flock` on a lock file) for its lifetime. That process owns builds, publication, leases and online GC.

A second writer fails with a clear message that names the owner. This includes a one-shot CLI command that uses the same `--store` while `ragwb serve` is running. That is a deliberate restriction. `ragwb store gc` takes the same lock, so it runs only when no server owns the namespace; a separate process cannot see in-memory leases anyway.

The browser store implements the same logical contract in memory, with no locking or persistence. Several independent processes sharing one writable store is out of scope and would need its own design.

### 7.3 Publication and crash recovery

```text
1. Write payloads into an owned temp directory.
2. Close the writers. Validate counts and codec metadata (checksums where a codec records them).
3. Move the complete payloads into the namespace.
4. Write their descriptors atomically.
5. Write the snapshot manifest.
6. Atomically replace the current pointer.
7. Update the lineage entries.
```

If the process crashes before step 6, the old snapshot stays current. Payloads that were already moved are reachable from no manifest, so GC removes them later. If it crashes after step 6, the lineage may be stale, which only costs a full build next time. A rename is atomic but not durable on its own. Sync the files and the directory around step 6, and test this on the supported platforms.

### 7.4 Garbage collection

GC marks artifacts reachable from:

- the current pointer of each build identity;
- the retained published snapshots (proposed default: 3 per build identity);
- snapshots that have live leases;
- open build scopes.

It follows descriptor dependencies transitively, then deletes the unmarked artifacts. The owning process serialises publication and the mark/sweep decision, so a candidate cannot be collected between becoming durable and being published.

Missing or corrupt descriptors abort collection with an actionable error. Never delete a file whose ownership is unknown. Payload files that are hard-linked into another artifact survive the deletion of one artifact. Report logical bytes and reclaimed disk bytes separately. The retention count is independent of query duration. Retaining old snapshots also retains notes that were since excluded from the corpus; document this privacy consequence.

## 8. Builds and refresh as shared jobs

`buildShared` becomes a small registry of **jobs**. It is not a durable scheduler. A job's key is the build identity plus the source digests plus every option that changes the result (`calls`, `full`, `force`). Today's key is the set of digests. Each job has its own context, linked to engine shutdown and to no request. Each party that wants the result is a **waiter**:

```text
request A ─wait─┐
                ├─> job J (own context) ─> build into scope ─> publish snapshot
request B ─wait─┤
refresh  ───────┘   (background waiter: never cancels on its own)

A cancels     → A detaches; J continues for B
B cancels     → no request waiters left; cancel J unless a background waiter remains
engine closes → cancel J, reject every waiter, join the cleanup
```

A job that is being cancelled is marked as cancelling. A waiter that arrives during that time does not attach to it. The waiter waits for J to settle and then starts a fresh job.

Progress events belong to the job, which fans them out to each waiter's subscription with job and operation ids attached. A slow or closed subscriber loses only its own events and cannot block the job. Timer, file-watch, manual and query-detected refreshes all go through this registry, which removes `__serve.refresh`'s private `eng.build` call. Features such as reindex status can be built on these jobs later without adding another refresh path.

## 9. One bridge for native async operations

Sample, decide and rerank share one lifecycle:

1. Create a Go context.
2. Connect the JS abort signal to it.
3. Run the work off the owner goroutine.
4. Post streaming events back to the owner.
5. Post exactly one settlement.
6. Release everything.

This lifecycle is extracted once. The provider-specific parts stay as small functions: request conversion, result validation, tool execution and error mapping.

```go
// Proposed: pkg/ragjs/operation.go (sketch). Values crossing goroutines are Go values or bytes.
type OutcomeKind int
const (Succeeded OutcomeKind = iota; Cancelled; DeadlineExceeded; Failed; Shutdown)

type Outcome struct {
    Kind    OutcomeKind
    Payload []byte
    Failure *Failure // code, message, provider details
}

type Spec struct {
    Name     string
    // emit posts an event to the owner; call runs fn on the owner and waits for its reply
    // (used for model tool calls). Neither lets the worker touch a Goja value.
    Execute  func(ctx context.Context, emit func(Event), call OwnerCall) ([]byte, error)
    Classify func(payload []byte, err error, cause error) Outcome
}

func Start(vm *goja.Runtime, services Services, signal goja.Value, spec Spec) goja.Value // a promise
```

`Start` installs the abort listener before any work begins and runs `Execute` under an owned task. Each operation has one atomic state, `running → settled | cancelled`, and the first transition wins. That state is the arbiter between a late result and a cancellation. A result accepted before the cancel transition stays the result; otherwise cancellation wins. `Classify` runs on the worker as soon as `Execute` returns, before any deferred cancel, so the outcome is fixed before cleanup can disturb it. Then `Start` posts the outcome to the owner once, and the owner removes the abort listener and settles the promise. If the runtime is closing, the posted callback may never run, but task and resource cleanup still happen.

For rerank, a provider error now becomes `Failed` on the worker. The later deferred cancel cannot turn it into `Cancelled`. Caller cancellation, deadline, provider failure and runtime shutdown are distinct outcome kinds, so `decide`'s current deadline-as-cancel mapping goes away. Related goroutines (rerank batches) run under an `errgroup`. On the JS side, the same outcomes form a TypeScript discriminated union.

## 10. One bounded HTTP response path

Both `/run` and the verb endpoints call one response helper. Domain code emits typed events and returns a terminal outcome. The helper owns admission control, JSON or SSE framing, and the request lifetime. Provider events stay independent of HTTP.

- The SSE writer is the only goroutine that writes or flushes. Before each write and flush (headers, events, heartbeats, the terminal event) it sets a deadline with `http.ResponseController.SetWriteDeadline` and calls the error-returning `ResponseController.Flush`. Any wrapping `ResponseWriter` must support `Unwrap`. If the deadline cannot be set, that is a setup error, not permission to stream without a bound.
- A write or flush error ends the stream. The helper stops pumping, cancels the operation and waits for it with a bound. Queue overflow has the same effect. Once a deadline has passed it cannot be extended, so no further writes are attempted, including a final error event.
- The helper sends at most one terminal event. A broken connection may receive none.
- Text coalescing, heartbeats and the queue are bounded in bytes as well as in entries.
- `/run` and verbs share one admission limit and one error vocabulary.

The in-flight slot belongs to the operation, not to the handler. After the bounded wait, the handler returns. The slot is released when the operation actually settles. An operation that ignores cancellation is therefore visible as "busy" instead of silently overcommitting capacity.

Per-write deadlines let a healthy long stream continue indefinitely, while a stalled stream ends within one deadline. The acceptance test uses a real socket whose client stops reading and checks that capacity comes back.

## 11. Graph references and transformations

### 11.1 One canonical reference form

The DSL stays concise, and the compiler normalises it once:

```js
// DSL
.retrieve('text', { k: 30, when: { decision: 'route.needs', min: 0.5 } })

// Canonical IR (proposed)
{ id: 'text', t: 'retrieve',
  p: { index: { kind: 'artifact', node: 'ix-text' }, k: 30 },
  when: { source: { kind: 'decision', node: 'route', output: 'needs' }, min: 0.5 } }
```

Components declare which parameters are references, replacing the free-form `uses(p)`:

```js
// Proposed component definition fragment
live('retrieve', { refs: { index: 'artifact' }, … });
```

One visitor serves the compiler, the editor and the exporter:

```ts
references(node: Node): Ref[];                              // in, artifact params, when, pinned outputs
rewriteReferences(node: Node, rename: Map<string, string>): Node;
```

`finalize` uses `references` to check existence, output types, phases and cycles. `rename` checks that the target id is free, rewrites every reference and validates once at the end of the edit. Renaming `route` to `router` in `adaptive` now updates the three `when` sources and passes validation. `remove` of a node that something references is rejected, and the error lists the dependents.

Lambdas need one extra rule. A regex over `ctx.decision('route')` cannot be renamed safely. A lambda that reads a decision declares the dependency and receives it by binding name. For example (proposed): `{ when: (s, ctx) => ctx.decision('r').needs.probability > 0.5, needs: { r: 'route' } }`. A rename changes the binding target, not the function source. A structural edit that would need to rewrite an undeclared reference is rejected.

### 11.2 Export as an explicit projection

Components declare capabilities: `local-index`, `external-model`, `host-filesystem`. The exporter keeps the components that a static page supports and applies a written rule to the rest. For the lexical page, the rule removes model-dependent nodes and makes any `when` that reads a removed decision unconditional. For `adaptive`:

```text
before: route(classify) → text, meta (when route.needs) → fuse → filter → rerank.decide → pack → answer
after:  text, meta (unconditional) → fuse → filter          hits output = filter
```

The exporter validates the projected graph before writing it. Instead of `engine.files()`, it bundles the source records that the current snapshot's source node read (name and text), so the page runs the same corpus pipeline in the browser.

## 12. Small independent fixes

- **Dotted store root.** `artifacts.List` (`pkg/artifacts/store.go`) skips any directory whose name contains `.`, including the root itself (`~/.ragwb-store` lists nothing). Skip by layout instead: `.tmp-*`, `lineage`, and `<key>.<codec>` payload directories below a namespace, but never the root. Fix and test this in phase 1. Phase 4's layout-based traversal must keep the test passing.
- **Model fallback.** `preferred` in `pkg/llm/gepp/backend.go` loops over `EnabledModels`. When that list is empty and the default model is unavailable, the loop finds nothing, even though eligible models exist. Choose the first eligible model in sorted reference order, and test that the choice is stable.

## 13. Modules, typing and deletions

Split code only as each contract lands, under `pkg/ragjs/src/`:

| Concern | Current home | Target |
|---|---|---|
| Datasets, refs | `engine/records.js` | `engine/dataset.ts` |
| Snapshots, leases, jobs | `engine/engine.js` | `engine/snapshots.ts`, `engine/jobs.ts` |
| Query execution, results | `engine/session.js` | `engine/session.ts` (executor plus `materializeResult`) |
| References | `graph/graph.js` | `graph/references.ts`, used by `graph.js` without import cycles |
| Export | `export/html.js` | same file, projection-based |
| Served-app packing | `pkg/runner/serve.go` harness | moves into `materializeResult` |

RAGWB-010 supplies the tooling. New contracts are strict TypeScript, with discriminated unions for terminal outcomes and explicit record provenance, and their declarations sit next to their implementations. Each phase migrates the consumers it replaces; do not add a declaration layer over the old array/view forks. TypeScript and Biome check types and style, but they prove neither runtime input validity nor resource lifetime safety, so the lifecycle and conformance tests remain necessary. The browser and Goja keep loading the same generated bundle. Update `pkg/ragjs/module.go` and the exported-page loading together.

Delete as each replacement lands:

- `unionChunks`, `isView`/`asArray`, the JS `RecordView` class and consumer-side view tests;
- `Engine.gc`, `retired`, and the use of `noteIr`/`recentIrs` as a GC input;
- `buildShared` and its first-waiter ownership;
- the no-op closer fallbacks and direct handle closing in engine code;
- the per-handler SSE pumps;
- id-only `result.chunk`.

Remove obsolete record formats, such as the older `records` (`ChunkRecords`) codec still loaded in `persist.go`, only after enumerating live references to them. Keep the memory algorithms that the browser and the quality comparisons use; a uniform interface does not justify deleting a useful backend.

The simplification budget is qualitative but reviewable. Each new concept retires at least one existing responsibility. The executor gets no component-specific branches where a component contract can express the behaviour.

## 14. Migration and implementation phases

**Migration.** Bump the serialized IR version and the artifact namespace version (`runner.ArtifactNamespace`). Existing local caches are disposable, but they are never deleted silently: document that old namespaces are removed with `ragwb store prune` when no process owns them. Saved plans, presets, examples and fixtures are updated in the same commit as each API cutover. The fluent `.from().parse().chunk().index()` and `.retrieve().fuse().filter().rerank().pack().answer()` scripts stay recognisable. `rag.run` and `result.top` remain because they are good interfaces. Bake and custom callbacks, raw hit consumers, inspection endpoints, the GUI, traces, evidence, evals, sweeps and export all migrate to datasets and refs.

**Phases.** Each phase is a vertical change that includes its tests and deletes the code it replaces. Do not extract empty abstractions first, and do not leave two engines running side by side.

1. **Regressions and local fixes.** Add a deterministic test for each of the twelve findings and record which ones fail today. Cover the cold, stored and delta paths, and changed text with equal counts for bakes. Fix the dotted root and the model fallback. If the stack will be used before the full cleanup, land the bounded SSE writer early, already in its final shared shape.
2. **Datasets and references.** The dataset contract with both backends and the conformance suite, index-to-dataset binding, refs through hits/pack/trace/evals/GUI, bake inputs, and removal of the union.
3. **Snapshots and registry.** Leases and the `freshness` option, build scopes, registry adoption in every opener, detached results, and generation-aware lookups in the shell. Delete `Engine.gc`.
4. **Durable store.** Descriptors, manifests, the current pointer, the namespace lock and mark/sweep. Lineage becomes derived.
5. **Jobs and operations.** The shared job registry, with refresh routed through it, and one async runner for sample, decide and rerank that keeps their provider-specific behaviour.
6. **HTTP.** One bounded response helper for `/run` and verbs.
7. **Graph references and export.** Canonical references, the visitor, projection-based export, and explicit rejection of undeclared lambda references.
8. **Cleanup and measurement.** Remove dead code and formats, update help and API docs, exercise the vault app and the reports workflows, and record measurements.

**Getting started.** Read `pkg/ragjs/src/README.md`, then run `pnpm install --frozen-lockfile && pnpm build && pnpm check` and `go test ./pkg/ragjs ./pkg/runner ./web`. Next, run the reports example and trace one query from `pkg/server/rag.go` `handleRun` through `serve.go` `__serve.run` into `Session.run`.

**Process.** The stacked PRs are not merged. Choose one integration branch for this cleanup and update the review stack coherently, rather than fixing divergent copies. This design ticket implies no force push, merge or review-thread resolution. Commit at meaningful validated boundaries. Run focused tests while developing each contract, and full checks at integration boundaries.

## 15. Tests and acceptance

| Contract | Test |
|---|---|
| Datasets | Memory, stored and delta backends return identical records, order and refs |
| Bakes | One callback API cold and warm; changed text with equal counts invalidates the bake |
| Provenance | Two datasets that share ids each resolve to their own bytes through retrieve, fuse, pack and display |
| Leases | Pause a query in a fake provider, publish two snapshots, resume, and check the result packages the old records |
| Freshness | `'current'` sees an edit on the next query; `'published'` answers immediately and the following query sees the refresh |
| Registry | Fresh, loaded and delta handles each close exactly once on error and on shutdown |
| Results | A result stays readable after its lease is released and unused handles are closed |
| Durable inventory | Create 40+ versions, retain 3, and collect exactly the unreachable set, whatever the lineage length |
| Lock | A second writer and a standalone gc are refused while a server owns the namespace |
| Crash | Fail between each pair of publication steps; reopening yields a complete old or new snapshot, never a mix |
| Jobs | Cancel A while B waits → B succeeds; cancel all → the job releases its resources; a new waiter never joins a cancelling job |
| Outcomes | A provider failure stays a failure after cleanup; a user cancel stays a cancel; a deadline is reported as a deadline |
| SSE | On a real socket, a non-reading client and a failing flush end within the configured bound, and the slot is released |
| Graph | Rename, remove and projection keep refs valid or reject explicitly; no dangling decisions |
| Export | `adaptive` exports a working unconditional lexical page |
| Local fixes | A dotted root lists its outputs; an empty enabled list picks a deterministic eligible model |

Use synthetic corpora, provider fakes and hold/release barriers; do not rely on sleeps. Count closes with counting closers, and check actual file descriptors where practical. Keep a real-socket SSE test, because a fake `ResponseWriter` cannot prove that write deadlines work.

Measure cold build, warm start, query latency, changed-note refresh, memory, file descriptors and store growth on the same fixture before and after the change, and report sizes and conditions. Passing unit tests do not establish performance parity: materialising results and carrying refs change allocation. A regression calls for investigation, not automatic abandonment of the cleaner contract.

**Done** means:

- every finding has a passing regression or a reviewed disposition;
- the mechanisms listed in §13 are gone;
- the same example works in the browser/memory host and in the Goja/persistent host;
- the documentation matches the public API;
- the shutdown, cancellation and publication sequences have been verified.

An abstraction with the right name does not resolve a review comment.

## 16. Decisions and open questions

**Snapshot key: structural build identity.** *Context:* a corpus must keep one "current" version while its data changes. *Alternatives:* digest set (changes on every edit, so there is no notion of "current"); lineage keys (undefined for `source.files`). *Decision:* a hash of the build subgraph's structure (§6.1). *Consequence:* plans with identical build graphs share snapshots, and the others share only handles. *Status:* proposed.

**Freshness: explicit per caller.** *Context:* editors want each query to see their edit, and servers want queries never to block on a refresh. *Alternatives:* always wait, as today, which makes server queries stall during refresh; always serve the published snapshot, which shows editors stale results. *Decision:* the `freshness` option, `'current'` by default and `'published'` for `serve` (§6.2). *Status:* proposed; it changes `serve`'s behaviour.

**Store concurrency: one writer per namespace.** *Context:* in-memory leases are invisible to other processes. *Alternatives:* cross-process leases or a distributed collector (out of scope). *Decision:* an exclusive lock, with gc only offline (§7.2). *Consequence:* CLI writers fail while a server owns the store. *Status:* proposed.

**Removal of referenced nodes: reject.** *Context:* guessing how a removed decision should affect a production query is unsafe. *Alternatives:* cascade, or a declared replacement. *Decision:* reject in `remove`; export applies its own written projection rule (§11). *Status:* proposed.

**Fusion across datasets: exact-ref dedupe.** *Context:* the old "later stream wins" behaviour was silent and storage-dependent. *Alternatives:* an engine-wide canonical merge. *Decision:* hits stay distinct unless `fuse` names a `canonical` dataset (§5.2). *Consequence:* a plan that fuses original and enriched sections without `canonical` can show both versions. *Status:* proposed.

**Duplicate adoption: reuse.** When an opener adopts an id that is already registered, it reuses the existing entry and closes the redundant handle (§6.3). *Status:* proposed.

**Still open.** These need the owner's input before or during phase 3:

- Are builds with deferred LLM items publishable snapshots (`partial: true`)? This guide proposes yes (§6.1). The question does not block phases 1–2.
- How many retired snapshots does the server keep open for browser lookups? Keeping more costs file descriptors and memory, while keeping fewer causes more `stale_generation` re-runs. A reasonable default is the current snapshot plus the previous one. Any choice is implementable; this is a tuning decision, not a blocker.

The result-size budget defaults, the per-write SSE deadline and the retained-snapshot count are ordinary implementation choices. Pick documented defaults.

**Risks.** The biggest implementation risk is result lifetime: releasing a lease before materialising recreates the use-after-close bug under a new API. The second is resource sharing: snapshots share immutable artifacts, so releasing a retired snapshot must not close handles that the current snapshot also owns. The third is cross-process GC, which is safe only because of the namespace lock. The costliest migration is record identity, not the fluent DSL: every trace, evidence, GUI and evaluation consumer changes. Reuse the operation runner only where the semantics match. A model tool request/reply is richer than a one-shot rerank, and it goes through the runner's owner-call hook rather than around it.

**Out of scope.** Distributed indexing, remote dataset backends, multi-process shared stores, full Obsidian rendering and production authentication are out of scope.

The design is more explicit than the current code. Its success shows as fewer places where a developer has to ask: Is this an array? Who closes this reader? Whose cancellation controls this build? Which generation does this hit belong to? Which references must this rename update? Each of these questions should have one answer, enforced at one boundary.
