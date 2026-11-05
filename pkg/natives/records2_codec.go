package natives

import (
	"encoding/json"
	"fmt"

	"github.com/dop251/goja"
)

// Docs and chunks as records2 documents (codec "records2"): one record per doc
// (its JSON) for docs, the doc's chunks (JSON of Chunk, as records v1) for chunks.

const (
	kindDocs   = "docs"
	kindChunks = "chunks"
)

// ChunksToR2 groups chunks by document. Chunks arrive grouped (a doc's chunks
// are consecutive); a doc id seen again later (two files with one id) joins
// its first group, keeping positions in order.
func ChunksToR2(chunks []Chunk) ([]R2Doc, error) {
	docs := []R2Doc{}
	at := map[string]int{}
	for i := range chunks {
		c := &chunks[i]
		b, err := json.Marshal(c)
		if err != nil {
			return nil, fmt.Errorf("chunk %s: %w", c.ID, err)
		}
		j, ok := at[c.DocID]
		if !ok {
			j = len(docs)
			at[c.DocID] = j
			docs = append(docs, R2Doc{ID: c.DocID, Name: c.DocName})
		}
		docs[j].Recs = append(docs[j].Recs, R2Rec{ID: c.ID, Data: b})
	}
	return docs, nil
}

// DocsToR2 makes one records2 document per doc; a repeated id joins the first.
func DocsToR2(ds []*Doc) ([]R2Doc, error) {
	docs := []R2Doc{}
	at := map[string]int{}
	for _, d := range ds {
		b, err := json.Marshal(d)
		if err != nil {
			return nil, fmt.Errorf("doc %s: %w", d.Name, err)
		}
		j, ok := at[d.ID]
		if !ok {
			j = len(docs)
			at[d.ID] = j
			docs = append(docs, R2Doc{ID: d.ID, Name: d.Name})
		}
		docs[j].Recs = append(docs[j].Recs, R2Rec{ID: d.ID, Data: b})
	}
	return docs, nil
}

func decodeDoc(b []byte) (*Doc, error) {
	var d Doc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.Meta.Values == nil {
		d.Meta.Values = map[string]any{}
	}
	return &d, nil
}

func decodeChunkR2(b []byte, name string) (Chunk, error) {
	c, err := decodeChunk(b)
	if c.Meta.Values == nil {
		c.Meta.Values = map[string]any{}
	}
	c.DocName = name
	return c, err
}

// R2Chunks reads chunks back from records2 (all of them, in canonical order).
func R2Chunks(r *Records2, from, to int) ([]Chunk, error) {
	out := make([]Chunk, 0, max(0, min(to, r.Len())-from))
	err := r.Slice(from, to, func(b []byte) error {
		c, err := decodeChunk(b)
		if err != nil {
			return err
		}
		out = append(out, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		if info, ok := r.DocInfo(out[i].DocID); ok {
			out[i].DocName = info.Name
		}
	}
	return out, err
}

// R2Docs reads docs back from records2.
func R2Docs(r *Records2, from, to int) ([]*Doc, error) {
	out := []*Doc{}
	err := r.Slice(from, to, func(b []byte) error {
		d, err := decodeDoc(b)
		if err == nil {
			out = append(out, d)
		}
		return err
	})
	return out, err
}

// records2Object is the Go side of a RecordView over records2 (docs or chunks),
// with the reader as a hidden handle so deltas and saves can reach it.
func records2Object(vm *goja.Runtime, r *Records2) *goja.Object {
	o := vm.NewObject()
	must := func(err error) {
		if err != nil {
			panic(vm.NewGoError(err))
		}
	}
	docs := r.Kind() == kindDocs
	toJS := func(b []byte) (goja.Value, error) {
		if docs {
			d, err := decodeDoc(b)
			if err != nil {
				return nil, err
			}
			return docToJS(vm, d), nil
		}
		c, err := decodeChunk(b)
		if err != nil {
			return nil, err
		}
		return chunkToJS(vm, &c), nil
	}
	_ = o.Set("len", func() int { return r.Len() })
	_ = o.Set("has", func(id string) bool { return r.Has(id) })
	_ = o.Set("get", func(id string) goja.Value {
		b, ok, err := r.Get(id)
		must(err)
		if !ok {
			return goja.Null()
		}
		v, err := toJS(b)
		must(err)
		return v
	})
	_ = o.Set("slice", func(from, to int) goja.Value {
		out := []any{}
		must(r.Slice(from, to, func(b []byte) error {
			v, err := toJS(b)
			if err == nil {
				out = append(out, v)
			}
			return err
		}))
		return vm.NewArray(out...)
	})
	_ = o.Set("all", func() goja.Value {
		if docs {
			ds, err := R2Docs(r, 0, r.Len())
			must(err)
			return docsArray(vm, ds)
		}
		cs, err := R2Chunks(r, 0, r.Len())
		must(err)
		return chunksArray(vm, cs)
	})
	_ = o.Set("order", func(docID string) []string {
		if docs {
			if _, ok := r.DocInfo(docID); ok {
				return []string{docID}
			}
			return []string{}
		}
		ids, err := r.Order(docID)
		must(err)
		if ids == nil {
			ids = []string{}
		}
		return ids
	})
	_ = o.Set("close", func() { _ = r.Close() })
	_ = o.DefineDataProperty(goHandleKey, vm.ToValue(r), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	return o
}

// records2Handle returns the reader behind a records2 view (RecordView over records2Object), or nil.
func records2Handle(v goja.Value) *Records2 {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	rec := o.Get("__records")
	if rec == nil {
		return nil
	}
	ro, ok := rec.(*goja.Object)
	if !ok {
		return nil
	}
	h := ro.Get(goHandleKey)
	if h == nil {
		return nil
	}
	r, _ := h.Export().(*Records2)
	return r
}

// docsHandle returns the Go docs behind a docs output (docsArray), or nil.
func docsHandle(v goja.Value) []*Doc {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	h := o.Get(goHandleKey)
	if h == nil {
		return nil
	}
	ds, _ := h.Export().([]*Doc)
	return ds
}
