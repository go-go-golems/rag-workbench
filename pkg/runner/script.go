package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"

	"github.com/go-go-golems/rag-workbench/pkg/ragjs"
)

// File is one corpus file handed to rag.configure({files}) for rag.files(glob) sources.
type File struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// ReadFlatCorpus reads *.md, *.markdown and *.txt files directly inside dir
// (not recursive), sorted by name: the shape the prototype's Files tab produced.
func ReadFlatCorpus(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := []File{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".md" && ext != ".markdown" && ext != ".txt" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, File{Name: e.Name(), Text: string(b)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// ScriptOptions controls RunWorkbenchScript.
type ScriptOptions struct {
	Files   []File         // corpus for rag.files(...) sources
	Signals map[string]any // overrides for the plan's signals (e.g. q, k)
	Trigger string         // 'change' (lexical only) or 'submit' (also LLM nodes); default 'change'
	Evals   bool           // also run the script's evals
	TopK    int            // hits to report; default 5
}

// ScriptReport is what a headless run of a workbench script produces.
type ScriptReport struct {
	Build    json.RawMessage `json:"build"`
	Run      json.RawMessage `json:"run"`
	Evals    json.RawMessage `json:"evals,omitempty"`
	Variants []string        `json:"variants,omitempty"`
	App      json.RawMessage `json:"app,omitempty"`
	Pending  json.RawMessage `json:"pending,omitempty"`
}

// The workbench form of a script: an async function body with rag, gui, log
// and kit in scope that returns {plan, evals, app, variants}. This mirrors
// sources/prototype/app.js:175 so presets run unchanged.
const workbenchHarness = `(async (__src, __opts) => {
  const rag = require("ragwb"), gui = require("ragwb/gui"), kit = require("ragwb/kit");
  rag.configure({ files: () => __opts.files || [] });
  const log = (...a) => console.log(...a);
  const fn = new Function('rag', 'gui', 'log', 'kit', '"use strict";return (async () => {\n' + __src + '\n})();');
  const out = (await fn(rag, gui, log, kit)) || {};
  if (!out.plan) throw new Error('script must return { plan, ... }');
  const eng = rag.engine();
  const ir = rag.compile(out.plan);
  const build = await eng.build(ir);
  const r = await eng.run(ir, __opts.signals || {}, { trigger: __opts.trigger || 'change' });
  const report = {
    build: { ok: build.ok, ms: build.ms, errors: build.errors,
             nodes: Object.values(build.nodes).map(n => ({ id: n.id, type: n.t, count: n.count, ms: n.ms, reused: n.reused, deferred: n.deferred || 0 })) },
    run: { trigger: r.trigger, signals: r.signals, ms: r.ms, calls: r.calls, errors: r.errors,
           top: r.top(__opts.topK || 5), trace: r.toJSON().trace,
           answer: r.answer ? { text: r.answer.text } : null },
    pending: eng.pending(ir),
  };
  if (__opts.evals && out.evals) {
    const ev = await out.evals.run(ir, { engine: eng });
    report.evals = ev.summary;
  }
  if (out.variants) report.variants = Object.keys(out.variants);
  if (out.app && out.app.compile) { const a = out.app.compile(); report.app = { kind: a.kind, id: a.id, title: a.title }; }
  return JSON.stringify(report);
})`

// RunWorkbenchScript runs a workbench-form script (e.g. a preset) headlessly.
func RunWorkbenchScript(ctx context.Context, factory *engine.RuntimeFactory, name, src string, opts ScriptOptions) (*ScriptReport, error) {
	rt, err := factory.NewRuntime(engine.WithStartupContext(ctx), engine.WithLifetimeContext(ctx))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close(context.Background()) }()

	files := make([]any, 0, len(opts.Files))
	for _, f := range opts.Files {
		files = append(files, map[string]any{"name": f.Name, "text": f.Text})
	}
	jsOpts := map[string]any{
		"files": files, "signals": opts.Signals, "trigger": opts.Trigger,
		"evals": opts.Evals, "topK": opts.TopK,
	}
	v, err := ragjs.Eval(ctx, rt, "ragwb.run", func(vm *goja.Runtime) (goja.Value, error) {
		h, err := vm.RunScript("ragwb-harness.js", workbenchHarness)
		if err != nil {
			return nil, err
		}
		fn, ok := goja.AssertFunction(h)
		if !ok {
			return nil, fmt.Errorf("harness is not a function")
		}
		return fn(goja.Undefined(), vm.ToValue(src), vm.ToValue(jsOpts))
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%s: harness returned %T", name, v)
	}
	rep := &ScriptReport{}
	if err := json.Unmarshal([]byte(s), rep); err != nil {
		return nil, err
	}
	return rep, nil
}
