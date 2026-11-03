# Part III · A grammar for pipelines

A pipeline written as a chain of builder calls is a value, not a running program: it can be compiled, checked, drawn, varied and stored. This part defines that grammar and the IR it compiles to.

## 7. Specs, not programs

This chapter explains what a pipeline *is* in the workbench. The short answer is that it is a value: a list of node records that says which components run, in what order, with which parameters. Nothing in that list has run yet. By the end of the chapter you should be able to read a pipeline script and say exactly what data it produces before it touches a single file, and you should understand why the workbench insists on that separation.

The alternative is the obvious one. A search script could open the files, split them, build an index and answer queries, all as ordinary function calls. That works once. It does not let you draw the pipeline, compare two versions of it, change one parameter while it is serving queries, or hand it to an agent that has to edit it without rerunning everything. Each of those needs the pipeline as data first and as computation second. Parts IV and VII depend on this, so it is the first thing Part III establishes.

### 7.1 Corpus specs and plan specs

A pipeline has two halves, split by when they run. The **corpus** half runs once per set of files: it reads markdown, cuts it into chunks and builds indexes. The **plan** half runs once per query: it takes the signals the page sends (the query text `q`, the result count `k`, any filters) and turns them into ranked hits, a packed context and an answer. The library has one builder for each half. Here is the whole of the *Minimal search* preset's pipeline:

```js
const corpus = rag.corpus('reports')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());

const plan = rag.plan('minimal').over(corpus)
  .signals({ q: 'rebalance law', k: 10 })
  .retrieve('text', { k: 20 });
```

None of these calls reads a file. Each one appends a node record to a list and returns a new spec holding the longer list. The arguments are data too: `rag.sections({ maxTokens: 450 })` returns a plain object that names a component type and its parameters.

```js
rag.sections({ maxTokens: 450 })
// → { "$c": "chunk.sections", "p": { "maxTokens": 450 } }
```

Asking the corpus spec for its JSON shows the node list it has accumulated:

```json
{ "kind": "corpus", "id": "reports", "nodes": [
  { "phase": "build", "id": "files",   "t": "source.files",   "in": [],         "p": { "glob": "*.md" } },
  { "phase": "build", "id": "parse",   "t": "parse.markdown", "in": ["files"],  "p": {} },
  { "phase": "build", "id": "chunks",  "t": "chunk.sections", "in": ["parse"],  "p": { "maxTokens": 450 } },
  { "phase": "build", "id": "ix-text", "t": "index.bm25",     "in": ["chunks"], "p": { "name": "text" } } ] }
```

Every node has the same five fields, and they are worth fixing in memory because the rest of the book manipulates them:

| Field | Meaning | Example |
| --- | --- | --- |
| `id` | The node's name, unique in the pipeline. Generated if you do not give one. | `chunks`, `ix-text` |
| `t` | The component type, a key into the registry (chapter 8). | `chunk.sections` |
| `in` | The ids of the nodes whose outputs this node consumes, in port order. | `["parse"]` |
| `p` | The parameters, as JSON. Functions become stored lambdas (chapter 9). | `{ "maxTokens": 450 }` |
| `phase` | `build` for corpus nodes, `live` for plan nodes. | `build` |

The plan spec looks the same, with live nodes and a signals object:

```json
{ "kind": "plan", "id": "minimal", "signals": { "q": "rebalance law", "k": 10 },
  "nodes": [ { "id": "text", "t": "retrieve", "phase": "live", "in": ["q"], "p": { "index": "text", "k": 20 } } ] }
```

Two details in that record show the builder doing work for you. The retrieve node was named `text` after the index it reads, because the `retrieve` component names its nodes that way. And its input is `q`, the query signal, even though the script never mentioned `q` in the `.retrieve` call. Declaring a `q` signal made it the plan's current source of queries, and `.retrieve` takes queries. Section 7.3 makes these inference rules explicit.

The plan holds a reference to its corpus (`.over(corpus)`), not a copy of the corpus nodes. Compiling the plan (chapter 10) concatenates the two node lists into one graph.

### 7.2 Immutability, and why variants cost nothing

Every builder method returns a new spec and leaves the receiver alone. The spec object itself is frozen, so an accidental assignment to it fails rather than silently changing a pipeline someone else is holding:

```js
Object.isFrozen(plan)    // → true
Object.isFrozen(corpus)  // → true

const small = corpus.chunk(rag.sections({ maxTokens: 200 }));
small.toJSON().nodes.map(n => n.id)   // → ["files", "parse", "chunks", "ix-text", "chunks_2"]
corpus.toJSON().nodes.map(n => n.id)  // → ["files", "parse", "chunks", "ix-text"]
```

Internally each method copies the node array and the small cursor object (section 7.3) and shares every node record with the original. Extending a spec therefore costs one array copy, whatever the size of the pipeline. Freezing is shallow, so the guarantee rests on the library never mutating a spec's state after construction, which it does not.

Immutability matters most for **variants**. A variant is a second pipeline that differs from the first in a few places, built so the two can be scored against each other. Because the original cannot change, a variant can be described as "that plan, plus these edits":

```js
const k5 = plan.variant('k5', [{ op: 'set', node: 'text', path: 'k', value: 5 }]);
```

The variant does not copy and modify the retrieve node. It records the edit as an operation, the same `set` operation the workspace uses in Part VII, and applies it when the plan is compiled:

```json
{ "kind": "plan", "id": "k5", "signals": { "q": "rebalance law", "k": 10 },
  "nodes": [ { "id": "text", "t": "retrieve", "phase": "live", "in": ["q"], "p": { "index": "text", "k": 20 } } ],
  "ops": [ { "op": "set", "node": "text", "path": "k", "value": 5 } ] }
```

After compilation the two graphs differ in exactly one parameter, which `rag.util.diffIr` reports:

```json
[ { "change": "param", "node": "text", "path": "k", "from": 20, "to": 5 } ]
```

This is why the *Rerank with a lambda* preset can declare two comparison pipelines in two lines, one that removes the rerank node and one that triples its heading weight. Neither line can disturb the main plan, and the eval sheet can run all three side by side. `variant` also accepts a function instead of an op list, `plan.variant('id', p => p.limit(5))`, for edits that are easier to write as builder calls.

There is a second benefit that only shows up later. Chapter 11 names every build node by a digest of what it would compute. Two variants that share their corpus share those digests, so building the second variant reuses every index the first one built. Immutable specs make that sharing safe: a node record that cannot change cannot invalidate a cached result behind the engine's back.

### 7.3 The chain infers the wiring; the graph form states it

A chain of builder calls never names its edges. The builder infers each node's `in` list from a **cursor**: a small map from an output type to the id of the node that most recently produced it. The corpus cursor tracks `files`, `docs` and `chunks`. When `.parse` is called, its input is whatever `files` points at; when `.chunk` is called, its input is the current `docs`, and afterwards `chunks` points at the new node.

