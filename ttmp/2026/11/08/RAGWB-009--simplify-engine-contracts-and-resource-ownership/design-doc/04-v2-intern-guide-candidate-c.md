---
Title: V2 intern guide candidate C
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
LastUpdated: 2026-11-08T21:10:09.929984376-05:00
WhatFor: ""
WhenToUse: ""
---

# A consistent engine: datasets, query lifetimes, operations and graph edits

## 1. What this design changes

rag-workbench builds searchable collections from documents, then runs query plans against those collections. A document change can start a new build while an earlier query is still using the previous build. A query can also wait for a model, stream progress to a browser, and look up text after retrieval has finished. These overlapping activities need explicit rules about which records they use, who keeps files open, and who can cancel shared work.

RAGWB-009 proposes those rules and the implementation changes that enforce them. A dataset gives components one way to read records. A snapshot groups the exact datasets and indexes used by a query. A resource registry keeps their native readers open until every user has finished. An operation runner gives asynchronous work one terminal outcome, and a shared-build registry separates a build's lifetime from any one request. A common graph-reference traversal makes compilation, rename and export agree about dependencies.

These are proposed contracts, not completed features. The original investigation used commit `0854c6a` on `task/ragwb-008-vault-ui`; its twelve review findings have not been independently reproduced by writing this guide. RAGWB-010 subsequently completed the source extraction and tooling foundation in `cac9262`, with documentation checkpoint `c267f91`. That work makes `pkg/ragjs/src/` the source of truth; it does not fix 009's semantic problems. The nine 009 implementation tasks remain open. No new tests or production-readiness claims follow from this rewrite.

The project is undeployed and compatibility may break. Migrate callers and stored formats directly. Do not add legacy readers, compatibility adapters or two parallel implementations. Preserve the useful fluent builders, Markdown/chunking algorithms, Bleve and terms indexes, incremental builds, provider behavior and meaningful evaluation cases.

### Follow one record through the system

Suppose the reports corpus contains `notes/operations.md`. A build parses it and creates chunk `notes/operations.md#cancellation`. The text index returns that chunk for a query. Before returning the answer, the engine needs the chunk's text, heading, document title and citation details. Those records must come from the same build as the index hit, even if the note changes while the model is answering.

```text
source files --> parse --> document dataset --> chunk dataset --> index
                                                   |               |
                                                   +-------+-------+
                                                           |
                                                 published snapshot A
                                                           |
query --> acquire A --> retrieve --> model awaits --> construct result
                                                           |
                                                copy needed records
                                                           |
                                                    release query A
                                                           |
                                                  JSON / SSE / GUI

refresh --> build candidate B privately --> validate --> publish B
                                              new queries acquire B
```

A **corpus** describes work performed when source documents change. A **plan** adds query-time retrieval, fusion, filtering, reranking, evidence packing and optional answer generation. A **component** implements one node in the compiled dependency graph. A **dataset** is an immutable version of document or chunk records. An **artifact** is a persisted build output, addressed by its identity. A **snapshot** maps validated build outputs to the datasets and indexes that belong together. A **lease** is the engine's recorded use of a snapshot until an operation finishes.

Application authors still write concise programs:

```js
// Existing builder style to retain; not a demonstration of the proposed APIs.
const corpus = rag.corpus('reports')
  .from(rag.source.dir({ root: '/data/reports' }))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());
const plan = rag.plan('search').over(corpus)
  .signals({ q: '', k: 10 })
  .retrieve('text', { k: 30 })
  .filter.where()
  .limit();
const result = await rag.run(plan, { q: 'operation identity' });
```

The engine handles leases and cleanup inside `rag.run`. Ordinary search callers do not acquire or release native readers.

## 2. Where to read and what evidence exists

Goja runs the generated JavaScript in CLI/server processes. Browser examples and standalone exports use the same generated runtime with memory implementations. Native Go components provide authorized filesystem access, local indexes and stored record readers. Backend algorithms and ranking scores can differ; collection behavior and ownership rules must agree.

`pkg/runner/runtime.go` constructs a runtime, and `pkg/ragjs/module.go` loads the embedded bundle and binds host services. `pkg/runner/serve.go` connects a long-lived app to query, document lookup and refresh. `pkg/server/rag.go` and `verbs.go` expose HTTP operations; `web/app-shell/shell.js` presents them through the GUI's host interface.

### Current source map after 010

| Work to perform | Authoritative starting point |
|---|---|
| Datasets, stored views and lazy records | `pkg/ragjs/src/engine/records.js` |
| Builds, shared jobs, artifact assembly and GC | `pkg/ragjs/src/engine/engine.js` |
| Query lifetime and returned results | `pkg/ragjs/src/engine/session.js` |
| Request scope capture and cancellation | `pkg/ragjs/src/engine/scope.ts` |
| Builders, compiler and graph edits | `pkg/ragjs/src/graph/graph.js` |
| Standalone export | `pkg/ragjs/src/export/html.js` |
| Public composition and registration | `pkg/ragjs/src/api/index.js`, `api/define.js` |
| Record algorithms and built-ins | `pkg/ragjs/src/components/` |
| Evaluation and sweeps | `pkg/ragjs/src/evaluation/evals.js` |

