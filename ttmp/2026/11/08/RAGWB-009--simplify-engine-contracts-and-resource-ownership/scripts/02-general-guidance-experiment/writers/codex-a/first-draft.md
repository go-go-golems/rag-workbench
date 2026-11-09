# Implementing consistent datasets and engine ownership

Rag-workbench lets an application describe a searchable corpus and the query that runs over it. A corpus reads files, parses documents, splits them into chunks and builds indexes. A plan retrieves candidates from those indexes, combines and filters rankings, optionally reranks them with a model, and packs evidence for an answer. The same JavaScript runtime supports browser experiments and a Goja host with native filesystem readers, indexes and persistent records.

RAGWB-009 changes the contracts connecting those pieces. A stored collection should behave like a freshly computed collection. A hit should identify the version of the text it retrieves. A query should finish using one consistent set of build outputs even if refresh publishes newer ones. Cancellation, cleanup, graph editing and HTTP streaming should each have one defined owner.

This is a proposed implementation guide, grounded in the runtime/source baseline c267f91 supplied for this task. Source was inspected; tests were not run. The existing engine does not yet implement these contracts. RAGWB-010 already extracted its authored source into `pkg/ragjs/src/`; `pkg/ragjs/assets/rag.js` is generated. Compatibility may break. Update callers and disposable cache formats directly, without adapters, legacy readers or a parallel engine.

## Follow a query before changing it

Start with `examples/presets/reports.js`. Its corpus has section chunks and failure-table row chunks, a bake callback deriving synonyms from documents, and text, failure and metadata indexes. Its plan rewrites a query, retrieves from three indexes, fuses rankings, reranks, expands neighbors, packs context and generates a cited answer. Typing runs change-triggered work; submitting permits the model steps. Variants and evaluations use the same graph and records.

The fluent builders compile to nodes with IDs, types, parameters, inputs and outputs. Build nodes run when their input digests change; live nodes run for queries. `graph/graph.js` validates types and dependencies and computes execution order. `runner.NewFactory` in `pkg/runner/runtime.go` installs the selected native implementations. `pkg/ragjs/module.go` loads the common generated runtime and binds host services. The browser uses memory implementations; native Goja components can use Bleve and lazy disk records. Identical ranking scores across different backends are not required, but ownership and collection behavior must agree.

A build artifact is a saved output identified by a content/configuration-derived key. A lineage groups versions of the same build step across source changes so incremental builds can locate a prior version. Today, `Engine.build` reuses memoized or lazy persisted outputs or calls full/delta component implementations. `Session.run` obtains `engine.artifacts(ir)`, builds if needed, then holds that artifact object across awaited live components. It returns hits plus convenience functions that still read the artifact object. The served application later asks for the current artifacts again to pack chunk bodies for HTTP.

```text
builders -> compiler -> build nodes -> memo / persistent artifacts
                            |                    |
                            +--- Session.run <---+
                                      |
                              hits, context, answer
                                      |
                        served-app packing -> JSON or SSE -> GUI
```

This last handoff matters. A query that starts with old text may wait on a model while refresh runs twice. Current GC uses rebuild cycles rather than query ownership, and packing may consult a newer generation. Fixing only the convenience result methods would leave the HTTP response inconsistent.

## What must be corrected

The original review inventory contains twelve findings. Source inspection supports the mechanisms below; it does not independently reproduce every reported failure. Preserve each finding as an implementation acceptance case.

