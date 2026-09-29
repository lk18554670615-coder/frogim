package httpapi

import (
	"context"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantAdminCreationCannotFallBackToLocalStore(t *testing.T) {
	x := &API{cfg: config.Config{TenantID: "a"}}
	job, code, _ := x.reserveTenantAdmin(context.Background(), "admin", "request", app.AdminUserBatchInput{}, "test")
	if job != nil || code != "PLATFORM_UNAVAILABLE" {
		t.Fatal("managed creation did not fail closed", code)
	}
	if adminPermission("POST", "/v2/admin/users") != "users.write" || adminPermission("POST", "/v2/admin/users/batch") != "users.write" {
		t.Fatal("creation permission changed")
	}
}

func TestManagedEnterpriseDeniesLegacyDirectoryBypass(t *testing.T) {
	x := &API{cfg: config.Config{TenantID: "a"}}
	for _, route := range []struct{ method, path string }{{"POST", "/v2/auth/password-login"}, {"POST", "/v2/auth/login"}, {"POST", "/v2/auth/refresh"}, {"POST", "/v2/auth/register"}, {"POST", "/v2/auth/qr/create"}, {"PATCH", "/v2/users/me/phone"}} {
		w := httptest.NewRecorder()
		if x.allowTenantRoute(w, httptest.NewRequest(route.method, route.path, nil)) || w.Code != 409 {
			t.Fatal(route, w.Code)
		}
	}
	for _, path := range []string{"/v2/auth/tenant-session", "/v2/auth/logout", "/v2/auth/im-session", "/v2/admin/groups", "/v2/admin/users", "/v2/admin/users/batch", "/v2/media-public/a/signature/content"} {
		if !x.allowTenantRoute(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil)) {
			t.Fatal(path)
		}
	}
	x.cfg.TenantID = ""
	if !x.allowTenantRoute(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v2/auth/register", nil)) {
		t.Fatal("changed standalone authentication")
	}
}

func TestTenantVersionManagementCannotRemainLocal(t *testing.T) {
	x := &API{cfg: config.Config{TenantID: "a"}}
	for _, p := range []string{"/v2/admin/client-versions", "/v2/admin/client-versions/android", "/v2/admin/client-versions/ios/history"} {
		for _, method := range []string{"GET", "PUT"} {
			w := httptest.NewRecorder()
			if x.allowTenantRoute(w, httptest.NewRequest(method, p, nil)) || w.Code != 409 {
				t.Fatal("tenant still owns rollout", p, method)
			}
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v2/config/version?platform=android&version=1.0.0&installId=old-install-id", nil)
	if !x.allowTenantRoute(w, r) {
		t.Fatal("legacy upgrade entry blocked")
	}
	x.clientVersion(w, r)
	if w.Code != 503 {
		t.Fatal("unavailable platform silently used legacy policy", w.Code)
	}
	x.cfg.TenantID = ""
	if !x.allowTenantRoute(httptest.NewRecorder(), httptest.NewRequest("PUT", "/v2/admin/client-versions/android", nil)) {
		t.Fatal("legacy publishing changed")
	}
}

func TestTenantControlRejectsForgedProxyIdentity(t *testing.T) {
	x := &API{cfg: config.Config{TenantID: "a"}}
	for _, path := range []string{"/internal/tenancy/identities/prepare", "/internal/tenancy/recovery/inventory"} {
		r := httptest.NewRequest("POST", path, nil)
		r.Header.Set("X-Tenant-ID", "a")
		r.Header.Set("X-SSL-Client-Verify", "SUCCESS")
		r.Header.Set("X-Platform-Identity", "spiffe://frogim/platform")
		w := httptest.NewRecorder()
		x.TenantControlHandler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatal(w.Code)
		}
	}
}

func TestTenantPushRegistrationCannotRemainLocal(t *testing.T) {
	x := &API{cfg: config.Config{TenantID: "a"}}
	w := httptest.NewRecorder()
	if x.allowTenantRoute(w, httptest.NewRequest("POST", "/v2/users/me/devices", nil)) || w.Code != 409 {
		t.Fatal("enterprise accepted provider token")
	}
	// Old binding removal and ordinary login-device metadata remain local.
	for _, route := range []struct{ method, path string }{{"DELETE", "/v2/users/me/devices/old"}, {"GET", "/v2/users/me/devices"}, {"POST", "/v2/users/me/client-device"}} {
		if !x.allowTenantRoute(httptest.NewRecorder(), httptest.NewRequest(route.method, route.path, nil)) {
			t.Fatal("unrelated device operation blocked")
		}
	}
	x.cfg.TenantID = ""
	if !x.allowTenantRoute(httptest.NewRecorder(), httptest.NewRequest("POST", "/v2/users/me/devices", nil)) {
		t.Fatal("standalone registration changed")
	}
}

func TestMediaKeyIndependenceAndLegacySignature(t *testing.T) {
	x := &API{cfg: config.Config{JWTSecret: "old-auth-key", MediaSigningSecret: "new-media-key", LegacyMediaSigningSecret: "old-auth-key"}}
	if x.permanentMediaSignature("m", "content") == signPermanentMedia(x.cfg.JWTSecret, "m", "content") {
		t.Fatal("media still signed with auth key")
	}
	first := x.permanentMediaSignature("m", "content")
	x.cfg.JWTSecret = "rotated-auth-key"
	if x.permanentMediaSignature("m", "content") != first {
		t.Fatal("auth rotation changed media URL")
	}
	if signPermanentMedia(x.cfg.LegacyMediaSigningSecret, "m", "content") != signPermanentMedia("old-auth-key", "m", "content") {
		t.Fatal("legacy URL changed")
	}
}
