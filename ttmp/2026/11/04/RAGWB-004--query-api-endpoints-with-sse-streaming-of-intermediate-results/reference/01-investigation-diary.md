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

## Step 4: Phase 2 — verbs run in the served runtime, in request scopes (ServedApp.Invoke)

The host can now run an app's verbs in the app's long-lived runtime:
- `ParseVerbParams` turns a JSON object (or query-string lists) into the verb's glazed values, the way the CLI parses flags;
- `ServedApp.Invoke` runs the verb inside a request scope and returns its result and how many `rag.run`s it made.

`app.js` may declare verbs. When it does, the harness requires it through the jsverbs loader (`/app.js`), which is what captures them.

The scope handoff needed no go-go-goja change (Phase 0 dropped):
1. under a per-app mutex, the host wraps the function jsverbs captured, once, and announces a pending scope;
2. it starts `jsverbs.InvokeInRuntime` in a goroutine;
3. it releases the mutex as soon as the wrapper has taken the scope.

Verbs run, and interleave, without the lock. Four concurrent `answer` requests over a streaming fake LLM each received only their own events.

### Prompt Context
**User prompt (verbatim):** see Step 3.
**Assistant interpretation:** Guide Phase 2 (host), with the D4 deviation recorded.
**Inferred user intent:** see Step 3.
**Commit (code):** the commit for this step: "RAGWB-004 Phase 2: served verbs in request scopes".

