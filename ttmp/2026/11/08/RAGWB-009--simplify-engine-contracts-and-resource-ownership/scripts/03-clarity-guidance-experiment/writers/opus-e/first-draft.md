# RAGWB-009: Consistent datasets, snapshots, operations and graph references

An analysis, design and implementation guide for engineers new to rag-workbench.

## 1. What this guide is for

rag-workbench lets someone describe a retrieval pipeline over a folder of Markdown notes in a few lines of JavaScript, then run it from the CLI, from a long-lived HTTP server with a browser GUI, or entirely inside a browser page. A user types a question; the pipeline finds the most relevant sections of the notes and, optionally, asks a language model to answer from them.

The pipeline engine works, but several of its internal boundaries are ambiguous. A value may be an in-memory array in one run and a disk-backed view in the next. A query may keep using index handles after a background rebuild has decided to close them. A build started by one HTTP request is cancelled when that request goes away, even if another request is waiting for it. Review of the open pull-request stack found twelve concrete defects that follow from these ambiguities.

RAGWB-009 replaces those boundaries with a few explicit contracts. This guide explains the current system first, then each problem and the contract that removes it, then the implementation order and tests. Everything in sections 5–10 is **proposed**; none of it is implemented. Source references are to the working tree at runtime baseline `c267f91` (the RAGWB-010 module layout under `pkg/ragjs/src`).

Two ground rules apply throughout:

- **Compatibility may break.** There is no deployed API or stored data to preserve. Callers, tests and stored formats are updated together. No adapters, dual code paths or legacy readers.
- **Simplicity is the goal.** Each new concept must remove an existing responsibility or duplicated mechanism. This is not a workflow framework, plugin system or job scheduler.

## 2. How the system works today

### 2.1 A pipeline in two halves

A pipeline has two halves with different lifetimes.

The **corpus** describes work that only needs repeating when the notes change: read files, parse Markdown into documents, split documents into chunks (sections of a note small enough to retrieve), optionally enrich chunks, and build search indexes over them.

The **plan** describes work done for every query: retrieve candidate chunks from indexes, combine rankings, filter, rerank, pack the best chunks into a context and optionally generate an answer.

```js
const corpus = rag.corpus('reports')
  .from(rag.source.dir({ root: '/data/reports' }))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());

const plan = rag.plan('search').over(corpus)
  .signals({ q: '', k: 10 })
  .retrieve('text', { k: 30 })
  .limit();

const result = await rag.run(plan, { q: 'operation identity' });
result.top(3); // [{ id, score, doc, heading }, …]
```

The builders are immutable values. `rag.compile(plan)` turns them into an **IR** (intermediate representation): a validated list of nodes in dependency order. Each node has an `id`, a component type `t`, parameters `p`, input node ids `in`, and a **phase**:

| Phase | Meaning | Examples |
|---|---|---|
| `build` | Corpus work, cached across queries | `source.dir`, `parse.markdown`, `chunk.sections`, `enrich`, `index.bm25` |
| `live` | Per-query work | `retrieve`, `fuse.rrf`, `filter`, `rerank.model`, `pack`, `answer`, `classify` |
| `signal` | Query inputs | `q`, `k` |

The example compiles to roughly `source → parse → chunk → ix-text` (build) and `q → text → limit` (live). The live node `text` is a `retrieve` whose parameter `index: 'text'` refers to the build node `ix-text`.

The compiler is `finalize` in `pkg/ragjs/src/graph/graph.js`. It checks ids, port types and cycles and records three kinds of edge: `data` (an input), `artifact` (a live node that *uses* a build output, such as `retrieve` using an index) and `decision` (a node whose `when` condition reads a `classify` result, see §2.6).

### 2.2 Who runs the JavaScript

The engine is one JavaScript bundle, authored as modules under `pkg/ragjs/src/` and generated into the committed `pkg/ragjs/assets/rag.js` by `pnpm build`. The same bundle runs in three hosts:

- **Goja** (a JavaScript interpreter written in Go) inside the `ragwb` CLI and server. `pkg/ragjs/module.go` loads the bundle and installs **natives**: Go implementations of components (Markdown parsing, chunking, Bleve BM25 indexes) and host services (LLM sampling, decisions, reranking, a disk artifact store).
- **Browsers**, where the same components run with memory-backed JavaScript implementations.
- **Exported HTML pages**, a standalone lexical-search page produced by `exportHtml`.

