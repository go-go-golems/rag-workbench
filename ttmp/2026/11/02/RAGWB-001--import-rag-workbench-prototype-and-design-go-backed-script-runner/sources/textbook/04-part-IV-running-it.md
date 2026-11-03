# Part IV · Running it

The engine runs an IR in two phases: a build that turns files into indexes once, and query runs that happen on every keystroke. Both avoid repeated work by recording what each node's output depended on.

## 11. Builds that remember

This chapter explains how the engine decides what to compute when a pipeline changes. The answer rests on one idea: every build node is named by a digest of what it would compute, and the engine keeps every output it has computed under that name. Once that is in place, an edit to the chunker rebuilds the chunks and the indexes after them, and nothing else; switching back to the previous setting costs nothing; and a report you upload recomputes the parse of that one file. By the end of the chapter you should be able to predict, before pressing Build, which nodes a change will recompute and which it will reuse.

All output in this chapter comes from running the real `rag.js` in node over the eleven bundled reports. Timings are from one machine and will differ on yours; counts and digests will not.

### 11.1 The build phase and the live phase

Every node in a compiled IR has a phase. Build nodes turn files into documents, documents into chunks, and chunks into indexes. Live nodes take a query and turn it into hits, a context and an answer. The engine runs the two phases at different times and for different reasons. A build runs when the corpus or a build parameter changes. A live run happens on every keystroke.

Here is the IR for the smallest useful pipeline, the corpus from `kit.reports()` with one retriever on top:

```js
const corpus = rag.corpus('reports').from(rag.files('*.md')).parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  .index('text', rag.bm25({ fields: { title: 2, headingPath: 3, callout: 2, text: 1 } }))
  .index('meta', rag.terms({ keys: ['type', 'tags', 'status', 'aliases'] }));
const plan = rag.plan('chunks').over(corpus).signals({ q: 'rebalance law', k: 8 }).retrieve('text', { k: 30 });
rag.compile(plan).summary();
```

```text
files:source.files → q:signal → k:signal → parse:parse.markdown → chunks:chunk.sections
  → ix-text:index.bm25 → ix-meta:index.terms → text:retrieve
```

Five of these nodes are build nodes (`files`, `parse`, `chunks`, `ix-text`, `ix-meta`), two are signals, and one, `text`, is live. The split exists because the two halves have very different costs. On the first build, parsing eleven files took 36.5 ms, sectioning them into 462 chunks took 75.4 ms, and building the BM25 index took 101.5 ms. A query against the finished index took about one millisecond. If each keystroke rebuilt the index, typing would be two hundred times slower than it needs to be.

|  | Build phase | Live phase |
| --- | --- | --- |
| Inputs | files, build params | signals (`q`, `k`, filters, sliders) and build outputs |
| Outputs | `files`, `docs`, `chunks`, `index`, `value` | `queries`, `hits`, `context`, `answer` |
| Runs when | the corpus or a build param changes | the query or another signal changes |
| Remembered by | digest, across edits (this chapter) | per node, across runs (chapter 12) |
| Typical cost | 100–250 ms, or Claude calls | 0.1–30 ms, or Claude calls on Enter |

Live nodes never call the build. They read its results through the run context: `ctx.index('text')`, `ctx.chunk(id)`, `ctx.neighbors(id)`. The engine assembles those from the stored build outputs with `engine.artifacts(ir)`, which returns `null` if any build node of the IR has not been computed yet. A session that finds no artifacts builds first; that is why the very first query after loading a preset is slower than the second.

### 11.2 The digest chain: type, params, inputs, file contents

To reuse work, the engine needs a name for each output that changes exactly when the output would change. The output itself is no use for this, because computing it is the cost being avoided. The engine instead names a node by everything its output depends on. This is the complete function:

```js
digests(ir) {
  const out = new Map();
  for (const n of ir.nodes) {
    if (n.phase !== 'build') continue;
    let extra = '';
    if (n.t === 'source.files') extra = this.files().map(f => this.fileDigest(f)).join(',');
    out.set(n.id, hash([n.t, n.p, n.in.map(i => out.get(i) || i), extra]));
  }
  return out;
}
```

A node's digest is the hash of four things: its component type, its params, the digests of its inputs, and, for the source node only, a hash of each file's name and text. The nodes are visited in topological order (chapter 10), so an input's digest is always known before the node that reads it. `hash` is a 53-bit string hash over a canonical serialization, `stable`, which sorts object keys, so `{ depth: 3, maxTokens: 450 }` and `{ maxTokens: 450, depth: 3 }` produce the same digest. Lambdas take part through their source text.

The digest therefore names a node by what it would compute rather than by what it did compute. You can check this by recomputing one by hand. For the `chunks` node of the pipeline above:

```js
const d = engine.digests(ir), n = ir.node('chunks');
// n = { t: 'chunk.sections', p: { depth: 3, maxTokens: 450 }, in: ['parse'] }
rag.util.hash([n.t, n.p, n.in.map(i => d.get(i)), '']);   // 'a9202e4e3c054c'
d.get('chunks');                                         // 'a9202e4e3c054c'
```

