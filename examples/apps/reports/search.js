// search.js — `ragwb verbs --dir examples/apps/reports reports search "query"`
const { rag } = require("./lib/corpus");
const plans = require("./lib/plans");

__package__({ name: "reports", short: "Search the eleven bundled project reports" });

async function search(q, k, plan, trigger) {
  const r = await rag.run(plans[plan], { q, k }, { trigger });
  return r.top(k).map((h, i) => ({ rank: i + 1, score: h.score, doc: h.doc, heading: h.heading, id: h.id }));
}
__verb__("search", {
  short: "Run one query through a plan",
  fields: {
    q: { argument: true, help: "Query text" },
    k: { type: "int", default: 5, help: "Number of hits" },
    plan: { type: "choice", choices: ["lexical", "fused", "semantic", "hybrid", "hybrid-rerank"], default: "lexical", help: "Which plan in lib/plans.js (dense plans need --embed lunaroute)" },
    trigger: { type: "choice", choices: ["change", "submit"], default: "submit", help: "submit also runs submit-trigger nodes (rerank.model, LLM nodes)" },
  },
});

async function explain(q, id, plan) {
  const r = await rag.run(plans[plan], { q, k: 20 });
  return r.explain(id).path.map(step => ({ node: step.node, rank: step.rank, score: step.score, terms: step.terms ? JSON.stringify(step.terms) : "" }));
}
__verb__("explain", {
  short: "Show how one chunk moved through the plan's nodes",
  fields: {
    q: { argument: true }, id: { argument: true, help: "Chunk id (from search)" },
    plan: { type: "choice", choices: ["lexical", "fused", "semantic", "hybrid", "hybrid-rerank"], default: "lexical" },
  },
});
