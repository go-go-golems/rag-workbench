---
Title: V2 intern guide candidate B
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
LastUpdated: 2026-11-08T21:10:09.813052438-05:00
WhatFor: ""
WhenToUse: ""
---

# Building an engine whose data, work and resources stay consistent

## 1. What this design changes

RAGWB-009 proposes a simpler engine contract for building searchable corpora and answering queries while those corpora change. A component should receive the same collection API after a cold build and a disk load. A query should keep reading the version it started with while refresh publishes a new one. A shared build should continue when one of its callers disconnects. The engine should enforce these rules without asking ordinary application code to close readers or manage query leases.

This guide explains the proposed contracts, the failures they address and a sequence for implementing them. It does **not** report their implementation or validation. The twelve review findings came from inspection of the stacked PRs against baseline `0854c6a`; this documentation work did not independently reproduce all twelve. Each needs a regression or an explicit reviewed disposition before being called resolved. The ticket's nine implementation tasks remain open.

The source layout has changed since that inspection. RAGWB-010 is implemented in `cac9262`, with documentation checkpoint `c267f91`. It extracted 26 modules into `pkg/ragjs/src/` and established TypeScript, Biome and deterministic bundle generation. Four modules are strict TypeScript; the other 22 remain JavaScript. It did not fix 009's semantic findings. PR #8 is open against the published `task/ragwb-008-vault-ui` parent, and the intended sequence is 009 implementation after that merge. This experiment changes documentation only.

There is no deployed format or API to preserve. Update callers directly and remove replaced paths. Keep the concise corpus/plan builders, shared browser/Goja implementation, useful parsing and indexing algorithms, provider behavior and meaningful evaluations. Do not create compatibility adapters, a second engine, a durable scheduler or a general dependency-injection framework.

## 2. Follow one document through the system

A **corpus** describes work performed when source documents change: read files, parse Markdown, split documents into chunks and build indexes. A **plan** adds work performed for each query: retrieve candidates, combine rankings, filter, rerank, pack evidence and possibly ask a model for an answer. Builders produce a graph; each node describes a component, its parameters, dependencies and output kinds.

```js
// Existing public style to retain; this is not a new ownership API.
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

Suppose `/data/reports/engine.md` contains a section about cancellation. Building the corpus creates a document record, one or more chunk records and an index over those chunks. A **dataset** is the read-only collection of document or chunk records from one particular build output. An **index** searches one exact chunk dataset. A **persistent artifact** is a saved output, addressed by a key, that may contain records or index files. A **lineage** groups successive outputs of the same build step so incremental work can find an earlier version and compute a delta.

The server runs the JavaScript engine inside Goja. Go native components supply authorized filesystem access, local indexes and disk-backed records. Browser examples run the same conceptual pipelines using memory implementations. Memory and disk implementations may differ in I/O, allocation and ranking algorithms; callers still need consistent collection and lifetime semantics.

In the proposed design, a **snapshot** groups the validated datasets and indexes that queries may use together. Refresh builds another snapshot privately, then makes it current in one publication step. Request Q acquires the current snapshot A; refresh can publish B while Q awaits a model. Q continues using A until it has copied the data needed by its outgoing events and result. A **lease** is the engine's record that Q is still using A. Application code does not manage this record.

```text
corpus/plan builders --> compiler --> build or refresh
                                        |
                             private candidate outputs
                                        |
                              validate and publish B
                                        |
                          current snapshot for new queries

query Q --> acquire snapshot A --> retrieve --> await model
                    |                              |
                    +--> resolve A's text/citations +--> detached result
                    |                                      |
                    +-------------- release A <------------+
