// app.js — the vault search UI, served by `ragwb serve` (P7). No verbs.
//   ragwb --root ~/code/wesen/go-go-golems/go-go-parc serve examples/apps/parc
// The plan is built once in the server's runtime (stored artifacts make that a load);
// each keystroke runs only the live nodes there.
const kit = require("ragwb/kit");
const { plan } = require("./lib/corpus");

module.exports = {
  app: kit.searchApp(plan(null, "lexical"), {
    id: "parc", title: "go-go-parc vault", placeholder: "Search the vault…",
    facets: ["type", "tags", "status"], answer: false,
  }),
};
