# Research before drafting

Read the entire original 009 design, including section 16 and the publication/ownership sections, plus the assigned candidate skill and both references. Applied the candidate's evidence-first and two-pass writing procedure. The shared context identifies runtime baseline c267f91; source inspection is read-only. No tests or examples were executed, and no review reports, earlier candidates or sibling outputs were read.

## System and representative path

A corpus compiles source/parse/chunk/index build steps; a plan adds signal-driven live steps. examples/presets/reports.js supplies a real multi-stream case: sections, failure-table rows, metadata terms, fusion, reranking, neighbors, context and answer. It also contains a bake callback iterating ctx.docs, evaluation labels and variants that graph edits must migrate.

runner.NewFactory in pkg/runner/runtime.go installs SourceDir, native markdown, BM25, terms, vectors and optional persistence; ArtifactNamespace already distinguishes code/native versions and backend. pkg/ragjs/module.go embeds generated rag.js and installs per-runtime host hooks. pkg/ragjs/src/README.md confirms authored sources, shared generated bundle, strict typed foundation only and pnpm check duties.

Session.run reads engine.artifacts, waits for buildShared if absent, executes live components across awaits, emits trace/events and decorates returned results with art-backed methods. Serve's __serve.run then closes the request scope, collects and fetches art() anew before packing result bodies. Failure path: provider exceptions are caught into per-node errors/skip outputs, cancellation breaks execution; build failure throws before live execution. The proposed outcome runner must account for the existing partial-result behavior rather than assume every node error currently rejects a query.

## Primary evidence

- engine/records.js: Lazy, isView/asArray and unionChunks. Array map wins over all views; views resolve first match. Separation destroys source stream precedence.
- engine/engine.js: build coerces inputs through asArray; resolveBakes uses raw outputs and docs.map/chunks.length; cache key has document IDs/section counts and chunk count. artifacts builds global chunk union and first non-enrich stream order. gc closes prior retirement list one cycle later. buildShared keys digests only and returns first caller's promise/options.
- engine/session.js: ctx.chunk/docChunks/neighbors and decorateResult use bare IDs and global art; hits are normalized by components/hits.js; builtins.js retrieval/pack/fusion use those IDs.
- engine/scope.ts: synchronous capture only; close disables events but does not abort. withScope does not persist async-local state.
- natives/persist.go InstallEnv: no-op AddCloser fallback; loads register Records2 and Bleve; view wrapper itself does not adopt. markdown_native.go parseDelta/chunkDelta return records2Object without runtime closer registration in these callbacks.
- artifacts/lineage.go: LineageKeep=32, LineageSearch=8; LineageRecord truncates, GC enumerates remaining lineage entries. It cannot discover dropped versions. store.go List skips dotted directory basenames, including a dotted traversal root.
- natives/rerank.go rerankAsync: worker defers cancel, posted settlement reads callCtx.Err later. sample.go/newSample and decide.go/newDecide classify before posting, with richer provider mapping/tool callback behavior to preserve.
- server/sse.go: bounded event count and queue wait, but writes/heartbeats lack deadlines, Flush cannot report errors, pump ignores returned write errors, pending text grows by concatenation. rag.go/verbs.go duplicate orchestration; verbs acquire admission slot, direct run does not in the inspected handler.
- graph/graph.js finalize separately discovers in, uses, decisions, signal reads; rename rewrites only in/outputs; remove has some existing dependency rejection and finalize rejects missing decision references. export/html.js removes submit-triggered nodes by heuristic then serializes engine.files(). A rejection is not successful corrupt export.
- llm/gepp/backend.go preferred: an empty enabled list allows default selection but fallback loops only that empty list. Sorting eligible keys supplies a deterministic proposed fallback.
- runner/records_test.go characterizes cold/warm view differences; delta_test.go compares incremental and full builds after edits, immutable originals and queries. These are useful fixtures, not proof of the proposed contracts.

## Decisions requiring explicit treatment

The original fixes policy on single writable namespace ownership, synchronous Dataset access, detached ordinary results, one snapshot per query, exact-reference fusion and canonical graph traversal. API names are sketches. Need to resolve document references in a multi-document-dataset snapshot; ChunkDataset.order(documentId) alone is ambiguous if reused across sources/versions. Fresh resource adoption timing, partial build ownership and publication winner rules need precision. Current source has partial LLM builds and per-node errors; define what constitutes a publishable snapshot and a successful versus failed query outcome. Freeze/copy nested records, cache identity source declarations, historical lookup retention, result/event byte budgets and bounded-join behavior require decisions. Power-loss guarantees need platform-specific evidence, not rename claims.

## Draft approach

Teach a reports query, distinguish logical document IDs, dataset versions, artifact keys and snapshot IDs, then explain each replacement and migration together. Preserve all twelve finding categories with source evidence and acceptance tests. Mark added policy proposals clearly and isolate blocking decisions from ordinary naming choices. Do not use the historical bundle line map as current navigation.
