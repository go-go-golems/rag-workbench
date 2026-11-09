---
Title: Design investigation diary
Ticket: RAGWB-009
Status: active
Topics:
    - rag
    - search
DocType: reference
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-08T18:12:11.742746384-05:00
WhatFor: ""
WhenToUse: ""
---

# Design investigation diary

## Step 1: Define consistent contracts and an implementation plan

Inspected the current engine and native boundaries to turn the review findings into a coherent simplification plan. The guide explains the existing architecture before proposing contracts, deletion targets, migration steps and focused acceptance tests. This step changes documentation only.

The central decision is to share semantic contracts, rather than generalize unrelated algorithms: datasets and explicit provenance, snapshot/resource ownership, operation lifecycles, and canonical graph references. Provider protocols, retrieval implementations and trigger scheduling remain specialized.

### Prompt Context

**User prompt (verbatim):**

> Ok, create a new docmgr ticket then to do this simplification while making the codebase more robust. Create  a detailed analysis / design / implementation guide that is for a new intern, explaining all the parts of the system needed to understand what it is, with prose paragraphs and bullet point sand pseudocode and diagrams and api references and file references. It should be very clear and technical. Store in the ticket and the nupload to remarkable.
>
> The document is detailed, but the cleanup is geared towards quality, simplicity, elegance.

**Follow-up (verbatim):**

> consistency and composability / reuse as well, when appropriate

**Assistant interpretation:** Create and deliver a researched design ticket; do not implement the refactor during this documentation step.

**Inferred user intent:** Resolve recurring classes of defects with fewer concepts, consistent interfaces and explicit ownership. Backwards compatibility is not a requirement.

**Baseline:** `0854c6a` on `task/ragwb-008-vault-ui`.

### What I did

- Created RAGWB-009 with docmgr, a design document, this diary and a phased implementation task list.
- Inspected `pkg/ragjs/assets/rag.js`, especially dataset consumption, shared builds, session execution, GC and graph export/editing.
- Traced persistence and delta reader cleanup through `pkg/natives/persist.go` and `pkg/natives/markdown_native.go`.
- Studied lineage retention in `pkg/artifacts/lineage.go`, result packaging in `pkg/runner/serve.go`, SSE writes in `pkg/server/sse.go`, and provider bridges in `pkg/ragjs/sample.go` and adjacent modules.
- Incorporated the twelve previously collected review findings, retaining their P1/P2 classification and distinguishing reported evidence from newly reproduced tests.
- Wrote Go and JavaScript pseudocode, architecture/lifetime diagrams, code/API references, deletion targets, direct migration guidance and regression acceptance criteria.
- Related the primary source files to the design via docmgr.

### Why

Fixing each array/view branch or cancellation symptom separately preserves the conditions that create the next defect. Shared contracts should establish identity, lifetime and ownership once, at the boundary that can enforce them. A design must also specify what disappears, otherwise abstraction work merely adds another layer.

### What worked

- Local code inspection exposed the additional result-packaging lifetime boundary: the server can consult current artifacts after query execution. The design therefore requires detached results before releasing a query lease.
- The review inventory maps cleanly to four major contract families, with two small corrections that do not warrant new abstractions.
- The existing high-level corpus and plan DSL can remain recognizable while low-level dataset access and result references change directly.

### What didn't work

`go doc net/http.ResponseController.SetWriteDeadline` and `go doc net/http.ResponseController.Flush` returned the requested documentation but also emitted module-proxy lookup warnings under restricted networking. The exact warning bytes were not retained; no dependency change or successful dependency download is claimed. These commands were documentation inspection, not test execution.

### What I learned

- Logical chunk IDs are insufficient for provenance when multiple enriched versions coexist. Dataset identity belongs in record references and index bindings.
- A bounded lineage history cannot be the authoritative inventory for garbage collection.
- Pinning only execution is insufficient if deferred result access still depends on native resources.
- Shared build cancellation and progress must belong to the job, with each caller holding an independent waiter.

