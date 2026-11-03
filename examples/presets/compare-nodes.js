// Compare nodes · what each stage of the plan returns, side by side.
// Every node's output is in s.out[nodeId], so results({ from }) can show any hits node. Typing
// updates all three columns; the rerank here is a lambda, so no Claude calls.
// Try: "cursor semantics tension", and watch hits move between columns.

const plan = rag.plan('stages').over(kit.reports())
  .signals({ q: 'cursor semantics tension', k: 8 })
  .retrieve('text', { k: 40 })
  .retrieve('meta', { k: 20 })
  .fuse.rrf({ weights: { text: 1, meta: 0.5 } })
  .rerank((s, ctx, hits, p) => {
    const words = String(s.q).toLowerCase().match(/[a-z0-9]{4,}/g) || [];
    return hits.map(h => ({ id: h.id, score: h.score * (1 + p.boost * words.filter(w => h.headingPath.join(' ').toLowerCase().includes(w)).length), reason: h.headingPath.slice(-1)[0] }))
      .sort((a, b) => b.score - a.score);
  }, { boost: 1 })
  .limit(s => s.k);

const app = gui.app('stages').title('Compare stages').use(plan).state({ selected: null })
  .view(v => v.col(c => c
    .searchBox('q')
    .grid(3, g => g
      .card('text · BM25', b => b.results({ from: 'text', limit: 8 }))
      .card('fuse · RRF with meta', b => b.results({ from: 'fuse', limit: 8 }))
      .card('rerank · heading λ', b => b.results({ from: 'limit', limit: 8 })))
    .chunk(s => s.selected)));

const ops = []; // end ops
return { plan: plan.patch(ops), evals: kit.goldens(), app };
