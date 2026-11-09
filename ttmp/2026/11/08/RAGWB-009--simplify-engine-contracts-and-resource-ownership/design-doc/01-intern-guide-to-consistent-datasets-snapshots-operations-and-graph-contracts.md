---
Title: Intern guide to consistent datasets snapshots operations and graph contracts
Ticket: RAGWB-009
Status: active
Topics:
    - rag
    - search
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://pkg/artifacts/lineage.go
      Note: Lineage history and GC ownership
    - Path: repo://pkg/natives/markdown_native.go
      Note: Delta reader creation
    - Path: repo://pkg/natives/persist.go
      Note: Resource opening and cleanup registration
    - Path: repo://pkg/ragjs/assets/rag.js
      Note: Current graph, datasets, engine and execution boundaries
    - Path: repo://pkg/ragjs/sample.go
      Note: Async operation bridge
    - Path: repo://pkg/runner/serve.go
      Note: Query packing and refresh lifetime
    - Path: repo://pkg/server/sse.go
      Note: Streaming transport lifecycle
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-08T18:12:11.54830049-05:00
WhatFor: ""
WhenToUse: ""
---

# A simpler and more robust rag-workbench engine

## 1. Purpose, status, and reading order

This is an analysis, target design, and implementation guide for RAGWB-009. It is written for an engineer joining the project without the conversation history. The baseline inspected is commit `0854c6a` on `task/ragwb-008-vault-ui`. The proposed APIs below are sketches for the refactor, not APIs already implemented. This ticket does not claim that the review findings have been fixed.

The objective is to reduce the number of concepts and exceptions an application author or component implementer must understand. Correctness, consistency, composability and reuse are the criteria. The size of this document reflects the care required to explain ownership and transitions, not a desire for a large framework. There is no deployed API or data format to preserve. We will update callers and invalidate old stored formats together; we will not add compatibility adapters, dual execution paths or legacy readers.

Read sections 2–4 to understand the existing system and evidence. Sections 5–11 define the target contracts. Sections 12–15 explain migration, implementation and acceptance. An intern should first run a reports example and read its plan, then follow a query through the engine and served-app code before editing resource management.

The design deliberately retains:

- The concise JavaScript corpus and plan builders.
- A shared JavaScript execution model for the browser and Goja host.
- Markdown parsing, section chunking, Bleve, the terms index and incremental build algorithms where they already work.
- Provider implementations and their protocol-specific errors.
- Existing evaluation cases and meaningful integration tests.

It replaces ambiguous boundaries: arrays versus record views, implicit global chunk merging, unmanaged handles, first-request ownership of shared builds, scattered graph references and duplicated response-pump orchestration.

## 2. System orientation

### 2.1 Corpus, plan, component and runtime

A corpus describes work done when documents change: read source files, parse documents, split them into chunks and construct indexes. A plan adds work performed for each query: retrieve candidates, combine rankings, filter, rerank, pack evidence and optionally generate an answer. The builders are immutable values that compile to a graph. Nodes have types, parameters, dependencies and output kinds.

```js
// Existing high-level style: retain this shape.
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

The JavaScript engine is currently concentrated in `pkg/ragjs/assets/rag.js`. Goja hosts it in the CLI/server. Go native components provide authorized filesystem reading, local indexes and disk-backed record collections. Browser examples execute the same conceptual pipelines with memory-backed implementations. These backends may have different algorithms or ranking scores; they must not have different ownership or collection semantics.

`pkg/runner/runtime.go` constructs the runtime. `pkg/ragjs/module.go` loads embedded JavaScript and binds host services. `pkg/runner/serve.go` opens a long-lived app and connects queries, document lookup and refresh to the engine. `pkg/server/rag.go` and `pkg/server/verbs.go` expose HTTP operations; `web/app-shell/shell.js` converts the remote responses into the GUI's local host interface.

### 2.2 Existing build and query flow

```text
JavaScript builders
        |
        v
compile/finalize -- type checks, dependency graph, topological order
        |
        +--> build nodes --> memo / lazy values / artifact store
        |                         |
        |                         v
        +--> Session.run ----> artifacts(ir)
                                  |
                             native handles
                                  |
                         query output and events
                                  |
                     runner --> JSON / SSE --> browser
