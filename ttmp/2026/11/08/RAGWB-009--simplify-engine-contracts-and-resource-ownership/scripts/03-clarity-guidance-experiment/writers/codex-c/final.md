# Consistent search data and explicit engine ownership

## What this change delivers

rag-workbench lets an application describe how to turn documents into searchable passages and how to answer a query using those passages. Its corpus builder reads sources, parses documents, splits them into chunks and creates indexes. Its query-plan builder retrieves and combines ranked hits, filters or reranks them, packs passages as evidence and optionally asks a model for an answer. The same JavaScript runtime runs in a browser and in Goja, the JavaScript interpreter embedded in the Go application. Native Go implementations supply disk-backed records and indexes.

RAGWB-009 proposes a simpler contract between these parts. Every document/chunk collection becomes an immutable dataset. Every hit identifies the dataset version that supplied its text. Every query retains one complete published build generation until its events and result have been packaged. Native resources have a single owner and explicit users. Provider operations and HTTP streams share lifecycle code where their semantics match. Graph transformations use one representation of dependencies.

These changes address failures that arise when one part has to infer another part's storage representation or lifetime. A search should not change text after a warm restart, a refresh should not close a reader an awaiting query still needs, and cancelling one request should not cancel work needed by another.

This is a proposed implementation guide, not a report of completed fixes. Source inspection uses the runtime baseline identified by the context as `c267f91`, after RAGWB-010 extracted the runtime. New contracts below are sketches. No tests were run for this guide. Compatibility may break: update callers and disposable stored formats together, without adapters or parallel legacy paths.

Keep the concise builders, browser/Goja execution model, Markdown parsing, section chunking, Bleve and terms indexes, incremental algorithms, provider-specific errors and meaningful evaluations. Replace the contracts around them. Distributed stores, remote datasets, production authentication and full Obsidian rendering are outside this change.

## Follow one search through today's code

The reports application supplies a useful ordinary example. In `examples/apps/reports/lib/corpus.js`, a directory source reads Markdown, the parser produces documents, section chunking produces passages, and text/metadata indexes serve those passages. The plans in `lib/plans.js` reuse that corpus. The lexical plan retrieves text hits; the fused plan combines text and metadata rankings. Other plans call decision models, rerankers or answer providers.

The fluent builders produce an intermediate representation, or IR: a graph whose nodes carry component types, parameters and dependencies. `pkg/ragjs/src/graph/graph.js::finalize` validates references and ports and orders execution. Build nodes run when source inputs change; live nodes run for query signals such as `q` and `k`.

```text
corpus + query builders → validated graph
                              |
                         Engine.build
                              |
                     memo / persistent outputs
                              |
                         Session.run
                              |
                   hits → evidence → answer
                              |
                   runner packing → JSON/SSE → GUI
```

An artifact is a saved build output addressed by a key. The engine can defer loading it through `Lazy` in `engine/records.js`. In Goja, persisted records may be a disk-backed view rather than an array. A lineage identifies successive versions of the same build step; incremental rebuilding searches that history for a usable base and applies a delta, the added/changed/removed records.

`engine/session.js::Session.run` obtains artifacts, waits for `Engine.buildShared` if necessary, then awaits live components. Its result methods close over those artifacts. The served application adds another step: embedded `__serve.run` in `pkg/runner/serve.go` asks for current artifacts again after execution to pack chunk bodies and neighbors. A correct query lease therefore has to include packaging, not only execution.

For a first local exercise, use the offline reports command documented in `examples/apps/reports/README.md`:

```sh
go run ./cmd/ragwb --decide fake --no-store verbs --dir examples/apps/reports reports challenge --names lexical,fused,graded,filtered --threshold 50
```

The fake provider checks wiring; it does not establish model quality. Trace the lexical plan before changing ownership code, then the adaptive plan to see decision references and the answer plan to see awaits.

## Problems the new boundaries must prevent

The original design records twelve review findings. The following source mechanisms support their concerns; the individual failures still need reproducible regressions before implementation claims closure.