Because each digest includes its inputs' digests, a change anywhere propagates down the edges and never up them. A new `maxTokens` changes the digest of `chunks`, which changes the input list of both indexes, which changes theirs. The digest of `parse` does not mention `chunks` at all, so it stays the same.

The most common misunderstanding is to read the digest as a hash of the node's output. It is not, and the difference matters in one place, which section 11.5 comes back to: two different outputs can exist under one digest if the engine ever stores something other than the full result of the computation the digest names.

### 11.3 The build memo and the dirty set

The engine keeps a map from digest to output, `engine.memo`. Building a node means: compute its digest; if the memo has it, reuse the stored value; otherwise compute the value and store it. `engine.dirty(ir)` lists the build nodes whose digests are not in the memo, which is exactly the set a build would compute.

Here is a sequence of four builds on one engine, with the per-node report each returns (`rep.nodes`). The first builds everything. The second builds the same IR again. The third applies one op, `{ op: 'set', node: 'chunks', path: 'maxTokens', value: 200 }`. The fourth builds the original IR once more.

```text
dirty before [ 'files', 'parse', 'chunks', 'ix-text', 'ix-meta' ]

# first build ok true ms 218.8
   files    source.files    967fb737993c00 computed count 11
   parse    parse.markdown  d8c4fd64af9808 computed count 11
   chunks   chunk.sections  ebe4a56ec01071 computed count 462
   ix-text  index.bm25      ed7fac9a111070 computed count 462
   ix-meta  index.terms     5fb5ef38ae6f1b computed count 462

# same IR again ok true ms 0.3            (every node: reused)

dirty after set maxTokens [ 'chunks', 'ix-text', 'ix-meta' ]
# maxTokens 200 ok true ms 119.5
   files    source.files    967fb737993c00 reused   count 11
   parse    parse.markdown  d8c4fd64af9808 reused   count 11
   chunks   chunk.sections  df3498bd275275 computed count 873
   ix-text  index.bm25      c47064c948bf04 computed count 873
   ix-meta  index.terms     11505f6e166c2b computed count 873

# back to 450 ok true ms 0.2              (every node: reused)
```

&#91;embedded content: digest chain · two builds, one changed param\]

Three properties of this design show up in the output.

- An edit costs what is downstream of it, not what is in the pipeline. Halving the chunk size reused the parse and spent 119.5 ms on chunks and indexes, against 218.8 ms for the full build.
- Undo is free. Going back to 450 tokens found the original digests still in the memo and took 0.2 ms. This is what makes the variant comparisons of chapter 20 cheap: four chunking variants cost four chunk builds once, and every later comparison between them is a lookup.
- The dirty set is a prediction you can read before spending anything. The workbench shows it after every edit, and the agent's dry runs (chapter 21) report it before an op list is applied.

The memo is never evicted during a session. Each entry holds a full output, so a long session of chunking experiments holds every chunk list and index it ever built. For eleven reports this is a few megabytes; for a corpus a hundred times larger it would need an eviction policy, which the engine does not have.

### 11.4 Map components and the per-item cache, in memory and in the database

The memo works at the granularity of a node, which is too coarse for one common edit: changing a single file. Every file feeds the source node, so any edit gives `files` a new digest, and with it every node downstream. Without something finer, adding one paragraph to one report would reparse all eleven.

The finer mechanism belongs to map components. A component registered with `map` instead of `all` processes its input one item at a time: `parse.markdown` maps over files, `chunk.sections` and `chunk` map over documents, `enrich` and `enrich.llm` map over chunks. Index builders use `all`, because an index needs every chunk at once. For a map component the engine keys each item separately:

```js
const fnKey = hash([n.t, p]);                // component type and params
const key = fnKey + ':' + hash(item);       // ...and this one item's content
if (this.items.has(key)) { results[i] = this.items.get(key); st.cached++; continue; }
```

The item key does not mention the node id or its inputs' digests, only the transformation and the item. Here is the build after appending an `## Addendum` section to one of the eleven reports:

```text
dirty after file edit [ 'files', 'parse', 'chunks', 'ix-text', 'ix-meta' ]
# one file edited ok true ms 113.7
   files    source.files    4d202e7d0e1ae0 computed count 11  computed 0  cached 0
   parse    parse.markdown  c5b18768ffabfb computed count 11  computed 1  cached 10
   chunks   chunk.sections  a9202e4e3c054c computed count 463 computed 1  cached 10
   ix-text  index.bm25      95a5b278407bc9 computed count 463
   ix-meta  index.terms     e2554e8611b322 computed count 463
```

Every node is dirty, as the digest chain requires, but the parse computed one document and took ten from the item cache, and the chunker did the same. The chunk count went from 462 to 463: the new section. The two indexes were rebuilt from scratch, because `all` components have no items to reuse, and they account for most of the 113.7 ms.

The item cache lives in memory on the engine. For asynchronous components, the ones that may call Claude, the engine also consults `env.cache`, an object with `get(key)` and `set(key, value)`. In the workbench that object writes to the `cache` collection of the user's database document, so an enrichment computed yesterday is found again today. The effect is visible with two engines that share one cache, the second standing in for a reloaded page:

```text
# A: build({calls:true})                     enrich_llm  computed 11  cached 0   calls 11
# B: fresh engine, same db cache             enrich_llm  computed 0   cached 11  calls 0
```