| Finding | Current evidence and required correction |
|---|---|
| Duplicate stored chunks differ from cold chunks | `engine/records.js:unionChunks` separates arrays from views and prioritizes the array map, then the first matching view. Replace implicit precedence with dataset provenance. |
| Bake callbacks receive views instead of arrays | `Engine.resolveBakes` reads raw outputs and uses `docs.map` and `chunks.length`. Give callbacks one dataset API. |
| Store root containing a dot is skipped | `pkg/artifacts/store.go:List` skips directory basenames containing dots, including the root passed to traversal. Traverse a defined layout. |
| Refresh closes a query's artifacts | `Engine.gc` closes the previous retirement batch one cycle later; `Session.run` holds resources across awaits. Add snapshot leases. |
| Old versions become undiscoverable by GC | `pkg/artifacts/lineage.go` truncates to 32 entries and collects by enumerating those entries. Separate inventory from search history. |
| Delta readers miss cleanup | `markdown_native.go` returns new record objects from parse/chunk delta callbacks; persisted loads register closers in `Persist.InstallEnv`. Centralize resource adoption. |
| Stalled SSE holds capacity | `server/sse.go:pump` has no write deadline and ignores write errors. Bound writes and join request cleanup. |
| First waiter controls a shared build | `Engine.buildShared` returns the first promise carrying the first options. Separate job and waiter ownership. |
| Deferred cleanup changes failure to cancellation | `rerankAsync` posts a callback reading the context after its worker defers cancellation. Freeze outcome before cleanup. |
| Empty enabled list defeats fallback | `llm/gepp/backend.go:preferred` permits the default with an empty list but falls back by iterating that list. Define deterministic eligible fallback. |
| Lexical export loses decision dependencies | `export/html.js` removes submit-triggered live nodes using output heuristics. Project by capability and rewrite decisions explicitly. |
| Rename misses decision references | `graph/graph.js` rewrites data inputs and plan outputs but not declarative decision strings. Share one reference visitor. |

The existing compiler rejects missing decision sources. The graph defect can therefore appear as a rejected rename or export, rather than successful execution of an invalid graph. Existing remove logic also rejects some artifact users. Preserve these safeguards while replacing their incomplete discovery mechanisms.

## Datasets carry meaning independently of storage

A dataset is one immutable version of a document or chunk collection. Its version identity identifies its contents and their construction semantics. A logical document ID identifies a source plus a vault-relative path; a logical chunk ID identifies a section within that document. The same logical chunk ID can occur in original and enriched datasets with different text. A dataset version and a record ID together identify the actual record.

The target public component contract is synchronous because both present hosts read local data. Do not make every lookup asynchronous for speculative remote storage. These TypeScript declarations are sketches; implementations and migrated consumers should use strict TypeScript.

```ts
type DatasetId = string;
type RecordRef = Readonly<{ dataset: DatasetId; id: string }>;
interface Dataset<T> {
  readonly id: DatasetId;
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): Readonly<T> | null;
  values(): IterableIterator<Readonly<T>>;
  materialize(): readonly Readonly<T>[];
}
interface ChunkDataset<T> extends Dataset<T> {
  document(chunkId: string): RecordRef;
  order(documentId: string): readonly RecordRef[];
}
interface Index {
  readonly datasetId: DatasetId;
  search(query: string, options: unknown): readonly Hit[];
}
interface Hit { readonly ref: RecordRef; readonly score: number }
```

For example, an original dataset may hold `reports/identity.md#ownership` with text “An operation has an ID.” An enriched dataset may hold the same logical ID with a contextual prefix. Retrieval from the enriched index must return `{ref: {dataset: 'enriched-v2', id: 'reports/identity.md#ownership'}, score: 0.82}`. Packing, trace display, citations and evaluation must resolve those exact bytes, rather than selecting whichever version a global union currently prefers.

An index binds to exactly one chunk dataset. Default fusion deduplicates exact references, so two indexes over the same dataset naturally combine rankings. If a plan combines original and enriched rankings but wants one logical section, require an explicit projection into a canonical dataset or an ordinary combine component with documented conflict rules. Do not add an engine-wide merge exception.

Memory and persistent datasets must share deterministic iteration, missing-ID behavior, order and materialization. Do not expose `__records`, `__ix`, codec selection or closing to callbacks. Copy or deeply freeze records, including metadata and nested arrays, so a callback cannot alter authoritative data. Caching immutable decoded records is permissible. `Readonly<T>` alone does not enforce that runtime property.

Replace bake callbacks in the same cutover:

```js
// Proposed callback API.
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id));
rag.bake(ctx => ctx.chunks.size);
```

Bind callback datasets explicitly when more than one document/chunk stream is available. The current “first output of this kind” selection is insufficient for a multi-stream corpus. Cache keys include full input dataset identities, callback source/configuration and component version; equal record counts or section counts do not identify equal content. Index identity likewise includes component and codec versions, normalized parameters and dataset identity. Keep document identity distinct from index/artifact identity; the latter changes when build semantics change.

