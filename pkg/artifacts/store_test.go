package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveHasLoad(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "ns1")
	if err != nil {
		t.Fatal(err)
	}
	if m, err := s.Has("abc123"); err != nil || m != nil {
		t.Fatalf("Has on empty store = %v, %v", m, err)
	}
	if err := s.Save("abc123", "chunk.sections", "chunks", 42, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	m, err := s.Has("abc123")
	if err != nil || m == nil || m.Count != 42 || m.Codec != "chunks" || m.Type != "chunk.sections" || m.Size != 7 {
		t.Fatalf("Has = %+v, %v", m, err)
	}
	_, b, err := s.Load("abc123")
	if err != nil || string(b) != "payload" {
		t.Fatalf("Load = %q, %v", b, err)
	}
	// keys can't escape the namespace
	for _, k := range []string{"", "../x", "A/B", "has space"} {
		if err := s.Save(k, "t", "json", 0, nil); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
	if _, err := Open(dir, "../up"); err == nil {
		t.Error("namespace ../up accepted")
	}
}

func TestTornPayloadIsMissing(t *testing.T) {
	s, _ := Open(t.TempDir(), "ns")
	_ = s.Save("k1", "t", "json", 1, []byte("12345"))
	// a payload that no longer matches its metadata (crash mid-write, manual edit)
	bin, _ := s.path("k1", ".bin")
	_ = os.WriteFile(bin, []byte("12"), 0o644)
	if m, _ := s.Has("k1"); m != nil {
		t.Fatalf("Has reported a torn payload: %+v", m)
	}
	// garbage metadata is missing too, and a later Save repairs it
	meta, _ := s.path("k1", ".json")
	_ = os.WriteFile(meta, []byte("{not json"), 0o644)
	if m, _ := s.Has("k1"); m != nil {
		t.Fatal("Has accepted garbage metadata")
	}
	if err := s.Save("k1", "t", "json", 1, []byte("12345")); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Has("k1"); m == nil {
		t.Fatal("Save did not repair the entry")
	}
}

func TestListAndPrune(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir, "old")
	b, _ := Open(dir, "cur")
	_ = a.Save("k1", "parse.markdown", "docs", 3, []byte("x"))
	_ = b.Save("k2", "index.bm25", "bm25", 5, []byte("yy"))
	ms, err := List(dir)
	if err != nil || len(ms) != 2 {
		t.Fatalf("List = %+v, %v", ms, err)
	}
	ns := map[string]string{}
	for _, m := range ms {
		ns[m.Key] = m.NS
	}
	if ns["k1"] != "old" || ns["k2"] != "cur" {
		t.Fatalf("namespaces = %v", ns)
	}
	n, err := Prune(dir, "cur")
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
		t.Fatal("old namespace still there")
	}
	if m, _ := b.Has("k2"); m == nil {
		t.Fatal("Prune removed the kept namespace")
	}
	if n, _ := Prune(dir, ""); n != 1 {
		t.Fatalf("Prune all = %d", n)
	}
}

func TestFileAndDirPayloads(t *testing.T) {
	s, err := Open(t.TempDir(), "ns1")
	if err != nil {
		t.Fatal(err)
	}
	// a directory payload, as a bleve index is: built in TempDir, moved in by SaveDir
	tmp, err := s.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	ix := filepath.Join(tmp, "index")
	if err := os.MkdirAll(filepath.Join(ix, "store"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(ix, "index_meta.json"), []byte(`{"storage":"scorch"}`), 0o644)
	_ = os.WriteFile(filepath.Join(ix, "store", "00000001.zap"), make([]byte, 100), 0o644)
	if m, _ := s.Has("k1"); m != nil {
		t.Fatal("not saved yet")
	}
	if err := s.SaveDir("k1", "index.bm25", "bleve", 5, ix); err != nil {
		t.Fatal(err)
	}
	m, p, err := s.Path("k1")
	if err != nil || m.Kind != KindDir || m.Size != 120 || m.Count != 5 || filepath.Base(p) != "k1.bleve" {
		t.Fatalf("Path: %+v %s %v", m, p, err)
	}
	if _, err := os.Stat(ix); !os.IsNotExist(err) {
		t.Fatal("source dir should have been moved")
	}
	if _, _, err := s.Load("k1"); err == nil {
		t.Fatal("Load of a dir payload should fail")
	}
	// saving the same key again (another process) keeps the stored payload and drops the new one
	tmp2, _ := s.TempDir()
	dup := filepath.Join(tmp2, "index")
	_ = os.MkdirAll(dup, 0o755)
	if err := s.SaveDir("k1", "index.bm25", "bleve", 5, dup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dup); !os.IsNotExist(err) {
		t.Fatal("duplicate source should have been removed")
	}

	// a file payload
	f := filepath.Join(tmp, "records.db")
	_ = os.WriteFile(f, []byte("0123456789"), 0o644)
	if err := s.SaveFile("k2", "chunk.sections", "records", 3, f); err != nil {
		t.Fatal(err)
	}
	if m, p, err := s.Path("k2"); err != nil || m.Kind != KindFile || m.Size != 10 || filepath.Base(p) != "k2.records" {
		t.Fatalf("file Path: %+v %s %v", m, p, err)
	}
	// a damaged payload (truncated file) reads as missing
	_ = os.WriteFile(filepath.Join(filepath.Dir(p0(t, s, "k2")), "k2.records"), []byte("01"), 0o644)
	if m, _ := s.Has("k2"); m != nil {
		t.Fatal("a truncated file payload should read as missing")
	}

	// List sees both, and nothing from inside the bleve directory (its index_meta.json)
	ms, err := List(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("List = %+v", ms)
	}
	// blob API unchanged
	if err := s.Save("k3", "x", "json", 1, []byte(`[1]`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Path("k3"); err == nil {
		t.Fatal("Path of a blob should fail")
	}
}

func p0(t *testing.T, s *Store, key string) string {
	t.Helper()
	p, err := s.path(key, ".json")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
