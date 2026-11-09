---
Title: Round two generic guidance five-writer comparison
Ticket: RAGWB-009
Status: active
Topics:
    - rag
    - search
DocType: reference
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://pkg/natives/rerank.go
      Note: Source check of conditional deferred-cleanup race
    - Path: repo://pkg/ragjs/await.go
      Note: Independent check of cancellation versus promise settlement
    - Path: repo://ttmp/2026/11/08/RAGWB-009--simplify-engine-contracts-and-resource-ownership/scripts/02-general-guidance-experiment/candidate-skill/references/writing-style.md
      Note: Frozen generic guidance evaluated by five independent writers
    - Path: repo://ttmp/2026/11/08/RAGWB-009--simplify-engine-contracts-and-resource-ownership/scripts/02-general-guidance-experiment/results-receipt.json
      Note: Artifact hashes, model completion evidence and revision measurements
ExternalSources: []
Summary: All five guides reach a useful explanation floor after self-review; B balances clarity and detail best. Remaining readability gaps and technical corrections are assessed separately.
LastUpdated: 2026-11-08T22:04:06.717160389-05:00
WhatFor: ""
WhenToUse: ""
---

# Round two generic guidance five-writer comparison

## Assessment

**On writing clarity, this batch meets a useful common floor.** All five guides explain the system, connect the twelve findings to their causes, walk through proposed behavior, and provide an implementation path. None relies mainly on a terse catalogue of architectural terms. There is meaningful variation in length and structure, but the explanatory approach carries across the batch.

**B is the easiest overall reading base.** A and C are more compact; D and E spend more time walking through current behavior. Their extra detail is often useful for the requested textbook-style guide, not intrinsically a defect. Every second pass improved explanations of relationships and transitions, rather than only polishing sentences.

The writing is not uniformly polished. Project vocabulary still sometimes appears before its purpose is explained, some abstract storage relationships need a concrete instance, and later sections occasionally contradict earlier ones. Those are the important remaining comprehension problems. Minor source inaccuracies belong in technical corrections; they should not erase the considerable improvement in teachability. D/E contain some more consequential contradictions, which matter because a reader cannot know which rule to follow, not because every sketch must be implementation-perfect.

Keep the generic two-pass method. For the next guidance revision, emphasize first-use explanations, concrete examples where needed, and a whole-document consistency reread. Preserve a separate technical review without turning the writing skill into an exhaustive correctness audit. This is an editorial assessment, not a model ranking or proof that a human intern understands the guides.

## Readability and understandability first

The user's final calibration was: “i'm really kind of interested on the clarity of the writing and understandability, than absolute perfectness.” The primary question here is therefore whether a new developer can form a useful mental model, not whether the guide has settled every implementation detail.

The largest common improvement is that **names now come with behavior**. A snapshot is explained through a query continuing to read an older generation while refresh publishes a new one. A detached result is explained through what a caller can still read after the query ends. Shared work is explained through A cancelling while B still waits. These sequences make the abstractions understandable without requiring the reader to reconstruct the intended behavior from interface signatures.

The guides also connect levels more effectively: a user-visible problem leads to the current mechanism, then to the replacement contract, then to a test or implementation step. Their later chapters generally continue explaining rather than reverting entirely to compressed bullet lists. B sustains that progression most evenly. D/E give a fuller current-system walkthrough, which can be especially helpful for a newcomer who wants to read the guide in order. A/C are easier to revisit as compact working references.

Three writing improvements would make the next batch better without demanding perfection:

- **Explain local terms where they first matter.** A developer may understand an index and a promise but still not know what this project calls a bake, verb or trigger. One sentence about its purpose and a small use is usually sufficient.
- **Show an instance when several abstract nouns interact.** B's tiny artifact/manifest/current-pointer example makes a relationship that A/C mostly explain through prose easier to retain. The general lesson is to instantiate the relationship, not to mandate a storage chapter.
- **Keep one account of the behavior throughout.** If a worked example, API sketch and later decision table differ, the reader has to resolve the inconsistency. A second pass should reconcile them. That matters more for comprehension than minor naming, line-number or wording precision.

I would use any of the five for orientation, with the caveat that they are design candidates. I would choose B for a consolidated guide because it balances explanation, navigation and detail, then incorporate the clearest individual examples from the others. The detailed technical findings later in this report are a separate readiness review, not the primary verdict on writing quality.

## Inputs, procedure and evidence

