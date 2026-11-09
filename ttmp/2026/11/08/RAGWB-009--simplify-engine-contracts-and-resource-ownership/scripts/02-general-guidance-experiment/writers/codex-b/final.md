# Implementing consistent data and ownership in rag-workbench

## What the engine does and what this change must accomplish

rag-workbench runs search and answer applications over document collections. An application defines a corpus that reads files, parses Markdown, divides documents into sections, and builds indexes. A query plan retrieves sections from those indexes, combines and filters results, optionally calls models, and returns passages or an answer with evidence. The same JavaScript runtime serves browser examples and Goja applications; Go supplies authorized filesystem access, persistent records, local indexes and model integrations.

RAGWB-009 changes the contracts between those parts. A component should receive the same read-only collection whether records were just computed or loaded from disk. A search result should identify the exact version of each passage. A refresh should publish a complete new generation while queries already using the previous generation finish safely. A cancelled request should release its interest in shared work without cancelling another request. These rules must also hold while packaging HTTP responses, streaming progress and editing/exporting a plan.

This is a proposed design and implementation guide, not a description of completed fixes. The runtime/source baseline is `c267f91`; the inspected working checkout is `3dcc6e0`, whose later changes are documentation. Source and test inspection support the mechanisms described here; no runtime tests were executed for this guide. The original design's `0854c6a` generated-asset line map is historical. Current authored runtime code is under `pkg/ragjs/src/`.

Compatibility can break. Update callers and serialized formats directly, delete replaced mechanisms, and invalidate old caches. Keep the fluent corpus/plan builders, `rag.run`, useful result helpers, Markdown/chunking algorithms, Bleve and memory indexes, incremental builds and provider-specific behavior. Do not introduce adapters, parallel legacy readers, a scheduler, a service-container framework or another inventory database.

## Follow an existing application

Start with `examples/apps/reports/lib/corpus.js` and `lib/plans.js`. The corpus parses the reports and builds text and metadata indexes. The `fused` plan retrieves from both indexes, combines rankings, filters on metadata and limits hits. `adaptive` adds a decision node named `route`; retrieval depends on its `needs` answer. For an offline wiring exercise, the reports README provides:

```sh
go run ./cmd/ragwb --decide fake --no-store verbs --dir examples/apps/reports reports challenge --names lexical,fused,graded,filtered --threshold 50
```

The fake provider checks wiring, not model quality. This command is an implementation starting point, not a validation result reported here.

The runtime compiles immutable builder values into a graph. Nodes have component types, parameters, inputs and output kinds. Build nodes read source files and produce documents, chunks and indexes when inputs change; live nodes execute for queries. Signals are query values such as `q`, `k` and filters. `graph/graph.js:finalize` validates ports, existence, phases and cycles and orders execution. Components declare some additional artifact dependencies through `uses`; decision and signal dependencies have separate discovery code today.

A build digest identifies a particular output given its inputs and component definition. A lineage identifies the same build step across source changes, allowing incremental algorithms to find an earlier output. An artifact is that output saved with codec metadata and payload. A lazy value defers loading the artifact. A record view reads disk records synchronously without decoding the entire corpus. These are existing mechanisms; a published snapshot and a query lease are new contracts defined below.

```text
corpus + plan --> graph compiler --> Engine.build --> memo / saved artifacts
                         |                                 |
                         +----> Session.run <---- Engine.artifacts
                                      |
                              hits / evidence / events
                                      |
                         runner response packing --> JSON or SSE
                                      |
                                  browser cache
```

`pkg/runner/runtime.go` configures the host; `pkg/ragjs/module.go` loads the embedded runtime and binds services. `pkg/runner/serve.go:OpenApp` builds a plan and retains a Session. `Session.run` gets artifacts, waits for `buildShared` when needed, executes live nodes and decorates the result with helpers. Its component calls can await providers. The server's embedded `__serve.run` then packs chunk bodies and neighbor previews. The HTTP handlers in `pkg/server/rag.go` and `verbs.go` return JSON or stream events. `web/app-shell/shell.js` caches chunks, documents and neighbors for synchronous GUI rendering.

Several current boundaries disagree. `engine/records.js:isView` and `asArray` expose storage differences. `Engine.artifacts` collects arrays into one map while holding views separately; `unionChunks` resolves map entries before views and then takes the first matching view. This loses the original ordering of chunk streams. `resolveBakes` passes raw storage-dependent values and hashes document IDs/section counts plus chunk length, so equal-sized edits need not change its cache key. `decorateResult` retains artifacts through `chunk`, `top` and `explain`. The served app packs using current artifacts after the run, which can differ from those used during retrieval.

