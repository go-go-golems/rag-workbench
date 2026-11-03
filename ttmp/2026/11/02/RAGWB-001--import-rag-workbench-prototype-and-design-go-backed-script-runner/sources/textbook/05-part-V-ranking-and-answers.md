# Part V · Ranking and answers

The live half of the pipeline turns a question into ranked hits, packs the best of them into a context, and asks Claude for a cited answer. This part takes those steps in order, then gives Claude a larger role in each.

&#91;embedded content: the live pipeline of the Reports preset · three steps call Claude\]

The chapters follow this pipeline. Chapter 14 covers the lexical core in the top row, chapter 15 the path from hits to an answer, and chapter 16 the two places where Claude can rewrite the queries or reorder the hits.

## 14. Retrieval and fusion

This chapter explains how a pipeline with several indexes produces one ranked list. A `retrieve` node asks one index for hits, and a `fuse` node combines several such lists into one. The central claim is that scores from different indexes are on different scales, so they should be combined by rank rather than by adding scores. By the end you should be able to compute a fused score by hand, predict what the fusion constant does, and choose between the two fusion rules the library ships.

The examples use the hybrid corpus from the *Reports* preset: a `text` BM25 index over sections, a `failures` BM25 index over the rows of failure tables, and a `meta` terms index over frontmatter. The question is the book's running example, *Why did ChatGPT show no app actions after OAuth worked?* All output is from `rag.js` running in node over the eleven reports, with `change` runs, so no Claude calls are involved.

### 14.1 The retrieve node: one index, several queries

A `retrieve` node has one input port of type `queries` and one output of type `hits`. When it is wired to the `q` signal, the session turns the question string into a one-element list (chapter 12). When a rewrite step sits in front of it, the list can hold several phrasings. The node's whole job is in a dozen lines of `rag.js`:

```js
const qs = p.perQuery === false ? [queries.join(' ')] : queries;
const best = new Map();
for (const q of qs) for (const h of ix.search(q, p.k ?? 20, { synonyms: ctx.synonyms })) {
  const prev = best.get(h.id);
  if (!prev || h.score > prev.score) best.set(h.id, { ...h, q });
}
return [...best.values()].sort((a, b) => b.score - a.score)
  .slice(0, p.k ?? 20).map(h => ({ ...h, via: ctx.node.id }));
```

Each query is searched on its own, and a chunk found by several queries keeps its best score. Every hit records the query that found it in `q` and the node that produced it in `via`. The alternative, `perQuery: false`, joins the queries into one string, so a chunk that matches several phrasings accumulates term contributions from all of them. Here is the difference for *why did DCR fail* with a rewrite lambda that adds two phrasings:

```text
perQuery (default)
1. 13.596   q="last_used_at migration"            CoinVault › 5.1 Observability channels
2. 12.7352  q="dynamic client registration 503"  CoinVault › 1. The acceptance problem
3. 11.022   q="last_used_at migration"            CoinVault › 15. Failure chronology and diagnostic lesson

perQuery: false
1. 23.7946  q="why did DCR fail dynamic client registration 503 last_used_at migration"  CoinVault › 15. Failure chronology
2. 22.0751  (same joined query)                                                         CoinVault › 5.1 Observability channels
3. 16.862   (same joined query)                                                         CoinVault › 6.2 …
```

The joined form rewards the failure-chronology section, which mentions the 503, the registration and the migration together; the per-query form lets each phrasing put forward its own best section. Neither is always right. Per-query is the default because it keeps a bad rewrite from changing the scores of the original question.

A retrieve node's id defaults to the index name, so `retrieve('text')` becomes node `text`. If that id is taken, the compiler appends a suffix. In the Reports preset a chunk node is already called `failures`, so the second retriever becomes `failures_2`. Section 14.3 shows why this matters.

For the running question the three retrievers return these lists:

| Rank | `text` (BM25 over sections) | `failures_2` (BM25 over failure rows) | `meta` (terms) |
| --- | --- | --- | --- |
| 1 | 26.476 · 11. Why ChatGPT still showed no actions | 16.541 · row: ChatGPT connected but showed no actions | 1.213 · 1.1 Exact deployed identity |
| 2 | 19.340 · 6.1 RFC 9728 path-specific metadata | 14.626 · row: ChatGPT said OAuth unsupported | 1.213 · 1. The acceptance problem |
| 3 | 19.121 · 15. Failure chronology and diagnostic lesson | 6.045 · row: Authorization returned invalid\_scope | 1.213 · 10. The hidden tool-scope propagation bug |
| 4 | 18.522 · 9. Consent behavior in a real browser | 2.271 · row: Inspector consent returned invalid\_grant | 1.213 · 11. Why ChatGPT still showed no actions |
| hits | 40 | 6 | 20 |

All three lists point at the CoinVault report, which is correct. They disagree about everything else, including the scale of their numbers.

### 14.2 Why two BM25 scores cannot be added

A BM25 score (chapter 5) is a sum over query terms of an IDF weight times a saturated, length-normalized term frequency. Every part of that formula is computed inside one index: the IDF depends on how many chunks of *that* collection contain the term, the length normalization depends on *that* collection's average length, and the field weights are *that* index's configuration. Two indexes over different chunk streams therefore produce numbers that cannot be compared. The `text` index's best score above is 26.5; the failure-row index's best is 16.5; the terms index gives every chunk of the matching document the same 1.213.

The effect holds even for the same chunk and the same query. In the *Fusion* preset, the section *Metadata: two representations, one* of the Agentforum report scores 24.28 in the `text` index and 3.93 in a `headings` index that sees only heading paths. If scores were added, the `text` index would decide every ranking and the other indexes would contribute rounding noise.

Normalizing each list, for example to the range 0 to 1, looks like a fix but depends on the shape of each list. The `meta` list is flat: twenty chunks tie at 1.213, because the terms index scores a match on a frontmatter value with `1 / log(2 + n/10)` and gives that score to every chunk of the document. Its order within the tie is alphabetical by chunk id, which carries no information. Any normalization of that list turns an arbitrary tie-break into apparent preference.

Ranks avoid the problem. Rank 1 means "the best this retriever could find," whatever the retriever's scale, and a rank-based rule only needs to decide how much a rank-1 vote is worth compared with a rank-10 vote.

### 14.3 Reciprocal rank fusion: 1/(k + rank), weights, and what k = 60 does

Reciprocal rank fusion (Cormack, Clarke and Büttcher, 2009) scores a chunk by summing, over the lists it appears in, the reciprocal of a constant plus its rank:

*score(c) = Σ\_lists w\_list / (k + rank\_list(c))*

The library's `fuse.rrf` is a direct transcription. Its input port is `hits*`, which accepts any number of hit lists; each list's weight is looked up by the id of the node that produced it:

```js
lists.forEach((list, li) => {
  const src = ctx.node.in[li], wt = w[src] ?? 1;
  arr(list).forEach((h, r) => {
    const a = acc.get(h.id) || { id: h.id, score: 0, parts: {} };
    a.score += wt / (k + r + 1);
    a.parts[src] = r + 1;
    acc.set(h.id, a);
  });
});
```

Each fused hit carries `parts`, the rank it had in each input list, which is what the Trace tab shows when you select a fused row. For the running question with `k: 60` and weights `{ text: 1, failures: 0.8, meta: 0.3 }` the top three are:

| Fused rank | Section | parts | By hand | Engine |
| --- | --- | --- | --- | --- |
| 1 | 11. Why ChatGPT still showed no actions | text 1, meta 4 | 1/61 + 0.3/64 = 0.02108 | 0.02108 |
| 2 | 15. Failure chronology and diagnostic lesson | text 3, meta 7 | 1/63 + 0.3/67 = 0.02035 | 0.02035 |
| 3 | 1. The acceptance problem | text 6, meta 2 | 1/66 + 0.3/62 = 0.01999 | 0.01999 |

Two things in this table deserve attention. First, the failure row *ChatGPT connected but showed no actions*, rank 1 in the failure index and arguably the most direct answer in the corpus, is not in the fused top ten. It appears in one list only, so its score is 1/61 = 0.01639, while the tenth fused hit has 0.01733. Sections appear in both `text` and `meta`, because the terms index votes for every chunk of a matching document; failure rows are a separate chunk stream that `meta` never sees. RRF rewards agreement among retrievers, and a retriever over its own chunk stream cannot receive agreement. The remedies are a larger weight for that retriever, a smaller `k` (below), or a terms index over the failure rows as well.

Second, the weight `failures: 0.8` in the preset never applies. Weights are keyed by input node id, the retriever's id is `failures_2`, and a missing key falls back to weight 1. The compiler does not check weight keys against input ids, so the mistake is silent. You can see it by printing the node:

```text
reports fuse in [ 'text', 'failures_2', 'meta' ] {"k":60,"weights":{"text":1,"failures":0.8,"meta":0.3}}
```

The constant `k` sets how quickly a vote loses value with rank. With small `k`, rank 1 is worth far more than rank 2; with large `k`, all ranks are worth nearly the same and the score approaches a weighted count of the lists a chunk appears in. Here are the top three for four values:

| k | 1st | 2nd | 3rd |
| --- | --- | --- | --- |
| 1 | 0.56 · 11. Why ChatGPT still showed no actions | 0.5 · failure row *ChatGPT connected but showed no actions* | 0.348 · 6.1 RFC 9728 path-specific metadata |
| 10 | 0.112 · 11. Why ChatGPT … | 0.0946 · 15. Failure chronology | 0.0933 · 6.1 RFC 9728 … |
| 60 | 0.02108 · 11. Why ChatGPT … | 0.02035 · 15. Failure chronology | 0.01999 · 1. The acceptance problem |
| 600 | 0.00216 · 11. Why ChatGPT … | 0.00215 · 15. Failure chronology | 0.00215 · 1. The acceptance problem |

At `k = 1` the failure row reaches second place on the strength of a single first-place vote. At `k = 600` the second through fourth scores differ in the fifth decimal, so the order is decided almost entirely by how many lists a chunk is in and their weights. The value 60 comes from the original paper, where it worked well across many TREC runs; it is a reasonable default, not a law.

The *Fusion* preset scores four variants on the twelve golden questions (chapter 18). These are real results:

| Variant | hit@5 | MRR | nDCG@10 |
| --- | --- | --- | --- |
| fusion (rrf, k 60, weights text 1, headings 0.7, meta 0.3) | 1 | 0.938 | 0.850 |
| rrf-k10 | 1 | 0.944 | 0.862 |
| fuse-max | 1 | 0.933 | 0.847 |
| text-weight-only | 1 | 0.933 | 0.847 |
| equal-weights | 1 | 0.896 | 0.865 |

The differences come from two or three questions. *How does protojson serialize int64 values?* finds its section at rank 1 under max fusion and rank 4 under the default RRF, while *How does the eval tool report line numbers in errors?* goes the other way, from rank 5 to rank 1. With twelve cases, one question moving changes MRR by several hundredths; chapter 19 discusses how large a difference must be before it means anything.

`fuse.rrf` also takes `limit` (default 60), the length of the fused list, and sets `via` on each hit to the fuse node's id.

### 14.4 Max fusion, and when it is the better rule

`fuse.max` keeps, for each chunk, the hit with the highest raw score from any list:

```js
for (const list of lists) for (const h of arr(list))
  if (!acc.has(h.id) || acc.get(h.id).score < h.score) acc.set(h.id, h);
return [...acc.values()].sort((a, b) => b.score - a.score);
```

This compares raw scores across lists, which section 14.2 argued against, and the running question shows the consequence: all of the top six fused hits come from `text`, whose scores run highest. On the goldens, `fuse-max` produces exactly the same numbers as `text-weight-only`; with these three indexes, max fusion is text-only retrieval.

Max fusion is the right rule when the lists share a scale. That happens when several lists come from the same index, for example several retrieve nodes over `text` with different query rewrites, or when one index is primary and the others exist to add chunks the primary missed, never to reorder it. In the first case the retrieve node's `perQuery` mode already does max fusion internally.

### 14.5 Filters and limits: `filter.where`, filter lambdas and `s.k`

After fusion, a pipeline usually narrows the list. `filter.where` reads a signal (default `filters`) holding an object of frontmatter keys to accepted values, and keeps hits whose chunk metadata contains one of the accepted values for every key. Values within a key are alternatives; keys are all required; matching ignores case; the key `doc` matches the document id. An empty object passes every hit through, which is why the facet panel's initial state is `{}`.

A filter lambda has the shape `(s, ctx, hit, p) => boolean`, where `hit` is a view with the chunk's `title`, `headingPath`, `text`, `meta` and `tokens` already attached. The *Faceted search* preset chains both:

````js
rag.plan('facets').over(kit.reports())
  .signals({ q: 'sync', k: 10, filters: {}, codeOnly: false })
  .retrieve('text', { k: 80 })
  .filter.where()
  .filter((s, ctx, hit) => !s.codeOnly || hit.text.includes('```'), { id: 'code_only' })
  .limit(30);
````

These are the trace counts for five signal settings:

| Signals | text | filter | code\_only | limit |
| --- | --- | --- | --- | --- |
| `{}` | 1→80 | 80→80 | 80→80 | 80→30 |
| `filters: { tags: ['go'] }` | 1→80 | 80→18 | 18→18 | 18→18 |
| `filters: { tags: ['go', 'r'] }` | 1→80 | 80→18 | 18→18 | 18→18 |
| `filters: { type: 'spec' }` | 1→80 | 80→1 | 1→1 | 1→1 |
| `tags: ['go']`, `codeOnly: true` | 1→80 | 80→18 | 18→6 | 6→6 |

