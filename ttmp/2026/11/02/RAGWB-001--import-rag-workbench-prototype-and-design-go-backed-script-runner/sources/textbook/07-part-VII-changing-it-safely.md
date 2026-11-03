# Part VII · Changing it safely

A pipeline that a person and an agent both edit while it serves queries needs edits that are small, checked and reversible. This part makes every change an operation on the IR and gives the workspace that applies them a history.

## 21. Edits as operations

This chapter explains how a running pipeline is changed. The answer is a small vocabulary of **operations**, or ops: plain JSON objects, each naming one node of the IR and one change to it. A list of ops is applied by copying the IR, applying each op in order, and compiling the result again with every check from chapter 10. If any op fails, or the resulting graph fails a check, the whole list is refused and nothing changes. By the end of the chapter you should be able to read any op list the agent or the editor produces, predict what it will rebuild, and explain why the workbench edits pipelines this way rather than by editing the script.

The running example is a four-node live plan over the standard corpus: two retrievers, one over the BM25 index and one over the frontmatter terms index, fused by reciprocal rank and cut to `s.k` hits.

```js
const corpus = rag.corpus('reports').from(rag.files('*.md')).parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  .index('text', rag.bm25({ fields: { title: 2, headingPath: 3, callout: 2, text: 1 } }))
  .index('meta', rag.terms({ keys: ['type', 'tags', 'status', 'aliases'] }));

const plan = rag.plan('demo').over(corpus).signals({ q: 'rebalance law', k: 8 })
  .retrieve('text', { k: 30 })
  .retrieve('meta', { k: 30 })
  .fuse.rrf()
  .limit(s => s.k);

const ws = rag.workspace(plan);
await ws.rebuild();
```

Compiled, this is the IR below. Every op in this chapter names one of these ids.

```text
files:source.files → q:signal → k:signal → parse:parse.markdown → chunks:chunk.sections
  → ix-text:index.bm25 → ix-meta:index.terms → text:retrieve → meta:retrieve → fuse:fuse.rrf → limit:limit
```

### 21.1 The ten ops

An op is an object with an `op` field and whatever fields that kind of op needs. The library defines exactly ten kinds. Anything else is refused with an error that lists them:

```text
[op] op 1 (move): unknown op 'move'. Ops: set unset insert remove replace wire signal trigger output rename
```

The function that applies them is short enough to state in full as pseudocode:

```text
// pseudocode of rag.applyOps(ir, ops)
raw = a copy of ir's nodes (without signal nodes), signals, pinned outputs
for each op at index i:
    raw = applyOp(raw, op)          // throws "op i+1 (kind): reason"
return finalize(raw)                // the chapter 10 compiler: ports, ids, cycles, order, digests
                                    // throws "after ops: N problems: ..."
```

Two properties follow from this shape. First, ops are applied to a copy, so a refused list leaves the original IR untouched; a workspace that receives a bad list keeps its revision and its graph. Second, the checks run once, on the final graph, so an op list may pass through an illegal intermediate state as long as it ends in a legal one. Removing a node that something else still depends on is the usual case: the list `[wire, remove]` succeeds where `[remove]` alone fails.

| Op | Fields | Effect |
| --- | --- | --- |
| `set` | `node`, `path`, `value` | Sets one param, by dotted path into `p`. |
| `unset` | `node`, `path` | Deletes one param, by dotted path. |
| `insert` | `node: {t, p}` plus `after`, `before` or `in` (+ `feeds`) | Adds a node and wires it into the graph. |
| `remove` | `node` | Deletes a node and rewires its consumers around it. |
| `replace` | `node` plus `fn` (and optional `param`) or `with: {t, p}` | Swaps a lambda, or swaps the component type while keeping the id and wiring. |
| `wire` | `node`, `in` | Sets a node's input list. |
| `signal` | `key`, `value` | Sets a signal's default value. |
| `trigger` | `node`, `value` | Sets a live node's trigger to `'change'` or `'submit'`. |
| `output` | `node`, `name` | Names a node as a pinned output of the plan. |
| `rename` | `node`, `to` | Changes a node's id and every reference to it. |

The workspace's `apply` method runs an op list and returns a **receipt**, which chapter 22 describes field by field. For now it is enough to know that `live` lists live nodes and signals whose definition changed, and `dirty` lists build nodes that must be rebuilt. Here is every op applied in turn to the demo workspace, with the receipt each returned and the structural difference reported by `rag.util.diffIr`:

