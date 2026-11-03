// corpus.js — the indexing pipeline as a plain module: no verbs, just a value other files require.
const rag = require("./rag.js");
const { files } = require("./corpus-data.js");

rag.configure({ files: () => files, buildCalls: "explicit" });

const reports = rag.corpus("reports")
  .from(rag.files("*.md"))
  .parse(rag.markdown())
  .chunk(rag.sections({ depth: 3, maxTokens: 450 }))
  .index("text", rag.bm25({ fields: { title: 2, headingPath: 3, callout: 2, text: 1 } }))
  .index("meta", rag.terms({ keys: ["type", "tags", "status", "aliases"] }));

module.exports = { rag, reports, engine: rag.engine() };
