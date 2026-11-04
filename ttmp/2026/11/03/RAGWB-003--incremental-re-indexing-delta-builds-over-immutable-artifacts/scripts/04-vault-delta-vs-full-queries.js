// Usage: S=<dir with vault/ (markdown copy) and parc/ (examples/apps/parc with VAULT pointed at $S/vault)>; replace $S below, then
// ragwb --store $S/store --root $S/vault --root $S run -v <this file>
const rag = require("ragwb");
const { corpus } = require("$S/parc/lib/corpus.js");
(async () => {
  const c = corpus();
  const plan = rag.plan("p").over(c).signals({ q: "", k: 10 }).retrieve("text", { k: 30 }).limit();
  const ir = rag.compile(plan), eng = rag.engine();
  await eng.build(ir);
  const art = eng.artifacts(ir);
  const out = { n: art.chunks.size, q: {} };
  for (const q of ["zebra edit", "goja regexp unicode", "sqlite forum for agents", "lunaroute embeddings cache", "Failure modes", "bleve scorch segments", "project status done"]) {
    const r = await rag.run(plan, { q });
    out.q[q] = r.top(10).map(h => [h.id, h.score]);
  }
  out.meta = art.indexes.get("meta").search("project-report active go", 20).map(h => [h.id, h.score]);
  console.log("RESULT " + JSON.stringify(out));
})();