```

A persistent artifact is a saved build output addressed by a key. A lineage describes successive outputs of the same build step as source data changes. The incremental builder searches lineage history for a suitable previous version and computes a delta. A record view reads stored records lazily rather than decoding the whole vault.

These are useful mechanisms. Their current composition is the problem. A caller must sometimes know whether a value is an array, a lazy wrapper or a record view. A query may await an LLM while source refresh changes the current artifacts. Cleanup is partly engine GC and partly runtime closer registration. Lineage history is also used as the collector's ownership inventory. Each of these choices makes correct behavior depend on details outside the caller's immediate operation.

## 3. Evidence and review-finding inventory

The prior review inspection found twelve unresolved threads in the stacked PRs. Priorities below are the review labels, not new severity claims. Source inspection supports the architectural concerns, but this design task did not independently reproduce every report. Each implementation phase must add or run the corresponding regression before claiming resolution.

| PR / priority | Reported failure | Contract that should prevent it |
|---|---|---|
| #2 / P2 | Stored duplicate chunks select a different version from cold arrays | Explicit dataset provenance and combination policy |
| #2 / P2 | Bake callback receives a record view instead of an array | One public collection contract |
| #2 / P2 | Store listing skips a root whose basename contains a dot | Explicit store layout traversal; small local correction |
| #3 / P2 | Two refreshes close artifacts still used by an awaiting query | Snapshot leases and resource ownership |
| #3 / P2 | Versions beyond the 32-entry lineage history become uncollectable | Authoritative inventory independent of lineage search |
| #3 / P2 | Delta-created record readers miss runtime cleanup | One constructor/registry path for owned resources |
| #4 / P1 | An SSE client that stops reading holds a slot indefinitely | Bounded transport writes and joined operation lifetime |
| #4 / P2 | Cancelling the first waiter cancels another waiter's build | Independent shared-job lifetime |
| #5 / P2 | Deferred context cleanup turns failure into cancellation | Immutable terminal outcome before cleanup |
| #6 / P2 | Empty enabled-model list prevents a valid fallback | Explicit deterministic model-selection policy |
| #7 / P2 | Lexical export retains references to removed decisions | Capability-aware graph projection |
| #7 / P2 | Rename fails to update decision references | One graph reference visitor |

Review sources: [PR #2](https://github.com/go-go-golems/rag-workbench/pull/2), [PR #3](https://github.com/go-go-golems/rag-workbench/pull/3), [PR #4](https://github.com/go-go-golems/rag-workbench/pull/4), [PR #5](https://github.com/go-go-golems/rag-workbench/pull/5), [PR #6](https://github.com/go-go-golems/rag-workbench/pull/6), [PR #7](https://github.com/go-go-golems/rag-workbench/pull/7).

The dotted-root and fallback findings do not justify architecture projects. Correct them locally and test them. The other findings are grouped by shared causes so the implementation can remove causes rather than accumulate special cases.

## 4. Current code map and failure mechanisms

Line numbers in this section refer to `0854c6a`; symbols are the stable navigation targets as the refactor moves code.

| File and starting point | Responsibility and relevant problem |
|---|---|
| `pkg/ragjs/assets/rag.js:1163`, `finalize` | Independently discovers data inputs, component `uses`, decision references and signal dependencies |
| `pkg/ragjs/assets/rag.js:1317`, `applyOps` | Edits graph fields; rename rewrites inputs/outputs but misses decision strings |
| `pkg/ragjs/assets/rag.js:1455`, `unionChunks` | Separates arrays and views, losing the original precedence relation |
| `pkg/ragjs/assets/rag.js:1875`, `Engine.gc` | Retires handles and closes them a later GC cycle without active-query ownership |
| `pkg/ragjs/assets/rag.js:1939`, `resolveBakes` | Passes storage-dependent values and hashes incomplete input information |
| `pkg/ragjs/assets/rag.js:1966`, `artifacts` | Builds a global chunk union and derives reading order from an implicit primary stream |
| `pkg/ragjs/assets/rag.js:2039`, `buildShared` | Reuses a promise carrying the first request's options and abort signal |
| `pkg/ragjs/assets/rag.js:2079`, `Session.run` | Holds artifacts across awaits and decorates a result with artifact-backed methods |
| `pkg/ragjs/assets/rag.js:2602`, `exportHtml` | Prunes nodes using trigger/output heuristics before dependent references are rewritten |
| `pkg/natives/persist.go`, `Persist.InstallEnv` | Registers cleanup for some loaded handles; allows a no-op closer fallback |
| `pkg/natives/markdown_native.go`, delta callbacks | Creates record readers through a different route from persistent loading |
| `pkg/artifacts/lineage.go`, `LineageRecord` / `GC` | Truncates ownership history, then collects only entries still in that history |
| `pkg/natives/rerank.go`, `rerankAsync` | Reads cancellation state in a posted callback after deferred cleanup can run |
| `pkg/ragjs/sample.go`, `newSample`; `decide.go`, `newDecide` | Similar async bridge machinery with different outcome handling |
| `pkg/server/sse.go`, `pump` | Ignores write/flush failure and lacks a bounded write policy |
| `pkg/server/verbs.go`, `handleVerb`; `rag.go`, `handleRun` | Duplicate orchestration around event pumps and terminal responses |
| `pkg/runner/serve.go`, embedded `__serve.run` | Packs a result using the current artifacts after the query, which needs generation consistency |

The last observation illustrates why this is more than a list of review patches. Fixing query lifetime alone is insufficient if response packaging subsequently consults a newer generation. Result construction must be inside the same lifetime boundary.

## 5. Target architecture and invariants

### 5.1 Modules, not a general workflow framework

Use a small set of modules with one direction of dependency:

```text
DSL and GUI/CLI/HTTP entry points
              |
              v
