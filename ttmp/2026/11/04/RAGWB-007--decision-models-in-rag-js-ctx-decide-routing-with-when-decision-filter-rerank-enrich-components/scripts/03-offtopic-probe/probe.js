// RAGWB-007 phase 6: how decisively does djev reject off-topic queries?
// ragwb --decide pi --no-store verbs --dir <this dir> probe offtopic --format json
const { rag, corpus } = require("./lib/corpus");
__package__({ name: "probe" });

const plan = rag.plan("probe").over(corpus)
  .signals({ q: "", k: 20 })
  .retrieve("text", { k: 30 })
  .retrieve("meta", { k: 30 })
  .fuse.rrf({ k: 60 })
  .rerank.decide({ top: 20, keep: 20, corpus: "engineering project reports and design notes" });

const QUERIES = [
  ["off", "what is the boiling point of water at sea level"],
  ["off", "best pasta carbonara recipe"],
  ["off", "who won the 1998 football world cup"],
  ["off", "how do I renew my passport"],
  ["on", "how does the rebalance law preserve placements"],
  ["on", "what is the bleve index used for"],
  ["on", "how are delta builds stored"],
  ["on", "what does the headless engine do in the PBUI workbench"],
];
async function offtopic() {
  const rows = [];
  for (const [kind, q] of QUERIES) {
    const r = await rag.run(plan, { q }, { trigger: "submit" });
    const ps = r.hits.map(h => h.score);
    rows.push({ kind, q, max_p: +(Math.max(...ps)).toFixed(3), above_0_5: ps.filter(p => p >= 0.5).length, graded: ps.length, ms: r.ms });
  }
  return rows;
}
__verb__("offtopic", { short: "Max p(relevant) of graded hits for off- and on-topic queries", fields: {} });