| Reported finding | Current source and required correction |
|---|---|
| #2 P2: stored duplicate chunks differ from cold arrays | `Engine.artifacts` separates arrays and views; `records.js::unionChunks` uses different precedence. Keep dataset provenance rather than a global union. |
| #2 P2: bake receives a view instead of an array | `Engine.resolveBakes` passes raw stored/computed outputs. Give callbacks one dataset API. |
| #2 P2: dotted store root disappears from listing | `artifacts/store.go::List` skips dotted directories, including the root. Traverse the defined layout. |
| #3 P2: two refreshes close an awaiting query's resources | `Engine.gc` closes retired handles one later GC cycle. Retain resources by actual use. |
| #3 P2: versions beyond 32 become uncollectable | `LineageRecord` truncates history and store GC scans only that history. Make ownership inventory independent. |
| #3 P2: delta readers miss runtime cleanup | `natives/markdown_native.go` wraps delta readers separately from persistent loading. Require one adoption path. |
| #4 P1: a non-reading SSE client holds capacity | `server/sse.go` bounds queue waiting but ignores pump write errors and has no bounded network writes. Bound transport and join cleanup. |
| #4 P2: first waiter cancellation stops another's build | `Engine.buildShared` shares the first request's options and promise. Separate waiter and job lifetime. |
| #5 P2: cleanup turns failure into cancellation | `natives/rerank.go::rerankAsync` checks context state in posted settlement after deferred cancellation. Fix outcome before cleanup. |
| #6 P2: empty enabled models prevents fallback | `llm/gepp/backend.go::preferred` permits all in its predicate but iterates only the empty list for fallback. Select deterministically from eligible models. |
| #7 P2: lexical export retains removed decision references | `export/html.js` prunes by triggers/output kinds. Project capabilities and rewrite dependencies together. |
| #7 P2: rename misses decision references | `graph.js::applyOp` rewrites inputs/outputs only. Traverse all structural references. |

Do not describe current graph validation as absent: `finalize` already checks decision existence/type and rejects dangling references. The transformation can fail validation because its rewrite is incomplete. Dotted-root listing and model fallback remain small local fixes.

## A dataset identifies the text, not its storage

A dataset is a read-only version of a collection of documents or chunks. Its version identity includes the full input identities, normalized configuration and component/codec versions. Changing any of those invalidates dependent outputs; index version identity includes the exact dataset identity it indexes. A logical record ID can stay stable across versions; it cannot by itself identify the bytes that a query used.

Consider a passage `reports/engine.md#ownership`. One dataset contains its original text; another contains a prefixed enrichment of that passage. An index built over the enriched dataset must resolve to enriched text even if the original dataset is also present. Today the global union can select by incidental storage precedence. The proposed records make that distinction visible:

```js
// Illustrative proposed data, abbreviated to the fields relevant here.
const original = { dataset: 'chunks-v1', id: 'reports/engine.md#ownership' };
const enriched = { dataset: 'chunks-v2', id: 'reports/engine.md#ownership' };
const hit = { ref: enriched, score: 0.82 };
// snapshot.resolve(original).text → 'Retain readers until the query finishes.'
// snapshot.resolve(hit.ref).text  → 'Engine ownership: Retain readers until the query finishes.'
```

The hit carries the version that supplied its text. Its logical ID alone would lose that information. The core contract is:

```ts
// Proposed core shapes; native/JSON inputs also require runtime validation.
type RecordRef = Readonly<{ dataset: string; id: string }>;
type Hit = Readonly<{ ref: RecordRef; score: number; reason?: string }>;

interface Dataset<T> {
  readonly id: string;
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): T | null;
  values(): Iterable<T>;
  materialize(): readonly T[];
}
interface ChunkDataset<T> extends Dataset<T> {
  document(id: string): RecordRef | null;
  order(documentId: string): readonly RecordRef[];
}
interface Index {
  readonly datasetId: string;
  search(query: string, options: unknown): readonly Hit[];
}
```