The plan cursor tracks `queries`, a list of current `hits` streams, `context` and `answer`. The hits entry is a list because retrieval fans out: each `.retrieve` call starts a new hits stream and adds it to the list. Any step that takes a single hits stream checks that list, and if it holds more than one stream it refuses to guess which one you meant:

```js
rag.plan('bad').over(kit.reports()).signals({ q: '' })
  .retrieve('text').retrieve('meta').limit(5);
// RagCompileError: limit: 2 hit streams (text, meta) need a fuse step first, e.g. .fuse.rrf()
```

The fix is to say how the streams combine. `.fuse.rrf()` takes a variadic hits input, consumes every open stream and leaves one stream behind (chapter 14 explains the fusion rule itself).

Named chunk streams follow a related rule. In the *Table rows as chunks* preset, `.chunk('rows', lambda)` adds a second chunker that emits one chunk per table row. Because the corpus already has a main chunk stream, the named one does not move the `chunks` cursor; it is reachable only by name. That is why the index built over it says where its input comes from:

```js
kit.reports()
  .chunk('rows', (doc, ctx, p) => /* one chunk per table row */, { minRows: 2 })
  .index('rows', rag.bm25({ fields: { headingPath: 2, text: 1 } }), { from: 'rows' });
```

Inference keeps simple pipelines short, but a reader has to know the cursor rules to see the edges. When a pipeline branches and merges, it is clearer to state every edge. `rag.graph` does that. Each node gets an explicit id and an explicit `from`:

```js
const g = rag.graph('minimal-g', g => {
  g.over(corpus);
  const q = g.signal('q', 'rebalance law');
  g.signal('k', 10);
  const t = g.node('text', rag.retrieve('text', { k: 20 }), { from: q });
  g.output('hits', t);
});
```

The graph form is not a second representation. It calls the same internal method the chain calls, with the inference switched off, and it compiles to the same IR. Comparing the compiled node lists of the chain version and the graph version above gives `true`. The only difference is in the outputs: the graph pinned `hits` explicitly with `g.output`, so its IR lists `pinned: ["hits"]`, while the chain lets the compiler derive the outputs (section 10.3).

|  | Chain form | Graph form |
| --- | --- | --- |
| Edges | Inferred from the cursor | Stated with `from` |
| Node ids | Generated unless given (`{ id }`) | Always given |
| Outputs | Derived by the compiler | Pinned with `g.output`, else derived |
| Best for | Linear pipelines, presets, quick edits | Branches, merges, several answers from one context |
| Compiles to | The same IR | The same IR |

The *Graph form* preset writes a fused two-retriever plan this way. Its fuse node lists both retrievers in `from`, and its fuse weights are keyed by those node ids (`{ lex: 1, tags: 0.3 }`), which is easier to check when the ids are visible in the source.

### 7.4 Fragments and the shared `kit`

Because a spec is a value and every builder method is a function from spec to spec, a reusable piece of pipeline is just such a function. `rag.fragment` gives it a name, and `.use` applies it with parameters:

```js
const hybrid = rag.fragment('hybrid', (p, o) =>
  p.retrieve('text', { k: o.k ?? 40 }).retrieve('meta', { k: 20 }).fuse.rrf({ k: 60 }));

const hp = rag.plan('h').over(kit.reports()).signals({ q: '', k: 8 })
  .use(hybrid, { k: 30 })
  .limit(s => s.k);

rag.compile(hp).summary()
// → files:source.files → q:signal → k:signal → parse:parse.markdown → chunks:chunk.sections
//   → ix-text:index.bm25 → ix-meta:index.terms → text:retrieve → meta:retrieve → fuse:fuse.rrf → limit:limit
```

A fragment has no special status after it is applied. The compiled graph contains ordinary nodes, and nothing records that three of them came from `hybrid`. That is deliberate: the engine, the evals and the agent all work on the graph, and a graph with no hidden structure is one they can all read.

The workbench's presets share a few such pieces through `kit`, which is ordinary `rag` and `gui` code loaded before every script:

- `kit.reports()` returns the standard corpus spec: markdown sections at depth 3 and at most 450 tokens, a BM25 index named `text` with field weights `title: 2, headingPath: 3, callout: 2, text: 1`, and a terms index named `meta` over `type`, `tags`, `status` and `aliases`.
- `kit.goldens()` returns the twelve golden questions for the bundled reports (chapter 18).
- `kit.searchApp(plan, options)` returns the standard search page layout (chapter 26).

Presets extend `kit.reports()` rather than copying it. The *Code search* preset adds a code chunker and an index over it; the *Table rows* preset adds a row chunker and an index over that. Because `kit.reports()` returns a fresh immutable spec, neither extension can leak into another preset.

### Key points

- A pipeline is a list of node records with an id, a component type, input ids, JSON parameters and a phase. Builder calls only extend that list.
- Specs are immutable. A variant is the original plus recorded operations, so the original can never change under you, and two variants can share every build result they have in common.
- The chain form infers edges from a cursor keyed by output type. It refuses to guess when several hit streams are open, and named chunk streams must be referenced by name.
- The graph form states every edge and compiles to the same IR. Use it when the pipeline branches.
- A fragment is a function from spec to spec. After it is applied, it leaves only ordinary nodes behind.

The node records in this chapter carried types like `chunk.sections` and `retrieve`, and the builder knew that `retrieve` takes queries and gives hits. Chapter 8 explains where that knowledge lives.

**Lab: Minimal search.** Load the preset and open the IR tab to see the node list described in 7.1. Change `maxTokens` to 200 and rebuild; the Build passes list shows `files` and `parse` reused. Then add `const k5 = plan.variant('k5', [{ op: 'set', node: 'text', path: 'k', value: 5 }]);` and return `variants: [k5]` to score both plans on the goldens. Finally, load *Graph form* and compare its IR tab with *Reports · hybrid + rerank + answer* to see that both forms produce the same kind of graph.

## 8. Components and typed ports

Chapter 7 showed node records whose `t` field named a component type: `chunk.sections`, `retrieve`, `fuse.rrf`. This chapter explains what stands behind those names. Every type is an entry in a registry that declares the component's phase, the type of data it takes and gives, and how it runs. The builder methods are generated from that registry, the compiler checks wiring against it, and `rag.define` adds to it. Understanding the registry is what lets you predict which pipelines compile and extend the grammar without touching the library.

The design goal is to catch a whole class of mistakes before anything runs. Building indexes over 535 KB of markdown takes a few hundred milliseconds, and a query plan that ends in a Claude call takes seconds and spends usage. A pipeline that feeds a packed context into a reranker, or a query signal into an answer node, should fail at compile time with a message that names the bad edge, not at the end of a build or a paid call.

### 8.1 The registry: type, phase, ports, trigger

The registry is a `Map` from a type name to a definition object. Two helpers populate it, `build(type, def)` and `live(type, def)`, which differ only in the phase they set. Here is the complete definition of `limit`, one of the simplest live components:

```js
live('limit', {
  in: ['hits'], out: 'hits',
  args: a => (typeof a === 'number' || isLambda(a) || typeof a === 'function'
    ? { k: typeof a === 'number' ? a : lam(a) } : { ...(a || {}) }),
  describe: p => `k ${typeof p.k === 'number' ? p.k : 's.k'}`,
  run: (s, ctx, hits, p) => hits.slice(0, Number(p.k ?? s.k ?? 8)),
});
```

The fields fall into three groups. Some describe the component to the compiler, some tell the builder how to turn call arguments into parameters, and the rest are the implementation.

| Field | Group | What it does |
| --- | --- | --- |
| `phase` | compiler | `build` (runs once per corpus) or `live` (runs per query). |
| `in`, `out` | compiler | Port types. `in` is a list, one entry per input; `out` is one type. |
| `trigger` | compiler | `submit` for components that wait for Enter. Defaults to `change`. |
| `uses` | compiler | Extra dependencies that are not data inputs, such as the index a retriever reads. |
| `reads` | compiler | Signals the component reads inside its own code (section 9.5). |
| `lambdas` | compiler | Parameter names that hold functions the component calls itself, rather than values to resolve first. |
| `args` | builder | Turns the call's arguments into a params object: `limit(10)` becomes `{ k: 10 }`. |
| `id` | builder | Chooses the default node id: `retrieve('text')` makes a node named `text`. |
| `describe` | display | A one-line summary for the DAG and the agent: `k s.k`. |
| `run` | live | `(s, ctx, input, p) => output`, called once per query. |
| `map` / `all` | build | `map(item, ctx, p)` per input item, cached per item; or `all(inputs, ctx, p)` over the whole input. |

`rag.components()` lists the registry. The bundled library defines 27 types:

| Type | Phase | In | Out | Trigger |
| --- | --- | --- | --- | --- |
| `source.files`, `source.inline` | build | none | files |  |
| `parse.markdown` | build | files | docs |  |
| `chunk.sections`, `chunk` | build | docs | chunks |  |
| `enrich`, `enrich.llm` | build | chunks | chunks |  |
| `index.bm25`, `index.terms` | build | chunks | index |  |
| `synonyms`, `bake` | build | docs | value |  |
| `retrieve` | live | queries | hits | change |
| `rewrite` / `rewrite.llm` | live | queries | queries | change / submit |
| `fuse.rrf`, `fuse.max` | live | hits\* | hits | change |
| `filter`, `filter.where`, `limit`, `expand.neighbors`, `expand.links`, `map` | live | hits | hits | change |
| `rerank` / `rerank.llm` | live | hits | hits | change / submit |
| `pack` | live | hits | context | change |
| `answer` | live | context | answer | submit |
| `agentic` | live | queries | answer | submit |

The pattern in the trigger column is not an accident. Every live component that calls Claude declares `trigger: 'submit'`, so typing never spends usage. Chapter 12 builds the scheduling on top of that one field.

### 8.2 Two typed streams

The port types form two chains, one per phase.

&#91;embedded content: port types of the two streams · build left, live right\]

The build stream converts `files` (name and text) to `docs` (frontmatter, heading tree, typed blocks), `docs` to `chunks` (the units that get ranked), and `chunks` to an `index`. A side branch from `docs` produces a `value`, used for tables such as query synonyms. The live stream starts from a **signal**, a named input the page sets, and converts `queries` (a list of strings) to `hits` (ranked chunk ids with scores), `hits` to a `context` (packed text plus evidence labels) and `context` to an `answer`. Most live components sit on the `hits` row and map hits to hits: they fuse, filter, rerank, cut or expand the list without changing its type.

The compiler checks each edge with one function. A port accepts a source if their types match, with one exception: a `queries` port also accepts a signal, which is how the `q` string reaches the first retriever. Fusion components declare their input as `hits*`, a variadic port that takes any number of hits streams.

```js
const accepts = (port, type) =>
  port.replace('*', '') === type || (port.startsWith('queries') && type === 'signal');
```

The two streams meet in exactly one place, and it is not a data port. A retriever needs a built index, but an index is not something a query run passes along; it is an artifact the build left behind. The `retrieve` definition therefore declares it as a dependency through `uses`, not `in`:

```js
live('retrieve', {
  in: ['queries'], out: 'hits', source: true, lambdas: ['fn'],
  id: p => (p.index ? String(p.index) : 'retrieve'),
  uses: p => (p.index ? ['ix-' + p.index] : []),
  // …
});
```

The compiler turns that into an edge of a third kind. Every edge in the IR carries one of three labels, and the DAG tab draws them differently:

| Edge kind | Comes from | Example | Drawn as |
| --- | --- | --- | --- |
| `data` | An `in` entry | `chunks → ix-text` | solid (dashed when the source is a build node) |
| `artifact` | A `uses` entry | `ix-text → text` | dashed |
| `signal` | A signal in `in`, or a signal a lambda reads | `q → text`, `k → limit` | blue |

The direction rule follows from the phases. A build node may not read a live node, because the build runs before any query exists; the compiler reports `a build node cannot read live node '…'` if one tries. Live nodes read build results only through `uses`.

When wiring is wrong, the compiler names the node, the input and both types. Feeding the query signal straight into a packer, or a packed context into a limit:

```text
1 problem:
  pack: input 'q' gives signal but this port takes hits

2 problems:
  b: input 'p' gives context but this port takes hits
  x: input 'a' gives hits but this port takes context
```

A wrong number of inputs and a retriever pointed at an index that does not exist produce messages of the same shape. The second lists the indexes that do exist, which is usually all the reader needs to fix it:

```text
1 problem:
  b (pack) takes hits but is wired to [a, q]

1 problem:
  a: uses 'ix-nope', which does not exist (indexes: text, meta)
```

### 8.3 Method names generated from the registry

No builder method is written by hand for a component. After the registry is filled, two functions derive the API from it.

`refreshFactories` groups types by the part before the dot and creates one factory per family on `rag`. A family with a bare member and variants becomes a function with properties: `rag.rerank` is a function that makes a `rerank` component, and `rag.rerank.llm` makes a `rerank.llm` component. A family with only variants becomes a plain object: `rag.fuse` has the keys `rrf` and `max`. A few short aliases are added for the build components presets use most: `rag.files`, `rag.markdown`, `rag.sections`, `rag.bm25` and `rag.terms`. Each factory runs the type's `args` normalizer and returns the `{ $c, p }` value seen in chapter 7.

`installFamilies` does the same for the plan builder. For every live family it defines a getter on `PlanSpec.prototype`. Reading `plan.fuse` calls the getter, which builds an object whose `rrf` and `max` methods add a node to *that* plan. Listing the prototype's own properties shows the result: the hand-written methods (`as`, `over`, `signals`, `patch`, `variant`, `use`, `then`, `toJSON`) followed by the generated ones (`retrieve`, `rewrite`, `fuse`, `filter`, `rerank`, `limit`, `expand`, `pack`, `answer`, `agentic`, `map`).

