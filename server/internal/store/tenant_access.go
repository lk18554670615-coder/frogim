package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

// The caller holds the shared session fence through freeze, IM/media removal
// and completion. Global unblocking never modifies enterprise ban/role/history.
func (p *Postgres) BeginTenantAccess(ctx context.Context, op tenancy.AccessOperation) (bool, error) {
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
	if account != op.Identity.AccountID || assignment != op.Identity.AssignmentVersion || state == "retired" {
		return false, ErrForbidden
	}
	var old tenancy.AccessOperation
	var previousState string
	old.Identity.TenantID = op.Identity.TenantID
	err = tx.QueryRow(ctx, `SELECT operation_id,account_id,local_user_id,assignment_version,auth_version,blocked,state FROM im_tenant_access_operations WHERE operation_id=$1`, op.OperationID).Scan(&old.OperationID, &old.Identity.AccountID, &old.Identity.LocalUserID, &old.Identity.AssignmentVersion, &old.AuthVersion, &old.Blocked, &previousState)
	if err == nil {
		if old != op {
			return false, ErrConflict
		}
		if previousState == "completed" {
			return true, tx.Commit(ctx)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	} else if version >= op.AuthVersion || state == "credentials_resetting" {
		return false, ErrConflict
	}
	if version > op.AuthVersion {
		return false, ErrForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE im_users SET platform_auth_version=$2,local_identity_state='platform_blocked',updated_at=now() WHERE id=$1`, op.Identity.LocalUserID, op.AuthVersion); err != nil {
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
	if _, err = tx.Exec(ctx, `INSERT INTO im_tenant_access_operations(operation_id,account_id,local_user_id,assignment_version,auth_version,blocked,state) VALUES($1,$2,$3,$4,$5,$6,'applying') ON CONFLICT(operation_id) DO NOTHING`, op.OperationID, op.Identity.AccountID, op.Identity.LocalUserID, op.Identity.AssignmentVersion, op.AuthVersion, op.Blocked); err != nil {
		return false, err
	}
	metadata, _ := json.Marshal(map[string]any{"operationId": op.OperationID, "authVersion": op.AuthVersion, "blocked": op.Blocked})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,'platform','identity.global_access.requested','user',$2,$3,now()) ON CONFLICT(id) DO NOTHING`, "aud_access_"+op.OperationID, op.Identity.LocalUserID, metadata); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (p *Postgres) FinishTenantAccess(ctx context.Context, op tenancy.AccessOperation) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	state := "active"
	if op.Blocked {
		state = "platform_blocked"
	}
	tag, err := tx.Exec(ctx, `UPDATE im_users SET local_identity_state=$5,updated_at=now() WHERE id=$1 AND platform_account_id=$2 AND assignment_version=$3 AND platform_auth_version=$4 AND local_identity_state='platform_blocked' AND NOT EXISTS(SELECT 1 FROM im_tenant_media_attempts WHERE local_user_id=$1)`, op.Identity.LocalUserID, op.Identity.AccountID, op.Identity.AssignmentVersion, op.AuthVersion, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	tag, err = tx.Exec(ctx, `UPDATE im_tenant_access_operations SET state='completed',updated_at=now() WHERE operation_id=$1 AND account_id=$2 AND local_user_id=$3 AND assignment_version=$4 AND auth_version=$5 AND blocked=$6 AND state='applying'`, op.OperationID, op.Identity.AccountID, op.Identity.LocalUserID, op.Identity.AssignmentVersion, op.AuthVersion, op.Blocked)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
