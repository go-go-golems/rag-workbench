// Fusion · three retrievers combined with reciprocal rank fusion.
// Variants swap the fusion rule (replace op) and its weights (set ops). Fusion is a live node:
// changing it never rebuilds an index.
// Try: Evals → tick all variants → Run. Then select the fuse node in the DAG and edit weights.

const corpus = kit.reports()
  .index('headings', rag.bm25({ fields: { headingPath: 1 } }));

const plan = rag.plan('fusion').over(corpus)
  .signals({ q: 'How are arrays of objects flattened into metadata terms?', k: 10, filters: {} })
  .retrieve('text', { k: 40 })
  .retrieve('headings', { k: 20 })
  .retrieve('meta', { k: 20 })
  .fuse.rrf({ k: 60, weights: { text: 1, headings: 0.7, meta: 0.3 } })
  .filter.where()
  .limit(s => s.k);

const max = plan.variant('fuse-max', [{ op: 'replace', node: 'fuse', with: { t: 'fuse.max', p: {} } }]);
const k10 = plan.variant('rrf-k10', [{ op: 'set', node: 'fuse', path: 'k', value: 10 }]);
const textOnly = plan.variant('text-weight-only', [{ op: 'set', node: 'fuse', path: 'weights', value: { text: 1, headings: 0, meta: 0 } }]);
const equal = plan.variant('equal-weights', [{ op: 'set', node: 'fuse', path: 'weights', value: { text: 1, headings: 1, meta: 1 } }]);

const ops = []; // end ops
return { plan: plan.patch(ops), variants: [max, k10, textOnly, equal], evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Fusion', answer: false }) };
