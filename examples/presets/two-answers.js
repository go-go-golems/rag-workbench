// Two answers · one context, two answer nodes with different instructions.
// The graph form branches from pack into a brief (quick tier) and a detailed answer. Both run on
// Enter, one after the other: 2 calls.
// Try: change the brief node's instructions in the DAG tab, then press Enter again.

const plan = rag.graph('two-answers', g => {
  g.over(kit.reports());
  const q = g.signal('q', 'How did the TTC project choose the number of latent classes?');
  g.signal('k', 8);
  const lex = g.node('text', rag.retrieve('text', { k: 30 }), { from: q });
  const top = g.node('top', rag.limit(s => s.k), { from: lex });
  const ctxt = g.node('pack', rag.pack({ budget: 30000 }), { from: top });
  g.node('brief', rag.answer({ tier: 'quick', instructions: 'Answer in at most two sentences, using only the evidence below. Cite as [E1].' }), { from: ctxt });
  const full = g.node('detailed', rag.answer({ instructions: 'Answer thoroughly from the evidence below. End with a short bulleted list of the key facts. Cite every fact as [E1], [E2].' }), { from: ctxt });
  g.output('hits', top);
  g.output('answer', full);
});

const app = gui.app('two-answers').title('Two answers').use(plan).state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c.searchBox('q').status().results({ limit: 8 }), { grow: 2 })
    .col(c => c.answer({ from: 'brief', title: 'Brief · quick tier' }).answer({ from: 'detailed', title: 'Detailed' }).chunk(s => s.selected), { grow: 3 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
