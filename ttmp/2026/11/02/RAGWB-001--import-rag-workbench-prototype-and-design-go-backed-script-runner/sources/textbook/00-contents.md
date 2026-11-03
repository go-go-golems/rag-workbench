# Search You Can See Into

A textbook on the RAG Workbench: a grammar for search pipelines, the engine that runs it, and the agent that edits it.

Oct 16, 2026 · @Manuel Odendahl

## About this book

This book explains how the RAG Workbench turns a folder of markdown into ranked, cited answers, and why each layer is built the way it is. It follows one question through eleven project reports: *Why did ChatGPT show no app actions after OAuth worked?*

The system has four layers, taken in order. A **grammar** describes a search pipeline as data. A **compiler** turns that description into a typed graph, the IR. An **engine** builds the graph's indexes and runs its query nodes. A **generative UI** and an **agent** sit on top, and both read and change the same graph through the same edit operations.

### Three readers

- **Computer scientists** get information retrieval from first principles (BM25, rank fusion, ranking metrics), a small compiler from specs to a typed DAG, content-addressed incremental computation, and closures that survive serialization.
- **Programmers** get the API, how to write presets and components, how to read a trace when a query misses, and how to change a running pipeline without breaking it.
- **Designers** get the decisions a search experience is made of: what typing does versus Enter, what a result shows, how cost and citations surface, and the grammar that lays out the page.

### Reading paths

| Reader | Read closely | Then | Skim |
| --- | --- | --- | --- |
| Computer scientist | Parts II, III, IV, VI | Parts V, VII | Parts VIII, X |
| Programmer | Parts I, III, IV, VII | Parts V, VI, IX | Ch. 5 (BM25 math), Part X |
| Designer | Parts I, VIII | Ch. 15, 18, 23, 29 | Parts III, IV, X |

### Conventions

- Every code sample is the real `rag` and `gui` API as it runs in the workbench. Pseudocode is labelled as pseudocode.
- Every chapter ends with a **Lab** that names a preset in the editor's picker; loading it reproduces the chapter's examples.
- Trace boxes show real output from the running page, with each line explained.
- Steps that spend the reader's Claude usage are marked, the same way the workbench marks them.

&#91;embedded content: map of the book · ten parts, the pipeline core outlined\]

Read Parts II to IV in order. After them, Parts V to VIII can be read in any order; Part IX assumes VII and VIII, and Part X describes the limits all of them were built within.

## Part I · The problem and the shape of the answer

Search quality is found by iteration, so the pipeline has to be something you can see, measure and change while it runs. Part I sets up the problem and the page built around it.

- **1. Retrieval is ranking under a budget.** The eleven reports total 535 KB and a prompt holds 256 KB, so something must choose what Claude reads, and the choice is a ranking. *Lab: Minimal search.*
  - 1.1 The running example: eleven reports and one question
  - 1.2 Why not send everything: the prompt budget in bytes
  - 1.3 Three jobs: retrieve, pack, answer
  - 1.4 Three failures you can name: the section was missed, misranked, or misread
- **2. A workbench, not a script.** A pipeline that is data can be drawn, diffed, scored and edited live; a pipeline that is code can only be rerun. *Lab: Reports · hybrid + rerank + answer.*
  - 2.1 The edit, measure, keep loop
  - 2.2 What must be visible: the graph, the trace, the scores
  - 2.3 A tour of the page: editor, sheet, inspector, agent
  - 2.4 Presets as worked examples, and how to read one

## Part II · Text into structure

What can be found depends on how text was cut and counted before anyone asked a question. Part II builds the corpus side: documents, chunks and indexes.

- **3. Parsing markdown into documents.** Frontmatter is metadata, headings form a path, and fences, tables and callouts are kept whole, because each is used differently later.
  - 3.1 Frontmatter as fields, not text
  - 3.2 The heading stack and section paths
  - 3.3 Blocks: paragraph, list, fence, table, callout
  - 3.4 Links between documents: wiki links and related notes
  - 3.5 What the parser gets wrong, and why that is acceptable here
