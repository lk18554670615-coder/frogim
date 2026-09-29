package platform

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixedCodeRequest(a *API, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.PublicHandler().ServeHTTP(w, r)
	return w
}

func TestFixedVerificationValidationAndPolicy(t *testing.T) {
	for _, code := range []string{"", "12345", "abcdef", "123456\n"} {
		if _, err := NewFixedOTP(code); err == nil {
			t.Fatal("invalid fixed code accepted")
		}
	}
	fixed, err := NewFixedOTP("123456")
	if err != nil {
		t.Fatal(err)
	}
	a := &API{Limiter: testLimit{}, OTP: fixed, RecoverySMS: fixed}
	w := httptest.NewRecorder()
	a.PublicHandler().ServeHTTP(w, httptest.NewRequest("GET", "/v2/config/auth", nil))
	var policy map[string]any
	if json.Unmarshal(w.Body.Bytes(), &policy) != nil {
		t.Fatal("invalid policy")
	}
	for _, key := range []string{"registrationEnabled", "otpLoginEnabled", "passwordResetEnabled"} {
		if policy[key] != true {
			t.Fatal(key, policy)
		}
	}
	for _, path := range []string{"/v2/auth/register", "/v2/auth/login"} {
		body := `{"phone":"13812345678","code":"654321","name":"Test"}`
		if path == "/v2/auth/register" {
			body = `{"phone":"13812345678","code":"654321","password":"Password123!","name":"Test"}`
		}
		if w := fixedCodeRequest(a, path, body); w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	if fixed.Verify(t.Context(), "13812345678", "123456") != nil || fixed.DeliverRecovery(t.Context(), "13812345678", "123456", "test") != nil {
		t.Fatal("fixed code rejected")
	}
	if fixed.DeliverRecovery(t.Context(), "13812345678", "654321", "test") == nil {
		t.Fatal("random recovery code accepted")
	}
	a.Limiter = testLimit{fail: true}
	if w := fixedCodeRequest(a, "/v2/auth/login", `{"phone":"13812345678","code":"123456"}`); w.Code != 503 {
		t.Fatal("rate limiter bypass", w.Code)
	}
}

func TestPlatformPostgresFixedVerificationLifecycle(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	fixed, _ := NewFixedOTP("123456")
	a := &API{Store: s, Limiter: testLimit{}, OTP: fixed, RecoverySMS: fixed}
	body := `{"phone":"13812345678","code":"123456","password":"Password123!","name":"Test"}`
	w := fixedCodeRequest(a, "/v2/auth/register", body)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	worker := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for range 2 {
		if _, err := worker.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Login(ctx, "13812345678", "Password123!"); err != nil {
		t.Fatal(err)
	}
	if w := fixedCodeRequest(a, "/v2/auth/login", `{"phone":"13812345678","code":"123456"}`); w.Code != 200 {
		t.Fatal("OTP login", w.Code)
	}
	if w := fixedCodeRequest(a, "/v2/auth/register", strings.Replace(body, "Password123!", "Different123!", 1)); w.Code != 409 {
		t.Fatal("duplicate registration", w.Code)
	}
	if _, err := s.Login(ctx, "13812345678", "Password123!"); err != nil {
		t.Fatal("original password changed", err)
	}
	token := recoverySecret(t)
	if err := s.RequestPasswordRecovery(ctx, "fixed-recovery", "13812345678", token, fixed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.verifyRecovery(ctx, "fixed-recovery", token, "654321"); err == nil {
		t.Fatal("wrong recovery code")
	}
	peer := &credentialPeer{}
	if _, err := s.RecoverPassword(ctx, "fixed-recovery", token, "123456", "RecoveredPassword123!", peer); err != nil {
		t.Fatal(err)
	}
	if _, err := (CredentialWorker{Store: s, Enterprise: peer}).Once(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "13812345678", "RecoveredPassword123!"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "13812345678", "Password123!"); err == nil {
		t.Fatal("old password retained")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE platform_accounts SET state='blocked' WHERE phone='13812345678'`); err != nil {
		t.Fatal(err)
	}
	if w := fixedCodeRequest(a, "/v2/auth/login", `{"phone":"13812345678","code":"123456"}`); w.Code != 401 {
		t.Fatal("blocked account login", w.Code)
	}
	if w := fixedCodeRequest(a, "/v2/auth/register", body); w.Code != 409 {
		t.Fatal("blocked identity re-registered", w.Code)
	}
}
