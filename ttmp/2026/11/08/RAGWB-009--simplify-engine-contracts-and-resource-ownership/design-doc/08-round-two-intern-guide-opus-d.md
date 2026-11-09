---
Title: Round two intern guide Opus D
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
Summary: "Independent round-two writing experiment candidate; see reference/06 for evaluation and unresolved defects."
LastUpdated: 2026-11-08T22:06:05.665151688-05:00
WhatFor: ""
WhenToUse: ""
---

# RAGWB-009: Consistent datasets, snapshots, operations and graph references

## 1. What this guide is for

rag-workbench lets a user describe a retrieval pipeline in a short JavaScript program, build it over a set of Markdown notes, and query it from a CLI, a served web app or a standalone HTML page. The same engine runs inside Go (through the Goja interpreter) and inside a browser.

The engine works, but several of its internal boundaries are ambiguous. A component can receive an array or a lazily loaded record view depending on whether its input came from disk. A query can still be running when a refresh closes the files it is reading. A build started for one HTTP request is cancelled when *that* request disconnects, even if other requests are waiting for it. A rename in the graph editor forgets some references. Twelve review findings on the open PR stack are symptoms of these boundaries.

This guide explains how the system works today, which mechanisms produce those findings, and a target design that removes their causes. It is written for an engineer who has not seen the earlier discussion and who will implement the change.

**Status.** Sections 5–10 are proposals; none of it is implemented. Sketches use TypeScript or Go, and names are suggestions unless a section says the choice is settled. Statements about current behavior come from reading source at runtime baseline `c267f91`. No tests were run to produce this guide, and some review findings were confirmed by reading the mechanism rather than by reproducing them; section 3 marks which.

**Compatibility.** There is no deployed API or data format to preserve. Callers, examples, fixtures and stored formats change in the same commit as the contract they use. There are no adapters, dual paths or legacy readers.

**What stays.** The fluent corpus/plan builders, one JavaScript engine for both hosts, the Markdown parser and section chunker, Bleve and the terms index, incremental (delta) build algorithms, provider implementations and their error codes, and the existing evaluation cases.

**Where the code is.** The runtime is authored in `pkg/ragjs/src/` and compiled by `pnpm build` into the committed `pkg/ragjs/assets/rag.js`, which Goja, browsers and exported pages all load. Edit `src/`, never the asset (`pkg/ragjs/src/README.md`). A first session:

```sh
pnpm install --frozen-lockfile
pnpm build && pnpm check
go test ./pkg/ragjs ./pkg/runner ./pkg/server ./pkg/artifacts ./pkg/natives
```

Then read `examples/apps/reports/lib/plans.js` and follow one query through `pkg/server/rag.go` → `pkg/runner/serve.go` → `pkg/ragjs/src/engine/session.js`.

## 2. How the system works today

### 2.1 Running example

The guide uses one pipeline throughout. It is the `adaptive` plan from `examples/apps/reports/lib/plans.js`, with an enrichment step added (enrichment syntax from `pkg/ragjs/enrich_decide_test.go`):

```js
const corpus = rag.corpus('reports')
  .from(rag.source.dir({ root: '/data/reports' }))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .enrich('kind', rag.enrich.decide({ questions: { topic: { type: 'choice', /* … */ } } }))
  .index('text', rag.bm25());

const adaptive = rag.plan('adaptive').over(corpus)
  .signals({ q: '', k: 8 })
  .classify({ id: 'route', questions: { needs: { type: 'noul', /* … */ } } })
  .retrieve('text', { k: 30, when: { decision: 'route.needs', min: 0.5 } })
  .pack({ budget: 30000 })
  .answer({ direct: true });

const lexical = rag.plan('lexical').over(corpus).signals({ q: '', k: 10 }).retrieve('text', { k: 20 }).limit();
```

The app is served with `ragwb serve --watch 30s`, so the server rechecks the notes every 30 seconds. `lexical` is a second plan over the same corpus, used by one of the app's verbs.

### 2.2 Plans compile to a graph of build and live nodes

Builders are immutable values. `rag.compile(plan)` turns them into an **IR** (intermediate representation, `version: 1`, graph.js:559): a list of nodes, each with an `id`, component type `t`, parameters `p`, input node IDs `in`, an output kind and a **phase**. `finalize` (`pkg/ragjs/src/graph/graph.js:405`) checks port types, the existence of references and cycles.

For `adaptive`:

| Node id | Type | Phase | Output | Notes |
|---|---|---|---|---|
| `files` | `source.dir` | build | files | reads the notes |
| `parse` | `parse.markdown` | build | docs | one record per note |
| `chunk` | `chunk.sections` | build | chunks | ids like `design#goals` |
| `enrich_kind` | `enrich.decide` | build | chunks | **same chunk ids**, added `kind` fields |
| `ix-text` | `index.bm25` | build | index | input is `enrich_kind` (the corpus cursor, graph.js:132-135) |
| `route` | `classify` | live, trigger `submit` | decision | one decision-model call per query |
| `text` | `retrieve` | live | hits | `when` reads `route.needs`; `uses: ['ix-text']` |
| `pack`, `answer` | … | live | context, answer | |

**Build nodes** run when documents change; **live nodes** run per query. Two facts here matter later. `chunk` and `enrich_kind` are two chunk outputs whose records share IDs but not bytes. And `retrieve('text')` refers to its index through the naming convention `'ix-' + p.index` (`components/builtins.js:180`), not through a stored node reference.

### 2.3 Build: digests, memo, store and lineage

`Engine.build(ir)` (`pkg/ragjs/src/engine/engine.js:147`) visits build nodes in order. For each node it computes a **digest**: a hash of type, parameters, input digests, file digests for sources and the source text of user-defined components (`digests`, engine.js:58). Because input digests are chained, editing one note changes the digest of `files` and of every build node downstream. The engine keeps an in-memory **memo** from digest to value.

With a Go artifact store, outputs are also saved under a **store key** (`hash([digest, def.src])`, `engine/records.js:86`). On a later build a stored output becomes a `Lazy` memo entry, loaded on first read. Loading a chunk or doc output in Go returns a **record view** (`recordsViewJS`, `pkg/natives/persist.go:437`): a JavaScript object over a disk-backed reader, with `get`, `has`, `order`, `values`, `toArray`, `size` and a hidden `__records` handle. Bleve indexes come back with a hidden `__ix` handle. Both handles hold open files.

A **lineage key** identifies "this node of this plan" independently of document contents (`lineages`, engine.js:81). After saving, the engine records `(lineage, storeKey, digest, input digests)` in a per-lineage list (`pkg/artifacts/lineage.go:98`). An incremental build searches that list for an output computed from the inputs' previous versions (`tryDelta`, engine.js:443) and asks the component to apply a delta. The Markdown parser and chunker do this by copy-on-write of the previous records directory, hard-linking unchanged shard files (`pkg/natives/delta.go:96`).

### 2.4 Artifacts: the merged view queries read

