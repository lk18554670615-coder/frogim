// Package accesslog records authentication telemetry without coupling it to
// authentication availability. Pending work is intentionally not durable.
package accesslog

import (
	"context"
	"github.com/linli/im/server/internal/store"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type Writer interface {
	RecordUserAccess(context.Context, store.UserAccessLog) error
}
type Recorder struct {
	queue                     chan store.UserAccessLog
	writer                    Writer
	once                      sync.Once
	pending                   atomic.Int64
	Written, Retried, Dropped atomic.Int64
	RetryDelay                time.Duration
}

func New(w Writer) *Recorder {
	return &Recorder{queue: make(chan store.UserAccessLog, 1000), writer: w, RetryDelay: time.Second}
}
func (r *Recorder) Enqueue(e store.UserAccessLog) bool {
	r.pending.Add(1)
	select {
	case r.queue <- e:
		return true
	default:
		r.pending.Add(-1)
		r.Dropped.Add(1)
		slog.Warn("user access queue full", "event_id", e.ID)
		return false
	}
}
func (r *Recorder) Pending() int64 { return r.pending.Load() }
func (r *Recorder) Run(ctx context.Context) {
	r.once.Do(func() {
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case e := <-r.queue:
						r.write(ctx, e)
						r.pending.Add(-1)
					}
				}
			}()
		}
		wg.Wait()
	})
}
func (r *Recorder) write(ctx context.Context, e store.UserAccessLog) {
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			break
		}
		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := r.writer.RecordUserAccess(writeCtx, e)
		cancel()
		if err == nil {
			r.Written.Add(1)
			return
		}
		if attempt == 2 {
			break
		}
		r.Retried.Add(1)
		timer := time.NewTimer(time.Duration(attempt+1) * r.RetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	r.Dropped.Add(1)
	slog.Warn("user access record dropped after retries", "event_id", e.ID)
}
