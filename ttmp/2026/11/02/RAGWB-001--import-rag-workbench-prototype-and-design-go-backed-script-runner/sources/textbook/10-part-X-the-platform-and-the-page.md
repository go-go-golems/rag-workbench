# Part X · The platform and the page

The workbench runs as a hosted page whose only services are a Claude sampler, a small per-user database and file downloads. This part describes those limits, which shaped Parts II to IX, and the page built inside them.

## 31. The artifact runtime

This chapter describes the platform the workbench runs on, and connects each of its limits to a decision made earlier in the book. The workbench is one HTML page published as a claude.ai artifact. It has no server of its own. Everything it does beyond running JavaScript in a browser tab comes from four services the page asks for by name: a Claude sampler, a small database, the identity of the viewer, and a way to hand the viewer a file. Each service has a size limit or an absence case, and most of the design in Parts II to IX is a response to one of them. By the end of the chapter you should be able to say, for any feature of the workbench, which service it depends on and what happens when that service is missing.

Output in this chapter comes from three places, each labelled where it appears: node scripts that run the real `rag.js` over the eleven bundled reports, a headless Chromium session that drives the real page with a stand-in for the claude.ai runtime, and the runtime's own type definitions (contract version 0.2.84). The stand-in returns fixed text in place of Claude, so no Claude output in this chapter is real; sizes, counts and database paths are.

### 31.1 `claude.use`: sample, db, user, downloads

A published artifact reaches the platform through one global function, `window.claude.use(name)`. It returns a promise that resolves to the capability's namespace, or to `null` when this view cannot use it. The contract makes the reasons for `null` indistinguishable on purpose: the capability may not be declared, the viewer may not have granted it, it may have failed to load, or no viewer may be listening at all, in which case `null` arrives after ten seconds. A page cannot ask why; it can only branch on presence.

The workbench asks for all four capabilities at once and stores whatever comes back:

```js
async function initCaps() {
  const c = window.claude;
  if (!c || typeof c.use !== 'function') return;
  const get = n => c.use(n).catch(() => null);
  const [sample, db, user, downloads] = await Promise.all([get('sample'), get('db'), get('user'), get('downloads')]);
  W.caps = { sample, db, user, downloads };
  if (sample && sample.limits) { try { W.caps.limits = await sample.limits(); } catch (_) { W.caps.limits = null; } }
  if (db && user) { try { W.caps.uid = await user.id(); } catch (_) { W.caps.uid = null; } }
}
```

The important line is not in this function but in the one that calls it. `boot()` starts `initCaps()` and does not wait for it. It loads the bundled corpus, compiles the first preset, builds it and mounts the search page, and only then, in a `.then` on the capabilities promise, configures the sampler, the cache and the database. The first search is therefore always lexical and always local. This ordering is what lets the page work at all when the runtime is absent. Opening the same files from a plain web server, where `window.claude` does not exist, the headless session recorded:

```text
status   Built · 11 docs · 490 chunks · 187 ms
console  Claude is not available in this view: LLM nodes are skipped with an error
         and the agent cannot run. Lexical search, evals and editing work.
```

The table lists what each capability is used for and what the page does without it.

| Capability | What the workbench uses it for | Without it |
| --- | --- | --- |
| `sample` | the components that call Claude (`rewrite.llm`, `rerank.llm`, `answer`, `agentic`, `enrich.llm`), eval judges, synthetic cases, and the agent | LLM nodes fail with *Claude is not available in this view, so LLM nodes cannot run*; Build refuses deferred steps; the agent cannot send. Lexical search, evals and editing work. |
| `db` | the user's settings, script and preset, disabled files, the conversation index, uploaded files, agent transcripts, and the per-item cache of Claude results | Nothing outlives the page load. Uploads work until the tab closes. |
| `user` | `user.id()`, the opaque id that names the user's private folder in the database | The same as no `db`: the store needs both. |
| `downloads` | Save JSON in the IR tab, Export search page, and `chat.download` from the agent | A toast: *Saving files is not available in this view*. |

Two things are absent from this list. The page keeps no shared state: every database path it writes is under the viewer's private folder, so two people using the same published workbench never see each other's files or conversations. And apart from one Google Fonts stylesheet, the page uses no network beyond the files the artifact host serves (the scripts, `corpus.json`, the presets) and the four services. There is no call to an external API anywhere in the code, which is why the rest of this chapter can account for every byte the page sends to Claude.

### 31.2 Sampling: the prompt cap, tools, `.json`, and `limits()`

The sampler is one function with three variants of use. `sample(input, opts)` sends text and resolves to `{ text, truncated }`. `sample.json(input, opts)` sends text and resolves to a parsed JSON value. Passing `opts.tools` lets Claude call functions in the page between rounds of its answer. Every call is memoryless: the input must carry the instructions, the data and the expected format, because Claude keeps nothing between calls.

