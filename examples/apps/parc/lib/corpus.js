// lib/corpus.js — the go-go-parc Obsidian vault as a corpus.
// Run verbs with: ragwb --root ~/code/wesen/go-go-golems/go-go-parc verbs --dir examples/apps/parc …
// Dense/hybrid variants additionally need: --embed lunaroute
const rag = require("ragwb");

const { VAULT, VAULT_NAME } = require("./config");
// Tooling and noise found by the vault survey (RAGWB-001 analysis §4).
const EXCLUDE = [".pi/**", "ttmp/**", ".trash/**", "**/_assets/**", "scripts/**", "Attachments/**"];

// corpus(include, dense) → a corpus spec over a subset of the vault (default: everything).
// dense adds an emb-granite vector index next to BM25 and the frontmatter terms index.
function corpus(include, dense) {
  const inc = include && include.length ? include : ["**/*.md"];
  const lexical = rag.corpus("parc")
    .from(rag.source.dir({ root: VAULT, include: inc, exclude: EXCLUDE, vaultIgnore: true }))
    .parse(rag.markdown())
    .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
    .index("text", rag.bm25({ fields: { title: 3, headingPath: 2, text: 1 } }))
    .index("meta", rag.terms({ keys: ["type", "tags", "status"] }));
  return dense ? lexical.index("vec", rag.vector({ model: "emb-granite" })) : lexical;
}

// plan(include, mode): lexical (BM25), semantic (vectors) or hybrid (BM25 + vectors, RRF).
function plan(include, mode) {
  const m = mode || "lexical";
  let p = rag.plan("parc-" + m).over(corpus(include, m !== "lexical")).signals({ q: "", k: 10, filters: {} });
  if (m === "lexical") p = p.retrieve("text", { k: 30 });
  else if (m === "semantic") p = p.retrieve("vec", { k: 30 });
  else p = p.retrieve("text", { k: 30 }).retrieve("vec", { k: 30 }).fuse.rrf({ k: 60 });
  return p.filter.where({ field: "filters" }).limit();
}

module.exports = { rag, corpus, plan, VAULT, VAULT_NAME, EXCLUDE };
