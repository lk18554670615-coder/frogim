package legacyimport

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// The database fixture supplies suspension acknowledgements. It does not claim
// to disconnect a real IM/RTC connection; the fake below tests durable ordering.
func importFixture(t *testing.T) (pgFixture, *store.Postgres, *platform.Store) {
	t.Helper()
	f := setupPG(t, false)
	execFixture(t, f.source, `UPDATE im_tenant_identity SET access_version=2,access_enabled=false;
INSERT INTO im_tenant_realm_operations(operation_id,access_version,enabled,state) VALUES('fixture-pause',2,false,'completed')`)
	execFixture(t, f.target, `UPDATE platform_tenants SET status='suspended',access_version=2 WHERE id='default';
INSERT INTO platform_realm_jobs(id,request_id,actor_id,tenant_id,enabled,expected_version,access_version,reason,state) VALUES('fixture-pause','fixture-pause','fixture','default',false,1,2,'isolated test suspension','completed')`)
	s, e := store.NewPostgresWithOptions(t.Context(), f.sourceURL, store.PostgresOptions{TenantID: "default"})
	if e != nil {
		t.Fatal("open source fixture failed")
	}
	t.Cleanup(s.Close)
	p, e := platform.Open(t.Context(), f.targetURL)
	if e != nil {
		t.Fatal("open target fixture failed")
	}
	t.Cleanup(p.Close)
	return f, s, p
}
func addLegacy(t *testing.T, f pgFixture, id, phone string, banned bool, until *time.Time) string {
	t.Helper()
	h, e := bcrypt.GenerateFromPassword([]byte("FixturePassword123!"), 4)
	if e != nil {
		t.Fatal(e)
	}
	execFixture(t, f.source, `INSERT INTO im_users(id,phone,name,password_hash,banned,banned_until,created_at) VALUES($1,$2,'historical fixture',$3,$4,$5,now())`, id, phone, string(h), banned, until)
	return string(h)
}
func importRequest(t *testing.T, f pgFixture, id string) ImportRequest {
	t.Helper()
	r, e := f.check(t, "default")
	if e != nil || !r.DataChecksPassed {
		t.Fatal("fixture preflight failed")
	}
	return ImportRequest{ID: id, TenantID: "default", Actor: "operator", Reason: "isolated legacy import test", ExpectedFingerprint: r.Fingerprint, Confirmed: true}
}

type dbRevoker struct {
	store         *store.Postgres
	fail, loseAck bool
	mu            sync.Mutex
	disconnects   int
}

func (r *dbRevoker) RevokeCredentials(ctx context.Context, op tenancy.CredentialOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.store.WithTenantSessionFence(ctx, op.Identity.LocalUserID, func() error {
		done, e := r.store.BeginTenantCredentialRevocation(ctx, op)
		if e != nil || done {
			return e
		}
		if r.fail {
			return errors.New("private remote body must not be persisted")
		}
		r.disconnects++
		if e = r.store.FinishTenantCredentialRevocation(ctx, op); e != nil {
			return e
		}
		if r.loseAck {
			r.loseAck = false
			return errors.New("ack lost")
		}
		return nil
	})
}
func retryImport(t *testing.T, f pgFixture) {
	t.Helper()
	execFixture(t, f.target, `UPDATE platform_legacy_import_items SET retry_at=now(),lease_until=NULL`)
}

func TestPostgresLegacyImportBackupMaintenanceExclusion(t *testing.T) {
	for _, state := range []string{"pending", "unconfirmed", "completed", "failed", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			f, _, _ := importFixture(t)
			addLegacy(t, f, "original-user", "19900000123", false, nil)
			r := importRequest(t, f, "import-after-backup")
			execFixture(t, f.target, `INSERT INTO platform_admin_accounts(id,username,password_hash,role) VALUES('fixture-operator','fixture-operator','unused-fixture-hash','operator');
 INSERT INTO platform_servers(id,tenant_id,display_name,host_fingerprint,isolation_mode,runtime,revision,verified_at) VALUES('fixture-server','default','fixture',repeat('a',64),'local_preview','linux/amd64',1,now());`)
			execFixture(t, f.target, `INSERT INTO platform_backup_jobs(id,actor_id,request_id,tenant_id,server_id,input,operation,release,binding,state) VALUES('fixture-backup','fixture-operator','fixture-request','default','fixture-server','{}','{}','{}','{}',$1)`, state)
			_, e := StartImport(t.Context(), f.source, f.target, r)
			if state == "pending" || state == "unconfirmed" {
				if !errors.Is(e, ErrMaintenance) {
					t.Fatal("backup exclusion bypassed", e)
				}
				var count int
				if err := f.target.QueryRow(t.Context(), `SELECT count(*) FROM platform_accounts`).Scan(&count); err != nil || count != 0 {
					t.Fatal("partial reservation", count, err)
				}
			} else if e != nil {
				t.Fatal("verified terminal backup blocked import", e)
			}
		})
	}
}

