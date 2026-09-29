package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

type capturedRecoverySMS struct {
	code  string
	count int
	fail  bool
}

type recoveryLimitFunc func(context.Context, string, int, time.Duration) (bool, error)

func (f recoveryLimitFunc) Allow(ctx context.Context, key string, n int, d time.Duration) (bool, error) {
	return f(ctx, key, n, d)
}

func TestRecoverySMSLimitsBeforeProviderOrDatabase(t *testing.T) {
	for _, scenario := range []string{"ip", "phone", "redis"} {
		t.Run(scenario, func(t *testing.T) {
			sms := &capturedRecoverySMS{}
			var ipChecked, phoneChecked bool
			a := &API{RecoverySMS: sms, Limiter: recoveryLimitFunc(func(_ context.Context, key string, max int, window time.Duration) (bool, error) {
				if strings.HasPrefix(key, "recovery-sms-ip:") {
					if key != "recovery-sms-ip:192.0.2.2" || max != 10 || window != 10*time.Minute {
						t.Fatal("wrong IP budget")
					}
					ipChecked = true
					if scenario == "redis" {
						return false, errors.New("test unavailable")
					}
					return scenario != "ip", nil
				}
				if strings.HasPrefix(key, "recovery-sms:") {
					if key != "recovery-sms:13800000701" || max != 3 || window != 10*time.Minute {
						t.Fatal("wrong phone budget")
					}
					phoneChecked = true
					return false, nil
				}
				return true, nil
			})}
			body, _ := json.Marshal(map[string]string{"requestId": "limits", "phone": "13800000701", "queryToken": recoverySecret(t)})
			r := httptest.NewRequest("POST", "/v2/auth/password-reset/code", strings.NewReader(string(body)))
			r.RemoteAddr = "192.0.2.2:1234"
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Forwarded-For", "198.51.100.4")
			w := httptest.NewRecorder()
			a.PublicHandler().ServeHTTP(w, r)
			want := 429
			if scenario == "redis" {
				want = 503
			}
			if w.Code != want || !ipChecked || (scenario == "phone" && !phoneChecked) || sms.count != 0 {
				t.Fatal("rate limiter bypass", w.Code)
			}
		})
	}
}

