---
Title: Round three intern guide Codex B
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
LastUpdated: 2026-11-08T22:54:10.62500238-05:00
WhatFor: ""
WhenToUse: ""
---

# Consistent search data, ownership and execution in rag-workbench

## What this change is for

rag-workbench lets an application describe how to turn documents into searchable passages and how to answer a query over those passages. The same JavaScript engine runs in a browser or in Goja, the JavaScript interpreter embedded in the Go CLI/server. Go provides filesystem access, persistent record readers, indexes and model-provider bridges. Application authors compose these features with corpus and plan builders.

A corpus describes build work: read documents, parse Markdown, split sections into chunks, and build indexes. A chunk is a passage with text, document metadata and an identity. A plan describes query work over a corpus: retrieve ranked hits, combine rankings, filter, rerank, pack evidence and optionally generate an answer. A hit currently names a chunk by its ID. That last detail becomes unsafe when two versions of a passage share an ID or refresh replaces the data while a query awaits a model. Refresh repeats build work after source documents change.

RAGWB-009 proposes one collection interface, dataset-qualified record references, immutable published snapshots, explicit ownership of native resources, one asynchronous operation lifecycle, and one graph-reference mechanism. An application should continue to write a concise search plan and call `rag.run`; it should not need to discover whether records live in memory or on disk, select among duplicate passage versions, or release search results.

This is a design and implementation guide, not an implemented API. Source observations refer to runtime baseline `c267f91`; inspected HEAD `6cc526d` has no differences from that baseline in `pkg`, `examples`, `web` or `package.json`. The proposal permits breaking APIs and stored formats. Update callers directly and remove replaced paths. Do not add adapters or legacy readers.

Keep the working Markdown/chunking algorithms, Bleve and terms indexes, incremental build algorithms, browser memory implementations, provider protocols and meaningful evaluations. The work changes how these parts compose, rather than replacing them with a new workflow framework.

## Follow one current query

Start with `examples/apps/reports/lib/corpus.js` and `lib/plans.js`. They define an ordinary corpus and lexical, fused, model-assisted and adaptive plans. The adaptive plan uses a model decision to decide whether retrieval should run. The following abbreviated composition uses the existing builder style; the source directory must be authorized by the Go host.

```js
const corpus = rag.corpus('reports')
  .from(rag.source.dir({ root: '/data/reports', include: ['*.md'] }))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());

const plan = rag.plan('search').over(corpus)
  .signals({ q: '', k: 10, filters: {} })
  .retrieve('text', { k: 30 })
  .filter.where({ field: 'filters' })
  .limit();
```

For a query such as “when should readers close?”, retrieval finds passages and assigns ranking scores. A model-assisted plan can pack selected passages into evidence: text plus labels such as `E1`, which the generated answer cites as `[E1]`. The trace records what each query node did, including timing and ranking details. The CLI and GUI use these records to explain results. This is why preserving a hit's text matters beyond the initial index search.

Signals are the per-query values, such as `q` and `k`. Compilation turns the builders into an intermediate representation, or IR: nodes with component types, parameters, references, output kinds and a dependency order. Build nodes produce documents, chunks and indexes; live nodes run for a query. Components declare what inputs they accept and supply their algorithms. `graph/graph.js:finalize` already checks many invalid graphs, including missing or wrong-type decision dependencies.

```text
builders -> compile IR -> build outputs -> Session.run(query)
                              |                  |
                       memory / store       hits, trace, evidence
                                                 |
                                 result / runner packing / HTTP / GUI
```

The authored runtime is under `pkg/ragjs/src/`. `engine/engine.js` builds and memoizes outputs; `engine/session.js` executes query nodes. `engine/records.js` provides lazy loading and current array/view conversions. Go's `pkg/natives` implements disk records and local indexes. `pkg/runner/serve.go` connects a long-lived runtime to queries and refresh. `pkg/server/rag.go`, `verbs.go` and `sse.go` implement HTTP responses; `web/app-shell/shell.js` consumes them.

A saved build output is an artifact. Its store key identifies immutable persisted data. A lineage groups successive artifacts from the same build step so an incremental build can find a previous compatible version and process a delta: changed and removed records rather than the whole corpus. These concepts are worth retaining. A record view is the existing lazy reader for stored records; it avoids decoding the entire collection at startup.

Today, a cold build may return arrays while a stored build returns views. `Engine.artifacts` merges chunk outputs into one global lookup, and `Session.run` resolves bare hit IDs against it. If a live component awaits a provider, refresh can advance the engine while that query still holds its old artifacts. `decorateResult` also closes over artifact lookups. The served path then reads current artifacts again to pack the result, so lifetime protection must include response construction.

To explore the existing reports example offline, its README supplies:

```sh
go run ./cmd/ragwb --decide fake --no-store verbs --dir examples/apps/reports reports challenge --names lexical,fused,graded,filtered --threshold 50
```

The fake provider tests wiring, not model quality. This guide reports source inspection; it does not claim that this command or the tests below have been run.

## What the existing mechanisms fail to guarantee

The original design records twelve review findings. They remain implementation acceptance obligations, even when several share one cause. The table distinguishes the source mechanism from the proposed remedy; it is not a claim that every reported failure has been reproduced here.

| Finding | Source mechanism and required response |
|---|---|
| Duplicate chunk versions differ between cold/stored paths | `records.js:unionChunks` prioritizes memory over views, then earlier views; array merging uses `Map.set`. Preserve dataset provenance instead of defining another implicit winner. |
| Bake callback receives a view instead of an array | `Engine.resolveBakes` assumes `.map` and `.length` on selected outputs. Give all callbacks one dataset API. |
| Dotted store root is skipped | `artifacts/store.go:List` skips directory names containing dots, including the root. Traverse the store schema explicitly. |
| Two refreshes close resources used by an awaiting query | `Engine.gc` retires outputs for a later GC call, without measuring query use. Retain snapshots until all readers finish. |
| Old versions disappear from collector inventory | `LineageRecord` truncates at 32 entries; `GC` discovers artifacts through those entries. Make inventory authoritative independently of bounded search history. |
| Delta record readers miss runtime cleanup | `markdown_native.go` exposes delta-created Records2 handles outside the persistent-loader closer registration. Centralize adoption of every opened resource. |
| A non-reading SSE client holds a request slot | `sse.go:pump` ignores write errors and lacks write deadlines; verb handlers wait for the pump before deferred slot release. Bound actual writes and joins. |
| Cancelling the first waiter cancels a shared build | `Engine.buildShared` returns a build promise carrying the first caller's options. Give jobs independent lifetime and detachable waiters. |
| Cleanup changes provider failure into cancellation | `rerankAsync` checks context state in an owner callback after deferred cancellation. Freeze outcomes before cleanup. |
| Empty enabled-model list defeats fallback | `llm/gepp/backend.go:preferred` allows the default with an empty list, but searches only that list for alternatives. Define deterministic eligible fallback. |
| Lexical export retains decision dependencies | `export/html.js` removes nodes by trigger/output in stages. Select by capability and explicitly rewrite excluded gates. |
| Rename misses decision references | `graph.js:applyOp` rewrites inputs and pinned outputs, but not `when` or all component dependencies. Reuse one reference visitor. |

Current graph validation may reject the broken rename or export. Preserve that safeguard: the problem is that valid requested transformations fail or need scattered knowledge, not that invalid graphs are always accepted. Current SSE queues also have capacity and a slow-queue timeout. Those are useful protections; they do not bound a socket write already blocking the pump.

## Data identity before storage details

### A dataset is one immutable output version

A dataset is a read-only collection of documents or chunks from one build output version. Its identity changes when the content or semantic build inputs change. An index records the exact chunk dataset it indexed. A snapshot, introduced next, groups compatible dataset and index versions for a query.

Consider two chunk outputs containing the logical section `report.md#ownership`. The original contains `Readers close after use.` An enriched output contains `Resource ownership: Readers close after use.` They are different records for retrieval purposes even though their local section IDs match. An index built on the enriched text must resolve a hit to that text, regardless of whether either output is on disk.

Proposed TypeScript contract sketches follow; they describe behavior, not a declaration layer to add over the old implementation:

```ts
type RecordRef = Readonly<{ dataset: string; id: string }>;
type Dataset<T> = {
  readonly id: string;
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): T | null;
  values(): Iterable<T>;           // deterministic order
  materialize(): readonly T[];     // explicit complete traversal
};
type ChunkDataset = Dataset<Chunk> & {
  document(id: string): RecordRef | null; // source document, or missing chunk
  order(document: RecordRef): readonly RecordRef[];
};
type Hit = Readonly<{ ref: RecordRef; score: number; reason?: string }>;
type Index = {
  readonly datasetId: string;
  search(query: string, options: SearchOptions): readonly Hit[];
};
```

`Chunk`, `SearchOptions` and ancillary fields are omitted here. Proposed checked-reference rules: `document` returns null for a missing local chunk ID; `order` accepts only a document reference belonging to this chunk dataset's recorded source-document relationship, and returns that document's ordered chunk references. A mismatched document reference is an error. Snapshot resolution rejects unknown dataset identities and reports a missing record distinctly; it never searches other datasets for the same local ID. Public datasets expose no `close`, codec selector, `__records` or `__ix`. Reads remain synchronous for current local memory/disk hosts. A speculative remote backend does not justify making every read asynchronous.