Graph compiler and transformations
              |
              v
Application operations: build / refresh / query / inspect
       |                              |
       v                              v
Shared build jobs                 Query execution
       |                              |
       +---------- Snapshots ---------+
                       |
                Datasets and indexes
                       |
          Resource registry + artifact store

Provider bridges --> Operation runner --> typed events
                                           |
                              JSON / SSE / GUI consumers
```

Do not create a durable job scheduler, plugin framework or generic service container. Reuse is justified when two actual consumers need the same contract: record arrays and persistent records; sample, decide and rerank; direct run and verb streaming; compile and graph edits.

### 5.2 Non-negotiable invariants

1. A record reference identifies both its dataset version and record ID.
2. Loading a stored output changes storage cost, not its public collection behavior.
3. Every query reads one published snapshot, including event/result packaging.
4. Every opened native resource has exactly one registry owner and explicit users.
5. A resource closes only after its final user releases it.
6. A bounded build-search history is never the authoritative artifact inventory.
7. Cancellation of one waiter cannot cancel a shared job still needed by another.
8. Every asynchronous operation has one immutable terminal outcome.
9. Every network write and flush has a bounded wait; transport failure ends that transport.
10. Every structural graph reference participates in the same validation and transformation API.
11. Application code does not need close/release calls for ordinary searches.
12. All new abstractions replace old paths in the same phase; no parallel legacy implementation remains.

These are runtime/module invariants, not a claim that JavaScript provides compile-time linear ownership. Private handles, central constructors, checked references and sequence tests make ordinary misuse difficult to express. Trusted native implementations can still contain bugs and must be tested.

## 6. Dataset and index contracts

### 6.1 One logical type

Every document or chunk output is a read-only dataset, whether it was computed in memory or loaded from disk. Keep access synchronous for the current local memory/disk hosts. Do not make every `get` asynchronous merely to speculate about remote storage; if that becomes a requirement, design a bulk remote boundary separately.

```js
// Proposed public component-facing contract; exact names may be refined.
Dataset {
  id;                 // immutable version identity
  kind;               // 'documents' or 'chunks'
  size;
  get(recordId);      // record or null
  values();           // deterministic iterator
  materialize();      // explicit full traversal returning an array
}

ChunkDataset extends Dataset {
  document(recordId); // reference to its source document
  order(documentId);  // ordered record references for that document
}

