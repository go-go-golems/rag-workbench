# Consistent records and ownership in the rag-workbench engine

## 1. What this change lets us rely on

Rag-workbench turns a collection of Markdown files into searchable chunks and, optionally, a cited answer. Application authors compose a corpus, which describes preparation when files change, and a plan, which describes each search. The same JavaScript runtime runs in browsers and in Goja, the JavaScript interpreter hosted by the Go CLI/server. Go components add authorized filesystem access, disk-backed records and indexes, and model providers: services that generate, classify or score text. Shared collection and ownership rules do not require different index backends to produce identical ranking scores; preserve existing parity and quality comparisons where applicable.

RAGWB-009 proposes a simpler contract across these hosts. A chunk retrieved from an index must resolve to the exact text that index searched. A query must finish using the generation it started with even if files change while a model is answering. Resources must remain open until their users finish, and then close exactly once. Ordinary application searches must not acquire a new cleanup obligation.

The implementation does not yet provide these guarantees. The runtime baseline supplied for this guide is `c267f91`; current source inspection uses the post-010 module layout. RAGWB-010 has already extracted authored code into `pkg/ragjs/src/` and established TypeScript/build tooling. The generated `pkg/ragjs/assets/rag.js` is still the shared host payload, but is no longer the place to edit. Proposed contracts and sketches below describe work for 009, not an implemented API.

Runtime module paths such as `engine/session.js` below are relative to `pkg/ragjs/src/`. Paths beginning with `pkg/`, `examples/` or `web/` are relative to the repository root. Navigate by symbols because extraction changed the original bundle line numbers.

Keep the fluent builders, Markdown/chunking algorithms, Bleve and terms indexes, incremental build algorithms, providers and useful evaluation cases. Replace storage-dependent collection handling, implicit chunk merging, rebuild-count cleanup, request-owned shared builds, duplicated async/HTTP lifecycles and scattered graph-reference logic. Compatibility can break: migrate callers and stored formats directly, without adapters or dual legacy paths.

## 2. Follow one search through today's system

A corpus contains build steps. Parsing produces documents with sections; chunking produces smaller records; an index records how to retrieve those chunks. A plan contains live steps: it takes signals such as query text, retrieves hits, combines rankings, filters candidates, reranks them with a second scoring step, packs evidence into a context, and may call a model to answer. A hit is a scored reference to a chunk; evidence is the selected text and citation information used by an answer.

This existing style should stay recognizable:

```js
// Existing composition style; requires a configured rag runtime and authorized root.
const reports = rag.corpus('reports')
  .from(rag.source.dir({ root: '/data/reports' }))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());
const search = rag.plan('search').over(reports)
  .signals({ q: '', k: 8 })
  .retrieve('text', { k: 30 }).limit();
const result = await rag.run(search, { q: 'operation identity' });
```

The compiler converts builders to an intermediate representation (IR): nodes with component types, parameters, typed inputs, execution phases and outputs. `graph/graph.js:finalize` validates ports, dependency existence, phases and cycles before execution. It already rejects missing declarative decision dependencies; the problem with edits is inconsistent rewriting, not permission to execute arbitrary dangling graphs.

`engine/engine.js:build` computes build digests: keys describing the producing step, parameters and inputs so matching outputs can be reused. It reuses memoized values, loads saved outputs lazily, or computes new ones. An artifact is one saved build output. A lineage is the history of a particular build step across changes; `tryDelta` uses a compatible prior entry to update only changed data. A record view is a disk-backed collection that can decode a record on demand. These mechanisms avoid rebuilding or reading a whole vault unnecessarily.

`Session.run` obtains `Engine.artifacts(ir)`, which aggregates documents, chunks, indexes and reading order. It runs live components against that aggregate and can await providers. `decorateResult` adds methods such as `chunk` and `top` that retain access to the aggregate. In `pkg/runner/serve.go`, the served app awaits the query, calls collection logic, and fetches artifacts again to package chunk bodies and neighbor previews. HTTP handlers send the response as JSON or Server-Sent Events (SSE), a stream of named JSON events.

```text
corpus + plan -> compiler -> build outputs -> artifacts aggregate
                                  |                |
                            persistent store   Session.run -> providers
                                                   |
                                            runner packaging
                                                   |
                                              JSON / SSE -> GUI
```

The reports preset in `examples/presets/reports.js` makes the relationships concrete: it creates section chunks and failure-table chunks, indexes both, adds a metadata index and combines three ranked lists with reciprocal-rank fusion. Its model steps run on submission; lexical steps also run while typing. This is why the refactor must cover more than a single array and a single query.

