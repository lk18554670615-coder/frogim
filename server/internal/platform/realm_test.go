package platform

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"sync"
	"testing"
)

type realmPeer struct {
	fail, badAck, badHealth bool
	remaining               int
	ops                     []tenancy.RealmOperation
}

func (p *realmPeer) SetRealm(_ context.Context, op tenancy.RealmOperation) (tenancy.RealmAck, error) {
	p.ops = append(p.ops, op)
	if p.fail {
		return tenancy.RealmAck{}, errors.New("upstream private failure")
	}
	ack := tenancy.RealmAck{RealmOperation: op, State: "completed"}
	if p.remaining > 0 {
		ack.State = "pending"
		ack.Remaining = p.remaining
	}
	if p.badAck {
		ack.Version++
	}
	return ack, nil
}
func (p *realmPeer) CheckReadiness(_ context.Context, id, nonce string) (tenancy.Readiness, error) {
	return tenancy.Readiness{Nonce: nonce, TenantID: id, HTTPBaseURL: "https://" + id + ".example", SchemaVersion: 77, Checks: map[string]bool{"databaseBinding": true, "databaseAndCache": true, "im": true, "media": !p.badHealth, "calls": true}}, nil
}
func realmRetry(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.pool.Exec(t.Context(), `UPDATE platform_realm_jobs SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
}
func TestPlatformPostgresRealmLifecycle(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	for _, tenant := range []string{"a", "b"} {
		phone := "13800000801"
		if tenant == "b" {
			phone = "13800000802"
		}
		if _, e := s.Reserve(ctx, Registration{Phone: phone, Name: "realm fixture", Password: "RealmPassword123!", Method: "admin", ForcedTenantID: tenant, Actor: "operator"}); e != nil {
			t.Fatal(e)
		}
	}
	iw := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for range 4 {
		if _, e := iw.Once(ctx); e != nil {
			t.Fatal(e)
		}
	}
	a := accessAccount(t, s, "13800000801")
	login, e := s.Login(ctx, a.Phone, "RealmPassword123!")
	if e != nil {
		t.Fatal(e)
	}
	sms := &capturedRecoverySMS{}
	capability := recoverySecret(t)
	if e = s.RequestPasswordRecovery(ctx, "realm-recovery", a.Phone, capability, sms); e != nil {
		t.Fatal(e)
	}
	for _, args := range []struct {
		reason             string
		v                  int64
		enabled, confirmed bool
	}{{"test", 1, false, false}, {"", 1, false, true}, {"test", 2, false, true}, {"test", 1, true, true}} {
		if _, e = s.RequestRealm(ctx, "a", "invalid", "operator", args.reason, args.v, args.enabled, args.confirmed); e == nil {
			t.Fatal("invalid transition accepted")
		}
	}
	var wg sync.WaitGroup
	jobs := make(chan RealmJob, 4)
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := s.RequestRealm(ctx, "a", "pause-1", "operator", "test pause", 1, false, true)
			jobs <- j
			errs <- e
		}()
	}
	wg.Wait()
	close(jobs)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for j := range jobs {
		if id != "" && id != j.ID {
			t.Fatal("duplicate job")
		}
		id = j.ID
	}
	if _, e = s.RequestRealm(ctx, "a", "pause-1", "operator", "different reason", 1, false, true); !errors.Is(e, ErrRequestChanged) {
		t.Fatal(e)
	}
	if _, e = s.RequestRealm(ctx, "a", "early-resume", "operator", "test resume", 2, true, true); e == nil {
		t.Fatal("overtook pause")
	}
	denied := func() {
		t.Helper()
		if _, e := s.Login(ctx, a.Phone, "RealmPassword123!"); !errors.Is(e, ErrDenied) {
			t.Fatal("paused login", e)
		}
		if _, e := s.Refresh(ctx, login.RefreshToken); !errors.Is(e, ErrDenied) {
			t.Fatal("paused refresh", e)
		}
		if _, e := s.Consume(ctx, "a", login.SessionTicket); !errors.Is(e, ErrDenied) {
			t.Fatal("paused ticket", e)
		}
	}
	denied()
	if _, e = s.Login(ctx, "13800000802", "RealmPassword123!"); e != nil {
		t.Fatal("other enterprise affected", e)
	}
	if _, e = s.Reserve(ctx, Registration{Phone: "13800000803", Name: "blocked signup", Password: "RealmPassword123!", Method: "password"}); !errors.Is(e, ErrDenied) {
		t.Fatal("paused signup", e)
	}
	if _, e = s.transfer(ctx, a.AccountID, "b", "operator", "paused source", true, nil); !errors.Is(e, ErrDenied) {
		t.Fatal("paused source transfer", e)
	}
	p := &realmPeer{fail: true}
	rw := RealmWorker{Store: s, Enterprise: p}
	run := func() {
		t.Helper()
		realmRetry(t, s)
		if worked, e := rw.Once(ctx); e != nil || !worked {
			t.Fatal(worked, e)
		}
	}
	run()
	denied()
	p.fail = false
	p.badAck = true
	run()
	denied()
	p.badAck = false
	p.remaining = 7
	run()
	var state, code string
	var remaining int
	if e = s.pool.QueryRow(ctx, `SELECT state,error_code,remaining FROM platform_realm_jobs WHERE id=$1`, id).Scan(&state, &code, &remaining); e != nil || state != "pending" || remaining != 7 || code != "" {
		t.Fatal("partial batch", state, code, remaining, e)
	}
	p.remaining = 0
	run()
	denied()
	if _, e = s.RequestRealm(ctx, "a", "resume-1", "operator", "test resume", 2, true, true); e != nil {
		t.Fatal(e)
	}
	p.badHealth = true
	before := len(p.ops)
	run()
	if len(p.ops) != before {
		t.Fatal("unready enterprise resumed")
	}
	denied()
	p.badHealth = false
	if _, e = s.pool.Exec(ctx, `CREATE FUNCTION fail_realm_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='tenant.access.completed' THEN RAISE EXCEPTION 'isolated audit outage'; END IF; RETURN NEW; END $$; CREATE TRIGGER realm_audit_fault BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION fail_realm_completion()`); e != nil {
		t.Fatal(e)
	}
	run()
	denied()
	if _, e = s.pool.Exec(ctx, `DROP TRIGGER realm_audit_fault ON platform_audits`); e != nil {
		t.Fatal(e)
	}
	run()
	// Old refresh, unconsumed ticket, password authorization and recovery stay dead.
	if _, e = s.Refresh(ctx, login.RefreshToken); !errors.Is(e, ErrDenied) {
		t.Fatal("old refresh revived", e)
	}
	if _, e = s.Consume(ctx, "a", login.SessionTicket); !errors.Is(e, ErrDenied) {
		t.Fatal("old ticket revived", e)
	}
	if _, e = s.ChangePassword(ctx, "old-change", login.RefreshToken, "RealmPassword123!", "NextPassword123!", &credentialPeer{}); !errors.Is(e, ErrDenied) {
		t.Fatal("old password capability revived", e)
	}
	if _, e = s.RecoverPassword(ctx, "realm-recovery", capability, sms.code, "NextPassword123!", &credentialPeer{}); !errors.Is(e, ErrDenied) {
		t.Fatal("old recovery capability revived", e)
	}
	fresh, e := s.Login(ctx, a.Phone, "RealmPassword123!")
	if e != nil {
		t.Fatal(e)
	}
	grant, e := s.Consume(ctx, "a", fresh.SessionTicket)
	if e != nil || grant.RealmVersion != 3 || grant.Identity != a.Identity || grant.AuthVersion != a.AuthVersion {
		t.Fatal("resume changed identity", e)
	}
	if j, e := s.RequestRealm(ctx, "a", "pause-1", "operator", "test pause", 1, false, true); e != nil || j.Status != "completed" {
		t.Fatal("lost ACK replay", e)
	}
	for range 2 {
		if e = s.Migrate(ctx); e != nil {
			t.Fatal(e)
		}
	}
	var v int64
	if e = s.pool.QueryRow(ctx, `SELECT status,access_version FROM platform_tenants WHERE id='a'`).Scan(&state, &v); e != nil || state != "active" || v != 3 {
		t.Fatal("migration or replay changed realm", e)
	}
	var count int
	if e = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action IN ('tenant.access.requested','tenant.access.completed')`).Scan(&count); e != nil || count != 4 {
		t.Fatal("audit duplicated", count, e)
	}
}