Index {
  datasetId;          // exact chunk dataset this index was built from
  search(query, options); // hits referring to that dataset
}
```

Objects returned to user callbacks must not allow mutations to alter the authoritative dataset. Freeze memory records or return detached records at the boundary, with tests for nested metadata. The implementation may cache immutable decoded records. Neither this API nor ordinary components expose `close`, `__records`, `__ix` or codec selection.

### 6.2 Provenance instead of an implicit union

```js
const hit = {
  ref: { dataset: 'dataset-version-key', id: 'document#section' },
  score: 0.82,
  reason: 'optional explanation'
};
const chunk = snapshot.resolve(hit.ref);
```

A logical section ID can be stable across enrichment versions, but its bytes are not. Original and prefixed/enriched chunks with the same ID are not interchangeable. Retrieval from an index must resolve against that index's dataset. `pack`, trace, context citations, evaluation and the GUI must carry the reference without dropping it.

Fusion deduplicates exact references by default. Combining indexes over the same dataset remains natural. Combining rankings over differently enriched datasets requires an explicit canonicalization policy; never silently use “last stream wins.” Prefer an explicit projection to a canonical dataset before fusion, or a combine component with documented conflict rules. Keep this an ordinary component, not a special engine-wide merge mechanism.

Document identity must also be unambiguous across multiple source corpora. Use a source identity plus vault-relative path for logical document identity; never assume a title or a truncated slug is globally unique. Index version identity derives from component/codec version, normalized parameters and input dataset identities.

### 6.3 Callback and cache semantics

Bake and custom-component contexts receive datasets, not storage-dependent arrays:

```js
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id));
// For counting, avoid a full traversal:
rag.bake(ctx => ctx.chunks.size);
```

Update all in-repository callbacks in the cutover. Build-cache keys must include full input dataset identities, callback source/configuration and component version. The current use of counts/section counts is insufficient to distinguish equal-sized inputs with different content. Memory and persisted implementations run one conformance suite for iteration order, missing IDs, immutable records, materialization and provenance.

## 7. Snapshots, resources and result lifetime

### 7.1 Published snapshots and leases

A snapshot is an immutable mapping from validated build outputs to datasets/indexes. A registry owns native handles, keyed by artifact identity. Several snapshots can share unchanged handles. A snapshot retains the registry entries it needs; a query lease retains that snapshot. A builder also owns a private scope until it publishes or aborts.

```text
Snapshot A: current, leases=1
        |
refresh builds B privately
        |
validate B and atomically publish B
        |
A: retired, leases=1       B: current, leases=0
        |
query using A finishes and materializes its result
        |
A: leases=0 -> release A's resources
        |
close only registry entries with zero owners/users
```

Acquiring the current snapshot and incrementing its lease count must be one serialized operation. Publication cannot occur between selecting the snapshot and retaining it. Keep these transitions on the JS runtime owner; Go registry operations that can cross goroutines require their own synchronization.

```js
async function query(plan, signals, request) {
  await builds.ensure(plan, request.signal);
  return snapshots.withCurrent(plan.buildKey, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot);
  }); // releases on success, exception, cancellation and packaging failure
}
```

In the query sketch, `builds.ensure` waits only when no usable snapshot exists for the requested plan. A background refresh must not block queries against the last good published snapshot. A failed or cancelled refresh does not replace the published snapshot. Builds write a private candidate and publish only after complete validation. A partial output may be useful internally for resumption, but is never the current query snapshot.

### 7.2 One path for opening resources

Persistent loading, delta creation and fresh native builds must all pass ownership through the same registry. Remove the no-op cleanup fallback for resource-bearing runtimes; failure to supply a registry is a construction error.

```go
// Proposed sketch: keep the generic lifecycle small.
type ResourceID string

type Registry interface {
    Adopt(id ResourceID, closer io.Closer) (Handle, error)
}

