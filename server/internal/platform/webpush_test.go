package platform

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
	"github.com/linli/im/server/internal/webpushpolicy"
)

func browserPushFixture(t *testing.T, s *Store, capture *pushCapture) (*PushService, PushDevice) {
	t.Helper()
	key, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	pub := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	policy, e := webpushpolicy.New(pub, base64.RawURLEncoding.EncodeToString(key.Bytes()), "mailto:ops@example.test", "push.example.test")
	if e != nil {
		t.Fatal(e)
	}
	p, e := NewPushService(s, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{14}, 32)), capture, []string{"webpush"}, policy)
	if e != nil {
		t.Fatal(e)
	}
	token, _ := json.Marshal(map[string]any{"endpoint": "https://push.example.test/private-device", "keys": map[string]string{"p256dh": pub, "auth": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 16))}})
	return p, PushDevice{DeviceID: "web-install", Platform: "web", Provider: "webpush", PushToken: string(token), NotificationsEnabled: true, PreviewEnabled: true}
}

func TestPlatformPostgresWebPushBindingRotationAndOwner(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	capture := &pushCapture{}
	p, d := browserPushFixture(t, s, capture)
	a, login := pushTestAccount(t, s, "19900008111")
	bound, e := p.Bind(ctx, login.RefreshToken, d)
	if e != nil || !bound.LeaseExpiresAt.After(time.Now()) {
		t.Fatal("browser binding", e)
	}
	canonical := d.PushToken
	d.PushToken = strings.Replace(d.PushToken, "push.example.test", "PUSH.example.test:443", 1)
	again, e := p.Bind(ctx, login.RefreshToken, d)
	if e != nil || again != bound {
		t.Fatal("canonical identity changed", e)
	}
	d.PushToken = canonical
	var count int
	var credentialHash, tokenHash, cipher []byte
	if e = s.pool.QueryRow(ctx, `SELECT token_hash,credential_hash,token_cipher FROM platform_push_devices WHERE id=$1`, bound.ID).Scan(&tokenHash, &credentialHash, &cipher); e != nil || bytes.Equal(tokenHash, credentialHash) || bytes.Contains(cipher, []byte("private-device")) {
		t.Fatal("unsafe credential storage", e)
	}
	beforeRotation := pushTestRequest(a, "browser-before-rotation")
	capture.failure = ErrUnavailable
	if _, e = p.Deliver(ctx, "a", beforeRotation); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	var sub map[string]any
	if e = json.Unmarshal([]byte(d.PushToken), &sub); e != nil {
		t.Fatal(e)
	}
	sub["keys"].(map[string]any)["auth"] = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 16))
	raw, _ := json.Marshal(sub)
	d.PushToken = string(raw)
	rotated, e := p.Bind(ctx, login.RefreshToken, d)
	if e != nil || rotated.ID != bound.ID || rotated.Revision != bound.Revision+1 {
		t.Fatal("endpoint keys rotation", e)
	}
	capture.failure = nil
	if r, e := p.Deliver(ctx, "a", beforeRotation); e != nil || r.Sent != 0 || r.Skipped != 1 || capture.count() != 1 {
		t.Fatal("old revision delivered", e)
	}
	if r, e := p.Deliver(ctx, "a", pushTestRequest(a, "browser-new-keys")); e != nil || r.Sent != 1 {
		t.Fatal(e)
	}
	call := capture.calls[len(capture.calls)-1]
	if call.BindingRevision != rotated.Revision || call.BindingID != rotated.ID || call.Device.PushToken != d.PushToken {
		t.Fatal("wrong browser scope")
	}
	b, loginB := pushTestAccount(t, s, "19900008112")
	newOwner, e := p.Bind(ctx, loginB.RefreshToken, d)
	if e != nil || newOwner.ID != bound.ID || newOwner.Revision != rotated.Revision+1 {
		t.Fatal("owner change", e)
	}
	if e = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_push_devices WHERE provider='webpush' AND revoked_at IS NULL`).Scan(&count); e != nil || count != 1 {
		t.Fatal("multiple endpoint owners", e)
	}
	if e = s.Logout(ctx, login.RefreshToken); e != nil {
		t.Fatal(e)
	}
	if r, e := p.Deliver(ctx, "a", pushTestRequest(b, "browser-owner-b")); e != nil || r.Sent != 1 {
		t.Fatal("old logout removed new binding", e)
	}
	d.NotificationsEnabled = false
	if _, e = p.Bind(ctx, loginB.RefreshToken, d); e != nil {
		t.Fatal(e)
	}
	if r, e := p.Deliver(ctx, "a", pushTestRequest(b, "browser-disabled")); e != nil || r.Sent != 0 {
		t.Fatal("disabled delivered", e)
	}
	d.NotificationsEnabled = true
	if _, e = p.Bind(ctx, loginB.RefreshToken, d); e != nil {
		t.Fatal(e)
	}
	capture.failure = ErrInvalidPushDevice
	if r, e := p.Deliver(ctx, "a", pushTestRequest(b, "browser-gone")); e != nil || r.Skipped != 1 {
		t.Fatal("invalid endpoint", e)
	}
	if e = s.pool.QueryRow(ctx, `SELECT token_cipher FROM platform_push_devices WHERE id=$1`, bound.ID).Scan(&cipher); e != nil || len(cipher) != 0 {
		t.Fatal("invalid endpoint retained credentials", e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal("repeat migration", e)
	}
}

func TestPlatformPostgresWebPushConfigAndLease(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	capture := &pushCapture{}
	p, d := browserPushFixture(t, s, capture)
	_, login := pushTestAccount(t, s, "19900008113")
	api := &API{Store: s, Limiter: testLimit{}, Push: p}
	response := httptest.NewRecorder()
	api.PublicHandler().ServeHTTP(response, httptest.NewRequest("GET", "/v2/config/push", nil))
	var config map[string]any
	if json.Unmarshal(response.Body.Bytes(), &config) != nil || response.Code != 200 || config["webPushEnabled"] != true || config["webPushPublicKey"] != p.web.PublicKey || len(config) != 4 {
		t.Fatal("invalid public capabilities")
	}
	for _, bad := range []PushDevice{
		{DeviceID: d.DeviceID, Platform: "android", Provider: "webpush", PushToken: d.PushToken},
		{DeviceID: d.DeviceID, Platform: "web", Provider: "webpush", PushToken: strings.Replace(d.PushToken, "push.example.test", "other.example.test", 1)},
	} {
		if _, e := p.Bind(ctx, login.RefreshToken, bad); !errors.Is(e, tenancy.ErrInvalid) {
			t.Fatal("invalid subscription accepted", e)
		}
	}
	bound, e := p.Bind(ctx, login.RefreshToken, d)
	if e != nil {
		t.Fatal(e)
	}
	refreshed, e := s.Refresh(ctx, login.RefreshToken)
	if e != nil {
		t.Fatal(e)
	}
	next, e := p.Bind(ctx, refreshed.RefreshToken, d)
	if e != nil || next.ID != bound.ID || next.Revision != bound.Revision || next.LeaseExpiresAt.Before(bound.LeaseExpiresAt) {
		t.Fatal("refresh lease", e)
	}
	if _, e = p.Bind(ctx, login.RefreshToken, d); !errors.Is(e, ErrDenied) {
		t.Fatal("old credential accepted", e)
	}
	var audit string
	if e = s.pool.QueryRow(ctx, `SELECT jsonb_agg(metadata)::text FROM platform_audits`).Scan(&audit); e != nil || strings.Contains(audit, "private-device") || strings.Contains(audit, d.PushToken) || strings.Contains(audit, login.RefreshToken) {
		t.Fatal("private subscription in audit", e)
	}
	if _, e = NewPushService(s, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{14}, 32)), capture, []string{"webpush"}); e == nil {
		t.Fatal("missing browser policy accepted")
	}
	if e = s.Logout(ctx, refreshed.RefreshToken); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Bind(ctx, refreshed.RefreshToken, d); !errors.Is(e, ErrDenied) {
		t.Fatal("logout rebound", e)
	}
}

func TestPlatformPostgresWebPushMigrationKeepsNativeBinding(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	capture := &pushCapture{}
	p := pushTestService(t, s, capture)
	_, login := pushTestAccount(t, s, "19900008114")
	d := pushTestDevice()
	before, e := p.Bind(ctx, login.RefreshToken, d)
	if e != nil {
		t.Fatal(e)
	}
	// Simulate the previous schema only inside this disposable test database.
	_, e = s.pool.Exec(ctx, `ALTER TABLE platform_push_devices DROP COLUMN credential_hash;
	 ALTER TABLE platform_push_devices DROP CONSTRAINT platform_push_devices_provider_check;
	 ALTER TABLE platform_push_devices ADD CONSTRAINT platform_push_devices_provider_check CHECK(provider IN ('getui','getui_voip'));
	 ALTER TABLE platform_push_devices DROP CONSTRAINT platform_push_devices_platform_check;
	 ALTER TABLE platform_push_devices ADD CONSTRAINT platform_push_devices_platform_check CHECK(platform IN ('android','ios'));
	 DELETE FROM platform_schema_migrations WHERE version=11;`)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = s.Migrate(ctx); e != nil {
			t.Fatal(e)
		}
	}
	after, e := p.Bind(ctx, login.RefreshToken, d)
	if e != nil || after != before {
		t.Fatal("migration changed native recipient", e)
	}
	var version int
	var hash, cipher []byte
	if e = s.pool.QueryRow(ctx, `SELECT max(version) FROM platform_schema_migrations`).Scan(&version); e != nil || version != SchemaVersion {
		t.Fatal("schema version", e)
	}
	if e = s.pool.QueryRow(ctx, `SELECT credential_hash,token_cipher FROM platform_push_devices WHERE id=$1`, before.ID).Scan(&hash, &cipher); e != nil || !bytes.Equal(hash, tenancy.Hash(d.PushToken)) {
		t.Fatal("native credential hash lost", e)
	}
	if decoded, e := p.open(d.Provider, cipher); e != nil || decoded != d.PushToken {
		t.Fatal("native credential lost", e)
	}
}
