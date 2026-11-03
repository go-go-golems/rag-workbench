package natives

import (
	"os"
	"path/filepath"
	"testing"
)

var bleveTestChunks = []struct{ id, doc, title, heading, text string }{
	{"a#1", "a", "Rebalance law", "The law", "A rebalance preserves every placement id and its view."},
	{"a#2", "a", "Rebalance law", "Verification", "Property tests generate trees and check the law holds."},
	{"b#1", "b", "Protobuf contracts", "Int64", "protojson encodes int64 as a string to keep precision."},
	{"b#2", "b", "Protobuf contracts", "Registry", "The schema registry is the enforcement boundary."},
	{"c#1", "c", "Garden notes", "Fruit", "Apples are red; bananas are yellow."},
}

var bleveTestFields = []Field{{"title", 2}, {"headingPath", 3}, {"text", 1}}

func bleveTestText(i, f int) (string, bool) {
	c := bleveTestChunks[i]
	return []string{c.title, c.heading, c.text}[f], true
}

func buildBoth(t *testing.T, dir string, temp bool) (*Bm25, *BleveBm25) {
	t.Helper()
	n := len(bleveTestChunks)
	id := func(i int) string { return bleveTestChunks[i].id }
	doc := func(i int) string { return bleveTestChunks[i].doc }
	mem := NewBm25(n, id, doc, bleveTestText, bleveTestFields, 1.2, 0.75, true)
	bl, err := BuildBleveBm25(dir, n, id, doc, bleveTestText, bleveTestFields, BleveOptions{Stem: true, Temp: temp})
	if err != nil {
		t.Fatal(err)
	}
	return mem, bl
}

func ids(hs []Hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

func TestBleveBm25AgreesOnClearQueries(t *testing.T) {
	mem, bl := buildBoth(t, filepath.Join(t.TempDir(), "ix"), false)
	defer bl.Close()
	for _, q := range []string{"rebalance law", "protojson int64", "schema registry", "apples bananas"} {
		m := mem.Search(q, 3, nil, nil)
		b, err := bl.Search(q, 3, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) == 0 || len(m) == 0 || b[0].ID != m[0].ID {
			t.Errorf("%q: bleve %v, memory %v", q, ids(b), ids(m))
		}
		for _, h := range b {
			for _, w := range h.Why {
				found := false
				for _, qt := range queryTermsFor(q, nil, true) {
					found = found || qt.t == w.Term
				}
				if !found {
					t.Errorf("%q: why term %q is not a query term", q, w.Term)
				}
			}
		}
	}
	if hs, _ := bl.Search("xyzzy", 5, nil, nil); len(hs) != 0 {
		t.Errorf("xyzzy: %v", ids(hs))
	}
	if hs, _ := bl.Search("", 5, nil, nil); len(hs) != 0 {
		t.Errorf("empty query: %v", ids(hs))
	}
}

func TestBleveBm25SynonymsSearchInOnly(t *testing.T) {
	_, bl := buildBoth(t, filepath.Join(t.TempDir(), "ix"), false)
	defer bl.Close()
	// "fruit salad" matches nothing by itself except the heading "Fruit"; the synonym adds bananas
	syn := []Synonym{{Key: "salad", Vals: []string{"bananas"}}}
	hs, err := bl.Search("salad", 3, syn, nil)
	if err != nil || len(hs) == 0 || hs[0].ID != "c#1" {
		t.Fatalf("synonyms: %v %v", ids(hs), err)
	}
	hs, _ = bl.SearchIn("a", "law", 5, nil)
	for _, h := range hs {
		if h.ID != "a#1" && h.ID != "a#2" {
			t.Fatalf("searchIn(a) returned %s", h.ID)
		}
	}
	if len(hs) != 2 {
		t.Fatalf("searchIn(a, law) = %v", ids(hs))
	}
	hs, _ = bl.Search("law", 5, nil, []string{"a#2"})
	if len(hs) != 1 || hs[0].ID != "a#2" {
		t.Fatalf("only: %v", ids(hs))
	}
}

func TestBleveBm25ReopenReadOnlyAndTempCleanup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ix")
	_, bl := buildBoth(t, dir, false)
	want, _ := bl.Search("rebalance law", 3, nil, nil)
	if err := bl.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenBleveBm25(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ro.Search("rebalance law", 3, nil, nil)
	if err != nil || len(got) != len(want) || got[0].ID != want[0].ID || got[0].Score != want[0].Score {
		t.Fatalf("reopened: %v vs %v (%v)", got, want, err)
	}
	if ro.Meta.N != len(bleveTestChunks) || len(ro.Meta.Fields) != 3 || !ro.Meta.Stem {
		t.Fatalf("meta: %+v", ro.Meta)
	}
	// a second read-only open of the same index at the same time (two processes in production)
	ro2, err := OpenBleveBm25(dir)
	if err != nil {
		t.Fatalf("second read-only open: %v", err)
	}
	_ = ro2.Close()
	_ = ro.Close()
	if _, err := ro.Search("law", 1, nil, nil); err == nil {
		t.Fatal("search on a closed index should fail")
	}

	tmp := filepath.Join(t.TempDir(), "tmpix")
	_, tb := buildBoth(t, tmp, true)
	_ = tb.Close()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temp index not removed: %v", err)
	}
}
