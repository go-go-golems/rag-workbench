// Agentic answer · one node that searches, reads and cites with tools.
// The agentic node gives Claude search/read/outline/documents tools over the indexes and a read
// budget. It runs on Enter only; typing does nothing here. Evals score the sections it read.
// Try: "Across all projects, which bugs came from something silently being empty or nil?"

const corpus = kit.reports()
  .chunk('rows', (doc, ctx) => doc.sections.flatMap(sec => sec.blocks
    .filter(b => b.kind === 'table')
    .flatMap(t => t.rows.map(row => ({ kind: 'row', headingPath: [...sec.path, row[0]], text: t.header.map((h, j) => `${h}: ${row[j] ?? ''}`).join('\n') })))))
  .index('rows', rag.bm25({ fields: { headingPath: 2, text: 1 } }), { from: 'rows' });

const plan = rag.plan('agentic').over(corpus)
  .signals({ q: 'Across all projects, which bugs came from something silently being empty or nil?', k: 8 })
  .agentic({ maxReads: 8, indexes: ['text', 'rows'], tier: 'default' });

const app = gui.app('agentic').title('Agentic answer').use(plan).state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c
      .searchBox('q', { submitLabel: 'Ask' })
      .note('Press Enter: the agent searches several phrasings, reads sections and cites them. Typing alone does nothing in this plan.', 'info')
      .answer({ title: 'Agent answer' }), { grow: 3 })
    .col(c => c
      .card('Tool calls', b => b.text(s => { const t = s.result && s.result.trace.find(x => x.node === 'agentic'); return t && t.notes ? t.notes.map(n => '- `' + n + '`').join('\n') : '_No run yet._'; }))
      .chunk(s => s.selected), { grow: 2 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
