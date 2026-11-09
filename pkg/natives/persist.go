package natives

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/go-go-golems/rag-workbench/pkg/artifacts"
	"github.com/go-go-golems/rag-workbench/pkg/rtenv"
	"github.com/go-go-golems/rag-workbench/pkg/timing"
)

// Version names the behavior of the Go components. Bump it whenever a native
// component's output changes for the same input (a port fix, a new field), so
// persisted artifacts from the old version are not reused (it is part of the
// artifact namespace, see runner.ArtifactNamespace).
const Version = "2026-11-04.3" // RAGWB-003: path-derived doc ids, per-doc chunk ord; bleve BM25 field statistics

// Persist installs rag's env.artifacts hook backed by an artifact store:
//
//	has(key)                     → { count, type } | null
//	save(key, type, data, count)    data: a JSON value, or a Go-backed handle (__go) with a codec
//	load(key)                    → data
//
// Plain data goes through the VM's own JSON.stringify/parse, so stored docs and
// chunks are exactly what JSON round-trips (the engine's JSON codec).
type Persist struct {
	Store  *artifacts.Store
	Timing *timing.Recorder // optional: per-load read/decode times
}

func (Persist) Name() string { return "artifacts" }

func (p Persist) Install(vm *goja.Runtime, rag *goja.Object) error {
	return p.InstallEnv(vm, rag, rtenv.Env{})
}