Getters on the prototype, rather than methods attached to each plan, are what make later definitions work. A plan created before `rag.define('diversify', …)` runs still gains a `diversify` method afterwards, because every plan looks up its methods on the same prototype:

```js
const p = rag.plan('x');
rag.define('diversify', { phase: 'live', in: 'hits', out: 'hits', run: (s, ctx, hits) => hits });
typeof p.diversify   // → "function"
```

The corpus builder is different on purpose. Its methods (`from`, `parse`, `chunk`, `enrich`, `index`, `synonyms`, `then`) are fixed, and each takes a component value as an argument: `.chunk(rag.sections({ … }))`, `.index('text', rag.bm25())`. Build stages are few and their order is fixed, so the method names describe the stage and the argument picks the implementation. Live steps are many and freely ordered, so the method name is the component.

### 8.4 `rag.define`: components that chain like built-ins

`rag.define(type, def)` validates a definition, fills in defaults, adds it to the registry and reruns both generators. The defaults are the common case: a live component takes and gives `hits`, a build component takes `docs` and gives `chunks`. The *Your own components* preset defines a step that keeps at most `perDoc` sections from each document:

```js
rag.define('diversify', {
  phase: 'live', in: 'hits', out: 'hits', params: { perDoc: 2 },
  describe: p => `≤ ${p.perDoc} per doc`,
  run: (s, ctx, hits, p) => {
    const seen = new Map();
    return hits.filter(h => {
      const d = (ctx.chunk(h.id) || {}).docId;
      const n = seen.get(d) || 0;
      seen.set(d, n + 1);
      return n < p.perDoc;
    });
  },
});

const plan = rag.plan('custom').over(kit.reports())
  .signals({ q: 'testing and verification', k: 10 })
  .retrieve('text', { k: 60 })
  .diversify({ perDoc: 1 })
  .limit(s => s.k);
```

The new node is indistinguishable from a built-in one in the IR: `{ id: "diversify", t: "diversify", phase: "live", in: ["text"], p: { perDoc: 1 }, out: "hits" }`. Its effect is visible in the results. Without it, the top five for *testing and verification* include two sections of *PBUI Workbench Core*, both headed "7. Verification". With `perDoc: 1`, the fourth and fifth places go to *PBUI Datalab* ("12.1 Gates") and *Agentforum Web* ("Testing and validation").

A build component works the same way. The *Code search* preset defines `chunk.codeBlocks`, whose `map` receives a parsed document and returns one chunk per fenced code block with at least `minLines` lines. The returned objects need only `headingPath`, `text` and any extra fields; `define` wraps them in full chunk records and assigns stable ids, exactly as the built-in chunkers do. Because the type has a dot, it becomes a variant of the existing `chunk` family and is called as `rag.chunk.codeBlocks({ minLines: 3 })`.

`define` checks its input, and its errors say what shape it expected:

```text
component type 'Boost-Recent' should look like 'family' or 'family.variant'
define needs phase: 'build' or 'live'
a live component needs run(s, ctx, input, p)
```

Two properties of defined components need care. First, a component's `run` is ordinary JavaScript in the script, not a stored lambda, so it may close over anything. The cost is that the IR records only the type name and params. A saved IR that mentions `diversify` can run only in a page where the `define` call has run, which is why presets put their definitions at the top of the script. Second, the compiler cannot scan `run` for signal reads the way it scans lambdas (section 9.5). A component that reads a signal inside `run` should say so with `reads`, as the preset's `boost.tags` does with `reads: () => ['boostTag']`, so the DAG shows the signal edge and the engine knows to rerun the node when that signal changes.

### Key points

- Every component type is a registry entry declaring its phase, typed ports, trigger and implementation. The compiler, the builders and the DAG all read the same entry.
- Data flows through two typed chains: files, docs, chunks, index at build time, and signal, queries, hits, context, answer per query. Most live steps map hits to hits.
- The only connection from build to live is a `uses` dependency, which becomes an artifact edge. A build node may never read a live node.
- Wiring errors are reported at compile time with the node, the input and both types named.
- Builder methods are generated from the registry. Live families become prototype getters, so components defined later appear on every plan.
- `rag.define` adds a component that chains like a built-in. Its code lives in the script, so a saved IR needs the definition to run again, and signals read inside `run` must be declared with `reads`.

The components in this chapter took their behaviour from code in the registry. Many presets instead pass a function as a parameter: a rerank lambda, a chunker, `s => s.k`. Those functions end up inside the IR, which is JSON. Chapter 9 explains how.

**Labs: Your own components, Code search.** In *Your own components*, select the `diversify` node in the DAG tab and change `perDoc` from 2 to 1; only the live nodes rerun. Then try wiring a mistake on purpose with an op in the script, such as `{ op: 'wire', node: 'limit', in: ['q'] }`, and read the compiler's message. In *Code search*, search for `protojson UnmarshalOptions` and use the `lang` facet; then change `minLines` to 10 and rebuild to see how many code chunks remain.

## 9. Lambdas that survive serialization

The registry covers the steps the library anticipated. Search is full of steps it did not: a chunker that turns table rows into chunks, a reranker that rewards heading overlap, a result count that follows a slider. The grammar lets you write those steps as functions and pass them as parameters. This chapter explains what happens to a function once it is inside a pipeline, why the workbench stores its source text instead of the function object, and what that costs you as a programmer. The cost is one rule, and most of the chapter is about living with it: **a lambda in a pipeline cannot see the variables around it.**

### 9.1 Functions as source text

A pipeline has to be JSON. Chapter 7 gave the reasons: the workspace saves it to the database, the agent reads it as text, ops edit it, the compiler digests it, and `diffIr` compares two versions of it. A JavaScript function object fits none of these. It cannot be serialized, two functions with the same text are never equal, and a function received from somewhere else cannot be inspected.

So the builder converts every function it finds in a component's parameters into a record holding its source text. The conversion is `lam`:

```js
rag.util.lam((s, ctx, hit) => hit.tokens > s.minTok)
// → { "$fn": "(s, ctx, hit) => hit.tokens > s.minTok" }
```

The text comes from `Function.prototype.toString`, which returns the function's source as written. Some functions have no usable source. A built-in such as `Math.max` prints as `function max() { [native code] }`, and a bound function prints the same way, so `lam` refuses both:

```text
native or bound functions cannot be stored as lambdas; write an arrow function
```

The stored record is what the IR holds. A filter step written as `.filter((s, ctx, hit, p) => hit.tokens > p.minTok, { minTok: 60 })` compiles to a node whose parameters are:

```json
{ "fn": { "$fn": "(s, ctx, hit, p) => hit.tokens > p.minTok" }, "minTok": 60 }
```