010 extracted 26 source modules. Four implementations are strict TypeScript: `core/errors.ts`, `core/identity.ts`, `core/text.ts` and `engine/scope.ts`. The other 22 remain JavaScript, using `allowJs: true`, `checkJs: false`. This is not a complete typed engine or public declaration package. Implement the changed 009 contracts in strict TypeScript and migrate their consumers when changing behavior; do not put unused declarations over the old array/view split.

`pkg/ragjs/src/README.md` documents the build. Edit source, run `pnpm build`, and commit the generated `pkg/ragjs/assets/rag.js` with the source change. Browser, Goja and standalone export use this bundle. Ordinary Go builds do not require the JavaScript toolchain. Keep local, acyclic imports; lower-level modules must not import the public API namespace. Runtime state belongs to each evaluated bundle rather than a process-global Go singleton.

### Twelve reported findings

These are prior review reports, with their original priorities. They are regression targets, not independently verified results of this document.

| Source | Reported behavior | Required correction |
|---|---|---|
| PR #2, P2 | Stored duplicate chunks select different versions from cold arrays | Dataset provenance and explicit combination policy |
| PR #2, P2 | Bake callback receives a record view where it expected an array | One callback-facing dataset API |
| PR #2, P2 | Store listing skips a root with a dot in its basename | Traverse the declared store layout |
| PR #3, P2 | Two refreshes close artifacts while an awaiting query uses them | Query snapshot leases and shared resource ownership |
| PR #3, P2 | Versions beyond the 32-entry lineage history become uncollectable | Inventory independent of recent build history |
| PR #3, P2 | Readers created by delta updates miss runtime cleanup | One adoption path for every native resource |
| PR #4, P1 | Non-reading SSE client holds a request slot indefinitely | Bounded writes and request cleanup |
| PR #4, P2 | Cancelling the first waiter cancels another waiter's build | Job-owned cancellation |
| PR #5, P2 | Deferred cleanup changes provider failure into cancellation | Fix terminal outcome before cleanup |
| PR #6, P2 | Empty enabled-model list blocks a valid fallback | Explicit deterministic model-selection policy |
| PR #7, P2 | Lexical export leaves references to removed decisions | Explicit export projection and final validation |
| PR #7, P2 | Rename misses decision references | Common structural reference traversal |

The dotted-root and model-fallback findings need focused local fixes and tests. They do not justify new subsystems.

Historical source anchors in `pkg/ragjs/assets/rag.js` at `0854c6a` explain the larger problems: `finalize:1163` discovers several reference kinds independently; `applyOps:1317` misses decision rewrites; `unionChunks:1455` separates arrays and views and loses precedence; `Engine.gc:1875` retires handles without active-query ownership; `resolveBakes:1939` exposes storage-dependent values and incomplete cache inputs; `artifacts:1966` creates a global chunk union and implicit primary-stream order; `buildShared:2039` inherits the first request's options/signal; `Session.run:2079` holds artifacts across awaits and adds artifact-backed result methods; `exportHtml:2602` prunes before dependent references are rewritten. These are historical line numbers. Use symbols and the current source map when implementing.

The native and server boundaries also matter: `pkg/natives/persist.go` registers only some loaded handles and allows a no-op closer fallback; delta callbacks in `markdown_native.go` open readers separately. `pkg/artifacts/lineage.go` truncates `LineageRecord` history that `GC` later treats as inventory. `rerankAsync` in `pkg/natives/rerank.go` reads cancellation in a posted callback after deferred cleanup; `newSample` and `newDecide` have similar, inconsistent bridge machinery. `pkg/server/sse.go`'s `pump` lacks bounded writes and ignores failures; query and verb handlers duplicate orchestration. The embedded `__serve.run` in `pkg/runner/serve.go` constructs outgoing results from current artifacts after query execution, so fixing execution leases alone would leave a generation mismatch.

## 3. Read records through one dataset contract

A fresh chunker can currently return an array while a stored build returns a RecordView. A callback that calls `.map()` works after a cold build and fails after loading the same output. Changing the callback to recognize storage implementations spreads that distinction to every consumer.

The proposed API gives fresh and stored outputs the same operations. Loading from disk can change I/O and memory use. It must not change which record `get` returns, how `values` orders records, or what a missing ID means. This is an API migration: callers use datasets rather than preserving today's array methods.

```ts
// Proposed shape; names remain reviewable during implementation.
type RecordRef = Readonly<{ dataset: string; id: string }>;
interface Dataset<T> {
  readonly id: string;  // immutable version identity
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(recordId: string): T | null;
  values(): Iterable<T>; // deterministic order
  materialize(): readonly T[]; // explicit full traversal
}
interface ChunkDataset<T> extends Dataset<T> {
  document(recordId: string): RecordRef;
  order(documentId: string): readonly RecordRef[];
}
interface Index {
  readonly datasetId: string;
  search(query: string, options: SearchOptions): readonly Hit[];
}
```