Queries do not read node outputs directly. `Engine.artifacts(ir)` (engine.js:556) builds one object, `art`:

- `art.chunks`: **all** chunk outputs merged by chunk ID into one map;
- `art.order`: each document's chunk order, from the first chunk node whose type is not `enrich`/`enrich.llm`;
- `art.indexes`: indexes by name;
- `art.docs`, `art.links`: computed on first use; the first document with a given ID wins.

`art` is cached in a single slot (`this._art`) keyed by the concatenated digests. Components read records with `ctx.chunk(id)`, which is `art.chunks.get(id)`.

### 2.5 Query: Session.run

`Session.run` (`pkg/ragjs/src/engine/session.js:37`):

1. Calls `engine.artifacts(ir)`. That recomputes digests, including source digests. If any output is missing (sources changed, or never built), it awaits `engine.buildShared(ir, …)` and calls `artifacts` again.
2. Runs live nodes in order. A node may await an LLM (`ctx.sample`), a decision (`ctx.decide`) or a reranker. Hits are normalized to `{ id, score, via, … }` (`components/hits.js:17`); a hit carries only a chunk ID.
3. Returns a result whose non-enumerable `chunk`, `top`, `explain` and `toJSON` methods close over `art` (`decorateResult`, session.js:361).

`art` is held across every await in step 2 and, through the result methods, after return.

### 2.6 Serving: one long-lived runtime per app

`runner.OpenApp` (`pkg/runner/serve.go:174`) creates one Goja runtime per app, evaluates a harness that builds the plan, and installs `globalThis.__serve`. All JavaScript for the app runs on the runtime's single **owner goroutine**; provider calls run on Go goroutines and post results back. JavaScript never runs concurrently, but two queries interleave at their awaits.

`__serve.run` (serve.go:134) runs `session.run`, then `collect()` (which calls `engine.gc` if the artifacts key changed), then **packs** the response: it reads `art()` *again*, copies chunk bodies for every ID mentioned in the outputs, and adds previous/next neighbors for the first 40 hits. `__serve.chunk(id)` and `__serve.doc(id)` serve browser cache misses from the current `art()`.

`__serve.refresh` rebuilds when sources changed and then runs `collect()`. `watchApp` (`cmd/ragwb/cmds/serve.go:169`) calls it on a ticker with a 10-minute timeout.

`engine.gc(irs)` (engine.js:410) keeps memo entries reachable from the app's plan and the eight most recently run plans (`noteIr`, engine.js:685), moves other values to `retired`, and closes the handles of values retired by the **previous** gc call. Retirement delays closing by exactly one more gc.

Go calls into the runtime through `ragjs.Eval` (`pkg/ragjs/await.go:15`). If the returned value is a promise, `Eval` polls it until it settles — but when the caller's context is cancelled it returns `ctx.Err()` immediately (await.go:60-62). The JavaScript work continues on the owner after the Go caller has returned.

### 2.7 HTTP and streaming

`POST /api/rag/{app}/run` (`handleRun`, `pkg/server/rag.go:227`) and `POST /api/rag/{app}/verbs/{verb}` (`handleVerb`, `pkg/server/verbs.go:209`) answer JSON or Server-Sent Events. In SSE mode the handler writes headers, starts a pump goroutine (`sseWriter.pump`, `pkg/server/sse.go:144`), hands a `streamSink` to the runtime, and when the operation returns closes the sink and waits for the pump. Verb requests pass a per-app semaphore (`acquire`, rag.go:52; default 8); direct runs do not. `web/app-shell/shell.js` adapts these responses to the GUI's host interface and caches the chunks it receives.

### 2.8 Native asynchronous calls

Three Go bridges return JavaScript promises for work done on goroutines: `newSample` (`pkg/ragjs/sample.go:67`), `newDecide` (`pkg/ragjs/decide.go`) and `rerankAsync` (`pkg/natives/rerank.go:108`). Each derives a context from the runtime lifetime, listens on the request's abort signal, runs the provider on a goroutine and posts a settle callback to the owner. They differ in when they classify the outcome.

### 2.9 Durable store layout

`pkg/artifacts/store.go` stores `<dir>/<ns>/<key[:2]>/<key>.json` (metadata) plus `<key>.bin`, `<key>.<codec>` or a `<key>.<codec>/` directory. The namespace `ns` hashes the Go component version, mode and the bytes of `rag.js` (`runner.ArtifactNamespace`, `pkg/runner/runtime.go:77`), so any change to the generated asset starts a fresh namespace. `Store.GC(keep)` (lineage.go:159) walks lineage files and deletes stored versions beyond the newest `keep`. `ragwb store gc` (`cmd/ragwb/cmds/store.go:96`) runs it from a separate process without coordination with a running server.

## 3. What goes wrong and why

| F# | PR / label | Symptom | Section | Evidence |
|---|---|---|---|---|
| F1 | #2 / P2 | stored duplicate chunks resolve differently from cold | 3.1 | mechanism confirmed in source |
| F2 | #2 / P2 | bake callback receives a record view | 3.1 | confirmed in source |
| F3 | #2 / P2 | store listing skips a dotted root | 3.3 | confirmed in source |
| F4 | #3 / P2 | two refreshes close artifacts under an awaiting query | 3.2 | confirmed in source |
| F5 | #3 / P2 | versions beyond 32 never collected | 3.3 | confirmed in source |
| F6 | #3 / P2 | delta-created readers miss cleanup | 3.2 | confirmed in source |
| F7 | #4 / P1 | non-reading SSE client holds a slot forever | 3.4 | mechanism deduced |
| F8 | #4 / P2 | cancelling first waiter cancels shared build | 3.4 | confirmed in source |
| F9 | #5 / P2 | deferred cleanup turns failure into cancellation | 3.4 | confirmed in `rerankAsync` |
| F10 | #6 / P2 | empty enabled-model list blocks fallback | 3.4 | confirmed in source |
| F11 | #7 / P2 | lexical export keeps references to removed decisions | 3.5 | mechanism deduced |
| F12 | #7 / P2 | rename misses decision references | 3.5 | confirmed, and broader |

