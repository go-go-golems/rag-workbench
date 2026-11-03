---
Title: 'Intern guide: RAG workbench on Go — architecture, APIs and implementation plan'
Ticket: RAGWB-001
Status: active
Topics:
    - rag
    - goja
    - backend
    - ui-dsl
DocType: design-doc
Intent: long-term
Owners: []
RelatedFiles:
    - Path: abs:///home/manuel/code/wesen/go-go-golems/go-go-goja/pkg/jsverbs/runtime.go
      Note: verb invocation + promise waiting
    - Path: abs:///home/manuel/code/wesen/go-go-golems/rag-evaluation-system/pkg/gojamodules/rag/module.go
      Note: competing rag v2 DSL and module-name collision (D8)
    - Path: abs:///home/manuel/code/wesen/go-go-golems/ragkit/rag/indexbundle/types.go
      Note: bundle build/open contracts for persistence (A5)
    - Path: abs:///home/manuel/code/wesen/go-go-golems/upwork/cmd/tracker/main.go
      Note: xgoja runtime-package + hand-written main pattern (D5)
    - Path: abs:///home/manuel/code/wesen/slop-computer/pkg/server/sample.go
      Note: sample SSE + page-tool protocol to port (D4)
    - Path: repo://ttmp/2026/11/02/RAGWB-001--import-rag-workbench-prototype-and-design-go-backed-script-runner/scripts/03-jsverbs-split/app/search.js
      Note: Working split-file jsverb example the guide's file conventions build on
    - Path: repo://ttmp/2026/11/02/RAGWB-001--import-rag-workbench-prototype-and-design-go-backed-script-runner/sources/prototype/rag.js
      Note: Engine, registry (define), index contract, host env — the code the guide explains
ExternalSources: []
Summary: 'Intern-facing guide to turning the claude.ai RAG workbench prototype into a Go-backed system: the JS pipeline grammar running in goja, Go-native indexing via ragkit/goja-bleve/goja-text, jsverbs CLI files, a workbench server with a window.claude shim, saved scripts, and ad-hoc search apps.'
LastUpdated: 0001-01-01T00:00:00Z
WhatFor: Onboarding an engineer to every part of the planned rag-workbench system and giving them a phased, testable implementation plan
WhenToUse: Read before working on any rag-workbench code; use the API and file references while implementing
---

# Intern guide: RAG workbench on Go — architecture, APIs and implementation plan

> **Who this is for.** You are new to this codebase and maybe new to retrieval-augmented generation. After reading this guide you should be able to say what the system is, what each part does, where the code that each part reuses lives, and what to build first. Read sections 1–4 in order. Sections 5–9 are reference material you will come back to while implementing.

## 1. Executive summary

We are building a toolkit for making **many small search-and-answer applications** over personal technical material: software documentation, an Obsidian vault (`~/code/wesen/go-go-golems/go-go-parc`, about 2,300 notes), project reports and notes. Each application is written in **JavaScript** with a small, composable grammar:

- a **corpus** says how to turn files into searchable pieces (read → parse → chunk → index);
- a **plan** says how to answer a query (retrieve → fuse → rerank → limit → answer);
- **evals** are golden questions that score a plan;
- an **app** is a user interface written in a UI grammar (`gui.app(...)`).

That grammar already exists. It was prototyped as a claude.ai artifact called the **RAG workbench**: a browser page with a script editor, 29 example scripts ("presets"), an inspector and an LLM agent that edits pipelines. The prototype runs entirely in the browser and depends on the claude.ai sandbox for LLM calls and storage. **This project gives it a Go backend.** The backend serves the workbench, saves scripts, runs pipelines server-side over real corpora with Go-native indexing, and lets any script run on its own: as a command-line verb, or as a small web app.

Our spikes already show three things:

