package platform

import (
	"context"
	"net/http"

	"github.com/linli/im/server/internal/tenancy"
)

func (e EnterpriseRPC) CheckReadiness(ctx context.Context, tenant, nonce string) (tenancy.Readiness, error) {
	var result tenancy.Readiness
	peer := e.Peers[tenant]
	if peer == nil {
		return result, ErrUnavailable
	}
	err := peer.Call(ctx, "/internal/tenancy/readiness", map[string]string{"nonce": nonce}, &result)
	return result, err
}

// Activation never edits routing, bypasses a missing control peer, or accepts
// a client-supplied health report. Network checks happen before the transaction;
// its compare-and-swap prevents activating a concurrently changed definition.
func (s *Store) ActivateTenant(ctx context.Context, id, actor, reason string, confirmed bool, version int64, check func(context.Context, string, string) (tenancy.Readiness, error)) error {
	if !tenancy.ValidID(id) || actor == "" || !adminReason(reason, confirmed) || version < 1 || check == nil {
		return tenancy.ErrInvalid
	}
	var address, state string
	var actual, accessVersion int64
	var archived bool
	if err := s.pool.QueryRow(ctx, `SELECT http_base_url,status,config_version,access_version,archived_at IS NOT NULL FROM platform_tenants WHERE id=$1`, id).Scan(&address, &state, &actual, &accessVersion, &archived); err != nil {
		return err
	}
	if archived || actual != version || (state != "provisioning" && state != "active") {
		return ErrConflict
	}
	nonce, err := tenancy.Secret()
	if err != nil {
		return err
	}
	report, err := check(ctx, id, nonce)
	if err != nil || !report.Valid(nonce, id, address) {
		return ErrUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedAddress string
	var lockedAccessVersion int64
	if err = tx.QueryRow(ctx, `SELECT http_base_url,status,config_version,access_version,archived_at IS NOT NULL FROM platform_tenants WHERE id=$1 FOR UPDATE`, id).Scan(&lockedAddress, &state, &actual, &lockedAccessVersion, &archived); err != nil {
		return err
	}
	if archived || actual != version || lockedAddress != address || lockedAccessVersion != accessVersion || (state != "provisioning" && state != "active") {
		return ErrConflict
	}
	if err = noPendingDeployment(ctx, tx, id); err != nil {
		return err
	}
	// Healthy containers do not imply an open business realm. Activation is not
	// a resume operation and must never mask a missing/stale suspension gate.
	if report.Realm == nil || report.Realm.Version != accessVersion || !report.Realm.Enabled || report.Realm.SuspensionConfirmed {
		return ErrUnavailable
	}
	if state == "active" {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_tenants SET status='active',updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,reason,metadata) VALUES($1,'tenant.activated',$2,$3,jsonb_build_object('configVersion',$4::bigint,'schemaVersion',$5::integer,'accessVersion',$6::bigint,'verification','mutual-tls-runtime-readiness'))`, actor, id, reason, version, report.SchemaVersion, accessVersion); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *API) adminActivateTenant(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ExpectedConfigVersion int64
		Reason                string
		Confirmed             bool
	}
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if err := a.Store.ActivateTenant(r.Context(), r.PathValue("id"), actorID(r), p.Reason, p.Confirmed, p.ExpectedConfigVersion, a.Peers.CheckReadiness); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": "active"})
}
