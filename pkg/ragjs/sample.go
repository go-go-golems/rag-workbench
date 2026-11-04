package ragjs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/runtimebridge"

	"github.com/go-go-golems/rag-workbench/pkg/llm"
)

// The claude.ai `sample` contract (sample(input, opts) / sample.json / sample.limits)
// for rag.configure({sample}). The JS half normalizes input, wraps tools and
// parses JSON exactly as the spec says; the Go half runs the backend on a
// goroutine and settles the promise back on the runtime's event loop.
const sampleJS = `(function (call) {
  const norm = input => {
    if (typeof input === 'string') return [{ role: 'user', content: input }];
    if (Array.isArray(input)) return input.map(t => ({ role: String(t && t.role), content: typeof (t && t.content) === 'string' ? t.content : JSON.stringify(t && t.content) }));
    return null;
  };
  const run = (input, o, json) => {
    o = o || {};
    const turns = norm(input);
    if (!turns) return Promise.reject({ code: 'invalid_request', message: 'input must be a string or an array of {role, content} turns' });
    if (o.signal && o.signal.aborted) return Promise.reject({ code: 'cancelled', message: 'the call was aborted' });
    const tools = Array.isArray(o.tools) ? o.tools : [];
    const byName = new Map(tools.map(t => [String(t.name), t]));
    // tool errors become tool results (the model sees them), never failed calls
    const exec = (name, inputJSON) => {
      const t = byName.get(name);
      if (!t) return Promise.resolve(JSON.stringify({ value: 'unknown tool ' + name, isError: true }));
      return Promise.resolve()
        .then(() => t.execute(JSON.parse(inputJSON || '{}'), { signal: o.signal }))
        .then(v => JSON.stringify({ value: v === undefined ? null : v }),
              e => JSON.stringify({ value: String((e && e.message) || e), isError: true }));
    };
    const onText = typeof o.onText === 'function' ? (text, delta) => { try { o.onText({ text, delta }); } catch (_) { /* page callback errors stay local */ } } : null;
    const req = { turns, tier: o.modelTier || 'default', model: o.model || '', json,
      tools: tools.map(t => ({ name: String(t.name), description: String(t.description || ''), inputSchema: t.inputSchema || null })) };
    return call(JSON.stringify(req), exec, onText, o.signal || null).then(s => {
      const r = JSON.parse(s);
      return json ? r.json : r.result;
    });
  };
  const sample = (input, o) => run(input, o, false);
  sample.json = (input, o) => run(input, o, true);
  sample.limits = () => Promise.resolve({ tools: { maxCount: 16 } });
  return sample;
})`

type sampleRequest struct {
	Turns []llm.Turn `json:"turns"`
	Tier  string     `json:"tier"`
	Model string     `json:"model"`
	JSON  bool       `json:"json"`
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
}