The compiler already rejects missing or wrongly typed decision dependencies. The graph defects concern transformations: rename does not rewrite decision references, and export removes nodes without one coherent policy for their dependents. Validation rejection is a safeguard to preserve, not a reason to claim the graph accepts every dangling reference.

## Findings the implementation must close

These are the twelve reported review findings preserved from the original design. Their labels describe those reports; reading the source is not independent reproduction of each failure.

| Review | Failure | Required correction |
|---|---|---|
| PR 2, P2 | Stored duplicate chunks differ from cold-array selection | Preserve dataset provenance; remove implicit union |
| PR 2, P2 | Bake receives a record view instead of an array | One dataset contract in callbacks |
| PR 2, P2 | Dotted store-root basename hides artifacts | Traverse defined layout, including root |
| PR 3, P2 | Two refreshes close resources used by an awaiting query | Query snapshot leases and registry ownership |
| PR 3, P2 | Versions beyond 32 lineage entries evade collection | Authoritative inventory independent of lineage |
| PR 3, P2 | Delta readers miss runtime cleanup | Central resource adoption for all openers |
| PR 4, P1 | A non-reading SSE client holds capacity indefinitely | Bounded writes and joined request cleanup |
| PR 4, P2 | First waiter cancellation cancels another's build | Independent job ownership and waiters |
| PR 5, P2 | Deferred cleanup converts provider failure to cancellation | Freeze terminal outcome before cleanup |
| PR 6, P2 | Empty enabled-model list prevents valid fallback | Deterministic eligible-model selection |
| PR 7, P2 | Lexical export retains removed decision references | Explicit capability projection |
| PR 7, P2 | Rename misses decision references | Shared structural-reference visitor |

Dotted roots and model selection need small local fixes. The other findings justify shared contracts because multiple real consumers already need them.

## Make records and their provenance explicit

A dataset is an immutable version of a document or chunk collection. One dataset has one identity, one kind and a deterministic iteration order. Its version identity must distinguish changed content even when record counts and logical IDs stay equal. Memory, persisted and delta implementations expose the same synchronous interface; remote storage is outside this change.

A logical document ID uses a source identity plus a vault-relative path. A title or truncated slug is insufficient across source corpora. A chunk ID can remain stable when its text is enriched, but that stability does not make the original and enriched bytes interchangeable. A record reference therefore contains both dataset version and record ID.

```ts
// Proposed contract sketches; record field schemas omitted.
type DatasetId = string;
type RecordRef = Readonly<{ dataset: DatasetId; id: string }>;
interface Dataset<K extends 'documents' | 'chunks', R> {
  readonly id: DatasetId;
  readonly kind: K;
  readonly size: number;
  get(id: string): R | null;
  values(): IterableIterator<R>;
  materialize(): readonly R[];
}
interface DocumentDataset extends Dataset<'documents', DocumentRecord> {}
interface ChunkDataset extends Dataset<'chunks', Chunk> {
  document(chunkId: string): RecordRef;
  order(document: RecordRef): readonly RecordRef[];
}
type AnyDataset = DocumentDataset | ChunkDataset;
interface Index {
  readonly datasetId: DatasetId;
  search(query: string, options: SearchOptions): readonly Hit[];
}
type Hit = Readonly<{ ref: RecordRef; score: number; reason?: string }>;
```

These types describe behavior, not a declaration layer to place over the existing array/view branches. Implement them in strict TypeScript and migrate the consumers. In particular, every index binds to its exact chunk dataset. Chunk-to-document links identify a document dataset version too. For this sketch, `document` requires an existing chunk ID; an invalid ID is an explicit reference error. `order` verifies the document dataset before returning chunk references. Missing `get` returns null. The literal kind types distinguish document and chunk datasets; validate these kinds and references again when receiving native or serialized values. The sketch omits branding and full record schemas.

Suppose `reports/a.md#ownership` exists in dataset `chunks-original-v1`, and an enrichment step produces `chunks-enriched-v1` with the same chunk ID but prefixed text. A hit from the original index carries `{dataset: 'chunks-original-v1', id: 'reports/a.md#ownership'}`. Packing resolves that pair and copies original text. A hit from the enriched index resolves enriched text. No global map chooses which text wins based on cache state. A proposed `snapshot.resolve(ref)` selects the referenced dataset in that snapshot before reading the record. A hit referring to a missing dataset or record is an invalid-reference error, not permission to fall back to another dataset with the same ID.