Engine B had an empty memo and an empty item cache, so it reparsed and rechunked locally, which takes milliseconds. The eleven Claude calls it would have needed were all answered from the database.

### 11.5 Builds that cost Claude calls: budgets, deferral and `pending()`

Some build nodes call Claude. The engine recognizes them with one predicate:

```js
function costsCalls(n) {
  const def = REG.get(n.t) || {};
  if (def.phase !== 'build') return false;
  return !!def.llm || Object.values(n.p || {}).some(v => isLambda(v) && /\bsample\b/.test(v.$fn));
}
```

A node costs calls if its component is marked `llm: true`, as `enrich.llm` is, or if one of its lambdas mentions `sample`. The second test reads source text, so it can be fooled by a lambda that mentions the word without calling it; it errs toward caution.

A build that spends the user's Claude usage should not start because someone changed a chunk size. The workbench therefore configures the engine with `buildCalls: 'explicit'`. In that mode an ordinary build still runs every node, but a costly map component does not call Claude for items it has not seen; it passes each such item through unchanged and counts it as deferred. The Doc cards preset shows this. Its corpus adds a card per document and enriches the cards with a summary, questions and keywords:

```js
.chunk('cards', (doc, ctx) => [{ kind: 'card', headingPath: ['Document card'],
  text: [doc.title, ...doc.sections.filter(s => s.level === 2).map(s => s.title)].join('\n') }])
.enrich('llm', rag.enrich.llm({ tier: 'quick' }), { from: 'cards' })
.index('cards', rag.bm25({ fields: { title: 2, text: 1, 'llm.summary': 2, 'llm.questions': 3, 'llm.keywords': 3 } }), { from: 'enrich_llm' })
```

The build reports below come from a node script with a mock sampler standing in for Claude; the call counts are real, the enrichment text is not.

```text
# A: build, calls not allowed ok true calls 0
   cards       computed count 11  computed 11
   enrich_llm  computed count 11  computed 0  deferred 11
   ix-cards    computed count 11
pending [ { node: 'enrich_llm', items: 11 } ]   dirty [ 'enrich_llm' ]   artifacts? true
```

The deferred output is stored in a separate map, `engine.partial`, not in the memo. That is why `enrich_llm` is still dirty, and why `engine.pending(ir)` can list it with the number of items still waiting. It is also why the pipeline works at once: `artifacts()` accepts partial outputs, so the cards index exists and routes queries lexically on titles and headings. The workbench shows the pending count in its status line, and the Build button calls `build(ir, { calls: true })`:

```text
# A: build({calls:true}) ok true calls 11
   enrich_llm  computed count 11  computed 11  cached 0
progress 1/11 2/11 3/11 4/11 5/11 6/11 7/11 8/11 9/11 10/11 11/11
pending []
```

The `onProgress` callback receives one event per item, which the page turns into “Building enrich\_llm 7/11”. A component can also be given a per-build budget, `budget: { calls: 4 }`. Items past the budget are skipped the same way deferred items are, so each press of Build advances the enrichment by at most four calls and the cache keeps what was already paid for:

```text
# B: new prompt, budget 4     enrich_llm  computed 4  cached 0  skipped 7    pending: 7 items
# B: again with budget 4      enrich_llm  computed 4  cached 4  skipped 3
```

A build also accepts an `AbortSignal`. When it fires, the map worker throws before its next item, the report comes back with `ok: false` and `errors: ['enrich_llm: build stopped']`, and the items finished before the abort stay in the item cache and the database.

There is one gap in this bookkeeping, and it is instructive because it follows directly from section 11.2. The deferred build stored the cards index in the memo under its digest, and that digest is computed from the digest of `enrich_llm`, which is the same whether the enrichment is partial or complete. When Build later completes the enrichment, the index's digest has not changed, so the engine reuses the index built over the unenriched cards. In the report above, `ix-cards` shows as reused after the full build. A query for a word that appears only in the enrichment demonstrates it:

```text
after explicit, then calls: 0 hits
direct full build:         5 hits
```

The rule the design needs is that a digest must name one output. A partial output is a different output, so anything computed from it needs a different name, for example the input digest with a marker appended while the input is partial. `engine.artifacts()` already applies this rule to its own cache key, appending `~` for partial nodes; the build loop does not yet apply it to downstream digests. Until it does, rebuilding after a completed enrichment requires a change to the index node, or a fresh engine.

The key points of the chapter:

- Every build node is named by a digest of its type, its params, its inputs' digests and, at the source, the files' contents. The name is fixed before any work is done.
- The engine stores each computed output under its digest and never evicts it during a session, so returning to an earlier setting is free and the dirty set predicts the cost of an edit.
- Map components cache each item under its transformation and content, so one edited file reparses one file, and enrichments survive reloads through the database cache.
- Builds that call Claude are deferred until the user asks for them, can be capped by a call budget, and report what is still pending.

A build turns files into artifacts once. The next chapter turns to the other half of the engine, the query runs that read those artifacts many times a second, and to the same idea applied at a finer grain: remembering what each live node's output depended on.