### Why current boundaries fail

`engine/records.js` exposes `isView`/`asArray`; consumers must know storage representation. `Engine.artifacts` combines chunk arrays in a Map but handles views separately through `unionChunks`. Arrays overwrite by ID as graph streams are visited; union lookup prefers the Map and then the first matching view. Cold and warm storage can therefore choose different text for a duplicate ID. Reading order separately comes from a selected primary chunk stream.

`resolveBakes` passes raw outputs to callbacks and keys them using document IDs/section counts and chunk length. A bake is a callback evaluated against prepared corpus data to construct a parameter, such as a synonym table. A stored view is not the same API as an array, and equal counts do not establish equal content.

`Engine.gc` closes previously retired native handles on a subsequent collection; it does not count active queries. A query paused in a provider can span two refreshes. Retaining a JavaScript object does not keep its native reader open. Fixing this alone would leave the runner's later current-artifact lookup free to mix generations.

The Go store keeps 32 lineage entries and searches eight (`pkg/artifacts/lineage.go`). Its GC discovers outputs through lineage entries. Once a version falls out of history, the collector can lose its ownership record. In addition, loaded readers register runtime closers in `Persist.InstallEnv`, while Markdown delta callbacks return readers through a separate construction path. Cleanup authority is spread across layers.

## 3. Give records explicit identity and one collection API

A dataset is a read-only, versioned output containing documents or chunks. Its version identifies exact contents and their producing contract; an ID within the dataset identifies one record. Two datasets may have the same local record ID and different bytes. For example, an enrichment step may keep section ID `notes/ops.md#retries` while adding a prefix to the text.

```text
plain dataset P                      enriched dataset E
id = notes/ops.md#retries             id = notes/ops.md#retries
text = "Retry after timeout."         text = "Operations: Retry after timeout."
index plain -> P                      index enriched -> E
```

Retrieval from the enriched index must return `{dataset: E, id: ...}` and resolve the enriched text. The engine must not ask an implicit global union which version wins. A snapshot, introduced in section 4, contains the datasets and indexes available to that query.

The following TypeScript sketch communicates the proposed component contract; `Chunk` and `SearchOptions` definitions and validators are omitted. Records returned by all methods must satisfy the same immutability rule:

```ts
type RecordRef = Readonly<{ dataset: string; id: string }>;
interface Dataset<T> {
  readonly id: string;
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): T | null;
  values(): Iterable<T>;
  materialize(): readonly T[];
}
interface ChunkDataset extends Dataset<Chunk> {
  document(chunkId: string): RecordRef | null;
  order(document: RecordRef): readonly RecordRef[];
}
interface Index {
  readonly datasetId: string;
  search(query: string, options: SearchOptions): readonly Hit[];
}
type Hit = Readonly<{ ref: RecordRef; score: number }>;
```

Keep record access synchronous for current local disk and memory hosts. Encoding, sharding and page access remain backend details. Deterministic iteration, null for missing records and explicit full materialization are shared behavior. Records exposed to user callbacks must be deeply immutable or detached, including nested metadata; a shallow freeze is insufficient. Neither callbacks nor the dataset API expose `close`, `__records`, `__ix` or codec choice.

A chunk's source document reference includes its document dataset version. `document` returns null for a missing chunk. `order` returns the ordered chunk references for a source document, or an empty list if that document has no chunks; it rejects a document reference from a different source dataset. Neighbor order belongs to its chunk dataset and source document, rather than a global primary stream. Document logical identity includes source identity and relative path; titles and truncated slugs are not global identities. Each index binds to the exact chunk dataset it was built from. Index identity includes normalized parameters, component/codec version and input dataset identities.

Here is the concrete relationship between record identity and query generation. These are illustrative target records, not existing response JSON:

```js
const snapshotA = {
  id: 'A', datasets: ['D', 'P', 'E'],
  indexes: { enriched: { datasetId: 'E' } }
};
const hit = { ref: { dataset: 'E', id: 'notes/ops.md#retries' }, score: 0.82 };
// E.document(hit.ref.id) identifies the source document in D.
const lookup = { snapshot: 'A', ref: hit.ref };
```

A later snapshot B may contain E2 instead of E. The hit still identifies E, and the lookup still requests A. The dataset version identifies the record bytes; the snapshot ID identifies the collection of mutually consistent build outputs used by the query. Neither identity should be reconstructed from the other. The example abbreviates the source-qualified document ID for readability.

