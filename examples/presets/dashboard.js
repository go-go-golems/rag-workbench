// Corpus dashboard · stats and charts from the built corpus.
// Lambdas read ctx.rag.docs() and ctx.rag.chunks(). Reading s.out makes them refresh after each
// run, so uploads and rebuilds show up. No Claude calls.
// Try: upload a markdown file in the Files tab and watch the charts change.

const plan = rag.plan('dash').over(kit.reports())
  .signals({ q: '', k: 10 })
  .retrieve('text', { k: 20 });

const app = gui.app('dashboard').title('Corpus dashboard').use(plan).state({ selected: null })
  .view(v => v.col(c => c
    .stats((s, ctx) => {
      s.out;
      const ch = ctx.rag.chunks(), tok = ch.reduce((a, x) => a + x.tokens, 0);
      return [
        { label: 'documents', value: ctx.rag.docs().length }, { label: 'chunks', value: ch.length },
        { label: 'tokens', value: tok }, { label: 'avg tokens', value: Math.round(tok / Math.max(1, ch.length)) },
        { label: 'with code', value: ch.filter(x => x.hasCode).length }, { label: 'with tables', value: ch.filter(x => x.hasTable).length },
      ];
    })
    .row(r => r
      .card('Chunks per document', b => b.bar((s, ctx) => { s.out; return ctx.rag.docs().map(d => [d.title.replace(/^PROJECT REPORT - |^PROJ - /, '').slice(0, 14), ctx.rag.chunks(x => x.docId === d.id).length]); }))
      .card('Chunk size, tokens', b => b.bar((s, ctx) => {
        s.out;
        const edges = [50, 100, 200, 300, 400, 500], ch = ctx.rag.chunks();
        return [...edges.map((e, i) => [`≤${e}`, ch.filter(x => x.tokens > (i ? edges[i - 1] : 0) && x.tokens <= e).length]), ['>500', ch.filter(x => x.tokens > 500).length]];
      })))
    .row(r => r
      .card('Tags', b => b.bar((s, ctx) => { s.out; const m = {}; for (const d of ctx.rag.docs()) for (const t of [].concat(d.meta.tags || [])) m[t] = (m[t] || 0) + 1; return Object.entries(m).sort((a, b) => b[1] - a[1]).slice(0, 10); }))
      .card('Largest sections', b => b.table((s, ctx) => { s.out; return ctx.rag.chunks().sort((a, b) => b.tokens - a.tokens).slice(0, 8).map(x => ({ section: x.headingPath.slice(-1)[0] || 'intro', tokens: x.tokens, id: x.id })); }, { columns: ['section', 'tokens'], onRowClick: (s, ctx, row) => ctx.set({ selected: row.id }) })))
    .chunk(s => s.selected)));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
