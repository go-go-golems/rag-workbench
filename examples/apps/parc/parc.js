// parc.js — index and search the vault from the command line.
const { rag, corpus, plan } = require("./lib/corpus");

__package__({ name: "parc", short: "Search the go-go-parc Obsidian vault" });
__section__("subset", {
  title: "Vault subset",
  fields: { include: { type: "stringList", default: ["**/*.md"], help: "Globs relative to the vault root, e.g. Research/**/*.md" } },
});

async function index(subset, dense) {
  const t0 = Date.now();
  const report = await rag.build(corpus(subset.include, dense));
  if (!report.ok) throw new Error(report.errors.join("\n"));
  const rows = Object.values(report.nodes).map(n => ({ node: n.id, type: n.t, items: n.count, ms: Math.round(n.ms) }));
  rows.push({ node: "total", type: "", items: "", ms: Date.now() - t0 });
  return rows;
}
__verb__("index", {
  short: "Build the vault corpus and report per-node cost",
  sections: ["subset"],
  fields: { subset: { bind: "subset" }, dense: { type: "bool", default: false, help: "Also embed chunks (needs --embed lunaroute)" } },
});

async function search(q, k, mode, subset) {
  const r = await rag.run(plan(subset.include, mode), { q, k });
  return r.top(k).map((h, i) => ({ rank: i + 1, score: h.score, doc: h.doc, heading: h.heading }));
}
__verb__("search", {
  short: "Search the vault",
  sections: ["subset"],
  fields: {
    q: { argument: true }, k: { type: "int", default: 8 },
    mode: { type: "choice", choices: ["lexical", "semantic", "hybrid"], default: "lexical", help: "semantic/hybrid need --embed lunaroute" },
    subset: { bind: "subset" },
  },
});
