# Review notes on first-draft.md

These come from rereading the whole first draft as if it were the reader's only explanation, plus targeted source checks. This is the author's own second pass. It is not an independent fresh-context review.

Each note gives the passage, the problem, its type (C = comprehension gap, T = technical correction, M = material omitted from the original), the proposed correction, and whether code settles it.

## Accuracy problems in the main argument

1. **§3.2 running example does not exhibit the bug (T, serious).** The draft says `chunk` → `enrich` gives different winners cold and warm. In source, `enrich` is a JS `map` component whose output is a plain array. `Persist.save` only converts Go-handle arrays (`chunksHandle`/`docsHandle`) to records2. Other values go through `encodeArtifact` → JSON, and JSON loads back as an array. Warm, then, `enrich` is an array and `chunk` is a view. `unionChunks` prefers arrays, so enrich still wins, which is the same as cold. *Code clarifies:* yes (`persist.go` save/load, `records.js` unionChunks). *Correction:* explain the rule exactly: cold, the later node wins; stored, arrays beat views and among views the first wins. Give a 2×2 table for two chunk nodes A (earlier) and B (later). State that enrich-after-sections agrees today only because of which codec each component happens to use. Keep enrich as the provenance example: two datasets share ids, and hits must say which one they came from. Do not claim it reproduces finding 1.

2. **§5.3 "dataset id is content-derived" (T).** `source.dir`'s digest covers each file's relative path, size and mtime (`sourcedir.go` comment at line 29 and `digest`), not its content. `source.files` hashes the text. *Correction:* say the id changes whenever an input file's path, size or mtime changes (or its text, for `source.files`). That is enough to fix the equal-count stale-bake case. Do not claim content hashing.

3. **§5.1 "freshly decoded (disk)" records (T).** The RecordView caches decoded objects (up to 4096), so a decoded object is shared between readers. *Correction:* freeze records in both implementations, or detach them at the callback boundary.

4. **§3.6 SSE failure (T, strengthen).** Verified that `ragwb serve` creates `http.Server{ReadHeaderTimeout: 10s}` with no `WriteTimeout` (`cmd/ragwb/cmds/serve.go:130`), so nothing outside the pump bounds the blocked write. Add this. It explains why the hang is indefinite rather than a long timeout.

5. **§3.3 delta readers (T, precision).** `Records2.Close` is idempotent (`records2.go:650`), so the risk is a leak until the engine GC happens to retire the reader. Unclosed readers at runtime shutdown are also possible. It is not a double close. Say so.

6. **§2.1 example (T).** `.limit()` exists as the `limit` live component. Keep it. The original's `.filter.where()` was dropped; that is fine.

## Comprehension gaps

7. **§6.1 snapshot and change detection (C, blocking decision hidden).** The draft says `builds.ensure` waits only when no snapshot exists. Today, though, `Session.run` rebuilds whenever `artifacts(ir)` is null, which is how a query notices changed sources. The draft silently changes when users see new content. *Correction:* explain that "current" now changes only when a build job publishes. Add a decision on freshness: one-shot library `rag.run` (browser, CLI) waits for a fresh build; a served app answers from the last good snapshot and starts or joins a background refresh. Label it as a proposed behavior change.

8. **§6.1 "buildKey" and "generation" undefined (C).** The reader will ask which thing `plan.buildKey` is and how snapshot id and generation relate. *Correction:* define build identity as the set of build-node digests plus semantic build options. Use it as both the job key and the snapshot "current pointer" key. Define the snapshot id as a hash of its artifact ids. Drop "generation number" as a separate concept, or define it as display-only. Show one small snapshot record.

9. **§6.2 where the registry lives relative to JS snapshots (C).** It is unclear how a JS snapshot "retains" a Go registry entry, and what happens in the browser. *Correction:* one Go registry per runtime, with refcounted entries. JS holds opaque handle objects; only the snapshot and builder modules call `retain`/`release`. A mutex is needed because shutdown runs off the owner goroutine. The browser has no native resources, so its datasets need no registry entries. Also cover the delta base reader opened only for a delta: it is adopted into the builder scope and released at commit.

10. **§6.3 result shape and lookups (C).** The draft does not show what the run JSON looks like with refs, what a "neighbor preview" is, or what happens to GUI ←/→ navigation after the old snapshot is released. *Correction:* show a JSON response sketch with `records` keyed by dataset then id. Define the neighbor preview as today's prev/next bodies for the top 40 hits (`NB_HITS`). Propose that the server keeps the previous snapshot resolvable (retention of one) and otherwise answers `stale_snapshot`, and that the shell clears its cache on a snapshot change. Add the original's points: text already delivered cannot be revoked, and retention is not access control.

