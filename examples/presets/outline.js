// Outline search · index headings only, to jump around a long corpus.
// The chunk lambda emits one small chunk per section: its heading path plus its first sentence.
// The index weights the heading path heavily. Fast, and good for "where is X discussed?".
// Try: "migration", "verification", "open questions".

const corpus = rag.corpus('outline')
  .from(rag.files('*.md'))
  .parse(rag.markdown())
  .chunk((doc, ctx) => doc.sections.filter(s => s.level > 0).map(s => ({
    kind: 'heading',
    headingPath: s.path,
    text: ((s.blocks.find(b => b.kind === 'paragraph') || { text: '' }).text.split(/(?<=[.!?])\s/)[0] || '').slice(0, 300),
  })))
  .index('headings', rag.bm25({ fields: { headingPath: 4, title: 1, text: 1 } }));

const plan = rag.plan('outline').over(corpus)
  .signals({ q: 'migration', k: 30 })
  .retrieve('headings', { k: 40 });

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Outline', answer: false, facets: false, placeholder: 'Find a heading…' }) };
