// Document cards · one chunk per document, for "which report covers X?".
// Each card holds the title, tags, summary callouts and every h2 heading. The evals here only
// check the document, not the section.
// Try: "Which project deals with OAuth?", "which reports mention goldens".

const corpus = rag.corpus('docs')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk((doc, ctx) => [{
    kind: 'doc',
    headingPath: [],
    text: [
      doc.title,
      [].concat(doc.meta.tags || []).join(', '),
      ...doc.sections.flatMap(s => s.blocks.filter(b => b.kind === 'callout').map(b => b.text)),
      ...doc.sections.filter(s => s.level === 2).map(s => '## ' + s.title),
    ].filter(Boolean).join('\n'),
  }])
  .index('docs', rag.bm25({ fields: { title: 3, text: 1 } }))
  .index('meta', rag.terms({ keys: ['type', 'tags'] }));

const plan = rag.plan('per-doc').over(corpus)
  .signals({ q: 'Which project deals with OAuth?', k: 5, filters: {} })
  .retrieve('docs', { k: 11 })
  .retrieve('meta', { k: 11 })
  .fuse.rrf({ weights: { docs: 1, meta: 0.5 } })
  .filter.where();

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens({ docOnly: true }), app: kit.searchApp(plan, { title: 'Which report?', answer: false }) };
