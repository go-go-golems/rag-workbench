# RAGWB-009: Consistent datasets, snapshots, operations and graph references

## 1. What this guide is for

rag-workbench lets a user describe a retrieval pipeline in a short JavaScript program, build it over a set of Markdown notes, and query it from a CLI, a served web app or a standalone HTML page. The same engine runs inside Go (through the Goja interpreter) and inside a browser.

The engine works, but several of its internal boundaries are ambiguous. A component can receive an array or a lazily loaded record view depending on whether its input came from disk. A query can still be running when a refresh closes the files it is reading. A build started for one HTTP request is cancelled when *that* request disconnects, even if other requests are waiting for it. A rename in the graph editor forgets some references. Twelve review findings on the open PR stack are symptoms of these boundaries.

This guide explains how the system works today, which mechanisms produce those findings, and a target design that removes the causes. It is written for an engineer who has not seen the earlier discussion and who will implement the change.

**Status.** Everything in sections 5–10 is a proposal. Nothing in it is implemented. Sketches use TypeScript or Go; names are suggestions unless a section says the choice is settled. Statements about current behavior come from reading source on the branch that contains this guide (runtime baseline `c267f91`); no tests were run to produce this guide, and several review findings were confirmed by reading the mechanism rather than by reproduction. Section 3 marks which.

**Compatibility.** There is no deployed API or data format to preserve. Callers, examples, fixtures and stored formats are updated in the same change as the contract they use. There are no adapters, dual paths or legacy readers.

**What stays.** The fluent corpus/plan builders, one JavaScript engine for both hosts, the Markdown parser and section chunker, Bleve and the terms index, incremental (delta) build algorithms, provider implementations and their error codes, and the existing evaluation cases.

**Where the code is.** The runtime is authored in `pkg/ragjs/src/` and compiled by `pnpm build` into the committed `pkg/ragjs/assets/rag.js`, which Goja, browsers and exported pages all load. Edit `src/`, never the asset. A useful first session:

```sh
pnpm install --frozen-lockfile
pnpm build && pnpm check
go test ./pkg/ragjs ./pkg/runner ./pkg/server ./pkg/artifacts ./pkg/natives
```

Then read `examples/apps/reports/lib/plans.js` and follow one query through `pkg/server/rag.go` → `pkg/runner/serve.go` → `pkg/ragjs/src/engine/session.js`.

## 2. How the system works today

### 2.1 Running example

The rest of this guide uses one pipeline. It is close to the `adaptive` plan in `examples/apps/reports/lib/plans.js`, with an enrichment step added (the enrichment syntax is from `pkg/ragjs/enrich_decide_test.go`):

```js
const corpus = rag.corpus('reports')
  .from(rag.source.dir({ root: '/data/reports' }))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .enrich('kind', rag.enrich.decide({ questions: { topic: { type: 'choice', /* … */ } } }))
  .index('text', rag.bm25());

const plan = rag.plan('adaptive').over(corpus)
  .signals({ q: '', k: 8 })
  .classify({ id: 'route', questions: { needs: { type: 'noul', /* … */ } } })
  .retrieve('text', { k: 30, when: { decision: 'route.needs', min: 0.5 } })
  .pack({ budget: 30000 })
  .answer({ direct: true });
```

It is served with `ragwb serve --watch 30s`, so the server rechecks the notes every 30 seconds.

### 2.2 Plans compile to a graph of build and live nodes

The builders are immutable values. `rag.compile(plan)` turns them into an **IR** (intermediate representation): a list of nodes, each with an `id`, a component type `t`, parameters `p`, input node IDs `in`, an output kind and a **phase**. `finalize` in `pkg/ragjs/src/graph/graph.js:405` checks port types, existence of references and cycles.

For the example, the IR contains:

| Node id | Type | Phase | Output kind | Notes |
|---|---|---|---|---|
| `source` (approx.) | `source.dir` | build | files | reads the notes |
| `parse` | `parse.markdown` | build | docs | one record per note |
| `chunk` | `chunk.sections` | build | chunks | section chunks with ids like `doc#section` |
| `enrich_kind` | `enrich.decide` | build | chunks | **same chunk ids**, extra `kind` fields |
| `ix-text` | `index.bm25` | build | index | built from `enrich_kind` (the corpus cursor) |
| `route` | `classify` | live, trigger `submit` | decision | one LLM decision per query |
| `text` | `retrieve` | live | hits | `when` reads `route.needs`; `uses: ['ix-text']` |
| `pack`, `answer` | … | live | context, answer | |

**Build nodes** run when documents change. **Live nodes** run per query. Two facts about this table matter later: `chunk` and `enrich_kind` are two chunk outputs whose records share IDs but not bytes; and `retrieve('text')` refers to its index by the naming convention `'ix-' + 'text'` (`builtins.js:180`), not by a stored node reference.

### 2.3 Build: digests, memo, store and lineage

`Engine.build(ir)` (`pkg/ragjs/src/engine/engine.js:147`) visits build nodes in order. For each node it computes a **digest**: a hash of type, parameters, input digests, file digests for sources and the source text of user-defined components (`digests`, engine.js:58). The digest identifies the output's content. The engine keeps an in-memory **memo** from digest to value.

When the Go host has an artifact store, each output is also saved under a **store key** (`storeKey = hash([digest, def.src])`, `records.js:86`). On a later build, a stored output becomes a `Lazy` memo entry that is loaded on first read. Loading chunk or doc outputs in Go returns a **record view** (`recordsViewJS` in `pkg/natives/persist.go:437`): a JavaScript object over a disk-backed reader with `get`, `has`, `order`, `values`, `toArray`, `size` and a hidden `__records` handle. Indexes such as Bleve are returned as objects with a hidden `__ix` handle. Both handles hold open files.

A **lineage** key identifies "this node of this pipeline" independent of document contents (`lineages`, engine.js:81). After saving, the engine records `(lineage, storeKey, digest, input digests)` in a per-lineage list (`pkg/artifacts/lineage.go:98`). An incremental build searches that list for a previous output computed from the inputs' previous versions (`tryDelta`, engine.js:443) and asks the component to apply a delta. The Markdown parser and chunker do this by copy-on-write of the previous records directory (`pkg/natives/delta.go:96`).

