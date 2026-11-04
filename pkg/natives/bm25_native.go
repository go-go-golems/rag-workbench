package natives

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dop251/goja"

	"github.com/go-go-golems/rag-workbench/pkg/rtenv"
)

// Bm25Native installs the Go implementations of index.bm25 under the
// prototype's type name. Two backends share one component definition, chosen
// per index by p.backend, else by Backend:
//
//   - "bleve" (default): BleveBm25, a disk-backed bleve index (RAGWB-002);
//     rankings may differ from rag.js within the quality gate (diary step 4)
//   - "memory": Bm25, an exact port of rag.js's Bm25Index (postings in memory)
//
// The returned index object has the JS class's surface (kind, N, fields,
// search, searchIn, toJSON) plus backend.
type Bm25Native struct {
	// Backend is the default when a plan does not set p.backend: "bleve" (default) or "memory".
	Backend string
	// TempRoot holds bleve indexes that are not (yet) stored artifacts ("" = os.TempDir()).
	TempRoot string
}

func (Bm25Native) Name() string { return "index.bm25" }

// Field texts are extracted in JS with rag.js's exact rules (getPath, arrays
// joined with ' ', non-strings JSON.stringify'd, null/” skipped) in one pass;
// tokenizing, postings and scoring happen in Go.
const bm25JS = `(function (rag, h) {
  const DEFAULT_FIELDS = { title: 2, headingPath: 3, text: 1 };
  const DEFAULT_BACKEND = h.defaultBackend;
  const getPath = (obj, path) => { if (!path) return obj; let cur = obj; for (const k of String(path).split('.')) { if (cur == null) return undefined; cur = cur[k]; } return cur; };
  const fieldText = (c, f) => { let v = getPath(c, f); if (Array.isArray(v)) v = v.join(' '); if (v == null || v === '') return null; return typeof v === 'string' ? v : JSON.stringify(v); };
  const synList = s => (s ? Object.entries(s).map(([k, v]) => [k, [].concat(v == null ? [] : v).map(String)]) : null);
  const backendOf = p => p.backend || DEFAULT_BACKEND;
  const wrap = (ix, fields) => {
    const bleve = ix.backend === 'bleve';
    const o = {
      kind: 'bm25', backend: ix.backend, N: ix.n, fields,
      search: (q, k = 20, o = {}) => ix.search(String(q), Number(k), synList(o.synonyms), o.only ? [...o.only].map(x => (bleve ? String(x) : x)) : null),
      searchIn: (docId, q, k = 3, o = {}) => ix.searchIn(String(docId), String(q), Number(k), synList(o.synonyms)),
      toJSON: () => (bleve ? { kind: 'bm25', backend: 'bleve', chunks: ix.n, terms: ix.terms, fields }
                           : { kind: 'bm25', chunks: ix.n, terms: ix.terms, avgLen: +ix.avg.toFixed(1), fields }),
    };
    Object.defineProperty(o, '__ix', { value: ix }); // not enumerable: invisible to JSON and the inspector
    return o;
  };
  rag.define('index.bm25', {
    id: () => 'index', phase: 'build', in: ['chunks'], out: 'index',
    describe: p => Object.entries(p.fields || DEFAULT_FIELDS).map(([k, v]) => k + '×' + v).join(' ') + ' (' + (backendOf(p) === 'bleve' ? 'bleve' : 'Go') + ')',
    all: ([chunks], ctx, p) => {
      chunks = chunks || [];
      const fields = p.fields || DEFAULT_FIELDS;
      const names = Object.keys(fields);
      const texts = chunks.map(c => names.map(f => fieldText(c, f)));
      const spec = names.map(f => [f, Number(fields[f])]);
      const backend = backendOf(p);
      if (backend === 'bleve') return wrap(h.buildBleve(chunks.map(c => c.id), chunks.map(c => c.docId), texts, spec, p.stem !== false, p.combined === true), fields);
      if (backend !== 'memory') throw new Error("index.bm25: unknown backend '" + backend + "' (memory | bleve)");
      return wrap(h.build(chunks.map(c => c.id), chunks.map(c => c.docId), texts, spec, p.k1 ?? 1.2, p.b ?? 0.75, p.stem !== false), fields);
    },
    // artifact store: the Go index travels as its handle (codec "bm25" or "bleve")
    persist: { save: v => v.__ix, load: (ix, p) => wrap(ix, p.fields || DEFAULT_FIELDS) },
    // delta build (bleve): clone the base index, delete the old chunks of changed
    // and removed docs, index the new ones. The memory backend rebuilds (8 s on the vault).
    delta: {
      apply(base, [chunksIn], ctx, p) {
        if (backendOf(p) !== 'bleve') return null;
        const d = chunksIn.delta, prev = base.load();
        if (!prev || !prev.__ix || prev.__ix.backend !== 'bleve') return null;
        const fields = p.fields || DEFAULT_FIELDS, names = Object.keys(fields);
        const up = d.upsert;
        const texts = up.map(c => names.map(f => fieldText(c, f)));
        const removedTexts = d.removed.map(c => names.map(f => fieldText(c, f)));
        const ix = h.deltaBleve(prev.__ix, ctx.store.tempDir(), d.removed.map(c => c.id), removedTexts, up.map(c => c.id), up.map(c => c.docId), texts);
        return { value: wrap(ix, fields), count: ix.n, delta: { full: false }, summary: { upserts: up.length, removes: d.removeIds.length } };
      },
    },
  });
})`

func (n Bm25Native) Install(vm *goja.Runtime, rag *goja.Object) error {
	return n.InstallEnv(vm, rag, rtenv.Env{})
}

