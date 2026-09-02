package accesslog

import (
	"context"
	"errors"
	"github.com/linli/im/server/internal/store"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWriter struct {
	mu    sync.Mutex
	calls []store.UserAccessLog
	fail  int
}

type blockingWriter struct {
	active, peak atomic.Int64
	release      chan struct{}
}

func (w *blockingWriter) RecordUserAccess(ctx context.Context, _ store.UserAccessLog) error {
	active := w.active.Add(1)
	defer w.active.Add(-1)
	for old := w.peak.Load(); active > old; old = w.peak.Load() {
		if w.peak.CompareAndSwap(old, active) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.release:
		return nil
	}
}
func TestRecorderTwoWorkers(t *testing.T) {
	w := &blockingWriter{release: make(chan struct{})}
	r := New(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range 20 {
		r.Enqueue(store.UserAccessLog{ID: "worker-test"})
	}
	go r.Run(ctx)
	await(t, func() bool { return w.active.Load() == 2 })
	close(w.release)
	await(t, func() bool { return r.Pending() == 0 })
	if w.peak.Load() != 2 || r.Written.Load() != 20 {
		t.Fatal("worker bound", w.peak.Load(), r.Written.Load())
	}
}

func (f *fakeWriter) RecordUserAccess(ctx context.Context, e store.UserAccessLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, e)
	if len(f.calls) <= f.fail {
		return errors.New("unavailable")
	}
	return nil
}
func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestRecorderRetryIdempotency(t *testing.T) {
	w := &fakeWriter{fail: 2}
	r := New(w)
	r.RetryDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	r.Enqueue(store.UserAccessLog{ID: "stable"})
	await(t, func() bool { return r.Pending() == 0 })
	if r.Written.Load() != 1 || r.Retried.Load() != 2 || len(w.calls) != 3 {
		t.Fatalf("counts written=%d retry=%d calls=%d", r.Written.Load(), r.Retried.Load(), len(w.calls))
	}
	for _, e := range w.calls {
		if e.ID != "stable" {
			t.Fatal("ID changed")
		}
	}
}
func TestRecorderQueueBoundAndExhaustion(t *testing.T) {
	r := New(&fakeWriter{})
	for range 1000 {
		if !r.Enqueue(store.UserAccessLog{ID: "bounded"}) {
			t.Fatal("premature rejection")
		}
	}
	if r.Enqueue(store.UserAccessLog{}) || r.Dropped.Load() != 1 {
		t.Fatal("unbounded queue")
	}
	w := &fakeWriter{fail: 100}
	r = New(w)
	r.RetryDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	r.Enqueue(store.UserAccessLog{ID: "fail"})
	await(t, func() bool { return r.Pending() == 0 })
	if r.Dropped.Load() != 1 || len(w.calls) != 3 {
		t.Fatal("retry did not terminate")
	}
}