When the node runs, `fnOf` compiles the text back into a function and caches it by label and source, so each distinct lambda is compiled once per page. Because the source is data, it can also be produced by something other than a JavaScript function: an op can replace a lambda with a string, `{ op: 'replace', node: 'filter', fn: '(s, ctx, hit) => hit.tokens > 40' }`, which is how the agent edits lambdas.

### 9.2 Closed lambdas: the guard and the free-identifier error

Source text loses the function's closure. Consider a script that defines a threshold and uses it in a filter:

```js
const minTok = 60;
const plan = rag.plan('f').over(kit.reports())
  .signals({ q: 'rebalance law', k: 10 })
  .retrieve('text', { k: 40 })
  .filter((s, ctx, hit) => hit.tokens > minTok);
```

The stored text is `(s, ctx, hit) => hit.tokens > minTok`. When that text is compiled again later, perhaps in a reloaded page or a workspace the agent built, `minTok` does not exist. Ordinary `new Function` compilation would resolve the name against the page's global scope, find nothing, and throw a `ReferenceError` that mentions neither the pipeline nor the node. Worse, if the page happened to have a global with that name, the lambda would silently read it.

The library prevents both outcomes by compiling every lambda inside a scope it controls. In pseudocode, the compiled wrapper is:

```text
// pseudocode of compileSource(src, label)
guard = Proxy where
  has(name)  = name is not in ALLOWED          // claim every name except safe globals
  get(name)  = throw "<label> references free identifier `name` …"
return new Function('__g', 'with (__g) { return (' + src + ') }')(guard)
```

The `with` statement makes every identifier lookup in the lambda body ask the proxy first. The proxy's `has` trap claims every name except a short allow-list of standard globals (`Math`, `JSON`, `Array`, `Object`, `String`, `Number`, `Date`, `RegExp`, `Map`, `Set`, `Promise`, `Intl`, `parseInt`, `console` and a few others). The lambda's own parameters and local variables are resolved before the `with` scope is consulted, so they work normally. Any other name reaches the proxy's `get` trap, which throws an error naming the node and the identifier. Running the plan above gives:

```text
filter lambda 'filter' references free identifier `minTok`. Lambdas are stored as source
and run without their closure: use their arguments, a signal, or rag.bake.
```

The error is raised when the lambda first runs, not when the plan compiles. The compiler does check every lambda's syntax, so a truncated edit is rejected at once (`filter.fn: Unexpected token ')'`), but finding free identifiers statically would need a JavaScript parser and scope analysis, and the guard is exact without one. In practice the gap is small. A live lambda that references a free name fails on the first query: the run's `errors` list carries the message, the trace marks the node `status: "error"`, and the node's input passes through so the rest of the page still renders. A build lambda with the same mistake stops the build, and the build report carries the same message prefixed with the node id, for example `rows: chunk lambda 'rows' references free identifier minRows`.

The rule is the same for every lambda in the system, including the generative UI in Part VIII. It is what makes a pipeline a self-contained value: everything a lambda depends on is either in its arguments or in the IR beside it.

### 9.3 Getting values in: signals, params as the last argument, `rag.bake`

A closed lambda still needs data from outside its body. The grammar provides three channels, each for a different kind of value.

**Params, as the last argument.** A component that calls a lambda passes the node's own parameters as the lambda's final argument. Anything you put in the params object beside the function is therefore visible to it and is stored next to it in the IR. The free-identifier example above becomes:

```js
.filter((s, ctx, hit, p) => hit.tokens > p.minTok, { minTok: 60 })
```

This is the right channel for constants that belong to the step: weights, thresholds, small lookup tables. Because they live in `p`, they can be edited in the DAG tab's node panel or with a `set` op (`{ op: 'set', node: 'filter', path: 'minTok', value: 100 }`) without touching the lambda's text. The *Rerank with a lambda* preset uses four of them: `{ heading: 1.5, code: 0, minTokens: 60, shortPenalty: 0.6 }`.

**Signals.** Every live lambda receives the run's signals as its first argument, `s`. A value that the page or the user changes at run time belongs there: the query, `k`, a filter object, a slider position. Declare it with `.signals({ minTok: 60 })` and read it as `s.minTok`. Chapter 12 shows that the engine reruns only the nodes whose signal reads changed, so moving a slider bound to `minTok` reruns the filter and what follows it, not the retriever.

**Bakes.** Some values depend on the corpus and should be computed once after it is built, not hand-written and not recomputed per query. `rag.bake(fn)` marks a parameter as such a value. Its source is stored as `{ "$bake": "…" }`, and after the build the engine calls it once with `{ docs, chunks, tokens, slug }` and substitutes the result. A rerank step that halves the score of hits from the two largest documents can compute that list from the chunks:

```js
.rerank((s, ctx, hits, p) => hits
    .map(h => ({ ...h, score: h.score * (p.common.includes(h.docId) ? 0.5 : 1) }))
    .sort((x, y) => y.score - x.score),
  { common: rag.bake(ctx => {
      const n = {};
      for (const c of ctx.chunks) n[c.docId] = (n[c.docId] || 0) + 1;
      return Object.entries(n).sort((a, b) => b[1] - a[1]).slice(0, 2).map(e => e[0]);
    }) })
```

The IR stores the bake's source. After a build, `engine.baked(node)` shows the substituted value the lambda will see:

```json
{ "common": [ "project-report-pbui-workbench-core-a-headless-engine-a-pure-plan",
              "agent-runtime-spec-portable-eval-agent" ] }
```

The same mechanism builds query-expansion tables. A synonyms node given `rag.bake(ctx => …)` can read every document's `aliases` frontmatter and map each alias to the document title, so a query for *ttc tier b* expands to the full report name. The baked table for the bundled reports starts with `lca`, `ttc customer clustering`, `thetreecenter.com segmentation`, `coinvault chatgpt mcp acceptance`.

| Channel | Set where | Changes when | Use for |
| --- | --- | --- | --- |
| Params (`p`, last argument) | Next to the lambda in the IR | An op or a DAG edit changes it | Constants of the step: weights, thresholds |
| Signals (`s`, first argument) | `.signals({ … })`, then the page | Every run, from the UI | Values the user controls: query, k, filters, sliders |
| Bakes (`rag.bake`) | Computed after the build | The corpus changes | Values derived from the documents: alias tables, document sizes |

### 9.4 Four phases: build callbacks, build slots, bakes, live slots

A function in a pipeline script can run at four different times. Knowing which one applies tells you what arguments it gets, whether it is stored, and how often it runs.

