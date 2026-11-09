# Review notes on first-draft.md

Method: one complete reread of `first-draft.md` as if it were the reader's only explanation, followed by targeted source checks for claims that carry the argument. This is the author's own second pass, not an independent fresh-context review. No tests were run.

Legend: **C** = comprehension gap (reader cannot follow), **T** = technical/accuracy correction, **S** = structure/scope (missing original requirement, repetition). "Code" says whether source reading resolves the point.

## Comprehension gaps

| # | Passage | Reader question / problem | Proposed correction | Code |
|---|---|---|---|---|
| C1 | §2.3, §3E, §11.2 use "submit-triggered" | "What is a trigger?" The export and finding-11 explanation depend on it, but it is never introduced. | In §2.3 explain `trigger`: `change` nodes run as the user types; `submit` nodes (LLM, decision, `rerank.model`) run only on an explicit submit. | Yes: `session.js` `wants = trigger === 'submit' \|\| triggerOf(n) === 'change'`; `plans.js` comments. |
| C2 | §2.4 "verb endpoints" | "What is a verb?" | One sentence: functions an `app.js` declares as named HTTP operations (jsverbs); they may run other plans on the same engine. | Yes: `serve.go` `__ragwbWrapVerb`, `verbs.go`. |
| C3 | §5.2 shows `snapshot.resolve(ref)` only | "What do component authors call instead of `ctx.chunk(id)`, `ctx.neighbors`, `ctx.chunks()`?" The custom-component contract change is invisible. | Add the live-context API: `ctx.resolve(ref)`, `ctx.neighbors(ref)`, `ctx.dataset(name)`; `ctx.chunks()` without a dataset is removed. | Yes: `session.js` `ctx()` lists today's methods. |
| C4 | §6.1 "build identity … like lineage keys" | Reader would assume lineage keys exist for every node; they don't (`source.files` → null). The definition is unusable as written. | Define build identity directly: hash of build nodes' (id, type, normalised params, input build identities, component source), excluding file contents and digest hooks. Always defined. | Yes: `engine.js` `lineages` sets `extra = null` for `source.files`. |
| C5 | §6.1/§6.3 two kinds of counting | "Is the lease count the same as the registry owner count?" JS snapshots and Go handles are mixed. | State the two levels explicitly: leases count users of a JS snapshot; registry entries count owning snapshots/build scopes. Show `Registry.Release(owner)`. Mention memory backend has nothing to close. | Design clarification. |
| C6 | §6.1 sketch: `await snapshots.acquire(...)` while claiming "acquire-and-increment is one step" | Looks contradictory. | Explain: any wait (cold build) happens first; the lease is taken synchronously at the moment a snapshot is returned. A cancelled request detaches from the build job (§8). | Design clarification. |
| C7 | §6.2 freshness rule | Reader in the browser workbench: "I edited a note and ran; why are results old?" The draft silently changes interactive semantics. | Turn into an explicit choice with a `freshness` option: `'current'` (wait for a build of current sources; default for `rag.run` and browser) vs `'published'` (lease last good snapshot and refresh in background; used by `serve`). Record as a decision. | Yes: `artifacts(ir)` returns null on any digest change; `Session.run` then builds. |
| C8 | §6.4 materialisation | "What does a materialised result look like?" and "what happens to `serve.go`'s `pack`?" | Add a small JSON example of `{snapshot, hits:[{ref}], records}` and state that serve's chunk/neighbour packing (`NB_HITS = 40`) moves into `materializeResult`. | Yes: `serve.go` `pack`, `NB_HITS`. |
| C9 | §8 job key | "Is a job per build identity? Then two different source states join one job?" | Key = build identity + source digests + result-affecting options (today's key is the digest set). Explain that a background refresh is a waiter that does not cancel by itself. | Yes: `buildShared` key = joined digests. |
| C10 | §9 sketch | Sample needs tool calls and streaming; the sketch has no hook for tool request/reply; the race "arbiter" isn't named. | Add an `OwnerCall` hook to the spec sketch; name the arbiter (one atomic state per operation: running → settled/cancelled, first transition wins). Use rerank as the concrete before/after. | Yes: `sample.go` `exec` posts tool calls. |
| C11 | §10 "join with a bound … release the slot" | "If the operation does not stop within the bound, is the slot released anyway?" | Decide: the handler returns after the bound, but the slot belongs to the operation and is released when it settles, so a stuck operation shows as busy instead of overcommitting. | Design choice. |
| C12 | §11.1 canonical IR `when: { ref: 1 }` | Index into an array is hard to read. | Use original shape `when: { source: { kind: 'decision', node, output }, min }` and `index: { kind: 'artifact', node: 'ix-text' }`; show how a component declares reference params. | — |
| C13 | §11.2 export | "What does the exported `adaptive` page actually contain?" | Give before/after node list for `adaptive`. Say what is bundled: the source records the snapshot read (name + text), so the page runs the same corpus pipeline. | Yes: `plans.js` adaptive; `html.js`. |
| C14 | §1 and §3 use first person ("I say so", "my status") | Guide should describe the system, not the author. | Neutral wording; define "confirmed" vs "inferred" and say that none were reproduced by running tests. | — |

## Technical corrections

| # | Passage | Problem | Correction | Code |
|---|---|---|---|---|
| T1 | §3A enrich example table | **Wrong.** `enrich` is a JS `map` component; its output is a plain JS array saved as JSON and loads back as an array, while `chunk.sections` (Go) output loads as a RecordView. Warm start: Map holds enriched (array) → enriched wins; cold: enriched wins. They agree in that case. | Restate the actual rule (computed/JSON arrays beat stored views regardless of order; among arrays the later stream wins; among views the earlier wins) and use a correct example: two streams that share ids where at least one is records-backed — the winner changes with storage. Note that which codec a stream uses depends on its component (Go-backed → records2; JS → JSON array), which is itself the problem. | Yes: `persist.go` `save` (`chunksHandle` only for Go-backed), `records.js` `unionChunks`, `artifacts` loop. |
| T2 | §2.2 "Docs and chunks are saved as records2" | Only Go-backed outputs; JS arrays are saved as JSON. | Correct wording. | Yes: `persist.go` `save`. |
| T3 | §3A bake `.map` claim | Overstated certainty. `synonyms` is not persisted (`out: 'value'`), so on a warm start it recomputes with a stored docs input; the key expression calls `docs.map`, which RecordView lacks. Not run. | Keep, labelled as source deduction; the regression test establishes the actual behaviour. | Partly: `builtins.js` `synonyms`; `resolveBakes`. |
| T4 | §5.1 `Index.search(...): Hit[]` | Vector retrieval needs an embedding call; a synchronous signature is wrong and an unnecessary change. | Do not redefine search; require only that an index records its dataset and every hit it returns carries `ref`. | Partly: vector index needs embedder (`natives/vector.go`). |
| T5 | §5.3 dataset id = "store key" | Browser has no store. | Say the key is the pure hash `storeKey(digest, def)` (`records.js`), computable with or without a store. | Yes. |
| T6 | §5.2 document id `sourceId + ':' + path` | `sourceId` undefined. | Define: the source node's id within the corpus (e.g. `reports/corpus:notes/oauth.md`). Note chunk ids derive from doc ids and will change; fixtures update. | Partly. |
| T7 | §6.3 `Adopt` duplicate case | "c is closed" but caller must then use the existing handle; sketch doesn't say. | Return the existing handle explicitly. | — |
| T8 | §7.1 vs §12 dotted root | Draft says the store redesign fixes it "as a side effect" and also lists it as a local fix — inconsistent sequencing. | Fix locally in phase 1 with its test; phase 4's layout-based traversal must keep the test passing. | Yes: `store.go` `List`. |
| T9 | §7.2 lock consequences | Missing: a one-shot CLI run with `--store` is also a writer and will now fail while `serve` owns the namespace. | State the consequence and the error message expectation (names the owner). | Inferred from `cmd/ragwb/cmds/store.go`, runtime factory. |
| T10 | §7.3 "checksums" | Not established that codecs carry checksums. | "counts and codec metadata; checksums where a codec records them". | Not inspected in detail. |
| T11 | §4 rules | Dropped original invariant "new abstractions replace old paths in the same phase". | Restore as rule 11. | — |
| T12 | §9 | `decide` currently maps `DeadlineExceeded` to `cancelled`; draft mentions it but the outcome table should make deadline its own kind. | Keep; tie it to the outcome kinds explicitly. | Yes: `decide.go:103`. |

## Missing original requirements / structure

| # | Missing or weak | Action |
|---|---|---|
| S1 | Migration: bump serialized IR and artifact namespace versions; old stores require explicit cleanup; never silently delete user directories; examples/fixtures updated in the same commit as each API cutover; no legacy reader. | Add a migration section. |
| S2 | Remove obsolete record formats (`records` / `ChunkRecords` codec still loaded in `persist.go`) only after enumerating live references; keep memory algorithms used by browser and quality comparisons. | Add to deletions. |
| S3 | Process notes: choose one integration branch for the cleanup; design ticket implies no force push/merge/thread resolution; commit at meaningful intervals; focused tests during development, full checks at integration boundaries. | Add briefly to implementation section. |
| S4 | Out of scope: distributed indexing, remote dataset backends, multi-process shared stores, full Obsidian rendering, production authentication; reindex status/manual refresh build on operations later. | Add to risks/scope. |
| S5 | 010 relationship: new contracts strict TS with discriminated unions for outcomes and provenance; Biome/TS don't prove lifetime safety; no declaration layer over old forks. | Expand in §13. |
| S6 | Performance: "do not claim parity because unit tests pass; regression → investigate". Test for "new waiter does not join doomed job". | Add to §15. |
| S7 | Decision table (§16) is too thin to be useful and duplicates prose. | Replace with short decision records for the consequential choices (freshness, snapshot key, store ownership, removal policy, cross-dataset fusion); keep open questions where they arise and summarise only blocking ones at the end. |
| S8 | Simplification budget wording ("each new concept retires one responsibility; no component-specific executor branches; no DI frameworks") only in §1. | Restate briefly in §13 next to deletions. |
| S9 | Partial (deferred-LLM) builds and their relation to "publish only complete, validated snapshots" are only an open bullet. | Explain at §6.1: proposal — a snapshot with deferred items is valid and publishable, marked `partial`; validation means structural completeness, not "all LLM work done". Mark as decision needing owner confirmation. |
| S10 | Ending is a list; original's closing "fewer questions with one answer each" helps the reader check the design. | Add a short closing paragraph. |

## Mental execution of key examples

- **Lease timeline (§6.1)**: S2 released at t3 while S3 current — correct only if S2 had zero leases; stated. Handles shared by S1/S2/S3 stay open because registry counts owners — correct with the C5 clarification.
- **Shared job (§8)**: A cancels → detaches; B cancels → cancel unless refresh owner. New waiter during cancellation: waits for settle then starts new job — consistent with C9 key (same sources → same key, but the old job is marked cancelling, so a new one is created).
- **Rerank outcome (§9)**: provider error → Classify on worker → `Failed` fixed → deferred cancel runs → posted settle uses fixed outcome. Correct.
- **Rename `route` → `router` (§11)**: visitor rewrites three `when` sources and nothing else; finalize passes. Correct with C12 shape.
- **Export adaptive (§11.2)**: remove `route`, `rerank.decide`, `pack`, `answer`; `when` on retrieves becomes unconditional; `fuse.rrf`, `filter.where` remain; plan validates. Need to say a `limit` is not present in `adaptive`; hits output becomes last hits node (`filter`). finalize picks `lastOf('hits')` — fine.
- **SSE stall (§10)**: deadline hit → write error → cancel op → bounded join → handler returns; slot released when op settles. Consistent with C11.