The `type: 'spec'` row shows the cost of filtering after retrieval: only one of the 80 retrieved sections came from the one spec document, so the user sees one result even though the spec has dozens of sections. A filter can only remove; it cannot ask the index for more. When filters are expected, either retrieve more (`k: 80` instead of 20) or filter inside the retriever with a custom lambda that calls `ix.searchIn(docId, q, k)`.

`limit` cuts the list to `k`. Its parameter can be a number or a lambda, and `limit(s => s.k)` reads the `k` signal, so moving a result-count slider reruns only the limit node (chapter 12).

The key points:

- A retrieve node searches each query separately by default and keeps each chunk's best score, so one bad phrasing cannot lower the scores of the others.
- BM25 scores from different indexes are on different scales, because IDF, length normalization and field weights are all computed per index; even one chunk can score 24 in one index and 4 in another.
- Reciprocal rank fusion combines lists by rank, rewards chunks that several retrievers agree on, and lets `k` trade the value of a first place against the value of agreement.
- Fusion weights are keyed by input node id. A key that names no input is ignored without an error, as the Reports preset's `failures` weight shows.
- Max fusion is correct only when the lists share a scale; across different indexes it reduces to the highest-scoring index.
- Filters can only remove hits, so a pipeline that expects narrow filters must retrieve more than it shows.

The fused, filtered list is still a list of ids and scores. The next chapter turns its best entries into the text Claude reads and the answer the user sees.

**Lab: Fusion; Reports · hybrid + rerank + answer.** Load *Fusion*, open the Evals tab, tick all variants and run them; then find the protojson question in the per-case table and select the `fuse` node in the DAG tab to see its `parts`. Set `k` to 1 and then to 600 and watch the order of the top five change. In *Reports*, select the fuse node and compare its `weights` keys with the ids of its inputs; rename the weight key to `failures_2`, rerun the running question, and see where the failure row lands.

## 15. Context and answers

This chapter covers the three steps between a ranked list and an answer: expanding the best hits with nearby material, packing them into a context under a size budget, and asking Claude to answer from that context with citations. The point to take away is that the answer can cite only what was packed. Packing is therefore the last ranking decision in the pipeline, and it is made by simple, inspectable rules rather than by the model.

The examples use the *Context expansion* preset and its question, *How does batch-preserving sync relate to idempotent operation ids?* That question is useful because its answer spans two reports: the Workbench Identity report defines operation ids, and the Workbench Core report describes the sync loop. The prompt text below is the real prompt the engine builds. Claude's replies come from a mock sampler that returns a fixed string, and they are marked as mock wherever they appear.

The plan is:

```js
rag.plan('context').over(kit.reports())
  .signals({ q: 'How does batch-preserving sync relate to idempotent operation ids?', k: 6 })
  .retrieve('text', { k: 30 })
  .limit(s => s.k)
  .expand.neighbors({ before: 0, after: 1, top: 4 })
  .expand.links({ decay: 0.4, max: 3, top: 3 })
  .pack({ budget: 40000, order: 'doc' })
  .answer();
```

Its trace on a `submit` run:

```text
text       ran  1→30
limit      ran 30→6
neighbors  ran  6→8
links      ran  8→10
pack       ran 10→8
answer     ran  8→8   calls 1
```

The rest of the chapter explains each of those counts.

### 15.1 Expanding hits: neighbouring sections and linked reports, with score decay

A section is a unit of retrieval, not a unit of meaning. The section that matches a question best often sets up an idea that the next section completes. `expand.neighbors` adds, for each of the first `top` hits, the sections just before and after it in reading order. Reading order comes from the first chunk stream of the corpus, so a neighbour of a section is always another section of the same document.

```js
hits.forEach((h, i) => {
  out.push(h);
  if (i >= (p.top ?? 5)) return;
  for (const n of ctx.neighbors(h.id, { before: p.before ?? 0, after: p.after ?? 1 })) {
    if (have.has(n.id) || extra >= (p.maxChars ?? 6000)) continue;
    have.add(n.id);
    extra += n.text.length;
    out.push({ id: n.id, score: +(h.score * (p.decay ?? 0.5)).toFixed(5), via: ctx.node.id, of: h.id });
  }
});
```

Three details matter. A neighbour is inserted directly after the hit that brought it, so list order keeps the two together. Its score is the parent's score times `decay` (0.5 by default), which keeps it below its parent if a later step sorts. And it records its parent in `of`, which the results list shows as a chip. In the example, the top four hits each asked for one following section, but only two were new:

```text
1. 18.1984  via text                                6.1 Batch identity
2.  9.0992  via neighbors  of 6.1                   6.2 Concrete send identity
3. 17.116   via text                                18. Tests as identity laws
4.  8.558   via neighbors  of 18.                   19. Consumer and packaging verification
5. 17.0725  via text                                6. Two levels of operation identity
6. 17.0014  via text                                1. The problem was semantic, not syntactic
…
```

The neighbour of section 6 is 6.1, already present; the neighbour of section 1 is section 2, which was already at rank 6. The `maxChars` cap (6,000 characters by default) bounds how much expansion can add, whatever `top` says.

All eight hits so far come from one report. `expand.links` reaches across reports. At build time the engine resolves each document's `[[wiki links]]` and its `related_vault_notes` frontmatter to document ids by matching titles, file names and aliases. In this corpus four reports have links:

| Report | Linked reports |
| --- | --- |
| PBUI Workbench Identity | 2 |
| PBUI Datalab on Workbench Core | 2 |
| PBUI Workbench Stabilization | 2 |
| PBUI Workbench Core | 1 |
| the other seven | 0 |

For each of the first `top` hits, `expand.links` visits the linked documents not yet represented, searches each one for the question with `searchIn`, and adds the best section, scored at the parent's score times `decay`. With `decay: 0.4` and `max: 3` it added two sections:

```text
 9. 7.27936  via links  of 6.1   PBUI Workbench Core › 5. The core's modules › 5.10 Batch-preserving sync, and persistence
10. 7.27936  via links  of 6.1   PBUI Workbench Stabilization › (introduction)
```

The first of these is exactly the missing half of the question: the section that describes the sync loop. It did not rank in the top six by itself because the question's words about ids and idempotency dominate. Following an author's link found it, which is a retrieval signal that BM25 cannot see.

### 15.2 Packing under a budget: rank order, per-document caps, the 40 KB default

`pack` turns hits into one string and an evidence table. Its rules are short enough to state in full:

```js
for (const h of hits) {
  const c = ctx.chunk(h.id);
  const n = perDoc.get(c.docId) || 0;
  if (n >= (p.maxPerDoc ?? 6)) continue;            // cap per document
  const piece = c.text.length;
  if (bytes + piece > budget && evidence.length) break; // stop at the first overflow
  perDoc.set(c.docId, n + 1);
  bytes += piece;
  evidence.push({ label: 'E' + (evidence.length + 1), id: c.id, docId: c.docId, title: c.title, headingPath: c.headingPath });
}
```