1. The prototype's engine (`rag.js`) runs **unmodified** inside goja, the Go JavaScript engine. There is one exception: goja does not support Unicode property escapes in regular expressions (`\p{L}`), and the failure is silent. With a one-line rewrite it reproduces the browser's scores exactly (score 12.676 for the textbook's worked example).
2. Pipeline, plans, query and evals can live in **separate files**. They share values through `require()`, and the existing **jsverbs** runner turns each query/eval function into a CLI command that emits tables, JSON or CSV.
3. Indexing in pure JS costs about 5 s/MB (parse + chunk + BM25; corrected 2026-11-02, see diary step 7). That is fine for a demo, and too slow and memory-hungry for an 81 MB vault. The heavy nodes must become **Go-native components**, which the grammar can host without changing its shape.

The rest of this guide explains the pieces, how they connect, and a phased plan to build them.

## 2. Problem statement and scope

### 2.1 What exists

- A working **browser-only prototype** (imported into this ticket at `sources/prototype/`), with a 10-part textbook explaining it (`sources/textbook/`).
- A mature **Go ecosystem** in neighbouring repositories: a JS runtime with modules and CLI generation (go-go-goja, xgoja, jsverbs), text and search modules (goja-text, goja-bleve), a RAG library (ragkit), an LLM library (geppetto), a server-side clone of the claude.ai page runtime (slop-computer), and a JSON→React widget renderer (rag-evaluation-system's `widget.dsl`).

### 2.2 What we need

1. **Workbench server.** Serve the ported workbench UI from Go. Provide the page APIs it expects (LLM calls with tools, per-user storage), so the in-page agent can write and **save** scripts.
2. **Headless runner.** Run saved scripts outside the browser to index real corpora (a whole vault, a docs tree) using Go natives.
3. **Ad-hoc apps.** Serve any script's `gui.app(...)` as a standalone page whose queries run on the backend.
4. **Files as commands.** Any file that exports a query, an eval or an indexing job can be run as a CLI verb.

### 2.3 Non-goals (for now)

- Multi-user hosting, authentication, quotas. This is a local, single-user tool. Path allowlisting is still required (section 6.10).
- A new UI framework. We reuse the prototype's `gui` runtime and, where it fits, `widget.dsl`.
- Rewriting the engine in Go (see Decision D1).

## 3. Orientation: concepts and vocabulary

Read this section carefully. Every later section uses these words.

### 3.1 Retrieval-augmented generation in one paragraph

A RAG system answers questions about a body of text by **retrieving** relevant passages first and then (optionally) asking an LLM to **generate** an answer grounded in them. Retrieval needs an **index**. A **lexical** index (BM25) matches words; a **vector** index matches meaning via **embeddings** (number arrays produced by an embedding model). Documents are too long to retrieve whole, so they are split into **chunks** (for markdown, usually one heading section each). Several retrievers can be combined by **fusion** (e.g. reciprocal rank fusion, RRF). Their results can be **reranked** (by a lambda or an LLM) and **packed** into a context window for the answer. Quality is measured with **golden questions** and metrics like hit@5 (is the right chunk in the top 5?), MRR (mean reciprocal rank) and nDCG@10.

### 3.2 The workbench grammar

| Term | Meaning | Where in code |
|---|---|---|
| **Component** | A typed processing step such as `parse.markdown`, `index.bm25`, `retrieve`, `fuse.rrf`, `rerank.llm`. Declared in a registry with a phase, input/output ports and a run function. | `sources/prototype/rag.js:474-800` (`build(...)`, `live(...)`) |
| **Phase** | `build` components turn files into indexes, once per corpus change. `live` components run per query. | `REG` entries, `phase:` |
| **Ports** | Typed edges: `files docs chunks index value` (build) and `queries hits context answer` (live). | `rag.js:476` comment |
| **Corpus** | A chain of build components: `rag.corpus(id).from(...).parse(...).chunk(...).index(name, ...)`. | `CorpusSpec`, `rag.js:801+` |
| **Plan** | A chain of live components over a corpus, with **signals** (inputs like `q`, `k`, `filters`). | `makePlan`, `rag.js:801+` |
| **IR** | The JSON graph (DAG) that builders compile to: nodes with ids, types, params, edges. Everything (engine, inspector, agent, ops) works on the IR. | `compile`, `rag.js:970-1112` |
| **Lambda** | A user function in a slot (chunker, reranker, `map`). Stored **as source text** and run **closed**: it cannot see outer variables, only its arguments and an allowlisted global set. | `lam`, `guard`, `compileSource`, `rag.js:80-128` |
| **Digest / memo** | Each build node's output is cached under a hash of its inputs and params, so editing a live node never rebuilds indexes. | `Engine.digests/build`, `rag.js:1228-1446` |
| **Trigger** | When a live node runs: `change` (each keystroke), `submit` (Enter), or explicit. LLM nodes default to `submit`. | `triggerOf` |
| **Session / run** | Executing a plan's live nodes for a signal set, with per-node caching and a trace. Result exposes `top(n)`, `explain(id)`, `toJSON()`. | `Session.run`, `rag.js:1470-1622` |
| **Workspace / ops** | An editable IR with undo, branches and merge. Edits are **ops** (`set insert remove replace wire signal trigger …`). The agent and the inspector use the same ops. | `Workspace`, `rag.js:1782-1915`; `applyOps`, `rag.js:1113-1226` |
| **Evals** | `rag.evals(id).case(q, {doc, section}).metrics([...])`. `.run(ir, {engine})` returns per-case ranks and a summary. | `Evals`, `rag.js:1623-1753` |
| **gui app** | `gui.app(id).title(t).use(plan).state({...}).derive(...).view(v => ...)` compiles to a JSON node tree. A browser runtime renders it and re-renders only the components whose state keys changed. | `sources/prototype/gui.js` |
| **Host** | The object the environment supplies: `files()`, `sample` (LLM), `log`, and for the gui runtime a `host.rag` facade over the current pipeline. | `rag.configure(env)`, `app.js:350 guiHost` |

### 3.3 The Go-side vocabulary

| Term | Meaning |
|---|---|
| **goja** | A pure-Go ECMAScript 5.1+ engine (with most of ES2015+). There is one `*goja.Runtime` per JS VM, and it is **not goroutine-safe**. |
| **go-go-goja** | Our layer over goja: a runtime factory, CommonJS `require` with native modules (`fs`, `db`, `path`, `yaml`, `express`, …), an event loop, and `runtimeowner` for safe cross-goroutine calls. |
| **Native module** | Go code exposed to JS through `require("name")`. |
| **xgoja** | A generator. A YAML plan (`xgoja.yaml`) lists **providers** (Go packages that contribute modules), JS sources and assets, and xgoja generates a CLI binary or an importable **runtime package**. |
| **Provider** | A Go package with `Register(*providerapi.ProviderRegistry)` that contributes named modules (and optionally commands/help) to xgoja. |
| **jsverbs** | Scans a directory of JS files. Each top-level function becomes a Glazed CLI command, optionally described by `__verb__`/`__section__`/`__package__` metadata. Return values become rows (table/JSON/CSV/YAML). |
| **Glazed** | Our CLI framework for structured output. |
| **ragkit** | A Go RAG library with typed contracts: exact-slice chunks, representations, embedders, BM25/Bleve/SQLite-vector indexes, RRF, immutable index **bundles**, evaluation. |
| **Representation** | ragkit term: searchable text *derived from* a chunk (`raw`, `summary`, `questions`, breadcrumbs…). A search hits representations, and hits are **collapsed** back to chunks. |
| **Bundle** | ragkit's immutable, content-addressed on-disk index set (Bleve + SQLite vectors + SQLite content + manifest). |
| **geppetto** | Our LLM library: engines (Claude, OpenAI, Gemini…), profiles, tool loops, embeddings, and a `require("geppetto")` JS module. |
| **`window.claude` / sample** | The claude.ai artifact runtime API: `await window.claude.use('sample')` gives `sample(turns, opts)`, an LLM call whose **tools execute inside the page**. slop-computer reimplements it on a Go server. |
| **widget.dsl** | A JS module (from `rag-evaluation-system`) that builds typed widget-page JSON on the server. An embedded React app renders it, and actions POST back to server routes. |
| **rag v2 DSL** | rag-evaluation-system's own `require("rag")`: authoring-only, Go-backed builders that compile to `rag-pipeline-ir/v2`. Not the same as the workbench grammar (D8). |

## 4. The system at a glance

### 4.1 A representative example

Here is the kind of application this system exists for: a browser for the Obsidian vault. It is four small files in one directory:

```text
apps/parc/
  lib/corpus.js   module.exports = rag.corpus('parc').from(rag.source.dir({root, include, exclude}))
                                    .parse(rag.markdown()).chunk(rag.sections({maxTokens: 450}))
                                    .index('text', rag.bm25()).index('meta', rag.terms({keys: ['type','tags']}))
  lib/plans.js    module.exports = { lexical: rag.plan('lexical').over(corpus).retrieve('text') ...,
                                     hybrid:  ... .retrieve('text').retrieve('vec').fuse.rrf().limit() }
  index.js        function index(force)   → builds and persists the corpus; rows = per-node stats
  search.js       function search(q, k, plan) → rows = ranked hits
  evals.js        function goldens()       → rows = metrics per plan
  app.js          module.exports = gui.app('parc').use(plans.hybrid).view(...); function serve(port)
```

and it is used three ways:

```bash
ragwb verbs --dir apps/parc parc index                       # headless build (Go natives do the heavy work)
ragwb verbs --dir apps/parc parc search "rebalance law" --plan hybrid --output json
ragwb serve apps/parc                                        # ad-hoc app at http://localhost:8790
ragwb workbench                                              # the editor + agent, editing the same files
```

### 4.2 Architecture diagram

```text
             ┌──────────────────────────── Browser ─────────────────────────────┐
             │  Workbench page (ported index.html/app.js/agent.js)              │
             │   ┌─ editor ── presets ── files ─┐  ┌─ inspector (DAG/trace/  ┐  │
             │   │  edits apps/<name>/*.js      │  │  evals/IR/history)      │  │
             │   └──────────────────────────────┘  └─────────────────────────┘  │
             │   agent.js ── sample(turns,{tools:[eval]}) ── page executes eval │
             │   gui runtime (gui.js) ── host.rag = RemoteRag ──────┐           │
             │   window.claude shim: sample · db · user · downloads │           │
             └───────┬───────────────────────┬──────────────────────┼───────────┘
          SSE + tool callbacks         /api/apps/*          /api/rag/:app/{run,chunk,doc,...}
             ┌───────▼───────────────────────▼──────────────────────▼───────────┐
             │ ragwb (Go, net/http ServeMux, go:embed assets)                   │
             │  ┌────────────┐ ┌──────────────┐ ┌──────────────────────────────┐ │
             │  │ llm bridge │ │ app store    │ │ runner (goja runtime pool)   │ │
             │  │ geppetto   │ │ apps dir on  │ │  require("ragwb")  = rag.js    │ │
             │  │ profiles,  │ │ disk + sqlite│ │  require("ragkit") natives   │ │
             │  │ tool loop  │ │ history/conv │ │  goja-text · goja-bleve · db │ │
             │  └────────────┘ └──────────────┘ │  ctx.sample → llm bridge     │ │
             │                                  └──────────────┬───────────────┘ │
             │                         artifacts: ~/.ragwb/store/<corpus>/<digest>│
             └──────────────────────────────────────────────────┼─────────────────┘
                                                                ▼
        CLI: ragwb verbs --dir apps/parc …   (same runner, no server; jsverbs + Glazed)
```

There are three **surfaces** (CLI verbs, ad-hoc app server, workbench server) over one **runner**. The runner is a goja runtime with `rag.js` loaded as a module and Go-native components registered into its component registry.

### 4.3 Who owns what

| Concern | Owner | Language |
|---|---|---|
| Grammar, IR, ops, engine orchestration, evals, lambdas | `rag.js` (vendored from the prototype, then evolved) | JS |
| UI grammar + reactive renderer | `gui.js` (prototype) | JS (browser) |
| File walking, markdown parsing, chunking at scale, BM25/Bleve, vectors, embeddings, bundles | Go natives exposed as `require("ragkit")` and registered as components | Go |
| LLM calls (scripts and page) | llm bridge on geppetto | Go |
| Script persistence, HTTP APIs, SSE | ragwb server | Go |
| CLI command discovery and output | jsverbs + Glazed | Go |

## 5. Current state: the prototype in detail

### 5.1 File inventory

All paths are relative to `ttmp/2026/11/02/RAGWB-001--…/sources/prototype/`.

| File | Lines | Role | Needs DOM? |
|---|---|---|---|
| `rag.js` | 2036 | Grammar, compiler, engine, ops, evals, sweeps, export. UMD: `module.exports` under CommonJS, `window.rag` in a browser. | No |
| `gui.js` | 721 | UI grammar `gui.app()`, about 45 components, reactive runtime. Spec building is DOM-free; rendering uses `document`. | Rendering only |
| `agent.js` | 548 | Chat agent: one `eval` tool, run modes (auto/ask/read-only), undo per step, rich chat parts. | Yes |
| `app.js` | 1044 | Page shell: editor, presets, files tab, inspector, wiring, host capabilities. | Yes |
| `kit.js` | 53 | Shared script helpers: standard corpus, 12 goldens, standard search app. | No |
| `pipeline.js`, `presets/*.js` (29), `presets/index.json` | — | Example scripts. | No |
| `corpus.json` | 545 KB | The 11 bundled reports. | — |
| `index.html` | 590 | Markup and styles ("Lattice" design system). | — |

### 5.2 How a script runs in the prototype

A script is the **body of an async function** with four names in scope (`app.js:175`):

```js
const fn = new Function('rag', 'gui', 'log', 'kit',
  `"use strict";return (async () => {\n${text}\n})();\n//# sourceURL=pipeline.js`);
const { plan, evals, app, variants } = await fn(rag, gui, log, kit);
```

The workbench then compiles `plan` to IR and builds it (deferring LLM-costing build nodes until the user presses **Build**). It runs the live nodes for the current signals, mounts `app` with the gui runtime, and runs `evals` in the Evals tab.

A minimal script (`presets/minimal.js`):

```js
const corpus = rag.corpus('reports')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());

const plan = rag.plan('minimal').over(corpus)
  .signals({ q: 'rebalance law', k: 10 })
  .retrieve('text', { k: 20 });

const app = gui.app('minimal').title('Minimal search').use(plan)
  .state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c.searchBox('q', { placeholder: 'Search the reports…' }).status().results(), { grow: 3 })
    .col(c => c.chunk(s => s.selected), { grow: 2 })));