In Goja, all JavaScript runs on one **owner goroutine** per runtime. Go code that wants to touch JavaScript values posts a function to that owner. Long Go work (an LLM call) runs on its own goroutine and posts its result back to settle a JavaScript promise. The backends may rank differently; they must not differ in how collections behave or who owns what.

### 2.3 Building: digests, the memo and the artifact store

`Engine.build` (`pkg/ragjs/src/engine/engine.js`) walks the build nodes in order. For each node it computes a **digest**: a hash of the component type, parameters, input digests and, for sources, the file contents. Equal digest means equal output, so the engine keeps a **memo**, a map from digest to built value, and skips nodes whose digest is already there.

In Goja the engine also has an **artifact store** (`pkg/artifacts/store.go`): a directory where build outputs are saved under a key derived from the digest. On a warm start the engine finds a stored output and installs a `Lazy` placeholder that loads it on first read. Large outputs are not decoded into arrays. Stored chunks and documents use the `records2` format: 64 shards read one record at a time. Loading them returns a **record view** (`recordsViewJS` in `pkg/natives/persist.go`), a JavaScript object with `get(id)`, `has(id)`, `size`, `order(docId)` and iteration, backed by an open Go reader. Bleve indexes are opened in place, also holding Go file handles.

When a source changes, rebuilding everything is slow. The **delta builder** (`Engine.tryDelta`) instead continues from a previous stored version of the same node. To find it, each build node also gets a **lineage key**: like a digest but independent of file contents, so it means "this node of this pipeline". The store keeps, per lineage, a list of recent versions (`pkg/artifacts/lineage.go`). The delta builder searches it for a version computed from exactly the inputs the current inputs changed from, then asks the component (for example `parseDelta`/`chunkDelta` in `pkg/natives/markdown_native.go`) to apply only the change.

### 2.4 Querying: the artifacts object and the session

A query needs the build outputs gathered in one place. `Engine.artifacts(ir)` returns an object, here called **`art`**:

```js
art = {
  key,        // concatenated digests: identifies this generation of outputs
  byId,       // node id → output
  chunks,     // ONE map of every chunk from every chunk-producing node, by chunk id
  order,      // docId → chunk ids in reading order (from one "primary" chunk node)
  indexes,    // index name → index
  docs, links, synonyms,
}
```

It returns `null` if any build node's current digest is not in the memo, which is also how the engine notices that sources changed.

`Session.run` (`pkg/ragjs/src/engine/session.js`) gets `art` (building first if needed), then runs the live nodes in order. Components reach data through a context: `ctx.index('text').search(q, k)` returns hits shaped `{ id, score }`, where `id` is a chunk id; `ctx.chunk(id)` looks the id up in `art.chunks`. A node may `await` an LLM. The result object is decorated with `result.chunk(id)` and `result.top(n)`, functions that keep reading from that `art`.

### 2.5 Serving

`ragwb serve` (`pkg/runner/serve.go`) opens an app in one long-lived Goja runtime: it builds the plan, warms `art`, creates a session and exposes four JavaScript functions to Go:

- `run`: execute a query, then **pack** the chunk bodies and reading-order neighbors the GUI will render (the GUI reads chunks synchronously, so they travel with the response).
- `chunk(id)` and `doc(id)`: lookups for a chunk the GUI did not receive.
- `refresh`: if sources changed, rebuild (incrementally) and release outputs that are no longer current.

With `--watch`, `watchApp` (`cmd/ragwb/cmds/serve.go`) calls `refresh` on a timer. HTTP endpoints in `pkg/server/rag.go` (`POST /api/rag/{app}/run`) and `pkg/server/verbs.go` (application-defined "verbs") return JSON or stream **Server-Sent Events** (SSE): engine events such as node progress and streamed LLM text, followed by one terminal `result` or `error` event. The browser shell `web/app-shell/shell.js` caches every chunk it receives by id.

### 2.6 Decisions and conditional nodes

A `classify` node asks a decision model a question and produces a decision value. Another live node can run conditionally: `when: { decision: 'route.needs', min: 0.5 }` means "run only if question `needs` of node `route` scored at least 0.5". The reference is the string `'route.needs'`.

## 3. What goes wrong, and why

### 3.1 The twelve review findings

The review of PRs #2–#7 left twelve unresolved findings. Priorities are the review labels. Source inspection for this guide confirms each mechanism (§3.2–3.7), but no regression has been run yet; phase 1 of the plan creates them.