### What I did
- `pkg/runner/verbs.go` (new):
  - `Event{Type, Data}`, `EventSink`, `SinkFunc`, `Detail{Top, Chunks}`, `ParamError{Fields}`;
  - `kebab`, a copy of jsverbs' field-name normalization, so JSON keys may be `maxTokens` or `max-tokens`;
  - `DescribeVerb` and `VerbField` (for `GET /verbs`);
  - `ParseVerbParams`: field index from `CommandDescriptionForVerb(verb).Schema`; nested section objects; `coerce` from JSON numbers and arrays to the field type (an integer that isn't one is an error); `sources.Execute(schema, vals, FromMap(m), FromDefaults())`; a required check;
  - `ServedApp.Verbs/Verb/Registry`;
  - `ServedApp.Invoke(ctx, verb, vals, sink, detail) (result, runs, err)`. Events cross into Go as `JSON.stringify` strings, so key order is kept and no export walk is needed; a cancelled ctx aborts the scope (`waitVerb`); the scope is closed after the verb.
- `pkg/runner/serve.go`:
  - `AppSpec.Verbs` and `ServedApp.verbs/verbMu`;
  - the module path (`/app.js`) is passed to the harness as `modulePath`;
  - harness: `__ragwbWrapVerb(entry, name)`; `collect()` calls `eng.gc([ir, ...eng.recentIrs()])`.
- `pkg/ragjs/assets/rag.js`:
  - `Engine.run` records the IR (`noteIr`, an LRU of 8; `recentIrs()`);
  - `Engine.gc(irs)` takes a list;
  - `Engine.buildShared`: a second query for a plan being built waits for that build (`{type:'build', waiting:true}`); `Session.run` uses it.
- `pkg/runner/verbs_test.go`:
  - an app whose `app.js` declares `search`, `answer` and `late` (a verb that awaits before `rag.run` and passes `{scope}`, plus a custom `scope.emit`);
  - `TestServedVerbInvokeStreamsEvents`: rows, 1 run, node/hits events, the default `k` from the field, deltas that join to the answer text, events from `late`;
  - `TestServedVerbsConcurrentScopes`: four goroutines, each sink sees only its own `run.signals.q`;
  - `TestServedVerbParams`: `k:"many"`, an unknown key and `k:2.5` are `ParamError`s; the query-string form `{"k":["4"]}` works; `DescribeVerb`.

### What worked
```
go test ./pkg/runner -run TestServedVerb -v
--- PASS: TestServedVerbInvokeStreamsEvents (0.18s)
--- PASS: TestServedVerbsConcurrentScopes (0.19s)
--- PASS: TestServedVerbParams (0.13s)
go vet ./... && go test ./... -count=1                     all ok
```

### What didn't work
- First compile: a function-local `type done struct` passed to `waitVerb`, which was declared with an anonymous struct type (`cannot use res (variable of type chan done) as <-chan struct{…}`). Fixed with a package type `verbDone`.

### What I learned
- jsverbs' loader replaces the require loader with one that knows only the scanned files, keyed `/rel/path.js`. The serve harness used to `require()` the absolute path of `app.js`, which that loader cannot resolve, so it must use the module path. Every other file the app requires is scanned too: all `.js` files except `*.wb.js`.
- `InvokeInRuntime` returns `result.Export()`. An integer JS result comes back as `int64` (the `late` verb test), and objects as `map[string]any`, which is ready for JSON.

### What was tricky to build
- **The scope handoff without upstream changes.** The announce → enter window must be exclusive, but the verb's run must not be, or a slow LLM answer would block every search. The mutex covers only "announce, start invoking, wait until the wrapper takes the scope". If invoking fails before the wrapper runs (argument binding, require), the pending scope is withdrawn under the same lock.

### What warrants a second pair of eyes
- If something other than the host calls a wrapped verb function (another verb calling it directly, say), it runs unscoped, because there is no pending scope. That is correct but silent.
- `Engine.buildShared` keys on the digest list; a build that fails is removed from the map (`finally`), so the next query retries.

### What should be done in the future
- Phase 3 (HTTP and SSE), and wiring the verb scan into `ragwb serve`.

### Code review instructions
- Read `Invoke` in `pkg/runner/verbs.go` and `__ragwbWrapVerb` in `serve.go`.
- Validate with `go test ./pkg/runner -run TestServedVerb -v`.

## Step 5: Phase 3 — verb endpoints over HTTP, JSON and SSE, in ragwb serve

`ragwb serve` now exposes every verb of a served app:
- `GET /api/rag/{app}/verbs` describes them;
- `POST` or `GET /api/rag/{app}/verbs/{verb...}` runs one, answering with a JSON envelope or an SSE stream.

SSE is selected by `Accept: text/event-stream` or `?stream=sse`, and the stream ends with `result` or `error`. Query options are `chunks=1`, `events=a,b` and `top=N`. A parameter error is a 400 that names the field, and more than `--max-inflight` concurrent requests get 503.

Against the real binary on `examples/apps/reports`, `reports search` answers in 5–11 ms per request.

### Prompt Context
**User prompt (verbatim):** see Step 3.
**Assistant interpretation:** Guide Phase 3.
**Inferred user intent:** see Step 3.
**Commit (code):** the commit for this step: "RAGWB-004 Phase 3: verb endpoints with JSON and SSE".

### What I did
- `pkg/server/sse.go` (new):
  - `streamSink`: a queue of 256 events. When the queue is full, `Emit` waits at most 100 ms and then cancels the request (`slow_client`), so the runtime is never stalled long;
  - `sseWriter`: `event:`/`id:`/`data:` framing with `seq` and `t` prepended, a flush per event, `: ping` heartbeats every 15 s;
  - `pump`: text deltas of one node that arrive within 30 ms are merged into one event;
  - `wantsSSE` and `eventFilter`; `start`, `result` and `error` always pass the filter.
- `pkg/server/verbs.go` (new):
  - `handleVerbs`: path, url, short, long, tags, fields;
  - `findVerb`: the full path, or the bare name when it is unique. jsverbs names a file's verbs after the file, so `app.js`'s `answer` is `app answer`, while `/verbs/answer` also works;
  - `requestParams`: the JSON body plus query strings, minus the control keys;
  - `paramsOf`, `runSummary` (`runs[]` and `chunks` for the envelope, from `run`/`runEnd`/`hits` events), `handleVerb` (JSON or SSE), and `logVerb`, one line per request that says when a verb streamed no runs and why.
- `pkg/server/rag.go`: routes; `RagApp.MaxInflight` with `acquire`/`release` (`DefaultMaxInflight = 8`).
- `pkg/runner/verbs.go`: string and `[]string` values are parsed with the field's own `ParseField`, so `k="lots"` is reported under `k`.
- `cmd/ragwb/cmds/serve.go`:
  - every app's directory is scanned with jsverbs. Verbs make the runtime use the jsverbs loader and pass the registry to `OpenApp`; a scan failure means no endpoints, with a message;
  - the startup line `N verb endpoint(s): …/verbs`;
  - `--max-inflight`.
- `pkg/ragjs/assets/rag.js`: `node` and `build` events carry `component` (the component type) instead of `t`, which collided with the writer's elapsed-time `t`.
- `pkg/server/verbs_test.go`:
  - `TestVerbEndpoints` (on the real `examples/apps/reports`): discovery, the JSON envelope, SSE (`start run …`, consecutive `seq`, hits capped by `top=4` with no chunk, `result` last), GET with query strings, `chunks=1` with `events=hits`, a 400 naming `k`, and a 404;
  - `TestVerbSSEStreamsLLMText` (a temp app whose `app.js` has an `answer` verb, fake LLM in 12 pieces): concatenated deltas equal the answer.

### What worked
```
go test ./pkg/server -run TestVerb -v
--- PASS: TestVerbEndpoints (0.22s)
    verbs_test.go:240: 12 deltas arrived as 2 text events
--- PASS: TestVerbSSEStreamsLLMText (0.19s)
go vet ./... && go test ./... -count=1        all ok
ragwb serve examples/apps/reports:
  ragwb serve: 4 verb endpoint(s): http://127.0.0.1:8796/api/rag/reports/verbs
  curl -sN -H 'Accept: text/event-stream' ".../verbs/search?q=rebalance+law&k=3&plan=fused&top=2"
    event: start … event: run … event: node {"node":"text","component":"retrieve","phase":"start"} … event: hits {"total":26,…}
  curl -s -X POST …/verbs/search -d '{"q":"x","k":"a"}'
    {"error":{"code":"bad_params","fields":{"k":"Could not parse argument k as integer: …"},…}}
  log: reports verb reports search q="protojson" · 1 run(s) · 11 event(s) · 11.0 ms · ok
```

### What didn't work
- `sseEvent redeclared in this block` (`server_test.go` already had one); the test types were renamed.
- `panic: index out of range [0] with length 0` in the test: the `meta` retriever's `hits` event had zero hits. That is legitimate, and the test now guards it.
- A 404 for `/verbs/answer`: the verb's path is `app answer`, and the list showed `url:/api/rag/qa/verbs/app/answer`. The unique-name fallback was added in `findVerb`.
- The parameter error came back as `fields:{*: "Invalid value for field k: …"}`, from glazed's own parse error. Strings are now parsed per field (`parseStrings`).
- Duplicate `"t"` keys in node events (`"t":4.8,"type":"node","node":"text","t":"retrieve"`), found with curl; renamed to `component`.
- `pkill -f "serve --addr 127.0.0.1:8796"` killed my own shell, because the pattern matched the command line that contained it (exit 144), and the rename in the same command never ran. I now stop the server with the job's PID.

### What I learned
- The merge window works as intended: 12 deltas 3 ms apart become 2 events. A real model streams slower than 30 ms per token-batch, so most text events go out unmerged.

### What was tricky to build
- **Ordering the end of a stream.** The pump must drain everything the verb emitted before the final event is written. `Invoke` returns, the sink is closed, the handler waits for the pump to finish, and only then writes `result` or `error`, on the same writer and goroutine. There are no concurrent writes to the response.

### What warrants a second pair of eyes
- `streamSink.Emit` holds its mutex while it waits up to 100 ms on a full queue, so other emitters on the same request wait too. They are all on the owner goroutine, so there is only ever one.
- `requestParams` lets a JSON body key win over the same key in the query string.

### What should be done in the future
- Phase 4 (the shell on SSE) and Phase 5 (LLM cancellation, examples, docs).

### Code review instructions
- Read `handleVerb`, then `sseWriter.pump`.
- Validate with `go test ./pkg/server -run TestVerb -v`, then `ragwb serve examples/apps/reports` and the curl lines above.
