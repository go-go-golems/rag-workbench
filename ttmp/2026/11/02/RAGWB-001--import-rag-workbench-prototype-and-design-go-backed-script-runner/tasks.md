# Tasks

## TODO
- [x] D1 engine strategy decided in guide: rag.js engine + Go-native heavy nodes (status proposed) <!-- t:x2mh -->
- [x] goja \p fix: go-go-goja PR #106 (bump to e1fc788) → after merge+v0.10.7: bump go-go-goja here, isLN → unicode.IsLetter||IsNumber, flip TestTokenizeWorksInGoja, re-run whole-vault parity <!-- t:k4hx -->
- [x] P0 scaffold: go.mod, cmd/ragwb, Makefile, vendored assets, examples <!-- t:k1ny -->
- [x] P1 ragwb module: rag.js/gui.js/kit.js as goja modules, D6 fix, headless run + presets sweep <!-- t:08l0 -->
- [x] P2 jsverbs: ragwb verbs --dir with ragwb runtime; split-file examples/apps/reports <!-- t:1y8c -->
- [x] P3 rag.source.dir native: walk, globs, excludes, stat digest, roots allowlist; go-go-parc subset <!-- t:q4fk -->
- [x] P4 native parse.markdown/chunk.sections (goldmark) with byte ranges (A2), parity fixtures <!-- t:ls4u -->
- [x] P5 workbench server: ported UI + window.claude shim (sample SSE/page tools, db, user) <!-- t:pgim -->
- [x] P6 app store: apps dir + history API; agent wb.save/load <!-- t:bhly -->
- [ ] P7 remote pipeline: /api/rag sessions, RemoteRag, ragwb serve <app> <!-- t:i5j5 -->
- [ ] P8 persistence + vectors: artifact store by digest, bleve index, embeddings + sqliteexact <!-- t:f6cl -->
- [x] P9 server-side LLM nodes: ctx.sample on geppetto <!-- t:jlcq -->
- [x] After go-go-goja v0.10.7: nil-slice audit (goja 6ace4dd nil slice → null) — add an empty-result test for natives (BM25 no hits, empty source.dir) <!-- t:de8c -->
