// ════════════════════════════════════════════════════════════════════════════
//  rag — a workbench grammar for building search experiences
//  Examples only, simplest first. No implementation.
//
//  Corpus used throughout: the 11 project reports and specs (≈535 KB, ~190 h2
//  sections). That is over the 256 KB cap of one sample() call, so retrieval
//  is real work here: stuffing the corpus into the prompt is not an option.
// ────────────────────────────────────────────────────────────────────────────
//  Lambda phases. The slot a lambda sits in decides its phase, as in gui.
//
//    form                         runs                      gets              stored as
//    b => b.x()                   once, while building      a builder         the node tree
//    build slot  (doc, ctx)       compile, per item         item + build ctx  output artifact, keyed by digest
//    rag.bake(ctx => v)           compile, once             { docs, chunks }  the frozen value
//    live slot   (s, ctx, item)   every query / event       signals, ctx      source text ($fn), closed
//    rag.live(f)                  forces runtime phase      same              same
//
//  Live lambdas are closed. They are rehydrated from source, so a free
//  identifier is an error. Values get in through signals, rag.bake or ctx.
//
//  Ports are typed streams. compile() checks every edge:
//    files → docs → chunks → index          (build side)
//    signals → queries → hits → context → answer   (live side)
//
//  Two ways to drive it:
//    fluent builders → compile() → IR (JSON DAG)    for authoring and sharing
//    rag.workspace(ir).apply([...ops])              for agents and runtime edits
// ════════════════════════════════════════════════════════════════════════════


// ── 01 · The smallest thing that searches ───────────────────────────────────

const notes = rag.corpus('reports')
  .from(rag.files('*.md'))                  // attached files, assets, or db rows
  .parse(rag.markdown())                    // frontmatter, headings, fences, tables
  .chunk(rag.sections())                    // one chunk per heading section
  .index('bm25', rag.bm25());

const hits = await rag.search(notes, 'BIC keeps buying components', { k: 5 });
// → [{ id, docId, title, headingPath, text, score }, …]
//   top: "Tier B › The mixture models, and a lesson about BIC"


// ── 02 · Field weights and frontmatter facets ───────────────────────────────

const notes2 = rag.corpus('reports')
  .from(rag.files('*.md'))
  .parse(rag.markdown({ frontmatter: true, callouts: true }))
  .chunk(rag.sections({ depth: 3, maxTokens: 500, overlap: 40 }))
  .index('bm25', rag.bm25({
    fields: { title: 3, headingPath: 2, callout: 2, text: 1 },   // > [!summary] blocks get a boost
    stem: 'en',
  }))
  .index('meta', rag.terms({                // the agentforum metadata_terms idea:
    from: 'meta',                           // flatten frontmatter into (key, value) rows
    keys: ['type', 'tags', 'status', 'repo', 'aliases'],
  }));

await rag.search(notes2, 'idempotency key primary key', {
  k: 5,
  where: { type: 'project', tags: 'sqlite' },   // exact match over the terms index
});
// → Agentforum › "Idempotency: replay returns the first result"
//   Agentforum › "Open questions" (idempotency_keys primary key)


// ── 03 · Build lambdas: a custom chunker and a baked alias table ────────────

const notes3 = notes2.extend('reports+chunks')         // shares upstream artifacts
  .chunk((doc, ctx) => {
    // build slot: runs once per doc at compile, output cached by digest
    // Keep code fences and tables whole, drop mermaid, carry the heading path.
    const out = [];
    for (const sec of doc.sections) {
      const blocks = sec.blocks.filter(b => !(b.kind === 'fence' && b.lang === 'mermaid'));
      let cur = [];
      for (const b of blocks) {
        const atomic = b.kind === 'fence' || b.kind === 'table';
        if (cur.length && ctx.tokens(cur) + ctx.tokens(b) > 600 && !atomic) {
          out.push({ headingPath: sec.path, blocks: cur });
          cur = [];
        }
        cur.push(b);
      }
      if (cur.length) out.push({ headingPath: sec.path, blocks: cur });
    }
    return out;
  })
  .synonyms(rag.bake(ctx => {
    // once per compile, over all parsed docs: frontmatter aliases become synonyms
    const table = {
      'lca': ['latent class', 'latent class analysis'],
      'idempotency': ['idempotent', 'operation id', 'replay'],
      'dcr': ['dynamic client registration'],
    };
    for (const d of ctx.docs) for (const a of d.meta.aliases ?? []) {
      (table[a.toLowerCase()] ??= []).push(d.title);
    }
    return table;                           // frozen into the IR as plain JSON
  }));

