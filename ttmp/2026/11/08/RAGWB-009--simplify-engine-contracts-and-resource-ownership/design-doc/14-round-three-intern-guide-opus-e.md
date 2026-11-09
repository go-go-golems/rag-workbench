---
Title: Round three intern guide Opus E
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
LastUpdated: 2026-11-08T22:54:11.074053241-05:00
WhatFor: ""
WhenToUse: ""
---

# RAGWB-009: Consistent datasets, snapshots, operations and graph references

An analysis, design and implementation guide for engineers new to rag-workbench.

## 1. What this guide is for

rag-workbench lets someone describe a retrieval pipeline over a folder of Markdown notes in a few lines of JavaScript, then run it from the CLI, from a long-lived HTTP server with a browser GUI, or entirely inside a browser page. A user types a question; the pipeline finds the most relevant sections of the notes and, optionally, asks a language model to answer from them.

The pipeline engine works, but several of its internal boundaries are ambiguous. The same collection may be an in-memory array in one run and a disk-backed view in the next, and the two behave differently. A query may keep using index handles after a background rebuild has closed them. A build started by one HTTP request is cancelled when that request goes away, even if another request is waiting for it. Review of the open pull-request stack found twelve concrete defects that follow from these ambiguities.

RAGWB-009 replaces those boundaries with a few explicit contracts. The guide explains the current system first (§2), then each problem and its cause (§3), then the proposed contracts (§4–10), the TypeScript and module conventions (§11), migration (§12), the implementation order and tests (§13–14), and the decisions still open (§15).

**Status.** Everything in §4–12 is a proposal; none of it is implemented. Code sketches show contracts, not finished APIs. Source references are to the working tree at runtime baseline `c267f91`, which uses the RAGWB-010 layout: authored runtime modules under `pkg/ragjs/src/`, generated into the committed `pkg/ragjs/assets/rag.js`. The descriptions of current behavior in §2–3 come from reading that source. They have not been reproduced by running regressions; creating and running those regressions is phase 1 of the plan (§13).

Ground rules:

- **Compatibility may break.** There is no deployed API or stored data to preserve. Callers, tests and stored formats are updated together. No adapters, dual code paths or legacy readers.
- **Simplicity is the goal.** Each new concept must remove an existing responsibility or duplicated mechanism. This is not a workflow framework, plugin system, job scheduler or dependency-injection container.
- **What stays:** the concise JavaScript corpus/plan builders, one JavaScript engine shared by browser and Go hosts, the Markdown parser, section chunking, Bleve and terms indexes, the incremental (delta) build algorithms, provider implementations with their protocol errors, and the existing evaluation cases and meaningful integration tests.

## 2. How the system works today

### 2.1 A pipeline in two halves

A pipeline has two halves with different lifetimes.

The **corpus** describes work that only needs repeating when the notes change: read files, parse Markdown into documents, split documents into **chunks** (sections of a note small enough to retrieve and show), optionally enrich chunks, and build search indexes over them.

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
| `live` | Per-query work | `retrieve`, `fuse.rrf`, `filter`, `limit`, `rerank.model`, `pack`, `answer`, `classify` |
| `signal` | Query inputs | `q`, `k` |

The example compiles to roughly `source → parse → chunk → ix-text` (build) and `q → text → limit` (live). The live node `text` is a `retrieve` whose parameter `index: 'text'` refers to the build node `ix-text`.

The compiler is `finalize` in `pkg/ragjs/src/graph/graph.js`. It checks ids, port types and cycles, and records three kinds of edge: `data` (a node input), `artifact` (a live node that *uses* a build output, as `retrieve` uses an index), and `decision` (a node whose `when` condition reads a `classify` result; see §2.6).

### 2.2 Who runs the JavaScript

The engine is one JavaScript bundle. `pnpm build` generates `pkg/ragjs/assets/rag.js` from `pkg/ragjs/src/`. That one bundle runs in three hosts:

- **Goja**, a JavaScript interpreter written in Go, inside the `ragwb` CLI and server. `pkg/ragjs/module.go` loads the bundle and installs **natives**. These are Go implementations of components (Markdown parsing, chunking, Bleve BM25 indexes) and of host services (LLM sampling, decisions, reranking, a disk artifact store).
- **Browsers**, which run the same components with memory-backed JavaScript implementations.
- **Exported HTML pages**: standalone lexical-search pages produced by `exportHtml`.

In Goja, all JavaScript for a runtime runs on one **owner goroutine**. Go code that needs to touch a JavaScript value posts a function to the owner. Long Go work, such as an LLM call, runs on its own goroutine and then posts its result back to settle a JavaScript promise. While a query awaits such a promise, the owner is free to run other JavaScript, including another query or a rebuild.

Backends may rank results differently. They must not differ in how collections behave or in who owns which resource.

### 2.3 Building: digests, the memo and the artifact store

`Engine.build` (`pkg/ragjs/src/engine/engine.js`) walks the build nodes in order. For each node it computes a **digest**: a hash of the component type, its parameters and its input digests. Source nodes also contribute a hash of their inputs. `source.files` hashes the file text; `source.dir` hashes each file's relative path, size and modification time (`pkg/natives/sourcedir.go`). An equal digest means an equal output. The engine therefore keeps a **memo**, a map from digest to built value, and skips any node whose digest is already in it.

In Goja the engine also has an **artifact store** (`pkg/artifacts/store.go`), a directory where build outputs are saved under a key derived from the digest. On a warm start the engine finds a stored output and installs a `Lazy` placeholder that loads it on first read. How a stored output comes back depends on what produced it:

- Documents and chunks produced by Go natives are saved in the `records2` format (64 shards, read one record at a time). Loading one returns a **record view** (`recordsViewJS` in `pkg/natives/persist.go`). The view is a JavaScript object with `get(id)`, `has(id)`, `size`, `order(docId)` and iteration, backed by an open Go reader.
- Bleve indexes are opened in place. They also hold Go file handles.
- Plain JavaScript values, such as the array an `enrich` component returns, are saved as JSON and load back as plain arrays.

Rebuilding everything when a source changes is slow. The **delta builder** (`Engine.tryDelta`) instead continues from a previous stored version of the same node. To find that version, each build node also gets a **lineage key**. A lineage key is like a digest but ignores file contents and statistics, so it identifies "this node of this pipeline" across edits. The store keeps a list of recent versions per lineage (`pkg/artifacts/lineage.go`). The delta builder searches that list for a version computed from exactly the inputs the current inputs changed from. It then asks the component to apply only the change, for example `parseDelta` or `chunkDelta` in `pkg/natives/markdown_native.go`. A delta output is a new record view even before it is saved.

### 2.4 Querying: the artifacts object and the session

