// Code search · fenced code blocks become their own chunks, tagged with their language.
// rag.define registers a build component; it then appears as rag.chunk.codeBlocks() with its
// own params. The language goes into chunk meta, so it is also a facet.
// Try: "protojson UnmarshalOptions", "eventReason", "crossprod basket matrix".

rag.define('chunk.codeBlocks', {
  phase: 'build', in: 'docs', out: 'chunks', params: { minLines: 2 },
  describe: p => `fences ≥ ${p.minLines} lines`,
  map: (doc, ctx, p) => doc.sections.flatMap(sec => sec.blocks
    .filter(b => b.kind === 'fence' && b.lang !== 'mermaid' && b.text.split('\n').length >= p.minLines)
    .map(b => ({
      kind: 'code',
      headingPath: [...sec.path, (b.lang || 'text') + ' block'],
      text: b.text,
      meta: { type: doc.meta.type, tags: doc.meta.tags, lang: b.lang || 'text' },
    }))),
});

const corpus = kit.reports()
  .chunk('code', rag.chunk.codeBlocks({ minLines: 3 }))
  .index('code', rag.bm25({ fields: { text: 1, headingPath: 2, 'meta.lang': 2 } }), { from: 'code' });

const plan = rag.plan('code').over(corpus)
  .signals({ q: 'protojson UnmarshalOptions', k: 12, filters: {} })
  .retrieve('code', { k: 20 })
  .retrieve('text', { k: 20 })
  .fuse.rrf({ k: 60, weights: { code: 1.2, text: 0.6 } })
  .filter.where()
  .limit(s => s.k);

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app: kit.searchApp(plan, { title: 'Code search', facets: ['lang', 'type'], answer: false, placeholder: 'Search code…' }) };
