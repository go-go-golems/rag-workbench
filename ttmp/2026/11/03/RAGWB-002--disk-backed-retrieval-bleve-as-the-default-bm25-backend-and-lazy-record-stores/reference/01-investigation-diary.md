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

## Step 4: PR #1 merged; Phase 0 + the bleve backend (opt-in); the quality check passes for the per-field mode

PR #1 was merged with a merge commit (`c97b2fa`), so the diaries' commit hashes stay valid, and `main` became the repo's default branch. Work continues on `task/ragwb-002-disk-backed-retrieval`, branched from `main`.

This step:
- adds bleve and a working bleve backend for `index.bm25`, selectable with `--index-backend bleve` or `rag.bm25({ backend: 'bleve' })` (not the default yet, not persisted yet);
- gives natives a runtime closer hook;
- runs the quality check that decides whether bleve's own scoring is acceptable.

The default per-field mode is acceptable. On the reports goldens across all 30 presets, it is slightly *better* than the in-memory engine (hit@5 0.925 → 0.942, MRR 0.872 → 0.893), and on the vault it retrieves a chunk from its own heading more often (0.725 → 0.755). Its top-10 lists differ a lot from the memory backend's on the vault, which is the expected effect of per-field IDF.

### Prompt Context
**User prompt (verbatim):** "merge it, merge commit. Then continue, commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill)"
**Assistant interpretation:** Merge PR #1 with a merge commit, then implement RAGWB-002 phase by phase, with commits and investigation-format diary steps.
**Inferred user intent:** Get the disk-backed retrieval built, without losing the review trail.
**Commit (code):** `78321cb` (P0 + backend); this step's commit adds the `combined` mode and the quality scripts.

### What I did
- `gh pr merge 1 --merge` → `c97b2fa`; `gh repo edit --default-branch main`; `git checkout -b task/ragwb-002-disk-backed-retrieval` from `main`.
- **Phase 0:** `go get github.com/blevesearch/bleve/v2@v2.6.0`. The `go` directive stays at 1.26.1: only goja-bleve needed 1.26.4. bbolt v1.4.0 arrives as an indirect dependency.
- **`pkg/natives/bleve_bm25.go`:**
  - `BuildBleveBm25`/`OpenBleveBm25` (read-only: `OpenUsing(dir, {"read_only": true})`), `Search`, `SearchIn`, `Close`.
  - Mapping: a custom analyzer `ragwb-pretokenized` (whitespace tokenizer, no filters) declared in the mapping itself, so an opened index needs no global registration. `ScoringModel = "bm25"`; one field per weighted plan field, named positionally (`f0`, `f1`, …) because plan paths like `meta.summary` contain dots, which bleve reads as nested documents. `docId` is a keyword field.
  - Query: a disjunction of `TermQuery(term)` per field, boosted by field weight × query-term weight. Terms come from `queryTermsFor`, refactored out of `Bm25.queryTerms` and shared by both backends (synonym expansions at 0.5).
  - `only` → a conjunction with `DocIDQuery`; `searchIn` → a conjunction with `docId:<id>`.
  - `why` comes from term locations: matched terms are exact, shares are approximate.
- **`pkg/natives/bm25_native.go`:** one `index.bm25` definition dispatching on `p.backend || Bm25Native.Backend`. Index objects gain `backend`. Unpersisted bleve indexes live in a temp dir, removed by a runtime closer.
- **Closer hook:**
  - `pkg/ragjs`: a ragwb registrar replacing `NativeModuleRegistrar`, which captures `RuntimeModuleRegistrationContext.AddCloser`, plus an optional `EnvNative` interface (`InstallEnv(vm, rag, rtenv.Env)`).
  - `pkg/rtenv`: holds `Env`, so `natives` doesn't import `ragjs` (import cycle via ragjs's tests).
  - `runner`: a compile-time assertion that `Bm25Native` is an `EnvNative`.
- **Wiring:** `runner.Config.IndexBackend`, global `--index-backend memory|bleve` (default `memory` until the gate passes), and `ArtifactNamespace(js, backend)`: `"go+bleve"` for a non-default backend.
- **Tests (`pkg/natives/bleve_bm25_test.go`):**
  - same top hit as memory for 4 clear queries; `why` terms ⊆ query terms; empty and unknown queries;
  - synonyms, `searchIn`, `only`;
  - read-only reopen gives identical hits and scores;
  - two simultaneous read-only opens;
  - a closed index errors;
  - a temp index is removed on Close.
- **Quality scripts:**
  - `scripts/01-presets-quality.py`: every preset × both backends via `ragwb --no-store --index-backend … run <preset> --evals --k 10`; result saved in `various/01-presets-quality-fields.txt`.
  - `scripts/02-vault-quality/main.go`: builds both backends from the stored vault chunks (`~/.ragwb/store/a2b021016d5826fa/ed/edc1ece5955f1f.bin`) with parc's fields. It compares 208 queries (200 sampled section headings plus 8 hand-written), and `-show "q1|q2"` prints both top-5s.
