package ragjs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-go-golems/rag-workbench/pkg/natives"
)

// Document ids and the hash behind their long-path suffix must be identical in
// Go (natives) and rag.js (the browser engine): RAGWB-003 §6.2.
func TestDocIDAndCyrb53MatchRagJS(t *testing.T) {
	long := "Projects/2026/05/06/" + strings.Repeat("Article - Protobuf payload contracts and sessionstream schemas ", 4) + ".md"
	names := []string{
		"a.md", "Notes/Foo Bar.md", "README.MD", "", "Ünïcödé — notes.md", "𝐾-theory 𝔽₂.md",
		strings.Repeat("x", 64) + ".md", strings.Repeat("y", 65) + ".md", strings.Repeat("z", 160) + ".md", strings.Repeat("w", 161) + ".md",
		long, long + "2", strings.Repeat("ab-", 60) + ".md",
	}
	rt := newRuntime(t, Options{})
	var js struct {
		IDs    []string `json:"ids"`
		Hashes []string `json:"hashes"`
	}
	in, _ := json.Marshal(names)
	evalJSON(t, rt, map[string]any{"names": string(in)}, `(() => {
		const rag = require("ragwb");
		const ns = JSON.parse(names);
		return JSON.stringify({
			ids: ns.map(n => rag.util.parseMarkdown({ name: n, text: "# t\n\nbody" }).id),
			hashes: ns.map(n => rag.util.hash(n)),
		});
	})()`, &js)
	for i, n := range names {
		if got := natives.Cyrb53(n); got != js.Hashes[i] {
			t.Errorf("Cyrb53(%q) = %s, rag.js %s", n, got, js.Hashes[i])
		}
		if got := natives.DocIDFor(n); got != js.IDs[i] {
			t.Errorf("DocIDFor(%q) = %q, rag.js %q", n, got, js.IDs[i])
		}
	}
	// ids up to 160 chars are the plain slug; longer ones get a distinct hash suffix
	a, b := natives.DocIDFor(long), natives.DocIDFor(long+"2")
	if a == b || len(a) > natives.DocIDMax || !strings.HasPrefix(a, "projects-2026-05-06-article-protobuf") {
		t.Errorf("long ids: %q / %q", a, b)
	}
	if got := natives.DocIDFor(strings.Repeat("y", 65) + ".md"); got != strings.Repeat("y", 65) {
		t.Errorf("65-char name should keep its full slug, got %q", got)
	}
}
