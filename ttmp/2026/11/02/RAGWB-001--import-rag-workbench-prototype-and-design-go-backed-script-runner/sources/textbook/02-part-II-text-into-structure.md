# Part II · Text into structure

What can be found depends on how text was cut and counted before anyone asked a question. This part builds the corpus side of the pipeline: documents, chunks and indexes.

## 3. Parsing markdown into documents

This chapter explains how a markdown file becomes a *document*: a JavaScript object with an id, a title, a metadata record, an ordered list of sections, and a list of links to other documents. Every later stage reads these fields and never the raw file, so any distinction the parser does not make is a distinction no later stage can use. By the end of the chapter you should be able to predict, for any line in a report, which field of which section it ends up in.

The corpus side of every pipeline in this book has the same four stages. Files are parsed into documents, documents are cut into chunks, and chunks are indexed. Part II takes them in that order, one chapter each, and the picture below gives the counts each stage produces on the eleven bundled reports.

&#91;embedded content: the corpus side of the pipeline · files, documents, chunks, indexes\]

In the grammar, parsing is one call in a corpus chain:

```js
const corpus = rag.corpus('reports')
  .from(rag.files('*.md'))      // source.files: every uploaded file whose name matches
  .parse(rag.markdown())        // parse.markdown: one document per file
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  .index('text', rag.bm25());
```

The parser is also exported on its own, which is how the outputs in this chapter were produced:

```js
const doc = rag.util.parseMarkdown({ name: file.name, text: file.text });
```

For the CoinVault report, the document looks like this, with long arrays abridged:

```js
{
  id:    'project-report-coinvault-mcp-from-local-conformance-to-chatgpt-2',
  title: 'PROJECT REPORT - CoinVault MCP - From Local Conformance to ChatGPT 2026-07-28',
  h1:    'CoinVault MCP: From Local Conformance to ChatGPT 2026-07-28',
  meta:  { type: 'article', status: 'active', tags: [ 'article', 'project-report', 'coinvault', … ],
           aliases: [ 'CoinVault ChatGPT MCP acceptance', … ], tickets: [ … ], … },
  sections: [ /* 33 sections */ ],
  links: [ 'PROJECT REPORT - CoinVault Remote MCP - Official Go SDK and Employee OAuth Authorization' ]
}
```

Two details are worth noticing before the sections that follow. The `id` is a slug of the *file name*, cut to 64 characters, which is why it ends in `-2`: the cut falls in the middle of `2026`. Renaming the title in the frontmatter leaves the id alone; renaming the file changes it, and with it every chunk id derived from it (section 4.5). The `title` comes from the frontmatter `title` field when there is one, then from the first `#` heading, then from the file name. Here the frontmatter wins, so the friendlier H1 is kept separately as `h1`.

### 3.1 Frontmatter as fields, not text

A report begins with a block of YAML between two `---` lines. The CoinVault report's begins like this:

```yaml
---
title: "PROJECT REPORT - CoinVault MCP - From Local Conformance to ChatGPT 2026-07-28"
aliases:
  - CoinVault ChatGPT MCP acceptance
  - CoinVault hosted OAuth debugging
  - CoinVault MCP 2026-07-28
status: active
type: article
created: 2026-09-03
repo: /home/manuel/workspaces/2026-08-28/coinvault-oidc-mcp/coinvault
tickets:
  - CV-MCP-ACCEPT-001
  - CV-OAUTH-OBS-001
tags:
  - article
  - project-report
  - coinvault
  - mcp
  - oauth
  …
---
```

The parser reads this block into `doc.meta` and removes it from the body. It does not leave it in the text, and the reason is a matter of counting. If `type: article` and the eleven tags stayed in the body, the words *project*, *report* and *article* would appear in the first chunk of nearly every report, and a lexical score would treat them as evidence. Worse, the evidence would be attached only to the first chunk of each document, because only that chunk contains the frontmatter. As fields, the same information can do what it is good at: filter a result list, count facets and match a question against a closed vocabulary (chapter 6).

The parser understands a small subset of YAML, chosen to cover what notes tools write:

| Form in the file | Parsed value |
| --- | --- |
| `status: active` | the string `'active'` |
| `title: "PROJECT REPORT - …"` | the string without its quotes |
| `tags: [a, b]` | the array `['a', 'b']` |
| a key with no value, followed by `   - item ` lines | an array of the items |
| `publish: false`, `count: 12` | the boolean `false`, the number `12` |
| an indented `key: value` under another key | nothing: the line is dropped (section 3.5) |

Every chunk carries a copy of its document's metadata, so that a filter can test a chunk without looking up its document. The copy is not complete. `pickMeta` drops the keys `title`, `created`, `updated`, `source_ticket_path`, `source_design_doc`, `related_vault_notes` and `derived-from`, and any string longer than 120 characters, because those values identify a file rather than describe its content and would only add noise to facet lists. The first chunk of the order-grain TTC report carries this:

```json
{ "aliases": ["TTC Customer Clustering", "thetreecenter.com segmentation"],
  "tags": ["project", "r", "shiny", "clustering", "marketing"],
  "status": "active", "type": "project",
  "repo": "/home/manuel/code/gec/2026-09-03--marketing-analysis", "publish": false }
```

### 3.2 The heading stack and section paths

A section is the run of blocks between one heading and the next. What makes it useful is its *path*: the titles of every heading it sits under, from the outermost in. The parser computes paths with a stack of open headings. When it reads a heading of level *L*, it pops every open heading whose level is *L* or deeper, pushes the new one, and records the stack's titles as the new section's path.

```text
Pseudocode: the heading stack

stack = []
for each heading (level L, title T) in the file, in order:
    if this is the first level-1 heading: remember T as h1; continue
    while stack is not empty and top(stack).level >= L: pop(stack)
    push(stack, {level: L, title: T})
    start a new section with path = titles of stack
```

Text before the first heading becomes a section with level 0 and an empty path, which the chunker later names `intro`. The first H1 is not a section at all: it is the document's heading, and treating it as a section would put the document title at the front of every path. Heading titles are passed through `plain()`, which strips backticks, bold markers and link syntax, so a heading written with inline code, such as ``## 4.1 The `typed-nil` deployment failure`` gets the path element `4.1 The typed-nil deployment failure`.

These are the first ten sections of the CoinVault report, with the kinds of blocks each one holds:

