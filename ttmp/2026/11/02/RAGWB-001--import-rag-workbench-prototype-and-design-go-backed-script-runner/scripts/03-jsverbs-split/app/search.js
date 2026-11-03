// search.js — a query file. Each top-level function is a CLI verb (jsverbs); rows come back as Glazed output.
const { rag, engine } = require("./lib/corpus.js");
const plans = require("./lib/plans.js");

__package__({ name: "reports", short: "Search the bundled reports" });

async function search(q, k, plan) {
  const ir = rag.compile(plans[plan]);
  const r = await engine.run(ir, { q, k });
  return r.top(k).map((h, i) => ({ rank: i + 1, score: +h.score.toFixed(3), doc: h.doc, heading: h.heading, id: h.id }));
}
__verb__("search", {
  short: "Run a plan for one query",
  fields: {
    q: { argument: true, help: "query text" },
    k: { type: "int", default: 5 },
    plan: { type: "choice", choices: ["lexical", "fused"], default: "lexical" },
  },
});
