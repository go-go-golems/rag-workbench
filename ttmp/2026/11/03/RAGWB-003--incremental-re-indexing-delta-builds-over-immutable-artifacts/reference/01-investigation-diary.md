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
