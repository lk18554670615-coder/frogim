package directorybackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

func stagedReviewFixture(t *testing.T) (Database, ReviewRequest, tenancy.RecoveryInventory) {
	t.Helper()
	source := integrationDatabase(t)
	seedDirectory(t, source)
	identity, e := source.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r, b, key := fixture()
	r.Expected.DirectoryID = identity.DirectoryID
	path := filepath.Join(t.TempDir(), "archive")
	proof, e := source.Capture(t.Context(), r, b, path, key)
	if e != nil {
		t.Fatal(e)
	}
	target := integrationDatabase(t)
	if e = target.Stage(t.Context(), r, path, key); e != nil {
		t.Fatal(e)
	}
	request := ReviewRequest{Request: r, ReviewID: "review-one", Manifest: proof, Confirmed: true, Peers: []ReviewPeer{{TenantID: "default", HTTPBaseURL: "https://fixture.example.test", ControlURL: "https://control.example.test", RealmVersion: 2}}}
	inventory := tenancy.RecoveryInventory{Version: 1, TenantID: "default", HTTPBaseURL: "https://fixture.example.test", Realm: tenancy.RealmSnapshot{Version: 2, SuspensionConfirmed: true}, Users: []tenancy.RecoveryIdentity{{Identity: tenancy.Identity{AccountID: "account-one", TenantID: "default", LocalUserID: "user-one", AssignmentVersion: 1}, AuthVersion: 1, Phone: "19911112222", State: "active"}}}
	return target, request, inventory
}