```text
0  []                                                          paragraph,paragraph,paragraph,callout
2  ["1. The acceptance problem"]                               paragraph,list,paragraph
3  ["1. The acceptance problem","1.1 Exact deployed identity"]  paragraph,table,paragraph
2  ["2. Architecture at acceptance"]                           paragraph,fence,paragraph
3  ["2. Architecture at acceptance","2.1 Repository responsibilities"]  table,paragraph
2  ["3. Local proof before hosted debugging"]                  paragraph,paragraph,fence,paragraph,…
2  ["4. Deploying a stable public MCP service"]                paragraph,paragraph,fence,paragraph
3  ["4. Deploying a stable public MCP service","4.1 The typed-nil deployment failure"]  paragraph,fence,…
2  ["5. Making safe failures diagnosable"]                     paragraph,fence,paragraph,paragraph,fence,paragraph
2  ["6. OAuth discovery and registration incompatibilities"]   (no blocks)
```

The last line shows a rule that is easy to miss. Section 6 has a heading and no text of its own: its content lives entirely in 6.1 and 6.2. The parser keeps it anyway, because a section with a heading is still a node in the outline and its title is still part of its children's paths. Only a level-0 section with no text is dropped. Across the corpus the parser produces 348 sections: 11 intros, 173 at level 2 and 164 at level 3. No report uses level 4 or deeper.

The path matters later in three ways. It is a field the BM25 index weights three times as heavily as body text, because a heading is the author's own summary of what follows (chapter 5). It is the second half of every result's label, `title › path`, which is how a reader decides whether to open a hit. And it is what golden cases match against when they check whether the right section was found (chapter 18), which is why evals survive changes to chunking.

### 3.3 Blocks: paragraph, list, fence, table, callout

Inside a section, the parser divides lines into blocks. It recognises a block by its first line and then consumes lines until the block ends:

| Kind | Starts with | Ends at | In the corpus | Used later for |
| --- | --- | --- | --- | --- |
| fence | ```` ``` ```` or `~~~`, optional language | the same marker | 195 | atomic during chunking; code chunks; `hasCode` |
| table | a line starting with `\|` | the first line not starting with `\|` | 51 | atomic during chunking; one chunk per row |
| callout | `> [!variant]` | the last `>` line | 10 | its own BM25 field, `callout` |
| quote | `>` without `[!…]` | the last `>` line | 1 | ordinary text |
| list | `-`, `*`, `+` or `1.` | a line that is neither an item nor indented | 106 | split by item, never mid-item |
| paragraph | anything else | a blank line or another block | 805 | split by sentence |

Fences are checked first, so a `#` line inside a code block is never mistaken for a heading. Tables keep both their raw text and a parsed form, a header row and data rows, with each cell passed through `plain()`. The last table in the CoinVault report parses to:

```js
{ kind: 'table',
  header: [ 'Visible symptom', 'Actual cause', 'Corrective change' ],
  rows: [ [ 'Browser ECS never healthy', 'Typed nil verifier activated RAG branch', 'Return a genuinely nil verifier interface' ],
          [ 'DCR returned 503', 'Legacy SQLite lacked last_used_at, then payload', 'Add migration support; reset explicitly disposable first-dev state' ],
          … ] }
```

Keeping the parsed form is what lets the *Table rows as chunks* preset turn every row into a chunk that reads `Visible symptom: DCR returned 503` and so on, without parsing the table a second time.

Fences keep their language tag. In this corpus that is `ts` 80 times, `text` 43, `js` 27, `mermaid` 19, and a handful of `go`, `json`, `r`, `bash` and others. The language becomes a facet in the *Code search* preset, and `mermaid` fences are dropped by the default chunker, because a diagram's source is mostly arrows and node ids that match nothing a person would type.

Callouts are Obsidian's `> [!summary]` blocks. All ten in this corpus are summaries at the top of a report, and each is a list of bolded findings. The CoinVault one begins: *“Acceptance was staged by boundary. Loopback transport, deterministic OIDC, real domain tools, …”*. The chunker copies callout text into a separate `callout` field of the chunk, so an index can weight an author's summary above ordinary text (section 5.5).

### 3.4 Links between documents: wiki links and related notes

Reports refer to each other with wiki links, `[[Title of another note]]`. The parser collects every link target in the file, including those in the frontmatter's `related_vault_notes`, strips any `#heading` or `|label` part, and stores the distinct targets in `doc.links`. It does not resolve them: a link is only a string at this point.

Resolution happens once all documents exist, when the engine assembles a build's artifacts. A target matches a document if, after lowercasing, removing a `PROJECT REPORT -` or `PROJ -` prefix and collapsing punctuation, it equals that document's title, file name or one of its aliases. Targets that match nothing are dropped. The Datalab report links to six notes; two of them are in this corpus, so it resolves to two neighbours:

```text
datalab         → workbench-core, workbench-stabilization
identity        → workbench-core, workbench-stabilization
workbench-core  → workbench-stabilization
stabilization   → workbench-core, datalab
(the other seven reports resolve to no links)
```

The CoinVault report's one link names a different CoinVault report that was never uploaded, so it resolves to nothing. That is the normal case for a set of files exported from a larger vault, and the code treats it as normal. The resolved graph is what `expand.links` follows when it adds sections from linked reports to an answer's context (chapter 15).

### 3.5 What the parser gets wrong, and why that is acceptable here

The parser is about 90 lines of line-by-line pattern matching, not a CommonMark implementation. This test file shows its main gaps:

```markdown
---
title: Edge cases
owner:
  name: Ada
  team: search
tags: [a, b]
---
# Edge cases

Setext heading
--------------

- item one
lazy continuation line

> [!note] Heads up
> second line
```

It parses to a document whose `meta` is `{ title: 'Edge cases', tags: ['a', 'b'] }` and whose only section holds these blocks:

```text
paragraph  "Setext heading\n--------------"
list       "- item one"
paragraph  "lazy continuation line"
callout    note: "Heads up\nsecond line"
```

Three things went wrong. The nested `owner` mapping vanished without an error, because indented `key: value` lines are not part of the supported subset. The underlined *setext* heading became a paragraph, so the section structure is flatter than the author intended. And the unindented line after the list item, which CommonMark treats as a continuation of the item, became a separate paragraph. The callout, by contrast, came through correctly.

These gaps are acceptable for this corpus because none of them occurs in it. A scan of the eleven reports finds no setext headings, no nested frontmatter keys and exactly one H1 per file; the reports come from one notes tool that writes one dialect. The decision rests on a principle that applies to every stage of Part II: a parser error matters only when it changes what can be retrieved. A dropped `owner.team` would matter if someone built a facet on it, and a setext heading would matter because its section would vanish from every path. If you upload files from another source, open the Corpus tab and compare a few documents' sections with the files before trusting any score.