const ops = []; // end ops — the workbench writes accumulated edits here ("Keep in script")
return { plan: plan.patch(ops), evals: kit.goldens(), app };
```

### 5.3 The component catalog

Build: `source.files source.inline parse.markdown chunk.sections chunk enrich enrich.llm index.bm25 index.terms synonyms bake`.
Live: `retrieve rewrite rewrite.llm fuse.rrf fuse.max filter filter.where rerank rerank.llm limit expand.neighbors expand.links pack answer agentic map`.

The fluent API is **generated from the registry** (`refreshFactories`, `rag.js:2005`). `rag.define('boost.recent', {...})` adds `plan.boost.recent()` everywhere, even to plans created earlier. That registry is the main extension point for Go natives (section 6.3).

Component definition contract (`define`, `rag.js:1917`):

```js
rag.define('family.variant', {
  phase: 'build' | 'live',
  in:  ['docs'] | ['chunks'] | ['queries'] | ['hits'] | ...,   // input ports
  out: 'chunks' | 'index' | 'value' | 'hits' | 'context' | 'answer',
  args: (...callArgs) => params,                // how builder arguments map to node params
  // build: exactly one of
  map: (item, ctx, p) => out | Promise<out>,     // per input item (memoized per item digest)
  all: (inputs, ctx, p) => out,                  // over all inputs at once
  // live:
  run: (s, ctx, input, p) => out | Promise<out>, // s = signals
  trigger: 'change' | 'submit', llm: true|false, describe: p => 'label'
});
```

**Index contract.** What `retrieve` and the gui expect from an index object (`Bm25Index`, `rag.js:361-433`):

```ts
interface RagIndex {
  kind: string;                                        // 'bm25' | 'terms' | ...
  search(q: string, k: number, opts?: { synonyms?: Record<string,string[]>, only?: Set<number> }):
    Array<{ id: string; score: number; why?: Record<string, number> }>;
  searchIn(docId: string, q: string, k?: number): Array<{ id, score, why? }>;
  toJSON(): object;                                    // shown in the inspector
}
```

**Build context** (`Engine.buildCtx`, `rag.js:1375`): `{node, sample, tokens, slug, hash, clip, log, files(), docs, chunks}`.
**Live context** (`Session.ctx`, `rag.js:1570`): `{node, sample, signal, signals, synonyms, index(name), indexNames(), firstIndex(kind), chunk(id), chunks(pred), doc(id), docs(), links(docId), neighbors(id,{before,after}), log, note, emit}`.

### 5.4 The host contract (everything the browser environment must provide)

| Host API | Used at | Shape |
|---|---|---|
| `window.claude.use('sample')` | `app.js:968`; `agent.js:422`; `rag.js:516,574,641,726,781,1742` | `sample(turns, {tools?, modelTier?, onText?, signal?, cache?}) → {text, truncated?}`, `sample.json(prompt, opts) → object`, `sample.limits() → {tools, ...}`. Each tool is `{name, description, inputSchema, execute(input, ctx)}` and **runs in the page**. |
| `use('db')` | `app.js:52` | JSON document tree. The workbench stores uploads, preset edits and conversations under `data/users/<uid>/ragwb`. |
| `use('user')` | `app.js:975` | `user.id()` |
| `use('downloads')` | export | save a file |
| static fetches | `app.js:258,988,992` | `corpus.json`, `presets/index.json`, `presets/<file>`, `rag.js` |

### 5.5 The seam between UI and pipeline

The gui runtime never touches the engine directly. It calls `host.rag` (`app.js:350-374`):

```js
host.rag = {
  session: () => ws.session(),     // .run(signals, {trigger}) → result
  ir:      () => ws.ir,
  chunk:   id => ..., doc: id => ...,
  chunks:  pred => [...],          // pred is a lambda
  docs:    () => [...],
  neighbors: id => ({ prev, next }),
}
```

This is the most important fact for the ad-hoc app design. If something implements this facade over HTTP (a **RemoteRag**), an unchanged `gui.js` can drive a plan that runs in Go.

### 5.6 Known prototype bugs (inherit consciously)

These were found while the textbook was written (`sources/conversation.md`, the Part IV–X notes):

- Doc cards preset searches a stale index after deferred LLM summaries are built.
- Undo history loses function-valued lambdas inside `insert`/`replace` ops. Source-string lambdas work.
- Reports preset weight `failures` never applies (the actual id is `failures_2`). Unknown weights are silently ignored.
- Ranking playground `k` slider has no visible effect. History shows `insert [object Object]`.
- The dry-run cost estimate counts every chunk. The agentic trace counts one call per run.
- `expect` conflict checks never fire. Read-only agent mode isn't sealed.
- Size limits count characters where the platform counts bytes.

## 6. Current state: the Go building blocks

For each block: what it is, the API we use, and file references. Paths are under `~/code/wesen/go-go-golems/` unless absolute.

### 6.1 go-go-goja: runtime, modules, owner

- **Runtime factory**: `engine.NewRuntimeFactoryBuilder(opts...).WithModules(...).UseModuleMiddleware(...).WithRuntimeInitializers(...).Build()` and then `factory.NewRuntime(ctx, ...)` (`go-go-goja/pkg/engine/factory.go:49-184`). A runtime is `{VM, Require, Loop, Owner}` (`pkg/engine/runtime.go:33`).
- **Owner**: all VM access from other goroutines (HTTP handlers!) must go through `rt.Owner.Call(ctx, name, func(ctx, vm) (any, error))` or `Post` (`pkg/runtimeowner/types.go:21`). goja is single-threaded.
- **Modules** (`go-go-goja/modules/`): `fs` (Promise + `*Sync`, optional `Root` confinement: `modules/fs/backend.go:21`), `path`, `yaml`, `database` (exposed as `db`: `configure/query/exec/begin/close`, `modules/database/database.go:221`), `fetch`, `exec`, `express`, `timer`, `events`, `crypto`, `os`, `time`, and `uidsl` (static HTML builder). **There is no recursive walk/glob.**
- **Native module interface**: `modules.NativeModule` (`modules/common.go:28`), self-registration with `modules.Register` in `init()`.
- The SQLite driver is mattn/go-sqlite3 (cgo). FTS5 needs the `sqlite_fts5` build tag.

### 6.2 jsverbs: files as commands

`go-go-goja/pkg/jsverbs` (docs: `pkg/doc/08-…overview.md`, `09-…fixture-format.md`, `10/11-…`).

- `jsverbs.ScanDir(dir)` walks `.js/.cjs` files and parses them with tree-sitter. **Top-level function declarations** and arrow functions become verbs. Subdirectories become command groups.
- Metadata (static literals only, no expressions):

```js
__package__({ name: "parc", short: "Obsidian vault search" });
__section__("filters", { fields: { type: { type: "choice", choices: ["article", "project"] } } });
async function search(q, k, plan, filters) { return rows; }
__verb__("search", {
  short: "Run a plan for one query",
  sections: ["filters"],
  fields: {
    q: { argument: true }, k: { type: "int", default: 10 },
    plan: { type: "choice", choices: ["lexical", "hybrid"], default: "lexical" },
    filters: { bind: "filters" },
  },
});
```

- Result → rows: object = 1 row, array = n rows, primitive = `value`, a Promise is awaited (`pkg/jsverbs/runtime.go:245 waitForPromise`). `output: "text"` makes a writer verb.
- `registry.Commands()` yields Glazed commands, and `cli.AddCommandsToRootCommand` mounts them (`cmd/jsverbs-example/main.go:53-120`).
- **Gotcha (observed):** inside a jsverbs app, `require("./data.json")` fails with `GoError: Invalid module`, because the source loader only serves the scanned `.js` files. Wrap data as `.js` or read it with `fs`.

### 6.3 xgoja: providers and generated runtimes

- Provider API: `registry.Package(id, providerapi.Module{Name, DefaultAs, Description, ConfigSchema, TypeScript, NewModuleFactory(ModuleSetupContext) (require.ModuleLoader, error)})` (`go-go-goja/pkg/xgoja/providerapi/provider_registry.go:31`, `module.go:44`). A minimal real provider is the upwork runtime identity: `~/code/wesen/go-go-golems/upwork/pkg/xgoja/providers/runtimeidentity/`.
- Shipped providers: `providers/core` (path, yaml, crypto, time, timer, events), `providers/host` (fs, fetch, exec, db), `providers/http` (express + `serve` command set).
- **Runtime-package pattern** (the one we copy): `xgoja generate -f xgoja.yaml --artifact runtime-package --output internal/xgojaruntime` (upwork `Makefile:47`) generates `NewBundle(opts)`, `bundle.NewRuntime(ctx)` and `bundle.AttachDefaultCommands(root)` (`upwork/internal/xgojaruntime/xgoja_runtime.gen.go:242-299`). A hand-written `main` adds Go commands next to it (`upwork/cmd/tracker/main.go:57-67`).
- upwork's `xgoja.yaml` is a full example: providers, multiple aliases of the same module (`fs:host`, `fs:assets`, `db`, `db:agent`), jsverbs sources, embedded assets, `serve` command.

### 6.4 goja-text: markdown, frontmatter, chunking

`goja-text/pkg/...`, provider `pkg/xgoja/providers/text/text.go:29`.

- `require("chunking")`: `lines`, `paragraphs`, `markdownBlocks`, `markdownSections`, `pack`, `packWeighted`, `recursive` (`pkg/chunking/module.go:70-110`). Results are **Spans** with byte, rune and line ranges (`pkg/chunking/types.go:4`). That matches ragkit's exact-slice model.
- `require("markdown")`: `parse`, `renderHTML`, `walk`, `textContent`, `validate`, `document`, `builder` (goldmark; `pkg/markdown/module.go:214-258`).
- `require("extract")`: `frontmatter` (yaml.v3), `markdownCodeBlocks`, … (`pkg/extract/module.go:38-56`).

### 6.5 goja-bleve: full-text search in JS

`goja-bleve/pkg/module.go` (`require("bleve")`, registration at :32 and `init()` :117, xgoja provider `pkg/provider.go:14`, d.ts at :41).

- Mapping: `bleve.mapping()`, `docMapping()`, `field(type)` with types text, keyword, number, datetime, boolean, geo, ip, `vector(dims)`.
- Index: `bleve.create(path)` / `open(path)` / `memory()`; `.mapping(m).build()`; `index(id, doc)`, `delete`, `docCount`, `newBatch()…execute()`, `search(req)`, `close` (`pkg/api_index.go:29-145`).
- Queries: `match, matchPhrase, term, queryString, prefix, fuzzy, regexp, wildcard, bool, conj, disj, matchAll` with `.field()`, `.boost()`.
- Search request: `size, from, fields, sort, highlight(fields, style), explain, score('rrf'|'rsf'), knn(...)` (`pkg/api_search.go:61-162`). Hits carry fragments and a score breakdown.
- **Missing:** facets. Vectors need `-tags=vectors` + FAISS.

### 6.6 ragkit: typed RAG contracts and backends

`ragkit/rag/` (README: contracts-first; no JS bindings yet).

```go
type Document struct { ID, SourceURI, Title, Text, ContentDigest string; Metadata map[string]string }
type Range    struct { ByteStart, ByteEnd int }
type Chunk    struct { ID, DocumentID string; Ordinal int; Range Range; Text, ContentDigest, Chunker string }
type Representation struct { ID, ChunkID, Kind, Text, ContentDigest, Model, PromptDigest string }
type Vector   struct { RepresentationID string; Values []float32; Model string }
```

(`rag/types.go:4-50`. `ValidateChunk` enforces `chunk.Text == doc.Text[range]`: `rag/validate.go:63`.)

- Interfaces: `Chunker`, `Generator`, `Embedder` (batch `[]string → [][]float32`), `Searcher`, `Index`, `Reranker` (`rag/components.go:9-86`).
- Chunkers: `chunking.Fixed`, `Markdown`, `MarkdownHeading{Max, Min, OverlapRunes}` (`rag/chunking/markdown_heading.go:21`); Go-source chunker `rag/gochunk`.
- Representations: raw, breadcrumbs, small-to-big, LLM summaries/questions (`rag/representations/`).
- Lexical: in-memory BM25 (`rag/lexical/bm25.go`), persistent Bleve with digest manifest (`rag/lexical/bleve/index.go`: Build:100, Open:363, Search:411).
- Vector: exact cosine, **sqliteexact** (persistent; `rag/vector/sqliteexact/index.go`), hnsw (memory).
- Retrieval: `Collapse`, `WeightedRRF(channels, cfg)`, `Hydrate` (`rag/retrieval/retrieval.go:23-152`). Rerank: `TermOverlap`, `CachedReranker`.
- **Bundles**: `indexbundle.Build(ctx, BuildInput{OutputRoot, CorpusPath, Documents, Chunks, Representations, Vectors, QueryEmbedder, Chunker, Embedding})` → `BuildResult{Manifest, Path, Reused}`; `BuildStream` for bounded batches; `Open(ctx, OpenOptions{Path, QueryEmbedder, ...})` → `Bundle{Manifest, Lexical, Vector rag.Index, Content content.Store}` (`rag/indexbundle/types.go:45-200`).
- Embeddings come through `rag/provider/geppetto.NewEmbedder` and `embedding.CachedEmbedder`.
- Production use of all of it: coinvault `internal/knowledge/service.go:89` (open bundle, lexical/hybrid, RRF :290, reranker :310) and `internal/knowledgebuild/build.go`.

### 6.7 geppetto: LLMs, embeddings, JS module, scopedjs

- Engines: `factory.NewStandardEngineFactory().CreateEngine(settings)` (`geppetto/pkg/inference/engine/factory/factory.go:95`). Tool loop: `pkg/inference/toolloop/loop.go`. Events: `pkg/events/chat-events.go` (text deltas, tool calls, results).
- Profiles: `pkg/engineprofiles` (YAML/SQLite registries, stacking). coinvault `profile-registry.yaml` is a working example.
- Embeddings: `pkg/embeddings` (`Provider{GenerateEmbedding, GenerateBatchEmbeddings, GetModel}`; OpenAI, Ollama, Cohere; LRU + disk cache).
- JS: `require("geppetto")` exports `inferenceProfiles, engine, embeddings, reranker, agent, tool, toolRegistry, schema` (`pkg/js/modules/geppetto/module.go:182-200`). Example: `gp.agent()...session().next().user(...).run()` (`examples/js/geppetto/30_real_provider_multiturn.js`). xgoja provider `pkg/js/modules/geppetto/provider/provider.go:50`.
- **scopedjs** (`pkg/inference/tools/scopedjs/`): wraps a prepared goja runtime as one LLM tool `eval_xxx` with contract `{code, input} → {result, console, error}`. It awaits promises and captures the console. This is the server-side twin of the workbench agent's `eval` tool.

### 6.8 slop-computer: `window.claude` on a Go server

`/home/manuel/code/wesen/slop-computer` runs unmodified claude.ai artifacts locally.

- Page runtime: `web/runtime/src/index.ts:13-62` (`claude.use(name)` for db, user, assets, downloads, artifact, sample, …), built to `pkg/server/assets/claude.js` and injected into each page's `<head>` (`pkg/server/sandbox.go:200-208`).
- **sample protocol** (the part we copy):
  1. The page POSTs `/_rt/sample` with `{input, modelTier, model, effort, tools:[{name, description, inputSchema}], images}` (`web/runtime/src/sample.ts:68-110`).
  2. The server streams SSE events `call` (carries the call id), `text`, `thinking`, `tool` (a tool-use request), `usage`, `result`, `error` (`pkg/server/sample.go:122-221`).
  3. On a `tool` event the page runs the tool's `execute` locally and POSTs `/_rt/sample/{call}/tools/{use}` with `{value, isError}` (`sampleTool`, `sample.go:224`). The server's tool callback is parked on a channel until then, with a timeout.
  4. `POST /_rt/sample/{call}/cancel` aborts (`sample.go:255`).
- LLM backend interface: `Backend{Models(); Sample(ctx, Request, emit func(Event), ExecuteTool); Close()}` (`pkg/llm/backend.go:114-118`). Geppetto implementation: `pkg/llm/gepp/sample.go:43-200`. Deterministic `pkg/llm/fake.go` for tests.
- Server-side actor `sample()` explicitly **does not support tools** (`pkg/actor/sample.go:103`).

### 6.9 rag-evaluation-system: widget.dsl and the React renderer

`~/code/wesen/go-go-golems/rag-evaluation-system` is a Go + React RAG evaluation toolkit. It matters to us in **two** ways: it owns `widget.dsl`, and it has **its own `require("rag")`** (see D8).

**widget.dsl (v3)** — provider `pkg/xgoja/providers/widgetsite/provider.go:13-33` (package `rag-widget-site`, module `widget.dsl` plus help source `widget-dsl`; no commands). Implementation: `pkg/widgetdsl/v3.go`, with typed spec → IR lowering in `pkg/widgetdsl/spec/`. Full API inventory: `pkg/xgoja/providers/widgetsite/doc/05-widget-dsl-v3-api-reference.md`.

- Namespaces: `page`, `act` (server, navigate, download, copy, open/close overlay), `bind` (field, path, template, …), `app.shell`, `ui` (stack, inline, splitPane, card, tabs, disclosure, form, inputs, markdownArticle, code, badge, status, emptyState, upload), `data` (fields, collection, matrix, activityFeed, selection), plus domain namespaces (`crm`, `cms`, `course`, `context`, `schedule`, `time`).
- `data.collection(...)` composes schema + search + paginate + table (row select, keyboard, commands) + masterDetail. **Pitfall:** pass `collection.toNode()`, not the builder, or it stringifies to `[object Object]` (this is even baked into a committed golden: `pkg/widgetdsl/testdata/v3/golden/42-keyboard-search-dialog.json`).
- Page JSON: `{schemaVersion, id, title, meta, shell, shortcuts, root}`. Nodes are `{kind: "component", type, props, children}` | element | text. Actions are `{kind: "server", name, payload, confirm}` | navigate.
- **Execution model: server-driven.** JS runs **on the server** per request. `GET /api/widget/pages/{id}?query` returns page JSON. Actions `POST /api/widget/actions/{name}` with `{payload, context}` and get back `{ok, refresh, toast, error, fieldErrors}`. **Any state change re-fetches the whole page.** Lambdas are build-time only, and there is no client-side reactive state.
- Real host example: upwork `verbs/upwork.js:228-330` (express routes per page and per action) and `verbs/lib/pages.js` (2.5k lines of page composition); reference sites in `rag-evaluation-system/examples/xgoja/widget-site/`.

**React renderer** — npm `@go-go-golems/rag-evaluation-site` (`packages/rag-evaluation-site`). `WidgetRenderer.tsx:71-112` is a recursive switch over a registry of adapters (`defineWidget({type, module, render(props, children, ctx, node)})`, `src/widgets/registry.ts:18-51`). Built with `pnpm build:app`, embedded via go:embed (`pkg/defaultspa`) or copied into an xgoja app's assets and served with `app.spaFromAssetsModule`. **The default `RagEvaluationSiteApp` hard-codes the default registry** (App.tsx), so a new widget type needs a custom host. Known bug: non-server actions are confirmed twice.

**Gaps for search experiences.** There is no debounced live query (SearchField submits on Enter), no facet widget, no highlighted result list, no answer streaming (no SSE/WS at all), no DAG/trace widget, and no partial page updates. The prototype's `gui` has all of these.

**Its own RAG DSL (v2).** `pkg/gojamodules/rag/module.go:51-82` exports `pipeline, fragment, queryPlan, product, study, variant, validate, explain, compileProduct, compileStudy, preview` and namespaces `inputs, units, chunks, representations, embeddings, indexes, retrieve, collapse, fusion, hydration, rerank, generation, metrics, datasets, recipes`. The builders are Go-backed (hidden `goja.Symbol` refs), and configurator callbacks run once at authoring time. The module is **authoring-only**: it compiles to `rag-pipeline-ir/v2` / `rag-product-plan/v2` (`pkg/ragcontract`) and executes in Go (`pkg/ragengine`, `pkg/ragproduct`, `cmd/rag-product-server /v1/query`). It has a typed query-trace schema (`rag-query-trace/v2`, `pkg/ragcontract/trace.go:32-46`) and eval metrics (`pkg/ragoperators/evaluate.go:109-150`). It uses bleve directly and does not use ragkit. Example: `examples/rag-v2/01-product.js`.

### 6.10 coinvault: production reference

`/home/manuel/code/gec/coinvault` (module `github.com/go-go-golems/gec-rag`). It has no goja. We use it as a reference for:

- ragkit wiring: `internal/knowledge/service.go`, `internal/knowledgebuild/build.go`, `knowledge_search` tool (`internal/knowledge/tool.go:100-136`);
- SPA embedding: `internal/webui/assets_embed.go` (`//go:build embed`, `//go:embed embed/public`), disk fallback, runtime-config injection (`handler.go:91`);
- profiles and tool allowlists: `profile-registry.yaml`, `internal/webchat/runtime.go:76-135`, `catalog.BuildRegistry(allowed)`.

