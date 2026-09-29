package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/push"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
)

type capturePlatformProvider struct {
	item store.OutboxItem
	err  error
}

func (p *capturePlatformProvider) Send(_ context.Context, item store.OutboxItem) error {
	p.item = item
	return p.err
}
func TestPlatformPushAdapterRouting(t *testing.T) {
	p := &capturePlatformProvider{}
	adapter := platformPushSender{providers: map[string]push.Provider{"getui": p}}
	d := platform.PushDelivery{ID: 42, BindingID: "binding", Request: tenancy.PushRequest{Identity: tenancy.Identity{AccountID: "platform-account", TenantID: "tenant-a", LocalUserID: "local-user", AssignmentVersion: 3}, EventType: "message.created", ConversationID: "conversation", MessageID: "message", MessageType: "text"}, Device: platform.PushDevice{Provider: "getui", Platform: "android", PushToken: "test-device-token"}}
	d.Request.AuthVersion, d.Request.RealmVersion, d.Request.ExpiresAt = 5, 6, time.Now().UTC().Add(time.Minute)
	d.BindingRevision = 2
	d.BeforeSend = func(context.Context) error { return nil }
	if e := adapter.Send(t.Context(), d); e != nil {
		t.Fatal(e)
	}
	if p.item.ID != 42 || len(p.item.Devices) != 1 || p.item.Devices[0].ID != "binding" || p.item.Payload["tenantId"] != "tenant-a" || p.item.Payload["assignmentVersion"] != int64(3) || p.item.Payload["authVersion"] != d.Request.AuthVersion || p.item.Payload["realmVersion"] != d.Request.RealmVersion || p.item.Payload["expiresAt"] == nil || p.item.Payload["pushBindingId"] != "binding" || len(p.item.Payload) != 10 {
		t.Fatal("scope or routing mismatch")
	}
	if p.item.Payload["pushBindingRevision"] != int64(2) {
		t.Fatal("missing binding revision")
	}
	p.err = errors.New("private provider details")
	if e := adapter.Send(t.Context(), d); !errors.Is(e, platform.ErrUnavailable) {
		t.Fatal("provider detail escaped")
	}
	p.err = &push.DeliveryError{Err: errors.New("bad device"), InvalidDeviceIDs: []string{"binding"}, InvalidOnly: true}
	if e := adapter.Send(t.Context(), d); !errors.Is(e, platform.ErrInvalidPushDevice) {
		t.Fatal("invalid device not classified")
	}
	p.err = &push.DeliveryError{Err: errors.New("another bad device"), InvalidDeviceIDs: []string{"other-binding"}, InvalidOnly: true}
	if e := adapter.Send(t.Context(), d); !errors.Is(e, platform.ErrUnavailable) {
		t.Fatal("foreign invalidation accepted")
	}
}
func TestPlatformPushDisabledAndInvalidConfig(t *testing.T) {
	t.Setenv("PLATFORM_PUSH_PROVIDER", "")
	t.Setenv("PLATFORM_WEB_PUSH_ENABLED", "")
	t.Setenv("PLATFORM_WEB_PUSH_PRIVATE_KEY", "")
	if p, e := configuredPlatformPush(nil); e != nil || p != nil {
		t.Fatal("default must stay disabled")
	}
	t.Setenv("PLATFORM_PUSH_PROVIDER", "webhook")
	if _, e := configuredPlatformPush(nil); e == nil {
		t.Fatal("arbitrary webhook provider enabled")
	}
	t.Setenv("PLATFORM_PUSH_PROVIDER", "getui")
	t.Setenv("PLATFORM_GETUI_APP_ID", "")
	if _, e := configuredPlatformPush(nil); e == nil {
		t.Fatal("incomplete credentials enabled")
	}
}