```

The engine also opens native resources, such as database readers or search indexes. A resource **registry** owns and closes those handles. Snapshots explicitly retain the entries they use, and active query leases keep their snapshots alive. This separates three questions: which version should a query read, who still needs that version, and who closes its underlying handles.

Before changing lifecycle code, run a reports example and read its plan. Then follow `Session.run` through query execution and `pkg/runner/serve.go` through response construction. A query's lifetime includes more than its retrieval loop.

## 3. Evidence and source navigation

These are reported review findings, with the review's priority labels. The proposed prevention is a design target, not a passing test result.

| Review | Reported failure | Proposed prevention |
|---|---|---|
| #2 / P2 | Stored duplicate chunks choose a different version from cold arrays | Carry dataset provenance and require explicit combination rules |
| #2 / P2 | Bake callback gets a record view where it expected an array | One dataset API for fresh and loaded outputs |
| #2 / P2 | Listing skips a store root whose basename contains a dot | Traverse the defined store layout |
| #3 / P2 | Two refreshes close artifacts an awaiting query still uses | Snapshot leases and explicit resource users |
| #3 / P2 | Versions beyond the 32-entry lineage history become uncollectable | Keep complete artifact inventory independently |
| #3 / P2 | Delta-created readers miss runtime cleanup | Adopt every opened resource through one registry path |
| #4 / P1 | A non-reading SSE client retains an execution slot indefinitely | Bound writes/flushes and join request cleanup |
| #4 / P2 | Cancelling the first waiter cancels another caller's build | Give shared jobs their own lifetime |
| #5 / P2 | Deferred context cleanup changes failure into cancellation | Fix the terminal outcome before cleanup |
| #6 / P2 | Empty enabled-model list blocks a valid fallback | Explicit deterministic model selection |
| #7 / P2 | Lexical export references removed decisions | Project by capability and rewrite dependent references |
| #7 / P2 | Rename misses decision references | One reference traversal for every structural consumer |

Review sources: [PR #2](https://github.com/go-go-golems/rag-workbench/pull/2), [PR #3](https://github.com/go-go-golems/rag-workbench/pull/3), [PR #4](https://github.com/go-go-golems/rag-workbench/pull/4), [PR #5](https://github.com/go-go-golems/rag-workbench/pull/5), [PR #6](https://github.com/go-go-golems/rag-workbench/pull/6), [PR #7](https://github.com/go-go-golems/rag-workbench/pull/7). The dotted-root and model-fallback cases need small local fixes and regressions; neither requires a new subsystem. The fallback fix must define eligibility and stable selection order and test an empty enabled list; this guide does not invent a provider-specific ordering rule.

Use the current source modules below for implementation. Do not edit the generated bundle directly.

| Concern | Current source |
|---|---|
| Collection/lazy persistence | `pkg/ragjs/src/engine/records.js` |
| Build sharing, artifacts and engine GC | `pkg/ragjs/src/engine/engine.js` |
| Query execution and result decoration | `pkg/ragjs/src/engine/session.js` |
| Request capture, cancellation and event scope | `pkg/ragjs/src/engine/scope.ts` |
| Builders, compilation and graph editing | `pkg/ragjs/src/graph/graph.js` |
| Standalone HTML projection | `pkg/ragjs/src/export/html.js` |
| Public composition and registration | `pkg/ragjs/src/api/index.js`, `api/define.js` |
| Algorithms and record consumers | `pkg/ragjs/src/components/` |
| Evaluations and sweeps | `pkg/ragjs/src/evaluation/evals.js` |

Historical anchors in `pkg/ragjs/assets/rag.js` at `0854c6a` explain the findings: `finalize:1163` separately discovers input, `uses`, decision and signal references; `applyOps:1317` misses decision strings on rename; `unionChunks:1455` separates arrays and views and loses their precedence; `Engine.gc:1875` closes retired handles after rebuilds without query ownership; `resolveBakes:1939` passes storage-dependent inputs and hashes incomplete information; `artifacts:1966` creates a global chunk union with implicit primary-stream order; `buildShared:2039` inherits the first request's options and signal; `Session.run:2079` returns methods backed by artifacts; `exportHtml:2602` prunes before rewriting dependent references. These are historical line numbers, not current edit locations.

Read native and application boundaries alongside the runtime: `pkg/natives/persist.go` (`Persist.InstallEnv`, closer registration and no-op fallback), `pkg/natives/markdown_native.go` (delta reader creation), `pkg/artifacts/lineage.go` (`LineageRecord`, `GC`), `pkg/natives/rerank.go` (`rerankAsync`), `pkg/ragjs/sample.go` (`newSample`), `pkg/ragjs/decide.go` (`newDecide`), `pkg/server/sse.go` (`pump`), and `pkg/server/verbs.go`/`rag.go` (`handleVerb`/`handleRun`). In `pkg/runner/serve.go`, embedded `__serve.run` performs post-query packing with current artifacts: protecting retrieval alone would leave that generation mismatch intact.

`pkg/runner/runtime.go` constructs runtimes; `pkg/ragjs/module.go` loads the embedded bundle and binds native services. `web/app-shell/shell.js` maps remote responses to the GUI host interface. All are integration boundaries for these changes.

## 4. Make collections and record identity explicit

### Fresh and stored records use the same operations

A bake callback might count chunks or map them to IDs. Today the cold path can hand it an array, while the stored path hands it a record view that lazily reads disk. A callback written against `.map` then fails after persistence is enabled. Asking every callback to detect its backend spreads storage details across the application.

The proposal is a read-only dataset contract. For either backend, `get` retrieves the same record or returns `null`, `values` visits the same records in deterministic order, `size` gives the same count, and `materialize` explicitly traverses the dataset to return an array. Loading may require I/O or less memory; it does not change these meanings. Keep access synchronous for the current local hosts. Remote storage, if needed later, deserves a separate bulk boundary rather than speculative promises on every lookup.

```ts
// Proposed strict TypeScript sketches; record schemas/names need implementation review.
type RecordRef = Readonly<{ dataset: string; id: string }>;
interface Dataset<R> {
  readonly id: string; // immutable version identity
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): R | null;
  values(): Iterable<R>;
  materialize(): readonly R[];
}
interface ChunkDataset<R> extends Dataset<R> {
  document(chunkId: string): RecordRef;
  order(documentId: string): readonly RecordRef[];
}
interface Hit {
  readonly ref: RecordRef;
  readonly score: number;
  readonly reason?: string;
}
interface Index {
  readonly datasetId: string;
  search(query: string, options: SearchOptions): readonly Hit[];
}
```

`SearchOptions` stands for the owning index's validated options; this sketch is not a compilable declaration file. Implement types with the behavior they describe. Records returned to callbacks cannot permit mutation of authoritative data, including nested metadata. Freeze records deeply enough or detach them at the boundary, and test the chosen policy. An implementation may cache immutable decoded records. Ordinary components never see `close`, `__records`, `__ix` or codec selection.

```js
// Proposed callback contract. Migrate all repository callbacks together.
rag.bake(ctx => ctx.chunks.materialize().map(chunk => chunk.id));
rag.bake(ctx => ctx.chunks.size); // counting requires no explicit full traversal
```

The engine constructs these datasets. Application authors choose whether their callback needs a full array; they do not choose memory versus disk behavior. Use one conformance suite for both implementations and the delta path: iteration order, absent IDs, immutable records, materialization and provenance.

**Invariant:** fresh, persisted and delta-created datasets expose one collection contract.

### A record's logical ID does not identify its version

Suppose chunk `engine.md#cancellation` exists in dataset `plain-v1` and in `prefixed-v1`, where an enrichment component added a heading prefix. The logical ID is the same, but the text differs. An index over `prefixed-v1` must return the prefixed record, even if a plain dataset was loaded later. A global union keyed only by chunk ID cannot express that requirement.

