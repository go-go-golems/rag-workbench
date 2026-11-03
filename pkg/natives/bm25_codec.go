package natives

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"sort"
)

// bm25Wire is the persisted form of a Bm25 index (artifact codec "bm25").
// Postings are flattened per term; terms are sorted so equal indexes encode equally.
type bm25Wire struct {
	Fields []Field
	K1, B  float64
	Stem   bool
	IDs    []string
	DocOf  []string
	Len    []float64
	Avg    float64
	Terms  []string
	PostI  [][]int32
	PostTF [][]float64
}

// EncodeBm25 serializes an index for the artifact store.
func EncodeBm25(ix *Bm25) ([]byte, error) {
	w := bm25Wire{Fields: ix.Fields, K1: ix.K1, B: ix.B, Stem: ix.Stem, IDs: ix.IDs, DocOf: ix.DocOf, Len: ix.Len, Avg: ix.Avg}
	w.Terms = make([]string, 0, len(ix.Post))
	for t := range ix.Post {
		w.Terms = append(w.Terms, t)
	}
	sort.Strings(w.Terms)
	w.PostI = make([][]int32, len(w.Terms))
	w.PostTF = make([][]float64, len(w.Terms))
	for k, t := range w.Terms {
		ps := ix.Post[t]
		is, tfs := make([]int32, len(ps)), make([]float64, len(ps))
		for j, p := range ps {
			is[j], tfs[j] = int32(p.i), p.tf
		}
		w.PostI[k], w.PostTF[k] = is, tfs
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&w); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeBm25 restores an index written by EncodeBm25.
func DecodeBm25(b []byte) (*Bm25, error) {
	var w bm25Wire
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&w); err != nil {
		return nil, fmt.Errorf("bm25 artifact: %w", err)
	}
	if len(w.PostI) != len(w.Terms) || len(w.PostTF) != len(w.Terms) || len(w.DocOf) != len(w.IDs) || len(w.Len) != len(w.IDs) {
		return nil, fmt.Errorf("bm25 artifact: inconsistent lengths")
	}
	ix := &Bm25{Fields: w.Fields, K1: w.K1, B: w.B, Stem: w.Stem, IDs: w.IDs, DocOf: w.DocOf, Len: w.Len, Avg: w.Avg, Post: make(map[string][]posting, len(w.Terms))}
	for k, t := range w.Terms {
		ps := make([]posting, len(w.PostI[k]))
		for j := range ps {
			ps[j] = posting{int(w.PostI[k][j]), w.PostTF[k][j]}
		}
		ix.Post[t] = ps
	}
	ix.N = len(ix.IDs)
	ix.terms = len(ix.Post)
	return ix, nil
}
