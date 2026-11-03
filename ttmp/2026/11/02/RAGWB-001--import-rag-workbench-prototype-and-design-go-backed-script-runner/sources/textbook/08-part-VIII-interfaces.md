# Part VIII · Interfaces

The page a search runs in is described the same way as the pipeline: a tree built once from a grammar, with props that are values or closed lambdas. This part builds that grammar, the runtime that keeps it current, and the decisions a search page is made of.

## 24. A grammar for pages

This chapter explains how a search page is described as data. By the end you should be able to read any preset's `gui.app(...)` block and say what it will draw, which parts of it can change after it is drawn, and why the description survives being saved, reloaded and edited by the agent. The design follows the pipeline grammar of Part III closely, on purpose: a page is a tree of typed nodes with props, the props are values or closed lambdas, and the whole tree is compiled once into JSON.

The alternative would have been to let preset authors write DOM code: a function that builds elements and wires event handlers. That is more flexible, and it is what most front-end code looks like. It also fails every requirement this workbench has. A function cannot be stored in the database and reloaded, cannot be shown to the agent as text it can edit, cannot be diffed between two presets, and cannot be re-rendered selectively, because nothing outside the function knows which part of the page depends on which value. A tree of nodes can do all four.

### 24.1 `gui.app`: title, `use(plan)`, state, derive, watch, view

An app is built with a fluent builder, like a plan. Here is the complete page of the Faceted search preset:

```js
const app = gui.app('facets').title('Faceted search').use(plan).state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c
      .searchBox('q')
      .toggle('codeOnly', 'Only sections with code')
      .status()
      .facets('filters', { fields: ['type', 'tags', 'status'], from: 'text', max: 10 })
      .results({ limit: 15 }), { grow: 3 })
    .col(c => c.chunk(s => s.selected), { grow: 2 })));
```

Each builder method sets one part of the app's description:

| Method | What it records | Example |
| --- | --- | --- |
| `title(t)` | The name shown above the page. | `'Faceted search'` |
| `use(plan)` | The id of the plan whose signals and outputs the page is bound to. | `{ plan: 'facets' }` |
| `state(obj)` | Initial values of keys the page owns, beyond the plan's signals. | `{ selected: null }` |
| `derive(key, f)` | A key computed from other keys, recomputed after every change. | `derive('n', s => s.out.limit?.length)` |
| `watch(key, f)` | A lambda called with the new value whenever `key` changes. | see the Document browser preset |
| `view(b)` | The tree of components, built by calling `b` once. | the `row`/`col` tree above |

Two of these deserve a closer look. `use(plan)` does not copy the plan into the app. It records only the plan's id, and at mount time the host hands the runtime a live connection to whichever workspace holds that plan. This is why the agent can change the pipeline with an op and the page keeps working: the page never had its own copy of the pipeline.

`watch` is for the cases where one key should drive another. The Document browser preset keeps a plain `doc` key for its dropdown and turns it into the plan's `filters` signal with a watcher:

```js
.state({ doc: '', selected: null })
.watch('doc', (s, ctx, v) => ctx.set({ filters: v ? { doc: v } : {}, selected: null }))
```

The watcher receives the new value as its third argument. It is a lambda like any other, so it is stored as source text and run without its closure (chapter 9).

### 24.2 Layout: row, col, grid, card, tabs, when

Six components arrange other components. They take a builder callback for their children and an optional props object.

| Component | Renders as | Props |
| --- | --- | --- |
| `col(b, o)` | A vertical stack with a 12 px gap. | `grow`, `gap: 'tight'` |
| `row(b, o)` | A horizontal flex row that wraps. Each child takes at least 280 px. | `grow` on each child |
| `grid(cols, b, o)` | A CSS grid with `auto-fit` columns at least `560 / cols` px wide. | `cols` |
| `card(title, b, o)` | A titled section. | `title` |
| `tabs(b, o)` | A tab bar; the open tab's index is kept in a state key. | `key` |
| `when(cond, b)` | Its children when `cond` is truthy, otherwise an empty hidden element. | `cond` |

The row rule is the one that matters most for designers. A `.g-row` is `display: flex; flex-wrap: wrap`, and every child has `flex: 1 1 280px`. A row of two columns therefore sits side by side when there is room for two 280 px columns and stacks into one column when there is not. No breakpoint is written anywhere in a preset; the layout adapts because the grammar has one opinion about minimum width. Chapter 27 returns to what this does on a phone.

`tabs` is the only layout component that owns state. The open tab is stored under a key the builder generates (`tab_12`, say) unless you pass one, so switching tabs is an ordinary state change and re-renders only the tab component:

```js
.tabs(t => t
  .tab('Answer', b => b.answer({ title: 'Answer' }).chunk(s => s.selected))
  .tab('Sources', b => b.facets('filters', { fields: ['type', 'tags'] }).results({ limit: 20 }))
  .tab('Trace', b => b.trace().dag()))
```

`when` takes a condition, which is almost always a lambda. The Document browser uses three of them to show an outline, a hint, or results depending on whether a document is chosen and whether anything is typed:

```js
.when(s => s.doc && !s.q, w => w.table(/* the outline */))
.when(s => !s.doc && !s.q, w => w.note('Choose a document to see its outline, or type to search everything.', 'info'))
.when(s => s.q, w => w.status().results())
```

### 24.3 Content: text, note, stat, table, list, chart, code

Content components show values. Each takes its value as a prop, and every prop may be a lambda.

| Component | Shows | Notes |
| --- | --- | --- |
| `h(text, level)` | A heading. |  |
| `text(md)` | Markdown, with `[E1]` citations turned into buttons. | Supports `wb:` links. |
| `note(md, tone)` | A tinted callout. | `info`, `ok`, `warn`, `bad` |
| `badge(text, tone)` | A small label. |  |
| `stat(label, value, o)` | One number with a label, an optional unit and a signed delta. | Numbers ≥ 1,000 print as `1.2k`. |
| `stats(items)` | A row of stats from an array or an object. |  |
| `kv(obj)` | A two-column key/value table. |  |
| `table(rows, o)` | A table. Columns are inferred from the first 50 rows unless given. | `columns`, `max` (200), `onRowClick` |
| `list(items)` | A bulleted list of markdown strings. |  |
| `chart(kind, data, o)` | An SVG `line`, `bar` or `scatter` chart. | `bar`, `line`, `scatter` are shortcuts. |
| `code`, `json`, `link`, `divider` | What their names say. |  |

The Corpus dashboard preset is built only from these. Its first component computes six statistics from the built corpus:

```js
.stats((s, ctx) => {
  s.out;
  const ch = ctx.rag.chunks(), tok = ch.reduce((a, x) => a + x.tokens, 0);
  return [
    { label: 'documents', value: ctx.rag.docs().length }, { label: 'chunks', value: ch.length },
    { label: 'tokens', value: tok }, { label: 'avg tokens', value: Math.round(tok / Math.max(1, ch.length)) },
    { label: 'with code', value: ch.filter(x => x.hasCode).length }, { label: 'with tables', value: ch.filter(x => x.hasTable).length },
  ];
})
```

The bare `s.out;` on the second line looks like a mistake. It is a read, and chapter 25 explains why a read with no use is how this lambda asks to be recomputed after every query run.

### 24.4 Controls bound to state keys: slider, number, select, toggle, input, button

A control is bound to a state key by name. It reads the key to draw itself and writes the key when the user acts.

| Control | Writes | When |
| --- | --- | --- |
| `slider(key, { min, max, step, label })` | a number | on every `input` event, while dragging |
| `number(key, o)` | a number | on `change` (blur or Enter) |
| `select(key, options, o)` | the chosen option's `value` | on `change` |
| `toggle(key, label)` | `!current` | on click |
| `input(key, o)` | the text | on every keystroke |
| `button(label, onClick, o)` | nothing by itself | calls `onClick(s, ctx)` |

The key may be a plan signal or a page-only key, and the control does not know or care which. That single rule is what makes the Ranking playground possible. Its four sliders are bound to `k`, `headingBoost`, `failureBoost` and `rrfK`, which are signals of its plan, so dragging one is enough to rerun the search:

```js
.card('Knobs', b => b
  .slider('k', { min: 3, max: 20, step: 1, label: 'results (k)' })
  .slider('headingBoost', { min: 0, max: 4, step: 0.25, label: 'heading boost' })
  .slider('failureBoost', { min: 0, max: 4, step: 0.25, label: 'failure-row boost' })
  .slider('rrfK', { min: 5, max: 120, step: 5, label: 'rrf k' }))
```

A `select` takes its options as a list of values or of `{ value, label }` objects, and the options may themselves be a lambda. The Document browser builds its list of reports at run time from the corpus, so an uploaded file appears in the dropdown after the next build without any change to the page:

```js
.select('doc', (s, ctx) => [{ value: '', label: 'All documents' },
  ...ctx.rag.docs().map(d => ({ value: d.id, label: d.title.replace(/^PROJECT REPORT - |^PROJ - /, '') }))],
  { label: 'Document' })
```

### 24.5 Props as lambdas, and why the tree is built only once

Every prop is one of three things: a plain value, a lambda, or a bake. The difference is when it is evaluated.

| Kind | Written as | Evaluated | Stored in the tree as |
| --- | --- | --- | --- |
| Value | `'Faceted search'`, `15`, `['type', 'tags']` | never; it is already a value | the value |
| Lambda | `s => s.selected` | every time the component renders | `{ "$fn": "s => s.selected" }` |
| Bake | `gui.bake(() => …)` | once, when the view is built | the result |

The builder callback passed to `view` runs exactly once. It does not draw anything. It appends nodes to a list, and `compile()` turns that list into JSON. Here is the compiled tree of the Faceted search page, produced by running the preset's code in node:

```json
{
  "kind": "gui", "version": 1, "id": "facets", "title": "Faceted search",
  "state": { "selected": null }, "derived": {}, "watchers": [],
  "uses": { "plan": "facets" },
  "root": { "id": "n9", "t": "row", "p": {}, "c": [
    { "id": "n6", "t": "col", "p": { "grow": 3 }, "c": [
      { "id": "n1", "t": "searchBox", "p": { "key": "q" } },
      { "id": "n2", "t": "toggle", "p": { "key": "codeOnly", "label": "Only sections with code" } },
      { "id": "n3", "t": "status", "p": {} },
      { "id": "n4", "t": "facets", "p": { "key": "filters", "fields": ["type", "tags", "status"], "from": "text", "max": 10 } },
      { "id": "n5", "t": "results", "p": { "limit": 15 } } ] },
    { "id": "n8", "t": "col", "p": { "grow": 2 }, "c": [
      { "id": "n7", "t": "chunk", "p": { "id": { "$fn": "s => s.selected" } } } ] } ] },
  "digest": "04b0e5487dc484"
}
```

Three details in this output are worth reading slowly. Every node has the same three fields, `id`, `t` (its type) and `p` (its props), plus `c` for children; there is nothing else to learn. The only lambda in the page is the chunk viewer's `id`, stored as its source text. And the ids are numbered in the order nodes were finished, not the order they appear: the search box is `n1` and the row that contains everything is `n9`, because a container's children are built inside its callback before the container itself is appended.

A lambda prop is stored as text and compiled with the same guard as a pipeline lambda, so it cannot see the variables around it. This is the rule from chapter 9, and it applies with the same error message. Compiling a page whose lambda refers to a local `limit` and then rendering it gives:

```
gui bad references free identifier `limit`. Lambdas are stored as source and run without their closure:
use their arguments, a signal, or rag.bake.
```

When a value is known at build time and should not change, a bake avoids the question. `gui.bake(f)` calls `f` while the view is being built and stores the result, so the compiled tree holds a plain value:

```js
gui.app('b').state({ n: 1 }).view(v => v
  .text(gui.bake(() => 'built at ' + 'compile'))
  .text(s => 'n is ' + s.n))
// root.c[0].p.md → "built at compile"
// root.c[1].p.md → { "$fn": "s => 'n is ' + s.n" }
```

Lambda props receive `(s, ctx)`. `s` is the page's state, which includes every plan signal, every page key, every derived key, and four keys the runtime maintains: `out` (each node's output from the last run, by node id), `result` (the whole run result), `run` (status, trigger, time and Claude calls of the last run) and `stream` (partial answer text while Claude is writing). `ctx` carries the actions a lambda may take:

| `ctx` member | Does |
| --- | --- |
| `ctx.set(patch)`, `ctx.get(key)` | Write or read state. |
| `ctx.run(trigger)`, `ctx.submit(patch)` | Start a query run, as typing or as Enter. |
| `ctx.rag.chunk(id)`, `.doc(id)`, `.chunks(f)`, `.docs()` | Read the built corpus. |
| `ctx.rag.explain(id)`, `.result()`, `.ir()` | Read the last run and the plan. |
| `ctx.toast`, `ctx.copy`, `ctx.download`, `ctx.log` | Talk to the host page. |
| `ctx.ws`, `ctx.evals`, `ctx.ui`, `ctx.lib` | The workspace, the eval set, the workbench UI, and the `rag` and `gui` libraries. |

