package platform

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPlatformPostgresGetuiVoIPRefreshInvalidationAndOwnership(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	capture := &pushCapture{}
	p := pushTestService(t, s, capture)
	a, login := pushTestAccount(t, s, "19900008671")
	normal := pushTestDevice()
	normal.Platform = "ios"
	if _, e := p.Bind(ctx, login.RefreshToken, normal); e != nil {
		t.Fatal(e)
	}
	call := pushTestRequest(a, "missing-voip")
	call.EventType = "call.invited"
	call.CallID = "call"
	call.MediaType = "audio"
	call.MessageID = ""
	call.MessageType = ""
	call.ExpiresAt = time.Now().Add(30 * time.Second).UTC()
	if r, e := p.Deliver(ctx, "a", call); e != nil || r.Status != "capability_not_ready" || r.Sent != 0 || capture.count() != 0 {
		t.Fatal("missing binding fell back or claimed sent", e, r)
	}
	voip := normal
	voip.Provider = "getui_voip"
	voip.DeviceID = "voip-generation-one"
	binding, e := p.Bind(ctx, login.RefreshToken, voip)
	if e != nil {
		t.Fatal(e)
	}
	call.RequestID = "ready-voip"
	for range 2 {
		if r, e := p.Deliver(ctx, "a", call); e != nil || r.Sent != 1 {
			t.Fatal(e, r)
		}
	}
	if capture.count() != 1 || capture.calls[0].Device.Provider != "getui_voip" {
		t.Fatal("duplicate or wrong call channel")
	}
	call.RequestID = "lost-before-token-refresh"
	capture.failure = errors.New("unavailable")
	if _, e = p.Deliver(ctx, "a", call); e == nil {
		t.Fatal("failure claimed success")
	}
	voip.DeviceID = "voip-generation-two"
	refreshed, e := p.Bind(ctx, login.RefreshToken, voip)
	if e != nil || refreshed.ID != binding.ID || refreshed.Revision <= binding.Revision {
		t.Fatal("refresh did not fence stale token generation", e)
	}
	capture.failure = nil
	if r, e := p.Deliver(ctx, "a", call); e != nil || r.Sent != 0 {
		t.Fatal("old event delivered after refresh", e)
	}
	if e = p.Unbind(ctx, login.RefreshToken, voip.DeviceID, voip.Provider); e != nil {
		t.Fatal(e)
	}
	call.RequestID = "invalidated-voip"
	if r, e := p.Deliver(ctx, "a", call); e != nil || r.Sent != 0 || r.Status != "capability_not_ready" {
		t.Fatal("invalidation", e)
	}
	if _, e = p.Bind(ctx, login.RefreshToken, voip); e != nil {
		t.Fatal(e)
	}
	_, other := pushTestAccount(t, s, "19900008672")
	if _, e = p.Bind(ctx, other.RefreshToken, normal); e != nil {
		t.Fatal(e)
	}
	call.RequestID = "old-owner"
	if r, e := p.Deliver(ctx, "a", call); e != nil || r.Sent != 0 {
		t.Fatal("CID rebound across accounts retained old VoIP owner", e)
	}
	obsolete := voip
	obsolete.Provider = "apns_voip"
	obsolete.PushToken = strings.Repeat("a", 64)
	if _, e = p.Bind(ctx, other.RefreshToken, obsolete); e == nil {
		t.Fatal("direct APNs binding accepted")
	}
}

func TestPlatformPostgresGetuiVoIPUpgradeRetiresDirectBindings(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	p := pushTestService(t, s, &pushCapture{})
	_, login := pushTestAccount(t, s, "19900008673")
	device := pushTestDevice()
	device.Platform = "ios"
	binding, e := p.Bind(ctx, login.RefreshToken, device)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_push_devices SET provider='apns_voip' WHERE id=$1;`, binding.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, `DELETE FROM platform_schema_migrations WHERE version=21`); e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var retired bool
	if e = s.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL AND octet_length(token_cipher)=0 AND revision=$2+1 FROM platform_push_devices WHERE id=$1`, binding.ID, binding.Revision).Scan(&retired); e != nil || !retired {
		t.Fatal("old direct APNs token survived upgrade", e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.pool.QueryRow(ctx, `SELECT revision=$2+1 FROM platform_push_devices WHERE id=$1`, binding.ID, binding.Revision).Scan(&retired); e != nil || !retired {
		t.Fatal("migration repeated destructive action", e)
	}
}
