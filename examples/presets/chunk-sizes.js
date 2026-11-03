// Chunk sizes · one plan, four chunking variants.
// Variants are ops on the chunk node. A build param change rebuilds only the chunks and the
// index after it; parsing is reused by digest. Open Evals, tick the variants and Run.
// Try: add { op: 'set', node: 'chunks', path: 'prefixHeading', value: true } as a variant.

const corpus = rag.corpus('reports')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  .index('text', rag.bm25({ fields: { title: 2, headingPath: 3, text: 1 } }));

const plan = rag.plan('chunks').over(corpus)
  .signals({ q: 'How does the long-poll loop avoid busy waiting?', k: 10 })
  .retrieve('text', { k: 30 })
  .expand.neighbors({ after: 1, top: 3 });

const small = plan.variant('200-tok', [{ op: 'set', node: 'chunks', path: 'maxTokens', value: 200 }]);
const large = plan.variant('900-tok', [{ op: 'set', node: 'chunks', path: 'maxTokens', value: 900 }]);
const h2only = plan.variant('h2-only', [{ op: 'set', node: 'chunks', path: 'depth', value: 2 }]);
const overlap = plan.variant('overlap-40', [{ op: 'set', node: 'chunks', path: 'overlap', value: 40 }]);

const ops = []; // end ops
return { plan: plan.patch(ops), variants: [small, large, h2only, overlap], evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Chunk sizes', answer: false, facets: false }) };
