package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

func (p *Postgres) TenantAuthIdentity(ctx context.Context, tenant, uid string) (tenancy.Identity, int64, error) {
	i := tenancy.Identity{TenantID: tenant, LocalUserID: uid}
	var version int64
	err := p.pool.QueryRow(ctx, `SELECT u.platform_account_id,u.assignment_version,u.platform_auth_version FROM im_users u JOIN im_tenant_identity t ON t.tenant_id=$1 AND t.access_enabled WHERE u.id=$2 AND u.platform_account_id IS NOT NULL AND u.local_identity_state='active' AND NOT u.banned AND u.deleted_at IS NULL`, tenant, uid).Scan(&i.AccountID, &i.AssignmentVersion, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrForbidden
	}
	return i, version, err
}

func (p *Postgres) ActivateTenantGrant(ctx context.Context, g tenancy.Grant) error {
	if g.Identity.Validate() != nil || g.AuthVersion < 1 || g.RealmVersion < 1 {
		return tenancy.ErrInvalid
	}
	tag, err := p.pool.Exec(ctx, `UPDATE im_users SET local_identity_state='active',platform_auth_version=$4 WHERE id=$1 AND platform_account_id=$2 AND assignment_version=$3 AND platform_auth_version<=$4 AND local_identity_state IN ('prepared','active') AND NOT banned AND deleted_at IS NULL AND EXISTS(SELECT 1 FROM im_tenant_identity WHERE tenant_id=$5 AND access_enabled AND access_version=$6)`, g.LocalUserID, g.AccountID, g.AssignmentVersion, g.AuthVersion, g.TenantID, g.RealmVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrForbidden
	}
	return nil
}

// Policy is checked without sending a password/hash to the enterprise.
func (p *Postgres) CheckTenantPassword(ctx context.Context, i tenancy.Identity, runes int) error {
	var minimum int
	err := p.pool.QueryRow(ctx, `SELECT COALESCE((SELECT (value#>>'{}')::int FROM im_settings WHERE key='passwordMinLength'),8) FROM im_users u WHERE u.id=$1 AND u.platform_account_id=$2 AND u.assignment_version=$3 AND u.local_identity_state IN ('prepared','active') AND NOT u.banned AND u.deleted_at IS NULL AND EXISTS(SELECT 1 FROM im_tenant_identity WHERE tenant_id=$4)`, i.LocalUserID, i.AccountID, i.AssignmentVersion, i.TenantID).Scan(&minimum)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if runes < max(8, min(16, minimum)) {
		return &tenancy.OperationRejected{Code: "TENANT_PASSWORD_POLICY_REJECTED"}
	}
	return nil
}

// Caller holds WithTenantSessionFence throughout begin, external disconnect,
// and finish. Completed replays must not revoke a newer login.
func (p *Postgres) BeginTenantCredentialRevocation(ctx context.Context, op tenancy.CredentialOperation) (bool, error) {
	if !tenancy.ValidID(op.OperationID) || op.Identity.Validate() != nil || op.AuthVersion < 2 {
		return false, tenancy.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = tenantMatches(ctx, tx, op.Identity.TenantID); err != nil {
		return false, err
	}
	var account, state string
	var assignment, version int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE(platform_account_id,''),assignment_version,platform_auth_version,local_identity_state FROM im_users WHERE id=$1 FOR UPDATE`, op.Identity.LocalUserID).Scan(&account, &assignment, &version, &state); err != nil {
		return false, err
	}
	if account != op.Identity.AccountID || assignment != op.Identity.AssignmentVersion || state == "retired" || version > op.AuthVersion {
		return false, ErrForbidden
	}
	var previous tenancy.CredentialOperation
	var previousState string
	previous.Identity.TenantID = op.Identity.TenantID
	err = tx.QueryRow(ctx, `SELECT operation_id,account_id,local_user_id,assignment_version,auth_version,state FROM im_tenant_credential_operations WHERE operation_id=$1`, op.OperationID).Scan(&previous.OperationID, &previous.Identity.AccountID, &previous.Identity.LocalUserID, &previous.Identity.AssignmentVersion, &previous.AuthVersion, &previousState)
	if err == nil {
		if previous != op {
			return false, ErrConflict
		}
		if previousState == "completed" {
			return true, tx.Commit(ctx)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	} else if version >= op.AuthVersion || state == "credentials_resetting" || state == "platform_blocked" {
		return false, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE im_users SET platform_auth_version=$2,local_identity_state='credentials_resetting',updated_at=now() WHERE id=$1`, op.Identity.LocalUserID, op.AuthVersion); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`, op.Identity.LocalUserID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_devices SET notifications_enabled=false,push_token='' WHERE user_id=$1`, op.Identity.LocalUserID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM im_wukong_credentials WHERE user_id=$1`, op.Identity.LocalUserID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_tenant_credential_operations(operation_id,account_id,local_user_id,assignment_version,auth_version,state) VALUES($1,$2,$3,$4,$5,'revoking') ON CONFLICT(operation_id) DO NOTHING`, op.OperationID, op.Identity.AccountID, op.Identity.LocalUserID, op.Identity.AssignmentVersion, op.AuthVersion); err != nil {
		return false, err
	}
	metadata, _ := json.Marshal(map[string]any{"operationId": op.OperationID, "authVersion": op.AuthVersion})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,'platform','identity.credentials.revoked','user',$2,$3,now()) ON CONFLICT(id) DO NOTHING`, "aud_creds_"+op.OperationID, op.Identity.LocalUserID, metadata); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (p *Postgres) FinishTenantCredentialRevocation(ctx context.Context, op tenancy.CredentialOperation) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE im_users SET local_identity_state='active',updated_at=now() WHERE id=$1 AND platform_account_id=$2 AND assignment_version=$3 AND platform_auth_version=$4 AND local_identity_state='credentials_resetting' AND NOT EXISTS(SELECT 1 FROM im_tenant_media_attempts WHERE local_user_id=$1)`, op.Identity.LocalUserID, op.Identity.AccountID, op.Identity.AssignmentVersion, op.AuthVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	tag, err = tx.Exec(ctx, `UPDATE im_tenant_credential_operations SET state='completed',updated_at=now() WHERE operation_id=$1 AND account_id=$2 AND local_user_id=$3 AND assignment_version=$4 AND auth_version=$5 AND state='revoking'`, op.OperationID, op.Identity.AccountID, op.Identity.LocalUserID, op.Identity.AssignmentVersion, op.AuthVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
