// app.js — the search UI for this app (served by `ragwb serve`, phase P7). No verbs.
const gui = require("ragwb/gui");
const kit = require("ragwb/kit");
const plans = require("./lib/plans");

module.exports = {
  app: kit.searchApp(plans.fused, { id: "reports", title: "Project reports", facets: ["type", "tags"] }),
};
