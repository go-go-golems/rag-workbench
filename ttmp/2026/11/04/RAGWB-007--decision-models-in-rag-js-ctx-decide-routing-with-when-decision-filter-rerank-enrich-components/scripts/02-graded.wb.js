// RAGWB-007 live check: BM25 top 20, graded by djev, kept top 5 by p(relevant).
const corpus = rag.corpus('reports')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk(rag.sections({ maxTokens: 450 }))
  .index('text', rag.bm25());

const plan = rag.plan('graded').over(corpus)
  .signals({ q: 'how does the rebalance law preserve placements?', k: 5 })
  .classify({ id: 'route', corpus: 'engineering project reports', questions: {
    strategy: { type: 'choice', instructions: 'Which retrieval strategy fits this query?', options: { keyword: 'exact identifiers, names, error strings', semantic: 'conceptual question', none: 'chit-chat, no documents needed' } } } })
  .retrieve('text', { k: 20, when: { decision: 'route.strategy', not: ['none'] } })
  .rerank.decide({ top: 20, keep: 5, corpus: 'engineering project reports' });

return { plan };
