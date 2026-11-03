package natives

import (
	"fmt"

	"github.com/dop251/goja"
)

// TermsNative installs the Go index.terms (Terms) under rag.js's type name.
// Value extraction stays in JS with rag.js's own rules, so the index is built
// from exactly the strings TermsIndex would see.
type TermsNative struct{}

func (TermsNative) Name() string { return "index.terms" }

const termsJS = `(function (rag, h) {
  const DEFAULT_KEYS = ['type', 'tags', 'status'];
  const arr = v => (v == null ? [] : Array.isArray(v) ? v : [v]);
  const getPath = (obj, path) => { if (!path) return obj; let cur = obj; for (const k of String(path).split('.')) { if (cur == null) return undefined; cur = cur[k]; } return cur; };
  const wrap = ix => {
    const o = {
      kind: 'terms', keys: ix.keys, N: ix.n,
      search: (q, k = 20) => ix.search(String(q), Number(k)),
      values: key => ix.values(String(key)),
      toJSON: () => ({ kind: 'terms', keys: ix.keys, values: ix.nvals }),
    };
    Object.defineProperty(o, '__ix', { value: ix });
    return o;
  };
  rag.define('index.terms', {
    id: () => 'index', phase: 'build', in: ['chunks'], out: 'index',
    describe: p => (p.keys || DEFAULT_KEYS).join(' · ') + ' (Go)',
    all: ([chunks], ctx, p) => {
      chunks = chunks || [];
      const keys = p.keys || DEFAULT_KEYS;
      const vals = chunks.map(c => keys.map(key => arr(getPath(c.meta, key)).map(v => String(v).toLowerCase())));
      return wrap(h.build(chunks.map(c => c.id), keys, vals));
    },
    // artifact store: the Go index travels as its handle (codec "terms")
    persist: { save: v => v.__ix, load: ix => wrap(ix) },
  });
})`

func (TermsNative) Install(vm *goja.Runtime, rag *goja.Object) error {
	h := vm.NewObject()
	_ = h.Set("build", func(ids, keys []string, vals [][][]string) (*goja.Object, error) {
		if len(vals) != len(ids) {
			return nil, fmt.Errorf("index.terms: %d ids, %d value rows", len(ids), len(vals))
		}
		return termsObject(vm, NewTerms(ids, keys, vals)), nil
	})
	v, err := vm.RunString(termsJS)
	if err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(v)
	if !ok {
		return fmt.Errorf("terms installer is not a function")
	}
	_, err = fn(goja.Undefined(), rag, h)
	return err
}

func termsObject(vm *goja.Runtime, t *Terms) *goja.Object {
	o := vm.NewObject()
	_ = o.DefineDataProperty(goHandleKey, vm.ToValue(t), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	keys := make([]any, len(t.Keys))
	for i, k := range t.Keys {
		keys[i] = k
	}
	_ = o.Set("keys", vm.NewArray(keys...))
	_ = o.Set("n", t.N)
	_ = o.Set("nvals", len(t.Vals))
	_ = o.Set("search", func(q string, k int) goja.Value {
		hits := t.Search(q, k)
		out := make([]any, len(hits))
		for i, h := range hits {
			ho := vm.NewObject()
			_ = ho.Set("id", h.ID)
			_ = ho.Set("score", h.Score)
			why := vm.NewObject()
			for _, kv := range h.Why {
				_ = why.Set(kv[0], kv[1])
			}
			_ = ho.Set("why", why)
			out[i] = ho
		}
		return vm.NewArray(out...)
	})
	_ = o.Set("values", func(key string) goja.Value {
		out := vm.NewObject()
		for _, kv := range t.Values(key) {
			_ = out.Set(kv[0].(string), kv[1])
		}
		return out
	})
	return o
}