Handler props receive a third argument describing the event: `onRowClick(s, ctx, row)`, `onClick(s, ctx, ev)` on a canvas, `item(s, ctx, hit)` on results, `onCite(s, ctx, evidence)` on an answer.

Why build the tree only once and evaluate lambdas on every render, instead of rerunning the whole `view` callback? Because a tree built once has stable node ids, and stable ids are what let the runtime replace one component without touching its neighbours. A callback rerun on every change would produce a new tree each time, and the runtime would have to compare two trees to find what changed. The workbench avoids that comparison entirely: structure is fixed at build time, and only lambdas vary.

### 24.6 `canvas`: drawing with `(s, ctx, g)`

When no component draws what you need, `canvas` hands you a drawing surface. Its `draw` lambda receives a third argument, `g`, with a small set of primitives: `clear`, `line`, `polyline`, `rect`, `circle` and `text`, plus `g.width`, `g.height` and `g.theme`, the page's colours read from its CSS variables. Coordinates are in the canvas's own units (the `width` and `height` props), and the canvas scales to its column.

The Result map preset draws one row per document and one dot per hit, further left for better ranks:

```js
.canvas((s, ctx, g) => {
  g.clear();
  const docs = ctx.rag.docs();
  const hits = ((s.out || {}).text || []).slice(0, 40);
  const L = 150, T = 14, rowH = (g.height - T - 8) / Math.max(1, docs.length);
  docs.forEach((d, i) => { /* label and a rule per document */ });
  hits.forEach((h, r) => {
    const ch = ctx.rag.chunk(h.id);
    const i = docs.findIndex(d => d.id === ch.docId);
    const y = T + i * rowH + rowH / 2, x = L + 8 + (r / 40) * (g.width - L - 30);
    const on = s.selected === h.id || s.hover === h.id;
    g.circle(x, y, Math.max(3, 8 - r / 6), { fill: on ? g.theme.accent : g.theme.ink, id: h.id });
  });
}, { width: 720, height: 300, label: 'Hits by document and rank',
  onClick: (s, ctx, ev) => { if (ev.hit) ctx.set({ selected: ev.hit }); },
  onHover: (s, ctx, ev) => ctx.set({ hover: ev.hit }) })
```

A shape drawn with an `id` becomes a target: the canvas remembers its geometry, and a click or a hover reports `ev.hit`, the id of the topmost shape under the pointer. That is the whole of the canvas's interaction model, and it is enough to make the map clickable without any DOM per dot.

The primitives check their arguments. A coordinate that is not a finite number throws `g.circle(x, y, r, o): coordinates must be finite numbers`, and the error is painted on the canvas itself in the alert colour rather than thrown into the page. The canvas is also the one component that redraws in place: when state it read changes, it calls `draw` again on the same element instead of being replaced.

The key points of this chapter:

- A page is a JSON tree of `{ id, t, p, c }` nodes, built once by the `view` callback and compiled with a content digest, exactly like a plan.
- A prop is a value, a lambda evaluated on every render, or a bake evaluated once at build time; lambdas are stored as source and cannot see local variables.
- `use(plan)` records only the plan's id, so the page follows whatever pipeline the workspace holds now.
- Controls write state keys by name, and a key that is also a plan signal reruns the search; the control does not need to know which kind of key it writes.
- A row wraps its children at 280 px, which is the only layout rule a preset author has to know.

The tree says what the page is. It does not say when any part of it is drawn again. That is the runtime's job, and the next chapter shows how it decides, component by component, what a change affects.

**Lab: Corpus dashboard, Result map.** Load Corpus dashboard and read its code next to the page: every chart is one `bar` call over `ctx.rag.chunks()`. Delete the bare `s.out;` line from the "Chunks per document" chart, upload a markdown file in the Files tab, and notice that this chart, alone, no longer updates. Then load Result map, search for something, and click a dot; change `Math.max(3, 8 - r / 6)` to a constant radius and see how much of the rank signal the dot size was carrying.

## 25. A reactive runtime

This chapter explains how the page decides what to draw again. A view tree from chapter 24 is drawn once when it is mounted. After that, every change to state, whether it comes from a keystroke, a slider, a facet click or a finished query run, has to reach the parts of the screen that show it and no others. By the end of the chapter you should be able to look at any component and say exactly which state keys make it redraw, and to explain why typing a sentence into the search box starts one query run rather than eighteen.

The runtime has one rule, and the rest of the chapter is consequences of it: **a component redraws when a key it read during its last render changes.** It does not redraw because its parent redrew, because a key it might read changed, or because time passed. The runtime knows which keys a component read because it watched the component read them.

### 25.1 Read tracking through a proxy

When the runtime renders a component, it creates an empty set of dependencies for it and hands every lambda prop a state object that records reads. The state object is a JavaScript `Proxy` whose `get` trap adds the key's name to that set before returning the value. This is the code, from `Runtime.view` in `gui.js`:

```js
view(deps) {
  return new Proxy({}, {
    get: (t, k) => { if (typeof k !== 'string') return undefined; if (deps) deps.add(k);
                     return k in this.der ? this.der[k] : this.state[k]; },
    // has, ownKeys and getOwnPropertyDescriptor make `in` and spreading work
  });
}
```

The chunk viewer's prop `s => s.selected` therefore leaves `{ selected }` in its dependency set after it renders, and nothing else. The Corpus dashboard's `stats` lambda reads `s.out` on purpose, without using the value, so that `out` lands in its set; that is the whole meaning of the bare `s.out;` line in chapter 24. A lambda that reads nothing from `s` never redraws, which is correct for a lambda whose output cannot change and surprising for one that reads the corpus through `ctx.rag` and expects to follow rebuilds.

Built-in components that do not take lambdas declare their reads directly. The status line adds `run` and `out`; the results list adds `run`, `q`, `out` and its selection key; a slider adds its own key. The table below lists them, because a designer who wants to know why a panel flickers needs exactly this list.

| Component | Keys it reads, besides its lambda props |
| --- | --- |
| `searchBox` | `run`, its key (default `q`) |
| `status` | `run`, `out` |
| `results` | `run`, `q`, `out`, its selection key (default `selected`) |
| `facets` | its key (default `filters`), `out` |
| `answer` | `out`, `stream`, `run` |
| `trace`, `dag` | `result` |
| `slider`, `number`, `select`, `toggle`, `input` | their key |
| `tabs` | its tab key |

Read tracking is dynamic, not static. A lambda such as `s => s.doc && !s.q` reads `doc` always but reads `q` only when `doc` is truthy, so its dependency set changes from render to render. This is the same distinction chapter 9 drew for pipeline lambdas, where a static scan of the source finds the signals a lambda might read and runtime tracking records the ones it did read. The page uses only the runtime half, because a page component is cheap to redraw and a missed dependency would show stale data.