// InstallEnv installs the hook; indexes opened from the store close with the runtime.
func (p Persist) InstallEnv(vm *goja.Runtime, rag *goja.Object, env rtenv.Env) error {
	if p.Store == nil {
		return nil
	}
	addCloser := env.AddCloser
	if addCloser == nil || env.Resources == nil {
		return fmt.Errorf("artifacts: resource registry is required")
	}
	mv, err := vm.RunString(recordsViewJS)
	if err != nil {
		return err
	}
	mkView, ok := goja.AssertFunction(mv)
	if !ok {
		return fmt.Errorf("artifacts: record view constructor is not a function")
	}
	jsonObj := vm.Get("JSON").ToObject(vm)
	stringify, ok1 := goja.AssertFunction(jsonObj.Get("stringify"))
	parse, ok2 := goja.AssertFunction(jsonObj.Get("parse"))
	if !ok1 || !ok2 {
		return fmt.Errorf("artifacts: JSON is not available")
	}
	a := vm.NewObject()
	_ = a.Set("describe", func(key, lineage string, deps []string) {
		if err := p.Store.Describe(key, lineage, deps); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = a.Set("publish", func(slot, id string, roots []string) {
		if err := p.Store.Publish(artifacts.Snapshot{ID: id, Slot: slot, Roots: roots}); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	_ = a.Set("pin", func(keys []string) func() {
		release, err := p.Store.Pin(keys)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		managed, err := env.Resources.Lease(release)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return managed
	})

	// lineage index (RAGWB-003 §6.4): the previous stored output of "this node of this pipeline"
	_ = a.Set("lineageLatest", func(lineage string) goja.Value {
		e, err := p.Store.LineageLatest(lineage)
		if err != nil || e == nil {
			return goja.Null()
		}
		o := vm.NewObject()
		_ = o.Set("storeKey", e.StoreKey)
		_ = o.Set("digest", e.Digest)
		return o
	})
	_ = a.Set("lineageList", func(lineage string) goja.Value {
		es, err := p.Store.LineageList(lineage)
		if err != nil {
			debugf("lineage %s: %v", lineage, err)
		}
		out := make([]any, len(es))
		for i, e := range es {
			o := vm.NewObject()
			_ = o.Set("storeKey", e.StoreKey)
			_ = o.Set("digest", e.Digest)
			_ = o.Set("inputs", strArray(vm, e.Inputs))
			out[i] = o
		}
		return vm.NewArray(out...)
	})
	_ = a.Set("lineageRecord", func(lineage, storeKey, digest string, inputs []string) {
		if err := p.Store.LineageRecord(artifacts.LineageEntry{Lineage: lineage, StoreKey: storeKey, Digest: digest, Inputs: inputs}); err != nil {
			debugf("lineage %s: %v", lineage, err)
		}
	})
	// delta builds write their outputs into temp directories of the store's
	// namespace (saved = moved in); the ones never saved go when the runtime closes
	var temps []string
	addCloser(func() error {
		for _, t := range temps {
			_ = os.RemoveAll(t)
		}
		return nil
	})
	_ = a.Set("tempDir", func() string {
		t, err := p.Store.TempDir()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		temps = append(temps, t)
		return t
	})
	_ = a.Set("view", func(r goja.Value) goja.Value {
		v, err := mkView(goja.Undefined(), r)
		if err != nil {
			panic(err)
		}
		return v
	})
	_ = a.Set("has", func(key string) goja.Value {
		m, err := p.Store.Has(key)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if m == nil {
			return goja.Null()
		}
		o := vm.NewObject()
		_ = o.Set("count", m.Count)
		_ = o.Set("type", m.Type)
		return o
	})
	_ = a.Set("save", func(key, typ string, v goja.Value, count int) {
		t0 := time.Now()
		if ix := bleveHandle(v); ix != nil {
			// a directory payload: move the built index into the store and reopen it there
			err := ix.Relocate(func(dir string) (string, error) {
				if err := p.Store.SaveDir(key, typ, "bleve", count, dir); err != nil {
					return "", err
				}
				_, path, err := p.Store.Path(key)
				return path, err
			})
			debugf("save %s bleve: %v", typ, time.Since(t0))
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("%s: %w", typ, err)))
			}
			return
		}
		if r := records2Handle(v); r != nil {
			// a delta output (RAGWB-003): built in the store's temp area, moved in and reopened there
			err := p.saveRecords2(key, typ, count, r)
			debugf("save %s records2 (delta): %v", typ, time.Since(t0))
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("%s: %w", typ, err)))
			}
			return
		}
		var r2 []R2Doc
		var kind string
		var err error
		if chunks := chunksHandle(v); chunks != nil {
			kind = kindChunks
			r2, err = ChunksToR2(chunks)
		} else if docs := docsHandle(v); docs != nil {
			kind = kindDocs
			r2, err = DocsToR2(docs)
		}
		if kind != "" {
			// a directory payload of 64 shards read one record at a time (codec "records2")
			var tmp string
			if err == nil {
				tmp, err = p.Store.TempDir()
			}
			if err == nil {
				if err = WriteRecords2(tmp, kind, r2); err == nil {
					err = p.Store.SaveDir(key, typ, "records2", count, tmp)
				}
				_ = os.RemoveAll(tmp)
			}
			debugf("save %s records2: %v", typ, time.Since(t0))
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("%s: %w", typ, err)))
			}
			return
		}
		codec, data, err := encodeArtifact(vm, stringify, v)
		t1 := time.Now()
		if err == nil {
			err = p.Store.Save(key, typ, codec, count, data)
		}
		debugf("save %s %s: encode %v, write %v, %d bytes", typ, codec, t1.Sub(t0), time.Since(t1), len(data))
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("%s: %w", typ, err)))
		}
	})
	_ = a.Set("load", func(key string) goja.Value {
		t0 := time.Now()
		if m, err := p.Store.Has(key); err == nil && m != nil && m.Kind == artifacts.KindFile && m.Codec == "records" {
			_, path, err := p.Store.Path(key)
			var r *ChunkRecords
			if err == nil {
				r, err = OpenChunkRecords(path)
			}
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("stored artifact %s (%s) could not be opened: %w; delete it with `ragwb store prune --all`", key, m.Type, err)))
			}
			view, err := mkView(goja.Undefined(), ownResource(vm, env, recordsObject(vm, r), r))
			if err != nil {
				panic(err)
			}
			p.Timing.Add("store", m.Type, float64(time.Since(t0).Microseconds())/1000,
				fmt.Sprintf("records · open %v · %d chunks · %.1f MB on disk", time.Since(t0).Round(time.Microsecond), r.Len(), float64(m.Size)/(1<<20)))
			return view
		}
		if m, err := p.Store.Has(key); err == nil && m != nil && m.Kind == artifacts.KindDir && m.Codec == "records2" {
			_, path, err := p.Store.Path(key)
			var r *Records2
			if err == nil {
				r, err = OpenRecords2(path)
			}
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("stored artifact %s (%s) could not be opened: %w; delete it with `ragwb store prune --all`", key, m.Type, err)))
			}
			view, err := mkView(goja.Undefined(), ownResource(vm, env, records2Object(vm, r), r))
			if err != nil {
				panic(err)
			}
			p.Timing.Add("store", m.Type, float64(time.Since(t0).Microseconds())/1000,
				fmt.Sprintf("records2 · open %v · %d %s · %.1f MB on disk", time.Since(t0).Round(time.Microsecond), r.Len(), r.Kind(), float64(m.Size)/(1<<20)))
			return view
		}
		if m, err := p.Store.Has(key); err == nil && m != nil && m.Kind == artifacts.KindDir && m.Codec == "bleve" {
			_, path, err := p.Store.Path(key)
			var ix *BleveBm25
			if err == nil {
				ix, err = OpenBleveBm25(path)
			}
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("stored artifact %s (%s) could not be opened: %w; delete it with `ragwb store prune --all`", key, m.Type, err)))
			}
			p.Timing.Add("store", m.Type, float64(time.Since(t0).Microseconds())/1000,
				fmt.Sprintf("bleve · open %v · %.1f MB on disk", time.Since(t0).Round(time.Microsecond), float64(m.Size)/(1<<20)))
			return ownResource(vm, env, bleveObject(vm, ix), ix)
		}
		m, data, err := p.Store.Load(key)
		t1 := time.Now()
		if err == nil {
			var v goja.Value
			if v, err = decodeArtifact(vm, parse, m.Codec, data); err == nil {
				debugf("load %s %s: read %v, decode %v, %d bytes", m.Type, m.Codec, t1.Sub(t0), time.Since(t1), len(data))
				p.Timing.Add("store", m.Type, float64(time.Since(t0).Microseconds())/1000,
					fmt.Sprintf("%s · read %v · decode %v · %.1f MB", m.Codec, t1.Sub(t0).Round(time.Millisecond), time.Since(t1).Round(time.Millisecond), float64(len(data))/(1<<20)))
				return v
			}
		}
		panic(vm.NewGoError(fmt.Errorf("stored artifact %s (%s) could not be loaded: %w; delete it with `ragwb store prune --all`", key, m.Type, err)))
	})
	configure, ok := goja.AssertFunction(rag.Get("configure"))
	if !ok {
		return fmt.Errorf("rag.configure is not a function")
	}
	cfg := vm.NewObject()
	_ = cfg.Set("artifacts", a)
	_, err = configure(rag, cfg)
	return err
}