The key points to take from this chapter:

- A document is the parser's whole output, and later stages read only its fields: `id`, `title`, `meta`, `sections` and `links`.
- Frontmatter becomes fields so that it can filter and facet without inflating word counts in the first chunk of each report.
- A section's path is computed with a stack of open headings, and the first H1 is the document's heading rather than a section.
- Fences, tables and callouts are recognised as kinds because each is used differently later: kept whole, split into rows, or weighted as a summary.
- Links are stored as strings at parse time and resolved against titles, file names and aliases only when all documents exist.
- The parser handles one dialect of markdown, and its gaps are acceptable only because the corpus was checked for them.

The next chapter cuts these sections into chunks, the unit that every later stage ranks, cites and scores.

**Lab: Minimal search.** Load the preset and open the Corpus tab in the inspector. Find the CoinVault report and compare its section list with the ten lines in section 3.2. Then search for `typed nil` and check that the result's label shows the path `4. Deploying a stable public MCP service › 4.1 The typed-nil deployment failure`.

## 4. Chunking decides what can be found

This chapter explains how documents are cut into *chunks*, and why that cut sets an upper bound on everything that follows. A chunk is the unit the index scores, the unit a result list shows, the unit the answer cites and the unit an eval checks. No later stage can return half a chunk or join two, so a fact split across two chunks has to be found twice, and a fact buried in a large chunk has to compete with everything else in it. By the end of the chapter you should be able to read a chunker's parameters and predict roughly how many chunks it produces, how large they are and what their ids will be.

A chunk is a plain object. The default chunker produces chunks with these fields:

| Field | Example | Used by |
| --- | --- | --- |
| `id` | `project-report-coinvault-…-chatgpt-2#4-1-the-typed-nil-deployment-failure` | fusion, neighbours, the UI, citations |
| `docId`, `title` | the parent document's id and title | labels, filters, link expansion |
| `headingPath` | `['4. Deploying a stable public MCP service', '4.1 The typed-nil deployment failure']` | BM25 field, labels, evals |
| `text` | the section's blocks joined back into markdown | BM25 field, answers |
| `callout` | text of any `[!summary]` callouts | BM25 field |
| `meta` | the document's metadata, minus file-only keys | filters, facets, the terms index |
| `tokens` | `240` | packing budgets, statistics |
| `part`, `parts` | `1`, `1` | splitting, ids |
| `hasCode`, `hasTable`, `kind` | `true`, `false`, `'section'` | filters, rerank heuristics |

### 4.1 Sections as the default unit

The default chunker, `rag.sections()`, starts from the sections the parser found. The reason is that a section is a unit the author chose. A report's author put a heading where the topic changed, so a section usually answers one question, and its heading path is a ready-made label for it. A fixed window of 450 tokens would cut wherever the count ran out, often in the middle of the explanation a reader needed.

The `depth` parameter decides which headings start a new chunk. A section at level `depth` or shallower starts a new group; a deeper section is appended to the group before it, with its heading kept as a line of text. With the default `depth: 3`, every H2 and H3 in these reports starts a chunk. With `depth: 2`, each H3 folds into its H2. For the CoinVault report that turns section 4 and its subsection 4.1 into one group of 467 tokens, which is then split into two parts:

```text
depth 3:  …-chatgpt-2#4-deploying-a-stable-public-mcp-service          218 tokens
          …-chatgpt-2#4-1-the-typed-nil-deployment-failure            240 tokens
depth 2:  …-chatgpt-2#4-deploying-a-stable-public-mcp-service          366 tokens  part 1/2
          …-chatgpt-2#4-deploying-a-stable-public-mcp-service-2        101 tokens  part 2/2
```

The folded chunk still contains the words *typed-nil*, so a lexical search still finds it. What it has lost is the heading path `… › 4.1 The typed-nil deployment failure`, and that loss is larger than it looks. The heading path is a weighted field in the index (chapter 5), it is the label a reader scans, and it is what the golden cases test. On the twelve goldens, `depth: 2` drops hit@5 from 0.917 to 0.667. All four cases that now miss expect a section that is an H3, such as *What does the rebalance law say?*, which expects `5.9 The rebalance law`: the right text is retrieved, but no chunk is labelled with that section any more. A chunker can therefore make a pipeline look worse to its evals without making it worse at retrieval, and the cure is to read misses, not only scores (chapter 13).

### 4.2 Token budgets, atomic blocks and the short tail

A section can be longer than anyone wants to read or rank as one unit, so the chunker splits groups that exceed `maxTokens`. Token counts here are an estimate, `Math.ceil(characters / 4)`, not the output of a model's tokenizer. The estimate is cheap and stable, and the budget only needs to be roughly right: 450 estimated tokens is about 1,800 characters, or a long screen of prose.

The split works on blocks, not characters:

```text
Pseudocode: splitting one group

units = []
for each block in the group:
    if tokens(block) <= max:                         keep it whole
    else if block is a fence or table
         and tokens(block) <= 3 * max:               keep it whole (atomic)
    else if block is a fence, table or list:         split it into runs of lines
    else:                                            split it into runs of sentences
parts = pack units greedily, starting a new part when the next unit would pass max
if there are 2+ parts and the last has fewer than minTokens (30):
    merge the last part into the one before it
```

Each rule protects something. Code and tables are allowed to exceed the budget up to three times, because half a code sample or a table without its header row is wrong rather than merely short. Lists split between items and paragraphs between sentences, so no part ends mid-sentence. The short-tail rule prevents a final fragment of a dozen words from becoming a chunk of its own, where it would rank badly and cite worse.

The budget has a large effect on the number and size of chunks. These are the real counts for the eleven reports, with `depth: 3`:

| `maxTokens` | Chunks | Median tokens | 90th percentile | Largest | Over budget |
| --- | --- | --- | --- | --- | --- |
| 200 | 873 | 144 | 197 | 568 | 66 |
| 450 | 462 | 278 | 423 | 743 | 8 |
| 900 | 342 | 322 | 699 | 901 | 1 |

The total is about 125,000 estimated tokens in every row; only the cut points move. Doubling the budget from 450 to 900 removes only a quarter of the chunks, because most sections are already shorter than 450 tokens: the budget only affects the sections that exceed it. The *over budget* column counts chunks kept whole by the atomic rule. The largest at 450 is a 743-token table in the Workbench Core report's section *2. The problem*.

