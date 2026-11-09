# Author review notes on first-draft.md

Whole-draft reread by the author, before revising. This is not an independent fresh-context review: I wrote the draft and already know the source, so prose gaps that a newcomer would hit may be invisible to me. Code checks below are targeted rereads of files already inspected.

## Accuracy errors

1. **§3.2, union precedence paragraph.** The draft contains a visible self-correction ("— wait, the reverse") and never states the outcome cleanly. Reader cannot tell which record wins. Code (`unionChunks`, `Engine.artifacts`): arrays go into a `Map` where later nodes overwrite; `get` consults the Map before any view; among views, `reduce(hit ?? v.get(id))` makes the *first* view win. Correction: give a small table for the running example — both cold → enriched; `chunk` cold + `enrich` stored → raw; `chunk` stored + `enrich` cold → enriched; both stored → raw. Code clarifies: yes.

2. **§7.3, dataset identity.** Draft says dataset ID = store key when stored, digest when memory-only. That makes the same output's references differ between cold and warm runs, violating invariant 2 that the section itself introduces. Correction: dataset ID is always the build digest (which already includes component source and input digests, `Engine.digests`); the store key is a storage address. Code clarifies: yes (`storeKey = hash([d, def.src])`, `digests` already hashes `def.src`).

3. **§8.1, "build key".** Draft defines the snapshot key as the build-node digests plus options, then talks about the "current snapshot per build key". Digests change whenever a file changes, so every refresh would create a new key and there would never be a "current" to replace. Two identities are conflated: (a) a stable *slot* for "this plan's build subgraph" (like `Engine.lineages`, independent of file contents) that has a current snapshot; (b) a content *build identity* (digests + semantic options) used to share jobs and name snapshots. Correction: define both explicitly in §8.1 and use them consistently in §8.3, §9 (current pointer per slot, retention per slot) and §10.2 (job key = build identity). Code clarifies: partly (lineage keys show a stable key exists; the slot definition is a proposal).

4. **§4 step 4 / §3.10, slot leakage.** The running example uses the direct query endpoint, but the slot leak described exists only for verbs (`handleRun` acquires no slot). Reader would think `/run` leaks slots. Correction: say a stalled `/run` stream leaks its handler goroutine and keeps the query's runtime work alive, and a stalled verb additionally keeps its in-flight slot; eight of them make verbs answer `503 busy`. Code clarifies: yes.

5. **§9.1, `deps`.** The paragraph is tangled and half-argues that deps are empty. Code: `CowRecords2` hard-links shard files into a new self-contained directory; Bleve indexes are relocated as self-contained directories; JSON/bin blobs are standalone. So no current payload needs another payload to be readable. Correction: state that manifests list every artifact key directly; descriptor `deps` from the original design is retained as a requirement only for a codec that references another payload; phase 4 must check each codec (notably `vectors`, whose loader binds data via `h.restore`, which I did not trace) and add the field only if needed. Make the deviation from the original explicit. Code clarifies: partly.

6. **§11.2 bounded join.** Draft claims capacity returns after "one write timeout plus cancellation latency". That depends on `ServedApp.Run`/`Invoke` returning after the scope abort, i.e. on the JS run actually stopping and `ragjs.Eval` returning. I did not verify `ragjs.Eval`'s behavior while a promise is pending after context cancellation. Correction: state the dependency and make it an explicit phase-6 test assertion rather than a claim.

## Missing information / likely confusion

7. **§8.4 registry ↔ JS.** The Go registry API is shown, but how JavaScript snapshots retain/release Go resources is not. Reader must guess how a `StoredDataset` knows its resource ID and how publication hands resources from the build scope to the snapshot without a close in between. Correction: add the `ResourceHost` TS interface, say Go-opened handles carry their resource ID, and spell out the handoff: snapshot retains every resource, then the build scope releases; on publish failure the scope keeps ownership and releases on abort.

9. **§7.2 fusion default.** "Keep both entries" silently yields duplicated evidence text in `pack`. A reader would ask why this is better than today. Better and still simple: when `fuse` inputs resolve to *different* chunk datasets, compilation requires an explicit `canonical` option (the compiler knows each retrieve's index and each index's input node). Same-dataset fusion needs nothing. Update D3. Code clarifies: the compiler has the data (`uses` → index node → `in`).

10. **§8.6 / D7 retention conflation.** D7 mixes in-memory retention for stale lookups and on-disk retention for history. Split into D7a (memory: no retired snapshot kept beyond leases → shell gets `410` after a refresh and must rerun) and D7b (disk: retained manifests per slot). State the user-visible consequence of D7a.

11. **§9.2 Prune.** `store prune` deletes other namespaces wholesale (`artifacts.Prune`). It must take each namespace's lock before deleting, or it can delete a namespace a server holds. Missing from draft.

12. **§9.3 when artifacts become durable.** The publication list reads as if all payloads are written at the end. Today each node saves when it completes (`Engine.build` calls `store.save` per node). Clarify: steps 1–4 happen per node as it finishes (protected by the build scope), steps 5–6 once at the end.

13. **§10.2 refresh cancellation.** Draft says a background owner keeps the job alive but not who can cancel it. `watchApp` passes a 10-minute context to `Refresh`, but that context is not connected to the JS build. Correction: the refresh operation's context becomes the background owner's signal; engine shutdown cancels it too.

14. **Session memo.** `Session.memo` (live-node output cache) is invalidated by `art.key`; draft never says what replaces it. Correction: key it by snapshot ID. One sentence.

15. **Partial builds (D12)** are introduced only in the decisions table. A reader meets `engine.partial` for the first time there. Move a one-line explanation into §3.1 and reference it in §8.1.

16. **Rerank batches use `sync.WaitGroup`**; original asked for errgroup for related goroutines. Add to §10.1 as guidance inside `Work`.

## Original requirements dropped or weakened

17. Original: "Do not tie the retention count to query duration." Missing; add to §9.4.
18. Original: result over budget → clear limit error *or explicit pagination contract*. Draft picks only the error; mention pagination as the rejected alternative in D6.
19. Original: Biome/TypeScript do not prove runtime validity or lifetime safety; keep lifecycle tests. Add one sentence in §6.2 or §16.
20. Original: browser and Goja load one deterministically generated implementation (done by 010). Draft mentions it in §1 and §13; sufficient.

## Structure / repetition

21. §5 table and §3 repeat file references; acceptable because §5 is the index the implementer returns to. Keep but shorten the "Evidence" column where §3 already explains.
22. §15 "Start here" is buried at the end of the phase list; move the start command to the end of §1 or into a short "Where to start" under §15 heading — keep in §15 but as its own short paragraph before the phases.
23. §13 claim "older records codec" should say why it is a candidate: `Persist.load` reads codec `records` but `save` only writes `records2` for chunks/docs. State it as an observation to confirm.

## Checks that passed on reread

- §3.8 rerank/sample/decide classification order matches `rerank.go` L145–170, `sample.go` L146–179, `decide.go` L50–72.
- §3.7 lineage constants and `GC` behavior match `lineage.go`.
- §3.10 `handleRun` has no `acquire` — confirmed in `rag.go`.
- §9.5 `List` root-dot skip matches `store.go` L282.
- §5 finding 10 matches `backend.go` `preferred`.