### 6.11 Target corpus: go-go-parc

Survey results (see `analysis/01-…` §4): 2,441 `.md` files, about 2,285 real notes / 81 MB after excluding `.pi/ ttmp/ .trash/ */_assets/** scripts/`. ~82% have YAML frontmatter (`title type tags created status aliases repo`), with inconsistencies: block vs inline tags, `repo` vs `repository`. There are 11.6k wikilinks, 122 notes over 100 KB, 43k code fences, and a few `.md` files that are actually HTML.

## 7. Evidence from the spikes

All spike code is in this ticket's `scripts/`.

| Spike | What it shows |
|---|---|
| `01-goja-smoke` (plain goja, same version as go-go-goja) | `rag.js`, `gui.js` and `kit.js` load unmodified. `new Function` and the `with(Proxy)` lambda guard work. Builds complete. App specs compile to JSON (`kind: gui`). |
| `02-goja-regex-probe` | `"rebalance law".match(/[\p{L}\p{N}_]+/gu)` returns **`null`** in goja: no error, no match. Lookbehind works. This alone made every search return 0 hits. |
| `01` with `RAGWB_COMPAT=1` | 5 presets: hit@5 = 1.0, MRR 0.933–1.0, query 4–9 ms, build 3.2–4.9 s for 540 KB / 462 chunks (parse ~0.55 s, chunk ~1.07 s, BM25 ~1.1 s; the 0.12 s BM25 figure first recorded was measured with the broken tokenizer, see diary step 7). |
| `03-jsverbs-split` | `lib/corpus.js` + `lib/plans.js` + `search.js` + `evals.js` run through `jsverbs-example`. The "rebalance law" top score is **12.676**, the same as the browser value in the textbook (12.6758). `goldens` gives lexical nDCG 0.845 vs fused 0.899. |

