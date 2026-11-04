---
Title: 'Intern guide: query API endpoints and SSE streaming'
Ticket: RAGWB-004
Status: active
Topics:
    - rag
    - backend
    - search
    - goja
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles:
    - Path: repo://cmd/ragwb/cmds/verbs.go
      Note: how jsverbs verbs are scanned and invoked for the CLI today
    - Path: repo://pkg/ragjs/assets/rag.js
      Note: |-
        Session.run / Engine.build / ctx; gains the event model and request scopes
        Session.run/ctx/Engine.build event model and scopes
    - Path: repo://pkg/ragjs/sample.go
      Note: |-
        LLM bridge; text deltas, tool calls, cancellation
        LLM bridge deltas, tools, cancellation
    - Path: repo://pkg/runner/serve.go
      Note: |-
        served app runtime and harness (__serve); gains Invoke for verbs
        served app harness, gains Invoke
    - Path: repo://pkg/server/rag.go
      Note: |-
        serve HTTP API; /run with NDJSON streaming today, gains verb endpoints and SSE
        serve HTTP API, NDJSON run handler to become SSE
    - Path: repo://web/app-shell/shell.js
      Note: |-
        browser shell client; NDJSON reader to be replaced by SSE
        shell client NDJSON reader to SSE
ExternalSources:
    - https://html.spec.whatwg.org/multipage/server-sent-events.html
    - github.com/go-go-golems/go-go-goja/pkg/jsverbs (v0.10.7)
    - github.com/go-go-golems/glazed/pkg/cmds/sources (v1.3.5)
Summary: 'How to turn the jsverbs verbs of a served app into HTTP endpoints of ragwb serve, with a JSON mode and a Server-Sent Events mode that streams typed events while a query runs: node progress, intermediate hits, LLM text and tool calls, then the result. Covers the engine event model, per-request scopes in a shared goja runtime, cancellation, the browser shell''s move from NDJSON to SSE, and a phased plan.'
LastUpdated: 2026-11-04T10:00:00-05:00
WhatFor: Onboarding an engineer who will implement RAGWB-004; reviewing its design decisions.
WhenToUse: Before changing ragwb serve's HTTP API, Session.run's callbacks, the LLM bridge, or the browser shell's run client.
---

# Intern guide: query API endpoints and SSE streaming

## 1. Executive summary

`ragwb serve` serves search apps today. Each app's plan is built once in a long-lived JavaScript runtime, and the browser shell sends every keystroke to `POST /api/rag/{app}/run`. That endpoint is a private contract between the server and its own shell. Its body is the plan's raw signals and its response is a large payload shaped for the UI. Its streaming mode (`?stream=1`, NDJSON) carries only the text an LLM node is generating.

This ticket adds a public query API made from the app's **verbs**. Verbs are the JavaScript functions an app already declares with `__verb__` for the CLI (`ragwb verbs … parc search "q"`).

- **Every verb of a served app becomes an endpoint**, `POST /api/rag/{app}/verbs/{verb}`. Its parameters are the verb's declared fields, sent as JSON, and its response is the verb's own return value. The same function is now a CLI command and an HTTP endpoint.
- **Every endpoint has two modes.** Plain JSON waits and returns the result. Server-Sent Events (SSE) streams typed events while the verb runs:
  - `node` (a plan node started or finished, with timing);
  - `hits` (intermediate results: what retrieval, fusion and reranking produced, as ids and scores, with full chunks only on request);
  - `text` (LLM output deltas);
  - `tool` (agentic tool calls);
  - `progress` (long nodes);
  - `build` (a query that had to build first);
  - `result` and `error`.
- **The engine learns to emit these events.** `Session.run` and `Engine.build` gain one `onEvent` callback that replaces today's `onNode`/`onText` pair. Nodes get `ctx.partial()` and `ctx.progress()`, and the LLM bridge reports tool calls.
- **Per-request scopes.** Many requests share one goja runtime and interleave at every `await`, so each request runs its verb inside a *scope*. The scope is captured when `rag.run` is called, and it routes events to that request's stream and carries its cancellation signal.
- **The browser shell moves from NDJSON to SSE** on the same event model, so there is a single streaming implementation.
- Out of scope: authentication, tenants and remote access (serve stays on loopback), and event replay (`Last-Event-ID`).

```
 curl / app / agent                         ragwb serve (one goja runtime per app)
 ─────────────────                          ──────────────────────────────────────
 POST /api/rag/parc/verbs/search  ───────►  parse params with the verb's glazed schema
   Accept: text/event-stream                 │
                                             ▼
                                           invoke verb in a request scope ──► rag.run(plan, {q,k})
 event: node   {retrieve-text start}  ◄────  scope.emit ◄── Session.run: node start/end
 event: hits   {retrieve-text, 20}    ◄────  scope.emit ◄── hits nodes (ids, scores)
 event: text   {answer, "The rebal…"} ◄────  scope.emit ◄── ctx.sample onText deltas
 event: result {rows…}                ◄────  verb returns
```

## 2. Problem statement and scope

### 2.1 The problem

There is no way for a program to *use* a served app as a service. A script that wants "search the vault for X" or "answer Y from the reports" has three options today:

- shell out to `ragwb verbs …`, which builds or loads the corpus in a fresh process every time (0.16 s warm, up to ~22 s cold);
- post raw signals to the shell's private `/run` endpoint and pick apart a UI payload;
- reimplement the plan.

Long-running query modes make it worse. An LLM answer takes seconds, a model rerank hundreds of milliseconds, and an agentic loop several model rounds. A caller sees nothing until the end, and if it gives up, the server keeps spending LLM calls.

### 2.2 In scope (confirmed with the user)

- Endpoints are **derived from verbs** declared in the app directory, including `app.js` itself.
- They are served by **`ragwb serve`**, not by a separate API command.
- **SSE** is the streaming transport, and the **browser shell moves to SSE** (NDJSON goes away).
- Events carry **ids and scores by default; full chunks are opt-in**.
- **No auth and no tenants**: serve stays on loopback.

### 2.3 Out of scope

- Authentication, rate limiting, CORS for foreign origins, TLS.
- Event replay and resumable streams (`Last-Event-ID`); a dropped stream is simply retried.
- WebSockets. SSE is one-directional, which is all a query needs.
- Streaming *builds* as a separate API. Builds show up only as `build` events when a query triggers one.

## 3. Orientation: what you need to know first

Read this section before the design. It walks through the parts of the system the feature touches, in the order a request travels through them.

### 3.1 Plans, the engine, build and live nodes

A **plan** is a DAG of components (`rag.corpus(...).from(...).parse(...).chunk(...).index(...)` plus `rag.plan(...).over(corpus).signals(...).retrieve(...).rerank(...).answer(...)`). `rag.compile(plan)` turns it into an **IR**: a list of nodes `{id, t, p, in, phase}`.

