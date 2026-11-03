// evals.js — golden questions as their own command: one metrics row per plan.
const { rag } = require("./lib/corpus");
const kit = require("ragwb/kit");
const plans = require("./lib/plans");

__package__({ name: "reports" });

async function goldens(names, trigger) {
  const rows = [];
  for (const name of names) {
    if (!plans[name]) throw new Error("unknown plan " + name + " (have: " + Object.keys(plans).join(", ") + ")");
    const ev = await kit.goldens().run(rag.compile(plans[name]), { engine: rag.engine(), trigger });
    rows.push({ plan: name, trigger, n: ev.summary.n, "hit@5": ev.summary["hit@5"], mrr: ev.summary.mrr, "ndcg@10": ev.summary["ndcg@10"] });
  }
  return rows;
}
__verb__("goldens", {
  short: "Score plans on the 12 golden questions",
  fields: {
    names: { type: "stringList", default: ["lexical", "fused"], help: "Plans to score (dense ones need --embed lunaroute)" },
    trigger: { type: "choice", choices: ["change", "submit"], default: "change", help: "submit includes rerank.model / LLM nodes" },
  },
});