Fusion deduplicates exact references by default. Two indexes of one dataset combine naturally. Rankings over different datasets require a declared canonicalization policy when the application wants one logical passage instead of multiple versions. Prefer an ordinary projection component mapping hits to a canonical dataset; another ordinary combine component may define conflict rules. There is no engine-wide last-stream-wins policy. Under exact-reference deduplication, different dataset/ID pairs remain separate hits, even when logical IDs match. If the application wants those versions to count as one passage, it must choose a canonicalization/conflict policy explicitly. A fusion component must document which behavior it performs.

Preserve references through retrieval, filters, reranking, expansion, packing, context citations, trace details, events, evaluations and GUI display. Neighbor traversal uses the hit's dataset and its document relationship. Inspection endpoints cannot infer provenance from a bare section ID. Low-level callers may construct a reference deliberately; `result.chunk(stringId)` must not silently choose a dataset.

Callbacks receive datasets bound to their declared inputs. The examples below assume one bound chunk dataset. For a callback over several document/chunk streams, require named input bindings; never choose the first graph node as `resolveBakes` does today. Keep convenient `ctx.docs` and `ctx.chunks` only where the binding is unambiguous, and reject ambiguous selection. The binding syntax is still to be designed; bound dataset identities all enter the bake key.

```js
// Proposed bake callback usage.
rag.bake(ctx => ctx.chunks.materialize().map(chunk => chunk.id));
rag.bake(ctx => ctx.chunks.size);
```

Expose no `__records`, `__ix`, codec selectors or close methods to ordinary components. Freeze records deeply or return detached values at callback boundaries so nested metadata mutation cannot alter authoritative data. Implementations may cache immutable decoded records. Define deterministic order through cold computation, persistence and delta updates, and run one conformance suite against all backends.

Dataset version keys include content-relevant inputs, normalized component parameters and component/codec versions. Index identities include their input dataset identity. Bake keys include full input dataset identities, callback source/configuration and component version. Counts are useful statistics, not sufficient content identity. Decide the exact source-identity and digest encoding before changing saved formats; do not accidentally rely on object insertion order.

## Publish snapshots; retain resources for their actual users

A snapshot is an immutable mapping from a validated plan's build outputs to dataset and index versions. It has a generation ID. A resource registry owns opened native readers/indexes by artifact identity. Several snapshots may share an unchanged resource; closing one retired snapshot must not close a handle still needed by another.

A query lease retains one snapshot for its entire execution and result construction. A builder has a private scope for resources acquired while building a candidate. The published snapshot itself retains its resources even when it has no active query. Retirement removes that published ownership only after all outstanding leases have finished. The registry closes an entry after its last scope/snapshot ownership ends. These relationships replace waiting for another rebuild before closing handles.

A stable snapshot slot identifies the normalized plan/build configuration whose current generation queries use; changing source content does not change that slot. A generation identifies particular source and output versions. A shared-job key identifies exact requested work, including selected input versions and semantic options. This separation is a proposed clarification: source edits should create new generations without making the last good generation inaccessible to ordinary queries.

```text
slot P --> snapshot A (published; query Q holds a lease)
                  |
          refresh builds candidate B in private scope
                  |
          validate and publish B; A becomes retired
                  |
Q still resolves A; later queries acquire B
                  |
Q copies all required result data, then releases A
                  |
A releases ownership; shared registry entries remain open for B
```

Acquisition means selecting current and retaining it in one serialized step on the JavaScript runtime owner. Publication uses the same owner. Never select a snapshot, yield and retain it afterward. Go registry state accessed across goroutines needs synchronization independently of JavaScript serialization. As a simple initial publication policy, serialize refresh builds for a slot; this avoids an older build finishing late and replacing a newer generation. Other slots proceed independently; unchanged immutable resources may still be shared through the registry. This proposal does not require another layer of step-level jobs.

```ts
// Proposed lifetime sketch, not current API.
async function query(plan, signals, request) {
  await builds.ensureUsable(plan, request.signal);
  return snapshots.withCurrent(plan.slot, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return detachResult(execution, snapshot, request.limits);
  });
}
```

Implement `withCurrent` with a `finally` release covering execution exceptions, cancellation, result-limit errors and packaging/serialization failure as well as success. Keep live-session memo values scoped to generation and relevant inputs/signals; they must not retain native handles after a lease ends. The current `Session.run` invalidates memo when `artKey` changes; migrate that safeguard to snapshot identity.

`ensureUsable` waits if there is no usable published snapshot. A background refresh does not block a query that can use the last good generation. Refresh failure or cancellation before publication commits releases the candidate scope and preserves that generation. Once publication has committed, cancellation cannot restore an earlier pointer; the publication outcome must record the committed generation. Partial outputs may support resumable internal builds but never become query snapshots. Current `Engine.artifacts` accepts `partial` entries, so that path must be removed from public snapshot selection.