- **Build nodes** (`phase: 'build'`: sources, parse, chunk, indexes) depend only on the corpus. `Engine.build(ir)` runs them once and stores their outputs (RAGWB-002/003).
- **Live nodes** (`phase: 'live'`: retrieve, fuse, filter, rerank, pack, answer, agentic…) run on every query. `Session.run(signals)` runs them in order, reading the built artifacts (`engine.artifacts(ir)`: chunks, indexes, docs).
- **Signals** are the query inputs (`q`, `k`, filters…), declared with `.signals({...})`.

A live node is defined with `live(type, {in, out, run(s, ctx, input, p)})` in `rag.js`. Its `ctx` is the API a node uses: `ctx.index(name)`, `ctx.chunk(id)`, `ctx.sample(prompt, opts)` for the LLM, `ctx.note(text)`, `ctx.emit(text)`.

### 3.2 One runtime per app, one owner goroutine

`ragwb serve` opens each app in a **long-lived goja runtime** (`runner.OpenApp`, `pkg/runner/serve.go:141`). goja is single-threaded, so go-go-goja gives every runtime an **owner**: all JS runs inside `rt.Owner.Call(ctx, op, fn)`, serialized on one goroutine. `ragjs.Eval` (`pkg/ragjs/await.go:15`) is the helper that calls into the runtime and then polls a returned promise until it settles. Each poll is another owner call.

The consequence matters for this design. **Requests interleave at every `await`.** While request A waits for an LLM response, the owner is free, and request B's JS runs. Any "current request" kept in a global variable would be wrong as soon as A resumes.

### 3.3 The served app harness

`serveHarness` (`pkg/runner/serve.go:43`) is the JS that opens an app. It requires `app.js`, compiles the plan, builds it, and installs `globalThis.__serve` (line 94), which has:

- `run(signalsJson, trigger, onText)`: one `session.run` on a kept session, packed with chunk bodies and neighbors for the shell;
- `chunk(id)` and `doc(id)`: lookups for shell cache misses;
- `refresh()`: the `--watch` rebuild (RAGWB-003 Phase 5).

The Go side is `ServedApp.Run/Chunk/Doc/Refresh`, each one `ragjs.Eval` of a `__serve` method.

### 3.4 The HTTP server

`pkg/server/rag.go` mounts the routes (lines 53–98):

```
GET  /api/rag                       → [{name, id, title}]
GET  /api/rag/{app}                 → {id, title, ir, app, build, stats}
POST /api/rag/{app}/run[?stream=1]  {signals, trigger} → {result, chunks, neighbors, packMs, timing}
GET  /api/rag/{app}/chunk?id=       → {chunks, neighbors}
GET  /api/rag/{app}/doc?id=         → doc metadata | null
GET  /api/rag/{app}/timings         → recorder entries
GET  /apps/{app}/                   → the app shell (HTML + app-shell.js)
```

`handleRun` (line 195) is the only streaming handler. With `stream=1` it writes NDJSON `{type:"text", node, text}` lines, throttled to one per 50 ms. It sends the *cumulative* text, so dropped frames lose nothing, and ends with one `{type:"result", …}` line.

### 3.5 The browser shell

`web/app-shell/shell.js` is the page's client. Its `run(signals, o)` (lines 73–107) POSTs to `/run`. When the page passes `onText` and the trigger is `submit`, it reads the response body with `getReader()`, splits it on newlines, and dispatches `text`, `error` and `result` messages. The shell is the only NDJSON consumer.

### 3.6 Verbs (jsverbs)

**jsverbs** (`github.com/go-go-golems/go-go-goja/pkg/jsverbs`) turns annotated JS functions into glazed CLI commands. A file declares:

```js
__package__({ name: "parc", short: "Search the go-go-parc Obsidian vault" });
__section__("subset", { title: "Vault subset", fields: { include: { type: "stringList", default: ["**/*.md"] } } });

async function search(q, k, mode, subset) {
  const r = await rag.run(plan(subset.include, mode), { q, k });
  return r.top(k).map((h, i) => ({ rank: i + 1, score: h.score, doc: h.doc, heading: h.heading }));
}
__verb__("search", {
  short: "Search the vault", sections: ["subset"],
  fields: { q: { argument: true }, k: { type: "int", default: 8 },
            mode: { type: "choice", choices: ["lexical", "semantic", "hybrid"], default: "lexical" },
            subset: { bind: "subset" } },
});
```

How the pieces fit:

- **`jsverbs.ScanDir(dir, opts)`** reads the files *statically* and returns a `Registry`, one `VerbSpec` per verb: `Name`, `Short`, `Fields map[string]*FieldSpec`, `UseSections`, `Params`, `File`, `Tags`, `OutputMode`. `FieldSpec` has `Type`, `Default`, `Choices`, `Required`, `Argument`, `Bind` and `Help`. In this repo the call is `jsverbs.ScanDir(dir, store.VerbScanOptions())`, which skips `*.wb.js`.
- **`registry.CommandDescriptionForVerb(verb)`** builds the glazed command description, which is the schema: sections, field definitions and types.
- **Invocation** (`registry.InvokeInRuntime(ctx, rt, verb, parsedValues)`, `jsverbs/runtime.go:48`):
  1. build the arguments from glazed `values.Values` (`buildArguments`, unexported: positional by field name, `bind: "<section>"` → that section's object, `bind: "all"`, `bind: "context"`);
  2. in one owner call, require the verb's module, which the registry's source loader overlays with a prelude that captures every function into `globalThis.__glazedVerbRegistry[module][name]`;
  3. call the captured function and wait for its promise.
- **`registry.RequireLoader()`** is the overlay loader. A runtime that should be able to invoke verbs must be built with it (`runner.Config.SourceLoader`). The CLI does this in `cmd/ragwb/cmds/verbs.go:108`.

Examples in this repo: `examples/apps/parc/parc.js` (index, search) and `examples/apps/reports/{search,index,evals}.js`. Today `app.js` files contain no verbs (`// No verbs.`). The serve harness requires `app.js` without the overlay, so a `__verb__` call there would currently throw `ReferenceError`.

### 3.7 `rag.run` and the default engine

`rag.run(target, signals, opts)` (`rag.js:2228`) compiles the target and runs it on the runtime's **default engine** (`defaultEngine()`, line 2212). That is the same engine the serve harness uses (`rag.engine()`). So a verb that calls `rag.run(plan, …)` inside the served runtime reuses everything already built or loaded for that plan, because the engine's memo is keyed by build digests. A verb whose plan differs from the app's (another mode or subset) builds it on first use: from the store if it was built before, otherwise incrementally or fully (RAGWB-003).

### 3.8 What the engine reports today

| Hook | Where | What it carries |
|---|---|---|
| `opts.onNode(entry)` | `Session.run`, `rag.js:1780` | one trace entry per *finished* live node: `{node, t, ms, in, out, status, calls, notes, top?}` |
| `opts.onText(node, text)` | `ctx.emit`, `rag.js:1823` | cumulative LLM text of a node (`answer`, `agentic` pass `onText: ({text}) => ctx.emit(text)`) |
| `opts.onProgress({node, done, total})` | `Engine.build` map nodes, line ~1467 | per-item progress of `enrich`-style build nodes |
| `this.emit({type:'node-built'…})` | `Engine.build` | engine listeners (the workbench) |
| `trace[]` / `result.outputs` | `Session.run` result | everything, but only at the end |

Gaps:
- nothing fires when a node *starts*;
- intermediate hits are visible only in the final `trace[i].top`;
- tool calls are only `ctx.note` strings;
- LLM text is cumulative, not deltas;
- the sample bridge (`pkg/ragjs/sample.go:136`) calls the model with the *runtime's lifetime context*, so a disconnected client cannot cancel an in-flight LLM call. The `signal` is checked only before the call starts.

## 4. Current state, with evidence

- **Private run API**: `pkg/server/rag.go:195-262`. The request is `{signals, trigger}`; the response is `{result, chunks, neighbors, packMs, timing}`; streaming is NDJSON, text only.
- **Cumulative, throttled text**: `rag.go:217-229` keeps one frame per 50 ms and drops the rest. That is safe only because each frame repeats everything so far.
- **Shell client**: `web/app-shell/shell.js:73-107`, a hand-written NDJSON line splitter.
- **Verbs are CLI-only**: `cmd/ragwb/cmds/verbs.go` builds a fresh runtime per invocation (`factory.NewRuntime` per call). serve does not scan for verbs.
- **No request identity in JS**: `Session.run` receives callbacks only through `opts`. Every `rag.run` call inside a verb would need them passed explicitly, and a verb has no way to receive them.
- **No mid-call LLM cancellation**: `sample.go:136` uses `services.Lifetime()`.
- **gc assumes one IR**: `Engine.gc(ir)` (`rag.js:1525`, RAGWB-003) drops memo entries whose digest the app's IR doesn't have. Verbs that run other plans in the same engine would have their outputs retired and closed under them.

## 5. Gap analysis

| Need | Today | Gap |
|---|---|---|
| Named, typed endpoints | one generic `/run` with raw signals | discover verbs in serve; map JSON to glazed values; route per verb |
| Stable response | UI payload | the verb's return value in a small envelope |
| Streaming events | NDJSON text frames | typed SSE events; node start/end, hits, tools, progress, build |
| Intermediate results | final `trace[].top` | emit `hits` when each hits-producing node finishes |
| Events to the right request | callbacks in `opts` | per-request scope captured by `rag.run` |
| Cancellation | none mid-call | abort signal per request → per-call LLM context |
| Shell on the same transport | NDJSON | SSE parser in shell.js; `/run` speaks SSE |
| Several plans in one served engine | `gc(appIr)` | gc over every recently used IR |

## 6. Proposed architecture

### 6.1 The picture

```
                     ┌──────────────────────── ragwb serve ────────────────────────────┐
 HTTP request        │  pkg/server                         pkg/runner                   │
 ───────────────     │  ┌──────────────────────┐          ┌───────────────────────────┐ │
 POST /verbs/search  │  │ handleVerb           │ Invoke() │ ServedApp                 │ │
 {q:"…",k:8}  ──────►│  │  params → values     │─────────►│  ragjs.Eval(owner call):  │ │
 Accept: SSE         │  │  sink := sseWriter   │          │   scope := newScope(sink) │ │
                     │  │  ctx  := r.Context() │          │   rag.withScope(scope, () │ │
                     │  └─────────▲────────────┘          │     verbFn(...args))      │ │
                     │            │ events (chan)         └────────────┬──────────────┘ │
                     │  ┌─────────┴────────────┐                       │ JS            │
 ◄── event: node …   │  │ SSE writer goroutine │          ┌────────────▼──────────────┐ │
 ◄── event: hits …   │  │  coalesce text,      │◄─────────│ rag.js                    │ │
 ◄── event: text …   │  │  heartbeat, flush    │ emit(ev) │  rag.run captures scope   │ │
 ◄── event: result … │  └──────────────────────┘          │  Session.run → onEvent    │ │
                     │                                     │  ctx.sample → text/tool   │ │
                     │                                     │  Engine.build → build     │ │
                     │                                     └───────────────────────────┘ │
                     └───────────────────────────────────────────────────────────────────┘
```

Three layers, each with one job:
1. **Engine (rag.js)**: produce events. It knows nothing about HTTP.
2. **Host (pkg/runner)**: invoke a verb in a scope and turn JS events into Go values on a channel. It knows nothing about SSE framing.
3. **Server (pkg/server)**: map HTTP to parameters, and events to JSON or SSE.

### 6.2 Endpoints derived from verbs

**Discovery.** When serve opens an app, it scans the app's directory with jsverbs (`jsverbs.ScanDir(dir, store.VerbScanOptions())`). For a workbench script (`*.wb.js`) it scans the script's directory. Every verb becomes an endpoint unless the verb is tagged `internal` (`__verb__("index", { tags: ["internal"], … })`). The runtime is created with `SourceLoader: registry.RequireLoader()`, so the harness's `require(app.js)` goes through the overlay: `app.js` may then declare verbs, and other files' verbs are captured when first required.

**Routes:**

```
GET  /api/rag/{app}/verbs                 → [{path, short, long, fields: [...], sections: [...]}]   (schema for clients)
POST /api/rag/{app}/verbs/{verb...}       body: JSON params           → JSON envelope, or SSE if asked
GET  /api/rag/{app}/verbs/{verb...}?k=…   query-string params         → same (lets EventSource and curl work)
```

`{verb...}` is the verb's full path with spaces as slashes. For example, `parc search` becomes `/api/rag/parc/verbs/parc/search`, and a verb without a package is just `/verbs/search`.

**Choosing the mode.** The response is SSE when `Accept` includes `text/event-stream` or the query has `stream=sse`; otherwise it is JSON. Detail options go in the query string, so they work for GET and POST alike:
- `chunks=1`: full chunk objects in `hits` events and a `chunks` map in the result;
- `events=node,hits,text`: an allow-list of event types;
- `top=N`: the cap on hits per `hits` event (default 20).

**Parameter mapping.** The verb's glazed schema (`CommandDescriptionForVerb`) defines what is accepted:

```
params (JSON object, keys = the verb's field names as written in __verb__, e.g. "q", "k", "mode")
  + section fields either flat ("include": [...]) or nested ("subset": {"include": [...]})
  → map[sectionSlug]map[cliFieldName]any
  → sources.Execute(schema, values, sources.FromDefaults(), sources.FromMap(m))   // glazed, as the CLI parses flags
  → validation: unknown keys → 400; type/choices/required errors → 400 {error:{code:"bad_params", fields:{k:"…"}}}
```

GET query strings are strings. They are parsed with the field's glazed type, the same parser the CLI uses for flag values. Lists are repeated keys (`?include=a&include=b`).

**JSON response envelope:**

```json
{
  "verb": "parc search",
  "params": { "q": "goja regexp", "k": 8, "mode": "lexical", "subset": { "include": ["**/*.md"] } },
  "result": [ { "rank": 1, "score": 0.81, "doc": "…", "heading": "…" } ],
  "runs": [ { "run": 1, "plan": "parc-lexical", "ms": 6.1, "calls": 0, "errors": [] } ],
  "chunks": { "…": { } },
  "ms": 9.4
}
```

- `result` is the verb's return value, exported from JS (`ragjs` `exportValue`). It is not converted to glazed rows: the HTTP API returns what the function returns.
- `runs` summarizes every `rag.run` the verb made, from the scope.
- `chunks` is present only with `chunks=1`.
- Errors: HTTP 400 for parameters; HTTP 500 `{error:{code:"verb_failed", message}}` for a verb that threw; HTTP 499-style client disconnect, which is logged only.

### 6.3 Request scopes: getting events to the right request

**The constraint** (§3.2): requests interleave at awaits, so "the current request" cannot be a plain global. But the *synchronous prefix* of a function call runs without interleaving, so the host can install a scope for exactly the synchronous part of the verb call, and `rag.run` captures it when it is called:

```js
// rag.js
let currentScope = null;
rag.scope = () => currentScope;                       // the scope of the call being started (or null)
rag.withScope = (scope, fn) => {                       // used by the host only
  const prev = currentScope; currentScope = scope;
  try { return fn(); } finally { currentScope = prev; } // restored before the first await resumes anything else
};
// rag.run / rag.build / engine.run capture it once, synchronously:
run: (target, signals, opts = {}) => {
  const scope = opts.scope ?? currentScope;
  return defaultEngine().run(compile(target), signals, scope ? withScopeOpts(opts, scope) : opts);
}
```

`withScopeOpts` adds three things to the options:
- `onEvent: scope.emit` (with the run number added);
- `signal: scope.signal`;
- `detail: scope.detail` (chunks or not, the hits cap).

From there, everything flows through `opts`, which already reaches `Session.run`, `Engine.build` (when a query triggers a build), every node's `ctx`, and `ctx.sample`.

**The one rule for verb authors.** `rag.run` must be *called* before the verb's first `await`. That is true for every verb in the repo, which all start with `const r = await rag.run(...)`. A verb that awaits something else first captures the scope explicitly:

```js
async function answer(q) {
  const scope = rag.scope();                    // first statement: still synchronous
  const plan = await loadPlanSomehow();          // an await: the implicit scope is gone after this
  const r = await rag.run(plan, { q }, { scope });
  scope && scope.emit({ type: 'note', text: 'post-processing' }); // verbs may emit their own events
  return shape(r);
}
```

When a scope is used after the verb's promise has settled, `scope.emit` is a no-op, so late events cannot leak into another request.

**How the host installs the scope.** `jsverbs.InvokeInRuntime` performs its own owner call, so there is no place to wrap it. The host invokes the verb itself, inside one `ragjs.Eval`:

```go
// pkg/runner/verbs.go (new)
func (a *ServedApp) Invoke(ctx context.Context, verb *jsverbs.VerbSpec, vals *values.Values, sink EventSink, detail Detail) (any, error) {
    args, err := a.verbs.Arguments(verb, vals)          // jsverbs: exported argument builder (Phase 0)
    ...
    return ragjs.Eval(ctx, a.rt, "ragwb.verb "+verb.FullPath(), func(vm *goja.Runtime) (goja.Value, error) {
        if _, err := a.rt.Require.Require(verb.File.ModulePath); err != nil { return nil, err }
        fn := capturedVerb(vm, verb)                    // __glazedVerbRegistry[module][function]
        scope := newScope(vm, sink, detail, ctx)        // JS object: emit, signal, detail
        withScope := ragFn(vm, "withScope")
        return withScope(goja.Undefined(), scope, vm.ToValue(func(goja.FunctionCall) goja.Value {
            v, err := fn(goja.Undefined(), toJS(vm, args)...)
            if err != nil { panic(err) }
            return v
        }))
    })
}
```

The one missing piece is building the arguments from glazed values, because `buildArguments` is unexported in jsverbs. Phase 0 exports it upstream in go-go-goja (we maintain it) as `(*Registry).Arguments(verb, values) ([]any, error)`. The CLI path does not change.

### 6.4 The event model

Every event is a JSON object with a `type`. The host adds `seq` (per stream) and `t` (milliseconds since the request started). Events from a `rag.run` carry `run` (1, 2, … within the request), because a verb may run several plans.

| type | when | payload |
|---|---|---|
| `start` | request accepted | `{verb, params}` |
| `run` | a `rag.run` begins | `{run, plan, trigger, signals}` |
| `build` | a query had to build first | `{run, node, t, ms, incremental, delta?, stored?}`, one per build node; then `{run, built: true, ms}` |
| `node` | a live node starts / ends | start: `{run, node, t, phase:"start"}`; end: `{run, node, phase:"end", status, ms, in, out, calls, notes?, error?}` |
| `hits` | a node with `out: 'hits'` finishes | `{run, node, hits:[{id, score, rank, via?, reason?, doc, heading}], total}`; with `chunks=1`, each hit also has `chunk:{…}` |
| `context` | a `pack` node finishes | `{run, node, evidence:[{label, id}], tokens}` |
| `text` | an LLM node produces text | `{run, node, delta}`; the concatenation of a node's deltas is its full text |
| `tool` | the LLM calls a tool | start: `{run, node, call, name, input}`; end: `{run, node, call, name, ms, summary, isError}` |
| `progress` | a long node reports progress | `{run, node, done, total, unit}` |
| `note` | `ctx.note(text)` | `{run, node, text}` |
| `runEnd` | a `rag.run` finishes | `{run, ms, calls, errors}` |
| `result` | the verb returned | the JSON envelope of §6.2 |
| `error` | the verb threw, or the request failed | `{code, message, node?}`; the stream ends after it |

Design choices worth noticing:

- **Deltas, not cumulative text.** SSE over a single connection is ordered and lossless, so deltas are cheaper and are what other LLM streaming APIs send. The server may *coalesce* adjacent deltas of the same node into one event (§6.6), which concatenates them and loses nothing.
- **`hits` are small by default.** On the vault, a retrieve node returns 20–30 hits. With ids, scores, document titles and headings that is about 3 KB per event. With `chunks=1`, chunk texts add about 2 KB per hit.
- **No `hits` for skipped or cached nodes.** A `node` end event with `status: "cached"` tells the client the node didn't run.

### 6.5 Engine instrumentation (rag.js)

**One callback.** `Session.run(signals, opts)` and `Engine.build(ir, opts)` accept `opts.onEvent(ev)`. The old `onNode`/`onText` become thin adapters so existing callers keep working: the workbench uses `onNode`, the shell harness `onText`.

```js
// Session.run (sketch of the change around rag.js:1707-1790)
const emit = ev => { if (opts.onEvent) try { opts.onEvent(ev); } catch (_) {} };
emit({ type: 'run', plan: ir.id, trigger, signals: s });
for (const n of ir.nodes) {
  ...
  if (cachedOrSkipped) { emit({ type: 'node', node: n.id, phase: 'end', status: entry.status, ms: 0, in: entry.in, out: entry.out }); continue; }
  emit({ type: 'node', node: n.id, phase: 'start' });
  const ctx = this.ctx(n, ir, art, sample, s, notes, opts, emit);    // ctx gets emit
  out = await def.run(sp, ctx, input, p);
  if (n.out === 'hits')    emit(hitsEvent(n, out, art, opts.detail));
  if (n.out === 'context') emit({ type: 'context', node: n.id, evidence: out.evidence.map(e => ({ label: e.label, id: e.id })), tokens: out.tokens });
  emit({ type: 'node', node: n.id, phase: 'end', status: entry.status, ms: entry.ms, in: entry.in, out: entry.out, calls: entry.calls, notes });
}
emit({ type: 'runEnd', ms: result.ms, calls: result.calls, errors });
```

**Node context additions:**

```js
ctx.emit(text)            // unchanged signature; becomes a text *delta* source: Session tracks the last text
                          // per node and emits {type:'text', node, delta: text.slice(prev.length)}
ctx.partial(hits)         // a node's intermediate list before it finishes (e.g. rerank.llm after parsing a first
                          // batch); emitted as {type:'hits', node, partial: true, …}
ctx.progress(done, total, unit)   // e.g. rerank.model batches, enrich items
ctx.note(text)            // unchanged; also emitted as {type:'note'}
```

`ctx.emit` keeps taking cumulative text, because `answer` and `agentic` pass `({text}) => ctx.emit(text)` and changing every caller is churn. Deltas are computed in one place.

**Tool events.** `ctx.sample(prompt, { tools })` wraps each tool's `execute` before handing it to the bridge:

```js
const wrapTools = (tools, emit, node) => tools.map((t, i) => ({ ...t, execute: async (input, o) => {
  const call = ++calls, t0 = now();
  emit({ type: 'tool', node, call, name: t.name, phase: 'start', input });
  try { const v = await t.execute(input, o); emit({ type: 'tool', node, call, name: t.name, phase: 'end', ms: now() - t0, summary: summarize(v) }); return v; }
  catch (e) { emit({ type: 'tool', node, call, name: t.name, phase: 'end', ms: now() - t0, isError: true, summary: String(e.message || e) }); throw e; }
} }));
```

`summarize` returns `"8 results"` for arrays and the first 120 characters for strings, so tool events stay small.

**Build events.** `Engine.build(ir, opts)` emits `{type:'build', node, ms, incremental, delta, stored}` per node. `Session.run` passes `onEvent` into the build it triggers (`rag.js:1711`) when the artifacts are stale. Builds started without an `onEvent` emit nothing.

**Go-backed nodes.** `rerank.model` (`pkg/natives/vector.go:65`) calls the rerank backend in batches. It reports `ctx.progress(done, total, 'passages')` per batch from its JS wrapper, and the Go side only returns per-batch results.

**Detail and size.** `hitsEvent(n, out, art, detail)` maps hits to `{id, score, rank, via, reason, doc: chunk.title, heading: chunk.headingPath.join(' › ')}` and caps them at `detail.top` (default 20). With `detail.chunks` it adds `chunk: art.chunks.get(id)` (the JSON chunk object).

### 6.6 Transport: SSE framing on the Go side

**Sink.** The host turns JS events into Go values on a bounded channel:

```go
// pkg/runner/events.go (new)
type Event struct { Type string; Data json.RawMessage }   // Data includes run/node/…; seq and t are added by the writer
type EventSink interface { Emit(Event) }                  // must not block the owner goroutine

type chanSink struct { ch chan Event; dropped atomic.Int64 }
func (s *chanSink) Emit(e Event) {
    select { case s.ch <- e: default: s.coalesceOrDrop(e) }  // text deltas are merged into a pending event; nothing else is dropped
}
```

`scope.emit` is a Go function exposed to JS. It runs on the owner goroutine, so it must only `json.Marshal` and push to the channel. It must never write to the network, because a slow client would otherwise stall every app request.

**SSE writer** (one goroutine per streaming request):

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-store
X-Accel-Buffering: no

event: start
id: 1
data: {"seq":1,"t":0.2,"verb":"parc search","params":{"q":"goja regexp","k":8}}

event: node
id: 2
data: {"seq":2,"t":1.1,"run":1,"node":"retrieve-text","phase":"start"}

: ping                                      ← comment line every 15 s while idle (keeps proxies and clients alive)

event: result
id: 9
data: {"seq":9,"t":9.4,"verb":"parc search","result":[…],"runs":[…],"ms":9.4}
```

Writer rules:
- One `event:`/`id:`/`data:` block per event; `data` is single-line JSON (no raw newlines, so no multi-line `data:` splitting).
- Flush after each event, or after each batch of text deltas. Deltas of the same node arriving within 30 ms are merged into one `text` event, which bounds the event rate during fast generation.
- Heartbeat: a comment line every 15 s.
- `id:` is the sequence number, informational only (no replay, §2.3).
- The stream always ends with exactly one `result` or `error`.

**Backpressure.** The channel holds 256 events. If it is full, a `text` event is merged into the pending text of its node, which is lossless. Any other event blocks for at most 100 ms, and if the channel is still full the request is cancelled with `error {code:"slow_client"}`. A stalled reader therefore cannot hold the runtime for long.

### 6.7 Cancellation

```
client closes the connection
  → r.Context() done (net/http)
  → ServedApp.Invoke's ctx done → scope.signal.abort()      (Go flips the JS signal on the owner: {aborted:true}, fires listeners)
      → Session.run: checks signal between nodes → stops with status 'cancelled'
      → ctx.sample: the per-call Go context is cancelled → the model request is aborted
  → ragjs.Eval stops polling (it returns ctx.Err()); the promise may settle later and is ignored
```

The new piece is in `pkg/ragjs/sample.go`. Today the bridge uses `services.Lifetime()` for every model call (line 136). The new version:
1. creates a per-call `context.WithCancel(services.Lifetime())`;
2. registers a listener on the JS `o.signal` that calls `cancel`;
3. passes that context to `backend.Sample`.

LunaRoute's HTTP client already honours its context, so the in-flight request is aborted. A cancelled call rejects with `{code:'cancelled'}`, which `Session.run` already handles by stopping the loop (`rag.js`: `if (e && e.code === 'cancelled') { trace.push(entry); break; }`).

### 6.8 Concurrency and engine state

- **Interleaving is allowed.** Two requests to the same app run their verbs in the same runtime and interleave at awaits. Each has its own scope, its own `Session` (every `rag.run` creates one), and its own channel. Artifacts are shared and read-only.
- **Builds are shared.** If two requests need the same unbuilt plan, both call `Engine.build`. Today the second would build again. The engine should remember an *in-flight build promise* per digest set and have the second caller await it. Both requests then receive `build` events, from the first caller's build, which is fine.
- **gc over several IRs.** The serve harness's `collect()` and `refresh()` call `eng.gc(ir)` with the app's IR (RAGWB-003). Verbs run other plans in the same engine, so gc must keep their outputs. `Engine.gc(irs)` takes a list, and the engine records every IR it runs in a small LRU (`this.recentIrs`, 8 entries by plan id plus digest). serve passes `[appIr, ...eng.recentIrs()]`.
- **A concurrency cap.** Each app gets a limit of concurrent verb requests (`--max-inflight`, default 8). Requests beyond it get 503 `busy`. This protects the owner from piling up LLM fan-out.

### 6.9 The browser shell on SSE

`/api/rag/{app}/run` keeps its request (`{signals, trigger}`) and final payload (`{result, chunks, neighbors, packMs, timing}`), but streams with SSE when asked:
- `Accept: text/event-stream` → the same event types as §6.4, from the kept session's run (`__serve.run` passes `onEvent`), then `event: result` with the shell payload;
- no `Accept` → plain JSON as today;
- `?stream=1` and NDJSON are removed.

`shell.js` replaces the line splitter with a small SSE parser over `fetch` + `getReader()`. `EventSource` cannot POST, and the shell needs the POST body.

```js
async function* sse(res) {                    // yields {event, data}
  const rd = res.body.getReader(), dec = new TextDecoder(); let buf = '';
  for (;;) {
    const { done, value } = await rd.read(); if (value) buf += dec.decode(value, { stream: true });
    let i; while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i); buf = buf.slice(i + 2);
      let event = 'message', data = '';
      for (const line of block.split('\n')) { if (line.startsWith('event:')) event = line.slice(6).trim(); else if (line.startsWith('data:')) data += line.slice(5).trim(); }
      if (data) yield { event, data: JSON.parse(data) };
    }
    if (done) return;
  }
}
// run(): for await (const m of sse(res)) { if (m.event === 'text') o.onText(m.data.node, (texts[m.data.node] = (texts[m.data.node] || '') + m.data.delta)); else if … }
```

The shell keeps its `onText(node, cumulativeText)` contract toward the page by accumulating deltas, so gui components don't change. A page can additionally pass `onEvent` to see node progress, for example to show "reranking…" or to draw the first-stage hits before the rerank lands.

## 7. Decision records

### D1: endpoints are the app's verbs, not a new declaration
- **Context:** The user wants query endpoints "in the app.js, derived from verbs".
- **Options:**
  - (a) a new `app.endpoint(name, {params, run})` API;
  - (b) expose the jsverbs verbs found in the app directory, including `app.js`;
  - (c) expose the plan's signals generically.
- **Decision:** (b).
- **Rationale:**
  - One function serves the CLI and HTTP.
  - Parameters already have types, defaults, choices and help (the glazed schema).
  - `GET /verbs` can describe them to clients.
- **Consequences:**
  - serve's runtime needs the jsverbs source loader.
  - Verbs that are not queries (`index`) are endpoints too, unless tagged `internal`.
  - Verb results are free-form JSON, not glazed rows.

### D2: SSE, and the shell moves to it
- **Context:** Streaming exists as NDJSON for the shell only.
- **Options:**
  - (a) keep NDJSON;
  - (b) SSE for the API, NDJSON for the shell;
  - (c) SSE everywhere;
  - (d) WebSockets.
- **Decision:** (c).
- **Rationale:**
  - SSE is the standard for one-way event streams, works with `curl -N` and `EventSource` (GET), and has named events.
  - One writer and one event model.
  - WebSockets add a protocol for no gain.
- **Consequences:** `shell.js` gets an SSE parser over `fetch` (it POSTs); `?stream=1` is removed.

### D3: per-request scopes captured at `rag.run` call time
- **Context:** Many requests share one runtime and interleave at awaits (§3.2).
- **Options:**
  - (a) a global "current request";
  - (b) pass callbacks explicitly into every `rag.run`;
  - (c) a scope installed around the verb's synchronous prefix and captured by `rag.run`, with `rag.scope()` for explicit use;
  - (d) one runtime per request.
- **Decision:** (c).
- **Rationale:**
  - It is correct under interleaving: a scope is captured synchronously and then travels in `opts`.
  - Existing verbs work unchanged.
  - (d) would lose the shared built artifacts, which is the point of serve.
- **Consequences:**
  - The documented rule: call `rag.run` before the first `await`, or capture `rag.scope()` first.
  - The host invokes verbs itself, which needs the jsverbs argument builder exported (D4).

### D4: export the jsverbs argument builder upstream
- **Context:** `InvokeInRuntime` performs its own owner call, so the host cannot install a scope around the function call. `buildArguments` is unexported.
- **Options:**
  - (a) add `(*Registry).Arguments(verb, values)` to go-go-goja;
  - (b) add a before/after hook to `InvokeInRuntime`;
  - (c) reimplement argument binding in rag-workbench.
- **Decision:** (a).
- **Rationale:** It is the smallest public API, the CLI path stays as is, and there is no duplicate binding logic to drift.
- **Consequences:** A go-go-goja release and a dependency bump (Phase 0). If that is blocked, (c) is a mechanical fallback of about 120 lines.

### D5: one `onEvent` stream from the engine; deltas for text
- **Context:** Hooks today are `onNode` (finished nodes), `onText` (cumulative), `onProgress` (builds).
- **Decision:**
  - a single `opts.onEvent`, with adapters for the old hooks;
  - text as deltas, computed in one place from the cumulative `ctx.emit`;
  - new `ctx.partial` and `ctx.progress`.
- **Rationale:**
  - One place to add event types.
  - Deltas are lossless over an ordered stream.
  - Components don't change for text.
- **Consequences:** Workbench consumers of `onNode` keep working through the adapter.

### D6: ids and scores by default, chunks opt-in
- **Decision:** `hits` events carry `{id, score, rank, via, reason, doc, heading}`, capped by `top` (default 20); `chunks=1` adds chunk objects to events and a `chunks` map to the result.
- **Rationale:** This keeps streams small (≈3 KB per hits event on the vault), as the user confirmed.

### D7: cancellation reaches the model call
- **Decision:** Every request has an abort signal. The LLM bridge uses a per-call context cancelled by that signal; `Session.run` stops between nodes.
- **Rationale:** Without it, a closed tab keeps spending model calls.
- **Consequences:** A change in `pkg/ragjs/sample.go`, tested with the fake backend: a slow fake plus a cancel.

## 8. Key flows

### 8.1 JSON call

```
client                 server.handleVerb          ServedApp.Invoke (owner)        rag.js
  │ POST /verbs/search    │                             │                           │
  │ {"q":"goja","k":8}    │ schema→values (400 on bad)  │                           │
  │──────────────────────►│ sink := collect-only        │                           │
  │                       │────────────────────────────►│ withScope(scope, verbFn)  │
  │                       │                             │──────────────────────────►│ rag.run captures scope
  │                       │                             │                           │ Session.run (events→runs[])
  │                       │                             │◄──────── result ──────────│
  │◄── 200 {verb,params,result,runs,ms} ────────────────│                           │