| # | PR / prio | Reported failure | Cause (section) |
|---|---|---|---|
| 1 | #2 / P2 | Duplicate chunk ids resolve to a different version when stored than when cold | Global chunk union (3.2) |
| 2 | #2 / P2 | A bake callback receives a record view instead of an array | Storage-dependent values (3.2) |
| 3 | #2 / P2 | `store list` skips a root whose basename contains a dot | Local bug (3.7) |
| 4 | #3 / P2 | Two refreshes close handles an awaiting query still uses | No query ownership (3.3) |
| 5 | #3 / P2 | Versions beyond the 32-entry lineage list are never collected | Lineage used as inventory (3.4) |
| 6 | #3 / P2 | Delta-created record readers miss runtime cleanup | Two resource paths (3.3) |
| 7 | #4 / P1 | An SSE client that stops reading holds a request slot forever | Unbounded writes (3.6) |
| 8 | #4 / P2 | Cancelling the first waiter cancels another waiter's build | First-request build ownership (3.5) |
| 9 | #5 / P2 | Deferred context cleanup turns a failure into a cancellation | Outcome decided after cleanup (3.5) |
| 10 | #6 / P2 | An empty enabled-model list prevents a valid fallback | Local bug (3.7) |
| 11 | #7 / P2 | Lexical export keeps references to removed decisions | Scattered graph references (3.7) |
| 12 | #7 / P2 | Rename does not update decision references | Scattered graph references (3.7) |

Findings 3 and 10 are local corrections. The other ten share six causes, described next.

### 3.2 A collection's behavior depends on where it was stored

Consider the reports corpus with an enrichment step that adds a `summary` field to each chunk:

```js
.chunk(rag.sections({ maxTokens: 450 }))   // node 'chunk'   → chunks with ids like 'q3-report#risks'
.enrich('summary', c => summarize(c))       // node 'enrich'  → same ids, extra field
.index('text', rag.bm25())                  // indexes the enriched chunks
```

Two build nodes now produce chunks with identical ids but different contents. `Engine.artifacts` merges every chunk node into the single `art.chunks` map:

- Cold build (all outputs are arrays): `chunks.set(c.id, c)` in node order, so the **later** node wins: `art.chunks.get('q3-report#risks')` returns the enriched chunk.
- Warm start (stored outputs are record views): views are combined by `unionChunks` (`pkg/ragjs/src/engine/records.js`). Its `get` prefers arrays over views and, among views, the **first**. If both nodes load as views, the unenriched chunk wins.

The same query therefore shows different text depending on whether the server restarted (finding 1). The underlying problem is that a hit carries only `id`, so nothing records which dataset the index actually searched. Reading order has the same issue: `art.order` comes from a "primary" chunk node chosen by excluding `enrich` and `enrich.llm` types (but not `enrich.decide`).

Bake callbacks (`rag.bake(ctx => …)`, values computed from the corpus at build time) show the array/view split directly. `Engine.resolveBakes` passes the first docs and chunks outputs as they are: an array cold, a record view warm. A callback calling `ctx.docs.map(...)` works cold and throws warm, because a record view has no `map` (finding 2). The bake cache key is `hash([docs.map(d => d.id + d.sections.length), chunks.length])`, so editing a note's text without changing its section count reuses a stale bake. Build components, in contrast, receive arrays through `asArray` in `buildCtx`; callers cannot predict which form they get.

Record objects are also mutable. A record view caches up to 4096 decoded objects, and memory arrays share their objects, so a callback that mutates a record changes what later readers see.

### 3.3 Nobody owns a handle for the duration of a query

A record view or Bleve index holds Go file handles. Two mechanisms close them:

1. **Runtime closers.** `Persist.InstallEnv` registers `r.Close` with the runtime when it opens a stored output (`pkg/natives/persist.go`, `load`). If the host supplies no closer hook, it silently substitutes a no-op (lines 47–50; `module.go` does the same).
2. **Engine GC.** `Engine.gc` drops memo entries the current plans no longer need, puts their values on a `retired` list, and closes values that were already retired at the *previous* call. The serve harness calls it after every refresh and after a query when `art.key` changed.

So a superseded handle survives exactly one additional GC. Follow one query through two refreshes:

```text
t0  query Q starts with art A (index handle H), awaits an LLM answer
t1  refresh: sources changed → build B → gc: A's values retired
t2  refresh: sources changed again → build C → gc: retired values closed, H closed
t3  Q resumes, its pack step calls H / A.chunks → use of a closed reader
```

