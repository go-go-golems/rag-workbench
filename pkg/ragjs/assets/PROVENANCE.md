# Vendored prototype sources

Copied from the claude.ai RAG workbench artifact v2 (ticket RAGWB-001, `sources/prototype/`) on 2026-11-02.
From now on these files are owned here and evolve with the Go backend.

| file | upstream sha256 |
|---|---|
| rag.js | `68ee1aef09ca578dcb4e37b498621547372836fd67c65d9c45978195a52df20c` |
| gui.js | `ccda5ada1656fc75e9a6b4aa230007a249982ad3a1355a2f60e2949c3142094b` |
| kit.js | `4cd146bbab85669c8a87c817cc0501e8e5ea9c9469aa3e112afc2eec67ca1daf` |

## Local changes

1. `rag.js`, `gui.js`: Unicode property escapes (`\p{L}\p{N}`) replaced by a feature-detected class body `LN` (`rag.util.LN`). goja matches nothing for `\p{..}` without raising an error (RAGWB-001 diary step 3, decision D6).
2. `rag.js` `Engine.digests`: generic `def.digest(params, engine)` hook so native source components fold content digests into their node digest.