func TestPostgresLegacyImportPreservesIDsHashAndBusinessData(t *testing.T) {
	f, s, p := importFixture(t)
	const uid = "original-user"
	hash := addLegacy(t, f, uid, "+8619900000123", false, nil)
	execFixture(t, f.source, `INSERT INTO im_refresh_sessions(id,user_id,token_hash,expires_at,session_id,device_kind) VALUES('old-refresh',$1,'\x01',now()+interval '1 day','old-session','web');
`, uid)
	execFixture(t, f.source, `INSERT INTO im_devices(id,user_id,platform,provider,push_token) VALUES('old-device',$1,'android','getui','fixture-push')`, uid)
	execFixture(t, f.source, `CREATE TABLE fixture_business_marker(user_id text NOT NULL REFERENCES im_users(id),body text NOT NULL);INSERT INTO fixture_business_marker VALUES('original-user','historical-message-sentinel')`)
	before := tableSnapshot(t, f.source, "fixture_business_marker")
	r := importRequest(t, f, "import-batch")
	b, e := StartImport(t.Context(), f.source, f.target, r)
	if e != nil || b.Total != 1 || b.State != "running" {
		t.Fatal("reservation failed", e)
	}
	var actualHash, actualUID, state, account string
	if f.target.QueryRow(t.Context(), `SELECT password_hash,local_user_id,state,id FROM platform_accounts`).Scan(&actualHash, &actualUID, &state, &account) != nil || actualHash != hash || actualUID != uid || state != "provisioning" {
		t.Fatal("hash/identity reservation changed")
	}
	if _, e = p.Login(t.Context(), "19900000123", "FixturePassword123!"); !errors.Is(e, platform.ErrDenied) {
		t.Fatal("reserved account could login")
	}
	replay, e := StartImport(t.Context(), f.source, f.target, r)
	if e != nil || replay.ID != b.ID || replay.Total != 1 {
		t.Fatal("reservation replay failed")
	}
	revoker := &dbRevoker{store: s}
	if worked, e := ResumeImportOne(t.Context(), f.source, f.target, b.ID, revoker); !worked || e != nil {
		t.Fatal("resume failed", e)
	}
	b, e = ImportStatus(t.Context(), f.target, b.ID)
	if e != nil || b.State != "completed" || b.Completed != 1 {
		t.Fatal("batch not completed")
	}
	if before != tableSnapshot(t, f.source, "fixture_business_marker") {
		t.Fatal("business history changed")
	}
	var phone, sourceHash, sourceAccount string
	var auth int64
	if f.source.QueryRow(t.Context(), `SELECT phone,password_hash,platform_account_id,platform_auth_version,local_identity_state FROM im_users WHERE id=$1`, uid).Scan(&phone, &sourceHash, &sourceAccount, &auth, &state) != nil || phone != "19900000123" || sourceHash != "" || sourceAccount != account || auth != 2 || state != "active" {
		t.Fatal("source binding invalid")
	}
	var activeSessions, activePush int
	if f.source.QueryRow(t.Context(), `SELECT count(*) FROM im_refresh_sessions WHERE revoked_at IS NULL`).Scan(&activeSessions) != nil || activeSessions != 0 {
		t.Fatal("old refresh survived")
	}
	if f.source.QueryRow(t.Context(), `SELECT count(*) FROM im_devices WHERE notifications_enabled OR push_token<>''`).Scan(&activePush) != nil || activePush != 0 {
		t.Fatal("old device binding survived")
	}
	if _, e = p.Login(t.Context(), phone, "FixturePassword123!"); !errors.Is(e, platform.ErrDenied) {
		t.Fatal("import activated paused tenant")
	}
	// Activation below is a test-only shortcut, not part of migration.
	execFixture(t, f.target, `UPDATE platform_tenants SET status='active' WHERE id='default'`)
	if _, e = p.Login(t.Context(), phone, "FixturePassword123!"); e != nil {
		t.Fatal("preserved password cannot login after separate activation", e)
	}
	if _, e = ResumeImportOne(t.Context(), f.source, f.target, b.ID, revoker); e != nil || revoker.disconnects != 1 {
		t.Fatal("completed replay revoked a newer session")
	}
	audits := tableSnapshot(t, f.target, "platform_audits") + tableSnapshot(t, f.source, "im_audits")
	for _, private := range []string{hash, "FixturePassword123!", "historical-message-sentinel", "19900000123"} {
		if strings.Contains(audits, private) {
			t.Fatal("audit copied sensitive payload")
		}
	}
}