// Closed-lambda rule, as it fails:
//   const MAX = 600;
//   notes2.chunk((doc, ctx) => … MAX …)
//   ✗ build lambda 'chunk' references free identifier `MAX`; use (doc, ctx) or rag.bake


// ── 04 · An answer with evidence ────────────────────────────────────────────

const ask0 = rag.plan('ask').over(notes2)
  .signals({ q: '' })
  .retrieve('bm25', { k: 8 })
  .pack({ budget: 30_000 })                 // bytes of context, never past sample's cap
  .answer({ cite: 'evidence', tier: 'default' });

const r = await rag.run(ask0, { q: 'Why did ChatGPT show "No app actions available yet"?' });
// r.answer   → "… it sent sessionless server/discover with MCP 2026-07-28 while the
//               transport was stateful [E1] … the fix made Streamable HTTP stateless [E2]"
// r.evidence → [{ id: 'E1', chunk: 'coinvault#11', title, headingPath, text }, …]
// r.trace    → per-node timings and in/out counts (see 06)


// ── 05 · Live lambdas: rewrite, filter, rerank (linear chain) ───────────────

const ask = rag.plan('ask').over(notes3)
  .signals({ q: '', k: 8, filters: {} })
  .rewrite(async (s, ctx) => {
    // live slot: runs per query; only on submit, not per keystroke
    const r = await ctx.sample.json(
      'Rewrite the question as 1-3 short keyword queries for a BM25 index over ' +
      'engineering project reports. Return {"queries": [string]}.\n\nQuestion: ' + s.q,
      { modelTier: 'quick' });
    return [s.q, ...r.queries];
  }, { trigger: 'submit' })
  .retrieve('bm25', { k: 40, perQuery: true })
  .fuse.rrf({ k: 60 })
  .filter((s, ctx, hit) =>
    Object.entries(s.filters).every(([k, v]) => [].concat(hit.meta[k] ?? []).includes(v)))
  .rerank.llm({ top: 20, keep: s => s.k, tier: 'quick', trigger: 'submit' })
  .expand.neighbors({ after: 1, maxChars: 4_000 })
  .pack({ budget: 40_000, order: 'doc' })
  .answer({ cite: 'evidence', trigger: 'submit' });

// The filter lambda reads s.filters. compile() runs it once through a tracking
// proxy and records the edge filters → filter, so a facet click re-runs only
// filter, rerank, expand, pack and answer.


// ── 06 · The same thing as an explicit DAG, compiled and inspected ──────────

const askGraph = rag.graph('ask', g => {
  const q = g.signal('q', ''), k = g.signal('k', 8), filters = g.signal('filters', {});

  const queries = g.node('rewrite', rag.rewrite.llm({ n: 3 }), { from: q, trigger: 'submit' });
  const lex   = g.node('lex',   rag.retrieve('bm25',  { k: 40 }), { from: queries, index: notes3.index('bm25') });
  const terms = g.node('terms', rag.retrieve('terms', { k: 20 }), { from: q,       index: notes3.index('meta') });
  const fused = g.node('fuse',  rag.fuse.rrf({ k: 60, weights: { lex: 1, terms: 0.5 } }), { from: [lex, terms] });
  const kept  = g.node('filter', rag.filter.where(), { from: [fused, filters] });
  const top   = g.node('rerank', rag.rerank.llm({ top: 20 }), { from: [kept, q, k], trigger: 'submit' });
  const ctxt  = g.node('pack', rag.pack({ budget: 40_000 }), { from: top });

  g.output('hits', top);
  g.output('answer', g.node('answer', rag.answer({ cite: 'evidence' }), { from: [ctxt, q] }));
});

const ir = rag.compile([notes3, askGraph]);

ir.passes;
// [ resolve(sources)    11 files                   2 ms
//   parse(markdown)     11 docs · 188 sections     9 ms
//   chunk(build λ)      reused 0 · computed 11     14 ms
//   bake(synonyms)      1 value                    0.3 ms
//   index(bm25, terms)  2 indexes                  31 ms
//   lower(plan)         8 live nodes · 3 λ         0.4 ms
//   check(ports)        13 edges · 0 warnings ]

