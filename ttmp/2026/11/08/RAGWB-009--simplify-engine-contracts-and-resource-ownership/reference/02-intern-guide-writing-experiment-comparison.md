---
Title: Intern guide writing experiment comparison
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
LastUpdated: 2026-11-08T21:10:10.034877834-05:00
WhatFor: ""
WhenToUse: ""
---

# Comparing three candidate-guided intern design drafts

## Experiment and limits

The setup was fixed in commit `1928e49`. Three independent `gpt-6.1-sol` agents at medium reasoning received the same ticket-local candidate skill, shared context packet and complete original 009 guide. Only their assigned output path differs. Writers cannot read each other's drafts or the evaluator's plan/report. They can inspect current source read-only. The parent reviews the actual documents and records evidence here.

The explicit context packet replaces an automatic history fork because the subagent API does not permit a model override with a full-history fork. It contains relevant project facts, user feedback and the successful explanatory examples; it is not a verbatim full transcript.

This is a qualitative replication on one topic, not an old-skill/new-skill controlled trial. All writers see the supplied examples; the evaluator authored the candidate skill and knows which document is which. No real intern comprehension test was performed. Results can establish observable differences and repeated weaknesses, not isolate causation or generalize to all design writing.

The candidate adds guidance at the individual-concept level: concrete entities, situation, failure, proposed behavior, caller responsibility, then technical contract. It asks for invariants after explanation and a manual comprehension check in middle/end sections. Installed skill files remain unchanged.

## Baseline reading

The baseline is the full original guide, including its explanatory sections and the TypeScript appendix, not just the invariant list. It already has meaningful technical explanation: section 7.1 follows snapshot A through refresh and release; section 9.2 follows two build waiters; sections 8 and 14 contain detailed ownership/publication constraints and tests. These strengths must be retained rather than credited as new discoveries by the rewrites.

Its weaker passages often present several abstract terms before a concrete failure. Section 5.2.6 says, “A bounded build-search history is never the authoritative artifact inventory.” Section 8.1 later calls lineage a “derived accelerator for incremental-base lookup,” but never walks the reader through build 33 dropping an old history entry while its files remain. The conversation explanation supplied that missing consequence.

Similarly, section 9.1 describes “one immutable terminal outcome” and gives compact pseudocode before a concrete example of a provider failure misclassified after cleanup cancels its context. The original is precise enough for an engineer who already knows the issue, but asks the intern to infer the sequence. Graph normalization has useful before/after data but introduces capabilities and transformation policy densely. Section 2 still describes the engine as concentrated in the old asset while section 16 later corrects the current source map.

## Whole-document review coverage

The user clarified during drafting: “yes, compare writing quality across the entire doc”. The review therefore includes every section, not just the originally questioned invariants. The fixed rubric already requires reading every draft; the close-reading sample supplements that complete review.

The comparison covers opening orientation and current-state accuracy; terminology and transitions; dataset and provenance explanations; snapshot/resource/result ownership; inventory and crash behavior; operation and cancellation sequencing; streaming transport; graph edits/export; migration and deletion; phased implementation/tests; and the usefulness of the ending/self-check. It will distinguish prose quality, technical fidelity and implementation usability rather than collapse them into one winner score.

## Results and recommendation

All three drafts explain the failures and proposed behavior more clearly across the document, not only in the initially disputed invariant list. **B is the preferred base for a later v2** because its connected prose gives a good balance of mechanism, caller responsibility and technical constraints. A is the strongest implementation reference; C is the most economical complete guide. None should be adopted unchanged.

Independent code inspection changed the evaluation: preserving the original's requirements is not sufficient to establish correctness. There are shared contract gaps and one concrete new inaccurate statement in C. Read the [independent technical review](04-independent-code-review-of-original-and-candidate-design-guides.md) before choosing material to combine. The [fresh full-reading notes](03-fresh-reading-notes-before-independent-code-inspection.md) preserve the writing judgments made before that inspection, including the whole-document section matrix.

### Manual rubric

Scores are anchored judgments under the predeclared 0–3 rubric, not measurements of reader comprehension. There is no aggregate score; a prose advantage cannot cancel an inaccurate statement.

