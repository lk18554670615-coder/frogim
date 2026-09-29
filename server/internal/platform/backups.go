package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

var ErrBackupChanged = errors.New("backup state changed")

type BackupRequest struct {
	RequestID             string `json:"requestId"`
	TenantID              string `json:"tenantId"`
	ServerID              string `json:"serverId"`
	ReleaseID             string `json:"releaseId"`
	ReleaseDigest         string `json:"releaseDigest"`
	ExpectedRevision      int64  `json:"expectedRevision"`
	ExpectedConfigVersion int64  `json:"expectedConfigVersion"`
	ExpectedAccessVersion int64  `json:"expectedAccessVersion"`
	ExpectedGeneration    int64  `json:"expectedGeneration"`
	Reason                string `json:"reason"`
	Confirmed             bool   `json:"confirmed"`
}

type BackupJob struct {
	ID        string                     `json:"id"`
	TenantID  string                     `json:"tenantId"`
	ServerID  string                     `json:"serverId"`
	State     string                     `json:"state"`
	Phase     string                     `json:"phase"`
	ErrorCode string                     `json:"errorCode,omitempty"`
	Operation deployment.BackupOperation `json:"operation"`
	Release   deployment.Release         `json:"release"`
	Receipt   deployment.BackupReceipt   `json:"receipt"`
}

type BackupControl interface {
	AgentInspector
	Backup(context.Context, string, string, deployment.BackupControlRequest) (deployment.BackupControlReport, error)
}

func (a AgentRPC) Backup(ctx context.Context, server, action string, in deployment.BackupControlRequest) (deployment.BackupControlReport, error) {
	var out deployment.BackupControlReport
	if a.Peers[server] == nil || !slices.Contains([]string{"status", "submit", "retry", "cancel"}, action) {
		return out, ErrAgentUnavailable
	}
	if e := a.Peers[server].Call(ctx, "/internal/agent/backup/"+action, in, &out); e != nil || out.Nonce != in.Nonce {
		return deployment.BackupControlReport{}, ErrAgentUnavailable
	}
	return out, nil
}

const backupColumns = `id,tenant_id,server_id,state,phase,error_code,operation,release,receipt`

func scanBackup(row pgx.Row) (BackupJob, error) {
	var j BackupJob
	var op, release, receipt []byte
	e := row.Scan(&j.ID, &j.TenantID, &j.ServerID, &j.State, &j.Phase, &j.ErrorCode, &op, &release, &receipt)
	if e == nil {
		e = json.Unmarshal(op, &j.Operation)
	}
	if e == nil {
		e = json.Unmarshal(release, &j.Release)
	}
	if e == nil {
		e = json.Unmarshal(receipt, &j.Receipt)
	}
	return j, e
}

func backupFence(ctx context.Context, tx pgx.Tx, tenant, server string) (deploymentBinding, error) {
	b, e := deploymentFence(ctx, tx, tenant, server)
	if e == nil && b.State != "suspended" {
		e = ErrDeploymentMaintenance
	}
	return b, e
}

func backupReplay(ctx context.Context, tx pgx.Tx, actor, request string, input []byte) (BackupJob, bool, error) {
	var id string
	var same bool
	e := tx.QueryRow(ctx, `SELECT id,input=$3::jsonb FROM platform_backup_jobs WHERE actor_id=$1 AND request_id=$2`, actor, request, input).Scan(&id, &same)
	if errors.Is(e, pgx.ErrNoRows) {
		return BackupJob{}, false, nil
	}
	if e != nil {
		return BackupJob{}, false, e
	}
	if !same {
		return BackupJob{}, false, ErrRequestChanged
	}
	j, e := scanBackup(tx.QueryRow(ctx, `SELECT `+backupColumns+` FROM platform_backup_jobs WHERE id=$1`, id))
	return j, true, e
}

// The backup directory, key and eventual off-site credentials never originate
// in an HTTP request. They belong to the selected agent's private configuration.
func (s *Store) RequestBackup(ctx context.Context, actor, token string, in BackupRequest, catalog map[string]deployment.Release, agent BackupControl) (BackupJob, error) {
	return s.requestBackup(ctx, actor, token, in, catalog, agent, nil)
}

