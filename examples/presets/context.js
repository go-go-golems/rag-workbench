// Context expansion · neighbours and linked notes fill the answer's context.
// expand.neighbors adds the section after each top hit; expand.links follows [[wiki links]] and
// related_vault_notes to the best section of each linked report. pack orders by document.
// Try: the results show which hits came from neighbors or links (chips on the right).

const plan = rag.plan('context').over(kit.reports())
  .signals({ q: 'How does batch-preserving sync relate to idempotent operation ids?', k: 6 })
  .retrieve('text', { k: 30 })
  .limit(s => s.k)
  .expand.neighbors({ before: 0, after: 1, top: 4 })
  .expand.links({ decay: 0.4, max: 3, top: 3 })
  .pack({ budget: 40000, order: 'doc' })
  .answer();

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Context expansion', facets: false }) };
