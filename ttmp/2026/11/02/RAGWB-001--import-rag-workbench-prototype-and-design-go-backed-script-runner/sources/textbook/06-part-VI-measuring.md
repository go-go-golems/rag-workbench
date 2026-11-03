# Part VI · Measuring

A change to a pipeline is an improvement only if it moves a number on questions chosen before the change was made. This part defines those questions, the numbers, and how pipelines are compared with them.

## 18. Golden questions

This chapter defines the unit of measurement for everything that follows: the golden case. A golden case is a question together with a rule that decides, for any chunk the pipeline returns, whether that chunk answers the question. By the end of the chapter you should be able to read a case, predict which chunks it will accept, write new cases that measure what users actually need, and recognise when a case has stopped measuring the pipeline and started measuring itself.

The reason for starting here is simple. Parts II to V describe dozens of choices: chunk size, field weights, fusion constants, rerankers, packing budgets. Every one of them changes the ranking, and most changes help some questions and hurt others. Without a fixed set of questions chosen before the change, "better" means "better on the query I happened to try last", and the pipeline drifts toward whatever the author tested by hand. Golden cases replace that habit with a number that is computed the same way every time.

### 18.1 A case: question, expected document, expected section, expected answer text

An eval set is built with the same immutable-builder pattern as corpora and plans. `rag.evals(id)` returns an empty set; `.case(question, expect)` returns a new set with one more case; `.metrics([...])` names the numbers to compute. The workbench's twelve hand-written cases live in `kit.js` as rows of question, document pattern, section pattern and, optionally, answer pattern:

```js
const CASES = [
  ['Why did ChatGPT show no actions after OAuth worked?', 'CoinVault', /no actions|stateless/i],
  ['What rule picked eight latent classes?', /Tier B|Broken Exports/, /latent class|model selection/i, /knee|ten percent|10%/i],
  ['How are arrays of objects flattened into metadata terms?', 'SQLite-Backed Forum', /metadata/i],
  // … nine more
];

let ev = rag.evals('goldens');
for (const [q, doc, section, answerHas] of CASES)
  ev = ev.case(q, { doc, section, ...(answerHas ? { answerHas } : {}) });
ev = ev.metrics(['hit@5', 'mrr', 'ndcg@10']);
```

Serialised with `ev.toJSON()`, the first two cases read:

```json
{ "q": "Why did ChatGPT show no actions after OAuth worked?",
  "expect": { "doc": "CoinVault", "section": "/no actions|stateless/i" } }
{ "q": "What rule picked eight latent classes?",
  "expect": { "doc": "/Tier B|Broken Exports/",
              "section": "/latent class|model selection/i",
              "answerHas": "/knee|ten percent|10%/i" } }
```

The `expect` object is not a list of correct chunk ids. It is a predicate, and the engine applies it to chunks with a function called `relevant`. Its rules are short enough to state completely:

- **The document pattern** is tested against the chunk's document title, the document's file name, its document id and every alias in its frontmatter. A match on any of them counts.
- **The section pattern** is tested against the chunk's heading path joined with `›`, for example `5. The eval tool › 5.2 Execution`. The flag `any: true` skips this test, so the case accepts any chunk from the right document.
- **A string pattern** matches as a case-insensitive substring. A regular expression is applied as written. An array of patterns matches if any member does.
- **A chunk is relevant** when both the document test and the section test pass.

Two numbers fall out of this predicate for every case. The **rank** is the 1-based position of the first relevant hit in the pipeline's result list, or `null` when no relevant hit appears. **R** is the number of chunks in the whole corpus that the predicate accepts, whether or not the pipeline returned them. Rank drives hit@k and MRR; R is the denominator of recall and fixes the ideal ranking that nDCG compares against. Chapter 19 defines those metrics; here it is enough to know that every case report carries both numbers.

The optional `answerHas` pattern is different in kind. It is tested against the text of the generated answer, not against chunks, and it is only scored when the run produced an answer. Section 18.4 explains when that happens and chapter 20 uses it.

### 18.2 Patterns instead of ids: why cases survive rechunking

A chunk id in this corpus looks like `project-report-coinvault-mcp-from-local-conformance-to-chatgpt-2#11-why-chatgpt-still-showed-no-actions`. It is built from the document id and the slug of the section heading, and a section split by the token budget gets a numeric suffix per piece. Change `maxTokens` from 450 to 200 and many sections split into two or three pieces with new ids. A case that said "the answer is chunk X" would be wrong after every chunking experiment, which is exactly the experiment Part II told you to run.

A pattern on headings survives that change. When the Execution section splits in two, both pieces carry the heading path `5. The eval tool › 5.2 Execution`, and both satisfy `/eval tool|execution/i`. The case keeps working, and R grows to count the extra pieces.

The protection is real but it has a limit, and the limit shows up in a real run. The Chunk sizes preset compares four chunking variants over the twelve goldens. Here are R and rank per case for the 450-token default and for the `h2-only` variant, which chunks at heading depth 2 instead of 3:

| Case | 450 tokens, depth 3 | h2-only |
| --- | --- | --- |
| 3 · arrays of objects flattened into metadata | R 2, rank 1 | **R 0, rank —** |
| 7 · ECS service restart loop | R 1, rank 1 | **R 0, rank —** |
| 8 · inbox cursor advances | R 2, rank 1 | **R 0, rank —** |
| 11 · the rebalance law | R 1, rank 1 | **R 0, rank —** |
| 9 · empty orders tab | R 3, rank 1 | R 1, rank 4 |

