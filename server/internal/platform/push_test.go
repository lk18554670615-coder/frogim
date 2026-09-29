package platform

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

type pushCapture struct {
	mu      sync.Mutex
	calls   []PushDelivery
	failure error
	entered chan struct{}
	release chan struct{}
}

func (p *pushCapture) Send(ctx context.Context, d PushDelivery) error {
	p.mu.Lock()
	p.calls = append(p.calls, d)
	failure, entered, release := p.failure, p.entered, p.release
	p.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return failure
}
func (p *pushCapture) count() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.calls) }
func pushTestService(t *testing.T, s *Store, sender PushSender) *PushService {
	t.Helper()
	p, err := NewPushService(s, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{12}, 32)), sender, []string{"getui", "getui_voip"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func pushTestAccount(t *testing.T, s *Store, phone string) (account, LoginResult) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Reserve(ctx, Registration{Phone: phone, Name: "Push fixture", Password: "PushFixture123!", Method: "password"}); err != nil {
		t.Fatal(err)
	}
	w := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for range 2 {
		if _, err := w.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	a := accessAccount(t, s, phone)
	login, err := s.Login(ctx, phone, "PushFixture123!")
	if err != nil {
		t.Fatal(err)
	}
	return a, login
}
func pushTestDevice() PushDevice {
	return PushDevice{DeviceID: "test-install", Platform: "android", Provider: "getui", PushToken: "isolated_push_token_never_log", NotificationsEnabled: true, PreviewEnabled: true, SoundEnabled: true, VibrationEnabled: true}
}
func pushTestRequest(a account, id string) tenancy.PushRequest {
	return tenancy.PushRequest{Identity: a.Identity, RequestID: id, AuthVersion: a.AuthVersion, RealmVersion: 1, EventType: "message.created", ConversationID: "conversation", MessageID: "message", MessageType: "image", ExpiresAt: time.Now().UTC().Add(time.Hour).Truncate(time.Second)}
}

func TestPlatformPostgresPushBindingAndDelivery(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	capture := &pushCapture{}
	p := pushTestService(t, s, capture)
	a, login := pushTestAccount(t, s, "19900008101")
	d := pushTestDevice()
	bound, err := p.Bind(ctx, login.RefreshToken, d)
	if err != nil || bound.Revision != 1 {
		t.Fatal("bind", err)
	}
	again, err := p.Bind(ctx, login.RefreshToken, d)
	if err != nil || again != bound {
		t.Fatal("binding not idempotent", err)
	}
	var encrypted []byte
	var audit string
	if err = s.pool.QueryRow(ctx, `SELECT token_cipher FROM platform_push_devices WHERE id=$1`, bound.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(d.PushToken)) {
		t.Fatal("token stored plaintext")
	}
	if decoded, e := p.open(d.Provider, encrypted); e != nil || decoded != d.PushToken {
		t.Fatal("decrypt", e)
	}
	if _, e := p.open("getui_voip", encrypted); e == nil {
		t.Fatal("AAD not bound to provider")
	}
	if err = s.pool.QueryRow(ctx, `SELECT jsonb_agg(metadata)::text FROM platform_audits`).Scan(&audit); err != nil || strings.Contains(audit, d.PushToken) || strings.Contains(audit, login.RefreshToken) {
		t.Fatal("secret in audit", err)
	}
	in := pushTestRequest(a, "one")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			r, e := p.Deliver(ctx, "a", in)
			if e != nil || r.Sent != 1 || r.Status != "completed" {
				t.Error("concurrent delivery", e)
			}
		})
	}
	wg.Wait()
	if capture.count() != 1 {
		t.Fatal("duplicate provider submit")
	}
	changed := in
	changed.MessageID = "another"
	if _, e := p.Deliver(ctx, "a", changed); !errors.Is(e, ErrRequestChanged) {
		t.Fatal("request drift", e)
	}
	if _, e := p.Deliver(ctx, "b", in); !errors.Is(e, ErrDenied) {
		t.Fatal("cross tenant", e)
	}
	refreshed, err := s.Refresh(ctx, login.RefreshToken)
	if err != nil {
		t.Fatal("refresh", err)
	}
	if _, e := p.Bind(ctx, login.RefreshToken, d); !errors.Is(e, ErrDenied) {
		t.Fatal("old session rebound", e)
	}
	if again, e := p.Bind(ctx, refreshed.RefreshToken, d); e != nil || again.ID != bound.ID || again.Revision != bound.Revision || again.LeaseExpiresAt.Before(bound.LeaseExpiresAt) {
		t.Fatal("refresh broke binding", e)
	}
	in.RequestID = "after-refresh"
	if r, e := p.Deliver(ctx, "a", in); e != nil || r.Sent != 1 {
		t.Fatal("push after refresh", e)
	}
	if err = s.Logout(ctx, refreshed.RefreshToken); err != nil {
		t.Fatal(err)
	}
	in.RequestID = "after-logout"
	if r, e := p.Deliver(ctx, "a", in); e != nil || r.Sent != 0 {
		t.Fatal("push after logout", e)
	}
	if _, e := p.Bind(ctx, refreshed.RefreshToken, d); !errors.Is(e, ErrDenied) {
		t.Fatal("logout rebound", e)
	}
	if err = s.pool.QueryRow(ctx, `SELECT token_cipher FROM platform_push_devices WHERE id=$1`, bound.ID).Scan(&encrypted); err != nil || len(encrypted) != 0 {
		t.Fatal("logout retained decryptable token", err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("repeat migration", err)
	}
}

