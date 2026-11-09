---
Title: Round three clarity guidance five-writer comparison
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
Summary: "Third five-writer experiment: clearer concrete explanations and better local teaching across the batch; remaining issues are density, uneven terminology and a few coherence gaps."
LastUpdated: 2026-11-08T22:52:45.366818266-05:00
WhatFor: ""
WhenToUse: ""
---

# Round three: does the guidance produce understandable guides?

## Main conclusion

**Yes: the five guides share a useful level of explanation for a new developer, and the revised instructions produced visible improvements.** The improvement over round two is incremental rather than a wholesale transformation. The stronger passages now explain a term's purpose, show a concrete instance, and tell the reader what to notice. That pattern appears in the middle and later chapters as well as the introductions.

I would keep this version of the generic guidance. B is the best balanced base for a consolidated working guide; D contributes particularly good newcomer explanations and a fuller current-system walkthrough. A is close to B in overall clarity, C is a useful compact account, and E has strong explanatory examples but needs a central identity explanation repaired. These are judgments about these documents, not rankings of models.

**The next useful editing step is selective tightening, not another layer of instructions.** Remove repeated migration/completion material, repair the few terms still left implicit, and reconcile the identified inconsistent relationships. The guidance is ready to try on a different real design task; repeating this same case cannot establish how general it is.

## What changed in the guidance

The [candidate skill](../scripts/03-clarity-guidance-experiment/candidate-skill/SKILL.md) remains ticket-local. Its [writing guidance](../scripts/03-clarity-guidance-experiment/candidate-skill/references/writing-style.md) adds six generic directions to round two:

- Teach a project-specific term at its first consequential use: its purpose, not merely a synonym.
- Give a small concrete instance when abstract objects or relationships are otherwise hard to reconstruct.
- Sustain the explanatory thread through the middle and ending.
- Separate comprehension gaps from technical corrections in review notes.
- Mentally execute important examples, proportionally.
- After revising, reconcile the entire document so prose, examples, diagrams and decisions agree.

These changes contain no rag-workbench answers or corrected API designs. The complete first draft and saved review notes remain part of the two-pass procedure; the consistency reread is not a required third draft. No fixed length or mandatory subsection template was introduced.

The setup was committed in `6cc526d`. The [change summary](../scripts/03-clarity-guidance-experiment/guidance-changes.md) and [evaluation plan](../scripts/03-clarity-guidance-experiment/evaluation-plan.md) were frozen before launch. The latter makes understandability primary and keeps technical review separate, following the user's explicit preference.

## Reading the whole documents

I read all five final guides through their implementation plans, tests and ending decisions. I also inspected the research and pre-revision notes, compared first/final passages and diffs, and made bounded source checks where an example or central claim depended on them. [Reading notes](../scripts/03-clarity-guidance-experiment/reading-notes.md) record specific strengths and remaining burdens. This is an informed editorial review, not a fresh-context reader or human-intern comprehension study.

| Guide | How it reads | Strong passages | Remaining reader work |
|---|---|---|---|
| [A](../design-doc/10-round-three-intern-guide-codex-a.md) | Coherent explanation of causes and replacements, with useful source navigation | §2 defines provider, reranking, digest, artifact, lineage and bake through their purposes. §3 shows P/E datasets and a snapshot/index/hit/lookup relationship. §7 shows what renaming a callback binding actually changes. | Verb and namespace remain assumed terms. §5's descriptor/manifest/pointer explanation is mostly abstract. Some early status/source-layout detail interrupts the ordinary-use story; later decisions repeat already-explained choices. |
| [B](../design-doc/11-round-three-intern-guide-codex-b.md) | Best balance of explanation, detail and navigability | Ordinary query → evidence label → citation → trace explains why provenance matters. The bake example distinguishes a graph node from its callback-local name. The storage example and explanation of `route.needs` carry the teaching approach into later chapters. | Verb and namespace are not introduced clearly; bake first appears in a findings table before its explanation. A few lifecycle paragraphs combine too many responsibilities. The ending repeats some constraints and decisions. |
| [C](../design-doc/12-round-three-intern-guide-codex-c.md) | Shortest, direct account; useful as a working reference | Actual original/enriched text makes refs understandable. The manifest-shaped example says which object supplies text, ranking and the consistent generation. The revised ownership sequence explains why releasing A does not close an index B still uses. | Signals are illustrated rather than fully defined; verb remains implicit. The transport paragraph becomes a dense set of requirements. The source-path convention arrives late, and the storage chapter could use a concrete ownership record. |
| [D](../design-doc/13-round-three-intern-guide-opus-d.md) | Strong textbook-style current-system account and explicit local vocabulary | §2 explains triggers and verbs. §6 separates query leases from registry owners. The result JSON and before/after adaptive-export graph show what the proposed changes produce. | Goja itself is not briefly defined; artifact/namespace usage is more assumed. The source-document relationship is less explicit than in A/B/C. Migration, deletion, tests, risks and decisions repeat some of the same teaching. |
| [E](../design-doc/14-round-three-intern-guide-opus-e.md) | Detailed, approachable walkthrough with useful concrete structures | Host/owner-goroutine explanation; actual artifact object; four served functions; cold/warm precedence table; detached response and neighbor navigation. | §6.1 conflates changing build versions with the stable place whose current snapshot is replaced. Trigger is used without its purpose being taught. The long ending repeats module, reuse, deletion, migration, acceptance and decision inventories. |