A query needs the build outputs gathered in one place. `Engine.artifacts(ir)` returns that object, called **`art`** in the code:

```js
art = {
  key,        // concatenated digests: identifies this set of outputs
  byId,       // node id → output
  chunks,     // ONE map of every chunk from every chunk-producing node, by chunk id
  order,      // docId → chunk ids in reading order, from one "primary" chunk node
  indexes,    // index name → index
  docs, links, synonyms,
}
```

`artifacts(ir)` returns `null` if any build node's current digest is missing from the memo. This is also how the engine notices that sources changed.

`Session.run` (`pkg/ragjs/src/engine/session.js`) obtains `art`, building first if it is `null`, and then runs the live nodes in order. Components reach data through a context object. `ctx.index('text').search(q, k)` returns hits shaped `{ id, score }`, where `id` is a chunk id. `ctx.chunk(id)` looks that id up in `art.chunks`. A node may `await` an LLM. The returned result carries non-enumerable `result.chunk(id)` and `result.top(n)` functions that keep reading from the same `art`.

### 2.5 Serving

`ragwb serve` (`pkg/runner/serve.go`) opens each app in one long-lived Goja runtime. It builds the plan, warms `art`, creates a session and exposes four JavaScript functions to Go through the `serveHarness` script:

- `run` executes a query. It then **packs** the response: it copies the chunk bodies the result mentions, plus the reading-order neighbors (previous and next chunk) of the top 40 hits. The GUI reads chunks synchronously while rendering, so they travel with the response.
- `chunk(id)` and `doc(id)` look up a chunk or document that the GUI did not receive.
- `refresh` rebuilds incrementally if the sources changed, then releases outputs that are no longer current.

With `--watch`, `watchApp` (`cmd/ragwb/cmds/serve.go`) calls `refresh` on a timer. Two HTTP endpoints run queries: `POST /api/rag/{app}/run` (`pkg/server/rag.go`) and the application-defined **verbs** (`pkg/server/verbs.go`). Both return JSON or stream **Server-Sent Events** (SSE). The stream carries engine events, such as node progress and streamed LLM text, followed by one terminal `result` or `error` event. The browser shell, `web/app-shell/shell.js`, caches every chunk it receives by chunk id for the life of the page.

### 2.6 Decisions and conditional nodes

A `classify` node asks a decision model one or more questions and outputs the answers. Another live node can run conditionally. `when: { decision: 'route.needs', min: 0.5 }` means "run only if question `needs` of node `route` scored at least 0.5". The reference to `route` lives inside the string `'route.needs'`. A `when` can also be a lambda that calls `ctx.decision('route')`.

## 3. What goes wrong, and why

### 3.1 The twelve review findings

Review of PRs #2–#7 left twelve unresolved findings. The priorities are the reviewers' labels. Source inspection confirms each mechanism below. Phase 1 of the plan (§13) turns each one into a failing regression before anything is fixed.

| # | PR / prio | Reported failure | Cause |
|---|---|---|---|
| 1 | #2 / P2 | Duplicate chunk ids resolve to a different version when stored than when cold | §3.2 |
| 2 | #2 / P2 | A bake callback receives a record view instead of an array | §3.2 |
| 3 | #2 / P2 | `store list` skips a store root whose basename contains a dot | §3.7, local |
| 4 | #3 / P2 | Two refreshes close handles that an awaiting query still uses | §3.3 |
| 5 | #3 / P2 | Versions beyond the 32-entry lineage list are never collected | §3.4 |
| 6 | #3 / P2 | Delta-created record readers miss runtime cleanup | §3.3 |
| 7 | #4 / P1 | An SSE client that stops reading holds a request slot forever | §3.6 |
| 8 | #4 / P2 | Cancelling the first waiter cancels another waiter's build | §3.5 |
| 9 | #5 / P2 | Deferred context cleanup turns a failure into a cancellation | §3.5 |
| 10 | #6 / P2 | An empty enabled-model list prevents a valid fallback | §3.7, local |
| 11 | #7 / P2 | Lexical export keeps references to removed decisions | §3.7 |
| 12 | #7 / P2 | Rename does not update decision references | §3.7 |

