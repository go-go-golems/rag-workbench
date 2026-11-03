// Judged evals · score answers, not just rankings.
// Cases can say what the answer must contain (answerHas); judges are live lambdas that ask Claude
// to grade each answer. Use Evals → "Run with Claude" (about 4 calls per case).
// Try: tighten the "concise" judge, or add answerHas to more cases.

const plan = rag.plan('judged').over(kit.reports())
  .signals({ q: 'What rule picked eight latent classes?', k: 6 })
  .retrieve('text', { k: 30 })
  .rerank.llm({ top: 15, keep: s => s.k })
  .pack({ budget: 24000 })
  .answer({ tier: 'default' });

const evals = kit.goldens()
  .case('Which tier of the explorer verified KPIs in a browser, and what did it check?', { doc: 'Tier B', section: /customer page/i, answerHas: /282,180|77,187/ })
  .judge('grounded', async (s, ctx, c) => {
    const v = await ctx.sample.json('Is every claim in the answer supported by the evidence? Reply with only JSON {"grounded": boolean}.\n\nAnswer:\n' + c.answer + '\n\nEvidence:\n' + c.evidence.join('\n---\n').slice(0, 20000), { modelTier: 'quick' });
    return v && v.grounded ? 1 : 0;
  })
  .judge('concise', async (s, ctx, c) => {
    const v = await ctx.sample.json('Does this answer address the question directly in under 150 words without filler? Reply with only JSON {"ok": boolean}.\n\nQuestion: ' + c.q + '\n\nAnswer:\n' + c.answer, { modelTier: 'quick' });
    return v && v.ok ? 1 : 0;
  });

const ops = []; // end ops
return { plan: plan.patch(ops), evals, app: kit.searchApp(plan, { title: 'Judged answers', facets: false }) };