- Added `combined` mode (`BleveOptions.Combined`, `rag.bm25({ combined: true })`): one field with each field's tokens repeated `round(weight)` times, i.e. BM25F-style tf/length with one IDF.

### Why
- The design's ranking decision (D2) is only acceptable if the results are "not completely wrong". That had to be measured before persistence and the default switch were built on top of it.
- The backend had to enter the artifact namespace now, not in Phase 3b. A memory-built and a bleve-built index share digest and component source, so with a store, `--index-backend bleve` would have silently loaded memory indexes and the comparison would have measured nothing.

### What worked
**Presets, reports goldens, 30 presets:**

| mode | mean hit@5 | mean MRR | mean nDCG@10 | median top-10 overlap | hit@5 drops > 0.1 |
|---|---|---|---|---|---|
| memory | 0.925 | 0.872 | 0.783 | — | — |
| bleve per-field (default) | **0.942** | **0.893** | 0.770 | 0.50 | none (largest: compare-nodes −0.083) |
| bleve combined | 0.917 | 0.873 | 0.769 | 0.82 | outline |

**Vault, 85,882 chunks, 208 queries:**

| mode | build | disk | top-10 overlap with memory (median / p10) | self-recall@10 | mean query |
|---|---|---|---|---|---|
| memory | 4.2–4.8 s | — | — | 0.725 | 14–20 ms |
| bleve per-field | 9.6–14.2 s | 153–159 MB | 0.11 / 0.05 | **0.755** | 7–10 ms |
| bleve combined | 11.3 s | 167 MB | 0.43 / 0.18 | 0.635 | 6.9 ms |

- Side by side (`-show`), "sqlite forum for agents" and "goja regexp unicode" agree at #1, and "Failure modes" gives failure-mode sections in both.
- `go test ./... -count=1` and `make lint` pass.

### What didn't work
- First build of `bleve_bm25.go`: `undefined: bleve.IndexMapping` (the constructor returns `*mapping.IndexMappingImpl`) and `f.Name undefined` (`natives.Field` has `Path`). Fixed. The dotted-path problem was found while fixing `Name`, which led to positional field names.
- First `InstallEnv` took an anonymous struct type mirroring `ragjs.Env`. It compiled, but would never have satisfied `ragjs.EnvNative`, so the closer would silently not be wired. Importing `ragjs` from `natives` then failed: `import cycle not allowed in test` (ragjs's tests import natives). Moving `Env` to `pkg/rtenv` plus the compile-time assertion in `runner` fixed both.
- `cp … ttmp/2026/11/03/RAGWB-002--*/various/…`: `no matches found`. The ticket had no `various/` dir yet, and zsh failed the glob; fixed with `mkdir -p`.
- **A per-field ranking weakness**, seen by hand for "lunaroute embeddings cache": memory ranks LunaRoute chunks first, while per-field bleve ranks "Cache Storage Analysis". "cache" is rare among *titles*, so its title-field IDF times the title boost outweighs the rare "lunaroute" in the text. Combined mode removes per-field IDF, but scored worse overall.

### What I learned
- Top-10 overlap with the old engine is a poor quality proxy on a big corpus with many near-equivalent chunks: per-field had 0.11 overlap yet higher recall. Self-recall (heading → its chunk) needs no goldens and tracks quality better. It may favour per-field scoring because heading words get their own IDF; the presets' goldens point the same way, though.
- bleve answered the vault queries about twice as fast as the in-memory Go BM25 (7–10 ms vs 14–20 ms). bleve's searcher skips postings; ours scores every posting of every query term.
- Building the bleve index takes 2–3× longer than the memory BM25 (with no text stored: 153 MB versus the probe's 315 MB).

### What was tricky to build
- **The "default" must not leak into stored artifacts.** The backend default lives in the runtime, not the IR. Both the store key (component source plus digest) and the namespace were blind to it, until the namespace learned the backend.

### What warrants a second pair of eyes
- `approxWhy` splits a hit's score by occurrences × field weight × query weight. That is a presentation approximation. If the inspector's "why" must be exact for bleve, it needs `Explain`, which is costly.
- Per-field IDF inflation (title words) is the main ranking risk. Levers if it bites: lower title weight for bleve, an additional combined field, or per-plan `combined: true`.

### What should be done in the future
- Phase 1 (artifact store file/dir payloads), then persist the bleve index (codec `bleve`, read-only open from the store), then Phase 2 (record store), then make `bleve` the default.

### Code review instructions
- `pkg/natives/bleve_bm25.go`, `bm25_native.go` (JS dispatch, `InstallEnv`), `pkg/ragjs/module.go` (`ragRegistrar`), `pkg/rtenv`, `runner.ArtifactNamespace`.
- Reproduce: `go test ./pkg/natives -run Bleve`; `python3 scripts/01-presets-quality.py`; `go run ./…/scripts/02-vault-quality -chunks <stored chunks .bin> [-combined]`.
