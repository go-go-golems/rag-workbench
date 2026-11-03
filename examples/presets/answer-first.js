// Answer first · tabs for the answer, its sources, and how it was made.
// The layout is plain gui: tabs(t => t.tab(…)). The Trace tab embeds the run trace and a compact
// DAG of the plan, both live.
// Try: press Enter, then switch to Trace.

const plan = rag.plan('answer-first').over(kit.reports())
  .signals({ q: 'What happens to product flags when a product is deleted?', k: 8, filters: {} })
  .retrieve('text', { k: 40 })
  .retrieve('meta', { k: 20 })
  .fuse.rrf({ weights: { text: 1, meta: 0.3 } })
  .filter.where()
  .rerank.llm({ top: 20, keep: s => s.k })
  .pack({ budget: 30000 })
  .answer();

const app = gui.app('answer-first').title('Answer first').use(plan).state({ selected: null })
  .view(v => v.col(c => c
    .searchBox('q', { placeholder: 'Ask a question and press Enter…' })
    .status()
    .tabs(t => t
      .tab('Answer', b => b.answer({ title: 'Answer' }).chunk(s => s.selected))
      .tab('Sources', b => b.facets('filters', { fields: ['type', 'tags'] }).results({ limit: 20 }))
      .tab('Trace', b => b.trace().dag()))));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
