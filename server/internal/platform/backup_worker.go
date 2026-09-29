package platform

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

type BackupWorker struct {
	Store     *Store
	Agent     BackupControl
	Catalog   map[string]deployment.Release
	Readiness func(context.Context, string, string) (tenancy.Readiness, error)
}
type backupWork struct {
	Job                        BackupJob
	Binding                    deploymentBinding
	Lease                      string
	Control                    string
	ControlRevision            int64
	Attempts                   int
	AgentSeen, DispatchStarted bool
}

func (w BackupWorker) claim(ctx context.Context) (*backupWork, error) {
	lease, e := tenancy.Secret()
	if e != nil {
		return nil, e
	}
	var id string
	e = w.Store.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM platform_backup_jobs WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_backup_jobs' AND h.object_id=platform_backup_jobs.id) AND state='pending' AND retry_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE platform_backup_jobs j SET lease_id=$1,lease_until=clock_timestamp()+interval '90 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id`, lease).Scan(&id)
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
	e = w.Store.pool.QueryRow(ctx, `SELECT binding,control_action,control_revision,attempts,agent_seen,dispatch_started FROM platform_backup_jobs WHERE id=$1 AND lease_id=$2`, id, lease).Scan(&raw, &work.Control, &work.ControlRevision, &work.Attempts, &work.AgentSeen, &work.DispatchStarted)
	if e == nil {
		e = json.Unmarshal(raw, &work.Binding)
	}
	return work, e
}

func (w BackupWorker) fence(ctx context.Context, tx pgx.Tx, j *backupWork) error {
	b, e := backupFence(ctx, tx, j.Job.TenantID, j.Job.ServerID)
	if e != nil {
		return e
	}
	if b != j.Binding {
		return ErrBackupChanged
	}
	var valid bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_backup_jobs WHERE id=$1 AND state='pending' AND lease_id=$2 AND lease_until>clock_timestamp())`, j.Job.ID, j.Lease).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return ErrBackupChanged
	}
	return nil
}

func archiveProofValid(p deployment.ArchiveProof) bool {
	b, e := hex.DecodeString(p.SHA256)
	return e == nil && len(b) == 32 && strings.ToLower(p.SHA256) == p.SHA256 && p.Bytes > 0 && p.Files == 8
}

func matchingBackup(j *backupWork, r deployment.BackupControlReport) bool {
	op, s, p := j.Job.Operation, r.Status, r.Receipt
	b := op.Binding
	if !deployment.ValidBackupOffsite(p) || op.OffsiteTargetID != "" && s.BackupOffsiteTargetID != op.OffsiteTargetID {
		return false
	}
	if s.ServerID != b.ServerID || s.TenantID != b.TenantID || s.HostFingerprint != op.HostFingerprint || s.Generation != b.Generation || s.CurrentReleaseID != b.ReleaseID || s.CurrentReleaseDigest != b.ReleaseDigest || s.ActiveOperationID != "" || p.Operation != op || p.Attempt < 1 || p.Revision < 1 || p.Revision < j.Job.Receipt.Revision || p.Attempt < j.Job.Receipt.Attempt || p.UpdatedAt.IsZero() {
		return false
	}
	if p.Archive != (deployment.ArchiveProof{}) && !archiveProofValid(p.Archive) {
		return false
	}
	if !slices.Contains([]string{"", "BACKUP_PREFLIGHT_UNCONFIRMED", "BACKUP_QUIESCE_UNCONFIRMED", "BACKUP_ARCHIVE_UNCONFIRMED"}, p.ErrorCode) || !slices.Contains([]string{"", "BACKUP_PREFLIGHT_UNCONFIRMED", "BACKUP_SERVICE_RECOVERY_UNCONFIRMED", "BACKUP_SERVICE_VERIFICATION_UNCONFIRMED"}, p.RecoveryErrorCode) {
		return false
	}
	if slices.Contains([]string{"completed", "failed", "cancelled"}, p.State) {
		if p.Phase != "finished" || p.RecoveryErrorCode != "" || s.ActiveBackupID != "" {
			return false
		}
		switch p.State {
		case "completed":
			return archiveProofValid(p.Archive) && p.ErrorCode == ""
		case "failed":
			return p.ErrorCode != "" && p.Archive == (deployment.ArchiveProof{})
		case "cancelled":
			return p.ErrorCode == "" && p.Archive == (deployment.ArchiveProof{})
		}
	}
	return s.ActiveBackupID == op.ID && slices.Contains([]string{"pending", "running", "unconfirmed"}, p.State) && slices.Contains([]string{"queued", "quiescing", "snapshotting", "restoring_services", "verifying"}, p.Phase)
}

func (w BackupWorker) ready(ctx context.Context, j *backupWork) error {
	nonce, e := tenancy.Secret()
	if e != nil {
		return e
	}
	r, e := w.Readiness(ctx, j.Job.TenantID, nonce)
	if e != nil || !r.Valid(nonce, j.Job.TenantID, j.Binding.Address) || r.SchemaVersion != j.Job.Release.SchemaVersion || r.Realm == nil || r.Realm.Version != j.Binding.Access || r.Realm.Enabled || !r.Realm.SuspensionConfirmed {
		return ErrDeploymentMaintenance
	}
	return nil
}

