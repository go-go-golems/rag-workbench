# Changelog

## 2026-11-03

- Initial workspace created

## 2026-11-03

Ticket opened; intern design/implementation guide for delta builds written (stable identity, source manifests, lineage, delta protocol, copy-on-write sharded records and bleve clones); doc-id collisions measured (271/2,287 files, all from the 64-char cap)

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/ttmp/2026/11/03/RAGWB-003--incremental-re-indexing-delta-builds-over-immutable-artifacts/design-doc/01-intern-guide-incremental-re-indexing-with-delta-builds.md — the guide
- /home/manuel/code/wesen/go-go-golems/rag-workbench/ttmp/2026/11/03/RAGWB-003--incremental-re-indexing-delta-builds-over-immutable-artifacts/scripts/01-doc-id-collisions.py — collision/length measurement

## 2026-11-03

Guide uploaded to reMarkable: /ai/2026/11/04/RAGWB-003/RAGWB-003 Incremental Re-indexing Intern Guide.pdf

## 2026-11-03

Phase 1 (stable identity, 29feef3) and Phase 2: source manifest + Diff, lineage keys (Engine.lineages) and the store's lineage index; diary steps 3-4

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/artifacts/lineage.go — lineage index
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/manifest.go — source manifest and Diff

## 2026-11-03

Phase 3: records2 (64 COW shards) for docs and chunks, delta protocol in Engine.build, source/parse/chunk deltas; vault one-note edit 21+23+24 ms for the first three nodes; diary step 5

### Related Files

- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/delta.go — parse and chunk deltas
- /home/manuel/code/wesen/go-go-golems/rag-workbench/pkg/natives/records2.go — copy-on-write sharded records