**Lab: Chunk sizes, Doc cards.** Load *Chunk sizes*, open the Corpus tab and change `maxTokens` in the editor from 450 to 200, then back. Watch which passes the build report marks as reused. Then load *Doc cards*: before pressing Build, the Cards column reads “press Build to enrich” and the status line reads “enrich\_llm: 11 items wait for Claude · press Build to spend those calls”. Press Build once and note the call count the status line reports.

## 12. Queries at runtime

This chapter explains what happens between a keystroke in the search box and a redrawn result list. The build gave us artifacts; a query run reads them through the live nodes of the plan. Two mechanisms keep that cheap enough to run on every keystroke. Each node has a trigger that says whether it runs while the user types or waits for Enter, and a session remembers, per node, exactly which inputs and signal values its last output depended on. Together they mean that most keystrokes rerun two or three nodes in under a millisecond, and that Claude is called only when the user asks for it.

The traces in this chapter come from node runs of the real engine. The plan in sections 12.2 to 12.5 contains three nodes that call Claude; in these runs a mock sampler stood in for Claude, returning fixed queries, a fixed ranking and a one-sentence answer after a 20–40 ms delay. The statuses, counts and call counts are what the engine produced; the Claude text and the timings of the Claude nodes are the mock's.

### 12.1 Signals: the named inputs of a query run

A signal is a named value that live nodes read: the query `q`, the result count `k`, the facet selection `filters`, or any slider a page defines. A plan declares its signals with defaults:

```js
const plan = rag.plan('reports').over(corpus)
  .signals({ q: 'Why did ChatGPT show no app actions after OAuth worked?', k: 8, filters: {} })
  .rewrite.llm({ n: 3 })
  .retrieve('text', { k: 40 })
  .retrieve('meta', { k: 20 })
  .fuse.rrf({ weights: { text: 1, meta: 0.3 } })
  .filter.where()
  .rerank.llm({ top: 20, keep: s => s.k })
  .pack({ budget: 30000 })
  .answer();
```

Each signal becomes a node of phase `signal` in the IR, so a signal can be an input like any other node. A run takes a partial set of values and merges it over the defaults, `{ ...ir.signals, ...signals }`; anything not passed keeps its default. The search box writes `q`, the facet list writes `filters`, and a slider writes the key it is bound to (chapter 25).

A node can depend on a signal in two ways, and the IR records both as edges. The first is as a data input: the first retriever's input port has type `queries`, and when the node before it is a signal the session coerces the string into a one-element query list. The second is as a read: a lambda param such as `keep: s => s.k`, or a component that declares `reads`, as `filter.where` does for `filters`. The Ranking playground's plan shows both kinds:

```text
["q","text","signal"]  ["q","failures_2","signal"]  ["rrfK","fuse","signal"]
["q","rerank","signal"]  ["failureBoost","rerank","signal"]  ["headingBoost","rerank","signal"]
["k","limit","signal"]
```

These edges are what the DAG tab draws as dotted lines from the signal chips to the nodes that read them. They come from a static scan of each lambda's source (chapter 9.5). The session does not rely on them to decide what to rerun; it records reads as they happen, which section 12.3 explains.

### 12.2 Triggers: `change` nodes run on every keystroke, `submit` nodes wait for Enter

Every run has a trigger, either `change` or `submit`, and every live node has a trigger too. The page runs with `change` while the user types, after a 140 ms pause, and with `submit` when they press Enter or click the submit button. A node runs on a `change` run only if its own trigger is `change`; on a `submit` run every node runs. A node's trigger is its component's default unless the plan overrides it:

| Node | Component | Trigger | Why |
| --- | --- | --- | --- |
| `rewrite` | `rewrite.llm` | submit | one Claude call per query |
| `text`, `meta` | `retrieve` | change | an index lookup, about a millisecond |
| `fuse` | `fuse.rrf` | change | arithmetic over two lists |
| `filter` | `filter.where` | change | a metadata test per hit |
| `rerank` | `rerank.llm` | submit | one Claude call over twenty passages |
| `pack` | `pack` | change | string assembly under a byte budget |
| `answer` | `answer` | submit | one streamed Claude call |

The rule is mechanical: components that call Claude default to `submit`, everything else to `change`. It is also a design decision, discussed from the reader's side in chapter 27.1. Typing should feel instant and cost nothing; an answer should be something the user asked for.

A node that does not run still has to produce something, because the nodes after it need an input. Each component says what it produces when skipped. `rewrite.llm` and `rerank.llm` declare `skip: 'pass'`, so a skipped rewrite forwards the original query and a skipped rerank forwards the fused hits unchanged. `answer` declares `skip: 'null'`, so a skipped answer is no answer. Here is the first run of the plan above with the `change` trigger:

```text
# change, first run 26.5 ms, calls 0
  rewrite    skipped    1→1        0ms calls 0
  text       ran        1→40     1.8ms calls 0
  meta       ran        1→20    23.1ms calls 0
  fuse       ran        2→40     0.3ms calls 0
  filter     ran       40→40     0.1ms calls 0
  rerank     skipped   40→40       0ms calls 0
  pack       ran       40→14     0.3ms calls 0
  answer     skipped   14→0        0ms calls 0
```