func TestPlatformRejectsDirectAPNSAndGatesGetuiVoIP(t *testing.T) {
	t.Setenv("PLATFORM_WEB_PUSH_ENABLED", "false")
	t.Setenv("PLATFORM_WEB_PUSH_PRIVATE_KEY", "")
	for _, mode := range []string{"apns_voip", "getui_apns_voip"} {
		t.Setenv("PLATFORM_PUSH_PROVIDER", mode)
		if _, e := configuredPlatformPush(&platform.Store{}); e == nil {
			t.Fatal("direct APNs enabled")
		}
	}
	t.Setenv("PLATFORM_PUSH_PROVIDER", "getui")
	t.Setenv("PLATFORM_GETUI_APP_ID", "fixture-app")
	t.Setenv("PLATFORM_GETUI_APP_KEY", strings.Repeat("k", 16))
	t.Setenv("PLATFORM_GETUI_MASTER_SECRET", strings.Repeat("m", 22))
	t.Setenv("PLATFORM_PUSH_ENCRYPTION_KEY", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	t.Setenv("PLATFORM_GETUI_VOIP_ENABLED", "true")
	if _, e := configuredPlatformPush(&platform.Store{}); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PLATFORM_GETUI_MASTER_SECRET", strings.Repeat("m", 21))
	if _, e := configuredPlatformPush(&platform.Store{}); e == nil {
		t.Fatal("truncated Getui credential accepted")
	}
	t.Setenv("PLATFORM_GETUI_MASTER_SECRET", strings.Repeat("m", 22))
	t.Setenv("PLATFORM_APNS_VOIP_KEY_FILE", "old-key.pem")
	if _, e := configuredPlatformPush(&platform.Store{}); e == nil {
		t.Fatal("old direct credential accepted")
	}
}

func TestPlatformPushAdapterRequiresLiveGuard(t *testing.T) {
	provider := &capturePlatformProvider{}
	adapter := platformPushSender{providers: map[string]push.Provider{"getui": provider}}
	d := platform.PushDelivery{ID: 41, Device: platform.PushDevice{Provider: "getui"}}
	if e := adapter.Send(t.Context(), d); !errors.Is(e, platform.ErrUnavailable) || provider.item.ID != 0 {
		t.Fatal("unguarded delivery accepted")
	}
	d.BeforeSend = func(context.Context) error { return errors.New("private database failure") }
	if e := adapter.Send(t.Context(), d); !errors.Is(e, platform.ErrUnavailable) || provider.item.ID != 0 {
		t.Fatal("failed guard reached provider")
	}
}

func TestPlatformWebPushConfiguration(t *testing.T) {
	t.Setenv("PLATFORM_PUSH_PROVIDER", "disabled")
	t.Setenv("PLATFORM_WEB_PUSH_ENABLED", "true")
	t.Setenv("PLATFORM_WEB_PUSH_PUBLIC_KEY", "")
	t.Setenv("PLATFORM_WEB_PUSH_PRIVATE_KEY", "")
	if _, e := configuredPlatformPush(&platform.Store{}); e == nil {
		t.Fatal("missing VAPID accepted")
	}
	private, public, e := webpush.GenerateVAPIDKeys()
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("PLATFORM_WEB_PUSH_PUBLIC_KEY", public)
	t.Setenv("PLATFORM_WEB_PUSH_PRIVATE_KEY", private)
	t.Setenv("PLATFORM_WEB_PUSH_SUBJECT", "mailto:ops@example.com")
	t.Setenv("PLATFORM_WEB_PUSH_ALLOWED_HOSTS", "push.example.com")
	t.Setenv("PLATFORM_PUSH_ENCRYPTION_KEY", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{15}, 32)))
	if p, e := configuredPlatformPush(&platform.Store{}); e != nil || p == nil {
		t.Fatal("web-only provider rejected", e)
	}
	t.Setenv("PLATFORM_WEB_PUSH_ALLOWED_HOSTS", "*.example.com")
	if _, e := configuredPlatformPush(&platform.Store{}); e == nil {
		t.Fatal("wildcard allowlist accepted")
	}
	t.Setenv("PLATFORM_WEB_PUSH_ENABLED", "false")
	if _, e := configuredPlatformPush(&platform.Store{}); e == nil {
		t.Fatal("disabled key not detected")
	}
	t.Setenv("PLATFORM_WEB_PUSH_ENABLED", "yes")
	if _, e := configuredPlatformPush(&platform.Store{}); e == nil {
		t.Fatal("ambiguous enable accepted")
	}
}
