package tenancy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/config"
)

type controlStub struct {
	mu                 sync.Mutex
	prepared, offline  int
	failReady, loseAck bool
	failSync           bool
}

func (s *controlStub) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/internal/directory/profile-status":
		return response(map[string]bool{"failed": s.failSync}), nil
	case "/internal/directory/ready":
		if s.failReady {
			return nil, fmt.Errorf("unreachable")
		}
		return response(map[string]any{"status": "ready", "tenantId": r.URL.Hostname(), "services": Services{API: "http://127.0.0.1:18701", IMWS: "ws://127.0.0.1:18750", IMTCP: "tcp://127.0.0.1:18740", RTC: "ws://127.0.0.1:18760", Media: "http://127.0.0.1:18770"}}), nil
	case "/internal/directory/prepare":
		s.prepared++
		return response(Snapshot{Version: 1, Profile: Profile{Name: "B独立资料", Gender: "unspecified"}}), nil
	case "/internal/directory/offline":
		s.offline++
		if s.loseAck {
			s.loseAck = false
			return nil, fmt.Errorf("confirmation lost")
		}
	}
	return response(map[string]bool{"ok": true}), nil
}
func response(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b))}
}
func testRequest(p *Platform, method, path string, body any, role string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	if role != "" {
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "test", "role": role, "typ": "platform-admin"}).SignedString([]byte(p.cfg.JWTSecret))
		r.Header.Set("Authorization", "Bearer "+s)
	}
	w := httptest.NewRecorder()
	p.Handler().ServeHTTP(w, r)
	return w
}
func enterpriseRequest(handler http.Handler, path string, body any, tenant string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, bytes.NewReader(b))
	r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}, PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: tenant}}}}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestDirectoryFlow(t *testing.T) {
	dsn := os.Getenv("TEST_LIGHT_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_LIGHT_DATABASE_URL to an isolated local PostgreSQL instance")
	}
	ctx := context.Background()
	base, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer base.Close()
	name := "lp_test_" + ID()[:16]
	if _, e = base.Exec(ctx, `CREATE DATABASE `+name); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.Database = name
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close(); _, _ = base.Exec(ctx, `DROP DATABASE `+name) }()
	if _, e = db.Exec(ctx, schema); e != nil {
		t.Fatal(e)
	}
	stub := &controlStub{}
	p := &Platform{DB: db, cfg: config.Config{DevMode: true, DevOTPCode: "123456", JWTSecret: strings.Repeat("x", 40)}, client: &http.Client{Transport: stub}}
	mk := func(id string) Tenant {
		return Tenant{ID: id, Name: id, Code: id, Enabled: true, ControlURL: "https://" + id + ":8443", Services: Services{API: "http://127.0.0.1:18701", IMWS: "ws://127.0.0.1:18750", IMTCP: "tcp://127.0.0.1:18740", RTC: "ws://127.0.0.1:18760", Media: "http://127.0.0.1:18770"}}
	}
	for _, id := range []string{"a", "b"} {
		b := map[string]any{}
		raw, _ := json.Marshal(mk(id))
		_ = json.Unmarshal(raw, &b)
		b["reason"] = "test"
		b["confirmed"] = true
		if w := testRequest(p, "POST", "/admin/tenants", b, "operator"); w.Code != 200 {
			t.Fatalf("tenant: %d %s", w.Code, w.Body)
		}
	}
	t.Run("exactly_one_default_and_readonly", func(t *testing.T) {
		var n int
		_ = db.QueryRow(ctx, `SELECT count(*) FROM lp_tenants WHERE is_default`).Scan(&n)
		if n != 1 {
			t.Fatal(n)
		}
		if w := testRequest(p, "POST", "/admin/tenants/b/default", Confirmation{"test", true, 1}, "viewer"); w.Code != 403 {
			t.Fatal(w.Code)
		}
		var wg sync.WaitGroup
		statuses := make(chan int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				statuses <- testRequest(p, "POST", "/admin/tenants/b/default", Confirmation{"test", true, 1}, "operator").Code
			}()
		}
		wg.Wait()
		close(statuses)
		ok, conflict := 0, 0
		for s := range statuses {
			if s == 200 {
				ok++
			} else if s == 409 {
				conflict++
			}
		}
		if ok != 1 || conflict != 1 {
			t.Fatalf("%d %d", ok, conflict)
		}
		_ = db.QueryRow(ctx, `SELECT count(*) FROM lp_tenants WHERE is_default`).Scan(&n)
		if n != 1 {
			t.Fatal(n)
		}
	})
	u, e := p.addUser(ctx, loginBody{Phone: "13822220001", Password: "LocalUser123!", Name: "A资料", InviteCode: "a"})
	if e != nil {
		t.Fatal(e)
	}
	t.Run("default_registration_and_invalid_code", func(t *testing.T) {
		created, err := p.addUser(ctx, loginBody{Phone: "13822220002", Password: "LocalUser123!", Name: "默认归属"})
		if err != nil || created.TenantID != "b" {
			t.Fatalf("default tenant=%s err=%v", created.TenantID, err)
		}
		if _, err = p.addUser(ctx, loginBody{Phone: "13822220003", Password: "LocalUser123!", InviteCode: "missing"}); err == nil {
			t.Fatal("invalid enterprise code accepted")
		}
	})
	t.Run("sync_failure_is_visible", func(t *testing.T) {
		stub.failSync = true
		w := testRequest(p, "GET", "/admin/users/"+u.ID, nil, "viewer")
		stub.failSync = false
		if w.Code != 200 || !strings.Contains(w.Body.String(), "资料同步失败") {
			t.Fatalf("missing sync failure status: %d", w.Code)
		}
	})
	t.Run("unified_version_policy", func(t *testing.T) {
		body := map[string]any{"version": 0, "reason": "test", "confirmed": true, "policy": map[string]any{"minimumVersion": "1.0.12", "latestVersion": "1.0.13", "downloadUrl": "https://example.com/downloads/app.apk"}}
		if w := testRequest(p, "PUT", "/admin/versions/android", body, "viewer"); w.Code != 403 {
			t.Fatal("readonly version policy accepted")
		}
		if w := testRequest(p, "PUT", "/admin/versions/android", body, "operator"); w.Code != 200 {
			t.Fatal(w.Code)
		}
		for _, item := range []struct {
			version string
			force   bool
		}{{"1.0.11", true}, {"1.0.12", false}, {"1.0.13", false}} {
			w := testRequest(p, "GET", "/v2/config/version?platform=android&version="+item.version, nil, "")
			var decision map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &decision)
			if w.Code != 200 || decision["forceUpdate"] != item.force {
				t.Fatalf("version %s force=%v", item.version, decision["forceUpdate"])
			}
		}
	})
	t.Run("single_use_grant_and_stale_snapshot", func(t *testing.T) {
		g, e := p.grant(ctx, u)
		if e != nil {
			t.Fatal(e)
		}
		h := p.ControlHandler()
		if w := enterpriseRequest(h, "/internal/directory/consume", map[string]string{"ticket": g.Token}, "b"); w.Code != 401 {
			t.Fatal(w.Code)
		}
		if w := enterpriseRequest(h, "/internal/directory/consume", map[string]string{"ticket": g.Token}, "a"); w.Code != 200 {
			t.Fatal(w.Code)
		}
		if w := enterpriseRequest(h, "/internal/directory/consume", map[string]string{"ticket": g.Token}, "a"); w.Code != 401 {
			t.Fatal(w.Code)
		}
		for _, s := range []Snapshot{{u.ID, 1, 3, Profile{Name: "new"}}, {u.ID, 1, 2, Profile{Name: "late"}}} {
			if w := enterpriseRequest(h, "/internal/directory/profile", s, "a"); w.Code != 200 {
				t.Fatal(w.Code)
			}
		}
		got, _ := p.user(ctx, u.ID)
		if got.Profile.Name != "new" {
			t.Fatal(got.Profile.Name)
		}
	})
	t.Run("unreachable_preserves_assignment", func(t *testing.T) {
		stub.failReady = true
		if _, e := p.startOperation(ctx, u.ID, "b", "switch", "test", "test", 1); e == nil {
			t.Fatal("unreachable accepted")
		}
		stub.failReady = false
		got, _ := p.user(ctx, u.ID)
		if got.Pending != "" || got.TenantID != "a" {
			t.Fatal(got)
		}
	})
	t.Run("lost_confirmation_restart_and_retry", func(t *testing.T) {
		o, e := p.startOperation(ctx, u.ID, "b", "switch", "test", "test", 1)
		if e != nil {
			t.Fatal(e)
		}
		stub.loseAck = true
		if e = p.runOperation(ctx, o); e == nil {
			t.Fatal("lost ack accepted")
		}
		got, _ := p.user(ctx, u.ID)
		if got.Pending != o.ID || got.TenantID != "a" {
			t.Fatal(got)
		}
		if w := testRequest(p, "POST", "/v2/auth/password-login", loginBody{Phone: u.Phone, Password: "LocalUser123!"}, ""); w.Code != 409 {
			t.Fatal(w.Code)
		}
		// Keep the caller's stale phase: durable state must win after locking.
		restarted := &Platform{DB: db, cfg: p.cfg, client: p.client}
		if e = restarted.runOperation(ctx, o); e != nil {
			t.Fatal(e)
		}
		if stub.prepared != 1 || stub.offline != 2 {
			t.Fatalf("prepare=%d offline=%d", stub.prepared, stub.offline)
		}
		if e = restarted.runOperation(ctx, o); e != nil {
			t.Fatalf("completed original retry: %v", e)
		}
		if stub.prepared != 1 || stub.offline != 2 {
			t.Fatal("completed retry repeated enterprise actions")
		}
		got, _ = p.user(ctx, u.ID)
		if got.TenantID != "b" || got.Revision != 2 || got.Pending != "" || got.Profile.Name != "B独立资料" {
			t.Fatal(got)
		}
		if w := enterpriseRequest(p.ControlHandler(), "/internal/directory/profile", Snapshot{u.ID, 1, 99, Profile{Name: "staleA"}}, "a"); w.Code != 409 {
			t.Fatal(w.Code)
		}
	})
	t.Run("credentials_never_in_details", func(t *testing.T) {
		w := testRequest(p, "GET", "/admin/users/"+u.ID, nil, "viewer")
		if w.Code != 200 || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "token") {
			t.Fatal(w.Code, w.Body.String())
		}
	})
}

func TestConnectionValidation(t *testing.T) {
	for _, s := range []string{"https://user:secret@example.com", "https://example.com?token=x", "javascript:alert(1)", "http://example.com"} {
		if serviceURL(s, "http", false) {
			t.Fatal(s)
		}
	}
	if !serviceURL("https://example.com/tenant-a", "http", false) {
		t.Fatal("valid address rejected")
	}
	if _, ok := versionCompare("oops", "1.0.12"); ok {
		t.Fatal("bad version accepted")
	}
}
