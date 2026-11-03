// Table rows as chunks · every markdown table row becomes its own chunk.
// A build lambda turns a row into "Header: value" lines under the section path plus the row's
// first cell. Its params arrive as the last argument (p.minRows), so the lambda stays closed.
// Try: "which HTTP status does ErrConflict map to", "what is the champions segment's median revenue".

const corpus = kit.reports()
  .chunk('rows', (doc, ctx, p) => doc.sections.flatMap(sec => sec.blocks
    .filter(b => b.kind === 'table' && b.rows.length >= p.minRows)
    .flatMap(t => t.rows.map(row => ({
      kind: 'row',
      headingPath: [...sec.path, row[0]],
      text: t.header.map((h, j) => `${h}: ${row[j] ?? ''}`).join('\n'),
    })))), { minRows: 2 })
  .index('rows', rag.bm25({ fields: { headingPath: 2, text: 1 } }), { from: 'rows' });

const plan = rag.plan('rows').over(corpus)
  .signals({ q: 'which HTTP status does ErrConflict map to', k: 10, filters: {} })
  .retrieve('rows', { k: 15 })
  .retrieve('text', { k: 40 })
  .fuse.rrf({ k: 60, weights: { rows: 1.2, text: 1 } })
  .filter.where()
  .limit(s => s.k);

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Table rows', answer: false }) };