That is finding 4. Delta builds add a third path: `parseDelta`/`chunkDelta` create a new `Records2` reader and wrap it with `ctx.store.view`, which never registers a runtime closer (finding 6). The reader is closed only if engine GC later retires it.

Even without GC, `__serve.run` packs the response after the query from `art()`, the **current** artifacts, which may be a newer generation than the one the query searched. Hit ids from generation A are then looked up in generation B. Fixing handle lifetime is therefore not enough: building the response must use the same generation as the query. The browser shell has the same issue across time: it caches chunks by bare id forever.

### 3.4 The lineage list doubles as the store's inventory

`LineageRecord` keeps the newest `LineageKeep = 32` entries per lineage. `Store.GC(keep)` walks only lineage files and deletes listed versions beyond the newest `keep`. A version that has fallen off the 32-entry list is no longer listed anywhere, so GC never deletes it (finding 5). A search optimization has become the authority on what exists.

GC also takes no lock. `ragwb store gc` runs as a separate process and cannot know which versions a running server has open; its help text asks the operator to "keep at least 2". Publication is not crash-safe as a set: payload and metadata are each written by rename, but nothing groups the outputs of one build, and `writeAtomic` does not sync files or directories.

### 3.5 Asynchronous work has unclear owners and outcomes

**Shared builds.** When a query finds no current `art`, it calls `Engine.buildShared`, which deduplicates concurrent builds by digest set. The first caller's options (its abort signal, progress callback and event sink) become the build's options. A second caller only gets `{type:'build', waiting:true}` and the same promise. If the first HTTP client disconnects, its signal aborts the build that the second client is waiting for (finding 8), and progress goes only to the first client.

**Native async bridges.** Sampling (`pkg/ragjs/sample.go`), decisions (`pkg/ragjs/decide.go`) and reranking (`pkg/natives/rerank.go`) each implement the same lifecycle by hand: create a promise, listen to the abort signal, derive a context, run the provider on a goroutine, post the settlement to the owner. They differ in details. `rerankAsync` posts a closure that checks `callCtx.Err()` when it later runs on the owner; by then the goroutine's `defer cancel()` has run, so a provider failure is reported as `cancelled` (finding 9). `decide` maps a deadline to `cancelled`. None removes its abort listener.

### 3.6 Streaming has no write bound

`sseWriter.pump` (`pkg/server/sse.go`) ignores write errors and sets no write deadline. If a client stops reading, the TCP buffer fills and a write blocks indefinitely. The sink notices its queue is full and cancels the request, the run finishes, and the handler then waits for the pump goroutine to exit (`<-done`). It never does. `handleVerb` holds its in-flight slot in a `defer`, so the slot is never released (finding 7). After eight such clients the app answers every verb with 503. `handleRun` has the same wait but no admission control at all. The two handlers duplicate the sink/pump/terminal-event code with different error codes.

### 3.7 Graph references are discovered in several places, and two local bugs

The compiler finds references through four separate mechanisms: `n.in`, `def.uses(p)` (which turns `p.index: 'text'` into a use of `ix-text`), `whenRefs` (which reads the `'route.needs'` string and also regex-scans lambda source for `.decision('route')`), and `def.reads` plus a scan of lambda source for signal reads. Edits do not use them. The `rename` operation in `applyOp` rewrites `in` and outputs only, so renaming `route` leaves `when: {decision: 'route.needs'}` dangling (finding 12); renaming `ix-text` leaves `retrieve`'s `p.index` pointing at nothing. `remove` checks `uses` but not `when` readers.

`exportHtml` (`pkg/ragjs/src/export/html.js`) removes every submit-triggered live node except answers, then answers and contexts. A `classify` node is submit-triggered, so it is removed, and any node with a `when` on it then fails compilation (finding 11). The export also embeds `engine.files()` as its corpus, which is empty for a server-side `source.dir`.

Two local bugs: `artifacts.List` skips any directory whose name contains a dot so that it does not descend into payload directories (`<key>.records2/`), but the check also applies to the root it was asked to walk, so `List("/data/store.v1")` returns nothing (finding 3). In `pkg/llm/gepp/backend.go`, `preferred` treats an empty `EnabledModels` as "everything enabled" when checking the default, but when the default is unavailable it falls back by iterating `EnabledModels`, which is empty, and finds nothing (finding 10).

## 4. Target design at a glance

### 4.1 Shape

