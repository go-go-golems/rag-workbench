// app.js — the vault search UI, served by `ragwb serve` (P7). No verbs.
//   ragwb --root ~/code/wesen/go-go-golems/go-go-parc serve examples/apps/parc
// The plan is built once in the server's runtime (stored artifacts make that a load);
// each keystroke runs only the live nodes there.
const kit = require("ragwb/kit");
const { plan, VAULT_NAME } = require("./lib/corpus");

module.exports = {
  app: kit.searchApp(plan(null, "lexical"), {
    id: "parc", title: VAULT_NAME + " vault", placeholder: "Search the vault…", submitLabel: "Search",
    facets: ["type", "tags", "status"], answer: false, obsidianVault: VAULT_NAME,
    results: { empty: "Search the vault to find notes." },
  }),
};
