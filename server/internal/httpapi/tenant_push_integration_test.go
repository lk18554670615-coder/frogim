package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/push"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
)

// This replaces the vendor only. HTTP/mTLS, both databases, encrypted binding,
// frozen recipients and the actual enterprise Outbox all remain real.
type tenantStackPushCapture struct {
	calls      atomic.Int64
	fail       atomic.Bool
	deliveryID atomic.Int64
}

func (c *tenantStackPushCapture) Send(_ context.Context, d platform.PushDelivery) error {
	c.calls.Add(1)
	previous := c.deliveryID.Swap(d.ID)
	if previous != 0 && previous != d.ID {
		return errors.New("unstable retry delivery id")
	}
	if c.fail.CompareAndSwap(true, false) {
		return errors.New("test vendor unavailable")
	}
	return nil
}

func tenancyStackPushBridge(t *testing.T, client *http.Client, public, control string, pki map[string]*tls.Config, db *store.Postgres, conn *pgx.Conn, uid, refresh string, capture *tenantStackPushCapture, loseAck *atomic.Bool) func() {
	t.Helper()
	ctx := t.Context()
	device := platform.PushDevice{DeviceID: "isolated-stack-install", Platform: "android", Provider: "getui", PushToken: "isolated-stack-cid-fixture", NotificationsEnabled: true, SoundEnabled: true}
	status, _ := tenancyStackRequest(t, client, "POST", public+"/v2/push/devices", "", map[string]any{"refreshToken": refresh, "device": device})
	if status != 201 {
		t.Fatal("platform binding failed", status)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO im_announcements(id,title,content,status,target_type,created_by,created_at,updated_at) VALUES('push-announcement','private-title','private-body','published','all','fixture',now(),now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO im_push_outbox(user_id,event_type,payload) VALUES($1,'announcement.published','{"announcementId":"push-announcement","title":"private-title","content":"private-body"}')`, uid); err != nil {
		t.Fatal(err)
	}
	items, err := db.ClaimPush(ctx, 100)
	if err != nil || len(items) != 1 || !items[0].ValidTenantPush() || len(items[0].Devices) != 0 {
		t.Fatal("real outbox not isolated", err)
	}
	item := items[0]
	if ok, e := db.CanPresentPush(ctx, item); e != nil || !ok {
		t.Fatal("real push policy", e)
	}
	rpc, err := tenancy.NewRPC(control, pki["a"], tenancy.PlatformIdentity)
	if err != nil {
		t.Fatal(err)
	}
	proxy := push.TenantProxy{TenantID: "a", Peer: rpc}
	capture.fail.Store(true)
	err = proxy.Send(ctx, item)
	var delivery *push.DeliveryError
	if !errors.As(err, &delivery) || !delivery.Retryable {
		t.Fatal("vendor failure not retried", err)
	}
	if e := db.CompletePush(ctx, item.ID, err); e != nil {
		t.Fatal(e)
	}
	if _, e := conn.Exec(ctx, `UPDATE im_push_outbox SET available_at=now() WHERE id=$1`, item.ID); e != nil {
		t.Fatal(e)
	}
	items, err = db.ClaimPush(ctx, 100)
	if err != nil || len(items) != 1 || *items[0].TenantPush != *item.TenantPush {
		t.Fatal("retry mutated frozen contract", err)
	}
	loseAck.Store(true)
	err = proxy.Send(ctx, items[0])
	if !errors.As(err, &delivery) || !delivery.Retryable {
		t.Fatal("lost ACK treated as success", err)
	}
	if err = proxy.Send(ctx, item); err != nil {
		t.Fatal("lost ACK retry", err)
	}
	if capture.calls.Load() != 2 {
		t.Fatal("provider submitted twice after committed ACK loss")
	}
	if err = db.CompletePush(ctx, item.ID, nil); err != nil {
		t.Fatal(err)
	}
	foreign, err := tenancy.NewRPC(control, pki["b"], tenancy.PlatformIdentity)
	if err != nil {
		t.Fatal(err)
	}
	err = (push.TenantProxy{TenantID: "a", Peer: foreign}).Send(ctx, item)
	if !errors.As(err, &delivery) || !delivery.Permanent() {
		t.Fatal("foreign certificate accepted", err)
	}
	response, err := client.Post(public+"/internal/tenancy/push/deliver", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("private push route on public listener", response.StatusCode)
	}
	return func() {
		if ok, e := db.CanPresentPush(ctx, item); e != nil || ok {
			t.Fatal("retired source notification visible", e)
		}
		err = proxy.Send(ctx, item) // Deliberately bypass local filter to exercise platform boundary.
		if !errors.As(err, &delivery) || !delivery.Permanent() || capture.calls.Load() != 2 {
			t.Fatal("old enterprise notification survived transfer", err)
		}
	}
}