`SearchOptions`, `Hit` and record shapes are placeholders for their owning implementations, not claims of existing types. `Hit` must contain a `RecordRef`. Keep access synchronous for current local memory/disk hosts. A speculative remote store is not a reason to make every lookup asynchronous; a future remote requirement should define a bulk boundary separately.

A read-only interface alone does not protect nested record metadata at runtime. Freeze in-memory records deeply enough for the supported record shape or return detached values at callback boundaries. Cached decoded records may be immutable. Test attempted nested mutations. Component code receives no `close`, `__records`, `__ix` or codec-selection escape hatch.

For bake/custom callbacks:

```js
// Proposed usage: update every in-repository callback in the same cutover.
rag.bake(ctx => ctx.chunks.materialize().map(chunk => chunk.id));
rag.bake(ctx => ctx.chunks.size); // counting does not need a traversal
```

A bake cache must also know which dataset it read. Two edits can preserve both document count and section count while changing every relevant byte. Include complete input dataset identities, callback source/configuration and component version in the key. Equal counts cannot establish identical inputs.

**Contract:** memory, loaded and delta-produced datasets pass one conformance suite for lookup, missing IDs, ordering, immutable records, materialization and provenance. Storage-specific code remains behind that interface.

### Identify the version as well as the section

Suppose dataset `original-v1` contains the chunk ID `operations#cancellation` with original text. Dataset `prefixed-v1` contains the same ID with a title prefix added for retrieval. An index built from `prefixed-v1` must not return a hit that later resolves through `original-v1`. The logical section name is equal, but the records are different.

```js
// Proposed hit representation.
const hit = {
  ref: { dataset: 'prefixed-v1', id: 'operations#cancellation' },
  score: 0.87,
  reason: 'optional explanation'
};
const record = snapshot.resolve(hit.ref);
```

Carry `ref` through retrieval, fusion, trace, evidence packing, citations, evaluation and GUI display. An index records its exact input dataset identity. Construct an index version from component/codec version, normalized parameters and input dataset identities. Identify logical documents by source identity plus vault-relative path; titles and truncated slugs are insufficient across corpora.

Fusion deduplicates exact dataset-and-ID references by default. Combining two indexes over the same dataset is straightforward. Combining original and enriched datasets requires an explicit policy: project hits to a canonical dataset before fusion, or use a combine component with documented conflict rules. Do not add engine-wide “last stream wins” behavior. Reading order likewise comes from the chunk dataset's document/order operations, not an implicit primary stream.

### Decision: dataset identity is part of a hit

- **Context:** equal section IDs can refer to different bytes in different build outputs.
- **Options considered:** global chunk union with precedence rules; explicit dataset-qualified references.
- **Decision:** use dataset-qualified references and ordinary explicit projection/combine components.
- **Rationale:** a hit states its origin rather than depending on artifact enumeration or storage representation.
- **Consequences:** every raw-hit consumer must migrate; cross-dataset fusion needs a declared equivalence policy.
- **Status:** proposed.

## 4. Keep a query's snapshot alive through result construction

A query starts against snapshot A and retrieves `chunk-42`. It then awaits a model call. Refresh publishes B, and another refresh publishes C. If cleanup closes A merely because two rebuilds occurred, the original query can no longer read its hit. Even if execution succeeds, looking up `chunk-42` through the newest snapshot while constructing the response can return changed text or no text.

A query therefore acquires a lease on A before it starts reading. All artifact-backed operations for that query use A, including the lookups that add text, titles, headings, citation details and trace records to outgoing events and responses. Only after those values have been copied into the outgoing data may the query release A.

A published snapshot is immutable. Refresh builds B privately; it validates B completely before atomically making B current. Existing queries continue with A, new queries use B, and a failed/cancelled build leaves A current. Partial outputs may support internal resumption but cannot become a query's current snapshot. A usable current snapshot should let queries proceed during background refresh; `ensure` waits only when the requested plan has no usable snapshot.

```ts
// Illustrative orchestration, not implemented APIs.
async function query(plan, signals, request) {
  await builds.ensure(plan, request.signal);
  return snapshots.withCurrent(plan.buildKey, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot);
  }); // finally releases on success, failure, abort or construction error
}
```

Selecting the current snapshot and incrementing its lease count is one serialized action. Otherwise refresh could retire A between the selection and the increment. Keep JS transitions on the runtime owner. Synchronize Go registry operations separately wherever goroutines can access them.

### One owner manages every native resource

A native resource is an open database, record reader or search index supplied by Go. The registry owns that open handle and is responsible for closing it. A snapshot records its use of the registry entry; queries retain the snapshot. Several snapshots may use one unchanged index:

```text
Registry entry X owns one open index
  users: snapshot A, snapshot B

Query Q retains A
Publish C; A stops being current
Q finishes copying records; release Q's lease on A
A has no remaining reason to stay live; release A's use of X
B still uses X; X remains open
Release B's final use of X; registry closes X once
```

