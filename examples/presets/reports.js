// Reports · hybrid search, rerank and a cited answer.
// Three retrievers (sections, failure-table rows, frontmatter terms) fused with RRF; Claude rewrites,
// reranks and answers on Enter. Typing runs only the lexical nodes.
// Scope: rag, gui, log, kit. Return { plan, evals, app, variants }.

// ── Corpus · build phase: runs once per item, cached by digest ─────────────
const reports = rag.corpus('reports')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  // A second chunk stream: one chunk per row of "failure | cause | fix" tables.
  .chunk('failures', (doc, ctx) => doc.sections.flatMap(sec => sec.blocks
    .filter(b => b.kind === 'table' && /failure|symptom/i.test(b.header[0] || ''))
    .flatMap(t => t.rows.map(row => ({
      headingPath: [...sec.path, row[0]],
      text: t.header.map((h, j) => `${h}: ${row[j] ?? ''}`).join('\n'),
    })))))
  // Frontmatter aliases become query synonyms, computed once per build.
  .synonyms(rag.bake(ctx => {
    const table = { lca: ['latent class'], dcr: ['dynamic client registration'], idempotency: ['idempotent', 'operation id'] };
    for (const d of ctx.docs) for (const a of [].concat(d.meta.aliases ?? [])) (table[String(a).toLowerCase()] ??= []).push(d.title);
    return table;
  }))
  .index('text', rag.bm25({ fields: { title: 2, headingPath: 3, callout: 2, text: 1 } }))
  .index('failures', rag.bm25({ fields: { headingPath: 2, text: 1 } }), { from: 'failures' })
  .index('meta', rag.terms({ keys: ['type', 'tags', 'status', 'aliases'] }));

// ── Plan · live phase: runs per query. LLM nodes run only on Enter ─────────
const ask = rag.plan('ask').over(reports)
  .signals({ q: 'Why did ChatGPT show no app actions after OAuth worked?', k: 8, filters: {} })
  .rewrite.llm({ n: 3 })
  .retrieve('text', { k: 40 })
  .retrieve('failures', { k: 10 })
  .retrieve('meta', { k: 20 })
  .fuse.rrf({ k: 60, weights: { text: 1, failures: 0.8, meta: 0.3 } })
  .filter.where()
  .rerank.llm({ top: 20, keep: s => s.k })
  .expand.neighbors({ after: 1 })
  .pack({ budget: 40000 })
  .answer();

// Variants are the plan with ops applied: the same edit language the agent uses.
const lexical = ask.variant('lexical', [{ op: 'remove', node: 'rewrite' }, { op: 'remove', node: 'rerank' }]);
const agentic = rag.plan('agentic').over(reports).signals({ q: '', k: 8 }).agentic({ maxReads: 8 });

// ── Goldens · hand-written cases with the section that should come back ────
const goldens = rag.evals('goldens')
  .case('Why did ChatGPT show no actions after OAuth worked?', { doc: 'CoinVault', section: /no actions|stateless/i })
  .case('What rule picked eight latent classes?', { doc: /Tier B|Broken Exports/, section: /latent class|model selection/i, answerHas: /knee|ten percent|10%/i })
  .case('How are arrays of objects flattened into metadata terms?', { doc: 'SQLite-Backed Forum', section: /metadata/i })
  .case('Why is a content hash not enough for an operation id?', { doc: 'Workbench Identity', section: /separately intended|content hash/i })
  .case('What happens to product flags when a product is deleted?', { doc: 'TTC', section: /cascade|what the data said|asked/i })
  .case('How does the eval tool report line numbers in errors?', { doc: 'Agent runtime spec', section: /eval tool|execution/i })
  .case('Why did the browser ECS service restart in a loop after the shared-image deploy?', { doc: 'CoinVault', section: /typed-nil/i })
  .case('Why does the inbox cursor advance past events an agent is not eligible for?', { doc: 'SQLite-Backed Forum', section: /inbox|cursor/i })
  .case('Why was the orders tab empty when the explorer loaded?', { doc: 'TTC', section: /failure|orders tab|architecture/i })
  .case('How does protojson serialize int64 values?', { doc: 'Agentforum Web', section: /type traps|protobuf/i })
  .case('What does the rebalance law say?', { doc: 'Workbench Core', section: /rebalance/i })
  .case('How are generative UI lambdas stored so they survive a reload?', { doc: 'Agent runtime spec', section: /lambda|generative ui|concepts/i })
  .metrics(['hit@5', 'mrr', 'ndcg@10'])
  .judge('grounded', async (s, ctx, c) => {
    const v = await ctx.sample.json('Is every claim in the answer supported by the evidence? Reply with only JSON {"grounded": boolean}.\n\nAnswer:\n' +
      c.answer + '\n\nEvidence:\n' + c.evidence.join('\n---\n').slice(0, 20000), { modelTier: 'quick' });
    return v && v.grounded ? 1 : 0;
  });

// ── Search experience · generative UI bound to the plan's signals ──────────
const app = gui.app('search').title('Project reports').use(ask)
  .state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c
      .searchBox('q', { placeholder: 'Ask the reports…' })
      .status()
      .facets('filters', { fields: ['type', 'tags'] })
      .results(), { grow: 3 })
    .col(c => c
      .answer()
      .chunk(s => s.selected), { grow: 2 })));

// Ops on top of this script. "Keep in script" on the History tab writes them here.
const ops = []; // end ops

return { plan: ask.patch(ops), variants: [lexical, agentic], evals: goldens, app };