func TestPostgresLegacyImportDisconnectFailureAndLostAckRecover(t *testing.T) {
	for _, mode := range []string{"disconnect-failure", "lost-ack"} {
		t.Run(mode, func(t *testing.T) {
			f, s, p := importFixture(t)
			addLegacy(t, f, "historical", "19900000124", false, nil)
			b, e := StartImport(t.Context(), f.source, f.target, importRequest(t, f, "failure-batch"))
			if e != nil {
				t.Fatal(e)
			}
			r := &dbRevoker{store: s, fail: mode == "disconnect-failure", loseAck: mode == "lost-ack"}
			if _, e = ResumeImportOne(t.Context(), f.source, f.target, b.ID, r); !errors.Is(e, ErrImportUnavailable) {
				t.Fatal("failed revocation claimed success")
			}
			var state, code string
			if f.target.QueryRow(t.Context(), `SELECT a.state,i.error_code FROM platform_accounts a JOIN platform_legacy_import_items i ON a.id=i.account_id`).Scan(&state, &code) != nil || state != "provisioning" || code != "LEGACY_IMPORT_UNCONFIRMED" {
				t.Fatal("failure did not keep inert sanitized task")
			}
			if _, e = p.RequestRealm(t.Context(), "default", "premature", "operator", "must remain suspended", 2, true, true); e == nil {
				t.Fatal("resumed during import")
			}
			r.fail = false
			retryImport(t, f)
			if _, e = ResumeImportOne(t.Context(), f.source, f.target, b.ID, r); e != nil {
				t.Fatal("recovery failed", e)
			}
			if r.disconnects != 1 {
				t.Fatal("confirmed revocation repeated after lost acknowledgement")
			}
			if _, e = p.RequestRealm(t.Context(), "default", "resume-after", "operator", "separate explicit resumption", 2, true, true); e != nil {
				t.Fatal("completed import still blocked realm resume", e)
			}
		})
	}
}

func TestPostgresLegacyImportValidationAndDriftRollback(t *testing.T) {
	f, s, _ := importFixture(t)
	addLegacy(t, f, "historical", "19900000125", false, nil)
	r := importRequest(t, f, "drift-batch")
	for _, change := range []func(*ImportRequest){func(r *ImportRequest) { r.Confirmed = false }, func(r *ImportRequest) { r.Reason = "" }, func(r *ImportRequest) { r.ExpectedFingerprint = strings.Repeat("0", 64) }} {
		x := r
		change(&x)
		if _, e := StartImport(t.Context(), f.source, f.target, x); e == nil {
			t.Fatal("invalid import accepted")
		}
	}
	execFixture(t, f.source, `UPDATE im_tenant_identity SET access_enabled=true`)
	if _, e := StartImport(t.Context(), f.source, f.target, r); !errors.Is(e, ErrMaintenance) {
		t.Fatal("active enterprise accepted")
	}
	execFixture(t, f.source, `UPDATE im_tenant_identity SET access_enabled=false`)
	if tableSnapshot(t, f.target, "platform_accounts") != "[]" {
		t.Fatal("rejected import left reserved accounts")
	}
	b, e := StartImport(t.Context(), f.source, f.target, r)
	if e != nil {
		t.Fatal(e)
	}
	changed := r
	changed.Reason = "changed reason"
	if _, e = StartImport(t.Context(), f.source, f.target, changed); !errors.Is(e, ErrImportConflict) {
		t.Fatal("request identity reused")
	}
	execFixture(t, f.source, `UPDATE im_users SET password_hash='' WHERE id='historical'`)
	if _, e = ResumeImportOne(t.Context(), f.source, f.target, b.ID, &dbRevoker{store: s}); !errors.Is(e, ErrImportConflict) {
		t.Fatal("source drift accepted")
	}
	var mapped bool
	if f.source.QueryRow(t.Context(), `SELECT platform_account_id IS NOT NULL FROM im_users WHERE id='historical'`).Scan(&mapped) != nil || mapped {
		t.Fatal("source drift partially bound")
	}
}