The engine never hands the raw sampler to a component. It wraps it so that every call increments a counter, which is how traces and build reports can say how many calls a step spent:

```js
sampler(counter, signal) {
  const base = this.env.sample;
  if (!base) {
    const no = () => Promise.reject({ code: 'sampling_unavailable', message: 'Claude is not available in this view, so LLM nodes cannot run' });
    no.json = no;
    return no;
  }
  const f = (input, opts = {}) => { counter.calls++; return base(input, { ...(signal ? { signal } : {}), ...opts }); };
  f.json = (input, opts = {}) => { counter.calls++; return base.json(input, { ...(signal ? { signal } : {}), ...opts }); };
  return f;
}
```

The wrapper also threads the run's `AbortSignal` into every call, which is how typing a new character cancels an answer that is still streaming (chapter 12). Components choose a model tier per call: the cheap structured steps (`rewrite.llm`, `rerank.llm`, `enrich.llm`, synthetic questions) default to `quick`, and the steps whose text the reader sees (`answer`, `agentic`) default to `default`. `answer` and `agentic` also pass `onText`, the streaming callback, which is how an answer appears word by word in the page.

Tools are not available in every view. `sample.limits()` reports what this view can do, and the workbench reads exactly one field of it:

```js
const limits = this.host.limits || {};
if (limits.tools) opts.tools = [this.evalTool(msg)]; else opts.cache = false;
```

Without `tools`, the agent can still talk but cannot act, and the page says so in the console. In the headless session the stand-in reported `{ maxPromptBytes: 262144, tools: { maxCount: 16 } }`; the first number is fixed by the contract, the second was chosen for the test.

The number that shapes the most design is `maxPromptBytes`. The contract caps every sampler input at 256 KiB of UTF-8 text, 262,144 bytes, and rejects anything larger with `prompt_too_large`. The bundled corpus is 535,461 bytes, 2.04 times the cap. The largest single report is 106,755 bytes and fits; all eleven together do not. This is the measurement behind chapter 1's claim that something must choose what Claude reads.

The workbench never reads `maxPromptBytes`. Instead, every prompt it sends has a bound built into the component that sends it. The table lists each caller, what bounds its input, and the size measured for the running question where a measurement is meaningful.

| Caller | What bounds the prompt | Measured |
| --- | --- | --- |
| `rewrite.llm` | the question plus a fixed instruction | 304 bytes |
| `rerank.llm` | the top 20 hits, each clipped to 700 characters | 14,964 bytes |
| `answer` | the `pack` budget: 40,000 characters of section text by default | 37,213 bytes of context at the default budget: 26 sections from 6 reports |
| `enrich.llm` | one section, clipped to 6,000 characters | at most about 6.3 KB per call |
| `agentic` | 8 search results of 220 characters; each read clipped to 6,000 characters; at most 8 reads | grows with each round |
| the agent | history trimmed until the turns serialize to 230,000 characters; each eval result truncated near 9,000 | grows with the conversation |

Two details in this table are worth reading closely. First, the `pack` node counts characters of section text, not bytes, and not the labels and heading paths it adds. For the running question its own counter said 32,846 while the context it produced was 36,917 characters and 37,213 UTF-8 bytes. At the default budget the gap does not matter, because the whole prompt is about 15 percent of the cap. A pack budget raised past 230,000 characters could produce a prompt the sampler refuses. Second, the agent and the agentic node are the only callers whose input grows during a call. For those, `prompt_too_large` is a real possibility, and the agent maps it to a message rather than a crash: *The conversation is too long. Start a new one.*

The agent's other error messages come from the same table, keyed by the contract's error codes: `not_granted` asks the viewer to allow Claude for the page, `rate_limited` says to try later, `tools_unavailable` explains that the agent can talk but not act. Inside the pipeline, a sampler error becomes a node error in the trace (chapter 13), with the rest of the run continuing where it can.

### 31.3 Why there are no embeddings, and what lexical retrieval plus Claude covers instead

Most retrieval systems built since 2020 rank by vector similarity: each section is turned into an embedding, a list of a few hundred numbers produced by a neural model, and a query is answered by finding the sections whose vectors are nearest to the query's vector. Embeddings find sections that share meaning with a query but not words, which is BM25's main weakness.

The runtime offers no embedding call. The sampler returns text, and text is the only thing Claude produces through it. Asking Claude to emit vectors as JSON would be slow, would spend one call per section, and would not produce vectors from a consistent model that could be compared with each other. Running an embedding model inside the browser was left out of scope for this book. The retrieval in Parts II and V is therefore lexical at its core, and the semantic gap is closed by using Claude where it is good: on text, at query time or at build time.

