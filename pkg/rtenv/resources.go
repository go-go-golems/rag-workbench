package rtenv

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/pkg/errors"
)

// Resources owns native handles and executing tasks for one runtime. Cancelling
// a promise does not remove its worker from this registry.
type Resources struct {
	mu         sync.Mutex
	handles    map[io.Closer]*Handle
	cleanup    []func() error
	operations map[*Operation]struct{}
	closing    bool
	done       chan struct{}
	once       sync.Once
	err        error
}
type Handle struct {
	once     sync.Once
	resource io.Closer
	registry *Resources
	err      error
}

var _ io.Closer = (*Handle)(nil)

func NewResources() *Resources {
	return &Resources{handles: map[io.Closer]*Handle{}, operations: map[*Operation]struct{}{}, done: make(chan struct{})}
}

// Adopt transfers ownership only on success. Every error leaves it with the opener.
func (r *Resources) Adopt(resource io.Closer) (*Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return nil, errors.New("runtime resources are closing")
	}
	if _, exists := r.handles[resource]; exists {
		return nil, errors.New("resource already adopted")
	}
	h := &Handle{resource: resource, registry: r}
	r.handles[resource] = h
	return h, nil
}
func (h *Handle) Close() error {
	h.once.Do(func() {
		h.err = h.resource.Close()
		h.registry.mu.Lock()
		delete(h.registry.handles, h.resource)
		h.registry.mu.Unlock()
	})
	return h.err
}
func (r *Resources) Defer(close func() error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanup = append(r.cleanup, close)
}
func (r *Resources) track(op *Operation) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return false
	}
	r.operations[op] = struct{}{}
	return true
}
func (r *Resources) finished(op *Operation) {
	r.mu.Lock()
	delete(r.operations, op)
	finish := r.closing && len(r.operations) == 0
	r.mu.Unlock()
	if finish {
		r.release()
	}
}
func (r *Resources) release() {
	r.once.Do(func() {
		r.mu.Lock()
		handles := make([]*Handle, 0, len(r.handles))
		for _, h := range r.handles {
			handles = append(handles, h)
		}
		cleanup := r.cleanup
		r.cleanup = nil
		r.mu.Unlock()
		for _, h := range handles {
			if err := h.Close(); err != nil && r.err == nil {
				r.err = err
			}
		}
		for i := len(cleanup) - 1; i >= 0; i-- {
			if err := cleanup[i](); err != nil && r.err == nil {
				r.err = err
			}
		}
		close(r.done)
	})
}
func (r *Resources) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r.mu.Lock()
	r.closing = true
	ops := make([]*Operation, 0, len(r.operations))
	for op := range r.operations {
		ops = append(ops, op)
	}
	r.mu.Unlock()
	for _, op := range ops {
		op.cancel()
	}
	if len(ops) == 0 {
		r.release()
	}
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "runtime workers have not exited; resources remain retained")
	}
}

var runtimes sync.Map

func Bind(vm *goja.Runtime, resources *Resources) {
	runtimes.Store(vm, resources)
	resources.Defer(func() error { runtimes.Delete(vm); return nil })
}
func resourcesFor(vm *goja.Runtime) *Resources {
	value, ok := runtimes.Load(vm)
	if !ok {
		return nil
	}
	return value.(*Resources)
}

type releaseFunc struct{ release func() }

func (r *releaseFunc) Close() error { r.release(); return nil }
func (r *Resources) Lease(release func()) (func(), error) {
	handle, err := r.Adopt(&releaseFunc{release: release})
	if err != nil {
		release()
		return nil, err
	}
	return func() { _ = handle.Close() }, nil
}