Each hit therefore includes both dataset version and record ID. Every consumer carries that reference: retrieval, fusion, packing, traces, context citations, evaluation and GUI display.

```js
// Proposed result from an index built over prefixed-v1.
const hit = {
  ref: { dataset: 'prefixed-v1', id: 'engine.md#cancellation' },
  score: 0.87
};
const record = snapshot.resolve(hit.ref);
```

Fusion deduplicates exact references by default. Two indexes over one dataset can combine rankings naturally. For indexes over different enrichments, choose a deliberate policy: project both to a canonical dataset before fusion, or use an ordinary combine component that documents its conflict rules. The engine must not guess using stream order or “last wins.”

Document identity also crosses corpus boundaries. Two sources can both contain `engine.md`; a title or shortened slug is insufficient. Use source identity plus vault-relative path for logical document identity. Derive index version identity from component/codec version, normalized parameters and input dataset identities. Bake cache identity likewise includes complete input dataset identities, callback source/configuration and component version. Equal record counts do not imply equal input: changing a sentence without changing the number of sections must invalidate dependent bakes.

**Invariant:** resolution uses `{dataset, id}`; cache identity includes inputs' versions, not just their sizes.

### Decision: one dataset interface and explicit provenance

- **Context:** arrays, views and global chunk unions expose different semantics at the same component boundary.
- **Options considered:** normalize everything eagerly to arrays; preserve backend detection; expose read-only datasets with explicit materialization.
- **Decision:** use datasets and versioned references; retain efficient lazy storage behind them.
- **Rationale:** callers get one contract without requiring whole-corpus decoding, and indexes can identify the exact bytes they searched.
- **Consequences:** raw-ID consumers and callbacks change together; conformance and end-to-end provenance tests are required.
- **Status:** proposed.

## 5. Keep a query's snapshot alive through result construction

### Refresh changes the current snapshot, not an active query

Request Q starts on A and retrieves a hit. It awaits a model. Refresh publishes B, then C. Closing A merely because two refreshes happened can break Q when it resumes. Even if the index remains open, looking up the hit in C may return changed text or no record. The proposed lease protects both resource lifetime and version selection.

A snapshot is an immutable mapping of validated build outputs to datasets/indexes. The engine retains its registry entries. A query retains its snapshot until all snapshot-dependent work finishes. Selecting the current snapshot and incrementing its lease count must be one serialized transition; refresh cannot retire it in between. Serialize JavaScript transitions on the runtime owner and synchronize any Go registry operations that cross goroutines.

```js
// Proposed control flow, not existing API.
async function query(plan, signals, request) {
  // Wait only if this plan has no usable published snapshot.
  await builds.ensure(plan, request.signal);
  return snapshots.withCurrent(plan.buildKey, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot);
  }); // release on success, failure, cancellation or materialization failure
}
```

A usable A continues serving during a background build of B. Build B writes privately and publishes only after complete validation. If B fails or is cancelled, A stays current. Partial outputs may help resume an internal build but are never a published query snapshot.

When retrieval returns `{ref, score}`, the engine still needs to add text, document title, heading, citation details and trace records for outgoing events and responses. Perform those lookups against Q's snapshot before releasing its lease. This is what “result construction” means here. It includes the runner's post-query response packing, not only `Session.run`'s computation.

```text
Q acquires A --> retrieve A --> await provider --> copy A's result records --> release A
                                  |
refresh: build B --> publish B ----+--> new requests acquire B
refresh: build C --> publish C --------> new requests acquire C
```

### The registry closes resources after their final user releases

The native registry is the one owner responsible for managing an opened database or index. Snapshot A and snapshot B can both use unchanged index I. Retiring A releases A's use of I; it cannot close I while B still needs it. Query leases keep retired snapshots' uses registered until those queries finish. Builders have private scopes that retain resources until publication transfers their use or failure releases them.

Every fresh opener, persistent loader and delta-created reader must adopt its resource immediately through the same path. The delta route cannot bypass registration. A resource-bearing runtime without a registry is a construction error; remove the no-op closer fallback.

