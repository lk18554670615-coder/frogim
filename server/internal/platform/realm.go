package platform

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
	"strings"
	"time"
)

type RealmJob struct {
	ID        string `json:"jobId"`
	RequestID string `json:"requestId"`
	TenantID  string `json:"tenantId"`
	Enabled   bool   `json:"enabled"`
	Version   int64  `json:"accessVersion"`
	Status    string `json:"status"`
	Remaining int    `json:"remaining"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// The directory gate closes atomically with the job. The enterprise gate is
// acknowledged asynchronously; neither suspension nor resumption is claimed
// complete while its existing connections remain unconfirmed.
func (s *Store) RequestRealm(ctx context.Context, tenant, request, actor, reason string, expected int64, enabled, confirmed bool) (RealmJob, error) {
	return s.requestRealm(ctx, tenant, request, actor, reason, expected, enabled, confirmed, nil)
}

func (s *Store) requestRealm(ctx context.Context, tenant, request, actor, reason string, expected int64, enabled, confirmed bool, permit *maintenancePermit) (RealmJob, error) {
	var j RealmJob
	if !tenancy.ValidID(tenant) || !tenancy.ValidID(request) || !tenancy.ValidID(actor) || !adminReason(reason, confirmed) || expected < 1 {
		return j, tenancy.ErrInvalid
	}
	reason = strings.TrimSpace(reason)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return j, err
	}
	defer tx.Rollback(ctx)
	var status string
	var archived bool
	var version int64
	if err = tx.QueryRow(ctx, `SELECT status,access_version,archived_at IS NOT NULL FROM platform_tenants WHERE id=$1 FOR UPDATE`, tenant).Scan(&status, &version, &archived); errors.Is(err, pgx.ErrNoRows) {
		return j, ErrDenied
	} else if err != nil {
		return j, err
	}
	if archived {
		return j, ErrTenantArchiveBlocked
	}
	phase := "pause"
	if enabled {
		phase = "resume"
	}
	if err = permit.check(ctx, tx, tenant, actor, phase, false); err != nil {
		return j, err
	}
	var priorReason string
	var priorExpected int64
	err = tx.QueryRow(ctx, `SELECT id,request_id,tenant_id,enabled,access_version,state,remaining,error_code,reason,expected_version FROM platform_realm_jobs WHERE actor_id=$1 AND request_id=$2`, actor, request).Scan(&j.ID, &j.RequestID, &j.TenantID, &j.Enabled, &j.Version, &j.Status, &j.Remaining, &j.ErrorCode, &priorReason, &priorExpected)
	if err == nil {
		if j.TenantID != tenant || j.Enabled != enabled || priorReason != reason || priorExpected != expected {
			return RealmJob{}, ErrRequestChanged
		}
		return j, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RealmJob{}, err
	}
	if err = permit.check(ctx, tx, tenant, actor, phase, !enabled); err != nil {
		return j, err
	}
	if err = noPendingMaintenance(ctx, tx, tenant, permit.id()); err != nil {
		return j, err
	}
	if version != expected || (!enabled && status != "active") || (enabled && status != "suspended") {
		return RealmJob{}, ErrConflict
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_realm_jobs WHERE tenant_id=$1 AND state<>'completed')`, tenant).Scan(&pending); err != nil {
		return j, err
	}
	if pending {
		return j, ErrConflict
	}
	if enabled {
		var held bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_recovery_holds WHERE kind='tenant' AND object_id=$1)`, tenant).Scan(&held); err != nil {
			return j, err
		}
		if held {
			return j, ErrConflict
		}
		// Opening is an irreversible write-intent receipt. Offline migration
		// rollback is forbidden from that point, even if the resume ACK is lost.
		var gated bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_legacy_cutovers WHERE tenant_id=$1 AND phase NOT IN ('opening','completed'))`, tenant).Scan(&gated); err != nil {
			return j, err
		}
		if gated {
			return j, ErrConflict
		}
		if err = noPendingDeploymentExcept(ctx, tx, tenant, permit.id()); err != nil {
			return j, err
		}
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_legacy_import_batches WHERE tenant_id=$1 AND state<>'completed')`, tenant).Scan(&pending); err != nil {
			return j, err
		}
		if pending {
			return j, ErrConflict
		}
	}
	id, err := newID("realm")
	if err != nil {
		return j, err
	}
	next := "suspending"
	if enabled {
		next = "resuming"
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_tenants SET status=$2,access_version=access_version+1,updated_at=now() WHERE id=$1`, tenant, next); err != nil {
		return j, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_realm_jobs(id,request_id,actor_id,tenant_id,enabled,expected_version,access_version,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, request, actor, tenant, enabled, expected, version+1, reason); err != nil {
		return j, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,reason,metadata) VALUES($1,'tenant.access.requested',$2,$3,$4,jsonb_build_object('beforeStatus',$5::text,'requestedEnabled',$6::boolean,'accessVersion',$7::bigint))`, actor, tenant, id, reason, status, enabled, version+1); err != nil {
		return j, err
	}
	j = RealmJob{ID: id, RequestID: request, TenantID: tenant, Enabled: enabled, Version: version + 1, Status: "pending"}
	return j, tx.Commit(ctx)
}

type realmWork struct {
	ID, Lease, Address string
	Attempts           int
	Op                 tenancy.RealmOperation
}

func (s *Store) claimRealm(ctx context.Context) (*realmWork, error) {
	lease, err := tenancy.Secret()
	if err != nil {
		return nil, err
	}
	j := &realmWork{Lease: lease}
	err = s.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM platform_realm_jobs WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_realm_jobs' AND h.object_id=platform_realm_jobs.id) AND state='pending' AND retry_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE platform_realm_jobs j SET lease_id=$1,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id,j.tenant_id,j.enabled,j.access_version,j.attempts`, lease).Scan(&j.ID, &j.Op.TenantID, &j.Op.Enabled, &j.Op.Version, &j.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Op.OperationID = j.ID
	return j, nil
}
func (s *Store) finishRealm(ctx context.Context, j *realmWork) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	var version int64
	if err = tx.QueryRow(ctx, `SELECT status,access_version FROM platform_tenants WHERE id=$1 FOR UPDATE`, j.Op.TenantID).Scan(&status, &version); err != nil {
		return err
	}
	expected, next := "suspending", "suspended"
	if j.Op.Enabled {
		expected, next = "resuming", "active"
	}
	if version != j.Op.Version || status != expected {
		return ErrConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE platform_realm_jobs SET state='completed',remaining=0,error_code='',lease_id=NULL,lease_until=NULL,updated_at=now() WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND state='pending'`, j.ID, j.Lease)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_tenants SET status=$2,updated_at=now() WHERE id=$1`, j.Op.TenantID, next); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,metadata) VALUES('system:realm-worker','tenant.access.completed',$1,$2,jsonb_build_object('enabled',$3::boolean,'accessVersion',$4::bigint))`, j.Op.TenantID, j.ID, j.Op.Enabled, j.Op.Version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type RealmWorker struct {
	Store      *Store
	Enterprise interface {
		SetRealm(context.Context, tenancy.RealmOperation) (tenancy.RealmAck, error)
		CheckReadiness(context.Context, string, string) (tenancy.Readiness, error)
	}
}

func (w RealmWorker) Once(ctx context.Context) (bool, error) {
	j, err := w.Store.claimRealm(ctx)
	if err != nil || j == nil {
		return false, err
	}
	var ack tenancy.RealmAck
	if j.Op.Enabled {
		err = w.Store.pool.QueryRow(ctx, `SELECT http_base_url FROM platform_tenants WHERE id=$1 AND status='resuming' AND access_version=$2`, j.Op.TenantID, j.Op.Version).Scan(&j.Address)
		if err == nil {
			var nonce string
			nonce, err = tenancy.Secret()
			if err == nil {
				var r tenancy.Readiness
				r, err = w.Enterprise.CheckReadiness(ctx, j.Op.TenantID, nonce)
				if err == nil && !r.Valid(nonce, j.Op.TenantID, j.Address) {
					err = ErrUnavailable
				}
			}
		}
	}
	if err == nil {
		ack, err = w.Enterprise.SetRealm(ctx, j.Op)
	}
	if err == nil && (ack.RealmOperation != j.Op || (ack.State != "completed" && ack.State != "pending") || ack.Remaining < 0 || (ack.State == "completed" && ack.Remaining != 0) || (ack.State == "pending" && ack.Remaining == 0)) {
		err = ErrUnavailable
	}
	if err == nil && ack.State == "completed" {
		err = w.Store.finishRealm(ctx, j)
		if err == nil {
			return true, nil
		}
	}
	code, delay := "ENTERPRISE_REALM_UNCONFIRMED", min(300, 1<<min(j.Attempts, 8))
	remaining := 0
	if err == nil {
		code, delay, remaining = "", 1, ack.Remaining
	}
	_, err = w.Store.pool.Exec(ctx, `UPDATE platform_realm_jobs SET lease_id=NULL,lease_until=NULL,error_code=$3,remaining=CASE WHEN $3='' THEN $4 ELSE remaining END,retry_at=clock_timestamp()+($5::integer*interval '1 second'),updated_at=now() WHERE id=$1 AND lease_id=$2 AND state='pending'`, j.ID, j.Lease, code, remaining, delay)
	return true, err
}
func (w RealmWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			call, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, _ = w.Once(call)
			cancel()
		}
	}
}
