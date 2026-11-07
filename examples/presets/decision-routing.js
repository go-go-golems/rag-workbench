// Decision routing: classify on Enter, retrieve and grade only when documents are needed.
// Start the workbench with --decide pi (or --decide fake) and an LLM backend.
// Try a greeting, then ask about the rebalance law; inspect Trace for the skipped nodes.
const plan = rag.plan('decision-routing').over(kit.reports())
  .signals({ q: 'rebalance law', k: 8, filters: {} })
  .classify({ id: 'route', corpus: 'engineering project reports and design notes', questions: {
    needs: { type: 'noul', instructions: 'Does answering require the project reports and their design details?',
      true: 'It asks about project designs, decisions, results, or the rebalance law', false: 'It is a greeting or general knowledge' },
  } })
  .retrieve('text', { k: 30, when: { decision: 'route.needs', min: 0.5 } })
  .retrieve('meta', { k: 20, when: { decision: 'route.needs', min: 0.5 } })
  .fuse.rrf()
  .filter.where()
  .rerank.decide({ top: 20, keep: s => s.k, corpus: 'engineering project reports and design notes', when: { decision: 'route.needs', min: 0.5 } })
  .pack({ budget: 30000 })
  .answer({ direct: true });

const app = gui.app('decision-routing').title('Decision routing').use(plan).state({ selected: null })
  .view(v => v.col(c => c
    .searchBox('q', { placeholder: 'Ask a report question or say hello…' }).status()
    .tabs(t => t
      .tab('Answer', b => b.answer().chunk(s => s.selected))
      .tab('Sources', b => b.results({ limit: 20 }))
      .tab('Trace', b => b.trace({ details: true }).dag()))));

return { plan, evals: kit.goldens(), app };