ir.json();
// { "kind": "rag", "version": 1, "id": "ask", "digest": "…",
//   "signals": { "q": "", "k": 8, "filters": {} },
//   "nodes": [
//     { "id": "chunk",  "t": "chunk",        "phase": "build", "in": ["parse"], "out": "chunks",
//       "p": { "fn": { "$fn": "(doc, ctx) => { … }" } }, "artifact": { "digest": "c1f3…" } },
//     { "id": "lex",    "t": "retrieve",     "phase": "live",  "in": ["rewrite", "bm25"], "out": "hits",
//       "p": { "k": 40 } },
//     { "id": "rerank", "t": "rerank.llm",   "phase": "live",  "trigger": "submit", … },
//     … ],
//   "edges": [["parse","chunk"], ["chunk","bm25"], ["q","rewrite"], ["rewrite","lex"],
//             ["bm25","lex"], ["filters","filter"], …] }

ir.mermaid();                      // for a diagram; the inspector draws the same DAG
ir.downstream('chunk');            // ['bm25', 'meta', 'lex', 'terms', 'fuse', …]

const res = await rag.run(ir, { q: 'what is the knee rule for latent classes', k: 5 });
res.trace;
// - { node: rewrite, ms: 812, in: 1,   out: 3 }
// - { node: lex,     ms: 3,   in: 3,   out: 112 }
// - { node: terms,   ms: 0.4, in: 1,   out: 0 }
// - { node: fuse,    ms: 0.2, in: 112, out: 71 }
// - { node: rerank,  ms: 1630, in: 20, out: 5 }
// - { node: answer,  ms: 2410, in: 5,  out: 1 }

rag.explain(res, res.hits[0]);
// { chunk: 'tier-b#latent-class-analysis',
//   lex:   { rank: 1, terms: { knee: 4.1, latent: 2.3, class: 1.8 }, field: 'text' },
//   fuse:  { rrf: 0.0328 }, rerank: { score: 0.94, reason: 'states the 10% marginal-gain rule' } }


// ── 07 · Build-time LLM enrichment: a semantic layer without vectors ────────

const notesSem = notes3.extend('reports+sem')
  .enrich('sem', async (chunk, ctx) => {
    // build slot, one sample.json call per chunk, cached by digest(fn source + chunk text)
    const r = await ctx.sample.json(
      'For this section of an engineering report, return ' +
      '{"summary": string, "questions": [3-5 questions it answers], "keywords": [string]}.\n\n' +
      chunk.headingPath.join(' › ') + '\n\n' + chunk.text,
      { modelTier: 'quick' });
    return r;
  }, { concurrency: 3, cache: 'db', budget: { calls: 600 } })
  .index('sem', rag.bm25({ fields: { 'sem.questions': 3, 'sem.keywords': 2, 'sem.summary': 1 } }));

const built = await rag.build(notesSem, { onProgress: p => log(p.node, p.done, p.total) });
// { enrich: { computed: 412, reused: 0, calls: 412 }, sem: { docs: 412 } }

// Edit the chunker later and rebuild: only chunks whose text changed are re-enriched.
// { enrich: { computed: 37, reused: 375, calls: 37 } }

// Use it as a second retriever. "Why did one-time buyers get excluded?" has
// no word overlap with "repeat buyers only", but the generated questions do.
const askSem = ask.extend('ask+sem')
  .replace('retrieve', p => p
    .retrieve('bm25', { k: 40, perQuery: true })
    .retrieve('sem',  { k: 40, perQuery: true })
    .fuse.rrf({ k: 60, weights: { bm25: 1, sem: 1 } }));


// ── 08 · The search experience: signals, view, triggers ─────────────────────