- **4. Chunking decides what can be found.** The chunk is the unit that gets ranked, cited and scored, so its size and boundaries set the ceiling on every later stage. *Labs: Chunk sizes, Table rows as chunks, Code search, Outline search, Which report?*
  - 4.1 Sections as the default unit
  - 4.2 Token budgets, atomic blocks and the short tail
  - 4.3 Overlap and heading prefixes
  - 4.4 Several chunk streams over one corpus: rows, code, cards
  - 4.5 Stable chunk ids, and what depends on them
- **5. BM25 from first principles.** A lexical score is three ideas multiplied together: rare terms matter more, repeated terms matter less each time, and long sections should not win by length. *Lab: Field weights.*
  - 5.1 Tokens, stop words and a small stemmer
  - 5.2 Term frequency and saturation (k1)
  - 5.3 Length normalization (b)
  - 5.4 Inverse document frequency
  - 5.5 Fields and weights: title, heading path, callout, text
  - 5.6 Scoring "rebalance law" by hand, then checking it against the engine
- **6. Metadata as an index.** Frontmatter terms answer different questions than body text: they filter, facet and boost rather than rank. *Labs: Faceted search, Query expansion.*
  - 6.1 A terms index over type, tags and aliases
  - 6.2 Facet counts from a result set
  - 6.3 Synonyms in the index versus rewriting the query

## Part III · A grammar for pipelines

A pipeline written as a chain of builder calls is a value, not a running program: it can be compiled, checked, drawn, varied and stored. Part III defines that grammar and the IR it compiles to.

- **7. Specs, not programs.** `rag.corpus` and `rag.plan` return immutable specs, so a variant is a new value and the original never changes under you. *Lab: Minimal search.*
  - 7.1 Corpus specs and plan specs
  - 7.2 Immutability, and why variants cost nothing
  - 7.3 The chain infers the wiring; the graph form states it
  - 7.4 Fragments and the shared `kit`
- **8. Components and typed ports.** Every step is a registered component with a phase and typed inputs and outputs, so wiring mistakes are caught before anything runs. *Labs: Your own components, Code search.*
  - 8.1 The registry: type, phase, ports, trigger
  - 8.2 Two typed streams: files → docs → chunks → index, and queries → hits → context → answer
  - 8.3 Method names generated from the registry
  - 8.4 `rag.define`: components that chain like built-ins
- **9. Lambdas that survive serialization.** A lambda is stored as source text and run without its closure, so a pipeline can be saved, reloaded and edited by an agent. *Labs: Rerank with a lambda, Table rows as chunks.*
  - 9.1 Functions as source text
  - 9.2 Closed lambdas: the guard and the free-identifier error
  - 9.3 Getting values in: signals, params as the last argument, `rag.bake`
  - 9.4 Four phases: build callbacks, build slots, bakes, live slots
  - 9.5 Which signals a lambda reads: static scan and runtime tracking
- **10. Compiling to an IR.** Compilation checks ports, ids and cycles, orders the nodes, derives the outputs and gives every build node a content digest. *Lab: Graph form.*
  - 10.1 The IR: nodes, edges, signals, outputs
  - 10.2 What the checker rejects, with the real error messages
  - 10.3 Topological order and derived outputs
  - 10.4 Digests: naming a node by what it would compute
  - 10.5 Reading the DAG tab

## Part IV · Running it

The engine runs an IR in two phases: a build that turns files into indexes once, and query runs that happen on every keystroke. Both avoid repeated work by recording what each node's output depended on.

- **11. Builds that remember.** A build node is named by a digest of its type, params, input digests and file contents, so an edit rebuilds only the nodes downstream of it. *Labs: Chunk sizes, Doc cards.*
  - 11.1 The build phase and the live phase
  - 11.2 The digest chain: type, params, inputs, file contents
  - 11.3 The build memo and the dirty set
  - 11.4 Map components and the per-item cache, in memory and in the database
  - 11.5 Builds that cost Claude calls: budgets, deferral and `pending()`
- **12. Queries at runtime.** A session reruns only the live nodes whose params, inputs or signal reads changed, and each node's trigger decides whether it runs while the user types or waits for Enter. *Labs: Answer first, Ranking playground.*
  - 12.1 Signals: the named inputs of a query run
  - 12.2 Triggers: `change` nodes run on every keystroke, `submit` nodes wait for Enter
  - 12.3 The session memo: input identity plus recorded signal reads
  - 12.4 Four node statuses: ran, cached, skipped, error
  - 12.5 Cancellation, and what a keystroke costs