```go
// Proposed small ownership boundary. Handle details remain internal.
type ResourceID string
type Registry interface {
    Adopt(id ResourceID, closer io.Closer) (Handle, error)
}

r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(id, r)
if err != nil { _ = r.Close(); return nil, err }
return NewChunkDataset(h), nil
```

On any later build failure, the scope releases uncommitted uses. Define duplicate-ID adoption explicitly: either reuse the registered entry and close the redundant newly opened handle, or reject adoption and close it. Neither path can leak the redundant handle or create independent owners of the same underlying resource.

Shutdown rejects new work, cancels operations, waits for them to settle, releases snapshots and closes the registry. Report a join timeout explicitly. Closing handles while workers still access them is not successful cleanup.

**Invariants:** one registry owns each opened resource; explicit users retain it; only the final release permits closure. Refresh count is irrelevant to query duration.

### Return data that remains readable without cleanup calls

A caller expects `await rag.run(...)` to produce a usable result. Requiring it to remember `result.close()` would export the engine's lifetime problem to every application. Instead, copy the bounded records required by the returned hits, trace, evidence and configured neighbor preview while the query lease is still held. `result.top()` and `result.chunk(hit)` read those detached records after release.

```js
// Proposed ordinary usage; no lease/close operation is public here.
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;
```

Copy the result's needed records, not the entire corpus. Declare size limits. If a requested trace exceeds its budget, return a clear limit error or an explicit pagination contract; do not silently omit records while returning hits that require them. Whole-corpus export and evaluations iterating all relevance labels use scoped internal snapshot operations.

Later GUI lookup is a separate request. Send snapshot ID plus record reference. The server resolves against a retained generation or returns an explicit stale-generation response. It must never reinterpret an old hit in the newest dataset. Browser caches key by generation/reference and clear when generations change. Retention does not revoke already delivered text and does not replace access checks.

### Decision: detached ordinary results, scoped bulk access

- **Context:** returned helpers currently reach live artifacts; callers should not manage native resources.
- **Options considered:** lease-owning public results; eager whole-corpus copies; bounded detached results with scoped internal bulk reads.
- **Decision:** detach ordinary results before releasing their query lease.
- **Rationale:** ordinary search remains simple while evaluations/export retain an efficient explicit lifetime.
- **Consequences:** budgets and allocation cost need measurement; no result may hide an unretained native handle.
- **Status:** proposed.

## 6. Track stored files independently of recent build history

### History helps choose a base; inventory tells us what exists

Incremental building needs a short search list of earlier outputs. Suppose it retains the most recent 32 versions. Creating version 33 evicts version 1 from that list, but version 1's files may still exist. If GC discovers files only through the list, it has lost the information needed to collect them. Increasing the history limit delays the failure without fixing the ownership error.

Keep the existing immutable payloads and useful record/index codecs. Extend artifact descriptors to list owned payloads and dependencies. Add immutable snapshot manifests listing artifact roots, and an atomic current-snapshot pointer per build identity. These descriptors/manifests form the authoritative inventory. Lineage remains a derived incremental-base lookup accelerator that can be rebuilt or discarded without losing ownership information. A separate database is unnecessary solely for this inventory.

GC starts from current snapshots, deliberately retained snapshots, active query/builder leases and temporary build ownership. It follows descriptor dependencies transitively and sweeps unmarked owned artifacts. Publication and the collection decision share one owner so GC cannot delete a candidate between its durable creation and publication. Missing or corrupt ownership metadata aborts destructive collection with an actionable error; an unfamiliar file is not presumed disposable.

Traverse the defined namespace layout, not punctuation in basenames. A root named `reports.store` must remain a valid root. For hard-linked payloads, each artifact owns its links; deleting one artifact's directory must not remove links belonging to another. Where practical report logical bytes separately from disk bytes actually reclaimed.

Retention is an explicit policy. Keeping three historical snapshots and keeping a query's snapshot until it finishes are different requirements. Historical retention also preserves formerly included notes; document the privacy consequence rather than treating longer retention as cost-free.

**Invariant:** truncating lineage search history cannot make stored artifacts undiscoverable to GC.

### One process coordinates a writable namespace

An in-memory lease protects A only from the collector that knows about that lease. A separate `store gc` process cannot see it. For the first implementation, the serving process holds an OS-backed exclusive namespace lock for its lifetime. It coordinates builds, publication, resource users and online GC. A second process fails clearly or uses a different namespace. Standalone GC acquires the same lock and therefore runs only offline.

The browser memory store obeys the same logical ownership contract without filesystem persistence or OS locking. Cross-machine leases and simultaneous independent writers are outside this design.

### Publish complete snapshots and recover after interruption

A snapshot pointer must never identify half-written data. Use the following proposed publication sequence:

```text
1. Write payloads in an owned staging directory.
2. Close writers and validate counts, checksums and codec metadata.
3. Move complete immutable payloads into the namespace.
4. Atomically publish artifact descriptors.
5. Publish the complete immutable snapshot manifest.
6. Atomically replace the current pointer.
7. Update derived lineage search information.
```

A crash before step 6 leaves the old snapshot current. A crash after step 6 can leave lineage stale but must leave the new snapshot readable. Rename atomicity alone does not prove power-loss durability: file and directory synchronization must match the supported filesystem/platform boundary. Add failure injection at every step and reopen the store. The expected result is a complete old or new snapshot, never a mixture.