The [candidate skill](../scripts/02-general-guidance-experiment/candidate-skill/SKILL.md) and its [writing guidance](../scripts/02-general-guidance-experiment/candidate-skill/references/writing-style.md) were frozen in `3dcc6e0`. All five writers received the original guide and repository source, the same substantive assignment, and disjoint output directories. Earlier candidate guides, reviews and explanatory answers were excluded from their permitted inputs. The evaluation criteria were written before launch in [evaluation-plan.md](../scripts/02-general-guidance-experiment/evaluation-plan.md).

Three fresh Codex agents used `gpt-6.1-sol`, medium; two independent tmux processes used `claude-opus-5-5 --effort medium` in safe mode with Read/Glob/Grep/Write. Model overrides prevented full-history forks, so the common context packet carried the task. Every writer saved research, a first draft, review notes, and a final guide. The two Claude receipts confirm successful exit, the requested model, no permission denials, and four Write calls in that order. All frozen input hashes still match.

I read all five final guides end to end, examined their research and review notes, compared first and final drafts, and independently checked important claims against source. No runtime tests were run in this round. Earlier reproduction work remains in [reference 04](04-independent-code-review-of-original-and-candidate-design-guides.md); it is not evidence that these new proposals are implemented or tested. Final writer bodies are preserved exactly beneath docmgr metadata. The [results receipt](../scripts/02-general-guidance-experiment/results-receipt.json) records hashes and bounded completion evidence; verbose CLI transcripts are not committed.

| Writer | Published guide | First draft → final words | Added / removed lines |
|---|---|---:|---:|
| A | [Codex A](../design-doc/05-round-two-intern-guide-codex-a.md) | 4,272 → 5,646 | 77 / 27 |
| B | [Codex B](../design-doc/06-round-two-intern-guide-codex-b.md) | 5,359 → 6,277 | 74 / 23 |
| C | [Codex C](../design-doc/07-round-two-intern-guide-codex-c.md) | 4,081 → 5,401 | 91 / 29 |
| D | [Opus D](../design-doc/08-round-two-intern-guide-opus-d.md) | 7,737 → 8,953 | 376 / 303 |
| E | [Opus E](../design-doc/09-round-two-intern-guide-opus-e.md) | 7,816 → 9,178 | 337 / 287 |

Counts use whitespace-separated words in writer bodies, without docmgr metadata. Line changes are textual, not a measure of quality. All revisions grew; growth was often useful, but these results do not support requiring longer documents.

## Whole-document quality floor

The fixed rubric uses 0 = absent/misleading, 1 = substantial gaps, 2 = mostly clear with specific gaps, 3 = clear and faithful. These are editorial judgments, not measurements of comprehension. There is deliberately no total score.

| Dimension | A | B | C | D | E |
|---|---:|---:|---:|---:|---:|
| Orientation and local terminology | 2 | 2 | 2 | 2 | 2 |
| Entity scope and relationships | 3 | 3 | 3 | 2 | 2 |
| Sequences and responsibility | 3 | 3 | 3 | 2 | 1 |
| Correctness and evidence discipline | 2 | 2 | 2 | 1 | 1 |
| Actionable implementation and tests | 3 | 3 | 3 | 2 | 2 |
| Proportional clarity across the document | 2 | 3 | 2 | 2 | 2 |

The weaker scores for D/E concern specific contradictions and overclaims below, not their sentence-level clarity, length or choice of structure. This rubric deliberately includes correctness, so it must not be read as a pure readability ranking. Every guide has a useful account of each major subsystem; the writing assessment above is the primary conclusion.