Fusion deduplicates exact references by default. Two rankings over P can combine naturally. Combining P and E as the same logical content requires an explicit projection to a canonical dataset or a component with a documented equivalence/conflict policy. The policy is application-visible; it must not become another engine union rule. Carry references intact through reranking, packing, citations, trace, evaluations and GUI display.

Bakes and custom components receive datasets in both cold and stored builds:

```js
// Proposed callback API.
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id));
rag.bake(ctx => ctx.chunks.size);
```

Prefer iteration or size when a full array is unnecessary. Build/bake cache identities include full input dataset versions, callback source/configuration and component version. Exact canonicalization and explicit captured callback configuration must be specified before implementing the cache; arbitrary lexical closures cannot be inferred reliably from a function string.

## 4. Hold one generation until a query is fully packaged

A snapshot is an immutable mapping from build nodes to validated datasets and indexes. A published snapshot is available for queries. A candidate snapshot is private to a build. A query lease records that the query is still using a particular published snapshot. Current publication, configured historical retention and active leases are interests that keep the snapshot alive. Only after its last interest ends does it release its resource references. A resource registry owns native readers and index handles; snapshots and builders retain entries they need. Several snapshots can share unchanged entries.

The registry is necessary because releasing an old snapshot cannot close a shared index still used by a newer one. Snapshot retirement and native closure are separate transitions:

```text
published A -- query acquires A -- query awaits model
     |
build B privately -> validate -> publish B (A retires)
build C privately -> validate -> publish C
     |
query resumes on A -> detach response records -> release lease A
     |
if A has no remaining retention/lease interest, release its resource references
close each resource only when no snapshot, builder or active user retains it
```

Selecting the current snapshot and incrementing its lease count is one serialized action on the JavaScript runtime owner. Publication uses the same owner. A Go registry accessed across goroutines additionally synchronizes its own state. A query cannot select A, yield to publication, then attempt to retain already-released A.

A proposed refinement is to distinguish a stable publication slot from a job version. The slot identifies the plan's build structure, source configuration and semantic host configuration across source-content changes. A structural/configuration change selects another slot; unchanged structure may deliberately serve older content while refreshing. Each job key includes the full normalized build inputs and options for the exact version being built. Otherwise a source change could make the last good snapshot undiscoverable precisely when background refresh should keep it usable.

For initial implementation, serialize candidate builds within each slot and coalesce queued refresh requests to the newest requested source state. Equivalent requests join the active job. A differing request waits for that job to terminate before starting the next candidate. This proposed coordinator rule prevents an older slow build from publishing after a newer one. Different slots may build independently. It adds publication ordering to the original design; it is not behavior established by today's digest-keyed promises.

```js
// Proposed flow; helper names are illustrative.
async function query(plan, signals, request) {
  await ensureUsableSnapshot(plan, request.signal);
  return snapshots.withCurrent(plan.slot, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return detachResult(execution, snapshot, request.resultLimits);
  });
}
```

`ensureUsableSnapshot` waits when the plan has no usable published snapshot. It does not wait for background refresh when a last good snapshot exists. A failed/cancelled candidate leaves that snapshot current. Publication occurs only after complete validation; partial outputs can aid later work but never become current query state.

The lease covers event record projection, live execution, evidence and final response packaging. Use `finally`-style scope release on success, cancellation, component failure and packaging errors. All returned hit bodies, trace detail, evidence and configured neighbor previews are copied before release. `result.top()` and `result.chunk(hit)` then read detached result data; callers need no cleanup call. Copy bounded output, not the entire corpus. Ordinary application code remains concise:

```js
// Proposed result API: hit carries its dataset-qualified reference.
const result = await rag.run(search, { q: 'operation identity' });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;
// result remains readable after the internal query lease is released.
```

If requested detail exceeds a declared record/byte budget, return an explicit limit error or a designed pagination response; never silently omit records required by emitted hits.

Whole-corpus evaluation and export can use internal scoped snapshot reads. Later GUI lookups carry snapshot ID plus record reference. The server resolves that generation if retained, otherwise returns a stale-generation response; it never resolves an old hit against the latest text. Cache by generation/reference and clear GUI caches on generation change. Retention cannot revoke text already delivered or replace access checks.

### Resource construction and shutdown

Fresh builds, persistent loads and delta-created readers all adopt resources through one registry path. Adoption accepts only a newly opened handle still owned by its constructor. Reusing an existing artifact acquires/retains its registry entry without reopening or adopting that handle again. Missing registry configuration in a resource-bearing runtime is a construction error; remove `Persist.InstallEnv`'s no-op closer fallback.