func encodeArtifact(vm *goja.Runtime, stringify goja.Callable, v goja.Value) (string, []byte, error) {
	if o, ok := v.(*goja.Object); ok {
		if h := o.Get(goHandleKey); h != nil {
			switch x := h.Export().(type) {
			case *Bm25:
				b, err := EncodeBm25(x)
				return "bm25", b, err
			case []*Doc:
				b, err := EncodeDocs(x)
				return "docs", b, err
			case []Chunk:
				b, err := EncodeChunks(x)
				return "chunks", b, err
			case *vecIndex:
				b, err := EncodeVectors(x)
				return "vectors", b, err
			case *Terms:
				b, err := EncodeTerms(x)
				return "terms", b, err
			case *Manifest:
				b, err := EncodeManifest(x)
				return "manifest", b, err
			default:
				return "", nil, fmt.Errorf("no artifact codec for %T", x)
			}
		}
	}
	s, err := stringify(goja.Undefined(), v)
	if err != nil {
		return "", nil, err
	}
	if goja.IsUndefined(s) {
		return "", nil, fmt.Errorf("value is not JSON-serializable")
	}
	return "json", []byte(s.String()), nil
}

func decodeArtifact(vm *goja.Runtime, parse goja.Callable, codec string, data []byte) (goja.Value, error) {
	switch codec {
	case "json":
		return parse(goja.Undefined(), vm.ToValue(string(data)))
	case "bm25":
		ix, err := DecodeBm25(data)
		if err != nil {
			return nil, err
		}
		return bm25Object(vm, ix), nil
	case "docs":
		docs, err := DecodeDocs(data)
		if err != nil {
			return nil, err
		}
		return docsArray(vm, docs), nil
	case "chunks":
		chunks, err := DecodeChunks(data)
		if err != nil {
			return nil, err
		}
		return chunksArray(vm, chunks), nil
	case "manifest":
		m, err := DecodeManifest(data)
		if err != nil {
			return nil, err
		}
		return manifestObject(vm, m), nil
	case "terms":
		t, err := DecodeTerms(data)
		if err != nil {
			return nil, err
		}
		return termsObject(vm, t), nil
	case "vectors": // the raw index; index.vector's persist.load binds it (h.restore)
		ix, err := DecodeVectors(data)
		if err != nil {
			return nil, err
		}
		return vm.ToValue(ix), nil
	}
	return nil, fmt.Errorf("unknown artifact codec %q", codec)
}

// debugf traces artifact loads and saves to stderr when RAGWB_STORE_DEBUG is set.
func debugf(format string, args ...any) {
	if os.Getenv("RAGWB_STORE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "store: "+format+"\n", args...)
	}
}