| Form | Runs | Receives | Stored in the IR |
| --- | --- | --- | --- |
| Build callback, e.g. `rag.graph(id, g => …)`, `plan.variant(id, p => …)`, `gui` view builders | Once, while the spec is being built | A builder | No |
| Build slot, e.g. `.chunk('rows', (doc, ctx, p) => …)`, `.enrich(name, (chunk, ctx, p) => …)` | At build time, once per input item, cached by digest | The item, a build context, params | Yes, as `$fn` |
| Bake, `rag.bake(ctx => …)` | Once after the build | `{ docs, chunks, tokens, slug }` | Yes, as `$bake` |
| Live slot, e.g. `.filter((s, ctx, hit, p) => …)`, `.limit(s => s.k)` | Per query | Signals, a live context, the input, params | Yes, as `$fn` |

A build callback is ordinary JavaScript. It runs inside your script, may close over anything, and leaves only its result behind; that is why `rag.graph`'s callback can use local constants freely. The other three forms are stored and run closed.

A build slot runs once per item of its input. The *Table rows as chunks* preset's chunker receives each parsed document and returns one chunk per table row:

```js
.chunk('rows', (doc, ctx, p) => doc.sections.flatMap(sec => sec.blocks
  .filter(b => b.kind === 'table' && b.rows.length >= p.minRows)
  .flatMap(t => t.rows.map(row => ({
    kind: 'row',
    headingPath: [...sec.path, row[0]],
    text: t.header.map((h, j) => `${h}: ${row[j] ?? ''}`).join('\n'),
  })))), { minRows: 2 })
```

Over the eleven reports it ran 11 times and produced 350 row chunks. One of them is the row that answers *which HTTP status does ErrConflict map to*: heading path `The HTTP server › Adapter discipline › ErrConflict`, text `Service sentinel: ErrConflict / HTTP: 409 / Error.code: conflict`. Raising `minRows` to 5 changes the node's digest, reruns the slot and leaves 293 chunks.

Live slots come in two kinds, and the difference is the `lambdas` field of the registry entry from chapter 8. A parameter listed there, such as `fn` on `filter` or `rerank`, is handed to the component as a function, which calls it with `(s, ctx, input, p)`. Any other parameter that holds a lambda is a **value slot**: the engine evaluates it with `(s, ctx)` just before the node runs and passes the result as an ordinary value. `limit(s => s.k)` is a value slot. Its stored form is `{ k: { "$fn": "s => s.k" } }`, and the `limit` component sees `p.k` as a number. Any parameter of any live component can be made dynamic this way, without the component's author having planned for it.

### 9.5 Which signals a lambda reads: static scan and runtime tracking

The engine needs to know which signals each live node depends on, for two reasons. The DAG draws a signal edge from `k` to `limit`, and Part VII's dry runs report which nodes an edit will rerun. Both must work without running anything. Separately, the session that executes queries must never reuse a node's cached output after a signal it read has changed. That must be exact.

The library uses two mechanisms, one for each need. At compile time, `signalReads` scans each live lambda's source with a regular expression. It finds the name of the first parameter and collects every `name.prop`, `name?.prop` and `name['prop']` that follows; if the first parameter is a destructuring pattern, it collects the destructured names. The results become the node's `reads` list and its signal edges:

```js
signalReads('(s, ctx, hit) => hit.tokens > s.minTok')          // → ["minTok"]
signalReads('({ q, k }) => q.length + k')                       // → ["q", "k"]
signalReads("(sig) => sig['boostTag'] + sig?.k")                // → ["boostTag", "k"]
signalReads('(s) => { const key = "k"; return s[key]; }')       // → []
```

The last line shows the limit of a textual scan: a computed property name is invisible to it. That is acceptable for drawing, but not for caching. So at run time the session passes each node a proxy over the signals object that records every property read, with the value it had. Before reusing a node's previous output, the session checks that every recorded read still has the same value. A plan whose limit reads a signal through a computed key demonstrates the difference:

```js
rag.plan('d').over(corpus).signals({ q: 'rebalance law', k: 10, field: 'k' })
  .retrieve('text', { k: 20 })
  .limit(s => { const key = s.field; return s[key]; });
```

The static scan finds only `["field"]`. Running the plan twice, with `k` changed from 10 to 3 between runs, shows the session getting it right anyway:

```text
run 1:  text ran 20    limit ran 10
run 2:  text cached 20  limit ran 3
recorded reads for limit: { field: "k", k: 3 }
```

The same tracking is what makes ordinary plans cheap to rerun. In the filter plan from section 9.3, with `minTok` and `k` as signals, changing only `k` reuses the retriever and the filter and reruns only `limit`; changing `minTok` reruns the filter and `limit` and still reuses the retriever. Chapter 12 builds the session memo on this.

### Key points

- Functions in pipeline parameters are stored as source text (`$fn`, `$bake`), so the IR stays JSON that can be saved, diffed, digested and edited by ops.
- Stored lambdas run closed. A guard proxy turns any free identifier into an error that names the node and the identifier. The compiler checks syntax; free identifiers are reported on first run.
- Values reach a closed lambda through three channels: params as the last argument for step constants, signals as the first argument for values the user changes, and `rag.bake` for values derived from the corpus.
- Functions run in four phases. Build callbacks run once and are not stored; build slots run per item at build time; bakes run once after the build; live slots run per query.
- A parameter listed in a component's `lambdas` is called by the component. Any other lambda parameter is a value slot evaluated with `(s, ctx)` before the node runs.
- A static scan of the source draws signal edges; runtime tracking of actual reads keeps the cache correct even when the scan misses a read.

With components, ports and lambdas in place, a script can describe any pipeline the workbench runs. Chapter 10 follows what happens when that description is compiled: which checks run, in what order the nodes end up, and how each node gets a name derived from what it computes.

**Labs: Rerank with a lambda, Table rows as chunks.** In *Rerank with a lambda*, select the `rerank` node in the DAG tab: its lambda source and its four params are shown separately. Change `heading` from 1.5 to 3 and compare with the `heading-x3` variant on the Evals tab. Then add `const boost = 2;` above the plan and use `boost` inside the lambda; search once and read the free-identifier error in the trace. In *Table rows as chunks*, search for *which HTTP status does ErrConflict map to*, then change `minRows` to 5 and watch the Build passes list rebuild only the row chunker and its index.

## 10. Compiling to an IR

A spec is convenient to write but awkward to execute. Its corpus and plan halves live in separate objects, its signals are a side table, some of its edits are still pending as ops, and nothing has checked that its wiring is legal. `rag.compile` turns a spec into an **IR** (intermediate representation): one flat, checked, ordered graph that every later stage reads. The engine builds and runs the IR, the workspace edits it, the evals score it, the DAG tab draws it and the agent reads it. This chapter describes that graph, the checks that guard it, the order its nodes are stored in, and the digests that name its build nodes by what they compute.

### 10.1 The IR: nodes, edges, signals, outputs

Here is the compiled IR of the *Minimal search* plan with one more step, `.limit(s => s.k)`, so that it contains a signal read. Node records are shortened to one line each; nothing has been removed.