Each line gives the node, its status, the size of its input and output, its time and its Claude calls. The two Claude nodes in the middle of the pipeline are skipped and pass their input through; the lexical pipeline still ranks 40 fused sections and packs 14 of them into the 30 KB budget. Pressing Enter on the same query runs everything:

```text
# submit, same q 86.2 ms, calls 3
  rewrite    ran        1→4     21.1ms calls 1
  text       ran        4→40     1.4ms calls 0
  meta       ran        4→20     0.7ms calls 0
  fuse       ran        2→40     0.1ms calls 0
  filter     ran       40→40       0ms calls 0
  rerank     ran       40→3     21.4ms calls 1
  pack       ran        3→3        0ms calls 0
  answer     ran        3→3     40.9ms calls 1
```

The rewrite turned one query into four (the original and three rewrites), so both retrievers reran with new input. The rerank kept the three passages the mock ranked. The answer cited `[E1]`. Three calls, one per Claude node.

### 12.3 The session memo: input identity plus recorded signal reads

A trigger decides whether a node may run. The session memo decides whether it needs to. A session (`engine.session(ir)`, or the one each page and workspace holds) keeps one memo entry per live node, recording the output and everything that output depended on. Before running a node, the session checks the entry:

```js
const nd = hash([n.t, n.p, n.trigger]);
const wants = trigger === 'submit' || triggerOf(n) === 'change';
const m = this.memo.get(n.id);
const sameInputs = m && m.inputs.length === inputs.length && m.inputs.every((v, i) => v === inputs[i]);
const sameReads = m && Object.entries(m.reads).every(([k, v]) => stable(s[k]) === v);
if (m && m.digest === nd && sameInputs && sameReads && (m.full || !wants)) { /* reuse: status 'cached' */ }
```

Four conditions must all hold. The node's definition (type, params, trigger) must be unchanged. Each input must be the identical object the last run received. Every signal the node read last time must have the same value now. And the stored output must be a full one, unless this run would skip the node anyway.

The second condition compares by identity, `===`, not by content. That is sound because the session reuses outputs: if a node was cached, its output is the very object it returned before, so the next node sees an identical input and can be cached too. Identity also makes the comparison constant time; comparing forty hits by content on every keystroke would cost more than some nodes take to run. The one place identity would fail is the query itself, because coercing the string `q` into a list makes a new array each run. The session keeps a small cache, `session.queries(v)`, that returns the same array for the same query text, and that is enough to let the retrievers be cached when only another signal changed.

The third condition is where the reads come in. While a node runs, the session hands it the signals through a proxy that writes down every key the node reads and the value it saw:

```js
const reads = {};
const sp = new Proxy(s, { get: (t, k) => { if (typeof k === 'string') reads[k] = stable(t[k]); return t[k]; } });
```

The proxy is passed both to the component's `run` and to every lambda param, so `keep: s => s.k` records `k` even though the lambda belongs to the plan rather than the component. After the runs above, the recorded reads were:

```text
rewrite: [q]   text: []   meta: []   fuse: []   filter: [filters]   rerank: [k, q]   pack: []   answer: [q]
```

The retrievers read no signals at all. They depend on the query only through their input. This matters for the next three runs. Pressing Enter again with nothing changed reruns nothing:

```text
# submit again, nothing changed 0.1 ms, calls 0     (all eight nodes: cached)
```

Selecting a facet changes `filters`. Only `filter` read it, so the run reuses everything before it:

```text
# change: filters set 0.6 ms, calls 0
  rewrite    cached     1→4        0ms
  text       cached     4→40       0ms
  meta       cached     4→20       0ms
  fuse       cached     2→40       0ms
  filter     ran       40→4      0.3ms
  rerank     skipped    4→4        0ms
  pack       ran        4→4        0ms
  answer     skipped    4→0        0ms
```

Two details deserve a look. The rewrite is `cached`, not `skipped`: its last output was a full one from the Enter press, its input and reads are unchanged, so the four Claude-written queries are reused on a `change` run without a new call. The page keeps the benefit of the rewrite for as long as the question stays the same. The rerank, on the other hand, is `skipped`: its input changed (four filtered hits instead of forty), so its stored ranking no longer applies and a `change` run may not call Claude to replace it.

Now the user edits the question. The new `q` is a new input for the rewrite, which may not run on `change`, so it passes the raw query through and every node after it runs on the lexical path:

```text
# change: typing 1 ms, calls 0
  rewrite    skipped    1→1        0ms
  text       ran        1→40     0.6ms
  ...
  answer     skipped   14→0        0ms
```

The Ranking playground makes the same mechanism visible with sliders. Its plan reads `s.rrfK` in the fusion, `s.headingBoost` and `s.failureBoost` in a rerank lambda, and `s.k` in the limit. Moving one slider at a time:

```text
# rrfK 60→20                 # failureBoost 1→3            # k 8→4
  text       cached             text       cached           text       cached
  failures_2 cached             failures_2 cached           failures_2 cached
  fuse       ran                fuse       cached           fuse       cached
  rerank     ran                rerank     ran              rerank     cached
  limit      ran                limit      ran              limit      ran
```