| Part of the guide | What is consistently explained | Remaining variation or gap |
|---|---|---|
| Orientation and current execution | All begin with a working system, source paths and a query/build flow. B/C offer an existing offline reports command; D/E provide a longer current-code walkthrough. | Bake, verb, Pi and trigger semantics still receive uneven introductions. Familiar Go/TypeScript vocabulary does not imply knowledge of project terms. E also conflates the served shell with a browser-local engine. |
| Findings and rationale | All preserve the twelve findings and map them to common causes or local fixes. A/B/C describe incomplete safeguards rather than claiming none exist. | D/E turn a scheduling-dependent rerank race into an inevitable outcome. Some statements about old PR status are copied as current facts without checking GitHub. |
| Datasets and record identity | All distinguish collection interface from storage, carry dataset-qualified hits, and explain why raw IDs lose provenance. | A gives the fullest document-reference relationship; C states the limitations of a single source-document binding. D/E reopen the fusion default and rely too broadly on current digests as content identity. |
| Snapshots, resources and results | All explain leases, sharing and copying result records before release. | B/C define adoption failure ownership precisely; A leaves duplicate rejection's actor unclear. E's adoption sketch contradicts its own API. D/E leave publication ordering between competing builds underspecified. |
| Durable storage | All separate inventory from lineage and require exclusive ownership, conservative collection and publication ordering. | B supplies the clearest small metadata example. A/C still introduce several metadata nouns mostly through prose. All need a more explicit account of payloads moved into final paths before their descriptors exist. |
| Operations and HTTP | All separate waiter cancellation from shared work and require bounded writes, owner-thread delivery and real-socket tests. | A explicitly retains an engine task charge for unfinished workers. B/C qualify cancellation bounds. D identifies `awaitPromise`, then overclaims what waiting for JS settlement proves. E leaves that path uninspected. |
| Graph edits and export | All explain canonical references and explicit lexical export policy. | A/B/C/E include graph-level outputs; D's node-only visitor leaves that responsibility implicit. D/E promise compile-time rejection of undeclared arbitrary callback references without a sufficient enforcement account. |
| Migration, tests, risks and decisions | All end with coherent phases, deletion targets, behavioral tests and measurements. | D/E's later decision tables contradict or reopen earlier commitments. C's general development-command appendix is less useful than its architectural content. B best distinguishes decisions that block implementation from private naming choices. |

## What changed in the second passes

### A: strong ownership explanation, some remaining abstraction

The [review notes](../scripts/02-general-guidance-experiment/writers/codex-a/review-notes.md) identify problems in the saved draft rather than merely claiming it was reread. The final dataset section adds concrete D1/C1/C2 relationships and a full document reference for ordering. The snapshot section separates stable publication slot, content-specific job and published generation. The operation and transport sections add an actual terminal arbiter and distinguish HTTP admission from still-running engine tasks. These are substantial improvements with implementation consequences.

Two particularly useful passages are “Resource adoption is not publication” and the explanation that releasing an HTTP slot must not allow unlimited abandoned workers. The acceptance matrix also covers trace-only records and unopened lazy payloads.

Limitations remain. “Bake” appears in the initial reports example without explaining the feature. The storage section still expects a reader to assemble descriptor/manifest/pointer relationships mentally. The duplicate-adoption paragraph says “reject and close it” after an opener-closes-on-error rule; it should name which actor closes on rejection. That is an ambiguity, not proof of an implemented double close.

### B: best balanced explanation and fidelity

B's [review notes](../scripts/02-general-guidance-experiment/writers/codex-b/review-notes.md) catch a first-draft contradiction between exact-reference fusion and rejecting mixed datasets, and the final removes it. The revision makes dataset kinds concrete, explains callback input binding, separates content identity from slot identity, and qualifies cancellation after publication commit. It also adds source-version consistency as a requirement rather than pretending an initial hash pins later file reads.

The strongest passages are the short metadata example in “Make the store an authoritative ownership inventory,” the success-only adoption handoff, and the distinction between an accepted outcome, worker completion and VM delivery. The final implementation order names existing tests and the old mechanism each change removes. It reads as an actionable guide without burying the architecture in a long decision catalogue.

It still assumes some local vocabulary, notably bake and verb. One small source claim groups `explain` with helpers retaining `art`, although `explain` reads result trace rather than the artifact map. The storage example is expressly illustrative: its dependency arrows must not be mistaken for verified dependencies of the existing codecs. Orphan recovery after payload relocation also remains a design gap. B is a revision base, not a finished specification.

### C: concise, concrete callback and provenance guidance

C's [review notes](../scripts/02-general-guidance-experiment/writers/codex-c/review-notes.md) lead to a named bake-binding example, a versioned source-document relationship and an explicit limitation for components combining document versions. The final separates network transmission from snapshot-dependent materialization and distinguishes worker cleanup from VM delivery. It adds graph-level output references to the visitor and keeps unsupported opaque rewrites outside the language contract.

Its existing offline entry point and source-path convention help a newcomer navigate. Its exact success/error adoption contract and example of a record present only in a rerank trace make the resource/result boundary unusually clear.

