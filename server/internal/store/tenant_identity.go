package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

func (p *Postgres) TenantReadiness(ctx context.Context, tenant string) (int, error) {
	var version int
	err := p.pool.QueryRow(ctx, `SELECT COALESCE((SELECT MAX(version) FROM im_schema_migrations),0) FROM im_tenant_identity WHERE singleton AND tenant_id=$1`, tenant).Scan(&version)
	return version, err
}

func (p *Postgres) verifyDatabaseRealm(ctx context.Context, tenant string) error {
	var platform, hasBinding bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_tables WHERE tablename='platform_accounts'),to_regclass('im_tenant_identity') IS NOT NULL`).Scan(&platform, &hasBinding); err != nil {
		return err
	}
	if platform {
		return errors.New("business server cannot use a platform database")
	}
	if hasBinding {
		var bound string
		err := p.pool.QueryRow(ctx, `SELECT tenant_id FROM im_tenant_identity WHERE singleton`).Scan(&bound)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && bound != tenant {
			return errors.New("configured enterprise does not match database identity")
		}
	}
	return nil
}

// Refuse implicit adoption before running even an additive migration against a
// populated standalone database. BindTenant repeats the check under a lock.
func (p *Postgres) verifyTenantAdoption(ctx context.Context, tenant string, allowExisting bool) error {
	if tenant == "" || allowExisting {
		return nil
	}
	var hasUsers, hasBinding bool
	if err := p.pool.QueryRow(ctx, `SELECT to_regclass('im_users') IS NOT NULL,to_regclass('im_tenant_identity') IS NOT NULL`).Scan(&hasUsers, &hasBinding); err != nil {
		return err
	}
	if !hasUsers {
		return nil
	}
	if hasBinding {
		var bound bool
		if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_tenant_identity WHERE tenant_id=$1)`, tenant).Scan(&bound); err != nil {
			return err
		}
		if bound {
			return nil
		}
	}
	var populated bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users)`).Scan(&populated); err != nil {
		return err
	}
	if populated {
		return errors.New("populated database requires explicit enterprise adoption before migration")
	}
	return nil
}

// BindTenant never changes an existing binding. Adopting a populated standalone
// database requires an explicit migration option, not just a mistyped env var.
func (p *Postgres) BindTenant(ctx context.Context, tenant string, allowExisting bool) error {
	if tenant == "" {
		return p.verifyDatabaseRealm(ctx, tenant)
	}
	if !tenancy.ValidID(tenant) {
		return tenancy.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739174)`); err != nil {
		return err
	}
	var bound string
	err = tx.QueryRow(ctx, `SELECT tenant_id FROM im_tenant_identity WHERE singleton`).Scan(&bound)
	if err == nil {
		if bound != tenant {
			return errors.New("enterprise database identity mismatch")
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var populated bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users)`).Scan(&populated); err != nil {
		return err
	}
	if populated && !allowExisting {
		return errors.New("populated database requires explicit enterprise adoption")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_tenant_identity(singleton,tenant_id) VALUES(true,$1)`, tenant); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type TenantProvision struct {
	OperationID                                     string
	Identity                                        tenancy.Identity
	Phone, Name, Gender, Method, PersonalInviteCode string
	PasswordRuneCount                               int
}