A retired snapshot cannot close every handle it names. Its job is to release its registered uses. The registry closes a handle only when its final user has released it. These transitions belong inside the engine, not in ordinary application callbacks.

Fresh native builds, persistent loading and delta readers all use the same constructor/adoption path. A builder has a private resource scope until publication; failure later in the build releases its uncommitted resources. Remove the no-op cleanup fallback: constructing a resource-bearing runtime without the registry is an error.

```go
// Proposed lifecycle sketch; concrete Handle and scope APIs need implementation.
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

Adoption must define duplicate-ID behavior: reuse the existing entry and close the redundant newly opened handle, or reject and close it. It must never silently create two owners of one underlying handle. Close on every failed handoff.

Shutdown rejects new work, cancels operations, joins their cleanup, releases snapshots and then closes registry entries. Report a shutdown timeout explicitly. Closing handles under still-running workers and calling that successful cleanup is incorrect.

### Return records that no longer depend on a live reader

After `await rag.run(...)`, callers expect to inspect the result without managing an engine lease. Return a detached result: copy the records needed by emitted hits, trace details, evidence and configured neighbor previews while the snapshot remains leased. `result.top()` and `result.chunk(hit)` then read the result's owned data.

```js
// Proposed caller behavior.
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;
// Works after the query lease is released; caller has no cleanup duty.
```

Copy the bounded result, not the whole corpus. Declare limits for large traces and previews. If required records exceed the budget, return a clear limit error or a designed pagination contract; do not return hits with silently missing required text. Evaluations that scan relevance labels and full-corpus export use explicit internal scoped reads.

A later browser document lookup is a separate operation. Send snapshot ID plus record reference. The engine resolves that retained generation or returns a stale-generation response. It must not reinterpret the old hit against current data. Key browser caches by generation/reference and invalidate on generation changes. Retention has privacy consequences: refresh cannot revoke text already delivered, and keeping a generation is not access control.

### Decision: engine-scoped leases and detached public results

- **Context:** refresh overlaps queries, while ordinary callers should not close result objects.
- **Options considered:** rebuild-count retirement; caller-managed leased results; scoped engine reads and detached results.
- **Decision:** retain the snapshot through all record-dependent work, then return bounded detached records.
- **Rationale:** lifetime follows actual use and callers keep the simple `rag.run` interface.
- **Consequences:** copying has measurable allocation cost; internal full scans need a scoped API; result limits need explicit errors or pagination.
- **Status:** proposed.

## 5. Track stored artifacts independently from recent builds

A lineage is the sequence of outputs from the same build step as input changes. The incremental builder searches recent lineage entries for a reusable base. A bounded history is reasonable for that search. It is insufficient as the list of everything stored on disk.

For example, build 33 can evict build 1 from a 32-entry history even though build 1's files still exist. If GC only searches that list, those files disappear from its knowledge and cannot be collected. Conversely, a file can be old while a paused query still needs it. Age and search-history position do not establish that deletion is safe.

Use existing immutable payloads and codecs, with authoritative artifact descriptors listing owned payloads and dependency identities. Immutable snapshot manifests list artifact roots. An atomic current-snapshot pointer selects a complete manifest for each build identity. Lineage is derived lookup information: rebuilding or discarding it must not lose ownership facts. A separate inventory database is unnecessary if descriptors express the inventory.

Store listing traverses the defined namespace layout. A root named `reports.store` is still a root; punctuation is not evidence that it is a payload file.

### Give the writable namespace one process owner

A serving process knows its active leases; a separate `store gc` process does not. An independent collector could delete the old dataset a serving query is using even if the server's lease accounting is perfect.

For the first implementation, hold an OS-backed exclusive lock for the writable namespace's entire lifetime. That owner runs builds, publication and online GC. A second process uses another namespace/store or fails clearly. Standalone GC acquires the same lock and runs only while the namespace is offline. The browser memory store implements the logical contract without filesystem persistence or an OS lock.

This deliberately excludes multi-process shared stores and distributed leases. If simultaneous independent writers/readers become necessary, design that coordination separately.

### Publish complete state in a recoverable order

```text
1. Write payloads under owned staging.
2. Close writers and validate counts, checksums and codec metadata.
3. Move complete immutable payloads into the namespace.
4. Atomically publish artifact descriptors.
5. Publish the complete immutable snapshot manifest.
6. Atomically replace the current-snapshot pointer.
7. Update derived lineage lookup data.
```

A crash before step 6 leaves the old snapshot current. A crash afterward can leave lineage stale, but the new current snapshot must be readable. Rename atomicity is insufficient to claim power-loss durability: implement and test the needed file and directory synchronization at the supported platform boundary.

GC marks from current snapshots, configured retained snapshots, active query/builder leases and temporary build ownership. It follows descriptor dependencies transitively, then sweeps unmarked owned artifacts. Serialize publication with the mark/sweep decision so a durable candidate cannot be collected just before publication. Missing or corrupt ownership metadata aborts destructive GC with an actionable error; unknown files are not automatically safe to delete.

Hard-linked files are owned through artifact directories. Removing one directory's links must preserve another artifact's links. Where possible, report logical bytes and actually reclaimed disk space separately. Define historical retention deliberately, including excluded-note privacy. A retention count does not replace leases or set a maximum query duration.

### Decision: descriptors, manifests and one writable owner

- **Context:** recent history cannot describe all stored files, and another process cannot observe live in-memory users.
- **Options considered:** extend lineage indefinitely; add an inventory database/distributed leases; use descriptors/manifests with an exclusive namespace owner.
- **Decision:** make descriptors/manifests authoritative and require one owner for publication and collection.
- **Rationale:** this meets the current local deployment without an additional database or coordination service.
- **Consequences:** standalone GC is offline; supported filesystem durability and lock behavior require tests.
- **Status:** proposed.

## 6. Make asynchronous outcomes stable

The sample, decide and rerank bridges all start work outside Goja, listen for cancellation, post events back to the VM owner, settle a promise and clean up. Duplicating that sequence allows small ordering differences to change public behavior.

Consider a provider returning an error. A deferred cleanup cancels a derived context before the posted callback runs. If the callback inspects that context to decide what happened, it reports cancellation instead of the provider failure. Cleanup has changed the meaning of completed work.

The proposed runner accepts exactly one terminal outcome before cleanup mutates cancellation state. A discriminated union makes the result explicit:

```ts
// Proposed semantic categories, not existing public spelling.
type Outcome<T> =
  | { kind: 'success'; value: T }
  | { kind: 'cancelled'; cause: Cancellation }
  | { kind: 'deadline'; error: OperationError }
  | { kind: 'failed'; error: OperationError }
  | { kind: 'shutdown'; error: OperationError };
