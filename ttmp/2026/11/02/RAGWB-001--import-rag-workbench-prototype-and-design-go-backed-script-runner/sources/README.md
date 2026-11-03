# RAG Workbench — full session archive

- `workspace/` — the project as it stands: `ragwb/` (the workbench page, library, presets, corpus) and the
  original API examples (`rag-workbench-examples.js`).
- `scratchpad/` — every working script: node tests (`t/`), Playwright checks and screenshots (`pw/`), and the
  research scripts behind each part of the textbook (`book2/`–`book10/`, `bookA/` for the appendices).
- `uploads/` — the eleven markdown reports you uploaded.
- `textbook/` — "Search You Can See Into" exported as markdown: contents, Parts II–X and the appendices.
  Diagrams appear as a one-line placeholder; the live doc has them.
- `transcripts/main/session.jsonl` — the full main conversation transcript, including the part before the
  context was compacted.
- `transcripts/subagents/` — one transcript per subagent (the ten that wrote textbook parts), with metadata.
- `transcripts/task-outputs/` — output of background shell tasks that are not subagents.
- `transcripts/tool-results/` — large tool results saved to disk during the session (doc exports, screenshots).

Transcripts are JSON Lines, one event per line.