At 200 tokens, the typed-nil section from section 4.1 above becomes two chunks of 139 and 101 tokens, the second with the id suffix `-2`. A question about the restart loop now has two candidate chunks, each holding part of the explanation. Whether that is better depends on the question. On the goldens, run with the *Chunk sizes* preset's neighbour expansion, 200 tokens moves one case from rank 6 to rank 2 and two others from rank 1 to 3, while 900 tokens gives the best MRR of the three:

| Variant | hit@5 | MRR | nDCG@10 |
| --- | --- | --- | --- |
| 450 (base) | 0.917 | 0.931 | 0.867 |
| 200-tok | 1.000 | 0.903 | 0.746 |
| 900-tok | 1.000 | 0.958 | 0.903 |
| h2-only | 0.667 | 0.563 | 0.509 |
| overlap-40 | 1.000 | 1.000 | 0.871 |

Twelve questions are too few to choose a budget from these numbers alone; one case moving five places changes MRR by about 0.07. Chapter 19 makes that argument precise. What the table does show is that chunk size is a parameter to measure, not a constant to look up.

### 4.3 Overlap and heading prefixes

Two options change what goes *into* a chunk rather than where it is cut. `overlap: n` prefixes each part after the first with the last *n* estimated tokens of the part before it, about 4*n* characters. Its purpose is to keep a sentence that introduces an idea next to the sentence that completes it when a split falls between them. The cost is visible in the text. This is the start of the second part of a TTC section with `overlap: 40`:

```text
es: when flags are produced by a join that can fail, classify by the failure mode you can
detect, and never let an empty flag mean "none of the above" silently.
Plant categories come from keyword matching on genus names (maple, spruce, …
```

The overlap is measured in characters, so it begins in the middle of a word. Overlap also duplicates text: the same sentences now appear in two chunks, and a query matching them scores both. With `overlap: 40` the corpus grows from 124,977 to 130,491 estimated tokens and 24 chunks exceed the budget instead of 8. On the goldens it happens to produce the only perfect MRR in the table above, which is a reason to keep it as a variant and test it on more questions, not a reason to make it the default.

`prefixHeading: true` writes the heading path as the first line of the chunk's text:

```text
4. Deploying a stable public MCP service › 4.1 The typed-nil deployment failure
The first shared-image deployment left MCP healthy but put the browser service into a restart loop. …
```

This matters for consumers that only see `text`: an answer prompt, a reranker or a rewrite step that reads the chunk as plain prose. The BM25 index does not need it, because it already indexes `headingPath` as a separate, weighted field, and prefixing would count those heading words a second time at text weight. The default leaves it off; the Chunk sizes preset suggests it as a variant to measure.

### 4.4 Several chunk streams over one corpus: rows, code, cards

A corpus is not limited to one chunker. Each `.chunk()` call adds a chunk stream, each `.index()` call can name the stream it indexes with `{ from }`, and a plan can retrieve from several indexes and fuse the results (chapter 14). This is how one set of documents serves questions of different shapes. The *Table rows as chunks* preset adds a stream with one chunk per table row:

```js
const corpus = kit.reports()
  .chunk('rows', (doc, ctx, p) => doc.sections.flatMap(sec => sec.blocks
    .filter(b => b.kind === 'table' && b.rows.length >= p.minRows)
    .flatMap(t => t.rows.map(row => ({
      kind: 'row',
      headingPath: [...sec.path, row[0]],
      text: t.header.map((h, j) => `${h}: ${row[j] ?? ''}`).join('\n'),
    })))), { minRows: 2 })
  .index('rows', rag.bm25({ fields: { headingPath: 2, text: 1 } }), { from: 'rows' });
```

The lambda receives a parsed document and returns plain objects; the chunker fills in `docId`, `title`, `meta`, `tokens` and the id. For the question *which HTTP status does ErrConflict map to*, the top hit is now a chunk that contains exactly the answer:

```js
{ id: 'project-report-agentforum-web-a-protobuf-contract-an-http-adapte#row-errconflict',
  kind: 'row',
  headingPath: [ 'The HTTP server', 'Adapter discipline', 'ErrConflict' ],
  text: 'Service sentinel: ErrConflict\nHTTP: 409\nError.code: conflict' }
```

In the section stream the same fact is one row in a 300-token section about adapter discipline, competing with every other row. The other chunking presets apply the same idea to other shapes:

| Preset | Stream | Chunks | Built from |
| --- | --- | --- | --- |
| Table rows as chunks | `rows` | 350 | one per row of tables with 2+ rows |
| Code search | `code` | 145 | one per fence of 3+ lines, except mermaid, with `meta.lang` |
| Outline search | `outline` | 337 | one per heading: its path and its first sentence |
| Which report? | `docs` | 11 | one card per document: title, tags, summary callout, H2 titles |

Each stream answers a different kind of question. Rows answer lookups of a single value. Code chunks answer questions phrased in identifiers. Outline chunks answer *where is X discussed?* and are small enough to scan as a list. Document cards answer *which report covers X?*, where returning a section would be too specific. Building them costs little: the four streams together took under 100 ms to build in node.

### 4.5 Stable chunk ids, and what depends on them

A chunk id is built from the document id and the slug of the last heading in the chunk's path: `docId#slug`. A section before the first heading uses `intro`. A chunk from a stream whose kind is not `section` puts the kind in front, as in `#row-errconflict` or `#heading-10-3-the-migration-note`. When two chunks would get the same id, which happens to the parts of a split section and to sections with identical headings, the second gets `-2`, the third `-3`, in reading order. The CoinVault intro is split into two parts, `#intro` and `#intro-2`.

The point of this scheme is that an id is predictable from the document and its outline, and does not change when unrelated text changes. Editing a paragraph in section 7 leaves every id in the document as it was. Changing the chunker's parameters is different: at 200 tokens the typed-nil section gains a `-2` chunk that does not exist at 450, and at `depth: 2` the id `#4-1-the-typed-nil-deployment-failure` disappears altogether.

Several parts of the workbench rely on ids, and it helps to know which:

- Fusion merges hits from different indexes by id. Two indexes built over the same stream share ids, so a section found by both is counted once with both ranks (chapter 14).
- Neighbour expansion uses each document's reading order of ids to add the section after a hit (chapter 15).
- The UI's selected chunk, citation links in answers, `ref.chunk(id)` links written by the agent and `rag.explain(result, id)` all name a chunk by id, so they break when a parameter change renames it.
- Golden cases deliberately do not use ids. They match the expected document and section with patterns against titles and heading paths, so a case written at 450 tokens still works at 200. As section 4.1 showed, they do depend on heading paths.
- The engine's per-item build cache is keyed by a hash of the whole input item and the step's parameters. A chunk whose id or text changed is a different item, so a per-chunk enrichment step computes it again (chapter 11).