```

```go
// Illustrative lifecycle; the implementation remains asynchronous.
func RunOperation(ctx context.Context, spec Spec) Future {
    // Install cancellation before work starts; use an owned task/errgroup.
    value, err := spec.Execute(ctx, spec.Emit)
    outcome := spec.Classify(value, err, context.Cause(ctx))
    // Accept outcome once, before cleanup cancels a derived context.
    // Post settlement onto the VM owner; release resources even if it closed.
    return SettleOnOwner(outcome)
}
```

Define a race rule at the acceptance boundary: a result accepted before a cancellation transition stays terminal; otherwise cancellation wins. Do not reclassify it in the later VM callback. Detach abort listeners on settlement. VM disposal can prevent JS promise delivery, but must not prevent task/resource cleanup. Use errgroup for related goroutines and transfer immutable Go values or bytes across goroutines. Worker goroutines must never access Goja values directly.

Reuse the lifecycle, retaining provider-specific request conversion, validation, tool execution and protocol errors. Streaming tool calls need an explicit request/reply hook on the VM owner; a one-shot rerank callback is not sufficient for that interaction. Do not bypass the runner to recover missing tool semantics.

Model fallback remains a small separate policy fix. Make selection eligibility and ordering explicit and deterministic, covering the reported empty enabled-model list with an eligible fallback. The exact policy must be verified against the provider configuration during implementation rather than invented here.

## 7. Share a build without giving one request control of it

A waiter is a caller waiting for a build. Request A starts build J. Request B needs the identical build and joins J. If J uses A's abort signal, A disconnecting cancels B's work too. The shared promise is not the problem; inheriting the first caller's lifetime is.

Give J its own context linked to engine shutdown. The shared-job key includes the full normalized build identity and every option affecting semantics. Each waiter separately registers interest and subscribes to job progress. A cancels by detaching A; B remains interested, so J continues.

```text
A joins J --> J starts privately
B joins J --> same semantic build, second waiter
A cancels --> remove A; J continues for B
B cancels --> cancel J if no background owner remains
shutdown --> cancel J and its waiters; join cleanup
```

A background refresh can explicitly own interest without a waiting HTTP request. If all request waiters leave but that owner remains, the job continues. If nobody remains, cancel the job. A late waiter arriving while cancellation is settling must not join the doomed promise: wait for termination and start/retry a new job. Release candidate resources on every unsuccessful completion.

Progress belongs to the job, not A's captured request scope. Fan it out to subscriptions using job/operation IDs for attribution. Bound each subscriber's queue. A slow or closed subscriber loses its own subscription/request; it cannot block the build or cancel B. Keep the registry small and in-memory; there is no requirement for durable scheduling.

**Contract:** a request controls its own interest; the job registry controls shared work based on remaining interests and shutdown.

## 8. Bound streaming writes and release request capacity

An SSE client can stay connected while it stops reading. A server writer may then block, retaining an inflight slot and preventing later work. A bounded queue alone does not solve a blocked network write. A fake writer that never blocks cannot demonstrate the fix.

Direct-query and verb endpoints should use one response helper. Domain execution produces typed events and a terminal outcome; the helper manages admission, subscriptions, JSON/SSE framing and request cleanup. Provider event types remain independent of HTTP.

One goroutine owns all writes and flushes. Before each write or flush, establish a bounded write deadline using `http.ResponseController.SetWriteDeadline`; use the controller's error-returning `Flush`. Include initial headers, heartbeats and terminal messages in this policy. Response wrappers must support controller unwrapping. If deadline capability is unavailable, fail setup rather than stream with an unbounded wait. After a write deadline has been exceeded, terminate the transport; do not try extending an already-failed stream.

```text
acquire request slot --> subscribe/start operation --> one writer pumps events
                                                   |
                            success / write error / flush error / overflow
                                                   |
                    stop writer, detach/cancel request, bounded join, release slot