Keep local access synchronous. Memory and persisted records share these semantics even when their algorithms and scores differ. `values` and `materialize` have deterministic order; missing `get` returns null. Records exposed to callbacks cannot mutate authoritative data, including nested metadata. Choose deep freezing or detached copies and test the chosen rule. The shallow `Readonly` types in this sketch do not establish deep immutability; the runtime boundary must enforce it. Native handles, codecs, `__records`, `__ix` and close methods remain private.

`document` returns the source-document reference for an existing chunk, or null if that chunk is absent; `order` returns an empty list for an absent document. An existing chunk must have a valid source-document reference. Snapshot resolution rejects unknown datasets and returns null for an absent record; index/result validation rejects hits that cannot resolve. These missing-value rules are proposed defaults to make the sketch concrete.

`document` points from a chunk to its source document dataset. Document logical identity includes source identity and relative path, since titles and shortened slugs are not globally unique. A snapshot resolves references through the datasets it contains. Dataset identity and snapshot identity differ: a later snapshot may reuse an unchanged dataset while replacing a different index.

Fusion deduplicates exact references by default. Text and metadata indexes over the same chunk dataset can combine naturally. Rankings over different enrichment datasets stay distinct unless a component explicitly projects references to a canonical dataset or specifies conflict rules. There is no global “last stream wins” policy. Preserve references through filters, ranking, packing, citations, traces, evaluations and GUI display.

Bake callbacks, which compute configuration from built data, receive datasets as well:

```js
// Proposed callback API: full traversal is explicit.
rag.bake(ctx => ctx.chunks.materialize().map(c => c.id));
rag.bake(ctx => ctx.chunks.size);
```

The current bake cache key uses document IDs/section lengths and chunk count. Equal-sized changed content can collide. Replace it with full input dataset identities, callback source/configuration and component version. Custom-component contexts must name the dataset dependencies they need rather than infer the first global chunk output. Update callbacks and tests in the same cutover.

## A snapshot keeps a query on one generation

A snapshot maps validated build outputs to immutable datasets and indexes. Publish it for a normalized build identity so query plans sharing the same corpus build can reuse it. The proposed identity split is:

| Identity | Purpose and example |
|---|---|
| Build identity | Selects a compatible family of build outputs: the reports source/configuration/component definitions. It stays stable across source-content refreshes. |
| Build job key | Identifies exact work to share: build identity plus source/input versions and every option that changes build semantics. |
| Snapshot ID | Identifies one complete published output mapping, such as `reports-s7`. |
| Dataset ID | Identifies one immutable document/chunk collection reused by any compatible snapshots, such as `chunks-v2`. |
| Artifact key | Addresses a stored payload/descriptor, such as a particular native index or record collection. |

This split refines the original design; exact normalization and encodings must be specified with the identity implementation. It lets `reports-s7` stay usable while a new job builds `reports-s8`, without treating an incompatible corpus configuration as a valid fallback. A lease is an internal retention of that snapshot while a query or scoped inspection uses it. The resource registry owns native readers/indexes, while snapshots and builders retain entries in that registry. Multiple snapshots may share the same unchanged artifact. For example, a manifest-shaped record might say:

```js
// Illustrative metadata, not an implemented wire format.
{
  id: 'reports-s7', build: 'reports-build',
  datasets: { docs: 'docs-v3', chunks: 'chunks-v2' },
  indexes: { text: { artifact: 'bm25-v4', dataset: 'chunks-v2' } }
}
```

`chunks-v2` supplies text; `bm25-v4` supplies ranking over that collection; `reports-s7` groups them into one usable generation. These names describe identity relationships, not a requirement to duplicate native payloads in the manifest.

A refresh builds a private candidate. Only after its outputs validate does it publish the candidate as current. Queries continue using the last good snapshot during background refresh. If no usable snapshot exists, a query waits for a build; if refresh fails or is cancelled, the previous snapshot remains current. Partial build outputs may support resumption but cannot become the current query snapshot.