```

In JSON mode the sink only keeps what the envelope needs: `runs` and, with `chunks=1`, the chunk ids.

### 8.2 SSE call with an LLM answer

```
event: start    {verb:"reports answer", params:{q:"why the rebalance law"}}
event: run      {run:1, plan:"answer-first", trigger:"submit"}
event: node     {run:1, node:"retrieve-text", phase:"start"}
event: node     {run:1, node:"retrieve-text", phase:"end", status:"ran", ms:4.1, in:1, out:20}
event: hits     {run:1, node:"retrieve-text", hits:[{id:"…#5-9-the-rebalance-law", score:2.93, rank:1, doc:"…", heading:"5.9 The rebalance law"}, …], total:20}
event: node     {run:1, node:"rerank", phase:"start"}
event: hits     {run:1, node:"rerank", hits:[…8], total:8}
event: context  {run:1, node:"pack", evidence:[{label:"E1", id:"…"}, …], tokens:1840}
event: node     {run:1, node:"answer", phase:"start"}
event: text     {run:1, node:"answer", delta:"The rebalance law says"}
event: text     {run:1, node:"answer", delta:" placements are preserved [E1]…"}
event: node     {run:1, node:"answer", phase:"end", status:"ran", ms:3120, calls:1}
event: runEnd   {run:1, ms:3190, calls:2, errors:[]}
event: result   {verb:"reports answer", result:{text:"…", cited:["E1","E3"]}, runs:[…], ms:3195}
```

### 8.3 Client disconnects mid-answer

`r.Context()` is cancelled. `Invoke` aborts the scope signal, and the sample bridge cancels the model request. `Session.run` records `answer` as `cancelled` and stops. The SSE writer exits without a `result`, since the client is gone, and serve logs `search … cancelled after 1.2 s, 1 call`.

### 8.4 A verb whose plan is not built yet

The first event after `run` is a series of `build` events, delivered as the build makes progress: from the store (fast), incremental, or full. Then the run proceeds. A second request for the same plan arriving meanwhile awaits the in-flight build (§6.8).

## 9. API reference

### 9.1 HTTP

| Method and path | Request | Response |
|---|---|---|
| `GET /api/rag/{app}/verbs` | — | `[{path, short, long, tags, fields:[{name, type, default, choices, required, argument, help, section}]}]` |
| `POST /api/rag/{app}/verbs/{verb...}` | JSON params; query `stream=sse`, `chunks=1`, `events=…`, `top=N` | JSON envelope (§6.2) or `text/event-stream` (§6.4) |
| `GET /api/rag/{app}/verbs/{verb...}?…` | params as query strings | same |
| `POST /api/rag/{app}/run` | `{signals, trigger}`; `Accept: text/event-stream` for SSE | shell payload, JSON or SSE (§6.9) |

Error body: `{ "error": { "code": "bad_params" | "unknown_verb" | "verb_failed" | "busy" | "slow_client", "message": "…", "fields"?: {…} } }`.

### 9.2 JS (rag.js and node ctx)

| API | Meaning |
|---|---|
| `rag.scope()` | the request scope of the current synchronous call, or `null` (CLI, workbench) |
| `rag.withScope(scope, fn)` | run `fn` with `scope` current; for the host |
| `rag.run(target, signals, { scope, onEvent, signal, detail })` | explicit scope or callbacks; the implicit scope is used when neither is given |
| `scope.emit(event)` | a verb's own event (`{type, …}`); no-op after the request ended |
| `scope.signal` | `{aborted, addEventListener('abort', f)}` |
| `scope.detail` | `{chunks: bool, top: number, events: Set}` |
| `Session.run(signals, { onEvent })` / `Engine.build(ir, { onEvent })` | the engine event stream (§6.4) |
| `ctx.emit(cumulativeText)` | unchanged; becomes `text` deltas |
| `ctx.partial(hits)` | intermediate hits of a running node |
| `ctx.progress(done, total, unit)` | progress of a running node |
| `Engine.gc(irs)` | gc over several IRs (was `gc(ir)`) |

### 9.3 Go

| Package | API |
|---|---|
| `go-go-goja/pkg/jsverbs` | `(*Registry).Arguments(verb *VerbSpec, vals *values.Values) ([]any, error)` (new, Phase 0) |
| `pkg/runner` | `type Event`, `type EventSink`, `NewChanSink(n int)`; `(*ServedApp).Verbs() []*jsverbs.VerbSpec`; `(*ServedApp).Invoke(ctx, verb, vals, sink, detail) (any, error)`; `(*ServedApp).Run(ctx, signals, trigger, sink)` (sink replaces `onText`) |
| `pkg/runner` | `ParseVerbParams(desc *cmds.CommandDescription, params map[string]any) (*values.Values, map[string]string, error)` |
| `pkg/server` | `handleVerbs`, `handleVerb`, `sseWriter{w, flusher, seq, t0}` with `Write(Event)`, `Ping()`, `Close(result)` |
| `pkg/ragjs` | sample bridge: per-call context tied to `o.signal` |
| `cmd/ragwb/cmds` | `serve --max-inflight N` |

## 10. Implementation phases

1. **Phase 0, upstream:** add `(*Registry).Arguments` to go-go-goja `pkg/jsverbs`, with a test. Release it, bump `go.mod`, and keep the CLI on `InvokeInRuntime`.
2. **Phase 1, engine events (rag.js):**
   - `opts.onEvent` in `Session.run` and `Engine.build`, with adapters for `onNode`/`onText`;
   - `node` start/end, `hits`, `context`, `run`/`runEnd`, `build`;
   - text deltas;
   - `ctx.partial`, `ctx.progress`, `note`;
   - tool wrapping in `ctx.sample`;
   - `rag.scope`/`rag.withScope`/scope capture in `rag.run`/`rag.build`/`engine.run`.

   Tests in `pkg/ragjs`: the event sequence for a fake-LLM plan, deltas that concatenate to the final text, scope isolation across two interleaved runs.
3. **Phase 2, host (pkg/runner):**
   - verb scan in `OpenApp` with the source loader;
   - `Event`/`EventSink`/chan sink with text coalescing;
   - the scope object (`emit`, `signal`, `detail`);
   - `Invoke`;
   - `ParseVerbParams`;
   - in-flight build sharing;
   - the `recentIrs` LRU and `gc(irs)`.

   Tests: invoke parc-style verbs in a served runtime; two concurrent invokes with a slow fake LLM receive only their own events; bad parameters.
4. **Phase 3, HTTP (pkg/server):**
   - `GET /verbs`;
   - `POST`/`GET /verbs/{verb...}` in JSON and SSE;
   - the SSE writer (framing, heartbeat, flush, slow client);
   - `--max-inflight`;
   - one log line per verb request.

   Tests: `httptest` SSE parsing, the event order and a final `result`, `chunks=1`, `events=` filtering, 400/404/503.
5. **Phase 4, shell to SSE:**
   - `/run` emits SSE on `Accept`;
   - `shell.js` SSE parser with an `onText` accumulator and an optional `onEvent`;
   - remove NDJSON.

   Tests: the server test for `/run` SSE; a manual check in the browser (Playwright).
6. **Phase 5, cancellation and polish:**
   - per-call sample context tied to the signal, tested with a slow fake plus a disconnect;
   - verbs in `examples/apps/parc/app.js` (`search`, `answer`) and `examples/apps/reports`;
   - a curl/`EventSource` example in the docs;
   - vault measurements: event overhead per query, time to the first `hits` event.

## 11. Test strategy

- **Event contract tests** (JS, through `pkg/ragjs` Go tests). For a fixed plan with the fake LLM:
  - the exact sequence of event types;
  - every `node` start has an end;
  - every `hits` event matches `trace[i].top`;
  - concatenated `text` deltas equal `answer.text`;
  - `runEnd.errors` equals `result.errors`.
- **Isolation test:** two `Invoke`s on one runtime with a fake LLM that yields between deltas. Each sink receives only its own `run` events, and each scope's `emit` after completion is a no-op.
- **Parameter tests:** every glazed field type (`int`, `bool`, `choice`, `stringList`, sections, arguments); JSON and query-string forms; unknown and invalid keys.
- **SSE writer tests:** framing of single-line JSON, heartbeat timing (injected clock), coalescing deltas under a full channel, the slow-client cutoff, exactly one terminal event.
- **Cancellation test:** a fake backend that blocks until its context is done; close the client connection; assert the backend saw cancellation and serve logged `cancelled`.
- **Regression:** existing `serve_test.go` and the shell's `/run` JSON path stay green; `TestServeRefreshIsIncremental` with verbs running a second plan, so gc keeps it.

## 12. Risks, alternatives and open questions

- **The capture rule.** A verb author who awaits before `rag.run` silently gets no events; results are unaffected. Mitigation: a `start` event is always sent, and serve logs once per verb when a request ends with zero `run` events (`verb … emitted no run events: call rag.run before the first await or pass { scope: rag.scope() }`).
- **Verb results that aren't JSON-friendly** (Go handles, cyclic objects): `exportValue` fails and the request returns `verb_failed` with the reason. Verbs return rows or plain objects in practice.
- **Event volume on large plans:** about 2 events per node plus hits. On the vault's lexical plan that is about 12 events, well under 1 ms of overhead. Measure in Phase 5.
- **Shared kept session in the shell:** the shell's `/run` uses one kept `Session` for all browser tabs, which is unchanged by this ticket. Interleaving two tabs' runs on it has always been possible. A per-tab session is a follow-up.
- **Open:** should a `GET /verbs/...` with `Accept: text/event-stream` be usable directly by `EventSource` from other origins? Not without CORS, which is out of scope (loopback, same origin).
- **Open:** should `index`-type verbs (builds) stream per-node build progress as their main output? They would naturally, through `build` events, if they call `rag.build` in scope.

## 13. File reference

| File | Role in this ticket |
|---|---|
| `pkg/server/rag.go` | routes; `handleRun` (NDJSON today, lines 195–262) → SSE; new `handleVerbs`/`handleVerb`; SSE writer |
| `pkg/runner/serve.go` | `serveHarness` (`__serve`, line 94), `OpenApp` (line 141), `ServedApp.Run` (line 218); gains verb scanning, `Invoke`, sinks |
| `pkg/runner/runtime.go` | `Config.SourceLoader` (jsverbs overlay) for served runtimes |
| `pkg/ragjs/assets/rag.js` | `Session.run` (line 1707), `ctx` (line ~1800), `Engine.build` (line 1387), `Engine.gc` (line 1525), `sampler` (line 1376), `rag.run` (line 2228), components `answer`/`agentic`/`rerank.llm` (lines 662–812) |
| `pkg/ragjs/sample.go` | LLM bridge: `onText` (line 40), tool exec, `services.Lifetime()` call context (line 136) |
| `pkg/ragjs/await.go` | `Eval`/`awaitPromise`: how the host calls into the runtime and waits |
| `pkg/natives/vector.go` | `rerank.model` (line 65): progress events |
| `cmd/ragwb/cmds/verbs.go` | CLI verb scanning and invocation (the reference for serve) |
| `cmd/ragwb/cmds/serve.go` | serve flags (`--watch`, new `--max-inflight`), app opening |
| `pkg/store/check.go` | `VerbScanOptions` (skips `*.wb.js`) |
| `web/app-shell/shell.js` | `run()` NDJSON reader (lines 73–107) → SSE parser |
| `examples/apps/parc/parc.js`, `examples/apps/reports/search.js` | verbs today; `app.js` gains query verbs |
| `~/go/pkg/mod/github.com/go-go-golems/go-go-goja@v0.10.7/pkg/jsverbs/{model,runtime,binding,command}.go` | `VerbSpec`/`FieldSpec`, `InvokeInRuntime`, `buildArguments`, `CommandDescriptionForVerb` |
| `~/go/pkg/mod/github.com/go-go-golems/glazed@v1.3.5/pkg/cmds/sources/update.go` | `FromDefaults`, `FromMap`: parsing parameters into `values.Values` |

## 14. References

- WHATWG HTML, *Server-sent events* (event stream format, `EventSource`).
- RAGWB-002 guide (disk-backed retrieval, serve warm path) and RAGWB-003 guide (delta builds, `Engine.gc`, `serve --watch`).
- go-go-goja `pkg/jsverbs` (v0.10.7) and glazed `pkg/cmds/sources` (v1.3.5) sources, read for this design.
