// Your own components · rag.define adds live steps that chain like built-ins.
// diversify caps sections per document; boost.tags multiplies scores for chunks tagged with the
// boostTag signal. Both then appear as plan.diversify() and plan.boost.tags().
// Try: pick a tag in the menu, or change perDoc to 1 in the DAG tab.

rag.define('diversify', {
  phase: 'live', in: 'hits', out: 'hits', params: { perDoc: 2 },
  describe: p => `≤ ${p.perDoc} per doc`,
  run: (s, ctx, hits, p) => {
    const seen = new Map();
    return hits.filter(h => {
      const d = (ctx.chunk(h.id) || {}).docId;
      const n = seen.get(d) || 0;
      seen.set(d, n + 1);
      return n < p.perDoc;
    });
  },
});

rag.define('boost.tags', {
  phase: 'live', in: 'hits', out: 'hits', params: { factor: 2 }, reads: () => ['boostTag'],
  describe: p => `×${p.factor} when tagged s.boostTag`,
  run: (s, ctx, hits, p) => hits
    .map(h => ({ ...h, score: [].concat(((ctx.chunk(h.id) || {}).meta || {}).tags || []).includes(s.boostTag) ? h.score * p.factor : h.score }))
    .sort((a, b) => b.score - a.score),
});

const plan = rag.plan('custom').over(kit.reports())
  .signals({ q: 'testing and verification', k: 10, boostTag: 'go' })
  .retrieve('text', { k: 60 })
  .boost.tags({ factor: 2 })
  .diversify({ perDoc: 2 })
  .limit(s => s.k);

const app = gui.app('custom').title('Custom components').use(plan)
  .state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c
      .searchBox('q')
      .select('boostTag', (s, ctx) => [{ value: '', label: 'no boost' }, ...[...new Set(ctx.rag.docs().flatMap(d => [].concat(d.meta.tags || [])))].sort().map(t => ({ value: t, label: t }))], { label: 'Boost tag' })
      .status()
      .results(), { grow: 3 })
    .col(c => c.chunk(s => s.selected), { grow: 2 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
