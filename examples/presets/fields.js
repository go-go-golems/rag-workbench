// Field weights · BM25F over title, heading path, callouts and text.
// Each variant changes one index param with a set op. Index params are build params, so each
// variant rebuilds only its index. Open Evals, tick the variants and Run to compare.
// Try: a variant that sets k1 to 2.0, or one that drops callouts.

const corpus = rag.corpus('reports')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  .index('text', rag.bm25({ fields: { title: 2, headingPath: 3, callout: 2, text: 1 }, k1: 1.2, b: 0.75 }));

const plan = rag.plan('fields').over(corpus)
  .signals({ q: 'opaque server revision token', k: 10 })
  .retrieve('text', { k: 30 });

const textOnly = plan.variant('text-only', [{ op: 'set', node: 'ix-text', path: 'fields', value: { text: 1 } }]);
const headings = plan.variant('headings-x6', [{ op: 'set', node: 'ix-text', path: 'fields.headingPath', value: 6 }]);
const flatB = plan.variant('b-0.3', [{ op: 'set', node: 'ix-text', path: 'b', value: 0.3 }]);

const ops = []; // end ops
return { plan: plan.patch(ops), variants: [textOnly, headings, flatB], evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Field weights', answer: false, facets: false }) };