```go
// Proposed ownership sketch; cleanup-error reporting helper is illustrative.
// Returned token is private to lifecycle modules.
r, err := OpenRecords2(path)
if err != nil { return nil, err }
token, err := scope.Adopt(resourceID, r)
if err != nil {
    closeErr := r.Close() // caller still owns r until successful adoption
    return nil, reportAdoptionFailure(err, closeErr)
}
return newChunkDataset(token), nil
```

Proposed policy: reject duplicate resource IDs during adoption, leaving the newly opened handle with its caller to close. Normal reuse uses the existing registry entry; snapshots share it through retain/release. The registry becomes the only closer after successful adoption. Constructor code must never pass a handle the registry already owns into this failure path. Keep these APIs private and test trusted native implementations; TypeScript cannot enforce native ownership. Build scopes release uncommitted resources after later-stage errors; successful publication transfers their interest to the snapshot without a gap. Record and surface close failures, including cleanup after failed adoption, rather than silently discarding them.

Shutdown rejects new work, cancels jobs/operations, joins workers, releases snapshots and closes the registry. If a worker misses the shutdown deadline, report incomplete cleanup; do not close resources it may still access and claim success. Application code does not close registry handles directly.

## 5. Make the durable inventory authoritative

A registry protects open resources in one process. It does not stop another process deleting a shard a reader has not opened yet. The initial deployment model therefore grants one process an OS-backed exclusive lock for each writable store namespace. That owner manages publication, builds, active leases and online GC. Another serving process fails clearly or uses another namespace. Standalone `store gc` must acquire the same lock and runs only offline. Browser memory storage follows the logical contract without OS locking.

Retain existing immutable payloads and codecs. Add an artifact descriptor listing owned payloads and artifact dependencies, an immutable snapshot manifest listing root artifacts, and a small current-snapshot pointer per publication slot. Descriptors/manifests, rather than bounded lineage history, form the inventory. Lineage becomes a derived incremental-base lookup that can be rebuilt or discarded. No separate inventory database is needed.

Publication establishes a complete recoverable generation:

1. Write payloads under an owned staging directory.
2. Close writers and validate counts, checksums and codec metadata.
3. Move complete immutable payloads into the namespace.
4. Publish artifact descriptors atomically.
5. Publish a complete immutable snapshot manifest.
6. Atomically replace the slot's current pointer.
7. Update derived lineage information.

A crash before the pointer switch preserves the old current snapshot. A crash afterward may leave stale lineage but must leave the new snapshot readable. Specify file/directory synchronization for supported filesystems; atomic rename alone does not establish power-loss durability. Recovery identifies abandoned staging and incomplete unpublished candidates from ownership metadata, without guessing that unknown files are disposable.

The owner serializes publication and GC's mark/sweep decision. Mark current and retained manifests, active queries/builders and staging ownership, then follow descriptor dependencies transitively. Sweep only unmarked owned artifacts. Missing or corrupt ownership metadata aborts destructive collection with an actionable error. This rule must cover newly durable candidates before their pointer is published.

Keep retention policy separate from query duration. An old snapshot with a lease remains safe even after many refreshes. A 40-version store must remain collectible even though only eight versions are searched as incremental bases. Retained historical notes also retain content users may have excluded from current search; choose and document retention deliberately. Hard-linked payloads are removed through their owning artifact directory; removing one link leaves other owners' links intact. Report logical removed bytes separately from actual reclaimed disk bytes where possible.

Fix dotted-root listing locally in `pkg/artifacts/store.go:List`: traverse the known namespace layout, not a heuristic that skips every directory name containing a dot. This does not require changing storage algorithms.

## 6. Separate operation completion, job interest and transport delivery

A model call, a shared build and an HTTP stream have different lifetimes. Reuse mechanisms where the responsibilities match; do not merge all three into a generic workflow framework.

### Native operation lifecycle

Sample, decide and rerank all install cancellation, execute Go work outside the VM owner, post events/results back and settle promises. Extract this lifecycle once, keeping provider request conversion, validation, errors and tool behavior supplied by the operation. Use immutable Go values/bytes across goroutines. Sample's tool callbacks require an owner-thread request/reply hook; a worker must not access Goja objects directly.

A terminal outcome is one fixed success or failure classification, not the state of a context read later. The rerank bridge currently inspects `callCtx.Err()` in a posted callback after deferred cancellation can run. Sample and decide already classify before posting; preserve that ordering in the shared runner.