### Decision: exclusive namespace ownership and manifest-based inventory

- **Context:** query leases live in one process, while files survive history truncation and crashes.
- **Options considered:** distributed lease coordination; offline-only stores; one serving owner with online GC and locked offline tooling.
- **Decision:** use one writable owner, immutable descriptors/manifests and atomic publication.
- **Rationale:** it matches the current deployment without adding a distributed coordination system.
- **Consequences:** independent processes cannot share a writable namespace; synchronization and crash recovery require platform tests.
- **Status:** proposed.

## 7. Give asynchronous operations a fixed outcome

Sample, decide and rerank each bridge JavaScript requests to asynchronous Go work. They all need context propagation, abort handling, event delivery, settlement on the Goja owner and cleanup. Their provider requests, protocol errors, validation and tool behavior differ. Share the first set of responsibilities; keep the second set in supplied provider functions.

The reported rerank failure concerns timing. Go work returns a provider error and schedules a callback on the runtime owner. Deferred cleanup cancels its derived context before that callback runs. If the callback then inspects the context to decide what happened, it can relabel the provider error as cancellation. Classify and store the terminal outcome before cleanup changes the context. The callback delivers the stored outcome.

```ts
// Proposed terminal vocabulary; keep payload types with their implementation.
type Outcome<T> =
  | { kind: 'success'; value: T }
  | { kind: 'cancelled'; cause: unknown }
  | { kind: 'deadline'; cause: unknown }
  | { kind: 'failure'; error: unknown }
  | { kind: 'shutdown'; cause: unknown };
```

```go
// Illustrative ordering; real execution is asynchronous and owned by an errgroup.
func RunOperation(ctx context.Context, spec Spec) Future {
    // Register abort handling before work starts.
    value, err := spec.Execute(ctx, spec.Emit)
    outcome := spec.Classify(value, err, context.Cause(ctx))
    // Atomically accept one outcome before cleanup cancels derived contexts.
    // Post the accepted immutable Go payload to the VM owner exactly once.
    // Detach listeners and release resources even if the owner has closed.
    return SettleOnOwner(outcome)
}
```

Define the cancellation race at the runner: a terminal result accepted before the cancellation transition stays terminal; otherwise cancellation wins. Use that rule consistently for success, provider failure, caller cancellation, deadline and shutdown. Runtime disposal may prevent JavaScript callback execution, but cannot prevent Go task/resource cleanup. Detach abort listeners on settlement and use `errgroup` for related Go goroutines.

Only immutable Go values or bytes cross worker goroutines. Streaming tools need a supported request/reply hook to the VM owner; they cannot touch a Goja value directly from a worker. A one-shot rerank helper is insufficient unless the shared runner explicitly supports that richer interaction.

**Invariant:** every operation accepts one immutable terminal outcome, and cleanup cannot reclassify it.

## 8. Share builds without sharing a request's cancellation

A **waiter** is one caller waiting for a shared build. Request A starts build J, and B discovers it needs the same output. Reusing A's promise is useful; reusing A's abort signal is wrong. A disconnects, but B still needs J. Give J its own context, linked to engine shutdown, and track A and B's interest separately.

```text
A waits ----+
            +--> J builds private candidate --> validate --> publish
B waits ----+

A cancels       remove A and its event subscription; J continues for B
B also cancels  cancel J unless a background refresh owner still needs it
engine closes  cancel J and all waiters; join task cleanup
```

The job key contains the full normalized build identity, including every option that affects semantics. It is not enough that two requests name the same plan if their build options differ. A waiter cancellation detaches only that waiter. Cancel the job when no waiter or explicit background-refresh owner remains. If C arrives after J has begun cancellation but before J terminates, do not attach C to a doomed promise: let it start/retry after termination.

Progress belongs to J. Fan events out to waiter subscriptions, carrying job/operation IDs so each request can attribute them. Do not permanently bind progress to A's request scope. A slow or closed subscription cannot block J or cancel B; bounded subscriber failure detaches only the affected request.

Ordinary callers still supply their cancellation signal. The engine translates it into waiter membership; callers do not coordinate with each other. Timer, manual and post-sync refresh should invoke this same build/refresh operation, with their own trigger scheduling and access checks.

**Invariant:** cancelling one waiter cannot cancel work that another waiter or background owner still needs.

## 9. Bound the HTTP stream and finish its request lifetime

SSE sends events while an operation runs. If a client stops reading, the operating system eventually fills its socket buffers and a write can block. A bounded event queue alone does not prevent the writer from blocking forever, so the handler can retain an admission slot even after useful work should end.

Both direct-query and verb endpoints should call one response helper. Domain execution emits typed events and returns a terminal outcome. The helper handles admission, subscription, JSON/SSE framing and request cleanup. Provider code should not know HTTP framing. Use the same terminal-error vocabulary and admission scope for both execution forms.

The SSE writer is the only goroutine that writes or flushes the response. Before each write/flush, set a bounded write deadline with `http.ResponseController.SetWriteDeadline`; use the error-returning `Flush`. Include initial headers, heartbeats and the terminal event. Response wrappers must support the controller's unwrapping path. If the server cannot enforce a deadline, fail setup rather than opening an unbounded stream. An already-exceeded write deadline cannot be extended according to the installed Go API; after that failure, terminate the stream.