| Dimension | Original | A | B | C | Evidence |
|---|---:|---:|---:|---:|---|
| Terms/entities before dependent claims | 2 | 2 | 2 | 2 | All teach primary concepts; all compress descriptor/manifest/root/pointer vocabulary in storage sections. |
| Actors, sequence and consequence | 2 | 3 | 3 | 3 | Original already explains snapshot/waiter sequences; each candidate adds provider-error chronology, export consequences and queue/write distinctions. |
| Why behavior prevents failure; responsibilities | 2 | 3 | 3 | 3 | A §§5–6, B §§4–5 and C §4 explain engine retention versus caller use and why copied results need no close call. |
| Fidelity and evidence/status accuracy | 2 | 2 | 2 | 2 | Proposals stay labelled. Original's source narrative is stale; all retain unresolved handoff/traversal contracts. C additionally misstates successful rename; see technical review. Same score does not imply equal defects. |
| Prose connected to APIs, files and tests | 3 | 3 | 3 | 3 | All retain substantial source maps, sketches, staged migrations and acceptance matrices. These are useful guidance, not proof of complete call-site coverage. |
| Readability and proportionality | 2 | 2 | 3 | 3 | A repeats review rules in several forms; B sustains explanatory prose; C retains coverage with less repetition. B/C still have dense reference paragraphs. |

Counts including metadata: original 6,161 words; A 7,561 (+22.7%); B 7,126 (+15.7%); C 6,852 (+11.2%). The added length buys concrete explanation in many places, but the storage gap survives in all three. Word count is not a quality score.

### A: strongest detailed reference, more repetition

[Candidate A](../design-doc/02-v2-intern-guide-candidate-a.md) opens with the actual query, section text and citation at risk. Its four-object introduction makes the architecture usable before presenting history and source navigation. Section 6's diagram shows registry I shared by A and B: finishing A's query does not close I while B still uses it. Section 8 follows provider failure, deferred cancellation and late callback classification in the right causal order. These are successful explanations, including transfer beyond the conversation examples.

Its current and historical source tables are easier to scan than B/C's historical paragraph. Separate phase headings and a detailed test matrix make the latter half useful during implementation. These strengths are present throughout the document, not just the opening.

The cost is repetition: several “Review invariants,” decision records and the final twelve questions reiterate the same rules. Section 7 still says “Store immutable snapshot manifests listing root artifacts” without showing a manifest or defining a root. Section 8's synchronous-looking code leaves the actual acceptance/delivery/cleanup transitions in comments. The ending's generic command placeholder is less useful than a concrete reports invocation. Technical review also found the shared adoption and graph-traversal ambiguities; detail alone did not prevent them.

### B: preferred connected explanation

[Candidate B](../design-doc/03-v2-intern-guide-candidate-b.md) §4 explains what each dataset method means before showing the interface, then assigns materialization choice to the application and backend choice to the engine. Section 7 distinguishes worker classification from delivery in the later callback. Section 9 explains how socket buffers fill and why a queue bound cannot unblock a write; “Internal terminal outcome and successful client receipt are separate facts” is supported by the preceding sequence. Section 10 provides the clearest complete classifier-removal example: unsupported classifier, dangling condition, deliberate unconditional retrieval, final validation.

The later reuse section also explains which responsibilities remain specialized, rather than merely listing module names. Migration, acceptance and risks retain the meaningful technical requirements. This makes B the best base for a document an intern reads continuously.

Weaknesses remain. The opening spends two paragraphs on status/commit/PR information before its concrete document example. Section 3 compresses nine historical source anchors into one long paragraph. Section 5's “Retiring A releases A's use of I” needs the query-lease condition in that sentence, although the next sentence supplies it. Section 6 still compresses storage metadata, and the outcome sketch hides concurrency in comments. Its `go run ./...` instruction needs an actual package. Prefer B as a base, not as an approved final design.

### C: concise and strong sequences, one factual regression

[Candidate C](../design-doc/04-v2-intern-guide-candidate-c.md) keeps the resource sequence concrete: Q retains A; A releases its use only when no longer needed; B keeps X open; final release closes X. Its shared-job explanation distinguishes the useful shared promise from incorrectly inheriting a request's lifetime. Section 8's “A queue of ten events can still contain ten enormous strings” explains byte limits directly. Its lifecycle diagram and post-explanation review checklist are particularly useful.

