package natives

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestManifestDiff covers RAGWB-003 §6.3: add, change, remove, rename, a touch
// with identical bytes, skip-status changes, other-source manifests and the
// full-rebuild threshold.
func TestManifestDiff(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.md":     "# A",
		"b.md":     "# B",
		"c.md":     "# C",
		"d.md":     "# D",
		"old.md":   "# renamed later",
		"big.md":   strings.Repeat("x", 2048),
		"touch.md": "# touched",
	})
	s := &SourceDir{Roots: []string{root}}
	p := map[string]any{"root": root, "maxBytes": float64(1024)}
	if _, _, err := s.Walk(p); err != nil {
		t.Fatal(err)
	}
	m, err := s.LastManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	// the manifest is what persist.save stores: it must round-trip through JSON
	b, _ := json.Marshal(m)
	var base Manifest
	if err := json.Unmarshal(b, &base); err != nil {
		t.Fatal(err)
	}
	if len(base.Entries) != 7 {
		t.Fatalf("entries = %d, want 7 (skipped files are tracked)", len(base.Entries))
	}

	// no change at all
	d, err := s.Diff(p, &base, 0.5)
	if err != nil || d.Full || d.Changes() != 0 || d.Unchanged != 7 {
		t.Fatalf("no-op diff = %+v, %v", d, err)
	}

	w := func(rel, text string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("a.md", "# A, edited")                                                    // changed
	w("e.md", "# E")                                                            // added
	_ = os.Remove(filepath.Join(root, "b.md"))                                  // removed
	_ = os.Rename(filepath.Join(root, "old.md"), filepath.Join(root, "new.md")) // rename = remove + add
	w("big.md", "now small")                                                    // skipped → returned: added
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(root, "touch.md"), future, future) // same bytes, new mtime

	d, err = s.Diff(p, &base, 0.9)
	if err != nil || d.Full {
		t.Fatalf("diff = %+v, %v", d, err)
	}
	if got := names(d.Added); got != "big.md,e.md,new.md" {
		t.Fatalf("added = %s", got)
	}
	if got := names(d.Changed); got != "a.md" {
		t.Fatalf("changed = %s", got)
	}
	if got := strings.Join(d.Removed, ","); got != "b.md,old.md" {
		t.Fatalf("removed = %s", got)
	}
	if d.Unchanged != 3 { // c, d, touch
		t.Fatalf("unchanged = %d", d.Unchanged)
	}
	if d.Changed[0].Text != "# A, edited" {
		t.Fatalf("changed record carries the new text, got %q", d.Changed[0].Text)
	}

	// the diff remembered the new manifest: diffing against it is a no-op
	m2, _ := s.LastManifest(p)
	if d2, _ := s.Diff(p, m2, 0.5); d2.Full || d2.Changes() != 0 {
		t.Fatalf("diff against the new manifest = %+v", d2)
	}

	// returned → skipped is a removal
	w("c.md", strings.Repeat("y", 2048))
	if d3, _ := s.Diff(p, m2, 0.9); strings.Join(d3.Removed, ",") != "c.md" || d3.Changes() != 1 {
		t.Fatalf("now-skipped = %+v", d3)
	}

	// threshold: 6 changes over 7 base entries > 50%
	if d4, _ := s.Diff(p, &base, 0.5); !d4.Full || !strings.Contains(d4.Reason, "changed") {
		t.Fatalf("threshold diff = %+v", d4)
	}
	// another source's manifest, or none, is not comparable
	other := map[string]any{"root": root, "maxBytes": float64(4096)}
	if d5, _ := s.Diff(other, &base, 0.9); !d5.Full {
		t.Fatalf("other-params diff = %+v", d5)
	}
	if d6, _ := s.Diff(p, nil, 0.9); !d6.Full {
		t.Fatalf("nil-base diff = %+v", d6)
	}
	// lineage ignores file contents
	l1, _ := s.Lineage(p)
	l2, _ := s.Lineage(other)
	if l1 == "" || l1 == l2 {
		t.Fatalf("lineage %q vs %q", l1, l2)
	}
}