```

A request-local failure removes this request's interest in a shared build; it does not cancel other waiters. A stream emits at most one terminal event. After a network failure the server cannot promise that any terminal event reached the client.

Bound bytes as well as event count. A queue of ten events can still contain ten enormous strings; coalesced text and heartbeats must not create unbounded memory. Per-write deadlines allow a healthy stream to remain open for a long operation without imposing a short total duration on it. Use the same admission scope and terminal error vocabulary for queries and verbs.

Acceptance requires a real socket test: connect, stop reading while enough data is sent to exercise backpressure, observe bounded termination, then prove that request capacity is available again. Also test controller/flush errors and races between final output and disconnect.

## 9. Give every structural graph reference the same treatment

A graph node can depend on another node's data, artifact, decision output or pinned output. Today, rename can update ordinary inputs yet leave `route.needs` unchanged in a decision condition. The editor says rename succeeded but the compiled plan still points to the old node. Export has a related failure: deleting a classifier without updating its dependent conditions leaves dangling references.

Normalize concise DSL references once in the compiler:

```js
// Concise input syntax may remain.
when: { decision: 'route.needs', min: 0.5 }

// Proposed canonical IR.
when: {
  source: { kind: 'decision', node: 'route', output: 'needs' },
  minimum: 0.5
}
```

Each component declares structural references through a common schema/visitor. `references(node)` enumerates data, artifact, decision and pinned-output dependencies; `rewriteReferences(node, mapping)` rewrites the same set. Compiler checks for existence, output type, phase and cycles use that enumeration. Rename checks target uniqueness, applies a complete edit transaction, and validates once afterward.

Deletion needs explicit policy: reject if dependents remain, cascade, or use a declared replacement. A general editor cannot guess that removing a decision should make a production retriever unconditional.

### Export makes its own explicit projection

A lexical standalone page cannot execute every server capability. Mark capabilities such as local text index, external model and host filesystem; choose the supported graph from those declarations. Being submit-triggered does not itself make a node unsuitable for export.

For lexical export, explicitly define the projection that omits a decision-only classifier and makes its retrieval unconditional. Rewrite references according to that policy and validate the final graph before serialization. Bundle a normalized portable dataset; do not assume `engine.files()` contains every server-side source.

Arbitrary callback JavaScript is opaque to structural rename. String replacement could change unrelated strings and miss computed references. Graph-aware callbacks instead declare named dependency bindings in their component options. Rename changes the binding's reference, not callback source. Reject transformations that require undeclared opaque references to be rewritten.

### Decision: common traversal, transformation-specific policy

- **Context:** compilation, editing and export must recognize the same dependencies but need different deletion behavior.
- **Options considered:** independently maintained field scans; common reference schema/visitor.
- **Decision:** share enumeration/rewriting and validate each resulting graph; keep projection/deletion policy explicit in its caller.
- **Rationale:** reuse removes missed references without pretending every transformation has the same semantics.
- **Consequences:** components declare dependencies; opaque callbacks constrain supported edits; capability projection needs end-to-end export tests.
- **Status:** proposed.

## 10. Review rules after the explanations

Use these concise invariants during implementation review. The preceding sections explain the actors, timing and failure each rule addresses.

1. Every record reference names a dataset version and record ID.
2. Fresh, loaded and delta datasets have one public collection behavior.
3. One query uses one snapshot through all outgoing record construction.
4. Every opened native handle has one registry owner and explicit users.
5. A handle closes only after its final user releases it.
6. Recent lineage history is never the authoritative stored-artifact inventory.
7. One waiter cannot cancel work another waiter or background owner still needs.
8. An async operation accepts one immutable terminal outcome before cleanup.
9. Every write/flush has a bounded wait; a failed transport ends.
10. Every structural graph reference participates in common traversal and validation.
11. Ordinary searches require no application close/release calls.
12. Each replacement removes its old path in the same phase.

These are enforced runtime/module rules, not a claim of linear ownership enforced by JavaScript or TypeScript. Private handles, centralized construction, runtime validation and sequence tests remain necessary. Trusted native implementations can still make mistakes.

## 11. Keep the implementation small and migrate all consumers

Use the existing extracted modules as starting points. Introduce focused ownership boundaries for graph, datasets, snapshots/resources, operations and execution as those contracts land. Keep the `rag` facade thin and maintain the shared deterministic bundle. Update host loading and standalone export together rather than creating browser/Goja copies.

| Actual consumers | Mechanism to share | Specialized behavior to retain |
|---|---|---|
| Memory/disk records | Dataset API and conformance tests | Codecs, shards, page access |
| Fresh/load/delta readers | Resource adoption | Build and delta algorithms |
| Sample/decide/rerank | Async lifecycle | Protocol errors, tool and request semantics |
| Query/verb HTTP handlers | Admission and response lifetime | Parsing and operation body |
| Compile/edit/export | Structural references | Edit/projection policy |
| Timer/manual/post-sync refresh | Build/refresh operation | Scheduling and access checks |

Each new concept should retire an existing responsibility or duplicate mechanism. Avoid a generic service container, dependency-injection framework, plugin system or durable workflow scheduler. The executor should not need component-specific branches for behavior that belongs in a component contract.

Delete `unionChunks`, consumer-side RecordView checks, scattered array conversion, one-rebuild retirement, direct closes in engine callers, first-request ownership of builds and duplicated HTTP pumps as replacements land. Enumerate live uses before deleting obsolete record formats/codecs; browser memory algorithms and quality-comparison backends remain useful.

Keep recognizable `.from().parse().chunk().index()` and `.retrieve().filter().rerank().pack().answer()` programs, `rag.run` and `result.top`. Their clarity is the reason to retain them. Change bake/custom callbacks, raw-hit APIs and inspection lookups directly. Do not silently accept `result.chunk(stringId)` when the dataset is ambiguous; expose a deliberate reference constructor if low-level callers need one.

Migrate traces, evidence, citations, GUI, Evals, sweeps, saved plans, presets and export along with each contract. Bump serialized IR and artifact namespace versions. Local caches are disposable, but deletion of user directories must be explicit and offline under the ownership lock. Document the cleanup command; do not silently remove old stores or implement a legacy reader. Update examples and fixtures in each API cutover.

TypeScript checks the new shapes; Biome handles formatting/lint. Neither validates native/JSON input nor proves lifetime safety. Keep declarations with their owning implementations. Preserve semantic behavior rather than incidental JSON object insertion order: 010's parity tests canonicalize object keys while retaining array order.

## 12. Implementation sequence for an intern

Before editing ownership, run a reports example, inspect its compiled plan, and follow one query through session execution and served result construction. Then locate each historical symbol in current sources. Choose one integration branch after the intended 010 merge; do not repeatedly patch divergent review-stack copies. This guide authorizes no merge, force push or review-thread resolution.

Each phase ends with a complete working path, focused tests and removal of the replaced mechanism. Do not extract empty abstractions first.

1. **Characterize the baseline.** Write deterministic regressions for all twelve reports and record which actually fail on the baseline. Cover fresh/stored/delta outputs and equal-count changed bake input. If the stack will be used before the full migration, prioritize the P1 SSE fix inside the future common transport rather than adding a temporary second pump.
2. **Datasets and provenance.** Implement strict typed dataset/reference contracts and memory/persistent conformance. Bind indexes to dataset identities. Migrate bake, hit, trace and evidence consumers, including browser consumers; remove implicit unions.
3. **Snapshots and native resources.** Add serialized scoped acquisition, centralized adoption and private candidate ownership. Hold leases through event/result record construction; return detached results. Migrate evaluations and inspection and remove rebuild-count GC/unmanaged closers.
4. **Durable inventory.** Add descriptors/manifests, namespace locking and mark/sweep. Derive lineage lookup. Correct dotted-root traversal. Verify crash boundaries and active-reader safety.
5. **Operations and shared builds.** Migrate sample, decide, rerank and build joining to owned lifetimes. Keep provider details and tool hooks. Verify acceptance races, VM closure, late waiters and progress fanout. Implement deterministic model fallback as a focused independent correction.
6. **HTTP transport.** Route query/verb responses through the common bounded writer and cleanup scope. Exercise non-reading clients, flush errors, terminal races and request-slot recovery.
7. **Graphs and export.** Add canonical references and common traversal. Migrate edits and compiler validation. Apply capability projection and portable dataset export; reject unsupported opaque references explicitly.
8. **Delete and integrate.** Remove dead code/obsolete formats; update examples, help and API docs. Run vault/report workflows on the final contracts and measure performance/resources before declaring completion.

Commit at coherent validated boundaries. Use focused tests during each change and full checks at integration boundaries. The relevant commands include:

```sh
pnpm install --frozen-lockfile
pnpm build
pnpm check
go test ./pkg/ragjs ./pkg/runner ./web
go test ./...
go build ./...
```

Run additional affected native/artifact/server package tests while implementing their changes. `pnpm check` combines strict checking, Biome, exact generated bytes and bundle/publication/export/module-graph tests. Node VM/minimal-DOM tests do not prove visual browser compatibility; Go tests exercise actual Goja/native behavior. Run servers in tmux and inspect with capture-pane; when stopping a web server on a port, use `lsof-who -p PORT -k` as required by project instructions.

## 13. Verification and completion criteria

Use synthetic corpora and fake providers with explicit pause/release barriers. Do not make race tests rely on arbitrary sleeps. Counting closers establish exact ownership transitions; supplement them with real file descriptor checks where practical. Keep real sockets for transport backpressure.

| Contract | Sequence and assertion |
|---|---|
| Dataset | Compare memory, stored and delta records, missing IDs, order and nested immutability through one suite |
| Bake cache | Use one callback API cold/warm; change bytes without changing counts and observe invalidation |
| Provenance | Give two datasets the same logical IDs and different bytes; retrieve/fuse/pack/display according to explicit policy |
| Snapshot | Pause a query in a provider, publish two later snapshots, resume and construct output from the original records |
| Resources | Exercise fresh/load/delta, failed adoption, later-stage failure and shutdown; close each opened handle exactly once |
| Shared resources | Retire A while B still uses its unchanged index; index remains open until B releases |
| Results | Release the query snapshot/unused handles; returned hits, evidence, trace and configured preview remain readable |
| Inventory | Create 40+ versions, retain three, collect the expected reachable set independent of 32-entry lineage search |
| Locking | Reject another namespace owner and standalone GC while serving; allow offline GC under the same lock |
| Crash recovery | Inject failure between publication steps; reopening selects complete old or complete new state, never a mixture |
| Shared jobs | Cancel A while B waits; B succeeds. Cancel all with/without background owner. Retry late waiter after doomed job terminates |
| Job events | Attribute by job/operation ID; a slow subscription cannot block J or cancel another subscriber |
| Outcomes | Provider failure stays failure after deferred cleanup; caller cancel, deadline and shutdown remain distinguishable |
| SSE | Real non-reading socket, failed flush/controller setup, overflow and terminal races end within bounds and free capacity |
| Graph edits | Rename, remove and project all reference kinds; validate typed dependencies or reject edits explicitly |
| Export | Export a classified source plan to functioning unconditional lexical search with portable data |
| Local fixes | Dotted root lists saved outputs; empty enabled list selects an eligible fallback deterministically |

Measure before/after on the same declared fixture: cold build, warm startup, query latency, changed-note refresh, memory, file descriptors and artifact growth. Report fixture sizes and conditions. Detached results and richer references can increase allocation; investigate a regression rather than claiming performance parity from unit tests.

Completion requires a passing regression or documented reviewed disposition for every finding; removal of the replaced mechanisms; matching docs and APIs; functioning memory/browser and persistent/Goja examples; and verified shutdown, cancellation and publication sequences. Moving code into a module or naming a class `Snapshot` does not resolve a review finding.

## 14. Remaining risks and boundaries

The highest-risk boundary is the last record lookup: releasing a lease before trace, citation or response construction is complete recreates the old bug. Shared immutable artifacts are the next risk: snapshot retirement must release uses rather than directly close shared handles. Cross-process collection remains unsafe without the namespace lock even if in-process leases are perfect.

The largest migration is record identity across all consumers. Review raw-ID uses explicitly, including evaluations, GUI caches and previews. Fusion equivalence, detached-result limits, retained-generation policy, duplicate adoption behavior and filesystem durability details need concrete choices and tests during implementation. The proposed ownership and failure rules constrain those choices; exact method spelling remains open.

The SSE default timeout/byte budgets and model eligibility ordering are not specified by the original guide. Set and document them with their implementation tests. A sensible name or type cannot substitute for deciding these policies.

Distributed indexing, remote datasets, simultaneous multi-process stores, full Obsidian rendering and production authentication are outside this design. Reindex status/manual refresh can later reuse the application operations; they should not create a second refresh path.

## 15. References and author self-check

Primary local references are [the original guide](01-intern-guide-to-consistent-datasets-snapshots-operations-and-graph-contracts.md), `pkg/ragjs/src/README.md`, the current source map in section 2, and [the RAGWB-010 guide](../../RAGWB-010--modular-runtime-sources-and-typescript-tooling-foundation/design-doc/01-intern-guide-to-modular-runtime-sources-typescript-and-build-tooling.md). Review sources are [PR #2](https://github.com/go-go-golems/rag-workbench/pull/2), [PR #3](https://github.com/go-go-golems/rag-workbench/pull/3), [PR #4](https://github.com/go-go-golems/rag-workbench/pull/4), [PR #5](https://github.com/go-go-golems/rag-workbench/pull/5), [PR #6](https://github.com/go-go-golems/rag-workbench/pull/6) and [PR #7](https://github.com/go-go-golems/rag-workbench/pull/7). Those review reports were carried forward from the investigation, not newly reproduced or re-reviewed for this rewrite.

For comprehension, I replaced the compressed result-packaging rule with the `chunk-42` sequence in section 4: the reader can identify which lookups occur, which snapshot they use and when copying permits release. I also revised graph transformation guidance around the concrete rename/export failure in section 9, explaining why shared traversal and distinct transformation policies are both needed.

Remaining weaknesses: this is a design, so several API types are illustrative and durable-store platform details remain unverified. The small model-fallback fix still needs source-level policy confirmation. The broad migration/test tables guide implementation but cannot establish that all current consumers have been enumerated. These limits require implementation evidence; no self-check proves that the candidate skill succeeded.