Every native opener follows one adoption route: fresh builds, stored loads, incremental bases, delta outputs and any wrapper construction. Today `Persist.InstallEnv` registers loaded closers but permits a no-op fallback; Markdown delta callbacks return record objects separately. Require registry construction for resource-bearing runtimes and fail early if missing.

```go
// Proposed transfer: a successful Adopt owns the new reader.
r, err := OpenRecords2(path)
if err != nil { return nil, errors.Wrap(err, "open chunk dataset") }
h, err := scope.Adopt(artifactID, r)
if err != nil {
    _ = r.Close() // ownership did not transfer
    return nil, errors.Wrap(err, "adopt chunk dataset")
}
return NewChunkDataset(h), nil
```

Define duplicate-ID adoption precisely. Proposed behavior: reuse the existing registry entry, close the redundant new opener, and return a retained handle. A rejected adoption leaves the caller owning the opener. A scope failure releases its previously adopted entries; a wrapper failure after adoption releases through that scope. A scope must transfer candidate ownership to the snapshot on success without a zero-owner gap. Ordinary executor/application callers never close raw handles directly.

Shutdown rejects new work, cancels operations/jobs, waits for workers and owner callbacks to settle, releases published and retired snapshots and finally closes the registry. Use errgroup for related Go goroutines. A timeout is an explicit shutdown failure; it does not justify reporting clean shutdown while workers can still touch closed resources.

### Results and later inspection

`rag.run` must return a detached result, so callers can use `top`, `chunk(hit)` and `explain` after the query lease ends without closing a result. Copy records required by returned hits, intermediate outputs exposed to the user, trace detail, evidence and configured neighbor previews while the snapshot remains retained. Events containing chunk bodies must be copied within that same boundary. Returning the raw execution object can leak handles, so define a public value projection for component outputs rather than assuming every custom output is safe to serialize.

Ordinary application usage remains concise:

```js
// Proposed result API after the cutover.
const result = await rag.run(plan, { q: 'operation identity' });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;
const summary = result.top(5);
// These values remain usable after the internal query lease is released.
```

Copy the bounded result, not the corpus. Declare hit/detail/byte limits. If the requested result exceeds them, return a clear limit error or a designed pagination contract; never return hits with silently missing required bodies. Evaluations that iterate relevance labels and whole-corpus export use internal scoped reads instead of making ordinary results own a corpus lease.

Additional browser lookups send a generation ID and record reference. The server checks and retains that generation in one step serialized with collection, then reads it under a scoped lease, or returns a specific stale-generation error. A retained durable manifest may reopen resources through the registry; retention need not keep all handles open. Protect its payloads before opening any lazy shards. It never substitutes the current generation. Cache documents, chunks, misses and neighbor links by generation/reference and reset generation-specific state on change. The response must identify its generation. Snapshot retention duration and stale response encoding remain policy decisions; last-good query availability does not imply indefinite availability for old browser links. Retained excluded/private notes also require an explicit retention policy. Refresh cannot revoke text already delivered, and retention does not provide access control.

## Make the store an authoritative ownership inventory

A runtime registry protects open handles. It cannot by itself stop another process deleting a lazy shard. Durable ownership therefore needs a store contract as well.

Retain existing immutable record/index codecs and payload algorithms. Extend artifact descriptors to list payload ownership and dependencies. Immutable snapshot manifests list artifact roots, and an atomic current pointer selects a manifest per snapshot slot. The inventory is descriptors plus manifests, not bounded lineage history. `pkg/artifacts/lineage.go` currently retains at most 32 entries, searches eight and collects entries still in those files; an older output dropped from that inventory is never considered. Lineage becomes a derived incremental-search accelerator that can be rebuilt or discarded.

Illustrative records, not a proposed final serialization schema:

```text
artifact index-7: codec=bleve-vN, payloads=[index-7.bleve/], dependencies=[chunks-4]
artifact chunks-4: codec=records2-vN, payloads=[chunks-4.records2/], dependencies=[docs-2]
snapshot generation-B: roots=[index-7, chunks-4, docs-2]
current slot-P: generation-B
```

One process holds an OS-backed exclusive lock on a writable namespace throughout its lifetime. That owner controls builds, publication, leases and online collection. A second process uses another namespace or fails clearly. Standalone `store gc` acquires the same lock and runs offline; it cannot observe another process's in-memory leases. The memory/browser implementation has the same logical store behavior without filesystem persistence or OS locking. Multi-process shared stores and distributed leases are outside this design.

Publication proceeds in a defined order:

1. Write payloads in owned staging directories.
2. Close writers and validate counts, checksums and codec metadata.
3. Move complete immutable payloads into the namespace.
4. Publish immutable artifact descriptors atomically.
5. Publish a complete snapshot manifest.
6. Atomically replace the slot's current pointer.
7. Update derived lineage metadata.

The candidate's input identities must describe the bytes actually read. Decide how to capture or revalidate changing source files so a build cannot label mixed or changed reads as an earlier input version; merely hashing the file tree before reading is insufficient if edits overlap the build. This is an input-consistency requirement to settle with the source component, not a property supplied by the current store.

Before the pointer change, recovery uses the old generation; afterward it can open the complete new generation even if lineage is stale. File and directory synchronization must match the supported filesystem/platform durability guarantee; rename atomicity alone does not establish power-loss durability. The durable pointer update and in-memory publication need a defined commit point. Cancellation after a committed pointer cannot be treated as if the old pointer still selected current. Specify and fault-test this boundary before implementing publication.

GC marks current snapshots, configured retained snapshots, active query/builder ownership and temporary build resources, then follows descriptor dependencies transitively. The owner serializes publication and mark/sweep decisions, preventing a staged or newly durable candidate from being swept before publication. Sweep only unmarked owned artifacts. Missing or corrupt ownership metadata aborts destructive collection with an actionable diagnostic; unknown files are not permission to guess. Crash-left staging cleanup must respect active build ownership rather than only directory age.

Hard-linked payloads belong to artifact directories: deleting one directory's links does not delete other artifacts' links. Report logical payload bytes separately from reclaimed disk bytes. Retention count controls historical availability and privacy; it is not a substitute for active-query leases. `store.go:List` must traverse known namespace/shard/descriptor positions, including a dotted root basename, without walking arbitrary directory payload internals.

## Give asynchronous work one terminal outcome

The sample, decide and rerank Go bridges repeat cancellation listeners, worker execution, owner posting, settlement and cleanup. Extract that common lifecycle while leaving provider request conversion, result validation, tool semantics and detailed errors specialized. `sample.go:newSample` already determines its failure before posting settlement; `rerank.go:rerankAsync` reads `callCtx.Err()` inside the posted callback, after a deferred cancel can execute. The new runner must preserve the former ordering consistently.

Use a discriminated terminal outcome with success, caller cancellation, deadline, provider failure and runtime shutdown. For example, the typed runtime boundary could expose:

```ts
// Proposed outcome shape; provider failure fields remain provider-specific.
type Outcome<T> =
  | { status: 'success'; value: T }
  | { status: 'cancelled'; reason: unknown }
  | { status: 'deadline'; message: string }
  | { status: 'failure'; error: ProviderFailure }
  | { status: 'shutdown'; message: string };
```

Provider failures retain structured status/code/request ID/retry details where available. Cleanup cancellation is never evidence that the caller cancelled.

The race rule is: a terminal completion accepted before a cancellation transition remains terminal; otherwise cancellation wins. A single operation state arbiter must serialize those transitions, for example with a mutex around pending-to-terminal changes. Compute provider classification using the real cause before calling derived-context cleanup, and capture the immutable outcome in the owner callback. Install cancellation before starting work, remove abort listeners during final owner settlement when the owner remains available, post settlement at most once and release host resources even if the owner has shut down and cannot execute a promise callback. Stop accepting new progress events once the operation has a terminal outcome.

Freezing an outcome does not mean the worker has stopped. If cancellation wins, signal its context and join worker/tool activity before releasing resources it can still access. Proposed lifecycle pseudocode follows; `tryTerminal` serializes with cancellation callbacks, and `startOwned` runs asynchronously under an owned errgroup:

```text
install cancellation callback:
    if tryTerminal(cancel/deadline/shutdown): cancel worker context
startOwned:
    value, error = execute(workerContext, emit, ownerToolHook)
    classification = classify(value, error, actualCancellationCause)
    tryTerminal(classification)       // cannot overwrite an earlier outcome
    join child work and tool activity
    release worker-owned host resources
    cancel derived context for cleanup
    post to owner exactly once: remove abort listener; settle captured outcome
    if posting fails/owner is disposed: finish host cleanup without JS settlement
```

The owner must keep servicing the request/reply hook while workers finish; never block that owner waiting for a worker that needs an owner callback. Runtime shutdown must coordinate stopping new calls, cancelling and draining owned work before disposing the owner.

Only immutable Go values or bytes cross goroutines. Goja values and functions remain on the runtime owner. Sample's tool calls need an explicit request/reply hook that posts tool execution to the owner and waits cancellation-aware for the result; a one-shot rerank callback abstraction is insufficient. Do not remove protocol-specific validation merely to share a runner.

