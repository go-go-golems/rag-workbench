---
Title: Investigation diary
Ticket: RAGWB-005
Status: active
Topics:
    - rag
    - backend
    - goja
    - frontend
DocType: reference
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-04T16:03:19.939579601-05:00
WhatFor: ""
WhenToUse: ""
---

# Investigation diary

## Goal

<!-- What is the purpose of this reference document? -->

## Context

<!-- Provide background context needed to use this reference -->

## Quick Reference

<!-- Provide copy/paste-ready content, API contracts, or quick-look tables -->

## Usage Examples

<!-- Show how to use this reference in practice -->

## Related

<!-- Link to related documents or resources -->

## Step 1: design doc for both follow-ups; rerank.model made asynchronous, batched and cancellable

The user asked for a small design doc covering both RAGWB-004 follow-ups, then the implementation and a vault report. I wrote `design-doc/01-design-trace-details-widget-and-asynchronous-batched-rerank.md`, then implemented the rerank half.

Reading `pkg/natives/vector.go` corrected a premise I had stated twice in chat and in the RAGWB-004 task line, that `rerank.model`'s "batches run inside Go". There were no batches. The component called `Embedder.Rerank` once, for all top-k passages, synchronously on the runtime's owner goroutine with `context.Background()`. So a model rerank also blocked every other request to the app, and could not be cancelled.

The new `rerank.model`:
- awaits `h.rerankAsync`;
- runs batches of 8, two in flight, on goroutines with a per-call context tied to the request's abort signal;
- reports `progress` and partial `hits` after each batch;
- returns the same ranking as one request.

### Prompt Context
**User prompt (verbatim):** "Create a small design doc  for both. Then implement,  write a detailed project report for the obsidian vault as a deep dive technical analysis blog post using a textbook writing style (no analogies, see skill).      
 Commit and push the bsidian vault when done (go-go-parc vault)."
**Assistant interpretation:** A new ticket (RAGWB-005) with one design doc for the trace details widget and for rerank progress; implement both; then a vault report, committed and pushed.
**Inferred user intent:** See each step's results in the UI while a query runs, including a model rerank.
**Commit (code):** the commit for this step: "RAGWB-005: design doc; asynchronous batched rerank.model with progress".

### What I did
- Branch `task/ragwb-005-trace-details-rerank`; ticket RAGWB-005; design doc and three tasks.
- `pkg/natives/rerank.go` (new):
  - `RerankBatched(ctx, e, model, q, docs, size, conc, onBatch)`: batches on goroutines with a semaphore; batch-local indexes are mapped to global ones; the first error cancels the rest; the merge sorts by score, ties by index.
  - `(*Vectors).rerankAsync`: a goja promise; `onBatch` and the settlement are posted to the event loop with `runtimebridge`; the signal's `abort` cancels the call context; a cancellation rejects with `{code:'cancelled'}` (`rerank_failed` otherwise).
- `pkg/natives/vector.go`: `rerank.model.run` is `async`, calls `rerankAsync` with `p.batch` (default 8) and `p.concurrency` (default 2), and calls `ctx.progress`/`ctx.partial` per batch; the synchronous `h.rerank` is removed; the type comment is updated.
- Tests:
  - `pkg/natives/rerank_test.go`: batched (size 3, two in flight) equals one request, 4 batches with global indexes, ties in original order; a failing batch fails the call;
  - `pkg/runner/rerank_test.go`: `TestRerankModelBatchedAsyncWithProgress` (a slow ranker at 60 ms per request, k=12, batch 4): progress `4/12 … 12/12`, partial hits of 4 and then 8, one final hits event, a lexical query that completed during the rerank, and a ranking identical to `batch: 99`. `TestRerankModelCancelled`: an abort after the first batch gives `rerank:error:the request was cancelled` in the trace, and the ranker saw its context cancelled.

### What worked
```
go test ./pkg/natives -run RerankBatched -v      PASS ×2
go test ./pkg/runner -run TestRerankModel -v
--- PASS: TestRerankModelBatchedAsyncWithProgress (0.63s)
--- PASS: TestRerankModelCancelled (0.72s)
go vet ./... && go test ./pkg/natives ./pkg/runner ./pkg/ragjs -count=1     ok
TestVectorIndexAndRerankModel (existing) still passes: the async component returns the same top hits
```

### What didn't work
- Nothing failed. The failure was in my earlier statement about batches, which this step corrects in the design doc.

### What I learned
- A component's `run` may be `async` and await a Go promise. The engine already awaits `def.run`, so making `rerank.model` asynchronous needed no engine change.

