# Evaluation plan fixed before the writers start

Compare the original guide and three independent candidate-guided rewrites. Treat the result as a qualitative, single-topic replication: there is no old-skill control arm, the conversation examples are supplied to every writer, and the parent is both skill author and evaluator. Do not claim the skill caused a measured improvement or that a real intern comprehended the text.

## Manual rubric

Rate each dimension 0–3 with excerpt/section evidence: 0 absent or misleading; 1 partially explained/cryptic; 2 mostly clear with specific gaps; 3 clear and technically faithful. Avoid a single total score that can hide a serious technical regression.

1. Terms and concrete entities appear before dependent claims.
2. Situations show actors, sequence and a concrete consequence.
3. Proposed behavior explains why the failure is prevented and who is responsible.
4. Technical fidelity preserves important edge cases and distinguishes current/proposed/tested.
5. Implementation guidance connects prose to APIs, code and tests.
6. Explanations remain readable and proportional rather than repetitive templates.

## Fixed sample set

Read every draft, then compare these passages closely:

- Familiar from conversation: storage-independent collection contract; result construction; ownership/release; inventory versus history; shared waiters.
- Transfer beyond supplied examples: immutable terminal outcomes before cleanup; bounded network writes/flush; canonical graph references and capability-based export; dataset-version provenance and fusion.
- Fidelity gates: late waiter after job cancellation, background refresh owner, Goja/event-loop shutdown cleanup, failed refresh retains current snapshot, results detached before release, missing/corrupt inventory fails safe, exclusive writable namespace lock, no application search close calls, no legacy adapters.

For each draft, record at least two successful passages and two weaknesses or limitations. Distinguish unclear prose from changed architecture. Compare source references against the current module map. Read the original's explanatory body, not only its terse invariant list, so the baseline is assessed fairly.

## Outcome

Keep all drafts unchanged as evidence. Recommend a preferred base for eventual v2, any passages to borrow and minimal further skill edits. Do not install the skill or silently replace the authoritative design. Mechanical counts and docmgr checks establish hygiene, not comprehension.
