// Doc cards with Claude · a cheap semantic layer, one quick call per document.
// A card per document (title + h2 headings) is enriched by rag.enrich.llm with a summary,
// questions and keywords. A routed retriever finds the best cards, then searches inside those docs.
// Build cost: about 11 quick calls, made only when you press Build. Until then the cards are
// plain headings and routing still works lexically. Results are cached per card.

const corpus = kit.reports()
  .chunk('cards', (doc, ctx) => [{
    kind: 'card',
    headingPath: ['Document card'],
    text: [doc.title, ...doc.sections.filter(s => s.level === 2).map(s => s.title)].join('\n'),
  }])
  .enrich('llm', rag.enrich.llm({ tier: 'quick' }), { from: 'cards' })
  .index('cards', rag.bm25({ fields: { title: 2, text: 1, 'llm.summary': 2, 'llm.questions': 3, 'llm.keywords': 3 } }), { from: 'enrich_llm' });

const plan = rag.plan('cards').over(corpus)
  .signals({ q: 'Which report explains why a criterion keeps adding clusters?', k: 8 })
  .retrieve('text', { k: 40 })
  .retrieve((s, ctx, queries, p) => {
    const q = queries.join(' ');
    const docs = ctx.index('cards').search(q, p.docs).map(h => ctx.chunk(h.id).docId);
    return docs.flatMap(d => ctx.index('text').searchIn(d, q, p.perDoc));
  }, { id: 'routed', docs: 3, perDoc: 4 })
  .fuse.rrf({ weights: { text: 1, routed: 1.2 } })
  .limit(s => s.k);

const app = gui.app('cards').title('Doc cards').use(plan).state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c.searchBox('q').status().results(), { grow: 3 })
    .col(c => c.tabs(t => t
      .tab('Section', b => b.chunk(s => s.selected))
      .tab('Cards', b => b.table((s, ctx) => { s.out; return ctx.rag.chunks(x => x.kind === 'card').map(x => ({ doc: x.title.replace(/^PROJECT REPORT - |^PROJ - /, '').slice(0, 40), summary: x.llm ? x.llm.summary : '— press Build to enrich' })); }, { columns: ['doc', 'summary'] }))), { grow: 2 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
