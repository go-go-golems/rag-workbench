# Implementing consistent engine contracts

## What this work changes

Rag-workbench lets an application describe a document search system in JavaScript. A corpus reads files, parses documents, chunks them and builds indexes. A plan adds query steps such as retrieval, fusion, filtering, reranking and evidence packing. Go runs the same JavaScript engine in Goja, supplies filesystem and disk-index implementations, and serves results to the browser. Browser-only applications use memory implementations.

RAGWB-009 proposes a refactor of the contracts between these parts. A component should receive the same collection interface on a cold build and a stored build. A search hit should identify the exact dataset containing its text. A query should keep its build outputs alive while it waits for a provider and while the server packages its response. A build shared by two requests should survive cancellation of either request while the other still needs it. Structural graph edits should rewrite every kind of dependency.

This guide describes a target, not completed fixes. The runtime context baseline is c267f91; source was inspected in checkout 3dcc6e0852463f3dfc1113588233e6d293d112c2. The original design's older bundle line numbers are historical. No tests were executed in preparing this guide. File and symbol references below support current-behavior claims; proposed sketches are intentionally new APIs. Runtime paths abbreviated as `engine/...` or `graph/...` are relative to `pkg/ragjs/src/`. Example paths remain repository-relative.

Keep the fluent builders, shared Goja/browser engine, parsing, section chunking, Bleve and terms algorithms, incremental build algorithms, provider behavior and useful evaluation cases. Change their boundaries directly. Compatibility may break: migrate callers and stored namespaces, delete replaced paths and provide no adapters or old-format readers. Distributed indexing, remote datasets, shared multiprocess writable stores, full Obsidian rendering and production authentication are outside scope.

## Follow an existing application

Begin with `examples/apps/reports/lib/corpus.js` and `examples/apps/reports/lib/plans.js`. The corpus builds a text index and a metadata terms index over the same section chunks. Its immutable dense variant adds vectors. The lexical plan retrieves text; the fused plan retrieves both indexes and combines their rankings; answer plans pack excerpts and call a provider. The adaptive plan classifies the query and uses `when: {decision: 'route.needs', min: 0.5}` to condition retrieval.

An existing offline entry point from the reports README is:

```sh
go run ./cmd/ragwb --decide fake --no-store verbs --dir examples/apps/reports reports challenge --names lexical,fused,graded,filtered --threshold 50
```

The fake checks wiring; it does not measure a real model's quality. The challenge distinguishes answerable ranking from rejecting unsupported questions and fails on provider errors.

The current execution path is:

```text
corpus + plan -> compile -> build nodes -> memo / saved payloads
                          |                   |
                          +-> Session.run -> artifacts(ir)
                                               |
                            retrieval -> filtering -> provider -> result
                                                                   |
                                                  runner packing -> JSON/SSE
```

`pkg/ragjs/src/graph/graph.js:finalize` validates component ports, dependencies, decisions, phases and cycles, then orders nodes. `engine/engine.js:build` computes digests, reuses memoized outputs or lazy persisted outputs, attempts a lineage-compatible delta and otherwise computes the node. An artifact is a saved build output. A lineage groups versions of the same build step across source changes so incremental work can find a previous base; it is not a snapshot.

`engine/session.js:Session.run` captures artifacts, executes live nodes, emits events and returns result helpers that close over those artifacts. `pkg/runner/serve.go` then packs chunk text and neighbors. `pkg/server/rag.go` and `verbs.go` frame direct queries and verb calls as JSON or SSE. `web/app-shell/shell.js` adapts remote results to the GUI's host interface.

`pkg/runner/runtime.go:NewFactory` installs authorized source roots, native algorithms, vectors and optional persistence. `pkg/ragjs/module.go` loads generated JavaScript separately into each runtime. Edit `pkg/ragjs/src`, never the generated engine asset. RAGWB-010 already supplies module extraction, pnpm generation, TypeScript and Biome. Only selected foundation modules are strict TypeScript today. Implement 009 contracts and their migrated consumers in strict TypeScript; do not add unused declarations over the current forks. Runtime validation remains necessary for native/JSON inputs. Use discriminated unions for outcomes and typed record provenance in the owning modules. Type checking and Biome do not prove resource lifetime safety.

