# Parent reading notes for round three

These notes record editorial reading, not a fresh-context or human comprehension test. No feedback is sent to writers during the run.

## C — full first draft and full final read

The final adds a small original/enriched hit example with literal text, then explains what to notice. It also adds a manifest-shaped instance and a five-identity table BEFORE the query sketch. These directly reduce reconstruction work. Success/error adoption ownership and accepted-outcome versus worker-exit are substantially clearer after revision. The end now distinguishes phase-blocking choices from local values; implementation and test tables remain usable and connected to earlier contracts.

Still uneven first use: bake first appears in the findings table and is explained later ("compute configuration from built data"); verbs are not explained; signals are illustrated by q/k rather than plainly defined. The middle/late HTTP cancellation paragraph is a dense cluster of requirements, with less narrative relief than the dataset chapter. The manifest example teaches the grouping well, but durable descriptor/current-pointer relations still rely mostly on prose. No need to demand a full wire schema to recognize the writing improvement. Final path-root convention arrives late, after abbreviated references have already appeared.

Second-pass evidence: first draft 4,372 words; final 5,464. Saved review notes identify the missing concrete example, ambiguous build identity and premature release inference. Final visibly repairs those. It preserves private partial builds and exact-ref fusion; no large policy contradiction noticed on this reading. Minor implementation precision (owner-thread listener detach) belongs to technical review, not the primary clarity verdict.


## A — full final and review notes read; selected first-draft passages compared

Stronger first-use teaching: corpus versus plan, hit/evidence, provider, reranking, digest, artifact/lineage/view, bake and decision condition all get purpose or behavior near use. The P/E original-enriched example and A/D/P/E snapshot lookup example explicitly explain what each identity tells the reader. A callback binding example later shows what rename changes rather than only saying "canonical references". The document sustains explanation through operations, transport, graph and migration; the seven-step durable protocol is readable but still abstract about concrete descriptor contents.

Second-pass notes correspond to visible additions: build digest/provider/rerank definitions, combined snapshot/index/hit example, concrete operation arbiter, callback binding and useful integration sequencing. A's duplicate-adoption policy differs from B/C but is clearly proposed and internally explained; do not penalize a reasonable alternative as a clarity defect.

Remaining burdens: verbs still arrive as assumed project knowledge in HTTP; the durable namespace is used without a short project-specific definition; the late decision table repeats several already-explained choices, and test/phase sections are fairly dense. The early source-layout/status paragraphs interrupt the user story slightly. On writing alone, A is coherent and more evenly explanatory than its round-two counterpart. No need to demand perfect error handling in every illustrative sketch.


## B — full final, first-draft opening and review notes read

The ordinary query outcome paragraph is a strong second-pass addition: hit → evidence label E1 → answer citation → per-node trace explains why provenance matters beyond search. Signals, corpus/plan, artifact/lineage and record view are plainly taught. The bake binding example names both the graph node and the callback-local variable and explains compiler validation. The graph chapter explains route, needs and the threshold before showing normalized IR; this is much more teachable than a reference type on its own. Concrete durable records continue the explanatory style in the middle. The ending remains mostly phased work and test tables, but those refer to behaviors already taught.

Remaining burdens: no definition of verb despite the offline command and transport comparison; bake appears in findings before its definition; namespace is assumed; no explicit abbreviated-path convention near first source references. Some lifecycle paragraphs are long and combine several responsibilities, and the ending repeats framework/compatibility warnings and stage-blocking decisions. These are editing opportunities, not evidence that the document is cryptic. B currently feels the best balanced of the three Codex guides, with A close; C is the shortest but leaves more terms implicit.

Technical review kept separate: proper conditional rerank source phrasing, exact-ref fusion and private partial outputs are preserved. Explicit error-ownership rule agrees with the adoption sketch. Unknown source pinning/recovery detail remains implementation work. Claims about generic stored artifact dependency arrows are illustrative target records, not existing codec facts. Test path abbreviation for lineage deserves correction but is not a comprehension blocker.


## E — full final, first-draft opening and review notes read

The current-system walkthrough is stronger and more pedagogical than the shorter candidates: host model, owner goroutine, concrete artifact object, four served functions, and the collection precedence table each explain a mechanism rather than just name it. Response JSON shows exactly what detached means for the browser, including neighbor navigation; the job diagram is simple and clear. Second-pass correction of the enrichment example is real: final distinguishes arrays persisted as JSON from Go record views rather than claiming every warm enrichment becomes a view. Targeted source inspection of Persist.save supports that correction. Rerank now correctly says deferred cancellation MAY run before callback classification.

Two significant reading burdens: (1) current snapshot is keyed by changing build digests (§6.1) while refresh is supposed to replace a previous current generation. The final explicitly teaches inconsistent relationships; this is comprehension-relevant, not a demand for perfect hashing details. (2) lengthy ending has module table, reuse table, deletion list, migration, eight phases, test table, done list, risks and decisions, repeating several points. Each is readable locally, but the accumulated repetition dilutes the main thread. Trigger remains undefined despite export depending on submit-triggered behavior. Verbs get an application-defined label but less of a purpose explanation than D's intended revision.

E is a useful orientation guide, particularly current operation, but should not be chosen wholesale over B solely because it explains more. Final is 9,369 words versus 6,123 first; most additions are relationships/requirements, with some avoidable repeated completion scaffolding. No major partial-publication contradiction in E this round: final retains private partial outputs. Fusion default still becomes a clearly labelled proposal changing original behavior; flag separately. Lifetime join is much thinner than A/B/C and does not establish unfinished-worker accounting. Keep that as bounded technical follow-up rather than dominate the prose review.


## D — full final, first-draft opening and review notes read

D now has the strongest explicit first-use treatment of trigger and verb, two recurring omissions in the earlier batch. Its current system is taught as a sequence with reasons, then the failure cases reuse those concepts. The lease-count versus registry-owner-count paragraph directly answers an inference burden. A before/after adaptive-export graph teaches what the projection does without asking the reader to imagine it. The detached JSON result and old→new context API table also serve newcomer understanding. The last decision records provide context and consequences rather than only labels.

Remaining burdens: Goja is used without the short interpreter explanation supplied by A/B/C/E; artifact/namespace terms are more implicit than trigger/verb. Dataset/source-document association is less explicit than the other candidates. The ending repeats migration/deletion/acceptance/risks several times; some repeated ground rules and process caveats can be cut. An 8,738-word guide is not inherently too long, but much of its 2,820-word second-pass increase is restored scope and repeated completion framing. D is particularly useful as a current-system teaching reference; B remains more economical overall.

Separate technical observations: partial publication is now explicitly proposed and explained, but it changes the original's private-only rule and should identify that departure clearly. The synchronous index correction in final matches vector.go's synchronous embed/search bridge (review notes had initially assumed otherwise). Rerank still says deferred cancel "always" runs before posted callback; write/ping still incorrectly called error-discarders rather than pump. These are precise corrections, not reasons to dismiss the walkthrough. Snapshot key is structurally stable (unlike E); exact job key is separate. Scope.Commit says all entries transfer but delta-base paragraph says temporary bases are released; flag as example/prose alignment if consolidating.