Objects returned from memory and disk must have the same mutation semantics. Deep-freeze authoritative records or return detached records at the callback boundary; shallow freezing leaves nested metadata mutable. Immutable decoded records may be cached. A conformance suite must cover record order, missing IDs, materialization, nested metadata, document relationships and provenance across memory, persistent and delta implementations.

Logical document identity must include a stable source identity and a vault-relative path. A title or truncated slug is insufficient across corpora. Dataset/index version identities must include normalized parameters, component/codec versions and input dataset identities. Specify the canonical encoding before persisting the new format; callback source/configuration must also enter cache identity. Equal record counts or section counts do not establish equal content.

### Combining rankings preserves references

The following is a proposed example, with shortened illustrative keys and local section IDs. Real logical document IDs also carry their source identity:

```js
const original = { dataset: 'chunks-original-v7', id: 'report.md#ownership' };
const enriched = { dataset: 'chunks-enriched-v3', id: 'report.md#ownership' };
// original and enriched refer to different records.
const hit = { ref: enriched, score: 0.82 };
const chunk = snapshot.resolve(hit.ref);
```

Retrieval, fusion, filters, rerank, evidence packing, traces, evaluation and display must carry the reference. Fusion deduplicates exact references by default. Combining two indexes over one dataset therefore still works naturally. Combining rankings over different enrichment datasets needs an explicit equivalence rule, preferably a component that projects references into a chosen canonical dataset before fusion. That projection must verify its mapping and document score/conflict behavior. There is no engine-wide “last stream wins” rule.

Bake callbacks compute parameters from build data. They receive datasets as well. A callback must identify which output it reads when a graph has several chunk streams. Proposed binding sketch, not an API available today:

```js
rag.bake(
  { datasets: { searchChunks: 'chunks-search' } },
  ({ datasets }) => datasets.searchChunks.materialize().map(c => c.id)
);
```

Here `chunks-search` is a build node ID; `searchChunks` is the callback's local name. The compiler validates that the binding names a chunk output, and execution passes that exact dataset version. This binding is also a structural graph reference, so rename and export must visit it. For a count, the callback reads `datasets.searchChunks.size` without full traversal. Migrate all bake/custom callbacks at the cutover, and include bound dataset identities, callback source/configuration and component version in cache identity. The binding spelling can be refined; selecting the first stream implicitly is not an acceptable default.

## A query owns a stable snapshot until its result is complete

### Publication and query leases

A snapshot is an immutable mapping from validated build outputs to datasets and indexes. A published snapshot is complete and eligible for queries. A refresh builds a candidate privately; it publishes only after validation. Failed, cancelled or partial builds never replace the current snapshot. Queries continue using the last good snapshot during refresh; only an initial build with no usable snapshot needs to block them.

The proposal needs two identities. A **publication slot** names the build specification whose current snapshot a query requests, independent of changing source contents. An **exact build key** names a particular computation, including source/dataset versions and semantic options. For example, the reports slot can point to snapshot A built from Monday's files, then snapshot B built from Tuesday's files. A and B have different exact build keys while sharing the slot. Each snapshot also has its own immutable snapshot ID. The precise key encoding is a phase-one decision; these distinct roles are required so “last good snapshot” has a clear meaning.

A query lease is internal permission to use a snapshot until execution and packaging finish. Application authors do not see or release it. Acquire the current snapshot and increase its reader count in one serialized action on the JavaScript owner. A publication cannot slip between selecting and retaining it.

```text
query Q acquires A ------ await provider ------ package A ------ release A
                          |            |
refresh builds B -> publish B       publish C
                          |
A is retired, but Q still retains it; no rebuild count can retire Q's lease.
```

A resource registry owns opened native handles, such as record readers and indexes, keyed by artifact identity. A snapshot retains the entries it uses; several snapshots can retain one unchanged artifact. The current snapshot also holds ownership even with zero queries. Retirement removes that current role, but readers keep it alive. Close a registry entry only when no snapshot, builder or other explicit user retains it.

Proposed execution shape:

```js
async function query(plan, signals, request) {
  await builds.ensureUsable(plan, request.signal);
  return snapshots.withCurrent(plan.slotKey, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return detachResult(execution, snapshot, request.resultBudget);
  }); // finally releases after execution AND packaging, including errors
}
```

`ensureUsable` returns immediately when a compatible published snapshot exists. Both the result's record copies and event payloads must use that acquired snapshot. `pkg/runner/serve.go` must stop repacking against `eng.artifacts(ir)` after `Session.run` returns. Timer, manual and post-sync refresh must invoke the same build/refresh operation.