## Snapshots define what a query can observe

A snapshot is an immutable mapping from build node outputs to datasets and indexes. It has its own identity and belongs to a normalized build identity. It can contain several chunk datasets and their source document datasets. The resource registry owns the actual native handles; snapshots retain registry entries and queries retain snapshots. Unchanged artifacts can be shared across multiple snapshots without duplicate ownership.

```text
publish A -> query Q retains A -> Q waits on provider
                    |
                refresh publishes B -> refresh publishes C
                    |
              A retired, Q still retains A
                    |
       Q resumes, packs its detached result, releases A
                    |
       release A's entries; close only entries with no users
```

Acquire the current pointer and retain it in one serialized action on the JavaScript owner. Publication must not interleave between selecting a snapshot and retaining it. Synchronize registry work separately when Go goroutines can access it. Builder scopes also retain their input and candidate resources before publication.

```ts
// Proposed control flow; helpers are internal contract sketches.
async function query(plan, signals, request) {
  await builds.ensureUsable(plan, request.signal);
  return snapshots.withCurrent(plan.buildKey, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return materializeResult(execution, snapshot, request.resultLimits);
  });
}
```

`ensureUsable` waits when the requested build identity has no usable snapshot. Queries may use the last good published snapshot while background refresh builds privately. Refresh publishes only a complete, validated candidate; failure or cancellation leaves the old current snapshot intact. Partial outputs remain private build state. A proposed policy is to reject publication when required build nodes are deferred for lack of model permission; if useful lexical operation without enrichment is desired, compile an explicit reduced plan with its own build identity.

Fresh builds, persistent loads and delta paths must all adopt handles through one constructor/registry route. If opening succeeds but adoption fails, the opener closes the handle. If a later stage fails, its private scope releases all uncommitted resources. Missing registry support in a resource-bearing runtime is a construction error; remove the no-op closer fallback from the loading path and host registration.

```go
// Proposed ownership handoff. Error handling is abbreviated.
r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(id, r)
if err != nil { _ = r.Close(); return nil, err }
return NewChunkDataset(h), nil
```

Duplicate adoption must be defined: either reuse the existing entry and close the redundant new handle, or reject and close it. Never close a shared underlying handle merely because one snapshot retires. During shutdown, reject new work, cancel jobs and operations, join workers, release snapshots and close remaining registry resources. A timeout reports incomplete shutdown; it is not permission to close resources still in use.

## Results cross the lifetime boundary as owned data

Ordinary application code should retain `rag.run`, `result.top()` and `result.chunk(hit)` without acquiring or releasing native handles. Before releasing a query snapshot, materialize records needed by returned hits, trace detail, evidence, outputs and configured neighbor previews. The detached result owns those bytes; it does not own a hidden corpus reader.

```js
// Proposed application behavior.
const result = await rag.run(plan, { q: 'operation identity' });
const first = result.hits[0];
const chunk = first ? result.chunk(first) : null;
```

This copies a bounded result, not the entire corpus. Define record and byte budgets; an over-budget request gets a clear limit error or an explicit pagination contract. Never return a successful hit whose required body was silently omitted. Streaming events that expose records also materialize them while the snapshot is leased. Evaluations needing all relevance labels and full-corpus exports use an internal scoped read instead of expanding ordinary result limits.

HTTP packing belongs inside this boundary. Replace the served app's post-query `art()` read in `pkg/runner/serve.go` with packing from the query's snapshot or its already detached output. Migrate `engine/events.js`, GUI details, agent tool reads, evidence, traces, Evals and sweeps along with hits.

Later GUI lookups send snapshot ID and record reference. The host either acquires that retained generation for the lookup or returns an explicit stale-generation response. It must never reinterpret the old reference against the current dataset. Browser caches key by generation/reference and clear on generation changes. Retention limits determine how long these later reads can succeed; delivered text cannot be revoked by refresh, and retention does not supply access control.

## Durable artifacts need durable ownership records

In-memory leases cannot protect files against another process running GC. The initial model is one process holding an OS-backed exclusive lock for a writable namespace throughout its lifetime. That process owns building, publication, query leases and online GC. A second process must use another namespace or fail. Standalone `store gc` acquires the same lock and therefore runs offline. A browser memory store implements the logical contracts without filesystem locking. Shared multiprocess stores are outside this ticket.

