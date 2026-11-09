# Author review of first-draft.md (before revision)

Method: reread the whole first draft as if it were the reader's only explanation, then did targeted source checks where a note depended on a fact. This is the author's own second pass, not an independent fresh-context review; it cannot show that a new reader actually understands the guide.

Format: passage → reader confusion or accuracy gap → proposed correction → does code clarify it?

## A. Accuracy problems

A1. **§2.2 table, `source` (approx.)** → Guessed node id. → `CorpusSpec.from` names the node `files` (graph.js:90). Use `files`. → Code clarifies: yes.

A2. **§4.1 diagram: g6 and g7 share `d:parse@a1`** → Wrong for successive generations of one plan. Any note change changes the source digest, so `parse`, `chunk`, `enrich_kind` and `ix-text` all get new digests. Successive generations of one plan share almost nothing. Sharing actually happens between *different plans over the same corpus* (`lexical` and `fused` in the reports app both use `chunk` / `ix-text`), and between a verb's plan and the app's plan (`recentIrs`). → Redraw with two plans sharing resources, plus a generation change that replaces all of them. Explain when sharing happens. → Code clarifies: yes (digests() chains input digests).

A3. **§6.1/§6.3 "per build key" / `plan.buildKey`** → Undefined and contradictory. If the key is the content digest set, each generation has a different key and "current snapshot per key" cannot have a predecessor. Jobs need the *content* key (two requests for the same content share a build); the snapshot table needs a *structure* key that stays fixed when notes change (the role lineage keys play today). → Define two keys: `planKey` (structure: node types/params/wiring, no source contents) selects the snapshot slot; `contentKey` (build digests + semantic options) identifies a job and a snapshot's content. Use them consistently in §6, §7 (manifest path), §8. → Code clarifies partially: `lineages()` already computes a content-independent key per node; `digests()` the content one.

A4. **§8.2 "the Go sink drops on overflow"** → Wrong. `streamSink.Emit` waits up to 100 ms on a full queue *on the owner goroutine*, then cancels its request (sse.go:50-69). So a slow subscriber can stall the runtime once for ≤100 ms, then its request is cancelled — which detaches that waiter. → State the actual behavior, and propose making overflow immediate (no wait on the owner). → Code clarifies: yes.

A5. **§8.4 "admission slot is released when the operation terminates"** → The draft does not say how, and current code works differently: `ragjs.Eval`'s `awaitPromise` returns `ctx.Err()` as soon as the request context ends, without waiting for the JS promise (await.go:60-62). `ServedApp.Run`/`Invoke` therefore return while the JS run, its LLM calls and its snapshot reads may still be in progress, and `handleVerb`'s deferred `release()` frees the slot early. The P1 hang is the pump blocked in `Fprintf`, not the operation. → Explain: (1) the pump is bounded by write deadlines; (2) on cancellation the Go caller must still wait for the JS promise to settle (bounded, since the bridge rejects promptly on abort) before releasing the slot; (3) only then release. This is the "joined operation lifetime" requirement made concrete. → Code clarifies: yes.

A6. **§8.1 "goroutine tracked by the runtime's task group"** → Implies an existing facility. I did not find one; rerank uses a local WaitGroup, sample/decide spawn bare goroutines. → Label as new: a per-runtime task group (errgroup-style) owned by the runtime wrapper that shutdown waits on. → Code clarifies: yes (absence).

A7. **§9.1 canonical IR shows both a `refs` array and the `when.source` field** → Duplicates the decision reference, so a rename would have two copies to keep in sync — the problem the section claims to remove. → Show canonical fields only (`index: { node: 'ix-text' }`, `when.source`), and say `references(node)` is *computed* from the component's declared reference parameters. → Design issue, not code.

A8. **§12 "Old stores: the namespace changes automatically"** → True for the asset (runtime.go:77-94), but the original also requires bumping the serialized IR version. IR has `version: 1` (graph.js:559). → Bump to 2 and reject other versions when loading saved plans. → Code clarifies: yes.

A9. **§8.1 immediate rejection on abort** → Missing consequence: after the promise rejects, a still-running `sample` worker may post tool-execution requests to the owner. → Tool callbacks must check the operation's settled state and reply with an error without running the tool. → Code: sample.go:114-145 posts tool calls unconditionally.

## B. Missing information the reader would need

