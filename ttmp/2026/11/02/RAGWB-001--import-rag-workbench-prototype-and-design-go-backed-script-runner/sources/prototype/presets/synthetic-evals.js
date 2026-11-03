// Synthetic evals · let Claude write questions for sampled sections, then score them.
// rag.evals.generate picks sections, asks for one question each (one quick call per 8), and
// expects that section back. Synthetic cases are marked, never mixed silently with goldens.
// Try: generate 8, then compare the score with the hand-written goldens in the Evals tab.

const plan = rag.plan('synthetic').over(kit.reports())
  .signals({ q: 'idempotency key primary key', k: 10, filters: {} })
  .retrieve('text', { k: 40 })
  .retrieve('meta', { k: 20 })
  .fuse.rrf({ weights: { text: 1, meta: 0.3 } })
  .filter.where();

const app = gui.app('synthetic').title('Synthetic evals').use(plan)
  .state({ busy: false, rows: null, summary: null, n: 8 })
  .view(v => v.row(r => r
    .col(c => c.searchBox('q').status().results({ limit: 8 }), { grow: 2 })
    .col(c => c.card('Generate and run', b => b
      .note('Claude writes one question per sampled section; each case expects that section back. One quick call per 8 questions.', 'info')
      .slider('n', { min: 4, max: 16, step: 1, label: 'questions' })
      .button(s => (s.busy ? 'Working…' : 'Generate and run'), async (s, ctx) => {
        ctx.set({ busy: true });
        try {
          const ev = await ctx.lib.rag.evals.generate(ctx.ws, { n: s.n, seed: Date.now() % 997 });
          const rep = await ev.run(ctx.ws);
          ctx.set({ rows: rep.cases.map(x => ({ question: x.q, rank: x.rank ?? '—', 'hit@5': x.m['hit@5'], mrr: +x.m.mrr.toFixed(2) })), summary: rep.summary });
        } catch (e) { ctx.toast('Could not generate: ' + (e.message || e.code)); }
        ctx.set({ busy: false });
      }, { tone: 'primary', disabled: s => s.busy })
      .stats(s => (s.summary ? ['hit@5', 'mrr', 'ndcg@10'].map(k => ({ label: k, value: s.summary[k] })) : []))
      .table(s => s.rows || [], { columns: ['question', 'rank', 'hit@5', 'mrr'] })), { grow: 3 })));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