Packing walks the list in order; it does not re-rank. It skips a chunk whose document already supplied `maxPerDoc` pieces (six by default), and it stops at the first chunk that would overflow the budget, except that the first chunk always enters, so a context is never empty when there are hits. The budget counts characters of chunk text (JavaScript string length), not bytes; the default of 40,000 is about 40 KB for English text. It leaves most of Claude's prompt allowance (chapter 31) for the instructions and the question, and it keeps the cost of each answer predictable.

In the example, ten hits became eight pieces. The Identity report contributed eight of the ten hits, so the cap of six dropped its last two (*13. Invalid aggregate isolation* and *2. The three-domain identity model*), and the two linked sections entered after them. The result was 8,361 characters from three reports. Changing the budget and cap gives:

| budget | maxPerDoc | pieces | characters | reports |
| --- | --- | --- | --- | --- |
| 40,000 | 6 | 8 | 8,361 | 3 |
| 8,000 | 6 | 7 | 7,791 | 2 |
| 40,000 | 2 | 4 | 3,724 | 3 |
| 1,000 | 6 | 1 | 899 | 1 |

The per-document cap is a diversity rule. Without it, a question that one report answers well would fill the whole context with that report, and a second report holding the other half of the answer would never be read. The 8,000-character row shows how the budget and the cap interact: the six Identity pieces use 6,435 characters, the cap skips the remaining Identity hits, the Workbench Core section (1,356 characters) still fits, and the Stabilization introduction is the first piece that would overflow. Had the cap been higher, two more Identity sections would have entered first and the Core section, the sync half of the answer, would not have fitted.

`order: 'doc'` changes the order of the text, not the selection. Pieces are grouped by document and sorted by their position in the document, so Claude reads each report's sections in the order their author wrote them. Labels are assigned before reordering, in rank order, so in this example the context begins with `[E7]`, the Workbench Core section.

### 15.3 Evidence labels and the answer prompt

Each piece in the context starts with its label, title and heading path. This is the real beginning of the context for the example:

```text
[E7] PBUI Workbench Core: A Headless Engine, a Pure Planner, and the Hard Cutover of the React Shell › 5. The core's modules › 5.10 Batch-preserving sync, and persistence
`sync/index.ts` is the union of the agentlogic and turboproof loops that the earlier report described, rewritten so the outbox holds whole committed batches:
…

---

[E1] PBUI Workbench Identity: Local Revisions, Opaque Server Tokens, and Idempotent Sync Operations › 6. Two levels of operation identity › 6.1 Batch identity
…
```

The `answer` node wraps the context in a fixed frame. This is the prompt it sent, with the evidence elided:

```text
Answer the question using only the evidence below, drawn from a set of engineering project reports. Cite evidence inline as [E1], [E2]. If the evidence does not answer the question, say what is missing. Be specific and concise: under 200 words unless the question needs more. Use plain markdown.

<evidence>
[E7] PBUI Workbench Core …
…
</evidence>

Question: How does batch-preserving sync relate to idempotent operation ids?
```

The whole prompt was 9,980 characters, sent at the `default` model tier. Each sentence of the instruction has a job. *Using only the evidence below* tells the model not to answer from its own knowledge, which for private project reports would be invention. *Cite evidence inline as \[E1\]* fixes a syntax that code can parse afterwards. *Say what is missing* gives the model a correct thing to do when retrieval failed, instead of an incentive to guess.

The `instructions` parameter replaces that first paragraph; the evidence block and the question are always appended. The *Two answers* preset uses this to branch one context into two answers:

```js
const ctxt = g.node('pack', rag.pack({ budget: 30000 }), { from: top });
g.node('brief', rag.answer({ tier: 'quick', instructions: 'Answer in at most two sentences, using only the evidence below. Cite as [E1].' }), { from: ctxt });
g.node('detailed', rag.answer({ instructions: 'Answer thoroughly from the evidence below. End with a short bulleted list of the key facts. Cite every fact as [E1], [E2].' }), { from: ctxt });
```

Its trace shows one pack feeding two answer nodes, one call each:

```text
text:ran 1→30 | top:ran 30→8 | pack:ran 8→8 | brief:ran 8→8 calls 1 | detailed:ran 8→8 calls 1
```

### 15.4 Citations as data, and an answer that says what is missing

The answer node returns a value, not just text:

```text
{ text: "MOCK answer citing [E1] and [E2].",        ← mock sampler output
  evidence: [ { label: "E1", id: "…#6-1-batch-identity", docId: "…", title: "…", headingPath: […] }, … 8 entries ],
  cited: [ "E1", "E2" ],
  truncated: false }
```

`cited` is computed by matching `\bE\d+\b` in the text. `evidence` maps every label to a chunk id. Together they let the interface turn `[E2]` into a link to section 6.2, show which packed pieces were cited and which were not, and let evals score an answer-only plan by the sections it used (chapter 18). They also bound what a citation can prove: the engine checks that a label exists, not that the sentence beside it is supported by that section. Checking support is the job of a judge, which chapter 20 adds.

When retrieval returns nothing, the answer node does not call Claude at all. A question with no matching terms produces this, with zero calls in the trace:

```text
{ text: "No evidence was retrieved for this question.", evidence: [], cited: [] }
```

That message is written by the engine, not the model. When there is evidence but it does not answer the question, the instruction asks the model to say what is missing; that is a request, and only an eval with `answerHas` or a judge can tell whether it was honoured.

### 15.5 Streaming the answer and showing its sources

`answer` has trigger `submit` and `skip: 'null'`. On a `change` run (typing) it is skipped and its output is `null`; the trace shows `answer:skipped` and the answer panel keeps the last answer or stays empty. On a `submit` run it calls Claude and streams text as it arrives through the run's `onText(nodeId, text)` callback, which the `answer` component uses to show the response as it is written. The node id in the callback is what lets a page with two answer nodes route each stream to its own panel. With the mock sampler, the stream is a single 33-character delivery; with Claude it arrives in many pieces.

For a designer, these mechanics translate into a few decisions:

- The answer area should say that it waits for Enter, because typing alone will never fill it.
- Each `[E#]` in the text should link to the section it names, and the list of sources should mark which pieces were cited, so the reader can check a claim in one click.
- A source list should show the title and heading path of each piece, which is the same information the model saw in each piece's header.
- `truncated: true` means the model stopped at its output limit; the interface should say so rather than present a cut-off answer as complete.

The key points:

- Expansion adds sections the ranking missed, from the same document by reading order or from linked documents by the author's own links, and marks each added hit with the hit that brought it.
- Packing walks the list in order, caps each document's share, and stops at the first piece that would overflow the budget, so the context is a predictable function of the list.
- The budget is counted in characters of chunk text, 40,000 by default.
- Evidence labels are assigned in rank order and survive reordering, so a citation always names the same section regardless of where it appears in the prompt.
- An answer is a value with text, evidence and parsed citations; the engine verifies that cited labels exist, not that claims are supported.
- With no evidence, the engine answers for itself and spends no call.

