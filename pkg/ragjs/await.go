package ragjs

import (
	"context"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/engine"
)

// Eval runs fn on the runtime's owner goroutine and, if it returns a Promise,
// waits for it to settle. The returned value is exported to Go (Export()) on
// the owner goroutine, so callers never touch the VM themselves.
func Eval(ctx context.Context, rt *engine.Runtime, op string, fn func(vm *goja.Runtime) (goja.Value, error)) (any, error) {
	v, err := rt.Owner.Call(ctx, op, func(_ context.Context, vm *goja.Runtime) (any, error) {
		val, err := fn(vm)
		if err != nil {
			return nil, err
		}
		if p, ok := val.Export().(*goja.Promise); ok {
			return p, nil
		}
		return exportValue(vm, val), nil
	})
	if err != nil {
		return nil, err
	}
	p, ok := v.(*goja.Promise)
	if !ok {
		return v, nil
	}
	return awaitPromise(ctx, rt, op, p)
}

func awaitPromise(ctx context.Context, rt *engine.Runtime, op string, p *goja.Promise) (any, error) {
	delay := 100 * time.Microsecond
	for {
		type settled struct {
			done  bool
			value any
			err   error
		}
		s, err := rt.Owner.Call(ctx, op+".await", func(_ context.Context, vm *goja.Runtime) (any, error) {
			switch p.State() {
			case goja.PromiseStateFulfilled:
				return settled{done: true, value: exportValue(vm, p.Result())}, nil
			case goja.PromiseStateRejected:
				return settled{done: true, err: rejection(vm, p.Result())}, nil
			default:
				return settled{}, nil
			}
		})
		if err != nil {
			return nil, err
		}
		if st := s.(settled); st.done {
			return st.value, st.err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 20*time.Millisecond {
			delay *= 2
		}
	}
}

// exportValue converts a JS value to plain Go data (null/undefined → nil).
// Callers that need JSON semantics (toJSON, dropping Maps) should JSON.stringify
// on the JS side and return a string.
func exportValue(_ *goja.Runtime, v goja.Value) any {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	return v.Export()
}

// RejectionError carries a rejected promise's message and JS stack.
type RejectionError struct {
	Message string
	Stack   string
}

func (e *RejectionError) Error() string {
	if e.Stack != "" {
		return e.Stack
	}
	return e.Message
}

func rejection(vm *goja.Runtime, v goja.Value) error {
	if o, ok := v.(*goja.Object); ok {
		msg := o.Get("message")
		stack := o.Get("stack")
		e := &RejectionError{}
		if msg != nil && !goja.IsUndefined(msg) {
			e.Message = msg.String()
		} else {
			e.Message = o.String()
		}
		if stack != nil && !goja.IsUndefined(stack) {
			e.Stack = stack.String()
		}
		return e
	}
	return fmt.Errorf("promise rejected: %v", v)
}
