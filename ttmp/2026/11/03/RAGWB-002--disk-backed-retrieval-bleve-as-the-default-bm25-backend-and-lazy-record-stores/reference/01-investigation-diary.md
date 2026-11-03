---
Title: Investigation diary
Ticket: RAGWB-002
Status: active
Topics:
    - rag
    - backend
    - goja
    - search
DocType: reference
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: "Chronological record of RAGWB-002: design, implementation phases, measurements and failures."
LastUpdated: 2026-11-03T16:37:02.273460931-05:00
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

## Step 1: ticket and intern guide for disk-backed retrieval, with bleve as the default

This step opens RAGWB-002 and writes the design and implementation guide for an intern. It covers how retrieval works today, why a served vault session costs 838 MB, and a phased plan:
- bleve becomes the default BM25 backend in Go runtimes;
- docs and chunks move to on-disk bbolt record stores, read one record at a time.

No code changed. All measurements come from RAGWB-001 (diary steps 23–26).

The main decision came out of reading bleve's scorer source while writing (D2 in the guide). bleve's own query scoring would **change rankings**, so making it the default naively would break the browser/CLI/serve parity that RAGWB-001 built. The design therefore uses bleve as an on-disk postings store and keeps our BM25F scorer.

### Prompt Context
**User prompt (verbatim):** "Create a new docmgr ticket and then Create  a detailed analysis / design / implementation guide that is for a new intern, explaining all the parts of the system needed to understand what it is, with prose paragraphs and bullet point sand pseudocode and diagrams and api references and file references. It should be very clear and technical. Store in the ticket and the nupload to remarkable. 


Make it the default."
**Assistant interpretation:** Open a new ticket for the bleve-backed retrieval work, write an intern-level guide for it, upload it to reMarkable, and design bleve as the *default* backend rather than an opt-in.
**Inferred user intent:** Have an implementable, reviewable plan before the work starts, and settle the default up front.
**Commit (code):** — (documents only).

### What I did
- **Ticket:** `docmgr ticket create-ticket --ticket RAGWB-002 …`, plus a new vocabulary topic `search`; the guide (design-doc) and this diary (reference); six phase tasks.
- **Engine facts re-read for the guide:**
  - the component registry (`rag.js:489`);
  - `index.bm25` (`rag.js:536`) and its Go replacement (`bm25_native.go:19-49`);
  - `retrieve` (`rag.js:564`);
  - the build/persist/Lazy path (`rag.js:1252`, `1340-1418`);
  - `artifacts()` (`1478`), `Session.ctx` (`1648`) and evals (`1744-1760`);
  - every `ctx.chunk` / `art.chunks` consumer.
- **Go BM25:** the scoring and index construction in `pkg/natives/bm25.go` (`NewBm25` 225-260, `queryTerms` 273-299, `Search` 317+).
- **bleve v2.6.0 source:**
  - `OpenUsing` and scorch `read_only` (`index.go:330`, `index/scorch/scorch.go:219`);
  - BM25 constants (`search/util.go:272-273`: k1 1.2, b 0.75; package-level, not per index);
  - the BM25 term score (`search/scorer/scorer_term.go:141-157`);
  - the disjunction coordination factor (`scorer_disjunction.go:65-66`);
  - the analyzer/tokenizer registries.
