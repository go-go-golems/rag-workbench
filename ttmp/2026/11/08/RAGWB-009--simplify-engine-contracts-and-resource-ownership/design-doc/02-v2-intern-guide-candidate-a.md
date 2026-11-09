---
Title: V2 intern guide candidate A
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
LastUpdated: 2026-11-08T21:10:09.603206085-05:00
WhatFor: ""
WhenToUse: ""
---

# Building consistent datasets, queries and resource lifetimes

## 1. What this design changes

A query should return the records that its indexes searched, even if a refresh completes while the query waits for a model. A caller should read those records through the same API whether they were just computed or loaded from disk. The engine should keep every database reader and search index open while work still uses it, then close it once. RAGWB-009 proposes the contracts needed to make these statements true throughout the engine, server, browser and evaluation tools.

Consider a query for “operation identity” over a reports corpus. The index finds section `report.md#identity`. While a model reranks the hits, someone edits that report and a refresh builds a new index. The original query must finish with its original section text and citation. New queries can use the refreshed text. The two versions can share unchanged resources, but neither query should have to know which resources are shared or call `close()` afterward.

The design connects four concrete objects:

- A **dataset** is a read-only collection of documents or chunks with an immutable version identity. A chunk is the section-sized text unit searched by an index.
- An **index** searches one exact chunk dataset and returns references into it.
- A **snapshot** records the datasets and indexes published together for queries. A query temporarily retains one snapshot.
- A **resource registry** owns open native readers and indexes. Snapshots and builds register their use; the registry closes a resource after its last user releases it.

Builds produce private candidate snapshots. Queries read published snapshots. Refresh publishes a complete candidate without changing the snapshot already held by a running query. Before returning or emitting data, the engine resolves hit references against the query's snapshot and copies the required text, metadata and citations into the outgoing value.

```text
source documents -> parse -> chunks -> index -> private candidate
                                                   |
                                             validate/publish
                                                   |
                                       current snapshot B
                                                   |
new query ------------------------------------- acquire B

older query -> retains snapshot A -> retrieve/rerank -> copy result -> release A
                         |
                  datasets/indexes
                         |
           registry retains each shared native resource
```

This is a target design and implementation guide, not a report of completed fixes. The twelve review findings below came from the stacked PR review and source inspection at `0854c6a`. This rewrite does not independently reproduce them or close implementation tasks. RAGWB-009's nine tasks remain open.

RAGWB-010 has since supplied the source-module and TypeScript foundation: implementation `cac9262`, documentation checkpoint `c267f91`. Its 26 modules under `pkg/ragjs/src/` now generate the committed `pkg/ragjs/assets/rag.js`. Four implementations are strict TypeScript; 22 remain JavaScript. That extraction did not implement this design. Use the current source map in section 3, not historical bundle line numbers, when making changes.

There is no deployed interface to preserve. Migrate callers and stored formats directly, removing the replaced paths in the same phase. Retain the concise corpus/plan builders, shared browser/Goja runtime, working indexing and incremental algorithms, provider-specific errors and meaningful evaluation cases. Do not introduce legacy adapters, a durable scheduler or a general service framework.

## 2. Following one build and query

A **corpus** describes work performed as source documents change: read files, parse them, split them into chunks and build indexes. A **plan** adds per-query work: retrieve candidates, combine rankings, filter, rerank, prepare evidence and optionally generate an answer. Their immutable builders compile into a graph of component nodes, parameters, dependencies and output kinds.

The existing high-level shape should remain recognizable:

```js
const reports = rag.corpus('reports')
  .from(rag.source.dir({ root: '/data/reports' }))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());

const search = rag.plan('search').over(reports)
  .signals({ q: '', k: 10 })
  .retrieve('text', { k: 30 })
  .filter.where()
  .limit();

const result = await rag.run(search, { q: 'operation identity' });
```

The compiler validates dependencies and determines execution order. Build nodes create outputs that can be cached or persisted. A saved output is an **artifact**, addressed by a key. A **lineage** records successive outputs of the same build step so an incremental build can find a previous version and compute only the change. A stored record collection may load records on demand through a record view rather than decoding the entire corpus.

These mechanisms already exist. Their boundaries currently expose too many details. Fresh chunks can be arrays while stored chunks are record views. Artifact cleanup follows rebuild cycles without tracking every active query. A shared build can inherit its first request's cancellation. Graph editing and compilation discover references independently. The proposal gives each of these responsibilities one owner and one contract.

The Go CLI/server runs the JavaScript bundle in Goja. Native components supply authorized filesystem access, disk-backed collections and indexes. Browser examples use memory-backed implementations of the same conceptual pipeline. The hosts may use different indexing algorithms or ranking scores; collection meaning, provenance and lifetime rules must agree.

`pkg/runner/runtime.go` constructs a runtime; `pkg/ragjs/module.go` loads the embedded bundle and binds host services. `pkg/runner/serve.go` connects a long-lived app to query, lookup and refresh. `pkg/server/rag.go` and `pkg/server/verbs.go` expose HTTP execution; `web/app-shell/shell.js` presents remote responses through the GUI's host interface. A fix must cross these boundaries when the affected value does: retaining a snapshot inside execution is insufficient if the server later fetches response text from the newest artifacts.

## 3. Evidence and where to work

The review priorities below are the original labels. Source inspection supports the design concerns, but regressions must establish each actual failure and its resolution.

