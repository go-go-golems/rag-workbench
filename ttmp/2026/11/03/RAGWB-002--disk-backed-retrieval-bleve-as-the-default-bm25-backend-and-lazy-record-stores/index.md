---
Title: 'Disk-backed retrieval: bleve as the default BM25 backend and lazy record stores'
Ticket: RAGWB-002
Status: active
Topics:
    - rag
    - backend
    - goja
    - search
DocType: index
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-03T16:37:02.035815661-05:00
WhatFor: ""
WhenToUse: ""
---

# Disk-backed retrieval: bleve as the default BM25 backend and lazy record stores

## Overview

Make retrieval in Go runtimes disk-backed: bleve becomes the default backend of `index.bm25`
(postings on disk, our BM25F scorer, same rankings as the browser engine) and docs/chunks are
read on demand from bbolt record stores, so a served vault app opens in milliseconds and needs
~100 MB instead of 1.4 GB. Follows RAGWB-001 (diary steps 23–26 hold the measurements).

- Design / intern guide: [design-doc/01-intern-guide-disk-backed-retrieval-with-bleve-and-lazy-record-stores.md](design-doc/01-intern-guide-disk-backed-retrieval-with-bleve-and-lazy-record-stores.md)
- Diary: [reference/01-investigation-diary.md](reference/01-investigation-diary.md)
- Status: design written; implementation phases 0–5 open (tasks.md).

## Key Links

- **Related Files**: See frontmatter RelatedFiles field
- **External Sources**: See frontmatter ExternalSources field

## Status

Current status: **active**

## Topics

- rag
- backend
- goja
- search

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