Everything in this chapter is deterministic except the final call. The next chapter gives Claude two earlier places to act, before retrieval and between retrieval and packing.

**Lab: Context expansion; Two answers.** In *Context expansion*, press Enter and look at the result chips: the `neighbors` and `links` rows name the hit that brought them. Set `maxPerDoc` on the pack node to 2 and check which reports enter the context; then set `budget` to 8000 with maxPerDoc back at 6 and see which piece is the first to drop. In *Two answers*, edit the `brief` node's `instructions` in the DAG tab and press Enter; the trace shows one pack and two answer calls.

## 16. Rewriting and reranking

A ranking can be changed without touching an index. Before retrieval, a pipeline can change the queries: add expansions of an acronym, keyword phrasings of a question, or a drafted answer. After retrieval, it can change the order: boost by features the index never scored, or let Claude read the passages and reorder them. This chapter covers both places, first with lambdas that cost nothing and then with Claude. It ends with the rule that keeps Claude's steps off the keystroke path.

As in the previous chapter, prompts are the real prompts the engine builds, and every Claude reply shown is from a mock sampler. The mocks were written to exercise the code paths, including malformed replies; they say nothing about how Claude would actually rank these passages.

### 16.1 Rewrite lambdas: acronyms and synonyms

A `rewrite` node maps a list of queries to a list of queries. Its lambda has the shape `(s, ctx, queries, p) => string[]`; the engine deduplicates the result and drops empty strings. The *Query expansion* preset uses one to expand acronyms from a table passed as a parameter:

```js
.rewrite((s, ctx, queries, p) => {
  const q = String(s.q).toLowerCase();
  const extra = Object.entries(p.acronyms)
    .filter(([a]) => new RegExp('\\b' + a + '\\b').test(q))
    .map(([a, full]) => q.replace(new RegExp('\\b' + a + '\\b', 'g'), full));
  return [...queries, ...extra];
}, { acronyms: { lca: 'latent class analysis', dcr: 'dynamic client registration', /* … */ } })
.retrieve('text', { k: 30, perQuery: true })
```

The table is a parameter, not a closure variable, because lambdas are stored as source and run without their closures (chapter 9). For *what is the knee rule for LCA* the node produces two queries, and the second changes what is found:

```text
queries ["what is the knee rule for LCA", "what is the knee rule for latent class analysis"]

with rewrite                                                           without rewrite
1. 20.078   Tier B › The mixture models… › Latent class analysis       1. 9.0812  Tier B › The mixture models… › Latent class analysis
2. 16.5276  Broken Exports › What model selection actually established   2. 7.7937  Tier B › What was learned
3. 16.2573  Tier B › The mixture models… › Latent class analysis…      3. 7.3555  Broken Exports › What model selection actually established
```

The top hit records `q: "what is the knee rule for latent class analysis"` and its term contributions `{ latent: 5.77, class: 5.25, knee: 4.81, analysis: 4.24 }`. The word *LCA* alone matched little, because the reports mostly spell the term out.

The same corpus also has an index-time synonym table (chapter 6). The two mechanisms differ in where they act and how much weight they give:

```text
index.terms('why did the oauth cursor break', synonyms)
→ oauth 1, cursor 1, break 1, authorization 0.5, token 0.5, sequenc 0.5, next 0.5, nextcursor 0.5
```

|  | Synonym table | Rewrite lambda |
| --- | --- | --- |
| Acts on | the terms of one query, inside the index | the list of queries, before retrieval |
| Weight of added words | 0.5 | 1, in a query of their own |
| Effect with `perQuery` | none; it is still one query | each phrasing is searched and ranked on its own |
| Visible in the trace | as term contributions in `why` | as the `rewrite` node's output list |
| Good for | near-synonyms that should help but not dominate | expansions that are the question in other words |

### 16.2 `rewrite.llm`: keyword queries in the corpus's own vocabulary

Users ask in their own words; reports are written in their authors' words. `rewrite.llm` asks Claude to bridge the gap. This is the exact prompt for the *Rewrite + rerank* preset's question:

```text
Rewrite the question as 3 short keyword queries (2-6 words each) for a BM25 index over engineering project reports. Use the vocabulary such reports would use, including likely technical terms. Reply with only JSON: {"queries": [string]}.

Question: Why did ChatGPT say OAuth was unsupported?
```

It is sent at the `quick` tier through `sample.json`, which parses the reply. The node keeps the original question first and appends at most `n` new queries. With the mock reply, its output was:

```text
["Why did ChatGPT say OAuth was unsupported?",      ← always kept
 "stateless discovery tools/list",                  ← mock reply
 "chatgpt connector no actions",                    ← mock reply
 "mcp session initialize"]                          ← mock reply
```

Keeping the original matters for the same reason `perQuery` is the default: if the rewrite is poor, the original question still gets its own ranking. The prompt asks for keywords rather than sentences because BM25 ignores grammar; *2-6 words* keeps each query focused on one idea.

### 16.3 HyDE: searching with a drafted answer

HyDE (hypothetical document embeddings, Gao et al., 2022) was proposed for vector search: draft a plausible answer, then search with the draft instead of the question, because an answer resembles other answers more than a question does. The idea transfers to BM25 because a drafted answer uses the vocabulary of the documents. The library has no HyDE component; the *HyDE* preset is a rewrite lambda that calls Claude:

```js
.rewrite(async (s, ctx, queries, p) => {
  if (!s.hyde) return queries;
  const r = await ctx.sample(`Write one plausible paragraph (about ${p.words} words) from an engineering project report that answers: ${s.q}\nNo preamble, no headings.`, { modelTier: 'quick' });
  ctx.note(r.text.slice(0, 160));
  return [...queries, r.text];
}, { trigger: 'submit', words: 80 })
```

Three features of the grammar meet here. The lambda reads a signal, `hyde`, so a toggle on the page turns the step off without editing code. It sets its own trigger to `submit`, because it calls Claude. And it records the draft with `ctx.note`, which the page shows in a card beside the results.

With a mock paragraph about latent classes and BIC, the results were:

```text
with the draft (mock)                                     hyde off
1. 52.1927  Broken Exports › What model selection…         1. 20.2441  Broken Exports › What model selection…
2. 39.3875  Tier B › … › Latent class analysis…          2. 19.0413  Tier B › The mixture models…
3. 32.672   Tier B › (introduction)                       3. 17.2575  Tier B › (introduction)
```

All of the top six hits were found by the draft, not by the question. That is a property of the retriever, not of HyDE: `perQuery` keeps each chunk's best raw score, and an 80-word draft matches far more terms than a 15-word question, so its scores are larger. This is the scale problem of section 14.2 inside a single retriever. If the original question should keep a voice, give the draft its own retrieve node and fuse the two lists by rank.

