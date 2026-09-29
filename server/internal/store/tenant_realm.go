package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
	"time"
)

func (p *Postgres) TenantRealmVersion(ctx context.Context, tenant string) (int64, error) {
	var v int64
	err := p.pool.QueryRow(ctx, `SELECT access_version FROM im_tenant_identity WHERE tenant_id=$1 AND access_enabled`, tenant).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrForbidden
	}
	return v, err
}

func (p *Postgres) TenantRealmSnapshot(ctx context.Context, tenant string) (*tenancy.RealmSnapshot, error) {
	var r tenancy.RealmSnapshot
	err := p.pool.QueryRow(ctx, `SELECT access_version,access_enabled,EXISTS(SELECT 1 FROM im_tenant_realm_operations o
 WHERE o.access_version=t.access_version AND NOT o.enabled AND o.state='completed') FROM im_tenant_identity t WHERE tenant_id=$1`, tenant).Scan(&r.Version, &r.Enabled, &r.SuspensionConfirmed)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Session issuance holds the shared realm fence before its user fence. Pausing
// only needs the exclusive fence while persisting the gate; disconnections are
// batched afterwards. Never hold this lock for an entire live media connection.
func (p *Postgres) withTenantRealmFence(ctx context.Context, exclusive bool, fn func() error) error {
	c, err := p.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer c.Release()
	lock, unlock := "SELECT pg_advisory_lock_shared(490739177)", "SELECT pg_advisory_unlock_shared(490739177)"
	if exclusive {
		lock, unlock = "SELECT pg_advisory_lock(490739177)", "SELECT pg_advisory_unlock(490739177)"
	}
	if _, err = c.Exec(ctx, lock); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = c.Conn().Close(cleanup)
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var done bool
		if e := c.QueryRow(cleanup, unlock).Scan(&done); e != nil || !done {
			_ = c.Conn().Close(cleanup)
		}
	}()
	return fn()
}
func (p *Postgres) WithTenantRealmFence(ctx context.Context, fn func() error) error {
	return p.withTenantRealmFence(ctx, true, fn)
}

func (p *Postgres) BeginTenantRealm(ctx context.Context, op tenancy.RealmOperation) (bool, error) {
	if !tenancy.ValidID(op.OperationID) || !tenancy.ValidID(op.TenantID) || op.Version < 2 {
		return false, tenancy.ErrInvalid
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var version int64
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT access_version,access_enabled FROM im_tenant_identity WHERE tenant_id=$1 FOR UPDATE`, op.TenantID).Scan(&version, &enabled); err != nil {
		return false, err
	}
	var priorVersion int64
	var priorEnabled bool
	var state string
	err = tx.QueryRow(ctx, `SELECT access_version,enabled,state FROM im_tenant_realm_operations WHERE operation_id=$1`, op.OperationID).Scan(&priorVersion, &priorEnabled, &state)
	if err == nil {
		if priorVersion != op.Version || priorEnabled != op.Enabled {
			return false, ErrConflict
		}
		if state == "completed" {
			return true, tx.Commit(ctx)
		}
		if version != op.Version || enabled {
			return false, ErrConflict
		}
		return false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if version+1 != op.Version || enabled == op.Enabled {
		return false, ErrConflict
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_tenant_realm_operations WHERE state<>'completed')`).Scan(&pending); err != nil {
		return false, err
	}
	if pending {
		return false, ErrConflict
	}
	if op.Enabled {
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_tenant_legacy_imports WHERE state<>'completed')`).Scan(&pending); err != nil {
			return false, err
		}
		if pending {
			return false, ErrConflict
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE im_tenant_identity SET access_enabled=false,access_version=$1 WHERE singleton`, op.Version); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_tenant_realm_operations(operation_id,access_version,enabled,state) VALUES($1,$2,$3,'revoking')`, op.OperationID, op.Version, op.Enabled); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_tenant_realm_targets(operation_id,local_user_id) SELECT $1,id FROM im_users`, op.OperationID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE im_refresh_sessions SET revoked_at=COALESCE(revoked_at,now()); UPDATE im_devices SET notifications_enabled=false,push_token=''; DELETE FROM im_wukong_credentials;`); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,'platform','tenant.access.started','tenant',$2,jsonb_build_object('version',$3::bigint,'enabled',$4::boolean),now())`, "aud_realm_"+op.OperationID, op.TenantID, op.Version, op.Enabled); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}
func (p *Postgres) TenantRealmTargets(ctx context.Context, op tenancy.RealmOperation) ([]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT t.local_user_id FROM im_tenant_realm_targets t JOIN im_tenant_realm_operations o USING(operation_id) WHERE t.operation_id=$1 AND NOT t.completed AND o.state='revoking' AND o.access_version=$2 AND o.enabled=$3 ORDER BY t.local_user_id LIMIT 10`, op.OperationID, op.Version, op.Enabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []string{}
	for rows.Next() {
		var user string
		if err = rows.Scan(&user); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}
func (p *Postgres) CompleteTenantRealmTarget(ctx context.Context, op tenancy.RealmOperation, user string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE im_tenant_realm_targets SET completed=true WHERE operation_id=$1 AND local_user_id=$2 AND EXISTS(SELECT 1 FROM im_tenant_identity WHERE tenant_id=$3 AND access_version=$4 AND NOT access_enabled) AND NOT EXISTS(SELECT 1 FROM im_tenant_media_attempts WHERE local_user_id=$2)`, op.OperationID, user, op.TenantID, op.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
func (p *Postgres) FinishTenantRealm(ctx context.Context, op tenancy.RealmOperation) (int, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var version int64
	if err = tx.QueryRow(ctx, `SELECT access_version FROM im_tenant_identity WHERE tenant_id=$1 AND NOT access_enabled FOR UPDATE`, op.TenantID).Scan(&version); err != nil {
		return 0, err
	}
	if version != op.Version {
		return 0, ErrConflict
	}
	var remaining int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_tenant_realm_targets WHERE operation_id=$1 AND NOT completed`, op.OperationID).Scan(&remaining); err != nil {
		return 0, err
	}
	if remaining > 0 {
		return remaining, tx.Commit(ctx)
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_tenant_media_attempts)`).Scan(&pending); err != nil {
		return 0, err
	}
	if pending {
		return 0, ErrTenantMediaUnconfirmed
	}
	tag, err := tx.Exec(ctx, `UPDATE im_tenant_realm_operations SET state='completed',updated_at=now() WHERE operation_id=$1 AND access_version=$2 AND enabled=$3 AND state='revoking'`, op.OperationID, op.Version, op.Enabled)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() != 1 {
		return 0, ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE im_tenant_identity SET access_enabled=$1 WHERE singleton`, op.Enabled); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,'platform','tenant.access.completed','tenant',$2,jsonb_build_object('version',$3::bigint,'enabled',$4::boolean),now())`, "aud_realm_done_"+op.OperationID, op.TenantID, op.Version, op.Enabled); err != nil {
		return 0, err
	}
	return 0, tx.Commit(ctx)
}