```text
## set        {op:'set', node:'text', path:'k', value:50}
   receipt {"revision":1,"live":["text"],"dirty":[],"changed":[],"removed":[]}
   diff    [{"change":"param","node":"text","path":"k","from":30,"to":50}]

## set nested {op:'set', node:'ix-text', path:'fields.headingPath', value:5}
   receipt {"revision":2,"live":[],"dirty":["ix-text"],"changed":["ix-text"],"removed":[]}
   rebuild computed [ 'ix-text' ] reused 4 ms 69

## unset      {op:'unset', node:'ix-text', path:'fields.callout'}
   receipt {"revision":3,"live":[],"dirty":["ix-text"],"changed":["ix-text"],"removed":[]}

## insert     {op:'insert', after:'fuse', node:{t:'rerank', p:{fn: <lambda>}}}
   receipt {"revision":4,"live":["rerank","limit"],"dirty":[],"changed":[],"removed":[]}
   diff    [{"change":"added","node":"rerank","t":"rerank"},
            {"change":"wiring","node":"limit","from":["fuse"],"to":["rerank"]}]

## replace fn {op:'replace', node:'rerank', fn:(s, ctx, hits, p) => hits.filter(h => h.tokens > 40)}
   receipt {"revision":5,"live":["rerank"],...}

## replace with {op:'replace', node:'fuse', with:{t:'fuse.max', p:{}}}
   receipt {"revision":6,"live":["fuse"],...}
   diff    [{"change":"type","node":"fuse","from":"fuse.rrf","to":"fuse.max"}]

## signal     {op:'signal', key:'k', value:5}
   receipt {"revision":7,"live":["k"],...}

## trigger    {op:'trigger', node:'rerank', value:'submit'}
   receipt {"revision":8,"live":["rerank"],...}

## wire       {op:'wire', node:'fuse', in:['text']}
   receipt {"revision":9,"live":["fuse"],...}
   diff    [{"change":"wiring","node":"fuse","from":["text","meta"],"to":["text"]}]

## output     {op:'output', node:'text', name:'lexical'}
   receipt {"revision":10,"live":[],"dirty":[],"changed":[],"removed":[]}

## rename     {op:'rename', node:'text', to:'bm25'}
   receipt {"revision":11,"live":["bm25","fuse"],"dirty":[],"changed":[],"removed":["text"]}

## remove     {op:'remove', node:'rerank'}
   receipt {"revision":12,"live":["limit"],"dirty":[],"changed":[],"removed":["rerank"]}
   diff    [{"change":"wiring","node":"limit","from":["rerank"],"to":["fuse"]},
            {"change":"removed","node":"rerank"}]
```

Read the receipts against the phases. Edits to live nodes (`text.k`, the rerank lambda, the fusion rule, a trigger) never make anything dirty, because live nodes have no build digest; they take effect on the next query run. Edits to build nodes (`ix-text.fields`) make exactly that node dirty, and the rebuild that follows reuses the other four build nodes. That is the digest rule of chapter 10, now seen from the editing side.

The positional ops deserve a closer look, because they are where most mistakes happen.

- **`insert` with `after`** puts the new node directly downstream of the named node and moves every consumer of that node onto the new one. Inserting a reranker `after: 'fuse'` therefore rewired `limit` from `fuse` to `rerank`. An `only: [...]` list restricts which consumers move. If the named node was a plan output, and the new node produces the same type, the output moves too.
- **`insert` with `before`** takes over the named node's single input. It refuses nodes with several inputs, because it cannot know which one you meant.
- **`insert` with `in`** wires the new node's inputs explicitly and, with `feeds`, appends it to the inputs of other nodes. This is how a second retriever is added beside an existing fusion.
- **`remove`** connects each consumer to the removed node's own input. That only has an unambiguous answer when there is exactly one data input to pass through. Removing `fuse`, which has two, is refused: `cannot rewire 'limit' around 'fuse' (it has 2 inputs); use wire`.
- **`rename`** rewrites every edge and output that mentions the old id. The graph is the same, but anything keyed by node id sees a removal and an addition, which is why the receipt above lists `text` as removed and `bm25` as live.

One rewiring rule produces a surprising error. When the consumer of a removed node is variadic, like a fusion node whose port is `hits*`, `remove` splices the removed node's inputs into the consumer's list. A retriever's input is the query signal, so removing `meta` while `fuse` still reads it would wire the signal `q` into the fusion:

```text
[op] after ops: 1 problem:
  fuse: input 'q' gives signal but this port takes hits
```

The compiler catches it, as it should, but the fix is to say what you mean in two steps: `[{op:'wire', node:'fuse', in:['text']}, {op:'remove', node:'meta'}]`. Because checks run on the final graph, the pair is accepted as one atomic edit.

### 21.2 Paths into params, and setting a lambda from source text

The `path` of `set` and `unset` is a dotted path into the node's params object, `p`. `'k'` addresses `p.k`; `'fields.headingPath'` addresses one weight inside the BM25 field map. Paths keep ops small and keep unrelated params out of the edit: changing one field weight does not restate the other three, so two edits to different weights do not overwrite each other. Values are normalized before they are stored, the same way the builder normalizes them (chapter 9): a function becomes a lambda `{ $fn: '<source>' }` and a regular expression becomes `{ $re, flags }`. The IR therefore stays JSON after any `set`.

Lambdas can enter a pipeline through three ops, and they do not all behave the same today.

| Op | How the lambda is written | Stored in history as |
| --- | --- | --- |
| `set` with a function `value` | `value: s => s.k * 2` | `{"$fn":"s => s.k * 2"}` |
| `replace` with `fn` | `fn: (s, ctx, hits) => hits.slice(0, 5)` | `"fn":"(s, ctx, hits) => hits.slice(0, 5)"` |
| `insert` or `replace` with a node whose `p` holds a function | `node: { t: 'rerank', p: { fn: (s, ctx, hits) => ... } }` | `{"t":"rerank","p":{}}` |

The first two convert the function to source text before the op is recorded. The third reaches the IR correctly, because `applyOps` normalizes node params, but the copy kept in the workspace history is made by a JSON clone, which drops function values. The live graph is right; the record of how it got there is not. The difference matters as soon as the history is replayed, which happens when a branch is merged (chapter 23) or when ops are written back into the script. A merge of a branch whose only change was such an insert produced:

```text
branch p   {"fn":{"$fn":"(s, ctx, hits) => hits.slice().reverse()"}}
merged p   {}
run errors [ 'rerank: fnOf(...) is not a function' ]
```

