# Whole-document review before revision

The complete first-draft.md was saved and then reread from beginning to end. These notes precede edits to final.md. This is the author's own second pass, not an independent reviewer or a demonstrated reader-comprehension test. Code was inspected, not executed. The first draft will remain unchanged.

## Specific revisions

1. **Passage: “Fusion deduplicates exact references by default” followed by “conservative initial proposal is rejection.”** A reader cannot tell whether distinct references from different datasets survive default fusion. The original requires exact-reference deduplication and explicit canonicalization for merging enrichment versions. Correct the paragraph: default identity is the pair and different pairs remain distinct; merging logical sections requires declared projection/conflict policy. Source `components/hits.js` and `engine/records.js` clarify why bare IDs are insufficient; they do not prescribe the new policy. Remove the unnecessary alternate default rather than change the original silently.

2. **Passage: Dataset<R> with a union kind and the later instruction to “discriminate ... in final types.”** The main sketch itself permits kind/record mismatches while the text promises a typed foundation. Make the sketch a generic kind-parameterized base with concrete DocumentDataset and ChunkDataset literal kinds, plus a dataset union. All symbols remain illustrative. Source README confirms strict new implementations are required; it cannot supply the proposed types.

3. **Passage: “Callbacks receive datasets” and ctx.chunks examples.** Which dataset appears when original/enriched streams coexist? Existing resolveBakes explicitly picks the first available docs/chunks node, recreating the implicit-selection problem unless explained. Require declared input bindings for multi-dataset callbacks; shorthand ctx.docs/ctx.chunks is unambiguous only when one dataset is bound. Full bound identities enter the bake key. Mark exact binding API as an implementation decision; do not invent a runnable signature. This is clarified by inspected `resolveBakes` code.

4. **Passage: slot/generation/job definitions and “Different slots can still share exact build work/resources.”** This risks implying a job can publish globally once into several different slots. Define slot identity as excluding changing source versions, exact job identity as including selected input versions and semantic options. Propose per-slot build/publication serialization and independent other slots; immutable resources may be shared. Do not add a second step-level job framework. Existing digests/lineages/artifacts show the conflation; the separation is a design clarification.

5. **Passage: “Refresh failure or cancellation ... preserves that generation.”** This conflicts with the later cancellation-after-commit warning. Qualify it as pre-commit failure/cancellation; after durable publication cancellation cannot unpublish. Add a publication outcome/commit decision and source consistency precondition: candidate descriptors must match bytes actually read, so settle how source versions are captured/validated. Source savePath demonstrates per-artifact payload-before-metadata but does not define snapshot commit or pinned inputs.

6. **Passage: “A query lease retains one snapshot ... release” and withCurrent sketch.** Success is clear, but exceptions, cancellation and detachResult limit/serialization failures need explicit finally release. Add that implementation requirement adjacent to sketch. Also specify generation-scoped live memo invalidation so cached output cannot retain retired handle access. `Session.run` artKey invalidation and artifact-backed helpers clarify both concerns.

7. **Passage: “server either retains and reads that generation ... or stale.”** State that lookup existence-check and retain must be serialized with GC, before opening lazy shards. Distinguish disk retention from open handles: a retained manifest can be reopened under a scoped registry ownership even without an already-open snapshot. The source's lazy record loading and current-ID lookups clarify the need; actual retention policy remains open.

8. **Passage: terminal-outcome diagram goes directly from cancellation outcome through cleanup to done.** Terminal decision is not worker completion. A cancelled provider can still be running; premature cleanup would recreate use-after-close. Add explicit accepted outcome, cancel worker if appropriate, join worker/tool activity, release worker-owned resources and post/finish owner settlement. Exactly-once outcome may be frozen early, but cleanup must not free resources before work stops. `sample.go` tool request/reply and rerank worker posting show this distinction. Add a compact discriminated union and lifecycle pseudocode; no generic framework is needed.

9. **Passage: shared jobs “Completion publishes once ...”.** Clarify one publication per candidate/slot through arbiter; job outcome and publication commit must agree. Record request-scope capture before first await, preserving `scope.ts`'s explicitly synchronous inheritance. Closing an event scope is not aborting work. Source directly confirms newScope.close only stops events, withScope is not async-local, scoped captures options synchronously.

10. **Passage: bounded writer and “capacity becomes available.”** Preserve bounded socket-write promise without implying forced termination of arbitrary uncooperative providers. Explain that supported workers must honor cancellation for the tested request join bound; bound violation is an operational failure, and safe resource ownership remains until worker exit. Capacity recovery acceptance must exercise the actual supported operation. Existing HTTP pump and workers establish the issue, but no source proves future join deadlines.

11. **Passage: guide teaches helpers but gives no proposed ordinary search usage.** Add a small `await rag.run`, guarded hit access, result.chunk(hit), result.top example that illustrates detached lifetime and no cleanup requirement. All examples are sketches; the existing API currently takes bare IDs, so mark it proposed clearly.

12. **Passage: “Then land datasets ...” implementation prose.** It contains the phases but is hard to use as a work order, and tests have source directories without named examples. Replace the long sequence sentence with numbered vertical checkpoints including which representative test files to extend and what replaced path must be removed. Keep tests in the final acceptance matrix without repeating every row in the phase plan. Original material requirements remain present. Source tests clarify useful existing semantic coverage.

## Retain as written

- Current versus proposed behavior and evidence limits are clear; compiler rejection of bad decision references is described accurately.
- All twelve findings and the 010 source/tooling prerequisite are present.
- Registry successful/failed adoption ownership, private scope transfer and duplicate-opener closure are materially explained.
- Durable publication, single namespace owner, transitive conservative GC, hard-link accounting and privacy retention are covered.
- Capability export, unconditional lexical rewrite, opaque lambda restriction and shared graph traversal include graph-level outputs.
- Browser/Goja common generated bundle and semantic array ordering are explicit.

## Questions remaining after revision

Exact identity/binding format, source-version capture, slot/publication commit policy, retained-generation/stale error defaults, limits/deadlines, and general graph deletion/binding syntax remain decisions. The final guide should identify when each blocks implementation, distinguish proposed defaults and avoid presenting them as current facts. No tests were run; the guide is the deliverable, not an implemented refactor.
