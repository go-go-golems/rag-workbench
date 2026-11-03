// Document browser · pick a report, read its outline, or search inside it.
// A select is bound to a plain state key; a watcher turns it into the filters signal, which the
// plan's filter.where() reads (filters.doc matches the document id).
// Try: choose a report with an empty search box, then type to search inside it.

const plan = rag.plan('browse').over(kit.reports())
  .signals({ q: '', k: 20, filters: {} })
  .retrieve('text', { k: 80 })
  .filter.where()
  .limit(s => s.k);

const app = gui.app('browse').title('Document browser').use(plan)
  .state({ doc: '', selected: null })
  .watch('doc', (s, ctx, v) => ctx.set({ filters: v ? { doc: v } : {}, selected: null }))
  .view(v => v.row(r => r
    .col(c => c
      .select('doc', (s, ctx) => [{ value: '', label: 'All documents' }, ...ctx.rag.docs().map(d => ({ value: d.id, label: d.title.replace(/^PROJECT REPORT - |^PROJ - /, '') }))], { label: 'Document' })
      .searchBox('q', { placeholder: 'Search inside…', submitLabel: 'Search' })
      .when(s => s.doc && !s.q, w => w.table((s, ctx) => ctx.rag.chunks(x => x.docId === s.doc).map(x => ({ section: x.headingPath.join(' › ') || 'intro', tokens: x.tokens, id: x.id })), { columns: ['section', 'tokens'], onRowClick: (s, ctx, row) => ctx.set({ selected: row.id }) }))
      .when(s => !s.doc && !s.q, w => w.note('Choose a document to see its outline, or type to search everything.', 'info'))
      .when(s => s.q, w => w.status().results()), { grow: 3 })
    .col(c => c.chunk(s => s.selected), { grow: 2 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