| Embedding retrieval would supply | What the workbench does instead | Where |
| --- | --- | --- |
| matching synonyms and paraphrases | synonym tables applied at search time; `rewrite.llm` writes keyword queries in the corpus's vocabulary | chapters 6, 16 |
| matching a question to an answer that uses different words | HyDE: search with an answer Claude drafts first | chapter 16 |
| ranking by meaning rather than term overlap | `rerank.llm` reorders the top 20 lexical hits with stated reasons | chapter 16 |
| a document-level semantic summary | `enrich.llm` writes a summary, questions and keywords per document at build time, and a second index searches them | chapters 11, 17 |
| following a line of inquiry across documents | the `agentic` node searches, reads and searches again | chapter 17 |

Each substitute costs Claude calls where an embedding index would have cost one build. The workbench makes those costs visible and optional: build-time enrichment waits for an explicit Build (chapter 11), and query-time calls wait for Enter (chapter 12). The lexical path that runs on every keystroke costs nothing and needs no service at all.

### 31.4 Persistence: one document per user, with transcripts, files and cache collections

The `db` capability is a document store. Paths alternate between collections and documents, so a path with an even number of segments names a document. The contract reserves `data/users/<id>/` as private to that user, where `<id>` is the value of `user.id()`. The workbench keeps everything under one document there:

```js
this.root = W.caps.db.doc(`data/users/${W.caps.uid}/ragwb`);
```

Below that document are three collections. The headless session recorded every document the page wrote during one sitting: an upload, a file switched off, the Doc cards preset built with Claude calls, an edit to the script, and one agent turn. The stand-in database holds what the real one would; the paths and sizes are the page's own.

```text
data/users/u1/ragwb                                   2383 bytes  disabled, preset, script, convIndex, updated
data/users/u1/ragwb/files/note-md-8e6076               309 bytes  name, text, added
data/users/u1/ragwb/cache/7f1b8901a5294e:9b5611bed81a20 1921 bytes  v, at
  … 10 more cache documents, 327 to 2089 bytes …
data/users/u1/ragwb/transcripts/cmvblhnkbo407         8808 bytes  id, title, created, updated, messages
```

| Document | Holds | Written when |
| --- | --- | --- |
| `ragwb` | the current preset id, the script if it differs from the preset, disabled files, agent settings, an index of the last 60 conversations | a successful build, a file toggle, a settings change, a saved conversation |
| `files/<slug>-<hash>` | one uploaded file: its name and full text | on upload; deleted on Remove |
| `cache/<key>` | one Claude result for one item of a map component, under the item cache key of chapter 11 | each time an `enrich.llm` item is computed |
| `transcripts/<id>` | one agent conversation | after each agent turn |

The root document stores the script only when it differs from the preset it came from. Before the edit in this sitting, the document held `"script": ""`; after a one-line edit to the Doc cards preset it held 2,060 characters. A user who never edits a preset therefore stores a preset id and nothing else.

Every size limit in the workbench's storage code comes from one rule in the contract: a document body is at most 256 KiB serialized. The page applies it in three places. An upload over 250,000 characters is refused before it is stored (*split it first*). A conversation whose serialized form passes 230,000 characters has large generated apps replaced by a placeholder and then loses its oldest turns, two at a time. Eval results and logs inside a stored conversation are clipped to 2,500 and 1,000 characters. The margins are in characters, not bytes. A 250,000-character file written mostly in a script that needs two or three bytes per character serializes past 256 KiB; the database refuses that write, the page logs *could not store*, and the file stays available until the tab closes.

Writes to the same document are serialized by a small per-path promise chain, `store.write(ref, fn)`, because the contract allows one write at a time per document and resolves concurrent writes as last-writer-wins. Reading back happens once, at boot. The same headless session reloaded the page after the writes above and recorded:

```text
status       Built · 11 docs · 453 chunks · 127 ms
console      restored preset “Doc cards” with your edits
files        11 of 12 on · 497 KB
build pass   enrich  enrich_llm 0 computed · 11 cached · 0 calls
agent        1 conversation in history: “Run the goldens and try a heading boost on a branch”, 2 messages
```

The last two lines are the point of the cache collection. Eleven Claude calls were spent once, before the reload; after it, the same eleven document cards came back from the database without a call, because each cache key names the component, its params and the item, not the session.

### 31.5 Uploading reports: what a new file does to digests, caches and evals

An upload goes through the Files tab of the editor. The page accepts `.md`, `.markdown` and `.txt`, renames the last two to `.md`, stores each file under `files/`, and rebuilds. Nothing else is special about an uploaded file: the source node reads bundled and uploaded files through one function, `activeFiles()`, which also drops any file the user has switched off.

