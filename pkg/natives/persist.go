package natives

import (
	"fmt"
	"os"
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
const Version = "2026-11-03.1"

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
	if addCloser == nil {
		addCloser = func(func() error) {}
	}
	jsonObj := vm.Get("JSON").ToObject(vm)
	stringify, ok1 := goja.AssertFunction(jsonObj.Get("stringify"))
	parse, ok2 := goja.AssertFunction(jsonObj.Get("parse"))
	if !ok1 || !ok2 {
		return fmt.Errorf("artifacts: JSON is not available")
	}
	a := vm.NewObject()
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
		if m, err := p.Store.Has(key); err == nil && m != nil && m.Kind == artifacts.KindDir && m.Codec == "bleve" {
			_, path, err := p.Store.Path(key)
			var ix *BleveBm25
			if err == nil {
				ix, err = OpenBleveBm25(path)
			}
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("stored artifact %s (%s) could not be opened: %w; delete it with `ragwb store prune --all`", key, m.Type, err)))
			}
			addCloser(ix.Close)
			p.Timing.Add("store", m.Type, float64(time.Since(t0).Microseconds())/1000,
				fmt.Sprintf("bleve · open %v · %.1f MB on disk", time.Since(t0).Round(time.Microsecond), float64(m.Size)/(1<<20)))
			return bleveObject(vm, ix)
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
	_, err := configure(rag, cfg)
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