Each slider reruns the node that reads it and the nodes after it, and nothing before it. The session never consulted the IR's signal edges to work this out; the recorded reads were enough.

### 12.4 Four node statuses: ran, cached, skipped, error

Every trace entry carries one of four statuses. They are worth learning exactly, because the trace view, the DAG's highlighting and the agent's diagnoses all use them.

| Status | Meaning | Output used downstream |
| --- | --- | --- |
| `ran` | the node executed in this run | what it returned |
| `cached` | the memo matched; the node did not execute | the stored output, the identical object |
| `skipped` | a `submit` node on a `change` run, with no usable stored output | its input (`skip: 'pass'`) or `null` (`skip: 'null'`) |
| `error` | the node threw | a fallback: its input for hits and queries, `null` for answers |

An error does not stop the run. If a rerank lambda throws, the session records `rerank: <message>` in `result.errors`, forwards the unreranked hits, and runs the rest of the plan, so the page shows results with an error marker instead of a blank. The one exception is cancellation, which section 12.5 covers.

A `skipped` entry is also stored in the memo, marked as not full. A later `change` run with the same input may reuse it as `skipped` again; a `submit` run never will, because a skipped node owes the user a real output once they press Enter.

### 12.5 Cancellation, and what a keystroke costs

A user who types faster than a run completes produces runs that nobody will see. The page handles this in three places. It waits 140 ms after the last keystroke before starting a `change` run. It holds one `AbortController` per search runtime and aborts the previous run when it starts a new one. And it numbers runs, discarding any result whose number is no longer the latest. Here is the relevant part of the gui runtime:

```js
schedule(trigger) {
  clearTimeout(this.debounce);
  if (trigger === 'submit') return this.runNow('submit');
  this.debounce = setTimeout(() => this.runNow('change'), 140);
}
async runNow(trigger = 'change') {
  const seq = ++this.runSeq;
  if (this.ctl) this.ctl.abort();
  const ctl = this.ctl = new AbortController();
  const r = await this.session.run(this.signals(), { trigger, signal: ctl.signal, onText: … });
  if (seq !== this.runSeq) return r;   // a newer run started; drop this result
  …
}
```

The session passes the signal to the sampler, so an aborted run also stops its Claude call. When a node fails with the code `cancelled`, the session records the error and stops instead of continuing to the next node. Here the abort fired 70 ms into a `submit` run, while the answer was streaming:

```text
# submit, aborted at 70ms 71.1 ms, calls 3 [ 'answer: cancelled' ]
  rewrite    ran        1→4     19.4ms calls 1
  ...
  rerank     ran       40→3       21ms calls 1
  pack       ran        3→3        0ms calls 0
  answer     error      3→0        0ms calls 0 ERR cancelled
```

The run counted three calls because the answer's call had started. The rewrite and rerank results were stored in the memo before the abort, so if the user presses Enter again on the same question, those two nodes come back `cached` and only the answer calls Claude.

Putting the numbers together gives the cost model of the page. The first `change` run after a build took 26.5 ms, most of it spent warming the terms index. Later `change` runs took between 0.1 and 1 ms and made no calls. A `submit` run on this plan makes three calls, and a repeated `submit` with nothing changed makes none. A designer can promise the user that typing is free and Enter costs a fixed, small number of calls, and the engine keeps that promise by construction.

The key points of the chapter:

- Signals are the named inputs of a query run; a node depends on one either as a data input or by reading it, and the session records reads as they happen.
- A run's trigger and each node's trigger decide whether a node may run; nodes that call Claude default to `submit`, so typing never spends calls.
- The session reuses a node's output when its definition, its inputs by identity and the values of the signals it read are all unchanged, so a slider reruns only the nodes from its first reader onward.
- Every trace entry is `ran`, `cached`, `skipped` or `error`; errors fall back and continue, and cancellation stops the run.

A run's trace says which nodes ran. It also records, for every node that produces hits, the top of its list with scores and their parts, which is enough to follow one section through the whole pipeline. That is the subject of the next chapter.

**Lab: Answer first, Ranking playground.** In *Answer first*, type a question and open the Trace tab before pressing Enter: `rerank` and `answer` show as skipped. Press Enter and watch them run. Then pick a facet in the Sources tab and confirm that the trace shows the retrievers as cached. In *Ranking playground*, move one slider at a time and read off which nodes rerun; then predict the result for `rrfK` before you move it.

## 13. Traces and explanations

This chapter is about the question every search builder asks a hundred times: why did this result come first, and why is the one I wanted not there? The engine answers it from data it already keeps. Every run returns a trace with one entry per live node, and every node that produces hits records the top of its list with the score of each hit and the parts that score was made of. `rag.explain` reads those lists and follows one section through every node. With it, chapter 1's three failures (missed, misranked, misread) stop being guesses and become something you can read off a path.

All runs in this chapter are lexical and call no model, so every number is the engine's own.

### 13.1 The trace: status, counts in and out, time, calls

`session.run` returns a result object. Its enumerable fields are the data of the run:

| Field | Contents |
| --- | --- |
| `hits` | the plan's hits output, the list the page shows |
| `context`, `answer` | the packed context and the answer, when the plan has them |
| `outputs` | every node's output by node id; the page reads these as `s.out[nodeId]` |
| `trace` | one entry per live node, in execution order |
| `errors` | `nodeId: message` for every node that failed |
| `ms`, `calls` | wall time of the run and Claude calls it made |