11. **§5.2 fuse rule and custom hits (C, T).** The compile-time dataset rule fails for `retrieve` with a lambda and for `rag.define`d components that return hits, because their dataset is not static. *Correction:* custom hit producers return refs built with `ctx.ref(nodeId, id)`, or declare `over: 'nodeId'`. A bare string id from a custom producer is rejected. Also, "drop with a trace note" conflicts with the rule against silently omitted records. Propose a compile-time requirement plus a runtime error when a re-pointed id is missing; mark dropping as the rejected alternative.

12. **§5.3 which datasets a bake sees (C, underspecified).** Today a bake gets "the first docs node and the first chunks node". That is an implicit primary dataset, the same kind of heuristic the design removes. *Correction:* propose that the bake context exposes the corpus's documents dataset and the corpus's final chunk dataset (the one `.index()` consumes), plus `ctx.dataset(nodeId)` for explicit choice. Mark as a decision.

13. **§8.1 race rule lacks an arbiter (C).** The draft says "a result classified before cancellation is observed stays terminal" but not who decides. *Correction:* each operation has one state cell set by compare-and-swap. The worker sets it on result; the abort listener (on the owner) sets it on cancel. The first to succeed wins, and settlement posts that value. Give the rerank example: a failure classified as failure before `cancel()` runs stays a failure.

14. **§8.2 job key and options (C, T).** Today `buildShared`'s key is digests only, so a `force` or `calls` build could join a normal one. *Correction:* the key includes semantic options (`force`, `full`, `calls`, `persist`). Also say who publishes: the job, once, on success. Say what a "refresh owner" is: the watch or refresh operation, which keeps the job alive with no waiters.

15. **§10.1 references API not shown concretely (C).** *Correction:* show `references(node)` output for a `retrieve` node with a `when`. Mention that index names in `p.index` are references (`uses`), so renaming `ix-text` must update them. Note that signal reads stay name-based and are not renamed by ops.

16. **§7 temp directories (C, simplification).** With an exclusive lock, the "older than an hour" heuristic for `.tmp-*` is unnecessary: under the lock, any staging directory not owned by a live builder scope is abandoned. Mention this.

17. **§15 open decisions are a bare list (C).** The guidance asks for compact decision records with recommendation and status, separating blocking from ordinary choices. *Correction:* replace the list with a decision table (context, recommendation, alternatives, blocking?), and keep the prose rationale where each subject is explained.

## Material from the original that the draft dropped (M)

18. Original §16, TypeScript: new contracts in strict TypeScript, discriminated unions for outcomes and provenance, declarations kept beside implementations, no declaration layer over old forks. Biome and TS do not prove runtime validity or lifetime safety, so the lifecycle tests stay. `pnpm check` content. Add a short section plus a current-module navigation table (records.js, engine.js, session.js, scope.ts, graph.js, export/html.js, api/index.js, components/).
19. The original lists what the design deliberately retains: the DSL, the shared JS model, Markdown/Bleve/terms/incremental algorithms, providers, evaluation cases. Add briefly in §1.
20. The original's §9.3 note that an already-exceeded write deadline cannot be extended, so the stream terminates after it. Also that wrappers must support the `ResponseController` unwrap path. Add.
21. Original §11: remove obsolete record formats/codecs (the older `records` codec still loaded by `persist.go`) only after enumerating live references. Add.
22. Original §13: the PR stack is unmerged; choose one integration branch and update the stack coherently. Creating the design implies no force push, merge or thread resolution. Add one sentence.
23. Original §14: do not claim performance parity from unit tests, because materialization and provenance change allocation. A regression calls for investigation, not automatic abandonment. Add.
24. Original §8.3: "the same owner serializes publication and mark/sweep" is present. Keep it. Also "Do not tie the retention count to query duration". Add.
25. Original §9.1: errgroup for related goroutines (rerank batches), and detaching abort listeners. The listener point is present; errgroup is missing. Add.
26. Original §12: "Expose a deliberate reference constructor if low-level callers need one." Covered by `ctx.ref` (note 11).

## Structure and repetition

27. §4.2 invariants partly repeat §5–9. Keep the list because it is a useful review checklist, but make each item point to its section.
28. §13 phases lack entry points. Add the key file per phase so an intern can start.
29. §11 deletion list and §13 overlap slightly. Acceptable; keep the deletion list as the reviewable "done" criterion.
30. The ending (§15) currently trails off. Close with the questions that should each have one answer: is this an array, who closes this, whose cancellation controls this build, which snapshot does this hit belong to, which references does a rename update.

## Checked and fine
- Timeline in §3.3 matches `Engine.gc` and the serve harness `collect()`. Refresh can run while a query awaits, because the owner is free during an await.
- §3.4 GC/lineage description matches `lineage.go`.
- §3.5 rerank race matches `rerank.go` (the deferred `cancel` runs before the posted settle executes).
- §3.7 `List` and `preferred` bugs match the source.
