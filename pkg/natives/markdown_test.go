package natives

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ranges (API adjustment A2) must point at the exact source of each block.
func TestBlockRangesPointAtSource(t *testing.T) {
	paths, _ := filepath.Glob("../../examples/apps/reports/corpus/*.md")
	if len(paths) == 0 {
		t.Fatal("no corpus files")
	}
	checked := 0
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		lines := strings.Split(text, "\n")
		doc := ParseMarkdown(filepath.Base(p), text, true)
		for _, s := range doc.Sections {
			for _, bl := range s.Blocks {
				r := bl.Range
				src := strings.ReplaceAll(text[r.ByteStart:r.ByteEnd], "\r", "")
				if got := strings.TrimSuffix(lines[r.LineStart-1], "\r"); !strings.HasPrefix(src, got) {
					t.Fatalf("%s: line %d %q does not start block slice %q", p, r.LineStart, got, src[:min(60, len(src))])
				}
				if strings.Count(src, "\n") != r.LineEnd-r.LineStart {
					t.Fatalf("%s: lines %d-%d but slice has %d newlines", p, r.LineStart, r.LineEnd, strings.Count(src, "\n"))
				}
				switch bl.Kind {
				case "paragraph", "list", "table":
					if src != bl.Text {
						t.Fatalf("%s %s: slice %q != text %q", p, bl.Kind, src, bl.Text)
					}
				case "fence":
					if !strings.Contains(src, bl.Text) || !strings.HasPrefix(strings.TrimLeft(src, " \t"), "```") && !strings.HasPrefix(strings.TrimLeft(src, " \t"), "~~~") {
						t.Fatalf("%s fence: slice %q does not wrap body", p, src[:min(80, len(src))])
					}
				}
				checked++
			}
		}
	}
	t.Logf("%d block ranges verified", checked)
}

func TestFrontmatterAndTitle(t *testing.T) {
	doc := ParseMarkdown("Note.md", "---\ntitle: \"Hello\"\ntags:\n  - a\n  - b\ncount: 3\nempty:\n---\n# H1 ignored for title\n\nBody text.\n", true)
	if doc.Title != "Hello" || doc.ID != "note" {
		t.Fatalf("title/id = %q/%q", doc.Title, doc.ID)
	}
	if tags, _ := doc.Meta.Values["tags"].([]any); len(tags) != 2 || tags[0] != "a" {
		t.Fatalf("tags = %v", doc.Meta.Values["tags"])
	}
	if doc.Meta.Values["count"] != float64(3) {
		t.Fatalf("count = %#v", doc.Meta.Values["count"])
	}
	if _, ok := doc.Meta.Values["empty"]; ok {
		t.Fatal("empty list keys must be deleted like in rag.js")
	}
	b := doc.Sections[0].Blocks[0]
	if b.Text != "Body text." || b.Range.LineStart != 11 {
		t.Fatalf("block = %+v", b)
	}
}

// rag.js counts an empty-text block as "[object Object]" when sizing parts
// (found by whole-vault parity: an empty code fence changed a split point).
func TestBlocksTokensEmptyTextQuirk(t *testing.T) {
	if got, want := blocksTokens([]Block{{Kind: "fence", Text: ""}}), tokens("[object Object]"); got != want {
		t.Fatalf("blocksTokens(empty) = %d, want %d", got, want)
	}
	if got, want := blocksTokens([]Block{{Text: "abcd"}, {Text: ""}}), tokens("abcd\n[object Object]"); got != want {
		t.Fatalf("blocksTokens(mixed) = %d, want %d", got, want)
	}
}