// Every opener immediately adopts or closes on failure.
r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(id, r)
if err != nil { _ = r.Close(); return nil, err }
return NewChunkDataset(h), nil
```

The scope releases all uncommitted resources if any later build stage fails. Adoption must define duplicate-ID behavior: reuse an existing entry and close the redundant new handle, or reject it and close it. Never silently register two owners for the same underlying handle. Shutdown rejects new work, cancels active operations, waits for them to settle, releases snapshots and then closes the registry. A timeout is reported explicitly; never declare cleanup successful while Go workers can still access closed resources.

### 7.3 Returned results must not retain hidden live handles

A simple `rag.run` should remain usable after it returns without requiring a caller to close a result. Therefore return a detached result containing the records needed for its emitted hits, trace detail, evidence and configured neighbor preview. `result.top()` and `result.chunk(hit)` read those owned records. Materialization occurs before the query lease is released. It copies the bounded result, not the entire corpus.

Large output limits must be explicit. Do not return hits whose required records were silently omitted. If a requested trace/result exceeds a declared budget, return a clear limit error or explicit pagination contract. Evaluations can use a scoped internal snapshot operation to iterate all relevance labels; exporting a whole search corpus also uses a scoped read.

Additional browser document/chunk lookups carry a snapshot ID and record reference. Resolve against that retained generation or return an explicit stale-generation response. Never reinterpret an old hit against the newest dataset. Browser caches key by generation/reference and clear on generation changes. A refresh cannot revoke text already delivered, and retention is not a substitute for access control.

## 8. Durable store ownership and collection

### 8.1 Keep the existing storage algorithms, simplify authority

Use the existing immutable payloads and record/index codecs. Extend artifact descriptors to record owned payloads and dependencies. Add immutable snapshot manifests listing artifact roots and a small atomic current-snapshot pointer per build identity. Artifact descriptors and manifests are authoritative. The bounded lineage list is a derived accelerator for incremental-base lookup and may be rebuilt or discarded without losing ownership.

Do not add a separate database solely to keep an inventory already expressible through descriptors. Traverse the defined namespace layout rather than guessing payload directories from arbitrary basename punctuation. This also eliminates the dotted-store-root failure.

### 8.2 Single owner per writable store namespace

Choose a deliberately simple initial deployment model: one process owns a writable namespace with an OS-backed exclusive lock for its lifetime. It owns builds, publication, resource leases and online GC. A second process must use another namespace/store or fail clearly. Standalone `store gc` acquires the same exclusive lock and therefore runs only when the namespace is offline; do not pretend a separate CLI process can see in-memory leases.

The browser memory store implements the same logical store contract without OS locking or filesystem persistence. We are not building cross-machine leases or a distributed collector. If simultaneous independent processes sharing one store becomes necessary, it is a separate explicit design.

### 8.3 Publication and crash recovery

```text
1. Write payloads in an owned staging directory.
2. Close writers; validate counts/checksums/codec metadata.
3. Move complete immutable payloads into the namespace.
4. Atomically publish their descriptors.
5. Publish a complete immutable snapshot manifest.
6. Atomically replace the current pointer.
7. Update derived lineage lookup information.
```

A crash before step 6 leaves the old snapshot current. A crash after step 6 may leave lineage acceleration stale but must not make the snapshot unreadable. Durability requires appropriate file and directory synchronization around publication; rename atomicity alone does not prove power-loss durability. Test the filesystem operations at the supported platform boundary.

GC marks artifacts reachable from current snapshots, configured retained snapshots, active query/builder leases and temporary build ownership. It follows descriptor dependencies transitively, then sweeps unmarked owned artifacts. The same owner serializes publication and the mark/sweep decision so a candidate cannot be collected between becoming durable and publication. Missing/corrupt ownership metadata aborts destructive collection with an actionable error. Never guess that an unknown file is safe to delete.

Hard-linked payload files are handled through directory ownership: removing one artifact's links does not remove links owned by another. Report logical bytes and actually reclaimed disk bytes separately where possible. Keep historical generations according to a deliberate policy, including the privacy consequences of retaining excluded notes. Do not tie the retention count to query duration.

## 9. Operations, shared builds and event transport

### 9.1 One bridge for native asynchronous operations

The sample, decision and rerank bridges all need context propagation, an abort listener, work outside the Goja owner, event posting, promise settlement and cleanup. Extract that lifecycle once. Provider-specific request conversion, result validation, tool execution and error mapping remain small supplied functions. Streaming tool callbacks require a supported request/reply hook onto the owner; they must not access a Goja value from a worker goroutine.

```go
// Sketch: payloads crossing goroutines are immutable Go values or bytes.
func RunOperation(ctx context.Context, spec Spec) Future {
    // Install cancellation before starting work.
    // Execute work under an owned task/errgroup.
    value, err := spec.Execute(ctx, spec.Emit)
    outcome := spec.Classify(value, err, context.Cause(ctx))
    // Outcome is fixed before cleanup cancels a derived context.
    // Post exactly once to the VM owner; release resources even if it is closed.
    return SettleOnOwner(outcome)
}
```

The implementation is asynchronous despite the compact pseudocode. Distinguish success, caller cancellation, deadline, provider failure and runtime shutdown. Define the race rule: a terminal result accepted before a cancellation transition remains terminal; otherwise cancellation wins. Runtime disposal may prevent a JS promise callback from running, but cannot prevent task/resource cleanup. Detach abort listeners on settlement. Use errgroup for related Go goroutines.

### 9.2 Shared jobs have waiters

`buildShared` becomes a small shared-job registry keyed by the full normalized build identity, including options that affect semantics. It is not a generic durable scheduler. Each job has its own cancellation context linked to engine shutdown and a count of active waiters. A waiter cancellation detaches that waiter. When the last waiter leaves, cancel the job unless it has an explicit background-refresh owner. If a new waiter arrives while cancellation is settling, let it start/retry after the cancelled job terminates rather than attaching it to a doomed promise.

```text
request A ---- wait ----+
                       +--> job J --> build candidate --> publish