func (s *capturedRecoverySMS) DeliverRecovery(_ context.Context, phone, code, id string) error {
	s.code = code
	s.count++
	if s.fail {
		return ErrUnavailable
	}
	return nil
}
func recoverySecret(t *testing.T) string {
	t.Helper()
	v, e := tenancy.Secret()
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func recoveryAccount(t *testing.T, s *Store) string {
	t.Helper()
	ctx := t.Context()
	if _, e := s.Reserve(ctx, Registration{Phone: "13800000701", Name: "recovery", Password: "OriginalPassword123!", Method: "password"}); e != nil {
		t.Fatal(e)
	}
	w := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for range 2 {
		if _, e := w.Once(ctx); e != nil {
			t.Fatal(e)
		}
	}
	var id string
	if e := s.pool.QueryRow(ctx, `SELECT id FROM platform_accounts WHERE phone='13800000701'`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	return id
}

type recoveryPolicyFunc func(context.Context, tenancy.Identity, int) error

func (f recoveryPolicyFunc) CheckPassword(ctx context.Context, identity tenancy.Identity, n int) error {
	return f(ctx, identity, n)
}

func TestPlatformPostgresRecoveryRechecksAfterPolicyRPC(t *testing.T) {
	for _, mutation := range []string{"auth_version=auth_version+1", "assignment_version=assignment_version+1", "state='blocked'"} {
		t.Run(mutation, func(t *testing.T) {
			s := isolatedPlatform(t)
			ctx := t.Context()
			accountID := recoveryAccount(t, s)
			sms := &capturedRecoverySMS{}
			token := recoverySecret(t)
			if e := s.RequestPasswordRecovery(ctx, "mid-rpc", "13800000701", token, sms); e != nil {
				t.Fatal(e)
			}
			policy := recoveryPolicyFunc(func(ctx context.Context, _ tenancy.Identity, _ int) error {
				// Explicit test-only mutation simulates a concurrent lifecycle task
				// while the platform lock is released for the enterprise policy RPC.
				_, e := s.pool.Exec(ctx, `UPDATE platform_accounts SET `+mutation+` WHERE id=$1`, accountID)
				return e
			})
			if _, e := s.RecoverPassword(ctx, "mid-rpc", token, sms.code, "RecoveredPassword123!", policy); !errors.Is(e, ErrDenied) {
				t.Fatal("stale authorization committed", e)
			}
			var count int
			var consumed bool
			if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_credential_jobs`).Scan(&count); e != nil || count != 0 {
				t.Fatal("stale task persisted", e)
			}
			if e := s.pool.QueryRow(ctx, `SELECT consumed_at IS NOT NULL FROM platform_password_recovery WHERE id='mid-rpc'`).Scan(&consumed); e != nil || consumed {
				t.Fatal("stale proof consumed", e)
			}
		})
	}
}
func TestPlatformPostgresPasswordRecovery(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	accountID := recoveryAccount(t, s)
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var schema int
	if e := s.pool.QueryRow(ctx, `SELECT max(version) FROM platform_schema_migrations`).Scan(&schema); e != nil || schema != SchemaVersion {
		t.Fatal("schema", schema, e)
	}
	sms := &capturedRecoverySMS{}
	peer := &credentialPeer{}
	token := recoverySecret(t)
	if e := s.RequestPasswordRecovery(ctx, "recovery-1", "13800000701", token, sms); e != nil {
		t.Fatal(e)
	}
	if len(sms.code) != 6 {
		t.Fatal("code format")
	}
	code := sms.code
	if e := s.RequestPasswordRecovery(ctx, "recovery-1", "13800000701", token, sms); e != nil || sms.count != 1 {
		t.Fatal("duplicate SMS", e)
	}
	if e := s.RequestPasswordRecovery(ctx, "recovery-1", "13800000702", token, sms); !errors.Is(e, ErrRequestChanged) {
		t.Fatal("changed phone", e)
	}
	if _, e := s.Login(ctx, "13800000701", code); !errors.Is(e, ErrDenied) {
		t.Fatal("recovery code became login password", e)
	}
	if _, e := s.Refresh(ctx, token); !errors.Is(e, ErrDenied) {
		t.Fatal("recovery capability became refresh token", e)
	}
	if _, e := s.ChangePassword(ctx, "recovery-1", token, code, "NextPassword123!", peer); !errors.Is(e, ErrDenied) {
		t.Fatal("cross-purpose capability", e)
	}
	if _, e := s.RecoverPassword(ctx, "recovery-1", token, code, "Eight123", peer); e == nil {
		t.Fatal("enterprise policy bypass")
	}
	var consumed bool
	if e := s.pool.QueryRow(ctx, `SELECT consumed_at IS NOT NULL FROM platform_password_recovery WHERE id='recovery-1'`).Scan(&consumed); e != nil || consumed {
		t.Fatal("policy rejection consumed challenge", e)
	}
	var wg sync.WaitGroup
	results := make(chan CredentialJob, 4)
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := s.RecoverPassword(ctx, "recovery-1", token, code, "RecoveredPassword123!", peer)
			results <- j
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for e := range errs {
		if e != nil {
			t.Fatal("concurrent recovery", e)
		}
	}
	var job CredentialJob
	for j := range results {
		if job.ID != "" && job.ID != j.ID {
			t.Fatal("duplicate reset")
		}
		job = j
	}
	if _, e := s.RecoverPassword(ctx, "recovery-1", token, code, "DifferentPassword123!", peer); !errors.Is(e, ErrRequestChanged) {
		t.Fatal("changed replay", e)
	}
	if j, e := s.RecoveryStatus(ctx, "recovery-1", token); e != nil || j.Status != "pending" {
		t.Fatal("lost acceptance cannot resume", e)
	}
	if _, e := s.RecoveryStatus(ctx, "recovery-1", recoverySecret(t)); !errors.Is(e, ErrDenied) {
		t.Fatal("foreign capability", e)
	}
	if _, e := s.Login(ctx, "13800000701", "RecoveredPassword123!"); !errors.Is(e, ErrDenied) {
		t.Fatal("premature login", e)
	}
	if _, e := (CredentialWorker{Store: s, Enterprise: peer}).Once(ctx); e != nil {
		t.Fatal(e)
	}
	if j, e := s.RecoveryStatus(ctx, "recovery-1", token); e != nil || j.Status != "completed" {
		t.Fatal("completion", e)
	}
	if _, e := s.Login(ctx, "13800000701", "OriginalPassword123!"); !errors.Is(e, ErrDenied) {
		t.Fatal("old password accepted", e)
	}
	if _, e := s.Login(ctx, "13800000701", "RecoveredPassword123!"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RecoverPassword(ctx, "recovery-1", token, "unused", "RecoveredPassword123!", peer); e != nil {
		t.Fatal("exact completed replay", e)
	}
	var count int
	if e := s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_credential_jobs WHERE account_id=$1`, accountID).Scan(&count); e != nil || count != 1 {
		t.Fatal("non-atomic task", e)
	}
	var leak bool
	if e := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_audits WHERE metadata::text LIKE '%Password123%' OR metadata::text LIKE '%code%' OR metadata::text LIKE '%token%')`).Scan(&leak); e != nil || leak {
		t.Fatal("audit secret", e)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE platform_password_recovery SET created_at=now()-interval '25 hours' WHERE id='recovery-1'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RecoveryStatus(ctx, "recovery-1", token); !errors.Is(e, ErrDenied) {
		t.Fatal("expired query", e)
	}
}

func TestPlatformPostgresRecoveryLimitsAndAccountRaces(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	id := recoveryAccount(t, s)
	peer := &credentialPeer{}
	for _, scenario := range []string{"attempts", "expired", "blocked", "assignment", "authversion", "unknown", "delivery"} {
		t.Run(scenario, func(t *testing.T) {
			sms := &capturedRecoverySMS{}
			token := recoverySecret(t)
			phone := "13800000701"
			if scenario == "unknown" {
				phone = "13800000999"
			}
			if scenario == "delivery" {
				sms.fail = true
			}
			e := s.RequestPasswordRecovery(ctx, scenario, phone, token, sms)
			if scenario == "delivery" {
				if !errors.Is(e, ErrUnavailable) {
					t.Fatal("lost delivery", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if j, e := s.RecoveryStatus(ctx, scenario, token); e != nil || j.Status != "unconfirmed" {
				t.Fatal("unknown means unconfirmed", e)
			}
			if scenario == "delivery" { // It may have reached the phone despite a lost HTTP ack.
				sms.fail = false
				if e = s.RequestPasswordRecovery(ctx, scenario, phone, token, sms); !errors.Is(e, ErrUnavailable) || sms.count != 1 {
					t.Fatal("uncertain delivery resent", e)
				}
				if j, e := s.RecoverPassword(ctx, scenario, token, sms.code, "RecoveredPassword123!", peer); e != nil || j.Status != "pending" {
					t.Fatal("valid delivered proof lost with provider ack", e)
				}
				return
			}
			switch scenario {
			case "attempts":
				wrong := "000000"
				if sms.code == wrong {
					wrong = "999999"
				}
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() { defer wg.Done(); s.RecoverPassword(ctx, scenario, token, wrong, "RecoveredPassword123!", peer) }()
				}
				wg.Wait()
				var n int
				s.pool.QueryRow(ctx, `SELECT attempts FROM platform_password_recovery WHERE id=$1`, scenario).Scan(&n)
				if n != 5 {
					t.Fatal("attempt bound", n)
				}
			case "expired":
				_, e = s.pool.Exec(ctx, `UPDATE platform_password_recovery SET expires_at=now() WHERE id=$1`, scenario)
			case "blocked":
				_, e = s.pool.Exec(ctx, `UPDATE platform_accounts SET state='blocked' WHERE id=$1`, id)
			case "assignment":
				_, e = s.pool.Exec(ctx, `UPDATE platform_accounts SET assignment_version=assignment_version+1 WHERE id=$1`, id)
			case "authversion":
				_, e = s.pool.Exec(ctx, `UPDATE platform_accounts SET auth_version=auth_version+1 WHERE id=$1`, id)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.RecoverPassword(ctx, scenario, token, sms.code, "RecoveredPassword123!", peer); !errors.Is(e, ErrDenied) {
				t.Fatal("unsafe recovery", e)
			}
			if scenario == "blocked" {
				if _, e = s.pool.Exec(ctx, `UPDATE platform_accounts SET state='active' WHERE id=$1`, id); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestRecoverySMSTransportIsPurposeBoundAndNeverRedirects(t *testing.T) {
	var delivered map[string]string
	var forwarded bool
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true; w.WriteHeader(204) }))
	defer other.Close()
	redirect := false
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirect {
			w.Header().Set("Location", other.URL)
			w.WriteHeader(307)
			return
		}
		if r.URL.Path != "/sms/recovery" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) || r.Header.Get("Idempotency-Key") != "reset-id" {
			t.Error("provider identity missing")
		}
		if e := json.NewDecoder(r.Body).Decode(&delivered); e != nil {
			t.Error(e)
		}
		w.WriteHeader(204)
	}))
	defer provider.Close()
	sms, e := NewWebhookRecoverySMS(provider.URL+"/sms/recovery", strings.Repeat("s", 32))
	if e != nil {
		t.Fatal(e)
	}
	sms.client.Transport = provider.Client().Transport
	if e = sms.DeliverRecovery(t.Context(), "13800000701", "123987", "reset-id"); e != nil {
		t.Fatal(e)
	}
	if delivered["purpose"] != "password_reset" || len(delivered) != 4 || delivered["code"] != "123987" {
		t.Fatal("purpose or payload")
	}
	redirect = true
	if e = sms.DeliverRecovery(t.Context(), "13800000701", "123987", "reset-id"); !errors.Is(e, ErrUnavailable) || forwarded {
		t.Fatal("redirect leaked")
	}
	for _, endpoint := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?secret=1", "https://example.com#fragment", "https://example.com?", "https:/relative"} {
		if _, e = NewWebhookRecoverySMS(endpoint, strings.Repeat("s", 32)); e == nil {
			t.Fatal("unsafe SMS endpoint accepted")
		}
	}
}
