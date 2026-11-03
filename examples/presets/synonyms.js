// Query expansion without Claude · two ways.
// Index side: .synonyms() expands phrases inside BM25 at half weight. Query side: a rewrite
// lambda adds acronym expansions as extra queries, from a table passed in as a param.
// Try: "what is the knee rule for LCA", "why did DCR fail", "BIC keeps buying components".

const corpus = kit.reports()
  .synonyms({ 'oauth': ['authorization', 'token'], 'cursor': ['sequence', 'next_cursor'], 'stateless': ['server/discover', 'sessionless'] });

const plan = rag.plan('synonyms').over(corpus)
  .signals({ q: 'what is the knee rule for LCA', k: 10 })
  .rewrite((s, ctx, queries, p) => {
    const q = String(s.q).toLowerCase();
    const extra = Object.entries(p.acronyms)
      .filter(([a]) => new RegExp('\\b' + a + '\\b').test(q))
      .map(([a, full]) => q.replace(new RegExp('\\b' + a + '\\b', 'g'), full));
    return [...queries, ...extra];
  }, { acronyms: {
    lca: 'latent class analysis', dcr: 'dynamic client registration', rfm: 'recency frequency monetary',
    ecs: 'elastic container service', alb: 'application load balancer', gmm: 'gaussian mixture model',
    bic: 'bayesian information criterion', pkce: 'proof key for code exchange', mcp: 'model context protocol',
    ir: 'intermediate representation', ari: 'adjusted rand index',
  } })
  .retrieve('text', { k: 30, perQuery: true })
  .limit(s => s.k);

const noRewrite = plan.variant('no-rewrite', [{ op: 'remove', node: 'rewrite' }]);
const noSynonyms = plan.variant('no-synonyms', [{ op: 'remove', node: 'synonyms' }]);

const ops = []; // end ops
return { plan: plan.patch(ops), variants: [noRewrite, noSynonyms], evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Query expansion', answer: false, facets: false }) };