```text
A current; query Q retains A
refresh builds B privately → validates → publishes B
refresh builds C privately → validates → publishes C
Q resumes its provider await, reads and packages A
Q releases A → close only resources with no remaining users
```

Selecting current and retaining it must be one serialized action on the JS runtime owner. Go-side registry access across goroutines needs synchronization too. Publication retires an old snapshot; it does not revoke outstanding leases.

```js
// Proposed internal flow; no application-facing release call.
async function query(plan, signals, request) {
  await builds.ensureUsable(plan, request.signal);
  return snapshots.withCurrent(plan.buildIdentity, async snapshot => {
    const execution = await executor.run(plan, signals, snapshot, request);
    return detachResult(execution, snapshot, request.detail);
  });
}
```

The scope releases on success, cancellation, execution error and packaging error. Events that include record detail must resolve it inside this scope. The runner must consume the detached result rather than call current `artifacts()` afterward.

A returned result owns copies of records needed for its emitted hits, trace detail, evidence and configured neighbor previews. `result.top()` and `result.chunk(hit)` operate after the lease is released. This copies bounded output, not the corpus. Declare count/byte budgets; exceeding one produces an explicit error or an explicit pagination contract, never hits whose required text was silently omitted. Evaluations and whole-corpus export use scoped snapshot reads instead of public result materialization.

```js
// Proposed retained convenience.
const result = await rag.run(plan, { q: 'ownership' });
const first = result.hits[0];
const chunk = first ? result.chunk(first) : null;
```

Additional browser lookups carry snapshot ID plus record reference. The server either resolves the retained generation or returns a stale-generation response. It must never silently look up an old hit in current data. Browser caches key by generation/reference and clear when generation changes. Retention is not authorization, and refreshing cannot revoke text already delivered.

### Open every native resource through its owner

Persistent loading, delta output creation and fresh builds all adopt their resources into one registry. Each private build scope retains its adopted entries. Publication first acquires all the new snapshot's entries, including unchanged resources reused from the previous snapshot, and only then releases the private scope and the former current-snapshot retention. On failure before publication, the private scope releases its entries and the previous current snapshot stays published. A retired snapshot remains retained while a query leases it.

If A and B both reference an unchanged index, publishing B acquires B's retention before releasing A's current retention. Releasing A's final query lease later cannot close that shared index while B still needs it. Abort or later-stage failure releases the whole private scope. `Persist.InstallEnv` must reject a resource-bearing setup without a registry instead of silently using a no-op closer.

```go
// Proposed ownership shape; error handling abbreviated.
r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(id, r)
if err != nil { _ = r.Close(); return nil, err }
return NewDataset(h), nil
```

The sketch uses this proposed failure rule: on adoption failure the caller still owns and closes the newly opened handle. On success the registry owns it. As a recommended duplicate-ID policy, reuse an existing compatible registry entry and close the redundant newly opened handle inside successful adoption. Reject mismatched identity/type metadata rather than reuse it. The caller must never close a handle after successful adoption. Implement and test this policy before routing native constructors through it; do not silently register duplicate owners for one underlying handle. Resources close after the final snapshot/builder/user releases them, exactly once.

Shutdown rejects new work, cancels active operations, joins workers, releases snapshots and then closes the registry. Report shutdown timeout explicitly; closing handles while workers still access them is not successful cleanup.

## Durable storage must remember ownership beyond lineage

A native handle protects open access in this process. Persistent metadata must separately describe which files belong to which artifacts, including readers that lazily open shards. A history limit such as 32 versions cannot establish this inventory.

Retain existing immutable payload formats and useful codecs. Extend artifact descriptors to list owned payloads and artifact dependencies. Introduce immutable snapshot manifests listing root artifacts and one atomically replaced current pointer per build identity. These descriptors/manifests are authoritative. Lineage remains a bounded, rebuildable index for finding incremental bases; losing it can cost rebuild work but cannot hide files from GC.

