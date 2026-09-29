package directorybackup

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

func recoveryActivationFixture(t *testing.T, approve bool) (Database, ActivationRequest, InventoryReader) {
	t.Helper()
	db, review, inventory := stagedReviewFixture(t)
	if approve {
		conn, e := db.connect(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		_, e = conn.Exec(t.Context(), `UPDATE platform_jobs SET step='completed'`)
		conn.Close(context.Background())
		if e != nil {
			t.Fatal(e)
		}
	}
	read := func(context.Context, ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		digest, e := inventory.Digest()
		return inventory, digest, e
	}
	result, e := db.Review(t.Context(), review, read)
	if e != nil {
		t.Fatal(e)
	}
	hash, e := bcrypt.GenerateFromPassword([]byte("new-private-recovery-password"), 12)
	if e != nil {
		t.Fatal(e)
	}
	r := ActivationRequest{ReviewRequest: review, ActivationID: "activation-one", EvidenceDigest: result.EvidenceDigest, AuthoritySHA256: strings.Repeat("a", 64), OldAuthoritiesSHA256: strings.Repeat("b", 64), PlatformControlURL: "https://new-platform.example.test", MaintenanceEvidenceSHA256: strings.Repeat("c", 64), AdminUsername: "recovered-operator", AdminPasswordHash: string(hash), AdminCredentialSHA256: reviewHash(string(hash)), Accounts: []RecoveryAccount{}}
	if approve {
		r.Accounts = append(r.Accounts, RecoveryAccount{Identity: inventory.Users[0].Identity, AuthVersion: 1, VerificationSHA256: strings.Repeat("d", 64), PasswordHash: string(hash), CredentialSHA256: reviewHash(string(hash))})
	}
	return db, r, read
}

func TestRecoveryActivationPostgresHoldsUnknownAuthority(t *testing.T) {
	db, r, read := recoveryActivationFixture(t, false)
	first, e := db.PrepareActivation(t.Context(), r)
	if e != nil || first.State != "prepared" {
		t.Fatal("prepare", e)
	}
	if _, e = db.PrepareActivation(t.Context(), r); e != nil {
		t.Fatal("prepare retry", e)
	}
	if _, e = platform.Open(t.Context(), db.DSN); e == nil {
		t.Fatal("prepared recovery started platform")
	}
	if _, e = db.Activate(t.Context(), r, read, func(context.Context, ReviewPeer) error { return errors.New("old source still trusted") }); e == nil {
		t.Fatal("old authority bypass")
	}
	status, e := db.ActivationStatus(t.Context(), r)
	if e != nil || status.State != "prepared" {
		t.Fatal("failed activation committed", e)
	}
	result, e := db.Activate(t.Context(), r, read, func(context.Context, ReviewPeer) error { return nil })
	if e != nil || result.HeldAccounts != 1 || result.ResumedAccounts != 0 {
		t.Fatal("activate", e, result)
	}
	if repeated, e := db.Activate(t.Context(), r, read, func(context.Context, ReviewPeer) error { t.Fatal("completed receipt reran remote effects"); return nil }); e != nil || repeated != result {
		t.Fatal("activation retry", e)
	}
	if _, e = platform.OpenWithAuthority(t.Context(), db.DSN, strings.Repeat("f", 64)); e == nil {
		t.Fatal("wrong control key booted")
	}
	s, e := platform.OpenWithAuthority(t.Context(), db.DSN, r.AuthoritySHA256)
	if e != nil {
		t.Fatal("correct authority could not start", e)
	}
	defer s.Close()
	if job, e := s.Claim(t.Context()); e != nil || job != nil {
		t.Fatal("restored identity work replayed", e)
	}
	conn, e := db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	var held bool
	if conn.QueryRow(t.Context(), `SELECT password_hash='' AND state='blocked' FROM platform_accounts WHERE id='account-one'`).Scan(&held) != nil || !held {
		t.Fatal("old credentials retained")
	}
	var raw []byte
	if conn.QueryRow(t.Context(), `SELECT input FROM frogim_recovery.activations WHERE id=$1`, r.ActivationID).Scan(&raw) != nil || strings.Contains(string(raw), r.AdminPasswordHash) || strings.Contains(string(raw), "19911112222") {
		t.Fatal("credentials or phone stored in operation")
	}
	var count int
	if conn.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action='platform.recovery.activated'`).Scan(&count) != nil || count != 1 {
		t.Fatal("audit duplicated")
	}
}

func TestRecoveryActivationPostgresApprovedAccountAndAuditRollback(t *testing.T) {
	db, r, read := recoveryActivationFixture(t, true)
	if _, e := db.PrepareActivation(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	conn, e := db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	if _, e = conn.Exec(t.Context(), `CREATE FUNCTION reject_activation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='platform.recovery.activated' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_activation BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_activation()`); e != nil {
		t.Fatal(e)
	}
	verify := func(context.Context, ReviewPeer) error { return nil }
	if _, e = db.Activate(t.Context(), r, read, verify); e == nil {
		t.Fatal("audit failure activated")
	}
	status, e := db.ActivationStatus(t.Context(), r)
	if e != nil || status.State != "prepared" {
		t.Fatal("audit rollback", e)
	}
	if _, e = conn.Exec(t.Context(), `DROP TRIGGER reject_activation ON platform_audits; DROP FUNCTION reject_activation()`); e != nil {
		t.Fatal(e)
	}
	result, e := db.Activate(t.Context(), r, read, verify)
	if e != nil || result.ResumedAccounts != 1 || result.HeldAccounts != 0 {
		t.Fatal("approved activation", e, result)
	}
	s, e := platform.OpenWithAuthority(t.Context(), db.DSN, r.AuthoritySHA256)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.Login(t.Context(), "19911112222", "new-private-recovery-password"); e == nil {
		t.Fatal("activation implicitly reopened enterprise")
	}
	// Separate explicit enterprise reopening is modelled here. Live enterprise
	// realm revocation and reopen are covered by the transport integration suite.
	if _, e = conn.Exec(t.Context(), `UPDATE platform_tenants SET status='active' WHERE id='default'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Login(t.Context(), "19911112222", "new-private-recovery-password"); e != nil {
		t.Fatal("fresh password rejected", e)
	}
	if _, e = s.Login(t.Context(), "19911112222", "fixture-hash-not-a-real-password"); e == nil {
		t.Fatal("old password accepted")
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), r.AdminPasswordHash) {
		t.Fatal("password hash serialized")
	}
}