The ending retains phased implementation, test criteria and open policies without A's volume of repeated invariants. It is the best source for concise transitions and the review checklist.

However, its opening introduces several target abstractions in one paragraph before developing them. Historical navigation and storage metadata are compressed. Most seriously, §9 says the editor reports rename success with a stale reference; the probe shows final validation rejects it. The same section describes pinned outputs as another node's dependency, obscuring plan-level output bindings. This is why a readability assessment alone must not endorse a final document.

### Technical constraints retained and remaining uncertainties

All three retain the fixed fidelity gates from the evaluation plan:

| Gate | Where retained |
|---|---|
| Late waiter does not join a cancelling job; background refresh owner keeps work alive | A §9, B §8, C §7 |
| Runtime disposal cannot bypass task/resource cleanup | A §8, B §7, C §6 |
| Failed refresh keeps last usable snapshot | A §5, B §5, C §4 |
| Required result data is copied before lease release; no ordinary caller close | A §5, B §5, C §4 |
| Missing/corrupt ownership metadata stops destructive GC | A §7, B §6, C §5 |
| Exclusive writable namespace owner and locked offline collector | A §7, B §6, C §5 |
| No legacy adapters; replacements remove old paths | A §§12–13, B §§11–12, C §§11–12 |

These are retained requirements, not proven implementations. The technical review found that adoption error ownership, graph-level output traversal, operation arbitration, timed-out worker ownership and interrupted-publication recovery still need precise contracts. Existing source also provides useful nuance absent from the prose: only verbs currently have the admission cap; sample/decide already classify before cleanup; lineage retains 32 entries but searches eight; Meta descriptors already exist. A future v2 should incorporate these distinctions.

## What the experiment supports about the candidate skill

The output is consistent with the intended improvement: writers explain concrete entities, sequences, failures and caller duties throughout the guides. The clearest transfer examples are terminal classification, transport backpressure and export projection, rather than only collection/snapshot paragraphs supplied by the conversation. The guides preserve the original's useful technical depth and generally avoid analogies.

It does not show that the skill alone caused this improvement. The context packet supplied examples and direct user preferences; all writers used the candidate; the evaluator knew the setup and authored the guidance. Independent writers on one source are useful replications, not a control group. No real intern has yet demonstrated comprehension.

The most important result is the limit: **better causal prose did not automatically yield clearer supporting data models or more precise ownership contracts.** All three can explain why a registry is needed while leaving error ownership ambiguous. All can describe a storage failure while leaving the new metadata unexplained. The author self-checks mostly acknowledge that implementation remains future work; they do not discover those specific specification gaps.

## Recommended guidance changes, not applied

1. Apply “define before use” to supporting metadata and control state, not only top-level nouns. Show one small descriptor/manifest/pointer example when those objects carry the correctness argument.
2. For handoffs and races, name the transition owner and explain success, error, cancellation and shutdown paths. A comment saying “atomic” or “asynchronous” is not an explanation of where arbitration occurs.
3. Check technical claims independently against source after the prose review. Distinguish rejected operations from silently accepted invalid state, existing safeguards from missing mechanisms, and local facts from historical review reports.
4. Make diagrams and pseudo-APIs cover the objects they claim to own: node visitors cannot silently stand in for plan-level outputs; request completion cannot silently stand in for worker cleanup.
5. Keep historical anchors in a table or appendix and consolidate review rules after teaching. Avoid repeating status and invariants at every boundary.
6. Require a self-check that identifies a concrete remaining ambiguity and its consequence, not merely “this design is not implemented.” Keep unresolved policy choices explicit without adding speculative frameworks.

If authorized later, use B as the base; borrow A's source table/resource diagram and C's resource trace, byte-limit example and concise checklist. Correct the technical findings before selecting polished prose. A small controlled old/new-skill comparison or actual intern reading would strengthen evidence further, but is not required to act on these observed weaknesses.

## Stop boundary

The experiment, fresh reread, independent source review and comparison are complete. Drafts and candidate inputs remain preserved. No installed skill was changed; the original design was not replaced; no upload, final merged v2 or 009 implementation was performed. The next decision belongs to the user.
