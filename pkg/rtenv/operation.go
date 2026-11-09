package rtenv

import (
	"context"
	"sync"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/runtimebridge"
	"golang.org/x/sync/errgroup"
)

// Operation distinguishes accepting a terminal outcome, delivering it to the
// VM owner, and actual worker exit. Cleanup can never reclassify an outcome.
type Operation struct {
	abort     func(string)
	resources *Resources
	ctx       context.Context
	cancel    context.CancelFunc
	services  runtimebridge.RuntimeServices
	name      string
	once      sync.Once
	detach    func()
	stop      func() bool
	tasks     errgroup.Group
	done      chan struct{}
}

// NewOperation must run on the VM owner, as must the supplied settlement callback.
// abort receives the accepted cancellation kind; provider errors are classified
// by the worker before calling Complete.
func NewOperation(vm *goja.Runtime, services runtimebridge.RuntimeServices, name string, signal goja.Value, abort func(string)) *Operation {
	ctx, cancel := context.WithCancel(services.Lifetime())
	op := &Operation{abort: abort, ctx: ctx, cancel: cancel, services: services, name: name, done: make(chan struct{})}
	var sig *goja.Object
	var listener goja.Value
	op.detach = func() {
		if sig != nil {
			if remove, ok := goja.AssertFunction(sig.Get("removeEventListener")); ok {
				_, _ = remove(sig, vm.ToValue("abort"), listener)
			}
		}
	}
	acceptCancel := func(kind string) { op.Complete(func(*goja.Runtime) { abort(kind) }); cancel() }
	if object, ok := signal.(*goja.Object); ok {
		sig = object
		listener = vm.ToValue(func() { acceptCancel("cancelled") })
		if add, ok := goja.AssertFunction(sig.Get("addEventListener")); ok {
			_, _ = add(sig, vm.ToValue("abort"), listener)
		}
		if a := sig.Get("aborted"); a != nil && a.ToBoolean() {
			acceptCancel("cancelled")
		}
	}
	op.stop = context.AfterFunc(services.Lifetime(), func() { acceptCancel("shutdown") })
	op.resources = resourcesFor(vm)
	return op
}
func (o *Operation) Context() context.Context { return o.ctx }
func (o *Operation) Complete(deliver func(*goja.Runtime)) {
	o.once.Do(func() {
		_ = o.services.PostWithLifetimeContext(o.name+".settle", func(_ context.Context, vm *goja.Runtime) { o.detach(); deliver(vm) })
	})
}

// Start takes immutable Go input captured by work; all VM access stays in posted callbacks.
func (o *Operation) Start(work func(context.Context)) {
	if o.resources != nil && !o.resources.track(o) {
		o.Complete(func(*goja.Runtime) { o.abort("shutdown") })
		o.stop()
		o.cancel()
		close(o.done)
		return
	}
	o.tasks.Go(func() error {
		defer func() {
			if o.resources != nil {
				o.resources.finished(o)
			}
		}()
		defer close(o.done)
		defer o.cancel()
		defer o.stop()
		work(o.ctx)
		return nil
	})
}
func (o *Operation) Wait(ctx context.Context) error {
	select {
	case <-o.done:
		return o.tasks.Wait()
	case <-ctx.Done():
		return ctx.Err()
	}
}