The rule that avoids this is the one the agent's instructions already follow: inside an op, write a lambda as source text. Either form below is stored, replayed and merged correctly, because it is JSON from the start.

```js
{ op: 'insert', after: 'fuse', node: { t: 'rerank', p: { fn: '(s, ctx, hits) => hits.slice().reverse()' } } }
{ op: 'insert', after: 'fuse', node: { t: 'rerank', p: { fn: { $fn: '(s, ctx, hits) => hits.slice().reverse()' } } } }
```

The `replace` op has two forms. With `fn` it replaces a lambda param in place, `fn` by default or the one named by `param`; it refuses a node that has no lambda, with an error that names the other form: `fuse (fuse.rrf) has no lambda; use {op:'replace', node, with:{t, p}}`. With `with` it replaces the component type and params while keeping the node's id and every edge, which is how `fuse.rrf` became `fuse.max` above without disturbing the retrievers or the limit.

### 21.3 `plan.patch(ops)`: the same ops at authoring time

Ops are not only for the running workspace. A plan spec has a `patch` method that appends ops to the spec; `rag.compile` applies them after it has built the IR from the builder calls. Because specs are immutable (chapter 7), `patch` returns a new spec:

```js
const p2 = plan.patch([{ op: 'set', node: 'text', path: 'k', value: 50 }]);
rag.compile(p2).node('text').p.k      // 50
rag.compile(plan).node('text').p.k    // 30, the original is unchanged
```

A bad op in a patch fails at compile time, with the same message the workspace would give: `op 1 (remove): 'ix-text' is used by text; remove those first`.

Every preset ends with the same two lines, and this is why:

```js
const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
```

The builder calls describe the pipeline as its author designed it. The `ops` array holds the edits made to it afterwards, in the workspace, by a person or by the agent. Keeping them separate means the script stays readable and the edits stay reviewable as a list. Chapter 23 shows the button that writes workspace ops into that line. Variants use the same mechanism: `plan.variant('wide', ops)` is `plan.as('wide').patch(ops)`, so a variant in the Evals tab is literally the base plan plus an op list.

### 21.4 Dry runs: which nodes go live, which rebuild, and what Claude would cost

`ws.dryRun(ops)` applies the list to a copy of the workspace's IR and classifies the result exactly as `apply` would, but commits nothing: the revision does not move and no event is emitted. It never throws; a refused list comes back as `{ ok: false, error }`.

```text
ws.dryRun([{op:'set', node:'chunks', path:'maxTokens', value:200}])
{"ok":true,"live":[],"dirty":["chunks","ix-text","ix-meta"],"changed":["chunks","ix-text","ix-meta"],"removed":[],"estimate":{"llmCalls":0}}

ws.dryRun([{op:'set', node:'text', path:'k', value:50}])
{"ok":true,"live":["text"],"dirty":[],"changed":[],"removed":[],"estimate":{"llmCalls":0}}

ws.dryRun([{op:'remove', node:'ix-text'}])
{"ok":false,"error":"op 1 (remove): 'ix-text' is used by text; remove those first"}
```

The first result says that changing the chunk size costs a rebuild of the chunker and both indexes, because both indexes are downstream of the chunks. The second says that changing a retriever's `k` costs nothing at build time. This is the information a person wants before pressing Apply on a large corpus, and the information the agent should check before an edit that could spend the user's Claude usage.

That cost is the `estimate.llmCalls` field. It is computed as the number of dirty build nodes that spend Claude calls (components marked `llm`, such as `enrich.llm`, and build lambdas that call `ctx.sample`), multiplied by the number of chunks in the current build. It is an upper bound, and on the Doc cards pipeline it is a loose one. Changing the enrichment's model tier there gives:

```text
dryRun tier {"ok":true,"live":[],"dirty":["enrich_llm","ix-cards"],"changed":["enrich_llm","ix-cards"],"removed":[],"estimate":{"llmCalls":473}}
pending     [{"node":"enrich_llm","items":11}]
```

The estimate multiplies by all 473 chunks in the build, but the enrichment only runs over the 11 document cards, so the real number is 11. The engine's `pending()` (chapter 11) reports that number exactly, once a build has run without calls. Treat the estimate as a signal that Claude calls are involved at all, and `pending()` as the count.

### 21.5 Why operations and not a text diff of the script

The workbench has a script, `pipeline.js`, and an agent that can write JavaScript. It would be possible to make every change by editing the script's text and rebuilding. The workbench does keep that path, for changes ops cannot express, such as defining a new component, changing the search page, or adding an eval set. For changes to the pipeline graph, it uses ops instead, for five reasons.

**Ops address the graph, not the text.** An op names a node by its id, and the agent finds ids by reading `ws.ir`, a JSON object. A text edit has to locate the right builder call among chained method calls and reproduce valid JavaScript around it. Two builder chains that produce the same graph can look nothing alike, so a text diff says little about what changed in the pipeline.

**Ops are checked individually and refused atomically.** Every refusal above names the op by index and kind, and names the node involved. A broken script edit fails as a syntax error or a compile error for the whole script, and in the workbench it also leaves the last good build in place with an error panel. For a model that must correct itself, `op 2 (set): no node 'nope'. Nodes: files, parse, chunks, ix-text, ix-meta, text, meta, fuse, limit` is a better error than a stack trace.

**Ops make history granular.** Each applied list becomes one history entry with an author (`user`, `agent` or `merge`) and a time. Rebuilding from the script, in contrast, is recorded as a single `reset` entry, `build from pipeline.js`, which replaces the whole IR.