func TestRecoveryActivationPostgresEvidenceDriftAndLeaseLoss(t *testing.T) {
	db, r, read := recoveryActivationFixture(t, true)
	if _, e := db.PrepareActivation(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	changed := func(ctx context.Context, p ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		inventory, _, e := read(ctx, p)
		inventory.Users[0].AuthVersion++
		digest, _ := inventory.Digest()
		return inventory, digest, e
	}
	if _, e := db.Activate(t.Context(), r, changed, func(context.Context, ReviewPeer) error { return nil }); e == nil {
		t.Fatal("changed live identity activated")
	}
	conn, e := db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	_, e = db.Activate(t.Context(), r, read, func(context.Context, ReviewPeer) error {
		var terminated bool
		if err := conn.QueryRow(t.Context(), `SELECT pg_terminate_backend(l.pid) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND l.objid=$1 AND a.datname=current_database() AND l.pid<>pg_backend_pid()`, tenancy.PlatformMigrationLock).Scan(&terminated); err != nil || !terminated {
			t.Fatal("fixture lock termination", err)
		}
		return nil
	})
	if e == nil {
		t.Fatal("lost connection activated")
	}
	if _, e = conn.Exec(t.Context(), `UPDATE platform_accounts SET globally_blocked=true WHERE id='account-one'`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Activate(t.Context(), r, read, func(context.Context, ReviewPeer) error { return nil }); e == nil {
		t.Fatal("changed directory snapshot activated")
	}
	if _, e = platform.Open(t.Context(), db.DSN); e == nil {
		t.Fatal("failed recovery unquarantined")
	}
}

func TestRecoveryActivationPostgresLaterVerification(t *testing.T) {
	db, r, read := recoveryActivationFixture(t, true)
	accounts := r.Accounts
	r.Accounts = nil
	verify := func(context.Context, ReviewPeer) error { return nil }
	if _, e := db.PrepareActivation(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Activate(t.Context(), r, read, verify); e != nil {
		t.Fatal(e)
	}
	conn, e := db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	var revoked bool
	if conn.QueryRow(t.Context(), `SELECT NOT EXISTS(SELECT 1 FROM platform_jobs WHERE poll_hash IS NOT NULL)`).Scan(&revoked) != nil || !revoked {
		t.Fatal("poll credential survived")
	}
	// A known global ban is preserved even when credentials and identity are
	// subsequently verified. Unblocking remains an explicit normal admin task.
	if _, e = conn.Exec(t.Context(), `UPDATE platform_accounts SET globally_blocked=true WHERE id='account-one'`); e != nil {
		t.Fatal(e)
	}
	r.Mode, r.ActivationID, r.ReviewID = "accounts", "later-verification", "later-review"
	r.AdminUsername, r.AdminPasswordHash, r.AdminCredentialSHA256 = "", "", ""
	r.Accounts = accounts
	review, e := db.Review(t.Context(), r.ReviewRequest, read)
	if e != nil {
		t.Fatal(e)
	}
	r.EvidenceDigest = review.EvidenceDigest
	s, e := platform.OpenWithAuthority(t.Context(), db.DSN, r.AuthoritySHA256)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.PrepareActivation(t.Context(), r); e == nil {
		t.Fatal("live directory accepted offline recovery")
	}
	s.Close()
	if _, e = db.PrepareActivation(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Activate(t.Context(), r, read, verify); e != nil {
		t.Fatal(e)
	}
	var preserved bool
	if conn.QueryRow(t.Context(), `SELECT globally_blocked AND state='blocked' AND password_hash<>'' AND NOT EXISTS(SELECT 1 FROM platform_recovery_holds WHERE kind='account' AND object_id='account-one') FROM platform_accounts WHERE id='account-one'`).Scan(&preserved) != nil || !preserved {
		t.Fatal("later verification lost ban or failed to release hold")
	}
	if _, e = db.Activate(t.Context(), r, read, verify); e != nil {
		t.Fatal("retry later verification", e)
	}
}