Use one writable process owner per namespace. It holds an OS-backed exclusive lock for its lifetime, so it can coordinate publication, active leases and online garbage collection. A second process fails clearly or chooses a different namespace. Standalone `store gc` takes the same lock and therefore runs only while that namespace is offline. Browser memory storage implements the logical contract without filesystem locking. Multi-process shared stores require a separate design.

Publication proceeds in this order:

1. Write payloads into an owned staging directory.
2. Close writers and validate checksums/counts/codec metadata.
3. Move complete immutable payloads into their final locations.
4. Publish artifact descriptors.
5. Publish a complete immutable snapshot manifest.
6. Atomically replace the current pointer.
7. Update derived lineage information.

A crash before step 6 leaves the old snapshot current; after it, the new snapshot must be complete even if lineage is stale. Atomic rename alone does not establish power-loss durability. Specify file and directory synchronization for supported filesystems and test the boundary.

GC marks current and configured retained snapshots, active query/builder leases and private build ownership. It follows descriptor dependencies, then sweeps only unmarked owned artifacts. Abandoned staging directories are collected through recorded ownership and absence of active build interest; age alone cannot establish that a build stopped using them. The namespace owner serializes publication with collection so a durable but unpublished candidate is protected. Missing/corrupt ownership metadata stops destructive collection with a useful error; unknown files are never presumed disposable. Directory ownership preserves other artifacts' hard links. Report logical size separately from actually reclaimed disk bytes where possible.

Historical retention policy is explicit and independent of query duration; retaining removed or excluded notes has privacy consequences. Fix dotted-root listing by following the defined layout rather than basename punctuation. Old cache namespaces are disposable, but cleanup must be an explicit command after no process owns them, not silent deletion of user directories.

## Operations have one outcome; callers have separate interest

Provider sample, decision and rerank calls all bridge Goja to Go work. Their shared lifecycle includes context propagation, abort listeners, worker ownership, posted events, settlement and cleanup. Keep provider request conversion, tools, validation and protocol-specific errors in their own implementations. In particular, a model tool callback requires a supported request/reply hook onto the JS owner, not direct worker access to Goja values.

Use a strict TypeScript discriminated outcome and corresponding immutable Go value with these cases: success, caller cancellation, deadline, provider failure and runtime shutdown. The operation runner arbitrates competing terminal transitions. A recommended implementation uses a synchronized first-terminal transition on the Go side, so shutdown cleanup does not depend on an available VM owner. Worker result reporting and external cancellation call that same transition; internal context cleanup cannot call it as caller cancellation.

```text
worker value/error --------+
caller cancel/deadline ----+--> accept one outcome --> post to VM owner
runtime shutdown ---------+             |
                                        +--> cancel remaining work if needed
                                             → join worker → release resources
```

If the runner accepts a result before cancellation, that outcome stays terminal; if it accepts cancellation first, cancellation wins. Worker reporting classifies its value/error before deferred cancellation and offers that immutable outcome to the arbiter. The posted callback uses the accepted outcome rather than reread mutable context state. Detach abort listeners when terminal state is accepted, and release worker-held resources when work actually ends. Acceptance, JS promise settlement and worker exit are separate events.

If the runtime closes before settlement can be posted, cleanup still runs even though the JS promise cannot be observed. Only immutable Go values or bytes cross goroutines; Goja values are accessed on its owner. Use errgroup for related goroutines. The exact transition synchronization and join timeout need implementation review and race tests; the diagram does not establish them.

A shared build is different from a request. `buildShared` becomes a small job registry keyed by the exact build job key defined above: compatible build identity, source/input versions and semantic options. The job owns its cancellation context linked to engine shutdown. Requests join as independently cancellable waiters; an explicit background refresh can also own interest.

```text
A joins job J; B joins J
A cancels → detach A; J continues for B
B cancels → cancel J if no background owner remains
C arrives while J cancels → wait for termination, then retry/start
```

Build progress belongs to J and fans out to subscriber queues with job/operation IDs. A slow or closed subscription cannot block J or cancel other waiters. Engine shutdown cancels jobs and waiters and joins cleanup. Do not turn this registry into a durable scheduler.