Like A, the durable-store chapter would benefit from one concrete metadata instance. Local terms are still unevenly taught, and the last pages include general repository workflow instructions that do less explanatory work than the architecture examples. The late draft edits do not resolve these weaknesses. The final remains shorter and easier to navigate than D/E, but brevity alone does not eliminate inferred relationships.

### D: useful independent discovery, incomplete reconciliation

D's [review notes](../scripts/02-general-guidance-experiment/writers/opus-d/review-notes.md) make extensive source-driven corrections. The final repairs an incorrect resource-sharing example, separates `planKey` from `contentKey`, fixes the sink-overflow description, and labels the proposed task group as new. Most usefully, it finds `ragjs.Eval`/`awaitPromise` returning on caller cancellation before the JavaScript promise settles. It also removes a duplicated reference representation from its first graph sketch.

The revised sharing diagram and current execution walkthrough are good teaching material. The explanation of why a runtime needs worker accounting independently of promise delivery is also valuable.

However, §8.4 then claims waiting for JS settlement makes the semaphore count work, while §8.1 explicitly allows the promise to settle before the worker exits. The guide needs retained worker capacity as well as settlement tracking. §6.1 says partial outputs are never current, while O1 proposes publishing them. D also changes the original freshness rule into an open per-host policy, omits graph-level outputs from its node-oriented visitor contract, and asserts a rerank race is “always” observed. The second pass found real problems but did not reconcile the whole revised document.

### E: helpful examples, significant contract errors

E's [review notes](../scripts/02-general-guidance-experiment/writers/opus-e/review-notes.md) produce a useful four-case cold/warm precedence table, repair memory-versus-store dataset identity, separate build slot from content identity, and distinguish in-memory from durable retention. The final also notices that namespace deletion through `Prune` must participate in locking. Those are substantive improvements, not stylistic polish.

Its current-flow explanations and explicit decision table make many assumptions visible. Its per-node saving versus end-of-build publication explanation is worth retaining after review.

But §8.4's API closes an input on an error, while the caller closes it again; successful duplicate adoption closes `rec`, then the caller wraps that same `rec`. The running example cannot fit two 30-second watcher ticks into a 20-second query. It changes private partial outputs into publishable snapshots and changes the fusion default. It admits not inspecting `Eval`, but still writes the preceding teardown story as if cancellation ensures query/resource completion. It therefore needs technical correction before becoming implementation authority. These problems do not negate its useful explanatory walkthrough.

## Independent technical checks and necessary corrections

These are source/contract reviews, not new reproduced failures. Paths below are repository-relative; section references identify candidate passages.

| Finding | Evidence and correction |
|---|---|
| E's ownership example can double-close or wrap a closed reader | E §8.4 says `Adopt` closes `c` after registry closure, then its caller executes `rec.Close()` on error. On duplicate success it closes the new `c`, returns only `error`, and wraps the same `rec`. Return a registry-owned handle; define transfer on success only, or consistently define consumption on every outcome. B/C already express the former contract. |
| Partial-publication requirement is not open in the original | Original §7.1 explicitly says partial output may be retained privately but is never the current query snapshot. D §6.1 preserves that, then O1 recommends the opposite. E §8.1/D12 recommends publication. Label a proposed change to an accepted requirement and give its rationale; do not treat it as an unspecified parameter. |
| JS settlement does not prove worker termination | `pkg/ragjs/await.go:awaitPromise` returns on context cancellation. D correctly identifies this. Its proposed §8.1 settles cancellation promptly while workers remain in `TaskGroup`; §8.4 nevertheless concludes waiting for that promise makes the slot count work. Track or charge unfinished workers separately, or require joining before claiming actual work capacity is released. E should inspect this path rather than leave a central lifecycle dependency unresolved. |
| Rerank misclassification depends on callback scheduling | `pkg/natives/rerank.go:rerankAsync` posts a callback, then deferred `cancel` runs on worker exit. The owner may execute the callback before that exit. D §3.4 and E §3.8 should say the error can be misclassified if cleanup precedes callback classification, not that it always is. B's conditional explanation is accurate. |
| Current digest is not an unconditional content-equivalence proof | `Engine.digests` explicitly excludes built-in source; `runner.ArtifactNamespace` separately includes native version and bundle bytes. D §5.1 and E §7.3 need a clearly scoped identity guarantee and versioned target identity. Namespacing disk storage does not by itself make a bare dataset digest globally content-identifying across implementations. |
| E's running timeline is impossible as stated | §2.2: watch every 30 seconds, query lasts 20 seconds. §4: two periodic refreshes occur during that query. Lengthen the query or explicitly introduce additional manual refreshes. This is an example check, not a source-code defect. |
| Metadata publication needs a recovery account between steps 3 and 4 | All guides move payloads before writing descriptors. After a crash there, a final-location payload can lack authoritative ownership metadata. “Collect orphaned payloads later” and “never guess/delete unknown files” need a reconciliation: a durable staging/transaction ownership record, another publication layout, or an explicit safe recovery rule. This is a shared remaining design obligation, not a reason to delete unknown files. D's durability sequence also names directory sync without explicitly syncing payload data. |
| Visitor scope and opaque callbacks need enforceable boundaries | D §9.1 exposes a node visitor but does not say who enumerates top-level outputs. D/E promise compile errors for undeclared callback reads; arbitrary JS cannot be soundly discovered by the retained regex or inferred declarations. Specify a restricted callback interface/language and reject unsupported transformations, as A/B/C describe, or provide a real validation mechanism. |
| Several lower-impact current-behavior claims need precision | `decorateResult` uses `art` in `chunk`/`top`; `explain` operates on trace. `sseWriter.write`/`ping` return write errors while `pump` discards them; E conflates those responsibilities. `web/app-shell/shell.js` is the remote served-app adapter, not evidence that its searches use a browser-local memory store. E's blocked `/run` pump need not imply its query worker remains active. |

