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
}

// The serve harness evaluates the app once and keeps a run/lookup API in
// globalThis.__serve for later calls.
const serveHarness = `(async (__mode, __path, __src, __opts) => {
  const rag = require("ragwb"), gui = require("ragwb/gui"), kit = require("ragwb/kit");
  rag.configure({ files: () => __opts.files || [] });
  const now = () => (typeof performance !== 'undefined' ? performance.now() : Date.now());
  let out;
  if (__mode === 'module') out = require(__path) || {};
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
  globalThis.__serve = {
    run: async (signalsJson, trigger, onText) => {
      const r = await session.run(JSON.parse(signalsJson || '{}'), {
        trigger: trigger || 'change',
        ...(onText ? { onText: (node, text) => onText(String(node), String(text)) } : {}),
      });
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
	a := &ServedApp{Name: spec.Name, rt: rt, stop: stop}
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
		return fn(goja.Undefined(), vm.ToValue(mode), vm.ToValue(path), vm.ToValue(src), vm.ToValue(map[string]any{"files": files}))
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
func (a *ServedApp) Run(ctx context.Context, signals json.RawMessage, trigger string, onText func(node, text string)) (*RunResult, error) {
	t0 := time.Now()
	v, err := ragjs.Eval(ctx, a.rt, "ragwb.serve.run", func(vm *goja.Runtime) (goja.Value, error) {
		fn, err := a.method(vm, "run")
		if err != nil {
			return nil, err
		}
		var cb goja.Value = goja.Undefined()
		if onText != nil {
			cb = vm.ToValue(onText)
		}
		return fn(goja.Undefined(), vm.ToValue(string(signals)), vm.ToValue(trigger), cb)
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

// Chunk returns {chunks, neighbors} for one chunk id (a cache miss in the browser).
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
