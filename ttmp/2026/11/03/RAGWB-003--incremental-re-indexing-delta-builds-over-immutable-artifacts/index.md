---
Title: 'Incremental re-indexing: delta builds over immutable artifacts'
Ticket: RAGWB-003
Status: active
Topics:
    - rag
    - backend
    - search
    - goja
DocType: index
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-03T19:30:09.625070284-05:00
WhatFor: ""
WhenToUse: ""
---

# Incremental re-indexing: delta builds over immutable artifacts

## Overview

Editing one note currently rebuilds the whole corpus (~28 s on the vault), because the source
digest covers every file. This ticket designs delta builds: stable per-file identity, source
manifests, lineage lookup of the previous build, a delta protocol through Engine.build, and
copy-on-write artifacts (sharded records, cloned bleve segments) — target: edit one note → search
in under 2 s, equivalent to a full rebuild.

- Design / intern guide: [design-doc/01-intern-guide-incremental-re-indexing-with-delta-builds.md](design-doc/01-intern-guide-incremental-re-indexing-with-delta-builds.md)
- Diary: [reference/01-investigation-diary.md](reference/01-investigation-diary.md)
- Status: design written; phases 0–5 open (tasks.md). Builds on RAGWB-002 (PR #2).

## Key Links

- **Related Files**: See frontmatter RelatedFiles field
- **External Sources**: See frontmatter ExternalSources field

## Status

Current status: **active**

## Topics

- rag
- backend
- search
- goja

## Tasks

See [tasks.md](./tasks.md) for the current task list.

## Changelog

See [changelog.md](./changelog.md) for recent changes and decisions.

## Structure

- design/ - Architecture and design documents
- reference/ - Prompt packs, API contracts, context summaries
- playbooks/ - Command sequences and test procedures
- scripts/ - Temporary code and tooling
- various/ - Working notes and research
- archive/ - Deprecated or reference-only artifacts