Concurrent refresh publication also needs an arbiter. Proposed simple policy: the slot coordinator permits one active refresh and records one coalesced rerun request if inputs change during it. A rerun recomputes the source version before building. All publication to that slot goes through this coordinator, preventing an older slow candidate from overwriting a newer snapshot. Exact-key jobs may be shared by several interested callers, but they submit candidates through the slot coordinator rather than writing its pointer themselves. This is a proposed scheduling policy to settle when implementing publication, not an existing runtime guarantee.

### Resource adoption and failure ownership

Every fresh, loaded, delta-created and temporary/base reader must enter the same registry. Existing persistent loading's `AddCloser` fallback must become a construction error when a resource-bearing host has no registry. This prevents successful opening with no eventual owner.

An opener owns a new handle until the registry accepts it. If opening fails there is no handle to release; if adoption fails, the opener closes it. If a later build stage fails, the builder scope releases every adopted candidate resource. Publication transfers the candidate's retained resources to the snapshot before releasing the builder scope. A failure before that transfer leaves the builder responsible.

```go
// Proposed shape; successful Adopt consumes r, every error leaves r owned here.
r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(id, r)
if err != nil {
    closeErr := r.Close()
    // Report closeErr too if non-nil; adoption failure is the primary error.
    return nil, reportAdoptionError(err, closeErr)
}
return NewChunkDataset(h), nil
```

Proposed adoption policy: success transfers sole ownership of the new handle to the registry; every error leaves ownership with the opener. `Adopt` rejects a duplicate artifact identity, and the opener closes that redundant handle as the sketch shows. Snapshots normally retain an already registered entry instead of reopening/adopting the artifact. Central constructors check the registry before opening, with duplicate rejection as a safety check. The sketch omits concrete handle types and uses an illustrative error-reporting helper; failed closure must be reported rather than counted as successful cleanup. Go registry operations crossing goroutines need synchronization in addition to JS owner serialization. Use interface assertions for concrete Go implementations and `github.com/pkg/errors` for wrapping new errors.

Shutdown first rejects new work, then cancels operations/jobs and waits for their tasks to settle. It next releases published/retained snapshots and closes the registry. If a worker cannot stop within the deadline, report incomplete shutdown and preserve resources it still uses; a timeout is not proof that access has ceased. Related Go tasks should use `errgroup`.

### Results are detached, later lookups are generation-aware

`rag.run` returns a `snapshotId` and owned plain data for the hits, trace detail, evidence and configured neighbor preview it exposes, including promised document metadata. Copy those records while the lease is held. `result.top()` and proposed `result.chunk(hit)` use those copies, so ordinary search callers never need cleanup. Resolve lookup arguments as hits or complete record references; do not add a `result.chunk(stringId)` fallback that chooses a dataset.

```js
const result = await rag.run(plan, { q: 'ownership' });
const hit = result.hits[0];
const chunk = hit ? result.chunk(hit) : null;
```

Detach the bounded result, not the entire corpus. Do not return references whose promised record bodies were silently dropped to meet a limit. Oversized results need a clear limit error or an explicitly designed pagination contract. The proposed custom-output boundary accepts primitives, arrays/plain objects and declared record references, with runtime validation and copying. It rejects functions, datasets, native handles and unrecognized object instances. Collect the declared references needed by returned outputs and trace/evidence detail, then copy their promised bodies and metadata before release. Exporting raw executor objects would defeat this rule. If copying or validation fails, fail packaging and still release the lease in `finally`. Evaluations that traverse all labels and whole-corpus exports use internal scoped reads rather than expanding ordinary result budgets.

A later HTTP document/chunk lookup sends both a snapshot ID and a record reference. If that generation is retained, acquire a lease and resolve it. Otherwise return a deliberate stale-generation response. Never resolve an old hit against today's data. Browser caches key by generation/reference and clear on generation change. Retention count/duration and the exact stale response remain policy decisions; neither controls the safety of active leases. Historical retention can preserve excluded private notes, and refreshing cannot revoke text already delivered. Access control remains separate.

## Durable ownership survives process restart

In-memory reader counts cannot protect files from a separate collector. The initial deployment therefore allows one process to own a writable store namespace for its lifetime, using an OS-backed exclusive lock. It owns builds, publication and online GC. A second process must fail clearly or use another namespace. Offline `store gc` obtains that same lock; it cannot run destructively beside the server. Browser memory stores implement the logical contract without filesystem locks. Shared independent writers or distributed leases are outside this design.

Retain the immutable payload formats and codecs where useful, but extend artifact descriptors to list owned payloads and artifact dependencies. A snapshot manifest lists its artifact roots. An atomic current pointer names a complete manifest for each publication slot; the manifest records the exact build key. Descriptors and manifests are ownership authority; lineage is only a bounded incremental-base search aid that can be rebuilt or discarded. No separate inventory database is necessary.

