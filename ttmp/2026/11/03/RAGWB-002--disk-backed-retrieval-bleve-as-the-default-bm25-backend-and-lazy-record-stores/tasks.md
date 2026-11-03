# Tasks

## TODO
- [x] Phase 0: bleve v2.6 dependency (go directive unchanged), --index-backend flag <!-- t:gwli -->
- [x] Phase 1: artifact store file/dir payloads (TempDir, SaveFile, SaveDir, Path, Meta.Kind) <!-- t:spkk -->
- [x] Phase 2: bbolt record store for chunks (docs stay a gob blob) + RecordView + rag.js view support (artifacts, ctx chunkList, evals R, agentic, build inputs) <!-- t:fvi3 -->
- [ ] Phase 3: bleve BM25 backend (our tokens, one field per weighted field, bleve BM25 scoring behind the quality gate; persisted, read-only open, closers) and make it the default <!-- t:k5c8 -->
- [ ] Phase 4: Go-native compact terms index with parity test <!-- t:1tnm -->
- [ ] Phase 5: vault measurements (open, RSS, heap, latency), presets sweep, docs and agent prompt updates <!-- t:iqs9 -->
