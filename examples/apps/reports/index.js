// index.js — build the corpus and report what each build node did.
const { rag, corpus, corpusDense } = require("./lib/corpus");

__package__({ name: "reports" });

async function index(dense) {
  const report = await rag.build(dense ? corpusDense : corpus);
  if (!report.ok) throw new Error(report.errors.join("\n"));
  return Object.values(report.nodes).map(n => ({ node: n.id, type: n.t, items: n.count, ms: Math.round(n.ms), reused: n.reused }));
}
__verb__("index", {
  short: "Build the corpus (files → docs → chunks → indexes)",
  fields: { dense: { type: "bool", default: false, help: "Also build the vector index (needs --embed lunaroute)" } },
});
