---
Title: 'Design: trace details widget and asynchronous batched rerank'
Ticket: RAGWB-005
Status: active
Topics:
    - rag
    - backend
    - goja
    - frontend
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://pkg/ragjs/assets/gui.js
      Note: trace details widget
    - Path: repo://pkg/ragjs/trace_fold_test.go
      Note: live fold tests
ExternalSources: []
Summary: 'Two follow-ups of RAGWB-004: a trace widget that shows each steps output (and fills in live from the engines events), and a rerank.model that no longer blocks the runtime: asynchronous, batched, cancellable, with progress and partial results.'
LastUpdated: 2026-11-04T16:03:19.84705563-05:00
WhatFor: Implementing and reviewing RAGWB-005.
WhenToUse: Before changing gui.js trace rendering, the gui runtimes run loop, or rerank.model.
---

# Design: trace details widget and asynchronous batched rerank

RAGWB-004 gave the engine an event stream and the browser page an SSE client. Two follow-ups were recorded on that ticket. This document designs both. They are small and independent, and they share one ticket because both make the progress of a query visible.

## 1. Trace details in the gui

### 1.1 Current state

`trace()` (`pkg/ragjs/assets/gui.js`, `RENDER.trace`) draws one table row per live node: `node`, `status`, `in`, `out`, `ms`. It reads `rt.state.result`, the final run result. Everything needed for a per-step view is already in that result:

- `result.outputs[nodeId]`: each node's output. That is a hit list `[{id, score, via, parts?, why?, reason?}]` for retrieve, fuse, filter and rerank nodes; a list of strings for query nodes; `{text, evidence, bytes}` for `pack`; and `{text, evidence, cited, rounds?}` for `answer` and `agentic`.
- `result.trace[i]`: `{node, t, ms, in, out, status, calls, notes, error, top}`, where `top` holds the first 50 hits of a hit-producing node with `why` (matched terms → score), `parts` (fusion: source → rank) and `reason`.
- `rt.host.rag.chunk(id)`: the chunk body for a hit. Under `ragwb serve`, the server packs chunk bodies for every id in every output array, so this is answered locally.

The page shows nothing during a slow run: the table appears when the result arrives. RAGWB-004 added `onEvent` to `session.run` in the shell and in the in-browser engine, but the gui runtime does not pass it.

### 1.2 Design

`trace({ details: true, limit: 10 })` keeps the table and makes every row expandable.

- **Expansion state** is runtime state (`traceOpen: {nodeId: true}`), so it survives re-renders and new runs. A row toggles with a click on its first cell (▸/▾).
- **Detail views by output shape:**

  | output | detail view |
  |---|---|
  | hit list | up to `limit` rows: rank, score, title › heading, `via`, fusion parts (`text #3 · meta #1`), matched terms (`why`, top 4), rerank `reason`. Clicking a row sets `selected` (the key `results()` and `chunk()` use), so the section opens in the page's chunk view. |
  | list of strings | the queries |
  | `{evidence}` (pack) | evidence labels with titles, and bytes |
  | `{text}` (answer, agentic) | the text as markdown, cited labels, agentic tool rounds |
  | anything else | clipped JSON |
  | notes / error | listed under the row |

- **Live mode** (default when `details` is set): the runtime passes `onEvent` to `session.run` for *submit* runs and folds the events into `state.live`, `{nodes: {id: {status, ms, in, out, calls, hits, text, progress}}}`, repainting at most every 60 ms (the existing `onText` throttle). Keystroke (`change`) runs stay on the plain JSON path. While `run.status === 'running'`, the trace renders from `state.live`: started nodes show `running…`, hit nodes show their `hits` event (compact: id, score, rank, title, heading), the answer node shows its text so far, and long nodes show `progress` as `done/total`. When the result arrives, the trace renders from it as before.

```
runNow('submit')
  live = { nodes: {} }
  session.run(signals, { trigger, signal, onText, onEvent: ev => fold(live, ev); throttle(set({ live })) })
fold(live, ev):
  node start → nodes[id] = { status: 'running' }
  node end   → nodes[id] = { …, status, ms, in, out, calls, error }
  hits       → nodes[id].hits = ev.hits (and partial flag)
  text       → nodes[id].text += ev.delta
  progress   → nodes[id].progress = { done, total, unit }
```

### 1.3 Decisions