```ts
// Proposed terminal vocabulary; provider details remain available on failure.
type Outcome<T> =
  | { kind: 'success'; value: T }
  | { kind: 'cancelled'; reason: unknown }
  | { kind: 'deadline'; reason: unknown }
  | { kind: 'failure'; code: string; message: string; details: unknown }
  | { kind: 'shutdown'; reason: unknown };
```

One operation arbiter accepts the first terminal transition. Success/provider failure accepted before cancellation remains terminal; otherwise cancellation wins. Fix classification before cleanup cancels derived contexts. Post settlement once; detach abort listeners on settlement. Runtime disposal can prevent JS callback delivery but must not prevent task/resource cleanup. Related Go workers use errgroup. The exact error mapping distinguishes caller abort, deadline, provider failure and runtime shutdown rather than turning every error into cancellation.

The arbiter is a small synchronized terminal-state owner shared by the cancellation callback and worker result path. A Go-like sketch shows the ordering; owner-posting errors and listener-removal details are omitted:

```go
// Register cancellation before starting worker; TryFinish accepts only once.
onCancel := func(cause error) {
    terminal.TryFinish(classifyCancellation(cause))
}
worker := func() error {
    defer releaseWorkerResources()
    value, err := execute(callCtx, emitImmutableEvents)
    terminal.TryFinish(classifyResult(value, err))
    return nil
}
// The terminal owner captures one fixed outcome, stops further events,
// detaches cancellation, and schedules settlement on the VM owner once.
// Derived-context cancellation for cleanup happens after outcome capture.
```

`TryFinish` serializes the competing terminal events; it does not reread a derived context after cleanup to change the outcome. If cancellation wins while the worker is still returning, its late value is ignored for settlement and its resources still release when the worker exits. JS delivery and worker cleanup remain separate responsibilities.

### Shared builds

Today's `buildShared` returns the first build promise and its request options. Replace it with a small job registry keyed by full normalized build identity, including all options affecting semantics. The job has an engine-owned context and private candidate resources. Each request is an independently cancellable waiter; an explicit refresh owner also counts as interest.

```text
A waits ---+
           +--> J builds one candidate --> validates --> publishes
B waits ---+
A aborts -> remove A; B remains
B aborts -> cancel J unless a refresh owner remains
engine shutdown -> cancel J and waiters; join cleanup
```

The per-slot coordinator described in section 4 orders different-version jobs; this registry handles equivalent-request joining and waiter lifetime. A new waiter must not attach to a job whose last owner has already initiated cancellation. Wait for its cleanup and start/retry a replacement. On successful publication the snapshot owns resources; if publication fails the job still owns cleanup.

Job progress fans out through waiter subscriptions with job and operation IDs. It is not tied to the first request's event scope. A closed or slow subscriber detaches locally and cannot block/cancel the shared job while other interest remains.

### Bounded HTTP streaming

`pkg/server/sse.go` already limits queue entries and cancels after a full-queue wait. It does not bound socket writes, ignores pump write errors and coalesces text without a byte cap. Both query and verb handlers join that pump before returning; a blocked client can retain admission capacity indefinitely.

Use one response helper for direct queries and verbs. Domain operations emit typed events and return an outcome; the helper owns common admission scope, request cancellation, subscription, JSON/SSE framing and terminal error vocabulary. Endpoint-specific parameter parsing and result envelopes remain local.

The SSE writer alone writes or flushes the response, including setup, heartbeats and terminal events. Verify deadline capability before committing response headers. Before each potentially blocking write/flush, install a bounded deadline using `http.ResponseController.SetWriteDeadline`; use its error-returning `Flush`. Middleware wrappers must support the controller's unwrapping path. Unsupported deadline capability is a setup error. Send the terminal event through this same writer and then join it; handlers must not write a final event themselves after the pump returns. An exceeded write deadline ends the stream; do not try to revive it by extending the deadline afterward.

Write failure, flush failure and queue/byte overflow stop transport, cancel/detach this request and initiate a bounded join. They do not rewrite an already-fixed operation outcome or cancel unrelated build waiters. Bound queued bytes, single-event size and coalesced text as well as queue entries. Per-write deadlines support healthy long streams without one short total timeout.

