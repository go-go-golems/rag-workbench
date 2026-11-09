---
Title: Simplify engine contracts and resource ownership
Ticket: RAGWB-009
Status: active
Topics:
    - rag
    - search
DocType: index
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-08T18:12:11.401639126-05:00
WhatFor: ""
WhenToUse: ""
---

# Simplify engine contracts and resource ownership

## Overview

Design a smaller, consistent engine architecture around datasets, snapshots, owned operations and canonical graph references. Reuse shared contracts where their semantics agree; retain specialized retrieval algorithms and provider protocols. No backwards compatibility layers are required.

The design is based on commit `0854c6a` and maps twelve PR review findings to systemic changes or small local corrections. This ticket currently contains the design and implementation plan; the refactor has not been implemented.

## Key links

- [Intern analysis, design and implementation guide](design-doc/01-intern-guide-to-consistent-datasets-snapshots-operations-and-graph-contracts.md)
- [Design investigation diary](reference/01-design-investigation-diary.md)
- [Implementation tasks](tasks.md)
- [Changelog](changelog.md)

## Writing experiment

- [Claude Opus 5.5 newcomer review of candidate B](reference/05-claude-opus-5-5-newcomer-comprehension-review-of-candidate-b.md)

- [Comparison and recommendation](reference/02-intern-guide-writing-experiment-comparison.md)
- [Fresh reading notes](reference/03-fresh-reading-notes-before-independent-code-inspection.md)
- [Independent technical review](reference/04-independent-code-review-of-original-and-candidate-design-guides.md)
- [Candidate A](design-doc/02-v2-intern-guide-candidate-a.md), [candidate B](design-doc/03-v2-intern-guide-candidate-b.md), [candidate C](design-doc/04-v2-intern-guide-candidate-c.md)
- [Uninstalled candidate skill](scripts/01-intern-guide-experiment/candidate-skill/SKILL.md)

## Round-two writing experiment

- [Whole-document comparison and recommendation](reference/06-round-two-generic-guidance-five-writer-comparison.md)
- [Generic two-pass candidate skill](scripts/02-general-guidance-experiment/candidate-skill/SKILL.md), [writing guidance](scripts/02-general-guidance-experiment/candidate-skill/references/writing-style.md)
- [Codex A](design-doc/05-round-two-intern-guide-codex-a.md), [Codex B](design-doc/06-round-two-intern-guide-codex-b.md), [Codex C](design-doc/07-round-two-intern-guide-codex-c.md)
- [Opus D](design-doc/08-round-two-intern-guide-opus-d.md), [Opus E](design-doc/09-round-two-intern-guide-opus-e.md)
- [Preserved draft hashes and completion evidence](scripts/02-general-guidance-experiment/results-receipt.json)

The experiment is complete. Candidates are preserved for comparison, including known defects; the original guide remains authoritative. Installed skills and runtime code were unchanged at the round-two checkpoint.

## Round-three results and selected guide

- [Selected intern guide: Codex B](design-doc/11-round-three-intern-guide-codex-b.md)
- [Whole-document comparison](reference/07-round-three-clarity-guidance-five-writer-comparison.md)
- [Codex A](design-doc/10-round-three-intern-guide-codex-a.md), [Codex C](design-doc/12-round-three-intern-guide-codex-c.md), [Opus D](design-doc/13-round-three-intern-guide-opus-d.md), [Opus E](design-doc/14-round-three-intern-guide-opus-e.md)
- [Experiment completion evidence](scripts/03-clarity-guidance-experiment/results-receipt.json)
- [Installed baseline receipt](scripts/03-clarity-guidance-experiment/baseline-installation-receipt.json)

Codex B was selected for the best balance of explanation, examples and navigation. Uploaded as **RAGWB-009 Selected Intern Guide Round Three B** to `/ai/2026/11/08/RAGWB-009`. The tested writing guidance is now the installed ticket-research baseline and is referenced by both discoverable textbook-authoring skills. The original design and experiment drafts remain preserved; selection does not resolve every design question or implement the refactor.

## Scope

Unify dataset access and provenance, resource ownership and query snapshot lifetime, artifact inventory and collection, async operation cancellation, HTTP streaming, and graph transformations. Migrate JavaScript callbacks and result references directly. Delete superseded mechanisms instead of preserving parallel paths.

## Status

Active: design delivery, with implementation tracked separately in the task list.