The interpretation drives three decisions below: keep the JS engine (D1), fix the regex at the source with feature detection (D6), and move heavy build nodes to Go (D2).

## 8. Gap analysis

| Need | Exists | Gap |
|---|---|---|
| Run the grammar server-side | rag.js works in goja | `\p{L}` fix; `require("ragwb")` module; host env (files, sample, log) from Go |
| Walk a vault | go-go-goja `fs` readdir/stat | recursive walk + include/exclude globs + per-file digest/mtime cache |
| Fast parse/chunk at 80 MB | goja-text (goldmark, spans), ragkit chunkers | adapter components that emit the prototype's doc/chunk shape **plus byte ranges** |
| Persistent indexes | ragkit bundles, Bleve, sqliteexact | build-node outputs persisted by digest; open-on-demand; incremental rebuild |
| Embeddings | geppetto embeddings, ragkit embedder | new `embed`/`index.vector` components; query-time embedding in `retrieve` |
| LLM nodes server-side | geppetto | `ctx.sample` implementation with claude.ai semantics (tiers, json, onText, tools) |
| Workbench LLM + page tools | slop sample protocol | port protocol into ragwb on geppetto |
| Saved scripts | — | apps directory + history + HTTP API + `wb.save` in agent scope |
| Files as CLI verbs | jsverbs | runtime with `rag` and natives preloaded; conventions for verb files |
| Remote pipeline for UI | `host.rag` seam | `RemoteRag` (browser) + `/api/rag/...` (server) + session lifetime |
| Ad-hoc app serving | gui runtime | a small page shell that mounts one app with RemoteRag |

## 9. Proposed architecture and APIs

### 9.1 Repository layout (`github.com/go-go-golems/rag-workbench`)

```text
cmd/ragwb/                 main: cobra root; attaches xgoja default commands + our Go commands
internal/xgojaruntime/     generated (xgoja runtime-package) — do not edit
pkg/ragjs/                 embedded rag.js, gui.js, kit.js (+ go:embed); the `rag` / `rag/gui` / `rag/kit` modules
  assets/rag.js …          vendored from the prototype; edited in place from now on
  module.go                NativeModule: loads rag.js into a runtime, installs host env
  host.go                  Go side of rag.configure({files, sample, log, natives})
pkg/natives/               Go-native components registered into rag.js
  walk/                    source.dir: walk + globs + digests
  markdown/                parse.markdown.go (goja-text / goldmark) emitting prototype doc shape + ranges
  bleve/                   index.bleve wrapping goja-bleve/ragkit bleve
  vector/                  embed + index.vector over ragkit sqliteexact + geppetto embeddings
  bundle/                  persist/open build artifacts (ragkit indexbundle)
pkg/llm/                   sample bridge: claude.ai sample semantics on geppetto (+ fake backend)
pkg/server/                net/http ServeMux: workbench, window.claude shim, app store API, RemoteRag API, SSE
pkg/store/                 apps dir + sqlite (history, conversations, uploads, doc tree for `db`)
pkg/xgoja/providers/ragwb/ xgoja provider exposing ragwb, ragwb/gui, ragwb/kit, ragkit modules
web/workbench/             ported index.html/app.js/agent.js + runtime shim (claude.js)
web/app-shell/             minimal page mounting one gui app with RemoteRag
examples/apps/reports/     the prototype's bundled corpus as a split-file app
examples/apps/parc/        the vault browser
xgoja.yaml, Makefile, go.mod
```

### 9.2 The `ragwb` module in goja (module name chosen to avoid rag-evaluation-system's `rag`, see D8)

`rag.js` is UMD, so the simplest working form is to evaluate it in a CommonJS wrapper and return `module.exports`. The Go module adds a **host environment** before scripts run:

```go
// pkg/ragjs/module.go (sketch)
type Host struct {
    Roots   []string                 // allowlisted filesystem roots
    Sample  llm.Sampler              // nil → LLM nodes fail with sampling_unavailable
    Log     func(args ...any)
    Natives *natives.Registry        // Go components to register via rag.define
}

func (m *Module) Loader(vm *goja.Runtime, module *goja.Object) {
    exports := evalUMD(vm, assets.RagJS)          // returns the rag object
    env := vm.NewObject()
    env.Set("log", m.host.Log)
    env.Set("sample", m.bindSample(vm))           // returns Promises via runtimeowner
    env.Set("files", func() []File { return nil })// overridden per corpus by source.dir
    call(exports, "configure", env)
    m.host.Natives.InstallInto(vm, exports)       // rag.define('source.dir', {...}) etc.
    module.Set("exports", exports)
}
```

`require("ragwb/gui")` evaluates `gui.js` with `root.rag` set to that same object. Spec building works headless, and rendering is browser-only. `require("ragwb/kit")` provides the kit helpers.

Script authors then write ordinary CommonJS:

```js
const rag = require("ragwb");
const gui = require("ragwb/gui");
```

The workbench editor keeps supporting the "async body that returns `{plan, evals, app}`" form, by wrapping it exactly as `app.js:175` does, so the 29 presets keep working.

### 9.3 Answering the core question: adapters or API changes?

**Short answer: both, at different layers. The concepts stay: builders → IR → build/live engine, closed lambdas, ops, evals, gui.**

1. **Adapters, at the component and index layer.** ragkit, goja-text and goja-bleve become implementations of components and indexes, registered with `rag.define` and satisfying the `RagIndex` contract (§5.3). Scripts keep writing `.index('text', rag.bm25())`. A `backend` param or a sibling variant (`rag.index.bleve(...)`) picks the native one. A low-level `require("ragkit")` module (1:1 with Go types) exists for power users and custom components.
2. **Targeted API adjustments** where the prototype's assumptions break at vault scale or clash with ragkit's contracts:

| # | Adjustment | Before (prototype) | After |
|---|---|---|---|
| A1 | **Directory sources** with roots, globs, ignores | `rag.files('*.md')` matches names in an in-memory list | `rag.source.dir({ root: '~/…/go-go-parc', include: ['**/*.md'], exclude: ['.pi/**', 'ttmp/**', '.trash/**', '**/_assets/**'] })` → file records `{path, name, text, size, mtime, digest}`. `rag.files()` stays for uploads/inline. |
| A2 | **Exact-slice chunks** | `chunk.text` is rebuilt from parsed blocks (`blocksText`, `rag.js:309`) | Every chunk gets `range: {byteStart, byteEnd, lineStart, lineEnd}` and `source` (the exact slice). `text` remains the indexed "raw" representation. This enables ragkit validation, highlighting and `obsidian://` / editor deep links. |
| A3 | **Explicit representations** | `enrich(name, fn)` adds ad-hoc fields; BM25 `fields` weights pick them | `.represent('summary', rag.llm.summary())` / `.represent('crumbs', rag.breadcrumbs())`. Indexes declare `over: ['raw', 'summary']`, and `retrieve` collapses representation hits to chunk hits (ragkit `Collapse`). `enrich` stays as sugar. |
| A4 | **Vectors** | none | `.embed({ profile: 'ollama-nomic' })` + `.index('vec', rag.vector({ over: ['raw'], store: 'sqlite' }))`. `retrieve('vec')` embeds the query (live, `trigger: 'change'` by default because local embeddings are cheap, configurable). |
| A5 | **Persistent build artifacts** | memo in a JS `Map`, lost on reload | `corpus.store('parc')` (or a default store path). Build-node outputs persist under `~/.ragwb/store/<corpus>/<node-digest>/`. `rag.open(corpus)` reuses them when digests match and reports **stale** nodes otherwise. File-level digests make rebuilds incremental. |
| A6 | **Artifacts as an interface** | `art.chunks` is a `Map` holding all chunk objects | `ctx.chunk(id)`, `ctx.chunks(pred)`, `ctx.doc(id)` stay the public API (lambdas already use them), but the engine reads through an accessor that can be backed by the bundle's SQLite content store, so 70k chunks need not live in JS memory. |
| A7 | **Modules instead of injected globals** | `rag, gui, log, kit` injected into a function body | `require("ragwb")`, `require("ragwb/gui")`, `require("ragwb/kit")`. A file exports values (`module.exports = {plan, evals, app}`) and other files require them. The body-returning form stays for the editor and presets. |
| A8 | **LLM through profiles** | `ctx.sample(input, {modelTier})` hits claude.ai | Same signature. `modelTier: quick/default/complex` maps to geppetto profiles in config, and `sample.json` keeps its JSON-extraction semantics. Add `ctx.embed(texts)`. |
| A9 | **Natives inside closed lambdas** | lambdas see only `ALLOWED` globals + args | `ctx.lib` (already present for gui) gains allowlisted native helpers (`ctx.lib.text`, `ctx.lib.ragkit`). Lambdas still cannot `require`, which keeps them storable as source. |
| A10 | **Facets at scale** | JS `TermsIndex` | Keep for small corpora. Add a SQLite-backed terms index (`rag.terms({ store: 'sqlite' })`) for the vault. |

