package platform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

type accessPeer struct {
	fail       bool
	operations []tenancy.AccessOperation
}

func (p *accessPeer) SetAccess(_ context.Context, op tenancy.AccessOperation) error {
	p.operations = append(p.operations, op)
	if p.fail {
		return errors.New("transport failure containing sensitive upstream data")
	}
	return nil
}
func accessAccount(t *testing.T, s *Store, phone string) account {
	t.Helper()
	var id string
	if e := s.pool.QueryRow(t.Context(), `SELECT id FROM platform_accounts WHERE phone=$1`, phone).Scan(&id); e != nil {
		t.Fatal(e)
	}
	tx, e := s.pool.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	a, e := readAccount(t.Context(), tx, id)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func readyAccessRetry(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.pool.Exec(t.Context(), `UPDATE platform_access_jobs SET retry_at=now()`); e != nil {
		t.Fatal(e)
	}
}
func TestPlatformPostgresGlobalAccessLifecycle(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	if _, e := s.Reserve(ctx, Registration{Phone: "13800000701", Name: "access fixture", Password: "AccessPassword123!", Method: "password"}); e != nil {
		t.Fatal(e)
	}
	iw := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for range 2 {
		if _, e := iw.Once(ctx); e != nil {
			t.Fatal(e)
		}
	}
	a := accessAccount(t, s, "13800000701")
	login, e := s.Login(ctx, "13800000701", "AccessPassword123!")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RequestAccess(ctx, a.AccountID, "invalid", "operator", "test block", a.AuthVersion, true, false); e == nil {
		t.Fatal("missing confirmation accepted")
	}
	if _, e = s.RequestAccess(ctx, a.AccountID, "invalid", "operator", "", a.AuthVersion, true, true); e == nil {
		t.Fatal("missing reason accepted")
	}
	if _, e = s.RequestAccess(ctx, a.AccountID, "stale", "operator", "test block", a.AuthVersion+1, true, true); !errors.Is(e, ErrConflict) {
		t.Fatal("stale version", e)
	}
	var wg sync.WaitGroup
	results := make(chan AccessJob, 4)
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := s.RequestAccess(ctx, a.AccountID, "block-1", "operator", "test block", a.AuthVersion, true, true)
			results <- j
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	var job AccessJob
	for j := range results {
		if job.ID != "" && job.ID != j.ID {
			t.Fatal("duplicate jobs")
		}
		job = j
	}
	if _, e = s.RequestAccess(ctx, a.AccountID, "block-1", "operator", "changed reason", a.AuthVersion, true, true); !errors.Is(e, ErrRequestChanged) {
		t.Fatal("changed replay", e)
	}
	if _, e = s.Login(ctx, "13800000701", "AccessPassword123!"); !errors.Is(e, ErrDenied) {
		t.Fatal("blocked login", e)
	}
	if _, e = s.Refresh(ctx, login.RefreshToken); !errors.Is(e, ErrDenied) {
		t.Fatal("blocked refresh", e)
	}
	if _, e = s.Consume(ctx, "a", login.SessionTicket); !errors.Is(e, ErrDenied) {
		t.Fatal("blocked ticket", e)
	}
	if _, e = s.RequestAccess(ctx, a.AccountID, "unblock-pending", "operator", "too early", a.AuthVersion, false, true); !errors.Is(e, ErrAccessPending) {
		t.Fatal("unblock overtook ban", e)
	}
	if _, e = s.transfer(ctx, a.AccountID, "b", "operator", "bypass ban", true, nil); !errors.Is(e, ErrConflict) {
		t.Fatal("transfer bypass", e)
	}
	peer := &accessPeer{fail: true}
	worker := AccessWorker{Store: s, Enterprise: peer}
	if work, e := worker.Once(ctx); e != nil || !work {
		t.Fatal(work, e)
	}
	var state, code string
	if e = s.pool.QueryRow(ctx, `SELECT state,error_code FROM platform_access_jobs WHERE id=$1`, job.ID).Scan(&state, &code); e != nil || state != "applying" || code != "ENTERPRISE_ACCESS_UNCONFIRMED" {
		t.Fatal(state, code, e)
	}
	blocked := accessAccount(t, s, "13800000701")
	if !blocked.GloballyBlocked || blocked.AuthVersion != a.AuthVersion+1 {
		t.Fatal("not safely frozen")
	}
	peer.fail = false
	readyAccessRetry(t, s)
	if _, e = worker.Once(ctx); e != nil {
		t.Fatal(e)
	}
	if len(peer.operations) != 2 || peer.operations[0] != peer.operations[1] {
		t.Fatal("changed uncertain operation")
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	j, e := s.RequestAccess(ctx, a.AccountID, "block-1", "operator", "test block", a.AuthVersion, true, true)
	if e != nil || j.Status != "completed" || j.ID != job.ID {
		t.Fatal("completed replay", j, e)
	}
	if _, e = s.RequestAccess(ctx, a.AccountID, "unblock-1", "operator", "test unblock", blocked.AuthVersion, false, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Login(ctx, "13800000701", "AccessPassword123!"); !errors.Is(e, ErrDenied) {
		t.Fatal("unconfirmed unban allowed login", e)
	}
	if _, e = s.pool.Exec(ctx, `CREATE FUNCTION fail_access_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='account.global_access.completed' THEN RAISE EXCEPTION 'isolated audit outage'; END IF; RETURN NEW; END $$; CREATE TRIGGER access_audit_fault BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION fail_access_completion()`); e != nil {
		t.Fatal(e)
	}
	if _, e = worker.Once(ctx); e != nil {
		t.Fatal(e)
	}
	if a := accessAccount(t, s, "13800000701"); !a.GloballyBlocked || a.State != "blocked" {
		t.Fatal("failed audit still enabled account")
	}
	if _, e = s.pool.Exec(ctx, `DROP TRIGGER access_audit_fault ON platform_audits`); e != nil {
		t.Fatal(e)
	}
	readyAccessRetry(t, s)
	if _, e = worker.Once(ctx); e != nil {
		t.Fatal(e)
	}
	after := accessAccount(t, s, "13800000701")
	if after.GloballyBlocked || after.State != "active" || after.Identity != a.Identity || after.AuthVersion != a.AuthVersion+2 {
		t.Fatal("unban replaced identity or restored stale generation")
	}
	if _, e = s.Login(ctx, "13800000701", "AccessPassword123!"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Refresh(ctx, login.RefreshToken); !errors.Is(e, ErrDenied) {
		t.Fatal("unban resurrected old refresh", e)
	}
	if _, e = s.RequestAccess(ctx, a.AccountID, "stale-ui", "operator", "old page", a.AuthVersion, true, true); !errors.Is(e, ErrConflict) {
		t.Fatal("stale page changed new state", e)
	}
	var n int
	if e = s.pool.QueryRow(ctx, `SELECT count(*) FROM platform_audits WHERE action LIKE 'account.global_access.%'`).Scan(&n); e != nil || n != 4 {
		t.Fatal("duplicate/missing audit", n, e)
	}
}

func TestPlatformPostgresBlockCoordinatesExistingOperations(t *testing.T) {
	for _, kind := range []string{"registration", "transfer", "password"} {
		t.Run(kind, func(t *testing.T) {
			s := isolatedPlatform(t)
			ctx := t.Context()
			phone := "13800000702"
			if _, e := s.Reserve(ctx, Registration{Phone: phone, Name: kind, Password: "AccessPassword123!", Method: "password"}); e != nil {
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
			cp := &credentialPeer{}
			if kind == "transfer" {
				if _, e := s.transfer(ctx, a.AccountID, "b", "operator", "race block", true, nil); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "password" {
				l, e := s.Login(ctx, phone, "AccessPassword123!")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.ChangePassword(ctx, "change", l.RefreshToken, "AccessPassword123!", "AccessPassword123!", cp); e != nil {
					t.Fatal(e)
				}
			}
			before := accessAccount(t, s, phone)
			if _, e := s.RequestAccess(ctx, a.AccountID, "block-race", "operator", "block during work", before.AuthVersion, true, true); e != nil {
				t.Fatal(e)
			}
			peer := &accessPeer{}
			aw := AccessWorker{Store: s, Enterprise: peer}
			if _, e := aw.Once(ctx); e != nil {
				t.Fatal(e)
			}
			if len(peer.operations) != 0 {
				t.Fatal("access operation raced identity/credential work")
			}
			var code string
			if e := s.pool.QueryRow(ctx, `SELECT error_code FROM platform_access_jobs`).Scan(&code); e != nil || code != "WAITING_ACCOUNT_OPERATION" {
				t.Fatal(code, e)
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
			}
			settled := accessAccount(t, s, phone)
			if !settled.GloballyBlocked || settled.State != "blocked" || settled.CredentialsPending {
				t.Fatal("lifecycle unblocked user")
			}
			if _, e := s.Login(ctx, phone, "AccessPassword123!"); !errors.Is(e, ErrDenied) {
				t.Fatal("login race", e)
			}
			readyAccessRetry(t, s)
			if _, e := aw.Once(ctx); e != nil {
				t.Fatal(e)
			}
			if len(peer.operations) != 1 || peer.operations[0].Identity != settled.Identity || peer.operations[0].AuthVersion != settled.AuthVersion+1 {
				t.Fatal("did not freeze final identity")
			}
		})
	}
}

func TestPlatformPostgresAccessHTTPPermissionAndRedaction(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	hash, e := bcrypt.GenerateFromPassword([]byte("PlatformTestPassword!"), 12)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.BootstrapAdmin(ctx, "operator", string(hash)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reserve(ctx, Registration{Phone: "13800000703", Name: "http", Password: "AccessPassword123!", Method: "password"}); e != nil {
		t.Fatal(e)
	}
	a := accessAccount(t, s, "13800000703")
	api := &API{Store: s, Limiter: testLimit{}}
	status, login := adminTestCall(t, api, "POST", "/platform/admin/auth/login", "", `{"username":"operator","password":"PlatformTestPassword!"}`)
	if status != 200 {
		t.Fatal(status)
	}
	token := login["accessToken"].(string)
	path := "/platform/admin/accounts/" + a.AccountID + "/access"
	body, _ := json.Marshal(map[string]any{"requestId": "http-ban", "blocked": true, "expectedAuthVersion": a.AuthVersion, "reason": "test global block", "confirmed": true})
	status, _ = adminTestCall(t, api, "POST", path, "enterprise-token", string(body))
	if status != 401 {
		t.Fatal(status)
	}
	status, result := adminTestCall(t, api, "POST", path, token, string(body))
	if status != 202 || result["status"] != "waiting" {
		t.Fatal(status, result)
	}
	for _, route := range []string{"accounts", "access-jobs"} {
		status, result = adminTestCall(t, api, "GET", "/platform/admin/"+route+"?pageSize=1", token, "")
		if status != 200 || result["total"] != float64(1) {
			t.Fatal(route, status, result)
		}
		data, _ := json.Marshal(result)
		for _, secret := range []string{"password", "lease_id", "AccessPassword123!", token} {
			if strings.Contains(string(data), secret) {
				t.Fatal("private material disclosed", route)
			}
		}
	}
	status, result = adminTestCall(t, api, "GET", "/platform/admin/access-jobs?q=http-ban&state=waiting&tenantId=a", token, "")
	if status != 200 || result["total"] != float64(1) {
		t.Fatal("request lookup", status)
	}
	status, result = adminTestCall(t, api, "GET", "/platform/admin/access-jobs?q=missing", token, "")
	if status != 200 || result["total"] != float64(0) {
		t.Fatal("unknown request", status)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET role='reader'`); e != nil {
		t.Fatal(e)
	}
	status, _ = adminTestCall(t, api, "POST", path, token, string(body))
	if status != 401 {
		t.Fatal("reader wrote", status)
	}
	status, _ = adminTestCall(t, api, "GET", "/platform/admin/access-jobs", token, "")
	if status != 200 {
		t.Fatal("reader unable to inspect", status)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE platform_admin_accounts SET enabled=false`); e != nil {
		t.Fatal(e)
	}
	status, _ = adminTestCall(t, api, "GET", "/platform/admin/access-jobs", token, "")
	if status != 401 {
		t.Fatal("disabled operator", status)
	}
}

func TestPlatformPostgresAccessMigrationDefaults(t *testing.T) {
	s := isolatedPlatform(t)
	ctx := t.Context()
	for _, phone := range []string{"13800000704", "13800000705"} {
		if _, e := s.Reserve(ctx, Registration{Phone: phone, Name: "migration", Password: "AccessPassword123!", Method: "password"}); e != nil {
			t.Fatal(e)
		}
	}
	iw := Worker{Store: s, Enterprise: &fakeEnterprise{}}
	for range 4 {
		if _, e := iw.Once(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if a := accessAccount(t, s, "13800000704"); a.GloballyBlocked {
		t.Fatal("new accounts default blocked")
	}
	if _, e := s.pool.Exec(ctx, `UPDATE platform_accounts SET state='blocked' WHERE phone='13800000704'`); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e := s.Migrate(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if a := accessAccount(t, s, "13800000704"); !a.GloballyBlocked {
		t.Fatal("legacy block lost")
	}
	if a := accessAccount(t, s, "13800000705"); a.GloballyBlocked {
		t.Fatal("migration blocked normal user")
	}
	var version int
	if e := s.pool.QueryRow(ctx, `SELECT max(version) FROM platform_schema_migrations`).Scan(&version); e != nil || version != SchemaVersion {
		t.Fatal(version, e)
	}
}