const app = rag.app('reports-search').title('Project reports')
  .use(askSem)                                   // brings q, k, filters and every node
  .state({ selected: null })
  .derive('scope', s => Object.keys(s.filters).length ? 'filtered' : 'all reports')
  .view(v => v.row(r => r
    .col(c => c
      .searchBox('q', { placeholder: 'Ask the reports…', debounce: 200 })
      .facets('filters', { fields: ['type', 'tags', 'status'], counts: true })
      .results({
        from: 'fuse',                            // lexical results update per keystroke
        swapTo: 'rerank',                        // replaced when the reranked list lands
        item: (s, ctx, hit) => ({ title: hit.title, sub: hit.headingPath.join(' › '), snippet: hit.highlight }),
        onClick: (s, ctx, hit) => ctx.set({ selected: hit.id }),
      }))
    .col(c => c
      .answer({ from: 'answer', citations: 'evidence', onCite: (s, ctx, e) => ctx.set({ selected: e.chunk }) })
      .when(s => s.selected, w => w.chunk(s => s.selected, { neighbors: 1, highlight: s => s.q }))
      .trace({ collapsed: true })                // the per-node table from 06
      .dag({ highlight: 'lastRun' }))));         // live/build/signal edges, as in the inspector

// Triggers decide cost: per-keystroke nodes are lexical only; every LLM node
// is trigger: 'submit'. The app shows "press Enter for a cited answer".
ui.app(app);


// ── 09 · Agentic retrieval as one node ──────────────────────────────────────

const askAgent = rag.plan('ask-agent').over(notesSem)
  .signals({ q: '' })
  .agentic({
    tools: {
      search:    rag.tool.search(['bm25', 'sem'], { k: 8 }),  // returns ids, titles, snippets
      read:      rag.tool.readChunk({ maxChars: 6_000 }),
      neighbors: rag.tool.neighbors(),
      outline:   rag.tool.outline(),                          // doc id → heading tree
      facets:    rag.tool.facets(['type', 'tags', 'repo']),
    },
    instructions: s =>
      'Answer from the project reports only. Search with several phrasings, read ' +
      'the sections you cite, and stop when you can answer. Question: ' + s.q,
    maxRounds: 8,
    tier: 'default',
    until: (s, ctx, step) => step.read.length >= 6,           // hard stop besides maxRounds
  })
  .answer({ cite: 'evidence' })
  .fallback(ask);                  // used when sample.limits() reports no tools in this view

const ra = await rag.run(askAgent, {
  q: 'Across all projects, which bugs were caused by something silently becoming empty or nil?',
});
ra.trace.rounds;
// 1 search("typed nil interface")           → coinvault#4.1
// 2 search("empty flags deleted products")  → ttc-order-grain#classification-cascade
// 3 search("event reactive empty tab")      → ttc-order-grain#failure-modes
// 4 search("omitted scope empty ceiling")   → coinvault#6.2
// 5 read × 4
// → answer cites 4 sections from 3 documents


// ── 10 · Goldens: the eval set comes first ──────────────────────────────────

const goldens = rag.evals('reports-goldens')
  .case('Why did ChatGPT show no actions after OAuth worked?',
        { doc: /CoinVault/, section: /stateless|server\/discover/i })
  .case('What rule picked eight latent classes?',
        { doc: /Tier B/, section: /Latent class/, answerHas: /ten percent|knee/i })
  .case('How are arrays of objects flattened into metadata terms?',
        { doc: /Agentforum — A SQLite/, section: /Metadata/ })
  .case('Why is a content hash not enough for an operation id?',
        { doc: /Workbench Identity/, section: /Separately intended|Content hash/ })
  .case('What happens to product flags when a product is deleted?',
        { doc: /TTC/, any: true })                      // three TTC docs answer it
  .case('How does the eval tool turn stack traces into line numbers?',
        { doc: /Agent runtime spec/, section: /eval tool/ })
  .metrics(['hit@5', 'mrr', 'ndcg@10'])
  .judge('grounded', async (s, ctx, c) => {
    // live λ: an LLM judge over the answer and its evidence
    const v = await ctx.sample.json(
      'Is every claim in the answer supported by the evidence? Return {"grounded": boolean, "unsupported": [string]}.\n\n' +
      JSON.stringify({ answer: c.answer, evidence: c.evidence.map(e => e.text) }),
      { modelTier: 'quick' });
    return v.grounded ? 1 : 0;
  });

// Synthetic cases, generated at build time from the sections themselves.
// Tagged synthetic so they never mix silently with the hand-written ones.
const synth = rag.evals.generate(notes3, { perSection: 1, sample: 60, tier: 'quick', seed: 7 });