The fixed safety gates are largely present across all five: late waiters retry, resources are cleaned even if VM delivery fails, leases enclose result copying, namespace ownership excludes competing collectors, and ordinary callers do not close results. There are no compatibility adapters. D models refresh as an engine/timeout-owned waiter rather than a separate background-owner type; that can preserve the required behavior, provided it remains independent of HTTP waiters. Its change of terminology is not itself a defect. D's corrupt-metadata paragraph should explicitly cover manifests and current pointers as well as descriptors. E's partial-publication deviation is a substantive failure of requirement fidelity.

## What to change in the generic guidance next

Keep research before drafting and the complete-draft second pass. The artifacts show that both uncover consequential omissions. Do not turn the skill into a longer domain-specific checklist. Instead make the reread perform a few concrete, general tasks:

1. **Follow the document as the reader's only source.** At the first use of a local concept, explain what it does and why it matters there. Define only the terms the reader needs; a glossary at the end does not repair an unexplained early example.
2. **Execute each important example mentally against its stated rules.** Check identities, arithmetic, ordering, return values and error branches. If prose says an operation consumes or transfers something, trace both success and failure through the code sketch. This generalizes beyond resource APIs to data transformations, permissions and state changes.
3. **Reconcile the whole document after revision.** Compare opening promises, diagrams, API sketches, decision tables and acceptance tests. Remove contradictions introduced by the second pass. Mark deliberate changes to existing requirements explicitly; distinguish an unknown implementation detail from a settled product constraint.
4. **Verify claims that carry the design's argument.** Trace the relevant source boundary and inspect the implementation behind a helper when the argument depends on it. Qualify source inference, observed behavior and proposed guarantees. Avoid “always,” “never,” or “impossible” without the needed conditions.
5. **Use one small concrete instance for an abstract relationship, then trim repetition.** A record, message, request, state transition or dependency example often teaches more than another paragraph of terminology. Add it only where the relationship is otherwise hard to reconstruct.

An independent reader remains useful after self-review. The author knows facts the document may omit, and this experiment shows that an author can record an excellent review note without fully implementing it. Ask the independent reader to explain a representative flow and a failure using only the guide, then check the explanation against code. That is stronger evidence than a claim to have reread with fresh eyes, although a model-simulated reader still is not a human comprehension test.

## Recommendation and limits

Retain the two-pass method and use B for a future consolidated guide. Borrow A's worker-capacity explanation, C's explicit callback binding, D's `awaitPromise` discovery and E's precedence table and `Prune` observation. Correct the shared recovery gap and the specific issues above before treating any guide as implementation-ready. Do not merge their policy proposals automatically.

This was one project with five runs, no old-guidance control group, no blind evaluator and no actual intern. Guidance, mandatory source study and writing procedure changed together, and the writers had different tool environments. The observed revisions support keeping the method; they do not isolate its causal effect or establish a general model ranking.

The original guide and installed skill remain unchanged. The five candidates and this comparison are the deliverables. Stop here for user review; 009 implementation remains a separate task.
