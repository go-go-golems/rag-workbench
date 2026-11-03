// 01-goja-smoke: does the claude.ai prototype's rag.js/gui.js/kit.js run unmodified in goja?
// Usage: go run . <prototype-dir> [preset.js]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dop251/goja"
)

func must(err error) {
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
}

func main() {
	dir := os.Args[1]
	preset := "presets/minimal.js"
	if len(os.Args) > 2 {
		preset = os.Args[2]
	}
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	// minimal host shims
	_, _ = vm.RunString(`globalThis.console = { log: (...a) => __log(a.map(String).join(' ')), warn: (...a) => __log('WARN '+a.join(' ')), error: (...a) => __log('ERR '+a.join(' ')) };`)
	must(vm.Set("__log", func(s string) { fmt.Println("  js:", s) }))
	start := time.Now()
	vm.Set("__now", func() float64 { return float64(time.Since(start).Microseconds()) / 1000 })
	_, _ = vm.RunString(`globalThis.performance = { now: __now };`)
	for _, f := range []string{"rag.js", "gui.js", "kit.js"} {
		src, err := os.ReadFile(filepath.Join(dir, f))
		must(err)
		if os.Getenv("RAGWB_COMPAT") != "" {
			// goja lacks \p{..} Unicode property escapes: approximate letters+digits with explicit ranges
			src = []byte(strings.ReplaceAll(string(src), `\p{L}\p{N}`, `A-Za-z0-9\u00C0-\u024F\u0370-\u03FF\u0400-\u04FF\u3040-\u30FF\u4E00-\u9FFF`))
		}
		t0 := time.Now()
		_, err = vm.RunScript(f, string(src))
		if err != nil {
			fmt.Printf("load %s: FAIL %v\n", f, err)
			os.Exit(1)
		}
		fmt.Printf("load %s: ok (%v)\n", f, time.Since(t0))
	}
	corpus, err := os.ReadFile(filepath.Join(dir, "corpus.json"))
	must(err)
	var c struct {
		Files []map[string]any `json:"files"`
	}
	must(json.Unmarshal(corpus, &c))
	must(vm.Set("__corpus", c.Files))
	pre, err := os.ReadFile(filepath.Join(dir, preset))
	must(err)
	must(vm.Set("__preset", string(pre)))
	script := `
(async () => {
  const files = __corpus.map(f => ({ name: f.name, text: f.text }));
  rag.configure({ files: () => files, log: (...a) => console.log(...a), buildCalls: 'explicit' });
  const eng = rag.engine();
  const fn = new Function('rag', 'gui', 'log', 'kit', '"use strict";return (async () => {\n' + __preset + '\n})();');
  const out = await fn(rag, gui, console.log, kit);
  const ir = rag.compile(out.plan);
  let t0 = performance.now();
  const b = await eng.build(ir);
  const t1 = performance.now();
  const r = await eng.run(ir, { q: 'rebalance law' });
  const t2 = performance.now();
  const top = r.top ? r.top(3) : null;
  let ev = null;
  if (out.evals) { try { ev = await out.evals.run(ir, { engine: eng }); } catch (e) { ev = 'evals error: ' + (e.stack || e.message); } }
  let appIr = null;
  try { appIr = out.app && out.app.compile ? out.app.compile() : null; } catch (e) { appIr = 'app compile error: ' + e.message; }
  return JSON.stringify({
    files: files.length,
    nodes: ir.nodes.map(n => n.id + ':' + n.t),
    buildMs: Math.round(t1 - t0), runMs: Math.round(t2 - t1),
    build: { ok: b.ok, ms: b.ms, errors: b.errors, nodes: Object.values(b.nodes).map(n => n.id + ' n=' + n.count + ' ' + n.ms + 'ms') },
    run: r.toJSON(),
    top: top && top.map(h => [h.id, +(h.score||0).toFixed(3)]),
    evals: typeof ev === 'string' ? ev : ev && (ev.summary || ev.metrics || Object.keys(ev)),
    appIr: typeof appIr === 'string' ? appIr : appIr && { kind: appIr.kind, rootType: appIr.view && appIr.view.t, bytes: JSON.stringify(appIr).length },
  }, null, 1);
})()`
	v, err := vm.RunString(script)
	must(err)
	p, ok := v.Export().(*goja.Promise)
	if !ok {
		fmt.Println("not a promise")
		os.Exit(1)
	}
	// no event loop needed: only microtasks (goja drains them after RunString)
	switch p.State() {
	case goja.PromiseStateFulfilled:
		fmt.Println(p.Result().String())
	case goja.PromiseStateRejected:
		r := p.Result()
		if o, ok := r.(*goja.Object); ok && o.Get("stack") != nil {
			fmt.Println("REJECTED:", o.Get("stack").String())
		} else {
			fmt.Println("REJECTED:", r.String())
		}
		os.Exit(1)
	default:
		fmt.Println("PENDING (needs event loop / timers)")
	}
}