Four cases have R = 0 under h2-only. No chunk in the corpus satisfies them, so no pipeline could score on them. The reason is visible in the heading paths. At depth 2, the SQLite forum report's subsections are folded into their parents, and its chunks carry paths like `Implementation analysis` and `The data model`; the word "metadata" that case 3 looks for appeared only in a depth-3 heading that no longer exists as a path. The answer text is still in the corpus, inside a larger chunk, but the case's section pattern can no longer see it.

This is the most common misunderstanding about pattern-based cases: a low score is read as a retrieval failure when it is a case failure. The defence is to read R. A case with R = 0 under some pipeline is not measuring that pipeline, and its zero should be excluded from any comparison, or the pattern should be widened (for example to `{ doc: 'SQLite-Backed Forum', any: true }`) if document-level retrieval is what matters. The report includes R for every case precisely so this check takes one glance.

### 18.3 Writing cases from the questions users actually ask

The twelve goldens were written as questions a colleague would type, not as headings with a question mark added. Compare the question "What rule picked eight latent classes?" with the section that answers it, "What model selection actually established". They share almost no words. A case like that tests whether the pipeline can bridge vocabulary, which is the hard part of search. A case like "What does the rebalance law say?", whose answer sits under the heading `5.9 The rebalance law`, tests something easier: whether an exact phrase in a heading wins. Both kinds belong in a set, and the set should say which is which, because a pipeline that is perfect on heading echoes can still fail every real question.

Four rules make cases useful:

- **Phrase the question as a user would.** Use the user's words, including vague ones. If users say "the orders tab was empty", the case says that, even though the report calls it a failure in the architecture section.
- **Make the section pattern as wide as the set of sections that genuinely answer the question, and no wider.** Case 1 accepts `/no actions|stateless/i` because both section 11 ("Why ChatGPT still showed no actions") and section 12 ("Stateful versus stateless Streamable HTTP") answer it. A pattern that also accepted the whole OAuth chapter would reward the pipeline for returning a section that only mentions the topic.
- **Cover the corpus.** The twelve cases reach nine of the eleven reports; the Datalab and Stabilization reports have no case, so a change that breaks retrieval there would go unnoticed. A set that only asks about one report measures how that report was written.
- **Add an answer pattern only where a fact can be checked by text.** Case 2's answer must mention the knee rule or the ten-percent criterion. Most "why" questions have no such fact, and forcing one produces a pattern that tests phrasing.

For designers, this is the place where the product is specified. The list of golden questions is the list of tasks the search page promises to support. Writing it is a design act: it decides whose questions matter, in whose words, and what counts as an answer. A page whose layout is beautiful but whose goldens are heading echoes has not been designed for its users; it has been designed for its author.

### 18.4 Which run a case exercises

Chapter 12 introduced triggers: `change` nodes run on every keystroke, `submit` nodes wait for Enter. An eval run chooses a trigger too, and the choice decides which part of the pipeline is measured and what the run costs.

`ev.run(target)` defaults to `trigger: 'change'`. In that mode, nodes marked `submit` (`rewrite.llm`, `rerank.llm`, `answer`, `agentic`) are skipped exactly as they are while a user types, so the run measures the lexical pipeline and makes no Claude calls. With `trigger: 'submit'`, every node runs, the answer is generated, `answerHas` can be scored, and judges can run if `judge: true` is passed. The Evals tab maps these to its two buttons: **Run 12 cases** is a `change` run, and **Run with Claude** is a `submit` run with judges.

The Judged evals preset shows the difference on the same thirteen cases (the twelve goldens plus one with an answer pattern). The Claude calls in the second run were served by a mock sampler in node, so the call count is real but the judge scores are not:

| Run | Nodes measured | Claude calls | Per case |
| --- | --- | --- | --- |
| `change` | retrieve | 0 | 0 |
| `submit`, `judge: true` | retrieve, rerank.llm, pack, answer, 2 judges | 52 | 4 |

Two details of the run matter when reading results. First, the engine asks for at least ten hits per case: the `k` signal is set to the largest of 10, the largest `@k` in the metric list, and the plan's own `k`. A metric can never be starved by a plan that returns five results. Second, when a run produces no hits list but does produce an answer with evidence, as an agentic node does, the evidence list stands in for the hits. That is how chapter 17's agent can be scored on the same cases as a fixed pipeline.

### 18.5 Synthetic cases: questions Claude writes for sampled sections

Twelve cases are few. `rag.evals.generate(target, { n, seed })` produces more by asking Claude to write one question per sampled section. It works in four steps:

1. Build the target if needed, then collect every chunk that is a heading section with more than 120 tokens and a non-empty heading path.
2. Pick `n` of them (default 12) with a seeded linear congruential generator, so the same seed picks the same sections.
3. Send them to Claude in batches of eight, one quick-tier call per batch, asking for "one question a colleague might ask that this section answers well", phrased without copying the heading.
4. Turn each question into a case whose document pattern is the chunk's title and whose section pattern is its full heading path, flagged `synthetic: true`.