- **13. Traces and explanations.** A run returns one trace entry per node and a score breakdown per hit, so a miss can be attributed to the stage that caused it. *Lab: Compare stages.*
  - 13.1 The trace: status, counts in and out, time, calls
  - 13.2 `why` and `parts`: where a hit's score came from
  - 13.3 `rag.explain`: following one chunk through every node
  - 13.4 Missed, misranked, misread: diagnosing chapter 1's three failures from a trace

## Part V · Ranking and answers

The live half of the pipeline turns a question into ranked hits, packs the best of them into a context, and asks Claude for a cited answer. Part V takes those steps in order, then gives Claude a larger role in each.

- **14. Retrieval and fusion.** Scores from different indexes are on different scales, so they are combined by rank, not by adding scores. *Labs: Fusion, Reports · hybrid + rerank + answer.*
  - 14.1 The retrieve node: one index, several queries
  - 14.2 Why two BM25 scores cannot be added
  - 14.3 Reciprocal rank fusion: 1/(k + rank), weights, and what k = 60 does
  - 14.4 Max fusion, and when it is the better rule
  - 14.5 Filters and limits: `filter.where`, filter lambdas and `s.k`
- **15. Context and answers.** The answer can cite only what was packed, so packing is a ranking decision under a byte budget. *Labs: Context expansion, Two answers.*
  - 15.1 Expanding hits: neighbouring sections and linked reports, with score decay
  - 15.2 Packing under a budget: rank order, per-document caps, the 40,000-character default
  - 15.3 Evidence labels and the answer prompt
  - 15.4 Citations as data, and an answer that says what is missing
  - 15.5 Streaming the answer and showing its sources
- **16. Rewriting and reranking.** A ranking can change without touching an index: before retrieval by changing the queries, and after it by reordering the hits. *Labs: Query expansion, Rerank with a lambda, Your own components, Rewrite + rerank, HyDE.*
  - 16.1 Rewrite lambdas: acronyms and synonyms
  - 16.2 `rewrite.llm`: keyword queries in the corpus's own vocabulary
  - 16.3 HyDE: searching with a drafted answer
  - 16.4 Rerank lambdas: heuristics over fields the index never scored
  - 16.5 `rerank.llm`: passages, scores and stated reasons
  - 16.6 Why steps that call Claude wait for Enter
- **17. Claude as the retriever.** An agentic node gives Claude search and read tools and lets it choose the queries, trading predictability and calls for recall. *Labs: Agentic answer, Doc cards.*
  - 17.1 Four tools: search, read, outline, documents
  - 17.2 A read budget, and evidence labels assigned on read
  - 17.3 Rounds in the trace: what was searched and what was read
  - 17.4 Routing by build-time enrichment: summaries and questions per document
  - 17.5 Fixed pipeline or agent: comparing both on the same goldens

## Part VI · Measuring

A change to a pipeline is an improvement only if it moves a number on questions chosen before the change was made. Part VI defines those questions, the numbers, and how pipelines are compared with them.

- **18. Golden questions.** A golden case is a question plus patterns for the document and section that should answer it, so relevance is checked by a rule rather than by a stored chunk id. *Labs: Reports · hybrid + rerank + answer, Synthetic evals.*
  - 18.1 A case: question, expected document, expected section, expected answer text
  - 18.2 Patterns instead of ids: why cases survive rechunking
  - 18.3 Writing cases from the questions users actually ask
  - 18.4 Which run a case exercises: `change` by default, `submit` when the answer is scored
  - 18.5 Synthetic cases: questions Claude writes for sampled sections
- **19. Ranking metrics.** hit@k, MRR, nDCG and recall each reward a different shape of result list, and choosing one is choosing how far down the list users are assumed to read. *Lab: Field weights.*
  - 19.1 hit@k: whether a relevant section was on the first screen
  - 19.2 MRR: how far down the first relevant section was
  - 19.3 nDCG@k: credit discounted by position and normalised by the best possible list
  - 19.4 recall@k: how many of the relevant sections were found
  - 19.5 All four by hand for one case
  - 19.6 Twelve cases, and how large a difference has to be before it means anything