Keep the existing immutable payloads, record codecs, Bleve, terms index and incremental algorithms. Extend artifact descriptors with their owned payloads and dependency artifact keys. Add immutable snapshot manifests listing roots and an atomic current pointer per build identity. These descriptors and manifests are the inventory. Lineage is a bounded, rebuildable search accelerator; losing it can force a full build but cannot hide collectible artifacts. No separate inventory database is needed.

Publication has an ordered boundary:

1. Write payloads into an owned staging directory.
2. Close writers and validate counts, checksums and codec metadata.
3. Move complete immutable payloads into their final namespace paths.
4. Publish artifact descriptors.
5. Publish a complete snapshot manifest.
6. Atomically replace the current pointer.
7. Update derived lineage information.

Before step 6 a crash leaves the old snapshot current. After step 6 the new manifest must be readable even if lineage updates never happened. File and directory synchronization at the supported filesystem boundary is required for power-loss durability; atomic rename alone establishes less. Recovery may reclaim abandoned owned staging files, but must not guess that unknown files are disposable.

Online GC marks roots from current and configured retained snapshots, query leases, builder leases and temporary candidate ownership; it follows descriptor dependencies transitively and sweeps unmarked owned artifacts. Serialize the publication/mark/sweep decision so candidates cannot disappear between durable creation and publication. Missing or corrupt ownership metadata aborts destructive collection with an actionable error.

Traverse the known namespace layout rather than using dots in arbitrary directory names to infer payloads. Fix the dotted-root bug with a local regression. Removing one artifact's hard links must preserve other artifacts' links. Report logical size separately from actual reclaimed space where possible. Choose historical retention deliberately, including privacy consequences for deleted or excluded notes; a fixed retention count cannot substitute for active-reader leases.

## Operations own completion; requests own their subscriptions

Sample, decide and rerank currently duplicate context creation, abort listeners, worker execution, posting to Goja and promise settlement. Extract that lifecycle once, keeping provider request conversion, result checks, protocol errors and tools in supplied functions. `newSample` already supports a worker-to-owner request/reply for tool execution; the shared runner must support that hook. A worker must not access a Goja value. Pass immutable Go values or bytes across goroutines and use errgroup for related workers.

An operation outcome is one immutable discriminated union: success, caller cancellation, deadline, provider failure or runtime shutdown. Capture provider failure information before deferred cleanup cancels derived contexts. In the inspected rerank implementation, classification occurs too late, in the posted callback; sample and decide classify earlier and should not lose their error detail during extraction.

Use a serialized terminal arbiter: whichever terminal result or cancellation transition it accepts first fixes the outcome. Later cancellation cannot overwrite accepted failure or success. Cleanup still runs if posting fails because the VM has closed. Install abort handling before work starts, detach listeners on settlement, and release task resources independently of JS promise delivery. A cancelled context is a request to stop; worker completion and cleanup require a join.

A shared build job has a semantic key, independent context linked to engine shutdown, private builder scope and a set of waiter subscriptions. The key includes all normalized options affecting output; event sinks and caller abort signals are waiter properties. Cancelling A detaches A while B continues waiting. Cancel the job when its final waiter leaves unless a background refresh owner remains. A new caller arriving after cancellation has begun must wait for termination and start/retry a fresh job rather than join a doomed promise.

```text
A subscribes --+
               +--> shared job J -> private candidate -> publication
B subscribes --+
A cancels -> detach A; J still has B
B cancels -> cancel J unless refresh owns it
engine shuts down -> cancel J and subscriptions, join resource cleanup
```

Progress belongs to J and fans out with job/operation IDs into each request's event stream. A subscriber with a full queue is detached locally; it cannot block the job or cancel B. Timer, manual and post-sync refresh use the same build operation, with separate trigger scheduling and authorization.

## One transport owns every response write

Direct query and verb handlers in `pkg/server/rag.go` and `verbs.go` should share admission, request cancellation, event subscription, terminal vocabulary and JSON/SSE orchestration. Keep parameter parsing and domain execution specialized. Current verb requests acquire an app slot; direct queries need the same policy.

