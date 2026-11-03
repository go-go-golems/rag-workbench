# Tasks

## TODO
- [ ] Decide engine strategy: rag.js engine + Go-native heavy nodes vs Go engine <!-- t:x2mh -->
- [ ] Server skeleton: net/http ServeMux + embedded workbench SPA + window.claude shim (port slop-computer runtime/sample) <!-- t:rswf -->
- [ ] Script store: sqlite scripts + versions + conversations; agent wb.save/load <!-- t:nz4i -->
- [ ] Runner: go-go-goja runtime with compat-patched rag.js, ctx.sample via geppetto <!-- t:rbbv -->
- [ ] Native nodes: vault walk/glob/ignore, goja-text markdown+frontmatter, bleve/ragkit bm25, ragkit sqliteexact + geppetto embeddings (index.vector) <!-- t:7b6s -->
- [ ] RemoteRag host.rag for gui runtime; ad-hoc app serving (ragwb serve script.js) <!-- t:hi2e -->
- [ ] Headless CLI: ragwb index/eval script.js --root ~/code/wesen/go-go-golems/go-go-parc <!-- t:tekq -->
- [ ] Fix goja \p{L} incompatibility properly (upstream goja or rewrite tokenizer regexes) <!-- t:k4hx -->
