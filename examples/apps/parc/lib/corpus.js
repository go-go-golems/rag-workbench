// lib/corpus.js — the go-go-parc Obsidian vault as a corpus.
// Run verbs with: ragwb --root ~/code/wesen/go-go-golems/go-go-parc verbs --dir examples/apps/parc …
const rag = require("ragwb");

const VAULT = "~/code/wesen/go-go-golems/go-go-parc";
// Tooling and noise found by the vault survey (RAGWB-001 analysis §4).
const EXCLUDE = [".pi/**", "ttmp/**", ".trash/**", "**/_assets/**", "scripts/**", "Attachments/**"];

// corpus(include) → a corpus spec over a subset of the vault (default: everything).
function corpus(include) {
  const inc = include && include.length ? include : ["**/*.md"];
  return rag.corpus("parc")
    .from(rag.source.dir({ root: VAULT, include: inc, exclude: EXCLUDE }))
    .parse(rag.markdown())
    .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
    .index("text", rag.bm25({ fields: { title: 3, headingPath: 2, text: 1 } }))
    .index("meta", rag.terms({ keys: ["type", "tags", "status"] }));
}

function plan(include) {
  return rag.plan("parc").over(corpus(include))
    .signals({ q: "", k: 10, filters: {} })
    .retrieve("text", { k: 30 })
    .filter.where({ field: "filters" })
    .limit();
}

module.exports = { rag, corpus, plan, VAULT, EXCLUDE };