### What was tricky to build

The design must preserve useful laziness without making consumers responsible for hidden native lifetimes. It separates read-only datasets, snapshot leases and detached public results. It also separates authoritative disk ownership from searchable lineage and chooses a single writable process per namespace, avoiding an unnecessary distributed coordination protocol.

### What warrants a second pair of eyes

- Validate snapshot publication/acquisition ordering and resource adoption across fresh, warm and delta builds.
- Review provenance and fusion rules for enriched datasets before migrating callbacks.
- Confirm manifest publication and sweep locking against crashes and interrupted builds.
- Check cancellation, loop disposal and blocked SSE writes using deterministic lifecycle tests.
- Measure materialization costs; the design does not claim performance parity without measurements.

### What should be done in the future

Implement the phased tasks in the guide. Start with regression characterization and contract conformance, then migrate consumers and delete superseded mechanisms at each boundary. Production authentication, distributed indexing and multiple writer processes remain separate work.

### Code review instructions

Read the guide's findings inventory and current-code map first, then the invariants and deletion list. Review the API migration and acceptance table against existing examples. Documentation validation uses `docmgr doctor --ticket RAGWB-009 --stale-after 30` and `git diff --check`; runtime suites are intentionally deferred because no runtime code changed.

### Technical details

Proposed modules cover datasets, snapshots, resource registry, artifact store, shared jobs, operation transport and graph references. These are design sketches, not existing exported packages. Query results retain explicit generation and dataset provenance but require no caller-owned close operation. Refresh failure leaves the last good snapshot usable. One canonical reference traversal must cover inputs, uses, conditions and outputs.

## Step 2: Validate and deliver the guide

Delivered the completed design guide to reMarkable and marked only the design/delivery task complete. The implementation tasks remain unchecked.

- **Validation:** `docmgr doctor --ticket RAGWB-009 --stale-after 30` passed; source-path inspection checked 13 referenced paths with none missing. No runtime tests were run for this documentation-only change.
- **Delivery command:** `remarquee upload bundle <guide> --name 'RAGWB 009 Engine Simplification Intern Guide' --remote-dir '/ai/2026/11/08/RAGWB-009' --toc-depth 2 --non-interactive`.
- **Receipt:** `OK: uploaded RAGWB 009 Engine Simplification Intern Guide.pdf -> /ai/2026/11/08/RAGWB-009`.
- **Limits:** The receipt confirms cloud delivery, not physical device synchronization. The proposed APIs and tests are not implemented by this ticket documentation commit.
- **Checkpoint:** This entry and the guide are committed together; use the ticket's git history to identify the documentation revision.

## Step 3: Integrate the implemented TypeScript foundation

RAGWB-010 implementation commit `cac9262` establishes modular authored sources and the committed bundle. Updated this guide's section 16 with the actual module map and strict type coverage. Existing baseline line references remain historical evidence.

- Four implementations are strict TypeScript: errors, identity, tokenization and request scopes. The remaining 22 extracted modules are JavaScript awaiting contract-specific migration.
- The user confirmed compatibility can break to improve simplicity, readability and robustness. Do not preserve object insertion order or source spelling as contracts.
- 009's semantic tasks remain open. Implement datasets, snapshots, operation outcomes and graph references in TypeScript while changing their runtime representations and consumers directly.
- Validation/delivery evidence for the foundation belongs to the [010 diary](../../RAGWB-010--modular-runtime-sources-and-typescript-tooling-foundation/reference/01-implementation-diary.md).

## Step 4: Prepare a three-writer experiment for the intern-guide skill

Created a ticket-local copy of the writing skill and changed its concept-level guidance before asking any writers to draft. The experiment will compare three independent complete rewrites with the original guide. The installed skills and authoritative guide remain unchanged.