The SSE writer is the sole writer and flusher for the response, including start, heartbeat and terminal events. Before every operation that can write, set a bounded deadline through `http.ResponseController.SetWriteDeadline`; use error-returning `Flush`. Wrappers must support the controller's unwrapping path. Unsupported deadline capability is a setup error. After an expired deadline, end the stream; do not attempt to extend it and resume.

On any write or flush failure, stop the writer, cancel/detach the request and join through a bounded cleanup path. Queue overflow has the same local effect. Bound bytes and coalesced text as well as event count; concatenating unbounded deltas defeats the current fixed queue. One healthy stream can last longer than a single per-write deadline because each successful write receives a new bound. At most one terminal event can be sent; failed transport cannot guarantee delivery of it.

A bounded join needs two promises: the transport cannot hang indefinitely, and a still-running worker is not forgotten or allowed to read closed resources. Keep remaining work engine-owned after a reported cleanup timeout and expose the failure. The concrete timeout and escalation policy must be agreed before acceptance. A real socket client that stops reading is necessary to prove writes unblock and app admission capacity recovers; a fake ResponseWriter cannot establish that.

Correct Pi model fallback independently in `pkg/llm/gepp/backend.go:preferred`. Proposed policy: prefer the eligible enabled default; with an explicit nonempty enabled list, use its first eligible model; with an empty list, treat all eligible models as allowed and use a stable sorted model reference fallback. Return no-model only when no eligible allowed model exists. Do not let Go map iteration pick a default.

## Graph transformations use the compiler's references

A structural reference connects a node or output to another node/output. Data inputs, artifact dependencies, decisions, signal dependencies and pinned outputs must participate in one traversal and rewrite contract. Declarative syntax may remain concise, but normalize it once:

```js
// Public syntax.
when: { decision: 'route.needs', min: 0.5 }
// Proposed canonical IR.
when: { source: { kind: 'decision', node: 'route', output: 'needs' }, minimum: 0.5 }
```

Component schemas expose structural references. `references` enumerates them; `rewriteReferences` changes them. Compiler existence/type/phase/cycle checks and editor rename use the same enumeration. Rename checks target uniqueness, rewrites the transaction and validates at its end. Generic deletion explicitly rejects dependents, cascades, or requires a declared replacement. It must not silently make an omitted classifier's dependents unconditional.

Lexical export is a separate transformation with that explicit policy: when it omits a decision-only classifier, surviving retrieval becomes unconditional. Select components by declared capabilities such as local text search, external model or host filesystem, rather than submit triggers. Rewrite or reject every excluded dependency and validate the completed projection before serialization. Package portable datasets from scoped reads; `engine.files()` alone does not represent every server-side source.

Arbitrary callback source is opaque. Graph-aware callbacks must declare named dependency bindings, so renaming changes the binding and leaves function source intact. Reject transformations requiring undeclared opaque references; do not add regex string rewriting or a compatibility fallback. Runtime validation remains necessary even after TypeScript types the canonical IR.

## Implement as vertical replacements

The module boundaries already exist. Use `pkg/ragjs/src/README.md` to navigate `engine/records.js`, `engine/engine.js`, `engine/session.js`, `engine/scope.ts`, `graph/graph.js`, `export/html.js`, `components/`, `api/` and `evaluation/evals.js`. Add strict TypeScript with owning implementations and migrate replaced consumers. Do not add unused declarations over the array/view forks. Maintain local acyclic imports and a thin public facade. Goja, browser and export must use the same deterministically generated implementation.

