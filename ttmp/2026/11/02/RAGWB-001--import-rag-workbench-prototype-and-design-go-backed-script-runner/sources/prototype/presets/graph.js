// Graph form · the same kind of plan with every node and edge named.
// rag.graph wires inputs explicitly; the chain form infers them. Both compile to the same IR,
// so the DAG tab, ops and evals work the same way.
// Try: add a second retriever node and list it in the fuse node's `from`.

const corpus = kit.reports();

const plan = rag.graph('graph', g => {
  g.over(corpus);
  const q = g.signal('q', 'Why did the browser ECS service restart in a loop?');
  g.signal('k', 8);
  g.signal('filters', {});
  const lex = g.node('lex', rag.retrieve('text', { k: 40 }), { from: q });
  const tags = g.node('tags', rag.retrieve('meta', { k: 20 }), { from: q });
  const fused = g.node('fuse', rag.fuse.rrf({ k: 60, weights: { lex: 1, tags: 0.3 } }), { from: [lex, tags] });
  const kept = g.node('filter', rag.filter.where(), { from: fused });
  const top = g.node('top', rag.limit(s => s.k), { from: kept });
  const ctxt = g.node('pack', rag.pack({ budget: 30000 }), { from: top });
  g.output('hits', top);
  g.output('answer', g.node('answer', rag.answer(), { from: ctxt }));
});

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Graph form' }) };