**Ops replay.** The same list can be applied to a branch, replayed onto the main workspace by a merge, written into the script's `ops` line, or attached to a plan as a variant. A text diff of a script can only be applied to that script.

**Ops can be priced before they run.** `dryRun` tells you what an op list will rebuild and whether it can spend Claude calls. A script edit tells you nothing until you build it.

|  | Edit the script | Apply ops |
| --- | --- | --- |
| Addressed by | Position in source text | Node id in the IR |
| Checked | Whole script, on build | Each op, then the final graph |
| Failure | Syntax or compile error; last good build stays up | Named op refused; IR unchanged |
| History | One `reset` entry per build | One entry per op list, with author |
| Cost known before running | No | Yes, with `dryRun` |
| Can express | Anything, including new components, apps and evals | Changes to nodes, wiring, params, signals, triggers and outputs |

The two paths are reconciled rather than kept apart: the `ops` line in every preset is where workspace edits go when they are worth keeping.

The key points to take from this chapter:

- An op is a JSON object naming one change to one node, signal or output, and there are exactly ten kinds.
- An op list is applied to a copy and compiled once at the end, so it is atomic, and it may pass through an illegal intermediate graph if it ends in a legal one.
- Positional ops rewire for you only when the rewiring is unambiguous; otherwise they refuse and tell you to use `wire`.
- Live edits never cause a rebuild; build edits rebuild only the changed node and what lies downstream of it.
- Lambdas inside op node specs should be written as source text, because function values are dropped from the recorded history and lost on replay.
- `dryRun` reports the cost of an edit without making it; its Claude-call estimate is an upper bound, and `pending()` gives the real count.

An op list describes an edit, but something has to hold the IR it edits, number the versions, and tell the editor, the search page and the agent that the graph has changed. That is the workspace, the subject of chapter 22.

**Lab: Minimal search.** Load the preset and open the DAG tab. Select `text` and change `k` in its params editor: the editor turns your JSON edit into `set` and `unset` ops and applies them, and the toast reports the new revision and any rebuild. Use **Run on Enter only** on a live node to apply a `trigger` op, and **Remove** to apply a `remove` op. Then open the History tab and read the list of entries, each with its author. Finally, ask the agent to change a field weight with `ws.dryRun` first and report what would be rebuilt.

## 22. Workspaces and revisions

Chapter 21 defined op lists as pure functions from one IR to another. A running workbench needs more than that. It needs one place that holds the current IR, applies op lists to it, numbers each version, and tells everyone who depends on the graph that it changed: the build engine, the search page, the inspector, and the agent. That place is the **workspace**. This chapter describes what a workspace holds, how it refuses edits planned against a version that no longer exists, what its receipts mean and who reads them, and how it refuses edits in Read-only mode. The design goal throughout is that a person and an agent can edit the same pipeline in the same page without either one silently overwriting the other.

### 22.1 A workspace: IR, engine, revision, listeners

`rag.workspace(target, opts)` compiles its target, which can be a plan spec, a corpus spec or an IR, and wraps the result:

```js
const ws = rag.workspace(plan, { engine, name: 'main' });
ws.ir          // the current compiled IR (read-only to callers; changed only by apply, undo, redo, reset, restore)
ws.revision    // 0, then 1, 2, 3, ... one per change
ws.history     // [{ revision, ops, by, note, at, before }]: what was applied, by whom, and the IR it replaced
ws.future      // entries that undo has stepped back over, for redo
ws.branches    // named child workspaces (chapter 23)
ws.engine      // the build engine; shared with branches, so built artifacts are shared too
```

The workspace does not own the engine. The engine is passed in, or the default engine is used, and its build memo is keyed by digest (chapter 11), not by workspace. Two workspaces, or a workspace and its branch, that need the same index get the same built index. That is what makes the experiments in chapter 23 cheap.

Every state change emits an event to the listeners registered with `ws.on(fn)`. Here are the events from a short session: one build, one applied edit, two undos, two redos, and a restore of a snapshot taken a few edits later.

```text
event {"type":"build","report":{"ok":true,"ms":191.4,"nodes":{...}}}
event {"type":"apply","by":"user","revision":1}
event {"type":"undo","revision":3,"undone":2}
event {"type":"undo","revision":4,"undone":1}
event {"type":"redo","revision":5}
event {"type":"redo","revision":6}
event {"type":"restore","revision":11}
```

In the workbench there is exactly one main workspace, created the first time the script builds. Everything that edits the pipeline goes through it:

| Source of the edit | What it calls | `by` |
| --- | --- | --- |
| The script editor's Build | `ws.reset(ir, 'build from pipeline.js')` | `user` |
| The node params editor in the DAG tab | `ws.apply(set / unset ops)` | `user` |
| The node buttons Run on Enter only, Remove | `ws.apply(trigger / remove op)` | `user` |
| The agent's eval code | `ws.apply(ops, { by: 'agent' })` | `agent` |
| A merge of a branch | `ws.apply(branch ops, { by: 'merge' })` | `merge` |
| The History tab's Undo and Redo | `ws.undo()`, `ws.redo()` | (not an entry) |

The page has one listener. It updates the header immediately, ignores build events, and otherwise waits 30 ms before acting, so that several edits made in one burst cause one reaction. Then it rebuilds if the engine reports dirty nodes, reruns the search with the `change` trigger, and redraws the inspector. In pseudocode:

```text
// pseudocode of the workbench's onWsChange(event)
update the header (revision, dirty count)
if event is a build: stop
after 30 ms of quiet:
    if engine.dirty(ws.ir) is not empty: rebuild those nodes
    rerun the search page's session with trigger 'change'
    redraw the inspector tab
```