func TestRecoveryReviewPostgresRejectsLiveDirectory(t *testing.T) {
	db := integrationDatabase(t)
	seedDirectory(t, db)
	identity, e := db.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r, _, _ := fixture()
	r.Expected.DirectoryID = identity.DirectoryID
	request := ReviewRequest{Request: r, ReviewID: "live-rejected", Manifest: backup.File{Name: "manifest", SHA256: reviewHash("fixture"), Size: 1}, Confirmed: true, Peers: []ReviewPeer{{TenantID: "default", HTTPBaseURL: "https://fixture.example.test", ControlURL: "https://control.example.test", RealmVersion: 2}}}
	called := false
	_, e = db.Review(t.Context(), request, func(context.Context, ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		called = true
		return tenancy.RecoveryInventory{}, "", nil
	})
	if !errors.Is(e, ErrQuarantined) || called {
		t.Fatal("live database accepted")
	}
	conn, e := db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	var exists bool
	if e = conn.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='frogim_recovery')`).Scan(&exists); e != nil || exists {
		t.Fatal("live database modified")
	}
}

func TestRecoveryReviewPostgresPersistentEvidence(t *testing.T) {
	db, r, inventory := stagedReviewFixture(t)
	read := func(ctx context.Context, p ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		digest, e := inventory.Digest()
		return inventory, digest, e
	}
	first, e := db.Review(t.Context(), r, read)
	if e != nil || first.State != "completed" || first.ActivationAllowed || first.IdentityCount != 1 {
		t.Fatal("review", e)
	}
	status, e := db.ReviewStatus(t.Context(), r)
	if e != nil || !reflect.DeepEqual(status, first) {
		t.Fatal("durable result", e)
	}
	replayed, e := db.Review(t.Context(), r, read)
	if e != nil || !reflect.DeepEqual(replayed, first) {
		t.Fatal("idempotent review", e)
	}
	conn, e := db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	var count int
	if e = conn.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action LIKE 'platform.recovery.review_%'`).Scan(&count); e != nil || count != 2 {
		t.Fatal("audit duplicated", count, e)
	}
	var metadata []byte
	if e = conn.QueryRow(t.Context(), `SELECT json_agg(metadata) FROM platform_audits WHERE action LIKE 'platform.recovery.review_%'`).Scan(&metadata); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(first)
	for _, secret := range []string{"19911112222", "fixture-hash-not-a-real-password", "databaseUrl", "password_hash"} {
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(metadata, []byte(secret)) {
			t.Fatal("evidence output/audit disclosure")
		}
	}
	for _, a := range []string{"account-one", "user-one"} {
		if bytes.Contains(metadata, []byte(a)) {
			t.Fatal("audit copied identity evidence")
		}
	}
	// Report completion never enables sessions, workers, accounts or realms.
	if _, e := platform.Open(t.Context(), db.DSN); e == nil {
		t.Fatal("review activated recovered platform")
	}
	if _, e = db.Inspect(t.Context()); !errors.Is(e, ErrQuarantined) {
		t.Fatal("quarantine removed")
	}
	if e = checkRestored(t.Context(), conn, r.Request); e != nil {
		t.Fatal("review changed restored authority", e)
	}
	if e = conn.QueryRow(t.Context(), `SELECT count(*) FROM frogim_recovery.reviews WHERE evidence->0->'inventory'->'users'->0->>'phone'='19911112222'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("private evidence absent")
	}
	changed := r
	changed.Reason = "different decision"
	if _, e = db.Review(t.Context(), changed, read); !errors.Is(e, ErrReviewChanged) {
		t.Fatal("mutated review input accepted", e)
	}
	changed = r
	changed.Manifest.SHA256 = reviewHash("other archive")
	if _, e = db.Review(t.Context(), changed, read); !errors.Is(e, ErrQuarantined) {
		t.Fatal("wrong archive accepted", e)
	}
	inventory.Users[0].AuthVersion++
	if _, e = db.Review(t.Context(), r, read); !errors.Is(e, ErrReviewChanged) {
		t.Fatal("changed live evidence reused", e)
	}
	r.ReviewID = "review-new"
	newResult, e := db.Review(t.Context(), r, read)
	if e != nil || newResult.EvidenceDigest == first.EvidenceDigest {
		t.Fatal("new review", e)
	}
}

func TestRecoveryReviewPostgresFailureAndAuditRollback(t *testing.T) {
	db, r, inventory := stagedReviewFixture(t)
	read := func(ctx context.Context, p ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		digest, e := inventory.Digest()
		return inventory, digest, e
	}
	conn, e := db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	_, e = conn.Exec(t.Context(), `CREATE FUNCTION reject_review_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'platform.recovery.review_%' THEN RAISE EXCEPTION 'fixture reject'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_review BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_review_audit()`)
	if e != nil {
		t.Fatal(e)
	}
	called := false
	if _, e = db.Review(t.Context(), r, func(ctx context.Context, p ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		called = true
		return read(ctx, p)
	}); e == nil || called {
		t.Fatal("audit-start failure allowed peer read")
	}
	if _, e = conn.Exec(t.Context(), `DROP TRIGGER reject_review ON platform_audits; DROP FUNCTION reject_review_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Review(t.Context(), r, func(context.Context, ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		return tenancy.RecoveryInventory{}, "", errors.New("private transport error")
	}); !errors.Is(e, ErrUnconfirmed) {
		t.Fatal("network failure", e)
	}
	status, e := db.ReviewStatus(t.Context(), r)
	if e != nil || status.State != "collecting" || status.ActivationAllowed {
		t.Fatal("failed review falsely completed", e)
	}
	_, e = conn.Exec(t.Context(), `CREATE FUNCTION reject_review_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='platform.recovery.review_completed' THEN RAISE EXCEPTION 'fixture reject'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_review BEFORE INSERT ON platform_audits FOR EACH ROW EXECUTE FUNCTION reject_review_completion()`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Review(t.Context(), r, read); e == nil {
		t.Fatal("completion audit ignored")
	}
	status, e = db.ReviewStatus(t.Context(), r)
	if e != nil || status.State != "collecting" {
		t.Fatal("completion was not atomic")
	}
	if _, e = conn.Exec(t.Context(), `DROP TRIGGER reject_review ON platform_audits; DROP FUNCTION reject_review_completion()`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Review(t.Context(), r, read); e != nil {
		t.Fatal("retry after audit repaired", e)
	}
	var count int
	if e = conn.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action LIKE 'platform.recovery.review_%'`).Scan(&count); e != nil || count != 2 {
		t.Fatal("retry duplicate audit")
	}
}

func TestRecoveryReviewPostgresLockLossAndSnapshotDrift(t *testing.T) {
	db, r, inventory := stagedReviewFixture(t)
	conn, e := db.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	if _, e = conn.Exec(t.Context(), `SELECT pg_advisory_lock($1)`, tenancy.PlatformMigrationLock); e != nil {
		t.Fatal(e)
	}
	read := func(ctx context.Context, p ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		digest, e := inventory.Digest()
		return inventory, digest, e
	}
	if _, e = db.Review(t.Context(), r, read); !errors.Is(e, ErrUnconfirmed) {
		t.Fatal("exclusive migration lock bypassed")
	}
	if _, e = conn.Exec(t.Context(), `SELECT pg_advisory_unlock($1)`, tenancy.PlatformMigrationLock); e != nil {
		t.Fatal(e)
	}
	_, e = db.Review(t.Context(), r, func(ctx context.Context, p ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		// Terminate only the review connection holding the exact lock in this
		// newly-created disposable database, never a broad backend wildcard.
		var terminated bool
		err := conn.QueryRow(t.Context(), `SELECT pg_terminate_backend(l.pid) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND l.objid=$1 AND a.datname=current_database() AND l.pid<>pg_backend_pid()`, tenancy.PlatformMigrationLock).Scan(&terminated)
		if err != nil || !terminated {
			t.Fatal("fixture lock termination", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("lost lock did not cancel reader")
		}
		return read(ctx, p)
	})
	if !errors.Is(e, ErrUnconfirmed) {
		t.Fatal("lost lock completion", e)
	}
	status, e := db.ReviewStatus(t.Context(), r)
	if e != nil || status.State != "collecting" {
		t.Fatal("lost lock falsely completed")
	}
	_, e = db.Review(t.Context(), r, func(ctx context.Context, p ReviewPeer) (tenancy.RecoveryInventory, string, error) {
		if _, err := conn.Exec(t.Context(), `UPDATE platform_accounts SET auth_version=auth_version+1 WHERE id='account-one'`); err != nil {
			t.Fatal(err)
		}
		return read(ctx, p)
	})
	if !errors.Is(e, ErrReviewChanged) {
		t.Fatal("changed source snapshot accepted", e)
	}
	if _, e = db.Review(t.Context(), r, read); !errors.Is(e, ErrReviewChanged) {
		t.Fatal("old review silently rebased", e)
	}
	r.ReviewID = "fresh-review"
	if _, e = db.Review(t.Context(), r, read); e != nil {
		t.Fatal("fresh review", e)
	}
}
