// Usage: S=<dir with vault/ (markdown copy) and parc/ (examples/apps/parc with VAULT pointed at $S/vault)>; replace $S below, then
// ragwb --store $S/store --root $S/vault --root $S run -v <this file>
const rag = require("ragwb");
const { corpus } = require("$S/parc/lib/corpus.js");
(async () => {
  const c = corpus();
  const plan = rag.plan("p").over(c).signals({ q: "" }).retrieve("text", { k: 5 }).limit();
  const ir = rag.compile(plan), eng = rag.engine();
  const rep = await eng.build(ir);
  for (const [id, st] of Object.entries(rep.nodes)) console.log(id, st.ms, st.incremental ? "INC " + JSON.stringify(st.delta) : (st.reused ? "reused" : "full"));
})();