The current source owners provide a practical navigation map:

| Concern | Authored entry points |
|---|---|
| Collection representation | `engine/records.js`, `components/hits.js`, `components/builtins.js` |
| Build, persistence lookup and retirement | `engine/engine.js`, `pkg/natives/persist.go` |
| Query and request scope | `engine/session.js`, `engine/scope.ts` |
| Builders, compile and edits | `graph/graph.js` |
| Public assembly and registration | `api/index.js`, `api/define.js` |
| Export and corpus-wide consumers | `export/html.js`, `evaluation/evals.js` |

Keep lower-level imports local and acyclic. Public API assembly belongs above these modules, and browser/Goja publication continues to use the same generated bundle.

## Why the boundaries need replacing

The twelve original review findings remain acceptance work. The table identifies mechanisms visible in source; it does not claim each reported failure was reproduced here.

| Finding | Current mechanism | Required correction |
|---|---|---|
| Duplicate chunk IDs choose different versions cold and warm (#2/P2) | `records.js:unionChunks` prefers maps over views and first matching view; arrays use later overwrite | Dataset-qualified references and explicit combination |
| Bake gets a view instead of an array (#2/P2) | `Engine.resolveBakes` passes raw outputs and calls `docs.map` | One collection contract and content identity |
| Dotted store root lists no outputs (#2/P2) | `artifacts/store.go:List` skips any dotted directory basename | Traverse the known store layout |
| Two refreshes close an awaiting query's artifacts (#3/P2) | `Engine.gc` closes retired handles one collection later | Snapshot leases |
| Versions beyond history cap escape GC (#3/P2) | `LineageRecord` caps at 32; `GC` enumerates only lineage | Authoritative inventory separate from search history |
| Delta readers miss runtime cleanup (#3/P2) | `markdown_native.go` creates record wrappers outside persistent-load registration | One resource adoption route |
| Non-reading SSE client retains capacity (#4/P1) | Queue timeout exists, but response writes and flushes are unbounded | Bounded writer and request cleanup |
| First waiter's cancellation controls another build waiter (#4/P2) | `buildShared` stores the first caller's promise/options | Job-owned context and independent waiters |
| Deferred cleanup changes provider failure to cancellation (#5/P2) | `rerankAsync` examines context in a later posted callback | Immutable terminal outcome |
| Empty enabled-model list prevents fallback (#6/P2) | `llm/gepp/backend.go:preferred` has no unrestricted fallback loop | Deterministic local policy |
| Lexical export keeps references to removed decisions (#7/P2) | `export/html.js` prunes by trigger/output | Explicit capability projection |
| Rename misses decision references (#7/P2) | `applyOps` rewrites inputs and outputs only | Shared structural-reference traversal |

Existing safeguards matter. Compilation already rejects missing decision nodes. The export defect can therefore appear as rejection of a valid intended lexical projection, not successful execution of an unchecked invalid graph. Persistence registers several loaded readers, and the SSE queue bounds how long an emitter waits on a full queue. Those measures are incomplete, rather than absent.

## Datasets: identify records before resolving them

A dataset is an immutable version of a document or chunk collection. Its storage may be a memory array or disk records; components see one synchronous read interface. Synchronous access fits the current hosts. Speculative remote storage does not justify making every record read asynchronous.

```ts
// Proposed sketches; record structures and validation are omitted.
type RecordRef = Readonly<{ dataset: string; id: string }>;
interface Dataset<T> {
  readonly id: string;
  readonly kind: 'documents' | 'chunks';
  readonly size: number;
  get(id: string): T | null;
  values(): IterableIterator<T>;
  materialize(): readonly T[];
}
interface ChunkDataset extends Dataset<Chunk> {
  readonly documentDatasetId: string; // proposed binding to source docs version
  document(chunkId: string): RecordRef | null;
  order(documentId: string): readonly RecordRef[]; // within that docs version
}
interface Index {
  readonly datasetId: string;
  search(query: string, options: SearchOptions): readonly Hit[];
}
type Hit = Readonly<{ ref: RecordRef; score: number; reason?: string }>;
```

Iteration order must be deterministic and identical for equivalent memory and persisted datasets. A missing ID returns null. Public records must be deeply immutable or detached so callback mutation, including nested metadata, cannot change the authoritative data. Cache immutable decoded records internally if useful. Do not expose native handles, codecs, `__records`, `__ix` or close methods to components.

Consider two chunk datasets with the same logical section `report#ownership`. Dataset D1 contains original text; D2 adds a generated prefix. The text index built from D1 returns `{ref:{dataset:'D1',id:'report#ownership'},score:0.82}`. A second index built from D2 returns a reference to D2. Both are valid hits; neither may resolve through an engine-wide map that chooses one text arbitrarily.

A chunk also needs a versioned source document. A minimal proposed rule is one `documentDatasetId` per chunk dataset: D1 and D2 above can both point to document dataset M1 and logical document `reports:report.md`. `document('report#ownership')` then returns `{dataset:'M1', id:'reports:report.md'}`; missing chunks return null. `order(documentId)` returns chunk references from that chunk dataset, and an absent document yields an empty list. An enrichment changes the chunk dataset without necessarily changing M1. A component combining different source-document versions must explicitly normalize them or declare a richer mapping; it cannot quietly violate the single-binding rule. This proposal needs agreement before implementing the TypeScript interface.

An index binds exactly one dataset version. Retrieval, fusion, filters, rerank, packing, citations, events, traces, GUI display and evaluation must preserve that reference. Fusion deduplicates exact references by default. If an application wants both indexes to vote for one canonical section, it must project to an explicitly selected dataset or use an ordinary combination component with declared conflict rules. Delete `unionChunks` rather than changing its precedence.

Logical document identity must include a source identity and relative path, not only title or slug. Immutable dataset identity must distinguish changed content even with equal record counts. Index identity includes its input dataset, normalized parameters and component/codec version. The precise canonical encoding and hash remain implementation decisions that must be specified and tested before persistent cutover.

Bake and custom components receive datasets. A concise callback such as `ctx.chunks.size` is meaningful only if `chunks` has a declared binding. Eliminate the current first-matching-stream selection in `buildCtx` and `resolveBakes`. One proposed API makes the binding explicit:

```js
// Proposed declaration syntax, not a runnable existing call.
rag.bake({ bind: { chunks: 'enriched-chunks' } },
  ctx => ctx.chunks.materialize().map(c => c.id));
```

The graph compiler resolves the binding to the output of `enriched-chunks`; it participates in ordinary reference validation, dependency ordering and rename. A count-only callback uses `ctx.chunks.size` without materialization. Single-dataset builders may fill this declaration when the input is unambiguous; multiple streams must never choose by graph order. Settle the exact syntax during migration, then update all callbacks and examples together.

Cache a bake using full identities of every bound input dataset, callback source/configuration and component version. Current `resolveBakes` hashes document IDs/section counts and chunk length, which misses changed content with unchanged counts. Bindings also make the cache's complete dependency set inspectable.

## Snapshots retain resources until the last read

A snapshot is an immutable mapping from graph build outputs to datasets and indexes. Separate three identities before implementing refresh:

| Identity | Scope and purpose |
|---|---|
| Publication slot | Stable build graph/configuration and semantic policy; selects the current usable generation as sources change |
| Build key | Full target input versions plus normalized semantic options and implementation versions; joins only equivalent work |
| Snapshot ID | One complete immutable published manifest, with its datasets and indexes; carried by queries and later lookups |

These names and encodings are proposed refinements of the target. The distinction is required: a full content key changes when a note changes, while the application's publication slot must still name its last good generation. Different query plans over the same corpus can share a build slot; configurations affecting build output must not share it accidentally. Different slots may share immutable artifacts. Several snapshots can share unchanged handles, so retiring one snapshot cannot close every handle it mentions.

A resource registry owns each native reader or index. A current snapshot retains its resources even with zero active queries. Deliberately retained history and query leases can also retain a snapshot. A private build scope retains resources before publication and releases them on failure. Query acquisition selects the slot's current snapshot and retains it in one serialized transition on the runtime owner. Go operations spanning goroutines need synchronization as well.

Retention covers the artifact payload and its dependencies even when a dataset reader or shard has not been opened yet. Open-handle reference counts alone cannot protect lazy reads against disk GC. Maintain that durable reachability alongside resident registry entries; do not force all lazy records into memory just to keep them alive.

```text
A current; query Q leases A
refresh builds and validates B privately
publish B; A retired but Q still leases A
publish C; Q still reads A
Q packs detached result and releases A
release A's registry references; close only entries with no remaining users
```

Refreshing leaves the last good snapshot available in the slot. Ordinary queries use that generation while newer source content is building; they wait only if the slot has no usable snapshot. Changing the graph or semantic configuration can select a different slot and require its first build. Partial outputs, including deferred costly build items, remain private and cannot masquerade as a complete published snapshot. Failed or cancelled refresh leaves the old current pointer intact.

Concurrent refresh publication needs an arbiter. A minimal proposed policy serializes publication per slot, captures the candidate's complete source/input revision, and rejects a candidate superseded by a newer accepted target revision. The owner then schedules the latest target through the same build operation. Do not add wall-clock ordering or compare hashes lexically as a freshness test. The current code has content digests but no complete source-revision arbitration contract, so define this before claiming concurrent refresh safety. Failed freshness validation releases the private candidate; it does not disturb current queries.

```ts
// Proposed control flow, not current APIs.
async function query(plan, signals, request) {
  const slot = builds.slotFor(plan);
  await builds.ensureUsable(slot, request.signal);
  return snapshots.withCurrent(slot, async snapshot => {
    const run = await executor.run(plan, signals, snapshot, request);
    return detachResult(run, snapshot, request.resultBudget);
  });
}
```

The lease includes event and result packaging, not just retrieval. The current served path awaits the query and then reacquires `eng.artifacts(ir)` for packing; remove that generation switch. Release in a finally path on execution, cancellation or packaging failure. Any serialization that still dereferences snapshot data belongs inside this scope. Once the result is detached, JSON encoding and network transmission require no query lease.

Every opener must adopt a resource immediately or close it if adoption fails. Fresh builds, persistent loads, delta readers and temporary delta bases use the same route. Remove the no-op AddCloser fallback in resource-bearing runtimes.

```go
// Proposed pattern. Scope owns adopted resources until transfer or release.
r, err := OpenRecords2(path)
if err != nil { return nil, err }
h, err := scope.Adopt(id, r)
if err != nil { _ = r.Close(); return nil, err }
return NewChunkDataset(h), nil
```

The sketch transfers closer ownership only on successful adoption. On error the opener still owns `r` and closes it. A recommended duplicate-success policy reuses the registered entry and has the registry close the redundant new handle immediately; callers must not close that handle again. Different underlying content for one resource identity is an error.

A registry entry has one closer owner and explicit retainers. Publication first establishes the new snapshot's retention, then changes current, then releases the builder scope and the old current retention. This order leaves no zero-owner interval. Retired snapshots release only after their query/history retainers are gone. Abandoned candidates release all build-owned resources. Shutdown rejects work, cancels operations, joins them, releases snapshots and closes the registry. Report a timeout if workers remain; do not close handles beneath them and claim success.

An ordinary `rag.run` returns detached data requiring no caller cleanup. Collect record references from every retained result field before releasing the lease: final hits, intermediate hit outputs, trace detail, packed/answer evidence and configured neighbor preview. Deduplicate by full reference, resolve in this snapshot and copy the records into the detached result. `result.top()` and `result.chunk(hit)` read these owned records. Event payloads must likewise contain detached data or a generation-qualified reference.

For example, a top-ten result can require a record appearing only in a rerank trace plus a neighbor not present in final hits. Copying only final hits breaks those consumers. Bound the complete result by declared byte/record limits. If the expanded set exceeds its budget, return an explicit limit error or a specified paginated response; never advertise records and omit their bodies silently. This is a bounded result copy, not full corpus materialization.

```js
// Proposed ordinary API; the result helpers read detached records.
const result = await rag.run(plan, { q: 'resource ownership' });
const first = result.hits[0];
const chunk = first ? result.chunk(first) : null;
const summary = result.top(5);
```

Whole-corpus export and evaluation label resolution use an internal scoped snapshot read.

Later browser lookups carry both snapshot ID and record reference. Resolve against a retained generation or return a stale-generation response. Cache by generation/reference and invalidate on generation changes. Retention duration and size limits remain policy decisions. Retaining generations can preserve newly excluded notes; retention does not provide authorization and refresh cannot revoke text already delivered.

## Durable inventory and collection

Query leases protect live readers in one process. Disk GC also needs durable ownership information and protection against another process deleting unopened shards. Keep existing immutable payloads, codecs and incremental algorithms, but replace lineage as collection authority.

Use artifact descriptors listing owned payloads and dependency artifacts; immutable snapshot manifests list roots; a small current pointer selects a manifest for each publication slot. Descriptors/manifests are authoritative. The bounded lineage search list is derived acceleration that can be rebuilt or discarded. Do not add a database just for this inventory.

The initial deployment contract is one process with an OS-backed exclusive lock per writable namespace for its lifetime. It owns build publication, leases and online GC. A second process fails clearly or selects another namespace. Standalone `store gc` takes the same lock and runs offline. The browser memory store follows the logical contract without filesystem locking.

Publication order is:

1. Write candidate payloads under owned staging.
2. Close writers and validate counts, checksums and codec metadata.
3. Move complete immutable payloads into the namespace.
4. Publish artifact descriptors atomically.
5. Publish the complete immutable snapshot manifest.
6. Replace the current pointer atomically.
7. Update derived lineage acceleration.

A crash before the pointer update leaves the old snapshot current; afterward the new snapshot must load even if lineage is stale. Atomic rename does not by itself establish power-loss durability. Specify file/directory sync operations and test the supported platform boundary.

GC marks current snapshots, deliberately retained historical snapshots, active query/builder leases and temporary build ownership. Follow descriptor dependencies transitively; sweep only unmarked owned artifacts. Serialize the mark/sweep decision with publication, including the interval between durable candidate creation and becoming current. Missing or corrupt ownership metadata aborts destructive collection; unknown files are never assumed disposable. Explicit layout traversal also fixes `List` skipping a dotted root.

Hard-linked files remain safe through directory ownership: deleting one artifact removes its links, not another artifact's links. Distinguish logical sizes from actually reclaimed disk bytes. Keep history according to an explicit retention policy independent of query duration. Old namespace cleanup is an explicit operator command when no process owns it, never automatic deletion of arbitrary directories.

## Operation outcomes, shared builds and transport

Sampling, deciding and reranking share context propagation, abort listeners, worker execution, VM posting, settlement and cleanup. Extract that lifecycle, leaving request conversion, result validation, tool semantics and provider error mapping specialized. Sample tools require worker-to-owner request/reply; all Goja value access stays on the VM owner. Transfer immutable Go values or bytes across goroutines and use errgroup for related workers.

Represent one immutable terminal outcome: success, caller cancellation, deadline, provider failure or runtime shutdown. For a native operation, a guarded terminal-state transition in the Go lifecycle runner arbitrates execution completion, caller abort, deadline and shutdown. If cancellation is accepted before completion, cancellation wins; a result accepted first remains fixed. The later cleanup of a derived context is not another competing operation outcome. Equivalent JavaScript-only transitions stay on their runtime owner. Classify provider errors and fix the outcome before deferred cleanup and before posting settlement.

```text
execution result ----+
caller abort --------+--> one guarded terminal transition --> immutable outcome
deadline/shutdown ---+                  |                         |
                               worker cleanup               post to VM owner
                                                            settle promise once
```

Worker cleanup may finish before the VM callback runs. Detach abort listeners when the owner is available; if the VM is closing, its disposal path must drop them without requiring worker access to Goja values. A closed VM can prevent promise delivery, but cannot prevent task/resource cleanup. The outcome can be recorded even when delivery is impossible. Preserve specialized sample JSON validation and tool-error-as-tool-result behavior, and preserve provider status/request/retry details.

`buildShared` becomes a small job registry, keyed by the full build key including semantic options such as build-call policy. A job has its own context linked to engine shutdown and explicit waiters. Request A and B can both wait for J; cancelling A detaches A, while B still owns interest. If all waiters detach, cancel J unless a background-refresh owner remains. A newcomer must wait/retry after a cancelling job terminates rather than join a doomed promise. Shutdown cancels jobs and waiters and joins cleanup.

Build progress belongs to the job, then fans out to request subscriptions with job/operation identity. Bounded, independent subscriptions prevent a slow client blocking the job or another waiter. Timer, manual and post-sync refresh must use the same build/refresh operation; triggers only schedule it.

Both HTTP execution forms should use one response helper for admission, subscription, JSON/SSE response and request cleanup. Keep domain events separate from HTTP framing. For SSE, one writer goroutine owns every write and flush, including headers, heartbeat, start and terminal events. Set a per-write deadline through `http.ResponseController.SetWriteDeadline` and use its error-returning Flush. Unsupported deadline control is setup failure; wrappers must expose unwrapping. After a deadline has expired, end that transport instead of attempting recovery by extending it.

On write/flush failure or queue overflow, stop pumping, cancel or detach this request and perform a bounded join. Require provider/native work to cooperate with context cancellation. A join timeout must remain a tracked operation with its resources retained; report it, and do not declare the runtime clean or reuse closed handles beneath it. A proposed supervisor policy marks that runtime unhealthy until workers finish. This exceptional policy still needs implementation agreement; a socket deadline alone does not stop a worker. Do not cancel unrelated job waiters. Keep both event count and queued/coalesced byte counts bounded. One huge event or endless accumulated text bypasses an entry-count limit. A healthy long stream can continue with per-write deadlines rather than one short global timeout.

At most one terminal event is attempted by the sole writer. A broken network may receive none. Cancellation, operation settlement and delivered terminal events are distinct facts. A domain success accepted before a socket failure stays a success in operation accounting; its response delivery failed. Both direct queries and verbs need the same capacity admission scope and terminal error vocabulary. JSON responses also need bounded network writes and explicit response-size limits through the shared helper; choosing JSON must not bypass the transport lifetime. A real socket test must show that a client that stops reading cannot retain a slot indefinitely; queue-only tests do not establish that.

Pi fallback is a small independent correction in `pkg/llm/gepp/backend.go:preferred`. Proposed policy: honor an explicit tier choice; otherwise select an eligible enabled default, then the first eligible ref in a nonempty enabled list; if that list is empty, select the lexically first eligible provider/model ref. With no eligible model return a clear error. Empty means unrestricted, consistently with the existing default check. Preserve provider errors and never depend on map iteration order.

## Graph transformations share one reference contract

The compiler, editor and exporter currently enumerate dependencies differently. Normalize the concise public DSL into typed structural references once, then use one visitor for validation and rewriting.

```ts
// Proposed normalization of when: {decision:'route.needs', min:0.5}
{
  source: {kind: 'decision', node: 'route', output: 'needs'},
  minimum: 0.5
}
```

Components declare data inputs, artifact dependencies and decision references; the graph also exposes pinned outputs and signal dependencies. `references` enumerates these, and `rewriteReferences` applies a mapping. Compile checks target existence, output type, phase and cycles from that enumeration. Rename checks unique targets, rewrites every reference, then validates the complete edit transaction. For the adaptive reports plan, renaming `route` to `router` updates both retrieval conditions' source nodes, any declared callback binding and any pinned output naming that node. The question output `needs` and threshold 0.5 remain the same. Enumerate graph-level output references as well as node-local inputs; a node-only visitor would leave that gap. Delete rejects, cascades or replaces dependent references according to explicit caller policy; it must not guess decision behavior.

Lexical HTML export is a named projection with a documented policy: when it omits a decision-only classifier, supported retrieval becomes unconditional. Capability declarations such as local index, external model and host filesystem guide selection. Submission trigger is not a capability. Validate the projected graph before serializing it, and bundle a portable normalized dataset from a scoped read. Do not assume server `source.dir` files are in `engine.files()`.

For the adaptive plan, first choose the browser's supported lexical capabilities, omit external classifier/reranker/answer steps, remove the declarative route gates under this named projection policy, and rewire compatible data edges. Then compile the complete projected graph and bundle its portable records/index inputs. If an excluded node has a dependent reference without a declared projection rule, reject export with that dependency named. Do not bypass compile validation or silently make arbitrary production decision gates unconditional.

Opaque JavaScript is a boundary: regex or string replacement cannot reliably rename arbitrary callback references. Graph-aware callbacks must declare named dependency bindings, and callback code uses those binding names. Rename updates the bound graph reference. Reject transformations requiring undeclared opaque rewrites. This is an explicit supported-language restriction.

## Implement in vertical cutovers

Use a few internal owners: graph, datasets, snapshots/resources, operations and executor, with a thin public `rag` facade. The 010 source modules already exist; preserve an acyclic local import graph and one generated Goja/browser bundle. Do not create a scheduler, container framework or second execution model. A new abstraction must replace an existing responsibility or duplicated path.

| Stage | Work and deletion | Evidence before proceeding |
|---|---|---|
| 1. Characterization | Add regressions for all twelve findings; establish cold/warm/delta and equal-count bake baseline | Record actual failures; make early SSE correction in the future shared helper if needed |
| 2. Dataset cutover | Implement memory/disk conformance, index binding, references, callbacks and all hit consumers; delete union and consumer view tests | Original/enriched duplicates resolve correctly across hosts |
| 3. Ownership | Adopt all resources; publish snapshots; lease query/event/response packing; detach results; delete rebuild-count retirement | Query paused across two refreshes succeeds and all handles close once |
| 4. Store | Add descriptors/manifests, lock, publication and mark/sweep; make lineage derived; fix dotted root | 40+ versions, retained roots, active readers and crash injection |
| 5. Operations | Share bridge lifecycle and job ownership; retain provider/tool behavior; fix fallback locally | Cancellation races, all-waiter exit, runtime shutdown, fanout |
| 6. Transport | Migrate both endpoint forms to one bounded writer and request lifecycle; delete duplicated pumps | Real stalled socket, flush failure and slot recovery |
| 7. Graph/export | Normalize references and capability projection; migrate graph consumers and remove scattered rewrite logic | Rename/delete/project either yield valid graph or explicit rejection |
| 8. Integration | Remove obsolete formats and dead paths; update examples, help and API docs | Reports and vault workflows, Goja/browser checks and resource measurements |

Stages describe coherent changes with removal of the old implementation, not permanent parallel engines. Coordinate an integration branch with the owner rather than assume the original PR stack's historical status still applies. Design delivery does not authorize merging, force-pushing or resolving review threads.

Migration includes GUI, shell caches, traces, citations, evidence, Evals, sweeps, saved plans, presets, custom components and inspection endpoints. Keep recognizable `rag.run` and `result.top`; remove ambiguous string-only `result.chunk(id)`. Bump IR and artifact namespace versions. Enumerate live codec references before removing obsolete formats; keep useful browser and quality-comparison backends.

The runtime README gives the starting checks: `pnpm build`, then `pnpm check`, then focused Goja/runner/web tests. At implementation integration boundaries run `go build ./...` and `go test ./...` plus required lint checks. Use tmux for servers and capture-pane for logs; stop a web server with `lsof-who -p PORT -k`. These are instructions for future implementation, not executed validation results.

## Validation and remaining choices

Preserve ordinary tests such as `runner/TestDeltaBuildEqualsFullBuild`, `TestChunkRecordsReplaceTheChunksMap`, `TestServeRefreshIsIncremental`, `ragjs/TestWhenInPlanEdits` and `artifacts/TestLineage`, updating obsolete representation assertions. Their existing cold/warm and small-history coverage does not prove the new lifetime contracts.

Add deterministic hold/release barriers and counting closers for these sequences:

- Memory, stored and delta datasets agree on immutable records, missing IDs, ordering and materialization. Equal-size changed content invalidates bakes. Multiple sources with similar paths remain distinct.
- Pause a query in a fake provider, publish B and C, resume and pack records from A. Retire A, close unused native handles and verify the returned result still works. Exercise failure during adoption and packaging.
- Build more than 32 versions, retain three and collect exactly the reachable set, independently of lineage length. Refuse a second namespace owner or offline collector while serving. Interrupt each publication boundary and reopen a complete old or new snapshot.
- Cancel A while B waits for a build; B succeeds. Cancel every waiter and verify resource release; attach a newcomer during cancellation and verify retry. Slow event subscribers cannot block other waiters.
- Preserve provider failure after cleanup; distinguish actual user cancellation and deadline; close the runtime during posted settlement and tool requests without leaking workers.
- Use a real client that stops reading SSE and enough bytes to exhaust socket buffering. Assert deadlines, bounded coalescing and capacity recovery. Cover final-event races and flush errors.
- Rename decision/artifact/data/output references, reject collisions and unsupported lambdas, and export an adaptive source plan into a working unconditional lexical page. Cover dotted store roots and deterministic fallback with empty/nonempty model lists.

Measure cold build, warm startup, query latency, changed-note refresh, memory, file descriptors and artifact growth on the same fixture before and after. Detached data changes allocation cost; passing tests does not establish performance parity. Report fixture size, limits and retained generations.

The main decisions and consequences are:

| Decision | Alternative considered | Status and consequence |
|---|---|---|
| Synchronous immutable datasets and explicit provenance | Async per-record reads; global ID union | Original target. Retains local storage efficiency; requires migrating all record consumers. |
| Scoped snapshots and detached ordinary results | Caller-released results; rebuild-count delay | Original target. Ordinary callers need no cleanup; materialization budgets become public behavior. |
| Descriptors/manifests and one writable namespace owner | Lineage inventory; shared multiprocess leases | Original target. GC can see complete ownership; simultaneous independent writers are refused. |
| Job-owned cancellation and one outcome | First-request ownership; late context classification | Original target. Requires waiter subscriptions and a terminal arbiter; provider logic stays specialized. |
| Shared graph reference traversal and capability projection | Per-transform reference searches and trigger pruning | Original target. Opaque callbacks need declared bindings or explicit rejection. |
| Separate slot/build/snapshot identities, one document binding, duplicate reuse, supervised join timeout | Implicit identity roles; multiple silent document sources; untracked workers | Refinements proposed here. Confirm before coding the affected contract. |

Before coding persistence and concurrency, settle canonical identity encoding, source-document version mapping, slot sharing/freshness arbitration and lock/sync platform support. Before public cutover, settle binding syntax, complete-result budget behavior and the stale-generation response/retention contract. These are contract decisions. Concrete numeric budgets and retention defaults are tuneable with measurements; their enforcement and explicit error behavior are required regardless of values.

When implementing Go ownership interfaces, add compile-time interface assertions; use context arguments, `github.com/pkg/errors` for wrapping and errgroup for related workers. Use only the top-level go.mod. A feature touched under a build tag must still compile without it. These repository conventions should support the small lifecycle implementation rather than justify a framework.

Completion requires passing regressions or an explicit reviewed disposition for all twelve findings; removal of the replaced mechanisms; consistent memory/browser and persistent/Goja examples; matching API documentation; and verified shutdown, cancellation and publication behavior. Moving code into modules or giving it a new name does not close a semantic defect.