### 2.4 Artifacts: the merged view queries read

A query does not read node outputs directly. `Engine.artifacts(ir)` (engine.js:556) builds one object `art` with:

- `art.chunks`: **all** chunk outputs merged by chunk ID into one map;
- `art.order`: each document's chunk order, taken from the first chunk node that is not `enrich`/`enrich.llm`;
- `art.indexes`: indexes by name;
- `art.docs`, `art.links`: computed on first use; the first document with a given ID wins.

`art` is cached by the concatenated digests (`this._art`). Components read records through `ctx.chunk(id)` → `art.chunks.get(id)`.

### 2.5 Query: Session.run

`Session.run` (`pkg/ragjs/src/engine/session.js:37`):

1. Calls `engine.artifacts(ir)`. If any build output is missing (sources changed or never built), it awaits `engine.buildShared(ir, …)` and calls `artifacts` again.
2. Runs live nodes in order. Each may await an LLM (`ctx.sample`), a decision (`ctx.decide`) or a reranker. Hits are normalized to `{ id, score, via, … }` (`components/hits.js:17`); a hit carries only a chunk ID.
3. Returns a result whose non-enumerable `chunk`, `top`, `explain` and `toJSON` methods close over `art` (`decorateResult`, session.js:361).

`art` is held across every await in step 2 and after return through the result methods.

### 2.6 Serving: one long-lived runtime per app

`runner.OpenApp` (`pkg/runner/serve.go:174`) creates one Goja runtime per app and evaluates a harness that builds the plan and installs `globalThis.__serve`. All JavaScript for that app runs on the runtime's single **owner goroutine**; LLM calls run on Go goroutines and post their results back. So JavaScript never runs concurrently, but two queries can interleave at their awaits.

`__serve.run` (serve.go:134) runs `session.run`, then calls `collect()` (which runs `engine.gc` if the artifacts key changed), then **packs** the response: it reads `art()` *again*, collects chunk bodies for every ID mentioned in outputs, and adds previous/next neighbors for the first 40 hits. `__serve.chunk(id)` and `__serve.doc(id)` answer browser cache misses using the current `art()`.

`__serve.refresh` rebuilds if the sources changed and runs `collect()`. `watchApp` (`cmd/ragwb/cmds/serve.go:169`) calls it on a ticker.

`engine.gc(irs)` (engine.js:410) drops memo entries no longer reachable from the app's plan and the last eight plans verbs ran (`recentIrs`), moves their values to `retired`, and closes the handles of values that were retired by the **previous** gc call. Retirement therefore delays closing by exactly one more gc.

### 2.7 HTTP and streaming

`pkg/server/rag.go` exposes `POST /api/rag/{app}/run` (`handleRun`) and `pkg/server/verbs.go` exposes `POST /api/rag/{app}/verbs/{verb}` (`handleVerb`). Either can answer JSON or Server-Sent Events. In SSE mode the handler starts a pump goroutine (`sseWriter.pump`, `pkg/server/sse.go:144`), passes a `streamSink` to the runtime, and after the operation returns, closes the sink and waits for the pump. Verb requests are admitted through a per-app semaphore (`acquire`, rag.go:52; default 8); direct runs are not. `web/app-shell/shell.js` turns these responses into the GUI's local host interface and caches chunks it receives.

### 2.8 Native asynchronous operations

Three Go bridges create JavaScript promises for work done on goroutines: `newSample` (`pkg/ragjs/sample.go:67`), `newDecide` (`pkg/ragjs/decide.go`) and `rerankAsync` (`pkg/natives/rerank.go:108`). Each derives a context from the runtime lifetime, adds a listener on the request's abort signal, runs the provider on a goroutine, and posts a settle callback to the owner goroutine. They differ in how and when they classify the outcome.

### 2.9 Durable store layout

`pkg/artifacts/store.go`: `<dir>/<ns>/<key[:2]>/<key>.json` (metadata) plus `<key>.bin`, `<key>.<codec>` or `<key>.<codec>/`. The namespace `ns` hashes the Go component version, mode and the bytes of `rag.js` (`runner.ArtifactNamespace`, `pkg/runner/runtime.go:77`), so any change to the generated asset already starts a fresh namespace. `Store.GC(keep)` (lineage.go:159) walks lineage files and deletes stored versions beyond the newest `keep`. `ragwb store gc` (`cmd/ragwb/cmds/store.go:96`) runs it from a separate process.

## 3. What goes wrong and why

The twelve review findings are listed with the review's priority labels. "Confirmed" means the mechanism was found by reading current source; "reported" means it comes from the review and the mechanism is a deduction. None were reproduced for this guide. Each implementation phase must first add a failing regression.

### 3.1 Representation decides behavior (datasets)

