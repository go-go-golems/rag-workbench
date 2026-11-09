package rtenv

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type countedCloser struct{ calls atomic.Int32 }

func (c *countedCloser) Close() error { c.calls.Add(1); return nil }

func TestResourcesCloseOnceAndReleaseCompletedLeases(t *testing.T) {
	r := NewResources()
	resource := &countedCloser{}
	handle, err := r.Adopt(resource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Adopt(resource); err == nil {
		t.Fatal("duplicate ownership accepted")
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resource.calls.Load() != 1 {
		t.Fatalf("closed %d times", resource.calls.Load())
	}
	late := &countedCloser{}
	if _, err := r.Adopt(late); err == nil {
		t.Fatal("adopted after shutdown")
	}
	if late.calls.Load() != 0 {
		t.Fatal("failed adoption took ownership")
	}
}

func TestShutdownRetainsResourcesUntilWorkerExit(t *testing.T) {
	r := NewResources()
	resource := &countedCloser{}
	if _, err := r.Adopt(resource); err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	op := &Operation{cancel: cancel}
	if !r.track(op) {
		t.Fatal("tracking rejected")
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if err := r.Close(ctx); err == nil {
		t.Fatal("shutdown reported completion before worker exit")
	}
	if resource.calls.Load() != 0 {
		t.Fatal("resource closed while worker remains")
	}
	r.finished(op)
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resource.calls.Load() != 1 {
		t.Fatal("resource not closed after worker exit")
	}
}