### 25.2 From a state change to a redraw: set, derive, refresh

Every change goes through one method, `set(patch, source, opts)`. It does five things in order:

1. It applies the patch to state and remembers the old values.
2. It recomputes every derived key, repeating up to four passes until no derived value changes, so a derived key may depend on another derived key.
3. It collects the set of keys whose values actually changed, by identity for state keys and by identity or JSON equality for derived keys. If the set is empty it stops.
4. It calls `refresh(changed, source)`, then every watcher whose key changed.
5. If any changed key is a plan signal, it schedules a query run (25.4).

`refresh` walks the mounted components. For each one still in the page, it skips the component that caused the change (the `source`, usually the input you are typing into, so that your cursor is not destroyed), and it redraws the component if any of its dependencies is in the changed set. Redrawing means rendering the node again from the same tree and replacing its element. Two components instead update their existing element in place: the search box, which must keep focus while results stream in, and the canvas, which repaints rather than being replaced.

Here is what that looks like when measured. The script for this chapter loads the Ranking playground in a headless browser with a mocked Claude, wraps `refresh` to record which components it redraws, and moves the heading-boost slider from 1 to 2. Three refreshes are recorded:

```
changed: [headingBoost]          redraw: []                                     source: n3 (the slider)
changed: [run]                   redraw: [searchBox (in place), status, results]
changed: [out, result, run]      redraw: [searchBox (in place), status, results, dag]
```

The first refresh redraws nothing. The slider is the only component that read `headingBoost`, and it is the source, so it is skipped; its number label was already updated by its own event handler. The second refresh comes from the runtime marking the run as started, and redraws the three components that show run status. The third comes from the finished run and adds the DAG, which reads `result` to colour the nodes that ran. The knobs card, the other three sliders and the section viewer are not touched at any point.

The same trace shows a cost worth knowing about. `results` reads `run` so that it can say “Searching…” when the list is empty, and that read makes it redraw at the start of every run as well as at the end. On a list of twelve results that costs well under a millisecond. On a component that does real work in its render, it is worth checking which of its reads are necessary.

### 25.3 Plan signals are state keys: the bridge between the page and the session

A page bound to a plan with `use(plan)` gets the plan's signals as its initial state. The runtime copies `ir.signals` into state when it is constructed, then layers the page's own `state({...})` on top, then adds `out`, `run`, `stream` and `result`:

```js
this.state = { ...signals, ...this.ir.state, out: {}, run: { status: 'idle' }, stream: null, result: null };
this.signalKeys = new Set(Object.keys(signals));
```

From then on, there is no separate signal store. The search box writes `q`, a facet writes `filters`, a slider writes `headingBoost`, and each of those writes is an ordinary `set`. The only thing that marks them as signals is membership in `signalKeys`, which `set` checks at step 5. When a run starts, the runtime collects the current values of exactly those keys and passes them to the session:

```js
signals() { const o = {}; for (const k of this.signalKeys) o[k] = this.state[k]; return o; }
```

When the run finishes, it writes the session's per-node outputs to `out`, the run result to `result`, and a summary to `run`. Any component can then show any node's output by reading `s.out[nodeId]`, which is how the Compare stages preset puts three nodes' results side by side.

The bridge works in the other direction too. Because the session remembers which signals each pipeline node read (chapter 12), the slider move above reran only the nodes that read `headingBoost`. The trace of that run, read from `state.result`, is:

```
text        cached   in 1   out 40
failures_2  cached   in 1   out 6
fuse        cached   in 2   out 46
rerank      ran      in 46  out 46
limit       ran      in 46  out 8
```

The page redrew four components and the pipeline reran two nodes. Neither side had to be told about the other's bookkeeping; each tracked its own reads.

### 25.4 Scheduling query runs by trigger

Chapter 12 defined the two triggers: a `change` run happens while the user types and runs only nodes whose trigger is `change`, and a `submit` run happens on Enter and runs everything. This section covers how the page decides when to start each kind.

&#91;embedded content: one keystroke · set, refresh, debounce, run, refresh\]

When `set` sees a changed signal, it calls `schedule`. A `submit` is started immediately. A `change` waits: `schedule` clears any pending timer and sets a new one for 140 ms. A key pressed within that window cancels the previous timer, so a burst of typing produces one run after the user pauses. Measured in the browser, typing “why no app actions” (18 characters, 40 ms apart) into the Reports preset started exactly one run:

```
runs: [ { trigger: 'change', t: 3245 } ]
```

Enter bypasses the timer. The search box handles the key itself: it writes `q` with `noRun: true`, so the write does not schedule a `change` run of its own, then calls `runNow('submit')`. Other ways to start a run follow the same pattern. `ctx.run(trigger)` starts one directly, and `ctx.submit(patch)` writes a patch without scheduling and then submits.

Runs can overlap: the user may press Enter and keep typing while Claude is answering. The runtime handles this with two mechanisms. Each run gets an increasing sequence number, and a finished run writes its outputs only if its number is still the latest, so a slow early run cannot overwrite a fast later one. And starting a run aborts the previous one through an `AbortController` whose signal is passed to the session and from there to every Claude call, so an abandoned answer stops spending. While a `submit` run is in flight, the search box's button reads Stop, and clicking it aborts the run and marks the page as done.

While Claude streams an answer, the session reports partial text through `onText`. The runtime writes it to the `stream` key at most once every 60 ms, which redraws the answer component and nothing else, because only the answer reads `stream`.

### 25.5 Errors that stay inside the component that raised them

A page made of lambdas will contain lambdas that throw. The runtime's position is that one broken component must not take the page down with it, and it implements that position separately for each place a lambda can run.

| Where the error happens | What the user sees |
| --- | --- |
| A prop lambda during render | That component is replaced by one red line, `✗ text: <message>`. Its neighbours render normally. |
| A canvas `draw` lambda | The message is painted on the canvas. The element stays, so later state changes can repaint it. |
| A handler (`onClick`, `onRowClick`, a watcher) | The message appears in the app's error bar at the top, `✗ <message>`. The page state is unchanged. |
| A derived key | The key becomes `undefined`, and the error is kept on the runtime. |
| A query run | The run's status becomes `error` or `partial`, and the status line shows the message. |

The evidence is a small app mounted in the browser, with one healthy `text`, one `text` whose lambda reads a missing property, a `stat`, a canvas that draws a circle with an undefined radius, and a button whose handler throws:

```js
gui.app('errs').state({ n: 1 }).view(v => v.col(c => c
  .text(s => 'n is ' + s.n)
  .text(s => s.missing.deep)
  .stat('n', s => s.n)
  .canvas((s, ctx, g) => { g.circle(10, 10, s.nope); }, { width: 100, height: 40 })
  .button('Break', (s, ctx) => { throw new Error('handler failed'); })));
```