There is no summed score. A guide can explain a subsystem well while containing a technical correction; it can also be technically detailed yet ask the reader to infer too much. The relevant common floor is that **all five explain ordinary operation, the reasons for change, key object relationships and the next implementation steps**. The differences concern how much explanation is implicit, how smoothly it continues and how much repeated material the reader must carry.

## Concrete evidence that the second pass helped

### From a type declaration to something the reader can resolve

C's first draft says “The proposed hit states the relationship explicitly,” then immediately shows generic TypeScript interfaces. The reader has to invent an actual pair of datasets and the differing records.

The final adds two references for the same local ID and shows the text each resolves to:

```js
const original = { dataset: 'chunks-v1', id: 'reports/engine.md#ownership' };
const enriched = { dataset: 'chunks-v2', id: 'reports/engine.md#ownership' };
// original text: 'Retain readers until the query finishes.'
// enriched text: 'Engine ownership: Retain readers until the query finishes.'
```

It follows that instance with the explanation: “The hit carries the version that supplied its text. Its logical ID alone would lose that information.” The interface now formalizes a relationship the reader already understands. This is precisely the kind of improvement the generic guidance sought.

### From unexplained project terms to useful concepts

D's first draft uses “submit-triggered” in its export discussion and refers to verb endpoints without introducing those features. Its review notes explicitly ask “What is a trigger?” and “What is a verb?”

The final explains `change` versus `submit` in the ordinary-use section and gives the reason costly model steps wait for submit. Before discussing HTTP differences, it defines verbs as named operations declared by an app and exposed through endpoints, including the fact that they may run other plans on the same engine. Those short additions make later arguments understandable without a detour into source.

This is not perfectly consistent across the batch: A/B/C still assume “verb,” and E's description is thinner. The skill improves the odds; it does not guarantee that every author notices every local term.

### From a feature list to the reason record identity matters

B's first draft introduces “pack evidence” and “trace” largely as pipeline vocabulary. Its final walks a query through ranked passages, evidence labels such as `E1`, an answer citing `[E1]`, and per-node trace data. It then states why preserving hit text matters beyond the initial search.

That small paragraph connects the public result to the architecture before the reader encounters ownership details. B also replaces a vague suggestion about callback bindings with a sketched call and explains both names in it. These are improvements to teaching, not merely additional technical coverage.

### From a plausible example to a faithful explanation

Both Opus writers initially used sections followed by JavaScript enrichment as a cold/warm disagreement example. During self-review they traced persistence and found that ordinary enrichment arrays are stored as JSON and remain arrays, while native records load as views. Their finals distinguish the cases with a small table and stop claiming that ordinary enrichment necessarily reproduces the bug.

I checked the relevant `Persist.InstallEnv` save branches in `pkg/natives/persist.go` and `unionChunks` in `engine/records.js`. This is an example of the mental-execution instruction improving both explanation and accuracy. It is not a new executed regression test.

### From a review plan to actual document changes

All five saved a complete first draft and specific notes before revision. Their finals visibly implement multiple named changes. The three Codex guides add concrete identities, bindings and responsibility transitions. D/E add current-host details, result examples and more explicit decisions. The [receipt](../scripts/03-clarity-guidance-experiment/results-receipt.json) records exact file hashes and sizes so these changes remain inspectable.

| Writer | First draft words | Final words | Observation |
|---|---:|---:|---|
| A | 4,936 | 6,084 | Adds definitions, linked identity example, binding example and clearer transitions |
| B | 5,659 | 6,465 | Adds query-result explanation and precise local examples without large structural expansion |
| C | 4,372 | 5,464 | Adds concrete records, a manifest-shaped example and clearer end-stage decisions |
| D | 5,918 | 8,738 | Adds useful vocabulary/examples and restores substantial scope; some repetition accumulates |
| E | 6,123 | 9,369 | Adds extensive explanation and restored scope; ending could be consolidated |

Counts exclude docmgr metadata and do not measure quality. Same letter means an independent writer assignment, not the same model session from round two.

## What improved relative to round two, and what did not