### A build job has independent waiters

Replace `Engine.buildShared`'s first-caller promise ownership with a small registry of jobs keyed by exact normalized build identity and semantic options. A job owns its context, candidate scope, progress and terminal outcome; engine shutdown cancels it. Each requester owns a waiter/subscription. Capture its request signal, detail and event sink before the first await, as `engine/scope.ts:scoped` already does. `withScope` provides synchronous inheritance, not async-local state. Closing an event scope stops delivery; it does not itself abort a worker. Request A and request B can wait for the same job without either request owning it.

A cancellation detaches A and stops its events; B continues. When the final waiter detaches, cancel the job unless an explicit background-refresh owner retains interest. A new waiter arriving during cancellation waits for termination and then starts/retries work; it does not attach to the doomed job. Successful completion publishes its candidate once into its slot through the snapshot arbiter, records the commit outcome, and settles attached waiters. An already committed generation remains published if a waiter subsequently cancels. Shutdown cancels jobs and waiters and joins cleanup.

Progress belongs to the job and fans out to request-local bounded subscriptions, carrying job and operation IDs. A slow/failed subscriber detaches locally; it does not block the job or cancel other subscribers. Distinguish options affecting build semantics, which belong in the job key, from event sinks and request signals, which do not. Timer, manual and post-sync refresh all call this same build/refresh operation; scheduling and access checks remain at their entry points.

### Bound the entire HTTP stream

A full event queue timeout already cancels slow requests in `pkg/server/sse.go`. It does not bound a writer blocked in the socket: the pump ignores write errors, plain `Flush()` returns no error, and handlers wait unconditionally for the pump. Coalesced text can also grow bytes without filling more queue entries.

Use one response helper for direct queries and verbs, sharing admission, cancellation, event subscription, JSON/SSE framing and terminal-error vocabulary. Keep parameter parsing and operation bodies separate. The helper also applies a bounded network-write policy to its JSON response. A single writer goroutine owns all SSE writes and flushes, including headers/start, events, heartbeat and the terminal event. Handlers submit terminal outcomes to it; they do not write afterward.

Before each write/flush, set a bounded deadline through `http.ResponseController.SetWriteDeadline`, then check write and controller `Flush` errors. Wrappers must expose the controller's unwrap path. Unsupported deadline capability is a setup error, not unbounded fallback. An exceeded deadline cannot be extended according to the installed Go API; end that stream. Per-write bounds permit a long healthy stream without imposing one short total stream timeout.

Bound queue bytes, individual events and coalesced text as well as queue count. A write/flush failure or overflow stops pumping, cancels/detaches the request, and runs a bounded join of its work and writer. At most one terminal event is attempted; failed transport cannot promise terminal delivery. Once the request's work and writer release their ownership, admission capacity becomes available. Supported operations must honor cancellation sufficiently to meet the tested request-join bound. A timeout cannot forcibly terminate an arbitrary Go worker. If one exceeds the bound, report an operational failure, keep its resources owned until it exits, and avoid claiming that its work or occupied capacity was reclaimed. Verify capacity recovery using the actual supported operation in the stall test. Exact timeouts and byte limits need configuration and tested defaults.

Test a real network connection that stops reading, not just fake writers. The observable criterion is recovery of an inflight slot within the configured bound. Also test initial and terminal write failure, flush failure, queue overflow, coalescing bounds and completion/cancellation races.

## Transform graph references as one contract

Normalize structural references once. Include data inputs, component artifact dependencies, decisions, signal dependencies and selected/pinned graph outputs. The compiler, editor and exporter must enumerate and rewrite these through the same schema/visitor rather than separately knowing where strings happen to live.

```js
// Concise DSL accepted at the boundary:
when: { decision: 'route.needs', min: 0.5 }
// Proposed canonical IR:
when: { source: { kind: 'decision', node: 'route', output: 'needs' }, minimum: 0.5 }
```

The visitor yields typed references with enough location information to rewrite them. Validation uses those references to check target existence, output kinds, phase rules and cycles. Rename rejects duplicate targets, rewrites every reference and validates the edited transaction once. Graph outputs are graph-level references even though they are not contained by a component node. Do not omit them from the common traversal.

General deletion must choose reject, cascade or explicit replacement. Proposed default: reject unresolved dependents. Preserve useful data rewiring only where an operation states its replacement behavior and type checks it. Lexical export has a distinct, explicit policy: remove unavailable decision-only model components and make affected supported retrieval unconditional. The policy does not authorize every production graph deletion to discard its conditions.