request B ---- wait ----+

A cancels: detach A; B still owns interest in J.
B cancels too: cancel J unless a refresh owner remains.
engine closes: cancel J and all waiters, join cleanup.
```

Build progress belongs to the job and is fanned out to waiter subscriptions. Include operation/job IDs so each HTTP request can attribute it. A slow or closed subscriber cannot block the shared job or cancel other subscribers. No job events are permanently attached to the first request's scope.

### 9.3 One bounded SSE transport

Both query and verb HTTP endpoints should call one response helper. Domain execution emits typed events and returns a terminal outcome; the transport handles JSON or SSE framing. Keep provider events independent of HTTP.

The SSE writer must be the only goroutine writing or flushing the response. Set a bounded write deadline before each write/flush, including initial headers, heartbeat and terminal event. Use `http.ResponseController.SetWriteDeadline` and error-returning `Flush`; wrappers must support the controller's unwrapping path. A deadline capability failure is a setup error, not permission to stream without a bound. The installed Go API notes that an already-exceeded write deadline cannot be extended; terminate the stream after that failure.

On write/flush failure, stop pumping, cancel/detach this request and return through a bounded join. Queue overflow has the same request-local effect. A stream can deliver at most one terminal event; a failed network cannot be promised one. Heartbeats and accumulated/coalesced text must also respect bounded memory and byte-size limits, not just a fixed number of queue entries. Per-write deadlines allow a long healthy stream without one short global timeout cutting it off.

Apply the same admission-control scope and terminal-error vocabulary to direct queries and verbs. Test with a real socket whose client stops reading, not only a fake ResponseWriter or an event queue test. Acceptance requires that capacity becomes available again.

## 10. Graph references and transformations

### 10.1 One canonical reference representation

Keep the user-friendly declarative syntax if it remains clear, but normalize it once in the compiler:

```js
// Public DSL may remain concise.
when: { decision: 'route.needs', min: 0.5 }