Chapter 11 explained what a change to the source node does. Here is the same mechanism run against an upload, in node, on the Reports preset, adding one short note written for this chapter:

```text
after adding one file:
   files       967fb737 -> c2a46e3a CHANGED
   parse       d8c4fd64 -> dfe4f98e CHANGED
   chunks      ebe4a56e -> b208d2af CHANGED
   failures    97261a69 -> 5935aad9 CHANGED
   synonyms    9c379cd0 -> 537c8414 CHANGED
   ix-text     ed7fac9a -> d6fe9dce CHANGED
   ix-meta     5fb5ef38 -> bf8fbbf1 CHANGED
   ix-failures a803a862 -> 0e8a9dfa CHANGED

files        source.files   count   12 computed 0 cached  0
parse        parse.markdown count   12 computed 1 cached 11
chunks       chunk.sections count  464 computed 1 cached 11
failures     chunk          count   28 computed 1 cached 11
ix-text      index.bm25     count  464                     68.6 ms
```

Every build digest changes, because every build node is downstream of the files. The work done is much smaller than that suggests. The map components, parse and the two chunkers, recompute only the new item and take the other eleven from the per-item cache. The indexes are built whole, because an index is a function of all its chunks at once, and the BM25 index is where the time goes: 68.6 ms of a 143 ms rebuild. Removing the file again restored the old digests, and every node was reused.

For Claude-backed map components the per-item cache also lives in the database, so an upload costs one call per new item and nothing for the rest. On the Doc cards preset: the first Build spent 11 calls; after the upload, Build spent 1 call, with 11 items cached; a new engine with the same database, which is what a reload is, spent 0 calls and read all 12 cards from the cache.

Evals are unaffected in their definition and affected in their numbers. A golden case names its expected document by pattern (chapter 18), so a new file cannot break a case, but it can outrank the expected section, and it changes the corpus statistics every BM25 score depends on. For the Reports preset, adding the note left hit@5 at 0.917 and MRR at 0.926 and moved nDCG@10 from 0.790 to 0.792. The plan's IR digest was the same before and after, which is the correct reading: the pipeline did not change, the corpus did. An eval report records the IR digest but not a digest of the files, so two reports with the same IR digest can come from different corpora. When comparing eval runs across uploads, note the corpus readout in the top bar alongside them.

### 31.6 The same library in node: testing without a page

`rag.js` ends with a two-way export: it assigns `window.rag` in a browser and `module.exports` in node. It touches no DOM, so the whole grammar, compiler, engine, evals and workspace run in node unchanged. `gui.js` also loads in node; it can build and compile app specs, and only `mount` needs a document. `kit.js` attaches to the global `rag`.

A test supplies the two things the runtime would: a list of files and, if the pipeline calls Claude, a sampler with the same shape as the real one.

```js
globalThis.rag = require('/home/claude/ragwb/rag.js');
require('/home/claude/ragwb/gui.js');
require('/home/claude/ragwb/kit.js');
const { files } = require('/home/claude/ragwb/corpus.json');
const sample = async (input, opts = {}) => ({ text: 'A drafted paragraph [E1].', truncated: false });
sample.json = async input => /keyword queries/.test(input) ? { queries: ['stateless discover'] } : {};
sample.limits = async () => ({ tools: { maxCount: 8 } });
rag.configure({ files: () => files, sample, buildCalls: 'explicit' });
```

With that in place, a script can load each preset exactly as the editor does, with `new Function('rag', 'gui', 'log', 'kit', …)`, then compile it, build it, run it on `change` and on `submit`, compile its app and variants, run its evals, and spend any deferred build calls. Running that over all 29 presets took 1.5 seconds and printed one line per preset; four lines are shown here.

```text
reports    328ms nodes 21 · lex top 46 (11. Why ChatGPT still showed no actions) · submit calls 3 answer
           · app row · variants 2 · hit@5 0.917 mrr 0.926 · pending —
minimal    133ms nodes 7 · lex top 20 (5. The core's modules › 5.9 The rebalance law) · submit calls 0
           · app row · variants 0 · hit@5 1 mrr 0.933 · pending —
doc-cards   38ms nodes 14 · lex top 8 (What was learned) · submit calls 11 · app row · variants 0
           · hit@5 0.75 mrr 0.769 · pending enrich_llm:11
           explicit build: computed enrich_llm calls 11; pending after 0
agentic     48ms nodes 10 · lex top 0 (—) · submit calls 1 answer · app row · variants 0
           · hit@5 0 mrr 0 · pending —
all presets ok
```

