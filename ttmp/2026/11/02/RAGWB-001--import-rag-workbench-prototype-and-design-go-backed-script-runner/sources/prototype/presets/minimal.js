// Minimal search · the smallest pipeline that works.
// One chunk per heading section, one BM25 index, one retriever. No Claude calls.
// Try: change maxTokens to 200, or give headingPath a weight: rag.bm25({ fields: { headingPath: 3, text: 1 } }).

const corpus = rag.corpus('reports')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());

const plan = rag.plan('minimal').over(corpus)
  .signals({ q: 'rebalance law', k: 10 })
  .retrieve('text', { k: 20 });

const app = gui.app('minimal').title('Minimal search').use(plan)
  .state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c.searchBox('q', { placeholder: 'Search the reports…', submitLabel: 'Search' }).status().results(), { grow: 3 })
    .col(c => c.chunk(s => s.selected), { grow: 2 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
