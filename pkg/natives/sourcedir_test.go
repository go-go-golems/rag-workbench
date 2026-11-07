package natives

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, text := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func names(fs []FileRecord) string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return strings.Join(out, ",")
}

func TestWalkIncludeExcludeAndSkips(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.md":                  "# A",
		"Projects/2026/b.md":    "# B",
		"Projects/2026/c.txt":   "not included",
		".obsidian/app.md":      "# config, default-excluded",
		"ttmp/x.md":             "# excluded by caller",
		"Research/_assets/y.md": "# excluded by caller glob",
		"page.md":               "<!DOCTYPE html><html>…",
		"big.md":                strings.Repeat("x", 2048),
	})
	s := &SourceDir{Roots: []string{root}}
	files, skipped, err := s.Walk(map[string]any{
		"root":     root,
		"exclude":  []any{"ttmp/**", "**/_assets/**"},
		"maxBytes": float64(1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(files); got != "Projects/2026/b.md,a.md" {
		t.Fatalf("files = %s", got)
	}
	reasons := map[string]string{}
	for _, sk := range skipped {
		reasons[sk.Name] = sk.Reason
	}
	if !strings.Contains(reasons["big.md"], "maxBytes") || !strings.Contains(reasons["page.md"], "HTML") || len(skipped) != 2 {
		t.Fatalf("skipped = %+v", skipped)
	}
}

func TestDigestChangesWithFiles(t *testing.T) {
	root := writeTree(t, map[string]string{"a.md": "# A", "b.md": "# B"})
	s := &SourceDir{Roots: []string{root}}
	p := map[string]any{"root": root}
	d1, err := s.Digest(p)
	if err != nil {
		t.Fatal(err)
	}
	d2, _ := s.Digest(p)
	if d1 != d2 {
		t.Fatalf("digest not stable: %s vs %s", d1, d2)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# A changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Join(root, "a.md"), later, later)
	d3, _ := s.Digest(p)
	if d3 == d1 {
		t.Fatal("digest did not change after an edit")
	}
	d4, _ := s.Digest(map[string]any{"root": root, "include": []any{"a.md"}})
	if d4 == d3 {
		t.Fatal("digest should depend on include globs")
	}
}

func TestRootsAllowlist(t *testing.T) {
	allowed := writeTree(t, map[string]string{"a.md": "# A"})
	other := writeTree(t, map[string]string{"b.md": "# B"})
	s := &SourceDir{Roots: []string{allowed}}
	if _, _, err := s.Walk(map[string]any{"root": filepath.Join(allowed, ".")}); err != nil {
		t.Fatalf("allowed root rejected: %v", err)
	}
	if _, _, err := s.Walk(map[string]any{"root": other}); err == nil || !strings.Contains(err.Error(), "outside the allowed roots") {
		t.Fatalf("expected allowlist rejection, got %v", err)
	}
	if _, _, err := s.Walk(map[string]any{"root": filepath.Join(allowed, "..")}); err == nil {
		t.Fatal("parent of an allowed root must be rejected")
	}
	if _, _, err := (&SourceDir{}).Walk(map[string]any{"root": allowed}); err == nil {
		t.Fatal("empty Roots must deny all access")
	}
}

func TestRelativeRootUsesBase(t *testing.T) {
	app := writeTree(t, map[string]string{"corpus/a.md": "# A"})
	s := &SourceDir{Roots: []string{app}, Base: app}
	files, _, err := s.Walk(map[string]any{"root": "corpus"})
	if err != nil {
		t.Fatal(err)
	}
	if names(files) != "a.md" {
		t.Fatalf("files = %s", names(files))
	}
}

func TestRelativeAllowlistEntry(t *testing.T) {
	app := writeTree(t, map[string]string{"corpus/a.md": "# A"})
	wd, _ := os.Getwd()
	rel, err := filepath.Rel(wd, app)
	if err != nil {
		t.Skip("temp dir not expressible relative to cwd")
	}
	s := &SourceDir{Roots: []string{rel}, Base: rel}
	if _, _, err := s.Walk(map[string]any{"root": "corpus"}); err != nil {
		t.Fatalf("relative allowlist entry rejected: %v", err)
	}
}

func TestSourceDirVaultIgnoreAndRefresh(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("visible.md", "# visible")
	write("Secrets/hidden.md", "# secret")
	write("Secrets/allowed.md", "# allowed")
	write(".vault-ignore", "/Secrets/\n!Secrets/allowed.md\n")
	src := &SourceDir{Roots: []string{root}, DigestTTL: time.Hour}
	p := map[string]any{"root": root, "vaultIgnore": true}
	files, _, err := src.Walk(p)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range files {
		names[f.Name] = true
	}
	if !names["visible.md"] || !names["Secrets/allowed.md"] || names["Secrets/hidden.md"] {
		t.Fatalf("unexpected files: %v", names)
	}
	first, err := src.Digest(p)
	if err != nil {
		t.Fatal(err)
	}
	write(".vault-ignore", "/Secrets/\n")
	second, err := src.Digest(p)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("ignore change did not invalidate cached digest")
	}
	files, _, err = src.Walk(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "visible.md" {
		t.Fatalf("ignore change not applied: %+v", files)
	}
}
