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

- [Comparison and recommendation](reference/02-intern-guide-writing-experiment-comparison.md)
- [Fresh reading notes](reference/03-fresh-reading-notes-before-independent-code-inspection.md)
- [Independent technical review](reference/04-independent-code-review-of-original-and-candidate-design-guides.md)
- [Candidate A](design-doc/02-v2-intern-guide-candidate-a.md), [candidate B](design-doc/03-v2-intern-guide-candidate-b.md), [candidate C](design-doc/04-v2-intern-guide-candidate-c.md)
- [Uninstalled candidate skill](scripts/01-intern-guide-experiment/candidate-skill/SKILL.md)

## Scope

Unify dataset access and provenance, resource ownership and query snapshot lifetime, artifact inventory and collection, async operation cancellation, HTTP streaming, and graph transformations. Migrate JavaScript callbacks and result references directly. Delete superseded mechanisms instead of preserving parallel paths.

## Status

Active: design delivery, with implementation tracked separately in the task list.
