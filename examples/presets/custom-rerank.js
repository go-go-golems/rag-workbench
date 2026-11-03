// Rerank with a lambda · no Claude calls.
// The rerank lambda receives the hits as views (title, headingPath, text, meta, tokens) and its
// params as the last argument. It runs on every keystroke because its trigger is 'change'.
// Try: raise p.heading to 3, or edit the params in the DAG tab (select the rerank node).

const plan = rag.plan('rerank').over(kit.reports())
  .signals({ q: 'Why did ChatGPT show no app actions?', k: 10 })
  .retrieve('text', { k: 40 })
  .rerank((s, ctx, hits, p) => {
    const words = String(s.q).toLowerCase().match(/[a-z0-9]{4,}/g) || [];
    const overlap = t => words.filter(w => t.toLowerCase().includes(w)).length / Math.max(1, words.length);
    return hits.map(h => {
      const o = overlap(h.headingPath.join(' '));
      let score = h.score * (1 + p.heading * o);
      if (h.text.includes('```')) score *= 1 + p.code;
      if (h.tokens < p.minTokens) score *= p.shortPenalty;
      return { id: h.id, score, reason: `heading overlap ${o.toFixed(2)}` };
    }).sort((a, b) => b.score - a.score);
  }, { heading: 1.5, code: 0, minTokens: 60, shortPenalty: 0.6 })
  .limit(s => s.k);

const none = plan.variant('no-rerank', [{ op: 'remove', node: 'rerank' }]);
const strong = plan.variant('heading-x3', [{ op: 'set', node: 'rerank', path: 'heading', value: 3 }]);

const ops = []; // end ops
return { plan: plan.patch(ops), variants: [none, strong], evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Lambda rerank', answer: false, facets: false }) };
