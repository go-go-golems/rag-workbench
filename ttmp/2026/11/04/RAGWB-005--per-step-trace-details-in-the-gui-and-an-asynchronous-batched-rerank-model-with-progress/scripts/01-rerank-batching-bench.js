// RAGWB-005: rerank.model against the real backend, one request vs. batches.
// Usage (from the repo root):
//   ./dist/ragwb --embed lunaroute --no-store \
//     run --files examples/apps/reports/corpus ttmp/2026/11/04/RAGWB-005--*/scripts/01-rerank-batching-bench.js
// For each configuration and query: total rerank ms, ms to the first progress
// event, the number of progress events, whether the order of the 30 reranked
// passages and the top 10 equal the one-request run, the largest score
// difference against it, and the latency of a lexical query issued while the
// rerank is in flight.
// The file is the async body of a workbench script; it returns { plan }.
const rag = require("ragwb");
const corpus = rag.corpus("bench").from(rag.files("*.md"))
  .parse(rag.markdown()).chunk(rag.sections()).index("text", rag.bm25());
const plan = (batch, concurrency) => rag.plan("rr-" + batch + "-" + concurrency).over(corpus)
  .signals({ q: "", k: 10 }).retrieve("text", { k: 30 })
  .rerank.model({ model: "bge-rr-v2-m3", k: 30, batch, concurrency }).limit();
const lexical = rag.plan("lex").over(corpus).signals({ q: "", k: 5 }).retrieve("text", { k: 5 }).limit();
const configs = [[30, 1], [8, 2], [4, 4]];
const queries = [
  "What happens to product flags when a product is deleted?",
  "how are server-sent events cancelled when the client disconnects",
  "incremental reindexing of changed documents",
  "why does goja not match unicode property escapes",
];
const now = () => Date.now();
{
  await rag.run(lexical, { q: "warm" });
  const out = [];
  for (const q of queries) {
    let reference = null;
    for (const [batch, conc] of configs) {
      const p = plan(batch, conc);
      await rag.run(p, { q: "warm" }); // build and compile; the rerank node does not run on 'change'
      const r0 = await rag.run(p, { q }, { trigger: "submit" }); // warm the backend for this query
      const t0 = now();
      let firstProgress = null, progress = 0;
      const sc = rag.newScope(ev => { if (ev.type === "progress") { progress++; if (firstProgress == null) firstProgress = now() - t0; } });
      const pending = rag.withScope(sc, () => rag.run(p, { q }, { trigger: "submit" }));
      const l0 = now();
      await rag.run(lexical, { q: "rerank batches" });
      const lexMs = now() - l0;
      const r = await pending;
      const total = now() - t0;
      const rr = r.trace.find(e => e.node === "rerank");
      // all 30 reranked passages: order, top-10 set and score drift against the one-request run
      const ranked = r.outputs.rerank.slice(0, 30).map(h => [h.id, h.score]);
      if (reference == null) reference = ranked;
      const refScore = new Map(reference.map(([id, s]) => [id, s]));
      const ids = x => x.map(([id]) => id);
      out.push({ q: q.slice(0, 40), batch, conc, totalMs: total, rerankMs: rr && rr.ms, firstProgressMs: firstProgress, progress,
        sameOrder: ids(ranked).join() === ids(reference).join(),
        sameTop10: ids(ranked).slice(0, 10).join() === ids(reference).slice(0, 10).join(),
        maxScoreDiff: +Math.max(...ranked.map(([id, s]) => Math.abs(s - refScore.get(id)))).toFixed(4),
        lexicalDuringMs: lexMs, errors: [...r0.errors, ...r.errors] });
    }
  }
  console.log("RESULT " + JSON.stringify(out));
}
return { plan: lexical };