The key points to take from this chapter:

- The chunk is the unit of ranking, display, citation and evaluation, so the chunker sets the ceiling for every later stage.
- The default chunker groups by heading up to `depth` and splits by an estimated token budget, keeping code and tables whole up to three times the budget.
- Most sections in this corpus are shorter than 450 tokens, so the budget changes the chunk count less than its size suggests.
- Folding headings into their parents removes them from the heading path, which hurts the label, the index and the evals at once.
- Overlap and heading prefixes add text to chunks; overlap duplicates evidence and heading prefixes duplicate heading words already indexed as a field.
- Several chunk streams over one corpus let one pipeline answer lookups, code questions, outline questions and document questions.
- Chunk ids are predictable from the outline and stable under text edits, but not under changes to chunking parameters.

The next chapter turns chunks into scores: what a BM25 index counts, and why its formula has the shape it does.

**Lab: Chunk sizes, Table rows as chunks, Code search, Outline search, Which report?** Load *Chunk sizes*, open the Evals tab, tick the four variants and run them; you should see the table in section 4.2. Then open the Misses list for `h2-only` and confirm that the rebalance-law case retrieves the right text under the wrong label. In *Table rows as chunks*, search `which HTTP status does ErrConflict map to` and compare the row chunk with the section chunk that contains it. In *Outline search*, search `migration` and notice that every result is one line long.

## 5. BM25 from first principles

This chapter derives the score the workbench uses to rank chunks against a query, and then checks one score by hand against the engine. BM25 is a formula from the 1990s that still sets the baseline in lexical search, and it is worth knowing in detail because every number the Trace tab shows for a lexical retriever is one of its terms. The formula combines three ideas: a term that appears in few chunks is better evidence than one that appears in many; each further occurrence of a term in a chunk adds less than the one before; and a long chunk should not win merely because it has room for more words. By the end of the chapter you should be able to look at a hit's `why` breakdown and say which of those three ideas produced each number.

The score of a chunk *d* for a query *q* is a sum over the query's terms:

```text
score(d, q) = Σ over terms t in q of   idf(t) · tf(t,d) · (k1 + 1) / ( tf(t,d) + k1 · (1 − b + b · len(d) / avglen) )

idf(t) = ln( 1 + (N − df(t) + 0.5) / (df(t) + 0.5) )
```

Here *N* is the number of chunks in the index, *df(t)* is the number of chunks containing *t*, *tf(t,d)* is how often *t* occurs in *d*, *len(d)* is the chunk's length in terms, and *avglen* is the mean length. The two constants are `k1 = 1.2` and `b = 0.75`, the engine's defaults and the values most systems use. The rest of the chapter takes the pieces in order: what counts as a term, then the tf part, the length part, the idf part, and finally how fields change *tf* and *len*.

### 5.1 Tokens, stop words and a small stemmer

Before anything can be counted, text must be cut into terms. The engine's `tokenize` function does four things, and it does exactly the same four things to chunks at build time and to queries at search time. That symmetry is the whole point: a query term can only match an indexed term that went through the same transformation.

1. It finds runs of letters, digits and underscores. Everything else, including `/`, `-`, `.` and backticks, separates terms.
2. It splits each run at underscores and at lowercase-to-uppercase boundaries, and also keeps the joined form. `parseMarkdown` yields `parse`, `markdown` and `parsemarkdown`; `next_cursor` yields `next`, `cursor` and `nextcursor`. A query can then match an identifier by its parts or by its whole name.
3. It lowercases each term, drops single letters (but not single digits) and drops 98 stop words such as *the*, *what*, *does* and *why*.
4. It stems what is left with a short list of suffix rules.

These are real outputs:

```text
tokenize('What does the rebalance law say?')   → [ 'rebalanc', 'law', 'say' ]
tokenize('parseMarkdown next_cursor HTTP2')    → [ 'pars', 'markdown', 'parsemarkdown', 'next', 'cursor', 'nextcursor', 'http2' ]
tokenize('ChatGPT')                            → [ 'chat', 'gpt', 'chatgpt' ]
tokenize('ErrConflict 409')                    → [ 'err', 'conflict', 'errconflict', '409' ]
```

The stemmer is not Porter's algorithm. It removes plural endings (*-ies* becomes *-y*, *-es* after *x*, *ch*, *sh* and *z*, a final *-s* in most other cases), then *-ing*, *-ed* and *-ly* on long enough words, then a doubled final consonant and a final *-e*. The aim is to make *rebalance*, *rebalanced* and *rebalancing* the same term, and it does:

```text
rebalance → rebalanc    rebalancing → rebalanc    rebalanced → rebalanc
policies → policy       classes → class           running → run         quickly → quick
laws → laws             tags → tags               tokens → token
```

The last line shows the stemmer's main limitation. The plural rule only applies to words longer than four letters, so *laws* and *tags* keep their *s*, and a query for *laws* does not match a chunk that only says *law*. The length limit exists to protect short words such as *bus*, *gas* and *this* from being cut; the price is a few short plurals that fail to merge. A larger stemmer would fix this case and introduce others, and the place to measure the trade is the evals, not intuition.

### 5.2 Term frequency and saturation (k1)

The simplest score would count how often each query term appears in the chunk. The problem is that the tenth occurrence of a word is much weaker evidence than the first. The first tells you the chunk is about the topic; the tenth mostly tells you the chunk is long or repetitive. BM25 handles this by passing the count through a function that rises quickly and then levels off:

```text
tf · (k1 + 1) / (tf + k1)          with k1 = 1.2 and no length correction
```

| tf | 1 | 2 | 3 | 5 | 10 | 20 |
| --- | --- | --- | --- | --- | --- | --- |
| contribution | 1.000 | 1.375 | 1.571 | 1.774 | 1.964 | 2.075 |

The contribution can never exceed *k1 + 1*, which is 2.2. *k1* controls how quickly the curve flattens: a small *k1* makes one occurrence nearly as good as many, and a large *k1* makes the score closer to a raw count. One practical consequence is that a chunk which mentions every query term once usually beats a chunk which mentions one query term many times, because the second chunk's extra occurrences are worth little and its missing terms are worth nothing.

### 5.3 Length normalization (b)

A long chunk has more words, so it contains any given term more often by chance. Without a correction, ranking would favour the longest sections in the corpus. BM25 corrects by making *k1* effectively larger for chunks longer than average, which flattens their curve:

```text
K(d) = k1 · (1 − b + b · len(d) / avglen)
contribution = tf · (k1 + 1) / (tf + K(d))
```