### 16.4 Rerank lambdas: heuristics over fields the index never scored

BM25 scores terms. It does not know that a heading which repeats the question's words is probably the answer, that a section with a code block is what a programmer wants, or that a 40-token section is too short to answer anything. A `rerank` lambda can. It receives the hits as views, with `title`, `headingPath`, `text`, `meta` and `tokens` attached, and returns hits in its preferred order. This is the *Rerank with a lambda* preset:

````js
.rerank((s, ctx, hits, p) => {
  const words = String(s.q).toLowerCase().match(/[a-z0-9]{4,}/g) || [];
  const overlap = t => words.filter(w => t.toLowerCase().includes(w)).length / Math.max(1, words.length);
  return hits.map(h => {
    const o = overlap(h.headingPath.join(' '));
    let score = h.score * (1 + p.heading * o);
    if (h.text.includes('```')) score *= 1 + p.code;
    if (h.tokens < p.minTokens) score *= p.shortPenalty;
    return { id: h.id, score, reason: `heading overlap ${o.toFixed(2)}` };
  }).sort((a, b) => b.score - a.score);
}, { heading: 1.5, code: 0, minTokens: 60, shortPenalty: 0.6 })
````

For *Why did ChatGPT show no app actions?* the long words are `chatgpt`, `show` and `actions`. The section *11. Why ChatGPT still showed no actions* contains all three in its heading (`show` matches inside *showed*), so its overlap is 1.00 and its score goes from 20.90 to 52.26. Every other top section has overlap 0 and keeps its score. Raising `heading` to 3 widens the gap to 83.61 and lets a PBUI section with overlap 0.33 into the top five. That is the usual shape of a boost: one parameter that helps the case it was designed for and starts to pull in unrelated material when pushed.

Whatever the lambda returns is normalized: strings become hits with score `1/(rank)`, and objects keep `id`, `score`, `via` and `reason`. `reason` is shown in the results list and the trace, so a heuristic can explain itself. Because the node's trigger is `change`, the lambda runs on every keystroke at zero calls; the trace for a keystroke is `text 1→40 | rerank 40→40 | limit 40→10`.

When a heuristic is worth reusing, `rag.define` turns it into a component (chapter 8). The *Your own components* preset defines `diversify` (at most `perDoc` sections per document) and `boost.tags` (multiply the score of chunks tagged with the `boostTag` signal). Its trace for *testing and verification* with `boostTag: 'go'` is `text 1→60 | tags 60→60 | diversify 60→16 | limit 16→10`. The two components pull in opposite directions, and the counts show it: without either, the top ten come from five reports; the tag boost alone concentrates them on three reports, seven of the ten from Workbench Stabilization; adding diversify spreads them back over six.

### 16.5 `rerank.llm`: passages, scores and stated reasons

A lexical score cannot tell whether a passage answers a question or merely mentions its words. Claude can, at the price of a call. `rerank.llm` sends the first `top` hits (20 by default) as numbered passages, each clipped to 700 characters:

```text
Rank the passages by how directly they answer the question. Reply with only JSON: {"ranking": [{"p": "p3", "score": 0.0-1.0, "reason": "under 12 words"}]}, best first, including only passages that help answer it.

Question: Why did ChatGPT say OAuth was unsupported?

Passages:
[p1] PROJECT REPORT - CoinVault MCP - From Local Conformance to ChatGPT 2026-07-28 › 15. Failure chronology and diagnostic lesson
| Visible symptom | Actual cause | Corrective change | … | ChatGPT said OAuth unsupported | Missing RFC 9728 path metadata | …
…
```

For twenty passages the prompt was 15,905 characters. The reply is parsed defensively. The mock returned five entries, two of them faulty, and the engine handled each:

| Mock entry | Engine's treatment |
| --- | --- |
| `p3`, 0.94, "explains the stateless discovery path" | kept, rank 1 |
| `p1`, 0.81, "names the no-actions symptom" | kept, rank 2 |
| `p3` again, 0.5 | dropped: already ranked |
| `p99`, 0.4 | dropped: no such passage |
| `p7`, 0.42, "background on tool scopes" | kept, rank 3 |

The output keeps only passages Claude ranked, cut to `keep`, which defaults to the `k` signal. Two consequences follow. The reranker can shrink the result list: `keep` was 8 but the reply named 3 passages, so the user saw 3 results. And a passage Claude omits is gone even if BM25 liked it, which is the intent of *including only passages that help* but also the risk. If the reply contains no usable entry, the node falls back to the lexical order, so a malformed reply degrades the result instead of emptying it. Each kept hit carries Claude's `reason`, which the results list shows beside the title.

### 16.6 Why steps that call Claude wait for Enter

A user typing a twelve-word question produces a dozen `change` runs. If each run called Claude twice, one question would cost two dozen calls and each keystroke would wait on a model. The library therefore gives every component that calls Claude the trigger `submit`, and gives each a skip rule that says what flows downstream when it does not run:

| Node | Trigger | When skipped |
| --- | --- | --- |
| `rewrite.llm` | submit | passes its input queries through |
| `rerank.llm` | submit | passes its input hits through |
| `answer` | submit | outputs `null` |
| `agentic` | submit | outputs `null` |
| HyDE rewrite lambda | submit (set in the preset) | passes its input queries through |
| `rewrite`, `rerank`, `filter` lambdas | change | run on every keystroke |

The *Rewrite + rerank* preset shows both kinds of run:

```text
submit  rewrite:ran 1→4 calls 1 | text:ran 4→40 | filter:ran 40→40 | rerank:ran 40→3 calls 1
change  rewrite:skipped 1→1     | text:ran 1→40 | filter:ran 40→40 | rerank:skipped 40→40
```

While the user types, the page shows lexical results for the question as typed. On Enter, the same plan adds the rewrites and the reranking. The design keeps the page responsive and makes the cost of a search a decision the user takes, one press at a time. Chapter 12 describes the session rules behind it, and chapter 27 how a page should show which actions spend calls.

The key points:

- A rewrite step changes the queries and leaves the index alone; with `perQuery`, each phrasing is ranked on its own and the original question always keeps its own ranking.
- Index-time synonyms add terms at half weight inside one query; rewrites add whole queries at full weight. They answer different needs.
- HyDE is a rewrite that searches with a drafted answer; because long queries score higher, the draft dominates a per-query retriever unless it is fused by rank.
- A rerank lambda can score anything the hit view exposes, explain itself with `reason`, and costs nothing per keystroke.
- `rerank.llm` reads clipped passages, ignores malformed entries, keeps only what Claude ranked, and falls back to the lexical order when the reply is unusable.
- Every step that calls Claude runs on Enter and passes its input through when skipped, so typing never waits on a model.

Rewriting and reranking keep Claude at the edges of a fixed pipeline. The next chapter puts Claude in charge of the retrieval itself.

