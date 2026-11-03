---
Title: Import rag-workbench prototype and design Go-backed script runner
Ticket: RAGWB-001
Status: active
Topics:
    - rag
    - goja
    - backend
    - ui-dsl
DocType: index
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: "Import the claude.ai RAG workbench prototype and analyze turning it into a Go-backed app: saved JS pipeline/UI scripts, headless runners for vaults/docs, ad-hoc apps."
LastUpdated: 2026-11-02T21:16:36.801657867-05:00
WhatFor: ""
WhenToUse: ""
---

# Import rag-workbench prototype and design Go-backed script runner

## Overview

Imported the claude.ai "RAG workbench with fluent builder API" session (artifact, 29 presets, textbook, corpus) into `sources/`, and analyzed how to back it with Go: ragkit, goja-bleve, goja-text, go-go-goja/xgoja, geppetto, slop-computer's `window.claude` runtime, and coinvault as the production RAG reference.

Status: analysis done. Spike shows `rag.js` runs unmodified in goja, except that goja doesn't support `\p{L}` regex escapes.

- Analysis: [analysis/01-prototype-analysis-and-go-backend-reuse-map.md](analysis/01-prototype-analysis-and-go-backend-reuse-map.md)
- Diary: [diary/01-investigation-diary.md](diary/01-investigation-diary.md)
- Spikes: `scripts/01-goja-smoke`, `scripts/02-goja-regex-probe`
- Sources: `sources/prototype/`, `sources/textbook/`, `sources/corpus-uploads/`, `sources/conversation.md`

## Key Links

- **Related Files**: See frontmatter RelatedFiles field
- **External Sources**: See frontmatter ExternalSources field

## Status

Current status: **active**

## Topics

- rag
- goja
- backend
- ui-dsl

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