Run in node with seed 7 and a mock sampler (so the question text below is a placeholder), eight sections cost exactly one call, and the generated cases look like this:

```json
{ "q": "MOCK question 3",
  "expect": { "doc": "PBUI Workbench Core: A Headless Engine, a Pure Planner, and the Hard Cutover of the React Shell",
              "section": "5. The core's modules › 5.9 The rebalance law" },
  "synthetic": true }
```

Synthetic cases are cheap and plentiful, and they have two biases you should name when you report them. Because Claude writes the question while reading the section, the question tends to reuse the section's vocabulary, which favours lexical retrieval and flatters BM25. And because the section pattern is that one heading path, a different section that answers the question equally well counts as a miss. Both biases push in known directions, which is why the Synthetic evals preset scores generated cases in their own table and never mixes them into the goldens. Use them to find sections nobody can reach, and use the goldens to decide whether a change shipped.

### Key points

- A golden case is a question plus a predicate over chunks; relevance is decided by matching document and heading-path patterns, not by stored ids.
- Every case report carries a rank (first relevant position) and R (relevant chunks in the corpus); read R before reading a score.
- A case with R = 0 under some pipeline measures the case, not the pipeline; the h2-only variant makes four goldens unanswerable by construction.
- Lexical eval runs use the `change` trigger and cost nothing; `submit` runs exercise Claude-backed nodes and judges and cost about four calls per case in the Judged evals preset.
- Synthetic cases are useful for coverage but are biased toward the section's own vocabulary and its exact heading, so they are kept separate from the goldens.

With cases in hand, the next question is how to turn a list of ranks into a number. Chapter 19 defines the four metrics the engine computes and shows what each one rewards.

**Lab · Reports · hybrid + rerank + answer, Synthetic evals.** Load the Reports preset, open the Evals tab and press **Run 12 cases**. Every row shows a rank; click a miss, if any, to run its question in the search sheet and see what came first instead. Then load Chunk sizes, tick `h2-only` and run again: find the rows whose rank is "—" and confirm in the Corpus tab that no chunk carries the heading the case expects. Finally, load Synthetic evals, generate eight questions, and read which sections the generated questions failed to reach.

## 19. Ranking metrics

A golden case produces a ranked list and a predicate that marks each entry relevant or not. A metric turns that pattern of marks into one number, and the mean over cases into one number for the pipeline. This chapter defines the four metrics the engine computes (hit@k, MRR, nDCG@k and recall@k), shows the eleven lines of code that implement them, works all four by hand for one real case, and ends with the question every reader of an eval table must ask: how large does a difference have to be before it means anything when there are only twelve cases?

The metrics are not interchangeable. Each one encodes an assumption about how far down the list a reader looks and whether a second relevant result is worth anything. Choosing a metric is choosing that assumption, so the definitions below are stated together with the reader they model.

All numbers in this chapter come from one run: the Minimal search pipeline (one BM25 index, one retriever) over the twelve goldens, with five metrics requested.

```js
const ev = kit.goldens().metrics(['hit@1', 'hit@5', 'mrr', 'ndcg@10', 'recall@10']);
const report = await ev.run(plan);
console.table(report.table());
```

| # | Question | rank | hit@1 | hit@5 | mrr | ndcg@10 | recall@10 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | Why did ChatGPT show no actions after OAuth worked? | 1 | 1 | 1 | 1 | 0.61 | 0.5 |
| 2 | What rule picked eight latent classes? | 1 | 1 | 1 | 1 | 1 | 1 |
| 3 | How are arrays of objects flattened into metadata terms? | 1 | 1 | 1 | 1 | 0.83 | 1 |
| 4 | Why is a content hash not enough for an operation id? | 1 | 1 | 1 | 1 | 1 | 1 |
| 5 | What happens to product flags when a product is deleted? | 1 | 1 | 1 | 1 | 0.77 | 0.67 |
| 6 | How does the eval tool report line numbers in errors? | 5 | 0 | 1 | 0.2 | 0.4 | 0.75 |
| 7 | Why did the browser ECS service restart in a loop …? | 1 | 1 | 1 | 1 | 1 | 1 |
| 8 | Why does the inbox cursor advance past events …? | 1 | 1 | 1 | 1 | 0.88 | 1 |
| 9 | Why was the orders tab empty when the explorer loaded? | 1 | 1 | 1 | 1 | 0.97 | 1 |
| 10 | How does protojson serialize int64 values? | 1 | 1 | 1 | 1 | 0.77 | 0.67 |
| 11 | What does the rebalance law say? | 1 | 1 | 1 | 1 | 1 | 1 |
| 12 | How are generative UI lambdas stored so they survive a reload? | 1 | 1 | 1 | 1 | 0.94 | 0.75 |

The summary is the mean of each column: hit@1 0.917, hit@5 1, mrr 0.933, ndcg@10 0.847, recall@10 0.861, with a median of 0.4 ms per case and zero Claude calls. The engine also records `p50ms` and `calls` in every summary, so cost sits beside quality in the same row.

In every formula below, `rel` is the list of relevance marks for the returned hits in order, `rank` is the position of the first `true` (or none), `R` is the number of relevant chunks in the corpus (chapter 18), and `k` is the cutoff named after the `@`. This is the engine's code, lightly reformatted:

```js
if (name === 'hit')    m = rank && rank <= k ? 1 : 0;
else if (name === 'mrr')    m = rank ? 1 / rank : 0;
else if (name === 'recall') m = R ? rel.slice(0, k).filter(Boolean).length / R : 0;
else if (name === 'ndcg') {
  const dcg  = rel.slice(0, k).reduce((a, r, j) => a + (r ? 1 / Math.log2(j + 2) : 0), 0);
  const idcg = Array.from({ length: Math.min(R, k) }, (_, j) => 1 / Math.log2(j + 2))
                    .reduce((a, b) => a + b, 0);
  m = idcg ? dcg / idcg : 0;
}
```

### 19.1 hit@k: whether a relevant section was on the first screen

hit@k is 1 when the first relevant chunk is at position k or better, and 0 otherwise. It models a reader who scans the first k results and is satisfied by any one relevant section. The model fits a results page well: if the page shows five results above the fold, hit@5 says whether the reader could find the answer without scrolling. Designers can choose k directly from the layout.

The weakness of hit@k is that it saturates. Minimal search scores hit@5 = 1 on all twelve cases. From that point on, no change to the pipeline can raise hit@5, and a change that moves case 6 from rank 5 to rank 1 is invisible. A saturated metric is still useful as a floor, since a drop below 1 is news, but it cannot rank two pipelines that both clear it. The usual response is to tighten the cutoff. hit@1 is not saturated here (0.917), because case 6 puts its first relevant chunk fifth.

### 19.2 MRR: how far down the first relevant section was

The reciprocal rank of a case is 1 / rank, or 0 when nothing relevant was returned; MRR is its mean. It models a reader who reads down the list until the first relevant result and stops, and it charges for every position they had to read past. The charge is steep at the top and flat lower down: rank 1 scores 1, rank 2 scores 0.5, rank 5 scores 0.2, rank 10 scores 0.1.

For the run above, eleven cases score 1 and case 6 scores 0.2, so MRR = (11 × 1 + 0.2) / 12 = 11.2 / 12 = 0.933, which is the engine's summary. MRR ignores everything after the first relevant hit. A pipeline that returns one relevant section at rank 1 and nine irrelevant ones scores the same as one that returns ten relevant sections. For a question with a single right section, which most of the goldens have, that is the correct behaviour. For a question whose answer is spread over several sections, it is not, and the next two metrics cover that case.

### 19.3 nDCG@k: credit discounted by position and normalised by the best possible list

Discounted cumulative gain adds a credit for every relevant hit in the top k, divided by log₂(position + 1). Position 1 earns 1, position 2 earns 0.631, position 3 earns 0.5, position 10 earns 0.289. The discount falls slowly, so nDCG rewards a list for putting several relevant sections high, not only the first. Normalised DCG divides by the DCG of the best possible list: all `min(R, k)` relevant chunks at the top. A list that cannot be improved scores 1.

In this engine relevance is binary, so every relevant hit has gain 1. nDCG is often defined with graded gains (highly relevant 3, partly relevant 1); the golden predicate has no grades, so nDCG here measures how many relevant sections came back and how high.

Case 1 shows what nDCG sees that MRR does not. Its pattern accepts two sections, "11. Why ChatGPT still showed no actions" and "12. Stateful versus stateless Streamable HTTP", so R = 2. The pipeline returns section 11 first and section 12 nowhere in the top ten. MRR is 1. nDCG is 1 / (1 + 0.631) = 0.613, because the ideal list had a second relevant section at position 2. The engine reports 0.6131471927654584.

### 19.4 recall@k: how many of the relevant sections were found

recall@k is the fraction of the R relevant chunks that appear in the top k, with no regard to order. It models a consumer that reads all k results, which is what the `pack` node does when it fills a context for Claude. For an answer pipeline, recall@k at the packing depth is the metric closest to "did Claude get to see the evidence".

recall has a dependency that the others lack: its denominator is R, and R depends on chunking. The Chunk sizes preset's 200-token variant splits sections into more pieces, and R grows with it; for case 12, R is 12 at 450 tokens and 20 at 200 tokens. With R = 20, recall@10 cannot exceed 0.5 even for a perfect list. Comparing recall across chunking variants therefore compares denominators as much as pipelines. Compare recall only between pipelines that share a chunker, or read it per case beside R.

### 19.5 All four by hand for one case

Case 6, "How does the eval tool report line numbers in errors?", is the one case where the metrics disagree, which makes it the best one to compute by hand. Its pattern is `{ doc: 'Agent runtime spec', section: /eval tool|execution/i }`, and four chunks in the corpus satisfy it: 5.1 Definition, the two pieces of 5.2 Execution (ids `…#5-2-execution` and `…#5-2-execution-2`), and 5.3 What good eval code looks like. So R = 4. Here is the top ten that Minimal search returned, all from the agent runtime spec:

| Position | Score | Section | Relevant |
| --- | --- | --- | --- |
| 1 | 12.4732 | 21. Generative UI: gui › 21.6 Canvas |  |
| 2 | 12.2952 | 7. State introspection: bench |  |
| 3 | 11.2416 | 12. Transcripts API |  |
| 4 | 11.1063 | 18. Annotations › 18.1 API |  |
| 5 | 10.9849 | 5. The eval tool › 5.2 Execution | yes |
| 6 | 10.9130 | 5. The eval tool › 5.2 Execution (first piece; position 5 is the second) | yes |
| 7 | 10.5125 | 9. Chat data model and rendering › 9.2 Rendering rules |  |
| 8 | 10.4692 | 9. Chat data model and rendering › 9.1 Data model |  |
| 9 | 10.1883 | 25. Testing without a live model › 25.1 Stand-ins |  |
| 10 | 10.1657 | 5. The eval tool › 5.1 Definition | yes |