The separate model-selection fix belongs in `pkg/llm/gepp/backend.go::preferred`: keep an eligible configured default when available, otherwise deterministically choose an eligible enabled model, treating an empty enabled list consistently as unrestricted. Define the stable fallback ordering and preserve clear failure when no eligible model exists.

## HTTP transport must finish even when a client stops reading

Server-Sent Events (SSE) frame operation events for streaming HTTP clients. Today `streamSink` already bounds waiting to enqueue an event. That does not bound a pump stuck in a socket write or flush; its ignored errors also prevent reliable termination. Coalescing text can grow in bytes even with a fixed event count.

Have direct queries and verbs use one response helper with matching admission-control scope and terminal-error vocabulary. Domain execution emits typed events and returns its outcome; this helper selects JSON/SSE framing. For SSE, one goroutine owns every response write/flush, including initial response, heartbeat and terminal event.

Set a bounded write deadline before each write/flush using `http.ResponseController.SetWriteDeadline`; use its error-returning `Flush`. Wrappers must expose the controller's unwrapping path. Reject unsupported deadline setup rather than stream without a bound. Once a write deadline has expired, terminate the transport; do not rely on extending that deadline to recover it. Per-write bounds allow long healthy streams without a short whole-operation timeout.

On write/flush failure or queue/byte-budget overflow, stop the pump, cancel/detach this request and perform a bounded join before releasing its admission slot. Cancellation requests worker exit; it does not prove exit. If a join times out, report that state, end the transport wait, and keep any remaining worker tracked by the operation owner with its resources retained. Do not close those resources underneath the worker or report cleanup complete. At engine shutdown, reject new work and report unjoined tasks explicitly. Review admission behavior for timed-out tasks so releasing HTTP capacity does not allow unbounded background accumulation. If other requests own a shared job, it continues. Permit at most one terminal event attempt; a failed network cannot guarantee delivery. Bound event bytes and pending/coalesced text as well as queue length. Define join timeout behavior explicitly; resource safety remains the operation owner's responsibility.

The required acceptance test uses a real client socket that stops reading and proves a subsequent request can acquire capacity. A fake ResponseWriter can check error paths but cannot prove a kernel socket write deadline works.

## Graph editing and export must transform every dependency

A graph reference says which node/output another node depends on. Data inputs, artifact dependencies, decisions, signals and pinned outputs all affect correctness. They must use the same enumeration and rewrite API, even if public syntax remains concise.

```js
// Public syntax may remain:
when: { decision: 'route.needs', min: 0.5 }
// Proposed normalized representation:
when: {
  source: { kind: 'decision', node: 'route', output: 'needs' },
  minimum: 0.5
}
```

`references(node)` enumerates structural references and their kinds. `rewriteReferences` applies a mapping; graph-level output roots participate as well. Compilation uses that enumeration to check existence, types, phases and cycles. Rename checks target uniqueness, rewrites all references and validates at the end of the edit transaction. Deletion requires a declared reject/cascade/replacement policy. Never guess the meaning of removing a routing decision.

Lexical export is an explicit projection to the capabilities available in an offline browser, not deletion based on a node's submit trigger. A capability declares requirements such as local text indexing, external models or host filesystem access. The exporter selects supported components and transforms dependencies before final validation. For the adaptive reports plan, its documented lexical projection omits the decision classifier and makes retained retrieval unconditional. An ordinary production deletion does not inherit that special policy.

Server directory sources cannot be exported by assuming `engine.files()` contains them. Package a normalized portable dataset from a scoped snapshot read, then load it with the same runtime implementation. Validate the projected graph and execute its exported search page.

Arbitrary callback source cannot be renamed safely by string replacement. Graph-aware callbacks declare named dependency bindings; rename updates the binding reference, not source text. Reject transformations that require changing undeclared opaque references. Keep existing lambda validation safeguards rather than implying static TypeScript can inspect arbitrary runtime JavaScript.

## Implement contracts where their behavior lives

