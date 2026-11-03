package natives

import (
	"fmt"

	"github.com/dop251/goja"
)

// Bm25Native installs a Go implementation of index.bm25 under the prototype's
// type name. The returned index object has the same surface as the JS class
// (kind, N, fields, search, searchIn, toJSON).
type Bm25Native struct{}

func (Bm25Native) Name() string { return "index.bm25" }

// Field texts are extracted in JS with rag.js's exact rules (getPath, arrays
// joined with ' ', non-strings JSON.stringify'd, null/” skipped) in one pass;
// tokenizing, postings and scoring happen in Go.
const bm25JS = `(function (rag, h) {
  const DEFAULT_FIELDS = { title: 2, headingPath: 3, text: 1 };
  const getPath = (obj, path) => { if (!path) return obj; let cur = obj; for (const k of String(path).split('.')) { if (cur == null) return undefined; cur = cur[k]; } return cur; };
  const fieldText = (c, f) => { let v = getPath(c, f); if (Array.isArray(v)) v = v.join(' '); if (v == null || v === '') return null; return typeof v === 'string' ? v : JSON.stringify(v); };
  const synList = s => (s ? Object.entries(s).map(([k, v]) => [k, [].concat(v == null ? [] : v).map(String)]) : null);
  rag.define('index.bm25', {
    id: () => 'index', phase: 'build', in: ['chunks'], out: 'index',
    describe: p => Object.entries(p.fields || DEFAULT_FIELDS).map(([k, v]) => k + '×' + v).join(' ') + ' (Go)',
    all: ([chunks], ctx, p) => {
      chunks = chunks || [];
      const fields = p.fields || DEFAULT_FIELDS;
      const names = Object.keys(fields);
      const texts = chunks.map(c => names.map(f => fieldText(c, f)));
      const ix = h.build(chunks.map(c => c.id), chunks.map(c => c.docId), texts,
        names.map(f => [f, Number(fields[f])]), p.k1 ?? 1.2, p.b ?? 0.75, p.stem !== false);
      return {
        kind: 'bm25', N: ix.n, fields,
        search: (q, k = 20, o = {}) => ix.search(String(q), Number(k), synList(o.synonyms), o.only ? [...o.only] : null),
        searchIn: (docId, q, k = 3, o = {}) => ix.searchIn(String(docId), String(q), Number(k), synList(o.synonyms)),
        toJSON: () => ({ kind: 'bm25', chunks: ix.n, terms: ix.terms, avgLen: +ix.avg.toFixed(1), fields }),
      };
    },
  });
})`

func (Bm25Native) Install(vm *goja.Runtime, rag *goja.Object) error {
	h := vm.NewObject()
	_ = h.Set("build", func(ids, docIDs []string, texts [][]any, fields [][]any, k1, b float64, doStem bool) (*goja.Object, error) {
		fs := make([]Field, len(fields))
		for i, f := range fields {
			if len(f) != 2 {
				return nil, fmt.Errorf("index.bm25: bad field spec %v", f)
			}
			name, _ := f[0].(string)
			w, ok := toFloat(f[1])
			if !ok {
				return nil, fmt.Errorf("index.bm25: field %q weight is not a number", name)
			}
			fs[i] = Field{Path: name, Weight: w}
		}
		if len(texts) != len(ids) || len(docIDs) != len(ids) {
			return nil, fmt.Errorf("index.bm25: %d ids, %d docIds, %d text rows", len(ids), len(docIDs), len(texts))
		}
		ix := NewBm25(len(ids), func(i int) string { return ids[i] }, func(i int) string { return docIDs[i] },
			func(i, f int) (string, bool) {
				if f < len(texts[i]) {
					if s, ok := texts[i][f].(string); ok {
						return s, true
					}
				}
				return "", false
			}, fs, k1, b, doStem)
		return bm25Object(vm, ix), nil
	})
	v, err := vm.RunString(bm25JS)
	if err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return fmt.Errorf("bm25 installer is not a function")
	}
	_, err = fn(goja.Undefined(), rag, h)
	return err
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}

func synonymsFrom(v [][]any) []Synonym {
	if v == nil {
		return nil
	}
	out := make([]Synonym, 0, len(v))
	for _, e := range v {
		if len(e) != 2 {
			continue
		}
		key, _ := e[0].(string)
		s := Synonym{Key: key}
		if vals, ok := e[1].([]any); ok {
			for _, x := range vals {
				if str, ok := x.(string); ok {
					s.Vals = append(s.Vals, str)
				}
			}
		}
		out = append(out, s)
	}
	return out
}

func hitsToJS(vm *goja.Runtime, hits []Hit) goja.Value {
	out := make([]any, len(hits))
	for i, h := range hits {
		o := vm.NewObject()
		_ = o.Set("id", h.ID)
		_ = o.Set("score", h.Score)
		why := vm.NewObject()
		for _, w := range h.Why {
			_ = why.Set(w.Term, w.Score)
		}
		_ = o.Set("why", why)
		out[i] = o
	}
	return vm.NewArray(out...)
}

func bm25Object(vm *goja.Runtime, ix *Bm25) *goja.Object {
	o := vm.NewObject()
	_ = o.Set("n", ix.N)
	_ = o.Set("terms", ix.terms)
	_ = o.Set("avg", ix.Avg)
	_ = o.Set("search", func(q string, k int, syn [][]any, only []int64) goja.Value {
		var set map[int]bool
		if only != nil {
			set = make(map[int]bool, len(only))
			for _, i := range only {
				set[int(i)] = true
			}
		}
		return hitsToJS(vm, ix.Search(q, k, synonymsFrom(syn), set))
	})
	_ = o.Set("searchIn", func(docID, q string, k int, syn [][]any) goja.Value {
		return hitsToJS(vm, ix.SearchIn(docID, q, k, synonymsFrom(syn)))
	})
	return o
}