- **20. Variants, sweeps and judges.** Pipelines are compared on the same cases with the same metrics, parameter grids are scored automatically, and Claude judges what string patterns cannot. *Labs: Chunk sizes, Parameter sweep, Judged evals.*
  - 20.1 Variants as patched plans; `compare` and its deltas
  - 20.2 A sweep is a grid of `set` ops, and each configuration rebuilds only what it changed
  - 20.3 Reading a sweep: the best configuration, ties, and overfitting to twelve questions
  - 20.4 `answerHas`: the cheapest check on an answer
  - 20.5 Judges: groundedness and length as lambdas that call Claude
  - 20.6 What an eval run costs, and when to spend it

## Part VII · Changing it safely

A pipeline that a person and an agent both edit while it serves queries needs edits that are small, checked and reversible. Part VII makes every change an operation on the IR and gives the workspace that applies them a history.

- **21. Edits as operations.** Ten operations cover every change to an IR, and each op list is applied by recompiling, so an edit that would break the graph is rejected before it takes effect. *Lab: the `ops` list at the end of every preset.*
  - 21.1 The ten ops: set, unset, insert, remove, replace, wire, signal, trigger, output, rename
  - 21.2 Paths into params, and setting a lambda from source text
  - 21.3 `plan.patch(ops)`: the same ops at authoring time
  - 21.4 Dry runs: which nodes go live, which rebuild, and how many Claude calls the rebuild would cost
  - 21.5 Why operations and not a text diff of the script
- **22. Workspaces and revisions.** A workspace holds the current IR and a revision number, and an op list planned against an older revision is refused rather than silently merged. *Lab: Reports · hybrid + rerank + answer, with the agent.*
  - 22.1 A workspace: IR, engine, revision, listeners
  - 22.2 `expect`: optimistic concurrency between the agent and the editor
  - 22.3 The receipt: live changes, nodes to rebuild, removals
  - 22.4 Read-only workspaces, and errors written for the agent that will read them
- **23. History: undo, branches and snapshots.** Every applied op list is stored with the IR it replaced, so undo restores a previous pipeline exactly and a branch lets a risky change be scored before it lands. *Lab: the History tab.*
  - 23.1 Undo and redo restore stored IRs; revisions only count up
  - 23.2 Branches, and why a merge replays ops instead of merging graphs
  - 23.3 Snapshots: what the agent saves before each step
  - 23.4 Keeping changes in the script, and exporting a standalone page

## Part VIII · Interfaces

The page a search runs in is described the same way as the pipeline: a tree built once from a grammar, with props that are values or closed lambdas. Part VIII builds that grammar, the runtime that keeps it current, and the decisions a search page is made of.

- **24. A grammar for pages.** An app is state, derived values and a view tree built once; each prop is a value, a closed lambda `(s, ctx) => …`, or a bake evaluated at build time. *Labs: Corpus dashboard, Result map.*
  - 24.1 `gui.app`: title, `use(plan)`, state, derive, watch, view
  - 24.2 Layout: row, col, grid, card, tabs, when
  - 24.3 Content: text, note, stat, table, list, chart, code
  - 24.4 Controls bound to state keys: slider, number, select, toggle, input, button
  - 24.5 Props as lambdas, and why the tree is built only once
  - 24.6 `canvas`: drawing with `(s, ctx, g)`
- **25. A reactive runtime.** The runtime records which state keys each component read while rendering, and when a key changes it re-renders only those components. *Lab: Ranking playground.*
  - 25.1 Read tracking through a proxy
  - 25.2 From a state change to a redraw: set, derive, refresh
  - 25.3 Plan signals are state keys: the bridge between the page and the session
  - 25.4 Scheduling query runs by trigger
  - 25.5 Errors that stay inside the component that raised them
- **26. Search components.** The search components are ordinary components that read the plan's signals and `s.out[nodeId]`, so any node's output can be shown anywhere on the page. *Labs: Compare stages, Answer first, Document browser.*
  - 26.1 `searchBox`: a query signal, a submit action, and a box that keeps focus while results redraw
  - 26.2 `results` and `hits`: showing any hits node, not only the last one
  - 26.3 `facets`: counts from the result set, written back as a filter signal
  - 26.4 `answer` and `chunk`: streamed text, citation links, the section viewer
  - 26.5 `trace`, `dag` and `status`: the pipeline as part of the page