### What was tricky to build
- **Callback order.** Batch callbacks come from several goroutines. Each runs under the merge mutex and posts its JS callback to the event loop. The settlement is posted only after `wg.Wait()`, so in the FIFO event loop every batch callback runs before the promise resolves. Partial results therefore never arrive after the final ones.

### What warrants a second pair of eyes
- With `concurrency: 2`, two requests go to the rerank service at once per query. Under a busy server that doubles that service's load compared with one request per query. Both numbers are parameters.

### What should be done in the future
- `index.vector` query embedding is still synchronous on the owner. It is one cached request per distinct query.

### Code review instructions
- Read `RerankBatched` and `rerankAsync` in `pkg/natives/rerank.go`, then the `rerank.model` JS in `vector.go`.
- Validate with `go test ./pkg/natives -run Rerank -v` and `go test ./pkg/runner -run TestRerankModel -v`.

## Step 2: trace({details: true}) — per-step outputs, live from engine events

This step implements the gui half of the design: an opt-in `trace({ details: true })` whose rows expand to show what each step produced. While a submit run is in flight, the same rows fill in from the RAGWB-004 engine events. The answer-first preset's Trace tab now uses it. The user's question that started this ("is it possible in say the "trace" tab … to have details on each pipeline step and the results at that point?") is answered by this widget.

The browser check found two defects that tests had not: the elapsed time of a running step froze between events, and cached steps showed "No output." during a live run. The second one needed an engine change in `rag.js`: a memoized step now reports its output through the same events and trace field as a step that ran.

### Prompt Context
**User prompt (verbatim):** see Step 1.
**Assistant interpretation:** Implement the trace-details half of the design, verify it in a browser, and keep the diary.
**Inferred user intent:** Inspect each pipeline step's intermediate results in the UI, including while a slow step is still running.
**Commit (code):** "RAGWB-005: trace({details: true}) per-step outputs, live from engine events".

### What I did
- `pkg/ragjs/assets/gui.js`:
  - The Runtime walks the IR once for a `trace` node with `details` (and `live !== false`) and sets `wantsEvents`.
  - `runNow` passes `onEvent` to `session.run` for submit runs only. Each event is folded by `foldEvent(live, ev)` into `state.live.nodes[id]` (status, `t0`, ms, in, out, calls, hits, partial, total, evidence, bytes, text, progress, tools, notes, error). Repaints are throttled to one per 60 ms, with a trailing timer.
  - `foldEvent` is exported as `gui.foldEvent` so Go tests can fold real event streams.
  - `RENDER.trace` keeps the compact table unless `n.p.details`. `traceDetails` renders one row per live IR node (pending/running/ran/cached/skipped) while running, and `result.trace` afterwards. `▸/▾` toggles live in `state.traceOpen`.
  - `stepDetail` chooses a view by the output's shape: a hit table (`#`, score, section, how: `via`, `parts` as `text #3`, the top four `why` terms, `↳ reason`), query lists, pack evidence plus byte count, answer markdown with citations and agent rounds, a JSON fallback, tool calls, notes, errors. Clicking a hit row sets `selected` (or `selectedKey`), which the chunk widget under the trace reads.
- `web/workbench/index.html`: `.g-trace-*` styles inside the generative-UI block (so `/gui.css` serves them), including `th.num` for right-aligned numeric headers.
- `examples/presets/answer-first.js`: the Trace tab is `b.trace({ details: true }).chunk(s => s.selected).dag()`.
- `cmd/ragwb/cmds/roots.go`: `--llm fake` reads `RAGWB_FAKE_STREAM="pieces,delay"` (e.g. `60,150ms`) so a browser can watch a slow fake answer and LLM rerank.
- Browser fixes:
  - a 250 ms `setInterval` repaints while a live submit run is in flight, so a running step's elapsed time counts up between events; it is cleared in `finally`;
  - live pack evidence shows `title › heading path` through `rt.host.rag.chunk` instead of raw ids, plus the byte count.
- `pkg/ragjs/assets/rag.js`: `describe(n, out, entry)` in `Session.run` sets `entry.top` and emits `hits`/`context`. It is called both for a step that ran and for a step served from the memo (`m.full`).
- `pkg/ragjs/trace_fold_test.go`:
  - `TestTraceFoldsRunEvents`: folding a scoped run's events gives the retrieve step its hits (`out == total`), the pack step its evidence, and the answer step text equal to the final answer.
  - `TestCachedStepsReportTheirOutput`: in one session, a change run and then a submit run of the same query; the folded retrieve and pack steps are `cached` and carry hits and evidence, and the cached trace entry has `top`.

