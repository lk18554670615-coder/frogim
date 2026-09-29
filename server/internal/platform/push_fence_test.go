package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

type pushSenderFunc func(context.Context, PushDelivery) error

func (f pushSenderFunc) Send(ctx context.Context, d PushDelivery) error { return f(ctx, d) }

// Only the test's isolated schema on the explicitly configured disposable PG
// instance is considered. Never select by application name across databases.
func killPushTransaction(t *testing.T, s *Store) {
	t.Helper()
	var pid int32
	if e := s.pool.QueryRow(t.Context(), `SELECT DISTINCT pid FROM pg_locks WHERE relation='platform_push_devices'::regclass AND mode='RowShareLock' AND granted AND pid<>pg_backend_pid()`).Scan(&pid); e != nil {
		t.Fatal("isolated delivery backend not uniquely identified", e)
	}
	var killed bool
	if e := s.pool.QueryRow(t.Context(), `SELECT pg_terminate_backend($1)`, pid).Scan(&killed); e != nil || !killed {
		t.Fatal("could not terminate fixture transaction", e)
	}
}
func pushFenceWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("push fence did not reach expected state")
	}
}
func pushFenceResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("push fence did not settle")
		return nil
	}
}

func TestPlatformPostgresPushLockConnectionLoss(t *testing.T) {
	for _, event := range []string{"message.created", "call.invited"} {
		t.Run(event, func(t *testing.T) {
			s := isolatedPlatform(t)
			entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			defer close(release)
			var submissions atomic.Int32
			var lateCheck func(context.Context) error
			sender := pushSenderFunc(func(ctx context.Context, d PushDelivery) error {
				if e := d.BeforeSend(ctx); e != nil {
					return e
				}
				if submissions.Add(1) > 1 {
					return nil
				}
				lateCheck = d.BeforeSend
				close(entered)
				select {
				case <-ctx.Done():
					close(cancelled)
				case <-release:
					return ErrUnavailable
				}
				// Deliberately ignore cancellation in the test sender. The service
				// must still reject a false success and must invalidate this check.
				return nil
			})
			p := pushTestService(t, s, sender)
			a, login := pushTestAccount(t, s, "19900008111")
			b, other := pushTestAccount(t, s, "19900008112")
			d := pushTestDevice()
			bound, e := p.Bind(t.Context(), login.RefreshToken, d)
			if e != nil {
				t.Fatal(e)
			}
			in := pushTestRequest(a, "connection-loss")
			if event == "call.invited" {
				in.EventType = event
				in.MessageID = ""
				in.MessageType = ""
				in.CallID = "call-one"
				in.MediaType = "audio"
				in.ExpiresAt = time.Now().Add(40 * time.Second)
			}
			done := make(chan error, 1)
			go func() { _, e := p.Deliver(t.Context(), "a", in); done <- e }()
			pushFenceWait(t, entered)
			killPushTransaction(t, s)
			pushFenceWait(t, cancelled)
			if e := pushFenceResult(t, done); !errors.Is(e, ErrUnavailable) {
				t.Fatal("lost lock claimed success", e)
			}
			if e := lateCheck(t.Context()); !errors.Is(e, ErrUnavailable) {
				t.Fatal("retained callback usable", e)
			}
			if e := s.Logout(t.Context(), login.RefreshToken); e != nil {
				t.Fatal(e)
			}
			rebound, e := p.Bind(t.Context(), other.RefreshToken, d)
			if e != nil || rebound.ID != bound.ID || rebound.Revision <= bound.Revision {
				t.Fatal("rebinding", e)
			}
			if r, e := p.Deliver(t.Context(), "a", in); e != nil || r.Sent != 0 || r.Skipped != 1 || submissions.Load() != 1 {
				t.Fatal("old owner resumed submission", r, e)
			}
			if r, e := p.Deliver(t.Context(), "a", pushTestRequest(b, "new-binding")); e != nil || r.Sent != 1 || submissions.Load() != 2 {
				t.Fatal("new owner could not submit", r, e)
			}
		})
	}
}