Illustrative proposed metadata:

```text
artifact chunks-v7: payloads [owned records directory], dependencies [docs-v4]
artifact text-index-v2: payloads [owned index directory], dependencies [chunks-v7]
snapshot S12: roots [text-index-v2, chunks-v7, docs-v4]
current[reports-slot]: S12  // manifest also records its exact build key
```

Real identifiers must use the version-key scheme, not these labels. The example shows why GC follows dependencies transitively: a root index can keep its records alive even when a lineage no longer lists them. Pinning a resource includes its lazily opened payload shards, not only file descriptors already open.

Publication proceeds under store ownership:

1. Write payloads to an owned staging directory.
2. Close writers and verify counts, checksums and codec metadata.
3. Move complete immutable payloads into their namespace locations.
4. Atomically publish their descriptors.
5. Publish the complete immutable snapshot manifest.
6. Atomically replace the current pointer.
7. Update derived lineage information.

A crash before pointer replacement leaves the old current snapshot; after replacement, the new one must be fully readable even if lineage is stale. Appropriate file and directory synchronization is required for the supported filesystem/platform durability promise. Atomic rename alone does not demonstrate power-loss safety. Specify recovery for staging and unpublished complete artifacts, and inject failures between steps before enabling destructive GC.

GC marks every current pointer in the namespace, including plans not currently loaded, plus policy-retained generations, active query and builder leases, and temporary build ownership. Removing an obsolete slot pointer is an explicit retention action, not a side effect of the plan being absent from one runtime. It follows descriptor dependencies, then sweeps unmarked owned artifacts. Serialize the publication/mark decision so a durable candidate cannot be swept before publication. If authoritative metadata is missing or corrupt, abort destructive collection and report an actionable error. Do not infer deletion permission from an unfamiliar filename. Traverse namespace layout explicitly, which also fixes dotted roots in `Store.List`.

Hard-linked payloads remain owned by their containing artifact directories: removing one artifact's links must leave the other's links intact. Report logical artifact bytes and actually reclaimed bytes separately where possible. A 40-plus-version regression with retention set to three must collect the exact unreachable set even though lineage search remains bounded. Retention is deliberate policy, not a substitute for leases or store locking.

## Operations have one outcome and explicit interested callers

### Native asynchronous work

Sample, decide and rerank currently repeat request-context creation, abort listeners, worker execution, owner posting and promise settlement. Extract that common lifecycle while leaving provider request conversion, validation, tool semantics and protocol-specific errors in their providers.

An operation has one immutable terminal outcome: success, caller cancellation, deadline, provider failure or runtime shutdown. Capture that outcome before cleanup cancels derived contexts. One synchronized per-operation state in Go accepts competing worker-completion, caller-cancellation, deadline and shutdown transitions once: if it accepts a terminal result before cancellation, that result remains; if cancellation is accepted first, it wins. The owner callback delivers this fixed value; it has no authority to reclassify it. Do not later reconstruct the outcome from `ctx.Err()` in the VM callback.

```ts
// Proposed discriminated union; payload/error details belong to each provider.
type Outcome<T> =
  | { kind: 'success'; value: T }
  | { kind: 'cancelled'; reason: unknown }
  | { kind: 'deadline'; error: OperationError }
  | { kind: 'failed'; error: OperationError }
  | { kind: 'shutdown'; error: OperationError };
```

The shared runner installs cancellation/listener tracking before starting its owned task, executes the provider adapter, submits completion to the terminal arbiter, and posts the accepted outcome to the owner. The winning transition schedules listener detachment and settlement on the VM owner, independently of worker exit; a losing completion only finishes task cleanup. Worker cleanup releases task resources and cancels derived contexts only after it can no longer alter classification. Cancellation can settle the caller before the worker stops, so task tracking continues until cleanup actually completes.

The worker passes immutable Go values or bytes across goroutines. All Goja access, event delivery and JS promise settlement happen on the runtime owner. Streaming tools need a supported request/reply hook: post invocation to the owner and return its reply or cancellation to the worker. They cannot be reduced to fire-and-forget progress messages. If runtime closure prevents listener detachment or promise callbacks, discard VM-facing registrations with the runtime while still completing Go task/resource cleanup; a posted callback is not proof of delivery.

The concrete regression is `pkg/natives/rerank.go:rerankAsync`: a provider error must remain `rerank_failed` after deferred cleanup runs. `pkg/ragjs/sample.go:newSample` and `pkg/ragjs/decide.go:newDecide` already classify before posting in important paths; preserve their provider details while centralizing lifecycle. Test cancellation before work, completion races, deadlines, owner closure and exactly one settlement.

### Shared build jobs

