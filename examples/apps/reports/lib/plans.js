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

// Decision-model plans (RAGWB-007; need --decide pi|lunaroute|fake). Decision
// nodes are 'submit'-trigger nodes, like rerank.model and the LLM nodes.
const CORPUS = "engineering project reports and design notes";

// fused hits graded by the decision model and reordered by p(relevant)
const graded = rag.plan("graded").over(corpus)
  .signals({ q: "", k: 10, filters: {} })
  .retrieve("text", { k: 30 })
  .retrieve("meta", { k: 30 })
  .fuse.rrf({ k: 60 })
  .filter.where({ field: "filters" })
  .rerank.decide({ top: 20, keep: s => s.k, corpus: CORPUS })
  .limit();

// fused hits with the off-topic ones dropped (CRAG-style grading), order kept
const filtered = rag.plan("filtered").over(corpus)
  .signals({ q: "", k: 10, filters: {} })
  .retrieve("text", { k: 30 })
  .retrieve("meta", { k: 30 })
  .fuse.rrf({ k: 60 })
  .filter.where({ field: "filters" })
  .filter.decide({ top: 20, threshold: 0.5, corpus: CORPUS }) // no min: a question the corpus cannot answer returns no hits
  .limit();

// Adaptive-RAG: classify the question first; skip retrieval for chit-chat,
// grade the hits otherwise, and answer directly when nothing was retrieved (needs --llm too)
const adaptive = rag.plan("adaptive").over(corpus)
  .signals({ q: "", k: 8, filters: {} })
  .classify({ id: "route", corpus: CORPUS, questions: {
    needs: { type: "noul", instructions: "Does answering this question require looking up facts in the document collection?",
             true: "It asks about projects, designs, decisions or results described in the documents", false: "Greeting, chit-chat, or general knowledge" },
  } })
  .retrieve("text", { k: 30, when: { decision: "route.needs", min: 0.5 } })
  .retrieve("meta", { k: 20, when: { decision: "route.needs", min: 0.5 } })
  .fuse.rrf({ k: 60 })
  .filter.where({ field: "filters" })
  .rerank.decide({ top: 20, keep: s => s.k, corpus: CORPUS, when: { decision: "route.needs", min: 0.5 } })
  .pack({ budget: 30000 })
  .answer({ direct: true });

module.exports = { lexical, fused, semantic, hybrid, "hybrid-rerank": hybridRerank, answer, graded, filtered, adaptive };
