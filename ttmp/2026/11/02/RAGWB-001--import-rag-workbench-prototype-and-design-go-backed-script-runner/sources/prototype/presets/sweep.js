// Parameter sweep · every combination of two params, scored on the goldens.
// rag.sweep applies set ops per configuration. The heading weight is a build param (rebuilds
// ix-text only, cached by digest); fuse.k is live (no rebuild). No Claude calls.
// Try: run it, then set the best values with the DAG tab's param editor and Keep in script.

const plan = rag.plan('sweep').over(kit.reports())
  .signals({ q: 'What does the rebalance law say?', k: 10 })
  .retrieve('text', { k: 40 })
  .retrieve('meta', { k: 20 })
  .fuse.rrf({ k: 60, weights: { text: 1, meta: 0.3 } })
  .limit(s => s.k);

const app = gui.app('sweep').title('Parameter sweep').use(plan)
  .state({ busy: false, rows: [], raw: [] })
  .view(v => v.col(c => c
    .note('Runs the 12 goldens for each of 12 configurations: fuse.k × the heading-path weight of the text index.', 'info')
    .button(s => (s.busy ? 'Sweeping…' : 'Run sweep · 12 configs'), async (s, ctx) => {
      ctx.set({ busy: true });
      const res = await ctx.lib.rag.sweep(ctx.ws, { 'fuse.k': [5, 20, 60, 120], 'ix-text.fields.headingPath': [1, 3, 6] }).run(ctx.evals, { metric: 'mrr' });
      ctx.set({
        busy: false, raw: res.rows,
        rows: res.rows.map(x => ({ 'fuse.k': x.config['fuse.k'], heading: x.config['ix-text.fields.headingPath'], mrr: x.mrr, 'hit@5': x['hit@5'], 'ndcg@10': x['ndcg@10'], rebuilt: x.rebuilt.join(' ') || '—' })),
      });
    }, { tone: 'primary', disabled: s => s.busy })
    .row(r => r
      .card('mrr by fuse.k', b => b.line(s => { const by = {}; for (const x of s.raw) (by['heading ×' + x.config['ix-text.fields.headingPath']] ??= []).push([x.config['fuse.k'], x.mrr]); return by; }, { xLabel: 'fuse.k', yLabel: 'mrr' }))
      .card('Configurations, best first', b => b.table(s => s.rows)))
    .searchBox('q')
    .results({ limit: 6 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