The numbers that come from the stand-in sampler are the call counts and the agentic preset's zero scores, which reflect a stand-in that searched for something unrelated. The lexical results, node counts, eval scores of the lexical presets and the pending counts are real. A node test checks what is deterministic: that every preset compiles, builds, runs and evaluates, and that Claude calls happen where and as often as the design says. It cannot check what Claude would write. Chapter 32 adds the browser checks the node test cannot make.

The key points of this chapter:

- The workbench uses four platform services, `sample`, `db`, `user` and `downloads`, each of which may be absent, and it builds and searches before asking whether any of them are present.
- The prompt cap of 256 KiB is half the size of the bundled corpus, which is why retrieval exists, and every caller bounds its own prompt rather than checking the cap.
- The runtime has no embedding call, so lexical retrieval is the core and Claude closes the semantic gap through rewriting, HyDE, reranking, enrichment and agentic search, each at a visible cost.
- All persistent state lives under the viewer's private folder, in one root document and three collections, and every size limit in the storage code derives from the 256 KiB document cap.
- An upload changes every build digest but recomputes only the new file's items and the indexes; Claude results for unchanged items come from the database cache, including after a reload.
- The library runs unchanged in node, where a stand-in sampler makes every preset testable in seconds.

The limits described here explain what the workbench does. The next chapter describes how it looks: the page that presents those capabilities to a person, and the checks that keep it working.

**Lab: Doc cards, then the Files tab.** Load Doc cards and press Build; note the number of Claude calls in the status line. Reload the page and press Build again: the status line should report no calls, because the cards come from your database. Now upload a short markdown file of your own in the Files tab, press Build, and confirm that it cost exactly one call. Switch one bundled report off and watch the corpus readout and the chunk count change; switch it back on and check in the DAG tab's build passes that nothing was recomputed. Finally, open the IR tab and press Save JSON to see the `downloads` capability ask before it saves.

## 32. The page itself

This chapter describes the page a person sees: its visual language, its layout, the preset picker that is the entry point to every chapter's Lab, and the checks that keep all of it working. The visual language is the Lattice design system, and it is built on one rule that this chapter returns to several times: a visual distinction exists only when it carries a meaning the reader needs. There is one accent colour, and it means *signal*. There are two broken line styles, and they mean *build time* and *runtime*. By the end of the chapter you should be able to look at any part of the workbench and say what each colour and line style is telling you, and to say which parts of the page have been checked by machine and which have not.

Measurements in this chapter come from the page's stylesheet, from a headless Chromium session that loaded the page with a stand-in for the claude.ai runtime, and from the node test of chapter 31. As before, no Claude output shown is real.

### 32.1 Ink on paper, with one signal blue

The stylesheet defines every colour once, as a custom property on `:root`, and redefines the same names for dark mode. The page draws nothing in a colour that is not one of these tokens.

| Token | Light | Dark | Used for | Contrast on the sheet, light / dark |
| --- | --- | --- | --- | --- |
| `--ink` | #111417 | #E3E6E9 | text, 1 px rules, borders, primary buttons | 18.5 / 13.7 |
| `--muted` | #5D646A | #98A0A7 | secondary text, labels, build nodes and edges | 6.0 / 6.5 |
| `--faint` | #A9AEB2 | #69727A | editor line numbers, console timestamps | 2.2 / 3.5 |
| `--sig` | #1F4FD1 | #86A2F7 | signals, live tags, links, focus, the selected node | 6.8 / 7.0 |
| `--ok` | #2A7752 | #5DBB8B | a finished build, runtime edges | 5.4 / 7.3 |
| `--warn` | #9C5F00 | #E0A23A | steps that spend Claude calls, dirty nodes | 5.2 / 7.7 |
| `--bad` | #C2361B | #F07158 | errors and destructive buttons | 5.5 / 5.9 |
| `--paper`, `--sheet` | #FAFAF8, #FFFFFF | #121518, #181C20 | the two background levels | — |

The contrast ratios were computed from the token values with the WCAG formula against `--sheet`. Every token that carries text a reader must read is above 4.5:1 in both themes. `--faint` is not, and is used only for line numbers and timestamps, which are reference marks rather than content. A stricter reading of the accessibility guideline would count line numbers as text, and by that reading the editor gutter fails in light mode.

*Ink on paper* names the default: black 1 px rules on a near-white page, no shadows except one hatch pattern, no rounded corners, no gradients. Hierarchy comes from type rather than colour. The stylesheet uses three families with fixed roles: Arial for running text, Archivo for the brand and node names, and IBM Plex Mono for values, buttons, status lines and anything a reader might compare digit by digit. Labels are 9 px bold capitals with wide letter spacing, which is the *stencil* style of the Lattice system.

