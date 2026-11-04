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