The user specifically wants to test whether the writing instructions help, rather than install a speculative edit. The evaluation plan therefore fixes both familiar topics and topics not explained in the conversation, and requires checking technical fidelity as well as readability.

### Prompt Context

**User prompt (verbatim):**

> ok, in order to make sure we do the best edits, let's try this: create an updated version of the skill in the scripts/ directory of the ticket, and then start 3 subagents (starting with your current context window) and give each the new skill refs). Let them write their own design doc each, and when they come back, compare and see if the updates you did actually helped properly. The subagents can be sol medium for starters). Before yous tart, repeat back to me what you understood in yur own words and how you will do it.

**User authorization (verbatim):**

> go ahead, commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill)

**Assistant interpretation:** Prepare an uninstalled candidate skill; commission three independent full v2 drafts using Sol medium; compare their actual writing and technical fidelity; record evidence and recommendations.

**Inferred user intent:** Improve the reusable instructions based on observed draft quality, keeping the existing technical intern guidance while avoiding compressed, cryptic design statements.

### What I did

- Created branch `task/ragwb-009-writing-experiment` from `c267f91`, isolating this documentation experiment from the open 010 PR.
- Used docmgr to create three separate candidate documents and a comparison report; added one experiment task without changing 009 implementation tasks.
- Copied `ticket-research-docmgr-remarkable` under `scripts/01-intern-guide-experiment/candidate-skill/`, including its existing supporting references and tools.
- Updated the candidate entry point, writing-style reference and deliverable checklist. Added concept-level explanation progression, concrete consequences, caller responsibilities and a middle/end-of-document comprehension check.
- Recorded original/candidate SHA-256 hashes for the three changed skill files.
- Wrote one shared context packet and a fixed evaluation plan, including source/current-state facts, the user's feedback and the successful explanatory examples.

### Why

The original skill already requests good orientation and concrete examples. The missing emphasis is applying that teaching discipline throughout the document. Testing full drafts also reveals whether clearer explanations dilute technical details or become repetitive.

### What worked

The baseline guide, current code map and prior successful explanations are available locally. Separate output files prevent shared-workspace collisions and preserve independent evidence.

### What didn't work

The subagent tool cannot combine a model/reasoning override with an automatic history fork. As explained to the user before authorization, each Sol-medium agent will receive the same explicit context packet instead. It is a curated reconstruction, not a literal copy of the entire context window.

### What I learned

The evaluation must sample topics beyond the examples provided in conversation. Repeating those examples alone would not show whether the candidate guidance transfers to other mechanisms.

### What was tricky to build

This is not a controlled old-skill/new-skill experiment: all three writers receive the updated skill and conversation examples, and the skill author is the evaluator. The plan limits conclusions to observed qualitative improvements and failures on this document.

### What warrants a second pair of eyes

Review whether the rubric rewards actual understanding rather than longer prose, whether technical edge cases survive, and whether a real new reader agrees with the evaluator's judgments.

### What should be done in the future

Launch the three writers after committing this checkpoint; preserve their drafts unchanged; compare all drafts with the original and recommend only evidence-backed skill changes. Do not install a skill or replace the original guide as an unreported side effect.

### Code review instructions

Start with the candidate writing-style diff, shared context packet and evaluation plan under `scripts/01-intern-guide-experiment/`. The three candidate docs are placeholders until the writers complete. No runtime code changes or tests are required for this setup.

### Technical details

Requested writer configuration: `gpt-6.1-sol`, reasoning `medium`, `fork_turns=none`; identical instructions with only output path differing. Planned outputs: design-doc candidates A/B/C and reference comparison report. The original guide and installed skills are read-only inputs.

## Step 5: Compare complete drafts, then independently inspect the code

All three Sol-medium writers completed their independent guides. Preserved their bytes and recorded hashes rather than editing their outputs to improve the experiment's result. After compaction, the user requested a fresh full reread, an initial set of review notes, and independent code inspection. Read the original and all three drafts again in full, saved the first notes, then inspected the implementation and ran bounded local probes.

