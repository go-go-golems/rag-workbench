package ragjs

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dop251/goja"
	"github.com/go-go-golems/geppetto/pkg/decision"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/providers/pi"
	"github.com/go-go-golems/go-go-goja/pkg/runtimebridge"
)

// newDecide builds env.decide (RAGWB-007 §3.1): decide(requestJSON, signal)
// returns a promise of the response JSON. The provider runs on its own
// goroutine; the promise settles on the runtime's event loop. rag.js
// normalises requests (options as ordered [{name, description}]) and
// responses (camelCase) around it.
func newDecide(vm *goja.Runtime, provider decision.Provider) goja.Value {
	return vm.ToValue(func(call goja.FunctionCall) goja.Value {
		promise, resolve, reject := vm.NewPromise()
		rejectWith := func(code, msg string, extra map[string]any) {
			o := vm.NewObject()
			_ = o.Set("code", code)
			_ = o.Set("message", msg)
			for k, v := range extra {
				_ = o.Set(k, v)
			}
			_ = reject(o)
		}
		var req decision.Request
		if err := json.Unmarshal([]byte(call.Argument(0).String()), &req); err != nil {
			rejectWith("invalid_request", "decide: "+err.Error(), nil)
			return vm.ToValue(promise)
		}
		services, ok := runtimebridge.Lookup(vm)
		if !ok {
			rejectWith("decide_unavailable", "runtime has no event-loop bridge", nil)
			return vm.ToValue(promise)
		}
		callCtx, cancel := context.WithCancel(services.Lifetime())
		if sig, ok := call.Argument(1).(*goja.Object); ok {
			if add, ok := goja.AssertFunction(sig.Get("addEventListener")); ok {
				_, _ = add(sig, vm.ToValue("abort"), vm.ToValue(func() { cancel() }))
			}
			if a := sig.Get("aborted"); a != nil && a.ToBoolean() {
				cancel()
			}
		}
		go func() {
			defer cancel()
			resp, err := provider.Decide(callCtx, req)
			var payload string
			if err == nil {
				b, merr := json.Marshal(resp)
				if merr != nil {
					err = merr
				} else {
					payload = string(b)
				}
			}
			code, msg, extra := "", "", map[string]any(nil)
			if err != nil {
				code, msg, extra = classifyDecideError(err, callCtx, services.Lifetime())
			}
			_ = services.PostWithLifetimeContext("ragwb.decide.settle", func(context.Context, *goja.Runtime) {
				if err != nil {
					rejectWith(code, msg, extra)
					return
				}
				_ = resolve(payload)
			})
		}()
		return vm.ToValue(promise)
	})
}

// classifyDecideError maps geppetto decision errors to the codes rag.js and
// pages see (the sample contract's vocabulary, RAGWB-007 §3.1).
func classifyDecideError(err error, call, lifetime context.Context) (string, string, map[string]any) {
	extra := map[string]any{}
	var pe *decision.ProviderError
	if errors.As(err, &pe) {
		extra["status"] = pe.Status
		if pe.Code != "" {
			extra["providerCode"] = pe.Code
		}
		if pe.RequestID != "" {
			extra["requestId"] = pe.RequestID
		}
		if pe.RetryAfter > 0 {
			extra["retryAfterMs"] = pe.RetryAfter.Milliseconds()
		}
	}
	var nc *pi.NoCredentialError
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (call.Err() != nil && lifetime.Err() == nil):
		return "cancelled", "the decision call was cancelled", nil
	case errors.As(err, &nc), errors.Is(err, decision.ErrNotGranted):
		return "not_granted", err.Error(), extra
	case errors.Is(err, decision.ErrInvalidRequest):
		return "invalid_request", err.Error(), extra
	case errors.Is(err, decision.ErrInvalidResponse):
		return "invalid_response", err.Error(), extra
	case errors.Is(err, decision.ErrRateLimited):
		return "rate_limited", err.Error(), extra
	case errors.Is(err, decision.ErrModelNotFound):
		return "model_not_found", err.Error(), extra
	case errors.Is(err, decision.ErrRequestTooLarge):
		return "prompt_too_large", err.Error(), extra
	default:
		return "upstream_error", err.Error(), extra
	}
}
