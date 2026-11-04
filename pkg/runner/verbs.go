package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/glazed/pkg/cmds/fields"
	"github.com/go-go-golems/glazed/pkg/cmds/sources"
	"github.com/go-go-golems/glazed/pkg/cmds/values"
	"github.com/go-go-golems/go-go-goja/pkg/jsverbs"
)

// Verb endpoints (RAGWB-004): the jsverbs verbs of a served app run in the app's
// long-lived runtime, inside a request scope that routes the engine's events
// (rag.js newScope/withScope) to an EventSink.

// Event is one engine or host event: Type plus its JSON object (which includes "type").
type Event struct {
	Type string
	Data json.RawMessage
}

// EventSink receives a request's events. Emit is called on the runtime's owner
// goroutine: it must not block on the network.
type EventSink interface {
	Emit(Event)
}

// SinkFunc adapts a function to EventSink.
type SinkFunc func(Event)

func (f SinkFunc) Emit(e Event) { f(e) }

// Detail is what events carry: Top hits per hits event, full chunks with Chunks.
type Detail struct {
	Top    int  `json:"top"`
	Chunks bool `json:"chunks"`
}

// ParamError is a request whose parameters don't fit the verb's fields.
type ParamError struct {
	Fields map[string]string
}

func (e *ParamError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e.Fields[k]
	}
	return "bad parameters: " + strings.Join(parts, "; ")
}