With *b* = 0.75 and a term that occurs five times, the contribution depends on length like this:

| len / avglen | 0.5 | 1.0 | 2.0 |
| --- | --- | --- | --- |
| K(d) | 0.75 | 1.20 | 2.10 |
| contribution for tf = 5 | 1.913 | 1.774 | 1.549 |

The parameter *b* sets how strongly length counts. At *b* = 0 length is ignored; at *b* = 1 a chunk twice the average length needs roughly twice the occurrences for the same contribution. Chunking (chapter 4) and *b* interact: a corpus of uniformly sized chunks barely needs length normalization, while a corpus mixing one-line outline chunks with 700-token tables needs it a great deal. The *Field weights* preset includes a `b-0.3` variant for this reason.

### 5.4 Inverse document frequency

The last idea is that matching a rare term is better evidence than matching a common one. In this corpus *workbench* appears in 269 of the 462 chunks, so a chunk containing it has told you very little. *law* appears in 7, so a chunk containing it is one of very few candidates. The idf factor turns that intuition into a weight:

```text
idf(t) = ln( 1 + (N − df + 0.5) / (df + 0.5) )        with N = 462
```

| Term | df | idf |
| --- | --- | --- |
| *law* | 7 | 4.123 |
| *oauth* | 22 | 3.024 |
| *rebalanc* | 24 | 2.939 |
| *agent* | 139 | 1.200 |
| *workbench* | 269 | 0.541 |
| a term in half the chunks | 231 | 0.693 |
| a term in 400 chunks | 400 | 0.145 |

The `1 +` inside the logarithm is a variant used by Lucene and most modern systems. The original formula has no such term and goes negative for a term that appears in more than half the chunks, which would make matching a common word *lower* a chunk's score. With the `1 +`, a common term contributes a small positive amount and never a penalty. The table also explains why stop words are removed: *the* would have df close to 462 and an idf close to zero, so removing it costs almost nothing in ranking and saves a large posting list.

### 5.5 Fields and weights: title, heading path, callout, text

So far a chunk has been a single bag of words. The workbench's chunks have structure: a document title, a heading path, an optional summary callout and the body text. A word in a heading is stronger evidence than the same word in a paragraph, because the author used the heading to say what the section is about. The index handles this with field weights, in the style of BM25F: before scoring, each field's term counts are multiplied by the field's weight, and the chunk's length is the weighted sum of its field lengths.

```js
rag.bm25({ fields: { title: 2, headingPath: 3, callout: 2, text: 1 }, k1: 1.2, b: 0.75 })
```

These are the weights `kit.reports()` uses. The defaults, without `callout`, are `{ title: 2, headingPath: 3, text: 1 }`. A field can be any path into the chunk, including `meta.lang`, which the *Code search* preset weights at 2 so that a query containing *ts* or *go* prefers code in that language.

Weights change rankings in ways that are easy to predict for one query and hard to predict across many. For *opaque server revision token*, the weighted index ranks first the Identity section titled *5. Server revision is an opaque concurrency token*, because four query terms appear in its heading at weight 3. With `fields: { text: 1 }`, that section falls out of the top three, and *2. The three-domain identity model* leads. Across the twelve goldens, the same change has mixed effects:

| Variant of the *Field weights* preset | hit@5 | MRR | nDCG@10 |
| --- | --- | --- | --- |
| title 2, headingPath 3, callout 2, text 1 | 1.000 | 0.933 | 0.847 |
| text 1 only | 1.000 | 1.000 | 0.809 |
| headingPath 6 | 1.000 | 0.938 | 0.842 |
| b 0.3 | 1.000 | 0.938 | 0.829 |

Text-only puts the first relevant section at rank 1 for all twelve questions, while the weighted index ranks one of them fifth; but the weighted index places more of the relevant sections near the top, which is what nDCG rewards. Neither is simply better. Chapter 19 explains how to read a disagreement between metrics.

The title weight deserves one more remark. Every chunk of a document carries the same title, so a query word that appears in a title raises *every* section of that report. That is useful for a question that names a project and harmful for a question that happens to share a word with an unrelated report's title.

### 5.6 Scoring “rebalance law” by hand, then checking it against the engine

The query *rebalance law* comes from the golden case *What does the rebalance law say?*, after tokenization has removed *what*, *does* and *the* and kept *say*. Using the two-word form keeps the arithmetic short. The index is the one `kit.reports()` builds: 462 chunks, 4,682 distinct terms, mean weighted length 168.004.

The engine's top hit is the Workbench Core section *5. The core's modules › 5.9 The rebalance law*, with a score of 12.6758 and this breakdown:

```js
{ id: 'project-report-pbui-workbench-core-a-headless-engine-a-pure-plan#5-9-the-rebalance-law',
  score: 12.6758,
  why: { law: 7.08, rebalanc: 5.59 } }
```

To reproduce it, first count the chunk's terms per field. The title has 11 terms, the heading path 7, the callout none and the text 163. Its weighted length is therefore:

```text
len = 2·11 + 3·7 + 2·0 + 1·163 = 206
```

Then count each query term per field and weight the counts:

| Term | in title | in heading path | in text | weighted tf | df |
| --- | --- | --- | --- | --- | --- |
| *law* | 0 | 1 | 2 | 3·1 + 1·2 = 5 | 7 |
| *rebalanc* | 0 | 1 | 6 | 3·1 + 1·6 = 9 | 24 |

The length factor is shared by both terms:

```text
K = 1.2 · (1 − 0.75 + 0.75 · 206 / 168.004) = 1.2 · 1.16962 = 1.40354
```

Now each term's contribution:

```text
law:       idf = ln(1 + (462 − 7 + 0.5) / (7 + 0.5))   = ln(61.733) = 4.1228
           tf part = 5 · 2.2 / (5 + 1.40354)          = 11 / 6.40354 = 1.71780
           contribution = 4.1228 · 1.71780            = 7.0822

rebalanc:  idf = ln(1 + (462 − 24 + 0.5) / (24 + 0.5)) = ln(18.898) = 2.9391
           tf part = 9 · 2.2 / (9 + 1.40354)          = 19.8 / 10.40354 = 1.90320
           contribution = 2.9391 · 1.90320            = 5.5937

score = 7.0822 + 5.5937 = 12.6759
```

