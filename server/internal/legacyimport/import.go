package legacyimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

var (
	ErrImportConflict    = errors.New("LEGACY_IMPORT_CONFLICT")
	ErrMaintenance       = errors.New("LEGACY_IMPORT_CONFIRMED_MAINTENANCE_REQUIRED")
	ErrImportUnavailable = errors.New("LEGACY_IMPORT_UNCONFIRMED")
)

type ImportRequest struct {
	ID, TenantID, Actor, Reason, ExpectedFingerprint string
	Confirmed, AllowPasswordless                     bool
}
type Batch struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenantId"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	State     string `json:"state"`
}
type importWork struct {
	id, batch, account, uid, tenant, phone, fingerprint, lease, step string
	realm                                                            int64
	banned                                                           bool
	until                                                            *time.Time
}

func credentialFingerprint(u sourceUser) string {
	if u.bannedUntil != nil {
		utc := u.bannedUntil.UTC()
		u.bannedUntil = &utc
	}
	b, _ := json.Marshal([]any{"legacy-credential-v1", u.id, u.phone, u.passwordHash, u.banned, u.bannedUntil})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func validFingerprint(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && strings.ToLower(v) == v
}

// Both sides must acknowledge the SAME completed suspension before adoption.
// This does not prove obsolete processes or public network paths were removed:
// that is a separate operator/deployment cutover gate, never inferred here.
func sourceMaintenance(ctx context.Context, tx pgx.Tx, tenant string, version int64) (int64, error) {
	var actual int64
	err := tx.QueryRow(ctx, `SELECT access_version FROM im_tenant_identity t WHERE tenant_id=$1 AND NOT access_enabled
AND EXISTS(SELECT 1 FROM im_tenant_realm_operations o WHERE o.access_version=t.access_version AND NOT o.enabled AND o.state='completed') FOR SHARE`, tenant).Scan(&actual)
	if err != nil || actual < 2 || (version != 0 && version != actual) {
		return 0, ErrMaintenance
	}
	return actual, nil
}
func targetMaintenance(ctx context.Context, tx pgx.Tx, tenant string, version int64) error {
	var actual int64
	err := tx.QueryRow(ctx, `SELECT access_version FROM platform_tenants t WHERE id=$1 AND is_default AND status='suspended'
AND EXISTS(SELECT 1 FROM platform_realm_jobs j WHERE j.tenant_id=t.id AND j.access_version=t.access_version AND NOT j.enabled AND j.state='completed') FOR UPDATE`, tenant).Scan(&actual)
	if err != nil || actual != version {
		return ErrMaintenance
	}
	var deploymentPending bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_deployment_jobs WHERE tenant_id=$1 AND state<>'completed') OR
 EXISTS(SELECT 1 FROM platform_backup_jobs WHERE tenant_id=$1 AND state IN ('pending','unconfirmed')) OR
 EXISTS(SELECT 1 FROM platform_maintenance_runs WHERE tenant_id=$1 AND state='pending')`, tenant).Scan(&deploymentPending) != nil || deploymentPending {
		return ErrMaintenance
	}
	return nil
}

// StartImport atomically reserves the ENTIRE valid inventory in the platform.
// The source is only read here; every resulting account is inert. There is no
// cross-database commit claim, automatic schema migration or tenant activation.
func StartImport(ctx context.Context, source, target *pgxpool.Pool, r ImportRequest) (Batch, error) {
	var empty Batch
	if source == nil || target == nil || !tenancy.ValidID(r.ID) || !tenancy.ValidID(r.TenantID) || !tenancy.ValidID(r.Actor) || !r.Confirmed || len(strings.TrimSpace(r.Reason)) < 1 || len([]rune(r.Reason)) > 500 || !validFingerprint(r.ExpectedFingerprint) {
		return empty, ErrConfig
	}
	r.Reason = strings.TrimSpace(r.Reason)
	s, err := source.Begin(ctx)
	if err != nil {
		return empty, ErrImportUnavailable
	}
	defer s.Rollback(ctx)
	// Prevent credential/profile edits while making the reservation snapshot.
	if _, err = s.Exec(ctx, `LOCK TABLE im_users IN SHARE MODE`); err != nil {
		return empty, ErrImportUnavailable
	}
	t, err := target.Begin(ctx)
	if err != nil {
		return empty, ErrImportUnavailable
	}
	defer t.Rollback(ctx)
	if _, err = t.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,79))`, r.ID); err != nil {
		return empty, ErrImportUnavailable
	}
	var prior Batch
	var actor, reason, fingerprint string
	var passwordless bool
	err = t.QueryRow(ctx, `SELECT id,tenant_id,total,state,actor_id,reason,fingerprint,allow_passwordless FROM platform_legacy_import_batches WHERE id=$1`, r.ID).Scan(&prior.ID, &prior.TenantID, &prior.Total, &prior.State, &actor, &reason, &fingerprint, &passwordless)
	if err == nil {
		if prior.TenantID != r.TenantID || actor != r.Actor || reason != r.Reason || fingerprint != r.ExpectedFingerprint || passwordless != r.AllowPasswordless {
			return empty, ErrImportConflict
		}
		if t.QueryRow(ctx, `SELECT count(*) FROM platform_legacy_import_items WHERE batch_id=$1 AND step='completed'`, r.ID).Scan(&prior.Completed) != nil {
			return empty, ErrImportUnavailable
		}
		return prior, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, ErrImportUnavailable
	}
	in := inventory{tenant: r.TenantID}
	if err = readSource(ctx, s, &in); err != nil {
		return empty, err
	}
	if err = readTarget(ctx, t, &in); err != nil {
		return empty, err
	}
	if in.sourceVersion != 79 || in.targetVersion != platform.SchemaVersion || in.boundTenant != r.TenantID {
		return empty, ErrSchema
	}
	version, err := sourceMaintenance(ctx, s, r.TenantID, 0)
	if err != nil {
		return empty, err
	}
	if err = targetMaintenance(ctx, t, r.TenantID, version); err != nil {
		return empty, err
	}
	var suspension string
	var matching bool
	if s.QueryRow(ctx, `SELECT operation_id FROM im_tenant_realm_operations WHERE access_version=$1 AND NOT enabled AND state='completed'`, version).Scan(&suspension) != nil || t.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_realm_jobs WHERE id=$1 AND tenant_id=$2 AND access_version=$3 AND state='completed' AND NOT enabled)`, suspension, r.TenantID, version).Scan(&matching) != nil || !matching {
		return empty, ErrMaintenance
	}
	report := analyze(in, time.Now().UTC())
	if !report.DataChecksPassed || report.Fingerprint != r.ExpectedFingerprint || (!r.AllowPasswordless && report.Counts.Passwordless > 0) {
		return empty, ErrImportConflict
	}
	state := "running"
	if report.Counts.Candidates == 0 {
		state = "completed"
	}
	if _, err = t.Exec(ctx, `INSERT INTO platform_legacy_import_batches(id,tenant_id,actor_id,reason,fingerprint,allow_passwordless,realm_version,total,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.ID, r.TenantID, r.Actor, r.Reason, r.ExpectedFingerprint, r.AllowPasswordless, version, report.Counts.Candidates, state); err != nil {
		return empty, ErrImportConflict
	}
	for _, u := range in.source {
		if u.deleted || u.state == "retired" || u.platformID != "" {
			continue
		}
		phone, e := platform.NormalizePhone(u.phone)
		if e != nil {
			return empty, ErrImportConflict
		}
		id, e := tenancy.Secret()
		if e != nil {
			return empty, ErrImportUnavailable
		}
		account := "acct_" + id
		id, e = tenancy.Secret()
		if e != nil {
			return empty, ErrImportUnavailable
		}
		item := "import_" + id
		if _, err = t.Exec(ctx, `INSERT INTO platform_accounts(id,phone,password_hash,state,tenant_id,local_user_id,assignment_version,auth_version) VALUES($1,$2,$3,'provisioning',$4,$5,1,2)`, account, phone, u.passwordHash, r.TenantID, u.id); err != nil {
			return empty, ErrImportConflict
		}
		if _, err = t.Exec(ctx, `INSERT INTO platform_legacy_import_items(id,batch_id,account_id,local_user_id,source_fingerprint,banned,banned_until,step) VALUES($1,$2,$3,$4,$5,$6,$7,'reserved')`, item, r.ID, account, u.id, credentialFingerprint(u), u.banned, u.bannedUntil); err != nil {
			return empty, ErrImportUnavailable
		}
	}
	if _, err = t.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,reason,metadata) VALUES($1,'legacy_import.reserved',$2,$3,$4,jsonb_build_object('count',$5::integer,'sourceSchema',79,'realmVersion',$6::bigint))`, r.Actor, r.TenantID, r.ID, r.Reason, report.Counts.Candidates, version); err != nil {
		return empty, ErrImportUnavailable
	}
	if err = t.Commit(ctx); err != nil {
		return empty, ErrImportUnavailable
	}
	return Batch{ID: r.ID, TenantID: r.TenantID, Total: report.Counts.Candidates, State: state}, nil
}

type CredentialRevoker interface {
	RevokeCredentials(context.Context, tenancy.CredentialOperation) error
}

func ImportStatus(ctx context.Context, target *pgxpool.Pool, id string) (Batch, error) {
	var b Batch
	if target == nil || !tenancy.ValidID(id) {
		return b, ErrConfig
	}
	err := target.QueryRow(ctx, `SELECT id,tenant_id,total,state,(SELECT count(*) FROM platform_legacy_import_items WHERE batch_id=b.id AND step='completed') FROM platform_legacy_import_batches b WHERE id=$1`, id).Scan(&b.ID, &b.TenantID, &b.Total, &b.State, &b.Completed)
	if err != nil {
		return Batch{}, ErrImportUnavailable
	}
	return b, nil
}

// ResumeImportOne is deliberately bounded. A caller may stop/restart between
// ANY steps. A lost response only repeats the same immutable operation, never a
// new identity or a second revocation of a subsequently activated session.
func ResumeImportOne(ctx context.Context, source, target *pgxpool.Pool, batch string, revoker CredentialRevoker) (bool, error) {
	if source == nil || target == nil || revoker == nil || !tenancy.ValidID(batch) {
		return false, ErrConfig
	}
	var hasHolds, held bool
	if target.QueryRow(ctx, `SELECT to_regclass('public.platform_recovery_holds') IS NOT NULL`).Scan(&hasHolds) != nil {
		return false, ErrImportUnavailable
	}
	if hasHolds {
		if target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_recovery_holds WHERE kind='platform_legacy_import_batches' AND object_id=$1)`, batch).Scan(&held) != nil || held {
			return false, ErrImportUnavailable
		}
	}
	lease, err := tenancy.Secret()
	if err != nil {
		return false, ErrImportUnavailable
	}
	w := importWork{batch: batch, lease: lease}
	err = target.QueryRow(ctx, `WITH candidate AS (SELECT i.id FROM platform_legacy_import_items i JOIN platform_legacy_import_batches b ON b.id=i.batch_id WHERE b.id=$1 AND b.state='running' AND i.step<>'completed' AND i.retry_at<=clock_timestamp() AND (i.lease_until IS NULL OR i.lease_until<clock_timestamp()) ORDER BY i.local_user_id FOR UPDATE OF i SKIP LOCKED LIMIT 1)
UPDATE platform_legacy_import_items i SET lease_id=$2,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1 FROM candidate c,platform_legacy_import_batches b,platform_accounts a WHERE i.id=c.id AND b.id=i.batch_id AND a.id=i.account_id
RETURNING i.id,i.account_id,i.local_user_id,b.tenant_id,a.phone,i.source_fingerprint,i.step,b.realm_version,i.banned,i.banned_until`, batch, lease).Scan(&w.id, &w.account, &w.uid, &w.tenant, &w.phone, &w.fingerprint, &w.step, &w.realm, &w.banned, &w.until)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrImportUnavailable
	}
	err = resumeWork(ctx, source, target, w, revoker)
	if err == nil {
		return true, nil
	}
	code := "LEGACY_IMPORT_UNCONFIRMED"
	if errors.Is(err, ErrMaintenance) {
		code = "LEGACY_IMPORT_MAINTENANCE_REQUIRED"
	}
	if errors.Is(err, ErrImportConflict) {
		code = "LEGACY_IMPORT_SOURCE_CHANGED"
	}
	// Remote bodies, hashes, phone numbers and DSNs are never written to errors.
	_, _ = target.Exec(ctx, `UPDATE platform_legacy_import_items SET lease_id=NULL,lease_until=NULL,error_code=$3,retry_at=clock_timestamp()+interval '5 seconds',updated_at=now() WHERE id=$1 AND lease_id=$2 AND step<>'completed'`, w.id, w.lease, code)
	return true, err
}
func targetStep(ctx context.Context, p *pgxpool.Pool, w importWork, next string) error {
	tx, e := p.Begin(ctx)
	if e != nil {
		return ErrImportUnavailable
	}
	defer tx.Rollback(ctx)
	if e = targetMaintenance(ctx, tx, w.tenant, w.realm); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE platform_legacy_import_items SET step=$3,error_code='',updated_at=now() WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND step<>'completed'`, w.id, w.lease, next)
	if e != nil || tag.RowsAffected() != 1 {
		return ErrImportUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrImportUnavailable
	}
	return nil
}
func resumeWork(ctx context.Context, source, target *pgxpool.Pool, w importWork, revoker CredentialRevoker) error {
	if w.step == "reserved" {
		if e := bindSource(ctx, source, w); e != nil {
			return e
		}
		if e := targetStep(ctx, target, w, "bound"); e != nil {
			return e
		}
		w.step = "bound"
	}
	if w.step == "bound" {
		op := tenancy.CredentialOperation{OperationID: w.id, Identity: tenancy.Identity{AccountID: w.account, TenantID: w.tenant, LocalUserID: w.uid, AssignmentVersion: 1}, AuthVersion: 2}
		if e := revoker.RevokeCredentials(ctx, op); e != nil {
			return ErrImportUnavailable
		}
		if e := targetStep(ctx, target, w, "revoked"); e != nil {
			return e
		}
		w.step = "revoked"
	}
	if w.step != "revoked" {
		return ErrImportConflict
	}
	if e := finishSource(ctx, source, w); e != nil {
		return e
	}
	return finishTarget(ctx, target, w)
}
func sameTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
func bindSource(ctx context.Context, p *pgxpool.Pool, w importWork) error {
	tx, e := p.Begin(ctx)
	if e != nil {
		return ErrImportUnavailable
	}
	defer tx.Rollback(ctx)
	if _, e = sourceMaintenance(ctx, tx, w.tenant, w.realm); e != nil {
		return e
	}
	var priorAccount, priorUser, priorFingerprint string
	e = tx.QueryRow(ctx, `SELECT account_id,local_user_id,source_fingerprint FROM im_tenant_legacy_imports WHERE operation_id=$1`, w.id).Scan(&priorAccount, &priorUser, &priorFingerprint)
	if e == nil {
		if priorAccount != w.account || priorUser != w.uid || priorFingerprint != w.fingerprint {
			return ErrImportConflict
		}
		return nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return ErrImportUnavailable
	}
	var u sourceUser
	u.id = w.uid
	e = tx.QueryRow(ctx, `SELECT phone,password_hash,banned,banned_until,deleted_at IS NOT NULL,COALESCE(platform_account_id,''),assignment_version,platform_auth_version,local_identity_state FROM im_users WHERE id=$1 FOR UPDATE`, w.uid).Scan(&u.phone, &u.passwordHash, &u.banned, &u.bannedUntil, &u.deleted, &u.platformID, &u.assignment, &u.auth, &u.state)
	if e != nil {
		return ErrImportUnavailable
	}
	// Natural expiry cleanup is not a credential change. Other concurrent ban
	// edits still stop the task and require explicit reconciliation.
	var sourceNow time.Time
	if tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&sourceNow) != nil {
		return ErrImportUnavailable
	}
	if w.banned && w.until != nil && !w.until.After(sourceNow) && !u.banned && u.bannedUntil == nil {
		u.banned = true
		u.bannedUntil = w.until
	}
	if u.deleted || u.platformID != "" || u.state != "active" || u.assignment != 1 || u.auth != 1 || credentialFingerprint(u) != w.fingerprint {
		return ErrImportConflict
	}
	phone, e := platform.NormalizePhone(u.phone)
	if e != nil || phone != w.phone || u.banned != w.banned || !sameTime(u.bannedUntil, w.until) {
		return ErrImportConflict
	}
	if _, e = tx.Exec(ctx, `UPDATE im_users SET platform_account_id=$2,phone=$3,password_hash='',local_identity_state='prepared',updated_at=now() WHERE id=$1`, w.uid, w.account, w.phone); e != nil {
		return ErrImportConflict
	}
	if _, e = tx.Exec(ctx, `INSERT INTO im_tenant_legacy_imports(operation_id,account_id,local_user_id,assignment_version,auth_version,source_fingerprint,state) VALUES($1,$2,$3,1,2,$4,'bound')`, w.id, w.account, w.uid, w.fingerprint); e != nil {
		return ErrImportConflict
	}
	if _, e = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,'platform','identity.legacy.bound','user',$2,jsonb_build_object('accountId',$3::text,'assignmentVersion',1,'authVersion',2),now())`, "aud_"+w.id, w.uid, w.account); e != nil {
		return ErrImportUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrImportUnavailable
	}
	return nil
}
func finishSource(ctx context.Context, p *pgxpool.Pool, w importWork) error {
	tx, e := p.Begin(ctx)
	if e != nil {
		return ErrImportUnavailable
	}
	defer tx.Rollback(ctx)
	if _, e = sourceMaintenance(ctx, tx, w.tenant, w.realm); e != nil {
		return e
	}
	var valid bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users u JOIN im_tenant_legacy_imports i ON i.local_user_id=u.id JOIN im_tenant_credential_operations c ON c.operation_id=i.operation_id
WHERE i.operation_id=$1 AND i.account_id=$2 AND i.source_fingerprint=$3 AND u.platform_account_id=$2 AND u.assignment_version=1 AND u.platform_auth_version=2 AND u.local_identity_state='active' AND u.deleted_at IS NULL AND u.password_hash=''
AND ((u.banned=$4 AND u.banned_until IS NOT DISTINCT FROM $5::timestamptz) OR ($4 AND $5::timestamptz<=clock_timestamp() AND NOT u.banned AND u.banned_until IS NULL))
AND c.account_id=$2 AND c.local_user_id=u.id AND c.assignment_version=1 AND c.auth_version=2 AND c.state='completed'
AND NOT EXISTS(SELECT 1 FROM im_tenant_media_attempts WHERE local_user_id=u.id))`, w.id, w.account, w.fingerprint, w.banned, w.until).Scan(&valid)
	if e != nil {
		return ErrImportUnavailable
	}
	if !valid {
		return ErrImportConflict
	}
	if _, e = tx.Exec(ctx, `UPDATE im_tenant_legacy_imports SET state='completed',updated_at=now() WHERE operation_id=$1`, w.id); e != nil {
		return ErrImportUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrImportUnavailable
	}
	return nil
}
func finishTarget(ctx context.Context, p *pgxpool.Pool, w importWork) error {
	tx, e := p.Begin(ctx)
	if e != nil {
		return ErrImportUnavailable
	}
	defer tx.Rollback(ctx)
	if e = targetMaintenance(ctx, tx, w.tenant, w.realm); e != nil {
		return e
	}
	var externallyBlocked bool
	e = tx.QueryRow(ctx, `SELECT globally_blocked FROM platform_accounts WHERE id=$1 AND tenant_id=$2 AND local_user_id=$3 AND assignment_version=1 AND auth_version=2 AND state='provisioning' AND NOT credentials_pending FOR UPDATE`, w.account, w.tenant, w.uid).Scan(&externallyBlocked)
	if e != nil {
		return ErrImportConflict
	}
	var now time.Time
	if tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
		return ErrImportUnavailable
	}
	legacyBlocked := w.banned && (w.until == nil || w.until.After(now))
	blocked := externallyBlocked || legacyBlocked
	tag, e := tx.Exec(ctx, `UPDATE platform_legacy_import_items SET step='completed',lease_id=NULL,lease_until=NULL,error_code='',updated_at=now() WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND step='revoked'`, w.id, w.lease)
	if e != nil || tag.RowsAffected() != 1 {
		return ErrImportUnavailable
	}
	if _, e = tx.Exec(ctx, `UPDATE platform_accounts SET globally_blocked=$2,state=CASE WHEN $2 THEN 'blocked' ELSE 'active' END,updated_at=now() WHERE id=$1`, w.account, blocked); e != nil {
		return ErrImportUnavailable
	}
	if legacyBlocked && w.until != nil && !externallyBlocked {
		if _, e = tx.Exec(ctx, `INSERT INTO platform_legacy_ban_expiries(account_id,request_id,expires_at,expected_auth_version) VALUES($1,$2,$3,2)`, w.account, w.id, w.until); e != nil {
			return ErrImportUnavailable
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,tenant_id,job_id,metadata) VALUES('system:legacy-import','legacy_import.identity.completed',$1,$2,$3,jsonb_build_object('localUserId',$4::text,'blocked',$5::boolean,'authVersion',2))`, w.account, w.tenant, w.batch, w.uid, blocked); e != nil {
		return ErrImportUnavailable
	}
	if _, e = tx.Exec(ctx, `UPDATE platform_legacy_import_batches b SET state='completed',updated_at=now() WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM platform_legacy_import_items WHERE batch_id=b.id AND step<>'completed')`, w.batch); e != nil {
		return ErrImportUnavailable
	}
	if tx.Commit(ctx) != nil {
		return ErrImportUnavailable
	}
	return nil
}