RAGWB-010 is already the tooling foundation: authored sources live under `pkg/ragjs/src`, and `pnpm build` creates committed `pkg/ragjs/assets/rag.js`. Do not edit that asset by hand or create separate browser/Goja implementations. Strict TypeScript currently covers core errors/identity/text and request scopes; extracted JavaScript elsewhere is not a complete typed engine. In the source map below, `engine/`, `components/`, `graph/` and `export/` are relative to `pkg/ragjs/src/`; Go paths are relative to `pkg/`.

Implement each new 009 contract in strict TypeScript with its owning implementation and migrate replaced consumers. Use discriminated unions for outcomes and explicit reference types. Avoid unused declarations over old array/view forks. TypeScript checks static contracts; Biome formats/lints; runtime validation and lifecycle tests establish different properties.

| Work | Start here and migrate adjacent consumers |
|---|---|
| Datasets/cache inputs | `engine/records.js`, `engine/engine.js`, `components/`, native record wrappers |
| Snapshot/query results | `engine/session.js`, `engine/engine.js`, `runner/serve.go`, `evaluation/evals.js` |
| Resource/store ownership | `natives/persist.go`, `natives/markdown_native.go`, `artifacts/lineage.go`, `artifacts/store.go` |
| Operations/request scope | `ragjs/sample.go`, `ragjs/decide.go`, `natives/rerank.go`, `engine/scope.ts` |
| Shared HTTP transport | `server/sse.go`, `server/rag.go`, `server/verbs.go` |
| References/projection | `graph/graph.js`, `export/html.js`, API builders and graph-aware callbacks |

Keep imports local and acyclic, compose at `api/index.js`, and test deterministic assembly. A module boundary should retire a duplicated responsibility: collections, resource opening, async lifecycle, transport or reference traversal. Do not add generic dependency injection, component-specific executor branches or a workflow framework.

## Decisions and the work they authorize

| Choice | Status, alternatives and consequence |
|---|---|
| One dataset API with synchronous local reads | Required by this design instead of array/view forks or speculative asynchronous getters. Preserve backend algorithms, migrate all consumers, expose full traversal explicitly. |
| Snapshot leases plus detached bounded results | Required instead of elapsed-refresh cleanup or application-facing close calls. Packing must happen before release; output allocation and limits become measurable costs. |
| One writable namespace owner | Required initial deployment model instead of cross-process leases. A second writer/collector is refused; another namespace is the supported alternative. |
| Stable build family plus exact job versions | Recommended refinement instead of a single digest serving both purposes. Specify normalization before implementing refresh selection and job sharing. |
| Duplicate resources reuse compatible entries | Recommended policy instead of rejection of every duplicate open. Success transfers ownership; failure leaves it with the caller. Validate identity/type and close redundant opens. |
| First-terminal operation arbitration | Required race rule with recommended Go-side synchronization instead of deferred context inspection. Provider semantics remain specialized; worker joining remains independent of settlement. |
| One reference visitor and explicit capability projection | Required instead of ad hoc rename/export pruning. Unsupported opaque callbacks are rejected rather than rewritten speculatively. |

Identity normalization, adoption handoff, durable publication/locking and terminal arbitration must be resolved before their corresponding phase is merged. Byte/count limits, retained-generation count, stale-response code, fallback ordering and capability names can be chosen while implementing their owning modules, but must become documented constants/contracts with tests. None calls for a distributed scheduler or a separate inventory database.

## A sequence that removes the old paths

First establish reproducible regressions for the twelve findings on the baseline. Include cold/stored/delta behavior and equal-count changed bake input. If the stack needs use before the full refactor, prioritize the P1 SSE bound within the transport that will remain. Decide an integration branch before implementation; do not infer present PR merge status from historical design notes.

Then implement these vertical changes with callers and meaningful tests together:

1. **Datasets and provenance:** implement memory/persisted conformance, bind indexes, migrate bake and raw hit consumers, then remove implicit unions and consumer-side view checks.
2. **Snapshot/resource lifetime:** adopt fresh/load/delta handles, add private build rollback and publication, retain queries through detached packing and migrate inspections/evaluations. Remove one-cycle retirement and caller-owned handle closing.
3. **Durable ownership:** add namespace locking, descriptors/manifests/current pointer, reachable-set GC and crash recovery; derive lineage and fix root traversal.
4. **Operations and shared builds:** consolidate provider lifecycle without losing tool request/reply, add independent waiters/fanout and fix model fallback locally.
5. **HTTP responses:** route both endpoint forms through bounded writes, request cancellation and joined cleanup; prove capacity recovery on real sockets.
6. **Graph transformation/export:** normalize references, update edits and capabilities, make lexical projection explicit and export portable data.
7. **Complete consumer migration:** GUI, shell, traces, evidence, Evals, sweeps, saved plans, presets, examples, exported pages and help use new identities and lifetime rules. Bump IR/artifact namespace versions, remove dead formats only after checking live uses, and document explicit old-store cleanup.

Retain recognizable `rag.run`, `result.top` and fluent corpus/query composition because they remain useful. A string-only `result.chunk(id)` cannot silently choose a dataset. Provide an explicit reference constructor if low-level consumers need one. Manual, timer and post-sync refreshes all call the same build operation.

## Verify the behavior, then the integration

Run focused tests as contracts change. At integration boundaries use `pnpm build`, `pnpm check`, relevant Go package tests and finally `go test ./...` and `go build ./...`. Follow repository lint/format checks. A Go build alone does not regenerate the bundle. Node VM tests cover bundle/publication and minimal-DOM export execution; they do not replace actual Goja/native tests or browser validation.

| Contract | Required test |
|---|---|
| Collections and identity | Memory, stored and delta implementations agree on iteration, missing IDs, nested immutability, materialization and provenance; duplicate logical IDs in distinct datasets resolve their own bytes through fusion/packing/display. |
| Bake caching | Cold/warm callbacks receive one API; changed bytes with unchanged counts invalidate cached configuration. |
| Query lifetime | Pause a provider, publish two later generations, resume and package the old query; returned result stays readable after lease release and unused-handle closure; failed/cancelled refresh keeps the last good snapshot; events and later lookups retain generation identity. |
| Resources | Count adoption/close on fresh/load/delta, duplicate adoption, failure rollback and shutdown; close exactly once after final use; shared handles survive one snapshot releasing them; packaging exceptions also release leases. |
| Store | Create over 40 versions, retain three, collect expected reachable artifacts independent of lineage limits; reject second owner/offline GC while locked; fault each publication step and reopen old/new complete state; corrupt ownership metadata aborts collection and abandoned staging never includes active work. |
| Jobs and outcomes | Cancel A while B waits and B succeeds; cancel all and clean up; new waiter avoids cancelling job; failure remains failure after deferred cleanup; runtime shutdown cleans unposted work; a slow event subscriber neither blocks the shared job nor affects another waiter. |
| Transport | Write/flush errors and non-reading sockets finish within bounds; byte limits hold, terminal event races do not double-settle and admission slots recover. |
| Graph/export/local fixes | Rename, removal and projection rewrite references or reject; adaptive lexical export searches unconditionally with data from a native directory source; dotted roots list outputs and empty enabled list selects deterministically. |

Use provider fakes and barriers to make interleavings repeatable, not arbitrary sleeps. Supplement counting closers with file-descriptor checks where practical. Measure cold build, warm startup, query latency, changed-note refresh, memory, descriptors and artifact growth against the same fixture before/after. Materialized results and explicit provenance may change allocations; passing correctness tests is not proof of performance parity.

Completion requires regressions or reviewed dispositions for all twelve findings, removal of replaced mechanisms, matching documentation and the same example working through browser memory and persistent Goja execution. Reindex status and manual refresh UI can follow using the same operations.

Begin with the reports corpus and the collection regressions, then carry its references through query execution and the served response. At each cutover review which dataset supplied the text, which snapshot is retained, and who releases the resource or operation. The test sequences above make those relationships observable rather than relying on the names of new abstractions.