**Lab: Query expansion; Rerank with a lambda; Your own components; Rewrite + rerank; HyDE.** In *Query expansion*, try *what is the knee rule for LCA* and *why did DCR fail*, then compare the `no-rewrite` and `no-synonyms` variants in the Evals tab. In *Rerank with a lambda*, set `heading` to 3 and watch which section enters the top five. In *Your own components*, pick a tag in the Boost tag menu. In *Rewrite + rerank*, type a question, then press Enter and open the Trace tab: the rewrite and rerank rows change from skipped to ran, each with one call. In *HyDE*, compare the results with the toggle on and off.

## 17. Claude as the retriever

Every pipeline so far has had a fixed shape: the same nodes run in the same order for every question, and Claude, where it appears, fills one slot. This chapter covers the two ways the workbench gives Claude a larger role. The `agentic` node hands Claude a set of tools over the indexes and lets it choose what to search and what to read. Build-time enrichment uses Claude once per document, ahead of any question, to write summaries and likely questions that a lexical index can then match. The chapter ends by comparing an agent with a fixed pipeline on the same golden questions.

The tool definitions, prompts, budgets and error messages below are real. The agent runs were driven by a scripted policy in place of Claude: a mock sampler that calls the tools in a fixed sequence. Those runs show what the node records and enforces. They do not show what Claude would choose to do.

### 17.1 Four tools: search, read, outline, documents

The *Agentic answer* preset is a one-node plan:

```js
rag.plan('agentic').over(corpus)
  .signals({ q: 'Across all projects, which bugs came from something silently being empty or nil?', k: 8 })
  .agentic({ maxReads: 8, indexes: ['text', 'rows'], tier: 'default' });
```

The node's input is the query list and its output is an answer, the same type `answer` produces, so the answer panel, the citation links and the evals work unchanged. Its question is chosen to defeat a fixed pipeline: the answer is scattered across failure tables in several reports, under headings that rarely use the words *empty* or *nil*.

The node gives Claude four tools. These are their descriptions as Claude receives them:

| Tool | Description |
| --- | --- |
| `search` | Search the report corpus with a short keyword query. Returns up to 8 sections as {id, doc, heading, snippet}. Indexes: text, rows. Search several phrasings. |
| `read` | Read one section by id. Returns its full text and the evidence label to cite, like \[E2\]. |
| `outline` | List a document's sections as {id, heading}. Pass a section id or a document id. |
| `documents` | List all documents as {id, title, type, tags}. |

The split between `search` and `read` is the important design choice. Search results are cheap: an id, the document title, the heading path and a 220-character snippet. Reading costs more context: the full section, clipped at 6,000 characters. Claude can therefore scan many candidates and pay for the few it needs, which is the same retrieve-then-pack structure as a fixed pipeline, with the model deciding both steps. A real search result looks like this:

```text
{ "id": "project-report-coinvault-mcp-…#row-browser-ecs-never-healthy",
  "doc": "PROJECT REPORT - CoinVault MCP - From Local Conformance to ChatGPT 2026-07-28",
  "heading": "15. Failure chronology and diagnostic lesson › Browser ECS never healthy",
  "snippet": "Visible symptom: Browser ECS never healthy Actual cause: Typed nil verifier activated RAG branch Corrective change: Return a genuinely nil verifier interface" }
```

`outline` and `documents` let Claude move through the corpus by structure rather than by words: list the reports, then a report's sections, then read one. The prompt states the method and the citation format:

```text
You answer questions about a corpus of engineering project reports using the tools. Search with several phrasings, read the sections you rely on, then answer. Cite read sections inline as [E1], [E2] using the labels read() returns. Stop searching as soon as you can answer. Final answer: plain markdown, under 250 words.

Question: Across all projects, which bugs came from something silently being empty or nil?
```

### 17.2 A read budget, and evidence labels assigned on read

An agent with unlimited reads can fill its context with marginal sections, take many turns, and cost more than the answer is worth. The node enforces `maxReads` (8 by default) inside the `read` tool. When the budget is spent, a read of a new section returns an error that tells Claude what to do next:

```text
read budget of 8 sections used; answer now from what you read
```

Re-reading a section already read does not count, and returns the same label. A read of an id that does not exist returns:

```text
no section no-such-id; use an id returned by search
```

Both messages are written for the model, not for a person. A tool error is part of the conversation the model sees, so the error should say how to recover.

Evidence labels are assigned when a section is first read, in the order of reading: the first section read is `[E1]`. The node collects the read sections into the same `evidence` table that `pack` builds (chapter 15), so a citation from the agent links to a section exactly as a citation from `answer` does. The difference is who chose the evidence: in a fixed pipeline the ranking chose it; here Claude did, one read at a time.

### 17.3 Rounds in the trace: what was searched and what was read

An agentic run is one node in the trace, so the trace alone would hide everything the agent did. The node therefore records each tool call in two places. `answer.rounds` lists every call with its arguments; the trace entry's `notes` list the searches and reads in a compact form. This is the scripted run (the sequence of calls is the script's, not Claude's):

```text
agentic  ran  in 1  out 8  calls 1
  search("silently empty nil") → 8
  search("typed nil interface") → 8
  search("empty result no error") → 8
  read(…coinvault…#row-browser-ecs-never-healthy) → [E1]
  read(…ttc…order-grain…#row-datatable-polling-an-empty-response) → [E2]
  read(…ttc…order-grain…#row-empty-orders-tab-on-load) → [E3]
  read(…coinvault…#4-1-the-typed-nil-deployment-failure) → [E4]
  …
  read(…stabilization…#row-known-optional-binding-left-empty) → [E8]

rounds: documents · search × 3 · read × 7 · outline · read
```

Two details of the record are worth knowing. The notes cover `search` and `read` only; `outline` and `documents` appear in `rounds` but not in the trace notes. And the trace's `calls` column counts invocations of the sampler, so a whole agentic run counts as 1 however many model turns the tool loop takes inside that invocation. The column is therefore a lower bound on usage for this node; the number of rounds is a better measure of how hard the agent worked.

On a `change` run the node is skipped and outputs `null`, like `answer`: the page does nothing while the user types.

### 17.4 Routing by build-time enrichment: summaries and questions per document

The agent spends Claude's effort per question. Enrichment spends it per document, once, at build time. The *Doc cards* preset makes one card per report from its title and second-level headings, asks Claude to enrich each card, and indexes the result:

```js
kit.reports()
  .chunk('cards', (doc, ctx) => [{
    kind: 'card', headingPath: ['Document card'],
    text: [doc.title, ...doc.sections.filter(s => s.level === 2).map(s => s.title)].join('\n'),
  }])
  .enrich('llm', rag.enrich.llm({ tier: 'quick' }), { from: 'cards' })
  .index('cards', rag.bm25({ fields: { title: 2, text: 1, 'llm.summary': 2, 'llm.questions': 3, 'llm.keywords': 3 } }), { from: 'enrich_llm' });
```