On write or flush failure, the writer stops, cancels/detaches this request and returns through a bounded join. Queue overflow has the same request-local effect. For shared work, detaching this request still follows the waiter rules above. At most one terminal event can be delivered; failed networks cannot guarantee delivery of even that event. Internal terminal outcome and successful client receipt are separate facts.

Bound bytes as well as queue entries: a single accumulated text item can otherwise grow without limit. Heartbeats and coalesced provider text count toward those bounds. Per-write deadlines let a healthy long stream continue without a short global timeout.

**Invariants:** every network write/flush has a bound; transport failure ends that transport and releases its request capacity within the cleanup policy.

Test with a real socket whose client stops reading. A fake `ResponseWriter` can cover error handling but cannot establish that operating-system socket writes obey the configured deadline. Assert that the admission slot becomes available again, not merely that an error was logged.

### Decision: share operation lifecycle and transport separately

- **Context:** sample/decide/rerank repeat bridge lifecycle, while query/verb handlers repeat response pumping.
- **Options considered:** one HTTP-aware operation framework; separate endpoint-specific implementations; a small operation runner plus one response helper.
- **Decision:** share each mechanism at its actual boundary.
- **Rationale:** provider semantics remain testable without HTTP, and both endpoint forms get the same stream bounds.
- **Consequences:** tool request/reply hooks and request-to-job detachment must be explicit integration contracts.
- **Status:** proposed.

## 10. Make graph edits visit every reference

A graph reference can mean a data input, artifact dependency, decision output, signal dependency or pinned output. Currently separate consumers discover or rewrite different subsets. Renaming `route` can update ordinary edges yet leave `when.decision: 'route.needs'` unchanged. The edited graph then contains a name that no longer exists.

Keep concise public syntax where useful, and normalize structural references once during compilation:

```js
// Public syntax.
when: { decision: 'route.needs', min: 0.5 }

// Proposed canonical IR.
when: {
  source: { kind: 'decision', node: 'route', output: 'needs' },
  minimum: 0.5
}
```

Component schemas declare all structural references. A shared `references(node)` enumerates them, and `rewriteReferences(node, mapping)` rewrites them. Compilation uses that enumeration to validate existence, output type, phase constraints and cycles. Rename checks target uniqueness, rewrites every reference and validates at the end of the edit transaction. Deletion requires an explicit dependent policy: reject, cascade or a declared replacement. Do not silently invent behavior for a deleted decision.

Export needs an additional policy. Suppose a server plan runs an external classifier, then enables lexical retrieval only when that decision says to search. A browser-only lexical page cannot run the classifier. Removing its node while leaving the condition makes the export invalid. For this documented lexical projection, remove the decision-only classifier and make its dependent retrieval unconditional, then validate the resulting graph before serialization.

Choose supported nodes by declared capabilities such as local text indexing, external models and host filesystem access. “Submit-triggered” does not mean “unavailable in a browser.” Bundle a normalized portable dataset for supported exports; `engine.files()` cannot be assumed to contain every server-side source.

Arbitrary callback source is opaque. A string replacement cannot reliably rename JavaScript references. For graph-aware callbacks, require named dependency bindings declared in component options; rename rewrites the binding, not callback source. Reject structural transformations requiring undeclared opaque references. The restriction must be visible to component authors before they rely on automatic edits.

**Invariant:** compiler, editor and exporter use one structural-reference API; each transformation supplies its own explicit policy and validates the finished graph.

### Decision: canonical references plus capability projection

- **Context:** references are scattered across fields, and export removes nodes with dependencies.
- **Options considered:** patch each field independently; rewrite strings broadly; normalize declared references and share a visitor.
- **Decision:** use a canonical IR and common visitor, with explicit deletion/export policies.
- **Rationale:** a new reference kind has one declaration point rather than several incomplete consumers.
- **Consequences:** opaque callbacks need bindings or explicit rejection; projection changes semantics deliberately and requires end-to-end tests.
- **Status:** proposed.

## 11. Keep the implementation smaller than the explanation

Use a few ownership boundaries with one dependency direction:

```text
DSL / GUI / CLI / HTTP
         |
graph compilation and transformations
         |
application operations: build / refresh / query / inspect
         |
shared jobs and query execution
         |
snapshots --> datasets/indexes --> registry and artifact store

provider bridges --> operation runner --> typed events --> response helper/GUI
```

Reuse mechanisms where two real consumers need them. Memory and persistent datasets share conformance, not encoding algorithms. Fresh/load/delta paths share adoption, not build algorithms. Providers share operation settlement, not request conversion or protocol errors. Endpoints share response lifetime, not parameter parsing. Compile/edit/export share reference traversal, not transformation policy.

As replacements land, delete `unionChunks`, consumer-side record-view detection, scattered array conversions, rebuild-count retirement, direct handle closing in engine callers, first-waiter build ownership and duplicate HTTP pumps. Enumerate live references before removing obsolete record formats/codecs. Keep memory algorithms used in the browser and quality comparisons; uniform interfaces do not make a useful backend obsolete.