func tenantMatches(ctx context.Context, tx pgx.Tx, tenant string) error {
	var match bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_tenant_identity WHERE tenant_id=$1)`, tenant).Scan(&match); err != nil {
		return err
	}
	if !match {
		return ErrForbidden
	}
	return nil
}

// PrepareTenantIdentity is idempotent and creates an inert identity. Only a
// valid consumed platform grant may activate it. Referral policy is rechecked
// inside the same transaction as local creation, not trusted from the client.
func (p *Postgres) PrepareTenantIdentity(ctx context.Context, in TenantProvision) error {
	if in.Identity.Validate() != nil || !tenancy.ValidID(in.OperationID) || strings.TrimSpace(in.Phone) == "" || len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 40 {
		return tenancy.ErrInvalid
	}
	if in.Method != "password" && in.Method != "otp" && in.Method != "admin" && in.Method != "transfer" {
		return tenancy.ErrInvalid
	}
	if in.Gender == "" {
		in.Gender = "unspecified"
	}
	if in.Gender != "unspecified" && in.Gender != "male" && in.Gender != "female" {
		return tenancy.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = tenantMatches(ctx, tx, in.Identity.TenantID); err != nil {
		return err
	}
	// A stable generation lock serializes duplicate prepare attempts even before
	// the local user row exists. Hash collisions only serialize unrelated work.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,73))`, in.Identity.AccountID); err != nil {
		return err
	}
	var existing tenancy.Identity
	existing.TenantID = in.Identity.TenantID
	var state string
	err = tx.QueryRow(ctx, `SELECT account_id,local_user_id,assignment_version,state FROM im_tenant_operations WHERE operation_id=$1 AND action='prepare'`, in.OperationID).Scan(&existing.AccountID, &existing.LocalUserID, &existing.AssignmentVersion, &state)
	if err == nil {
		if existing != in.Identity || state != "prepared" {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	mode := "optional"
	enabled := true
	minimum := 8
	rows, err := tx.Query(ctx, `SELECT key,value FROM im_settings WHERE key IN ('registrationEnabled','allowRegistration','inviteRegistrationMode','passwordMinLength') FOR SHARE`)
	if err != nil {
		return err
	}
	settings := map[string]json.RawMessage{}
	for rows.Next() {
		var key string
		var value []byte
		if err = rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		settings[key] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if raw, ok := settings["registrationEnabled"]; ok {
		if json.Unmarshal(raw, &enabled) != nil {
			return tenancy.ErrInvalid
		}
	} else if raw, ok := settings["allowRegistration"]; ok {
		if json.Unmarshal(raw, &enabled) != nil {
			return tenancy.ErrInvalid
		}
	}
	if raw, ok := settings["inviteRegistrationMode"]; ok {
		if json.Unmarshal(raw, &mode) != nil {
			return tenancy.ErrInvalid
		}
	}
	if raw, ok := settings["passwordMinLength"]; ok {
		if json.Unmarshal(raw, &minimum) != nil {
			return tenancy.ErrInvalid
		}
	}
	minimum = max(8, min(16, minimum))
	if (in.Method == "password" || in.Method == "admin") && in.PasswordRuneCount < minimum {
		return &tenancy.OperationRejected{Code: "TENANT_PASSWORD_POLICY_REJECTED"}
	}
	var inviter *InviteCode
	if in.Method != "admin" && in.Method != "transfer" {
		if !enabled {
			return &tenancy.OperationRejected{Code: "TENANT_REGISTRATION_DISABLED"}
		}
		code := strings.ToUpper(strings.TrimSpace(in.PersonalInviteCode))
		if err = validateInviteMode(mode, code); err != nil {
			return err
		}
		if mode != "disabled" && code != "" {
			inviter, err = resolveInviteCodeTx(ctx, tx, code)
			if err != nil {
				return err
			}
		}
	}
	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `INSERT INTO im_users(id,phone,name,handle,created_at,platform_account_id,assignment_version,local_identity_state,gender) VALUES($1,$2,$3,'gg_'||left(md5($1),20),$4,$5,$6,'prepared',$7)`, in.Identity.LocalUserID, in.Phone, in.Name, now, in.Identity.AccountID, in.Identity.AssignmentVersion, in.Gender); err != nil {
		return err
	}
	if _, err = ensureInviteCodeTx(ctx, tx, in.Identity.LocalUserID, "system", "platform", now); err != nil {
		return err
	}
	if inviter != nil {
		if _, err = tx.Exec(ctx, `INSERT INTO im_user_invite_relations(invitee_user_id,inviter_user_id,invite_code_id,registration_method,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$5)`, in.Identity.LocalUserID, inviter.UserID, inviter.ID, in.Method, now); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_tenant_operations(operation_id,action,account_id,local_user_id,assignment_version,state) VALUES($1,'prepare',$2,$3,$4,'prepared')`, in.OperationID, in.Identity.AccountID, in.Identity.LocalUserID, in.Identity.AssignmentVersion); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"accountId": in.Identity.AccountID, "assignmentVersion": in.Identity.AssignmentVersion, "operationId": in.OperationID})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,'platform','identity.prepared','user',$2,$3,$4)`, "aud_tenant_"+in.OperationID, in.Identity.LocalUserID, metadata, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ActivateTenantIdentity(ctx context.Context, i tenancy.Identity) error {
	if i.Validate() != nil {
		return tenancy.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = tenantMatches(ctx, tx, i.TenantID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE im_users SET local_identity_state='active' WHERE id=$1 AND platform_account_id=$2 AND assignment_version=$3 AND local_identity_state IN ('prepared','active') AND NOT banned AND deleted_at IS NULL`, i.LocalUserID, i.AccountID, i.AssignmentVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrForbidden
	}
	return tx.Commit(ctx)
}

func (p *Postgres) TenantIdentity(ctx context.Context, tenant, localUser string) (tenancy.Identity, error) {
	i := tenancy.Identity{TenantID: tenant, LocalUserID: localUser}
	err := p.pool.QueryRow(ctx, `SELECT u.platform_account_id,u.assignment_version FROM im_users u JOIN im_tenant_identity t ON t.tenant_id=$1 AND t.access_enabled WHERE u.id=$2 AND u.platform_account_id IS NOT NULL AND u.local_identity_state='active' AND NOT u.banned AND u.deleted_at IS NULL`, tenant, localUser).Scan(&i.AccountID, &i.AssignmentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrForbidden
	}
	return i, err
}

// Preflight is read-only and keeps the source account usable when a group must
// first be transferred. Revocation still repeats the check under its own lock.
func (p *Postgres) CheckTenantTransfer(ctx context.Context, i tenancy.Identity) error {
	actual, err := p.TenantIdentity(ctx, i.TenantID, i.LocalUserID)
	if err != nil {
		return err
	}
	if actual != i {
		return ErrForbidden
	}
	var owner bool
	if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_members m JOIN im_groups g ON g.conversation_id=m.conversation_id WHERE m.user_id=$1 AND m.role='owner' AND g.dissolved_at IS NULL)`, i.LocalUserID).Scan(&owner); err != nil {
		return err
	}
	if owner {
		return &tenancy.OperationRejected{Code: "GROUP_OWNERSHIP_TRANSFER_REQUIRED"}
	}
	return nil
}

// BeginTenantRevocation permanently fences local reads/writes before contacting
// IM. Retries keep the old identity retired; they never roll it back to active.
func (p *Postgres) BeginTenantRevocation(ctx context.Context, operation string, i tenancy.Identity) error {
	if i.Validate() != nil || !tenancy.ValidID(operation) {
		return tenancy.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = tenantMatches(ctx, tx, i.TenantID); err != nil {
		return err
	}
	var account, state string
	var version int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(platform_account_id,''),assignment_version,local_identity_state FROM im_users WHERE id=$1 FOR UPDATE`, i.LocalUserID).Scan(&account, &version, &state); err != nil {
		return err
	}
	if account != i.AccountID || version != i.AssignmentVersion {
		return ErrForbidden
	}
	var owner bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_members m JOIN im_groups g ON g.conversation_id=m.conversation_id WHERE m.user_id=$1 AND m.role='owner' AND g.dissolved_at IS NULL)`, i.LocalUserID).Scan(&owner); err != nil {
		return err
	}
	if owner {
		return &tenancy.OperationRejected{Code: "GROUP_OWNERSHIP_TRANSFER_REQUIRED"}
	}
	var previousAccount, previousUser string
	var previousVersion int64
	err = tx.QueryRow(ctx, `SELECT account_id,local_user_id,assignment_version FROM im_tenant_operations WHERE operation_id=$1 AND action='revoke'`, operation).Scan(&previousAccount, &previousUser, &previousVersion)
	if err == nil && (previousAccount != i.AccountID || previousUser != i.LocalUserID || previousVersion != i.AssignmentVersion) {
		return ErrConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_users SET local_identity_state='retired',updated_at=now() WHERE id=$1`, i.LocalUserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`, i.LocalUserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_devices SET notifications_enabled=false,push_token='' WHERE user_id=$1`, i.LocalUserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_wukong_credentials WHERE user_id=$1`, i.LocalUserID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_tenant_operations(operation_id,action,account_id,local_user_id,assignment_version,state) VALUES($1,'revoke',$2,$3,$4,'revoking') ON CONFLICT(operation_id,action) DO NOTHING`, operation, i.AccountID, i.LocalUserID, i.AssignmentVersion); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"accountId": i.AccountID, "assignmentVersion": i.AssignmentVersion, "operationId": operation})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,'platform','identity.retired','user',$2,$3,now()) ON CONFLICT(id) DO NOTHING`, "aud_tenant_revoke_"+operation, i.LocalUserID, metadata); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) FinishTenantRevocation(ctx context.Context, op string, i tenancy.Identity) error {
	tag, err := p.pool.Exec(ctx, `UPDATE im_tenant_operations SET state='revoked',updated_at=now() WHERE operation_id=$1 AND action='revoke' AND account_id=$2 AND local_user_id=$3 AND assignment_version=$4 AND EXISTS(SELECT 1 FROM im_users WHERE id=$3 AND local_identity_state='retired') AND NOT EXISTS(SELECT 1 FROM im_tenant_media_attempts WHERE local_user_id=$3)`, op, i.AccountID, i.LocalUserID, i.AssignmentVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// WithTenantSessionFence serializes session/IM issuance with revocation across
// all API replicas. Use a session-level lock, not an idle transaction held
// across IM network requests (the database terminates idle transactions).
// The dedicated connection is never returned to the pool with a retained lock.
func (p *Postgres) WithTenantSessionFence(ctx context.Context, localUser string, fn func() error) error {
	return p.withTenantRealmFence(ctx, false, func() error { return p.withTenantUserFence(ctx, localUser, fn) })
}
func (p *Postgres) withTenantUserFence(ctx context.Context, localUser string, fn func() error) error {
	connection, err := p.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err = connection.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,74))`, localUser); err != nil {
		// A cancelled query may have acquired the lock before cancellation was
		// observed. Discard the connection instead of risking a pooled lock leak.
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = connection.Conn().Close(cleanup)
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var unlocked bool
		if err := connection.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,74))`, localUser).Scan(&unlocked); err != nil || !unlocked {
			_ = connection.Conn().Close(cleanup)
		}
	}()
	return fn()
}