A healthy completed stream sends one terminal event; every stream permits at most one attempt. A disconnected client cannot be promised delivery. Slot-recovery acceptance requires both transport termination and cancellation-aware operation cleanup within the configured join bound. Choose that bound and verify provider cancellation before enabling the path. If a provider ignores cancellation, report a join timeout and retain accounting for its active worker/resources until it actually exits; do not close live handles or report successful cleanup. This is an explicit implementation gate, not a property deadlines alone establish. A real non-reading TCP client is required because fake ResponseWriters cannot demonstrate socket deadline behavior.

## 7. Make graph edits use the compiler's references

A decision component produces named answers that can control later nodes. For example, `when: { decision: 'route.needs', min: 0.5 }` makes retrieval conditional on the `needs` answer from node `route`. Rename must update that reference, and removing route requires an explicit policy for retrieval.

Normalize convenient DSL strings once into typed structural references:

```js
// Proposed canonical IR condition.
when: {
  source: { kind: 'decision', node: 'route', output: 'needs' },
  minimum: 0.5
}
```

A common schema/visitor enumerates and rewrites data inputs, artifact dependencies, decision references, signal dependencies and pinned plan outputs. Compiler existence/type/phase/cycle validation uses that same enumeration. Rename checks uniqueness, rewrites every reference and validates the resulting transaction once. Removal either rejects dependents, cascades explicitly or supplies a declared replacement; never guess how a missing classifier should change production behavior.

Export has a distinct policy. A standalone lexical page cannot call server-only model/filesystem components. Declare capabilities such as local index, external model and host filesystem; select supported components and project unsupported parts intentionally. When a decision-only classifier is omitted under the lexical policy, retained retrieval becomes unconditional. Validate the complete projected graph before serialization. Do not infer exportability from whether a node runs on typing or submission.

`export/html.js` currently embeds `engine.files()`, which does not prove native directory sources are present. Export must bundle a normalized portable dataset from a scoped snapshot read and the shared generated runtime. Test an exported classified plan actually searching without a provider.

Arbitrary JavaScript callback source cannot be safely renamed through text replacement. Graph-aware callbacks declare named dependency bindings in component options. Rename updates binding references; callback code uses stable binding names. Reject transformations that would require rewriting undeclared opaque structural references. This restriction replaces current heuristic scanning rather than introducing a compatibility path.

For example, a conceptual component declaration can bind `{ route: { kind: 'decision', node: 'classifier' } }`; its callback reads `ctx.bindings.route`. Renaming `classifier` to `intent` changes the binding's node field and leaves the stable callback name `route` unchanged. This illustrates the dependency contract, not a new public builder signature.

## 8. Implement contracts where their code now lives

010's source extraction is complete; full engine typing is not. `core/errors.ts`, `identity.ts`, `text.ts` and `engine/scope.ts` are strict implementations; most other modules remain JavaScript. As 009 changes behavior, implement dataset/reference, snapshot/resource and operation contracts in strict TypeScript and migrate their consumers. Discriminated outcomes and typed provenance help express the rules; runtime native/JSON validation and lifecycle tests still enforce boundaries types cannot prove.

| Work | Current starting points | Remove on cutover |
|---|---|---|
| Datasets/provenance | `engine/records.js`, `components/`, `engine/engine.js` | Array/view branches and `unionChunks` |
| Query lifetime | `engine/session.js`, `pkg/runner/serve.go` | Artifact-backed result methods and current-generation repacking |
| Ownership/store | `pkg/natives/persist.go`, `pkg/natives/markdown_native.go`, `pkg/natives/delta.go`, `pkg/artifacts/` | Unmanaged readers, no-op closer, rebuild-count closure, lineage inventory |
| Operations/jobs | `engine/engine.js`, `engine/scope.ts`, `pkg/ragjs/sample.go`, `pkg/ragjs/decide.go`, `pkg/natives/rerank.go` | First-request job ownership and duplicated bridge lifecycle |
| HTTP | `pkg/server/sse.go`, `pkg/server/rag.go`, `pkg/server/verbs.go` | Duplicated pumps and unbounded writes |
| Graph/export | `graph/graph.js`, `export/html.js` | Separate reference rewrites and trigger-based pruning |

Maintain an acyclic dependency direction: facade/builders -> graph and application operations -> executor/jobs/snapshots -> datasets/resources/store. Provider bridges feed operation events; HTTP consumes them. Keep `rag` thin and load the same generated implementation in browsers, Goja and exports. Update `pkg/ragjs/module.go` when module loading changes, and migrate `web/app-shell/shell.js` where remote references/generations become GUI host data. Deterministic assembly and publication tests already exist; extend them, without hand-copied host forks.

