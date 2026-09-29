package platform

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

type MaintenanceRun struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenantId"`
	ActorID      string    `json:"actorId"`
	State        string    `json:"state"`
	Phase        string    `json:"phase"`
	WindowEnd    time.Time `json:"windowEnd"`
	PauseID      string    `json:"pauseId"`
	BackupID     string    `json:"backupId"`
	ResumeID     string    `json:"resumeId"`
	BackupResult string    `json:"backupResult"`
	ErrorCode    string    `json:"errorCode"`
}
type maintenanceWork struct {
	MaintenanceRun
	Binding    deploymentBinding
	Release    deployment.Release
	Generation int64
	Lease      string
	Now        time.Time
}
type MaintenanceWorker struct {
	Store     *Store
	Agent     BackupControl
	Catalog   map[string]deployment.Release
	Readiness func(context.Context, string, string) (tenancy.Readiness, error)
}

func (w MaintenanceWorker) claim(ctx context.Context) (*maintenanceWork, error) {
	lease, e := tenancy.Secret()
	if e != nil {
		return nil, e
	}
	j := &maintenanceWork{Lease: lease}
	var binding, release []byte
	e = w.Store.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM platform_maintenance_runs WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_maintenance_runs' AND h.object_id=platform_maintenance_runs.id) AND state='pending' AND retry_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE platform_maintenance_runs j SET lease_id=$1,lease_until=clock_timestamp()+interval '90 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id
 RETURNING j.id,j.tenant_id,j.actor_id,j.state,j.phase,j.window_end,j.pause_id,j.backup_id,j.resume_id,j.backup_result,j.error_code,j.binding,j.release,j.generation,clock_timestamp()`, lease).Scan(&j.ID, &j.TenantID, &j.ActorID, &j.State, &j.Phase, &j.WindowEnd, &j.PauseID, &j.BackupID, &j.ResumeID, &j.BackupResult, &j.ErrorCode, &binding, &release, &j.Generation, &j.Now)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(binding, &j.Binding); e != nil {
		return nil, e
	}
	if e = json.Unmarshal(release, &j.Release); e != nil {
		return nil, e
	}
	return j, nil
}

func (w MaintenanceWorker) fence(ctx context.Context, tx pgx.Tx, j *maintenanceWork) error {
	var b deploymentBinding
	b.TenantID, b.ServerID = j.TenantID, j.Binding.ServerID
	if e := tx.QueryRow(ctx, `SELECT http_base_url,status,config_version,access_version FROM platform_tenants WHERE id=$1 FOR UPDATE`, j.TenantID).Scan(&b.Address, &b.State, &b.Config, &b.Access); e != nil {
		return e
	}
	if e := tx.QueryRow(ctx, `SELECT host_fingerprint,runtime,isolation_mode,revision FROM platform_servers WHERE id=$1 AND tenant_id=$2 FOR SHARE`, b.ServerID, j.TenantID).Scan(&b.Host, &b.Runtime, &b.Mode, &b.Revision); e != nil {
		return e
	}
	frozen := j.Binding
	frozen.State, frozen.Access = b.State, b.Access
	if b != frozen {
		return ErrMaintenanceChanged
	}
	permit := maintenancePermit{j.ID, j.Lease, j.Phase}
	if e := permit.check(ctx, tx, j.TenantID, j.ActorID, j.Phase, false); e != nil {
		return e
	}
	if j.Phase == "prepare" || j.Phase == "pause" && b.State == "active" && b.Access == j.Binding.Access {
		if b.State != "active" || b.Access != j.Binding.Access {
			return ErrMaintenanceChanged
		}
		return nil
	}
	// A matching version alone is insufficient. Only this run's exact pause
	// and resume jobs prove ownership of the temporary access change.
	var paused, resuming bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_realm_jobs WHERE tenant_id=$1 AND actor_id=$2 AND request_id=$3 AND NOT enabled AND expected_version=$4 AND access_version=$4+1 AND ($5 OR state='completed')),
 EXISTS(SELECT 1 FROM platform_realm_jobs WHERE tenant_id=$1 AND actor_id=$2 AND request_id=$6 AND enabled AND expected_version=$4+1 AND access_version=$4+2)`, j.TenantID, j.ActorID, j.ID+"-pause", j.Binding.Access, j.Phase == "pause", j.ID+"-resume").Scan(&paused, &resuming)
	if e != nil {
		return e
	}
	if !paused {
		return ErrMaintenanceChanged
	}
	if j.Phase == "pause" && b.Access == j.Binding.Access+1 && slices.Contains([]string{"suspending", "suspended"}, b.State) {
		return nil
	}
	if (j.Phase == "backup" || j.Phase == "resume") && b.Access == j.Binding.Access+1 && b.State == "suspended" {
		return nil
	}
	if j.Phase == "resume" && resuming && b.Access == j.Binding.Access+2 && slices.Contains([]string{"resuming", "active"}, b.State) {
		return nil
	}
	return ErrMaintenanceChanged
}