```text
DSL, GUI, CLI, HTTP entry points
          │
Graph: compile, references, edits, projections
          │
Application operations: build / refresh / query / lookup
     │                                   │
Build jobs (shared, with waiters)     Query execution
     │                                   │
     └────────── Snapshots (leased) ─────┘
                     │
         Datasets and indexes (with identity)
                     │
     Resource registry  +  artifact store (descriptors, manifests)

Provider bridges → operation runner → typed events → JSON / SSE transport
```

Dependencies point downward. There is no durable scheduler, plugin framework or service container. A shared mechanism exists only where two existing consumers need the same contract.

### 4.2 Invariants

1. A record reference names both a dataset version and a record id.
2. Loading a stored output changes cost, not collection behavior.
3. A query reads exactly one published snapshot, including building its response.
4. Every opened native resource has exactly one registry owner.
5. A resource closes only after its last user releases it.
6. A bounded search history is never the store's inventory.
7. Cancelling one waiter never cancels a shared job another waiter needs.
8. Every asynchronous operation has one immutable terminal outcome.
9. Every network write and flush has a bounded wait; a transport failure ends that transport.
10. Every structural graph reference is visible to one validation and rewrite API.
11. Ordinary searches need no close or release call from application code.
12. Each new mechanism replaces the old one in the same phase.

JavaScript cannot enforce linear ownership at compile time. These invariants are enforced by private handles, single constructors, checked references and sequence tests.

## 5. Datasets and provenance

### 5.1 One dataset type

Every documents or chunks output becomes a read-only **dataset**, however it is stored. A dataset has an identity: the build digest of the node that produced it. Memory and record-view implementations satisfy the same interface; storage chooses only cost.

```ts
// Proposed (strict TypeScript, pkg/ragjs/src/engine/dataset.ts). Names may be refined.
type DatasetId = string; // build digest of the producing node

interface RecordRef { dataset: DatasetId; id: string }

interface Dataset<R> {
  readonly id: DatasetId;
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): R | null;        // frozen or detached record
  values(): IterableIterator<R>;    // deterministic order
  materialize(): R[];               // explicit full read
}

interface ChunkDataset extends Dataset<Chunk> {
  document(id: string): RecordRef | null; // the chunk's source document
  order(documentId: string): RecordRef[]; // reading order within this dataset
}

interface Index {
  readonly dataset: DatasetId;       // the chunk dataset it was built from
  search(query: string, opts: SearchOptions): Hit[];
}

interface Hit { ref: RecordRef; score: number; via: string; reason?: string }
```

Access stays synchronous. The current hosts read local memory or local disk; making every `get` asynchronous to anticipate remote storage would complicate every component. Records handed to callbacks are frozen (memory) or freshly decoded (disk), with a test that nested `meta` cannot be mutated. Datasets expose no `close`, `__records`, `__ix` or codec choice.

### 5.2 Hits resolve against their own dataset

With the enriched corpus from §3.2, the index `ix-text` is built from `enrich`'s output, so its hits carry that dataset:

```js
{ ref: { dataset: 'd41f…(enrich)', id: 'q3-report#risks' }, score: 7.2, via: 'text' }
snapshot.resolve(hit.ref) // → the enriched chunk, cold or warm
```

The global `art.chunks` union, `unionChunks` and the "primary chunk node" heuristic are deleted. Neighbors and reading order come from `order` of the hit's own dataset. `pack`, traces, citations, evaluations and the GUI keep the full `ref`.