```json
{ "kind": "rag", "version": 1, "id": "minimal", "corpus": "reports",
  "signals": { "q": "rebalance law", "k": 10 },
  "nodes": [
    { "id": "files",   "t": "source.files",   "phase": "build",  "in": [],         "p": { "glob": "*.md" },       "out": "files",  "uses": [], "reads": [] },
    { "id": "q",       "t": "signal",         "phase": "signal", "in": [],         "p": {},                       "out": "signal" },
    { "id": "k",       "t": "signal",         "phase": "signal", "in": [],         "p": {},                       "out": "signal" },
    { "id": "parse",   "t": "parse.markdown", "phase": "build",  "in": ["files"],  "p": {},                       "out": "docs",   "uses": [], "reads": [] },
    { "id": "chunks",  "t": "chunk.sections", "phase": "build",  "in": ["parse"],  "p": { "maxTokens": 450 },    "out": "chunks", "uses": [], "reads": [] },
    { "id": "ix-text", "t": "index.bm25",     "phase": "build",  "in": ["chunks"], "p": { "name": "text" },     "out": "index",  "uses": [], "reads": [] },
    { "id": "text",    "t": "retrieve",       "phase": "live",   "in": ["q"],      "p": { "index": "text", "k": 20 }, "out": "hits", "uses": ["ix-text"], "reads": [] },
    { "id": "limit",   "t": "limit",          "phase": "live",   "in": ["text"],   "p": { "k": { "$fn": "s => s.k" } }, "out": "hits", "uses": [], "reads": ["k"] } ],
  "edges": [ ["files","parse","data"], ["parse","chunks","data"], ["chunks","ix-text","data"],
             ["q","text","signal"], ["ix-text","text","artifact"], ["text","limit","data"], ["k","limit","signal"] ],
  "outputs": { "hits": "limit" },
  "pinned": [],
  "digest": "02ef3933d734f5" }
```

Compared with the spec, four things have changed.

- **The two halves are one list.** The corpus nodes and the plan nodes share one `nodes` array, and the plan's reference to its corpus survives only as the `corpus` field.
- **Signals are nodes.** Each signal becomes a node of type `signal` with phase `signal`, so an edge can start from it and the DAG can draw it. The `signals` object keeps the default values.
- **Each node carries derived fields.** `out` is the node's output type from the registry, `uses` lists its artifact dependencies, and `reads` lists the signals its lambdas read (section 9.5). These are computed, not written by hand, and they are recomputed on every compile.
- **Edges are explicit.** Each edge is a triple `[from, to, kind]`, where the kind is `data`, `signal` or `artifact` as defined in section 8.2. Data and signal edges come from `in`; artifact edges come from `uses`; the `k → limit` signal edge comes from the lambda's reads.

The IR also carries methods, attached as non-enumerable properties so they do not appear in its JSON: `ir.node(id)`, `ir.downstream(id)`, `ir.upstream(id)`, `ir.summary()`, `ir.mermaid()` and `ir.describe(id)`. For this graph, `ir.downstream('chunks')` is `["ix-text", "text", "limit"]`, which is exactly the set of nodes a change to the chunker can affect.

### 10.2 What the checker rejects, with the real error messages

Compilation happens in `finalize`, which walks the nodes once, collects every problem it finds, and throws a single error listing all of them. It does not stop at the first problem. An agent editing a pipeline, or a person reading the error panel, can fix everything in one pass instead of discovering problems one compile at a time.

| Check | Example message |
| --- | --- |
| Node ids are well formed | `node id 'ix.text' must start with a letter and use letters, digits, _ or - (no dots)` |
| Node ids are unique | `duplicate node id 'a'` |
| The component type exists | `x: unknown component 'rerank.semantic'` |
| The number of inputs matches the ports | `b (pack) takes hits but is wired to [a, q]` |
| Every input exists | `limit: input 'rerank' does not exist` |
| Each input's type fits its port | `limit: input 'text' gives hits but this port takes context` |
| A build node reads no live node | `c: a build node cannot read live node 'x'` |
| Every `uses` target exists | `a: uses 'ix-nope', which does not exist (indexes: text, meta)` |
| Every lambda parses | `filter.fn: filter.fn: Unexpected token ')'` |
| The graph is acyclic | `the graph has a cycle through: text, limit` |
| Every output points at a node | `output hits points at missing node 'x'` |

The error message starts with a count. Rewiring the retriever of the minimal plan to read from `limit`, which reads from the retriever, breaks two rules at once, and both are reported:

```text
after ops: 2 problems:
  text: input 'limit' gives hits but this port takes queries
  the graph has a cycle through: text, limit
```

The prefix `after ops:` says the problem appeared after a list of ops was applied, which is how the workspace reports edits that would break the graph. Ops can also fail before compilation, when the op itself makes no sense; those messages name the op's position and kind: `op 1 (insert): node 'text' already exists`, `op 1 (set): set needs a path into the node params, e.g. "k" or "fields.title"`, `op 1 (move): unknown op 'move'. Ops: set unset insert remove replace wire signal trigger output rename`.

Two limits of the checker are worth knowing. It checks lambda syntax but not free identifiers, which surface on first run (section 9.2). And a duplicate id currently produces a second, empty line, `the graph has a cycle through:` with no node names, because the ordering step cannot place two nodes with the same id. Fixing the duplicate removes both lines.

### 10.3 Topological order and derived outputs

The nodes array in the IR is in **topological order**: every node appears after all the nodes it has an edge from. The engine relies on this. A build or a query run is a single pass over the array, and when a node's turn comes, its inputs are already computed.

The ordering is Kahn's algorithm. Count each node's incoming edges, start with the nodes that have none, and repeatedly emit a node and decrement the counts of its successors, emitting each successor when its count reaches zero. Ties are broken by the order nodes were declared. That is why the minimal plan's summary reads:

```text
files:source.files → q:signal → k:signal → parse:parse.markdown → chunks:chunk.sections
  → ix-text:index.bm25 → text:retrieve → limit:limit
```

The signals appear second and third because they have no inputs and were declared before `parse` became ready. If the algorithm ends with nodes it never emitted, those nodes lie on a cycle, and the checker lists them. The same pass therefore detects cycles at no extra cost.

After ordering, the compiler decides the plan's **outputs**: which node's result the page shows as *the* hits, *the* context and *the* answer. You can pin an output explicitly, with `g.output` in the graph form or an `output` op. Otherwise the compiler derives it:

- `answer` is the last node in order whose output type is `answer`.
- `context` is the last node whose output type is `context`.
- `hits` is the last rerank node if there is one; otherwise the input of the context node; otherwise the last node producing hits.

The hits rule needs explaining. The list a user should see is the best-ordered list before the pipeline starts cutting and expanding it for the answer. A rerank step is the most deliberate ordering in a plan, so its output wins. Without one, what went into the packer is what the answer was built from. A full hybrid plan shows the rule in action:

