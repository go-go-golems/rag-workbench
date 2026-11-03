package natives

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// With DigestTTL the tree is walked once per interval: an edit is invisible until
// the TTL passes (ragwb serve's source snapshot); without it every call sees it.
func TestSourceDirDigestTTL(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.md")
	if err := os.WriteFile(f, []byte("# a"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := map[string]any{"root": dir, "include": []any{"*.md"}}
	cached := &SourceDir{Roots: []string{dir}, DigestTTL: time.Hour}
	live := &SourceDir{Roots: []string{dir}}
	c0, err := cached.Digest(p)
	if err != nil {
		t.Fatal(err)
	}
	l0, _ := live.Digest(p)
	if err := os.WriteFile(f, []byte("# a, edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c1, _ := cached.Digest(p); c1 != c0 {
		t.Fatal("cached digest changed within its TTL")
	}
	if l1, _ := live.Digest(p); l1 == l0 {
		t.Fatal("uncached digest missed the edit")
	}
	cached.DigestTTL = time.Nanosecond
	if c2, _ := cached.Digest(p); c2 == c0 {
		t.Fatal("expired digest was reused")
	}
}