func TestPlatformPostgresPushScopeExpiresDuringSubmission(t *testing.T) {
	for _, field := range []string{"notification", "session"} {
		t.Run(field, func(t *testing.T) {
			s := isolatedPlatform(t)
			var calls atomic.Int32
			p := pushTestService(t, s, pushSenderFunc(func(ctx context.Context, d PushDelivery) error {
				calls.Add(1)
				if d.Request.ExpiresAt.After(time.Now().Add(time.Second)) {
					t.Error("payload outlives the owning session/notification")
					return ErrUnavailable
				}
				if e := d.BeforeSend(ctx); e != nil {
					return e
				}
				<-ctx.Done()
				return ErrInvalidPushDevice // must not invalidate the still-valid device
			}))
			a, login := pushTestAccount(t, s, "19900008113")
			bound, e := p.Bind(t.Context(), login.RefreshToken, pushTestDevice())
			if e != nil {
				t.Fatal(e)
			}
			in := pushTestRequest(a, "short-lived")
			if field == "notification" {
				in.ExpiresAt = time.Now().Add(750 * time.Millisecond)
			} else {
				if _, e := s.pool.Exec(t.Context(), `UPDATE platform_sessions SET expires_at=clock_timestamp()+interval '750 milliseconds' WHERE token_hash=$1`, tenancy.Hash(login.RefreshToken)); e != nil {
					t.Fatal(e)
				}
			}
			done := make(chan error, 1)
			go func() { _, e := p.Deliver(t.Context(), "a", in); done <- e }()
			if e := pushFenceResult(t, done); !errors.Is(e, ErrUnavailable) || calls.Load() != 1 {
				t.Fatal("expired submission accepted", e)
			}
			var active bool
			if e := s.pool.QueryRow(t.Context(), `SELECT revoked_at IS NULL AND octet_length(token_cipher)>0 FROM platform_push_devices WHERE id=$1`, bound.ID).Scan(&active); e != nil || !active {
				t.Fatal("expiry incorrectly invalidated device", e)
			}
			if r, e := p.Deliver(t.Context(), "a", in); e != nil || r.Sent != 0 || calls.Load() != 1 {
				t.Fatal("expired retry submitted", r, e)
			}
		})
	}
}

func TestPlatformPostgresPushInFlightHTTPIsCancelledOnLockLoss(t *testing.T) {
	s := isolatedPlatform(t)
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
			w.WriteHeader(200)
		}
	}))
	defer endpoint.Close()
	defer close(release)
	var retained func(context.Context) error
	p := pushTestService(t, s, pushSenderFunc(func(ctx context.Context, d PushDelivery) error {
		retained = d.BeforeSend
		if e := d.BeforeSend(ctx); e != nil {
			return e
		}
		r, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL+"/synthetic-provider", nil)
		if e != nil {
			return e
		}
		response, e := endpoint.Client().Do(r)
		if response != nil {
			response.Body.Close()
		}
		return e
	}))
	a, login := pushTestAccount(t, s, "19900008114")
	if _, e := p.Bind(t.Context(), login.RefreshToken, pushTestDevice()); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := p.Deliver(t.Context(), "a", pushTestRequest(a, "http-in-flight")); done <- e }()
	pushFenceWait(t, entered)
	killPushTransaction(t, s)
	pushFenceWait(t, cancelled)
	if e := pushFenceResult(t, done); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if retained(t.Context()) == nil {
		t.Fatal("reused stale transaction authority")
	}
	var state string
	if e := s.pool.QueryRow(t.Context(), `SELECT status FROM platform_push_deliveries`).Scan(&state); e != nil || state != "pending" {
		t.Fatal("unknown result marked delivered", state, e)
	}
}

func TestPlatformPostgresPushCallbackCannotOutliveSubmission(t *testing.T) {
	s := isolatedPlatform(t)
	var retained func(context.Context) error
	p := pushTestService(t, s, pushSenderFunc(func(ctx context.Context, d PushDelivery) error { retained = d.BeforeSend; return d.BeforeSend(ctx) }))
	a, login := pushTestAccount(t, s, "19900008115")
	if _, e := p.Bind(t.Context(), login.RefreshToken, pushTestDevice()); e != nil {
		t.Fatal(e)
	}
	if r, e := p.Deliver(t.Context(), "a", pushTestRequest(a, "completed-send")); e != nil || r.Sent != 1 {
		t.Fatal(r, e)
	}
	if e := retained(t.Context()); !errors.Is(e, ErrUnavailable) {
		t.Fatal("finished callback remains live", e)
	}
	if e := s.Logout(t.Context(), login.RefreshToken); e != nil {
		t.Fatal("guard leaked transaction", e)
	}
}