// kebab is jsverbs' field-name normalization (cleanCommandWord): JSON keys may
// use the names written in __verb__ (maxTokens) or the CLI's (max-tokens).
func kebab(s string) string {
	s = strings.TrimSpace(s)
	var out []rune
	prevLower := false
	for i, r := range s {
		switch {
		case r == '/' || r == '_' || r == ' ':
			if len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
			prevLower = false
		case r >= 'A' && r <= 'Z':
			if i > 0 && prevLower && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
			out = append(out, r+'a'-'A')
			prevLower = false
		default:
			out = append(out, r)
			prevLower = (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		}
	}
	return strings.Trim(strings.ReplaceAll(string(out), "--", "-"), "-")
}

// VerbField describes one parameter of a verb endpoint (GET /verbs).
type VerbField struct {
	Name     string   `json:"name"`
	Section  string   `json:"section"`
	Type     string   `json:"type"`
	Default  any      `json:"default,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Required bool     `json:"required,omitempty"`
	Argument bool     `json:"argument,omitempty"`
	Help     string   `json:"help,omitempty"`
}

type verbField struct {
	section string
	def     *fields.Definition
}

func verbFields(reg *jsverbs.Registry, verb *jsverbs.VerbSpec) ([]verbField, error) {
	desc, err := reg.CommandDescriptionForVerb(verb)
	if err != nil {
		return nil, err
	}
	var out []verbField
	for pair := desc.Schema.Oldest(); pair != nil; pair = pair.Next() {
		slug := pair.Key
		pair.Value.GetDefinitions().ForEach(func(d *fields.Definition) {
			out = append(out, verbField{slug, d})
		})
	}
	return out, nil
}

// DescribeVerb lists a verb's parameters.
func DescribeVerb(reg *jsverbs.Registry, verb *jsverbs.VerbSpec) ([]VerbField, error) {
	fs, err := verbFields(reg, verb)
	if err != nil {
		return nil, err
	}
	out := make([]VerbField, 0, len(fs))
	for _, f := range fs {
		vf := VerbField{Name: f.def.Name, Section: f.section, Type: string(f.def.Type), Choices: f.def.Choices, Required: f.def.Required, Argument: f.def.IsArgument, Help: f.def.Help}
		if f.def.Default != nil {
			vf.Default = *f.def.Default
		}
		out = append(out, vf)
	}
	return out, nil
}

// ParseVerbParams turns a JSON object (or query-string values) into the verb's
// glazed values, as the CLI would parse flags: defaults first, then the given
// values, checked against the field types and choices. Keys are field names
// (either spelling, see kebab); a section's fields may also be nested under its
// slug ({"subset": {"include": [...]}}).
func ParseVerbParams(reg *jsverbs.Registry, verb *jsverbs.VerbSpec, params map[string]any) (*values.Values, error) {
	desc, err := reg.CommandDescriptionForVerb(verb)
	if err != nil {
		return nil, err
	}
	fs, err := verbFields(reg, verb)
	if err != nil {
		return nil, err
	}
	byName := map[string]verbField{}
	sections := map[string]bool{}
	for _, f := range fs {
		byName[f.def.Name] = f
		sections[f.section] = true
	}
	m := map[string]map[string]any{}
	bad := map[string]string{}
	var put func(key string, v any, nested bool)
	put = func(key string, v any, nested bool) {
		name := kebab(key)
		f, ok := byName[name]
		if !ok {
			if obj, isObj := v.(map[string]any); isObj && sections[name] && !nested {
				for k, x := range obj {
					put(k, x, true)
				}
				return
			}
			bad[key] = "unknown parameter"
			return
		}
		cv, err := coerce(f.def, v)
		if err != nil {
			bad[key] = err.Error()
			return
		}
		if m[f.section] == nil {
			m[f.section] = map[string]any{}
		}
		m[f.section][f.def.Name] = cv
	}
	for k, v := range params {
		put(k, v, false)
	}
	if len(bad) > 0 {
		return nil, &ParamError{Fields: bad}
	}
	vals := values.New()
	if err := sources.Execute(desc.Schema, vals, sources.FromMap(m), sources.FromDefaults()); err != nil {
		return nil, &ParamError{Fields: map[string]string{"*": err.Error()}}
	}
	for _, f := range fs {
		if !f.def.Required {
			continue
		}
		if _, ok := m[f.section][f.def.Name]; !ok && f.def.Default == nil {
			bad[f.def.Name] = "required"
		}
	}
	if len(bad) > 0 {
		return nil, &ParamError{Fields: bad}
	}
	return vals, nil
}

// coerce maps JSON values onto a field's Go type (JSON numbers are float64,
// arrays []any); strings are left to glazed's own parser (query strings).
func coerce(d *fields.Definition, v any) (any, error) {
	switch x := v.(type) {
	case float64:
		switch d.Type {
		case fields.TypeInteger:
			if x != math.Trunc(x) {
				return nil, fmt.Errorf("want an integer, got %v", x)
			}
			return int(x), nil
		case fields.TypeFloat:
			return x, nil
		case fields.TypeString, fields.TypeChoice:
			return fmt.Sprint(x), nil
		}
	case []any:
		switch d.Type {
		case fields.TypeStringList, fields.TypeChoiceList:
			out := make([]string, len(x))
			for i, e := range x {
				out[i] = fmt.Sprint(e)
			}
			return out, nil
		case fields.TypeIntegerList:
			out := make([]int, len(x))
			for i, e := range x {
				f, ok := e.(float64)
				if !ok || f != math.Trunc(f) {
					return nil, fmt.Errorf("want integers, got %v", e)
				}
				out[i] = int(f)
			}
			return out, nil
		case fields.TypeFloatList:
			out := make([]float64, len(x))
			for i, e := range x {
				f, ok := e.(float64)
				if !ok {
					return nil, fmt.Errorf("want numbers, got %v", e)
				}
				out[i] = f
			}
			return out, nil
		}
	case []string:
		switch d.Type {
		case fields.TypeStringList, fields.TypeChoiceList, fields.TypeIntegerList, fields.TypeFloatList:
			return x, nil
		}
		if len(x) == 1 {
			return x[0], nil
		}
		return nil, fmt.Errorf("one value expected, got %d", len(x))
	}
	return v, nil
}

// Verbs lists the app's verbs (none if it was opened without a registry).
func (a *ServedApp) Verbs() []*jsverbs.VerbSpec {
	if a.verbs == nil {
		return nil
	}
	return a.verbs.Verbs()
}

// Registry is the app's jsverbs registry (nil without verbs).
func (a *ServedApp) Registry() *jsverbs.Registry { return a.verbs }

// Verb finds a verb by its full path ("parc search").
func (a *ServedApp) Verb(path string) (*jsverbs.VerbSpec, bool) {
	if a.verbs == nil {
		return nil, false
	}
	return a.verbs.Verb(path)
}

// Invoke runs a verb in the app's runtime inside a request scope: the engine
// events of every rag.run/rag.build it starts go to sink, and cancelling ctx
// aborts the scope's signal (LLM calls and remaining nodes stop). It returns the
// verb's result (exported from JS) and the number of rag runs it made.
//
// The scope reaches the verb through a wrapper around the function jsverbs
// captured (__ragwbWrapVerb): the host announces the scope (__ragwbPending)
// under a.verbMu and holds the lock until the wrapper has taken it, so
// concurrent requests can't take each other's scope, while the verbs themselves
// run (and interleave at awaits) without the lock.
func (a *ServedApp) Invoke(ctx context.Context, verb *jsverbs.VerbSpec, vals *values.Values, sink EventSink, detail Detail) (any, int, error) {
	if a.verbs == nil {
		return nil, 0, fmt.Errorf("app %s has no verbs", a.Name)
	}
	if sink == nil {
		sink = SinkFunc(func(Event) {})
	}
	entered := make(chan struct{})
	var once sync.Once
	var scope *goja.Object
	a.verbMu.Lock()
	locked := true
	unlock := func() {
		if locked {
			locked = false
			a.verbMu.Unlock()
		}
	}
	defer unlock()
	_, err := a.rt.Owner.Call(ctx, "ragwb.verb.prepare", func(_ context.Context, vm *goja.Runtime) (any, error) {
		if _, err := a.rt.Require.Require(verb.File.ModulePath); err != nil {
			return nil, err
		}
		reg := vm.Get("__glazedVerbRegistry")
		if reg == nil || goja.IsUndefined(reg) {
			return nil, fmt.Errorf("js verb registry not initialized")
		}
		entry := reg.ToObject(vm).Get(verb.File.ModulePath)
		wrap, ok := goja.AssertFunction(vm.Get("__ragwbWrapVerb"))
		if !ok {
			return nil, fmt.Errorf("serve harness has no verb wrapper")
		}
		w, err := wrap(goja.Undefined(), entry, vm.ToValue(verb.FunctionName))
		if err != nil {
			return nil, err
		}
		if !w.ToBoolean() {
			return nil, fmt.Errorf("js function %s not captured for %s", verb.FunctionName, verb.File.RelPath)
		}
		rag, err := a.rt.Require.Require("ragwb")
		if err != nil {
			return nil, err
		}
		newScope, ok := goja.AssertFunction(rag.ToObject(vm).Get("newScope"))
		if !ok {
			return nil, fmt.Errorf("ragwb has no newScope")
		}
		stringify, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
		emit := func(call goja.FunctionCall) goja.Value {
			ev := call.Argument(0)
			s, err := stringify(goja.Undefined(), ev)
			if err != nil || goja.IsUndefined(s) {
				return goja.Undefined()
			}
			typ := ""
			if o, ok := ev.(*goja.Object); ok {
				if t := o.Get("type"); t != nil {
					typ = t.String()
				}
			}
			sink.Emit(Event{Type: typ, Data: json.RawMessage(s.String())})
			return goja.Undefined()
		}
		d := map[string]any{"chunks": detail.Chunks}
		if detail.Top > 0 {
			d["top"] = detail.Top
		}
		sv, err := newScope(goja.Undefined(), vm.ToValue(emit), vm.ToValue(d))
		if err != nil {
			return nil, err
		}
		scope = sv.ToObject(vm)
		pending := vm.NewObject()
		_ = pending.Set("scope", scope)
		_ = pending.Set("entered", func() { once.Do(func() { close(entered) }) })
		return nil, vm.Set("__ragwbPending", pending)
	})
	if err != nil {
		return nil, 0, err
	}
	res := make(chan verbDone, 1)
	go func() {
		v, err := a.verbs.InvokeInRuntime(ctx, a.rt, verb, vals)
		res <- verbDone{v, err}
	}()
	var out verbDone
	select {
	case <-entered:
		unlock()
		out = a.waitVerb(ctx, res, scope)
	case out = <-res: // failed before calling the function (argument binding): withdraw the scope
		_, _ = a.rt.Owner.Call(context.Background(), "ragwb.verb.withdraw", func(_ context.Context, vm *goja.Runtime) (any, error) {
			return nil, vm.Set("__ragwbPending", goja.Null())
		})
		unlock()
	}
	runs := 0
	_, _ = a.rt.Owner.Call(context.Background(), "ragwb.verb.close", func(_ context.Context, vm *goja.Runtime) (any, error) {
		if scope != nil {
			if f, ok := goja.AssertFunction(scope.Get("runs")); ok {
				if v, err := f(scope); err == nil {
					runs = int(v.ToInteger())
				}
			}
			if f, ok := goja.AssertFunction(scope.Get("close")); ok {
				_, _ = f(scope)
			}
		}
		return nil, nil
	})
	return out.v, runs, out.err
}

// waitVerb waits for the verb; when ctx ends first, the scope is aborted so the
// verb's LLM calls and remaining nodes stop, and the cancellation is returned.
type verbDone struct {
	v   any
	err error
}

func (a *ServedApp) waitVerb(ctx context.Context, res <-chan verbDone, scope *goja.Object) verbDone {
	select {
	case r := <-res:
		return r
	case <-ctx.Done():
		actx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = a.rt.Owner.Call(actx, "ragwb.verb.abort", func(_ context.Context, vm *goja.Runtime) (any, error) {
			if f, ok := goja.AssertFunction(scope.Get("abort")); ok {
				_, _ = f(scope, vm.ToValue("request cancelled"))
			}
			return nil, nil
		})
		cancel()
		return verbDone{nil, ctx.Err()}
	}
}