A shared build job executes one semantic build for several callers. A waiter is one caller's interest in its result. The job has an engine-owned cancellation context and its own progress stream, rather than borrowing its first waiter's request context.

```text
A waits ----+
            +--> job J --> candidate --> validated publication
B waits ----+

A cancels: detach A; J continues for B.
B cancels: cancel J unless a background-refresh owner remains.
new C during cancellation: wait for J to settle, then start/retry; do not join J.
```

Jobs use the exact build key, including normalized build inputs and every option affecting semantics. They are distinct from the stable publication slot. Per-waiter detail/event filters do not alter the build and should remain subscription options. Add an explicit background owner for refresh; otherwise the final waiter leaving cancels the job. Engine shutdown cancels jobs and joins their cleanup.

Progress events identify both job and request/operation as appropriate. Fanout must be bounded; a slow or closed subscription detaches that request without blocking the build or other waiters. Never permanently attach job events to the first request's scope. Keep this as a small registry for actual build sharing, not a durable scheduler.

### One HTTP transport lifecycle

Both direct query and verb endpoints should use the same admission scope and response helper. Parsing parameters and performing the domain operation stay specialized; JSON/SSE framing, event subscription, cancellation and terminal handling are shared. Today admission control applies to verbs, and direct run duplicates transport orchestration without that same cap.

For SSE, one writer goroutine owns headers, heartbeats, event frames, text coalescing and the terminal frame. Use `http.ResponseController.SetWriteDeadline` before each potentially blocking write or flush, including initial response work, and its error-returning `Flush`. HTTP wrappers must expose the controller's supported unwrapping path. If write deadlines are unsupported, fail setup clearly; do not stream without the required bound. After an exceeded write deadline, end the stream instead of assuming the deadline can be revived.

Keep queue capacity and add byte limits for event bodies and coalesced text. A bounded number of unbounded strings is not bounded memory. Each write has its own deadline, allowing a long healthy stream. Write/flush error, queue overflow or request cancellation stops the transport and cancels/detaches this request's operation/subscription. It must not cancel another waiter's job.

The helper requests cancellation/detachment and joins the response pump and owned operation through configured bounds. Cancellation is a notification, not proof that the worker finished. If joining times out, report that outcome explicitly. The HTTP handler can release its HTTP admission slot when its transport lifecycle ends, but the operation tracker must retain the unfinished task and its resources. If the host separately limits executing workers, that capacity remains occupied until the task actually exits; returning HTTP capacity must not pretend to recover worker capacity. Finalize the timeout/exhaustion policy before exposing this behavior, and test it with a deliberately uncooperative fake. Do not close a resource underneath a worker or claim successful joining. The transport attempts at most one terminal event; a failed connection cannot be guaranteed to receive it. JSON and SSE should report the same terminal error vocabulary.

A real non-reading socket is mandatory verification. A fake writer or full queue cannot show that a blocked network write terminates, the handler returns and request capacity recovers. Also cover initial/heartbeat/terminal failures and text byte bounds.

## Graph transformations describe all references once

A structural reference connects one graph entity to another: a data input, index/artifact dependency, signal dependency, decision gate or pinned output. Compilation currently discovers these in separate places, while editing rewrites a smaller subset. The common mechanism must enumerate them all for validation and rewriting; transformation policy remains with the caller.

For example, the reports adaptive plan contains the classification node `route`, which answers a named question `needs`: does this query require corpus lookup? Retrieval runs when that answer meets the configured minimum probability, here 0.5. `route.needs` names the node and its question output; renaming the node changes `route`, not `needs`. The public DSL can retain that spelling. Normalize it once into a typed IR reference:

```js
// Public DSL
when: { decision: 'route.needs', min: 0.5 }
// Proposed normalized IR
when: {
  source: { kind: 'decision', node: 'route', output: 'needs' },
  minimum: 0.5
}
```

Component schemas declare their structural dependencies. `references(node)` enumerates them and `rewriteReferences(node, mapping)` changes them. The compiler uses the same representation to check existence, output types, build/live phases and cycles, including pinned outputs at the graph level. Renaming `route` to `routing` checks target uniqueness, rewrites every declared use and validates once at the end of the transaction. A failed transformation leaves the original immutable graph available.

Deletion needs a declared policy: reject dependents, cascade, or replace a reference. Proposed ordinary default is reject; a caller must explicitly request another behavior. Lexical export has a different, explicit projection policy. It removes model-dependent classifiers and makes the retrieval gates they controlled unconditional, then validates the complete transformed graph before serializing. It must not validate an intermediate graph with dangling gates and stop before rewriting them.

