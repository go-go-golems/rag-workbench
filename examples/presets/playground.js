// Ranking playground · sliders bound to signals the plan reads.
// The fuse k and the rerank weights are live lambdas over signals (s.rrfK, s.headingBoost…), so
// each slider re-runs only the nodes that read it. The mini DAG shows those signal edges.
// Try: drag failureBoost up and watch failure-table rows climb for "orders tab empty".

const corpus = kit.reports()
  .chunk('failures', (doc, ctx) => doc.sections.flatMap(sec => sec.blocks
    .filter(b => b.kind === 'table' && /failure|symptom/i.test(b.header[0] || ''))
    .flatMap(t => t.rows.map(row => ({ kind: 'failure', headingPath: [...sec.path, row[0]], text: t.header.map((h, j) => `${h}: ${row[j] ?? ''}`).join('\n') })))))
  .index('failures', rag.bm25({ fields: { headingPath: 2, text: 1 } }), { from: 'failures' });

const plan = rag.plan('playground').over(corpus)
  .signals({ q: 'Why was the orders tab empty?', k: 8, headingBoost: 1, failureBoost: 1, rrfK: 60 })
  .retrieve('text', { k: 40 })
  .retrieve('failures', { k: 10 })
  .fuse.rrf({ k: s => s.rrfK })
  .rerank((s, ctx, hits) => {
    const words = String(s.q).toLowerCase().match(/[a-z0-9]{4,}/g) || [];
    return hits.map(h => {
      const o = words.filter(w => h.headingPath.join(' ').toLowerCase().includes(w)).length / Math.max(1, words.length);
      const f = h.kind === 'failure' ? s.failureBoost : 1;
      return { id: h.id, score: h.score * (1 + s.headingBoost * o) * f, reason: `heading ${o.toFixed(2)} · ${h.kind}` };
    }).sort((a, b) => b.score - a.score);
  })
  .limit(s => s.k);

const app = gui.app('playground').title('Ranking playground').use(plan).state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c
      .searchBox('q')
      .card('Knobs', b => b
        .slider('k', { min: 3, max: 20, step: 1, label: 'results (k)' })
        .slider('headingBoost', { min: 0, max: 4, step: 0.25, label: 'heading boost' })
        .slider('failureBoost', { min: 0, max: 4, step: 0.25, label: 'failure-row boost' })
        .slider('rrfK', { min: 5, max: 120, step: 5, label: 'rrf k' }))
      .status()
      .results(), { grow: 3 })
    .col(c => c.card('Plan', b => b.dag()).chunk(s => s.selected), { grow: 2 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
