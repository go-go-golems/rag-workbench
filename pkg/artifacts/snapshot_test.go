package artifacts

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotsInventoryAndPins(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".ragwb.store")
	s, err := Open(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if second, err := Open(root, "test"); err == nil {
		second.Close()
		t.Fatal("second owner accepted")
	}
	for i := 0; i < 45; i++ {
		key := fmt.Sprintf("artifact%02d", i)
		if err := s.Save(key, "chunks", "json", 1, []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
		if err := s.LineageRecord(LineageEntry{Lineage: "chain", StoreKey: key, Digest: key}); err != nil {
			t.Fatal(err)
		}
		if err := s.Publish(Snapshot{ID: fmt.Sprintf("snapshot%02d", i), Slot: "reports", Roots: []string{key}}); err != nil {
			t.Fatal(err)
		}
	}
	release, err := s.Pin([]string{"artifact00"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.GC(3)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 41 {
		t.Fatalf("removed %d, want 41", result.Removed)
	}
	inventory, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 4 {
		t.Fatalf("inventory=%d", len(inventory))
	}
	if err := s.Close(); err == nil {
		t.Fatal("store closed with active reader")
	}
	release()
	if result, err := s.GC(3); err != nil || result.Removed != 1 {
		t.Fatalf("released generation: %+v, %v", result, err)
	}
}

func TestCorruptManifestPreventsCollection(t *testing.T) {
	s, err := Open(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Save("a", "chunks", "json", 1, []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(Snapshot{ID: "snapshot", Slot: "slot", Roots: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, s.NS, "snapshots", "snapshot.json"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GC(1); err == nil {
		t.Fatal("corrupt metadata accepted")
	}
	if m, _ := s.Has("a"); m == nil {
		t.Fatal("collection deleted authoritative payload")
	}
}

func TestBuildLeaseAndTransitiveDependencies(t *testing.T) {
	s, err := Open(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	release, err := s.BeginBuild()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"base", "derived", "orphan"} {
		if err := s.Save(key, "chunks", "json", 1, []byte(`[]`)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.GC(1); err == nil {
		t.Fatal("collector ran during build")
	}
	if err := s.Describe("derived", "", []string{"base"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(Snapshot{ID: "snapshot", Slot: "slot", Roots: []string{"derived"}}); err != nil {
		t.Fatal(err)
	}
	release()
	release()
	result, err := s.GC(1)
	if err != nil || result.Removed != 1 {
		t.Fatalf("collection: %+v %v", result, err)
	}
	if meta, _ := s.Has("base"); meta == nil {
		t.Fatal("transitive dependency deleted")
	}
}
