// Faceted search · frontmatter becomes filters.
// filter.where() keeps hits whose meta matches s.filters. A second filter lambda reads the
// codeOnly signal, so the toggle re-runs only the nodes after it.
// Try: click tags, toggle "only sections with code", and watch the Trace tab's cached rows.

const plan = rag.plan('facets').over(kit.reports())
  .signals({ q: 'sync', k: 10, filters: {}, codeOnly: false })
  .retrieve('text', { k: 80 })
  .filter.where()
  .filter((s, ctx, hit) => !s.codeOnly || hit.text.includes('```'), { id: 'code_only' })
  .limit(30);

const app = gui.app('facets').title('Faceted search').use(plan).state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c
      .searchBox('q')
      .toggle('codeOnly', 'Only sections with code')
      .status()
      .facets('filters', { fields: ['type', 'tags', 'status'], from: 'text', max: 10 })
      .results({ limit: 15 }), { grow: 3 })
    .col(c => c.chunk(s => s.selected), { grow: 2 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