B1. **§3 lists findings in an order different from their numbering** → Reader cannot quickly find F7 or map to PRs. → Add a compact index table at the top of §3 (F#, PR, priority, section, confirmed/deduced).

B2. **§4.1 Snapshot contents** → Only datasets/indexes are listed, but §9.2 later needs the `files` output and the engine also has `synonyms` and baked values. → Define a snapshot as holding every build output of the plan: datasets, indexes and other values (files, synonyms, baked values).

B3. **§6.2 lease (JS) vs owner count (Go)** → Two counters in two languages, not explained. → One paragraph: the JS snapshot holds `Handle` objects; publish calls `Retain` once per handle per snapshot; snapshot release calls `Release`. Leases are a JS integer on the snapshot. Query code never touches handles.

B4. **§6.2 registry ID** → "store key, or unique id for temp outputs" leaves open what happens when a fresh Bleve index is saved and relocated (persist.go:136-149). → The entry keeps its id for the life of the handle; dedup by store key applies only to handles opened from the store.

B5. **§6.4 consequence of "current or still leased"** → After a refresh, any lookup for the previous generation fails even milliseconds after the query returned. Reader may think this is a bug. → State it as the intended simple rule and the shell's response (re-run). Note the neighbor preview reduces how often it occurs.

B6. **§7.2 browser memory store** → Original requires the browser memory store to implement the same logical store contract without locking. Dropped. → Restore one sentence.

B7. **§11 obsolete codecs** → Original: remove obsolete record formats/codecs only after enumerating live references; keep memory algorithms used by the browser and quality comparisons. Dropped. → Restore, naming the `records` (v1) path in persist.go:200-217 as the likely candidate to verify.

B8. **§8.4 terminal-error vocabulary** → Original requires one vocabulary for both endpoints. Not listed. → Name the codes in use today (`busy`, `bad_request`, `bad_params`, `run_failed`, `verb_failed`, `slow_client`) and propose one set for both: `busy`, `bad_request`, `bad_params`, `failed`, `cancelled`, `slow_client`.

B9. **§13 phase 2 depends on `snapshot.resolve` from phase 3** → Ordering looks circular. → Clarify: in phase 2 `artifacts()` becomes a map of datasets by id and `resolve(ref)` lives there; phase 3 adds leases and lifetimes to that same object.

B10. **§5.2 `ctx.dataset(nodeId)`** → Introduced only in §12. → Define in §5.2 with the other component context methods (`resolve`, `dataset`, `index`), and what `ctx.neighbors(ref)` becomes.

B11. **§8.3 D3 requires the structure key** → With A3 fixed, explain that under "use current snapshot" the query acquires the slot's current snapshot even if its contentKey is older than the latest sources.

B12. **§4.1 Resource row "Identity: store key"** but §4.1 dataset identity = build digest → Reader may confuse the two. → One sentence: dataset id is what references carry; store key = hash(digest, component source) is where its bytes live; a memory dataset has no store key.

## C. Clarity and structure

C1. **§1 is long before the reader meets the system** → Status/compatibility paragraphs are fine but "What stays" can be shorter. Acceptable; keep, trim slightly.

C2. **§6.1 F4 sequence uses `r2`, defined in §4.1 under g7** → Labels inconsistent across diagrams. → Rewrite with the revised §4.1 example (g6 → g7 → g8, resources named per generation).

C3. **§2.6 "the last eight plans verbs ran"** → Imprecise: `noteIr` keeps the eight most recently run plans of any caller (engine.js:685-690). → Fix wording.

C4. **§8.1 sketch is generic over T with Encode** → Fine, but say why bytes: the result crosses goroutines and must not be a Goja value.

C5. **Repetition** → Invariants (§4.2) and risks (§15) partly restate §6. Keep invariants (they are the review rules); shorten risks to items not already stated.

## D. Checks against the original's material requirements

Present: dataset contract, sync access, frozen records, provenance/fusion policy, document identity across sources, index identity, bake cache keys, conformance suite, snapshots/leases/atomic acquisition, private build scope, no publish on failure, registry and no no-op fallback, duplicate adoption, shutdown order, detached results with limits, generation-aware lookups and browser cache, descriptors/manifests/pointer, lineage derived, layout traversal, single-owner lock, crash steps and fsync, GC mark/sweep and abort on corrupt metadata, hard links, bytes reporting, retention/privacy, bridge lifecycle and race rule, tool hook, shared jobs semantics, event fan-out with ids, single SSE writer and deadlines, ResponseController, queue/byte bounds, real-socket test, admission scope, canonical refs and visitor, rename/remove policy, export projection and portable data, lambda bindings, module split, simplification budget, migration, phases, tests, measurements, completion criteria, scope boundaries, strict TS and discriminated unions.

Missing or weakened (to restore): B6 browser store, B7 codec removal rule, B8 vocabulary, A8 IR version, original note that `ResponseController` requires wrappers to support `Unwrap` (add to §8.4), original's "do not tie retention count to query duration" (add to O4 text), original's "background refresh must not block queries" (now D3 — keep as an open decision but say the original asked for it).

## E. Things the code does not settle (stay open in the guide)

D2 fusion default, D3 freshness, O1 partial outputs, O2 result limit, O3 stale status code, O4 retention, O5 CLI under lock, O6 non-Unix lock, O7 codec path dependencies. Add O8: whether `Eval` should wait for promise settlement on cancellation globally or only for request operations (A5) — proposal: always, with a bound, since every caller otherwise misreports when work ended.