- **go-go-goja v0.10.7:** `Runtime.AddCloser` (`engine/runtime.go:66`) and the module context's `AddCloser` (`engine/runtime_modules.go:28`), for closing index handles with the runtime.
- **bbolt:** confirmed it is already a transitive dependency of bleve (`go.etcd.io/bbolt v1.4.0` in the probe module's go.sum).

### Why
- The user asked for the guide and for bleve to be the default. A default that changes rankings would quietly invalidate every plan tuned in the browser page and every eval baseline, so the guide had to settle scoring first.

### What worked
- The design keeps every query-path consumer unchanged by preserving two contracts:
  - the index contract (`search`, `searchIn`, `kind`, `N`, `toJSON`, `__ix`);
  - a `Map`-compatible view for `art.chunks`.
- Only five places in rag.js change: `artifacts()`, `Session.ctx`'s `chunkList`, the evals `R` count, the agentic "sections of a document" tool, and the materialization of build inputs.

### What didn't work
- Nothing failed; this step wrote documents only.

### What I learned
- **Where bleve's BM25 differs from ours** (`natives/bm25.go`):
  - Same IDF formula.
  - Its numerator uses `k1` where ours uses `k1+1`: a constant factor, harmless for ranking.
  - It scores each field separately; ours is BM25F (weighted tf and length summed into one virtual document).
  - It multiplies multi-term disjunctions by `matched/total`. That changes rankings.
- **Pre-tokenizing reproduces BM25F in stock bleve.** Tokenize in Go and index each field's tokens `weight` times into one whitespace-analyzed field. The term frequencies and field lengths are then exactly ours, for integer weights (all built-in defaults are integers).
- **The remaining unknown is length precision.** bleve keeps a norm `1/sqrt(len)` in reduced precision. Phase 3 starts with a spike comparing scores, with exact lengths as a doc value as the fallback.

### What was tricky to build
- **Defining "the default" without breaking parity.**
  - The browser cannot run bleve, so the default applies to Go runtimes only.
  - The backend is a component param (it enters the digest when written explicitly) plus a runtime default.
  - Since the runtime default is not in the IR, the artifact namespace must include the backend, so stored outputs of the two backends never mix.

### What warrants a second pair of eyes
- D2 (our scorer over bleve postings) relies on bleve's lower-level reader API (`IndexReader`, `TermFieldReader`, `ExternalID`), which may change more between versions than the query API.
- D5 (bbolt records with JSON per record) costs one `JSON.parse` per `get`. `filter.where` over many hits is the first place this could show up.

### What should be done in the future
- Phase 0 onwards (tasks.md). Start Phase 3 with the scoring spike, because it can invalidate D2.

### Code review instructions
- Read the guide top to bottom. Check its line references against `pkg/ragjs/assets/rag.js`, `pkg/natives/bm25.go` and the bleve module source (`~/go/pkg/mod/github.com/blevesearch/bleve/v2@v2.6.0`).

## Step 2: guide v2 — the two browser pages made explicit

The user asked what "the browser page" is, and whether `ragwb serve` runs queries on the backend (it should). It does: the app shell posts every query to `/api/rag/<app>/run`. The guide's phrase "the browser workbench is unchanged" was ambiguous between the two pages, so I added §3.2, a table contrasting the workbench page (queries run in the tab) with the app shell (queries run on the server, so RAGWB-002 applies to it automatically). Later sections were renumbered, and the summary and scope lines were reworded to match.

### Prompt Context
**User prompt (verbatim):** "what's the browser page? when I run serve, will it hit the backend to run the queries? (it should)" then "Upload as v2"
**Assistant interpretation:** Clarify the two pages in the guide and upload a second version without overwriting the first (which may carry annotations).
**Commit (code):** — (documents only).

### What I did
- Guide: new §3.2 with a table (started by, files, where `rag.js` builds and queries, corpus, server role, affected by RAGWB-002); reworded the executive summary, §2.3 and §6.7; §3.3–3.5 renumbered.
- Uploaded as "RAGWB-002 Disk-backed Retrieval Intern Guide v2" next to the original, without `--force`.

## Step 3: rankings may change — D2 switched to bleve's own scoring behind a quality gate

The user said rankings may change with bleve, "as long as it's not completely wrong". That removes the constraint behind D2 (exact parity through our own scorer over bleve's low-level postings). The guide now uses bleve's public query API: a disjunction of term queries built from our query terms, over one pre-tokenized field per weighted plan field, with field weight × query weight as boosts. An explicit quality gate against the memory backend replaces the parity requirement.

### Prompt Context
**User prompt (verbatim):** "ranking can change btw when adding bleve, as long as it's not completely wrong. Address code review comments."
**Assistant interpretation:** Relax the design's ranking constraint. (The review comments are on PR #1 and are recorded in the RAGWB-001 diary, step 27.)
**Commit (code):** — (documents only).

### What I did
- **Rewrote:** §6.4.2 (indexing: `f.<field>` pre-tokenized fields, no weighted repetition), §6.4.3 (search pseudocode: `TermQuery` per term × field with boosts, `DocIDQuery` for `only`, a `docId` term for `searchIn`; `why` from term locations, exact with `explain`) and the score-difference list.
- **D2** is now "bleve scores; rankings may change within a quality gate" (accepted, user decision). **D3** is "our tokens, one field per weighted field".
- **Phase 3 step 1** is now a quality spike that sets the thresholds. The proposed gate:
  - goldens hit@5 and MRR within 0.05 of the memory backend;
  - no preset's HIT@5 drops by more than 0.1;
  - vault median top-10 overlap ≥ 0.5, with queries below 0.2 inspected by hand.
- **Risks:** "norm precision" is replaced by "ranking drift", with the levers to pull if the gate fails. The test strategy gains a Quality row; the parity tests stay, pinned to the memory backend.

### What I learned
- The relaxation simplifies more than scoring. Fractional field weights now work (as boosts), so the fallback to the memory backend is gone. Field-length precision is no longer a concern. The low-level `IndexReader`/`ExternalID` API is no longer needed.

### What should be done in the future
- Re-upload the guide as v3 if the user wants the reMarkable copy current.