**More consistently concrete relationships.** A/B/C all now put actual values or small data structures beside important identity relationships. D/E show result records and transformations rather than only interfaces. The reader has fewer objects to invent mentally.

**Better local teaching, but incomplete coverage.** A's provider/reranking/digest explanations, B's evidence/trace explanation and D's trigger/verb explanations are clear improvements. Several other terms remain implicit. This suggests one targeted editorial reread for local vocabulary, not an exhaustive glossary mandate.

**Explanation continues past the introduction.** Callback bindings, operation outcomes, detached responses and graph rewrites are taught with consequences and examples. Most implementation/testing sections are necessarily more list-like, but they usually refer to behavior already explained. That is appropriate; not every test needs another narrative.

**Whole-document consistency is still the weakest part of self-review.** E saves a clearer snapshot example but gives it an identity that changes when the data changes, while describing replacement under the same current key. The extra explanation makes the intended idea visible but does not make it coherent. D explicitly explains its partial-build alternative, but still needs to identify it as a departure from the original requirement. A final consistency reread helps, but it does not eliminate author blind spots.

**Length did not become more disciplined automatically.** All finals grew. Much of the added text earns its place, particularly examples and definitions. The later migration/deletion/acceptance/risk/decision repetitions could be reduced without losing substance. Do not solve this by imposing a fixed word count; ask which repeated paragraph actually helps the reader.

## Separate technical corrections

These findings matter before consolidating or implementing a guide. They are not the primary verdict on writing quality.

- **E: separate stable publication identity from changing job identity.** §6.1 keys “current” by build-node digests, which change on refresh, then describes replacing A with B under that key. This is a comprehension blocker as well as a design issue. A/B/C/D explicitly separate those roles; use one of their explanations.
- **D: mark the partial-build change against the original.** Original §7.1 says partial outputs remain private. D §6.1 proposes publishing structurally complete deferred-LLM outputs and asks for confirmation. The alternative is explained, but it should say it changes the original rule, rather than presenting the question as previously unspecified. E preserves private partial outputs in this round; its mixed-dataset fusion default is still a proposed change from original exact-reference behavior.
- **Bound lifecycle claims to what they establish.** A/B/C discuss unfinished workers explicitly. D's “slot released when operation settles” still needs a definition distinguishing promise settlement from worker exit; E's bounded-join account leaves that relationship largely unstated. Preserve safe lifetime requirements while specifying implementation later.
- **Correct a few source overclaims without rewriting the guide around them.** D says deferred cancel has “always” run before the posted callback; it is a scheduling-dependent race. D also attributes discarded write errors to `write`/`ping`; those return errors and the pump discards them. E's digest discussion should distinguish a reuse assumption from proof of identical content: `source.dir` uses file statistics, and implementation versions also involve the artifact namespace. These corrections are small compared with the useful explanations around them.

The shared payload-before-descriptor recovery gap from round two remains implementation work. It does not require another exhaustive audit in this writing experiment. No runtime tests or new reproductions were run here, and no implementation finding is marked fixed.

## Recommendation

Use this candidate guidance for future design writing. For a consolidated 009 guide, start with B's structure and balance, borrow D's first-use explanations and export comparison, A's connected identity example and C's explicit text-resolution example. E's current-host walkthrough and precedence table are also useful. Correct the limited issues above rather than combining every paragraph from all five.

Do not add another long checklist to the skill. The practical editorial follow-up is three questions: Where must the reader supply a missing local fact? Which relationship would one small example clarify? Which later paragraph repeats or contradicts something already taught?

## Evidence and limits

Three fresh `gpt-6.1-sol` medium agents and two `claude-opus-5-5` medium tmux writers received the same original design, source baseline and revised skill. They were not given prior candidates or review answers. They saved research, first draft, pre-revision notes and final; their self-review is not independent fresh eyes. The original guide and installed skill remain unchanged.

This is one case with no control arm, blind evaluator or human-intern study. Both model variation and stochastic variation remain. We can inspect explanatory improvements and consistency across these five artifacts; we cannot attribute every difference to the new instructions or claim general performance rankings. The experiment ends at this review, with implementation tasks still open.

## Subsequent selection and promotion

After this comparison, the user requested selection, reMarkable delivery and promotion of the guidance to the installed baseline. Codex B was selected and uploaded unchanged. The tested writing-style and deliverable-checklist references were installed in `ticket-research-docmgr-remarkable`; both discoverable copies of `textbook-authoring` now route to that guidance and summarize its newcomer-oriented two-pass approach. Earlier statements about the candidate remaining local describe the experiment checkpoint. See the [installation receipt](../scripts/03-clarity-guidance-experiment/baseline-installation-receipt.json) for exact changed paths and hashes.