| Phase | Coherent change and removal | Useful verification |
|---|---|---|
| Characterize | Record baseline failures for all twelve findings; protect equal-count bake invalidation and cold/warm/delta semantics. | Existing records/delta/lineage, server, sample/decide/rerank tests provide fixtures. |
| Datasets | Bind indexes and references, migrate callbacks/hit consumers, remove unionChunks and consumer array/view checks. | One conformance suite per backend; exact bytes for colliding logical IDs. |
| Snapshots | Adopt all resources, publish candidates transactionally, detach results and pack under the query lease. Remove rebuild-cycle GC and caller closing. | Barrier-paused query across two refreshes; failure/close counts and shared artifacts. |
| Inventory | Descriptors/manifests, namespace lock, recovery and mark/sweep; lineage becomes derived. Fix dotted-root listing. | More than 40 versions, three retained; active unopened shards survive; second owner rejected. |
| Operations | Migrate sample/decide/rerank and shared jobs, delete first-waiter ownership. Fix deterministic model fallback locally. | Terminal races, listener cleanup, posting failure, waiter detach/rejoin and fanout. |
| Transport | One bounded writer and lifecycle for query/verbs; remove duplicate pumps. | Stalls, flush failure, byte bounds, terminal races and recovered admission slots. |
| Graph/export | Canonical references, transactional edits, capability projection and portable data. | Decision rename/remove, collision, cycles, opaque callback rejection, exported lexical query. |
| Integration | Update examples, API/help, saved plans, GUI, traces, Evals, sweeps and fixtures; remove dead codecs only after enumerating users. | Common examples in browser/memory and Goja/persistence plus lifecycle and resource measurements. |

If the SSE P1 issue must be fixed before the full migration, put the early correction in the future shared transport path. Do not create a temporary second pump. Integration branch/PR choices belong to the repository owner; the original stack history does not establish its current merge state.

Bump serialized IR and artifact namespace versions. Explain an explicit offline cleanup command for obsolete disposable stores; never delete users' directories automatically. Keep meaningful memory algorithms for browser execution and quality comparisons. Uniform contracts do not require identical algorithms. A new helper must replace a concrete duplicated responsibility; avoid a generic scheduler, dependency injection system or plugin framework.

For runtime changes, begin with `pnpm check`, which runs type checks, Biome, generated-byte checks and runtime tests. Regenerate with `pnpm build`; ordinary Go consumers load the committed asset without a JavaScript toolchain. Run focused Go tests in `pkg/ragjs`, `pkg/runner`, `pkg/natives`, `pkg/artifacts`, `pkg/server` and `web`, then `go test ./...` and `go build ./...` at integration boundaries. Use tmux for any interactive server run and stop web servers through `lsof-who -p PORT -k` as required by repository instructions.

## Acceptance and decisions to settle

Use deterministic provider fakes and hold/release barriers, not sleeps, for competing lifecycle transitions. Required assertions include cold/persisted/delta collection equivalence; nested immutability; same-ID different-dataset resolution through fusion, evidence and display; equal-count changed-content bake invalidation; query and packaging survival after two refreshes; detached results usable after resource release; fresh/load/delta handles closed exactly once; aborted candidates released; more than 32 old versions collectible; active readers protected from online GC; offline collector exclusion; injected failure at each publication step; caller cancellation versus provider errors; shared build waiter races; actual stalled socket recovery; decision edits and functional lexical export; dotted-root listing and empty-list model fallback.

Measure cold build, warm startup, query latency, changed-note refresh, memory, file descriptors and artifact growth on a fixed fixture before and after. Detached result copies and additional provenance may change allocation cost. Record corpus size and options; unit tests alone cannot establish parity or a speed claim. Preserve existing evaluation cases and provider-specific errors.

Before coding publication and public contracts, settle these underspecified points:

- The exact relationship between chunk datasets and multiple document datasets, including ordering lookup keys.
- The normalization of semantic build options, snapshot identity and any competing publication rule.
- Publishability of deferred build outputs and preservation of intentional per-node partial query results.
- Result/event byte limits, historical lookup retention and stale-generation error shape.
- Registry duplicate adoption, supported lock/filesystem durability semantics and cleanup timeout behavior.
- Named callback dependencies and explicit cross-dataset canonicalization policy for existing examples.

The proposals above give starting choices where possible; they are not claims about implemented behavior. API names and file splits can be chosen during implementation. Distributed indexing, remote datasets, multiprocess shared writers, full Obsidian rendering and production authentication remain outside scope. Reindex status and manual refresh should later reuse these operations.

The implementation is complete only when each review finding has a passing regression or a reviewed disposition, replaced mechanisms are removed, examples and documentation match the new API, common flows work in both hosts, and shutdown, publication, cancellation and result lifetime have been verified. Creating a type or moving a function does not satisfy those conditions.