The comparison favors B for continuous explanatory prose, A for implementation navigation and C for concision. All three improve causal explanation, but the code review found shared specification ambiguities and an inaccurate success claim in C's rename explanation. The result is a comparison plus separate technical review, not an installed skill or a replacement design.

### Prompt Context

**Earlier authorization:** see Step 4.

**User prompts (verbatim):**

> after you copmared the 3 resulting documents, stop and let me know

> yes, compare writing quality across the entire doc

> after compaction, read the docs again (design doc, subagent docs, then read them (fresh, since we compacteed) and understand what they are about. Write your first set of review notes. Then do your own analysis of the codebase to not just validate how well the docs explained the setup, but if they are correct or not, and what needs addressing in terms of clarity or so.

**Assistant interpretation:** Compare every section, preserve first reading judgments before independent source review, distinguish readability from correctness, then report and stop.

**Inferred user intent:** Establish whether the candidate guidance produces an understandable and technically trustworthy implementation guide, rather than just a polished restatement of inherited claims.

**Commits:** `1928e49` — setup, frozen skill/context/evaluation protocol; `fe227fc` — unchanged writer outputs, draft hashes and fresh pre-inspection reading notes. Final comparison, source review and diary are committed together after validation.

### What I did

- Received A (7,561 words), B (7,126) and C (6,852), compared with the original (6,161), including frontmatter. Counts describe size, not quality.
- Re-read all four complete documents after compaction. Recovered output-truncated ranges with narrower reads so the evaluation did not rely on summaries.
- Saved `reference/03-fresh-reading-notes-before-independent-code-inspection.md` before source analysis, including a whole-document section matrix, strengths, weaknesses and questions to verify.
- Inspected authored JS/TS sources, native persistence/delta creation, artifact metadata/GC, provider bridges, model fallback, runner packing, query/verb/SSE handlers, runtime tooling and the local go-go-goja shutdown bridge.
- Added `scripts/01-intern-guide-experiment/02-code-probes.mjs` and its JSONL output. Six probes exercise duplicate precedence, the fluent example with memory input, shared-build options, synthetic resource closure, decision rename and classified export.
- Wrote the independent technical review and completed the fixed-rubric comparison. Linked them and the preserved drafts from the ticket index.
- Validated all six draft/review frontmatters, relative document links, immutable-input/draft hashes, original-guide preservation and whitespace. Only the writing-experiment task is completed; nine implementation tasks remain open.

### Why

A rewrite can preserve an original design's mistakes while making them easier to read. Independent inspection separates that outcome from actual improvement in technical understanding. Preserving pre-inspection notes makes the effect of source evidence visible: B was provisionally strongest prose, but no draft was thereby approved as an implementable specification.

### What worked

- The final Node probe run exited 0. Cold duplicate selection was `new`, two-view selection was `old`; shared calls returned the same promise with the first signal; a synthetic resource closed on the second GC; rename and export both failed final validation with a missing `route` decision.
- The fluent retrieval/filter/limit example returned one hit and no errors when supplied a memory source. This checks the builder chain, not native directory authorization.
- Source inspection confirms current descriptor metadata already exists, lineage keeps 32 entries but searches eight, only verb handlers hold the explicit admission cap, and sample/decide classify errors before deferred cleanup while rerank classifies later.
- All three rewrites preserve the fixed fidelity gates and provide useful causal explanations beyond the supplied conversation examples.
- Hash checks confirm the candidate inputs, installed skill files and writer drafts remain unchanged; the original design matches its setup revision.

### What didn't work

- Whole-ticket validation encountered raw skill/reference fixtures under `scripts/`: `docmgr doctor --ticket RAGWB-009 --stale-after 30` reported seven `frontmatter delimiters '---' not found` errors. Retrying with `--ignore-dir 01-intern-guide-experiment --fail-on error` did not suppress those parse errors. Preserved frozen skill files and validated the actual ticket documents individually instead; the broad doctor result is not reported as passing.
- The first probe incorrectly invoked the internal function name as a public API: `rag.exportHtml is not a function`. The assertion expecting `/route/` failed. Inspected `pkg/ragjs/src/api/index.js:98`, changed the probe to `rag.export(routed)`, and reran successfully. This was a probe error, not a product defect.
- One broad source-location lookup included absent `../../go.work`; `rg` reported `No such file or directory`. The existing `../go.work` identifies the local dependency used for inspection. Another optional standard-library path `/opt/go/src/net/http/responsecontroller.go` was absent; the file exists under `/usr/local/go/src/net/http/` and was read there.
- Long tool outputs were occasionally truncated; narrower reads recovered the relevant text. No source or draft was changed to work around output limits.

### What I learned

The biggest writing improvement is explicit sequence and consequence, not simply replacing technical vocabulary. B's provider-error and export examples and C's queue-byte example teach mechanisms. However, every candidate still compresses the supporting storage model. Every candidate also inherits ambiguity about resource ownership on failed adoption and about covering plan-level outputs with a node visitor. A self-check saying “implementation remains future work” does not expose these concrete gaps.

The original architecture is broadly supported by current source. Precision is needed at its transitions: cancellation acceptance, callback delivery failure, unfinished-worker ownership, resource handoff and interrupted publication. More framework layers would not resolve an unclear contract.

### What was tricky to build

The comparison must keep three evidence levels separate: reader judgments, directly inspected source behavior, and demonstrated probe behavior. The resource probe uses fake handles and the shared-build probe substitutes build work; they confirm mechanisms without proving a full provider/refresh race. The read-only review does not claim real-socket deadline behavior or crash recovery. Historical PR labels are carried forward, not freshly verified.

There is also a real design tension to explain: a handler can return after a bounded join while a worker still owns resources. The future operation owner must retain that work and bound its admission; blindly releasing the only cap or force-closing handles would contradict other requirements. The technical report names this unresolved policy without implementing it.

### What warrants a second pair of eyes

- Review the adoption success/error contract and the boundary between graph-level and node-level references.
- Check that timeout/cancellation proposals protect native handles even when JS settlement never runs.
- Review interrupted-publication cleanup separately from atomic selection of the current snapshot.
- Ask whether an actual new reader agrees with the whole-document prose ranking. The evaluator authored the candidate guidance, knows the documents and has project context; this is not a blinded or controlled comprehension trial.

### What should be done in the future

If authorized, use B as the base, borrow A's reference/resource diagrams and C's concise sequences/checklist, and correct technical gaps before creating final v2. Refine the skill to require supporting data examples, explicit handoff/error ownership and independent code checks. These are recommendations only. Stop here as requested; do not install skills, merge drafts, upload or begin 009 implementation.

### Code review instructions

Start with the comparison report, then its linked fresh reading notes and independent technical review. Inspect the unmodified drafts against their hashes. Reproduce local probes from repository root:

```sh
node ttmp/2026/11/08/RAGWB-009--simplify-engine-contracts-and-resource-ownership/scripts/01-intern-guide-experiment/02-code-probes.mjs
```

The JSONL receipt records observed behavior, including expected product failures. No runtime code or existing tests changed. Review `git diff --check` and focused docmgr frontmatter validation; do not infer all-ticket doctor success from those narrower checks.

### Technical details

Writer configuration stayed `gpt-6.1-sol`, medium reasoning, separate context with identical packet. Candidate inputs were frozen in `1928e49`; all writers used the same instructions and were prohibited from reading sibling drafts. No old-skill control arm or real-intern test exists. Local source review includes go-go-goja at `e9bc7891a7b4435ec659577dc346647daa548050`. The final reports distinguish preserved requirements from tested implementation. The three drafts and installed skill hashes are recorded under `scripts/01-intern-guide-experiment/`.