Capabilities express what a component needs, such as a local text index, external model or host filesystem. Export uses them to select supported browser computation, rather than treating every submit-triggered node as unavailable. Export must bundle a normalized portable dataset; server-side sources are not necessarily in `engine.files()`. Unsupported capabilities need a clear error or a defined projection, not silent omission. Execute the resulting page's search graph in the browser-style test and verify Goja consumes the same generated runtime.

Arbitrary JavaScript callbacks can hide graph lookups. Regex/string replacement cannot reliably rename them. For graph-aware callbacks, require declared named dependency bindings; rename rewrites those bindings, not callback source. Reject structural transformations needing opaque undeclared references. This intentionally narrows the API without adding a compatibility path.

## Decisions and implementation boundaries

These decisions should guide review. The required directions come from 009; proposed defaults below still need agreement when their phase begins.

| Decision and status | Alternatives, reason and consequence |
|---|---|
| Required: synchronous dataset reads | Keep local reads simple; an async-everywhere interface adds cost without a remote backend requirement. Remote bulk reads can be designed later. |
| Required: detached ordinary results | Caller-owned leases would make every search require cleanup. Detachment preserves simple use but requires budgets and copy-cost measurements. |
| Required: one writable namespace owner | Cross-process leases are substantially broader. Exclusive ownership makes active-reader GC protection enforceable; separate processes use separate namespaces. |
| Required: descriptors/manifests authoritative | Bounded lineage cannot inventory all artifacts; adding a separate database duplicates authority. Derived lineage remains useful for incremental lookup. |
| Required: direct API/format cutover | There is no compatibility obligation. Migrate consumers and invalidate caches together; never silently delete a user's store. |
| Proposed: reject ordinary deletion with dependents | Cascade is surprising; explicit cascade/replacement remains possible. Export uses its own documented projection. |
| Proposed: one active refresh per publication slot, coalesced rerun | Avoid out-of-order publication without a general scheduler. Slot identity is stable across source changes; exact job keys include source versions. |
| Proposed: deterministic fallback by sorted eligible reference when enabled list is empty | Preserve an eligible configured default and explicit enabled-list order; sort only the unrestricted fallback. Go map iteration is not policy. |

Organize implementation around the existing module responsibilities: graph, datasets, engine/snapshots/resources, session/executor and operations. Keep a thin public `rag` facade. Browser and Goja must load the same deterministic generated implementation. Keep runtime imports local and acyclic; the module-graph checks reject cycles and external host-package runtime imports. 010 has already supplied `pkg/ragjs/src`, `pnpm build`, strict TypeScript foundations and Biome; it did not implement these contracts. New datasets, references, snapshots, outcomes and their replaced consumers should be strict TypeScript with declarations next to implementations. Do not add unused types over array/view branches.

Avoid a generic service container, dependency-injection framework, plugin system or durable job scheduler. Share mechanisms only where real consumers need the same contract: memory/disk records; fresh/load/delta resource adoption; sample/decide/rerank lifecycle; query/verb transport; compile/edit/export references; timer/manual/post-sync refresh. Every new abstraction must retire an old responsibility or duplicate path.

## Implement vertical changes and remove the replaced path

Choose one integration branch for the work and verify actual stack status before planning publication. Historical review status is not current branch evidence. This guide authorizes neither merging nor review-thread resolution.

1. **Characterize the twelve findings.** Add discriminating regressions using existing record, delta, lineage, rerank and server fixtures. Record which fail on the baseline. Include equal-count changed inputs for bake cache identity. If the server stack will be used early, prioritize bounded SSE writes in the future shared helper, without a temporary alternate transport.
2. **Unify datasets and provenance.** Implement memory/persistent conformance, bind indexes to datasets, migrate bake/custom callbacks and every hit/evidence consumer. Update reports plans, traces, evaluation, sweeps, GUI, saved plans, inspection and export. Remove `unionChunks`, view tests and consumer array conversions.
3. **Implement snapshot and registry ownership.** Adopt all fresh/load/delta/base handles, build candidates privately, serialize publication, scope queries through packaging and detach results. Update `runner/serve.go`, evaluation and lookups. Remove rebuild-count GC and direct caller handle closure.
4. **Replace durable GC authority.** Add descriptors, manifests, current pointers, namespace locking and crash recovery. Derive lineage, correct dotted-root traversal and test collection with active readers. Do not enable destructive sweeping until ownership metadata and lock enforcement are complete.
5. **Unify operation/job lifecycle.** Move provider bridges to immutable outcomes and owner-safe callbacks, then replace first-waiter build ownership and add bounded fanout. Preserve streaming tool request/reply. Fix deterministic Pi model fallback as a local tested policy change.
6. **Complete shared HTTP execution.** Migrate query and verb handlers to the common admission/response helper, bounding writes, queues, coalesced bytes and joins. Test capacity recovery on real stalled sockets.
7. **Unify graph references and export.** Migrate compiler/editor references, transaction validation, callback bindings and capability projection. Test the classified reports plan's unconditional lexical export.
8. **Finish cleanup and integrated checks.** Bump IR/artifact namespace versions, update fixtures/examples/help and document an explicit old-store cleanup command for offline unowned namespaces. Remove obsolete codecs only after enumerating live consumers; retain useful memory algorithms and parity comparisons.

