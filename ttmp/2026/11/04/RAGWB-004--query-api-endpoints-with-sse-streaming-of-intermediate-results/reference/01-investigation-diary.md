---
Title: Investigation diary
Ticket: RAGWB-004
Status: active
Topics:
    - rag
    - backend
    - search
    - goja
DocType: reference
Intent: long-term
Owners: []
RelatedFiles: []
ExternalSources: []
Summary: ""
LastUpdated: 2026-11-04T14:23:09.154468243-05:00
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

## Step 1: ticket created; requirements restated for validation

I created the ticket and checked what streaming already exists before restating the request. No design was written yet: the user asked to validate my understanding first.

### Prompt Context
**User prompt (verbatim):** "I want to be able to expose queries as API endpoints, which can potentially also provie an SSE mode so we get streaming events for longer running query modes (say, with LLM or reranking or such, and also just intermediate results. this might require potentially updating the JS implementation / go side of the JS implementation to collect and emit that kind of information, including say intermediate results etc...).

Create a new docmgr ticket, and then repeat back to me what you understood from my request, so i can valiate, before we write a full desisgn guide"
**Assistant interpretation:** Make the queries of a plan into HTTP endpoints, with a JSON mode and an SSE mode. The SSE mode streams per-node progress, LLM text and intermediate results, which needs event plumbing in rag.js and the Go host.

### What I did
- `docmgr ticket create-ticket --ticket RAGWB-004 …`, plus this diary.
- Read the existing serve streaming:
  - `pkg/server/rag.go:209-263`: `POST /api/rag/{app}/run?stream=1` sends NDJSON, only `{type:"text",node,text}` frames (cumulative LLM text) and then the result;
  - `rag.js`: `Session.run` has `opts.onNode` (one trace entry per finished node, line 1780) and `opts.onText` (`ctx.emit`, line 1823);
  - `Engine.build` has `opts.onProgress` for map nodes.

## Step 2: intern design guide (verbs → endpoints, SSE event model, request scopes) written and uploaded

The user confirmed five points:
- endpoints are declared in `app.js`, derived from verbs;
- they are served by `ragwb serve`;
- the browser shell moves to SSE;
- full chunks are opt-in;
- there is no auth and no tenants.

They asked for a detailed intern guide. I read the parts the design touches (jsverbs invocation, glazed parsing, `Session.run`/`ctx`, the sample bridge, the serve harness, shell.js) and wrote `design-doc/01-intern-guide-query-api-endpoints-and-sse-streaming.md`.

### Prompt Context
**User prompt (verbatim):** "- in the app.js, derived from verbs
- ragwb serve
- move browser shell SSE
- full chunks opt in
- yep, no auth / tenants for now

Create  a detailed analysis / design / implementation guide that is for a new intern, explaining all the parts of the system needed to understand what it is, with prose paragraphs and bullet point sand pseudocode and diagrams and api references and file references. It should be very clear and technical. Store in the ticket and the nupload to remarkable."
**Assistant interpretation:** Write the design guide on the confirmed decisions, store it in RAGWB-004, and upload it to reMarkable. No implementation yet.
**Inferred user intent:** A reviewable, implementable plan before any code.

### What I did
- Read `go-go-goja@v0.10.7/pkg/jsverbs/{model,runtime,binding,command}.go`:
  - `InvokeInRuntime` (`runtime.go:48`) does its own owner call;
  - `buildArguments` is unexported;
  - the binding modes are positional, section, `all` and `context`.
- Read `glazed@v1.3.5/pkg/cmds/sources/update.go`: `FromDefaults` and `FromMap` can turn JSON parameters into `values.Values`.
- Read `rag.js`:
  - `Session.run` (1707), whose `onNode` fires only for finished nodes (1780);
  - `ctx.emit` (1823) sends cumulative text;
  - the `answer`, `agentic` and `rerank.llm` components (662–812);
  - `rag.run` uses `defaultEngine()` (2212, 2228), which is the same engine as the serve harness.
- Read `pkg/ragjs/sample.go`: model calls use `services.Lifetime()` (136), so a request cannot cancel them mid-flight.
- Read `pkg/server/rag.go:195-262` (NDJSON text frames, throttled to one per 50 ms, cumulative) and `web/app-shell/shell.js:73-107`.

### What I learned
- **Request scopes are the core design problem.** Requests share one goja runtime and interleave at every `await`, so a global "current request" is wrong. A scope installed around the verb's synchronous prefix and captured by `rag.run` is correct, and existing verbs work unchanged. It needs the host to call the captured verb function itself, which means exporting the jsverbs argument builder upstream (D4).
- `Engine.gc(ir)` from RAGWB-003 would break verb endpoints: verbs run other plans in the same engine, and their outputs would be retired. The design changes it to `gc(irs)` over recently used IRs.

### What warrants a second pair of eyes
- D3/D4, the scope capture rule and the upstream jsverbs change.
- D1 exposes *every* verb, including build verbs like `index`, unless it is tagged `internal`.

### Code review instructions
- Read guide §3.2, then §6.3, then §6.4. Check the event table against the components in `rag.js`.

## Step 3: Phase 1 — the engine event stream and request scopes (rag.js)