Export declares component capabilities such as browser text index, external model and host filesystem. Trigger timing alone does not determine portability. Project the complete graph under supported capabilities, rewrite affected references, then validate once before serialization. Bundle normalized portable datasets so a Go-backed source does not depend on `engine.files()` enumerating server files. Test the resulting browser execution using the same generated runtime bundle.

Opaque JavaScript callbacks cannot be safely renamed by replacing source text. Graph-aware callbacks declare named dependency bindings in their component options. The visitor changes the bound reference while callback source uses the stable binding name. Reject a structural edit/export requiring rewriting undeclared opaque references. This restriction must be visible to custom-component authors.

## Implement in vertical changes

RAGWB-010 already provides modular authored sources and build tooling. Keep one acyclic local import graph and one generated bundle for Goja, browsers and standalone export. `pnpm build` generates `pkg/ragjs/assets/rag.js`; do not edit it independently. Strict TypeScript currently covers errors, identity, text and request scope, while extracted JavaScript is not fully checked. New dataset, provenance, snapshot and operation implementations and migrated consumers must become strict TypeScript. Keep declarations with owners and validate native/JSON inputs at runtime. Biome formatting/lint and TypeScript checks cannot prove ownership safety.

| Change | Primary entry points and consumers | Remove when it lands |
|---|---|---|
| Datasets/provenance | `engine/records.js`, components, hits/events, evaluation, GUI | `unionChunks`, `isView` branches and consumer array conversions |
| Snapshot/results | `engine/engine.js`, `session.js`, `runner/serve.go`, shell lookup/cache | rebuild-count retirement and artifact-backed result helpers |
| Registry | `natives/persist.go`, Markdown delta/fresh/index constructors, runtime setup | no-op closer fallback and unmanaged direct close paths |
| Inventory/publication | `artifacts/store.go`, `lineage.go`, store CLI and runner setup | lineage as inventory; unsafe independent GC |
| Operations/jobs | `sample.go`, `decide.go`, `natives/rerank.go`, `buildShared`, `scope.ts` | duplicated lifecycle and first-waiter ownership |
| HTTP transport | `server/sse.go`, `rag.go`, `verbs.go` | separate pump/terminal orchestration |
| Graph/export | `graph/graph.js`, `export/html.js`, API registration | separate reference discovery and trigger-only pruning |

First characterize all twelve findings and record which reproduce on the baseline. Include cold, persisted and delta builds and equal-count bake edits. If the stack is being used, implement the P1 SSE correction early inside the transport intended to survive the refactor.

Use these vertical checkpoints after characterization:

1. **Datasets and provenance:** implement both backends and migrate bake, built-ins and all hit consumers. Extend `pkg/runner/records_test.go` and `delta_test.go` for public conformance; remove union and representation tests from consumers.
2. **Snapshots and resources:** migrate query, result/event packaging, evaluation and inspection in the same checkpoint. Extend runner serve/refresh tests with a held provider and counting closers. Remove rebuild-count retirement and unmanaged native close paths.
3. **Durable inventory:** add descriptors/manifests, namespace locking and conservative mark/sweep. Extend artifact store/lineage tests with more than 40 versions, crash boundaries and live readers; change store CLI ownership too. Correct dotted-root traversal here.
4. **Operation runner and shared jobs:** migrate sample, decide, rerank and build joining. Extend their existing tests with cancellation barriers, owner shutdown, tool replies and request-local progress. Remove first-waiter context ownership and duplicated settlement code.
5. **Shared transport:** route run and verb endpoints through the single bounded writer/lifetime. Extend server rag/verb tests with stalled real sockets and slot recovery, then delete duplicate pump orchestration. Land this checkpoint early if the P1 fix is needed before the other phases.
6. **Graph and export:** migrate compiler/editor/export references together. Extend route/decision and runtime export tests for rename, removal and portable unconditional lexical projection. Remove trigger-only pruning and separate reference discovery.
7. **Integrated cleanup:** remove unused formats after auditing consumers, update examples and help, run both hosts and measure resource/performance behavior. Review that each new module replaced an old responsibility.

The Pi fallback fix is independent: prefer a configured eligible enabled default, then configured eligible enabled order; when the enabled list is empty, choose from a deterministic eligible order rather than iterating the empty list. Sort full provider/model references as a proposed default and test it. Empty eligible sets still produce the appropriate unavailable error.

Each phase must replace working paths and ship meaningful tests. Do not extract empty modules first or leave two engines. Keep provider protocols, memory ranking algorithms and immutable incremental file behavior that already work. Enumerate live codec references before deleting obsolete formats. A new abstraction earns its place by removing duplicated responsibility; the executor should not grow component-specific branches where component contracts suffice.