The four metrics follow directly:

- **hit@5.** The first relevant chunk is at position 5, and 5 ≤ 5, so hit@5 = 1. hit@1 = 0.
- **MRR.** 1 / 5 = 0.2.
- **recall@10.** Three of the four relevant chunks are in the top ten, so 3 / 4 = 0.75. The fourth, 5.3, is not.
- **nDCG@10.** DCG = 1/log₂ 6 + 1/log₂ 7 + 1/log₂ 11 = 0.38685 + 0.35621 + 0.28906 = 1.03212. The ideal list puts min(4, 10) = 4 relevant chunks first: IDCG = 1 + 0.63093 + 0.5 + 0.43068 = 2.56161. nDCG = 1.03212 / 2.56161 = 0.40292.

The engine reports `{"hit@5":1,"mrr":0.2,"ndcg@10":0.4029209390875161,"recall@10":0.75}` for this case, which agrees with the hand computation to every printed digit.

The trace explains the ranking once the per-term contributions (`why`, chapter 13) are visible. Every section of this document mentions eval and tool, so those terms barely separate them. The sections above Execution win on the stem `number`: it contributes 4.42 to "18.1 API" and 4.12 to "12. Transcripts API", against 2.21 for the second Execution piece, whose own strengths are `eval` 3.31 and `line` 2.88. The section that answers the question wins on no single word by a wide margin, so it lands at 10.98, about one and a half points behind a section on canvas drawing. This is the "misranked" failure from chapter 1, and the metrics locate it: hit@5 says the answer was on screen, MRR says it was fifth, and nDCG and recall say two more answering sections were close behind.

### 19.6 Twelve cases, and how large a difference has to be before it means anything

Every summary number is a mean over twelve cases, and that fixes the smallest possible change in each metric. The resolution is worth working out once:

| Event in one case | Change in the mean |
| --- | --- |
| hit@k flips between 0 and 1 | 1/12 = 0.083 |
| first relevant moves from rank 1 to rank 2 | MRR − 0.5/12 = −0.042 |
| first relevant moves from rank 5 to rank 4 | MRR + 0.05/12 = +0.004 |
| one more relevant chunk enters the top 10, R = 4 | recall@10 + 0.25/12 = +0.021 |

The parameter sweep in chapter 20 produces twelve configurations whose MRR ranges from 0.931 to 0.938. That entire spread is case 6 moving between ranks 4, 5 and 6; the other eleven cases sit at rank 1 in every configuration. A difference of 0.007 in MRR is a single case moving one position, in a case that is in the top five either way.

Two habits follow from this. First, read differences in units of cases, not decimals: before calling a change an improvement, find the cases whose rank changed, using `compare(…).cases`, which prints the rank of every case under every variant. Second, ask whether the count of cases that improved against the count that got worse could have happened by chance. Because every variant runs the same cases, the comparison is paired, and a sign test is the simplest honest check. Under the null hypothesis that a change is equally likely to help or hurt any case it affects, six improvements out of six affected cases has a two-sided probability of 2 × 0.5⁶ = 0.031. One improvement out of one has probability 1. With twelve goldens, most single changes affect one or two cases, and they cannot be distinguished from noise by the numbers alone. What they can do is send you to the cases, where a trace (chapter 13) shows why the rank moved, and a reason you can state is worth more than a decimal you cannot.

The remedy for small sets is more cases, not more decimals. Synthetic cases (section 18.5) add volume with known biases; additional hand-written cases from real user questions add volume without them.

| Metric | Reader it models | Uses R | Saturates when |
| --- | --- | --- | --- |
| hit@k | scans the first k, any one relevant result is enough | no | every case has a relevant hit in the top k |
| MRR | reads down to the first relevant result and stops | no | every case is at rank 1 |
| nDCG@k | reads the top k, values every relevant result, more near the top | yes, capped at k | every list is ideal |
| recall@k | consumes all k (as `pack` does) | yes | every relevant chunk is in the top k |

### Key points

- hit@k, MRR, nDCG@k and recall@k each model a different reader; pick the one whose reader matches the page or the consumer, and report a second one to cover what the first ignores.
- A metric at its maximum on every case can no longer detect improvement; tighten k or switch metric before comparing pipelines.
- nDCG and recall depend on R, so they change when chunking changes even if retrieval does not; compare them only across pipelines with the same chunker.
- The engine's metric values for case 6 match a hand computation exactly: hit@5 1, MRR 0.2, nDCG@10 0.403, recall@10 0.75.
- On twelve cases, one case is 0.083 of hit@k and as little as 0.004 of MRR; read differences as cases that moved, and look at those cases before believing the summary.

Metrics turn one run into numbers. Chapter 20 turns several runs into a comparison: variants of one plan, grids of parameters, and judges for the answers themselves.