Each new abstraction should retire an existing responsibility or duplication. Avoid component-specific executor branches when a component contract can express the behavior. Module splitting follows coherent ownership; it is not a reason to make cyclic imports or add a service container.

### Use the implemented TypeScript foundation

`pkg/ragjs/src/` is authoritative; `pkg/ragjs/assets/rag.js` is the generated, committed bundle used by Goja, browsers and standalone HTML. Build it with `pnpm build` from the repository root. Ordinary Go builds need no JavaScript toolchain. Keep browser and Goja assembly shared; no handwritten host forks.

Strict implementations currently cover `core/errors.ts`, `core/identity.ts`, `core/text.ts` and `engine/scope.ts`. The other modules use `allowJs: true`, `checkJs: false`; extraction is not complete type coverage. Implement new dataset, reference, snapshot and operation contracts in strict TypeScript as behavior changes, migrating their affected consumers in the same phase. Keep types beside their owning implementation. Do not add an unused declaration layer describing an API the runtime does not enforce. Use discriminated outcomes and explicit references rather than `any` or `@ts-nocheck`.

Imports must remain local and acyclic; lower-level modules must not import the public namespace. Built-ins install explicitly before family factories, and state belongs to each evaluated bundle rather than a process-global Go singleton. Respect existing request-scope capture in `engine/scope.ts` when wiring async operations.

`pnpm check` combines TypeScript, selected Biome correctness checks, exact generated-byte checks and bundle/module tests. The import-graph test rejects cycles and runtime imports of external host packages. The ES2020 build target is not a polyfill promise. Node VM tests cover publication/export script execution with a minimal DOM; Go tests exercise actual Goja. Neither replaces visual browser testing or runtime lifecycle checks.

See `pkg/ragjs/src/README.md` and [the 010 intern guide](../../RAGWB-010--modular-runtime-sources-and-typescript-tooling-foundation/design-doc/01-intern-guide-to-modular-runtime-sources-typescript-and-build-tooling.md). TypeScript cannot prove native lifetime safety or validate external JSON by itself. Keep runtime validation and sequence tests.

## 12. Migrate callers and storage in coherent cuts

Retain recognizable `.from().parse().chunk().index()` and `.retrieve().filter().rerank().pack().answer()` scripts. Retain `rag.run` and `result.top` because they are useful interfaces. Update bake/custom callbacks to datasets, all hit consumers to references, and inspections to generation-aware lookup. There is no silent `result.chunk(stringId)` fallback that guesses which dataset the caller meant. If low-level code needs to construct a reference, expose that operation deliberately.

Migrate GUI, traces, evidence, Evals, sweeps, saved plans, example presets and export alongside each contract. Bump serialized IR and artifact namespace versions. Existing caches are disposable, but the program must not silently delete user directories: document explicit offline cleanup after no process owns the old store. Update fixtures/examples with the API cutover and remove the old readers. Preserve intended semantics rather than incidental JSON object insertion order; 010 parity checks canonicalize keys while preserving array order.

This is an implementation plan, not authorization to merge, force-push or resolve review threads. After the 010 prerequisite merge, choose one integration branch for 009 so fixes do not diverge across the old stack.

1. **Characterize the failures.** Add deterministic regressions for all twelve findings, recording which fail at the baseline. Cover cold/stored/delta outputs and equal-count changed bake input. If the stack will be used before the full migration, address the P1 SSE issue early through the intended shared transport, without adding a temporary alternate pump.
2. **Cut over datasets and provenance.** Implement and type memory/persistent contracts, bind indexes to dataset versions, migrate callbacks and hit/evidence consumers, and delete implicit unions. Run browser and Goja conformance together.
3. **Cut over snapshot/resource lifetime.** Add atomic acquisition, scoped adoption, private candidates, publication and detached results. Include query execution, runner packing, evaluations and inspection. Remove rebuild-count GC and unmanaged closer routes.
4. **Establish store authority.** Add descriptors/manifests, exclusive namespace ownership and mark/sweep. Make lineage derived, fix dotted-root traversal, and test active readers plus crash boundaries.
5. **Unify async lifecycle and shared builds.** Migrate sample/decide/rerank and build joining to the operation runner/job registry. Keep provider-specific semantics. Cover cancellation races, closed runtimes, background owners and event fanout. Fix deterministic Pi fallback separately without broadening the framework.
6. **Unify streaming responses.** Move both HTTP execution forms to the bounded writer and common request lifetime. Exercise stalled clients, failed flushes, terminal races and capacity recovery.
7. **Unify graph references and export.** Add canonical references and shared traversal; migrate edits and capability projection; bundle portable datasets; reject unsupported opaque callbacks explicitly.
8. **Remove leftovers and verify the integration.** Delete obsolete mechanisms/formats, update help/API documentation, exercise vault and reports workflows, and measure retained resources and performance.

Each phase should be a vertical change with its regression and deletion of the old path. Commit coherent validated checkpoints rather than extracting empty abstractions first. Use focused checks during development and broad checks at integration boundaries.

## 13. How to prove the contracts hold

Use synthetic corpora and provider fakes to control ordering. Hold/release barriers let a test pause a query or build at a precise transition; arbitrary sleeps do not establish the race sequence. Counting closers test exact closure; file-descriptor checks add evidence for real native resources.