// newSample builds the JS sample function bound to backend.
func newSample(vm *goja.Runtime, backend llm.Backend) (goja.Value, error) {
	callFn := func(call goja.FunctionCall) goja.Value {
		promise, resolve, reject := vm.NewPromise()
		rejectWith := func(f *llm.Failure) {
			o := vm.NewObject()
			_ = o.Set("code", f.Code)
			_ = o.Set("message", f.Message)
			if f.Text != "" {
				_ = o.Set("text", f.Text)
			}
			_ = reject(o)
		}
		var req sampleRequest
		if err := json.Unmarshal([]byte(call.Argument(0).String()), &req); err != nil {
			rejectWith(&llm.Failure{Code: "invalid_request", Message: err.Error()})
			return vm.ToValue(promise)
		}
		execJS, _ := goja.AssertFunction(call.Argument(1))
		onTextJS, hasOnText := goja.AssertFunction(call.Argument(2))
		services, ok := runtimebridge.Lookup(vm)
		if !ok {
			rejectWith(&llm.Failure{Code: "sampling_disabled", Message: "runtime has no event-loop bridge"})
			return vm.ToValue(promise)
		}
		// the call's own context: cancelled when the JS abort signal fires (a request
		// scope's signal, RAGWB-004 §6.7), so an abandoned request stops its model call
		callCtx, cancelCall := context.WithCancel(services.Lifetime())
		if sig, ok := call.Argument(3).(*goja.Object); ok {
			if add, ok := goja.AssertFunction(sig.Get("addEventListener")); ok {
				_, _ = add(sig, vm.ToValue("abort"), vm.ToValue(func() { cancelCall() }))
			}
			if a := sig.Get("aborted"); a != nil && a.ToBoolean() {
				cancelCall()
			}
		}
		lreq := llm.Request{Turns: req.Turns, Tier: req.Tier, Model: req.Model, JSON: req.JSON}
		for _, t := range req.Tools {
			lreq.Tools = append(lreq.Tools, llm.Tool{Name: t.Name, Description: t.Description, InputSchema: nullToEmpty(t.InputSchema)})
		}
		var onText llm.OnText
		if hasOnText {
			onText = func(text, delta string) {
				_ = services.PostWithLifetimeContext("ragwb.sample.onText", func(_ context.Context, vm *goja.Runtime) {
					_, _ = onTextJS(goja.Undefined(), vm.ToValue(text), vm.ToValue(delta))
				})
			}
		}
		exec := func(ctx context.Context, tc llm.ToolCall) (llm.ToolResult, error) {
			if execJS == nil {
				return llm.ToolResult{Value: json.RawMessage(`"tools are not available"`), IsError: true}, nil
			}
			done := make(chan string, 1)
			err := services.PostWithLifetimeContext("ragwb.sample.tool", func(_ context.Context, vm *goja.Runtime) {
				p, err := execJS(goja.Undefined(), vm.ToValue(tc.Name), vm.ToValue(string(tc.Input)))
				if err != nil {
					done <- fmt.Sprintf(`{"value": %q, "isError": true}`, err.Error())
					return
				}
				then, _ := goja.AssertFunction(p.ToObject(vm).Get("then"))
				_, _ = then(p, vm.ToValue(func(v goja.Value) { done <- v.String() }),
					vm.ToValue(func(v goja.Value) { done <- fmt.Sprintf(`{"value": %q, "isError": true}`, v.String()) }))
			})
			if err != nil {
				return llm.ToolResult{}, err
			}
			select {
			case s := <-done:
				var r struct {
					Value   json.RawMessage `json:"value"`
					IsError bool            `json:"isError"`
				}
				if err := json.Unmarshal([]byte(s), &r); err != nil {
					return llm.ToolResult{}, err
				}
				return llm.ToolResult{Value: r.Value, IsError: r.IsError}, nil
			case <-ctx.Done():
				return llm.ToolResult{}, ctx.Err()
			}
		}
		go func() {
			defer cancelCall()
			res, err := backend.Sample(callCtx, lreq, onText, exec)
			var payload string
			var failure *llm.Failure
			if err == nil {
				out := map[string]any{"result": res}
				if lreq.JSON {
					raw, ok := llm.ParseJSON(res.Text)
					if !ok || res.Truncated {
						failure = &llm.Failure{Code: "invalid_json", Message: llm.InvalidJSONMessage(res), Text: res.Text}
					} else {
						out["json"] = raw
					}
				}
				if failure == nil {
					b, _ := json.Marshal(out)
					payload = string(b)
				}
			} else if f, ok := err.(*llm.Failure); ok {
				failure = f
			} else if callCtx.Err() != nil && services.Lifetime().Err() == nil {
				failure = &llm.Failure{Code: "cancelled", Message: "the request was cancelled"}
			} else {
				failure = &llm.Failure{Code: "upstream_error", Message: err.Error()}
			}
			_ = services.PostWithLifetimeContext("ragwb.sample.settle", func(_ context.Context, vm *goja.Runtime) {
				if failure != nil {
					rejectWith(failure)
					return
				}
				_ = resolve(payload)
			})
		}()
		return vm.ToValue(promise)
	}
	v, err := vm.RunString(sampleJS)
	if err != nil {
		return nil, err
	}
	factory, ok := goja.AssertFunction(v)
	if !ok {
		return nil, fmt.Errorf("sample factory is not a function")
	}
	return factory(goja.Undefined(), vm.ToValue(callFn))
}

func nullToEmpty(r json.RawMessage) json.RawMessage {
	if string(r) == "null" {
		return nil
	}
	return r
}
