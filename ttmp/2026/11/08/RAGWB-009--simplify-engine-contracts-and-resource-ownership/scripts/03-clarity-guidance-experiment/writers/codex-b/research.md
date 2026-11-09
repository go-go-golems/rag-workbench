# Research before drafting

Read the complete original 009 guide, including its TypeScript-foundation section, and the supplied candidate skill, writing-style reference and deliverable checklist. Applied the candidate's two-pass writing procedure; ticket registration and implementation are outside this assignment. Source inspection only: no tests, example execution, or independent bug reproductions.

## Reader's task and system understanding

A new engineer must migrate one shared browser/Goja JavaScript search runtime to explicit dataset identity, snapshot ownership, asynchronous outcomes and graph references. Corpus builders describe source/parse/chunk/index build work. Plans add query signals, retrieval, fusion, filters, provider calls, evidence packing and answers. Go natives supply filesystem authorization and disk-backed implementations. HTTP and GUI consumers also interpret hit identity and must migrate with the engine.

The input packet names runtime baseline c267f91. Inspected HEAD is 6cc526d; `git diff --stat c267f91 -- pkg examples web package.json` produced no differences. This supports using c267f91 for the inspected runtime claims. Authored runtime source is pkg/ragjs/src; assets/rag.js is generated. No implementation changes made.

## Primary evidence

- `pkg/ragjs/src/README.md`, package.json: shared generated bundle; pnpm build/check; only core helpers and scope currently strict TypeScript. New contracts must become strict TS with their consumers, not unused declarations.
- `examples/apps/reports/lib/corpus.js`, `lib/plans.js`, README: real corpus and lexical/fused/answer/adaptive plans. Adaptive uses route.needs decision gates. Offline fake decision challenge is documented; not run.
- `engine/records.js`: Lazy and Outputs force stored values; isView checks __records; asArray converts views; unionChunks searches memory first, then views in order. Cold arrays are combined with Map.set, so backend affects duplicate precedence.
- `engine/engine.js`: build chooses stored/full/delta paths and incremental fallbacks. resolveBakes selects first docs/chunks, assumes docs.map/chunks.length, hashes IDs/section counts and chunk count. artifacts globally combines chunks and chooses first non-enrichment stream for reading order. gc closes retired handles on a later collection, without query leases. buildShared keys by build digests and returns the first build promise/options.
- `engine/session.js`: run obtains artifacts, awaits component work and normalizes hits; ctx.chunk uses bare IDs; decorateResult closes over artifact lookup. `components/hits.js` also resolves bare IDs. These are end-to-end provenance consumers.
- `runner/serve.go`: __serve.run awaits session, closes scope, collects, reads current artifacts again, and packs output plus neighbors. Fixing only Session lifetime would leave packaging inconsistent. Existing NB_HITS=40 illustrates preview bounding, not a new result budget policy.
- `natives/persist.go: Persist.InstallEnv`: AddCloser defaults to no-op; persistent record/index opens register closers. `markdown_native.go` parseDelta/chunkDelta expose returned Records2 handles without equivalent registration. Registry must include fresh, loaded, delta and temporary/base-reader paths.
- `artifacts/lineage.go`: LineageKeep=32, search=8; recording truncates history; GC discovers old outputs from remaining lineage records. GC comments rely on retained versions rather than active leases. `store.go: List` skips any directory basename containing a dot, including a dotted input root.
- `natives/rerank.go: rerankAsync`: deferred cancel runs after posting owner callback, whose classification later reads callCtx.Err. Failure can be relabeled. sample.go/newSample and decide.go/newDecide compute error classification before posting but repeat lifecycle setup. Sample has a tool request/reply bridge; generic lifecycle cannot erase it.
- `server/sse.go`: queue bounds and slow-queue cancellation already exist; writer has no write deadlines, uses http.Flusher without error, pump discards write/ping errors and coalesces text without byte bound. rag.go/handleRun and verbs.go/handleVerb independently orchestrate pump and terminal writes. Admission currently applies to verbs, not direct runs. A stalled pump can keep verb's deferred slot release from running.
- `llm/gepp/backend.go: preferred`: default allowed when enabled list empty, but fallback loops only that empty list. Deterministic eligible fallback remains a local policy choice.
- `graph/graph.js: finalize`: validates missing and wrong-type decision references already, along with inputs/uses, and finalizes after edit batch. rename rewrites inputs/pinned outputs, misses when and component uses. Existing validation can reject the result; guide must not claim dangling plans always execute. html.js prunes by trigger/output in separately validated stages; may fail on retained gates before reaching a useful export.
- Inspected existing tests: runner/records_test.go cold/warm semantics; runner/delta_test.go full-vs-delta test names; runner/lineage_test.go; natives/rerank_test.go; server/verbs_test.go. They offer reusable fixtures but do not establish the proposed contracts.

## Requirements to preserve

One synchronous read-only dataset API; dataset-qualified hits/document identity; explicit cross-dataset fusion; complete cache identities. Immutable published snapshots, query leases through packaging, detached bounded results, generation-aware later lookup. Exactly one registry owner with checked adoption and release, transactional candidates, ordered shutdown. Authoritative durable descriptors/manifests and exclusive process namespace ownership, publication durability and conservative GC. Shared jobs independent of one waiter; terminal outcome fixed before cleanup; owner-thread VM access; bounded SSE writing and joining, common admission for direct/verb. Canonical graph visitor and capability export with explicit unconditional lexical gates; reject opaque undeclared callback rewrites. Strict TS cutovers, no adapters, no new generic scheduler/container/database, preserve meaningful algorithms/providers/tests.

## Underspecified choices to expose

- Exact dataset identity encoding, stable source identity and named callback dataset bindings; prevent reinstating first-stream selection in a new interface.
- Complete detached output policy: arbitrary custom outputs/trace/evidence, required documents and preview budget; fail explicitly if limits exceeded.
- Retention duration/count and stale-generation wire status; necessary before GUI contract completion, independent of active lease safety.
- Duplicate handle adoption policy and close failures; choose a simple explicit policy in the proposal.
- Serializing competing refresh publication; job build identity alone does not prevent an older candidate replacing a newer snapshot. Propose one active refresh per plan build identity and an explicit queued rerun.
- Cancellation arbitration and deadline/shutdown distinctions; specify one terminal arbiter. Bounded HTTP return must not falsely imply an uncooperative provider worker has joined.
- Store schema/durability support, restart staging recovery and lock portability before destructive GC is enabled.
- Deletion default, capability declarations and deterministic fallback ordering; propose concrete defaults rather than imply old guide settled them.

These notes precede all drafting. The final guide will distinguish existing observations, required target behavior, proposed policy details and implementation decisions still needed.