After mounting, clicking Break, and setting `n` to 2, the page's components read:

```
n1 g-md:          n is 2
n2 g-err-inline:  ✗ text: Cannot read properties of undefined (reading 'deep')
n3 g-stat:        2 n
n4 g-canvas:      (message painted: g.circle(x, y, r, o): coordinates must be finite numbers)
n5 btn:           Break
error bar:        ✗ handler failed
```

The healthy text and the stat both followed `n` from 1 to 2. The broken text stayed broken and stayed small. The button still works and can be clicked again. Nothing in the page was blank.

The key points of this chapter:

- A component redraws when, and only when, a key it read during its last render changes; the runtime learns the reads by handing lambdas a recording proxy.
- Every change goes through `set`, which recomputes derived keys, redraws the readers of the changed keys except the component that caused the change, calls watchers, and schedules a run if a signal changed.
- Plan signals are ordinary state keys that the runtime also passes to the session, so a control does not need to know whether it is driving the page or the pipeline.
- Typing schedules one `change` run 140 ms after the last key; Enter starts a `submit` run at once; a newer run aborts and outranks an older one.
- An error is shown at the smallest scope that contains it: the component, the canvas, the error bar, or the status line.

The runtime is general: nothing in this chapter is specific to search. The next chapter turns to the components that are, and shows how each one reads the plan's outputs.

**Lab: Ranking playground.** Load the preset, open the Inspect panel's Trace tab, and drag one slider at a time. For each slider, predict from the plan which pipeline nodes will show `ran` and which `cached`, then check. `rrfK` is read by `fuse`, so moving it reruns three nodes; `headingBoost` and `failureBoost` are read by the rerank lambda, so they rerun two. Then type a sentence quickly and watch the status line change once, not once per key.

## 26. Search components

This chapter covers the nine components that know about search: `searchBox`, `results`, `hits`, `facets`, `answer`, `chunk`, `trace`, `dag` and `status`. They are not special to the runtime. Each is an ordinary component that reads state keys, and the keys it reads are the plan's signals and the run's outputs described in chapter 25. What makes them search components is what they read: `s.q`, `s.filters`, `s.out[nodeId]`, `s.run`, `s.result`. By the end of the chapter you should know, for each one, which node's output it shows by default, how to point it at a different node, and what it writes back.

The central fact is that **every node's output is in `s.out`, keyed by node id.** A results list does not show “the results”; it shows the hits output of one particular node, chosen by a rule you can override. Once that is clear, layouts such as the Compare stages preset, which puts three nodes of the same plan side by side, stop looking like special features:

```js
.grid(3, g => g
  .card('text · BM25', b => b.results({ from: 'text', limit: 8 }))
  .card('fuse · RRF with meta', b => b.results({ from: 'fuse', limit: 8 }))
  .card('rerank · heading λ', b => b.results({ from: 'limit', limit: 8 })))
```

### 26.1 `searchBox`: a query signal, a submit action, and a box that keeps focus while results redraw

`searchBox(key, o)` draws a text field and a button. It is bound to a state key, `q` by default, and takes `placeholder` and `submitLabel` (default “Ask”). It has three behaviours, each a direct consequence of chapter 25:

- Typing writes the key on every input event, with the box itself as the change's source. The write is a signal change, so it schedules a `change` run 140 ms later, and the box is not redrawn by its own write.
- Enter, or a click on the button, writes the key with `noRun: true` and then starts a `submit` run. This is the only path that runs nodes with a `submit` trigger, which in the bundled presets means the only path that calls Claude.
- While a `submit` run is in flight, the button reads Stop. Clicking it aborts the run through its `AbortController` and marks the page idle.

The search box reads `run` (to switch between Ask and Stop) and its key, so it is affected by every run. If it were redrawn like other components, its `<input>` element would be replaced twice per run, and the user would lose focus and cursor position in the middle of typing. It is the one component besides the canvas that redraws in place: its `_redraw` updates the button's label and, only if the input does not have focus, copies the key's value into the field. The browser test confirms the effect: after pressing Enter and waiting for the streamed answer to finish, `document.activeElement` was still the search field.

The “only if not focused” condition matters in one case. When something other than the box writes `q`, such as the agent's `ui.query(...)` or a lambda calling `ctx.set({ q })`, the field shows the new value only when the user is not typing in it. The user's keystrokes win.

### 26.2 `results` and `hits`: showing any hits node, not only the last one

`results(o)` shows a ranked list from `s.out[from]`. When `from` is not given, it uses `ir.outputs.hits`, which the compiler derives (chapter 10) by this rule, in order:

1. an output the plan pinned with `.output('hits', node)`;
2. otherwise the last `rerank` node;
3. otherwise the node that feeds `pack`;
4. otherwise the last node whose output type is `hits`.

The rule is chosen so that the default list shows what the answer will be built from. It has a consequence that is easy to miss, and the Ranking playground shows it. Its plan ends in `rerank(…)` followed by `limit(s => s.k)`, so the derived output is `rerank`, not `limit`. Setting the `k` slider to 3 changes the limit node's output and nothing the page shows:

```
outputs:            { hits: 'rerank' }
before:  k = 8   results drawn: 12   out.limit.length: 8
after:   k = 3   results drawn: 12   out.limit.length: 3   status: “46 results · lexical · 0.1 ms”
```

The list shows the first 12 of rerank's 46 hits, because 12 is the component's own default `limit`. The fix is one prop, `results({ from: 'limit' })`, or one line in the plan, `.output('hits', 'limit')`. The lesson for a page author is to name `from` whenever the plan's last hits node is not a rerank.

Each result is drawn from the hit and from the chunk it points to:

| Part | Source |
| --- | --- |
| Rank, zero-padded | position in the list |
| Title | the last heading of the chunk's heading path, or the document title |
| Chips | the nodes that contributed the hit: the keys of `hit.parts` after fusion, or `hit.via`; plus the chunk's kind if it is not a section |
| Location line | document title › the rest of the heading path |
| Snippet | 260 characters of the chunk's text, starting near the first query word, with query words marked |
| Reason | `hit.reason`, prefixed with ↳, when a rerank gave one |

The first result of the playground after a search for “Why was the orders tab empty?” shows every part:

```
01  Empty orders tab on load                                   RERANK  FAILURE
TTC Customer Clustering — Order-Grain Segmentation and the Shiny Explorer › The Shiny explorer › Failure modes encountered
Failure: Empty orders tab on load Mechanism: Event reactive never evaluates before first button click Fix: Plain reactive with debounce
↳ heading 1.00 · failure
```

