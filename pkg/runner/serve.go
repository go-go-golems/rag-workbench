package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-go-golems/go-go-goja/pkg/jsverbs"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// AppSpec names what `ragwb serve` serves: an app directory whose app.js exports
// { app, plan? } (the app's gui; its plan comes from app.use(plan)), or a
// workbench script (*.wb.js) that returns { plan, app? }.
type AppSpec struct {
	Name  string // URL segment, e.g. "reports"
	Path  string // app directory (with app.js) or a workbench script
	Files []File // corpus for rag.files(...) sources (script form)
	// Verbs are the app directory's jsverbs verbs (RAGWB-004 endpoints); the
	// runtime factory must then use Verbs.RequireLoader(). Nil: no verb endpoints.
	Verbs *jsverbs.Registry
}

// ServedApp is one opened app: a long-lived runtime holding the built plan and a
// rag Session, so each query pays only for the live nodes (P7). Runs on the same
// app share the runtime's owner goroutine: JS never runs concurrently, but an
// LLM call awaiting in one run does not block another run.
type ServedApp struct {
	Name string
	// Info is the open response: { id, title, ir, app, build, stats }.
	Info json.RawMessage
	rt   *engine.Runtime
	stop context.CancelFunc
	once sync.Once
	// verb endpoints
	verbs  *jsverbs.Registry
	verbMu sync.Mutex // held from announcing a scope until the verb took it (Invoke)
}

// The serve harness evaluates the app once and keeps a run/lookup API in
// globalThis.__serve for later calls.
const serveHarness = `(async (__mode, __path, __src, __opts) => {
  const rag = require("ragwb"), gui = require("ragwb/gui"), kit = require("ragwb/kit");
  rag.configure({ files: () => __opts.files || [] });
  // app.js may declare verbs; without the jsverbs loader (no scan) the declarations are no-ops
  for (const k of ['__package__', '__section__', '__verb__']) if (typeof globalThis[k] !== 'function') globalThis[k] = function () {};
  if (typeof globalThis.doc !== 'function') globalThis.doc = function () { return ''; };
  const now = () => (typeof performance !== 'undefined' ? performance.now() : Date.now());
  let out;
  if (__mode === 'module') out = require(__opts.modulePath || __path) || {};
  else {
    const log = (...a) => console.log(...a);
    const fn = new Function('rag', 'gui', 'log', 'kit', '"use strict";return (async () => {\n' + __src + '\n})();');
    out = (await fn(rag, gui, log, kit)) || {};
  }
  const app = out.app || null;
  const plan = out.plan || (app && app.plan) || null;
  if (!plan) throw new Error(__mode === 'module'
    ? 'app.js must export { app } built with gui.app(...).use(plan), or { plan }'
    : 'script must return { plan, app? }');
  const eng = rag.engine();
  const ir = rag.compile(plan);
  const build = await eng.build(ir);
  if (!build.ok) throw new Error('build failed:\n' + build.errors.join('\n'));
  const appIr = app ? (app.kind === 'gui' ? app : app.compile()) : kit.searchApp(plan, { id: ir.id, title: ir.id }).compile();
  const t0 = now();
  const art0 = eng.artifacts(ir); // load chunks and indexes now, not on the first keystroke
  const warm = now() - t0;
  const session = eng.session(ir);
  const art = () => eng.artifacts(ir);

  const neighbors = (a, id) => {
    const c = a.chunks.get(id);
    if (!c) return null;
    const list = a.order.get(c.docId) || [], i = list.indexOf(id);
    return { prev: i > 0 ? list[i - 1] : null, next: i >= 0 && i < list.length - 1 ? list[i + 1] : null };
  };
  // chunk bodies for ids, plus neighbor links for nbIds (whose prev/next bodies come along,
  // so the chunk view's ← → work without a round trip)
  const pack = (a, ids, nbIds) => {
    const chunks = {}, nb = {};
    for (const id of nbIds) {
      const n = neighbors(a, id);
      if (!n) continue;
      nb[id] = n;
      if (n.prev) ids.add(n.prev);
      if (n.next) ids.add(n.next);
    }
    for (const id of ids) { const c = a.chunks.get(id); if (c) chunks[id] = c; }
    return { chunks, neighbors: nb };
  };
  const NB_HITS = 40;
  // after a rebuild (a query found the sources changed, or refresh): release the old version
  let artKey = art0.key;
  // gc keeps the outputs of every plan run recently: the app's and those its verbs ran
  const collect = () => { const a = art(); if (a && a.key !== artKey) { artKey = a.key; return eng.gc([ir, ...eng.recentIrs()]); } return null; };
  // verb endpoints (RAGWB-004): a captured verb function is wrapped once, so that
  // an invocation the host announced (__ragwbPending) runs in its request scope
  globalThis.__ragwbWrapVerb = (entry, name) => {
    const f = entry && entry[name];
    if (typeof f !== 'function' || f.__ragwb) return typeof f === 'function';
    const w = function (...args) {
      const p = globalThis.__ragwbPending;
      globalThis.__ragwbPending = null;
      if (!p) return f.apply(this, args);
      p.entered();
      return rag.withScope(p.scope, () => f.apply(this, args));
    };
    w.__ragwb = true;
    entry[name] = w;
    return true;
  };
  globalThis.__serve = {
    // refresh: rebuild now if the sources changed (serve --watch), so the next query doesn't wait
    refresh: async () => {
      if (eng.artifacts(ir)) return 'null';
      const rep = await eng.build(ir);
      if (!rep.ok) return JSON.stringify({ ok: false, errors: rep.errors });
      const gc = collect();
      const nodes = Object.values(rep.nodes).filter(n => !n.reused).map(n => ({ id: n.id, ms: n.ms, incremental: !!n.incremental, delta: n.delta || null }));
      return JSON.stringify({ ok: true, ms: rep.ms, nodes, gc, chunks: art().chunks.size });
    },
    // emit(type, json): the engine events of this run (RAGWB-004: the shell's SSE);
    // holder.scope: its abort signal, for the host to cancel
    run: async (signalsJson, trigger, emit, holder) => {
      const sc = rag.newScope(emit ? ev => emit(String(ev.type), JSON.stringify(ev)) : () => {});
      if (holder) holder.scope = sc;
      const r = await session.run(JSON.parse(signalsJson || '{}'), {
        trigger: trigger || 'change', signal: sc.signal, detail: sc.detail,
        ...(emit ? { onEvent: sc.emit } : {}),
      });
      sc.close();
      collect();
      const t1 = now();
      const a = art();
      const ids = new Set();
      const add = x => { if (x && typeof x.id === 'string' && a.chunks.has(x.id)) ids.add(x.id); };
      for (const v of Object.values(r.outputs)) {
        if (Array.isArray(v)) v.forEach(add);
        else if (v && Array.isArray(v.evidence)) v.evidence.forEach(add);
      }
      if (r.answer && Array.isArray(r.answer.evidence)) r.answer.evidence.forEach(add);
      const p = pack(a, ids, r.hits.slice(0, NB_HITS).map(h => h.id));
      // the plain fields only: result.toJSON is the compact log form
      const result = { runId: r.runId, trigger: r.trigger, signals: r.signals, hits: r.hits, context: r.context, answer: r.answer,
        outputs: r.outputs, trace: r.trace, errors: r.errors, ms: r.ms, calls: r.calls };
      return JSON.stringify({ result, chunks: p.chunks, neighbors: p.neighbors, packMs: +(now() - t1).toFixed(2) });
    },
    chunk: id => JSON.stringify(pack(art(), new Set([id]), [id])),
    doc: id => {
      const d = art().docs.get(id);
      return JSON.stringify(d ? { id: d.id, name: d.name, title: d.title, meta: d.meta, links: d.links, path: d.path } : null);
    },
  };
  return JSON.stringify({
    id: ir.id, title: appIr.title || ir.id, ir: JSON.parse(JSON.stringify(ir)), app: appIr,
    build: { ok: build.ok, ms: build.ms, nodes: Object.values(build.nodes).map(n => ({ id: n.id, type: n.t, count: n.count, ms: n.ms, reused: !!n.reused, stored: !!n.stored })) },
    stats: { chunks: art0.chunks.size, indexes: [...art0.indexes.keys()], warmMs: +warm.toFixed(1) },
  });
})`

