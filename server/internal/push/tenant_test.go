package push

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
)

type tenantCapture struct {
	calls   int
	raw     []byte
	err     error
	receipt tenancy.PushReceipt
}

func (c *tenantCapture) Call(_ context.Context, path string, request, response any) error {
	if path != "/internal/tenancy/push/deliver" {
		return errors.New("wrong path")
	}
	c.calls++
	c.raw, _ = json.Marshal(request)
	*response.(*tenancy.PushReceipt) = c.receipt
	return c.err
}
func tenantPushItem() store.OutboxItem {
	return store.OutboxItem{ID: 17, UserID: "local", EventType: "message.created", TenantManaged: true,
		Payload:    map[string]any{"body": "private-message-sentinel", "url": "private-media-sentinel"},
		Devices:    []store.Device{{PushToken: "private-device-sentinel"}},
		TenantPush: &tenancy.PushRequest{Identity: tenancy.Identity{TenantID: "a", AccountID: "account", LocalUserID: "local", AssignmentVersion: 1}, AuthVersion: 1, RealmVersion: 1, RequestID: "push_17", EventType: "message.created", MessageID: "100", ConversationID: "group", MessageType: "image", ExpiresAt: time.Now().UTC().Add(time.Hour)}}
}
func TestTenantProxyUsesFrozenContractAndPlatformDevices(t *testing.T) {
	c := &tenantCapture{receipt: tenancy.PushReceipt{Status: "completed", Sent: 1}}
	p := TenantProxy{TenantID: "a", Peer: c}
	item := tenantPushItem()
	if err := p.Send(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(c.raw), "private-") || strings.Contains(string(c.raw), "devices") || strings.Contains(string(c.raw), "payload") {
		t.Fatal("enterprise data crossed platform boundary")
	}
	first := string(c.raw)
	item.Attempts++
	if err := p.Send(t.Context(), item); err != nil || string(c.raw) != first {
		t.Fatal("retry changed frozen request", err)
	}
	item.Devices = nil
	s := &presentationStore{dispatcherStore: dispatcherStore{items: []store.OutboxItem{item}}, allowed: true}
	NewDispatcher(s, p).once(t.Context())
	if c.calls != 3 || s.completed != nil {
		t.Fatal("platform delivery required enterprise device row")
	}
	s.items, s.allowed = []store.OutboxItem{item}, false
	NewDispatcher(s, p).once(t.Context())
	if c.calls != 3 {
		t.Fatal("presentation policy bypassed")
	}
	s.items, s.allowed, s.policyError = []store.OutboxItem{item}, true, errors.New("db unavailable")
	NewDispatcher(s, p).once(t.Context())
	if c.calls != 3 || s.completed == nil {
		t.Fatal("policy failure bypassed")
	}
}
func TestTenantProxyRefusesMissingForeignExpiredOrChangedProvenance(t *testing.T) {
	for _, change := range []func(*store.OutboxItem){
		func(i *store.OutboxItem) { i.TenantManaged = false }, func(i *store.OutboxItem) { i.TenantPush = nil },
		func(i *store.OutboxItem) { i.TenantPush.TenantID = "b" }, func(i *store.OutboxItem) { i.UserID = "someone" },
		func(i *store.OutboxItem) { i.ID++ }, func(i *store.OutboxItem) { i.TenantPush.AuthVersion = 0 },
		func(i *store.OutboxItem) { i.EventType = "messages.deleted" },
	} {
		item := tenantPushItem()
		change(&item)
		c := &tenantCapture{}
		err := (TenantProxy{TenantID: "a", Peer: c}).Send(t.Context(), item)
		var delivery *DeliveryError
		if !errors.As(err, &delivery) || !delivery.Permanent() || c.calls != 0 {
			t.Fatal("bad provenance submitted")
		}
	}
	i := tenantPushItem()
	i.TenantPush.ExpiresAt = time.Now().Add(-time.Second)
	c := &tenantCapture{}
	if err := (TenantProxy{TenantID: "a", Peer: c}).Send(t.Context(), i); err != nil || c.calls != 0 {
		t.Fatal("expired request submitted")
	}
}
func TestTenantProxyErrorClassificationAndNoRemoteSecrets(t *testing.T) {
	for _, tc := range []struct {
		err       error
		receipt   tenancy.PushReceipt
		permanent bool
	}{
		{err: &tenancy.PushRejected{}, permanent: true},
		{err: errors.New("remote-secret-sentinel")},
		{receipt: tenancy.PushReceipt{Status: "queued"}},
		{receipt: tenancy.PushReceipt{Status: "completed", Sent: -1}},
	} {
		c := &tenantCapture{err: tc.err, receipt: tc.receipt}
		err := (TenantProxy{TenantID: "a", Peer: c}).Send(t.Context(), tenantPushItem())
		var delivery *DeliveryError
		if !errors.As(err, &delivery) || delivery.Permanent() != tc.permanent || strings.Contains(err.Error(), "sentinel") {
			t.Fatal("unsafe error classification")
		}
	}
}
