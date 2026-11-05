# Tasks

## TODO
- [x] Phase 0: not needed — the host wraps the captured verb function instead (diary step 3) <!-- t:41vl -->
- [x] Phase 1: engine events in rag.js (onEvent, node/hits/text deltas/tool/progress/build, rag.scope/withScope) <!-- t:beme -->
- [x] Phase 2: host — verb scan in OpenApp, EventSink, ServedApp.Invoke, ParseVerbParams, in-flight build sharing, gc(irs) <!-- t:krev -->
- [x] Phase 3: HTTP — GET /verbs, POST/GET /verbs/{verb...} JSON + SSE writer, --max-inflight <!-- t:ayfp -->
- [x] Phase 4: browser shell /run on SSE; remove NDJSON <!-- t:va1s -->
- [x] Phase 5: LLM cancellation via signal, example verbs in app.js, docs, vault measurements <!-- t:sh0c -->
- [ ] Follow-up: trace({details: true}) in gui.js — per-step outputs (hits with scores/why/reasons, evidence, answer), live from onEvent <!-- t:mwvo -->
- [ ] Follow-up: rerank.model progress events per batch (Go callback) <!-- t:bhsc -->