### What worked
```
go test ./pkg/ragjs -run 'TestTraceFolds|TestCachedSteps|TestRunEvents|TestInterleaved' -count=1
ok  	github.com/go-go-golems/rag-workbench/pkg/ragjs	0.143s
gofmt -l .                     (no output)
go vet ./...                   (no output)
go test ./... -count=1         all ok (runner 70.8 s, ragjs 8.4 s, natives 4.1 s, server 1.9 s)
```
Browser, against
`RAGWB_FAKE_STREAM="60,150ms" ./dist/ragwb --llm fake --no-store serve --files examples/apps/reports/corpus --addr 127.0.0.1:8797 answer.wb.js`:
- After Ask, about 1.6 s into the run: text/meta/fuse/filter `cached`, `rerank rerank.llm running 1491…` (a moment later `1991…`), pack and answer `pending`. The expanded cached fuse step lists its 10 of 20 hits.
- During the answer: rerank `ran 40→8 9036.9 ms 1 call`, pack evidence `[E1] TTC Customer Clustering — … › The data model › The product classification cascade` … `10503 bytes of context`, `answer running 3043…` with the streamed text so far.
- After the run: fuse rows show `text #1 … text #10`; rerank rows show `via fuse · text #n`.
- Screenshots are in the vault, `Projects/2026/11/04/_assets/ragwb-005-trace-{live-rerank,live-answer,finished}.png`. They were taken before the last cosmetic fix: the live-answer shot shows a trailing `›` on intro chunks, whose heading path is empty.

### What didn't work
- The first live capture stayed at `rerank rerank.llm running … 60…` for 3 s: the ms cell was computed at paint time, and nothing repainted between events. Fixed with the 250 ms ticker.
- Expanding a cached step during a live run showed `No output.` A cached step emitted only `node:end`. Fixed in the engine (above).
- The first version of `TestCachedStepsReportTheirOutput` used `rag.run` twice and failed with `status:ran`: `rag.run` builds a fresh `Session` per call, so there is no memo between calls. The test now uses `rag.engine().session(rag.compile(plan))`. `plan.session()` does not exist on a builder (`TypeError: Object has no member 'session'`).
- Earlier screenshots were taken after the run had finished, because the fake LLM streamed too fast. `RAGWB_FAKE_STREAM` was added for this.
- Numeric cells were right-aligned under left-aligned headers. Fixed with `th.num`.

### What I learned
- The session memo has one entry per node, keyed on the last run that computed it. The browser's `state.result` can lag it (superseded runs, server-side sessions), so reusing the previous result for cached steps would sometimes show the wrong output. Having the engine describe cached outputs keeps the event stream complete by construction.
- The live `hits` event is compact (id, score, rank, doc, heading, via). The `how` column is therefore empty while running and fills in from `result.trace[].top` (with `parts`, `why`, `reason`) when the run ends.

### What was tricky to build
- **Two paint paths.** `onText` already repainted the streaming answer at 60 ms. The live trace uses its own throttle plus a trailing timer, so the last event before a quiet period is always painted. A superseded run (`seq !== runSeq`) never paints.
- **Detail views by shape, not by component type.** A user component can return hits, a context, or text. Shape detection covers them without a registry: an array of objects with `id`, an array of strings, `{evidence, text}` without `cited`, `{text}`.

### What warrants a second pair of eyes
- Cached steps now emit `hits` events on every submit run. With SSE (RAGWB-004) the stream grows by one capped `hits` event per cached hit step. `?events=` can filter them out.
- The ticker repaints the whole widget tree every 250 ms during a submit run with `details`. The trace is small, but a page with many widgets that depend on `live` would repaint with it.

### What should be done in the future
- Include `parts` in the compact `hits` event, so the `how` column is filled while running.
- A test that drives `traceDetails` rendering (DOM) rather than only `foldEvent`.

### Code review instructions
- Start at `foldEvent`, `stepDetail` and `traceDetails` in `pkg/ragjs/assets/gui.js`, then `runNow` (the `onEvent`, ticker and `finally`), then `describe` in `rag.js`.
- Validate with `go test ./pkg/ragjs -run 'TestTraceFolds|TestCachedSteps' -v`. Then serve the answer-first preset with `RAGWB_FAKE_STREAM="60,150ms" --llm fake`, open the Trace tab, press Ask, and expand rows while the rerank and the answer run.