Each phase should land a coherent replacement with affected callers and tests; do not extract empty abstractions and keep both engines indefinitely. Meaningful checkpoint commits follow validated changes. Documenting a design does not finish an implementation phase.

## How to tell the contracts hold

Tests should verify transitions and user-visible records, not only the existence of new types. Reuse `pkg/runner/records_test.go`, `delta_test.go`, `lineage_test.go`, `pkg/natives/rerank_test.go` and `pkg/server/verbs_test.go` fixtures, adjusting assertions away from obsolete storage markers.

| Area | Required discriminating check |
|---|---|
| Data/caches | Memory, warm persisted and delta datasets give the same ordering/missing/immutable-record behavior; callbacks have one API; equal-sized changed content invalidates bake results. |
| Provenance | Two datasets with one logical section ID retain their own bytes through retrieval, explicit fusion/projection, packing, trace, evidence and GUI. |
| Snapshot/result lifetime | Pause a query in a fake provider, publish two newer snapshots, resume and package old records; verify slot/exact-key distinction and refresh ordering. Returned results remain readable after release; hidden-handle outputs and oversized results fail explicitly. |
| Resource ownership | Count adoption/release/close on fresh/load/delta/base paths, exceptions, duplicate adoption and shutdown; each handle closes exactly once after its final user. |
| Durable store | Build 40+ versions and retain three; collect the exact reachable set. Refuse a second owner/offline collector, preserve hard-linked owners and reject corrupt metadata. Inject crashes between publication steps and recover a complete old or new snapshot. |
| Shared jobs | Cancel A while B waits and B succeeds; cancel all and observe cleanup; keep a background owner; ensure a late waiter avoids a cancelling job; slow subscribers cannot stall peers. |
| Outcomes | Provider failure stays failure after cleanup; accepted cancellation stays cancellation; race arbitration, deadline, owner closure and shutdown settle/clean up once. |
| HTTP | Stalled socket and write/flush failures terminate within configured bounds; HTTP capacity recovers for query and verb. An uncooperative fake remains tracked with resources/execution capacity retained. Final-event races and byte limits do not leak or block. |
| Graph/export | Rename all reference kinds, reject collisions, enforce deletion policy, retain phase/type/cycle checks, reject opaque undeclared transformations; export adaptive plan as functioning unconditional lexical search. |
| Small fixes | Dotted roots list their artifacts; empty enabled-model lists choose the documented deterministic eligible fallback, preserving valid default/list precedence. |

Use barriers with synthetic corpora/provider fakes to control races; arbitrary sleeps do not establish ordering. Counting closers should be supplemented by file descriptor observations where practical. Use real filesystem and socket boundaries for claims those fakes cannot establish.

Run focused tests for each changed contract. After runtime source changes, `pnpm build` regenerates the committed asset and `pnpm check` runs type, Biome, generation and runtime checks. At integration boundaries run `go test ./...` and `go build ./...`, plus required repository lint/generation checks. Goja and browser-style runtime tests must both pass; a bundle syntax check is insufficient. Any long-running interactive server check uses tmux and capture-pane; stop the port owner with `lsof-who -p PORT -k` per repository guidance.

Measure cold build, warm startup, query latency, changed-note refresh, memory, open descriptors and artifact growth on the same fixture before/after. Report fixture sizes and conditions. Detached records and explicit provenance may change allocation cost; passing tests does not establish performance parity.

Before dataset/publication implementation, settle canonical source/version/slot key encoding and the callback-binding spelling. The role of each identity and explicit dataset selection must remain as described above. Before result/GUI cutover, settle detachment budgets, generation retention and stale-response details. Before HTTP cutover, settle bounded-join timeout reporting and execution-capacity exhaustion policy. Before destructive GC, settle supported lock/durability behavior, recovery schema and corruption handling. These choices block the affected phase, while concrete internal class names and helper signatures can be chosen during implementation.

Completion requires a passing regression or reviewed disposition for all twelve findings; removal of replaced unions, storage-shape branches, delayed-close GC, unmanaged openers, first-waiter jobs and duplicated response pumps; consistent behavior across browser memory and persistent Goja examples; documentation matching the new API; and verified publication/cancellation/shutdown sequences. Distributed indexing, remote datasets, independent processes sharing a store, full Obsidian rendering and production authentication remain outside this ticket.