Fusing rankings is the case that needs a rule. Fusing two indexes over the same dataset is ordinary: deduplicate exact refs. Fusing an index over `chunk` with one over `enrich` mixes two versions of each chunk. Proposed rule: the compiler knows statically which dataset each hits stream comes from (an index's input chunk node is in the IR), so a fuse over different datasets is a **compile error** unless the fuse names the dataset to resolve against:

```js
.fuse.rrf({ on: 'enrich' }) // all hits re-pointed to dataset of node 'enrich' by id; ids missing there are dropped with a trace note
```

This is a parameter of an ordinary component, not an engine-wide merge.

Document identity must be unambiguous across several sources: a source identity plus vault-relative path, never a title or slug. An index's identity derives from component version, normalized parameters and its input dataset id, which the existing digest scheme already provides.

### 5.3 Callbacks and bake caching

Bake and custom-component contexts receive datasets:

```js
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id));
rag.bake(ctx => ctx.chunks.size); // no full traversal
```

The bake cache key becomes the input dataset ids plus callback source. Because a dataset id is a content-derived digest, an edited note produces a new key even with the same section count. Memory and persisted datasets share one conformance suite: order, missing ids, immutability, `materialize`, `order`, provenance.

## 6. Snapshots, resources and result lifetime

### 6.1 Snapshots and leases

A **snapshot** replaces `art`. It is an immutable map from build node ids to datasets and indexes for one complete, validated build of a plan, with an id and a generation number. A **lease** is a counted claim on a snapshot by a query; while any lease exists the snapshot's resources stay open.

```text
Snapshot A current, leases = 1 (query Q)
refresh builds B privately, validates it, publishes B
A retired, leases = 1        B current, leases = 0
Q finishes, including materializing its result → A leases = 0
A releases its resources; the registry closes those no other snapshot uses
```

Acquiring the current snapshot and incrementing its lease must be one step with no publication in between. All of it runs on the JavaScript owner, so ordinary sequential code suffices there.

```ts
// Proposed sketch
async function query(plan, signals, request) {
  await builds.ensure(plan, request);  // waits only if no snapshot exists yet
  return snapshots.withCurrent(plan.buildKey, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot);
  }); // lease released on success, error, cancellation and packaging failure
}
```

A background refresh never blocks queries; they continue on the last published snapshot. A failed or cancelled refresh publishes nothing. Partial outputs may be kept for resumption, but never become current.

### 6.2 One registry for native resources

The **resource registry** owns every native handle in a runtime, keyed by artifact id. Snapshots that share an unchanged artifact share its entry; the entry closes when no snapshot or builder references it. Loading from the store, delta creation and fresh native builds all adopt their handle through the registry. The no-op closer fallback is removed: a runtime with resource-bearing natives and no registry fails to construct.

```go
// Proposed sketch (Go): the registry lives per runtime.
type Registry interface {
    Adopt(id ResourceID, c io.Closer) (Handle, error)
}

r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(id, r)
if err != nil { _ = r.Close(); return nil, err }
```

A builder adopts into a private scope that commits on publication and releases on failure. Adopting an id that already exists reuses the existing entry and closes the new handle (proposed). Shutdown rejects new work, cancels active operations, waits for them, releases snapshots and closes the registry; a timeout is reported, never treated as clean.

### 6.3 Results own their data

`rag.run` must keep working without a close call. The result therefore contains copies of the records its hits, trace, evidence and neighbor previews need, made before the lease is released. `result.chunk(hit)` and `result.top()` read those copies. The copy is bounded by the result, not the corpus. If a result would exceed a declared budget, the query fails with a limit error rather than returning hits whose records were dropped. Evaluations and full-corpus exports use an explicit scoped snapshot read.

Lookups from the browser carry the snapshot id and the ref. The server resolves them against that snapshot if it is still held, or answers with an explicit stale-generation response. The shell keys its cache by snapshot and ref and clears it when the generation changes.

## 7. Durable store ownership and collection

### 7.1 Authority

Keep the existing payload formats. Extend each artifact's metadata into a **descriptor** that lists its payload and the artifacts it depends on. Add an immutable **snapshot manifest** per published snapshot (the artifact keys per node) and a small atomically replaced **current pointer** per plan build identity. Descriptors and manifests are the inventory. The lineage list becomes a derived accelerator for delta-base lookup that may be rebuilt or deleted at any time.

GC traverses the defined layout instead of guessing from punctuation, which also fixes the dotted-root listing.

### 7.2 One owner per writable namespace

One process owns a writable namespace, holding an OS exclusive lock for its lifetime. It performs builds, publication, leases and online GC. A second process must use another namespace or fail with a clear error. `ragwb store gc` takes the same lock, so it runs only when no server holds the namespace. The browser memory store implements the same logical contract without locking. Multi-process sharing is out of scope.

### 7.3 Publication and crash recovery

```text
1. Write payloads into an owned staging directory.
2. Close writers; validate counts, checksums and codec metadata.
3. Move complete payloads into the namespace.
4. Write their descriptors atomically.
5. Write the snapshot manifest.
6. Atomically replace the current pointer.
7. Update the derived lineage list.
```

A crash before step 6 leaves the old snapshot current; after it, only lineage acceleration may be stale. Durable publication needs file and directory sync around the renames; rename atomicity alone does not establish power-loss durability.

GC marks everything reachable from current pointers, retained snapshots (by a configured retention count), active leases and builder scopes, following descriptor dependencies, then deletes unmarked artifacts. Publication and the mark/sweep decision are serialized by the owner so a candidate cannot be collected between becoming durable and being published. Missing or corrupt metadata aborts deletion. Hard-linked payload files are deleted through directory ownership only. Report logical bytes and reclaimed bytes separately. The retention policy also determines how long excluded notes remain on disk, which is a privacy consideration.

## 8. Operations and shared jobs

### 8.1 One native async bridge

Extract the lifecycle shared by sample, decide and rerank into one Go helper. Provider-specific request conversion, result validation, tool execution and error mapping remain small functions supplied to it.

```go
// Proposed sketch
type Outcome struct {
    Kind  OutcomeKind // Succeeded | Cancelled | DeadlineExceeded | Failed | Shutdown
    Value []byte
    Err   *Failure
}

func RunOperation(services Services, signal JSSignal, spec Spec) *goja.Promise {
    // 1. derive ctx from runtime lifetime; attach abort listener
    // 2. run spec.Execute(ctx, emit) on a goroutine
    // 3. outcome := spec.Classify(value, err, context.Cause(ctx))  -- before any cancel()
    // 4. post exactly once to the owner: settle promise from outcome, detach listener
    // 5. cancel ctx and release resources even if the post fails (runtime closed)
}
```

Race rule: a terminal result classified before a cancellation is observed stays terminal; otherwise cancellation wins. Runtime disposal may prevent the promise callback from running but never prevents cleanup. Tool calls during sampling use a request/reply hook on the owner; worker goroutines never touch Goja values.

### 8.2 Shared build jobs with waiters

`buildShared` becomes a small registry of **jobs** keyed by the full normalized build identity. Each job has its own context linked to engine shutdown and a set of waiters.

```text
A waits ─┐
         ├─> job J ─> build candidate ─> validate ─> publish
B waits ─┘
A cancels: A detaches; J continues for B.
B cancels: no waiters left → J is cancelled (unless a refresh owns it).
New waiter during cancellation: waits for J to settle, then starts a new job.
Engine close: cancel J, settle all waiters, join cleanup.
```

Progress belongs to the job and is fanned out to each waiter with job and operation ids. A slow or closed subscriber cannot block the job.

## 9. One bounded HTTP transport

Both `handleRun` and `handleVerb` call one response helper. Domain code emits typed events and returns a terminal outcome; the helper handles admission, JSON or SSE framing and the request lifetime.

The SSE writer is the only goroutine that writes to the response. Before every write and flush, including headers, heartbeats and the terminal event, it sets a deadline with `http.ResponseController.SetWriteDeadline` and uses the error-returning `Flush`. If the controller cannot set a deadline, that is a setup error. On any write or flush error, or queue overflow, it stops, cancels the request and returns through a bounded join. A failed connection cannot be promised a terminal event. Coalesced text and heartbeat buffers are bounded in bytes as well as entries. Per-write deadlines allow a long healthy stream.

Direct queries and verbs share admission and the terminal error vocabulary. The acceptance test uses a real socket with a client that stops reading, and checks that the slot becomes available again.

## 10. Graph references and export

### 10.1 One reference representation

The DSL can stay concise. The compiler normalizes every structural reference into one form:

```js
// DSL
when: { decision: 'route.needs', min: 0.5 }
retrieve('text')
// Canonical IR
when: { source: { kind: 'decision', node: 'route', output: 'needs' }, minimum: 0.5 }
p: { index: { kind: 'artifact', node: 'ix-text' } }
```

Every component declares its references through a shared schema: inputs, artifact uses, decisions and pinned outputs. `references(node)` lists them and `rewriteReferences(node, mapping)` rewrites them. The compiler validates existence, types, phases and cycles from this list; `rename` checks uniqueness, rewrites and validates once; `remove` must specify reject, cascade or replace (default reject).

Lambdas cannot be renamed safely by string replacement. Graph-aware lambdas receive named bindings declared in the component options; transformations that would require rewriting an undeclared reference inside a lambda are rejected.

### 10.2 Capability-based export

Components declare capabilities (local text index, external model, host filesystem). The exporter keeps components a browser page supports and applies documented rules to the rest. For the lexical export, removing a decision-only classifier makes nodes conditioned on it unconditional. The exporter validates the transformed graph before serializing it and bundles a normalized portable dataset rather than `engine.files()`.

## 11. Module boundaries and what gets deleted

Split the engine further only as each contract lands: graph, datasets, snapshots/resources, operations, executor, with a thin `rag` facade. Browser and Goja keep loading the same generated bundle.

| Boundary | Shared mechanism | Stays specialized |
|---|---|---|
| Memory and disk records | Dataset contract + conformance tests | Encoding, shards |
| Fresh, loaded, delta outputs | Registry adoption | Build/delta algorithms |
| Sample, decide, rerank | Async lifecycle, outcome | Requests, errors, tools |
| Direct query, verbs | Admission, events, JSON/SSE | Parameter parsing |
| Compiler, editor, exporter | Reference traversal | Transformation policy |
| Timer, manual, post-sync refresh | Build/refresh operation | Scheduling |

Deleted: `unionChunks`, `isView`/`asArray` conversions, record-view consumer tests, `Engine.gc` and its `retired` list, direct handle closing, `buildShared` first-waiter options, duplicated HTTP pumps, the no-op closer fallback. Memory algorithms used by the browser stay.

## 12. Migration

`.from().parse().chunk().index()` and `.retrieve()…` scripts remain recognizable. `rag.run` and `result.top` stay. Bake/custom callbacks move to datasets, raw hit consumers to refs, inspection endpoints to snapshot-aware lookups. GUI, traces, evaluations, sweeps, saved plans, presets and export migrate together. `result.chunk('id')` with a bare string is removed. Serialized IR and artifact namespace versions are bumped; old caches are disposable but not deleted silently.

## 13. Implementation sequence

1. **Characterize.** Deterministic regressions for all twelve findings on the current code, recording which fail. Include cold, stored and delta paths. If the stack will be used meanwhile, fix the SSE P1 early inside the future shared transport.
2. **Datasets and provenance.** Dataset contract, refs on hits, index→dataset binding, bake migration; delete unions.
3. **Snapshots and resources.** Leases, registry, transactional publication, detached results; migrate serve run/pack, evaluations, lookups; delete engine GC and no-op closers.
4. **Store inventory and GC.** Descriptors, manifests, lock, mark/sweep; lineage derived; fix `List`.
5. **Operations and shared jobs.** One async runner for sample/decide/rerank; job registry. Fix model fallback separately.
6. **Transport.** One bounded writer for both endpoints.
7. **Graph references and export.** Canonical references, edits, capability projection.
8. **Cleanup and verification.** Remove dead code, update docs, measure.

Run focused tests during each phase and full checks at integration boundaries:

```sh
pnpm install --frozen-lockfile && pnpm build && pnpm check
go test ./pkg/ragjs ./pkg/runner ./pkg/server ./pkg/artifacts ./pkg/natives ./web
```

## 14. Tests and acceptance

| Contract | Sequence and assertion |
|---|---|
| Dataset consistency | Same records and resolution via memory, stored and delta datasets |
| Bake inputs | One API cold and warm; edited text with equal counts invalidates |
| Provenance | Two datasets sharing ids resolve their own bytes through retrieve, fuse, pack and display |
| Leases | Pause query in a provider; publish two snapshots; resume; result built from old snapshot |
| Resource lifecycle | Load, delta, fresh all adopt; errors and shutdown close each handle exactly once |
| Result ownership | Result readable after lease release and handle close |
| Inventory | 40+ versions, retain 3, GC leaves the expected set |
| Locking | Second owner and standalone GC refused while serving |
| Crash recovery | Fail between each publication step; reopen to complete old or new |
| Shared jobs | Cancel A while B waits; B succeeds; cancel all releases; no join to doomed job |
| Async outcome | Failure stays failure after cleanup; cancellation stays cancellation |
| SSE bounds | Real non-reading socket terminates within bounds; slots recover |
| Graph edits | Rename/remove/projection keep references or reject |
| Export | Classified plan exports a working unconditional lexical page |
| Small fixes | Dotted root lists; empty enabled list selects deterministic model |

Use synthetic corpora, provider fakes and hold/release barriers; no sleeps. Count closers and check file descriptors. Measure build, warm start, query latency, refresh, memory, descriptors and store growth before and after.

Done means: all twelve findings have passing regressions or a reviewed disposition, the replaced mechanisms are deleted, the same example works in browser and Goja, and documentation matches the API.

## 15. Risks and open decisions

- **Result lifetime** is the biggest risk: releasing a lease before copying records recreates use-after-close.
- **Shared resources**: a retired snapshot must not close handles another snapshot shares.
- **Cross-process GC** is safe only with the namespace lock.
- **Ref migration** touches every trace/evidence/GUI/evaluation consumer.

Open decisions: fusion policy default; result budget values; stale-generation response; retention count; removal default; registry duplicate policy; whether refresh-owned jobs outlive waiters.

Out of scope: distributed indexing, remote datasets, multi-process stores, full Obsidian rendering, authentication.
