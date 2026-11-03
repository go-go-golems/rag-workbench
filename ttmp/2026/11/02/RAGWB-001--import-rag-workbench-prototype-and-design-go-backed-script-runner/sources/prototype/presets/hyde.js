// HyDE · search with a hypothetical answer.
// On Enter, a live lambda asks Claude for a short paragraph that would answer the question and
// adds it as an extra query. The hyde signal (a toggle) turns it off without editing code.
// Try: compare results with the toggle on and off for "why were component counts fixed by rule?".

const plan = rag.plan('hyde').over(kit.reports())
  .signals({ q: 'Why were the component counts fixed by a rule instead of chosen by BIC?', k: 10, hyde: true })
  .rewrite(async (s, ctx, queries, p) => {
    if (!s.hyde) return queries;
    const r = await ctx.sample(`Write one plausible paragraph (about ${p.words} words) from an engineering project report that answers: ${s.q}\nNo preamble, no headings.`, { modelTier: 'quick' });
    ctx.note(r.text.slice(0, 160));
    return [...queries, r.text];
  }, { trigger: 'submit', words: 80 })
  .retrieve('text', { k: 30, perQuery: true })
  .limit(s => s.k);

const app = gui.app('hyde').title('HyDE').use(plan).state({ selected: null })
  .view(v => v.row(r => r
    .col(c => c.searchBox('q').toggle('hyde', 'Add a hypothetical answer as a query (1 quick call on Enter)').status().results(), { grow: 3 })
    .col(c => c
      .card('Hypothetical answer', b => b.text(s => { const t = s.result && s.result.trace.find(x => x.node === 'rewrite'); return t && t.notes ? '> ' + t.notes[0] + '…' : '_Press Enter to draft one._'; }))
      .chunk(s => s.selected), { grow: 2 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