**F1 — duplicate chunks resolve differently when stored (PR #2, P2). Confirmed mechanism.** In `artifacts()` array outputs are merged with `Map.set`, so the *last* chunk node wins for a shared ID. Record views are kept aside and combined with `unionChunks` (`records.js:30`), whose `get` prefers the array map and then the *first* view. Which record a query sees for `doc#section` therefore depends on which outputs happened to be loaded as views. In the example both `chunk` and `enrich_kind` produce `doc#section`; the precedence rule, not the plan, chooses whether the original or the enriched text is packed into an answer.

**F2 — bake callbacks receive a record view (PR #2, P2). Confirmed.** `resolveBakes` (engine.js:517) passes `outputs.get(firstChunksNode)` straight to the user callback. Cold, that is an array; warm from the store, it is a `RecordView` with no `map`/`filter`. Its cache key `hash([docs.map(d => d.id + d.sections.length), chunks.length])` also treats two inputs with equal counts but different text as identical.

These are the same defect: there is no single collection contract, and record identity is an ID without a dataset.

### 3.2 Lifetimes are not owned (snapshots and resources)

**F4 — two refreshes close artifacts used by an awaiting query (PR #3, P2). Confirmed.** A query captures `art` and awaits `route`'s LLM call. Refresh 1 publishes new outputs; its `gc` retires the old values. Refresh 2's `gc` closes them. The query resumes and reads a closed record reader.

**F6 — delta-created record readers miss cleanup (PR #3, P2). Confirmed.** `persist.go` `load` registers each opened reader with `addCloser` (persist.go:209, 227, 245). Delta builds open their result with `cowOpen` → `OpenRecords2` (delta.go:101) and wrap it with `ctx.store.view` (`markdown_native.go:38, 56`) without registering a closer. Separately, `addCloser` silently becomes a no-op when the host provides none (persist.go:47; `pkg/ragjs/module.go:143`).

A related source finding not in the review: `__serve.run` packs chunk bodies from `art()` *after* the query and after `collect()`. If a refresh landed during the query, hit IDs from the old generation are resolved against the new one: a removed chunk disappears from the response and a changed chunk shows new text next to old scores. `evaluation/evals.js:87` has the same pattern across evaluation cases. Fixing F4 alone does not fix this; response packaging has to use the query's generation.

### 3.3 The search index is used as the inventory (durable store)

**F5 — versions beyond 32 become uncollectable (PR #3, P2). Confirmed.** `LineageRecord` keeps the newest 32 entries and drops the rest from the file (lineage.go:110). `GC` only deletes entries it finds in lineage files. A dropped entry's payload is never deleted. Outputs of nodes without a lineage key are never collected either.

**F3 — store listing skips a root whose basename contains a dot (PR #2, P2). Confirmed.** `List` skips any directory whose name contains `.` to avoid descending into `<key>.<codec>/` payloads (store.go:282). The walk's root is also a directory, so `~/.cache/ragwb` lists nothing. This is a local bug with a local fix.

Two further source observations: `writeAtomic` renames without `fsync`, so it is atomic against concurrent readers but not durable across power loss; and `GC` deletes temp build directories older than one hour without knowing whether a running process still owns one.

### 3.4 Operations share the wrong owner (jobs, async outcomes, transport)

**F8 — cancelling the first waiter cancels another waiter's build (PR #4, P2). Confirmed.** `buildShared` (engine.js:695) starts `build(ir, opts)` with the first caller's `signal`, `onProgress` and `onEvent`, and returns the same promise to later callers. If request A disconnects, its signal aborts the build that request B is also waiting on; B receives only `{type:'build', waiting:true}` and never its own progress events. The key also omits options that change the result (`calls`, `full`, `force`, `persist`).

**F9 — deferred context cleanup turns failure into cancellation (PR #5, P2). Confirmed in `rerankAsync`.** The goroutine does `defer cancel()` and posts the settle callback; the callback runs later on the owner and tests `callCtx.Err() != nil`, which is now always true. A provider failure is reported as `cancelled`. `newSample` and `newDecide` classify before posting and are not affected, which shows the fix and why the three bridges should share one implementation.

**F7 — an SSE client that stops reading holds a slot indefinitely (PR #4, P1). Mechanism deduced.** `sseWriter.write` calls `fmt.Fprintf` and `Flush` with no write deadline and ignores flush errors; `pump` ignores write errors. When a client stops reading, the TCP send buffer fills and `Fprintf` blocks. The sink's 100 ms back-pressure rule cancels the operation, but `handleVerb` then waits on `<-done` for the blocked pump, so it never returns and never runs `defer a.release()`. After eight such clients an app answers every verb with 503.

`handleRun` and `handleVerb` duplicate the stream lifecycle with different terminal-error behavior, and only verbs are admitted through the semaphore.

**F10 — empty enabled-model list prevents a valid fallback (PR #6, P2). Confirmed.** In `pkg/llm/gepp/backend.go:114`, `preferred` treats an empty `EnabledModels` as "all enabled" for the default model but then iterates `EnabledModels` for the fallback, which is empty. Local fix.

### 3.5 Graph references are discovered in several places

**F12 — rename does not update decision references (PR #7, P2). Confirmed, and broader.** `applyOp('rename')` (graph.js:877) rewrites `in` and `outputs` only. It misses `when.decision` strings, the `p.index`/`p.indexes` parameters behind `uses` (renaming `ix-text` breaks `retrieve('text')`), and decision reads in lambda source. It does not check that the target ID is free (finalize later reports a duplicate).

**F11 — lexical export keeps references to removed decisions (PR #7, P2). Mechanism deduced.** `exportHtml` (`export/html.js:4`) removes every submit-triggered live node except answers. `classify` is submit-triggered (`builtins.js:637`), so `route` is removed, but `remove` checks only `uses` dependents (graph.js:841), not `when` readers. `retrieve('text')` still reads `route.needs` and `finalize` rejects the graph. The exporter also bundles `engine.files()`, which contains only `rag.files(...)` inputs, not `source.dir` notes.

The root cause is that `finalize`, `applyOp` and the exporter each enumerate references in their own way.

## 4. Target design at a glance

### 4.1 Entities and how they relate

| Entity | What it is | Identity | Lifetime | Owner |
|---|---|---|---|---|
| **Dataset** | Read-only records (documents or chunks) produced by one build node | `dataset.id` = that node's build digest | as long as some snapshot or builder holds it | the snapshot(s) that include it |
| **Record reference** | `{ dataset, id }` | both fields | a value; may outlive the data | whoever holds it |
| **Index** | Searchable structure built from exactly one chunk dataset; hits refer to that dataset | build digest; `index.dataset` = input dataset id | like a dataset | snapshot(s) |
| **Resource** | An open native handle (record reader, Bleve index) | store key | until its last owner releases it | the **registry** |
| **Snapshot** | Immutable mapping from a plan's build nodes to datasets/indexes, published as a whole | `snapshot.id` (generation) | until retired and unleased | the engine's snapshot table |
| **Lease** | A query's hold on one snapshot | — | one operation | the operation |
| **Build job** | One in-progress build of a plan's build key | normalized build key | until it publishes, fails or is cancelled | the job registry; waiters hold interest |
| **Operation** | One request-level unit of work (query, verb, refresh, native call) with one terminal outcome | operation id | from start to terminal outcome | its caller |

Relationships, for the running example:

```text
snapshot g7 (current)                         registry
  parse        -> Dataset d:parse@a1   ───────> reader r1 (owners: g7, g6)
  chunk        -> Dataset d:chunk@b2   ───────> reader r2 (owners: g7)
  enrich_kind  -> Dataset d:enrich@c3  (memory, no resource)
  ix-text      -> Index    i:text@e4   ───────> bleve  r3 (owners: g7)
                    index.dataset = d:enrich@c3
snapshot g6 (retired, leases=1)
  parse        -> d:parse@a1  (shared, unchanged notes' docs)
  ...
query q42 holds lease on g6
```

A hit from `retrieve('text')` is `{ ref: { dataset: 'enrich@c3', id: 'design#goals' }, score }`. Packing resolves it in g7, which returns the enriched record. Nothing chooses between `chunk` and `enrich_kind` by precedence.

### 4.2 Invariants

1. A record reference names its dataset; resolving it never consults another dataset.
2. Whether a dataset is in memory or on disk changes cost, not behavior.
3. A query reads exactly one snapshot from its first read through its packaged result.
4. Every opened native resource is adopted by the registry at the moment it is opened.
5. A resource is closed only after its last owner releases it.
6. The durable store's inventory is its descriptors and snapshot manifests; lineage lists are only a search accelerator.
7. Cancelling one waiter never cancels a build that another waiter still needs.
8. Every asynchronous operation has exactly one terminal outcome, fixed before cleanup runs.
9. Every network write and flush has a deadline; a transport failure ends that transport only.
10. Every structural graph reference is enumerated and rewritten by one visitor.
11. Ordinary application code never calls close or release.
12. Each phase removes the code it replaces.

These are enforced by private handles, single constructors and tests. JavaScript does not check them statically.

### 4.3 Module boundaries

```text
DSL builders, GUI, CLI, HTTP handlers
             │
Graph: compile, references visitor, edits, projections
             │
Application operations: build/refresh, query, lookup, evaluate
       │                         │
Build jobs                  Query executor
       └──────── Snapshots ──────┘
                    │
          Datasets and indexes
                    │
   Resource registry + artifact store

Native bridge (sample/decide/rerank) ─▶ operation outcome + events
HTTP respond helper ─▶ JSON or bounded SSE
```

No general workflow engine, plugin framework or dependency injection. Each shared mechanism replaces at least two existing copies.

## 5. Datasets and record references

### 5.1 The collection contract

Every docs or chunks output becomes a `Dataset`, whether it was computed in memory or opened from disk. Access stays synchronous: both current hosts are local.

```ts
// Proposed: pkg/ragjs/src/engine/datasets.ts
export interface RecordRef { readonly dataset: string; readonly id: string }

export interface Dataset<R> {
  readonly id: string;                // the producing node's build digest
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): R | null;          // frozen record or null
  values(): IterableIterator<R>;      // deterministic: production order
  materialize(): readonly R[];        // explicit full read
  ref(id: string): RecordRef;
}

export interface ChunkDataset extends Dataset<Chunk> {
  order(documentId: string): readonly string[]; // chunk ids in reading order
}

export interface Index {
  readonly id: string;
  readonly dataset: string;           // id of the ChunkDataset it was built from
  search(query: string, k: number, opts?: SearchOptions): Hit[];
}

export interface Hit { readonly ref: RecordRef; score: number; via?: string; reason?: string }
```

Two implementations: `MemoryDataset` over an array (browser and Go cold builds) and `StoredDataset` over a native reader handle (replacing `RecordView`). Records returned to callbacks are frozen (deeply, for `meta`) or are fresh copies; a callback cannot change what the next reader sees. `close`, `__records`, `__ix` and codec choice are not visible to components.

`order` is per dataset. An enrichment that maps chunks one-to-one keeps its input's order. This removes the "primary stream" guess in `artifacts()` (which today does not exclude `enrich.decide`).

### 5.2 How hits find their records

An index knows its dataset, so `retrieve` produces hits with full references. Every consumer resolves through the snapshot:

```js
const chunk = ctx.resolve(hit.ref);           // in a component
const chunk = result.record(hit.ref);         // on a returned result
```

`ctx.chunk(id)` and `art.chunks` are deleted. About fifteen call sites in `components/builtins.js`, plus `hits.js`, `events.js`, `session.js` and `export/html.js`, move to `ctx.resolve(hit.ref)`. Traces, evidence labels, events, evaluation and the GUI carry `ref` and never reduce it to an ID.

Documents: `parse` produces a documents dataset; a chunk's `docId` is resolved against the documents dataset that its chunk dataset was derived from. Document IDs are path-derived today (`natives.Version` comment, persist.go:21). With several sources in one plan, the logical document ID must include the source identity as well as the path; titles and slugs are never assumed unique.

#### Decision record D1 — hits carry `ref`, not `id`

- Context: chunk IDs repeat across enrichment datasets.
- Alternatives: (a) keep `hit.id` and add `hit.dataset`; (b) replace with `hit.ref`; (c) make IDs globally unique by prefixing the dataset.
- Decision (proposed): (b). A single field cannot be half-migrated; code that reads `hit.id` fails visibly. (c) would change user-visible IDs and still need parsing.
- Consequences: every hit consumer, event payload and GUI renderer changes. Display code uses `hit.ref.id`.

### 5.3 Combining rankings

Fusion (`fuse.rrf`) deduplicates by exact reference. Fusing hits from two indexes over the same dataset is unchanged. Fusing hits over *different* datasets (for example, an index over `chunk` and another over `enrich_kind`) needs an explicit rule, because `design#goals` from each is a different record.

The compiler knows statically which dataset every hits stream refers to: `retrieve` → its index node → that node's chunks input. A custom `retrieve(fn)` must declare its dataset or return full refs.

#### Decision record D2 — fusing across datasets

- Context: today the "last stream wins" rule silently picks a version.
- Alternatives: (a) reject at compile time unless the plan inserts a projection; (b) a fusion option `canonical: 'enrich_kind'` that maps foreign refs by ID; (c) allow and keep both.
- Decision (proposed): (a) as the default, with (b) as the only built-in projection. Keep this inside `fuse` and a small `project` component, not in the engine.
- Status: **open for owner confirmation**; it does not block phases 1–3 because no in-repo plan fuses across datasets today (to verify while migrating examples).

### 5.4 Bakes, callbacks and cache keys

Bake and custom build callbacks receive datasets:

```js
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id));
rag.bake(ctx => ctx.chunks.size);   // no full traversal
```

Which dataset is `ctx.chunks`? Today it is "the first chunks node in the IR". Proposed: the bake declares its input (`{ from: 'enrich_kind' }`), defaulting to the corpus cursor at the point the bake was added, which the builder already tracks (`this.st.cursor.chunks`, graph.js:125). The compiler records it as an ordinary input so it participates in digests.

The bake cache key becomes `hash([input dataset ids, bake source, component version])`. Since a dataset id is a build digest, changed text with equal counts changes the key.

Memory and stored datasets pass one conformance suite: iteration order, missing IDs, frozen records, `materialize`, `order`, and refs.

## 6. Snapshots, resources and results

### 6.1 Snapshot lifecycle

A **snapshot** is created by a successful build of a plan's build nodes. It is immutable. The engine keeps, per build key, at most one *current* snapshot plus any *retired* snapshots that still have leases.

```text
          build job publishes
 (none) ───────────────────────▶ current ──(newer publish)──▶ retired
                                    │                           │
                                    │ acquire/release leases    │ last lease released
                                    ▼                           ▼
                                 (leases>0)                  released: registry owners -1
```

States and transitions:

1. A build job writes into a private **build scope**. Every resource it opens is adopted into that scope.
2. On success, publish: create the snapshot, transfer the scope's resources to it, make it current, mark the previous current as retired. Publishing is a synchronous step on the runtime owner.
3. A query calls `acquire`, which returns the current snapshot and increments its lease count in the same synchronous step. Because both acquire and publish run on the single owner goroutine without an await between them, no publish can intervene. This is the arbiter.
4. When a retired snapshot's lease count reaches zero, it releases its registry ownership. A resource closes when no snapshot or build scope owns it.
5. A failed or cancelled build discards its scope (closing its resources) and leaves the current snapshot unchanged.

Sequence for F4, fixed:

```text
q42 acquire g6 (leases 1) ── awaits route LLM ──────────────────────────── resumes, packs from g6, release
refresh 1: build → publish g7; g6 retired (leases 1)
refresh 2: build → publish g8; g7 retired (leases 0) → released
                                                     q42 release: g6 leases 0 → released
                                                     r2 owners 0 → close
```

### 6.2 The resource registry

The registry lives in Go, one per runtime, because the handles are Go objects and because runtime shutdown must close them from Go.

```go
// Proposed: pkg/resources
type ID string // the artifact store key, or a unique id for temp outputs

type Registry struct { mu sync.Mutex; entries map[ID]*entry; closed bool }
type entry struct { c io.Closer; owners int }

// Adopt registers c with one owner. If id is already present, the existing
// entry gains an owner and c is closed: one underlying handle per id.
func (r *Registry) Adopt(id ID, c io.Closer) (Handle, error)
func (r *Registry) Retain(id ID) error
func (r *Registry) Release(id ID) error // closes at owners == 0
func (r *Registry) Close(ctx context.Context) error // shutdown
```

Every opener follows one shape:

```go
rd, err := OpenRecords2(dir)
if err != nil { return nil, err }
h, err := scope.Adopt(resources.ID(key), rd)
if err != nil { _ = rd.Close(); return nil, err }
return newStoredDataset(vm, h), nil
```

`persist.go` `load`, the delta callbacks in `markdown_native.go`, fresh Bleve builds in `bm25_native.go` and the delta base loaded by `tryDelta` all use it. `rtenv.Env.AddCloser`'s no-op fallback is removed for runtimes that have a store: constructing a store-backed runtime without a registry is an error. A browser runtime has no native resources and uses a registry that never receives entries.

Duplicate adoption reuses the existing entry and closes the new handle (chosen over rejecting, because two loads of the same store key in one runtime are legitimate).

Shutdown order: reject new operations; cancel active ones; wait for them to settle, with a deadline; release snapshots; `Registry.Close`. If the deadline passes, report which operations are still running and do not close resources they can reach.

### 6.3 Queries and detached results

```ts
// Proposed sketch: pkg/ragjs/src/engine/session.ts
async function query(plan: Plan, signals: Signals, req: Request): Promise<Result> {
  await jobs.ensure(plan, req);                   // joins or starts a build when needed
  const snap = snapshots.acquire(plan.buildKey);  // synchronous; throws if none
  try {
    const run = await executor.run(plan, signals, snap, req);
    return materialize(run, snap, req.limits);    // copies the records the result needs
  } finally {
    snapshots.release(snap);
  }
}
```

`materialize` produces a **detached result**: plain data plus the records needed for its hits, trace entries, evidence and neighbor preview, keyed by reference:

```json
{
  "snapshot": "reports:g6",
  "hits": [{ "ref": { "dataset": "enrich@c3", "id": "design#goals" }, "score": 7.1 }],
  "records": { "enrich@c3": { "design#goals": { "title": "Design", "text": "…" } } },
  "neighbors": { "enrich@c3": { "design#goals": { "prev": "design#intro", "next": "design#risks" } } },
  "trace": [ … ], "answer": { … }
}
```

`result.record(ref)`, `result.top(n)` and `result.explain(ref)` read only this data. The result stays valid after the lease is released, needs no close, and is the same object the server serializes; `__serve.run`'s separate `pack` step disappears. The neighbor preview keeps today's limit (first 40 hits).

Size: copy only what the result references. If a declared limit would be exceeded (for example a trace with `detail.chunks` on a very large `k`), return a `result_too_large` error naming the limit; never return a hit without its record.

Evaluation and whole-corpus export run inside one lease using an internal `snapshots.withLease(key, fn)` so all cases see one generation.

### 6.4 Browser lookups after a query

The shell asks for chunks that were not in the preview (`GET /api/rag/{app}/chunk`). The request carries `snapshot` and `ref`. The server resolves against that snapshot if it is current or still leased; otherwise it answers `409` with code `stale_generation`, and the shell re-runs the query. It never resolves an old reference against a newer dataset. The shell keys its cache by snapshot and clears it when a response carries a new snapshot id.

## 7. Durable store: inventory, ownership, collection

### 7.1 Authority

Keep the existing payloads and codecs. Change what is authoritative:

- **Descriptor** (the existing `<key>.json`, extended): type, codec, kind, size, count, plus `digest`, `lineage`, `inputs` (input digests). It owns exactly its payload path.
- **Snapshot manifest** (new, immutable): `snapshots/<buildKey>/<generation>.json` listing each build node and its store key.
- **Current pointer** (new): `snapshots/<buildKey>/current`, replaced atomically.
- **Lineage lists**: a cache that can be rebuilt from descriptors. They are never consulted to decide what to delete.

Hard-linked shard files (records2 copy-on-write) need no dependency tracking: each artifact owns its directory entries, and removing one artifact's links leaves the other's intact. If any codec references another artifact by path rather than by hard link, its descriptor must list that dependency; this needs a check of the codecs during phase 4.

`List` walks the defined layout (`<ns>/<shard>/<key>.json`) instead of skipping names with dots, which also fixes F3.

### 7.2 One writer per namespace

One process owns a writable namespace for its lifetime, holding an exclusive OS lock (`flock` on `<ns>/.lock`). It performs builds, publication, leases and online GC. Another process that tries to open the same namespace for writing fails with a message naming the owner's PID. `ragwb store gc` takes the same lock, so it runs only when no server is using the namespace. Cross-process leases and distributed collection are out of scope.

This affects one-shot CLI commands that also use the store while `ragwb serve` runs: they will be refused. See open decision O5.

### 7.3 Publication and crash behavior

```text
1. Write payloads into the namespace's staging directory.
2. Close writers; validate counts and codec metadata.
3. Rename payloads to their final paths; fsync the directories.
4. Write descriptors atomically (temp, fsync, rename, fsync dir).
5. Write the snapshot manifest the same way.
6. Replace the current pointer the same way.
7. Update lineage lists (best effort).
```

A crash before step 6 leaves the previous snapshot current; orphaned payloads and descriptors are collected later. A crash after step 6 leaves lineage stale, which only costs an incremental base. Today's `writeAtomic` lacks fsync; add it here, and test on Linux, which is the supported platform for durability claims.

### 7.4 Collection

Mark: artifacts listed by current manifests, by the retained older manifests (policy O4), by snapshots with live leases in the owning process, and by the owner's staging scopes. Sweep: descriptors not marked, then their payloads; staging directories not owned by a live build. The owner serializes publication and mark/sweep on one lock so a candidate cannot be swept between becoming durable and being published. Missing or unreadable descriptors stop the sweep with an error; unknown files are reported, never guessed safe. Report logical bytes and reclaimed bytes (`unlinkedSize` already distinguishes hard links).

## 8. Operations: native calls, shared builds and HTTP streams

### 8.1 One bridge for native asynchronous calls

```go
// Proposed: pkg/ragjs/opbridge
type Spec[T any] struct {
    Name    string
    Execute func(ctx context.Context, emit func(Event)) (T, error)
    Encode  func(T) ([]byte, error) // result crosses to the owner as bytes
    Classify func(err error) Failure // provider-specific mapping
}

func Start[T any](vm *goja.Runtime, signal goja.Value, spec Spec[T]) goja.Value // returns a promise
```

Lifecycle, in order:

1. On the owner: create the promise, derive `ctx` from the runtime lifetime, attach the abort listener. If the signal is already aborted, reject with `cancelled` immediately.
2. On a goroutine tracked by the runtime's task group: `Execute(ctx, emit)`. `emit` posts events to the owner.
3. When `Execute` returns, compute the outcome on the goroutine: success (encoded), provider failure (`Classify`), deadline, or runtime shutdown. The outcome is a value, computed before any `cancel()` runs.
4. A single `settle` guarded by a `sync.Once` decides the race between step 3 and an abort. Rule: whichever reaches `settle` first is the outcome. An abort reaching it first rejects with `cancelled` and cancels `ctx`; the worker's later result is discarded but its cleanup still runs.
5. Post one callback to the owner to resolve or reject; remove the abort listener. If the runtime is closing, the post may be dropped, but the goroutine still releases its resources and the task group still observes it.

The outcome type in TypeScript, for consumers:

```ts
type Outcome<T> =
  | { kind: 'ok'; value: T }
  | { kind: 'cancelled' }
  | { kind: 'deadline' }
  | { kind: 'failed'; code: string; message: string }
  | { kind: 'shutdown' };
```

`sample`, `decide` and `rerank` keep their request conversion, JSON validation and error codes as `Execute`/`Classify`. Tool calls in `sample` use a `CallOnOwner(ctx, fn) ([]byte, error)` helper so no goroutine touches a Goja value.

### 8.2 Shared build jobs

`buildShared` becomes a small job registry in `pkg/ragjs/src/engine/jobs.ts`:

- Key: the plan's build key plus every option that changes results (`calls`, `full`, `force`, `persist`).
- A job has its own scope (cancellation and event fan-out), linked to engine shutdown, and a set of waiters.
- `ensure(plan, req)` adds a waiter with the request's signal and event sink, and resolves when the job publishes.
- A waiter's abort removes only that waiter. When the last waiter leaves, the job is cancelled.
- A waiter that arrives while the job is cancelling waits for it to finish and then starts a new job.
- Progress events go to every waiter's sink, tagged with `job` id; a waiter's sink cannot block the job (sinks are already non-throwing and the Go sink drops on overflow).

A refresh is an ordinary waiter whose signal is its own timeout and engine shutdown. This removes the need for a separate "background owner" concept.

```text
A ─wait─┐
        ├─▶ job J ─▶ build scope ─▶ publish g8
B ─wait─┘
A aborts: A detached, J continues for B.
B aborts: no waiters → J cancelled, scope discarded, g7 stays current.
C arrives while J cancels: C waits for J to end, then starts J'.
```

### 8.3 When does a query wait for a build?

Today a query recomputes digests (including source digests, subject to `SourceDigestTTL`) and, if anything changed, waits for a build. With `--watch`, the original design wants queries to use the last good snapshot while a refresh runs. See decision D3.

#### Decision record D3 — query freshness

- Context: interactive workbench use expects edits to show up on the next query; a served app with `--watch` should not stall queries behind a rebuild.
- Alternatives: (a) always wait for current digests (today); (b) always use the current snapshot if one exists; (c) a per-host policy.
- Decision (proposed): (c). `rag.run` and the browser workbench wait (a). `ragwb serve --watch` uses (b) and relies on the watcher's refresh. `ragwb serve` without `--watch` keeps (a).
- Status: **open for owner confirmation**. It affects `jobs.ensure` only, not the snapshot contract.

### 8.4 One HTTP response path

```go
// Proposed: pkg/server/respond.go
type Operation func(ctx context.Context, sink runner.EventSink) (json.RawMessage, error)

func (s *RagServer) respond(w http.ResponseWriter, r *http.Request, a *RagApp, start json.RawMessage, op Operation)
```

`respond` admits the request through the app's semaphore (now shared by direct runs and verbs), chooses JSON or SSE, runs the operation, and writes exactly one terminal response. `handleRun` and `handleVerb` keep only parameter parsing and the body of `op`.

The SSE writer:

- One goroutine, the pump, performs every write and flush, including headers, pings and the terminal event.
- Before each write and flush it calls `http.NewResponseController(w).SetWriteDeadline(now + writeTimeout)`, and it uses the controller's error-returning `Flush`. If the deadline cannot be set, `respond` fails the request before streaming. After a write or flush error the stream is finished; a deadline already exceeded cannot be extended.
- On error or queue overflow: stop the pump, cancel the request's context. The operation's abort propagates through the bridge, so the operation reaches its terminal outcome promptly. The admission slot is released when the operation terminates, not when the handler gives up.
- Coalesced text is flushed when it reaches a byte limit as well as on the 30 ms timer.
- At most one terminal event. If the network failed, none is promised.

Tests use a real TCP connection whose client stops reading, and assert that the slot becomes available within the configured bound.

## 9. Graph references, edits and export

### 9.1 One reference visitor

Each component declares which parameters are references. The compiler normalizes concise DSL forms into one canonical shape:

```js
// DSL (unchanged)
.retrieve('text', { when: { decision: 'route.needs', min: 0.5 } })

// Canonical IR (proposed)
{ id: 'text', t: 'retrieve',
  refs: [
    { kind: 'artifact', node: 'ix-text',  param: 'index' },
    { kind: 'decision', node: 'route', output: 'needs', param: 'when' }
  ],
  in: ['q'], when: { source: { node: 'route', output: 'needs' }, minimum: 0.5 } }
```

`references(node)` lists data inputs, artifact uses, decisions and pinned outputs. `rewriteReferences(node, mapping)` rewrites them. `finalize`, `applyOp` and projections all use these two functions. `synonyms` becomes an ordinary declared reference instead of the special case at graph.js:455.

Edits:

- **rename** checks that the new ID is free, rewrites every reference through the visitor, and validates once at the end of the edit batch.
- **remove** rejects when any reference points at the node, unless the op says `cascade: true` or names a replacement. The error lists the referencing nodes and kinds.

Lambdas: arbitrary JavaScript cannot be renamed by text replacement. A lambda that reads a decision declares a binding (`deps: { route: 'route' }`) and calls `ctx.decision('route')` through it. Renaming changes the binding's target. A lambda whose source calls `ctx.decision('x')` without a declared binding is rejected at compile time (the existing regex in `whenRefs` is kept only to produce that error).

### 9.2 Export as an explicit projection

Components declare capabilities: `model` (LLM/decision/rerank), `host-fs`, `native-index`. `exportHtml` applies a documented projection:

1. Remove components whose capabilities the exported page lacks.
2. For each removed decision node, drop the `when` conditions that read it; those nodes become unconditional. (Today's export intends this; it is now explicit.)
3. Remove `answer` and `context` nodes and rewire hits consumers as today.
4. Validate the projected graph; fail with the list of unsupported nodes if validation fails.
5. Bundle the source node's files from the current snapshot rather than `engine.files()`, so `source.dir` corpora export.

Test: exporting the `adaptive` plan yields a page whose lexical search runs.

## 10. Local fixes

- **F3**: `artifacts.List` traverses the layout (section 7.1). Test with a root named `x.store`.
- **F10**: in `preferred`, when `EnabledModels` is empty, iterate eligible model refs in sorted order. Test with an empty list and an ineligible default.

## 11. What moves and what is deleted

| Area | New home (proposed) | Deleted |
|---|---|---|
| Datasets, refs | `src/engine/datasets.ts` | `records.js` `unionChunks`, `isView`, `asArray`, `Outputs`; `recordsViewJS` in persist.go |
| Snapshots, leases | `src/engine/snapshots.ts` | `Engine.retired`, `Engine.gc` retirement, `_art` cache, `artifacts()` merge |
| Detached results | `src/engine/results.ts` | `decorateResult`; `pack` in serve harness |
| Build jobs | `src/engine/jobs.ts` | `buildShared` |
| Graph references | `src/graph/references.ts` | per-op reference handling in `applyOp`, `whenRefs` discovery |
| Registry | `pkg/resources` | `AddCloser` no-op fallbacks for store runtimes, direct `close()` calls in engine |
| Native bridge | `pkg/ragjs/opbridge` | three hand-written lifecycles |
| HTTP | `pkg/server/respond.go` | duplicated pump/terminal code in `rag.go` and `verbs.go` |
| Store | `pkg/artifacts` manifests, lock | lineage-driven `GC` |

New contracts are strict TypeScript, per `pkg/ragjs/src/README.md`. `pnpm check` verifies types, Biome, generation and the acyclic import graph; it does not test lifetimes, which remain the job of the tests in section 13.

## 12. Migrating callers

- Plans written with the fluent builders stay as they are.
- `rag.run` and `result.top` stay. `result.chunk(id)` becomes `result.record(ref)`; there is no fallback that accepts a bare string.
- Custom components replace `ctx.chunk(id)` with `ctx.resolve(hit.ref)`, and `ctx.chunks()` with an explicit dataset (`ctx.dataset('enrich_kind')`).
- Bake callbacks take `ctx.chunks` / `ctx.docs` datasets.
- The GUI, app shell, traces, evidence rendering, evaluation output, sweeps, saved plans and example presets switch to refs in the same change.
- Old stores: the namespace changes automatically with the asset; document `ragwb store prune` for removing old namespaces. Never delete user directories implicitly.

## 13. Implementation phases

Each phase lands as a vertical change with tests and with the replaced code removed. Use one integration branch rather than patching each PR in the stack separately.

1. **Regressions and small fixes.** Add a failing test for each finding (cold, stored and delta paths for F1/F2; equal-count changed input for bakes). Fix F3 and F10. If the stack will be used before phase 5, land the bounded SSE writer now in its final location (`respond.go`).
2. **Datasets and refs.** `MemoryDataset`, `StoredDataset`, conformance suite in Goja and Node; indexes record their dataset; hits carry refs; migrate all consumers; delete `unionChunks` and views.
3. **Snapshots, registry, detached results.** Build scopes, publication, leases, registry adoption on every open path, detached results, lookups with snapshot ids, evaluation under one lease. Delete gc retirement and `pack`.
4. **Durable inventory.** Descriptors, manifests, current pointer, namespace lock, mark/sweep, fsync. Lineage becomes a cache.
5. **Operations.** `opbridge` for sample/decide/rerank; build job registry; freshness policy D3.
6. **HTTP.** `respond` for both endpoints, shared admission, deadlines, byte-bounded coalescing.
7. **Graph.** Reference visitor, canonical IR, rename/remove, lambda bindings, capability export.
8. **Integration.** Remove dead code, update docs and examples, run the reports and vault apps, record measurements.

## 14. Tests and acceptance

| Contract | Test |
|---|---|
| Dataset conformance | Same records, order, missing-ID result and frozen records from memory, stored and delta datasets, in Goja and Node |
| Provenance | `chunk` and `enrich_kind` share IDs; retrieval, packing, events and GUI show the enriched record for hits from `ix-text` |
| Bake | Cold and warm callbacks see a dataset; same counts with changed text re-run the bake |
| Snapshot leases | A query paused in a fake provider survives two publishes and packs records from its own snapshot |
| Registry | Fresh, loaded and delta resources each closed exactly once on success, failure and shutdown (counting closers, plus open-fd checks on Linux) |
| Detached result | Result readable after its snapshot is released and closed |
| Stale lookup | Lookup with a released snapshot id returns `stale_generation` |
| Inventory | 40 versions, retain 3, GC removes exactly the unreachable ones |
| Lock | Second writer and `store gc` refused while `serve` holds the namespace |
| Crash | Inject failure after each publication step; reopen yields old or new snapshot, never mixed |
| Shared jobs | A cancels, B succeeds; both cancel, job releases; C during cancel starts a new job |
| Outcomes | Rerank provider failure stays `failed` after cleanup; abort stays `cancelled` |
| SSE | Real non-reading socket and failing flush terminate within bound; slot recovers |
| Graph | Rename of `route` and `ix-text` updates all refs; remove with dependents is rejected |
| Export | `adaptive` exports a working lexical page |
| Local fixes | Dotted root lists; empty enabled list picks a deterministic model |

Use synthetic corpora and provider fakes with explicit hold/release barriers, not sleeps. Measure cold build, warm open, query latency, refresh time, memory, open files and store growth on the same fixture before and after; detached results add copying, so do not assume parity.

Completion means every finding has a passing regression or a documented disposition, the deleted code in section 11 is gone, the same example runs in browser and Goja hosts, and documentation matches the API.

## 15. Open decisions and risks

| Id | Decision | Blocks |
|---|---|---|
| D2 | Default policy for fusing across datasets | phase 2 for any plan that does it |
| D3 | Query freshness per host | phase 5 |
| O1 | Whether snapshots with deferred-LLM outputs (`Engine.partial`) are publishable; proposal: publish in memory, mark `partial`, never durable | phase 3 |
| O2 | Default size limit for detached results | phase 3 (any reasonable default works) |
| O3 | Status code for stale lookups (`409 stale_generation` proposed) | phase 3, minor |
| O4 | Retention policy for old manifests, including privacy of notes since removed | phase 4 |
| O5 | What one-shot CLI commands do when `serve` holds the namespace (fail, or run without the store) | phase 4 |
| O6 | Lock implementation on non-Unix platforms (`nlink_other.go` shows such builds exist) | phase 4 |
| O7 | Whether any codec references another artifact by path | phase 4, verification |

Risks:

- Releasing a lease before materializing reintroduces use-after-close in a new form. Materialize inside `try`, release in `finally`.
- Snapshots share resources; releasing a snapshot must decrement owners, never close directly.
- The exclusive lock is the only thing that makes online GC safe; any path that opens the store without it is a bug.
- The largest migration is hit identity, not the DSL.
- Distributed indexing, remote datasets, multi-process stores, Obsidian rendering and authentication are out of scope. Manual refresh and reindex status are built on the refresh operation, not as another refresh implementation.