// OpenApp evaluates the app in a new long-lived runtime from factory, builds its
// plan (loading stored artifacts when the factory has a store) and keeps the
// session for Run. Close releases the runtime.
func OpenApp(ctx context.Context, factory *engine.RuntimeFactory, spec AppSpec) (*ServedApp, error) {
	mode, path, src, err := appSource(spec.Path)
	if err != nil {
		return nil, err
	}
	life, stop := context.WithCancel(context.Background())
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(life))
	if err != nil {
		stop()
		return nil, err
	}
	a := &ServedApp{Name: spec.Name, rt: rt, stop: stop, verbs: spec.Verbs}
	modulePath := ""
	if spec.Verbs != nil && mode == "module" {
		// the jsverbs loader knows the scanned files by module path (/app.js), and
		// requiring app.js through it is what captures verbs declared there
		root, _ := filepath.Abs(spec.Verbs.RootDir)
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			modulePath = "/" + filepath.ToSlash(rel)
		}
	}
	files := make([]any, 0, len(spec.Files))
	for _, f := range spec.Files {
		files = append(files, map[string]any{"name": f.Name, "text": f.Text})
	}
	v, err := ragjs.Eval(ctx, rt, "ragwb.serve.open", func(vm *goja.Runtime) (goja.Value, error) {
		h, err := vm.RunScript("ragwb-serve.js", serveHarness)
		if err != nil {
			return nil, err
		}
		fn, ok := goja.AssertFunction(h)
		if !ok {
			return nil, fmt.Errorf("serve harness is not a function")
		}
		return fn(goja.Undefined(), vm.ToValue(mode), vm.ToValue(path), vm.ToValue(src), vm.ToValue(map[string]any{"files": files, "modulePath": modulePath}))
	})
	if err != nil {
		a.Close()
		return nil, fmt.Errorf("%s: %w", spec.Path, err)
	}
	s, ok := v.(string)
	if !ok {
		a.Close()
		return nil, fmt.Errorf("%s: serve harness returned %T", spec.Path, v)
	}
	a.Info = json.RawMessage(s)
	return a, nil
}