The engine now produces the event model from guide §6.4. Every run started inside a scope reports:
- `run` and `runEnd`;
- `node` start and end;
- `hits` (capped by `detail.top`, chunks only with `detail.chunks`);
- `context`, `note` and `progress`;
- `text` as deltas;
- `tool` start and end around every tool call of an LLM node;
- `build` and `built` when the run has to build first.

Scopes are plain JS objects (`rag.newScope(emit, detail)`), made current only for the synchronous start of a call (`rag.withScope`). `Engine.run` and `rag.build` capture them through `scoped(opts)`. Two runs interleaving at awaits saw only their own events.

I also decided against the upstream jsverbs change (guide D4). The host can wrap the verb function that jsverbs captures and hand it its scope; Phase 2 does this. jsverbs then stays as is, and its argument binding is reused unchanged.

### Prompt Context
**User prompt (verbatim):** "Impement. commit at appropriate intervals and keep a detailed diary as you work (using the diary format from the skill). (diary format from skill: manatory)"
**Assistant interpretation:** Implement guide Phases 1–5 on a new branch, with milestone commits and investigation-format diary steps.
**Inferred user intent:** Working query endpoints with SSE, built on the reviewed design.
**Commit (code):** the commit for this step: "RAGWB-004 Phase 1: engine events and request scopes".

### What I did
- Branch `task/ragwb-004-query-api-sse`, from the RAGWB-003 branch.
- `pkg/ragjs/assets/rag.js`:
  - `newScope(emit, detail)` returns `{emit, signal, detail, nextRun, runs, close, abort}`. The signal is an `AbortSignal` look-alike (`aborted`, `addEventListener('abort')`), and `emit` becomes a no-op after `close()`;
  - `withScope(scope, fn)`;
  - `scoped(opts)` adds the scope's numbered `onEvent`, its `signal` and its `detail`, then sets `scope: null` so nested calls don't count twice;
  - `hitsEvent`, `summarize`, and `eventSample`, which wraps tools' `execute` to emit `tool` start and end;
  - `Engine.run` (still async; `scoped()` runs before the first await) and `rag.build` use `scoped`; `rag.scope`, `rag.withScope` and `rag.newScope` are exported;
  - `Session.run`: `run`, node start/end (including cached and skipped nodes, which get only an end), `hits`, `context`, `runEnd`; a build it triggers gets `onEvent` with `scope: null`;
  - `Engine.build`: `build` per node (reused or computed, with `incremental`/`delta`), `built`, and `progress` for map nodes;
  - `ctx`: `emit(cumulative)` → `text` deltas (`reset` if a text ever doesn't extend the previous one); `note` → event; new `ctx.partial(hits)` and `ctx.progress(done, total, unit)`; `ctx.sample` is wrapped only when there is a listener.
- `pkg/llm/fake.go`: `Pieces`/`Delay` stream a reply in parts and honour ctx between parts, for the delta and cancellation tests.
- `pkg/ragjs/events_test.go`:
  - `TestRunEventsInScope`: the event sequence (build, run, node pairs, hits capped at 2 without chunks, context, at least 2 text deltas that join to `answer.text`, runEnd), agentic `tool` start/end, no events after `close()` or from unscoped runs, and no implicit scope after an await;
  - `TestInterleavedScopesStayIsolated`: two scoped runs over a streaming fake. Their sinks interleave (`/ab+a|ba+b/`), each sees only its own query, and `detail.chunks` applies per scope.

### What worked
```
go test ./pkg/ragjs -count=1                       ok (3.3 s)
go test ./pkg/ragjs ./pkg/runner -count=1          ok (runner 67.7 s) — existing onNode/onText users unchanged
event types of a scoped first run: build@files progress@parse … build@ix-text built run node:start@text hits@text node:end@text node:start@pack context@pack node:end@pack node:start@answer text@answer ×4 node:end@answer runEnd
```

### What didn't work
- The first test expected node `retrieve-text` (`missing node:start@retrieve-text in … node:start@text …`). A `retrieve('text')` node's id is `text` (`id: p => p.index`); only the test was wrong.

### What I learned
- A first query against an unbuilt plan streams its whole build (`build` per node, with `progress` from the parse and chunk map nodes) before `run`. That falls out of passing `onEvent` into the triggered build, and it's what the guide's §8.4 wanted.

### What was tricky to build
- **Not counting a run twice.** `rag.run` → `Engine.run` → `Session.run` → maybe `Engine.build`, and each could capture the scope. Only the public entry points call `scoped()`. The options it returns carry `scope: null`, and the internal build call passes `scope: null` explicitly, because it can run synchronously inside `withScope`.

### What warrants a second pair of eyes
- `Engine.run` lost and regained `async`; if it were synchronous, `compile` errors would throw instead of rejecting.
- `ctx.emit` with text that doesn't extend the previous text sends `reset` plus the whole text. No component does that today.

### What should be done in the future
- `rerank.model` progress per batch needs a Go callback; it is not wired yet.

### Code review instructions
- Read `newScope`/`withScope`/`scoped` (search for `request scopes and events` in `rag.js`), then the `emit(` calls in `Session.run`.
- Validate with `go test ./pkg/ragjs -run 'Events|Interleaved' -v`.