func (w MaintenanceWorker) prepare(ctx context.Context, j *maintenanceWork) error {
	if w.Agent == nil || w.Readiness == nil {
		return ErrAgentUnavailable
	}
	r, ok := w.Catalog[j.Release.ID]
	if !ok || !r.Valid() || r.Digest() != j.Release.Digest() || !r.MatchesTarget(j.TenantID, j.Binding.ServerID) {
		return ErrMaintenanceChanged
	}
	nonce, e := tenancy.Secret()
	if e != nil {
		return e
	}
	inspection, e := w.Agent.Inspect(ctx, j.Binding.ServerID, nonce)
	if e != nil || !inspection.Valid(nonce, j.Binding.ServerID, j.TenantID, j.Binding.Address) || inspection.HostFingerprint != j.Binding.Host || inspection.Runtime != j.Binding.Runtime || inspection.IsolationMode != j.Binding.Mode || !slices.Contains(inspection.Capabilities, "backup") {
		return ErrAgentUnavailable
	}
	nonce, e = tenancy.Secret()
	if e != nil {
		return e
	}
	report, e := w.Agent.Backup(ctx, j.Binding.ServerID, "status", deployment.BackupControlRequest{Nonce: nonce})
	s := report.Status
	if j.Binding.Mode == "dedicated_host" && s.BackupOffsiteTargetID == "" {
		return ErrAgentUnavailable
	}
	if e != nil || report.Nonce != nonce || s.ServerID != j.Binding.ServerID || s.TenantID != j.TenantID || s.HostFingerprint != j.Binding.Host || s.Generation != j.Generation || s.CurrentReleaseID != r.ID || s.CurrentReleaseDigest != r.Digest() || s.ActiveOperationID != "" || s.ActiveBackupID != "" {
		return ErrAgentUnavailable
	}
	nonce, e = tenancy.Secret()
	if e != nil {
		return e
	}
	ready, e := w.Readiness(ctx, j.TenantID, nonce)
	if e != nil || !ready.Valid(nonce, j.TenantID, j.Binding.Address) || ready.SchemaVersion != r.SchemaVersion || ready.Realm == nil || ready.Realm.Version != j.Binding.Access || !ready.Realm.Enabled || ready.Realm.SuspensionConfirmed {
		return ErrUnavailable
	}
	return nil
}