The consequence for a person watching the page is that an edit by the agent looks the same as an edit of their own. The DAG redraws, the results change, and the History tab gains an entry marked `agent`. Nothing about the agent's edits is hidden in a separate copy of the pipeline.

### 22.2 `expect`: optimistic concurrency between the agent and the editor

A person and the agent can both edit the workspace, and JavaScript gives them no locks to take. They do not need locks in the usual sense: the page runs on one thread, so two `apply` calls never interleave inside the workspace. The problem is a different one. The agent reads `ws.ir`, reasons about it, possibly across several tool calls and several seconds, and then applies ops. If the person changed the pipeline in the meantime, the agent's ops were planned against a graph that no longer exists. They may still apply cleanly and still be wrong, for instance by setting a weight back to a value the person just changed on purpose.

The workspace handles this with **optimistic concurrency**. A caller that wants protection passes the revision it planned against as `expect`. If the workspace has moved on, `apply` refuses:

```js
const seen = ws.revision;                                                     // 0
await ws.apply([{ op: 'set', node: 'text', path: 'k', value: 50 }], { by: 'user', expect: seen });
await ws.apply([{ op: 'set', node: 'text', path: 'k', value: 60 }], { by: 'agent', expect: seen });
```

```text
conflict [conflict] workspace 'main' is at revision 1; these ops were planned against 0. Re-read ws.ir and plan again.
```

The scheme is called optimistic because it assumes conflicts are rare and checks only at the end, instead of preventing other edits while a plan is being made. There is no automatic resolution. The workspace could try to rebase the agent's ops onto the new IR, but whether `k: 60` should win over `k: 50` is a question about intent, and only the caller can answer it. The error message therefore tells the caller what to do: read the IR again and plan again.

The guard is only as good as the revision it is given. The agent's instructions show the pattern `ws.apply(ops, { expect: ws.revision })`. Written that way, inside a single expression, the revision is read at the moment of applying, so the check always passes. It protects nothing. The guard does its job when the revision is recorded where the plan was made: read `ws.revision` alongside `ws.ir` in one step, and pass that number to `apply` in a later step, after the person has had a chance to act. The same reasoning applies to code that awaits between reading and writing, such as a rebuild or an eval run, because the person's edits can land during the await.

### 22.3 The receipt: live changes, nodes to rebuild, removals

Every successful `apply` returns a receipt, and `dryRun` returns the same fields without the revision. The fields are computed by comparing the IR before the ops with the IR after them.

| Field | Contains | Computed as | Who acts on it |
| --- | --- | --- | --- |
| `revision` | The new revision number | Previous revision + 1 | Callers that pass `expect` next time |
| `live` | Live nodes and signals whose definition changed | Live nodes whose type, inputs, params or trigger differ; signals whose default differs | The search page reruns on `change` |
| `dirty` | Build nodes with no built result | Build nodes whose new digest is not in the engine's memo | The listener rebuilds them |
| `changed` | Build nodes whose digest changed | New digest differs from the old one | Reports and the agent |
| `removed` | Node ids that no longer exist | Ids in the old IR missing from the new one | The inspector clears a selection |

The distinction between `dirty` and `changed` is the one that most often confuses readers. A changed build node has a different digest than before the edit. A dirty build node has a digest the engine has not built in this session. Most of the time the two lists are equal. They differ when an edit returns a node to a configuration that was built earlier. Changing `maxTokens` from 450 to 200 and back makes the chunker changed both times but dirty only the first time, because the second digest is still in the memo. The receipt reports both so that a caller can tell "the graph changed here" apart from "work is needed here".

Some receipts look empty and are not. An `output` op changes the IR's pinned outputs, but outputs are neither live nodes nor build nodes, so its receipt lists nothing. A `signal` op appears under `live` by signal name (`"live":["k"]`) although `diffIr`, which compares nodes only, reports no difference. A `rename` appears as a removal of the old id and a live change under the new one, because every list is keyed by id.

In the workbench, the receipt becomes the toast after an edit in the params editor, in this format:

```text
rev 2: 1 op · rebuilding ix-text
```

The agent receives the receipt as the return value of its code and sees it as YAML in its tool result. A receipt with a non-empty `dirty` list tells it to expect a rebuild before the next search reflects the change; a receipt with only `live` entries tells it the next run already does.

### 22.4 Read-only workspaces, and errors written for the agent that will read them

The agent runs in one of three modes, which chapter 29 describes from the agent's side. In Read-only mode the agent may inspect and measure but not change the pipeline. The library supports this with a flag, `ws.readonly`, checked by every method that changes state:

```text
readonly [readonly] ws.apply is blocked in Read-only mode. Describe the change instead, or ask the user to switch to Auto or Ask.
readonly [readonly] ws.undo is blocked in Read-only mode. Describe the change instead, or ask the user to switch to Auto or Ask.
```

The workbench does not set the flag on its main workspace, because the person must still be able to edit while the agent is restricted. Instead it gives the agent a proxy of the workspace that blocks seven methods (`apply`, `rebuild`, `undo`, `redo`, `merge`, `reset`, `restore`) and passes everything else through. Its message differs in one clause:

```text
ws.apply is blocked in Read-only mode. Describe the change instead, or measure it on a branch (ws.branch).
```

That clause is deliberate. `ws.branch` is not blocked, and a branch is an ordinary workspace without the restriction. In Read-only mode the agent can therefore make a branch, apply ops to it, rebuild it with the shared engine, run the evals against it, and report the measured effect of a change it is not allowed to make. Only `merge` would bring the change into the main workspace, and `merge` is blocked. Read-only mode thus forbids changing what the person sees, not experimenting.

