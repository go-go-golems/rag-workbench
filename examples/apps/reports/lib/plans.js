// lib/plans.js — query plans over the shared corpus. Variants are just more exports.
const { rag, corpus, corpusDense } = require("./corpus");

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

// Dense and hybrid plans (need --embed lunaroute).
const semantic = rag.plan("semantic").over(corpusDense)
  .signals({ q: "", k: 10, filters: {} })
  .retrieve("vec", { k: 20 })
  .filter.where({ field: "filters" })
  .limit();

const hybrid = rag.plan("hybrid").over(corpusDense)
  .signals({ q: "", k: 10, filters: {} })
  .retrieve("text", { k: 30 })
  .retrieve("vec", { k: 30 })
  .fuse.rrf({ k: 60 })
  .filter.where({ field: "filters" })
  .limit();

// rerank.model is a 'submit'-trigger node: run evals with --trigger submit to include it.
const hybridRerank = rag.plan("hybrid-rerank").over(corpusDense)
  .signals({ q: "", k: 10, filters: {} })
  .retrieve("text", { k: 30 })
  .retrieve("vec", { k: 30 })
  .fuse.rrf({ k: 60 })
  .rerank.model({ model: "bge-rr-v2-m3", k: 30 })
  .filter.where({ field: "filters" })
  .limit();

// An LLM answer over the fused hits (needs --llm): rerank.llm and answer are
// 'submit'-trigger nodes, so callers run it with { trigger: "submit" }.
const answer = rag.plan("answer").over(corpus)
  .signals({ q: "", k: 8, filters: {} })
  .retrieve("text", { k: 30 })
  .retrieve("meta", { k: 20 })
  .fuse.rrf({ k: 60 })
  .filter.where({ field: "filters" })
  .rerank.llm({ top: 20, keep: s => s.k })
  .pack({ budget: 30000 })
  .answer();

module.exports = { lexical, fused, semantic, hybrid, "hybrid-rerank": hybridRerank, answer };