| Contract | Required sequence and assertion |
|---|---|
| Dataset API | Fresh, persisted and delta forms return the same records/order/missing-ID behavior; nested metadata cannot mutate authority |
| Provenance | Two datasets share a logical chunk ID but different text; retrieval, fusion policy, packing and display resolve each intended version |
| Bake identity | Cold/warm callbacks use one API; change bytes without changing counts and observe cache invalidation |
| Snapshot lifetime | Pause Q in a provider; publish B and C; resume Q and construct its events/result using A successfully |
| Resource ownership | Fresh/load/delta register; duplicate adoption, exceptions and shutdown close every handle exactly once |
| Detached results | Release the query snapshot and close unused resources; returned helpers still read their owned records |
| Inventory | Create 40+ versions, retain three and collect the expected reachable set despite the lineage search limit |
| Store exclusion | Refuse a second writable owner and standalone GC while the server holds the namespace |
| Crash recovery | Inject interruption between every publication step; reopen to a complete old/new snapshot |
| Shared cancellation | Cancel A while B waits: B succeeds. Cancel all: cleanup completes. Keep a background owner: job continues. Late C avoids a cancelling job |
| Operation settlement | Provider failure survives deferred cancellation; caller cancellation/deadline/shutdown classify consistently; owner closure still cleans resources |
| Event fanout | Closed/slow A neither blocks J nor stops B; events retain job/operation attribution |
| SSE | Real non-reading socket, failed writes/flushes, overflow and terminal races end within bounds and restore admission capacity |
| Graph transformations | Rename/removal/projection preserve typed references or reject explicitly; target collisions and dangling decisions fail validation |
| Export | A classified server plan becomes a functioning unconditional lexical page under the documented capability projection |
| Local fixes | Dotted root lists outputs; empty enabled-model list selects a deterministic eligible fallback |

Run the repository's focused commands as applicable:

```sh
pnpm check
go test ./pkg/ragjs ./pkg/runner ./web
go test ./...
go build ./...
```

Use `go run ./...` with the actual command package when exercising examples, rather than building and launching a binary by hand. For a server, use tmux and capture-pane so it can be inspected and stopped; follow the workspace's `lsof-who -p PORT -k` port cleanup convention. The commands above are implementation guidance, not results of this documentation experiment.

Measure cold build, warm startup, query latency, changed-note refresh, memory, open file descriptors and artifact growth on the same fixture before and after. Record fixture sizes and conditions. Detached results add allocations; dataset references may add lookup work. Investigate regressions using measurements rather than treating passing tests as performance evidence.

Completion requires passing regressions or a documented reviewed disposition for all twelve findings, deletion of replaced mechanisms, one example working in memory/browser and persistent/Goja hosts, public documentation matching implementation, and verified cancellation/publication/shutdown sequences. Naming a helper `Snapshot` or moving code into TypeScript does not satisfy those criteria.

## 14. Risks and decisions still requiring implementation evidence

The most dangerous integration gap is releasing the query lease before runner packing copies needed records. Review that boundary explicitly. Shared immutable resources are another risk: retiring A cannot blindly close every handle A mentions when B shares some of them. Cross-process GC is safe only while namespace ownership is enforced.

Raw identity migration touches more consumers than the fluent DSL. Search every trace/evidence/GUI/evaluation path for places that discard `dataset` or resolve plain IDs. Review fusion equivalence as behavior, not merely type compatibility. Provider tool callbacks also need inspection: do not bypass the runner to preserve an unsupported request/reply interaction.

Exact API names, record schemas, duplicate-adoption choice, output budgets, retained-generation duration, timeout values and supported filesystem durability guarantees need implementation decisions and tests. The architecture specifies the behavior those choices must preserve; it does not claim current values. The deterministic model-fallback policy needs provider-specific eligibility review. These open details are not reasons to add compatibility layers.

Distributed indexing, remote dataset backends, simultaneous independent store owners, full Obsidian rendering and production authentication remain outside scope. Reindex status/manual refresh can be added through the same application operation after the core lifecycle works.

The design's concise review rules follow from the preceding examples: carry versioned references; expose one dataset API; retain one snapshot through outgoing-data construction; register every resource and release it only after its final user; separate inventory from history; separate shared-job lifetime from waiter lifetime; fix terminal outcomes before cleanup; bound transport writes; and make structural transformations use one reference traversal. Ordinary searches require no close calls, and every replacement removes its old path.

## 15. Author self-check

Two passages received particular comprehension review. Section 5 follows Q through retrieval, model wait and runner response packing before stating the snapshot rule; it identifies both premature closure and looking up newer text as distinct failures. Section 6 follows version 1 out of a 32-entry history while its files remain, then assigns inventory authority and collection work to descriptors/manifests and the namespace owner. Both sections name the actor that enforces the contract and keep ordinary caller responsibilities separate.

A remaining weakness is that API sketches cannot settle implementation-specific details such as filesystem sync behavior or tool callback hooks. They are labelled proposals and accompanied by required tests, but an implementer must inspect those boundaries before choosing concrete APIs. This rewrite preserves the reported evidence level; it adds no regression results or production-readiness claim.
