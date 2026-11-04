---
Title: Investigation diary
Ticket: RAGWB-003
Status: active
Topics:
    - rag
    - backend
    - search
    - goja
DocType: reference
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: "Chronological record of RAGWB-003 (incremental re-indexing): design, implementation phases, measurements and failures."
LastUpdated: 2026-11-03T19:30:09.805443735-05:00
WhatFor: ""
WhenToUse: ""
---

# Investigation diary

## Goal

<!-- What is the purpose of this reference document? -->

## Context

<!-- Provide background context needed to use this reference -->

## Quick Reference

<!-- Provide copy/paste-ready content, API contracts, or quick-look tables -->

## Usage Examples

<!-- Show how to use this reference in practice -->

## Related

<!-- Link to related documents or resources -->

# Investigation diary

## Step 1: ticket and intern guide for incremental re-indexing; two identity findings measured

The user asked for a ticket and an intern-level design guide for incremental re-indexing, after learning that editing one note means a full ~28 s rebuild. The guide designs delta builds over the immutable artifact store:
- stable per-file identity;
- source manifests;
- lineage lookup of the previous output;
- a delta protocol in `Engine.build`;
- copy-on-write artifacts: sharded record stores and cloned bleve segments.

While reading the code for the guide, I found and measured an existing identity problem. Doc ids collide: 271 of 2,287 vault files share an id, all through the 64-character slug cut. Chunk-id suffixes and `ord` are also assigned over the whole corpus. Both block incremental builds, so "stable identity" is Phase 1.