func (w BackupWorker) Once(ctx context.Context) (bool, error) {
	j, e := w.claim(ctx)
	if e != nil || j == nil {
		return false, e
	}
	state, phase, code := "pending", "contacting_agent", ""
	receipt := j.Job.Receipt
	controlDone := false
	workErr := func() error {
		if w.Agent == nil || w.Readiness == nil {
			return ErrAgentUnavailable
		}
		r, ok := w.Catalog[j.Job.Release.ID]
		if !ok || !r.Valid() || r.Digest() != j.Job.Release.Digest() || r.Digest() != j.Job.Operation.Binding.ReleaseDigest || !r.MatchesTarget(j.Job.TenantID, j.Job.ServerID) {
			return ErrBackupChanged
		}
		// Persist the first-dispatch intent AFTER readiness, BEFORE the RPC.
		// A lost submit ACK may leave the API intentionally stopped. Requiring
		// another preflight in that state would prevent polling/recovery forever.
		if !j.DispatchStarted {
			if e := w.ready(ctx, j); e != nil {
				return e
			}
		}
		tx, e := w.Store.pool.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		if e = w.fence(ctx, tx, j); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE platform_backup_jobs SET dispatch_started=true WHERE id=$1 AND lease_id=$2`, j.Job.ID, j.Lease); e != nil {
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
		j.DispatchStarted = true
		call := func(action string, revision int64) (deployment.BackupControlReport, error) {
			nonce, e := tenancy.Secret()
			if e != nil {
				return deployment.BackupControlReport{}, e
			}
			out, e := w.Agent.Backup(ctx, j.Job.ServerID, action, deployment.BackupControlRequest{Nonce: nonce, Operation: j.Job.Operation, ExpectedRevision: revision})
			if e != nil || out.Nonce != nonce || !matchingBackup(j, out) {
				return deployment.BackupControlReport{}, ErrAgentUnavailable
			}
			return out, nil
		}
		action := "submit"
		if j.AgentSeen {
			action = "status"
		}
		out, e := call(action, 0)
		if e != nil {
			return e
		}
		j.AgentSeen = true
		receipt = out.Receipt
		if j.Control != "" {
			// Only replay the exact revision. If an ACK was lost, the next poll
			// observes the newer durable receipt; never issue a second retry.
			if receipt.Revision == j.ControlRevision {
				out, e = call(j.Control, j.ControlRevision)
				if e != nil {
					return e
				}
				receipt = out.Receipt
			}
			controlDone = true
		}
		phase = receipt.Phase
		if receipt.State == "unconfirmed" {
			state, code = "unconfirmed", "BACKUP_EXECUTION_UNCONFIRMED"
			return nil
		}
		if slices.Contains([]string{"completed", "failed", "cancelled"}, receipt.State) {
			phase = "verifying_business"
			if e = w.ready(ctx, j); e != nil {
				return e
			}
			state, phase = receipt.State, "verified"
			if state == "failed" {
				code = "BACKUP_CAPTURE_FAILED"
			}
		}
		return nil
	}()
	if workErr != nil {
		code = "BACKUP_CONTROL_UNCONFIRMED"
		if phase == "verifying_business" {
			code = "BACKUP_BUSINESS_UNCONFIRMED"
		}
	}
	tx, e := w.Store.pool.Begin(ctx)
	if e != nil {
		return true, e
	}
	defer tx.Rollback(ctx)
	if e = w.fence(ctx, tx, j); e != nil {
		return true, e
	}
	delay, failures := 2, 0
	if workErr != nil {
		failures = 1
		if j.Job.Phase == phase && j.Job.ErrorCode == code {
			failures = max(1, min(j.Attempts, 10))
		}
		delay = min(300, 1<<min(failures, 9))
	}
	raw, _ := json.Marshal(receipt)
	tag, e := tx.Exec(ctx, `UPDATE platform_backup_jobs SET state=$3,phase=$4,error_code=$5,receipt=$6,agent_seen=$7,
 control_action=CASE WHEN $8 THEN '' ELSE control_action END,control_revision=CASE WHEN $8 THEN 0 ELSE control_revision END,
 lease_id=NULL,lease_until=NULL,retry_at=clock_timestamp()+($9::integer*interval '1 second'),attempts=$10,updated_at=now()
 WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND state='pending'`, j.Job.ID, j.Lease, state, phase, code, raw, j.AgentSeen, controlDone, delay, failures)
	if e != nil {
		return true, e
	}
	if tag.RowsAffected() != 1 {
		return true, ErrBackupChanged
	}
	if state != "pending" || controlDone {
		archiveRaw, _ := json.Marshal(receipt.Archive)
		if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,metadata) VALUES('system:backup-worker',$1,$2,$3,jsonb_build_object('state',$4::text,'agentRevision',$5::bigint,'control',$6::text,'archive',$7::jsonb,'scope','agent-archive-and-business-readiness'))`, "backup."+state, j.Job.TenantID, j.Job.ID, state, receipt.Revision, j.Control, archiveRaw); e != nil {
			return true, e
		}
	}
	if state == "completed" && receipt.Offsite.State != "" {
		if e = auditOffsite(ctx, tx, j.Job, receipt); e != nil {
			return true, e
		}
	}
	return true, tx.Commit(ctx)
}

func (w BackupWorker) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			call, cancel := context.WithTimeout(ctx, 60*time.Second)
			_, _ = w.Once(call)
			cancel()
			call, cancel = context.WithTimeout(ctx, 60*time.Second)
			_, _ = w.OffsiteOnce(call)
			cancel()
		}
	}
}