The error messages throughout the workspace follow one rule: the reader of an error is often a model, and a model corrects itself best when the error names the thing that was wrong and the action that would fix it. Compare the messages the library actually produces with the minimal message each could have been:

| Minimal message | Message the library gives |
| --- | --- |
| `node not found` | `op 1 (set): no node 'txt'. Nodes: files, parse, chunks, ix-text, ix-meta, text, meta, fuse, limit` |
| `invalid remove` | `op 1 (remove): 'ix-text' is used by text; remove those first` |
| `cannot remove node` | `op 1 (remove): cannot rewire 'limit' around 'fuse' (it has 2 inputs); use wire` |
| `type error` | `after ops: 1 problem: limit: input 'chunks' gives chunks but this port takes hits` |
| `cycle` | `after ops: 1 problem: the graph has a cycle through: fuse, limit` |
| `wrong op` | `op 1 (replace): fuse (fuse.rrf) has no lambda; use {op:'replace', node, with:{t, p}}` |
| `stale` | `workspace 'main' is at revision 1; these ops were planned against 0. Re-read ws.ir and plan again.` |

Each message carries an error code (`op`, `conflict`, `readonly`) that code can branch on, the index and kind of the failing op so a long list can be corrected in place, the ids involved, and, where there is one, the op that would have worked. The list of valid node ids in the first message is there because the most common cause of that error is a guessed id. Printing the real ones turns a second failed attempt into a successful one.

The key points to take from this chapter:

- A workspace holds the current IR, a revision counter, a history of applied op lists with the IR each replaced, and a set of listeners; the build engine is shared, not owned.
- In the workbench, the editor, the DAG buttons, the agent and merges all change the pipeline through the one main workspace, so every change is visible and recorded with its author.
- `expect` refuses an op list planned against an older revision; it protects only when the revision is recorded at planning time, not read in the same expression as the apply.
- The receipt separates live changes, which take effect on the next run, from dirty build nodes, which need a rebuild, and from changed build nodes, which may already be built.
- Read-only mode blocks the seven methods that change the main workspace but leaves branches open, so the agent can still measure a change it may not make.
- Errors name the failing op, the ids involved and the fix, because their most frequent reader is the agent.

The workspace's history has so far been a list that only grows. Chapter 23 walks it backwards: undo and redo, branches that hold an experiment until it is measured, and snapshots that cover what ops do not.

**Lab: Reports · hybrid + rerank + answer, with the agent.** Load the preset and switch the agent to Read-only mode. Ask it to try a higher heading weight in `ix-text` and tell you whether the goldens improve. Watch it make a branch, apply ops to the branch, and report a comparison while the History tab of the main workspace stays unchanged. Then, in Auto mode, ask it to record `ws.revision` in one step and apply a change in the next; between the two steps, change a param yourself in the DAG tab, and read the conflict error it receives.

## 23. History: undo, branches and snapshots

Chapter 22 recorded every applied op list in the workspace's history together with the IR it replaced. This chapter puts that record to use in four ways. Undo and redo walk back and forth through it. Branches hold an experiment in a separate workspace until it has been measured, then replay it onto the main one. Snapshots save and restore states that ops do not cover, which is what lets a person revert any step the agent took. Exports take the history out of the workspace, either into the pipeline script or into a standalone search page. The design question in each case is the same: what exactly is stored, so that going back is exact rather than approximate.

### 23.1 Undo and redo restore stored IRs; revisions only count up

A history entry is a record with six fields:

```js
{ revision: 2, ops: [{ op: 'set', node: 'chunks', path: 'maxTokens', value: 200 }],
  by: 'user', note: undefined, at: Date.now(), before: { /* the whole IR before these ops */ } }
```

`undo` pops the last entry, sets the workspace's IR to that entry's `before`, and pushes the entry onto `future` together with the IR it is leaving, as `after`. `redo` reverses that: it pops from `future`, records the current IR as the new `before`, and sets the IR to `after`. Applying a new op list empties `future`, because the states it held no longer follow from the current one. Both return `null` when there is nothing to undo or redo.

Here is the revision counter, the two params involved and the IR digest through two edits, two undos and two redos. The history and future columns list the revision numbers of the entries on each stack.

```text
step               rev  text.k  maxTokens  IR digest        history  future  dirty
start              0    30      450        4eb9f2b953e869
set text.k 50      1    50      450        00a65e4a98a8c9   1
set maxTokens 200  2    50      200        cff34eb3954775   1,2              chunks,ix-text,ix-meta
rebuild            2    50      200        cff34eb3954775   1,2
undo               3    50      450        00a65e4a98a8c9   1        2
undo               4    30      450        4eb9f2b953e869            2,1
redo               5    50      450        00a65e4a98a8c9   1        2
redo               6    50      200        cff34eb3954775   1,2
```

&#91;embedded content: revisions of one workspace · two edits, two undos, two redos\]

Two decisions in this table need explaining.

The first is that undo restores a stored IR rather than applying an inverse op. An inverse is easy to write for `set`, where the old value is known, but not for the positional ops. Undoing an `insert after` must restore every consumer that was moved onto the new node and every output that moved with it. Undoing a `remove` must restore the node and the rewiring that `remove` performed around it. Each inverse would be a second implementation of the op's rewiring rules, with its own bugs. Storing the IR is exact by construction, and cheap: the demo pipeline's IR is 1,806 bytes of JSON, and a history of a hundred edits costs well under a megabyte.