Everything in the table is additive or internal, except A2. A2 changes what `chunk.text` means to the inspector, and needs a migration note in `kit.js` and the presets.

### 9.4 Files, verbs and apps: conventions

A directory under `apps/` is one **app**. Files fall into roles by what they export:

| Role | Convention | Runs as |
|---|---|---|
| library (`lib/*.js`) | `module.exports = { corpus, plans, goldens }`. No top-level functions, so no verbs. | `require` only |
| index job | `async function index(force) { return rows }` with `__verb__` | `ragwb verbs --dir apps/x x index` |
| query | `async function search(q, k, plan) {...}` | `… x search "query" --output json` |
| evals | `async function goldens(plan) {...}` → metric rows | `… x goldens` (CI-friendly) |
| app | `module.exports.app = gui.app(...)` (+ optional `function serve(port)`) | `ragwb serve apps/x` or the workbench App tab |
| workbench script | async-body form returning `{plan, evals, app}` (presets) | the editor |

Helper verbs we provide on the `rag` side so that verb files stay small:

```js
// in a verb file
const rag = require("ragwb");
const { corpus } = require("./lib/corpus");
async function index(force) {
  const report = await rag.build(corpus, { persist: true, force });
  return Object.values(report.nodes).map(n => ({ node: n.id, type: n.t, items: n.count, ms: n.ms, reused: n.reused }));
}
__verb__("index", { short: "Build and persist the corpus", fields: { force: { type: "bool", default: false } } });
```

### 9.5 The native component bridge

Go natives are registered **into rag.js's registry** so the IR, inspector, ops and agent see them like any other component:

```js
// installed by pkg/natives at module load (JS side of the bridge)
rag.define('source.dir', {
  phase: 'build', in: [], out: 'files',
  args: o => ({ ...o }),                       // root/include/exclude become node params → part of the digest
  all: (inputs, ctx, p) => __natives.walk(p),  // Go: returns [{path,name,text,size,mtime,digest}]
});
rag.define('parse.markdown', {                 // replaces the JS parser when natives are present
  phase: 'build', in: ['files'], out: 'docs',
  map: (file, ctx, p) => __natives.parseMarkdown(file, p),   // goldmark + yaml.v3, same doc shape + ranges
});
rag.define('index.bleve', {
  phase: 'build', in: ['chunks'], out: 'index',
  all: ([chunks], ctx, p) => __natives.bleveIndex(chunks, p, ctx.node), // returns a RagIndex-shaped object
});
```

On the Go side, a native index is a goja object whose `search` method calls Bleve/ragkit:

```go
// pkg/natives/bleve (sketch)
func (n *Natives) BleveIndex(chunks []Chunk, p Params, node NodeInfo) (*goja.Object, error) {
    dir := n.store.NodeDir(node.Corpus, node.Digest)         // persisted by digest (A5)
    idx, reused, err := openOrBuild(dir, chunks, p.Fields)    // ragkit/rag/lexical/bleve Build/Open
    if err != nil { return nil, err }
    obj := n.vm.NewObject()
    obj.Set("kind", "bm25")
    obj.Set("search", func(q string, k int, opts map[string]any) []Hit { return idx.Search(q, k, synonyms(opts)) })
    obj.Set("searchIn", ...)
    obj.Set("toJSON", func() map[string]any { return map[string]any{"kind": "bleve", "dir": dir, "reused": reused} })
    return obj, nil
}
```

**Rule:** native functions are **synchronous** and called on the VM goroutine. They may block on disk I/O. Anything long-running (embedding a corpus) is exposed as a Promise resolved through `runtimeowner.Post` from a worker goroutine, so the event loop stays responsive for the server.

### 9.6 The LLM bridge

There is one Go interface, used both by `ctx.sample` (scripts) and by the page's `sample` (workbench agent):

```go
// pkg/llm (adapted from slop-computer pkg/llm/backend.go:114)
type Request struct { Input json.RawMessage; ModelTier, Model, Effort string; MaxTokens int; Tools []Tool }
type Event   struct { Type string; Text string; ToolUse *ToolUse; Usage *Usage; Error *Failure }  // call|text|thinking|tool|usage|result|error
type ExecuteTool func(ctx context.Context, use ToolUse) (ToolResult, error)
type Backend interface {
    Sample(ctx context.Context, req Request, emit func(Event), exec ExecuteTool) error
    Close() error
}
// implementations: gepp (geppetto engine + toolloop, tiers → profiles), fake (deterministic for tests)
```

- **Scripts** (`ctx.sample`): tools execute in goja (wrapped JS functions, called via the runtime owner). Returns a Promise of `{text}`. `sample.json` asks for JSON and parses it.
- **Page**: the slop protocol (§6.8). The workbench's `agent.js` keeps its `eval` tool running in the page, and needs no code change beyond pointing `window.claude` at our shim.

### 9.7 Server API (net/http ServeMux, Go 1.22 patterns)

| Method & path | Purpose |
|---|---|
| `GET /` , `/workbench/*` | workbench SPA (embedded) |
| `GET /_rt/claude.js` | `window.claude` shim |
| `POST /_rt/sample` → SSE | LLM call (`call, text, thinking, tool, usage, result, error`) |
| `POST /_rt/sample/{call}/tools/{use}` | page tool result `{value, isError}` |
| `POST /_rt/sample/{call}/cancel` | abort |
| `POST /_rt/rpc` | `db.get/db.put/db.list/db.delete`, `user.id`, `sample.limits` |
| `GET /api/apps` · `GET /api/apps/{app}` | list apps / files |
| `GET/PUT/DELETE /api/apps/{app}/files/{path...}` | read / save (creates a history entry) / delete a script |
| `GET /api/apps/{app}/history` | saved versions |
| `POST /api/rag/{app}/open` | compile + build/open the app's plan → `{session, ir, stale, stats}` |
| `POST /api/rag/{session}/run` | `{signals, trigger}` → result JSON (`hits`, `top`, `trace`, `answer`, `errors`) |
| `GET /api/rag/{session}/chunk/{id}` · `doc/{id}` · `neighbors/{id}` | artifact lookups for `host.rag` |
| `POST /api/rag/{session}/chunks` | `{pred: "<lambda source>"}` → chunk list (lambda runs closed server-side) |
| `GET /apps/{app}/` | ad-hoc app shell mounting the app's gui with RemoteRag |

### 9.8 RemoteRag (browser)

```js
// web/app-shell/remote-rag.js — implements the host.rag facade from app.js:350
function remoteRag(base, sessionId) {
  const j = (p, b) => fetch(`${base}/api/rag/${sessionId}${p}`, b ? { method: 'POST', body: JSON.stringify(b) } : {}).then(r => r.json());
  return {
    session: () => ({ run: (signals, o = {}) => j('/run', { signals, trigger: o.trigger }).then(decorate) }),
    ir: () => cachedIr,
    chunk: id => cache.chunk(id) ?? j(`/chunk/${encodeURIComponent(id)}`),   // NOTE: gui expects sync → prefetch hit chunks with each run
    doc: id => cache.doc(id),
    chunks: pred => j('/chunks', { pred: String(pred) }),
    docs: () => cache.docs(),
    neighbors: id => cache.neighbors(id),
  };
}
```

**Tricky part:** the gui runtime calls `host.rag.chunk(id)` **synchronously** while rendering. The run endpoint must therefore return the chunk bodies of all hits it returns (and their neighbors). RemoteRag serves `chunk/doc/neighbors` from that cache, and fetches misses asynchronously before re-rendering. Validate this against `gui.js:609-697` (`results`, `answer`, `chunk` components) before building it.

### 9.9 Storage

- **Apps directory is the source of truth**, `~/.ragwb/apps/<app>/…` by default (configurable, can point at a git repo). jsverbs scans it directly, so "a saved script is runnable" needs no export step.
- **SQLite `~/.ragwb/ragwb.db`**: `history(app, path, version, sha256, source, comment, created_at)`, `conversations(id, app, json, updated_at)`, `docs(path, json)` for the `db` capability (uploads, UI state).
- **Artifact store** `~/.ragwb/store/<corpus>/<node-digest>/` for persisted build outputs and ragkit bundles.

### 9.10 Security boundaries