*One signal blue* means the accent has exactly one job. `--sig` appears 41 times in the stylesheet, and every use marks something that is live or selected: signal nodes and the edges that read them, the `live` tag, links, the focus outline, the selected node in the DAG, and the busy status. The three state colours are kept for states. Green says a build finished or data flowed; amber says a step will spend Claude calls or a node is out of date; red says something failed. Each has a light wash (`--ok-w`, `--warn-w`, `--bad-w`) for backgrounds, so a state can be shown without large areas of saturated colour.

Dark mode is chosen by the operating system, through `prefers-color-scheme`, and can be forced either way by setting `data-theme` on the root element, which is how the artifact host switches themes. Both themes define every token, so no component has a dark-mode rule of its own. In the headless session the computed value of `--sig` was `#1F4FD1` with a light colour scheme and `#86A2F7` with a dark one, and the measured layout was identical.

### 32.2 Dashed for build, dotted for runtime, in the DAG, the editor and the agent panel

Chapter 11 divided every pipeline into a build phase, run once per corpus, and a live phase, run per query. The page shows that division with line style, so that a reader can see which part of a pipeline an edit will rebuild without reading a type name. The DAG renderer assigns each edge a class from the edge's kind and its source node's phase:

```js
const cls = kind === 'signal' ? 'signal' : kind === 'artifact' ? 'artifact' : byId.get(a).phase === 'build' ? 'build' : 'data';
paths += `<path class="${cls}${hot}" d="${d}"/>`;
if (kind === 'data' && ta && (ta.status === 'ran' || ta.status === 'cached') && ta.out > 0 && byId.get(a).phase === 'live')
  paths += `<path class="run" d="${d}"/>`;
```

and the stylesheet gives each class a stroke:

| Class | Stroke | Meaning |
| --- | --- | --- |
| `build` | muted, dashed 3 2 | data flowing between build nodes |
| `artifact` | muted, dashed 3 2 | a live node using a build output, such as an index |
| `data` | ink, solid | data flowing between live nodes |
| `signal` | blue, solid, 0.9 px | a node reading a signal |
| `run` | green, dotted 1 2.5, 1.6 px, round caps | an edge that carried data in the last run, drawn over its `data` edge |

Build nodes get a dashed border to match their edges, and the `build` tag in the node detail panel is dashed as well. Live nodes that run a lambda are filled with a fine blue hatch, so query-time code you wrote is distinguishable from configured components at a glance; build lambdas keep the plain dashed border. Signals are drawn as pills in the blue wash.

The dotted runtime line is the only element in the DAG that changes on every keystroke. After typing the running question into the Reports preset at 1,440 px, the headless session counted the DAG's paths and nodes:

```text
paths  build 7 · artifact 6 · data 11 · signal 5 · run 7
nodes  build 6 · build lam 2 · signal 3 · live ran 7 · live skipped 2 · live lam skipped 1
```

Seven live nodes ran and seven of the eleven live edges are dotted: the path the query actually took. The three skipped nodes are the ones that wait for Enter, and they are drawn at half opacity with no dotted edge into them. Pressing Enter dots the rest. A legend under the DAG states the convention in words: *data, build / artifact, signal read, carried data last run, Enter only*.

The convention follows the DAG wherever it is drawn. The agent's `chat.dag()` and the `dag` component of chapter 26 call the same renderer, so a graph in a conversation or inside a generated app reads the same way as the inspector's. The agent's links into the workbench, the `wb:` references of chapter 30, are underlined with a dotted line, marking them as live references into the running page rather than links to the web. One dashed element does not follow the rule: the file drop zone in the editor's Files tab has a dashed border, the common convention for a drop target. It sits beside the build controls and could be read as one; a solid border in the hatch pattern would remove the ambiguity.

### 32.3 Layout: editor, search sheet, inspector, agent

The page is one CSS grid. Three rows hold a 46 px top bar, a row of panel tabs that exists only on narrow screens, and the main area. The main area has three columns: the pipeline editor, a centre column, and the agent. The centre column is itself split into the stage, which holds the search sheet, and the inspector below it.

```css
.app    { display: grid; grid-template-rows: 46px auto minmax(0, 1fr); }
.main   { display: grid; grid-template-columns: var(--ide-w) minmax(0, 1fr) var(--side-w); }
.center { display: grid; grid-template-rows: minmax(0, 1fr) var(--insp-h); }
:root   { --ide-w: 360px; --side-w: 370px; --insp-h: 340px; }
```

&#91;embedded content: page layout · three width ranges\]

