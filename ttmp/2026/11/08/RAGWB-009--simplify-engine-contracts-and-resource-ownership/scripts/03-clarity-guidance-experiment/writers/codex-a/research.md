# Research before drafting

The reader must understand how a reports search becomes build outputs, query hits and browser data, then implement replacement contracts without preserving storage-dependent behavior. The supplied original design was read in full, including its durable publication protocol and section 16. This is analysis/design only; source and test inspection below did not execute tests.

## System understanding

A corpus builder describes build nodes (source files, Markdown documents, section chunks, indexes). A plan adds signal and live nodes (retrieval, fusion, filters, provider calls, evidence packing and answers). The compiler validates ports and dependencies and produces an ordered graph. Engine.build memoizes by build digest, optionally loads persisted outputs lazily, and attempts incremental continuation through lineage history. Session.run obtains an artifacts aggregate, runs live nodes across awaits and returns methods closing over that aggregate. The served app performs another artifact lookup when packing records for HTTP clients.

010 already extracted authored runtime code to pkg/ragjs/src. The generated assets/rag.js remains the shared browser/Goja payload. The context identifies c267f91 as runtime baseline; the inspected worktree HEAD is 6cc526d290eb7898efbe8a801710471c7d2a0cb5, with documentation additions according to the supplied context. Do not reuse the original's 0854c6a line numbers. Current source symbols are the guide's navigation targets.

## Primary evidence

- pkg/ragjs/src/README.md and package.json: pnpm build generates the asset; pnpm check combines strict typed modules, Biome, exact generation and Node bundle tests. Most extracted modules remain unchecked JavaScript. New contracts belong in strict TypeScript. Import graph is local/acyclic; browser VM tests do not establish actual browser-engine/visual compatibility.
- examples/presets/reports.js: multi-stream corpus, frontmatter bake, three indexes, reciprocal-rank fusion, trigger-sensitive LLM calls, neighbors, packing, answers, evaluations and GUI all consume the same plan. This is a useful representative application rather than a single-index toy.
- pkg/ragjs/src/graph/graph.js, finalize/whenRefs/applyOps: current compiler checks data ports, uses, decisions and signals through different logic. It rejects missing decision nodes. Rename updates inputs and outputs but not when.decision; remove guards declared uses and rewires inputs but does not establish a decision replacement policy. Lambda reference scanning is heuristic.
- pkg/ragjs/src/engine/engine.js, digests/build/tryDelta/resolveBakes/artifacts/buildShared/gc: build digests propagate inputs; baked key hashes document ID/section counts and chunk length; bake receives raw output. Artifacts collapses chunk arrays into a Map and separately collects views; unionChunks gives map records priority then the first matching view. Primary stream defines order. Shared build key uses digests and reuses the first build's opts. gc closes previous retired handles without query counts.
- pkg/ragjs/src/engine/records.js, Lazy/isView/asArray/unionChunks: public consumers can distinguish __records and require array conversion. Mixed precedence does not preserve graph stream order.
- pkg/ragjs/src/engine/session.js, Session.run/ctx/decorateResult: captures art before awaited execution, emits bare-ID hit/context records, and result.chunk/top read art later. No snapshot lease protects this capture.
- pkg/runner/serve.go, embedded __serve: refresh builds and collect runs gc on artifact changes. __serve.run awaits session then collects and fetches art() again before packing output/evidence/neighbor bodies. Fixing only Session lifetime would leave a packaging generation mismatch.
- pkg/natives/persist.go, Persist.InstallEnv: env.AddCloser may be replaced by no-op; loading records/records2 registers reader closers. Delta output is saved through a separate route.
- pkg/natives/markdown_native.go delta callbacks call ParseDelta/ChunkDelta and wrap returned records; pkg/natives/delta.go eventually OpenRecords2(dir). This construction route lacks the runtime closer registration visible in persistent loading.
- pkg/artifacts/lineage.go, LineageRecord/LineageList/GC: keeps 32 entries, searches 8, GC traverses lineage entries to discover outputs. Entries dropped before GC cease being inventory. Existing hard-link accounting uses link counts. pkg/artifacts/store.go List skips directories containing a dot, including the walked root.
- pkg/natives/rerank.go, rerankAsync: deferred cancel runs on worker exit; posted settle callback later inspects callCtx.Err, allowing cleanup to change failure classification. pkg/ragjs/sample.go and decide.go classify before posting, a useful safeguard to preserve. Sample tools post request/reply work to the VM owner.
- pkg/ragjs/src/engine/scope.ts: request options capture synchronous currentScope before await; close disables events, abort changes cancellation state. They are different operations.
- pkg/server/sse.go: bounded event-count queue and 100ms full-queue cancellation already exist. pump ignores write errors, Flush has no error return and there is no per-write deadline. Text coalescing can grow independent of queue entry count. pkg/server/rag.go handleRun and verbs.go handleVerb each create/join pumps; blocked writes prevent returning/releasing admission capacity.
- pkg/llm/gepp/backend.go, Backend.preferred: empty enabled list permits default but fallback loops only that list. With unavailable/missing default and eligible models, it returns none. Sorting eligible references is a proposed deterministic fix, not existing behavior.
- pkg/ragjs/src/export/html.js, exportHtml: removes submit-triggered live non-answer nodes, then answer/context; embeds engine.files. This does not express capabilities or guarantee server native sources are portable.

## Tests inspected, not run

pkg/runner/records_test.go checks cold/warm records, order, neighbors and filtering but assumes one chunk stream and explicitly asserts different storage representations. Preserve the semantic assertions and replace representation assertions. pkg/runner/delta_test.go compares full and incremental builds across edits/add/remove/rename/touch/empty files and checks original stored files stay unchanged. Other discovered useful suites are serve_watch_test.go (incremental refresh and ignore), lineage_test.go (recording), server verb/query integration, native rerank batching. No inspected suite establishes all proposed lifecycle invariants.

## Decisions the guide must not silently invent

- Dataset version hashing/canonicalization and captured callback configuration need an exact specification. Record IDs must distinguish source corpus plus relative path; references also carry dataset version.
- Separate stable publication slot identity from full versioned job identity so background refresh can serve the last good snapshot. This clarifies an underspecified original buildKey sketch; label it a proposed refinement.
- Choose duplicate-adoption behavior and define who closes a fresh handle if adoption fails. Prefer reuse plus immediate close of redundant newly opened handle; ownership transfers only on successful adoption.
- Initial writable-store deployment is a single process with an OS-backed namespace lock. Exact lock and fsync support must be selected for supported platforms, not assumed from atomic rename.
- Retention count, stale-generation HTTP shape, result/trace byte limits and transport/join deadlines need explicit settings. They do not require new frameworks; generation-safe stale refusal is a valid first policy.
- Terminal outcome/cancellation arbitration must happen once before deferred cleanup; runtime closure may prevent JS delivery but must not skip resource/task cleanup.
- Cross-dataset fusion needs an explicit equivalence/canonicalization component when exact references are insufficient. Do not restore implicit last-stream wins.

The original's twelve findings remain required acceptance items. No original review thread, external PR content, sibling guide or prior experiment was consulted. No implementation edits are authorized.