func TestPlatformPostgresPushRebindingAndRevocation(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	capture := &pushCapture{failure: errors.New("upstream private URL/token must not escape")}
	p := pushTestService(t, s, capture)
	a, la := pushTestAccount(t, s, "19900008102")
	b, lb := pushTestAccount(t, s, "19900008103")
	d := pushTestDevice()
	bound, err := p.Bind(ctx, la.RefreshToken, d)
	if err != nil {
		t.Fatal(err)
	}
	in := pushTestRequest(a, "uncertain")
	if _, e := p.Deliver(ctx, "a", in); !errors.Is(e, ErrUnavailable) || strings.Contains(e.Error(), "private") {
		t.Fatal("unknown delivery not sanitized")
	}
	// Switching account on the same installation takes ownership only through
	// possession of the SDK token, not a caller-specified enterprise user id.
	rebound, err := p.Bind(ctx, lb.RefreshToken, d)
	if err != nil || rebound.ID != bound.ID || rebound.Revision <= bound.Revision {
		t.Fatal("rebind", err)
	}
	capture.failure = nil
	if r, e := p.Deliver(ctx, "a", in); e != nil || r.Sent != 0 || r.Skipped != 1 || capture.count() != 1 {
		t.Fatal("old event reached new owner", e)
	}
	if e := p.Unbind(ctx, la.RefreshToken, d.DeviceID, d.Provider); e != nil {
		t.Fatal(e)
	}
	if r, e := p.Deliver(ctx, "a", pushTestRequest(b, "new-owner")); e != nil || r.Sent != 1 {
		t.Fatal("old logout/unbind affected new owner", e)
	}
	// In-flight provider submission holds the account lock; logout can only
	// complete after that bounded submission, and no subsequent send is allowed.
	capture.entered = make(chan struct{}, 1)
	capture.release = make(chan struct{})
	pushDone := make(chan error, 1)
	logoutDone := make(chan error, 1)
	go func() { _, e := p.Deliver(ctx, "a", pushTestRequest(b, "in-flight")); pushDone <- e }()
	select {
	case <-capture.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("sender not entered")
	}
	go func() { logoutDone <- s.Logout(ctx, lb.RefreshToken) }()
	select {
	case <-logoutDone:
		t.Fatal("logout returned while provider still submits")
	case <-time.After(100 * time.Millisecond):
	}
	close(capture.release)
	if e := <-pushDone; e != nil {
		t.Fatal(e)
	}
	if e := <-logoutDone; e != nil {
		t.Fatal(e)
	}
	if r, e := p.Deliver(ctx, "a", pushTestRequest(b, "logged-out")); e != nil || r.Sent != 0 {
		t.Fatal("send survived logout", e)
	}
	for _, update := range []string{"globally_blocked=true", "credentials_pending=true", "state='transferring'", "auth_version=2", "assignment_version=2"} {
		if _, e := s.pool.Exec(ctx, `UPDATE platform_accounts SET `+update+` WHERE id=$1`, a.AccountID); e != nil {
			t.Fatal(e)
		}
		if _, e := p.Deliver(ctx, "a", pushTestRequest(a, "forbidden")); !errors.Is(e, ErrDenied) {
			t.Fatal("access state accepted", update, e)
		}
		if _, e := s.pool.Exec(ctx, `UPDATE platform_accounts SET globally_blocked=false,credentials_pending=false,state='active',auth_version=1,assignment_version=1 WHERE id=$1`, a.AccountID); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.pool.Exec(ctx, `UPDATE platform_tenants SET status='suspended' WHERE id='a'`); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Deliver(ctx, "a", pushTestRequest(a, "tenant-off")); !errors.Is(e, ErrDenied) {
		t.Fatal("suspended tenant push", e)
	}
}

func TestPlatformPostgresPushHTTPAndDevicePolicy(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	capture := &pushCapture{}
	p := pushTestService(t, s, capture)
	a, login := pushTestAccount(t, s, "19900008104")
	api := &API{Store: s, Limiter: testLimit{}, Push: p}
	d := pushTestDevice()
	call := func(method, path string, body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		return adminTestCall(t, api, method, path, "", string(raw))
	}
	if status, data := call("GET", "/v2/config/push", nil); status != 200 || data["enabled"] != true {
		t.Fatal("config", status)
	}
	if status, _ := call("POST", "/v2/push/devices", map[string]any{"refreshToken": login.RefreshToken, "device": d, "accountId": a.AccountID}); status != 400 {
		t.Fatal("caller account field accepted", status)
	}
	if status, _ := call("POST", "/v2/push/devices", map[string]any{"refreshToken": login.RefreshToken, "device": d}); status != 201 {
		t.Fatal("HTTP bind", status)
	}
	voip := d
	voip.DeviceID = "voip"
	voip.Platform = "ios"
	voip.Provider = "getui_voip"
	voip.PushToken = strings.Repeat("A", 64)
	if _, e := p.Bind(ctx, login.RefreshToken, voip); e != nil {
		t.Fatal("voip bind", e)
	}
	ios := d
	ios.DeviceID = "ios"
	ios.Platform = "ios"
	ios.PushToken = "another_isolated_ios_push_token"
	if _, e := p.Bind(ctx, login.RefreshToken, ios); e != nil {
		t.Fatal("ios bind", e)
	}
	in := pushTestRequest(a, "normal")
	if r, e := p.Deliver(ctx, "a", in); e != nil || r.Sent != 2 {
		t.Fatal("normal message wrongly sent to VoIP", e)
	}
	in.RequestID = "call"
	in.EventType = "call.invited"
	in.CallID = "call"
	in.MediaType = "audio"
	in.MessageID = ""
	in.MessageType = ""
	in.ExpiresAt = time.Now().UTC().Add(45 * time.Second).Truncate(time.Second)
	if r, e := p.Deliver(ctx, "a", in); e != nil || r.Sent != 2 {
		t.Fatal("duplicate iOS call channel", e)
	}
	bad := d
	bad.Provider = "webhook"
	if _, e := p.Bind(ctx, login.RefreshToken, bad); !errors.Is(e, tenancy.ErrInvalid) {
		t.Fatal("arbitrary provider", e)
	}
	bad = d
	bad.PushToken = "https://arbitrary.example/token"
	if _, e := p.Bind(ctx, login.RefreshToken, bad); !errors.Is(e, tenancy.ErrInvalid) {
		t.Fatal("token URL accepted", e)
	}
	// Direct handler tests verify that headers cannot supply the mTLS identity.
	raw, _ := json.Marshal(in)
	internalCall := func(peer string, verified bool) int {
		r := httptest.NewRequest("POST", "/internal/tenancy/push/deliver", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Tenant-ID", "a")
		if peer != "" {
			u, _ := url.Parse(peer)
			cert := &x509.Certificate{URIs: []*url.URL{u}}
			r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
			if verified {
				r.TLS.VerifiedChains = [][]*x509.Certificate{{cert}}
			}
		}
		w := httptest.NewRecorder()
		api.InternalHandler().ServeHTTP(w, r)
		return w.Code
	}
	for _, peer := range []string{"", tenancy.PlatformIdentity, tenancy.EnterpriseIdentity("b")} {
		if status := internalCall(peer, true); status != 401 {
			t.Fatal("foreign control identity", status)
		}
	}
	if status := internalCall(tenancy.EnterpriseIdentity("a"), false); status != 401 {
		t.Fatal("unverified certificate accepted", status)
	}
	if status := internalCall(tenancy.EnterpriseIdentity("a"), true); status != 200 {
		t.Fatal("verified tenant", status)
	}
	publicRequest := httptest.NewRequest("POST", "/internal/tenancy/push/deliver", bytes.NewReader(raw))
	publicRequest.Header.Set("Content-Type", "application/json")
	publicResponse := httptest.NewRecorder()
	api.PublicHandler().ServeHTTP(publicResponse, publicRequest)
	if publicResponse.Code != 404 {
		t.Fatal("private route public", publicResponse.Code)
	}
	capture.failure = ErrInvalidPushDevice
	in = pushTestRequest(a, "invalid-token")
	if r, e := p.Deliver(ctx, "a", in); e != nil || r.Sent != 0 || r.Skipped != 2 {
		t.Fatal("invalid token", e)
	}
	var count int
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_push_devices WHERE provider='getui' AND revoked_at IS NULL`).Scan(&count); e != nil || count != 0 {
		t.Fatal("invalid token retained", e)
	}
	if _, e := s.pool.Exec(ctx, `CREATE FUNCTION reject_push_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='push.device.bound' THEN RAISE EXCEPTION 'isolated audit outage'; END IF; RETURN NEW; END $$; CREATE TRIGGER push_audit_failure BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_push_audit();`); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Bind(ctx, login.RefreshToken, d); e == nil {
		t.Fatal("audit failure accepted")
	}
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_push_devices WHERE provider='getui' AND revoked_at IS NULL`).Scan(&count); e != nil || count != 0 {
		t.Fatal("audit partial bind", e)
	}
	api.Push = nil
	if status, data := call("GET", "/v2/config/push", nil); status != 200 || data["enabled"] != false {
		t.Fatal("unconfigured enabled", status)
	}
	if status, _ := call("POST", "/v2/push/devices", map[string]any{"refreshToken": login.RefreshToken, "device": d}); status != 503 {
		t.Fatal("unconfigured bind", status)
	}
}

func TestPlatformPostgresPushFrozenRecipientsAndExpiry(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	capture := &pushCapture{}
	p := pushTestService(t, s, capture)
	a, login := pushTestAccount(t, s, "19900008105")
	d := pushTestDevice()
	empty := pushTestRequest(a, "before-binding")
	if r, e := p.Deliver(ctx, "a", empty); e != nil || r.Sent != 0 {
		t.Fatal("empty recipient set", e)
	}
	bound, err := p.Bind(ctx, login.RefreshToken, d)
	if err != nil {
		t.Fatal(err)
	}
	if r, e := p.Deliver(ctx, "a", empty); e != nil || r.Sent != 0 || capture.count() != 0 {
		t.Fatal("old notification acquired new recipient", e)
	}
	old := pushTestRequest(a, "before-rotation")
	capture.failure = ErrUnavailable
	if _, e := p.Deliver(ctx, "a", old); !errors.Is(e, ErrUnavailable) {
		t.Fatal("provider failure", e)
	}
	oldCallID := capture.calls[0].ID
	// Same pending delivery retries with the exact provider id, even if a
	// response was lost. Provider acceptance is not device receipt/visibility.
	if _, e := p.Deliver(ctx, "a", old); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if capture.calls[1].ID != oldCallID {
		t.Fatal("retry changed provider id")
	}
	d.PushToken = "rotated_isolated_push_token"
	if _, e := p.Bind(ctx, login.RefreshToken, d); e != nil {
		t.Fatal(e)
	}
	capture.failure = nil
	if r, e := p.Deliver(ctx, "a", old); e != nil || r.Skipped != 1 || capture.count() != 2 {
		t.Fatal("rotated token received old event", e)
	}
	expired := pushTestRequest(a, "expired-event")
	expired.ExpiresAt = time.Now().Add(-time.Second)
	if r, e := p.Deliver(ctx, "a", expired); e != nil || r.Sent != 0 {
		t.Fatal("expired event delivered", e)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE platform_sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, tenancy.Hash(login.RefreshToken)); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Bind(ctx, login.RefreshToken, d); !errors.Is(e, ErrDenied) {
		t.Fatal("expired session binding", e)
	}
	if r, e := p.Deliver(ctx, "a", pushTestRequest(a, "expired-session")); e != nil || r.Sent != 0 {
		t.Fatal("expired session delivered", e)
	}
	var ciphertext []byte
	if e := s.pool.QueryRow(ctx, `SELECT token_cipher FROM platform_push_devices WHERE id=$1`, bound.ID).Scan(&ciphertext); e != nil || len(ciphertext) != 0 {
		t.Fatal("rotation retained old ciphertext", e)
	}
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{13}, 32))
	other, e := NewPushService(s, key, capture, []string{"getui"})
	if e != nil {
		t.Fatal(e)
	}
	encrypted, e := p.seal("getui", d.PushToken)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = other.open("getui", encrypted); !errors.Is(e, ErrUnavailable) {
		t.Fatal("wrong key accepted", e)
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, e = p.open("getui", encrypted); !errors.Is(e, ErrUnavailable) {
		t.Fatal("ciphertext tampering accepted", e)
	}
	if _, e = NewPushService(s, "invalid-key", capture, []string{"getui"}); e == nil {
		t.Fatal("invalid encryption key accepted")
	}
}