Three width ranges produce three arrangements. At 1,240 px and wider all three columns are open. Between 980 and 1,240 px the page starts with the pipeline editor folded to a 34 px rail, because the search sheet would otherwise be too narrow to read; at 1,280 px and below both side columns also shrink to 340 px. Below 980 px the page shows one panel at a time and adds four tabs, Search, Pipeline, Inspect and Agent. The headless session measured these sizes:

| Viewport | Pipeline | Centre | Agent | Inspector | Page width |
| --- | --- | --- | --- | --- | --- |
| 1440 × 900 | 360 | 676 | 370 | 676 × 340 | 1440 |
| 1200 × 800 | 34 (rail) | 792 | 340 | 792 × 340 | 1200 |
| 980 × 800 | hidden | 946 | hidden | hidden | 980 |
| 390 × 844 | hidden | 356 | hidden | hidden (own tab) | 390 |

The last column is the check that matters on a phone: the document is exactly as wide as the viewport at every size, so nothing scrolls sideways. The top bar is the one element allowed to overflow, and it scrolls inside itself. The body's side padding is `max(16px, env(safe-area-inset-left))`, which keeps a 16 px gutter and moves content clear of a phone's rounded corners.

Each boundary between panels can be dragged. The drags are clamped: the editor between 260 and 720 px, the agent between 280 and 720 px, the inspector between 120 px and the centre's height minus 120. Each side panel folds to its rail with a button, so a reader can give the search sheet the whole width.

The search sheet is drawn as a sheet of drafting paper on a grid. The stage behind it has two grids, fine lines every 12 px and heavier lines every 48 px, and the sheet casts a hatched offset shadow instead of a blurred one. A title block in the stage header shows the plan's id, the workspace revision, the chunk count and the last build time, which is the same information a drawing's title block gives: what this is, which revision, and how it was produced.

One layout problem is visible at full width. The inspector's build-pass list is a four-column grid inside a 270 px panel, and a pass that built two outputs wraps one word per line (*chunks* / *462 ·* / *failures* / *28*). The panel is too narrow for the summary it is asked to hold.

### 32.4 The preset picker: stashed edits and restoring a session

Every Lab in this book starts from the preset picker at the top of the editor. The button shows the preset's position and group, *Preset 01 of 29 · Start here*, and its title; the arrows beside it step to the previous and next preset. The menu lists the 29 presets in six groups: Start here (3), Corpus & chunking (5), Ranking (6), With Claude (6), Search experiences (7) and Evaluation (2), with a filter box that matches titles, descriptions, groups and ids.

Each preset's entry in `presets/index.json` declares whether it spends Claude calls and when. Ten are marked `enter`, one, Doc cards, is marked `build`, and eighteen are marked as free. The picker turns that field into the last sentence of the description under the button, *Enter spends Claude calls.* or *Build spends Claude calls.*, so the cost is stated before the reader acts (chapter 27).

A preset is only a script, so editing it is ordinary editing, and the head shows an *edited* marker while the text differs from the preset. Loading another preset over edited text would lose the edit, so the picker keeps it:

```js
async function loadPreset(id, opts = {}) {
  const p = findPreset(id);
  const text = await fetchPreset(p);
  const cur = src().value;
  if (!opts.silent && cur.trim() && cur !== W.presetText) W.stash = { text: cur, label: W.preset ? W.preset.title : 'your script' };
  …
}
async function restoreStash() {
  if (!W.stash) return;
  const st = W.stash;
  W.stash = { text: src().value, label: W.preset ? W.preset.title : 'the preset' };
  setEditor(st.text);
  …
  await buildScript();
}
```

The stash holds one script. While it is set, the menu's first entry is *↺ Back to your edits of “Parameter sweep”* (the label the headless session recorded after editing the sweep preset and stepping to the next one). Restoring swaps the stash with the current text, so pressing it twice returns to where you were.

The session itself is restored from the database (chapter 31). After each successful build the page saves the preset's id and, if the text differs from the preset, the script. On load, once the database answers, the page reloads the saved preset and applies the saved script on top of it. The headless session showed this after a reload: *restored preset “Doc cards” with your edits*, with the *edited* marker visible. A script written from scratch, with no preset, is restored the same way under the message *restored your pipeline.js*.

The stash is not saved. It lives in a page variable, and the database holds only the current preset and script. In the headless run, after editing the sweep preset and stepping to the next one, the saved state read `{"preset":"reports","script":""}`. A reader who edits a preset, switches to another, and then reloads has lost the edits; they were recoverable only until the tab closed.

### 32.5 Checking the page: node tests and a headless run of every preset

The workbench has two automatic checks, and they divide the work by what each environment can see.

The node test of chapter 31 checks the library: every preset compiles, builds, runs on `change` and on `submit`, compiles its app and variants, runs its evals, and spends its deferred build calls when asked. It takes 1.5 seconds and checks nothing about the page.

