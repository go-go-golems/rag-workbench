# Whole-draft review before revision

The complete first draft is saved and has now been reread end to end, including its implementation and validation sections. The notes below precede final.md. This is the author's second reading, not independent fresh-context review or a human comprehension test. No implementation tests were run.

## Comprehension gaps

1. **Opening and flow diagram: “pack evidence,” “trace,” “live nodes.”** The newcomer can follow source/parse/chunk but may not know what the query delivers or why packing needs the old records. Add an ordinary query outcome: ranked hit -> evidence label/text -> answer citation; explain trace as per-node execution/ranking detail. State that a refresh repeats build work after source changes. Code clarifies this: components/builtins.js pack/answer emit evidence labels E1 and cited labels, while Session builds trace. This is explanatory repair, not a new requirement.

2. **Dataset relationship sketch: `document(id): RecordRef` and `order(document: RecordRef)`.** The first promises a record even for a missing chunk, and the second says nothing about a document from another dataset. Change document() to nullable, state that order validates membership in the dataset's recorded document relationship, and unknown dataset references are rejected by snapshot resolution. Explain that example IDs are shortened and logical IDs remain source-scoped. Existing source cannot settle the new error policy; label it as a proposed checked-reference rule.

3. **Bake passage: “propose explicit named bindings such as datasets.searchChunks.”** The text warns about first-stream selection but leaves no call shape showing who declares the name. Add a small labelled API sketch binding searchChunks to a chunk node, then materializing it inside a callback; explain that the compiler validates the node and types. Source confirms the current firstOf/first chunks behavior, not a target API. Keep new spelling explicitly proposed and not runnable today.

4. **Snapshot section: “plan.buildKey,” “logical plan build identity,” “full normalized build inputs.”** These phrases could refer to the same key, although the last good snapshot intentionally survives source changes. Introduce two relationships: stable publication slot for a build specification and exact job key for particular source versions/options. A snapshot carries its exact build key but is current under a stable slot. Update execution sketch, refresh policy, store pointer and decision row consistently. Source demonstrates digests change with inputs but does not establish this proposed naming/policy. It is a needed design clarification before publication implementation.

5. **Results: “Custom component outputs must also be checked for hidden handles.”** “Checked” does not tell a component author what to return. Specify a proposed restricted boundary: primitives, arrays/plain objects, declared record references, all validated and detached; reject functions, datasets and native handles, and resolve all promised record bodies/doc metadata under the lease. Add snapshotId to the result description and explicitly prohibit ambiguous string-ID lookup. Keep limits/pagination policy open but make limit failure concrete.

6. **Graph example: “route.needs is sufficiently likely.”** Explain route as the classification node, needs as one named question output, and min=0.5 as its gate threshold. A reader should know which identifier rename changes. The actual adaptive reports plan provides this evidence. Preserve final graph validation and unconditional lexical export explanation; do not add generic graph theory.

## Technical corrections and responsibility gaps

7. **Adoption: sketch leaves the opener closing on error, next paragraph proposes duplicate reuse and consumption uncertainty.** This is a real contract ambiguity, not a wording issue. Pick the simpler proposed rule: successful Adopt consumes ownership; every error, including duplicate identity, leaves ownership with the opener, which closes; snapshots reuse registered entries through retain rather than reopen/adopt. Centralize/check before opening on the owner. Report close failures rather than silently treating them as successful cleanup. This fits the original allowed reject-and-close policy. Current AddCloser paths do not answer this new ownership decision.

8. **Async outcome: “A terminal arbiter decides ... once.”** Name where the arbiter acts: one synchronized per-operation state in Go accepts worker completion/cancellation/deadline/shutdown transitions; VM delivery uses its fixed value and cannot reclassify it. Add a compact lifecycle sequence covering listener install, worker, freeze, detach/post/cleanup. This is proposed behavior. rerankAsync source confirms why the ordering matters; sample's tool bridge means owner request/reply must remain explicit.

9. **HTTP: “one writer ... owns headers” but helper “joins ... operation through configured bounds.”** Explain cancellation is notification, not worker completion. A bounded handler can return while a worker remains tracked; do not return that worker's execution capacity to a pool and claim all work ended. Distinguish HTTP admission scope from retained operation/resource ownership and require a reported timeout. The source's verb-only cap and wait-for-pump support the transport issue; uncooperative provider policy remains a design decision. Do not add a new scheduler or suggest forcibly closing resources.

10. **Durable pointer: “for each build identity” conflicts with changing source versions.** Resolve with item 4's stable slot and immutable exact-version manifests. Explicitly mark roots for every active plan slot in a namespace, not only the last app plan. Add no new database. Source's recentIrs exists because multiple plans can share an engine; current/retained roots must cover that cardinality.

## Remove or tighten without losing scope

11. **Decision and ending:** Some “no framework / no compatibility” rationale repeats. Keep the opening rule and one simplification paragraph; make ending decisions concrete. Add current source map precision: sample.go/decide.go are in pkg/ragjs; schema/codec details are ordinary phase decisions after the required ownership direction is fixed. Do not turn every missing default into a large open-question list.

12. **Acceptance:** Add regressions exercising the clarified slot vs exact build key and strict detachment policy. Preserve all twelve finding obligations, full/delta/cold/warm paths, active reader GC, real socket writes, provider details, shared browser/Goja bundle and strict TypeScript migration. Keep source-only validation language. First-draft preservation and self-review belong here, not in the final guide.

## Revision and final consistency plan

Apply these repairs in final.md only. Recheck the whole guide against the opening promises: dataset references agree with result lookup; slot naming agrees across query, refresh, store and jobs; Adopt errors agree with the Go sketch; operation acceptance precedes cleanup while joining remains distinct; graph export rewrites before final validation; implementation/test phases cover the clarified contracts. Preserve the first draft unchanged.
