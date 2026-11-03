// lib/corpus.js — the indexing pipeline as a plain value (no verbs here).
// Builders are immutable, so every file that requires this module shares the same spec.
const rag = require("ragwb");

const corpus = rag.corpus("reports")
  .from(rag.source.dir({ root: "corpus", include: ["*.md"] })) // root is relative to the app dir
  .parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  .index("text", rag.bm25({ fields: { title: 2, headingPath: 3, callout: 2, text: 1 } }))
  .index("meta", rag.terms({ keys: ["type", "tags", "status", "aliases"] }));

module.exports = { rag, corpus };