The `FAILURE` chip says this chunk came from the failure-table chunk stream (chapter 4), and the reason line is the string the rerank lambda returned. When the defaults are wrong for a corpus, the `item` prop replaces the title, location line or snippet: `item: (s, ctx, hit) => ({ title, sub, snippet })`. Clicking a result writes its id to `selected`, or to `selectedKey` if given, unless an `onSelect` handler takes over. The list shows `limit` results (default 12) and a count of the rest.

`hits(list, o)` is the compact sibling. It takes a list rather than a node id, so it can show hits that never passed through the plan: a list a lambda computed, or a list the agent wants to put in the chat. Each row shows rank, title, document, score and reason, and a click opens the section in the workbench.

### 26.3 `facets`: counts from the result set, written back as a filter signal

`facets(key, o)` draws one row of chips per metadata field, each chip a value with a count. It is bound to a state key, `filters` by default, holding an object from field name to a list of chosen values. The plan reads that key in `filter.where()` (chapter 6), so a click on a chip is a signal change and reruns the plan.

The question a facet component has to answer is: counts of what? If it counted the hits after filtering, choosing `tags: architecture` would make every other tag's count drop to zero, and the user could no longer see what choosing another tag would give. So by default `facets` counts the input of the plan's first filter node, the hits before filtering. The Faceted search preset names that node explicitly with `from: 'text'`. Before and after clicking the `architecture` chip, the chip rows read:

```
type    project-report 79   spec 1
tags    architecture 79   pbui 79   project-report 79   typescript 79   workbench 79   testing 61   sync 60 …
status  complete 79   proposed 1

after the click:   filters = { tags: ['architecture'] }   (counts unchanged)
```

The trace of the run the click caused shows the other half of the design: `retrieve` was not rerun, because its inputs had not changed, and only the nodes after it ran.

```
text       cached   in 80  out 80
filter     ran      in 80  out 79
code_only  ran      in 79  out 79
limit      ran      in 79  out 30
```

Three more details complete the component. Choices within one field are alternatives, and choices across fields must all hold, because `filter.where` keeps a hit when, for every field in the filter, the hit's value is one of the chosen values. A chosen value that no longer appears in the counted hits is still drawn, with a count of 0, so the user can always unchoose it. And clicking the last chosen value of a field removes the field from the object, so `filters` returns to `{}` rather than to `{ tags: [] }`. Each field shows at most `max` values (default 8); with no hits, the component says “Facets appear once there are results.”

### 26.4 `answer` and `chunk`: streamed text, citation links, the section viewer

`answer(o)` shows `s.out[from]`, by default the plan's answer output. While a `submit` run is streaming, it shows `s.stream` instead, the partial text written every 60 ms (25.4). The header counts citations, and the body renders the answer's markdown with every `[E1]`-style label turned into a button. Below the text, a list shows the evidence that was cited, with its section and document. In the browser test, with Claude replaced by a mock that returns a fixed two-sentence answer, the component read:

```
ANSWER                                                        2 of 4 sources cited
ChatGPT sent sessionless server/discover with MCP 2026-07-28, which the stateful transport did not accept E1.
Making Streamable HTTP stateless fixed it E2.
E1  12. Stateful versus stateless Streamable HTTP · PROJECT REPORT - CoinVault MCP - From Local Con…
E2  14. Evidence returned by knowledge_search · PROJECT REPORT - CoinVault MCP - From Local Con…
```

“2 of 4” says the answer cited two of the four sections it was given. Clicking E1 wrote that section's id to `selected`, and the section viewer next to it showed `… › 12. Stateful versus stateless Streamable HTTP`. An `onCite` handler replaces that behaviour.

The answer has four other states, each with its own text. Before any `submit` run it says “Press Enter in the search box for a cited answer. Typing only runs the lexical nodes.” While the run has started but no text has arrived, it says “Thinking…”. When the run reported errors, they appear in a red line under the body. And when the plan has no answer node, it says so.

One behaviour follows from chapter 12 and surprises users. The answer node's trigger is `submit`, so on a `change` run it is skipped and its output is `null`. Typing one more character after an answer has appeared therefore clears it: in the test, after the answer above, typing “ x” returned the component to “Press Enter…”. The answer belongs to the question it answered, and the page does not show it under a different question. Chapter 27 discusses whether that is the right choice.

`chunk(id, o)` shows one section. Its `id` prop is almost always the lambda `s => s.selected`, which makes the viewer follow whatever was last clicked in a result list, an answer citation, a table row or a canvas dot. It shows the section's token count and id, its full heading path, its text rendered as markdown, and three buttons: previous and next section of the same document, which rewrite `selected`, and “Why this?”, which opens the inspector's Trace tab on `rag.explain` for this chunk (chapter 13). With nothing selected it says “Select a result to read the section.”

### 26.5 `trace`, `dag` and `status`: the pipeline as part of the page

The last three components show the pipeline itself. They exist because chapter 2's argument, that a search pipeline should be something you can see into, applies to the people using a search page as much as to the people building it.

`status(o)` is one line. It reads `run` and `out` and prints one of four things:

| State | Text |
| --- | --- |
| no run yet | (empty) |
| running | `Searching…` on a `change` run, `Answering…` on a `submit` run |
| failed | `✗` and the message, in the alert colour |
| done | `<n> results · lexical · <ms> ms` after typing; `reranked` in place of `lexical` after Enter; `· <c> Claude calls` appended when the run made any |

The browser test recorded the status line through one typed question and one Enter:

```
42 results · lexical · 1.3 ms
Answering…
3 results · reranked · 406.6 ms · 3 Claude calls
```

“Searching…” never appeared, because a lexical run over these reports finishes within one animation frame. The word after the count is chosen by trigger, not by what ran: a `submit` run is labelled “reranked” even if the plan has no rerank node. The Claude-call count is the run's own count from the session, so it is exact; in this test the three calls went to the mock.

`trace(o)` is a table of the last run's trace: node, status, items in, items out, milliseconds, one row per live node, with each row styled by its status. It is the same data as the inspector's Trace tab, small enough to put in a page. `dag(o)` draws the plan's graph using the workbench's own renderer in compact form, coloured by the last run's statuses. By default it shows only live nodes, because the build half of a pipeline does not change while someone searches; `dag({ all: true })` shows both. The Answer first preset puts the two together in a tab:

```js
.tab('Trace', b => b.trace().dag())
```

The key points of this chapter:

- Every search component reads ordinary state: `q`, `filters`, `selected`, `run`, `result`, and `out[nodeId]`.
- `results`, `facets` and `answer` take a `from` prop; without it they use the plan's derived outputs, and the derived hits output is the last rerank, not the last node.
- The search box schedules `change` runs while typing, starts a `submit` run on Enter, offers Stop during a submit, and keeps focus because it updates in place.
- Facets count the hits before filtering and write an object of chosen values that `filter.where` reads; values within a field are alternatives, fields are combined.
- The answer streams, turns `[E#]` labels into buttons that select the cited section, and clears when the question changes.
- `status`, `trace` and `dag` put the run's cost and path on the page itself.

These components are mechanisms. Whether a page should show the answer first, what a result should say, and what to do when nothing is found are decisions about people, not code, and the next chapter takes them up.

**Lab: Compare stages, Answer first, Document browser.** In Compare stages, type “cursor semantics tension” and watch sections move between the three columns as each node reorders them; then change the third card to `from: 'rerank'` and confirm it shows the same list as `from: 'limit'` up to the limit. In Answer first, press Enter, open the Trace tab, and match the `trace` rows against the `dag`'s colours. In the Ranking playground, set k to 3 and see the list stay at twelve; add `from: 'limit'` to its `results()` call and set it again.

## 27. Designing search experiences

This chapter is about the decisions a search page is made of, and it is written first for designers. The previous three chapters described mechanisms: a grammar, a runtime, a set of components. None of them says what a search page should do when the user presses Enter, what a result should tell them, or what to show when nothing matches. Those are decisions about time, trust and cost, and every search page makes them, deliberately or by default. The workbench makes each one in a specific way, and the point of this chapter is to name each decision, show what the workbench does, show the evidence, and say plainly where it does not yet handle a case.

Three quantities shape almost every decision below, so it is worth fixing them first.

| Quantity | Typing (`change` run) | Enter (`submit` run) |
| --- | --- | --- |
| Time, measured in the browser | 1.3 ms for 42 results | 406.6 ms, of which about 350 ms is the mock's artificial delay; real Claude calls take seconds |
| Claude calls | 0 | 3 in the Reports preset: rewrite, rerank, answer |
| What the user gets | a ranked list of sections | a reranked list and a cited answer |

One interaction is a thousand times cheaper and faster than the other. A design that treats them alike will either make typing slow or make answers feel free.

### 27.1 Type versus Enter: instant ranking, deliberate answers

The first decision is what typing does. The workbench's answer is that typing searches and Enter answers. While the user types, the page reruns the lexical part of the pipeline 140 ms after they pause, and the result list follows their words with no visible delay. Nothing that calls Claude runs until they press Enter or the Ask button. Chapter 12 described the mechanism, per-node triggers; this section is about why it is the right default.

Typing is exploration. A user typing “orders tab” is not yet asking a question; they are watching the list to see whether the corpus uses their words. Lexical results are the right feedback for that, because they respond to exactly the words typed and they cost nothing. An answer is a commitment: it takes seconds, it spends the user's Claude usage, and it is only useful for a finished question. Running it on every pause would spend calls on fragments such as “why was the ord” and would replace the answer under the user's eyes while they are still writing.

The page tells the user the rule in two places. The answer panel's empty state says “Press Enter in the search box for a cited answer. Typing only runs the lexical nodes.”, and the Answer first preset's placeholder says “Ask a question and press Enter…”. The status line then confirms which kind of run happened: `lexical` after typing, `reranked` and a call count after Enter.