- Scripts can read files only under allowlisted **roots** (config + CLI flag). `source.dir` and the `fs` module are both configured with those roots. go-go-goja's host `fs` explicitly "does not sandbox paths" unless given a `Root`.
- Lambdas run closed (no `require`). Verb files are trusted code: they are your own scripts.
- The server binds to `127.0.0.1` by default. The `exec` module is not exposed.

## 10. Decision records

### Decision D1: Keep the engine in JS; move heavy nodes to Go

- **Context:** rag.js (2k lines) runs in goja today with the browser's semantics. Its build phase is about 5 s/MB.
- **Options considered:** (a) reimplement the engine in Go and keep JS builders only; (b) keep the JS engine and register Go-native components for heavy build nodes; (c) run JS everywhere.
- **Decision:** (b).
- **Rationale:** Query-time engine overhead is milliseconds, so the cost is in parse, chunk and index, which are exactly the nodes we replace. One engine means the browser workbench, the textbook and the server agree on semantics, ops and traces.
- **Consequences:** Native components must respect the engine's digest and memo rules and return prototype-shaped values. Engine-level concurrency is limited to one VM per session. We can parallelize *inside* native nodes.
- **Status:** proposed

### Decision D2: Adapters at the component layer, plus targeted API adjustments

- **Context:** The user likes the grammar. ragkit has stricter contracts (exact slices, representations, bundles).
- **Options considered:** (a) wrap ragkit 1:1 and let scripts call it directly; (b) reshape the grammar around ragkit's types; (c) keep the grammar, implement components with ragkit, adjust where needed (§9.3).
- **Decision:** (c), plus a low-level `require("ragkit")` for custom components.
- **Rationale:** Keeps every preset and the textbook valid, and still gains ragkit's persistence and validation.
- **Consequences:** A2 (exact-slice chunks) touches the shared chunk shape and needs a careful migration.
- **Status:** proposed

### Decision D3: UI model — keep `gui` for search experiences; use widget.dsl for server-driven pages

- **Context:** Two UI grammars exist. The prototype's `gui` is client-reactive: state, derive and lambdas run in the browser, typing re-runs only `change`-trigger live nodes, and it already has `searchBox`, `facets`, `results`, `answer`, `chunk`, `trace`, `dag` and charts. `widget.dsl` is server-driven page JSON with typed widgets (tables, forms, master-detail, dialogs) and whole-page refresh, and has no search-specific widgets (§6.9).
- **Options considered:** (a) port the search experiences to widget.dsl; (b) keep `gui` for search experiences, and use widget.dsl for server-driven pages (app list, corpus/build status, eval tables, history); (c) a converged renderer: add a `ragwb.guiApp` widget type to a custom WidgetRenderer host that mounts a gui app with RemoteRag inside a widget page.
- **Decision:** (b) now, (c) as the convergence path.
- **Rationale:** Search UIs need per-keystroke partial updates and client state. widget.dsl would need debounced search, facets, highlights, streaming and partial patches before it could host them. Management pages are exactly what widget.dsl is good at.
- **Consequences:** Two renderers in the browser bundle for a while. (c) requires a custom React host, because `RagEvaluationSiteApp` hard-codes its registry.
- **Status:** proposed

### Decision D4: Where the workbench agent loop runs

- **Context:** agent.js's `eval` tool manipulates page state (editor, inspector, workspace).
- **Options considered:** (a) LLM loop on the server, tool executed in the page (slop protocol); (b) whole agent on the server via scopedjs; (c) LLM calls straight from the browser.
- **Decision:** (a) first; (b) later for headless agents ("index this folder and make me an app").
- **Rationale:** (a) needs no agent rewrite, and the protocol and implementation exist in slop-computer. (c) leaks keys and bypasses profiles.
- **Consequences:** Tool round-trips need timeouts and cancellation (slop has both).
- **Status:** proposed

### Decision D5: Binary composition via xgoja runtime-package + hand-written main