The second is that undo increments the revision instead of decrementing it. A revision number exists to answer one question for `expect` (chapter 22): is the IR now the same one the caller planned against? If undo moved the counter back from 2 to 1, revision 1 would name two different moments, and a caller who planned at the first one would pass the check at the second even if the person had done something in between. With a counter that only grows, every revision names one moment. Equal content is a separate question, answered by the IR digest: revisions 0 and 4 hold the same IR and have the same digest, `4eb9f2b953e869`.

That equality is also why undo is free. The `dirty` column is empty after every undo and redo. The IRs they restore were built before, so every build digest they contain is still in the engine's memo, and the search page reruns immediately without rebuilding anything.

The workbench adds one rule. Its Undo button is disabled when the most recent history entry is a `reset`, the entry that a Build from `pipeline.js` writes. The library would happily undo a reset and restore the IR from before the build, but the editor would then show a script that no longer describes the running pipeline. Undo in the History tab therefore goes back as far as the last build and no further; going back past a build means editing the script.

### 23.2 Branches, and why a merge replays ops instead of merging graphs

A **branch** is a second workspace that starts from the current IR of the first:

```js
const b = ws.branch('headings');   // a new Workspace: same IR, same engine, revision 0, baseRevision 0
```

It is stored in `ws.branches` under its name, it records the parent's revision at the moment it was made as `baseRevision`, and it shares the parent's engine. Edits to the branch leave the parent untouched. That makes a branch the right place for a change whose value is not yet known. Here is a full experiment: raise the heading weight and lower the title weight in the BM25 index, rebuild, and score both workspaces on the twelve golden questions of chapter 18.

```js
const ev = kit.goldens();
await ev.run(ws);                                 // main
await b.apply([{ op: 'set', node: 'ix-text', path: 'fields.headingPath', value: 6 },
               { op: 'set', node: 'ix-text', path: 'fields.title', value: 1 }], { by: 'agent' });
await b.rebuild();
await ev.compare([ws, b]);
```

```text
main     {"n":12,"hit@5":1,"mrr":0.892,"ndcg@10":0.818,"p50ms":0.7,"calls":0}
branch receipt {"revision":1,"live":[],"dirty":["ix-text"],"changed":["ix-text"],"removed":[]}
branch rebuild computed [ 'ix-text' ] reused 4
branch   {"n":12,"hit@5":1,"mrr":0.917,"ndcg@10":0.838,"p50ms":0.7,"calls":0}
main unchanged rev 0 fields {"title":2,"headingPath":3,"callout":2,"text":1}
diff main→branch [{"change":"param","node":"ix-text","path":"fields",
                   "from":{"title":2,"headingPath":3,"callout":2,"text":1},
                   "to":{"title":1,"headingPath":6,"callout":2,"text":1}}]
delta    {"hit@5":0,"mrr":0.025,"ndcg@10":0.02}
cases that moved [{"q":"How does the eval tool report line numbers in errors?","main":5,"headings":2}]
```

The branch rebuilt one index and reused the other four build nodes from the shared engine. The comparison is specific: one question moved from rank 5 to rank 2 and nothing else changed, which accounts for the whole MRR difference of 0.025 (from 1/5 to 1/2 on one of twelve cases). Whether that is worth keeping is a judgement chapter 19 discusses. If it is, the branch is merged:

```text
merge {"revision":1,"live":[],"dirty":[],"changed":["ix-text"],"removed":[],"merged":"headings","ops":2}
history [{"revision":1,"by":"merge","ops":"set ix-text.fields.headingPath, set ix-text.fields.title"}]
```

A merge does two things. It checks that the parent has not moved since the branch was made, that is, that `baseRevision` still equals the parent's revision. Then it concatenates the ops of every entry in the branch's history and applies them to the parent as one list, by `merge`. The result is a single history entry on the main workspace, undoable as a unit, and the branch is deleted. The receipt shows `changed` but not `dirty`: the branch already built that index digest in the shared engine, so the merged main workspace needs no rebuild at all. Ops the branch undid are not replayed, because undo removed their entries from the branch's history.

If the parent has moved, the merge is refused:

```text
merge conflict [conflict] 'main' moved from revision 1 to 2 since branch 'k60' was made. Re-apply the branch ops on a fresh branch.
```

The remedy the message names is mechanical, and it works:

```js
const ops = c.history.flatMap(h => h.ops);   // the stale branch's ops
const c2 = ws.branch('k60-again');
await c2.apply(ops);                         // fails here, with a named op, if they no longer fit
await ws.merge(c2);                          // {"revision":3,"live":["text"],...,"merged":"k60-again","ops":1}
```

Why refuse rather than merge? A version control system for text merges two edited copies against their common ancestor, and resolves each region by rules about which side changed it. The same could be done for IRs, field by field. The workbench refuses for a reason specific to its purpose. A branch exists to be measured, and its eval numbers were measured on the base revision plus the branch's ops. If the parent has moved, the merged pipeline is base plus the parent's new ops plus the branch's ops, a graph nobody has measured. Refusing forces the experiment to be replayed on the current state, where it can be measured again before it is kept. Replaying ops also reuses every check of chapter 21: if the branch's ops no longer fit the moved parent, for example because a node they name was removed, the fresh branch reports which op failed.