func TestPlatformPostgresRealmCoordinatesAcceptedWork(t *testing.T) {
	for _, kind := range []string{"registration", "transfer", "password"} {
		t.Run(kind, func(t *testing.T) {
			s := isolatedPlatform(t)
			ctx := t.Context()
			phone := "13800000807"
			if _, e := s.Reserve(ctx, Registration{Phone: phone, Name: kind, Password: "RealmPassword123!", Method: "password"}); e != nil {
				t.Fatal(e)
			}
			iw := Worker{Store: s, Enterprise: &fakeEnterprise{}}
			if kind != "registration" {
				for range 2 {
					if _, e := iw.Once(ctx); e != nil {
						t.Fatal(e)
					}
				}
			}
			a := accessAccount(t, s, phone)
			tenant := "a"
			password := "RealmPassword123!"
			if kind == "transfer" {
				if _, e := s.transfer(ctx, a.AccountID, "b", "operator", "test transfer", true, nil); e != nil {
					t.Fatal(e)
				}
				tenant = "b"
			}
			cp := &credentialPeer{}
			if kind == "password" {
				session, e := s.Login(ctx, phone, password)
				if e != nil {
					t.Fatal(e)
				}
				password = "NewRealmPassword123!"
				if _, e = s.ChangePassword(ctx, "pending-password", session.RefreshToken, "RealmPassword123!", password, cp); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.RequestRealm(ctx, tenant, "pause", "operator", "pause during work", 1, false, true); e != nil {
				t.Fatal(e)
			}
			rw := RealmWorker{Store: s, Enterprise: &realmPeer{}}
			if _, e := rw.Once(ctx); e != nil {
				t.Fatal(e)
			}
			if kind == "password" {
				if _, e := (CredentialWorker{Store: s, Enterprise: cp}).Once(ctx); e != nil {
					t.Fatal(e)
				}
			} else {
				steps := 2
				if kind == "transfer" {
					steps = 3
				}
				for range steps {
					if _, e := iw.Once(ctx); e != nil {
						t.Fatal(e)
					}
				}
				var step string
				if e := s.pool.QueryRow(ctx, `SELECT step FROM platform_jobs WHERE account_id=$1 AND step<>'completed'`, a.AccountID).Scan(&step); e != nil || step != "activate" {
					t.Fatal("paused target activated", step, e)
				}
			}
			if _, e := s.Login(ctx, phone, password); !errors.Is(e, ErrDenied) {
				t.Fatal("accepted work bypassed realm", e)
			}
			if _, e := s.RequestRealm(ctx, tenant, "resume", "operator", "resume accepted work", 2, true, true); e != nil {
				t.Fatal(e)
			}
			if _, e := rw.Once(ctx); e != nil {
				t.Fatal(e)
			}
			if kind != "password" {
				if _, e := s.pool.Exec(ctx, `UPDATE platform_jobs SET retry_at=now()`); e != nil {
					t.Fatal(e)
				}
				if _, e := iw.Once(ctx); e != nil {
					t.Fatal(e)
				}
			}
			result, e := s.Login(ctx, phone, password)
			if e != nil || result.TenantContext.TenantID != tenant {
				t.Fatal("work did not resume", e)
			}
		})
	}
}

func TestPlatformPostgresRealmHTTPPermissions(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	hash, e := bcrypt.GenerateFromPassword([]byte("PlatformTestPassword!"), 12)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.BootstrapAdmin(ctx, "operator", string(hash)); e != nil {
		t.Fatal(e)
	}
	api := &API{Store: s, Limiter: testLimit{}}
	status, result := adminTestCall(t, api, "POST", "/platform/admin/auth/login", "", `{"username":"operator","password":"PlatformTestPassword!"}`)
	if status != 200 {
		t.Fatal(status)
	}
	token := result["accessToken"].(string)
	path := "/platform/admin/tenants/a/access"
	body := `{"requestId":"pause-http","reason":"test enterprise pause","confirmed":true,"enabled":false,"expectedAccessVersion":1}`
	if status, _ = adminTestCall(t, api, "POST", path, "enterprise-token", body); status != 401 {
		t.Fatal(status)
	}
	if status, _ = adminTestCall(t, api, "POST", path, token, `{"requestId":"invalid"}`); status != 400 {
		t.Fatal(status)
	}
	if status, result = adminTestCall(t, api, "POST", path, token, body); status != 202 || result["status"] != "pending" {
		t.Fatal(status, result)
	}
	status, result = adminTestCall(t, api, "GET", "/platform/admin/realm-jobs?tenantId=a&state=pending&q=pause-http", token, "")
	if status != 200 || result["total"] != float64(1) {
		t.Fatal(status, result)
	}
	raw, _ := json.Marshal(result)
	for _, secret := range []string{token, "password", "lease_id"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret disclosed")
		}
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='reader'`); e != nil {
		t.Fatal(e)
	}
	if status, _ = adminTestCall(t, api, "POST", path, token, body); status != 401 {
		t.Fatal("reader wrote", status)
	}
	if status, _ = adminTestCall(t, api, "GET", "/platform/admin/realm-jobs", token, ""); status != 200 {
		t.Fatal("reader cannot inspect", status)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET enabled=false`); e != nil {
		t.Fatal(e)
	}
	if status, _ = adminTestCall(t, api, "POST", path, token, body); status != 401 {
		t.Fatal("disabled operator wrote", status)
	}
}