Review sources: PRs [#2](https://github.com/go-go-golems/rag-workbench/pull/2), [#3](https://github.com/go-go-golems/rag-workbench/pull/3), [#4](https://github.com/go-go-golems/rag-workbench/pull/4), [#5](https://github.com/go-go-golems/rag-workbench/pull/5), [#6](https://github.com/go-go-golems/rag-workbench/pull/6), [#7](https://github.com/go-go-golems/rag-workbench/pull/7).

Findings 3 and 10 are local bugs with local fixes. The other ten come from six shared causes. Fixing those causes, instead of patching each symptom, is the point of this ticket.

### 3.2 A collection's behavior depends on where it was stored

`Engine.artifacts` merges the output of every chunk-producing build node into the single `art.chunks` map. When two nodes produce chunks with the same id, the winner depends on storage form:

- If both outputs are arrays, `chunks.set(c.id, c)` runs in node order, so the **later** node wins.
- If any output is a record view, `unionChunks` (`pkg/ragjs/src/engine/records.js`) combines them. Its `get` checks arrays first and then views in node order, so **arrays beat views**, and the **first** view beats later views.

Take two chunk nodes, A (earlier in the IR) and B (later), that share a chunk id:

| A is | B is | Winner |
|---|---|---|
| array | array | B |
| view | view | A |
| array | view | A |
| view | array | B |

A cold build produces arrays. A restart that loads from the store produces views for Go-native chunkers. A delta rebuild in a running server also produces views. The same query can therefore show different text before and after a restart or refresh (finding 1). The common `chunk.sections` → `enrich` pipeline happens to agree today, because `enrich` returns a JSON-persisted array that wins either way. That agreement comes from which codec each component uses, not from any rule.

The deeper problem is that a hit carries only a chunk `id`. Nothing records which node's output the index actually searched. If `ix-text` indexes the enriched chunks, its hit for `q3-report#risks` means the enriched record. The global map may still hand back a different record with the same id. Reading order has the same weakness. `art.order` comes from a "primary" chunk node, chosen as the first chunk node whose type is not `enrich` or `enrich.llm`; `enrich.decide` is not excluded.

**Bakes** show the array/view split directly. A bake, `rag.bake(ctx => …)`, computes a parameter value from the corpus at build time, such as a synonym table. `Engine.resolveBakes` passes it the first docs output and the first chunks output exactly as they are: an array when cold, a record view when warm. A callback calling `ctx.docs.map(...)` works cold and throws warm, because a record view has no `map` (finding 2). Build components, by contrast, always receive arrays through `asArray` in `buildCtx`, so a caller cannot predict which form it will get. The bake cache key is `hash([docs.map(d => d.id + d.sections.length), chunks.length])`. Editing a note's text without changing its section count therefore reuses a stale bake.

Records are also mutable. Memory arrays share their objects, and a record view caches up to 4096 decoded objects. A callback that mutates a record changes what later readers see.

### 3.3 Nobody owns a handle for the duration of a query

Record views and Bleve indexes hold Go file handles. Two independent mechanisms close them:

1. **Runtime closers.** When `Persist.InstallEnv` (`pkg/natives/persist.go`) opens a stored output in `load`, it registers `r.Close` to run when the runtime closes. If the host supplied no closer hook, it silently substitutes a no-op (lines 47–50). `module.go` does the same.
2. **Engine GC.** `Engine.gc` drops memo entries the current plans no longer need and puts their values on a `retired` list. It closes the values that were already retired at the *previous* call. The serve harness calls it (via `collect()`) after a refresh, and after a query whenever `art.key` has changed.

A superseded handle therefore survives exactly one more GC. Follow one query through two refreshes:

```text
t0  query Q starts with art A (index handle H) and awaits an LLM answer
t1  refresh (owner is free while Q awaits): sources changed → build B → gc retires A's values
t2  refresh: sources changed again → build C → gc closes the retired values, including H
t3  Q resumes; a later node calls H or reads A.chunks → use of a closed reader
```

That is finding 4. Delta builds add a third path. `parseDelta`/`chunkDelta` create a new `Records2` reader, and the JavaScript wraps it with `ctx.store.view`, which never registers a runtime closer (finding 6). The reader is closed only if the engine GC later happens to retire it. `Records2.Close` is idempotent, so the risk is leaked handles, not double closes.

Even with perfect handle lifetime, response packing is wrong. `__serve.run` packs the response after the query from `art()`, which is the **current** set of outputs and may be a newer one than the query searched. Chunk ids from one build are then looked up in another. A lifetime fix alone is therefore insufficient: building the response must happen inside the same lifetime as the query. The browser shell repeats the problem over a longer span, because it caches chunks by bare id forever.

### 3.4 The lineage list doubles as the store's inventory

`LineageRecord` keeps the newest `LineageKeep = 32` entries per lineage. `Store.GC(keep)` walks only the lineage files and deletes listed versions beyond the newest `keep`. A version that has fallen off its 32-entry list is listed nowhere, so GC never deletes it (finding 5). A search accelerator has become the authority on what exists.

GC also takes no lock. `ragwb store gc` runs as a separate process and cannot see which versions a running server has open. Its help text asks the operator to "keep at least 2". Publication is not crash-safe as a unit. Payload and metadata are each renamed into place, but nothing groups the outputs of one build. `writeAtomic` syncs neither files nor directories.

### 3.5 Asynchronous work has unclear owners and outcomes

**Shared builds.** When a query finds no current `art`, it calls `Engine.buildShared`, which deduplicates concurrent builds by their digest set. The first caller's options become the build's options: its abort signal, its progress callback and its event sink. A second caller gets only an event `{type: 'build', waiting: true}` and the same promise. If the first HTTP client disconnects, its signal aborts the build the second client is waiting for (finding 8). Progress events go only to the first client. The key also ignores options such as `force` and `calls`, so builds that mean different things can be merged.

**Native async bridges.** Sampling (`pkg/ragjs/sample.go`), decisions (`pkg/ragjs/decide.go`) and reranking (`pkg/natives/rerank.go`) each implement the same lifecycle by hand. Each creates a promise, listens to the abort signal, derives a context, runs the provider on a goroutine and posts the settlement to the owner. They differ in the details. `rerankAsync` posts a closure that checks `callCtx.Err()` only when it runs on the owner. By then the goroutine's `defer cancel()` may already have run, so a provider failure can be reported as `cancelled` (finding 9). `decide` maps a deadline to `cancelled`. None of the three removes its abort listener.

### 3.6 Streaming has no write bound

`sseWriter.pump` (`pkg/server/sse.go`) ignores write errors and sets no write deadline. The server is created with `ReadHeaderTimeout` only, and no `WriteTimeout` (`cmd/ragwb/cmds/serve.go:130`). If a client stops reading, the TCP buffer fills and the pump's write blocks indefinitely. This is the sequence:

1. The pump blocks in a write. The event queue (256 entries) fills.
2. `streamSink.Emit` waits 100 ms, marks the client slow and cancels the request.
3. The run finishes. The handler closes the sink and waits for the pump to exit (`<-done`).
4. The pump is still blocked in the write, so the handler never returns.

`handleVerb` releases its in-flight slot in a `defer`, so the slot is never released (finding 7). After eight such clients (the default limit), every verb request gets 503. `handleRun` has the same wait and no admission control at all. The two handlers duplicate the sink, pump and terminal-event code with different error codes.

### 3.7 Graph references are found in four places; two local bugs

The compiler discovers references through four separate mechanisms:

- `n.in`, the node's inputs.
- `def.uses(p)`, which turns `p.index: 'text'` into a use of node `ix-text`.
- `whenRefs`, which splits the `'route.needs'` string and also regex-scans lambda source for `.decision('route')`.
- `def.reads` plus a scan of lambda source for the signals it reads.

Graph edits use none of these. `rename` in `applyOp` rewrites inputs and outputs only. Renaming `route` leaves `when: {decision: 'route.needs'}` dangling (finding 12), and renaming `ix-text` leaves `retrieve`'s `p.index` pointing at nothing. `remove` checks `uses` but not `when` readers.

`exportHtml` (`pkg/ragjs/src/export/html.js`) removes every submit-triggered live node except answers, then removes answers and contexts. A `classify` node is submit-triggered, so it is removed, and any node with a `when` on it then fails to compile (finding 11). The export also embeds `engine.files()` as its corpus, which is empty for a server-side `source.dir`.

The two local bugs:

- `artifacts.List` avoids descending into payload directories (`<key>.records2/`) by skipping any directory whose name contains a dot. The check also applies to the root it was asked to walk, so `List("/data/store.v1")` returns nothing (finding 3).
- In `pkg/llm/gepp/backend.go`, `preferred` treats an empty `EnabledModels` as "everything enabled" when it checks the default model. When the default is unavailable, though, it falls back by iterating `EnabledModels`. That list is empty, so it finds nothing (finding 10).

## 4. Target design at a glance

### 4.1 Shape

```text
DSL, GUI, CLI, HTTP entry points
          │
Graph: compile, references, edits, projections                 (§10)
          │
Application operations: build / refresh / query / lookup
     │                                   │
Build jobs (shared, with waiters) (§8.2)  Query execution
     │                                   │
     └────────── Snapshots (leased) ─────┘                       (§6)
                     │
         Datasets and indexes with identity                      (§5)
                     │
     Resource registry (§6.2) + artifact store (§7)

Provider bridges (§8.1) → typed events → one JSON/SSE transport (§9)
```

Dependencies point downward. A shared mechanism exists only where two existing consumers need the same contract: memory and disk records; fresh, loaded and delta outputs; sample, decide and rerank; direct queries and verbs; compiler, editor and exporter.

### 4.2 Invariants

These are the rules a reviewer checks every change against.

1. A record reference names a dataset version and a record id (§5).
2. Loading a stored output changes cost, not collection behavior (§5).
3. A query reads exactly one published snapshot, including while building its response (§6.1, §6.3).
4. Every opened native resource has exactly one registry owner; it closes only after its last user releases it (§6.2).
5. A bounded search history is never the store's inventory (§7).
6. Cancelling one waiter never cancels a shared job another waiter needs (§8.2).
7. Every asynchronous operation has one immutable terminal outcome (§8.1).
8. Every network write and flush has a bounded wait; a transport failure ends that transport (§9).
9. Every structural graph reference is visible to one validation and rewrite API (§10).
10. Ordinary searches need no close or release call from application code (§6.3).
11. Each new mechanism replaces the old one in the same phase; no parallel legacy path remains (§13).

JavaScript cannot enforce ownership at compile time. Private handles, single constructors, checked references and sequence tests make misuse hard to express. Trusted native code can still have bugs and must be tested.

## 5. Datasets and provenance

### 5.1 One dataset type

Every documents or chunks output becomes a read-only **dataset**, regardless of storage. Its identity, the `DatasetId`, is the build digest of the node that produced it. That id changes whenever any upstream input changes: parameters, the source of a `rag.define`d component, file text for `source.files`, or file path/size/mtime for `source.dir`. (Native component versions are covered separately, by the artifact namespace.) Memory and record-view implementations satisfy the same interface. Storage chooses only cost.

```ts
// Proposed, strict TypeScript (for example pkg/ragjs/src/engine/dataset.ts).
type DatasetId = string;                      // build digest of the producing node
interface RecordRef { dataset: DatasetId; id: string }

interface Dataset<R> {
  readonly id: DatasetId;
  readonly node: string;                      // producing node id, for messages and binding
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): Readonly<R> | null;
  values(): IterableIterator<Readonly<R>>;    // deterministic order
  materialize(): Readonly<R>[];               // explicit full read
}

interface ChunkDataset extends Dataset<Chunk> {
  document(id: string): RecordRef | null;     // the chunk's source document
  order(documentId: string): RecordRef[];     // reading order within THIS dataset
}

interface Index {
  readonly dataset: DatasetId;                // the chunk dataset it was built from
  search(query: string, opts: SearchOptions): Hit[];
}

interface Hit { ref: RecordRef; score: number; via: string; reason?: string }
```

Access stays synchronous. The current hosts read local memory or local disk, and making every `get` asynchronous in anticipation of remote storage would complicate every component. If remote datasets become a requirement, they need a separately designed bulk interface.

Records returned by either implementation are frozen, including nested `meta`. The record-view cache may keep returning the same object only because it is frozen. Datasets expose no `close`, `__records`, `__ix` or codec choice.

### 5.2 Hits resolve against their own dataset

Return to the reports corpus with an enrichment step:

```js
.chunk(rag.sections({ maxTokens: 450 }))   // node 'chunk'  → chunks 'q3-report#risks', …
.enrich('summary', c => summarize(c))       // node 'enrich' → same ids plus a summary field
.index('text', rag.bm25())                  // node 'ix-text', built from 'enrich'
```

`ix-text` is built from the `enrich` dataset, so its hits carry that dataset:

```js
{ ref: { dataset: '9c2e…', id: 'q3-report#risks' }, score: 7.2, via: 'text' }
snapshot.resolve(hit.ref)  // → the enriched record: cold, warm or after a delta
```

The global `art.chunks` union, `unionChunks` and the "primary chunk node" heuristic are deleted. Neighbors and reading order come from `order()` on the hit's own dataset. `pack`, traces, citations, evaluations and the GUI carry the full `ref`.

**Custom hit producers.** A `retrieve` lambda or a `rag.define`d component returning hits cannot be traced statically to a dataset. Such code builds refs explicitly with `ctx.ref(nodeId, id)`, which resolves `nodeId` to that node's dataset in the current snapshot. A bare string id is rejected rather than guessed. This is the deliberate reference constructor for low-level callers.

**Fusion.** Fusing two rankings over the same dataset is ordinary: deduplicate exact refs. Fusing an index over `chunk` with one over `enrich` mixes two versions of each chunk. The compiler knows which dataset each built-in hits stream comes from, because an index's input node is in the IR. Proposed rule: a fuse over different datasets is a **compile error** unless the fuse names one dataset to resolve all hits against:

```js
.fuse.rrf({ on: 'enrich' })
// each input hit is re-pointed to { dataset: <enrich>, id }; an id absent from
// 'enrich' is a run error naming the hit, not a silently dropped record
```

This is a parameter of an ordinary component, not an engine-wide merge mechanism. (Status: proposed. See §15, D1.)

**Document identity** must be unambiguous across several sources: a source identity plus the vault-relative path, never a title or truncated slug. An index's identity already derives from its component version, normalized parameters and input dataset id through the digest scheme.

### 5.3 Bakes and callbacks

Bake and custom-component contexts receive datasets:

```js
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id));
rag.bake(ctx => ctx.chunks.size);            // no full traversal needed
rag.bake(ctx => ctx.dataset('chunk').size);  // an explicit node's dataset
```

Today a bake receives "the first docs node and the first chunks node", which is the same kind of implicit-primary heuristic the design removes. Proposed: `ctx.docs` is the corpus's parsed documents dataset, and `ctx.chunks` is the corpus's final chunk dataset (the one `.index()` consumes). `ctx.dataset(nodeId)` chooses explicitly. (Status: proposed. See §15, D2.)

The bake cache key becomes the ids of the datasets the bake can read, plus the callback source. Because dataset ids change with any upstream input change, an edited note invalidates the bake even when section and chunk counts are unchanged.

Memory and persisted datasets share one conformance suite covering iteration order, missing ids, frozen records, `materialize`, `order`, `document` and provenance. All in-repository callbacks are updated in the same change.

## 6. Snapshots, resources and result lifetime

### 6.1 Snapshots and leases

A **snapshot** replaces `art`. It is an immutable record of one complete, validated build of a plan's corpus: for each build node, its dataset or index. It is keyed by a **build identity**, the set of build-node digests plus the build options that change meaning (`force`, `full`, `calls`, `persist`). Plans that share the same build nodes share snapshots.

```js
// Illustrative snapshot record
{
  id: 'snap-5d1a…',              // hash of the artifact ids below
  build: 'b-77f0…',              // build identity
  outputs: {
    source: 'a01…', parse: 'b02…', chunk: 'c03…', enrich: '9c2e…', 'ix-text': 'e05…'
  },
}
```

For each build identity, one snapshot is **current**. A **lease** is a counted claim on a snapshot by one operation, such as a query or a lookup. While a snapshot has leases, or is retained by policy, its resources stay open.

```text
Snapshot A current, leases = 1 (query Q awaiting an LLM)
refresh builds B privately, validates it, publishes B
A retired, leases = 1          B current, leases = 0
Q finishes, including materializing its result → A leases = 0
A's registry references are released; entries no other snapshot uses close
```

Acquiring the current snapshot and incrementing its lease is one step; no publication can happen in between. Both run on the JavaScript owner, so ordinary sequential code is enough there.

```ts
// Proposed sketch
async function query(plan, signals, request): Promise<Result> {
  await builds.ensure(plan, request);           // see "freshness" below
  return snapshots.withCurrent(plan.buildIdentity, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot);
  }); // lease released on success, error, cancellation and packaging failure
}
```

**When does "current" change?** Only when a build job publishes. This is a deliberate change. Today a query that finds `art` null (because sources changed) rebuilds and waits. Under the proposal, the host chooses a freshness policy (§15, D3):

- **One-shot library use** (`rag.run` in a browser page or CLI script) waits for a fresh build, so a user who just edited a file sees it.
- **A served app** answers from the last good snapshot. If it notices changed sources, it starts or joins a background refresh job and does not wait. `builds.ensure` blocks only when no snapshot exists yet.

A failed or cancelled build publishes nothing. Builds write a private candidate and publish only after complete validation. Partial outputs may be kept internally for resumption but never become current.

### 6.2 One registry for native resources

The **resource registry** owns every native handle in a Goja runtime. It is a Go object, one per runtime, holding refcounted entries keyed by artifact id. JavaScript sees only opaque handle objects, and only the snapshot and builder modules call `retain` and `release` on them. Registry operations normally run on the owner goroutine. Shutdown runs from another goroutine, so the registry takes a mutex. Browser datasets hold no native resources and need no registry entries.

All three ways of obtaining a handle adopt it the same way: loading from the store, creating a delta output, and building fresh. The no-op closer fallback is removed. A runtime that installs resource-bearing natives without a registry fails to construct.

```go
// Proposed sketch
type ResourceID string

type Registry interface {
    Adopt(id ResourceID, c io.Closer) (Handle, error) // refcount 1, owned by the caller's scope
}

// Every opener adopts immediately, or closes on failure.
r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(ResourceID(key), r)
if err != nil { _ = r.Close(); return nil, err }
return newChunkDataset(h), nil
```

A builder adopts into a private **scope**. On publication the scope's references transfer to the new snapshot. On failure the scope releases them all. A base reader opened only to compute a delta is adopted into the builder scope and released when the scope commits, unless the snapshot also uses it.

Proposed duplicate rule: adopting an id that already has an entry increments that entry and closes the redundant new handle, so one underlying file never has two owners (§15, D4).

Shutdown proceeds in order. It rejects new work, cancels active operations and waits for them to settle. Then it releases snapshots and closes the registry. If waiting times out, shutdown reports the timeout. It never reports success while Go workers might still touch closed resources.

### 6.3 Results own their data

A plain `rag.run` must remain usable after it returns, with no close call. The result therefore contains **copies** of the records it needs, made before the lease is released. Those are the records referenced by its hits, trace entries, packed evidence and neighbor previews (the previous/next chunk of the top hits, which today means 40). `result.chunk(hit)` and `result.top()` read those copies.

```js
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;  // takes a hit or ref, not a bare id
// nothing to close: the result owns detached data
```

The copy is bounded by the result, not by the corpus. If a result would exceed a declared budget, the query fails with a clear limit error. It never returns hits whose records were silently left out. Evaluations, sweeps and full-corpus exports iterate through an explicit scoped read (`snapshots.withCurrent(…, s => …)`) instead of a result.

The served run response follows the same rule. Packing happens inside the lease, and records are keyed by dataset and then id:

```json
{
  "snapshot": "snap-5d1a…",
  "result": { "hits": [{ "ref": { "dataset": "9c2e…", "id": "q3-report#risks" }, "score": 7.2 }] },
  "records": { "9c2e…": { "q3-report#risks": { "title": "Q3 report", "text": "…" } } },
  "neighbors": { "9c2e…": { "q3-report#risks": { "prev": "q3-report#scope", "next": null } } }
}
```

Follow-up lookups (`/chunk`, `/doc`) carry the snapshot id and the ref. The server resolves them against that snapshot if it still holds it. Proposed: the served app retains the previous snapshot in memory, so ←/→ navigation keeps working across one refresh. Beyond that, it answers with an explicit `stale_snapshot` error. It never reinterprets an old ref against the newest data (§15, D5). The shell keys its cache by snapshot and ref and clears it when the snapshot changes. A refresh cannot revoke text the browser already received, and snapshot retention is not access control.

## 7. Durable store ownership and collection

### 7.1 Authority

Keep the existing payload formats and codecs. Extend each artifact's metadata (`Meta` in `store.go`) into a **descriptor** that lists the payload it owns and the artifacts it depends on. Then add two kinds of file:

- An immutable **snapshot manifest** per published snapshot, listing its artifact keys (the record in §6.1).
- A small, atomically replaced **current pointer** per build identity, naming the current manifest.

Descriptors and manifests are the store's inventory. The lineage list becomes a derived accelerator for finding delta bases. It may be rebuilt or deleted at any time without losing track of anything. Do not add a database for an inventory the descriptors already express.

GC and listing traverse the defined layout instead of guessing from directory-name punctuation. That traversal also fixes the dotted-root bug, which phase 4 corrects.

### 7.2 One owner per writable namespace

The deployment model is deliberately simple. One process owns a writable namespace by holding an OS exclusive lock (for example `flock` on `<ns>/.lock`) for its lifetime. That process performs builds, publication, leases and online GC. A second process must use a different namespace, or it fails with a clear error. `ragwb store gc` takes the same lock, so it runs only while no server holds the namespace; it cannot see another process's in-memory leases, and with the lock it never needs to. Under the lock, any staging directory not owned by a live builder scope is abandoned and can be removed. The current "older than an hour" heuristic goes away.

The browser memory store implements the same logical contract without locks or persistence. Cross-machine leases, a distributed collector and several processes sharing one store are out of scope. They would need a separate design.

### 7.3 Publication, recovery and GC

```text
1. Write payloads into a staging directory owned by the builder scope.
2. Close writers; validate counts, checksums and codec metadata.
3. Move complete payloads into the namespace.
4. Write their descriptors atomically.
5. Write the snapshot manifest.
6. Atomically replace the current pointer.        ← the publication point
7. Update the derived lineage list.
```

A crash before step 6 leaves the old snapshot current. A crash after it can leave only the lineage accelerator stale. Durability across power loss needs file and directory sync around these renames, because rename atomicity alone does not establish it. Test this at the supported platform boundary.

GC is mark-and-sweep. It marks every artifact reachable from current pointers, retained snapshots, active leases and builder scopes, following descriptor dependencies transitively. Then it deletes unmarked artifacts. The owning process serializes publication with the mark/sweep decision, so an artifact cannot be collected between becoming durable and being published. Missing or corrupt metadata aborts deletion with an actionable error; GC never guesses that an unknown file is safe to delete. Hard-linked payload files are removed through directory ownership, so deleting one artifact's links never removes another's. GC reports logical bytes and actually reclaimed bytes separately.

Retention is a deliberate policy, a count of historical snapshots per build identity (§15, D6), and is not tied to query duration. Retained snapshots also keep the content of notes that were since excluded or deleted, which is a privacy consequence the policy must state.

## 8. Operations and shared jobs

### 8.1 One native async bridge

Sample, decide and rerank all need the same lifecycle: propagate the context, listen for abort, run work off the owner, post events, settle the promise once and clean up. Extract that lifecycle once, in Go. Each provider supplies small functions for request conversion, execution, result validation and error mapping.

```go
// Proposed sketch. Payloads crossing goroutines are immutable Go values or bytes.
type OutcomeKind int // Succeeded | Cancelled | DeadlineExceeded | Failed | Shutdown

type Outcome struct {
    Kind    OutcomeKind
    Payload []byte
    Failure *Failure // code, message, provider details
}

func RunOperation(svc Services, signal JSSignal, spec Spec) goja.Value /* promise */ {
    // 1. ctx := derived from runtime lifetime; attach abort listener (owner)
    // 2. worker goroutine: value, err := spec.Execute(ctx, emit)  (errgroup for batches)
    // 3. worker: outcome := spec.Classify(value, err, context.Cause(ctx)); state.CompareAndSwap(pending, outcome)
    // 4. abort listener: state.CompareAndSwap(pending, Cancelled); cancel(ctx)
    // 5. post once to the owner: settle the promise from state; detach the listener
    // 6. cancel ctx and release resources even if posting fails (runtime closed)
}
```

The **arbiter** is one state cell per operation, set by compare-and-swap. The worker sets it after classifying the provider's result. The abort listener sets it to `Cancelled` when the caller aborts. Whichever succeeds first is the outcome, and settlement only reads it. In the rerank case of finding 9, the failure is classified and stored before `cancel()` runs, so it stays a failure. A deadline is `DeadlineExceeded`, not `Cancelled`. Runtime disposal may prevent the promise callback from running, but it never prevents cleanup.

Sampling with tools needs one more capability. The model may call a tool implemented in JavaScript while the worker is running. That uses a request/reply hook on the owner: the worker posts the call and waits for the reply or for cancellation. Worker goroutines never touch Goja values. A tool round trip is richer than a one-shot rerank request, so reuse the lifecycle only where the semantics match, and do not bypass the runner for tools.

### 8.2 Shared build jobs with waiters

`buildShared` becomes a small registry of **jobs** keyed by build identity (§6.1). It is not a durable scheduler. Each job has its own context, linked to engine shutdown, and a set of **waiters**: the queries or HTTP requests that need its result. A job may also have a **refresh owner**, the watch timer or an explicit refresh operation, which keeps it alive with no waiters. The job publishes its snapshot once, on success.

```text
request A ─ wait ─┐
                  ├─> job J ─> build candidate ─> validate ─> publish snapshot
request B ─ wait ─┘

A cancels      → A detaches; J continues for B.
B cancels too  → no waiters and no refresh owner → J is cancelled and its scope released.
C arrives while J is cancelling → C waits for J to settle, then starts a fresh job.
Engine closes  → J cancelled; every waiter settled; cleanup joined.
```

Build progress belongs to the job and is fanned out to each waiter's subscription, tagged with job and operation ids so each HTTP response can attribute it. A slow or closed subscriber cannot block the job or other subscribers. No job events stay attached to the first request's scope.

## 9. One bounded HTTP transport

`handleRun` and `handleVerb` both call one response helper. Domain code (the query or verb) emits typed events and returns a terminal outcome. The helper owns admission, JSON or SSE framing and the request lifetime. Provider events stay independent of HTTP. Direct queries and verbs share the same admission limit and the same terminal error vocabulary.

The SSE writer is the only goroutine that writes to the response. Before every write and flush, including headers, heartbeats and the terminal event, it sets a deadline with `http.ResponseController.SetWriteDeadline` and uses the controller's error-returning `Flush`. Any response-writer wrapper must support the controller's `Unwrap` path. If a deadline cannot be set, that is a setup error, not permission to stream without a bound. Once a write deadline has been exceeded it cannot be extended, so the stream ends.

On a write or flush error, or on queue overflow, the writer:

1. stops pumping,
2. cancels this request's operation (detaching it from any shared job), and
3. returns through a bounded join, which releases the admission slot.

A broken connection cannot be promised a terminal event. A stream delivers at most one, and only while the connection works. Coalesced text and heartbeat buffers are bounded in bytes as well as entries. Per-write deadlines (proposed default: 10 s, configurable) allow a long healthy stream without one global timeout cutting it off.

The acceptance test uses a real TCP socket whose client stops reading, because a fake `ResponseWriter` cannot show that deadlines work. The test passes when the handler returns within the bound and the slot can be acquired again. If the stack is used before the full migration, this P1 fix lands early, but inside the new shared helper, not as a temporary alternative path.

## 10. Graph references and export

### 10.1 One reference representation

The DSL can stay concise. The compiler normalizes every structural reference into one canonical form:

```js
// DSL
.retrieve('text', { k: 30, when: { decision: 'route.needs', min: 0.5 } })

// Canonical IR (sketch)
{ id: 'text', t: 'retrieve', in: ['q'],
  p: { index: { kind: 'artifact', node: 'ix-text' }, k: 30 },
  when: { source: { kind: 'decision', node: 'route', output: 'needs' }, minimum: 0.5 } }
```

Every component declares its references through one shared schema. `references(node)` lists them, and `rewriteReferences(node, mapping)` rewrites them:

```js
references(textNode)
// [ { kind: 'input',    path: ['in', 0],        node: 'q' },
//   { kind: 'artifact', path: ['p', 'index'],   node: 'ix-text' },
//   { kind: 'decision', path: ['when','source'], node: 'route', output: 'needs' } ]
```

Pinned outputs (`outputs.hits = 'limit'`) are references of the same kind. The compiler checks existence, types, phases and cycles from this one list. Edits run as transactions that validate once at the end:

- `rename` checks that the target id is free, rewrites every reference and validates.
- `remove` states what happens to dependents: **reject** (default), **cascade**, or a declared **replacement**. The editor never guesses how a removed decision should affect a production query.

Signal names that lambdas read stay name-based. Ops do not rename signals.

Lambdas cannot be renamed safely by string replacement. A graph-aware lambda receives named bindings declared in its component options, for example `when: { fn: (s, { route }) => …, bind: { route: 'route' } }`. Renaming changes the binding, not the source. A transformation that would have to rewrite an undeclared reference inside a lambda is rejected. This is a clear restriction, not a compatibility workaround.

### 10.2 Capability-based export

"Submit-triggered" does not mean "unavailable in an exported page". Components instead declare capabilities, such as local text index, external model or host filesystem. The exporter keeps the components a static page supports and applies a documented projection to the rest. For the lexical export, the projection removes decision-only classifiers, makes the nodes conditioned on them unconditional (their `when` is dropped), and removes answer and context nodes. The exporter validates the transformed graph before serializing it. Instead of `engine.files()`, it bundles a normalized portable dataset taken from the snapshot.

## 11. Modules, TypeScript and what gets deleted

Split the engine into modules only as each contract lands: graph, datasets, snapshots/resources, operations, executor, with a thin `rag` facade. Browser, Goja and export keep loading the same generated bundle. `pkg/ragjs/module.go` and export loading change together. Imports stay local and acyclic; the runtime test enforces this.

Where each concern lives today:

| Concern | Current module |
|---|---|
| Lazy persistence, array/view handling (replaced by datasets) | `pkg/ragjs/src/engine/records.js` |
| Build, shared builds, `art`, engine GC | `pkg/ragjs/src/engine/engine.js` |
| Query execution and result decoration | `pkg/ragjs/src/engine/session.js` |
| Request scope capture and cancellation | `pkg/ragjs/src/engine/scope.ts` |
| Builders, compiler, ops | `pkg/ragjs/src/graph/graph.js` |
| HTML export | `pkg/ragjs/src/export/html.js` |
| Public namespace, `rag.define` | `pkg/ragjs/src/api/index.js`, `api/define.js` |
| Built-in components and record consumers | `pkg/ragjs/src/components/` |
| Evaluations and sweeps | `pkg/ragjs/src/evaluation/evals.js` |

New contracts are written in strict TypeScript as their runtime behavior changes. That covers datasets, record refs, snapshots, the job registry and operation outcomes. Use discriminated unions for terminal outcomes and for record provenance, and keep declarations beside the implementation that owns them. Do not add a declaration layer over the array/view forks being deleted. Today only `core/errors.ts`, `core/identity.ts`, `core/text.ts` and `engine/scope.ts` are strict; the rest is extracted JavaScript (`checkJs: false`). Consumers that 009 replaces are migrated and typed as part of that replacement.

`pnpm check` runs TypeScript, Biome (formatting and selected lint rules), an exact generated-asset check and bundle tests. Neither TypeScript nor Biome proves runtime input validity or resource lifetime; the lifecycle tests in §14 remain necessary.

| Boundary | Shared mechanism | Stays specialized |
|---|---|---|
| Memory and disk records | Dataset contract and conformance tests | Encoding, shards, page access |
| Fresh, loaded and delta outputs | Registry adoption | Build and delta algorithms |
| Sample, decide, rerank | Async lifecycle and outcome | Requests, protocol errors, tools |
| Direct query and verbs | Admission, events, JSON/SSE lifecycle | Parameter parsing, operation body |
| Compiler, editor, exporter | Reference traversal | Transformation policy |
| Timer, manual, post-sync refresh | One build/refresh operation | Trigger scheduling, access checks |

**Deleted as replacements land:** `unionChunks`, `isView`/`asArray` and scattered array conversions, record-view consumer tests, `Engine.gc` with its `retired` list, direct handle closing in engine callers, `buildShared`'s first-waiter options, the duplicated HTTP pumps, and the no-op closer fallback. Obsolete record formats, such as the older `records` codec still handled in `persist.go` `load`, are removed only after their live references have been enumerated. Memory algorithms used by the browser and by quality comparisons stay; a uniform interface is no reason to delete a useful backend.

No component-specific branch is added to the executor where a component contract can express the behavior.

## 12. Migration

Ordinary scripts stay recognizable: `.from().parse().chunk().index()` and `.retrieve().filter().rerank().pack().answer()`. `rag.run` and `result.top` stay because they are good interfaces. What changes for callers:

- Bake and custom callbacks receive datasets (§5.3).
- Raw hit consumers use `hit.ref`, and custom producers use `ctx.ref` (§5.2).
- `result.chunk('id')` with a bare string is removed. It takes a hit or ref.
- The served run response and the `/chunk` and `/doc` lookups become snapshot-aware (§6.3).

The GUI, traces, evaluations, sweeps, saved plans, example presets and export migrate together with the API cutover that affects them, in the same commit as their fixtures. Bump the serialized IR version and the artifact namespace version. Existing local caches are disposable. Document that an old store needs an explicit cleanup command run while no process owns it; never silently delete a user's directory. No legacy reader is needed.

## 13. Implementation sequence

Each phase is one vertical change: the new contract, its tests, its consumers migrated and the replaced code deleted. Do not extract empty abstractions first or leave two engines side by side. The existing PR stack is not merged. Choose one integration branch for this cleanup and update the review stack coherently, rather than fixing divergent copies. Nothing in this design implies a force push, merge or review-thread resolution.

1. **Characterize.** Write deterministic regressions for all twelve findings against the current code and record which fail. Cover cold, stored and delta paths, plus an equal-count text edit for bake caching. Start from `pkg/runner/serve_watch_test.go` (refresh), `pkg/server/verbs_test.go` (SSE), `pkg/artifacts/store_test.go` (GC/List) and `pkg/ragjs/*_test.go` (engine).
2. **Datasets and provenance.** Add the dataset contract with memory and record-view implementations, refs on hits, index-to-dataset binding, `ctx.ref`, the fusion rule and bake migration. Delete `unionChunks` and the array/view conversions. Entry points: `engine/records.js`, `engine/engine.js` `artifacts`/`resolveBakes`, `components/builtins.js`, `natives/persist.go` `recordsViewJS`.
3. **Snapshots and resources.** Add leases, the Go registry and builder scopes, publication and detached results. Migrate `Session.run`, the serve harness `run`/`pack`/`chunk`/`doc`, evaluations and the shell cache. Delete `Engine.gc` and the no-op closers. Entry points: `engine/session.js`, `runner/serve.go`, `natives/persist.go`, `natives/markdown_native.go`, `ragjs/module.go`, `web/app-shell/shell.js`.
4. **Store inventory and GC.** Add descriptors, manifests, current pointers, the namespace lock and mark/sweep. Make lineage derived and fix `List`. Add crash-boundary and active-reader tests. Entry points: `artifacts/store.go`, `artifacts/lineage.go`, `cmd/ragwb/cmds/store.go`.
5. **Operations and shared jobs.** Move sample, decide and rerank onto the one runner, and replace `buildShared` with the job registry. Exercise cancellation races, runtime closure and event fan-out. Fix the model fallback as a small independent change: with an empty enabled list, choose the first eligible model sorted by reference (`gepp/backend.go` `preferred`).
6. **Transport.** Route both HTTP forms through one bounded writer and request lifetime (`server/sse.go`, `rag.go`, `verbs.go`). Test client stalls, flush failures, final-event races and slot recovery.
7. **Graph references and export.** Add canonical references, `references`/`rewriteReferences`, transactional rename/remove, lambda bindings, capability projection and portable export data (`graph/graph.js`, `export/html.js`).
8. **Cleanup and integrated verification.** Remove dead code and obsolete formats, and update examples, help and API documentation. Exercise the served vault app and the report workflows end to end. Record performance and retained-resource measurements.

Commit at meaningful, validated boundaries. Run focused tests while developing a contract and the full checks at integration boundaries:

```sh
pnpm install --frozen-lockfile && pnpm build && pnpm check
go test ./pkg/ragjs ./pkg/runner ./pkg/server ./pkg/artifacts ./pkg/natives ./web
```

## 14. Tests and acceptance

| Contract | Sequence and assertion |
|---|---|
| Dataset consistency | Same records, order and resolution through memory, stored and delta datasets; all four rows of the §3.2 table resolve by ref |
| Bake inputs | One API cold and warm; a text edit with equal counts invalidates the bake |
| Provenance | Two datasets sharing ids resolve their own bytes through retrieve, fuse (`on`), pack, trace and display; unbound mixed fuse fails to compile |
| Leases | Hold a query inside a provider fake; publish two later snapshots; release; the result is built from the original snapshot |
| Resource lifecycle | Load, delta and fresh paths all adopt; errors and shutdown close each handle exactly once |
| Result ownership | A returned result stays readable after its lease is released and unused handles close |
| Inventory | Create 40+ versions, retain 3, GC leaves exactly the reachable set regardless of lineage length |
| Locking | A second owner and standalone `store gc` are refused while a server holds the namespace |
| Crash recovery | Fail between each publication step; reopen to a complete old or new snapshot, never a mix |
| Shared jobs | Cancel A while B waits: B succeeds. Cancel all: the job releases resources. A new waiter never joins a job being cancelled |
| Async outcome | A provider failure stays a failure after cleanup; a caller cancel stays a cancellation; a deadline is a deadline |
| SSE bounds | A real non-reading socket and a flush error end within the bound; slots recover |
| Graph edits | Rename, remove and projection preserve typed references or reject explicitly; no dangling decisions or index names |
| Export | A plan with a classifier exports a working, unconditional lexical page |
| Local fixes | A dotted store root lists its outputs; an empty enabled list selects a deterministic eligible model |

Use synthetic corpora and provider fakes for lifecycle tests. Use hold/release barriers to make races deterministic, never sleeps. Count closer calls and, where practical, check actual open file descriptors.

Measure cold build time, warm startup, query latency, changed-note refresh, memory, open file descriptors and store growth on the same fixture before and after the change, and report sizes and conditions. Passing unit tests do not establish performance parity: copying result records and carrying refs change allocation. A regression calls for investigation, not automatic abandonment of the cleaner contract.

**Done** means:

- all twelve findings have passing regressions or a documented, reviewed disposition;
- the mechanisms listed in §11 are deleted;
- the same example works in a browser (memory) and in Goja (persistent);
- the documentation matches the public API;
- the shutdown, cancellation and publication sequences are verified by tests.

A finding is not resolved merely because an abstraction with the right name exists, or because its code moved into a module.

## 15. Risks and decisions

### 15.1 Risks

- **Result lifetime.** Releasing a lease before copying the needed records recreates use-after-close under a new API. `materializeResult` must run inside `withCurrent`.
- **Shared resources.** Snapshots share unchanged artifacts. Retiring a snapshot releases references; it must never close every handle the snapshot mentions.
- **Cross-process GC.** In-memory leases protect nothing from another process. Only the namespace lock makes GC safe.
- **Ref migration** is the costliest change. Every trace, evidence, GUI and evaluation consumer must carry refs, and the fusion rule must be explicit.

### 15.2 Decisions

| ID | Question | Recommendation | Alternatives | Blocks |
|---|---|---|---|---|
| D1 | Fusing hits from different datasets | Compile error unless `on: nodeId`; missing id is a run error | Silent canonical projection; "last stream wins" (rejected) | Phase 2 |
| D2 | Which datasets a bake sees | Corpus documents + final chunk dataset, plus `ctx.dataset(id)` | Only explicit `ctx.dataset` | Phase 2 |
| D3 | Freshness when sources change | One-shot `rag.run` waits; served apps use last good snapshot and refresh in background | Always wait (today); never wait | Phase 3 |
| D4 | Registry duplicate adoption | Reuse entry, close the new handle | Reject and close | Phase 3 (small) |
| D5 | Lookups against old snapshots | Retain one previous snapshot; otherwise `stale_snapshot` | Retain N; always re-run the query | Phase 3 |
| D6 | Durable snapshot retention | Fixed count per build identity (for example 3), privacy note in docs | Age-based | Phase 4 |
| D7 | Default for removing a referenced node | Reject; cascade/replace explicit | Cascade by default | Phase 7 |
| D8 | Result size budget and limit error code | Choose during phase 3 from measured result sizes | Pagination contract | No (ordinary choice) |
| D9 | SSE write-deadline default | 10 s per write, configurable | Shorter, with retries | No (ordinary choice) |

D1–D3 change user-visible behavior and need agreement before their phase starts. The rest can be settled by the implementer and recorded in the phase's commit.

### 15.3 Out of scope

Distributed indexing, remote dataset backends, multi-process shared stores, full Obsidian rendering and production authentication are out of scope. Reindex status and manual refresh can be built later on the application operations; they must not introduce another refresh implementation.

The design succeeds if a developer stops needing to ask: Is this an array or a view? Who closes this reader? Whose cancellation controls this build? Which snapshot does this hit belong to? Which references does this rename update? Each question should have one answer, enforced at one boundary.