// Canonical IR sketch.
when: {
  source: { kind: 'decision', node: 'route', output: 'needs' },
  minimum: 0.5
}
```

Every component declares structural references through a common schema/visitor: data inputs, artifact dependencies, decisions and pinned outputs. `references(node)` enumerates them; `rewriteReferences(node, mapping)` rewrites them. The compiler checks existence, output types, phases and cycles using the same enumeration. Rename checks target uniqueness, rewrites references and validates once at the end of the edit transaction.

Deletion must specify the behavior for dependents: reject, cascade or a declared replacement. Avoid guessing how a removed decision should affect a production query. Lexical export is a separate explicit projection whose documented policy makes retrieval unconditional when its decision-only classifier is omitted. It validates the final transformed graph before serialization.

### 10.2 Capability-based export and lambda limits

Do not equate “submit-triggered” with “unavailable in an exported page.” Declare component capabilities such as local text index, external model or host filesystem. The exporter selects a supported graph and applies explicit rules for excluded components. Bundle a normalized portable dataset for supported browser builds rather than assuming `engine.files()` contains every server-side source.

Arbitrary JavaScript cannot be reliably renamed by string replacement. For graph-aware callbacks, pass named dependency bindings declared in their component options. Renaming changes the bound reference, not the callback source. Reject structural transformations that require rewriting opaque undeclared references. This is a clear restriction, not a compatibility workaround.

## 11. Module boundaries, reuse and simplification budget

Split the monolithic engine into a few internal modules only as the contracts land: graph, datasets, snapshots/resources, operations and executor. Keep a thin `rag` facade. Browser and Goja must load the same implementation. Choose a deterministic assembly/load mechanism and test it; do not maintain hand-copied browser and host forks. Update `pkg/ragjs/module.go` and exported-page loading together.

| Boundary | Shared mechanism | What remains specialized |
|---|---|---|
| Memory and disk records | Dataset contract and conformance tests | Encoding, shards, page access |
| Fresh, loaded and delta outputs | Resource adoption and ownership | Build/delta algorithms |
| Sample, decide, rerank | Async lifecycle and terminal outcome | Requests, protocol errors, tool semantics |
| Direct query and verb endpoints | Admission, event subscription, JSON/SSE response lifecycle | Parameter parsing and operation body |
| Compiler, editor, exporter | Structural reference traversal | Transformation policy |
| Timer, manual and post-sync refresh | Build/refresh operation | Trigger scheduling and access checks |

Delete `unionChunks`, consumer-side record-view tests, scattered array conversions, one-rebuild retirement, direct handle closing in engine callers, first-waiter build ownership and duplicated HTTP pumps as their replacements land. Remove obsolete record formats/codecs only after enumerating live references; keep memory algorithms used by the browser and quality comparisons. Uniform interfaces do not justify deleting a useful backend.

The simplification budget is qualitative but reviewable: each new concept must retire at least one existing responsibility or duplicated mechanism. No component-specific branch should be added to the executor when a component contract can express the behavior. Do not introduce generic configuration or dependency-injection frameworks for these fixed responsibilities.

## 12. JavaScript and application migration

Ordinary `.from().parse().chunk().index()` and `.retrieve().filter().rerank().pack().answer()` scripts should remain recognizable. Retain `rag.run` and `result.top` because they are good interfaces, not because old callers must be preserved. Update bake/custom callbacks to the dataset contract, raw hit consumers to references, and inspection endpoints to generation-aware lookup.

```js
const result = await rag.run(plan, { q });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;
// No resource cleanup is required: result owns detached data.
```

Migrate GUI, traces, evidence, Evals, sweeps, saved plans, example presets and export together. No silent `result.chunk(stringId)` fallback should choose a dataset when the ID is ambiguous. Expose a deliberate reference constructor if low-level callers need one.

Bump serialized IR and artifact namespace versions. Existing local caches are disposable; document that old stores require an explicit cleanup command when no process owns them. Do not silently delete users' directories. Saved examples and fixtures are updated in the same commit as each API cutover. This project does not need a legacy reader.

## 13. Implementation sequence and checkpoints

Each phase is a coherent vertical change with tests and removal of its replaced code. Avoid extracting empty abstractions first or leaving two engines indefinitely. The existing PR stack is not merged; decide one integration branch for the cleanup and update the review stack coherently rather than repeatedly fixing divergent copies. This ticket is design work only; no force push, merge or review-thread resolution is implied by creating it.

1. **Characterize failures and establish a baseline.** Add deterministic regressions for all twelve findings. Record which fail on the baseline. Include cold, stored and delta paths, and equal-count changed input for bake caching. Make the P1 SSE correction early if the stack will be used before the full migration; it must live in the future shared transport rather than a temporary alternate path.
2. **Datasets and provenance.** Implement memory and persistent contracts; bind indexes to datasets; migrate hit/evidence consumers and bake callbacks; remove implicit unions. Keep browser and Goja conformance together.
3. **Snapshot and resource ownership.** Add scoped acquisition, resource adoption, transactional publication and detached results. Migrate query execution, response packing, evaluations and inspection; delete rebuild-count GC and unmanaged closers.
4. **Store inventory and GC.** Introduce authoritative descriptors/manifests, exclusive namespace ownership and mark/sweep. Make lineage derived. Add crash-boundary and active-reader tests. Correct store root traversal here.
5. **Operation lifecycle and shared jobs.** Migrate sample, decide, rerank and build joining to one runner. Preserve provider-specific behavior. Exercise cancellation races, runtime closure and event fanout. Correct deterministic Pi fallback as a small independent change.
6. **Streaming transport.** Route both HTTP execution forms through one bounded writer/pump and request lifetime. Test client stalls, flush failures, final-event races and inflight-slot recovery.
7. **Graph transformations and export.** Introduce canonical references, migrate all structural edits, add capability projection and portable export checks. Reject unsupported opaque callbacks explicitly.
8. **Cleanup and integrated verification.** Remove dead code and obsolete formats; update examples, help and API documentation; exercise the vault app and report workflows with the new contracts. Record performance and retained-resource measurements before declaring success.

Commit at meaningful intervals. Run focused tests while developing each contract and full checks at integration boundaries; repeated full suites for every documentation or small bookkeeping commit add no evidence.

## 14. Test strategy and acceptance criteria

| Contract | Required sequence / assertion |
|---|---|
| Dataset consistency | Same records and resolution through memory, persisted and delta backends; mixed datasets remain explicit |
| Bake inputs | Cold/warm callbacks have one API; changed bytes with equal counts invalidate a bake |
| Provenance | Two datasets sharing logical IDs resolve their own bytes through retrieval, fusion policy, packing and display |
| Snapshot leases | Query pauses in a provider; publish two later snapshots; resume query and package old records successfully |
| Resource lifecycle | Fresh/load/delta paths all register; exceptions and runtime shutdown close each handle exactly once |
| Result ownership | Returned result remains readable after releasing its query snapshot and closing unused handles |
| Durable inventory | Create 40+ versions, retain three, collect to the expected reachable set regardless of lineage search length |
| Store locking | Second owner/standalone GC is refused while the serving owner holds the namespace |
| Crash recovery | Fail between each publication step; reopen to a complete old or new snapshot, never a mixed one |
| Shared jobs | Cancel A while B waits; B succeeds. Cancel all; job releases resources. New waiter does not join a doomed job |
| Async outcome | Provider failure remains failure after deferred cleanup; user cancellation remains cancellation |
| SSE bounds | Real non-reading socket and flush errors terminate within configured bounds; app slots recover |
| Graph edits | Rename/removal/projection preserve typed references or reject explicitly; no dangling decisions |
| Export | Classified source plan exports a functioning unconditional lexical page under documented projection rules |
| Small fixes | Dotted root lists saved outputs; empty enabled list selects a deterministic eligible model |

Use synthetic corpora and provider fakes for lifecycle tests. Hold/release barriers make races deterministic; do not depend on arbitrary sleeps. Use counting closers plus actual file descriptor checks where practical. Keep a real-socket SSE test because fake writers cannot prove network write deadlines work.

Measure cold build time, warm startup, query latency, changed-note refresh, memory, file descriptors and artifact growth on the same fixture before and after. Report sizes and conditions. Do not claim performance parity just because unit tests pass; explicit result materialization and provenance can change allocation cost. A regression needs investigation, not automatic abandonment of a cleaner contract.

Completion means all twelve findings have passing regressions or a documented, reviewed disposition; the old mechanisms listed in section 11 are removed; the same example works in memory/browser and persistent/Goja hosts; documentation matches the actual public API; and the shutdown/cancellation/publication sequences are verified. Review comments are not resolved merely because an abstraction with the right name exists.

## 15. Risks, decisions and follow-up boundaries

The most important implementation risk is result lifetime: releasing a query lease before copying its needed records recreates the use-after-close bug under a different API. The second is resource sharing: snapshots reference common immutable artifacts, so closing a retired snapshot cannot blindly close every handle it mentions. The third is cross-process GC: in-memory leases protect nothing against an independent collector without the namespace ownership rule.

The costliest API migration is raw record identity, not the fluent DSL. Update every trace/evidence/GUI/evaluation consumer, and define fusion equivalence explicitly. Reuse the lifecycle helpers only where semantics match; a model tool request/reply is richer than a one-shot rerank request, and must have a supported hook rather than bypassing the runner.

The design leaves distributed indexing, remote dataset backends, multi-process shared stores, full Obsidian rendering and production authentication out of scope. Reindex status and manual refresh can be built on the application operations afterward. They should not introduce another refresh implementation.

The design is intentionally more explicit than the current code. Its success should be visible as fewer places where a developer must ask: Is this an array? Who closes this reader? Whose cancellation controls this build? Which generation does this hit belong to? Which references must this rename update? Each question should have one answer enforced at one boundary.

## 16. TypeScript foundation and sequencing (RAGWB-010)

RAGWB-010 is the implementation prerequisite for this refactor. It extracts the runtime into source modules and builds the committed `pkg/ragjs/assets/rag.js` used by both Goja and browsers. It adds TypeScript and Biome tooling without changing the ownership contracts described here. See [the 010 intern guide](../../RAGWB-010--modular-runtime-sources-and-typescript-tooling-foundation/design-doc/01-intern-guide-to-modular-runtime-sources-typescript-and-build-tooling.md).

Implement the new dataset, record-reference, snapshot and operation contracts in strict TypeScript as their runtime behavior changes. Use discriminated unions for terminal outcomes and explicit record provenance; keep declarations with the owning implementation. Do not add an unused declaration layer over the existing array/view forks. Transitional extracted JavaScript is allowed in 010; 009 must migrate and type the consumers it replaces.

Biome handles formatting and linting; TypeScript handles static type checking. Neither proves runtime input validity or resource lifetime safety. Keep the lifecycle/conformance tests in section 14. The existing baseline file/line map is historical; a current module map will be added after 010 extraction.