func (s *Store) requestBackup(ctx context.Context, actor, token string, in BackupRequest, catalog map[string]deployment.Release, agent BackupControl, permit *maintenancePermit) (BackupJob, error) {
	var empty BackupJob
	in.Reason = strings.TrimSpace(in.Reason)
	if !tenancy.ValidID(actor) || (permit == nil && len(token) != 43) || !tenancy.ValidID(in.RequestID) || !tenancy.ValidID(in.TenantID) || !tenancy.ValidID(in.ServerID) || !tenancy.ValidID(in.ReleaseID) ||
		in.ExpectedRevision < 1 || in.ExpectedConfigVersion < 1 || in.ExpectedAccessVersion < 2 || in.ExpectedGeneration < 1 || !adminReason(in.Reason, in.Confirmed) {
		return empty, tenancy.ErrInvalid
	}
	input, _ := json.Marshal(in)
	first, e := s.pool.Begin(ctx)
	if e != nil {
		return empty, e
	}
	defer first.Rollback(ctx)
	if _, e = first.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,145))`, actor+":"+in.RequestID); e != nil {
		return empty, e
	}
	if permit == nil {
		if e = serverOperator(ctx, first, actor, token); e != nil {
			return empty, e
		}
	}
	if j, found, e := backupReplay(ctx, first, actor, in.RequestID, input); e != nil || found {
		return j, e
	}
	before, e := backupFence(ctx, first, in.TenantID, in.ServerID)
	if e != nil {
		return empty, e
	}
	if e = permit.check(ctx, first, in.TenantID, actor, "backup", true); e != nil {
		return empty, e
	}
	if e = noPendingDeploymentExcept(ctx, first, in.TenantID, permit.id()); e != nil {
		return empty, e
	}
	if e = first.Commit(ctx); e != nil {
		return empty, e
	}
	r, ok := catalog[in.ReleaseID]
	if !ok || !r.Valid() || r.Digest() != in.ReleaseDigest || r.Runtime != before.Runtime || !r.MatchesTarget(in.TenantID, in.ServerID) || before.Revision != in.ExpectedRevision || before.Config != in.ExpectedConfigVersion || before.Access != in.ExpectedAccessVersion {
		return empty, ErrBackupChanged
	}
	if agent == nil {
		return empty, ErrAgentUnavailable
	}
	nonce, e := tenancy.Secret()
	if e != nil {
		return empty, e
	}
	inspection, e := agent.Inspect(ctx, in.ServerID, nonce)
	if e != nil || !inspection.Valid(nonce, in.ServerID, in.TenantID, before.Address) || inspection.HostFingerprint != before.Host || inspection.Runtime != before.Runtime || inspection.IsolationMode != before.Mode || !slices.Contains(inspection.Capabilities, "backup") {
		return empty, ErrAgentUnavailable
	}
	nonce, e = tenancy.Secret()
	if e != nil {
		return empty, e
	}
	report, e := agent.Backup(ctx, in.ServerID, "status", deployment.BackupControlRequest{Nonce: nonce})
	st := report.Status
	if e != nil || report.Nonce != nonce || st.ServerID != in.ServerID || st.TenantID != in.TenantID || st.HostFingerprint != before.Host || st.ActiveOperationID != "" || st.ActiveBackupID != "" || st.Generation != in.ExpectedGeneration || st.CurrentReleaseID != r.ID || st.CurrentReleaseDigest != r.Digest() {
		return empty, ErrBackupChanged
	}
	if before.Mode == "dedicated_host" && st.BackupOffsiteTargetID == "" || st.BackupOffsiteTargetID != "" && !tenancy.ValidID(st.BackupOffsiteTargetID) {
		return empty, ErrAgentUnavailable
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,145))`, actor+":"+in.RequestID); e != nil {
		return empty, e
	}
	if permit == nil {
		if e = serverOperator(ctx, tx, actor, token); e != nil {
			return empty, e
		}
	}
	if j, found, e := backupReplay(ctx, tx, actor, in.RequestID, input); e != nil || found {
		return j, e
	}
	bound, e := backupFence(ctx, tx, in.TenantID, in.ServerID)
	if e != nil {
		return empty, e
	}
	if bound != before {
		return empty, ErrBackupChanged
	}
	if e = permit.check(ctx, tx, in.TenantID, actor, "backup", true); e != nil {
		return empty, e
	}
	if e = noPendingDeploymentExcept(ctx, tx, in.TenantID, permit.id()); e != nil {
		return empty, e
	}
	var generation int64
	var releaseID, digest string
	e = tx.QueryRow(ctx, `SELECT generation,operation->>'releaseId',operation->>'releaseDigest' FROM platform_deployment_jobs WHERE server_id=$1 AND state='completed' ORDER BY generation DESC LIMIT 1`, in.ServerID).Scan(&generation, &releaseID, &digest)
	if e != nil || generation != st.Generation || releaseID != st.CurrentReleaseID || digest != st.CurrentReleaseDigest {
		return empty, ErrBackupChanged
	}
	// Hex-only suffix is compatible with the agent's path-safe operation IDs.
	var entropy [16]byte
	if _, e = rand.Read(entropy[:]); e != nil {
		return empty, e
	}
	id := "backup-" + hex.EncodeToString(entropy[:])
	op := deployment.BackupOperation{ID: id, HostFingerprint: before.Host, Binding: backup.Binding{TenantID: in.TenantID, ServerID: in.ServerID, ReleaseID: r.ID, ReleaseDigest: r.Digest(), Generation: st.Generation, AccessVersion: before.Access, SchemaVersion: r.SchemaVersion}}
	op.OffsiteTargetID = st.BackupOffsiteTargetID
	if !op.Binding.Valid() {
		return empty, ErrBackupChanged
	}
	opRaw, _ := json.Marshal(op)
	releaseRaw, _ := json.Marshal(r)
	bindingRaw, _ := json.Marshal(before)
	j, e := scanBackup(tx.QueryRow(ctx, `INSERT INTO platform_backup_jobs(id,actor_id,request_id,tenant_id,server_id,input,operation,release,binding) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+backupColumns, id, actor, in.RequestID, in.TenantID, in.ServerID, input, opRaw, releaseRaw, bindingRaw))
	if e != nil {
		return empty, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,reason,metadata) VALUES($1,'backup.requested',$2,$3,$4,jsonb_build_object('serverId',$5::text,'releaseId',$6::text,'generation',$7::bigint,'accessVersion',$8::bigint))`, actor, in.TenantID, id, in.Reason, in.ServerID, r.ID, st.Generation, before.Access); e != nil {
		return empty, e
	}
	return j, tx.Commit(ctx)
}