func parseFields(component string, fields [][]any) ([]Field, error) {
	fs := make([]Field, len(fields))
	for i, f := range fields {
		if len(f) != 2 {
			return nil, fmt.Errorf("%s: bad field spec %v", component, f)
		}
		name, _ := f[0].(string)
		w, ok := toFloat(f[1])
		if !ok {
			return nil, fmt.Errorf("%s: field %q weight is not a number", component, name)
		}
		fs[i] = Field{Path: name, Weight: w}
	}
	return fs, nil
}

func textAt(texts [][]any) func(i, f int) (string, bool) {
	return func(i, f int) (string, bool) {
		if f < len(texts[i]) {
			if s, ok := texts[i][f].(string); ok {
				return s, true
			}
		}
		return "", false
	}
}

func (n Bm25Native) InstallEnv(vm *goja.Runtime, rag *goja.Object, env rtenv.Env) error {
	backend := n.Backend
	if backend == "" {
		backend = "bleve"
	}
	addCloser := env.AddCloser
	if addCloser == nil {
		addCloser = func(func() error) {}
	}
	h := vm.NewObject()
	_ = h.Set("defaultBackend", backend)
	_ = h.Set("build", func(ids, docIDs []string, texts [][]any, fields [][]any, k1, b float64, doStem bool) (*goja.Object, error) {
		fs, err := parseFields("index.bm25", fields)
		if err != nil {
			return nil, err
		}
		if len(texts) != len(ids) || len(docIDs) != len(ids) {
			return nil, fmt.Errorf("index.bm25: %d ids, %d docIds, %d text rows", len(ids), len(docIDs), len(texts))
		}
		ix := NewBm25(len(ids), func(i int) string { return ids[i] }, func(i int) string { return docIDs[i] }, textAt(texts), fs, k1, b, doStem)
		return bm25Object(vm, ix), nil
	})
	_ = h.Set("buildBleve", func(ids, docIDs []string, texts [][]any, fields [][]any, doStem, combined bool) (*goja.Object, error) {
		fs, err := parseFields("index.bm25", fields)
		if err != nil {
			return nil, err
		}
		if len(texts) != len(ids) || len(docIDs) != len(ids) {
			return nil, fmt.Errorf("index.bm25: %d ids, %d docIds, %d text rows", len(ids), len(docIDs), len(texts))
		}
		// built under TempRoot (the artifact store's namespace when there is one, so
		// storing it is a rename on the same filesystem); removed on Close unless stored
		root, err := os.MkdirTemp(n.TempRoot, ".tmp-bm25-")
		if err != nil {
			return nil, err
		}
		ix, err := BuildBleveBm25(filepath.Join(root, "index"), len(ids), func(i int) string { return ids[i] }, func(i int) string { return docIDs[i] }, textAt(texts), fs, BleveOptions{Stem: doStem, Combined: combined})
		if err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
		ix.SetCleanup(root)
		addCloser(ix.Close)
		return bleveObject(vm, ix), nil
	})
	_ = h.Set("deltaBleve", func(base goja.Value, tmp string, removeIDs []string, removedTexts [][]any, ids, docIDs []string, texts [][]any) (*goja.Object, error) {
		bo, ok := base.(*goja.Object)
		var b *BleveBm25
		if ok {
			if hv := bo.Get(goHandleKey); hv != nil {
				b, _ = hv.Export().(*BleveBm25)
			}
		}
		if b == nil {
			return nil, fmt.Errorf("index.bm25: delta base is not a bleve index")
		}
		if len(texts) != len(ids) || len(docIDs) != len(ids) {
			return nil, fmt.Errorf("index.bm25: %d ids, %d docIds, %d text rows", len(ids), len(docIDs), len(texts))
		}
		if len(removedTexts) != len(removeIDs) {
			return nil, fmt.Errorf("index.bm25: %d removed ids, %d removed text rows", len(removeIDs), len(removedTexts))
		}
		ix, err := b.ApplyDelta(filepath.Join(tmp, "index"), removeIDs, textAt(removedTexts), len(ids), func(i int) string { return ids[i] }, func(i int) string { return docIDs[i] }, textAt(texts))
		if err != nil {
			return nil, err
		}
		ix.SetCleanup(tmp)
		addCloser(ix.Close)
		return bleveObject(vm, ix), nil
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

// goHandleKey holds the Go value behind a JS handle (non-enumerable), for artifact codecs.
const goHandleKey = goDocKey

func bm25Object(vm *goja.Runtime, ix *Bm25) *goja.Object {
	o := vm.NewObject()
	_ = o.DefineDataProperty(goHandleKey, vm.ToValue(ix), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	_ = o.Set("backend", "memory")
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

func bleveObject(vm *goja.Runtime, ix *BleveBm25) *goja.Object {
	o := vm.NewObject()
	_ = o.DefineDataProperty(goHandleKey, vm.ToValue(ix), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	_ = o.Set("backend", "bleve")
	_ = o.Set("n", ix.Meta.N)
	_ = o.Set("terms", ix.Meta.Terms)
	_ = o.Set("search", func(q string, k int, syn [][]any, only []string) goja.Value {
		hits, err := ix.Search(q, k, synonymsFrom(syn), only)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return hitsToJS(vm, hits)
	})
	_ = o.Set("searchIn", func(docID, q string, k int, syn [][]any) goja.Value {
		hits, err := ix.SearchIn(docID, q, k, synonymsFrom(syn))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return hitsToJS(vm, hits)
	})
	return o
}