Review sources: PRs [#2](https://github.com/go-go-golems/rag-workbench/pull/2)–[#7](https://github.com/go-go-golems/rag-workbench/pull/7). None of the findings was reproduced for this guide; each implementation phase starts with a failing regression.

### 3.1 Representation decides behavior

**F1.** In `artifacts()`, array outputs are merged with `Map.set`, so the *last* chunk node wins a shared ID. Record views are kept aside and combined by `unionChunks` (`records.js:30`), whose `get` prefers the array map and then the *first* view. Which record a query sees for `design#goals` therefore depends on which outputs happened to be loaded as views. In the example, both `chunk` and `enrich_kind` produce `design#goals`; a precedence rule, not the plan, decides whether the original or the enriched text goes into an answer.

**F2.** `resolveBakes` (engine.js:517) passes `outputs.get(firstChunksNode)` straight to the user callback: an array when cold, a `RecordView` without `map`/`filter` when warm. Its cache key, `hash([docs.map(d => d.id + d.sections.length), chunks.length])`, treats inputs with equal counts and different text as identical.

Both come from one defect: there is no single collection contract, and a record's identity is an ID without its dataset.

### 3.2 Lifetimes are not owned

**F4.** A query captures `art` and awaits `route`'s decision call. Refresh 1 builds; its `gc` retires the old values. Refresh 2's `gc` closes them. The query resumes and reads a closed reader.

**F6.** `persist.go` `load` registers each opened reader with `addCloser` (persist.go:209, 227, 245). Delta builds open their result through `cowOpen` → `OpenRecords2` (delta.go:101) and wrap it with `ctx.store.view` (`natives/markdown_native.go:38, 56`) without registering it. Separately, `addCloser` silently becomes a no-op when the host provides none (persist.go:47; `pkg/ragjs/module.go:143`).

**Not in the review, found in source:** `__serve.run` packs chunk bodies from `art()` *after* the query and after `collect()`. If a refresh landed during the query, old hit IDs are resolved against the new generation: a removed chunk vanishes from the response; a changed chunk shows new text beside old scores. `evaluation/evals.js:87` captures `art` once and then runs many cases that may each see a different generation. Fixing F4 alone does not fix this; packaging must use the query's generation.

### 3.3 A search index serves as the inventory

**F5.** `LineageRecord` keeps the newest 32 entries and drops the rest from the file (lineage.go:110). `GC` deletes only entries it finds in lineage files, so a dropped entry's payload is never deleted. Outputs of nodes with no lineage key are never collected.

**F3.** `List` skips every directory whose name contains `.`, to avoid descending into `<key>.<codec>/` payloads (store.go:282). The walk root is also a directory, so a store at `~/.cache/ragwb` lists nothing. Local bug, local fix.

Also from source: `writeAtomic` renames without `fsync` (atomic for concurrent readers, not durable across power loss), and `GC` deletes temp build directories older than an hour without knowing whether a running process owns them.

### 3.4 Operations have the wrong owner

**F8.** `buildShared` (engine.js:695) starts `build(ir, opts)` with the first caller's `signal`, `onProgress` and `onEvent`, and hands the same promise to later callers. If request A disconnects, its signal stops the build request B is also waiting for; B only ever receives `{type:'build', waiting:true}`. The key also omits options that change the result (`calls`, `full`, `force`, `persist`).

**F9.** `rerankAsync`'s goroutine does `defer cancel()` and posts its settle callback. The callback runs later on the owner and tests `callCtx.Err() != nil`, which is by then always true, so a provider failure is reported as `cancelled` (rerank.go:145-170). `newSample` and `newDecide` classify before posting and are not affected — evidence for the fix, and for sharing one implementation.

**F7.** `sseWriter.write` calls `fmt.Fprintf` and `Flush` with no write deadline and ignores flush errors; `pump` ignores write errors. When a client stops reading, the TCP send buffer fills and `Fprintf` blocks. The sink's back-pressure rule (`streamSink.Emit` waits up to 100 ms on a full queue, on the owner goroutine, then cancels the request) stops the operation, and `Invoke` returns because `Eval` honours the cancelled context. But `handleVerb` then waits on `<-done` for the blocked pump, never returns, and never runs `defer a.release()`. After eight such clients the app answers every verb with 503.

A related gap: when `Eval` returns early on cancellation, the semaphore slot is released while the JavaScript operation is still running on the owner. Admission therefore counts handlers, not work.

`handleRun` and `handleVerb` duplicate the stream lifecycle with different terminal behavior, and only verbs are admitted.

**F10.** `preferred` (`pkg/llm/gepp/backend.go:114`) treats an empty `EnabledModels` as "all enabled" for the default model, then iterates the empty `EnabledModels` for the fallback. Local fix.

### 3.5 Graph references are discovered in several places

**F12.** `applyOp('rename')` (graph.js:877) rewrites `in` and `outputs` only. It misses `when.decision` strings, the `p.index`/`p.indexes` parameters behind `uses` (renaming `ix-text` breaks `retrieve('text')`), and decision reads inside lambda source. It does not check that the new ID is free.

**F11.** `exportHtml` (`export/html.js:4`) removes every submit-triggered live node except answers. `classify` is submit-triggered (builtins.js:637), so `route` is removed; `remove` checks only `uses` dependents (graph.js:841), not `when` readers, so `retrieve('text')` still reads `route.needs` and `finalize` rejects the graph. The exporter also bundles `engine.files()`, which holds only `rag.files(...)` inputs, not `source.dir` notes.

The common cause: `finalize`, `applyOp` and the exporter each enumerate references their own way.

## 4. Target design at a glance

### 4.1 Entities

| Entity | What it is | Identity | Owned by |
|---|---|---|---|
| **Dataset** | Read-only documents or chunks produced by one build node | `dataset.id` = that node's build digest | the snapshots and build scopes that include it |
| **Record reference** | `{ dataset, id }` | both fields | a plain value |
| **Index** | Searchable structure over exactly one chunk dataset | build digest; `index.dataset` names its input | snapshots |
| **Resource** | An open native handle (record reader, Bleve index) | registry id (section 6.2) | the per-runtime **registry** |
| **Snapshot** | Immutable set of *all* build outputs of one plan — datasets, indexes and other values (files, synonyms, baked values) | `snapshot.id`, plus its `contentKey` | the snapshot table, one slot per `planKey` |
| **Lease** | One operation's hold on one snapshot | — | that operation |
| **Build job** | One in-progress build of a `contentKey` | `contentKey` + semantic options | the job table; waiters hold interest |
| **Operation** | One request-level unit of work with one terminal outcome | operation id | its caller |

Two keys are used throughout and must not be confused:

- **`planKey`** identifies a plan's build structure — node types, parameters and wiring, without document contents. It is what today's lineage keys compute per node. It selects the *snapshot slot*, so a new generation of the same plan replaces the previous one in the same slot.
- **`contentKey`** identifies built content: the plan's build digests. Two requests that need the same content share one job. Each snapshot records the `contentKey` it was built from.

Dataset ids (build digests) are what references carry. A store key (`hash(digest, component source)`) is where a stored dataset's bytes live; a memory dataset has no store key.

### 4.2 When snapshots share resources

Snapshots of *different plans over the same corpus* share datasets and indexes. Successive *generations of one plan* usually share nothing, because editing a note changes the `files` digest and therefore every downstream digest.

```text
planKey adaptive ─ current: snapshot A7 (contentKey c7)
                     files, parse@p7, chunk@k7, enrich_kind@e7, ix-text@i7
planKey lexical  ─ current: snapshot L7 (contentKey c7')
                     files, parse@p7, chunk@k7, enrich_kind@e7, ix-text@i7   (same digests)

registry
  reader(chunk@k7)   owners: A7, L7
  bleve(ix-text@i7)  owners: A7, L7
  (enrich_kind@e7 is a memory dataset: no resource)

after a note edit and refresh of adaptive:
  adaptive ─ current A8 (all new digests); A7 retired, leases 1 (query q42)
  lexical  ─ current L7 (until its own refresh)
  reader(chunk@k7) owners: A7, L7 → stays open until both release it
```

A hit from `retrieve('text')` in A7 is `{ ref: { dataset: 'e7', id: 'design#goals' }, score }`. Resolving it returns the enriched record from `enrich_kind@e7`. No rule chooses between `chunk` and `enrich_kind`.

### 4.3 Invariants

1. A record reference names its dataset; resolving it never consults another dataset.
2. Memory versus disk changes cost, not behavior.
3. An operation reads exactly one snapshot from its first read through its packaged result.
4. Every opened native resource is adopted by the registry where it is opened.
5. A resource closes only after its last owner releases it.
6. The durable inventory is descriptors and snapshot manifests; lineage lists only accelerate base search.
7. Cancelling one waiter never cancels a build another waiter still needs.
8. Every asynchronous operation has exactly one terminal outcome, fixed before cleanup runs.
9. Every network write and flush has a deadline; a transport failure ends only that transport.
10. Every structural graph reference is enumerated and rewritten by one visitor.
11. Ordinary application code never calls close or release.
12. Each phase deletes the code it replaces.

These hold by construction — private handles, single constructors, one visitor — and by tests. JavaScript does not check them statically, and trusted native code can still violate them.

### 4.4 Module boundaries

```text
DSL builders, GUI, CLI, HTTP handlers
             │
Graph: compile, reference visitor, edits, projections
             │
Application operations: build/refresh, query, lookup, evaluate
       │                         │
Build jobs                  Query executor
       └──────── Snapshots ──────┘
                    │
          Datasets and indexes
                    │
   Resource registry + artifact store

Native bridge (sample/decide/rerank) ─▶ one outcome + events
HTTP respond helper ─▶ JSON or bounded SSE
```

No workflow engine, plugin framework or dependency injection. A new concept is justified only if it replaces at least one existing mechanism; each row of section 11 names what it replaces.

## 5. Datasets and record references

### 5.1 The collection contract

Every docs or chunks output becomes a `Dataset`, whether computed in memory or opened from disk. Access stays synchronous; both hosts are local, and an asynchronous `get` would be speculation about remote storage.

```ts
// Proposed: pkg/ragjs/src/engine/datasets.ts
export interface RecordRef { readonly dataset: string; readonly id: string }

export interface Dataset<R> {
  readonly id: string;                 // producing node's build digest
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): R | null;           // frozen record, or null
  values(): IterableIterator<R>;       // deterministic production order
  materialize(): readonly R[];         // explicit full read
  ref(id: string): RecordRef;
}

export interface ChunkDataset extends Dataset<Chunk> {
  readonly documents: string;          // id of the documents dataset its docIds refer to
  order(documentId: string): readonly string[];
}

export interface Index {
  readonly id: string;
  readonly dataset: string;            // the ChunkDataset it was built from
  search(query: string, k: number, opts?: SearchOptions): Hit[];
}

export interface Hit { readonly ref: RecordRef; score: number; via?: string; reason?: string }
```

Two implementations: `MemoryDataset` over an array (browser, and Go cold builds) and `StoredDataset` over a registry handle (replacing `RecordView`). Records handed to callbacks are deeply frozen or freshly copied, including nested `meta`, so a callback cannot change what the next reader sees. Components never see `close`, `__records`, `__ix` or a codec.

`order` belongs to each chunk dataset. A one-to-one enrichment keeps its input's order. This removes the "primary stream" guess in `artifacts()`, which today does not exclude `enrich.decide`.

Index identity is its build digest, which already covers component, normalized parameters and input dataset id; codec version is covered by the namespace.

### 5.2 How components and results find records

`retrieve` asks its index, which knows its dataset, so hits carry full references. The component context gains three methods and loses the global ones:

| New | Replaces | Meaning |
|---|---|---|
| `ctx.resolve(ref)` | `ctx.chunk(id)` | record from the referenced dataset, or null |
| `ctx.dataset(nodeId)` | `ctx.chunks()`, `ctx.docs()` | an explicit dataset of the current snapshot |
| `ctx.neighbors(ref, {before, after})` | `ctx.neighbors(id)` | refs adjacent in that dataset's `order` |

About fifteen `ctx.chunk(h.id)` call sites in `components/builtins.js`, plus `hits.js`, `events.js`, `session.js` and `export/html.js`, move to `ctx.resolve(hit.ref)`. Traces, evidence labels, events, evaluation output and the GUI carry `ref` and never reduce it to an ID.

A chunk's `docId` is resolved against `chunkDataset.documents`. With several sources in one plan, the logical document ID is source identity plus vault-relative path; titles and slugs are never assumed unique.

#### Decision record D1 — hits carry `ref`, not `id`

- **Context:** chunk IDs repeat across enrichment datasets.
- **Alternatives:** (a) keep `hit.id` and add `hit.dataset`; (b) replace with `hit.ref`; (c) prefix IDs with the dataset to make them globally unique.
- **Decision (proposed):** (b). Code still reading `hit.id` fails visibly instead of silently resolving the wrong record. (c) changes user-visible IDs and still needs parsing.
- **Consequences:** every hit consumer, event payload and GUI renderer changes. Display code uses `hit.ref.id`.

### 5.3 Combining rankings

`fuse.rrf` deduplicates by exact reference. Fusing two indexes over the same dataset behaves as today. Fusing hits from different datasets (an index over `chunk` and one over `enrich_kind`) needs an explicit rule, because `design#goals` in each is a different record.

The compiler knows each hits stream's dataset statically: `retrieve` → its index node → that node's chunk input. A custom `retrieve(fn)` must declare its dataset or return full refs.

#### Decision record D2 — fusing across datasets

- **Context:** today "last stream wins" silently picks a version.
- **Alternatives:** (a) compile error unless the plan inserts a projection; (b) a `fuse` option `canonical: 'enrich_kind'` that maps foreign refs by ID; (c) keep both versions as separate hits.
- **Decision (proposed):** (a) by default, with (b) as the one built-in projection. Implement in `fuse` and a small `project` component, not in the engine.
- **Status:** open for owner confirmation. It blocks only plans that fuse across datasets; check the examples during phase 2.

### 5.4 Bakes, build callbacks and cache keys

Bake and custom build callbacks receive datasets:

```js
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id));
rag.bake(ctx => ctx.chunks.size);   // counts without a full read
```

Today `ctx.chunks` means "the first chunks node in the IR". Proposed: a bake declares its input (`{ from: 'enrich_kind' }`), defaulting to the corpus cursor where the bake was added (the builder already tracks it, `this.st.cursor.chunks`, graph.js:125). The compiler records it as an ordinary input, so it takes part in digests and in the reference visitor.

The bake cache key becomes `hash([input dataset ids, bake source, component version])`. A dataset id is a build digest, so changed text with equal counts changes the key.

Memory and stored datasets pass one conformance suite, run in Goja and in Node: iteration order, missing IDs, frozen nested records, `materialize`, `order` and refs.

## 6. Snapshots, resources and results

### 6.1 Snapshot lifecycle

```text
                 job publishes
   (empty slot) ───────────────▶ current ──(newer publish in slot)──▶ retired
                                    │                                    │
                         leases acquired/released             last lease released
                                                                         ▼
                                                     released: Release() on each handle
```

1. A build job writes into a private **build scope**. Each resource it opens is adopted into that scope.
2. On success, **publish**: create the snapshot, transfer the scope's handles to it, make it current in its `planKey` slot, and mark the previous current snapshot retired. Publish is one synchronous step on the owner goroutine (for a durable store it includes the synchronous file operations of section 7.3).
3. An operation calls `acquire(planKey)`, which returns the current snapshot and increments its lease count in the same synchronous step. Acquire and publish both run on the single owner goroutine with no await between selecting and retaining, so a publish cannot fall between them. The owner goroutine is the arbiter.
4. When a retired snapshot's lease count reaches zero, it releases its handles. A resource closes when no snapshot or build scope owns it.
5. A failed or cancelled build discards its scope, which releases what it opened, and leaves the slot's current snapshot unchanged. A partial build output may be kept privately for resumption but is never current.

F4 with the new rules (resources named as in section 4.2):

```text
q42: acquire A7 (leases 1) ── awaits route decision ─────────────── resumes, materializes from A7, release
refresh 1: job c8 → publish A8; A7 retired (leases 1)
refresh 2: job c9 → publish A9; A8 retired (leases 0) → A8 releases its handles → closed
                                                     q42 release: A7 leases 0 → releases handles
                                                     reader(chunk@k7) still owned by L7 → stays open
```

### 6.2 The resource registry

The registry is in Go, one per runtime: the handles are Go objects, and runtime shutdown must close them from Go.

```go
// Proposed: pkg/resources
type ID string

type Registry struct {
    mu      sync.Mutex // shutdown runs off the owner goroutine
    entries map[ID]*entry
    closed  bool
}
type entry struct{ c io.Closer; owners int }

// Adopt registers c with one owner. If id exists, that entry gains an owner
// and c is closed, so one id never has two underlying handles.
func (r *Registry) Adopt(id ID, c io.Closer) (Handle, error)
func (r *Registry) Retain(id ID) error
func (r *Registry) Release(id ID) error         // closes when owners reaches 0
func (r *Registry) Close(ctx context.Context) error
```

Every opener has the same shape:

```go
rd, err := OpenRecords2(dir)
if err != nil { return nil, err }
h, err := scope.Adopt(resources.ID(storeKey), rd)
if err != nil { _ = rd.Close(); return nil, err }
return newStoredDataset(vm, h), nil
```

**Ids.** A handle opened from the store uses its store key, so two loads of the same payload share one handle (adopting a duplicate closes the redundant one; rejecting would break legitimate repeated loads). A handle created by a build (a fresh Bleve index, a delta output in a temp directory) gets a generated id and keeps it for life, including after `save` relocates its payload into the store (persist.go:136-158).

**Who counts what.** Leases are a JavaScript integer on the snapshot. Registry owner counts are Go integers per handle. Publishing a snapshot calls `Retain` once per handle it holds (the build scope's adoption count transfers to the first snapshot); releasing a snapshot calls `Release` once per handle. Query code touches neither.

**Every open path.** `persist.go` `load`, the delta callbacks in `markdown_native.go`, fresh builds in `bm25_native.go` and the base opened by `tryDelta` all adopt. The `AddCloser` no-op fallback goes: constructing a store-backed runtime without a registry is an error. A browser runtime opens no native resources; its registry simply never receives entries.

**Shutdown order.** Reject new operations; abort active ones; wait for them to settle, with a deadline; release all snapshots; `Registry.Close`. If the deadline passes, report the operations still running and do not close resources they can reach; never report a clean shutdown while a Go worker can still use a closed handle.

### 6.3 Queries and detached results

```ts
// Proposed sketch: pkg/ragjs/src/engine/session.ts
async function query(plan: Plan, signals: Signals, req: Request): Promise<Result> {
  await jobs.ensure(plan, req);                 // section 8.3 decides when this waits
  const snap = snapshots.acquire(plan.planKey); // synchronous; throws if the slot is empty
  try {
    const run = await executor.run(plan, signals, snap, req);
    return materialize(run, snap, req.limits);  // copies the records the result needs
  } finally {
    snapshots.release(snap);                    // success, error, cancellation, limit error
  }
}
```

`materialize` produces a **detached result**: plain data plus the records needed by its hits, trace entries, evidence and neighbor preview, grouped by dataset:

```json
{
  "snapshot": "A7",
  "hits": [{ "ref": { "dataset": "e7", "id": "design#goals" }, "score": 7.1 }],
  "records": { "e7": { "design#goals": { "title": "Design", "headingPath": ["Goals"], "text": "…" } } },
  "neighbors": { "e7": { "design#goals": { "prev": "design#intro", "next": "design#risks" } } },
  "trace": [ "…" ],
  "answer": { "…": "…" }
}
```

`result.record(ref)`, `result.top(n)` and `result.explain(ref)` read only this data. The result needs no close, stays valid after the lease is released, and is the object the server serializes; the harness's separate `pack` step disappears. The neighbor preview keeps today's bound (first 40 hits).

Copy only what the result references, not the corpus. If a declared limit would be exceeded (say, a trace with `detail.chunks` on a large `k`), fail with `result_too_large` naming the limit; never return a hit without its record.

Evaluations and whole-corpus export run under one lease through an internal `snapshots.withLease(planKey, fn)`, so all evaluation cases see one generation.

### 6.4 Browser lookups after a query

The shell fetches chunks that were not in the preview (`GET /api/rag/{app}/chunk`). The request carries the result's `snapshot` id and a `ref`. The server resolves against that snapshot only if it is still current or still leased; otherwise it answers `409` with code `stale_generation` and the shell re-runs the query. It never resolves an old reference against a newer dataset. The shell keys its cache by snapshot and clears it when a response carries a new snapshot id.

This rule is deliberately simple: right after a refresh, every lookup for the previous generation is stale, even if its query finished milliseconds earlier. The neighbor preview makes such lookups rare. A refresh cannot revoke text already delivered, and generation retention is not access control.

## 7. Durable store: inventory, ownership, collection

### 7.1 What is authoritative

Keep the existing payloads and codecs. Change authority:

- **Descriptor** — the existing `<key>.json`, extended with `digest`, `lineage` and `inputs` (input digests). It owns exactly its payload path.
- **Snapshot manifest** (new, immutable) — `snapshots/<planKey>/<snapshotId>.json`, listing each build node's store key and the snapshot's `contentKey`.
- **Current pointer** (new) — `snapshots/<planKey>/current`, replaced atomically.
- **Lineage lists** — a cache rebuildable from descriptors; never consulted to decide deletion.

Records2 copy-on-write hard-links shard files, but each artifact owns its own directory entries; removing one artifact's links leaves the other's files intact, so hard links need no dependency tracking. If any codec references another artifact by *path*, its descriptor must list that dependency and the collector must follow it (verify in phase 4, O7).

`List` walks the defined layout (`<ns>/<shard>/<key>.json`) instead of skipping dotted names, which fixes F3. No separate database is needed for an inventory the descriptors already express.

### 7.2 One writer per namespace

One process owns a writable namespace for its lifetime, holding an exclusive OS lock (`flock` on `<ns>/.lock`). It runs builds, publication, leases and online GC. Another process that opens the namespace for writing fails with a message naming the owner. `ragwb store gc` takes the same lock, so it runs only while no server uses the namespace; a separate process cannot see in-memory leases, and pretending otherwise is the cross-process hazard the lock prevents. Cross-machine leases and distributed collection are out of scope.

The browser's memory store implements the same logical contract (descriptors, manifests, current pointer, collection) without locking or files.

### 7.3 Publication and crash behavior

```text
1. Write payloads into the namespace staging directory owned by this build scope.
2. Close writers; validate counts and codec metadata.
3. Rename payloads to final paths; fsync the parent directories.
4. Write descriptors atomically (temp file, fsync, rename, fsync directory).
5. Write the snapshot manifest the same way.
6. Replace the current pointer the same way.
7. Update lineage lists (best effort).
```

A crash before step 6 leaves the previous snapshot current; orphaned payloads and descriptors are collected later. A crash after step 6 can leave lineage stale, which only costs an incremental base. Today's `writeAtomic` lacks fsync; add it here. Rename atomicity alone does not prove power-loss durability; test the sequence on Linux, the platform for which durability is claimed.

### 7.4 Collection

**Mark** artifacts named by current manifests, by retained older manifests (policy O4), by snapshots with leases in the owning process, and by the owner's live build scopes. **Sweep** unmarked descriptors and then their payloads, and staging directories that no live scope owns. The owner serializes publication and mark/sweep on one mutex, so an artifact cannot be swept between becoming durable and being published. A missing or unreadable descriptor stops the sweep with an actionable error; unknown files are reported, never assumed safe to delete. Report logical bytes and reclaimed bytes separately (`unlinkedSize` already accounts for hard links). Retention is a count or age policy, never tied to query duration.

## 8. Operations: native calls, shared builds and HTTP streams

### 8.1 One bridge for native asynchronous calls

```go
// Proposed: pkg/ragjs/opbridge
type Spec[T any] struct {
    Name     string
    Execute  func(ctx context.Context, emit func(Event)) (T, error)
    Encode   func(T) ([]byte, error)   // results cross goroutines as bytes, never Goja values
    Classify func(err error) Failure   // provider-specific error codes
}

// Start returns a promise. It must be called on the owner goroutine.
func Start[T any](vm *goja.Runtime, tasks *TaskGroup, signal goja.Value, spec Spec[T]) goja.Value
```

`TaskGroup` is new: a per-runtime group of worker goroutines (errgroup-style) that shutdown waits on. Today `sample` and `decide` start bare goroutines and `rerank` uses a local `WaitGroup`.

Lifecycle:

1. On the owner: create the promise; derive `ctx` from the runtime lifetime; attach an abort listener. If the signal is already aborted, reject with `cancelled` and stop.
2. On a goroutine in `tasks`: run `Execute(ctx, emit)`. `emit` posts events to the owner.
3. When `Execute` returns, the goroutine computes the outcome — success (encoded), provider failure (`Classify`), deadline, or runtime shutdown — as a value, *before* anything cancels `ctx`.
4. A single `settle`, guarded by `sync.Once`, decides between step 3 and an abort. Whichever reaches it first is the outcome. An abort that wins rejects with `cancelled` at once and cancels `ctx`; the worker's later result is discarded, but the worker still runs its cleanup and `tasks` still waits for it.
5. Post one callback to the owner to resolve or reject, and remove the abort listener. If the runtime is closing, the post may be dropped; resource cleanup does not depend on it.

Cancellation, completion and delivery stay distinct: rejection on abort tells JavaScript the operation is over for it; the worker's exit tells the task group the goroutine is done; delivery of the promise callback depends on the runtime still running.

```ts
// Consumers see one discriminated union.
type Outcome<T> =
  | { kind: 'ok'; value: T }
  | { kind: 'cancelled' }
  | { kind: 'deadline' }
  | { kind: 'failed'; code: string; message: string }
  | { kind: 'shutdown' };
```

`sample`, `decide` and `rerank` keep their request conversion, JSON validation and error codes as `Execute` and `Classify`. Tool calls in `sample` go through a `CallOnOwner(ctx, fn) ([]byte, error)` helper so no goroutine touches a Goja value; once the operation has settled, a tool request is answered with an error without running the tool (today sample.go:119 posts tool calls unconditionally).

### 8.2 Shared build jobs

`buildShared` becomes a small job table in `pkg/ragjs/src/engine/jobs.ts`. It is not a durable scheduler.

- **Key:** `contentKey` plus every option that changes results (`calls`, `full`, `force`, `persist`).
- **Job:** its own cancellation scope linked to engine shutdown; a build scope (section 6.1); a set of waiters; an event fan-out.
- **`ensure(plan, req)`** adds a waiter with the request's signal and event sink, and resolves when the job publishes (rejects when it fails).
- A waiter's abort removes only that waiter. When the last waiter leaves, the job is cancelled and its scope discarded.
- A waiter arriving while the job is cancelling waits for it to end, then starts a fresh job.
- Progress events go to every waiter's sink, tagged with the job id. Nothing is attached to the first request's scope.

A refresh is an ordinary waiter whose signal fires on its own timeout or on engine shutdown. No separate "background owner" concept is needed.

```text
A ─wait─┐
        ├─▶ job J(c8) ─▶ build scope ─▶ publish A8
B ─wait─┘
A aborts:  A detached; J continues for B.
B aborts:  no waiters → J cancelled, scope discarded, A7 stays current.
C arrives while J cancels: C waits for J to end, then starts J'(c8).
```

A slow subscriber must not stall the job. Today `streamSink.Emit` waits up to 100 ms on the owner goroutine before cancelling its request (sse.go:61-68). Make overflow immediate: a full queue cancels that request at once, which detaches that waiter only.

### 8.3 When does a query wait for a build?

Today a query recomputes digests, including source digests (subject to `SourceDigestTTL`), and waits for a build whenever anything changed. The original design asks that a served app's background refresh not block queries against the last good snapshot. With `planKey` slots that is possible: the query acquires the slot's current snapshot even when its `contentKey` is older than the latest sources.

#### Decision record D3 — query freshness

- **Context:** interactive workbench use expects an edit to show up on the next query; `serve --watch` should not stall queries behind rebuilds.
- **Alternatives:** (a) always wait for current contents (today); (b) use the slot's current snapshot whenever one exists; (c) choose per host.
- **Decision (proposed):** (c). `rag.run`, the browser workbench and `serve` without `--watch` use (a); `serve --watch` uses (b) and relies on the watcher. With (a), `ensure` waits only when the slot's current `contentKey` differs from the plan's.
- **Status:** open for owner confirmation. It changes only `jobs.ensure`.

### 8.4 One HTTP response path

```go
// Proposed: pkg/server/respond.go
type Operation func(ctx context.Context, sink runner.EventSink) (json.RawMessage, error)

func (s *RagServer) respond(w http.ResponseWriter, r *http.Request, a *RagApp,
    start json.RawMessage, op Operation)
```

`respond` admits through one per-app semaphore shared by direct runs and verbs, chooses JSON or SSE, runs the operation and writes at most one terminal response. `handleRun` and `handleVerb` keep only parameter parsing and the body of `op`. Both use one terminal vocabulary: `busy`, `bad_request`, `bad_params`, `failed`, `cancelled`, `slow_client`, replacing today's `run_failed`/`verb_failed` split.

The SSE writer:

- One pump goroutine performs every write and flush: headers, pings, events and the terminal event.
- Before each write and flush it calls `http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout))` and uses the controller's error-returning `Flush`. Middleware wrapping the `ResponseWriter` must implement `Unwrap` so the controller reaches the connection. If the deadline cannot be set, fail the request before streaming — streaming without a bound is not allowed. After a write or flush error the stream is finished; an exceeded deadline cannot be extended.
- Coalesced text is flushed when it reaches a byte limit as well as on the 30 ms timer; heartbeats count against the same limits.
- At most one terminal event. A failed network is not promised one.

Ending a request, step by step:

1. A write error, flush error or queue overflow stops the pump and cancels the request context.
2. The cancellation reaches the operation's abort signal; the bridge rejects pending native calls promptly (8.1), and the JavaScript operation settles.
3. The Go caller waits for that settlement before returning. Today `ragjs.Eval` returns on `ctx.Done()` without waiting (await.go:60-62); change it to keep polling the promise for a bounded grace period after cancellation, and report a timeout explicitly if it does not settle (O8).
4. `respond` waits for the pump, whose blocked write is bounded by the write deadline.
5. Only then is the semaphore slot released. The slot now counts work, not handlers.

Test with a real TCP connection whose client stops reading, and assert that the slot becomes available within `writeTimeout` plus the grace period.

## 9. Graph references, edits and export

### 9.1 One reference visitor

Each component declares which parameters hold structural references. The compiler normalizes concise DSL forms into a canonical IR once, and `references(node)` is *computed* from those declarations rather than stored separately:

```js
// DSL (unchanged)
.retrieve('text', { k: 30, when: { decision: 'route.needs', min: 0.5 } })

// Canonical IR node (proposed)
{ id: 'text', t: 'retrieve', in: ['q'],
  p: { index: { node: 'ix-text' }, k: 30 },
  when: { source: { node: 'route', output: 'needs' }, minimum: 0.5 } }

// references(node) →
// [ { kind: 'data', node: 'q' },
//   { kind: 'artifact', node: 'ix-text', at: 'p.index' },
//   { kind: 'decision', node: 'route', output: 'needs', at: 'when.source' } ]
```

The `'ix-' + name` convention becomes a normalization step in the compiler; the canonical reference names the node. The index's display name (`p.name` on the index node, used by `ctx.index(name)` in lambdas) stays a separate parameter. `synonyms` becomes an ordinary declared reference instead of the special case at graph.js:455.

`finalize`, `applyOp` and projections all use `references(node)` and `rewriteReferences(node, mapping)`. Existence, output types, phases and cycles are checked from the same enumeration.

Edits:

- **rename** checks that the new ID is free, rewrites every reference through the visitor, and validates once at the end of the edit batch.
- **remove** rejects when any reference points at the node, unless the op says `cascade: true` or names a replacement. The error lists the referencing nodes and reference kinds. Guessing how a removed decision should affect a production query is not allowed.

**Lambdas.** Arbitrary JavaScript cannot be renamed by text replacement. A lambda that reads a decision declares a binding in its options (`deps: { route: 'route' }`) and calls `ctx.decision('route')` through it; rename changes the binding's target, not the source. A lambda whose source calls `ctx.decision('x')` without a declared binding is a compile error. The existing regex in `whenRefs` survives only to produce that error.

### 9.2 Export as an explicit projection

Components declare capabilities: `model` (LLM, decision, rerank calls), `host-fs`, `native-index`. Submit-triggered is no longer used as a proxy for "unavailable in a page". `exportHtml` applies a documented projection:

1. Remove components whose capabilities a standalone page lacks.
2. For each removed decision node, drop the `when` conditions that read it; those nodes run unconditionally. This makes today's intent explicit.
3. Remove `answer` and `context` nodes; rewire hits consumers as `remove` does today.
4. Validate the projected graph; on failure, list the unsupported nodes.
5. Bundle the `files` output from the current snapshot instead of `engine.files()`, so `source.dir` corpora export. The page embeds every note's text, as today.

Acceptance: exporting `adaptive` yields a page whose lexical search returns hits.

## 10. Local fixes

- **F3:** `artifacts.List` traverses the layout (7.1). Test with a store root named `x.store`.
- **F10:** in `preferred`, when `EnabledModels` is empty, iterate eligible model refs in sorted order. Test with an empty list and an ineligible default model.

## 11. What moves and what is deleted

| Area | New home (proposed) | Replaces / deletes |
|---|---|---|
| Datasets, refs | `src/engine/datasets.ts` | `unionChunks`, `isView`, `asArray`, `Outputs` (records.js); `recordsViewJS` (persist.go) |
| Snapshots, leases | `src/engine/snapshots.ts` | `Engine.retired`, gc retirement, the `_art` cache, the `artifacts()` merge |
| Detached results | `src/engine/results.ts` | `decorateResult`; `pack` in the serve harness |
| Build jobs | `src/engine/jobs.ts` | `buildShared` |
| Graph references | `src/graph/references.ts` | per-op reference handling in `applyOp`; `whenRefs` discovery |
| Registry | `pkg/resources` | `AddCloser` no-op fallbacks for store runtimes; direct `close()` in engine code |
| Native bridge | `pkg/ragjs/opbridge` | three hand-written lifecycles |
| HTTP | `pkg/server/respond.go` | duplicated pump and terminal code in `rag.go`/`verbs.go` |
| Store | manifests, pointer and lock in `pkg/artifacts` | lineage-driven `GC` |

Remove obsolete record formats and codecs only after enumerating live references; the v1 `records` file codec path (persist.go:200-217) is the first candidate to check. Keep memory implementations used by the browser and by quality comparisons; a uniform interface does not justify deleting a useful backend.

New contracts are strict TypeScript, with declarations beside their implementation, per `pkg/ragjs/src/README.md`. `pnpm check` verifies types, Biome, generated bytes and the acyclic import graph. None of these proves runtime input validity or resource lifetimes; the tests in section 13 do.

## 12. Migrating callers

- Plans written with the builders stay as they are.
- `rag.run` and `result.top` stay because they are good interfaces. `result.chunk(id)` becomes `result.record(ref)`; there is no fallback accepting a bare string. Low-level callers that need a ref use `dataset.ref(id)`.
- Custom components move to `ctx.resolve`, `ctx.dataset` and `ctx.neighbors(ref)` (5.2).
- Bake callbacks receive datasets (5.4).
- GUI, app shell, traces, evidence rendering, evaluation output, sweeps, saved plans, example presets and export move to refs in the same change.
- Bump the IR `version` to 2 and reject other versions when loading saved plans; regenerate fixtures.
- The artifact namespace changes automatically with the asset. Document `ragwb store prune` for removing old namespaces; never delete user directories implicitly.

## 13. Implementation phases

Each phase is a vertical change with tests that deletes the code it replaces. Use one integration branch for the cleanup rather than patching each PR of the stack separately.

1. **Regressions and local fixes.** A failing test per finding (cold, stored and delta paths for F1/F2; equal-count changed input for bakes). Fix F3 and F10. If the stack will be used before phase 6, land the bounded SSE writer now, in its final place in `respond.go`.
2. **Datasets and refs.** `MemoryDataset`, `StoredDataset` and the conformance suite; indexes record their dataset; hits carry refs; migrate every consumer; delete `unionChunks` and record views. In this phase `artifacts()` becomes a map of datasets and indexes by id with `resolve(ref)`; it is the object phase 3 turns into a snapshot.
3. **Snapshots, registry, detached results.** Build scopes, publish, `planKey` slots, leases, registry adoption on every open path, detached results, snapshot-aware lookups, evaluation under one lease. Delete gc retirement and `pack`.
4. **Durable inventory.** Descriptors, manifests, current pointer, namespace lock, mark/sweep, fsync. Lineage becomes a cache.
5. **Operations.** `opbridge` and `TaskGroup` for sample/decide/rerank; job table; freshness policy D3; immediate sink overflow.
6. **HTTP.** `respond` for both endpoints; shared admission; deadlines; byte-bounded coalescing; `Eval` waiting for settlement after cancellation.
7. **Graph.** Reference visitor, canonical IR, rename/remove, lambda bindings, capability export.
8. **Integration.** Delete dead code; update docs, help and examples; exercise the reports and vault apps; record measurements.

Run focused tests while developing each contract and the full suites at phase boundaries.

## 14. Tests and acceptance

| Contract | Test |
|---|---|
| Dataset conformance | Same records, order, missing-ID result and frozen nested records from memory, stored and delta datasets, in Goja and Node |
| Provenance | `chunk` and `enrich_kind` share IDs; hits from `ix-text` show the enriched record in retrieval, packing, events and GUI |
| Bake | Cold and warm callbacks get a dataset; equal counts with changed text re-run the bake |
| Snapshot leases | A query paused in a fake provider survives two publishes and materializes records from its own snapshot |
| Shared resources | Releasing `adaptive`'s old snapshot leaves a handle that `lexical`'s snapshot still owns open |
| Registry | Fresh, loaded and delta handles closed exactly once on success, failure and shutdown (counting closers; open-fd checks on Linux) |
| Detached result | Result readable after its snapshot is released and its handles closed |
| Stale lookup | A lookup naming a released snapshot returns `stale_generation` |
| Inventory | 40 versions, retain 3: GC removes exactly the unreachable ones, independent of lineage length |
| Lock | A second writer and `store gc` are refused while `serve` holds the namespace |
| Crash | Fail after each publication step; reopening yields the old or new snapshot, never a mix |
| Shared jobs | A cancels, B succeeds; both cancel, job releases its scope; C during cancellation starts a new job |
| Outcomes | Rerank provider failure stays `failed` after cleanup; an abort stays `cancelled`; a tool request after settlement is refused |
| SSE | Real non-reading socket and a failing flush end within bound; the slot recovers only after the operation settles |
| Graph | Renaming `route` and `ix-text` updates every reference; removing a referenced node is rejected |
| Export | `adaptive` exports a working lexical page |
| Local fixes | Dotted root lists outputs; empty enabled list picks a deterministic model |

Use synthetic corpora and provider fakes with explicit hold/release barriers, not sleeps. Keep the real-socket SSE test; a fake `ResponseWriter` cannot show that write deadlines work.

Measure cold build, warm open, query latency, changed-note refresh, memory, open files and store growth on one fixture before and after, and report sizes and conditions. Detached results add copying, so parity is not assumed; a regression needs investigation, not automatic abandonment of the contract.

Completion means: every finding has a passing regression or a reviewed disposition; the code in section 11 is deleted; the same example works in the browser and in Goja; documentation matches the public API; and the shutdown, cancellation and publication sequences are tested. A finding is not resolved because an abstraction with the right name exists.

## 15. Open decisions and risks

| Id | Decision | Blocks |
|---|---|---|
| D2 | Default for fusing across datasets (proposed: compile error + `canonical` projection) | phase 2, only for such plans |
| D3 | Query freshness per host (proposed: wait, except `serve --watch`) | phase 5 |
| O1 | Whether outputs with deferred LLM items (`Engine.partial`) are publishable (proposed: in memory, flagged `partial`, never durable) | phase 3 |
| O2 | Default size limit for detached results | phase 3; any reasonable default unblocks |
| O3 | Status code for stale lookups (proposed `409 stale_generation`) | phase 3; minor |
| O4 | Retention of old manifests, including privacy of notes since removed or excluded | phase 4 |
| O5 | Behavior of one-shot CLI commands when `serve` holds the namespace (fail clearly, or run without the store) | phase 4 |
| O6 | Lock implementation on non-Unix builds (`nlink_other.go` shows they exist) | phase 4 |
| O7 | Whether any codec references another artifact by path | phase 4; verification |
| O8 | Grace period for `Eval` after cancellation, and whether all callers or only request operations use it (proposed: all) | phase 6 |

Risks that are not restated elsewhere:

- Materializing after releasing the lease reintroduces use-after-close under a new API. Keep `materialize` inside the `try`.
- The namespace lock is the only thing that makes online GC safe; any path that opens the store for writing without it is a bug.
- The largest migration is hit identity across GUI, traces, evidence and evaluation, not the DSL.
- `sample` tool calls are a request/reply exchange, richer than a one-shot rerank; they must go through `CallOnOwner`, not around the bridge.

Out of scope: distributed indexing, remote datasets, multi-process shared stores, full Obsidian rendering and authentication. Reindex status and manual refresh are built later on the refresh operation, not as another refresh implementation.

The design succeeds when each of these questions has one answer enforced at one boundary: Is this an array? Who closes this reader? Whose cancellation controls this build? Which generation does this hit belong to? Which references does this rename update?