- **Opt-in through `details: true`.** Plain `trace()` stays the compact table, so existing presets do not change.
- **Events only for submit runs.** Keystroke runs are 5–15 ms and finish before a live view would paint.
- **No new server API.** Everything comes from the run result and the RAGWB-004 events.
- **Cached steps describe their output (added during implementation).** A memoized step used to emit only `node:end`, so during a submit run that follows a keystroke run of the same query, the cached retrieve and pack steps had nothing to show. The engine now emits the same `hits`/`context` events, and records the same `top` entry, for a cached step as for one that ran (`describe` in `Session.run`).

## 2. An asynchronous, batched `rerank.model`

### 2.1 Current state

`rerank.model` (`pkg/natives/vector.go`) calls `h.rerank(model, q, docs)`, a Go function that calls `Embedder.Rerank` **synchronously on the runtime's owner goroutine** with `context.Background()`. The RAGWB-004 task said "progress per batch", but there are no batches: the call sends all top-`k` passages (default 30) in one request. That has three consequences:

1. Every other request to the app waits while the rerank runs, because the owner goroutine is blocked. That is a few hundred milliseconds with a remote model.
2. A cancelled request cannot stop it.
3. There is nothing to report progress on.

### 2.2 Design

- **Asynchronous.** A new `h.rerankAsync(model, q, docs, {batch, concurrency}, onBatch, signal)` returns a promise. Go runs the batches on goroutines with a per-call context cancelled by the JS `signal` (the RAGWB-004 pattern from `sample.go`), and posts each finished batch and the final settlement back to the event loop (`runtimebridge` `PostWithLifetimeContext`). The owner goroutine is free while the model works.
- **Batched.** The passages are split into batches of `p.batch` (default 8), with at most `p.concurrency` (default 2) requests in flight. A cross-encoder scores each (query, passage) pair independently, so the scores of a batched rerank equal those of a single request, and the merged order (score descending, original position for ties) is the same.
- **Progress and partial results.** After each batch, the component calls `ctx.progress(scored, total, 'passages')` and `ctx.partial(scoredSoFarSortedByScore)`. Under RAGWB-004 these become `progress` events and `hits` events with `partial: true`, which the live trace displays.
- **Unchanged surface.** Same component type, parameters (`model`, `k`, `keepTail`) and output; the new parameters are optional.

```
run: async (s, ctx, hits, p) => {
  head = hits[0:k]; docs = texts(head)
  scored = []
  ranked = await h.rerankAsync(model, q, docs, { batch, concurrency }, batch => {
    scored.push(...batch)                                      // [{index, score}]
    ctx.progress(scored.length, docs.length, 'passages')
    ctx.partial(sortByScore(scored).map(r => withScore(head[r.index], r.score)))
  }, ctx.signal)
  return merge(ranked, head) ++ (keepTail ? rest : [])
}
```

### 2.3 Decisions

- **Default batch 8, concurrency 2.** With k=30 there are four requests, two in flight: progress is visible without multiplying request overhead. Both are parameters.
- **Results are merged in Go**, sorted by score with the original index as the tie-break, so the order is deterministic whatever order batches finish in.
- **Out of scope:** query embedding in `index.vector` search is also synchronous on the owner. It is one short request per distinct query (cached), and is noted for later.

## 3. Tests

- **Go, `pkg/runner`, with the `bowEmbedder` test fake:** batched rerank equals an unbatched one (`batch: 99`); `progress` events count up to k; `hits` partial events grow; the result is identical; while a slow embedder reranks, another request to the same runtime completes (the owner is not blocked); cancelling the request cancels the rerank's context.
- **gui:** a JS test of the fold of events into `live`; a Playwright check on a served `answer-first` with `trace({details: true})`: rows expand to hits with scores and terms, a click opens the chunk, and live rows appear during a slow submit (fake LLM with delay).

## 4. Files

| File | Change |
|---|---|
| `pkg/ragjs/assets/gui.js` | `trace({details, limit, live})`, detail views, `traceOpen`, live folding in `runNow` |
| `web/workbench/index.html` | `.g-trace-*` styles (the gui CSS served at `/gui.css` is cut from this file) |
| `pkg/natives/vector.go` | `h.rerankAsync`, async batched `rerank.model` |
| `examples/presets/answer-first.js` | Trace tab uses `trace({ details: true })` |