```text
… text:retrieve → meta:retrieve → fuse:fuse.rrf → rerank:rerank.llm! → limit:limit → pack:pack → answer:answer!

outputs: { "answer": "answer", "context": "pack", "hits": "rerank" }
after { op: 'remove', node: 'rerank' }: { "answer": "answer", "context": "pack", "hits": "limit" }
```

The `!` after a node in the summary marks a `submit` trigger: that node waits for Enter (chapter 12). Pinned outputs are listed in the IR's `pinned` array and survive edits; derived ones are recomputed every time the graph changes.

### 10.4 Digests: naming a node by what it would compute

The last thing compilation produces is a set of **digests**. A digest is a short hash that identifies a piece of computation by its inputs. The engine computes one per build node, and the compiler computes one for the whole IR. The build-node digests are what chapter 11's incremental builds are keyed on, so it is worth seeing exactly what goes into them.

In pseudocode, the digest of a build node is:

```text
// pseudocode of engine.digests(ir), in topological order
digest(n) = hash([ n.t, n.p, [digest(i) for i in n.in], fileContents(n) ])
fileContents(n) = hashes of every file's name and text, if n is source.files; otherwise ''
hash(v) = cyrb53(stable(v))   // stable = JSON with object keys sorted
```

Three properties follow from that definition, and each has a purpose.

- **A digest covers everything upstream.** Each node's digest includes its inputs' digests, so a change to a parameter changes the digest of that node and of every build node downstream of it, and of no other.
- **File contents count.** The `source.files` node hashes each file's name and text. Uploading a new report changes the first digest and therefore every build digest after it.
- **The node id does not count.** The digest says what the node computes, not what it is called. Renaming a node leaves its digest, and its cached result, intact.

Changing the chunker's `maxTokens` from 450 to 200 shows the first property:

```text
node      before          after
files     967fb737993c00  967fb737993c00  same
parse     d8c4fd64af9808  d8c4fd64af9808  same
chunks    7f4be2a1d17826  7b7903cada3901  changed
ix-text   b7e8f0afe8c72b  670946ba2ee997  changed
```

The build reports that follow are what the digests buy. The first build computed everything: 11 files, 11 parsed documents, 462 chunks and an index, in about 220 ms, most of it chunking and indexing. The second build reused `files` and `parse` and recomputed only `chunks` (now 873 chunks) and `ix-text`. Renaming `chunks` to `sections` instead produces the same four digests as the original, and the build after the rename reuses all four nodes.

Live nodes have no build digest. Changing the retriever's `k` from 20 to 40 leaves every build digest unchanged, so no rebuild happens; the change affects only query runs, which chapter 12 caches differently.

The whole-IR digest, the `digest` field in the JSON above, is a hash of every node's id, type, inputs, params and trigger, plus the signals and outputs. Unlike the build digests, it changes on a rename and on any live edit: the three versions above (original, `maxTokens: 200`, `k: 40`) have IR digests `02ef3933d734f5`, `92d021d6f7f3e2` and `164e199c974419`. Eval reports record it as `irDigest`, so a score can always be traced to the exact graph that produced it.

| Digest | Covers | Changes on | Used for |
| --- | --- | --- | --- |
| Build node | Type, params, input digests, file contents | Upstream build edits, file changes | Reusing built chunks and indexes (chapter 11) |
| Whole IR | Ids, types, wiring, params, triggers, signals, outputs | Any edit, including renames and live params | Tagging eval reports and history |

The hash function is cyrb53, a fast non-cryptographic 53-bit hash, printed as 14 hex digits. It is applied to a *stable* serialization in which object keys are sorted, so `{ k: 20, index: 'text' }` and `{ index: 'text', k: 20 }` hash the same. A non-cryptographic hash is adequate because digests here only need to distinguish a user's own pipeline versions from each other, not resist an adversary.

### 10.5 Reading the DAG tab

The DAG tab in the inspector draws the current IR. Everything on it is one of the fields described in this chapter.

The layout is by layer. Each node is placed one column to the right of its furthest predecessor, so every edge points rightwards, and live nodes are pulled as far right as their consumers allow. The columns are headed *build · per corpus* and *live · per query*. Within a column, build nodes come first, then signals, then live nodes.

| On the tab | Comes from |
| --- | --- |
| Node name and small type label | `id` and `t` |
| Solid edge | A `data` edge between live nodes |
| Dashed grey edge | A `data` edge out of a build node, or an `artifact` edge |
| Blue edge | A `signal` edge |
| Dotted green edge | A data edge that carried at least one item in the last run |
| `llm` tag | The node's trigger is `submit` (it waits for Enter and may spend Claude calls) |
| `λ` tag | A parameter holds a `$fn` or `$bake` |
| Number on a build node | Items it produced in the last build |
| `in→out` on a live node | Counts from the last run's trace; `skip` if the node was skipped |

Selecting a node opens its detail panel: its params, its lambda source, its last build or run statistics. Editing a param there does not change the script; it applies a `set` op to the workspace, which recompiles the IR, recomputes the digests and rebuilds only the dirty nodes. That loop, edit an op, recompile, rebuild the difference, is the subject of Parts IV and VII. The same graph is available as text with `ir.mermaid()`, which draws build nodes as parallelograms, signals as circles and live nodes as rectangles, with `-->` for data, `-.->` for artifact and `-.-` for signal edges.

### Key points

- The IR is one flat graph: all corpus and plan nodes in one list, signals as nodes, and explicit edges labelled `data`, `signal` or `artifact`. Derived fields (`out`, `uses`, `reads`) are recomputed on every compile.
- The checker collects every problem and throws one error that lists them all, each naming the node, the input and the types involved. Free identifiers in lambdas are the one class of error it leaves to run time.
- Nodes are stored in topological order, so a build or a run is a single pass. The ordering pass also detects cycles.
- Outputs are pinned or derived. The derived hits output prefers a rerank node, then the packer's input, then the last hits node.
- A build node's digest hashes its type, params, input digests and, for the source, file contents, but not its id. It changes exactly when the node's result would change.
- The whole-IR digest changes on any edit and ties eval reports to the graph that produced them.

Part III has described a pipeline as a value and shown how it becomes a checked graph with names derived from content. Part IV runs that graph: chapter 11 shows the engine using build digests to rebuild only what changed, and chapter 12 shows the session caching live nodes by input identity and recorded signal reads.

**Lab: Graph form.** Load *Graph form* and open the IR tab. Find the five signal edges: `q` into `lex`, `tags` and `answer`, `filters` into `filter`, and `k` into `top`, the last one coming from the lambda `s => s.k`. Add a node to the graph, `g.node('n2', rag.retrieve('text', { k: 10 }), { from: q })`, without listing it in the fuse node's `from`. The plan still compiles, because a dangling retriever breaks no rule, but `n2` now runs on every query and nothing reads its hits. List it in `from` and check that `fuse` has three inputs. Finally, in the DAG tab, select `chunks`, change `maxTokens`, and watch the build nodes downstream of it turn dirty while `files` and `parse` stay reused.
