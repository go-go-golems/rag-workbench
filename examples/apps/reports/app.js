// app.js — the search UI for this app (served by `ragwb serve`, phase P7), and an
// `ask` verb: under `ragwb serve` every verb of the app directory is an endpoint
// (RAGWB-004), e.g. POST /api/rag/reports/verbs/reports/ask {"q": "…"}, with
// Accept: text/event-stream for the run's events (hits, rerank, answer text).
const gui = require("ragwb/gui");
const kit = require("ragwb/kit");
const plans = require("./lib/plans");
const { rag } = require("./lib/corpus");

__package__({ name: "reports" });

async function ask(q, k) {
  const r = await rag.run(plans.answer, { q, k }, { trigger: "submit" });
  return {
    answer: r.answer ? r.answer.text : null,
    cited: r.answer ? r.answer.cited : [],
    sources: r.top(k).map((h, i) => ({ rank: i + 1, id: h.id, doc: h.doc, heading: h.heading, reason: h.reason })),
    errors: r.errors,
  };
}
__verb__("ask", {
  short: "Answer a question from the reports with cited sources (needs --llm)",
  fields: { q: { argument: true, help: "Question" }, k: { type: "int", default: 8, help: "Sources to keep after reranking" } },
});

module.exports = {
  app: kit.searchApp(plans.fused, { id: "reports", title: "Project reports", facets: ["type", "tags"] }),
};
