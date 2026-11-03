package natives

import (
	"fmt"
	"reflect"
	"testing"
)

func TestBm25CodecRoundTrip(t *testing.T) {
	texts := []string{"the rebalance law moves slots", "protojson int64 as strings", "OAuth worked but no actions showed", "rebalance the cluster nightly", "Ünïcödé x² façade"}
	ix := NewBm25(len(texts), func(i int) string { return fmt.Sprint("c", i) }, func(i int) string { return fmt.Sprint("d", i%2) },
		func(i, f int) (string, bool) { return texts[i], f == 0 }, []Field{{"text", 1}, {"title", 2}}, 1.2, 0.75, true)
	b, err := EncodeBm25(ix)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeBm25(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.N != ix.N || got.terms != ix.terms || got.Avg != ix.Avg || !reflect.DeepEqual(got.Post, ix.Post) {
		t.Fatalf("decoded index differs: N %d/%d terms %d/%d", got.N, ix.N, got.terms, ix.terms)
	}
	for _, q := range []string{"rebalance law", "int64", "x²", "nothing here"} {
		if a, b := ix.Search(q, 5, nil, nil), got.Search(q, 5, nil, nil); !reflect.DeepEqual(a, b) {
			t.Errorf("Search(%q): %v vs %v", q, a, b)
		}
		if a, b := ix.SearchIn("d0", q, 3, nil), got.SearchIn("d0", q, 3, nil); !reflect.DeepEqual(a, b) {
			t.Errorf("SearchIn(%q): %v vs %v", q, a, b)
		}
	}
	// encoding is deterministic (terms sorted), so equal indexes store equal bytes
	b2, _ := EncodeBm25(got)
	if string(b) != string(b2) {
		t.Error("re-encoding the decoded index changed the bytes")
	}
	if _, err := DecodeBm25(b[:len(b)/2]); err == nil {
		t.Error("truncated artifact decoded without error")
	}
}