const report = await goldens.run(ask, { judge: true });
// { hit@5: 0.83, mrr: 0.71, ndcg@10: 0.77, grounded: 1.0,
//   misses: [{ q: 'What rule picked eight latent classes?', top: 'Tier B › Choosing the population…' }] }


// ── 11 · Variants and sweeps ────────────────────────────────────────────────

const lexOnly = ask.variant('lex-only', v => v.remove('rewrite').remove('rerank'));
const withSem = askSem;

const cmp = await goldens.concat(synth).compare([lexOnly, ask, withSem]);
// variant    hit@5  mrr   llm calls/q  p50 ms
// lex-only   0.64   0.52  1            2,300
// ask        0.83   0.71  3            4,900
// ask+sem    0.91   0.80  3            5,100

const sweep = rag.sweep(withSem, {
  'chunk.maxTokens':        [300, 600, 900],          // build param: rebuilds chunk → indexes
  'bm25.fields.headingPath': [1, 3],                  // build param: rebuilds bm25 only
  'fuse.weights.sem':        [0.5, 1, 2],             // live param: no rebuild
});
const best = await sweep.run(goldens, { metric: 'mrr', concurrency: 2 });
// 18 configurations · 6 chunk builds · 12 index builds · 0 re-enrichments (digest hits)
best.top(3);


// ── 12 · Mutation mode: a workspace for agents and runtime edits ────────────

const ws = rag.workspace(ir);              // owns the IR, a local revision, and history
ws.revision;                               // 0

// Ops are data: loggable, replayable, undoable. A batch is atomic.
const receipt = await ws.apply([
  { op: 'set',    node: 'bm25',  path: 'fields.headingPath', value: 3 },
  { op: 'set',    node: 'fuse',  path: 'weights.terms', value: 0.8 },
  { op: 'insert', after: 'fuse', id: 'links',
    node: { t: 'expand.links', p: { from: ['related_vault_notes', 'wikilinks'], decay: 0.5 } } },
  { op: 'signal', key: 'k', value: 6 },
], { expect: 0 });                         // precondition on revision; stale → conflict
// { revision: 1,
//   live:  ['fuse', 'links', 'k'],        // applied now, next query uses them
//   dirty: ['bm25'],                      // build phase: needs a rebuild
//   inverse: [ … ] }                      // what undo will apply

await ws.rebuild();                        // recomputes bm25 only; chunks reused by digest
await ws.dryRun([{ op: 'set', node: 'chunk', path: 'maxTokens', value: 900 }]);
// { dirty: ['chunk', 'bm25', 'meta', 'sem-enrich', 'sem'], estCalls: 412 }   ← costly, ask first

// Replace a lambda by source text. It is checked for free identifiers like any other.
await ws.apply([{ op: 'replace', node: 'filter', fn:
  '(s, ctx, hit) => !s.filters.type || hit.meta.type === s.filters.type' }]);

ws.undo();                                 // back one batch
ws.history;                                // [{ revision, ops, by: 'agent' | 'user', at }]

// Branches isolate experiments; merge is a batch like any other.
const b = ws.branch('title-boost');
await b.apply([{ op: 'set', node: 'bm25', path: 'fields.title', value: 5 }]);
await b.rebuild();
await ws.merge(b);                         // fails with conflict if ws moved meanwhile


// ── 13 · An agent session: eval steps against the workspace ─────────────────
// The agent from the runtime spec, with `rag`, `ws`, `goldens` added to scope.
// Each function is one eval() call; its parameters are the scope objects.

const agentSteps = [

  // step 1 · look before changing anything
  async ({ rag, ws, goldens }) => {
    const base = await goldens.run(ws.ir);
    return {
      score: base.summary,
      misses: base.misses.map(m => ({ q: m.q, got: m.top, want: m.expect })),
      graph: ws.ir.nodes.map(n => `${n.id}:${n.phase}`),
    };
  },

  // step 2 · try a fix on a branch, measure, show
  async ({ ws, goldens, chat }) => {
    const b2 = ws.branch('section-titles-in-text');
    await b2.apply([{ op: 'set', node: 'chunk', path: 'prefixHeading', value: true }]);
    await b2.rebuild();
    const d = await goldens.compare([ws, b2]);
    chat.table(d.rows, { title: 'baseline vs heading-prefixed chunks' });
    return { delta: d.delta, rebuilt: b2.lastBuild.computed };
  },

  // step 3 · user says "keep it"
  async ({ ws, ref }) => {
    const m = await ws.merge(ws.branches['section-titles-in-text']);
    return { revision: m.revision, link: ref.rag({ revision: m.revision }) };
  },
];