// appSource decides between the module form (a directory with app.js, or a .js
// file that is not a workbench script) and the script form (*.wb.js).
func appSource(p string) (mode, path, src string, err error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", "", "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", "", "", err
	}
	if fi.IsDir() {
		abs = filepath.Join(abs, "app.js")
		if _, err := os.Stat(abs); err != nil {
			return "", "", "", fmt.Errorf("%s has no app.js (serve an app directory with app.js, or a *.wb.js script)", p)
		}
	}
	if strings.HasSuffix(abs, ".wb.js") {
		b, err := os.ReadFile(abs)
		if err != nil {
			return "", "", "", err
		}
		return "script", abs, string(b), nil
	}
	return "module", abs, "", nil
}

// RunResult is one query: the engine result plus chunk bodies for the ids it
// mentions (the gui reads chunks synchronously while rendering).
type RunResult struct {
	JSON   string        // {result, chunks, neighbors, packMs}
	EvalMS float64       // Go-side time for the whole call (engine + packing + JSON)
	Took   time.Duration // same, as a duration
}

// Run executes the plan's live nodes for signals (a JSON object). onText, when
// set, receives streamed LLM text (node id, text so far) on the runtime's goroutine.
func (a *ServedApp) Run(ctx context.Context, signals json.RawMessage, trigger string, sink EventSink) (*RunResult, error) {
	t0 := time.Now()
	var holder *goja.Object
	stop := make(chan struct{})
	defer close(stop)
	v, err := ragjs.Eval(ctx, a.rt, "ragwb.serve.run", func(vm *goja.Runtime) (goja.Value, error) {
		fn, err := a.method(vm, "run")
		if err != nil {
			return nil, err
		}
		var cb goja.Value = goja.Undefined()
		if sink != nil {
			cb = vm.ToValue(func(typ, data string) { sink.Emit(Event{Type: typ, Data: json.RawMessage(data)}) })
		}
		holder = vm.NewObject()
		// a cancelled request aborts the run (remaining nodes and LLM calls stop)
		go func() {
			select {
			case <-ctx.Done():
				a.abortScope(holder)
			case <-stop:
			}
		}()
		return fn(goja.Undefined(), vm.ToValue(string(signals)), vm.ToValue(trigger), cb, holder)
	})
	if err != nil {
		return nil, err
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("run returned %T", v)
	}
	took := time.Since(t0)
	return &RunResult{JSON: s, EvalMS: float64(took.Microseconds()) / 1000, Took: took}, nil
}

// abortScope aborts holder.scope (a rag request scope) on the owner.
func (a *ServedApp) abortScope(holder *goja.Object) {
	actx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = a.rt.Owner.Call(actx, "ragwb.serve.abort", func(_ context.Context, vm *goja.Runtime) (any, error) {
		sc, ok := holder.Get("scope").(*goja.Object)
		if !ok {
			return nil, nil
		}
		if f, ok := goja.AssertFunction(sc.Get("abort")); ok {
			_, _ = f(sc, vm.ToValue("request cancelled"))
		}
		return nil, nil
	})
}

// Chunk returns {chunks, neighbors} for one chunk id (a cache miss in the browser).
// Refresh rebuilds the app's plan if its sources changed since the last build
// (incrementally, from the stored previous version) and releases the old
// artifacts. It returns "null" when nothing changed, else a JSON summary.
func (a *ServedApp) Refresh(ctx context.Context) (string, error) {
	return a.call(ctx, "refresh", "")
}

func (a *ServedApp) Chunk(ctx context.Context, id string) (string, error) {
	return a.call(ctx, "chunk", id)
}

// Doc returns a document's metadata (no text) or null.
func (a *ServedApp) Doc(ctx context.Context, id string) (string, error) {
	return a.call(ctx, "doc", id)
}

func (a *ServedApp) call(ctx context.Context, name, arg string) (string, error) {
	v, err := ragjs.Eval(ctx, a.rt, "ragwb.serve."+name, func(vm *goja.Runtime) (goja.Value, error) {
		fn, err := a.method(vm, name)
		if err != nil {
			return nil, err
		}
		return fn(goja.Undefined(), vm.ToValue(arg))
	})
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s returned %T", name, v)
	}
	return s, nil
}

func (a *ServedApp) method(vm *goja.Runtime, name string) (goja.Callable, error) {
	o := vm.Get("__serve")
	if o == nil || goja.IsUndefined(o) {
		return nil, fmt.Errorf("app %s is not open", a.Name)
	}
	fn, ok := goja.AssertFunction(o.ToObject(vm).Get(name))
	if !ok {
		return nil, fmt.Errorf("__serve.%s is not a function", name)
	}
	return fn, nil
}

// Close releases the runtime.
func (a *ServedApp) Close() {
	a.once.Do(func() {
		_ = a.rt.Close(context.Background())
		a.stop()
	})
}
