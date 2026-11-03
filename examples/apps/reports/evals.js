// evals.js — golden questions as their own command: one metrics row per plan.
const { rag } = require("./lib/corpus");
const kit = require("ragwb/kit");
const plans = require("./lib/plans");

__package__({ name: "reports" });

async function goldens() {
  const rows = [];
  for (const [name, plan] of Object.entries(plans)) {
    const ev = await kit.goldens().run(rag.compile(plan), { engine: rag.engine() });
    rows.push({ plan: name, n: ev.summary.n, "hit@5": ev.summary["hit@5"], mrr: ev.summary.mrr, "ndcg@10": ev.summary["ndcg@10"] });
  }
  return rows;
}
__verb__("goldens", { short: "Score every plan on the 12 golden questions", fields: {} });