Migrate GUI, trace/evidence, Evals, sweeps, saved plans, presets, export, examples and help with each API cutover. Bump IR and artifact namespace versions. Old caches are disposable, but their deletion is an explicit offline cleanup action, never silent directory deletion. A single integration branch should carry the refactor; verify current PR status before deciding how to update the review stack. This design does not authorize merging, force pushing or resolving review threads.

For runtime edits, start with `pnpm build`, `pnpm check` and focused tests such as `go test ./pkg/ragjs ./pkg/runner ./web`; add artifact/server/native tests for their phases, then run `go test ./...` and `go build ./...` at integration boundaries. Use tmux for any long-running server or TUI interaction and capture its output. Follow repository formatting/lint requirements. Preserve existing useful tests while replacing assertions about obsolete representation, such as the cold Map/warm view distinction in `runner/records_test.go`.

## Verify transitions, not just interfaces

Use small synthetic corpora and deterministic provider barriers. Hold and release a provider explicitly to test overlapping refreshes; arbitrary sleeps do not establish the intended ordering. Counting closers and file-descriptor measurements supplement value assertions.

| Contract | Required sequence and assertion |
|---|---|
| Dataset | Same values, missing-ID behavior, iteration order and nested immutability through memory, saved load and delta |
| Provenance | Two datasets share IDs but different text; retrieval, fusion policy, pack, trace and GUI preserve correct bytes |
| Bake | Cold/warm callback uses one API; equal-count changed content invalidates cached bake |
| Lease | Pause query on A, publish B then C, resume and package A successfully; failed refresh keeps last good |
| Resource | Fresh/load/base/delta/wrapper-failure/shutdown close each resource exactly once after its last user |
| Detached result | Helpers and emitted records remain readable after lease release and unused native handles close |
| Inventory | Create more than 40 versions, retain three plus active leases, collect exact reachable set regardless of lineage bounds |
| Store exclusion | Second serving owner and standalone GC fail to acquire a live namespace |
| Crash | Fail between publication steps, reopen to complete old or new snapshot; corrupt metadata blocks destructive GC |
| Jobs | A cancels while B waits and B succeeds; all detach cancels; new waiter retries after cancelling job; subscriber overflow stays local |
| Outcome | Provider error remains failure after deferred cleanup; cancellation/deadline/shutdown races settle once with defined cause |
| SSE | Non-reading real socket, flush/write faults and overflow terminate within bounds and release capacity |
| Graph | Rename all reference kinds; deletion rejects or applies declared policy; projection has no dangling references |
| Export | Adaptive source exports a functioning unconditional lexical page with portable data |
| Small fixes | Dotted root lists outputs; empty enabled list selects deterministic eligible fallback |

Run the same representative example in browser/memory and Goja/persistent hosts. Preserve semantic parity, including ordered arrays; incidental JSON object insertion order is not a compatibility constraint. Bundle publication/module tests and actual Goja tests cover different host boundaries. Minimal DOM Node tests are not visual browser validation.

Measure cold build, warm startup, query latency, changed-note refresh, memory, open descriptors and artifact growth on the same corpus and conditions before and after. Detached results add allocations; provenance also changes payloads. Tests passing does not establish performance parity. Investigate material regressions and report the measurements.

Completion requires all twelve findings to have passing regressions or reviewed dispositions; replaced paths removed; APIs documented as implemented; browser and Goja examples working; and publication, cancellation, shutdown and cleanup sequences verified. Naming a registry or snapshot is not evidence that it enforces its contract.

## Decisions to settle before coding each boundary

The required direction is established: explicit provenance, synchronous uniform datasets, query leases, central resource ownership, detached results, durable inventory, exclusive namespace owner, independent jobs, immutable outcomes and canonical graph references. The proposals in this guide refine gaps in that direction; they are not already implemented or separately approved APIs.

Before dataset migration, settle source identity/digest encoding, deterministic order and explicit callback/canonicalization binding syntax. Before snapshot publication, settle slot identity, source-version capture, serialized refresh policy, durable/in-memory commit behavior and duplicate adoption. Before shipping HTTP/inspection, choose retention, stale error encoding, result/event byte limits and write/join bounds. Before graph migration, settle general deletion policy and callback dependency bindings. These affect correctness and caller behavior; exact private class names do not block implementation.

Distributed indexing, remote datasets, independently concurrent processes sharing a store, full Obsidian rendering and production authentication remain outside scope. Later reindex status and manual refresh should use the same operations rather than adding another refresh implementation. The intended result is a smaller engine whose ordinary callers need no storage tests or resource cleanup and whose internals have one place to decide identity, lifetime, cancellation and structural references.