The engine reports 12.6758; the difference in the fourth decimal comes from rounding the intermediate values above. Three observations follow from the arithmetic, one for each idea in the formula. *law* contributes more than *rebalanc* even though it occurs fewer times, because it is rarer: idf 4.12 against 2.94. *rebalanc* occurs nearly twice as often as *law* but its tf part is only 11 percent higher, which is saturation at work. And the chunk is 23 percent longer than average, which raises *K* from 1.2 to 1.40 and costs both terms a little. The heading path supplies 3 of the 5 weighted occurrences of *law*; without field weights the same chunk still ranks first, but with a score of 10.32 instead of 12.68 and a narrower margin over the second hit.

The second hit, *1. Project status* (part 2) of the same report, scores 7.39: it mentions both terms in passing, in a longer chunk with no heading match. That gap of more than five points between first and second is what a confident lexical match looks like. When the gap is small, the trace is telling you that BM25 could not tell the candidates apart, and the later stages of Part V exist for that case.

The key points to take from this chapter:

- The same tokenizer runs at build time and at query time, so a query term matches only what went through the same splitting, lowercasing, stop-word removal and stemming.
- The tf part saturates at k1 + 1, so repeated occurrences add less and less, and covering every query term matters more than repeating one.
- Length normalization raises the saturation constant for long chunks, with b setting how strongly length counts.
- idf weights rare terms above common ones, and the Lucene-style `1 +` keeps every matching term's contribution positive.
- Field weights multiply term counts and lengths per field, which lets headings and summaries count more than body text.
- A hit's `why` object lists each term's contribution, so any lexical score in the workbench can be decomposed and checked.

The next chapter turns to the part of each document that BM25 does not score: the frontmatter fields, which filter and facet rather than rank.

**Lab: Field weights.** Load the preset, search `rebalance law` and open the Trace tab: the `why` values on the first hit should be the 7.08 and 5.59 computed above. Then open Evals, tick `text-only`, `headings-x6` and `b-0.3`, and run them to reproduce the table in section 5.5. As an exercise, add a variant that sets `k1` to 2.0 and predict, before running it, whether the rebalance-law section's margin over the second hit grows or shrinks.

## 6. Metadata as an index

This chapter is about the frontmatter fields that chapter 3 kept out of the text. They describe a document rather than say anything in it: its type, its status, its tags, the other names people use for it. Those values form a small, closed vocabulary, and a closed vocabulary answers different questions than prose does. It can filter a result list exactly, it can be counted to show what a result list contains, and it can recognise a project by name. It ranks badly, because every section of a document carries the same values. By the end of the chapter you should know which of those jobs to give metadata in a pipeline, and what goes wrong when it is given the wrong one.

### 6.1 A terms index over type, tags and aliases

`kit.reports()` builds a second index next to the BM25 one:

```js
.index('meta', rag.terms({ keys: ['type', 'tags', 'status', 'aliases'] }))
```

A terms index is an inverted map from each metadata value, lowercased, to the chunks that carry it, kept separately per key. Over the eleven reports it holds 94 distinct values. Because chunks inherit their document's metadata, the counts are counts of *chunks*:

| Key | Values | Largest |
| --- | --- | --- |
| `type` | 4 | project-report 254, project 99, spec 77, article 32 |
| `status` | 3 | complete 267, active 118, proposed 77 |
| `tags` | 47 | project-report 286, pbui 254, workbench 254, typescript 254, architecture 254, … |
| `aliases` | 44 | *ttc tier b* 17, *coinvault mcp 2026-07-28* 32, *workbench-core* 83, … |

Searching it does not tokenize or stem. The query is lowercased and its punctuation turned into spaces, and a value matches when its words occur in the query as a contiguous phrase. Each match adds a weight to every chunk that carries the value, and the weight falls slowly as the value becomes more common:

```text
weight(value, key) = 1 / ln(2 + n / 10)        n = number of chunks with that value under that key
```

For *what happened in TTC Tier B*, the alias *ttc tier b* matches, it is carried by 17 chunks, and each of them scores 1 / ln(3.7) = 0.7643. That is the index doing its job: a name the team uses for a project, which may appear nowhere in the report's text, routes the question to the right document. All 17 chunks tie, because all 17 carry the same alias; the terms index identifies a document and leaves the choice of section to BM25 and fusion.