- **Context:** We need jsverbs, providers and embedded assets (xgoja's strengths), plus Go-heavy HTTP endpoints (SSE, tool parking).
- **Options considered:** (a) xgoja-generated binary only, with server routes in JS `express`; (b) hand-written runtime wiring without xgoja; (c) xgoja `runtime-package` + `cmd/ragwb` adding Go commands (the upwork pattern).
- **Decision:** (c). Our modules are packaged as xgoja providers so other xgoja apps (e.g. a vault-specific binary) can include them.
- **Consequences:** We depend on the xgoja generator in the build (`make generate`). Generated code is committed, as in upwork.
- **Status:** proposed — **first slice uses (b)** (plain go-go-goja engine + jsverbs) to validate the module before adding the generator.

### Decision D6: Fix `\p{L}` with feature detection in rag.js/gui.js

- **Context:** goja silently fails Unicode property escapes. Browsers support them.
- **Options considered:** (a) rewrite at load time in Go (spike approach); (b) patch the vendored sources to build the regex via a helper that feature-detects `\p{L}` support and falls back to explicit ranges; (c) fix goja upstream.
- **Decision:** (b) now, and (c) as a follow-up issue.
- **Rationale:** One source file for both browser and goja. Browsers keep full Unicode.
- **Consequences:** Fallback ranges cover Latin, Greek, Cyrillic, kana and CJK only. Document this.
- **Status:** proposed

### Decision D7: Apps directory as the source of truth for saved scripts

- **Context:** Saved scripts must be runnable standalone with jsverbs, which scans directories.
- **Options considered:** (a) scripts in SQLite and exported on demand; (b) files on disk, with history in SQLite; (c) files in a git repo with one commit per save.
- **Decision:** (b), with (c) possible by pointing the apps dir at a git checkout.
- **Status:** proposed

### Decision D8: Relationship to rag-evaluation-system's `rag` v2 DSL

- **Context:** rag-evaluation-system already ships `require("rag")`: Go-backed builders compiling to `rag-pipeline-ir/v2` and executed by a Go engine. It overlaps with our grammar, and its module name collides with the prototype's `rag`.
- **Options considered:** (a) adopt rag v2 as the scripting API and drop the workbench grammar; (b) keep the workbench grammar, name our module `ragwb`, and borrow v2's contracts where useful (query-trace schema, metrics, representation/collapse vocabulary); (c) make the workbench IR lower to `rag-pipeline-ir/v2` so ragproduct can execute workbench plans.
- **Decision:** (b), with (c) as an optional later bridge for "productionizing" a plan.
- **Rationale:** The user explicitly wants the workbench API's concepts. v2 is authoring-only from JS and has no runtime lambdas, live engine, ops/undo or client UI, which are exactly what the workbench and the agent need. v2's typed IRs are a good *target* for frozen products, not for exploration.
- **Consequences:** Two RAG DSLs exist in the org. The guide and help pages must say which to use when: **workbench for exploring and ad-hoc apps, v2 for audited products/studies**. Our module is `require("ragwb")`; scripts typically write `const rag = require("ragwb")`.
- **Status:** proposed

## 11. Key flows (pseudocode)

### 11.1 `ragwb verbs --dir apps/parc parc index`

```text
main → registry = jsverbs.ScanDir(apps/parc)             // finds index.js#index, search.js#search …
     → cobra dispatch "parc index"
     → rt = runtimeFactory.NewRuntime(ctx)                // modules: ragwb, ragwb/gui, ragkit, fs(root-confined), db
     → registry.InvokeInRuntime(ctx, rt, verb, values)
         JS: corpus = require('./lib/corpus')             // builder value; no work yet
             rag.build(corpus, {persist:true})
               engine.digests(ir)                         // per node: hash(params, upstream digests, file digests)
               for build node n in topo order:
                 if store.has(n.digest): load handle; reused=true
                 else: out = n.map/all(...)               // natives: walk → parse(goldmark) → chunk → bleve.Build
                       store.put(n.digest, out)
         → rows per node → Glazed table/json
```

### 11.2 A keystroke in an ad-hoc app

```text
browser gui runtime: state.q = "rebal" → plan signal q changed
  host.rag.session().run({q:"rebal"}, {trigger:"change"})
    RemoteRag → POST /api/rag/{session}/run {signals, trigger}
server: rt.Owner.Call(ctx, "run", func(vm){ return session.run(signals, {trigger}) })
          live nodes with trigger 'change' run; LLM nodes (submit) are skipped
        response: { hits, top, trace, chunks: {id → chunk}, docs: {...} }
browser: cache chunks; gui re-renders results/facets (only components reading those keys)
```

### 11.3 A workbench agent turn

```text
agent.js: sample(turns, {tools:[eval], onText})
  shim → POST /_rt/sample {input, tools:[{name:'eval', inputSchema}]}  → SSE
server: backend.Sample(ctx, req, emit, exec)                // geppetto engine + toolloop
          model asks tool 'eval' {code}
          exec(): emit("tool", {use, input}); wait on chan[use] (timeout 120s)
browser: on 'tool' event → runEval(code) in page scope (rag, gui, wb, ws, ui, chat, ref)
          POST /_rt/sample/{call}/tools/{use} {value}
server: chan[use] ← value → loop continues → emit text/result
agent.js: renders parts; wb.save(path, text) → PUT /api/apps/{app}/files/{path}
```

### 11.4 Saving a script

```text
PUT /api/apps/{app}/files/{path} {source, comment}
  validate path inside app dir; write temp + rename (atomic)
  history.insert(app, path, version+1, sha256, source, comment)
  optional: jsverbs.ScanDir(appDir) → return diagnostics (bad __verb__ metadata surfaces immediately)
```

## 12. API reference (planned surface)

### 12.1 JS: `require("ragwb")` additions (the prototype `rag` namespace plus these)

```ts
rag.source.dir(o: { root: string; include?: string[]; exclude?: string[]; maxBytes?: number }): Component
rag.vector(o?: { over?: string[]; store?: 'memory' | 'sqlite'; metric?: 'cosine' }): Component
corpus.embed(o: { profile?: string; model?: string; batch?: number }): CorpusSpec
corpus.represent(kind: string, c: Component | Lambda): CorpusSpec
corpus.store(nameOrPath: string): CorpusSpec
rag.build(target, o?: { persist?: boolean; force?: boolean; onProgress?(e) }): Promise<BuildReport>
rag.open(target): Promise<{ ir; stale: string[]; artifacts }>
// unchanged: corpus, plan, compile, run, search, evals, sweep, workspace, define, components, explain
```

### 12.2 JS: `require("ragkit")` (low level)

```ts
ragkit.chunk.markdownHeading(text: string, o?: { max?: number; min?: number; overlap?: number }): Span[]
ragkit.bleve.build(dir: string, records: {id; fields}[], mapping?): BleveIndex
ragkit.bleve.open(dir: string): BleveIndex       // .search(q, k) → Hit[]
ragkit.vectors.open(path: string): VectorIndex   // .search(vec, k), .searchText(q, k) (with embedder)
ragkit.rrf(channels: Record<string, Hit[]>, o?: { k?: number; weights?: Record<string, number> }): Hit[]
ragkit.embed(texts: string[], o?: { profile?: string }): Promise<number[][]>
```

### 12.3 Go packages (planned)

| Package | Key symbols |
|---|---|
| `pkg/ragjs` | `NewModule(host Host) modules.NativeModule`, `Assets` (embed.FS), `Host{Roots, Sample, Log, Natives}` |
| `pkg/natives` | `Registry`, `InstallInto(vm, ragObj)`, components `walk`, `markdown`, `bleve`, `vector`, `bundle` |
| `pkg/llm` | `Backend`, `Request`, `Event`, `ExecuteTool`, `NewGeppetto(profiles)`, `NewFake()` |
| `pkg/store` | `Apps{List, Read, Write, History}`, `Docs` (for `db`), `Artifacts{NodeDir, Has, Put}` |
| `pkg/server` | `New(cfg) *http.ServeMux`, handlers in §9.7 |

### 12.4 Existing APIs you will call most

- `jsverbs.ScanDir(dir) (*Registry, error)`, `registry.Commands()`, `registry.InvokeInRuntime(ctx, rt, verb, values)` — `go-go-goja/pkg/jsverbs/`
- `engine.NewRuntimeFactoryBuilder()…Build()`, `factory.NewRuntime(ctx)`, `rt.Owner.Call(ctx, name, fn)` — `go-go-goja/pkg/engine`, `pkg/runtimeowner`
- `indexbundle.Build/BuildStream/Open` — `ragkit/rag/indexbundle`
- `bleveindex.Build/Open/Search` — `ragkit/rag/lexical/bleve/index.go`
- `factory.NewStandardEngineFactory().CreateEngine(settings)`, `toolloop.New(...).RunLoop` — geppetto
- slop `pkg/server/sample.go` + `web/runtime/src/sample.ts` — protocol to port

## 13. Implementation phases

Each phase ends with a demo command and tests. Commit at each phase boundary.

| Phase | Deliverable | Acceptance |
|---|---|---|
| **P0 Scaffold** | `go.mod`, `cmd/ragwb` (cobra), Makefile, vendored `pkg/ragjs/assets/{rag,gui,kit}.js` + presets + corpus as example data | `go build ./...`, `ragwb version` |
| **P1 `ragwb` module** | `pkg/ragjs`: UMD loader, `\p{L}` feature-detect fix (D6), host env (log, files), `ragwb run file.js` | Go test: minimal preset → top hit "5.9 The rebalance law" with score 12.676; goldens hit@5 = 1 |
| **P2 jsverbs** | `ragwb verbs --dir <apps>` using our runtime (`ragwb` resolvable); `examples/apps/reports` split into lib/search/evals | `ragwb verbs --dir examples/apps/reports reports search "rebalance law"` gives the same rows as spike 03 |
| **P3 Directory source** | `rag.source.dir` native: walk, doublestar globs, excludes, mtime/size/digest cache, roots allowlist | Index `go-go-parc` `Research/` subset; build time recorded; excluded dirs absent |
| **P4 Native parse/chunk** | goldmark-based `parse.markdown` + `chunk.sections` emitting the prototype shape + ranges (A2) | Golden test: chunk ids/heading paths match the JS implementation on the 11 reports; build time at least 5× faster |
| **P5 Workbench server** | serve the ported workbench; `window.claude` shim (`sample` via fake + geppetto, `db` via SQLite, `user`) | Prototype opens from `ragwb workbench`; presets build; agent turn with `eval` round-trip works with the fake backend |
| **P6 App store** | apps dir + history API; agent `wb.save/load`; editor opens app files | Save from browser → file on disk → runnable via `ragwb verbs` |
| **P7 Remote pipeline** | `/api/rag/...` sessions + RemoteRag + `ragwb serve <app>` | Ad-hoc app searches the vault with a server-side plan; typing latency < 100 ms on the parc subset |
| **P8 Persistence + vectors** | bleve index node, artifact store by digest, embeddings + sqliteexact (A4/A5), `rag.open` stale reporting | Second `index` run reuses all nodes; hybrid plan beats lexical on vault goldens |
| **P9 LLM nodes server-side** | `ctx.sample` on geppetto; `answer`, `rerank.llm`, `rewrite.llm` presets run headless | "With Claude" presets run via `ragwb run` against a real profile |

## 14. Test strategy

- **Parity tests (most important):** run each preset through the Go runtime and compare top-k ids, scores and eval summaries against fixtures captured from the JS engine. The 12.676 "rebalance law" score is the first fixture. Natives (P4) must pass the same fixtures.
- **Module tests:** go-go-goja style, using `engine` + `require()` in Go tests (see the `go-go-goja-module-authoring` skill).
- **jsverbs tests:** scan `examples/apps/*`, assert verb inventory and row shapes, and assert that metadata errors surface as `ScanError`.
- **Server tests:** `httptest` for the sample SSE protocol with the fake backend: tool round-trip, timeout and cancel paths (port slop's tests where present).
- **Browser smoke:** Playwright loading the workbench, building a preset and running one agent turn against the fake backend.
- **Scale check:** a timed build over `go-go-parc`, recorded in the diary with machine and commit.

## 15. Risks and open questions

- **Synchronous `host.rag.chunk` in the gui runtime** (§9.8) may need a small gui.js change (async-aware components) if prefetching is not enough.
- **goja performance** for live lambdas over large candidate sets: measure `rerank` lambdas with k = 200.
- **Memory**: the prototype keeps artifacts in JS `Map`s. A6 must land before the full vault is loaded into the server.
- **Exact-slice chunks (A2)** change `chunk.text` semantics. Decide whether `text` stays the "clean" text (recommended) with `source` added, or the reverse.
- **Embedding model choice** for the vault (local Ollama vs API), and query-time cost under `trigger: 'change'`.
- **Upstream goja `\p{L}`**: file an issue / PR and remove the fallback when fixed.
- **Prototype bug list** (§5.6): decide which to fix during vendoring.

## 16. File reference index

| Path | Why it matters |
|---|---|
| `sources/prototype/rag.js` | Engine; registry `:474`; define `:1917`; Engine `:1228`; Session.ctx `:1570`; namespace `:1974` |
| `sources/prototype/gui.js` | UI grammar and renderer; search components `:558-697` |
| `sources/prototype/app.js` | Script execution `:175`; host caps `:968`; guiHost seam `:350` |
| `sources/prototype/agent.js` | Agent loop `:405-440`; eval tool `:445` |
| `sources/textbook/*.md` | Conceptual explanations (Parts II–X), appendices with the API catalog |
| `scripts/01-goja-smoke/main.go` | goja compatibility harness |
| `scripts/03-jsverbs-split/app/` | Working split-file + jsverbs example |
| `~/code/wesen/go-go-golems/go-go-goja/pkg/jsverbs/` | verb scanning + invocation |
| `~/code/wesen/go-go-golems/go-go-goja/pkg/engine/factory.go` | runtime factory |
| `~/code/wesen/go-go-golems/upwork/xgoja.yaml`, `cmd/tracker/main.go` | xgoja runtime-package + hand-written main pattern |
| `~/code/wesen/go-go-golems/ragkit/rag/types.go`, `components.go`, `indexbundle/` | contracts + bundles |
| `~/code/wesen/go-go-golems/goja-bleve/pkg/module.go` | Bleve JS module |
| `~/code/wesen/go-go-golems/goja-text/pkg/chunking/module.go` | Span-based chunkers |
| `~/code/wesen/go-go-golems/geppetto/pkg/js/modules/geppetto/module.go`, `pkg/inference/tools/scopedjs/` | LLM JS module, eval tool |
| `/home/manuel/code/wesen/slop-computer/pkg/server/sample.go`, `web/runtime/src/sample.ts`, `pkg/llm/backend.go` | sample protocol |
| `/home/manuel/code/gec/coinvault/internal/knowledge/service.go`, `internal/webui/` | production ragkit + SPA embed |
| `~/code/wesen/go-go-golems/rag-evaluation-system/pkg/widgetdsl/v3.go`, `pkg/xgoja/providers/widgetsite/doc/05-widget-dsl-v3-api-reference.md` | widget.dsl implementation + API |
| `~/code/wesen/go-go-golems/rag-evaluation-system/packages/rag-evaluation-site/src/widgets/WidgetRenderer.tsx`, `registry.ts` | React renderer + adapter registry |
| `~/code/wesen/go-go-golems/rag-evaluation-system/pkg/gojamodules/rag/module.go`, `pkg/ragcontract/` | rag v2 DSL + IR contracts (D8) |
| `~/code/wesen/go-go-golems/upwork/verbs/upwork.js`, `verbs/lib/pages.js` | widget.dsl host: express page/action routes |
