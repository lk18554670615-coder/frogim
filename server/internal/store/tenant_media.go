package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"

	"github.com/linli/im/server/internal/tenancy"
)

var ErrTenantMediaUnconfirmed = errors.New("enterprise media handshake not confirmed")

// BeginTenantMediaAttempt repeats identity validation while holding a row lock.
// This is essential even with the outer advisory fence: losing that connection
// must not let a delayed request start after revocation has already committed.
func (p *Postgres) BeginTenantMediaAttempt(ctx context.Context, i tenancy.Identity, authVersion, realmVersion int64) (string, error) {
	if i.Validate() != nil || authVersion < 1 || realmVersion < 1 {
		return "", ErrForbidden
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var realm int64
	if err = tx.QueryRow(ctx, `SELECT access_version FROM im_tenant_identity WHERE tenant_id=$1 AND access_enabled FOR SHARE`, i.TenantID).Scan(&realm); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrForbidden
		}
		return "", err
	}
	if realm != realmVersion {
		return "", ErrForbidden
	}
	if err = tenantMatches(ctx, tx, i.TenantID); err != nil {
		return "", err
	}
	var account, state string
	var assignment, version int64
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT COALESCE(platform_account_id,''),local_identity_state,assignment_version,platform_auth_version,NOT banned AND deleted_at IS NULL FROM im_users WHERE id=$1 FOR UPDATE`, i.LocalUserID).Scan(&account, &state, &assignment, &version, &allowed); err != nil {
		return "", err
	}
	if !allowed || state != "active" || account != i.AccountID || assignment != i.AssignmentVersion || version != authVersion {
		return "", ErrForbidden
	}
	// An uncertain connection is not retried into another potentially live one.
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_tenant_media_attempts WHERE local_user_id=$1)`, i.LocalUserID).Scan(&pending); err != nil {
		return "", err
	}
	if pending {
		return "", ErrTenantMediaUnconfirmed
	}
	id, err := tenancy.Secret()
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_tenant_media_attempts(id,local_user_id,assignment_version,auth_version) VALUES($1,$2,$3,$4)`, id, i.LocalUserID, i.AssignmentVersion, authVersion); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// In the live request path, only a confirmed HTTP 101 upstream response may
// clear this barrier. Network errors, cancellation, process death and database
// failures leave it intact. The separate operator-only RepairColdMedia path
// requires a paused realm and all original writers/LiveKit stopped, and audits
// the clearance atomically; it is not a timeout cleanup or an HTTP bypass.
func (p *Postgres) CompleteTenantMediaAttempt(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM im_tenant_media_attempts WHERE id=$1`, id)
	return err
}

func (p *Postgres) CheckTenantMediaSettled(ctx context.Context, user string) error {
	var pending bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_tenant_media_attempts WHERE local_user_id=$1)`, user).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrTenantMediaUnconfirmed
	}
	return nil
}