**Lab · Field weights.** Load the Field weights preset, open the Evals tab and run the twelve cases. Find case 6 in the table and confirm its rank, MRR and nDCG against section 19.5. Then click the question to run it in the search sheet, and use the Trace tab to see the `why` terms of the sections ranked above 5.2 Execution. Change the metric list in the script to include `hit@1` and `recall@10`, press Build, and run again to see which of the four numbers moves when you change a field weight.

## 20. Variants, sweeps and judges

Chapters 18 and 19 produced a number for one pipeline. This chapter produces comparisons: several pipelines scored on the same cases with the same metrics, a grid of parameter settings scored automatically, and judges that score what string patterns cannot, the answers themselves. It ends with the cost of each kind of run, because the decision of what to measure is also a decision of what to spend.

The design rests on two earlier results. A variant of a plan is a new immutable value made by applying ops (chapter 7), so a comparison never mutates the pipeline under test. And the build engine names every build node by a digest (chapter 11), so ten variants that share a parser parse the corpus once. Together they make comparison cheap enough to run after every edit.

### 20.1 Variants as patched plans; `compare` and its deltas

`plan.variant(id, ops)` returns a copy of the plan with a new id and the ops applied. The Field weights preset defines three variants of one plan, each changing one parameter of the BM25 index:

```js
const textOnly = plan.variant('text-only',  [{ op: 'set', node: 'ix-text', path: 'fields', value: { text: 1 } }]);
const headings = plan.variant('headings-x6', [{ op: 'set', node: 'ix-text', path: 'fields.headingPath', value: 6 }]);
const flatB    = plan.variant('b-0.3',       [{ op: 'set', node: 'ix-text', path: 'b', value: 0.3 }]);
```

`ev.compare(targets)` runs the eval set against each target in turn and returns four things: `rows`, one summary per target; `cases`, the rank of every case under every target; `reports`, the full per-target reports with their misses; and `delta`, the difference between the last target's summary and the first's. The delta is only last-minus-first, not a matrix, so put the baseline first and the candidate last when you want it to mean "what the change did".

Here is the real comparison for Field weights, with five metrics requested:

| variant | hit@1 | hit@5 | mrr | ndcg@10 | recall@10 |
| --- | --- | --- | --- | --- | --- |
| fields (baseline) | 0.917 | 1 | 0.933 | 0.847 | 0.861 |
| text-only | 1 | 1 | 1 | 0.809 | 0.792 |
| headings-x6 | 0.917 | 1 | 0.938 | 0.842 | 0.861 |
| b-0.3 | 0.917 | 1 | 0.938 | 0.829 | 0.840 |

The `cases` table shows that only one case changed rank in any variant. Case 6 ("How does the eval tool report line numbers in errors?") sits at rank 5 under the baseline, rank 1 under text-only, and rank 4 under the other two. Every other case is at rank 1 everywhere. The headline therefore has to be read with chapter 19's arithmetic in mind. text-only's perfect hit@1 and MRR are one case moving from 5 to 1. Its lower nDCG and recall are the same mechanism working against it on other cases: with heading and title weights removed, the first relevant section still wins, but the second and third relevant sections of multi-section answers fall out of the top ten. Whether text-only is better depends on which reader you serve. A results page judged by its first result prefers it; a `pack` node filling Claude's context prefers the baseline.

The Chunk sizes preset gives a comparison with larger movements:

| variant | hit@1 | hit@5 | mrr | ndcg@10 | recall@10 |
| --- | --- | --- | --- | --- | --- |
| chunks (450 tokens, depth 3) | 0.917 | 0.917 | 0.931 | 0.867 | 0.896 |
| 200-tok | 0.833 | 1 | 0.903 | 0.746 | 0.757 |
| 900-tok | 0.917 | 1 | 0.958 | 0.903 | 0.931 |
| h2-only | 0.5 | 0.667 | 0.563 | 0.509 | 0.579 |
| overlap-40 | 1 | 1 | 1 | 0.871 | 0.889 |

Two cautions from earlier chapters apply directly. h2-only's collapse is partly real and partly the four cases with R = 0 that section 18.2 showed are unanswerable under that chunker; four zeros out of twelve cap its hit@5 at 0.667 before retrieval is considered. And 200-tok's lower recall is partly its larger R: more, smaller relevant chunks make a perfect recall@10 harder to reach. Of the remaining variants, overlap-40 and 900-tok improve the first-result metrics, and 900-tok improves nDCG and recall as well. The cases table attributes overlap-40's perfect hit@1 to one case, case 6 again, which moves from rank 6 to rank 1.

In the workbench, variants appear as chips in the Evals tab. Ticking chips and pressing **Run 12 cases** calls `compare` with the current workspace first and the ticked variants after it, and the tab shows the rows and the per-case ranks.

### 20.2 A sweep is a grid of `set` ops, and each configuration rebuilds only what it changed

A variant is one hand-chosen change. A sweep is every combination of a few parameter values. `rag.sweep(target, grid)` takes a grid whose keys are `node.path` strings and whose values are lists, forms the Cartesian product, and for each configuration:

1. turns each key into a `set` op on that node and path,
2. applies the ops to the base IR with `rag.applyOps`,
3. builds the result with the target's engine, recording which build nodes were not reused,
4. runs the eval set and stores the summary with the configuration and the rebuilt list.

The Parameter sweep preset sweeps a live parameter, the RRF constant `fuse.k`, against a build parameter, the heading-path weight of the text index:

```js
const res = await rag.sweep(ws, {
  'fuse.k': [5, 20, 60, 120],
  'ix-text.fields.headingPath': [1, 3, 6],
}).run(kit.goldens().metrics(['mrr', 'hit@1', 'hit@5', 'ndcg@10']), { metric: 'mrr' });
```

The real result, twelve configurations in 590 ms in node, sorted best first by MRR:

| fuse.k | heading weight | mrr | hit@1 | hit@5 | ndcg@10 | rebuilt |
| --- | --- | --- | --- | --- | --- | --- |
| 5 | 6 | 0.938 | 0.917 | 1 | 0.861 | ix-text |
| 20 | 6 | 0.938 | 0.917 | 1 | 0.859 | — |
| 60 | 6 | 0.938 | 0.917 | 1 | 0.849 | — |
| 120 | 6 | 0.938 | 0.917 | 1 | 0.845 | — |
| 5 | 3 | 0.933 | 0.917 | 1 | 0.860 | ix-text |
| 20 | 3 | 0.933 | 0.917 | 1 | 0.860 | — |
| 60 | 3 | 0.933 | 0.917 | 1 | 0.851 | — |
| 120 | 3 | 0.933 | 0.917 | 1 | 0.849 | — |
| 5 | 1 | 0.931 | 0.917 | 0.917 | 0.843 | files parse chunks ix-text ix-meta |
| 20 | 1 | 0.931 | 0.917 | 0.917 | 0.844 | — |
| 60 | 1 | 0.931 | 0.917 | 0.917 | 0.840 | — |
| 120 | 1 | 0.931 | 0.917 | 0.917 | 0.838 | — |

The rebuilt column is the build engine at work. The configurations ran in grid order, (5, 1), (5, 3), (5, 6), (20, 1) and so on. The first had nothing built yet, so it built every node. The second and third changed only the heading weight, a parameter of `ix-text`, so only `ix-text` was rebuilt; the parser and chunker were reused by digest. Every later configuration reused an `ix-text` it had already built for that heading weight, because the digest of the index with weight 3 is the same no matter which `fuse.k` accompanies it. `fuse.k` is a parameter of a live node and never causes a rebuild. Twelve configurations cost one full build and two index builds.

### 20.3 Reading a sweep: the best configuration, ties, and overfitting to twelve questions

The sweep reports `best`, which is the first row after sorting by the chosen metric. Here it is `{ 'fuse.k': 5, 'ix-text.fields.headingPath': 6 }`. Three things about that answer should be checked before it is adopted.

First, ties. Four configurations share the top MRR of 0.938. The sort is stable, so among ties the winner is whichever ran first in grid order. `best` is a ranking artefact among equals, not a finding that `fuse.k` should be 5.

Second, what moved. The whole MRR spread, 0.931 to 0.938, is one case. Per-case ranks with heading weight 3 and with heading weight 6 are identical except for case 6:

```
heading 3: 1,1,1,1,1,5,1,1,1,1,1,1
heading 6: 1,1,1,1,1,4,1,1,1,1,1,1
```

With heading weight 1, case 6 falls to rank 6, which is why hit@5 drops to 0.917 in the bottom four rows. The secondary metric looks like a trend: within each heading weight, ndcg@10 mostly falls as `fuse.k` rises. Per-case values show what the trend is made of. Between `fuse.k` 5 and 120 at heading weight 6, three cases move: case 1 rises from 0.798 to 0.818, case 8 rises from 0.850 to 0.877, and case 12 falls from 1.000 to 0.766. The downward slope is one case losing a secondary relevant section, partly offset by two small gains. A smooth line in a summary chart can still be the movement of a single case, and the per-case table is the only way to tell.

Third, overfitting. A sweep searches for the configuration that scores best on these twelve questions. With twelve configurations and twelve cases, the winner is partly selected for the particular wording of case 6. The protection is the same as in any model selection: keep cases the sweep did not see. In practice that means running the chosen configuration against synthetic cases (section 18.5) or newly written goldens before adopting it, and preferring the simplest configuration within one case of the best. Here, heading weight 3 with the default `fuse.k` of 60 is within one case-position of the winner on every metric.

### 20.4 `answerHas`: the cheapest check on an answer

Ranking metrics stop at the hits. An answer can be wrong even when the right section was retrieved, because Claude can misread it or the packer can drop it; that is the "misread" failure from chapter 1. The cheapest answer check is a text pattern. A case with `answerHas` is scored on whether the generated answer's text matches the pattern, by the same matching rules as document and section patterns.

The Judged evals preset adds one such case to the goldens:

```js
kit.goldens()
  .case('Which tier of the explorer verified KPIs in a browser, and what did it check?',
        { doc: 'Tier B', section: /customer page/i, answerHas: /282,180|77,187/ })
```

The pattern asks for one of two numbers that appear in the evidence. It costs nothing beyond the answer itself, and it is precise where it applies. Three properties limit it. It is scored only in `submit` runs, because only those produce an answer. Its summary is averaged over the cases that define it, so with two such cases the `answerHas` column moves in steps of 0.5. And it tests spelling, not meaning: an answer that writes 282180 or "about 282 thousand" fails the pattern while being correct. Use it for facts with one canonical form (identifiers, numbers as the reports print them, named rules) and use a judge for everything else.

