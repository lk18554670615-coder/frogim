package platform

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// A database transaction cannot atomically commit an external notification.
// This guard bounds lock-loss detection and cancels in-flight HTTP work, checks
// again at EVERY actual provider request, and never treats an unknown outcome
// as successful. Already accepted external notifications cannot be recalled.
func (p *PushService) sendFenced(ctx context.Context, tx pgx.Tx, until time.Time, delivery PushDelivery) error {
	// The receiver must not accept a notification later than the owning session.
	// This is a delivery-local copy; the durable input/idempotency hash is unchanged.
	delivery.Request.ExpiresAt = until
	call, cancel := context.WithDeadline(ctx, until)
	defer cancel()
	var mu sync.Mutex
	active := true
	check := func(request context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if !active || request.Err() != nil || call.Err() != nil {
			return ErrUnavailable
		}
		// pgx transactions are not goroutine-safe. All probes use this mutex;
		// the parent performs no SQL until this helper has stopped its monitor.
		probe, stop := context.WithTimeout(call, time.Second)
		detach := context.AfterFunc(request, stop)
		defer stop()
		defer detach()
		var fresh bool
		err := tx.QueryRow(probe, `/* platform push submission fence */ SELECT clock_timestamp()<$1::timestamptz`, until).Scan(&fresh)
		if err != nil || !fresh || tx.Conn().PgConn().TxStatus() != 'T' {
			cancel()
			return ErrUnavailable
		}
		return nil
	}
	// A retained callback is not a reusable authority after Send returns. This
	// defer also executes on initial probe failure or a sender panic.
	defer func() { mu.Lock(); active = false; mu.Unlock() }()
	if err := check(call); err != nil {
		return err
	}
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	stopMonitor := func() { once.Do(func() { close(stop); <-done }) }
	go func() {
		defer close(done)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-call.Done():
				return
			case <-tick.C:
				if check(call) != nil {
					return
				}
			}
		}
	}()
	defer stopMonitor()
	delivery.BeforeSend = check
	err := p.sender.Send(call, delivery)
	stopMonitor()
	if call.Err() != nil || check(call) != nil {
		return ErrUnavailable
	}
	return err
}
