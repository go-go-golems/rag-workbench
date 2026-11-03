// Claude rewrite and rerank · the two LLM steps most search stacks add first.
// rewrite.llm turns the question into keyword queries; rerank.llm reorders the top 20 and keeps
// s.k with a one-line reason each. Both run on Enter only (about 2 quick calls).
// Try: press Enter, then open Trace and click the rewrite and rerank rows.

const plan = rag.plan('llm').over(kit.reports())
  .signals({ q: 'Why did ChatGPT say OAuth was unsupported?', k: 8, filters: {} })
  .rewrite.llm({ n: 3, tier: 'quick' })
  .retrieve('text', { k: 40 })
  .filter.where()
  .rerank.llm({ top: 20, keep: s => s.k, tier: 'quick' });

const lexical = plan.variant('lexical', [{ op: 'remove', node: 'rewrite' }, { op: 'remove', node: 'rerank' }]);

const ops = []; // end ops
return { plan: plan.patch(ops), variants: [lexical], evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Rewrite + rerank', answer: false }) };