func TestPostgresLegacyImportAtomicReservationAndConcurrentWorkers(t *testing.T) {
	f, s, _ := importFixture(t)
	addLegacy(t, f, "one", "19900000126", false, nil)
	addLegacy(t, f, "two", "19900000127", false, nil)
	r := importRequest(t, f, "batch-concurrent")
	execFixture(t, f.target, `CREATE FUNCTION fixture_fail_import() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.local_user_id='two' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;
CREATE TRIGGER fixture_fail_import BEFORE INSERT ON platform_accounts FOR EACH ROW EXECUTE FUNCTION fixture_fail_import()`)
	if _, e := StartImport(t.Context(), f.source, f.target, r); e == nil {
		t.Fatal("partial reserve success")
	}
	if tableSnapshot(t, f.target, "platform_accounts") != "[]" || tableSnapshot(t, f.target, "platform_legacy_import_batches") != "[]" {
		t.Fatal("reservation not atomic")
	}
	execFixture(t, f.target, `DROP TRIGGER fixture_fail_import ON platform_accounts`)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := StartImport(context.Background(), f.source, f.target, r); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("concurrent start failed", e)
		}
	}
	revoker := &dbRevoker{store: s}
	errs = make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := ResumeImportOne(context.Background(), f.source, f.target, r.ID, revoker)
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("concurrent worker failed", e)
		}
	}
	b, e := ImportStatus(t.Context(), f.target, r.ID)
	if e != nil || b.Total != 2 || b.Completed != 2 || b.State != "completed" || revoker.disconnects != 2 {
		t.Fatal("concurrent completion wrong")
	}
}

func TestPostgresLegacyImportBansPasswordlessAndNaturalExpiry(t *testing.T) {
	f, s, p := importFixture(t)
	future := time.Now().UTC().Add(time.Hour)
	past := time.Now().UTC().Add(-time.Hour)
	addLegacy(t, f, "temporary", "19900000128", true, &future)
	addLegacy(t, f, "permanent", "19900000129", true, nil)
	addLegacy(t, f, "expired", "19900000130", true, &past)
	addLegacy(t, f, "passwordless", "19900000131", false, nil)
	execFixture(t, f.source, `UPDATE im_users SET password_hash='' WHERE id='passwordless'`)
	r := importRequest(t, f, "ban-batch")
	if _, e := StartImport(t.Context(), f.source, f.target, r); !errors.Is(e, ErrImportConflict) {
		t.Fatal("passwordless warning not acknowledged")
	}
	r.AllowPasswordless = true
	if _, e := StartImport(t.Context(), f.source, f.target, r); e != nil {
		t.Fatal(e)
	}
	// Simulate the pre-existing business expiry worker between reserve and bind.
	execFixture(t, f.source, `UPDATE im_users SET banned=false,banned_until=NULL WHERE id='expired'`)
	revoker := &dbRevoker{store: s}
	for range 4 {
		if _, e := ResumeImportOne(t.Context(), f.source, f.target, r.ID, revoker); e != nil {
			t.Fatal("ban import failed", e)
		}
	}
	for _, uid := range []string{"temporary", "permanent", "expired", "passwordless"} {
		var blocked bool
		var state string
		if f.target.QueryRow(t.Context(), `SELECT globally_blocked,state FROM platform_accounts WHERE local_user_id=$1`, uid).Scan(&blocked, &state) != nil {
			t.Fatal("missing imported account")
		}
		want := uid == "temporary" || uid == "permanent"
		if blocked != want || (state == "blocked") != want {
			t.Fatal("ban semantics changed", uid)
		}
	}
	var count int
	if f.target.QueryRow(t.Context(), `SELECT count(*) FROM platform_legacy_ban_expiries`).Scan(&count) != nil || count != 1 {
		t.Fatal("temporary expiry not scheduled precisely")
	}
	execFixture(t, f.target, `UPDATE platform_tenants SET status='active' WHERE id='default'`)
	for _, phone := range []string{"19900000128", "19900000129", "19900000131"} {
		if _, e := p.Login(t.Context(), phone, "FixturePassword123!"); !errors.Is(e, platform.ErrDenied) {
			t.Fatal("blocked/passwordless login accepted")
		}
	}
}