| Review source | Reported failure | Proposed boundary |
|---|---|---|
| PR #2, P2 | Stored duplicate chunks select a different version from cold arrays | Dataset provenance and explicit combination policy |
| PR #2, P2 | Bake callback receives a record view instead of an array | Uniform dataset API |
| PR #2, P2 | Store listing skips a root whose basename contains a dot | Traverse the defined store layout |
| PR #3, P2 | Two refreshes close artifacts still used by an awaiting query | Snapshot retention and resource registry |
| PR #3, P2 | Versions beyond the 32-entry lineage history become uncollectable | Durable inventory independent of search history |
| PR #3, P2 | Delta-created record readers miss runtime cleanup | Central resource adoption |
| PR #4, P1 | A non-reading SSE client holds a slot indefinitely | Bounded writes and request cleanup |
| PR #4, P2 | Cancelling the first waiter cancels another waiter's build | Independent job lifetime |
| PR #5, P2 | Deferred cleanup turns failure into cancellation | Fix terminal outcome before cleanup |
| PR #6, P2 | Empty enabled-model list prevents a valid fallback | Explicit deterministic fallback policy |
| PR #7, P2 | Lexical export references removed decisions | Capability-aware graph projection |
| PR #7, P2 | Rename misses decision references | Shared structural reference traversal |