### 20.5 Judges: groundedness and length as lambdas that call Claude

A judge is a named lambda attached to the eval set with `.judge(name, fn)`. In a run with `judge: true`, after each case's answer is produced, the engine calls every judge with three arguments: the run's signals, a context whose `sample` is Claude, and a record `{ q, answer, evidence }`, where `evidence` is the text of every chunk the answer was given. The judge returns a number, normally 0 or 1, which becomes a metric column. If it throws, the value is `null` and the case records `judgeError`; a failing judge never fails the run. Judges are lambdas like every other user function in the system (chapter 9), so they are stored as source and run closed.

The Judged evals preset defines two:

```js
.judge('grounded', async (s, ctx, c) => {
  const v = await ctx.sample.json('Is every claim in the answer supported by the evidence? Reply with only JSON {"grounded": boolean}.\n\nAnswer:\n' + c.answer + '\n\nEvidence:\n' + c.evidence.join('\n---\n').slice(0, 20000), { modelTier: 'quick' });
  return v && v.grounded ? 1 : 0;
})
.judge('concise', async (s, ctx, c) => {
  const v = await ctx.sample.json('Does this answer address the question directly in under 150 words without filler? Reply with only JSON {"ok": boolean}.\n\nQuestion: ' + c.q + '\n\nAnswer:\n' + c.answer, { modelTier: 'quick' });
  return v && v.ok ? 1 : 0;
})
```

The run below was made in node with a mock sampler that returns fixed replies: every answer is the same placeholder sentence, every `grounded` verdict is true and every `concise` verdict is false. The numbers in the judge columns therefore show the mechanism, not the quality of any pipeline, and the ranking metrics in a mocked `submit` run reflect the mock reranker, not Claude's:

```
submit + judge: { n: 13, grounded: 1, concise: 0, answerHas: 0, …, calls: 52 }
```

The call count is real arithmetic: 13 cases × (one `rerank.llm` call + one `answer` call + two judge calls) = 52. Every judge adds one call per case.

A judge is a model grading a model, and it deserves the same scepticism as any other measurement instrument. Three practices keep it honest. Ask binary questions with a stated criterion, as both judges above do, rather than asking for a score from 1 to 10, whose scale drifts. Calibrate before trusting: run the judge on a handful of answers you have labelled yourself and count how often it agrees. And keep judges narrow. "Grounded" asks whether claims are supported by the evidence, which a model can check by reading; "correct" would ask the judge to know the answer, which reintroduces the problem the pipeline exists to solve.

### 20.6 What an eval run costs, and when to spend it

The costs below are measured on the bundled corpus. Lexical latencies are per case in node; Claude call counts are exact and do not depend on the sampler.

| Run | Claude calls | Time |
| --- | --- | --- |
| 12 goldens, `change` trigger (Run 12 cases) | 0 | median 0.4 ms per case |
| compare, baseline + 3 variants, `change` | 0 | under a second |
| sweep, 12 configurations, `change` | 0 | 590 ms including 3 builds |
| 13 cases, `submit` with rerank.llm + answer | 26 | dominated by Claude latency |
| same, plus 2 judges (Run with Claude) | 52 | dominated by Claude latency |
| `evals.generate`, 8 synthetic questions | 1 | one quick-tier call |

The table suggests a policy. Lexical runs are free, so run them after every edit; the Evals tab makes that one click, and the agent in Part IX can run them from its eval tool. Variant comparisons and sweeps are also free when the parameters they change are lexical, so explore widely there. Spend Claude calls when the change touches something only a `submit` run can see: a rewrite or rerank prompt, the packing budget, the answer instructions. Spend judge calls last, when retrieval is stable and the question is whether the answers are good enough to ship. Each step in that order costs more and changes less often, which is the order in which a pipeline settles.

### Key points

- `plan.variant(id, ops)` makes an immutable patched copy, and `compare` scores every target on the same cases; its `delta` is the last target minus the first.
- The per-case ranks in `compare(…).cases` show which cases moved; in Field weights, the only case whose first relevant rank changes between variants is case 6.
- A sweep is a Cartesian grid of `set` ops; the digest-addressed build memo means twelve configurations cost one full build and one index build per distinct heading weight.
- The sweep's `best` is the first row after a stable sort, so ties are broken by grid order; check what moved and confirm the winner on cases the sweep did not see.
- `answerHas` checks an answer's text for a canonical fact at no extra cost; judges check what text patterns cannot, at one Claude call per judge per case, and must be calibrated against hand labels.

Everything in this part scored a pipeline that stood still. Part VII turns to pipelines that change while they run: edits as operations, workspaces with revisions, and a history that lets any change be undone.

**Lab · Chunk sizes, Parameter sweep, Judged evals.** Load Chunk sizes, open the Evals tab, tick all four variant chips and press **Run 12 cases**. In the per-case table, find the four dashes in the h2-only column and confirm they are the R = 0 cases from section 18.2. Then load Parameter sweep and press **Run sweep · 12 configs**; check the rebuilt column, and read the line chart to see that only the heading weight separates the lines. Finally, if your session can call Claude, load Judged evals and press **Run with Claude**. It spends about four calls per case, roughly 52 in all. Read one answer that the `concise` judge failed and decide whether you agree with it.
