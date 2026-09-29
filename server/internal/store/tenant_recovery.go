package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

// Inventory is read-only, uses one MVCC snapshot and requires an independently
// selected, fully suspended realm. It never suspends a tenant, clears ambiguous
// handshakes, changes local roles or lets a stale platform grant activate users.
func (p *Postgres) TenantRecoveryInventory(ctx context.Context, address string, request tenancy.RecoveryInventoryRequest) (tenancy.RecoveryInventoryPage, error) {
	var empty tenancy.RecoveryInventoryPage
	if !request.Valid() {
		return empty, tenancy.ErrInvalid
	}
	tx, e := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return empty, e
	}
	defer tx.Rollback(ctx)
	r := tenancy.RecoveryInventory{Version: 1, TenantID: request.TenantID, HTTPBaseURL: address, Users: []tenancy.RecoveryIdentity{}}
	e = tx.QueryRow(ctx, `SELECT access_version,access_enabled,EXISTS(SELECT 1 FROM im_tenant_realm_operations o WHERE o.access_version=t.access_version AND NOT o.enabled AND o.state='completed') FROM im_tenant_identity t WHERE tenant_id=$1`, request.TenantID).Scan(&r.Realm.Version, &r.Realm.Enabled, &r.Realm.SuspensionConfirmed)
	if e != nil || r.Realm.Version != request.RealmVersion || r.Realm.Enabled || !r.Realm.SuspensionConfirmed {
		return empty, ErrConflict
	}
	e = tx.QueryRow(ctx, `SELECT
(SELECT count(*) FROM im_users WHERE platform_account_id IS NULL AND local_identity_state<>'retired' AND deleted_at IS NULL),
(SELECT count(*) FROM im_tenant_operations WHERE action='revoke' AND state<>'revoked'),
(SELECT count(*) FROM im_tenant_credential_operations WHERE state<>'completed'),
(SELECT count(*) FROM im_tenant_access_operations WHERE state<>'completed'),
(SELECT count(*) FROM im_tenant_realm_operations WHERE state<>'completed'),
(SELECT count(*) FROM im_tenant_legacy_imports WHERE state<>'completed'),
(SELECT count(*) FROM im_tenant_media_attempts)`).Scan(&r.UnlinkedUsers, &r.Pending.Revocations, &r.Pending.Credentials, &r.Pending.Access, &r.Pending.Realm, &r.Pending.Imports, &r.Pending.Media)
	if e != nil {
		return empty, e
	}
	rows, e := tx.Query(ctx, `SELECT platform_account_id,id,assignment_version,platform_auth_version,phone,local_identity_state,banned,deleted_at IS NOT NULL FROM im_users WHERE platform_account_id IS NOT NULL ORDER BY id COLLATE "C" LIMIT $1`, tenancy.RecoveryInventoryMaximum+1)
	if e != nil {
		return empty, e
	}
	for rows.Next() {
		u := tenancy.RecoveryIdentity{}
		u.Identity.TenantID = request.TenantID
		if e = rows.Scan(&u.Identity.AccountID, &u.Identity.LocalUserID, &u.Identity.AssignmentVersion, &u.AuthVersion, &u.Phone, &u.State, &u.EnterpriseBanned, &u.Deleted); e != nil {
			rows.Close()
			return empty, e
		}
		r.Users = append(r.Users, u)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return empty, e
	}
	out, e := r.Page(request)
	if e != nil {
		return empty, e
	}
	if e = tx.Commit(ctx); e != nil {
		return empty, e
	}
	return out, nil
}