It also has three non-enumerable methods: `top(n, from)` lists the first `n` hits of the output or of any node, `chunk(id)` returns a section, and `explain(id)` is section 13.3's subject.

A trace entry records what happened at one node: `node`, the component type `t`, `status` (chapter 12.4), `in` and `out` (the sizes of its input and output), `ms`, `calls`, and, when present, `notes` and `error`. A node whose output type is `hits` also gets `top`, its first fifty hits with `id`, `score` and whichever of `why`, `parts` and `reason` the node attached. `result.toJSON()` prints the trace in one line per node, which is the form the agent reads:

```text
text ran 1.3ms 1→40
failures_2 ran 0.1ms 1→6
fuse ran 0.3ms 2→46
rerank ran 0.7ms 46→46
limit ran 0.2ms 46→8
```

These five lines are the first run of the Ranking playground for “Why was the orders tab empty?”. They already say a good deal. The failure-table index found 6 rows against the section index's 40; fusion merged them into 46 distinct ids, so no section appeared in both lists; the rerank reordered without dropping; the limit cut to `k`. The counts are often the fastest diagnosis there is. A retriever with `1→0` found nothing; a filter with `40→0` removed everything; a fusion whose output size equals the sum of its inputs found no overlap between its retrievers.

Timing is the second column to read. A node that takes most of a run's time is the one to look at when typing feels slow, and a Claude node's time dominates every `submit` run it is part of.

### 13.2 `why` and `parts`: where a hit's score came from

Scores mean different things at different nodes, and each kind of node explains its own. A BM25 retriever attaches `why`, the contribution of each query term to the score, keeping the five largest. Fusion attaches `parts`, the rank the hit had in each list it was fused from. A rerank attaches `reason`, a short string from the lambda or from Claude. Here is the same section as the first hit of `text` and of `fuse`, from the playground run with `rrfK` set to 20:

```text
text  {"id":"…#failure-modes-encountered","score":11.0288,"why":{"tab":4.74,"empty":4.36,"order":1.93},"q":"Why was the orders tab empty?"}
fuse  {"id":"…#failure-modes-encountered","score":0.04762,"parts":{"text":1}}
```

The BM25 score, 11.03, is the sum of three term contributions (chapter 5 derives each one). “tab” contributes most, more than “empty”, and “order” least, because “order” occurs in 137 of the 462 sections, against 15 for “tab”, so its inverse document frequency is low. The query's other words, “why” and “was”, are stop words and contribute nothing. The `q` field says which of the node's queries produced the best score, which matters once a rewrite has turned one question into four.

The fused score is 0.04762, which is 1/(20 + 1): reciprocal rank fusion with `k = 20` gives a hit at rank 1 in one list exactly that much (chapter 14.3). The `parts` field shows the hit came from `text` at rank 1 and did not appear in `failures_2` at all. A hit found by both retrievers would show both ranks and a score near twice as large.

Two cautions apply when reading these numbers. Scores are comparable only within one node: 11.03 and 0.048 measure different things, and a rerank lambda that multiplies the fused score produces a third scale. And `why` shows the five largest terms, not all of them, so for long queries the listed contributions can sum to less than the score.

### 13.3 `rag.explain`: following one chunk through every node

`result.explain(id)` walks the trace in order and, for every entry with a `top` list, reports the rank the section had there, or `null` if it was absent from a list that had input. The implementation is short enough to read whole:

```js
function explain(result, id) {
  const out = { chunk: id, path: [] };
  for (const t of result.trace) {
    if (!t.top) continue;
    const i = t.top.findIndex(h => h.id === id);
    if (i < 0) { if (t.in) out.path.push({ node: t.node, rank: null }); continue; }
    const h = t.top[i];
    out.path.push({ node: t.node, rank: i + 1, score: h.score, ...(h.why ? { terms: h.why } : {}),
      ...(h.parts ? { parts: h.parts } : {}), ...(h.reason ? { reason: h.reason } : {}) });
  }
  const c = result.chunk(id);
  if (c) out.where = `${c.title} › ${c.headingPath.join(' › ')}`;
  return out;
}
```

The golden case “How does the eval tool report line numbers in errors?” (chapter 18) expects a section under “5. The eval tool” of the agent runtime spec. On a plan with a section retriever, a metadata retriever, fusion and `limit(8)`, the first relevant section comes fifth, which gives the case an MRR of 0.2. Explaining the section the user wanted, “5.2 Execution”, and the one that beat it:

```text
explain(…#5-2-execution-2)                             explain(…#21-6-canvas-2)   (rank 1)
  text   rank 5  score 11.0819                            text   rank 1  score 12.6489
         terms eval 3.33  line 2.9  tool 2.6  number 2.25        terms eval 2.62  number 2.36  tool 2.12
  meta   rank null                                                     line 1.98  error 1.91
  fuse   rank 5  score 0.01538  parts { text: 5 }          meta   rank null
  limit  rank 5  score 0.01538  parts { text: 5 }          fuse   rank 1  score 0.01639  parts { text: 1 }
  where: Agent runtime spec … › 5. The eval tool › 5.2 Execution
```