This is the prompt each card is sent with, and the start of a real card:

```text
For this section of an engineering report, reply with only JSON: {"summary": string (one sentence), "questions": [3-5 questions this section answers], "keywords": [5-8 search keywords, including synonyms not in the text]}.

Document: TTC Customer Clustering — Order-Grain Segmentation and the Shiny Explorer
Section: Document card

TTC Customer Clustering — Order-Grain Segmentation and the Shiny Explorer
Why this project exists
Project shape
The data model
…
```

The questions and keywords are the point. A user asks *which report explains why a criterion keeps adding clusters*; the report says *BIC kept improving as components were added*. A keyword list written with synonyms not in the text, weighted 3 in the card index, can carry *criterion* and *clusters* to the right card.

The plan routes with a retrieve lambda: find the best three cards, then search inside those three documents, four sections each, and fuse with plain retrieval:

```js
.retrieve((s, ctx, queries, p) => {
  const q = queries.join(' ');
  const docs = ctx.index('cards').search(q, p.docs).map(h => ctx.chunk(h.id).docId);
  return docs.flatMap(d => ctx.index('text').searchIn(d, q, p.perDoc));
}, { id: 'routed', docs: 3, perDoc: 4 })
.fuse.rrf({ weights: { text: 1, routed: 1.2 } })
```

The enrichment costs eleven quick calls, one per report. The workbench defers it to an explicit Build (chapter 11), so before that press the cards hold only titles and headings. That state is instructive, because it shows routing without enrichment:

```text
build   enrich_llm: computed, deferred 11, calls 0      pending: [{ node: "enrich_llm", items: 11 }]

cards for "Which report explains why a criterion keeps adding clusters?"
2.8841  CoinVault card        why { report: 2.88 }
2.2654  Tier B card           why { cluster: 2.27 }
2.1362  Broken Exports card   why { cluster: 2.14 }

routed (in output order)
1. 4.1578   CoinVault › 19. Engineering rules worth preserving
2. 2.5095   CoinVault › (introduction)
…
5. 12.5382  Tier B › What was learned
6. 12.2974  Tier B › The mixture models, and a lesson about BIC

fused
1. 0.03459  { text: 2, routed: 5 }   Tier B › What was learned
…
8. 0.03078  { text: 30, routed: 1 }  CoinVault › 19. Engineering rules worth preserving
```

Two things went wrong, and both are instructive. The bare cards routed first to CoinVault because its title begins *PROJECT REPORT* and the question contains *report*; a card with headings only has too few words to outvote one accidental match. And the lambda returns its hits grouped by document without sorting, so RRF reads the CoinVault sections as ranks 1 to 4 and the Tier B sections, scoring three times higher, as ranks 5 to 8. The CoinVault section, rank 30 of 40 in plain retrieval, entered the fused top eight on the strength of that position. A routing lambda should sort its output by score, or interleave documents, before fusion reads its ranks. Enrichment would address the first problem, because a summary and keywords give the card index real content to match; the second is a bug in the lambda that enrichment cannot fix.

There is also a known engine bug on this path. After a deferred build, pressing Build runs the eleven enrichment calls, but the downstream `ix-cards` index is reused rather than rebuilt: its digest depends on the enrichment node's parameters, which did not change, not on the enriched output. In a node test, a search for a keyword that only the mock enrichment produced found 0 cards after Build, and 3 cards after a fresh build on a new engine. Until the engine is fixed, reloading the page after Build (which rebuilds from the per-item cache without new calls) makes the enriched cards searchable; in the same test, a fresh engine sharing the cache rebuilt the index with 11 cached items, 0 calls, and found all 3 cards.

### 17.5 Fixed pipeline or agent: comparing both on the same goldens

Evals score a plan by the ranked list it returns (chapter 18). An agentic plan returns an answer, not a list, so evals fall back to the answer's evidence in reading order. That makes an agent and a fixed pipeline comparable with the same cases and metrics. These are real numbers for a lexical pipeline (`retrieve('text', { k: 40 }).limit(s => s.k)`) and for the agentic plan driven by a simple scripted policy that searches the question and reads the top five results:

| Plan | hit@5 | MRR | nDCG@10 | sampler calls |
| --- | --- | --- | --- | --- |
| fixed lexical | 1 | 0.933 | 0.847 | 0 |
| agentic, scripted policy (search once, read 5) | 1 | 0.933 | 0.653 | 12 |

The scripted policy finds the same first relevant section as the lexical pipeline, because it is the lexical pipeline with extra steps. Its nDCG is lower because five reads make a shorter list than ten results, and nDCG@10 gives credit for relevant sections further down. The comparison shows the method, not a verdict on agents: an agent driven by Claude will search different phrasings, read different sections, and score differently from run to run. Those differences are the point of running the comparison.

The broader trade-off is between two kinds of system:

|  | Fixed pipeline | Agentic node |
| --- | --- | --- |
| What decides the evidence | a ranking you can read in the trace | Claude, one search and read at a time |
| Same question, same result | yes | not guaranteed |
| Cost per question | 0 to 3 calls, known in advance | 1 invocation with an unknown number of turns |
| Latency | milliseconds until the answer call | several model turns |
| Questions spread across many reports | limited by fusion and packing | can follow leads across reports |
| How to debug a miss | the trace shows which stage dropped it | `rounds` shows what was searched and read |

A reasonable practice is to build the fixed pipeline first, measure it, and use an agent for the questions the goldens show it cannot answer, keeping both as variants scored on the same cases.

The key points:

- The agentic node gives Claude search, read, outline and documents tools over the indexes; cheap searches and expensive reads mirror the retrieve-then-pack structure of a fixed pipeline.
- A read budget is enforced inside the tool, and tool errors are written to tell the model how to recover.
- Evidence labels are assigned in reading order, so an agent's citations link to sections exactly as a fixed pipeline's do.
- `rounds` and the trace notes record what the agent did; the trace's call count is one per sampler invocation, not per model turn.
- Build-time enrichment spends Claude once per document so that a lexical index can match questions the text does not phrase; a routing lambda must still sort its output before fusion reads its ranks.
- After a deferred enrichment build, the downstream index is currently reused instead of rebuilt; reloading the page rebuilds it from the cache.
- Evals compare agents and fixed pipelines on the same cases by scoring the agent's evidence in reading order.

This chapter ends the tour of the live pipeline. Part VI turns to the question every chapter so far has raised: when a change moves a result, how do you know it was an improvement?

**Lab: Agentic answer; Doc cards.** In *Agentic answer*, press Enter, then read the *Tool calls* card: each line is a search or a read, and the labels in the answer match the reads. Lower `maxReads` to 3 in the DAG tab and ask again. In *Doc cards*, ask the default question before pressing Build and look at the `routed` row in the Trace tab; then press Build (eleven quick calls), reload the page, and ask again. Open the *Cards* tab to read the summaries Claude wrote.