The useful reuse pairs are concrete: memory/disk collections share dataset behavior; fresh/load/delta paths share adoption; model operations share lifecycle; query/verb endpoints share response lifetime; compile/edit/export share references; manual/timer/post-sync refresh share build operations. They do not need a durable scheduler, service container or plugin framework. Each abstraction should replace an existing responsibility or duplicate path in its phase. Preserve browser memory algorithms and quality-comparison backends even when interfaces become uniform.

A small independent correction belongs in `pkg/llm/gepp/backend.go:preferred`. Existing code treats an empty enabled-model list as unrestricted for checking a default but has no unrestricted fallback. Proposed deterministic policy: use eligible enabled default, otherwise first eligible configured enabled entry, otherwise—only when the list is empty—first eligible model by sorted full reference. Eligibility still requires supported API and credentials. Preserve explicit configured choices and clear no-model errors; add focused tests instead of designing a new model registry.

## 9. Cut over in tested vertical steps

Begin by reading `examples/presets/reports.js`, then `Session.run`, `Engine.artifacts`, and the embedded `__serve.run` in `pkg/runner/serve.go`. For a concrete local baseline command, run `go test ./pkg/runner -run TestChunkRecordsReplaceTheChunksMap`; it exercises cold/warm records today. Inspect its representation assertions before adapting it to the new shared dataset suite.

1. Characterize all twelve reported findings with deterministic regressions and record baseline results. Make the P1 stalled-SSE correction early if the stack will run before the broader refactor, in the transport that will become shared.
2. Land datasets and provenance across memory/persistence/components, bakes, indexes, raw hit consumers and result evidence. Remove union and storage detection. Equal-count changed input must invalidate bakes.
3. Land snapshots, adoption and detached results together through query execution and runner packing. Migrate evaluations and inspection, then remove retired-by-rebuild closure.
4. Land descriptor/manifests, namespace locking, publication/recovery and inventory GC. Fix dotted-root traversal. Make lineage derived. Snapshot behavior from step 3 can first be tested with memory or private fixture stores, but safe persistent serving/online collection is gated on this step; do not ship leased snapshots over an independent unsafe collector.
5. Land the operation runner and shared-job waiter lifecycle, preserving provider errors and sample tool request/reply. Correct deterministic model fallback locally.
6. Route both HTTP execution forms through bounded response orchestration and verify admission recovery.
7. Land shared structural references, graph transactions and capability export with portable data.
8. Remove obsolete formats/dead paths, update examples/help and perform integrated correctness and resource/performance checks.

Each step replaces its old route; avoid empty scaffolding or two permanent engines. Migrate GUI, traces, citations, evaluation/sweeps, saved plans, examples and exports with the contract they consume. Keep `rag.run`, fluent composition and `result.top` because they serve users. Update `result.chunk` to take a hit/reference; do not guess a dataset from a bare string. Bump serialized IR and artifact namespace versions and update fixtures. Old caches require explicit offline cleanup instructions; do not delete user directories silently or add legacy readers.

Coordinate one integration branch and any review stack before implementation; historical stack status is not established by this guide. No merge, force-push or thread-resolution action follows from the design. Commit coherent tested milestones.

## 10. Verify sequences, not abstraction names

The original review inventory remains the acceptance checklist, with its historical priority labels. These are required regressions for the proposed refactor; no row claims that its behavior has already passed.

| Reported finding | Required regression and boundary |
|---|---|
| #2/P2 duplicate chunk precedence | Same logical ID in two datasets resolves its own indexed bytes through cold, warm, delta, fusion, packing and GUI |
| #2/P2 bake gets record view | One callback API across hosts/backends; nested metadata cannot mutate source; equal-count changed content invalidates cache |
| #2/P2 dotted store root | Saved outputs list under dotted root using defined layout |
| #3/P2 query crosses two refreshes | Pause provider, publish twice, resume and package original generation before lease release |
| #3/P2 history loses old versions | Create 40+ versions, retain three, collect exact reachable set independent of lineage bounds |
| #3/P2 delta readers miss cleanup | Fresh, load and delta adoption; failed stages and shutdown close each handle exactly once |
| #4/P1 stalled SSE holds capacity | Real non-reading socket, queue/byte overflow and flush errors terminate within bounds and capacity recovers |
| #4/P2 first waiter controls build | A aborts while B succeeds; all leave cancels; refresh owner preserves job; newcomer avoids cancelling job |
| #5/P2 cleanup reclassifies failure | Provider failure, success, deadline, abort and shutdown races preserve one accepted outcome |
| #6/P2 empty enabled list | Missing/unavailable default falls back deterministically to eligible model; explicit restrictions remain enforced |
| #7/P2 export leaves decisions | Classified plan projects to valid unconditional lexical search with actual portable records |
| #7/P2 rename misses decisions | Rename rewrites all structural references; collision/removal/opaque callback cases reject or follow declared policy |