Sources: [PR #2](https://github.com/go-go-golems/rag-workbench/pull/2), [PR #3](https://github.com/go-go-golems/rag-workbench/pull/3), [PR #4](https://github.com/go-go-golems/rag-workbench/pull/4), [PR #5](https://github.com/go-go-golems/rag-workbench/pull/5), [PR #6](https://github.com/go-go-golems/rag-workbench/pull/6), [PR #7](https://github.com/go-go-golems/rag-workbench/pull/7). The dotted-root and model-fallback defects need local corrections and focused tests, not new architectural subsystems.

### Current navigation after RAGWB-010

| Concern | Authoritative source |
|---|---|
| Array/view handling and lazy persistence | `pkg/ragjs/src/engine/records.js` |
| Builds, shared work, artifacts and GC | `pkg/ragjs/src/engine/engine.js` |
| Query execution and result methods | `pkg/ragjs/src/engine/session.js` |
| Request capture and cancellation | `pkg/ragjs/src/engine/scope.ts` |
| Builders, compiler, references and edits | `pkg/ragjs/src/graph/graph.js` |
| Standalone export | `pkg/ragjs/src/export/html.js` |
| Public composition and registration | `pkg/ragjs/src/api/index.js`, `api/define.js` |
| Record algorithms and consumers | `pkg/ragjs/src/components/` |
| Evaluation and sweeps | `pkg/ragjs/src/evaluation/evals.js` |

The existing strict implementations are `core/errors.ts`, `core/identity.ts`, `core/text.ts` and `engine/scope.ts`. `pkg/ragjs/src/README.md` explains assembly and checks. Implement new contracts in strict TypeScript with declarations beside their implementations; migrate the consumers whose behavior changes. Do not add unused type declarations over the old array/view branches. Native and JSON inputs still need runtime validation, and types cannot prove that a handle stays open across an asynchronous operation.

The following historical references identify the inspected mechanisms in `0854c6a`; they are evidence anchors rather than instructions to edit today's generated file:

| Historical starting point | Relevant behavior |
|---|---|
| `pkg/ragjs/assets/rag.js:1163`, `finalize` | Separately discovers data, component `uses`, decisions and signals |
| `pkg/ragjs/assets/rag.js:1317`, `applyOps` | Rewrites some references during rename but misses decisions |
| `pkg/ragjs/assets/rag.js:1455`, `unionChunks` | Separates arrays from views and loses precedence |
| `pkg/ragjs/assets/rag.js:1875`, `Engine.gc` | Retires handles by later GC cycle rather than active-query ownership |
| `pkg/ragjs/assets/rag.js:1939`, `resolveBakes` | Exposes storage-dependent inputs and incomplete input hashing |
| `pkg/ragjs/assets/rag.js:1966`, `artifacts` | Creates a global chunk union and implicit primary reading order |
| `pkg/ragjs/assets/rag.js:2039`, `buildShared` | Shares a promise containing first-request options and signal |
| `pkg/ragjs/assets/rag.js:2079`, `Session.run` | Awaits with artifacts and attaches artifact-backed result methods |
| `pkg/ragjs/assets/rag.js:2602`, `exportHtml` | Prunes before consistently rewriting dependent references |

Go evidence remains in `pkg/natives/persist.go` (`Persist.InstallEnv`, cleanup registration and no-op closer fallback), `pkg/natives/markdown_native.go` (delta reader construction), `pkg/artifacts/lineage.go` (`LineageRecord` and `GC`), `pkg/natives/rerank.go` (`rerankAsync`, outcome classification), `pkg/ragjs/sample.go` (`newSample`) and `pkg/ragjs/decide.go` (`newDecide`). HTTP lifecycle evidence is in `pkg/server/sse.go` (`pump`), `pkg/server/verbs.go` (`handleVerb`) and `pkg/server/rag.go` (`handleRun`). The embedded `__serve.run` in `pkg/runner/serve.go` also needs migration because it constructs responses after execution.

## 4. Datasets: the same operations on fresh and stored records

Suppose a bake callback counts chunk IDs. On a cold run it receives an array and calls `.map()`. On a warm run it receives a stored record view and fails. The reported bug is one symptom of exposing the storage representation as the collection API.

Give both implementations one read-only dataset contract. Callers can explicitly request an array when they need one, but the engine always supplies a dataset. Loading from disk may cost I/O and save memory; it must not change lookup, iteration order or the meaning of the returned records.

```ts
// Proposed types; sketches omit implementation details.
type DatasetId = string;
type RecordRef = Readonly<{ dataset: DatasetId; id: string }>;

interface Dataset<T> {
  readonly id: DatasetId;
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(recordId: string): T | null;
  values(): Iterable<T>;          // deterministic order
  materialize(): readonly T[];   // explicit full traversal
}

interface ChunkDataset extends Dataset<Chunk> {
  document(recordId: string): RecordRef;
  order(documentId: string): readonly RecordRef[];
}

interface Index {
  readonly datasetId: DatasetId;
  search(query: string, options: SearchOptions): readonly Hit[];
}
```

Keep access synchronous for the current local memory and disk hosts. A hypothetical remote store does not justify making every local `get()` asynchronous. If remote datasets become necessary, design their bulk access boundary explicitly.

A callback must not be able to mutate an authoritative record through the returned object. Freeze memory records, including nested metadata, or return detached records at the boundary. Immutable decoded-record caches are acceptable. Components should never inspect `__records`, `__ix`, choose codecs or close a dataset.

Migrate repository callbacks directly:

```js
rag.bake(ctx => ctx.chunks.materialize().map(chunk => chunk.id));
rag.bake(ctx => ctx.chunks.size); // counting needs no full traversal
```

Bake cache identity must include full input dataset identities, callback source/configuration and component version. Two versions can have the same record and section counts while containing different text. Counts cannot establish equivalence. The memory, stored and delta paths must share conformance tests for lookup, missing IDs, iteration order, materialization, provenance and nested immutability.

### A hit identifies a version as well as a section

Now suppose one index searches original chunks and another searches prefixed chunks that include a document heading. Both contain `report.md#identity`, but the texts differ. If retrieval returns only that ID and the engine later chooses text from a global union, it can display a version that the index never searched. Changing array/view load order can change which version wins.

An index therefore returns an exact dataset reference:

```js
const hit = {
  ref: { dataset: 'reports-prefixed-v12', id: 'report.md#identity' },
  score: 0.87,
  reason: 'optional explanation'
};
const chunk = snapshot.resolve(hit.ref);
```

`pack`, traces, context citations, evaluation and GUI data must preserve this reference. A logical section ID may survive an enrichment; its bytes do not necessarily survive. Likewise, source identity plus vault-relative path should identify a logical document across multiple corpora. Titles and truncated slugs are insufficient global identities. Index versions include component/codec version, normalized parameters and input dataset identities.

Fusion combines rankings. By default it deduplicates only exact references, so two indexes over the same dataset combine naturally. Different enriched datasets require an explicit equivalence policy. Prefer a component that projects hits to a canonical dataset before fusion, or a combine component with documented conflict rules. Do not hide “last stream wins” inside the engine.

### Decision: one dataset API and explicit provenance

- **Context:** Storage representation and duplicate logical IDs currently affect caller behavior.
- **Options considered:** Convert every stored output into arrays; continue exposing arrays/views; provide one read-only dataset API.
- **Decision:** Use datasets and versioned record references throughout. Status: proposed.
- **Rationale:** This preserves lazy reads while making storage changes invisible to collection consumers and version choices explicit.
- **Consequences:** Callbacks and raw-hit consumers must migrate together. Full traversal and cross-dataset canonicalization become deliberate operations. No string-ID fallback may silently select a dataset.

Review invariants: fresh and loaded datasets implement the same operations; every hit identifies its dataset version; fusion never silently resolves conflicts between versions.

## 5. Query snapshots: keep the searched generation through response construction

An internal hit such as `{ ref, score }` is not yet a complete outgoing result. The engine still needs chunk text, title, heading, citation details, trace records and possibly neighbor preview. These lookups belong to the query, even when they occur after reranking or answer generation.

Follow the reports query across a refresh:

1. Query Q acquires snapshot A and retrieves a hit from A's index.
2. Q waits for a provider.
3. Refresh builds and publishes snapshot B. New queries acquire B.
4. Q resumes. It reads hit text, citations and trace data from A.
5. Q copies everything its outgoing result needs, then releases A.

Looking up Q's hit in B at step 4 could return changed text or nothing. Releasing A immediately after retrieval is also too early: response construction still needs its records. Keep the snapshot until all outgoing events and the final response contain their required detached data.

A **lease** is the engine's temporary retention of a snapshot for one operation. Ordinary callers neither acquire nor release it. Acquiring the current snapshot and incrementing its lease count must be one serialized transition; publication must not fit between those actions. Keep JS transitions on the runtime owner. Synchronize registry operations separately if Go goroutines can reach them.

```ts
// Proposed flow, not existing implementation.
async function query(plan, signals, request) {
  // Wait only if this plan has no usable published snapshot.
  await builds.ensure(plan, request.signal);
  return snapshots.withCurrent(plan.buildKey, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot);
  }); // release on success, throw, cancellation or result-construction failure
}
```

A refresh builds privately and publishes only a complete validated candidate. A failed or cancelled refresh leaves the last good snapshot current. A partial output can support internal resumption but cannot become the query snapshot. Background refresh must not block queries already able to use a published version.

### Returned results contain owned data

`rag.run` should return a result that remains readable without a cleanup call:

```js
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;
const top = result.top();
```

Here `result.chunk()` and `result.top()` read data copied before the lease ended. They do not reopen readers or fetch the latest artifacts. Copy the bounded result, including required trace/evidence/neighbor records, rather than the entire corpus. Emitted events need the same treatment before they leave the snapshot scope.

Large results need explicit limits. If a requested trace exceeds the declared budget, return a clear limit error or a deliberate pagination contract. Never return a hit whose required record was silently omitted. Whole-corpus export and evaluation over all relevance labels use internal scoped reads rather than keeping an ordinary result alive indefinitely.

Later browser document requests carry a snapshot ID and record reference. The server either resolves that retained generation or returns an explicit stale-generation response. Browser caches key by generation/reference and clear when generation changes. Refresh cannot revoke text already delivered; retention does not provide access control.

Review invariants: every query uses one published generation through outgoing-data construction; returned results own their data; application authors need no resource cleanup for ordinary searches.

## 6. Native resources: one owner, explicit users, final release

An open native resource can be a database reader or search index created by Go. Several snapshots may need the same unchanged resource. If each snapshot independently closes the handle, retiring one can break another. If no component is responsible for closing it, the handle leaks.

The registry is the single component responsible for managing and closing each open resource. A snapshot registers its use. A private build scope registers resources it has opened but not published. A query retains its snapshot, which in turn keeps its resources in use.

```text
registry owns index I
    ^                ^
    | uses           | uses
snapshot A       snapshot B
    ^
    | retained by Q
query Q

publish B -> retire A -> Q completes -> release A's use of I
I stays open because B still uses it
release B's final use -> registry closes I exactly once
```

Retirement means a snapshot stops being current; it does not mean its users have finished. Two refreshes must not force-close a resource still needed by a query. Resource release follows actual users, not a fixed number of rebuilds or a timer.

Fresh native builds, persistent loads and delta-created readers all use the same adoption path. The delta route cannot omit cleanup merely because it bypassed persistent loading. A resource-bearing runtime without a registry is a construction error; remove the no-op closer fallback.

```go
// Proposed lifecycle sketch. Concrete handle/scope types remain to be designed.
type ResourceID string

type Registry interface {
    Adopt(id ResourceID, closer io.Closer) (Handle, error)
}

r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(id, r)
if err != nil {
    _ = r.Close()
    return nil, err
}
return NewChunkDataset(h), nil
```

Every opener immediately adopts its handle or closes it on failure. If a later build stage fails, the private scope releases its uncommitted resources. Adoption must specify duplicate-ID behavior: reuse the existing registry entry and close the newly opened redundant handle, or reject and close it. It must never create two independent closing authorities for one underlying handle. Go implementations should assert implemented interfaces with `var _ Interface = &Concrete{}` where appropriate.

Shutdown proceeds in order: reject new work, cancel active operations, join their cleanup, release snapshots, then close the registry. A join timeout must be reported. Do not declare shutdown successful and close handles while workers can still use them.

### Decision: registry ownership with scoped retention

- **Context:** Resources can be shared by snapshots, queries and candidate builds.
- **Options considered:** Close on each refresh; delay closure by a rebuild count; track explicit users centrally.
- **Decision:** Registry ownership plus build scopes and snapshot leases. Status: proposed.
- **Rationale:** Users can outlive any fixed refresh count, and shared immutable artifacts can outlive one snapshot.
- **Consequences:** Acquisition/release paths need deterministic sequence tests. Private handles and central constructors constrain misuse; TypeScript does not provide linear ownership, and native implementations still need testing.

Review invariants: one registry owns each native resource; every user is represented; only the final release permits closure; failed construction and runtime shutdown follow the same ownership rules.

## 7. Stored artifacts: separate reuse history from durable inventory

Recent lineage history answers “which prior build should an incremental update reuse?” The artifact inventory answers “which files and indexes exist, and what depends on them?” These questions need different lifetimes.

Suppose lineage keeps 32 versions. On build 33, the oldest history entry disappears, but its files can still exist. If GC discovers artifacts only through that list, it can no longer find the old files to collect them. Increasing the history bound merely postpones the failure. Query duration also must not depend on that bound.

Keep existing immutable payloads and record/index codecs. Extend artifact descriptors with their owned payloads and dependency identities. Store immutable snapshot manifests listing root artifacts, plus an atomic current-snapshot pointer per build identity. These descriptors and manifests are authoritative. Lineage becomes a rebuildable accelerator for selecting an incremental base.

Do not add a separate inventory database when descriptors already express ownership. Listing must traverse the defined namespace layout. A dot in a parent directory's basename is not evidence that its contents should be skipped; correct the reported dotted-root case locally within that traversal.

### One writable namespace has one process owner

A running server holds an OS-backed exclusive lock on its writable store namespace for its lifetime. It owns publication, active resource accounting and online GC. A second process uses another namespace or fails clearly. Offline `store gc` acquires the same lock, so it cannot collect while a server owns that namespace.

This matters because an independent collector cannot see the server's in-memory leases. A correct query lease alone cannot protect a file from a different process that deletes it. The browser memory store follows the same logical store contract without OS locks or filesystem persistence. Shared multi-process or cross-machine stores require a separate design.

### Publish complete snapshots and recover to a complete version

Use this publication order:

```text
write payloads in owned staging directory
    -> close writers and validate counts/checksums/codec metadata
    -> move complete immutable payloads into namespace
    -> atomically publish artifact descriptors
    -> publish complete immutable snapshot manifest
    -> atomically replace current pointer
    -> update derived lineage acceleration
```

A crash before the pointer change leaves the old snapshot current. A crash afterward may leave stale lineage, but must leave the new snapshot readable. File and directory synchronization at the supported platform boundary is part of durability; atomic rename alone does not prove power-loss durability. Test failures between publication steps and validate the actual filesystem operations.

GC starts from current snapshots, configured retained snapshots, active query/builder leases and temporary build ownership. It follows descriptor dependencies transitively, marks everything needed, then sweeps unmarked owned artifacts. The store owner serializes publication with the mark/sweep decision so a durable candidate is not collected before it can be published.

Missing or corrupt ownership metadata aborts destructive collection with an actionable error. Unknown files are not assumed safe to delete. Hard-linked payloads follow directory ownership: deleting one artifact's links does not remove another artifact's links. Report logical bytes and actually reclaimed disk bytes separately where possible.

Retention is a deliberate policy. Keeping old generations can retain notes that a later build excludes, with privacy consequences. This policy is distinct from active query protection and from how many versions incremental-base search inspects. Old cache namespaces are disposable, but migration must document an explicit offline cleanup command; do not silently remove users' directories.

### Decision: descriptors plus one namespace owner

- **Context:** Bounded lineage loses inventory information; cross-process GC cannot observe local readers.
- **Options considered:** Unbounded lineage; separate inventory database and distributed leases; descriptors/manifests with exclusive namespace ownership.
- **Decision:** Use descriptors/manifests and a single process owner initially. Status: proposed.
- **Rationale:** Existing immutable artifacts support this model without introducing a distributed collector.
- **Consequences:** Concurrent independent writers are refused. Crash tests, lock tests and conservative metadata validation are required before enabling collection.

Review invariants: lineage may be discarded without losing inventory; GC protects all reachable owned artifacts; publication is complete or remains unpublished; offline collection respects the same namespace lock.

## 8. Async operations: fix the outcome before cleaning up

Sample, decide and rerank each bridge JavaScript to Go work. They capture a request context, install cancellation, run outside the Goja owner, post events, settle a promise and release resources. Their provider requests and errors differ, but this lifecycle should be implemented once.

The reported rerank failure illustrates why ordering matters. A provider returns an error. Deferred cleanup cancels a derived context. A callback posted to the runtime later reads that context and reports cancellation instead of the provider failure. Cleanup has changed how a completed operation appears to its caller.

Choose the terminal outcome while the operation's actual completion and cancellation state are known, before cleanup changes derived state. Post that immutable outcome exactly once. A later cleanup cancellation does not reclassify it.

```ts
// Proposed discriminated union, with domain payload/error types to be supplied.
type Outcome<T> =
  | { kind: 'success'; value: T }
  | { kind: 'cancelled'; cause: CancellationCause }
  | { kind: 'deadline'; error: OperationError }
  | { kind: 'failure'; error: OperationError }
  | { kind: 'shutdown'; error: OperationError };
```

```go
// Illustrative asynchronous lifecycle, not a synchronous implementation.
func RunOperation(ctx context.Context, spec Spec) Future {
    // Install abort handling before starting an owned task/errgroup.
    // Work crosses goroutines as immutable Go values or bytes.
    value, err := spec.Execute(ctx, spec.Emit)
    outcome := spec.Classify(value, err, context.Cause(ctx))
    // Freeze outcome before cleanup cancels the derived context.
    return SettleOnOwner(outcome) // exactly once; cleanup even if owner closed
}
```

Define the race rule as part of the runner: an accepted terminal result before a cancellation transition remains terminal; otherwise cancellation wins. Distinguish caller cancellation, deadline, provider failure and runtime shutdown. Detach abort listeners on settlement. If runtime disposal prevents the JavaScript promise callback from running, workers and resources must still be cleaned up. Use `errgroup` for related Go goroutines.

Provider-specific conversion, validation, protocol errors and tool semantics remain supplied functions. Tool streaming needs a supported request/reply hook onto the runtime owner. Worker goroutines must not access Goja values directly. Avoid simplifying the shared runner so far that a rich tool interaction has to bypass it.

Review invariant: an asynchronous operation has exactly one immutable terminal outcome, selected independently of cleanup side effects.

## 9. Shared builds: cancellation belongs to each caller's interest

A **waiter** is a caller waiting for a shared build to finish. Request A starts build J; B needs the same output and joins it. If A disconnects, B still needs the work. Giving J the first request's context would let A cancel B's build.

The job therefore owns its cancellation context, linked to engine shutdown. Each waiter registers interest and has its own cancellation. A waiter cancellation removes that interest; it does not automatically cancel the job. If the last waiter leaves, cancel the job unless an explicit background-refresh owner still needs it.

```text
A joins J -> B joins J -> A cancels -> J continues for B
B cancels, no background owner -> cancel J -> join cleanup
B cancels, refresh owner remains -> J continues for refresh
engine shuts down -> cancel job and waiters -> join cleanup
```

Handle a late waiter explicitly. If all callers left and J is already cancelling, a new request must not inherit its doomed promise. Wait for cancellation to settle, then start/retry an eligible job. The job registry key includes the full normalized build identity and every option that changes semantics; equal plan names alone do not imply interchangeable work.

Progress belongs to J and is fanned out to waiter subscriptions with job/operation IDs. A slow or closed subscriber cannot block the shared job or cancel other subscribers. Events cannot remain bound to A's original scope after B joins. Queue overflow detaches or cancels the affected request according to the transport policy, leaving other interested consumers intact.

### Decision: a small shared-job registry

- **Context:** Build deduplication must survive cancellation of its initiating request.
- **Options considered:** First-request ownership; duplicate every build; independent job lifetime with explicit interest.
- **Decision:** Independent jobs with waiters and optional background owner. Status: proposed.
- **Rationale:** This keeps reuse while giving cancellation the correct scope.
- **Consequences:** Last-waiter and late-waiter transitions need tests. This does not introduce persistence, scheduling or durable job recovery.

Review invariant: one request can withdraw its interest without cancelling work still needed elsewhere.

## 10. HTTP events: a stalled client must release its request slot

SSE sends a sequence of events over a long-lived HTTP response. If the client stops reading, a socket write or flush can block. A bounded event queue does not by itself stop that blocked write. The reported P1 defect leaves admission capacity occupied indefinitely, preventing later requests from running.

Query and verb endpoints should use one response helper. Domain execution emits typed events and a terminal outcome; the helper owns JSON/SSE framing, event subscription, admission and request cleanup. Provider code should not know about HTTP.

One goroutine writes and flushes each SSE response. Before every write and flush, set a bounded write deadline using `http.ResponseController.SetWriteDeadline`. Use the error-returning `Flush`, and ensure wrappers support the controller's unwrapping path. This includes initial headers, heartbeat and final-event writes. If deadline support is unavailable, fail setup rather than streaming without bounds. Once a write deadline is exceeded, terminate; the installed Go API does not guarantee that extending an already-exceeded deadline recovers the writer.

On write or flush failure, stop pumping, detach/cancel this request and return through a bounded join. Queue overflow has the same request-local effect. A long healthy stream can continue because the bound applies per write rather than as one short overall timeout. Queues, accumulated/coalesced text and heartbeat output also need byte-size and memory bounds; a fixed entry count is insufficient when entries can be huge.

A working stream sends at most one terminal event. A failed network may deliver none, so the server must not promise that every client receives completion. Cleanup and slot release still happen. Direct queries and verbs use the same admission scope and terminal-error vocabulary.

Test with a real socket whose client stops reading enough data to stall writes. Assert that the configured bound terminates the request and another request can acquire the released slot. Fake writers are useful for explicit flush errors, but cannot establish actual network deadline behavior.

Review invariants: every network write/flush has a bound; transport failure ends that transport; one slow subscriber cannot retain global capacity indefinitely or cancel other users of shared work.

## 11. Graph references: compilation and editing must see the same dependencies

A graph dependency can appear as a data input, an artifact dependency, a decision condition or a pinned output. Consider a retrieval node conditioned on `route.needs`. If rename changes node `route` to `classify` but only rewrites data inputs, the condition still points to `route`. Export can create the same problem by deleting the classifier while leaving its condition behind.

Normalize structural references once in the compiler. Keep the concise public syntax when useful:

```js
// Public DSL
when: { decision: 'route.needs', min: 0.5 }

// Proposed canonical IR
when: {
  source: { kind: 'decision', node: 'route', output: 'needs' },
  minimum: 0.5
}
```

Components declare structural references through a common schema/visitor. `references(node)` enumerates them; `rewriteReferences(node, mapping)` rewrites them. The compiler uses that same enumeration to validate existence, output types, phase constraints and cycles. A rename checks target uniqueness, rewrites every declared reference and validates once at the end of the edit transaction.

Deletion requires a policy for dependents: reject it, cascade deletion or use a declared replacement. The editor must not invent the meaning of a query after removing its decision. Lexical export is a separate projection with a deliberate policy: when it omits a decision-only classifier, retrieval becomes unconditional. It validates the transformed graph before serialization.

### Export according to capabilities

A submit-triggered component is not automatically unavailable in a standalone page. Declare actual capabilities, such as local text index, external model or host filesystem. The exporter chooses supported components and applies documented rules for excluded ones. Bundle a normalized portable dataset; do not assume `engine.files()` contains every server-side source.

A classified server plan can therefore export a lexical page whose supported local retrieval works without the omitted model decision. This is a chosen export behavior, not a general rule for deleting decision nodes in any plan.

### Callbacks declare dependencies; source rewriting is not reliable

An arbitrary JavaScript callback can contain strings that resemble node names without being graph references. String replacement cannot safely rename it. For graph-aware callbacks, require named dependency bindings in component options. Rename changes the binding, not callback source. Reject transformations that would need to rewrite opaque undeclared references.

### Decision: one reference visitor, separate transformation policies

- **Context:** Compiler, rename and export currently discover different subsets of dependencies.
- **Options considered:** Add special cases to every operation; rewrite callback strings; normalize structural references with one visitor.
- **Decision:** Shared reference enumeration/rewriting with explicit edit/export policies. Status: proposed.
- **Rationale:** A common dependency model makes completeness checkable without pretending arbitrary code is inspectable.
- **Consequences:** Components must declare references. Unsupported opaque callbacks can block a transformation with a clear error. Export still owns its capability and projection rules.

Review invariant: all structural references participate in the same validation and rewrite API, and every transformed graph is validated before use.

## 12. Keeping the implementation small

The modules should follow a clear dependency direction:

```text
DSL / GUI / CLI / HTTP
          |
graph compilation and transformation
          |
application operations: build / refresh / query / inspect
          |
shared jobs + execution -> snapshots -> datasets/indexes
                                           |
                               registry + artifact store

provider bridges -> operation runner -> typed events -> response helper
```

The 010 module extraction is already complete. Evolve those modules as contracts land rather than extracting another empty layer. Keep a thin `rag` facade. Lower-level code must not import the public namespace; imports stay local and acyclic. Bundle state belongs to each evaluated runtime, not a process-wide Go singleton.

| Actual consumers | Shared mechanism | Specialized behavior retained |
|---|---|---|
| Memory and persistent records | Dataset contract/conformance | Encoding, shards and page access |
| Fresh, loaded and delta outputs | Adoption and scopes | Build/delta algorithms |
| Sample, decide and rerank | Async lifecycle/outcome | Requests, protocol errors, tool semantics |
| Direct query and verb endpoints | Admission, subscription, response lifetime | Parameters and domain operation |
| Compiler, editor and exporter | Reference traversal | Transformation policy |
| Timer, manual and post-sync refresh | Build/refresh operation | Scheduling and access checks |

Each new abstraction must retire an existing responsibility or duplicated path. Remove `unionChunks`, consumer-side view tests, scattered array conversion, rebuild-count retirement, caller-side direct handle closure, first-waiter build ownership and duplicate HTTP pump orchestration as their replacements land. Do not add executor branches for individual components when the contract can express the behavior.

Enumerate references before deleting obsolete codecs/formats. A uniform dataset API does not make browser memory algorithms or backend quality comparisons obsolete. Keep working markdown parsing, section chunking, Bleve, terms indexing and incremental algorithms unless a concrete contract change requires altering them.

### Tooling and shared runtime

Edit `pkg/ragjs/src/`, then generate the committed asset with `pnpm build`. Goja, browser use and standalone export all load that generated implementation. Do not hand-edit the bundle or maintain separate host/browser copies. The readable ES2020-targeted bundle preserves names; this does not promise polyfills or browser compatibility beyond tested behavior.

`pnpm check` combines strict type checks, Biome, exact generation checking and runtime publication/export/module-graph tests. Biome formats/lints authored files; TypeScript checks static contracts. Node VM export tests use a minimal DOM and do not prove visual browser behavior. Go tests exercise the actual Goja interpreter and native/JS parity. Preserve intended semantics and array ordering; incidental JSON property insertion order is not a compatibility requirement.

## 13. Migration and implementation phases

Use one coherent integration branch after the intended parent merge. The original review stack was unmerged when inspected; this guide authorizes no merge, force push or thread resolution. The present experiment changes documentation only.

Before editing, run a reports example, inspect its compiled plan, then trace one query through `session.js`, `pkg/runner/serve.go` and the HTTP helper. Follow a resource from creation through adoption, snapshot use and closure. This establishes the actual lifetime boundary before changing it.

### Phase 1: characterize and establish a baseline

Add deterministic regressions for all twelve findings and record which fail on the baseline. Include cold, stored and delta paths, duplicate IDs with distinct text, and equal-count changed bake inputs. If the stack is used before the full migration, prioritize the SSE P1 correction, placing it in the eventual shared transport rather than leaving a temporary alternate pump. Correct model fallback as a small focused change: an empty enabled list must not prevent a valid fallback, and eligible selection must be explicit and deterministic. The exact policy needs implementation-level inspection and tests; this document does not invent a provider ordering.

### Phase 2: datasets and provenance

Implement strict dataset/reference types and memory/persistent implementations. Bind indexes to dataset identities. Migrate bake/custom callbacks, retrieval, fusion, packing, traces, evidence, GUI and evaluation consumers together. Replace ambiguous raw IDs and remove the implicit union. Run the same conformance suite in browser/memory and Goja/persistent paths.

### Phase 3: snapshots and resource lifetimes

Implement atomic acquire/retain, private build scopes, transactional publication and registry adoption for fresh/load/delta resources. Keep query execution, emitted-event construction and final response construction in the snapshot scope. Make results detached. Migrate inspection/evaluation scoped reads and generation-aware lookup. Delete rebuild-count GC and unmanaged closer routes. Exercise failed builds and shutdown, not just success.

### Phase 4: durable inventory and GC

Introduce authoritative descriptors/manifests, current pointers, namespace locking and conservative mark/sweep. Make lineage derived. Correct dotted-root traversal. Test publication crash boundaries and active-reader protection before enabling destructive collection. Record explicit old-namespace cleanup instructions.

### Phase 5: operations and shared builds

Move sample, decide and rerank onto one runner, preserving protocol behavior and owner-thread hooks. Introduce job interest tracking, background ownership and safe late-waiter handling. Fan out progress with job/operation IDs. Test terminal races, runtime disposal and all-waiter cancellation.

### Phase 6: streaming transport

Route both HTTP execution forms through one admission/event/JSON/SSE helper. Bound every write and flush and the join after transport failure. Verify real stalled clients, flush errors, terminal races, queue memory and inflight-slot recovery.

### Phase 7: graph transformations and export

Normalize references, use one visitor across compile/edit/export, and validate edit transactions. Add capability projection, explicit lexical decision removal and portable datasets. Test rename, delete and export across every reference kind. Reject undeclared opaque callback rewrites.

### Phase 8: remove replaced code and verify integration

Update examples, help, API documentation, GUI, Evals, sweeps, saved plans, presets and standalone export. Bump IR and artifact namespace versions; no legacy reader remains. Retain `rag.run` and `result.top` because they are useful shapes, while migrating their underlying semantics directly. If low-level code needs a reference constructor, expose an explicit one; never guess a dataset for `result.chunk(stringId)`.

Commit coherent validated changes, including fixtures and callers, at meaningful boundaries. Focus tests during development, then run full checks at integration boundaries. This plan does not require repeated full suites after unrelated prose edits.

## 14. Tests that establish the contracts

Use synthetic corpora, provider fakes and hold/release barriers to place work at exact transitions. Arbitrary sleeps make lifetime tests unreliable. Counting closers establish exact closure; actual descriptor/file checks add useful host evidence. Keep the real-socket test because fake responses cannot prove socket deadlines.

| Area | Sequence and required observation |
|---|---|
| Dataset consistency | Memory, persisted and delta datasets return equivalent ordered records and missing-ID results; nested metadata cannot mutate authority |
| Bake caching | Cold/warm callbacks use one API; equal counts with changed bytes invalidate the bake |
| Provenance | Original/enriched datasets share logical IDs; retrieval, fusion policy, packing, trace and display resolve the intended bytes |
| Query lifetime | Pause a provider in A, publish B and C, resume A and construct its result successfully |
| Resource ownership | Fresh/load/delta constructors adopt; failures and shutdown close each opened handle exactly once; shared resources remain open for other snapshots |
| Detached results | Release the query snapshot and close unused handles; `result.top()` and `result.chunk(hit)` still work |
| Publication | Failed/cancelled refresh leaves last good current; crash between each durable step reopens a complete old or new generation, never a mix |
| Inventory | Build 40+ versions, retain three, collect the expected reachable set independently of lineage length |
| Namespace ownership | A second writer and standalone collector fail while the serving owner holds the lock |
| Shared jobs | Cancel A while B waits and B succeeds; cancel all and resources release; a refresh owner preserves work; a late waiter avoids a cancelling job |
| Async outcome | Provider error stays failure after deferred cleanup; caller cancellation, deadline and shutdown retain their distinct classification |
| SSE | Non-reading real socket and flush failure end within configured bounds; request slot becomes available; terminal event occurs at most once |
| Graph editing | Rename, deletion and projection update all typed references or reject explicitly; no dangling decision survives |
| Standalone export | Classified source plan produces a working unconditional lexical page according to the documented projection |
| Local corrections | Dotted-root listing finds saved outputs; empty enabled list chooses the deterministic eligible fallback |

Normal repository checks include:

```sh
pnpm install --frozen-lockfile
pnpm build
pnpm check
go test ./pkg/ragjs ./pkg/runner ./web
go test ./...
go build ./...
```

Use the project's required lint target at integration. For interactive server checks use tmux, capture output with `capture-pane`, and use `lsof-who -p $PORT -k` to stop a server on a chosen port. Run Go binaries with `go run ./path/to/command`.

Measure cold build, warm startup, query latency, changed-note refresh, memory, open descriptors and artifact growth against the same fixture before and after. Report corpus size and conditions. Detached result construction can allocate more memory; tests passing does not establish performance parity. Investigate regressions with measurements.

Completion requires passing regressions or a documented reviewed disposition for all twelve findings; removal of replaced mechanisms; one working example across memory/browser and persistent/Goja; actual API documentation; and verified cancellation, shutdown and publication sequences. Moving code into modules, adding a type or naming a class `Snapshot` does not establish those behaviors. This rewrite records no new implementation validation.

## 15. Risks, remaining decisions and scope

The highest-risk boundary is the final lookup before snapshot release. A correct retrieval lease that ends before event/result construction recreates the old failure. The second is sharing: retiring a snapshot cannot close every handle it names if another snapshot uses one. The third is the filesystem: in-memory leases do not protect against a collector outside the exclusive namespace-owner rule.

The broadest migration is record identity. Audit every trace, evidence object, evaluation path, saved response and GUI lookup for places that drop the dataset reference. Fusion equivalence must be explicit; equal logical IDs alone are insufficient. The lifecycle runner also needs enough structure for streaming tool request/reply without allowing worker access to Goja values.

Implementation still must settle exact scope/handle types, duplicate-adoption policy, result budgets, retention settings, supported filesystem durability operations and deterministic model fallback ordering. These details need source inspection and focused tests. They do not justify parallel legacy interfaces or weakening the stated contracts.

Distributed indexing, remote dataset backends, simultaneous multi-process store sharing, full Obsidian rendering and production authentication remain outside this design. Later manual/reindex status features should reuse application operations rather than create another refresh path. Retaining a historical generation is not an authorization decision.

An intern can use the following questions in code review after reading the corresponding explanations:

1. Does each record reference identify a dataset version and record ID?
2. Do fresh and stored datasets provide identical collection meaning?
3. Does every query retain one snapshot until its outgoing data owns what it needs?
4. Does each native resource have one registry owner and explicit users?
5. Does only the final release permit closure?
6. Can lineage history be dropped without losing artifact inventory?
7. Can a waiter leave without cancelling work another owner still needs?
8. Is the terminal outcome fixed once, before cleanup changes cancellation state?
9. Are writes, flushes, queues and failure joins bounded?
10. Do all structural references use one validation/rewrite mechanism?
11. Can ordinary callers use returned results without a close/release call?
12. Did this phase remove the paths that its new contract replaces?

## 16. References and author self-check

Primary source: [original RAGWB-009 guide](01-intern-guide-to-consistent-datasets-snapshots-operations-and-graph-contracts.md). It retains the original design and historical evidence. Current runtime orientation: `pkg/ragjs/src/README.md`. Foundation design: [RAGWB-010 intern guide](../../RAGWB-010--modular-runtime-sources-and-typescript-tooling-foundation/design-doc/01-intern-guide-to-modular-runtime-sources-typescript-and-build-tooling.md). Review links and source anchors are in section 3.

Two comprehension revisions shaped this version. Section 5 replaces “event/result packaging” with the actual lookups for text, heading, citations and trace records, then follows a paused query across publication. That makes it possible to identify the exact moment when a lease may end. Section 11 follows a decision reference through rename and classifier removal before presenting the reference visitor; it explains why shared traversal is needed beyond merely showing its API.

Remaining weaknesses: resource handles, durable synchronization and model fallback policy are still design sketches, so this guide cannot provide final implementation signatures or claim their behavior is tested. The migration spans many consumers; the file map is a starting point, not proof of a complete call-site inventory. The self-check records author judgment, not experimental evidence that the candidate skill improved writing.