- **27. Designing search experiences.** A search page is a set of decisions about time, trust and cost: what each keystroke does, what a result shows, and when the reader is told that an action spends Claude usage. *Labs: Faceted search, Answer first, Document browser, Result map.*
  - 27.1 Type versus Enter: instant ranking, deliberate answers
  - 27.2 What a result shows: title, heading path, snippet, score, reason
  - 27.3 Answer first or results first
  - 27.4 Citations the reader can check in one click
  - 27.5 Empty, loading, partial and failed states
  - 27.6 Marking the actions that cost Claude calls
  - 27.7 Phone width: one column, no horizontal scroll

## Part IX · The agent

The agent is Claude with one tool that runs JavaScript in the page, so it works through the same `rag`, `gui` and workspace objects as a programmer and is held to the same checks. Part IX explains that design and the controls around it.

- **28. One tool: eval.** Instead of one tool per action, the agent gets a single tool that runs code against the page's API, so its abilities grow with the API and its mistakes arrive as ordinary errors. *Lab: the suggestion chips in the agent panel.*
  - 28.1 Why one eval tool rather than many narrow tools
  - 28.2 The scope: `rag`, `gui`, `wb`, `ws`, `ui`, `chat`, `ref`, `log`, `console`
  - 28.3 Results as short YAML, truncated near 9,000 characters
  - 28.4 Errors with line numbers, and how the agent recovers from them
  - 28.5 The system prompt: an API reference plus the current state of the page
- **29. Run modes and reversibility.** Auto, Ask and Read-only decide whether a step runs, waits for approval or is refused when it mutates, and every step that runs is snapshotted so the person can revert it. *Lab: the mode switch in the agent panel.*
  - 29.1 Auto: run, snapshot, report
  - 29.2 Ask: approve one step, or the rest of the turn
  - 29.3 Read-only: mutating calls throw, and branches stay open for experiments
  - 29.4 Reverting to before any step
  - 29.5 Editing the live pipeline with ops versus editing the script
- **30. Answers the agent can draw.** The agent puts tables, charts, hit lists, eval reports, DAGs and whole gui apps into the conversation, and links into the workbench from its prose. *Lab: ask the agent for a search page.*
  - 30.1 `chat` outputs: md, table, chart, hits, evals, dag, ui, download
  - 30.2 `ref` links: pointing at a query, a chunk, a node or a tab
  - 30.3 `ui`: running a query, opening a tab or a section for the person
  - 30.4 Conversations saved per user, and what is clipped when they are stored
  - 30.5 A worked session: diagnose a miss, branch, fix, score, merge

## Part X · The platform and the page

The workbench runs as a hosted page whose only services are a Claude sampler, a small per-user database and file downloads. Part X describes those limits, which shaped Parts II to IX, and the page built inside them.

- **31. The artifact runtime.** Every design choice in this book fits inside four services and a 256 KiB prompt, and this chapter says which choice each limit forced.
  - 31.1 `claude.use`: sample, db, user, downloads
  - 31.2 Sampling: the prompt cap, tools, `.json`, and `limits()`
  - 31.3 Why there are no embeddings, and what lexical retrieval plus Claude covers instead
  - 31.4 Persistence: one document per user, with transcripts, files and cache collections
  - 31.5 Uploading reports: what a new file does to digests, caches and evals
  - 31.6 The same library in node: testing without a page
- **32. The page itself.** The Lattice design system gives the workbench one accent colour and two line styles, and both carry meaning: dashed is build time and dotted is runtime.
  - 32.1 Ink on paper, with one signal blue
  - 32.2 Dashed for build, dotted for runtime, in the DAG, the editor and the agent panel
  - 32.3 Layout: editor, search sheet, inspector, agent
  - 32.4 The preset picker: stashed edits and restoring a session
  - 32.5 Checking the page: node tests and a headless run of every preset

### Appendices

- **A. API reference.** Every `rag` and `gui` builder method and component type, with its phase, ports, default trigger and options.
- **B. Preset catalogue.** The 29 presets by group, each with the chapters it illustrates.
- **C. Formats.** The IR, the ten ops, trace entries and eval reports, as JSON with field-by-field notes.
- **D. Glossary.** Chunk, digest, signal, trigger, bake, golden case, evidence label and the other terms as this book defines them.