Run one dataset conformance suite against memory, persisted and delta outputs: deterministic ordering, missing-ID behavior, materialization, nested immutability, document linkage and rejection of invalid dataset references. Adapt `pkg/runner/records_test.go:TestChunkRecordsReplaceTheChunksMap` to preserve semantic assertions while removing its required cold-Map/warm-view distinction. Preserve `pkg/runner/delta_test.go:TestDeltaBuildEqualsFullBuild` and incremental/ignore coverage in `serve_watch_test.go`.

Also test first query with no current snapshot, failed refresh preserving the last good one, per-slot build ordering/coalescing, resource sharing between snapshots, detached results after query release, stale-generation inspection, duplicate adoption, publication failure cleanup, refusal of a second namespace owner or standalone collector while serving, and recovery at every durable publication boundary. Corrupt ownership metadata must stop deletion. Preserve semantic parity rather than assertions about incidental object key order or private storage markers.

Use synthetic corpora/provider fakes, barriers controlling awaits, and counting closers. Add file descriptor checks where practical. A barrier that holds the provider while two refreshes complete directly tests the failure; arbitrary sleeps do not. Real socket tests remain necessary for streaming deadlines.

After runtime edits run `pnpm build`, `pnpm check` and `go test ./pkg/ragjs ./pkg/runner ./web`; run focused native/store/server checks for changed packages and full `go test ./...`/`go build ./...` at integration boundaries. Follow repository formatting/lint guidance. `pnpm check` verifies TypeScript, Biome, generated bytes and bundle/module tests. Node's minimal DOM tests do not establish actual browser-engine behavior; exercise the reports and vault apps in their intended hosts. Use tmux for interactive servers and the repository's process cleanup instructions.

Measure the same fixtures before/after: cold build, warm startup, query latency, changed-note refresh, memory, file descriptors and artifact growth. Record configuration and sizes. Detached result copying can change allocation costs; passing correctness checks is not evidence of performance parity.

## 11. Decisions still needed and completion conditions

These compact decision records distinguish the original target requirements from proposed refinements in this guide. The target remains proposed work, not implemented behavior.

| Context and alternatives | Target decision and reason | Consequence / status |
|---|---|---|
| Local records could expose storage-specific APIs or async access everywhere | One synchronous dataset contract preserves local simplicity | Backend encoding stays private; inherited 009 target |
| Different chunk versions could compete in a union or carry provenance | Dataset-qualified references retain the indexed bytes | All hit/evidence consumers migrate; inherited 009 target |
| Old readers could close after a fixed rebuild count or after actual users leave | Snapshot leases and registry references track real use | Shared entries close only after final use; inherited 009 target |
| Returned results could hold handles requiring caller release or own copied data | Bounded detached results keep ordinary search cleanup-free | Output budgets become explicit; inherited 009 target |
| A store could coordinate independent process leases or have one writable owner | Exclusive namespace ownership makes online GC see every user | Standalone GC is offline; inherited 009 target |
| Different build versions could publish concurrently or be ordered per slot | One candidate at a time per stable slot prevents late stale publication | Queue/coalescing rules need implementation review; proposed refinement |
| Duplicate resource adoption could reuse a fresh handle or reject it | Reject duplicate adoption; normal reuse retains an existing entry | Fresh constructor retains cleanup on failure; proposed refinement |

Remote storage, distributed indexing, multi-process writable stores, full Obsidian rendering and production authentication remain outside this change. Manual refresh/status features should reuse the new operations later.

Before implementing identity/publication, specify canonical hashing, callback configuration, slot/job identity and supported locking/durability primitives. Before exposing results/inspection, choose output budgets, snapshot retention and stale-response shape. Before declaring transport complete, choose write/queue-byte/join limits and define timeout accounting so an unjoined worker is never reported as stopped. These are concrete policy decisions, not requests for new frameworks.

The implementation is complete when all twelve findings have passing regressions or reviewed dispositions, obsolete routes are removed, the same meaningful example runs across browser/memory and Goja/persistence, shutdown/publication/cancellation sequences pass, and public documentation describes the new API. No finding is resolved merely by adding a type named Snapshot or Registry.
