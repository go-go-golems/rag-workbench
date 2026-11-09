# Shared context packet for three independent writers

This is a curated reconstruction of relevant conversation context, not an automatic full-history fork. All three agents receive the identical packet, candidate skill and original document. The model tool requires `fork_turns=none` when selecting Sol medium. The parent remains responsible for commits, diary and comparison.

## Task and scope

Write an independent, complete v2 of the RAGWB-009 intern design guide using the candidate skill. Keep the installed skills and original guide unchanged. Write only your assigned output file, preserving its docmgr YAML frontmatter. Do not inspect the other candidates, comparison report or sibling agent output. Do not implement code, commit, upload or modify ticket bookkeeping. Read-only source inspection is allowed when needed. Return the output path and a short description of the explanatory choices and unresolved uncertainties.

The goal is quality, simplicity, elegance, consistency and appropriate composability/reuse. Compatibility can break: the system is undeployed. Do not propose legacy adapters or parallel old/new paths. The document may be detailed; the implementation should remain small and coherent. Explain directly with no analogies.

## Current project facts

- Repository: `/home/manuel/workspaces/2026-11-02/rag-workbench/rag-workbench-resume`.
- Original design: `ttmp/2026/11/08/RAGWB-009--simplify-engine-contracts-and-resource-ownership/design-doc/01-intern-guide-to-consistent-datasets-snapshots-operations-and-graph-contracts.md`.
- 009 is proposed, not implemented. It covers twelve PR review findings and systemic dataset, provenance, snapshot, resource, store, operation and graph-reference changes. Preserve their technical substance and distinction between reported evidence and tested behavior.
- RAGWB-010 implementation is complete in `cac9262`; documentation checkpoint `c267f91`. 26 source modules now live under `pkg/ragjs/src/`; four strict TypeScript implementations cover errors, identity, tokenization and request scopes. The other 22 modules remain JS. `assets/rag.js` is a generated committed bundle for Goja/browser/standalone export.
- 010 PR #8 is open against the published `task/ragwb-008-vault-ui` parent; `@codex review` was posted. The user wants 009 implementation after merge. This experiment is documentation work only on a separate branch.
- Use the current module map in original section 16 and `pkg/ragjs/src/README.md`; original pre-extraction file/line references remain historical evidence.
- 009's nine implementation tasks remain open. No new validation or production readiness should be claimed by rewriting the guide.

## The user's readability feedback

The user asked what these statements meant:

- “Loading a stored output changes storage cost, not its public collection behavior.”
- “Event/result packaging.”
- Invariants 4–7: resource ownership, release, history versus inventory, and shared-job cancellation.

After plain-language explanations, the user said:

> ok, so this is much better explained. can you write a v2 of the design doc that uses this approach in addition to the textbook intern guidance that already exists?

The user then requested three independent Sol-medium drafts to evaluate candidate skill updates before installing them. All writers receive the improved explanations, so this is not a controlled causal experiment comparing old and new skills.

## Examples of the explanatory style the user preferred

### The collection contract

Callers should use a dataset the same way whether it was freshly computed or loaded from disk. Currently, fresh output might be an array, while stored output is a RecordView. That forces callers to handle differences and caused the bake callback bug.

The proposal is one contract: `dataset.get(id)`, `dataset.values()`, `dataset.size`, `dataset.materialize()`. Both implementations should return the same records with the same ordering and lookup semantics. Disk loading may involve more I/O or less memory, but it should not change what those operations mean. This does not require preserving today's array API; callers can migrate.

### Constructing outgoing events and responses

An internal retrieval hit might be `{ id: "chunk-42", score: 0.87 }`. Before sending it to a caller, the engine adds text, document title, heading, citation details and traces. Those lookups must use the dataset snapshot that produced the hit. If refresh happens midway, consulting the newest snapshot could produce different text or nothing. Keep the query's snapshot alive until the outgoing data contains everything it needs.

### One owner and explicit users

A native resource is something opened by the Go backend, such as a database file or search index. The resource registry is the one place responsible for managing and closing it. Other parts of the system explicitly register their use:

```text
Registry owns an open index.
Snapshot A uses that index.
Snapshot B also uses that index.
```

Neither snapshot independently closes it. This prevents forgotten cleanup and multiple components each believing they should close the same index.

### Close after the final user releases

```text
Query starts using A.
Refresh publishes B.
New queries use B.
The original query continues using A.
That query finishes.
A can now release its resources.
```

If B shares a resource, it stays open until B releases it too. This bookkeeping belongs inside the engine; ordinary search callers do not manage it.

### Recent history versus all stored files

Recent build history helps find an earlier build to reuse. The stored artifact inventory records what files and indexes exist. Suppose history keeps only 32 builds. When build 33 arrives, the oldest history entry disappears, but its files may still exist. If cleanup only reads recent history, it can no longer discover those old files. Track stored artifacts independently and check what current snapshots, running queries and builds still need.

### Shared build cancellation

A waiter is a caller waiting for work to finish:

```text
Request A starts the build.
Request B needs the same build and waits for it.
A disconnects.
B still needs the result.
```

The build must continue for B. Give the job its own lifetime; canceling one request removes that caller's interest. Cancel when nobody needs the job, or on engine shutdown. Preserve the original guide's additional background-owner and late-waiter cases in the full design.

## Draft requirements

Read the candidate SKILL.md, writing-style.md and deliverable-checklist.md before drafting. Read the complete original guide. Produce a standalone technical intern guide, not merely expanded explanations of seven bullets. Cover the original design's major concepts, twelve findings, API sketches, ownership and cancellation edge cases, file references, migration/deletion plan, phased implementation and tests. Integrate the TypeScript foundation accurately. Keep concrete scenarios and clear actor/action descriptions throughout, including topics not explained in this packet. Put concise invariants after their explanations. Do not blindly repeat six headings per concept.

No fixed word count. Preserve technical substance without filler or unnecessary expansion. State any technical uncertainty rather than inventing an implementation. At the end, include a compact author self-check identifying two passages you revised for comprehension and any remaining weaknesses. This self-check is not evidence that the skill succeeded; the parent will read the actual document.