func (w MaintenanceWorker) Once(ctx context.Context) (bool, error) {
	j, e := w.claim(ctx)
	if e != nil || j == nil {
		return false, e
	}
	state, phase, code, result := j.State, j.Phase, "", j.BackupResult
	pauseID, backupID, resumeID := j.PauseID, j.BackupID, j.ResumeID
	permit := &maintenancePermit{j.ID, j.Lease, j.Phase}
	workErr := func() error {
		tx, e := w.Store.pool.Begin(ctx)
		if e != nil {
			return e
		}
		defer tx.Rollback(ctx)
		if e = w.fence(ctx, tx, j); e != nil {
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
		switch j.Phase {
		case "prepare":
			if !j.Now.Before(j.WindowEnd) {
				state, phase, code = "skipped", "finished", "MAINTENANCE_WINDOW_EXPIRED"
				return nil
			}
			if e = w.prepare(ctx, j); e != nil {
				return e
			}
			phase = "pause"
		case "pause":
			p, e := w.Store.requestRealm(ctx, j.TenantID, j.ID+"-pause", j.ActorID, "每日备份已确认的维护停用", j.Binding.Access, false, true, permit)
			if errors.Is(e, errMaintenanceWindow) {
				state, phase, code = "skipped", "finished", "MAINTENANCE_WINDOW_EXPIRED"
				return nil
			}
			if e != nil {
				return e
			}
			pauseID = p.ID
			if p.Status == "completed" {
				phase = "backup"
			}
		case "backup":
			in := BackupRequest{RequestID: j.ID + "-backup", TenantID: j.TenantID, ServerID: j.Binding.ServerID, ReleaseID: j.Release.ID, ReleaseDigest: j.Release.Digest(), ExpectedRevision: j.Binding.Revision, ExpectedConfigVersion: j.Binding.Config, ExpectedAccessVersion: j.Binding.Access + 1, ExpectedGeneration: j.Generation, Reason: "每日备份已确认的维护任务", Confirmed: true}
			b, e := w.Store.requestBackup(ctx, j.ActorID, "", in, w.Catalog, w.Agent, permit)
			if errors.Is(e, errMaintenanceWindow) {
				phase, result = "resume", "window_expired"
				return nil
			}
			if e != nil {
				return e
			}
			backupID = b.ID
			if slices.Contains([]string{"completed", "failed", "cancelled"}, b.State) {
				phase, result = "resume", b.State
			} else if b.State == "unconfirmed" {
				code = "MAINTENANCE_BACKUP_NEEDS_ATTENTION"
			}
		case "resume":
			if !slices.Contains([]string{"completed", "failed", "cancelled", "window_expired"}, j.BackupResult) {
				return ErrMaintenanceChanged
			}
			r, e := w.Store.requestRealm(ctx, j.TenantID, j.ID+"-resume", j.ActorID, "每日备份恢复本任务停用的企业", j.Binding.Access+1, true, true, permit)
			if e != nil {
				return e
			}
			resumeID = r.ID
			if r.Status == "completed" {
				state, phase = "completed", "finished"
				if j.BackupResult == "window_expired" {
					state, code = "skipped", "MAINTENANCE_WINDOW_EXPIRED"
				} else if j.BackupResult != "completed" {
					state, code = "failed", "MAINTENANCE_BACKUP_FAILED"
				}
			}
		default:
			return ErrMaintenanceChanged
		}
		return nil
	}()
	if workErr != nil {
		code = "MAINTENANCE_CONTROL_UNCONFIRMED"
	}
	tx, e := w.Store.pool.Begin(ctx)
	if e != nil {
		return true, e
	}
	defer tx.Rollback(ctx)
	if e = w.fence(ctx, tx, j); e != nil {
		return true, e
	}
	tag, e := tx.Exec(ctx, `UPDATE platform_maintenance_runs SET state=$3,phase=$4,error_code=$5,backup_result=$6,pause_id=$7,backup_id=$8,resume_id=$9,
 lease_id=NULL,lease_until=NULL,retry_at=clock_timestamp()+interval '5 seconds',updated_at=now() WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND state='pending'`, j.ID, j.Lease, state, phase, code, result, pauseID, backupID, resumeID)
	if e != nil {
		return true, e
	}
	if tag.RowsAffected() != 1 {
		return true, ErrMaintenanceChanged
	}
	if state != j.State || phase != j.Phase || code != j.ErrorCode {
		if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,metadata) VALUES('system:maintenance-worker','maintenance.progress',$1,$2,jsonb_build_object('state',$3::text,'phase',$4::text,'errorCode',$5::text,'backupId',$6::text,'backupResult',$7::text,'pauseId',$8::text,'resumeId',$9::text))`, j.TenantID, j.ID, state, phase, code, backupID, result, pauseID, resumeID); e != nil {
			return true, e
		}
	}
	return true, tx.Commit(ctx)
}

func (w MaintenanceWorker) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var minute int64 = -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			call, cancel := context.WithTimeout(ctx, 60*time.Second)
			if current := time.Now().Unix() / 60; current != minute {
				_, _ = w.Store.ScheduleBackups(call)
				minute = current
			}
			_, _ = w.Once(call)
			cancel()
		}
	}
}