// bleveHandle returns the Go index behind a bleve index.bm25 handle, or nil.
func bleveHandle(v goja.Value) *BleveBm25 {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	h := o.Get(goHandleKey)
	if h == nil {
		return nil
	}
	ix, _ := h.Export().(*BleveBm25)
	return ix
}

// chunksHandle returns the Go chunks behind a chunks output (the JS array made
// by chunksArray), or nil.
func chunksHandle(v goja.Value) []Chunk {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	h := o.Get(goHandleKey)
	if h == nil {
		return nil
	}
	cs, _ := h.Export().([]Chunk)
	return cs
}

// recordsObject is the Go side of a RecordView.
func recordsObject(vm *goja.Runtime, r *ChunkRecords) *goja.Object {
	o := vm.NewObject()
	must := func(err error) {
		if err != nil {
			panic(vm.NewGoError(err))
		}
	}
	_ = o.Set("len", func() int { return r.Len() })
	_ = o.Set("has", func(id string) bool { return r.Has(id) })
	_ = o.Set("get", func(id string) goja.Value {
		c, ok, err := r.Get(id)
		must(err)
		if !ok {
			return goja.Null()
		}
		return chunkToJS(vm, &c)
	})
	_ = o.Set("slice", func(from, to int) goja.Value {
		cs, err := r.Slice(from, to)
		must(err)
		out := make([]any, len(cs))
		for i := range cs {
			out[i] = chunkToJS(vm, &cs[i])
		}
		return vm.NewArray(out...)
	})
	_ = o.Set("all", func() goja.Value {
		cs, err := r.Slice(0, r.Len())
		must(err)
		return chunksArray(vm, cs)
	})
	_ = o.Set("order", func(docID string) []string {
		ids, err := r.Order(docID)
		must(err)
		return ids
	})
	_ = o.Set("close", func() { _ = r.Close() })
	return o
}

// recordsViewJS makes a stored chunks output usable where the engine expected
// the chunks array or the art.chunks Map: iteration yields chunks in array
// order (like the array), get/has/size behave like the Map, order(docId) gives
// a document's chunk ids (art.order), toArray() materializes the array for
// build components that need one. Recently read chunks are cached (identity
// holds within a query).
const recordsViewJS = `(function () {
  const CACHE = 4096, BATCH = 512;
  class RecordView {
    constructor(r) {
      Object.defineProperty(this, '__records', { value: r });
      Object.defineProperty(this, '_cache', { value: new Map() });
      this.size = r.len();
    }
    get length() { return this.size; }
    get(id) {
      const k = String(id);
      let v = this._cache.get(k);
      if (v === undefined) {
        v = this.__records.get(k);
        if (v === null) return undefined;
        if (this._cache.size >= CACHE) this._cache.delete(this._cache.keys().next().value);
        this._cache.set(k, v);
      }
      return v;
    }
    has(id) { return this._cache.has(String(id)) || this.__records.has(String(id)); }
    order(docId) { return this.__records.order(String(docId)); }
    *values() { for (let i = 0; i < this.size; i += BATCH) yield* this.__records.slice(i, Math.min(this.size, i + BATCH)); }
    *keys() { for (const c of this.values()) yield c.id; }
    *entries() { for (const c of this.values()) yield [c.id, c]; }
    forEach(f) { for (const c of this.values()) f(c, c.id, this); }
    [Symbol.iterator]() { return this.values(); }
    toArray() { return this.__records.all(); }
    toJSON() { return { records: this.size }; }
  }
  return r => new RecordView(r);
})()`

// saveRecords2 stores a records2 output under key. A directory in the store's
// temp area (a delta build's output) is moved in; any other (an input passed
// through, already stored under another key) is linked into a new directory
// first, so the store never moves an artifact out from under its key.
func (p Persist) saveRecords2(key, typ string, count int, r *Records2) error {
	dir := r.Dir()
	if !strings.HasPrefix(filepath.Base(dir), ".tmp-") || filepath.Dir(dir) != filepath.Join(p.Store.Dir, p.Store.NS) {
		tmp, err := p.Store.TempDir()
		if err != nil {
			return err
		}
		if err := CowRecords2(r, tmp, nil, nil); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		dir = tmp
	}
	if err := p.Store.SaveDir(key, typ, "records2", count, dir); err != nil {
		return err
	}
	_, path, err := p.Store.Path(key)
	if err != nil {
		return err
	}
	if dir != r.Dir() {
		return nil
	}
	return r.Relocate(path)
}