A designer can move the line between the two by changing triggers, not pages. A rerank lambda with no Claude call runs on every keystroke (the Ranking playground's rerank does); setting a node's trigger to `submit` with a `trigger` op holds it back for Enter. The page needs no change, because the search box only ever says “this is typing” or “this is Enter”.

### 27.2 What a result shows: title, heading path, snippet, score, reason

The second decision is what one result says. A result is a claim that this section is relevant, and the user's job is to judge the claim in a second or two. Every part of the workbench's result row exists to help with that judgement, and each is shown or omitted for a reason.

| Part | Shown | Why |
| --- | --- | --- |
| Section heading as the title | yes | The unit of retrieval is a section (chapter 4), so the section's own heading names it better than the document's title does. |
| Document and heading path | yes | Users trust a section more when they can see where it lives. “The Shiny explorer › Failure modes encountered” tells them what kind of text to expect. |
| Snippet near the first query word, with matches marked | yes | It shows the words that matched in context, which is the evidence for the lexical claim. |
| Retriever chips (`TEXT`, `RERANK`, `FAILURE`) | yes | They tell a pipeline builder which node found the hit. For end users they are noise, and the workbench does not yet have a prop to hide them. |
| Rerank reason | when present | A rerank that explains itself (“names the discover/stateless cause”) gives the user a second, semantic claim to judge. |
| Score | no, in `results`; yes, in `hits` | A BM25 score of 12.68 means nothing to a reader and invites comparisons across queries that are not valid. The compact `hits` list, used mostly by the agent, shows it. |

When the defaults are wrong for a corpus, the `item` prop replaces the title, the location line or the snippet with a lambda over the hit. A corpus of short notes might use the document title as the title; a corpus of code might show the first line of the function instead of a snippet. The row's layout and order stay the same, which keeps lists from different presets readable in the same way.

### 27.3 Answer first or results first

The third decision is which comes first on the page. The workbench offers both arrangements, and neither is the default for every corpus.

The standard layout, `kit.searchApp`, puts results first: a search column that takes three fifths of the width, with the answer and the section viewer in a column beside it. It suits users who are exploring, who will read sections themselves, and who press Enter only for some questions. The answer is there when asked for and out of the way when not.

The Answer first preset puts the answer first: a search box, a status line, and three tabs, Answer, Sources and Trace. It suits users who came with a question and want a sentence back. Sources are one click away rather than always visible, which is a statement about how much the designer expects users to check.

|  | Results first (`kit.searchApp`) | Answer first (preset) |
| --- | --- | --- |
| First thing seen | ranked sections, instantly | an empty answer panel until Enter |
| Cost of a visit with no Enter | 0 Claude calls, useful page | 0 Claude calls, nearly empty page |
| Checking a claim | citation click shows the section beside the answer | citation click shows the section under the answer; the list is on another tab |
| Best for | exploring a corpus | asking it questions |

One behaviour applies to both layouts and is a decision in its own right. Chapter 26 showed that the answer clears as soon as the user types again, because the answer node is skipped on `change` runs and its output becomes empty. The benefit is that the page never shows an answer beneath a question it did not answer. The cost is that a user refining a question loses the answer they were refining it from. A third option, keeping the previous answer visible but marked as an answer to the previous question, is not implemented in the workbench; it would need the answer component to remember the question each answer was given for.

### 27.4 Citations the reader can check in one click

The fourth decision is how a reader checks an answer. An answer generated from retrieved text is only as trustworthy as the reader's ability to see that text, so the workbench treats every citation as a link to its section.

The pieces were described in chapter 26: `[E1]` labels in the answer become buttons, a click writes the cited section's id to `selected`, and the section viewer shows that section with its full heading path. In the browser test, clicking E1 in the mocked answer showed `… › 12. Stateful versus stateless Streamable HTTP` in the viewer, one click from claim to source. The header's “2 of 4 sources cited” adds a second signal: the reader knows the answer was built from four sections and relied on two, and can open the other two from the Sources list if they want to know what was left out.

Two gaps are worth naming. The answer does not mark sentences that carry no citation, so an uncited claim looks the same as a cited one; the answer prompt asks Claude to cite, and the `answerHas` and judge checks of chapter 20 measure how well it does, but the page does not show it. And the section viewer shows the whole section, not the sentences that support the claim, so on a long section the reader still has to find the passage.

### 27.5 Empty, loading, partial and failed states

The fifth decision is what each part of the page says when it has nothing normal to say. Most of a search page's states are not “showing results”. The table lists every such state the workbench's components draw, with the exact text, collected from the code and confirmed in the browser where marked.

| Component | State | Text |
| --- | --- | --- |
| results | nothing typed | “Type a question about the reports.” (confirmed) |
| results | no matches | “No sections match. Try other words, or clear a filter.” (confirmed) |
| results | running, list empty | “Searching…” |
| facets | no hits | “Facets appear once there are results.” |
| answer | before Enter | “Press Enter in the search box for a cited answer. Typing only runs the lexical nodes.” (confirmed) |
| answer | waiting for the first token | “Thinking…” |
| answer | nothing retrieved | “No evidence was retrieved for this question.” (written by the answer node, without a Claude call) |
| answer | run errors | the errors, in a red line under the body |
| chunk | nothing selected | “Select a result to read the section.” |
| trace | no run yet | “No run yet.” |
| status | running | “Searching…” or “Answering…” |
| status | failed | “✗” and the error, in the alert colour |

The no-match text is the most important line in the table. It tells the user what to do next, and it names the two causes a user can act on, their words and their filters. The empty-query text does a different job: it tells a first-time user what the box is for.

Three cases are not handled well, and a designer adapting the workbench should know them.

- The empty-state texts mention “the reports” and are built into the components, so a page over a different corpus shows the wrong noun. Only the search box's placeholder and button label are props.
- A run that finishes with errors in some nodes has status `partial`, and the status line prints it exactly like a successful run. The errors are visible only in the answer panel, so a page without an answer component does not show them at all.
- “Searching…” is almost never seen. Lexical runs over this corpus finish within one animation frame, which is good, but on a larger corpus there is no progress indicator between the old list and the new one.

### 27.6 Marking the actions that cost Claude calls

The sixth decision is how much the page says about cost. Claude calls spend the user's usage, and a user cannot make sensible choices about an action whose cost is hidden. The workbench marks cost in several places, before and after the fact.

| Where | What it says | When |
| --- | --- | --- |
| Preset picker | an `enter` or `build` tag on presets with Claude steps, and “Enter spends Claude calls.” or “Build spends Claude calls.” in the description | before loading |
| DAG | an `llm` tag on every node that runs on submit | while building the pipeline |
| Build status | “\<node>: \<n> items wait for Claude · press Build to spend those calls” | before a build with deferred calls |
| Evals tab | a separate “Run with Claude” button, whose tooltip says it spends calls | before running evals |
| Status line | “· 3 Claude calls” | after each run |
| Search box | Stop, while a submit run is in flight | during a run |

The pattern is that the expensive action is always a separate, explicit act (Enter, Build, Run with Claude) and the page says what it spent afterwards. What the workbench does not yet do is say what Enter will cost before it is pressed. The Ask button looks the same on a plan with no Claude steps as on one with three, and the user learns the number only from the status line after the run. An estimate is available in principle, because the plan knows which nodes have a `submit` trigger and which of them call Claude; it is not drawn.

### 27.7 Phone width: one column, no horizontal scroll

The last decision is what the page does on a phone. The workbench's answer comes from one CSS rule in the grammar (chapter 24): a row's children are at least 280 px wide and wrap. Below about 600 px, every row becomes a column, in source order, and nothing scrolls sideways.

The browser test loaded the Faceted search preset at 390 × 844, the size of a current phone, and measured the page:

```
scrollWidth 390   clientWidth 390        (no horizontal scroll)
column 1 (search, facets, results):  x 46   y 149    width 298
column 2 (section viewer):           x 46   y 3273   width 298
```

At this width the workbench also collapses its own panels into four tabs, Search, Pipeline, Inspect and Agent, so the search page has the screen to itself.

The numbers also show the problem. The section viewer, which sits beside the results on a laptop, is 3,124 pixels below the top of the results on a phone, under fifteen results. Clicking a result writes `selected`, and the viewer updates, but the page does not scroll to it, so on a phone the click appears to do nothing. The workbench does not handle this case. A page designed for phones can avoid it within the grammar: put the viewer in a `when(s => s.selected, …)` above the results, or put results and section in two tabs, so that selection changes what is on screen rather than what is far below it.

The key points of this chapter:

- Typing and Enter differ in cost by orders of magnitude, so the workbench lets typing search and makes answering a separate, explicit act.
- Each part of a result row exists to help the user judge one claim of relevance; scores are left out of the main list because they mean nothing to readers.
- Results first suits exploring and answer first suits asking; both clear the answer when the question changes, which keeps answers honest and loses context.
- Citations are buttons that open the cited section, and the answer says how many of its sources it used.
- Every component has explicit empty, waiting and error texts, but the texts name “the reports”, partial failures look like success in the status line, and there is no progress indicator for slow lexical runs.
- Cost is marked before every expensive act except Enter, where it is reported only afterwards.
- One wrapping rule gives a single column on phones with no horizontal scroll, but selection does not scroll the section viewer into view.

This completes the account of the page. Part IX turns to the other way of changing the workbench: an agent that edits the same pipeline and draws the same components, using the operations of Part VII and the grammar of this part.

**Lab: Faceted search, Answer first, Document browser, Result map.** Open Faceted search on a phone, or narrow the browser below 600 px, click a result, and find where the section went. Then edit the page so the section appears above the results when one is selected, using `when`. In Answer first, ask a question, then type one more word and watch the answer disappear; decide whether your users would want that. In Document browser, choose a report without typing and notice that the page shows an outline instead of an empty list, an empty state that is useful rather than apologetic. In Result map, compare how quickly you can see which report dominates a query against reading the same information from the result list.