### Prompt Context
**User prompt (verbatim):** "then create a ticket for incremental reindexing. Create  a detailed analysis / design / implementation guide that is for a new intern, explaining all the parts of the system needed to understand what it is, with prose paragraphs and bullet point sand pseudocode and diagrams and api references and file references. It should be very clear and technical. Store in the ticket and the nupload to remarkable."
(It arrived while I was pushing RAGWB-002 / PR #2, uploading the RAGWB-002 guide v3 and closing RAGWB-001.)
**Assistant interpretation:** A new docmgr ticket (RAGWB-003) with a design and implementation guide for an intern, stored in the ticket and uploaded to reMarkable. Design only, no implementation.
**Inferred user intent:** Have a reviewable, implementable plan for making edits cheap, as the next piece of work after RAGWB-002.
**Commit (code):** — (documents and a measurement script).

### What I did
- **Code read for the guide:**
  - `source.dir` (`sourcedir.go`: walk, digest, the persist marker);
  - the markdown natives (`markdown_native.go`: `parse.markdown` and `chunk.sections` are `all` components; `ParseMarkdown` is per file; `ChunkSections` is per doc, followed by a corpus-wide `AssignIDs`);
  - doc ids (`markdown.go:348`, rag.js `:284`) and `slug` (rag.js `:62`, cap 64);
  - `assignIds` (rag.js `:332`) and the one `ord` reader (`pack` order `'doc'`, rag.js `:731`);
  - the Engine build, digests and artifacts paths; records v1 (ordinal keys); the bleve backend; terms; vectors and the embedding cache; serve `--watch`.
- **bleve v2.6.0 source:**
  - postings `Count()` subtracts deleted docs (`zapx/v16 posting.go:235-249`);
  - `DocCount` sums `CountRoot` (`snapshot_index.go:478`);
  - BM25's average length uses `FieldCardinality` (`snapshot_index.go:215`, `search_term.go:83-130`), whose behaviour after deletes is left as a Phase 0 spike.
- **Stored vault index layout:** 16 immutable `*.zap` segments (2–39 MB each) plus `root.bolt` (128 KB), which is what makes hard-link cloning attractive.
- **Measured identity on the stored vault chunks** (a throwaway Go program decoding the RAGWB-002 chunk blob): 85,882 chunks, 2,063 distinct doc ids for 2,282 files, 49 doc ids with more than one title, 18,627 chunk ids with a `-N` suffix.
- **Measured slug collisions and lengths** on the current file list: `scripts/01-doc-id-collisions.py`, a re-implementation of `slug` with parc's include/exclude rules.
  - 2,287 files, 2,068 ids, 271 files sharing an id.
  - **0 true collisions** (equal uncapped slugs).
  - Uncapped lengths: median 88, p90 118, p99 146, max 183.
  - Ids over the cap: 1,911 at 64, 110 at 128, 9 at 160, 0 at 200.
- Created the ticket (RAGWB-003), the guide (design-doc), this diary, six phase tasks, and related files.

### Why
- Incremental builds are only correct if a file's outputs depend on that file alone. Measuring the identity situation first decided Phase 1's rule. The first draft hashed every id over 64 characters, which would have made 84% of vault ids unreadable; the measurement moved the cap to 160 (9 hashed ids).

### What worked
- The design keeps the RAGWB-002 invariants: immutable, content-addressed artifacts; read-only shared opens; "open, don't decode". Incrementality comes from *deriving* new artifacts cheaply (copy-on-write), not from mutating stored ones.

### What didn't work
- **The first measurement program was put in the wrong place.** It wanted to live under `ttmp/` as part of the module, so I placed it in `ttmp/_scratch_idcheck` for one `go run` and deleted it afterwards. (Not committed.)
- **The first draft of §6.2 had a placeholder** (`!COLLIDES`) that contradicted the purely local rule stated right after it. It was replaced before the upload.

### What I learned
- Every doc-id collision on the vault is caused by the 64-character cut. Full path slugs are unique there.
- `ord` is a global position, but its only reader needs within-document order, so making it per-doc costs nothing.
- bleve keeps document frequencies correct under deletion, but the BM25 average length may lag until segments merge. That is the one place where a delta-built index could differ from a fresh one; hence the Phase 0 spike.

### What was tricky to build
- **Defining "equivalent to a full build"** precisely enough to test (§6.9 of the guide). Records must match byte for byte in canonical order, terms must give identical results (hence rebuilding them from canonical rows, D6), and bleve must give the same hits, with scores within tolerance pending the spike.

### What warrants a second pair of eyes
- D3 changes ids on the vault (full slugs instead of 64-character cuts). Citations and stored references to chunk ids from before the change will not resolve.
- D4's 64-way merge for `values()`: the cost of whole-corpus iteration (evals, sampling) goes up somewhat.

### What should be done in the future
- Phase 0 spikes, then Phases 1–5 (tasks.md).

### Code review instructions
- Read the guide. Check the measurements with `python3 scripts/01-doc-id-collisions.py`, and the bleve claims against `~/go/pkg/mod/github.com/blevesearch/bleve/v2@v2.6.0` and `zapx/v16@v16.3.4`.

## Step 2: Phase 0 spike — a cloned bleve index plus a delta scores identically to a fresh build, in 70 ms instead of 12 s

The user asked to implement RAGWB-003. Phase 0 answers the one question that could change the bleve part of the design: is an index made by hard-linking a stored index's segments and applying a delta equivalent to a fresh build of the same chunks?

It is, for scoring. On the vault (85,882 chunks), a 30-document delta (445 chunk deletes, 418 new chunks) gave identical scores to a fresh build on all 48 queries, and identical top-10 ids on 45. The other three differ only in the order of equal-scored hits. Cloning took 0.3 ms and applying the delta 70 ms, against 12.4 s for a fresh build.

### Prompt Context
**User prompt (verbatim):** "Alright, implement, commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill) (diary format from skill: MANDATORY)"
**Assistant interpretation:** Implement RAGWB-003's phases 0–5, with commits per milestone and investigation-format diary steps.
**Inferred user intent:** Make an edit to one note cheap in practice, not just on paper.
**Commit (code):** this step's commit adds `scripts/02-bleve-delta-spike`.

### What I did
- `scripts/02-bleve-delta-spike/main.go`:
  1. Load all chunks from the stored vault record file (`~/.ragwb/store/2725d65d16063b99/e9/e9647dbeba55cc.records`).
  2. Build index A fresh. Pick 30 docs with seed 3: 10 removed, 10 modified (" incremental zebra marker" appended to every chunk), 10 added (copies of other docs under new doc ids).
  3. Clone A's directory: hard-link `*.zap`, copy `root.bolt` and `index_meta.json`. Open it writable, batch-delete the 445 old chunk ids, index the 418 new chunks (documents built exactly like `BuildBleveBm25`), close.
  4. Build index B fresh from the final chunk set, then compare `DocCount`, `FieldCardinality(f0..f2)` (via `idx.Advanced().Reader().(index.BM25Reader)`) and top-10 ids and scores for 48 queries (5 hand-written plus 43 sampled headings).

### What worked
```
A: 85882 chunks · B: 85855 chunks · delta: delete 445, index 418 (docs: 10 removed, 10 modified, 10 added)
fresh build A: 10.426s
clone (hard links): 294µs · open+apply+close: 70ms
fresh build B: 12.355s
DocCount clone 85855 fresh 85855 · FieldCardinality clone [5583 33328 249021] fresh [5490 32564 244152]
base after delta: DocCount 85882 (want 85882)
queries 48 · identical top-10 ids 45 · identical scores 48 · max relative score diff 0
```

### What didn't work
- Nothing failed. One expectation was wrong in an instructive way (below).

### What I learned
- **`FieldCardinality` does lag after deletes.** The clone still counts terms that only deleted documents contained (249,021 vs 244,152 for the text field).
- **Scores are unaffected anyway.** bleve's BM25 average length is `ceil(FieldCardinality / DocCount)` (`search/searcher/search_term.go:118`), and the ceiling rounds both cases to the same integer: 3 for text, 1 for title and headings.
  - It is a coarse statistic: the number of distinct terms per document, rounded up.
  - Drift could cross an integer boundary after many deletions. If that ever matters, a forced merge resets it; not needed now.
- **Tie order follows bleve's internal document numbers,** which differ between a clone and a fresh build. The equivalence contract (guide §6.9) already allows reordering among equal scores.
- **The base directory is unaffected by changes to the clone**, as hard-link semantics promise for immutable segments.

### What should be done in the future
- The bbolt shard copy-on-write timing is measured in Phase 3 with the real `records2` code instead of a separate spike.

### Code review instructions
- Run `go run ./ttmp/2026/11/03/RAGWB-003-…/scripts/02-bleve-delta-spike -records <stored chunk records>`.

## Step 3: Phase 1 — stable per-file identity in Go and rag.js

Phase 1 makes a document's id depend only on its file name, so an unchanged file keeps its id and chunk ids across builds. Previously, ids came from a slug capped at 64 characters, which made 271 of the 2,287 vault files share an id with another file. Chunk ordinals ran across the whole corpus, so one inserted file renumbered every later chunk.

Now the id is the full slug if it has at most 160 characters. Otherwise it is the slug cut to 151 characters, with trailing dashes trimmed, plus `-` and 8 hex digits of `cyrb53(name)`. Chunk ordinals count within each document. On the vault, 9 ids get the hash suffix instead of 1,911 with a 64-character cap, and there are no true collisions.

### Prompt Context
**User prompt (verbatim):** see Step 2.
**Assistant interpretation:** First implementation phase: stable identity, the precondition for every delta.
**Inferred user intent:** Unchanged notes keep their artifacts across edits elsewhere.
**Commit (code):** `29feef3` — "RAGWB-003 Phase 1: stable per-file identity (Go and rag.js)".

### What I did
- `pkg/natives/jsstr.go`:
  - `slugFull`, with `slug` as its capped wrapper;
  - `DocIDMax = 160`, `DocIDFor(name)`;
  - `Cyrb53(s)`, which hashes UTF-16 code units, as JS does.
- `pkg/natives/markdown.go`: `id := DocIDFor(name)`.
- `pkg/natives/chunk.go`: `AssignIDs` uses an ordinal per document (a `pos` map keyed by doc).
- `pkg/ragjs/assets/rag.js`:
  - `slugFull`, `DOC_ID_MAX` and `docIdFor`; `parseMarkdown` uses `docIdFor(name)`; `assignIds` uses an ordinal per document;
  - `artifacts()` keeps the first document of a duplicated id and logs the duplicate through `this.env.log`.
- `pkg/natives/markdown_native.go`: the `parse.markdown` `all` wrapper logs duplicate ids.
- `pkg/ragjs/identity_test.go`: `DocIDFor` and `Cyrb53` equal rag.js on 13 names (ASCII, umlauts, emoji and astral characters, 159/160/161/250-character names, separator runs at the cut).
- `natives.Version` → `"2026-11-04.2"`, so stored artifacts with old ids are not reused.

### What worked
- `go test ./pkg/...`: all green, including markdown and BM25 parity on the reports corpus. Ids changed but Go and JS agree.
- With the 160-character cap, 9 vault files get a hashed id (1,911 with a 64-character cap). The uncapped slug length has a median of 88 and a maximum of 183.

### What didn't work
- `artifacts()` first used `engine.env.log`, but `engine` is not in scope there: `ReferenceError: engine is not defined` on the duplicate-id path. Fixed to `this.env`.

### What I learned
- None of the 271 shared ids were true collisions: every one came from the 64-character cap. Distinct names with the same slug (`a b.md` / `a-b.md`) do not occur in the vault, but the duplicate-id warning covers them.

### What was tricky to build
- Matching `cyrb53` exactly: JS `Math.imul` and `>>>` semantics over UTF-16 code units, not runes. An astral character contributes two code units. The test names include one.

### What warrants a second pair of eyes
- A duplicate id keeps the first document in canonical order and drops the second. It is logged rather than failing the build.

### What should be done in the future
- The whole-vault markdown parity run under the new ids was started in the background (`RAGWB_PARITY_INCLUDE='**/*.md' go test ./pkg/runner -run TestMarkdownParityVault`). Its result is recorded in a later step.

### Code review instructions
- Start at `DocIDFor` in `pkg/natives/jsstr.go` and `docIdFor` in `rag.js`. Then run `go test ./pkg/ragjs -run Identity -v`.

## Step 4: Phase 2 — source manifest, diff, and lineage keys in the artifact store

Phase 2 gives a build a way to find "the previous version of this node" and to know which files changed since then. Lineage keys are the first part. They are digests computed without the file stats: `source.dir` contributes `def.lineage(p)`, its resolved params, instead of `def.digest(p)`. So "the chunks of this pipeline over this folder" keep the same key across corpus edits.

The artifact store gained a small lineage index, `lineage/<l[:2]>/<l>.json`. It holds the last three (storeKey, digest) pairs, newest first. rag.js records an entry after every save.

The second part is the manifest. `source.dir` now stores it in place of the old zero marker: every matched file's name, size, mtime in nanoseconds and sha256, with skipped files tracked too. `SourceDir.Diff(p, base, maxFraction)` compares the tree with a base manifest. It stats every file and hashes only those whose (size, mtime) changed.

### Prompt Context
**User prompt (verbatim):** see Step 2.
**Assistant interpretation:** Second implementation phase: the data a delta build needs. Nothing consumes it yet; Phase 3 does.
**Inferred user intent:** see Step 2.
**Commit (code):** the commit for this step: "RAGWB-003 Phase 2: source manifest, diff, lineage index".

### What I did
- `pkg/natives/manifest.go` (new):
  - `Manifest{Version, Params, Entries}`, `ManifestEntry{Name, Size, Mtime, Hash, Skipped}`;
  - `FilesDiff{Full, Reason, Added, Changed, Removed, Unchanged}`;
  - `readMatched`, which applies the skip rules (maxBytes, HTML) to one file, as `Walk` does;
  - `Diff`, `LastManifest`, `Lineage`.
- `pkg/natives/sourcedir.go`:
  - `Walk` goes through `readMatched` and remembers the manifest for each param key;
  - the JS definition gains `lineage: p => h.lineage(p)`;
  - `persist.save: (_, p) => JSON.parse(h.manifest(p))`, so the stored payload is the manifest as plain JSON.
- `pkg/artifacts/lineage.go` (new): `LineageEntry`, `LineageKeep = 3`, `LineageLatest`, `LineageRecord`. These are atomic temp+rename writes. `List` skips the `lineage` directory.
- `pkg/natives/persist.go`: `env.artifacts.lineageLatest(l)` and `lineageRecord(l, storeKey, digest)`.
- `pkg/ragjs/assets/rag.js`:
  - `Engine.lineages(ir)`. It is null for `source.files`, whose params are the contents, and for any node with a `digest` hook but no `lineage` hook, plus everything downstream of those;
  - `build` computes lineages when the store supports them and records one after each successful save;
  - `codec.save(value, n.p)` now receives the params.
- Tests:
  - `pkg/natives/manifest_test.go` covers a no-op diff, add, change, remove, rename (remove plus add), a touch with identical bytes, skipped→returned, returned→skipped, the threshold, other params, a nil base, and lineage ignoring contents;
  - `pkg/artifacts/store_test.go` `TestLineage` covers newest-first order, keep 3, and absence from `List`;
  - `pkg/runner/lineage_test.go` runs two stored builds around a one-file edit. Every node has a lineage key; every saved node's `LineageLatest` points at a stored artifact with its digest; the `source.dir` payload parses as a `Manifest`; and after the edit, lineage keys are equal and saved digests differ.

### What worked
```
go test ./pkg/natives -run TestManifestDiff -count=1   ok   0.011s
go test ./pkg/runner -run 'TestLineageRecorded|TestBleveIndexPersists' -count=1 -v
--- PASS: TestBleveIndexPersistsAcrossRuntimes (0.32s)
--- PASS: TestLineageRecordedForStoredNodes (0.41s)
go vet ./pkg/... && go test ./pkg/... -count=1   all ok (runner 75 s)
```

### What didn't work
- `go build` failed after the first edit with `pkg/natives/sourcedir.go:352:13: undefined: json`. The `encoding/json` import was missing; I added it.
- The first version of `persist.save` returned the manifest's JSON string. Persist's generic path stringifies values, so that would have stored a JSON string containing JSON. Wrapping it in `JSON.parse` stores the object; the parse costs microseconds for about 2,300 entries.
- `TestLineage` first expected the wrong `List` count (it counted lineage files). Fixed to 4 entries, none from the lineage directory.

### What I learned
- A touched-but-identical file must not count as a change. Editors and `git checkout` rewrite mtimes. The diff re-hashes on a stat mismatch, and equal hashes count as unchanged.
- A rename is a removal plus an addition. The id follows the name, so that is also what the downstream artifacts need.

### What was tricky to build
- Deciding which nodes get a lineage at all. A node whose output depends on something outside its params and inputs (a `digest` hook) can only be continued if it also says what stays the same (a `lineage` hook). Otherwise a stale "previous version" could be patched into a wrong result, so the key is null and the node always does a full build.

### What warrants a second pair of eyes
- `Diff` with `maxFraction`: more than that fraction of the base changing means `Full`. The threshold is relative to the base entry count, and an empty base never trips it. Phase 3 picks the default (the guide says 0.3).
- The manifest is remembered in memory per param key and saved from the last walk. If two builds over the same folder run concurrently in one process, the later walk wins. Both describe the same tree up to a race, and the digest still guards correctness.

### What should be done in the future
- Phase 3 consumes all of this: `Engine.build` looks up `lineageLatest`, loads the base manifest, calls `Diff`, and passes the delta downstream.

### Code review instructions
- Read `pkg/natives/manifest.go` `Diff`, then `Engine.lineages` in `rag.js`, then the runner test.
- Validate with `go test ./pkg/natives ./pkg/artifacts ./pkg/runner -run 'Manifest|Lineage' -count=1 -v`.

## Step 5: Phase 3 — records2, the delta protocol, and source/parse/chunk deltas (one-note edit: 21 + 23 + 24 ms)

Phase 3 makes the first three nodes of the pipeline incremental. Docs and chunks are now stored as `records2`: 64 bbolt shards by document plus an `index.json` that holds the canonical order. A new version copies only the shards of the documents it touched and hard-links the rest.

`Engine.build` gained the delta protocol. Each node gets a delta (`{none}`, `{full}`, or a component-specific change set). Each delta carries `from`, the digest of the version it diffs from. A node continues from the newest stored version of its lineage whose recorded input digests equal those `from`s, so a delta is never applied to a base built from different inputs.

On a copy of the vault, after a one-line edit to one note:
- `source.dir` takes 21 ms (a diff against the stored manifest);
- `parse.markdown` takes 23 ms;
- `chunk.sections` takes 24 ms.

Before, they took 0.4 s, 2.5 s and 2.5 s. The two indexes still rebuild fully (ix-text 14–18 s, ix-meta 2–4.7 s); Phase 4 makes them incremental.

### Prompt Context
**User prompt (verbatim):** see Step 2.
**Assistant interpretation:** Phase 3 of the guide: the copy-on-write record format, the engine protocol and the first three component deltas.
**Inferred user intent:** see Step 2.
**Commit (code):** the commit for this step: "RAGWB-003 Phase 3: records2, delta protocol, source/parse/chunk deltas".

### What I did
- `pkg/natives/records2.go` (new):
  - `WriteRecords2(dir, kind, docs)` writes the 64 shards in parallel;
  - `CowRecords2(base, dst, remove, upsert)`: kept documents and upserts are merged by name; untouched shards are hard-linked (`linkOrCopy`); a touched shard is copied, then its documents are deleted and re-put;
  - the reader `Records2`: `Len`, `Get`, `Has`, `Slice` (ordinal ranges via prefix sums over the index), `DocRecords`, `Order`, `DocInfo`, `DocByName`, `Relocate` (reopen after the store moves the directory), `Close`.
  - A record's shard comes from its id: the part before `#` is its document, and other ids go into `index.ext`.
- `pkg/natives/records2_codec.go` (new):
  - `ChunksToR2` and `DocsToR2` (a repeated doc id joins its first group);
  - `R2Chunks` and `R2Docs`;
  - `records2Object`, the JS side of a RecordView for docs or chunks, with the reader as a hidden handle;
  - `records2Handle`, `docsHandle`.
- `pkg/natives/persist.go`:
  - docs and chunks are saved as `records2` directory payloads;
  - a `records2` view (a delta output) is moved in from the store's temp area and reopened there. Any other `records2` view, such as an input passed through, is linked into a new directory first, so an artifact is never moved away from its key;
  - `records2` loads as a view;
  - a `manifest` codec;
  - `env.artifacts` gains `lineageList(l)`, `lineageRecord(l, key, digest, inputs)`, `tempDir()` (unsaved temp directories are removed when the runtime closes) and `view(records)`.
- `pkg/artifacts/lineage.go`: `LineageEntry.Inputs`, plus `LineageList`, which returns stored versions only.
- `pkg/natives/manifest.go` and `sourcedir.go`:
  - the manifest stays in Go (`manifestObject`/`manifestHandle`);
  - `h.diff(p, base, maxFraction)`;
  - the `source.dir` delta: its value is lazy (the files are re-read only if a downstream node needs all of them), and its delta is `{added, changed, removed}`.
- `pkg/natives/delta.go` (new):
  - `ParseDelta`: parse the added and changed files; removed files map to doc ids through the base index (`DocByName`). It declines (a full build) on doc-id collisions.
  - `ChunkDelta`: chunk the upserted docs with per-doc `AssignIDs`; collect the old chunk ids of removed and upserted docs, for the index deltas in Phase 4; copy-on-write the base.
- `pkg/natives/markdown_native.go`: the `delta.apply` hooks for `parse.markdown` and `chunk.sections`, `h.parseDelta` and `h.chunkDelta`. The full `h.chunk` sets `Chunk.DocName` (Go-only, `json:"-"`).
- `pkg/ragjs/assets/rag.js`:
  - `FULL_DELTA`;
  - `Engine.build` keeps `deltas` and calls `tryDelta` before `map`/`all`;
  - a component may return `lazy`;
  - stats `incremental`, `base` and `delta`;
  - lineage records carry the input digests;
  - inputs are materialized only for a full computation (`inputArrays()`).
- Tests:
  - `pkg/natives/records2_test.go`: six random copy-on-write steps over 300 docs; each equals a fresh write (records, index, `Order`, `Get`, slices across doc boundaries), and at least 50 of 64 shards are hard links;
  - `pkg/runner/delta_test.go`: eight edit steps on a temp corpus (51 notes): edit, append, add, remove, rename, touch, emptying a note, a new subfolder. After each step, an incremental build is compared with a full build in a fresh store: docs JSON, chunks JSON, reading order and top-8 search results (ties normalized) are equal, and all 8 steps ran incrementally.
  - `records_test.go` now expects `records2`.

### What worked
```
go test ./pkg/natives -run TestRecords2 -v                         PASS (0.18s)
go test ./pkg/runner -run TestDeltaBuildEqualsFullBuild -v         PASS, 8/8 steps incremental
  step 1 (edit n10.md, append n03.md): chunks inc=true {docs:2 removes:6 upserts:7}; files {upserts:2 unchanged:49}; parse {upserts:2}
  step 3 (rename …, touch n23.md): files {removes:1 upserts:1 unchanged:50} — the touch is not a change
go test ./pkg/... -count=1                                          all ok
```
Vault copy (2,287 notes, 85,882 chunks; `$S/vault` = markdown files rsynced into the scratchpad, parc app with `VAULT` pointed there):
```
cold (ragwb --timings … verbs --dir parc parc search):  23.0 s total — ix-text 15.4 s, parse 2.5 s, chunks 2.5 s, ix-meta 1.9 s, files 0.45 s
one-line edit (ragwb run -v build.js):  files 21.4 ms INC {upserts:1, unchanged:2286} · parse 22.6 ms INC · chunks 24 ms INC {upserts:40, removes:40} · ix-text 18.4 s full · ix-meta 4.2 s full
```
- The whole-vault markdown parity run from Step 3 passed (`--- PASS: TestMarkdownParityVault (1051.93s)`; `11736345 bytes of chunks identical; build js=334854ms go=4198ms`).

### What didn't work
- The first vault run showed `chunks 2424.9 INC`: incremental, but 2.4 s. The cause was `const dctx = { ...ctx, … }`. Spreading invokes `ctx`'s `docs` and `chunks` getters (`firstOf('docs')` → `asArray(view)`), which materialized all 2,282 docs. The fix is `Object.assign(Object.create(ctx), …)`, and chunks dropped to 24 ms.
- The first `Engine.build` change computed `inputs = n.in.map(i => asArray(outputs.get(i)))` before trying the delta. That forces the lazy files and materializes the views, so it became `inputArrays()`, called only on the full path.
- The first manifest round trip went through JS (`JSON.parse(h.manifest(p))`). I caught before running it that nanosecond mtimes (~1.7e18) exceed 2^53: JS numbers would round them, every stat would mismatch, and every file would be re-hashed. The fix is the Go-only `manifest` codec.
- `TestDeltaBuildEqualsFullBuild` first failed on top-8 results: the same scores, but a different order among three tied hits (0.1928). The guide's equivalence contract (§6.9) allows that, so the test sorts ties by id and drops the last tied group at the cut.
- `ragwb -v …` is not a global flag (`unknown command`); `-v` belongs to `ragwb run`.

### What I learned
- Every engine-side convenience that reads "all docs" is a hazard for delta builds: getters, `asArray`, `{...spread}`. The delta path must touch only the handles.
- Changing `rag.js` moves the store namespace, so after every `rag.js` edit the next vault build is full. That is expected, but it confuses measurements.
- On disk, `du` counts hard links once. Incremental versions add 6–17 MB each, but a full records2 store is larger than v1: chunks 436 MB against 272 MB, docs 316 MB. The `i` and `o` buckets fill pages in random order, and 64 small files carry fixed overhead.

### What was tricky to build
- **Delta ↔ base consistency.** A delta says "changes since version X". The node's base must have been built from exactly X, or applying the delta yields a wrong output, for example after a build that failed between parse and chunks. Lineage entries now store the input digests, and `tryDelta` picks the newest entry whose inputs equal the deltas' `from`s. A reused input contributes `{none, from: currentDigest}`.
- **Canonical order under copy-on-write.** Order lives in `index.json`, never in shard key order. Upserts are merged by source name into the kept docs, which reproduces the full build's order: files are sorted by name, and chunks follow docs.

### What warrants a second pair of eyes
- `saveRecords2`'s rule for moving versus linking, which keys on `.tmp-` under the namespace.
- `Relocate` reopening after `SaveDir` found an equal output already stored (another process): the shard file names differ, so the reader must re-read the index. It does.
- Docs are now a view when loaded from the store. `art.docs` iterates the view, and every runner test passes, but presets that index into `art.docs` like an array would see a Map.

### What should be done in the future
- Phase 4: bleve clone plus delta using `removeIds` and `upsert`; terms rows plus rebuild.
- Shrink `records2`: `FillPercent` 1 on `r`, a sorted bulk load for `i`, perhaps compressing records.

### Code review instructions
- Read `tryDelta` and the changed `build` loop in `rag.js`, then `CowRecords2`, then `delta.go`.
- Validate with `go test ./pkg/natives -run 'Records2|Manifest' -v` and `go test ./pkg/runner -run 'Delta|Lineage|Records' -v`.