type BackupAction struct {
	RequestID        string `json:"requestId"`
	Action           string `json:"action"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Reason           string `json:"reason"`
	Confirmed        bool   `json:"confirmed"`
}

// Only an explicitly acknowledged unconfirmed phase can be retried. Failed
// archives are kept; another capture uses a NEW job, never overwrites an archive.
func (s *Store) ControlBackup(ctx context.Context, actor, token, id string, in BackupAction) (BackupJob, error) {
	var empty BackupJob
	in.Reason = strings.TrimSpace(in.Reason)
	if !tenancy.ValidID(actor) || !tenancy.ValidID(id) || !tenancy.ValidID(in.RequestID) || !slices.Contains([]string{"retry", "cancel"}, in.Action) || in.ExpectedRevision < 1 || !adminReason(in.Reason, in.Confirmed) {
		return empty, tenancy.ErrInvalid
	}
	raw, _ := json.Marshal(in)
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,146))`, actor+":"+in.RequestID); e != nil {
		return empty, e
	}
	if e = serverOperator(ctx, tx, actor, token); e != nil {
		return empty, e
	}
	j, e := scanBackup(tx.QueryRow(ctx, `SELECT `+backupColumns+` FROM platform_backup_jobs WHERE id=$1`, id))
	if e != nil {
		return empty, e
	}
	var prior string
	var same bool
	e = tx.QueryRow(ctx, `SELECT job_id,input=$3::jsonb FROM platform_backup_controls WHERE actor_id=$1 AND request_id=$2`, actor, in.RequestID, raw).Scan(&prior, &same)
	if e == nil {
		if prior != id || !same {
			return empty, ErrRequestChanged
		}
		return j, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return empty, e
	}
	if _, e = backupFence(ctx, tx, j.TenantID, j.ServerID); e != nil {
		return empty, e
	}
	j, e = scanBackup(tx.QueryRow(ctx, `UPDATE platform_backup_jobs SET state='pending',control_action=$2,control_revision=$3,retry_at=clock_timestamp(),updated_at=now()
 WHERE id=$1 AND control_action='' AND lease_id IS NULL AND (receipt->>'revision')::bigint=$3 AND
 (($2='retry' AND state='unconfirmed' AND receipt->>'state'='unconfirmed') OR ($2='cancel' AND state='pending' AND receipt->>'state'='pending' AND receipt->>'phase'='queued')) RETURNING `+backupColumns, id, in.Action, in.ExpectedRevision))
	if errors.Is(e, pgx.ErrNoRows) {
		return empty, ErrBackupChanged
	}
	if e != nil {
		return empty, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_backup_controls(actor_id,request_id,job_id,input) VALUES($1,$2,$3,$4)`, actor, in.RequestID, id, raw); e != nil {
		return empty, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,reason,metadata) VALUES($1,$2,$3,$4,$5,jsonb_build_object('expectedRevision',$6::bigint))`, actor, "backup."+in.Action+".requested", j.TenantID, id, in.Reason, in.ExpectedRevision); e != nil {
		return empty, e
	}
	return j, tx.Commit(ctx)
}
