package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

// Local completion releases maintenance independently. Remote delivery is
// polled from the SAME authenticated agent and original immutable operation;
// neither resumed access nor a later deployment may reinterpret that binding.
func (w BackupWorker) claimOffsite(ctx context.Context) (*backupWork, error) {
	lease, e := tenancy.Secret()
	if e != nil {
		return nil, e
	}
	var id string
	e = w.Store.pool.QueryRow(ctx, `WITH candidate AS (
 SELECT id FROM platform_backup_jobs WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_backup_jobs' AND h.object_id=platform_backup_jobs.id) AND state='completed' AND operation->>'offsiteTargetId'<>''
 AND receipt->'offsite'->>'state' IN ('pending','running','unconfirmed')
 AND retry_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp())
 ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE platform_backup_jobs j SET lease_id=$1,lease_until=clock_timestamp()+interval '90 seconds'
 FROM candidate c WHERE j.id=c.id RETURNING j.id`, lease).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	j, e := scanBackup(w.Store.pool.QueryRow(ctx, `SELECT `+backupColumns+` FROM platform_backup_jobs WHERE id=$1 AND lease_id=$2`, id, lease))
	if e != nil {
		return nil, e
	}
	work := &backupWork{Job: j, Lease: lease}
	var raw []byte
	e = w.Store.pool.QueryRow(ctx, `SELECT binding,attempts FROM platform_backup_jobs WHERE id=$1 AND lease_id=$2`, id, lease).Scan(&raw, &work.Attempts)
	if e == nil {
		e = json.Unmarshal(raw, &work.Binding)
	}
	return work, e
}

func (w BackupWorker) offsiteFence(ctx context.Context, tx pgx.Tx, j *backupWork) error {
	var tenant string
	if e := tx.QueryRow(ctx, `SELECT id FROM platform_tenants WHERE id=$1 FOR SHARE`, j.Job.TenantID).Scan(&tenant); e != nil {
		return e
	}
	var host, mode, runtime string
	var revision int64
	e := tx.QueryRow(ctx, `SELECT host_fingerprint,isolation_mode,runtime,revision FROM platform_servers WHERE id=$1 AND tenant_id=$2 FOR SHARE`, j.Job.ServerID, tenant).Scan(&host, &mode, &runtime, &revision)
	if e != nil {
		return e
	}
	if host != j.Binding.Host || mode != j.Binding.Mode || runtime != j.Binding.Runtime || revision != j.Binding.Revision {
		return ErrBackupChanged
	}
	var valid bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_backup_jobs WHERE id=$1 AND state='completed' AND lease_id=$2 AND lease_until>clock_timestamp())`, j.Job.ID, j.Lease).Scan(&valid)
	if e == nil && !valid {
		e = ErrBackupChanged
	}
	return e
}

func matchingOffsite(j *backupWork, out deployment.BackupControlReport) bool {
	s, p, old, op := out.Status, out.Receipt, j.Job.Receipt, j.Job.Operation
	if p.Operation != op || s.TenantID != op.Binding.TenantID || s.ServerID != op.Binding.ServerID || s.HostFingerprint != op.HostFingerprint || s.Generation < op.Binding.Generation || s.BackupOffsiteTargetID != op.OffsiteTargetID || p.State != "completed" || p.Phase != "finished" || p.ErrorCode != "" || p.RecoveryErrorCode != "" || p.Archive != old.Archive || p.Attempt != old.Attempt || p.Revision < old.Revision || p.UpdatedAt.Before(old.UpdatedAt) || p.Offsite.UpdatedAt.Before(old.Offsite.UpdatedAt) || p.Offsite.Attempts < old.Offsite.Attempts || !deployment.ValidBackupOffsite(p) {
		return false
	}
	if p.Revision == old.Revision {
		a, _ := json.Marshal(p)
		b, _ := json.Marshal(old)
		return bytes.Equal(a, b)
	}
	return true
}

func auditOffsite(ctx context.Context, tx pgx.Tx, j BackupJob, p deployment.BackupReceipt) error {
	raw, _ := json.Marshal(p.Offsite)
	_, e := tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,metadata)
 VALUES('system:backup-worker',$1,$2,$3,jsonb_build_object('agentRevision',$4::bigint,'offsite',$5::jsonb,'scope','authenticated-agent-offsite-receipt'))`, "backup.offsite."+p.Offsite.State, j.TenantID, j.ID, p.Revision, raw)
	return e
}

func (w BackupWorker) OffsiteOnce(ctx context.Context) (bool, error) {
	j, e := w.claimOffsite(ctx)
	if e != nil || j == nil {
		return false, e
	}
	// Check the owner/host before dispatch and again before persisting a result.
	pre, e := w.Store.pool.Begin(ctx)
	if e != nil {
		return true, e
	}
	defer pre.Rollback(ctx)
	if e = w.offsiteFence(ctx, pre, j); e != nil {
		return true, e
	}
	if e = pre.Commit(ctx); e != nil {
		return true, e
	}
	receipt, code := j.Job.Receipt, ""
	nonce, e := tenancy.Secret()
	if e == nil && w.Agent != nil {
		var out deployment.BackupControlReport
		out, e = w.Agent.Backup(ctx, j.Job.ServerID, "status", deployment.BackupControlRequest{Nonce: nonce, Operation: j.Job.Operation})
		if e == nil && (out.Nonce != nonce || !matchingOffsite(j, out)) {
			e = ErrAgentUnavailable
		}
		if e == nil {
			receipt = out.Receipt
		}
	} else {
		e = ErrAgentUnavailable
	}
	failures, delay := 0, 15
	if e != nil {
		code = "BACKUP_OFFSITE_CONTROL_UNCONFIRMED"
		failures = min(j.Attempts+1, 10)
		delay = min(900, 1<<min(failures+3, 10))
	}
	tx, e := w.Store.pool.Begin(ctx)
	if e != nil {
		return true, e
	}
	defer tx.Rollback(ctx)
	if e = w.offsiteFence(ctx, tx, j); e != nil {
		return true, e
	}
	raw, _ := json.Marshal(receipt)
	tag, e := tx.Exec(ctx, `UPDATE platform_backup_jobs SET receipt=$3,error_code=$4,attempts=$5,
 lease_id=NULL,lease_until=NULL,retry_at=clock_timestamp()+($6::integer*interval '1 second'),updated_at=now()
 WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND state='completed'`, j.Job.ID, j.Lease, raw, code, failures, delay)
	if e != nil {
		return true, e
	}
	if tag.RowsAffected() != 1 {
		return true, ErrBackupChanged
	}
	if code != j.Job.ErrorCode {
		if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,metadata) VALUES('system:backup-worker','backup.offsite.control',$1,$2,jsonb_build_object('errorCode',$3::text))`, j.Job.TenantID, j.Job.ID, code); e != nil {
			return true, e
		}
	}
	if receipt.Offsite.State != j.Job.Receipt.Offsite.State {
		if e = auditOffsite(ctx, tx, j.Job, receipt); e != nil {
			return true, e
		}
	}
	return true, tx.Commit(ctx)
}
