package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPlatformAdminBrowserSessionSurvivesReload(t *testing.T) {
	s := isolatedPlatform(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("SyntheticCookiePassword123!"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), `INSERT INTO platform_admin_accounts(id,username,password_hash,role) VALUES('cookie-operator','cookie-operator',$1,'operator')`, string(hash)); err != nil {
		t.Fatal(err)
	}
	a := &API{Store: s, Limiter: testLimit{}, WebOrigin: "https://admin.example.test"}
	call := func(method, path, body, origin, fetchSite string, cookie *http.Cookie, browserLogin bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if browserLogin {
			r.Header.Set("X-Platform-Session", "cookie")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.PublicHandler().ServeHTTP(w, r)
		return w
	}
	const login = `{"username":"cookie-operator","password":"SyntheticCookiePassword123!"}`
	if w := call("POST", "/platform/admin/auth/login", login, "https://admin.example.test", "cross-site", nil, true); w.Code != 401 {
		t.Fatal("cross-site cookie login accepted", w.Code)
	}
	w := call("POST", "/platform/admin/auth/login", login, "https://admin.example.test", "same-origin", nil, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "accessToken") {
		t.Fatal("browser login did not use cookie-only response", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != adminSessionCookie || cookies[0].Path != "/platform/admin" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("platform cookie attributes", cookies)
	}
	cookie := cookies[0]
	if w = call("GET", "/platform/admin/auth/me", "", "", "", cookie, false); w.Code != 200 {
		t.Fatal("cookie session lost after reload", w.Code)
	}
	if w = call("POST", "/platform/admin/auth/logout", `{}`, "https://admin.example.test", "cross-site", cookie, false); w.Code != 401 {
		t.Fatal("cross-site logout accepted", w.Code)
	}
	if w = call("POST", "/platform/admin/auth/logout", `{}`, "https://admin.example.test", "same-origin", cookie, false); w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("cookie logout failed", w.Code)
	}
	if w = call("GET", "/platform/admin/auth/me", "", "", "", cookie, false); w.Code != 401 {
		t.Fatal("logged out cookie still valid", w.Code)
	}
	if status, data := adminTestCall(t, a, "POST", "/platform/admin/auth/login", "", login); status != 200 || data["accessToken"] == nil {
		t.Fatal("bearer login compatibility", status)
	}
}
