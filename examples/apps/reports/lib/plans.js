// lib/plans.js — query plans over the shared corpus. Variants are just more exports.
const { rag, corpus } = require("./corpus");

const lexical = rag.plan("lexical").over(corpus)
  .signals({ q: "", k: 10, filters: {} })
  .retrieve("text", { k: 20 })
  .filter.where({ field: "filters" })
  .limit();

const fused = rag.plan("fused").over(corpus)
  .signals({ q: "", k: 10, filters: {} })
  .retrieve("text", { k: 30 })
  .retrieve("meta", { k: 30 })
  .fuse.rrf({ k: 60 })
  .filter.where({ field: "filters" })
  .limit();

module.exports = { lexical, fused };