type importRevokerFunc func(context.Context, tenancy.CredentialOperation) error

func (f importRevokerFunc) RevokeCredentials(ctx context.Context, op tenancy.CredentialOperation) error {
	return f(ctx, op)
}

func TestPostgresLegacyImportAuditFailureAndLeaseLossStayInert(t *testing.T) {
	for _, mode := range []string{"source-audit", "completion-audit", "lease-loss"} {
		t.Run(mode, func(t *testing.T) {
			f, s, _ := importFixture(t)
			hash := addLegacy(t, f, "historical", "19900000132", false, nil)
			request := importRequest(t, f, "fault-batch")
			if _, e := StartImport(t.Context(), f.source, f.target, request); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "source-audit":
				execFixture(t, f.source, `CREATE FUNCTION fixture_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture audit unavailable'; END $$;
CREATE TRIGGER fixture_reject_audit BEFORE INSERT ON im_audits FOR EACH ROW EXECUTE FUNCTION fixture_reject_audit()`)
			case "completion-audit":
				execFixture(t, f.target, `CREATE FUNCTION fixture_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture audit unavailable'; END $$;
CREATE TRIGGER fixture_reject_audit BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION fixture_reject_audit()`)
			}
			base := &dbRevoker{store: s}
			revoker := importRevokerFunc(func(ctx context.Context, op tenancy.CredentialOperation) error {
				if e := base.RevokeCredentials(ctx, op); e != nil {
					return e
				}
				if mode == "lease-loss" {
					// Another owner acquired an expired lease; the old owner cannot
					// advance or activate, even after receiving a valid revoke ACK.
					_, e := f.target.Exec(ctx, `UPDATE platform_legacy_import_items SET lease_id='replacement-owner',lease_until=clock_timestamp()+interval '1 minute' WHERE id=$1`, op.OperationID)
					return e
				}
				return nil
			})
			if _, e := ResumeImportOne(t.Context(), f.source, f.target, request.ID, revoker); !errors.Is(e, ErrImportUnavailable) {
				t.Fatal("faulted task reported completion", e)
			}
			var state string
			if f.target.QueryRow(t.Context(), `SELECT state FROM platform_accounts`).Scan(&state) != nil || state != "provisioning" {
				t.Fatal("unconfirmed task activated account")
			}
			if mode == "source-audit" {
				var password, account string
				if f.source.QueryRow(t.Context(), `SELECT password_hash,COALESCE(platform_account_id,'') FROM im_users WHERE id='historical'`).Scan(&password, &account) != nil || password != hash || account != "" {
					t.Fatal("source audit failure partially changed identity")
				}
				execFixture(t, f.source, `DROP TRIGGER fixture_reject_audit ON im_audits`)
			} else if mode == "completion-audit" {
				var step string
				if f.target.QueryRow(t.Context(), `SELECT step FROM platform_legacy_import_items`).Scan(&step) != nil || step != "revoked" {
					t.Fatal("completion audit did not roll back item update")
				}
				execFixture(t, f.target, `DROP TRIGGER fixture_reject_audit ON platform_audits`)
			} else {
				if worked, e := ResumeImportOne(t.Context(), f.source, f.target, request.ID, base); e != nil || worked {
					t.Fatal("active replacement lease ignored")
				}
			}
			retryImport(t, f)
			if _, e := ResumeImportOne(t.Context(), f.source, f.target, request.ID, base); e != nil {
				t.Fatal("fault recovery failed", e)
			}
			b, e := ImportStatus(t.Context(), f.target, request.ID)
			if e != nil || b.State != "completed" || base.disconnects != 1 {
				t.Fatal("recovery duplicated revocation or failed completion")
			}
		})
	}
}
