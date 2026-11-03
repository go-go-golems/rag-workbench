// evals.js — golden questions as their own file; run every plan variant and emit one metrics row each.
const { rag, engine } = require("./lib/corpus.js");
const plans = require("./lib/plans.js");

__package__({ name: "reports" });

const goldens = rag.evals("goldens")
  .case("What does the rebalance law say?", { doc: "Workbench Core", section: /rebalance/i })
  .case("How does protojson serialize int64 values?", { doc: "Agentforum Web", section: /type traps|protobuf/i })
  .case("Why did ChatGPT show no actions after OAuth worked?", { doc: "CoinVault", section: /no actions|stateless/i })
  .case("Why is a content hash not enough for an operation id?", { doc: "Workbench Identity", section: /separately intended|content hash/i })
  .metrics(["hit@5", "mrr", "ndcg@10"]);

async function goldensVerb() {
  const rows = [];
  for (const [name, plan] of Object.entries(plans)) {
    const ev = await goldens.run(rag.compile(plan), { engine });
    rows.push({ plan: name, n: ev.summary.n, "hit@5": ev.summary["hit@5"], mrr: ev.summary.mrr, "ndcg@10": ev.summary["ndcg@10"] });
  }
  return rows;
}
__verb__("goldensVerb", { name: "goldens", short: "Score each plan on the golden questions", fields: {} });
