package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testLimit struct{ fail bool }

func (l testLimit) Allow(context.Context, string, int, time.Duration) (bool, error) {
	if l.fail {
		return false, errors.New("redis down")
	}
	return true, nil
}

func TestNoOTPMeansNoRegistrationOrOTPLogin(t *testing.T) {
	a := &API{Limiter: testLimit{}, WebOrigin: "https://app.example"}
	for _, path := range []string{"/v2/auth/register", "/v2/auth/login", "/v2/auth/code", "/v2/auth/password-reset", "/v2/auth/password-reset/code"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.PublicHandler().ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.PublicHandler().ServeHTTP(w, httptest.NewRequest("GET", "/v2/config/auth", nil))
	if strings.Contains(w.Body.String(), `"otpLoginEnabled":true`) || strings.Contains(w.Body.String(), `"registrationEnabled":true`) || strings.Contains(w.Body.String(), `"passwordResetEnabled":true`) {
		t.Fatal(w.Body.String())
	}
}
func TestRateFailureAndCORSFailClosed(t *testing.T) {
	a := &API{Limiter: testLimit{fail: true}, WebOrigin: "https://app.example"}
	w := httptest.NewRecorder()
	a.PublicHandler().ServeHTTP(w, httptest.NewRequest("GET", "/v2/config/auth", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	a.Limiter = testLimit{}
	r := httptest.NewRequest("GET", "/v2/config/auth", nil)
	r.Header.Set("Origin", "https://attacker.example")
	w = httptest.NewRecorder()
	a.PublicHandler().ServeHTTP(w, r)
	if w.Code != 401 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal(w.Code, w.Header())
	}
}

func TestMultipleExactPlatformOrigins(t *testing.T) {
	a := &API{Limiter: testLimit{}, WebOrigins: []string{"https://127.0.0.1:18443", "http://127.0.0.1:18900"}}
	for origin, want := range map[string]int{"https://127.0.0.1:18443": 200, "http://127.0.0.1:18900": 200, "https://127.0.0.1:18444": 401, "https://127.0.0.1.attacker.example": 401} {
		r := httptest.NewRequest("GET", "/v2/config/auth", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		a.PublicHandler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(origin, w.Code)
		}
		if want == 200 && w.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatal("origin missing")
		}
		if want != 200 && w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("rejected origin granted")
		}
	}
}
func TestPublicAPIHasNoInternalTicketOrPhoneDirectoryRoute(t *testing.T) {
	a := &API{Limiter: testLimit{}}
	for _, path := range []string{"/internal/tenancy/tickets/consume", "/v2/auth/phone-tenant", "/v2/users/me"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		a.PublicHandler().ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}