Replay has one weakness, inherited from section 21.2. A merge applies the ops recorded in the branch's history, and a function value inside an `insert` or `replace ... with` node spec is not recorded. A branch that inserted a reranker as `p: { fn: (s, ctx, hits) => ... }` works on the branch and merges into a reranker with no lambda, which fails on the first run with `rerank: fnOf(...) is not a function`. Writing lambdas in ops as source text avoids this.

### 23.3 Snapshots: what the agent saves before each step

Undo covers ops. Some changes are not ops. The agent can replace the script's text, rebuild from it, add or remove a corpus file, load a different preset, or mount a new search page, and a single tool step can do several of these at once. To make every step reversible, the workbench needs a record of state that is larger than the workspace's history.

The workspace contributes its part with `snapshot()` and `restore(snap)`:

```js
const snap = ws.snapshot();   // { ir, revision, history: <number of entries>, future }
```

```text
snapshot {"revision":8,"history":2,"future":0,"digest":"229dc7426bd87c"}
after two more ops   rev 10   ... → text:retrieve → fuse:fuse.rrf → limit:limit           (meta removed)
after restore        rev 11   digest 229dc7426bd87c   history 2   ... → text:retrieve → meta:retrieve → fuse:fuse.rrf → limit:limit
```

A snapshot stores the IR, the revision, the length of the history and the redo stack. Restoring sets the IR back, truncates the history to its old length, and restores the redo stack. Like undo, it increments the revision, to 11 here, so that a caller holding revision 8 or 10 cannot mistake the restored state for the one it saw.

The workbench's own snapshot wraps this one with everything else a step can change:

| Field | Holds |
| --- | --- |
| `ws` | The workspace snapshot above |
| `script`, `scriptBuilt` | The editor's text, and the text of the last successful build |
| `uploaded`, `disabled` | The user's uploaded files, and which corpus files are switched off |
| `appIr` | The gui app currently mounted in the App tab |
| `preset`, `presetText` | The loaded preset and its original text |

Before every tool step the agent runs, outside Read-only mode, the agent panel takes one of these snapshots and keeps it with the step. Each completed step then shows **Revert to before this step**, and each answer shows **Undo this answer**, which reverts to before its first step. Restoring a snapshot puts back the uploaded files, rebuilds the script if its text differs, restores the workspace, and remounts the app; the step and every step after it are marked as reverted. Read-only steps take no snapshot, because they cannot change anything.

The two mechanisms do different jobs. Workspace undo is fine-grained and covers only the pipeline graph; it is what a person uses after a param edit they did not like. Snapshots are coarse and cover the whole page; they are what a person uses after an agent step that did several things at once.

### 23.4 Exporting a workspace

The workspace's state can leave it in three forms.

**Back into the script.** The History tab's **Keep in script** button collects the ops applied since the last build, meaning the entries after the most recent `reset`. It appends them to the ops the script already had, writes the result into the script's `const ops = [...]; // end ops` line, and builds the script again. Running that logic on the Minimal search preset after two edits, one by the user and one by the agent, leaves this at the end of the script:

```js
const ops = [
  { "op": "set", "node": "text", "path": "k", "value": 40 },
  { "op": "insert", "after": "text", "node": { "t": "limit", "p": { "k": 10 } } }
]; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
```

The edits are now part of the script and survive a reload, and the builder calls above that line are untouched. A script without the marker line cannot receive them, and the button says so: `pipeline.js needs a "const ops = […]; // end ops" line to write into`.

**As the IR.** The IR tab saves the current IR as `<plan id>-rev<revision>.json` and copies it as Mermaid text. This is the exact graph, and `rag.ir(json)` reads it back.

**As a standalone search page.** `rag.export(ws, { title })`, the IR tab's **Export search page** button, writes one HTML file that runs without the workbench and without Claude. It first removes every live node that runs only on submit, except answers, and then every answer and context node, which leaves the lexical part of the pipeline:

```text
full      ... → fuse:fuse.rrf → limit:limit → rerank:rerank.llm! → pack:pack → answer:answer!
exported  ... → fuse:fuse.rrf → limit:limit
```

The file embeds the corpus files, the library source and the pruned IR, and runs a search 150 ms after each keystroke. It is a way to hand someone the search experience the workbench measured, without the workbench.

The key points to take from this chapter:

- Each history entry stores the IR it replaced, so undo and redo restore exact states instead of computing inverse ops.
- Revisions only count up, so a revision names one moment; equal content is recognized by equal IR digests, which also makes undo free of rebuilds.
- A branch is a separate workspace that shares the engine, so experiments rebuild only what they change and can be measured before they are kept.
- A merge replays the branch's ops onto the parent and refuses if the parent moved, because the branch's measurements describe only the base it was built on.
- Snapshots cover the whole page (script, files, app and workspace), which is what lets every agent step be reverted.
- Workspace edits can be kept in the script's `ops` line, saved as an IR, or exported as a standalone lexical search page.

Parts III to VII have treated the pipeline as data to compile, run, measure and change. Part VIII turns to the page the pipeline runs in, and shows that the page is described the same way: a grammar of components, compiled once, with closed lambdas for anything that changes.

**Lab: the History tab.** Load Reports · hybrid + rerank + answer. Change two params in the DAG tab and watch both entries appear in the History tab, marked `user`. Press Undo twice and Redo once and watch the revision count up while the results switch back and forth without a rebuild. Then ask the agent, using its suggestion chip, to fix the eval-tool miss on a branch, compare it with main and keep it if it helps; afterwards, find the single `merge` entry it produced. Finally, press **Keep in script** and read the `ops` line it wrote, then use **Revert to before this step** on the agent's last step and confirm the script and the History tab return to where they were.
