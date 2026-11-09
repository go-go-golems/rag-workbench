---
name: ticket-research-docmgr-remarkable
description: Create evidence-backed ticket research and implementation deliverables with docmgr, chronological diaries and requested reMarkable delivery. Use for codebase investigations, intern guides, phased implementation and research handoffs.
---

# Ticket research and implementation

## Scope and owners

Follow the user's requested deliverable: analysis-only does not authorize implementation, and implementation does not automatically require another upload. Preserve unrelated work and historical evidence. This core selects workflow phases; it does not change Pi's loader, user pins or higher-priority instructions.

- `docmgr` owns actual ticket commands and storage contracts.
- `diary` owns entry modes and detailed investigation format. An explicit detailed diary request requires investigation mode.
- `remarkable-upload` owns delivery commands, auth and verification defaults. Load it for requested delivery; do not duplicate its command sequence here.
- Writing quality: [writing style](references/writing-style.md). Read it before drafting a design guide. Study the system, write a complete draft, then record a whole-document comprehension/accuracy review before revising. Apply it proportionally across domains, not only to the introduction.

## Phase selection

Use [phase checklists](references/phase-checklists.md) for the relevant phase only, subject to higher-priority full-read requirements.

1. **Start:** locate/create ticket, inspect current state and contract, establish tasks and diary, capture repository ownership boundaries.
2. **Implement/investigate:** gather evidence before conclusions; preserve actual failure diagnostics; change only scoped files; run tests covering changed behavior.
3. **Checkpoint:** record one coherent milestone with evidence/commit references. Do not copy identical current-state facts into every document. Make focused commits at meaningful validated boundaries, not mechanically two commits per tiny change.
4. **Resume:** use the current request, files and latest checkpoint; follow [resume guidance](references/resume-packet.md). Reload absent/changed instructions, not every historical reference by default.
5. **Validate/close:** audit every explicit requirement and integration boundary, check docmgr hygiene, and deliver only what was requested. Use the [deliverable checklist](references/deliverable-checklist.md).

## Evidence and validation

Use the diary's evidence levels: routine results need command/result/revision, experiments need configuration and measurements, failures need exact relevant diagnostics. Raw-byte archives are warranted when byte identity matters, not for decorative success-output whitespace.

Validate changed documents and all their affected figures/assets. A changed renderer, stylesheet or validation policy invalidates dependent checks. Syntax success is not visual review. Perform a full contract/integration review at delivery; cached checks never authorize an unsupported completion claim.

For optional local checks and dependency manifests, see [workflow tooling](references/workflow-tooling.md). These helpers validate declared conventions and fixtures, not arbitrary instruction semantics or agent behavior.

## Handoff

Report ticket/doc paths, commits, validation, requested delivery result and unresolved requirements. Distinguish a delivered design from an implemented feature; never close an implementation task merely because its guide is finished.
