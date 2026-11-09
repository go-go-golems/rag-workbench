# Resume without duplicated state

Prefer the derived `docmgr ticket resume` result when available. It exposes schema_version, ticket, status, phase, next, latest_milestone, remaining, revisions, documents, evidence and conflicts. The CLI nests it under the `resume` field of one structured row. Do not copy it into a second manually maintained ledger.

When resuming:
1. Read the current user request and applicable higher-priority instructions.
2. Obtain current ticket state; inspect pending operations and stale evidence before treating the checkpoint as current.
3. Read the latest relevant diary step and required contract sections. Expand earlier history when a decision or failure needs context.
4. Reload applicable skills when absent or changed, and obey mandatory full-read rules. A stored hash is not proof of memory.
5. Select the next unresolved requirement and execute its concrete action.

The optional workflow helper validates a resume result's shape and warns about conflicts; it does not verify the underlying files. It must not authorize ignoring a new request, resurrect an obsolete goal, or convert a test pass into full-contract completion.

For older binaries, use current task/doc listings plus the latest diary checkpoint. Label any manually prepared handoff as a snapshot with its revision/time; do not pretend a proposed CLI exists.