The browser check loads the real page in headless Chromium with a stand-in for the claude.ai runtime: a sampler that returns fixed text, a database held in memory, a fixed user id, and a downloads service that records the filename. It then drives the page the way a reader would. It opens the picker, chooses each of the 29 presets by its title, waits until the built script matches the editor, and records the number of result rows, the status line, any error box, any inline component error and any console error. For six presets it also exercises the preset's own controls. The run recorded, in part:

```text
reports            results  12  Built · 11 docs · 490 chunks · 0 ms
minimal            results  12  Built · 11 docs · 462 chunks · 85 ms
table-rows         results  10  Built · 11 docs · 812 chunks · 27 ms
per-doc            results  10  Built · 11 docs · 11 chunks · 19 ms
doc-cards          results   8  enrich_llm: 11 items wait for Claude · press Build to spend those call
   after Build: Built · 473 chunks · 203 ms · 11 Claude calls
doc-browser        results   0  Built · 11 docs · 462 chunks · 0 ms
   outline rows: 17
playground         results  12  Built · 11 docs · 490 chunks · 15 ms
   after slider: 46 results · lexical · 0.5 ms
sweep              results   6  Built · 11 docs · 462 chunks · 0 ms
   sweep rows: 12
edited marker visible: True
stash entry: 1 ↺ Back to your edits of “Parameter sweep”
mobile scrollWidth 390

ERRORS: []
```

A build time of 0 ms means every build node was reused from the memo, because an earlier preset had built the same corpus. A count of zero results is expected for the presets whose pages show something other than a result list: an outline, a canvas, a dashboard, tabs, or an agentic answer. The playground line moved the third slider, the failure-row boost, to 3; the status still reports 46 results because a boost reorders hits without removing any. The check does not move the `k` slider, so it could not have caught the bug described in chapter 26.

| Check | Node test | Browser run | Neither |
| --- | --- | --- | --- |
| every preset compiles, builds and runs | yes | yes |  |
| eval scores of lexical presets | yes |  |  |
| Claude calls happen where and as often as designed | yes (counted) | yes (Doc cards) |  |
| the page mounts each preset without errors |  | yes |  |
| preset controls: sweep, slider, outline, Build |  | yes, six presets | the other 23 |
| no sideways scroll on a phone |  | yes, at 390 px |  |
| what Claude actually writes; real latency |  |  | yes |
| permission prompts, real database limits, rate limits |  |  | yes |
| visual regressions |  | screenshots, compared by eye | yes |

The last three rows are the honest boundary of this book's evidence. Everything about Claude's behaviour in Parts V, VI and IX was observed through a stand-in, and the database's 256 KiB document limit and quotas were read from the runtime's type definitions rather than triggered. Those are the first things to check on a real deployment, and the workbench makes them checkable: the trace counts every call, the status line reports every build's cost, and the agent's errors name the runtime's codes.

The key points of this chapter:

- Every colour is a token defined once for each theme, and every distinction it draws carries a meaning: blue for signals and selection, green, amber and red for states.
- Dashed lines mark the build phase and dotted lines mark the path the last query took, in every place a DAG is drawn; the file drop zone is the one dashed element that means something else.
- One grid produces three arrangements by width, and at every measured width the page is exactly as wide as the viewport.
- The preset picker states each preset's Claude cost before it is used, stashes one set of edits when switching, and restores the current preset and script after a reload, though not the stash.
- Node tests check the library and a headless run checks the page; neither checks what Claude writes.

The book began with a question about eleven reports and the observation that a prompt cannot hold them all, so something must choose what Claude reads. Every part since has been an answer to the question of how to make that choice visible and changeable. Part III turned the pipeline into data, so it could be drawn and diffed. Part IV made changing it cheap, by naming every result after what it depends on. Parts V and VI made a change judgeable, with traces that explain a ranking and evals that score it. Part VII made a change safe, as an operation that can be checked, undone and replayed. Parts VIII and IX gave two kinds of user, a person with a page and an agent with one tool, the same access to the same graph. This last part showed the limits all of that was built inside. The workbench is small enough to read in an afternoon; the habit it is meant to teach, of never changing a search system without seeing what the change did, scales to systems far larger.

**Lab: the page itself.** Switch your system to dark mode and back, and find the one colour that changes meaning: none should. Load Reports, open the DAG tab, type a question, and trace the dotted line from the query signal to the results; press Enter and watch the remaining edges dot. Narrow the window below 1,240 px and then below 980 px and confirm that nothing scrolls sideways. Finally, edit any preset, switch to another, use *Back to your edits*, and then reload the page: decide whether the session restored what you expected, and what you would change about the stash.
