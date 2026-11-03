// plans.js — query plans over the shared corpus. Builders are immutable values, so variants are just exports.
const { rag, reports } = require("./corpus.js");

const lexical = rag.plan("lexical").over(reports)
  .signals({ q: "", k: 10 })
  .retrieve("text", { k: 20 });

const fused = rag.plan("fused").over(reports)
  .signals({ q: "", k: 10 })
  .retrieve("text", { k: 30 })
  .retrieve("meta", { k: 30 })
  .fuse.rrf({ k: 60 })
  .limit();

module.exports = { lexical, fused };
