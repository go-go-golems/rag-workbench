package store

import (
	"strings"
	"testing"
)

func save(t *testing.T, a *Apps, app, p, src string) CheckResult {
	t.Helper()
	if _, err := a.Save(app, p, []byte(src), ""); err != nil {
		t.Fatal(err)
	}
	return a.Check(app, p, []byte(src))
}

func TestCheckVerbsFile(t *testing.T) {
	a := openTest(t)
	save(t, a, "notes", "lib/corpus.js", "module.exports = { n: 1 };\n")
	r := save(t, a, "notes", "search.js", `const { n } = require("./lib/corpus");
async function search(q, k) { return [{ q, k, n }]; }
__verb__("search", { short: "Search notes", fields: { q: { argument: true }, k: { type: "int", default: 5 } } });
`)
	if !r.OK || r.Role != "verbs" || len(r.Problems) != 0 {
		t.Fatalf("check = %+v", r)
	}
	if len(r.Verbs) != 1 || r.Verbs[0].Name != "search search" || r.Verbs[0].Source != "search.js#search" {
		t.Fatalf("verbs = %+v", r.Verbs)
	}
	if len(r.Run) != 1 || !strings.HasSuffix(r.Run[0], "/notes search search") {
		t.Fatalf("run = %v", r.Run)
	}
}

func TestCheckSyntaxErrorHasLine(t *testing.T) {
	a := openTest(t)
	r := save(t, a, "x", "broken.js", "const a = 1;\n\nconst b = ;\nconst c = 3;\n")
	if r.OK || len(r.Problems) == 0 || r.Problems[0].Line != 3 {
		t.Fatalf("check = %+v", r)
	}
}

func TestCheckWorkbenchScript(t *testing.T) {
	a := openTest(t)
	// a workbench script is an async body: top-level return and await are fine,
	// and it is not scanned for verbs (its helper functions are not commands)
	r := save(t, a, "x", "pipeline.wb.js", "function helper() { return 1; }\nconst c = await Promise.resolve(helper());\nreturn { plan: c };\n")
	if !r.OK || r.Role != "workbench" || len(r.Verbs) != 0 || len(r.Run) != 1 || !strings.HasPrefix(r.Run[0], "ragwb run ") {
		t.Fatalf("check = %+v", r)
	}
	r = save(t, a, "x", "bad.wb.js", "const a = 1;\nreturn { plan: a ;\n")
	if r.OK || r.Problems[0].Line != 2 {
		t.Fatalf("check = %+v", r)
	}
	// with a workbench script in the app, the verbs of other files still scan
	r = save(t, a, "x", "tool.js", "function hello(name) { return [{ hi: name }]; }\n__verb__(\"hello\", {});\n")
	if !r.OK || len(r.Verbs) != 1 || r.Verbs[0].Name != "tool hello" {
		t.Fatalf("check = %+v", r)
	}
}

func TestCheckJSONAndLibrary(t *testing.T) {
	a := openTest(t)
	if r := save(t, a, "x", "goldens.json", `{"cases": [`); r.OK || !strings.Contains(r.Problems[0].Message, "invalid JSON") {
		t.Fatalf("check = %+v", r)
	}
	if r := save(t, a, "x", "lib/plans.js", "module.exports = { a: 1 };\n"); !r.OK || r.Role != "library" || len(r.Run) != 0 {
		t.Fatalf("check = %+v", r)
	}
}