The path tells the whole story. Nothing after the section retriever changed the order: the metadata index found nothing (`rank null` at `meta` for both), fusion with a single contributing list preserves rank, and the limit kept the first eight. The ranking was decided by BM25 alone, and BM25 preferred “21.6 Canvas” because that section happens to contain all six query terms (eval, tool, report, line, number, error; the why field lists only the largest five), while “5.2 Execution” contains four, each scoring higher than in the Canvas section but not enough to make up for the two it lacks. The remedy is therefore a ranking change, not a retrieval change: a heavier heading-path weight (chapter 5.5) or a rerank that rewards heading overlap (chapter 16.4) would both lift a section titled “The eval tool” over one titled “Canvas”.

One property of the current implementation limits where `explain` can be used. Only entries for nodes that ran carry a `top` list; a `cached` entry records counts and status but not the hits. On a run where most nodes were cached, such as a facet click after a search, `explain` reports only the nodes that ran. To follow a section through every node, explain a run in which they all ran: the first run of a fresh session, or a run after a change to the query.

### 13.4 Missed, misranked, misread: diagnosing chapter 1's three failures from a trace

Chapter 1 named three ways a search can fail the reader. With `explain` each one has a recognizable signature in the path of the section that should have been found.

| Failure | Signature in the path | Where to fix it |
| --- | --- | --- |
| Missed | `rank: null` at every retriever | the corpus side or the query: chunking, fields, synonyms, rewriting |
| Misranked | a rank at the retriever, then `null` at a later node, or a rank above `k` at the output | ranking: weights, fusion, rerank, `k` |
| Misread | rank within `k` at the output and the section in the packed evidence, yet the answer is wrong | the answer step: packing order, budget, prompt |

A missed section is one no retriever found. The question “Why did the pod keep dying?” is about the incident in section 4.1 of the CoinVault report, “The typed-nil deployment failure”, in which the browser service went into a restart loop. The path of that section is empty all the way down:

```text
Why did the pod keep dying? | query terms: pod keep dying | text:null  meta:null  fuse:null  limit:null
```

The query terms column explains why. After stop words are removed and stems taken, the query is `pod keep dying`, and the section says “restart loop”, “startup failed” and “browser service”. Not one query term occurs in it. No ranking change can repair this, because nothing downstream ever sees the section. The fixes are on the vocabulary side: index-time synonyms (chapter 6.3), a query rewrite that produces “crash loop” or “restart” (chapter 16), or enrichment that adds such keywords to the section at build time (chapter 17.4).

A misranked section is one a retriever found and a later stage lost. A rephrasing of the same question shows it:

```text
why did the browser container keep crashing | browser container keep crash | text:18  meta:null  fuse:18  limit:null
```

The word “browser” matched, so the section was retrieved at rank 18, and fusion kept that rank. The limit cut the list to eight, and the section was gone. The section retriever has `k: 40`, so it was well inside the candidate set. This is the failure a rerank exists for: a step that looks at more than term overlap and can move rank 18 to rank 3. It is also the case where raising `k` hides the problem from the eval while leaving it in front of the reader, who still has to scroll past seventeen sections.

The eval-tool question in section 13.3 is a milder misranking: the section survived to the output at rank 5, so it counts as a hit at 5 and costs only MRR.

A misread is the one failure the retrieval trace cannot show, because retrieval succeeded. Its signature is a section that reaches the output within `k`, appears in `answer.evidence` with a label such as `E3`, and yet the answer is wrong or says the evidence is missing. Three checks narrow it down. If `E3` is absent from `answer.cited`, the model had the section and did not use it; look at where the section sits in the packed context and how much text surrounds it. If the context's `bytes` is close to the pack budget, other evidence the answer needed may have been left out, because the pack node stops at the first section that does not fit. If the section is cited and the answer still misstates it, the problem is the prompt or the model tier. Showing a real misread needs a real model answer, so this book leaves it to the Lab and to the answer checks of chapter 20.4.

The key points of the chapter:

- A run returns a trace with one entry per live node, and its status, counts, time and calls are usually enough to locate a problem before looking at any hit.
- Each kind of node explains its own scores: BM25 hits carry per-term contributions, fused hits carry their source ranks, and reranked hits carry a reason.
- `result.explain(id)` follows one section through every node that ran and reports its rank and score at each, so the node where a section was lost can be read off its path.
- Missed, misranked and misread failures have distinct signatures in that path, and each points to a different part of the pipeline to change.

Part IV has now covered the whole engine: builds that are reused by digest, query runs that are reused by input and signal reads, and traces that explain what both did. Part V uses this machinery to build better rankings, starting with the question the fused scores above raised: how to combine lists whose scores are not on the same scale.

**Lab: Compare stages.** Load *Compare stages* and type “cursor semantics tension”. The three columns show the `text`, `fuse` and `limit` outputs side by side. Pick a section that moves between columns, open the Trace tab and the agent panel, and ask the agent to run `explain` on it; the path it returns should match the columns you see. Then try the two typed-nil questions from section 13.4 and confirm their paths.
