# Tasks

## TODO
- [x] Phase 0: spikes — bleve clone+delta vs fresh build (DocCount, FieldCardinality, top-10), bbolt shard COW timing <!-- t:w90r -->
- [x] Phase 1: stable identity — DocIDFor (full slug ≤160, hash beyond), per-doc AssignIDs and ord, in Go and rag.js; collision warnings; parity tests <!-- t:p0lk -->
- [x] Phase 2: source manifest (size, mtime, sha256) + DiffManifests; Engine.lineages + lineage index in the artifact store <!-- t:45f8 -->
- [x] Phase 3: records2 (64 shards, canonical keys, COW) for chunks and docs; delta protocol in Engine.build; deltas for source.dir, parse.markdown, chunk.sections <!-- t:j9a8 -->
- [ ] Phase 4: index deltas — bleve clone+apply, terms rows+rebuild, vector rows, generic map components; crash-safety tests <!-- t:yiur -->
- [ ] Phase 5: serve --watch delta builds + Engine.gc/deferred close; --full, store verify, store gc; vault measurements (edit one note < 2 s) <!-- t:577m -->