// In Read-only mode ws.apply / merge / rebuild throw:
//   ✗ ws.apply is blocked in Read-only mode. Describe the change, or ask the
//     user to switch to Auto or Ask.
// rag.run, goldens.run and branches without merge stay allowed.


// ── 14 · Defining components and reusable fragments ─────────────────────────

// A live component: follow [[wiki links]] and related_vault_notes one hop.
rag.define('expand.links', {
  phase: 'live',
  in:  { hits: 'hits' },
  out: { hits: 'hits' },
  params: { decay: 0.5, max: 4 },
  run: (input, p, ctx) => {
    const seen = new Set(input.hits.map(h => h.docId));
    const extra = [];
    for (const h of input.hits.slice(0, 5)) {
      for (const docId of ctx.links(h.docId)) {
        if (seen.has(docId) || extra.length >= p.max) continue;
        seen.add(docId);
        const best = ctx.index('bm25').searchIn(docId, ctx.signals.q, 1)[0];
        if (best) extra.push({ ...best, score: h.score * p.decay, via: h.docId });
      }
    }
    return { hits: [...input.hits, ...extra] };
  },
});

// A build component: parse "Failure | Mechanism | Fix" tables into their own
// chunk type, so "why was the tab empty" lands on the row, not the section.
rag.define('chunk.failureTables', {
  phase: 'build',
  in:  { docs: 'docs' },
  out: { chunks: 'chunks' },
  map: (doc, p, ctx) => doc.tables
    .filter(t => /failure|symptom/i.test(t.header[0]) && t.header.length >= 3)
    .flatMap(t => t.rows.map((row, i) => ({
      kind: 'failure-row',
      headingPath: [...t.sectionPath, `row ${i + 1}`],
      text: t.header.map((h, j) => `${h}: ${row[j]}`).join('\n'),
    }))),
});

// A fragment is a reusable sub-plan with its own parameters.
const hybrid = rag.fragment('hybrid', (f, p = { k: 40 }) => f
  .retrieve('bm25', { k: p.k, perQuery: true })
  .retrieve('sem',  { k: p.k, perQuery: true })
  .retrieve('failures', { k: 10 })
  .fuse.rrf({ k: 60 })
  .expand.links({ decay: 0.4 }));

const notesFinal = notesSem.extend('reports+failures')
  .chunk('failures', rag.use('chunk.failureTables'))       // a second chunk stream
  .index('failures', rag.bm25({ fields: { text: 1 } }), { from: 'failures' });

const askFinal = rag.plan('ask-final').over(notesFinal)
  .signals({ q: '', k: 8, filters: {} })
  .rewrite(rag.rewrite.llm({ n: 3 }), { trigger: 'submit' })
  .use(hybrid, { k: 30 })
  .filter.where()
  .rerank.llm({ top: 24, keep: s => s.k, trigger: 'submit' })
  .pack({ budget: 40_000 })
  .answer({ cite: 'evidence', trigger: 'submit' });


// ── 15 · Persist, reload, hand off ──────────────────────────────────────────

const irFinal = rag.compile([notesFinal, askFinal, app]);

await rag.save(irFinal, { db: 'rag/pipelines/reports' });   // IR with $fn sources, no closures
await rag.persist(irFinal, { to: 'assets' });               // chunks, enrichments, indexes as JSON assets
// Index artifacts are keyed by digest, so a reload with the same IR skips the build.

const back = await rag.load({ db: 'rag/pipelines/reports' });
await rag.run(back, { q: 'what did the hard cutover delete?' });   // lambdas rehydrated, closed

// A frozen copy for someone without the workbench: IR, indexes and the app view
// in one HTML string. LLM nodes stay if the page declares sample, else they
// are dropped and the app falls back to lexical results.
const html = rag.export(irFinal, { app: 'reports-search', llm: 'if-available' });
chat.download('reports-search.html', html);