The same mechanism produces a failure that is worth studying. For *Which project deals with OAuth?*, two values match. *oauth* is a tag of the CoinVault report, carried by 32 chunks, worth 1 / ln(5.2) = 0.6065. But *project* is also a value: it is the `type` of the three TTC reports and the two Agentforum reports, and one of their tags, carried by 99 chunks under each key, and the question contains the word *project*. Each of those 99 chunks collects 0.4038 twice, for 0.8076, and outranks every CoinVault chunk. In the *Which report?* preset, which fuses this index with a BM25 index over document cards, the TTC order-grain report comes first and CoinVault second. (The five matching reports tie in the meta index; the order-grain report's id sorts first, and fusion with the card index leaves it on top.)

The question used *project* as an ordinary word, and the index read it as a value. BM25 is protected from this by idf, which gives a word found everywhere almost no weight; the terms index has only the gentle `n / 10` decay. There are three remedies, each with a cost. The preset already uses the first: it gives the meta retriever half the weight of the text retriever in fusion (`weights: { docs: 1, meta: 0.5 }`), which limits the damage but does not remove it. The second is to index only keys whose values rarely occur as ordinary words, such as `aliases` and specific tags, and to leave `type` and `status` to filters. The third is to filter out values that are also common query words. The general lesson is that a closed vocabulary is precise when a user *chooses* a value and noisy when values are *recognised* in free text.

### 6.2 Facet counts from a result set

The better use of metadata is to let the user choose. A *facet* is a list of values for one key, each with the number of results that carry it; clicking a value filters the results to it. The *Faceted search* preset wires this up:

````js
const plan = rag.plan('facets').over(kit.reports())
  .signals({ q: 'sync', k: 10, filters: {}, codeOnly: false })
  .retrieve('text', { k: 80 })
  .filter.where()
  .filter((s, ctx, hit) => !s.codeOnly || hit.text.includes('```'), { id: 'code_only' })
  .limit(30);

// in the app
c.facets('filters', { fields: ['type', 'tags', 'status'], from: 'text', max: 10 })
````

The `facets` component counts values over the hits of the node named in `from`, here the BM25 retriever, so the counts describe the result set *before* any filter. For the query *sync*, the 80 retrieved chunks give these counts:

```text
type     project-report 79 · spec 1
tags     architecture 79 · pbui 79 · project-report 79 · typescript 79 · workbench 79 · testing 61
         sync 60 · idempotency 42 · identity 42 · optimistic-concurrency 42
status   complete 79 · proposed 1
```

The counts say something a ranked list cannot say at a glance: 79 of the 80 hits come from the PBUI reports, and the one exception is the agent runtime spec. A reader who wanted the spec's view of synchronisation now knows the result set holds exactly one candidate for it.

Clicking a value writes it into the `filters` signal, and `filter.where()` keeps the hits whose metadata matches. The rule is: within one key, a hit must carry *any* of the selected values; across keys, it must satisfy *every* key. Values compare case-insensitively, and the special key `doc` filters by document id. The trace for `filters: { tags: ['pbui'] }` shows each node's input and output counts:

```text
text       ran   1 → 80
filter     ran  80 → 79
limit      ran  79 → 30
```

Adding `type: ['article']` to the same filter leaves 0 hits, because no PBUI chunk in this result set is an article.

Two decisions in this design are worth stating, because they are choices a designer could make differently. First, counts are taken before filtering, so selecting a tag does not make the other tags' counts collapse to the intersection. The counts keep describing the result set the user started from, which keeps the other values clickable, at the price that a number on a chip is not always the number of results you will get. A selected value with no hits stays visible with a count of 0, so the user can see why the list is empty and undo it. Second, the counts are counts of chunks, not documents: *pbui 79* means 79 sections, from several reports. A facet over documents would need to count distinct `docId`s, and would say *4* instead.

### 6.3 Synonyms in the index versus rewriting the query

A lexical index finds only the words that were written. A reader who asks about *LCA* will not match a report that always writes *latent class analysis*, and a reader who asks about *stateless* mode will not match a section that talks about *server/discover*. The workbench offers two ways to close that gap without calling a model, and the *Query expansion* preset uses both.

The first is a synonyms table attached to the corpus. It is a build node, so its table can be computed from the documents with a bake:

```js
// the preset: a literal table
const corpus = kit.reports()
  .synonyms({ 'oauth': ['authorization', 'token'], 'cursor': ['sequence', 'next_cursor'],
              'stateless': ['server/discover', 'sessionless'] });

// an example: a table baked from every document's aliases at build time
.synonyms(rag.bake(ctx => {
  const table = {};
  for (const d of ctx.docs) for (const a of [].concat(d.meta.aliases ?? []))
    (table[String(a).toLowerCase()] ??= []).push(d.title);
  return table;
}))
```

The table is built once, but it is not written into the index. It is applied inside BM25 when a query is scored: if the query contains a key as a phrase, the tokens of that key's synonyms are added as extra query terms with weight 0.5, unless they are already in the query. The engine's own term list for one query shows the effect:

```text
without the table:  chat 1 · gpt 1 · chatgpt 1 · need 1 · stateless 1 · mode 1
with the table:     chat 1 · gpt 1 · chatgpt 1 · need 1 · stateless 1 · mode 1 · server 0.5 · discover 0.5 · sessionless 0.5
```

For *why did ChatGPT need stateless mode*, the top hit stays the same, *12. Stateful versus stateless Streamable HTTP*, but its score rises from 21.27 to 25.13 and the third hit changes from *15. Failure chronology* to *18. Current status*. The half weight is a hedge: a synonym is evidence that the chunk is about the same thing, but weaker evidence than the word the user typed.

The second way is a rewrite step in the plan, which produces additional queries. The preset's rewrite lambda receives a table of acronyms as a parameter and adds one expanded query for each acronym it finds:

```js
.rewrite((s, ctx, queries, p) => {
  const q = String(s.q).toLowerCase();
  const extra = Object.entries(p.acronyms)
    .filter(([a]) => new RegExp('\\b' + a + '\\b').test(q))
    .map(([a, full]) => q.replace(new RegExp('\\b' + a + '\\b', 'g'), full));
  return [...queries, ...extra];
}, { acronyms: { lca: 'latent class analysis', dcr: 'dynamic client registration', … } })
.retrieve('text', { k: 30, perQuery: true })
```

For *what is the knee rule for LCA*, the rewrite node outputs two queries, the original and *what is the knee rule for latent class analysis*. The retriever scores each separately and keeps each chunk's best score. The top hit, *Latent class analysis: the interpretable alternative*, goes from 9.08 to 20.08, and the second hit becomes *What model selection actually established*, the section a golden case expects for the related question about latent classes.

The two mechanisms differ in ways that decide where each belongs:

|  | Synonyms table | Rewrite step |
| --- | --- | --- |
| Where it lives | the corpus, as a build node | the plan, as a live node |
| Can be computed from documents | yes, with `rag.bake` | only from params and signals |
| Effect on scoring | adds terms at weight 0.5 to the same query | adds whole queries, each scored at full weight |
| Visible in the trace | in a hit's `why` terms | as the rewrite node's output list |
| Applies to | every retriever over that BM25 index | the retrievers downstream of the step |

On the twelve goldens, removing either one changes almost nothing: hit@5 stays at 1.0 and MRR at 0.933, and only nDCG@10 moves, from 0.857 to 0.847 without the synonyms. That is not evidence that expansion is useless. None of the twelve questions uses an acronym or the word *stateless*, so the goldens do not exercise what these steps do. A team that adds an expansion should add the questions that motivated it to the goldens at the same time, or the evals will report that the change did nothing (chapter 18).

The key points to take from this chapter:

- Frontmatter values form a closed vocabulary, which is precise for filtering and counting and noisy for ranking.
- A terms index matches values as phrases in the question and identifies documents, leaving the choice of section to BM25 and fusion.
- Recognising values in free text can misfire when a value is also an ordinary word, as *project* did for the OAuth question.
- Facet counts are computed over the result set before filtering, and they count chunks, not documents.
- Selected values within a key combine with *or*, and different keys combine with *and*.
- A synonyms table is built once and applied inside BM25 at half weight; a rewrite step adds whole queries in the plan.
- An expansion step needs golden cases that use the vocabulary it expands, or the evals cannot see its effect.

This completes the corpus side of the pipeline: documents, chunks, a lexical index and a metadata index. Part III steps back from what each stage computes and asks how a pipeline is written down, so that it can be compiled, compared and changed while it runs.

**Lab: Faceted search, Query expansion.** In *Faceted search*, search `sync`, read the facet counts and confirm that one hit is not from the PBUI reports; then select the tag `pbui` and watch the Trace tab's counts for the filter node. Select `type: article` as well and look at how the empty state is shown. In *Query expansion*, search `what is the knee rule for LCA`, open the Trace tab to see the rewrite node's two queries, then run the evals with the `no-rewrite` and `no-synonyms` variants. Finally, add a golden case that uses an acronym and run the comparison again.
