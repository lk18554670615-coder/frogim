package platform

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

var ErrDeploymentMaintenance = errors.New("deployment requires confirmed maintenance")
var ErrDeploymentChanged = errors.New("deployment state or release changed")

type DeploymentRequest struct {
	RequestID             string `json:"requestId"`
	TenantID              string `json:"tenantId"`
	ServerID              string `json:"serverId"`
	ReleaseID             string `json:"releaseId"`
	ReleaseDigest         string `json:"releaseDigest"`
	Action                string `json:"action"`
	ExpectedRevision      int64  `json:"expectedRevision"`
	ExpectedConfigVersion int64  `json:"expectedConfigVersion"`
	ExpectedAccessVersion int64  `json:"expectedAccessVersion"`
	ExpectedGeneration    int64  `json:"expectedGeneration"`
	ExpectedReleaseID     string `json:"expectedReleaseId"`
	Reason                string `json:"reason"`
	Confirmed             bool   `json:"confirmed"`
}
type DeploymentJob struct {
	ID            string               `json:"id"`
	TenantID      string               `json:"tenantId"`
	ServerID      string               `json:"serverId"`
	State         string               `json:"state"`
	Phase         string               `json:"phase"`
	ErrorCode     string               `json:"errorCode,omitempty"`
	AgentAttempts int                  `json:"agentAttempts"`
	Generation    int64                `json:"generation"`
	Operation     deployment.Operation `json:"operation"`
	Release       deployment.Release   `json:"release"`
}
type deploymentBinding struct {
	ServerID string `json:"serverId"`
	TenantID string `json:"tenantId"`
	Address  string `json:"address"`
	Host     string `json:"host"`
	Runtime  string `json:"runtime"`
	Mode     string `json:"mode"`
	State    string `json:"state"`
	Revision int64  `json:"revision"`
	Config   int64  `json:"config"`
	Access   int64  `json:"access"`
}
type DeploymentControl interface {
	AgentInspector
	Deployment(context.Context, string, string, deployment.ControlRequest) (deployment.ControlReport, error)
}

func (a AgentRPC) Deployment(ctx context.Context, server, action string, in deployment.ControlRequest) (deployment.ControlReport, error) {
	var out deployment.ControlReport
	if a.Peers[server] == nil || !slices.Contains([]string{"status", "submit", "retry"}, action) {
		return out, ErrAgentUnavailable
	}
	if e := a.Peers[server].Call(ctx, "/internal/agent/deployment/"+action, in, &out); e != nil || out.Nonce != in.Nonce {
		return deployment.ControlReport{}, ErrAgentUnavailable
	}
	return out, nil
}

// Must be called while holding the tenant row fence. Even unknown or failed
// deployment outcomes retain this durable exclusion until verified complete.
func noPendingDeployment(ctx context.Context, tx pgx.Tx, tenant string) error {
	return noPendingDeploymentExcept(ctx, tx, tenant, "")
}

func noPendingDeploymentExcept(ctx context.Context, tx pgx.Tx, tenant, maintenance string) error {
	var pending bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_deployment_jobs WHERE tenant_id=$1 AND state<>'completed') OR
 EXISTS(SELECT 1 FROM platform_backup_jobs WHERE tenant_id=$1 AND state IN ('pending','unconfirmed'))`, tenant).Scan(&pending); e != nil {
		return e
	}
	if pending {
		return ErrDeploymentMaintenance
	}
	return noPendingMaintenance(ctx, tx, tenant, maintenance)
}
func deploymentFence(ctx context.Context, tx pgx.Tx, tenant, server string) (deploymentBinding, error) {
	var b deploymentBinding
	b.TenantID, b.ServerID = tenant, server
	var archived bool
	if e := tx.QueryRow(ctx, `SELECT http_base_url,status,config_version,access_version,archived_at IS NOT NULL FROM platform_tenants WHERE id=$1 FOR UPDATE`, tenant).Scan(&b.Address, &b.State, &b.Config, &b.Access, &archived); e != nil {
		return b, e
	}
	if archived {
		return b, ErrTenantArchiveBlocked
	}
	if b.State != "provisioning" && b.State != "suspended" {
		return b, ErrDeploymentMaintenance
	}
	var suspended, pending bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_realm_jobs WHERE tenant_id=$1 AND access_version=$2 AND NOT enabled AND state='completed'),
 EXISTS(SELECT 1 FROM platform_realm_jobs WHERE tenant_id=$1 AND state<>'completed') OR
 EXISTS(SELECT 1 FROM platform_legacy_import_batches WHERE tenant_id=$1 AND state<>'completed')`, tenant, b.Access).Scan(&suspended, &pending); e != nil {
		return b, e
	}
	if pending || (b.State == "suspended" && !suspended) || (b.State == "provisioning" && b.Access != 1) {
		return b, ErrDeploymentMaintenance
	}
	if e := tx.QueryRow(ctx, `SELECT host_fingerprint,runtime,isolation_mode,revision FROM platform_servers WHERE id=$1 AND tenant_id=$2 FOR SHARE`, server, tenant).Scan(&b.Host, &b.Runtime, &b.Mode, &b.Revision); e != nil {
		return b, e
	}
	return b, nil
}

const deploymentColumns = `id,tenant_id,server_id,state,phase,error_code,agent_attempts,generation,operation,release`

func scanDeployment(row pgx.Row) (DeploymentJob, error) {
	var j DeploymentJob
	var op, release []byte
	e := row.Scan(&j.ID, &j.TenantID, &j.ServerID, &j.State, &j.Phase, &j.ErrorCode, &j.AgentAttempts, &j.Generation, &op, &release)
	if e == nil {
		e = json.Unmarshal(op, &j.Operation)
	}
	if e == nil {
		e = json.Unmarshal(release, &j.Release)
	}
	return j, e
}
func deploymentReplay(ctx context.Context, tx pgx.Tx, actor, request string, input []byte) (DeploymentJob, bool, error) {
	var id string
	var same bool
	e := tx.QueryRow(ctx, `SELECT id,input=$3::jsonb FROM platform_deployment_jobs WHERE actor_id=$1 AND request_id=$2`, actor, request, input).Scan(&id, &same)
	if errors.Is(e, pgx.ErrNoRows) {
		return DeploymentJob{}, false, nil
	}
	if e != nil {
		return DeploymentJob{}, false, e
	}
	if !same {
		return DeploymentJob{}, false, ErrRequestChanged
	}
	j, e := scanDeployment(tx.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM platform_deployment_jobs WHERE id=$1`, id))
	return j, true, e
}
func (s *Store) RequestDeployment(ctx context.Context, actor, token string, in DeploymentRequest, catalog map[string]deployment.Release, agent DeploymentControl) (DeploymentJob, error) {
	var empty DeploymentJob
	in.Reason = strings.TrimSpace(in.Reason)
	if !tenancy.ValidID(actor) || len(token) != 43 || !tenancy.ValidID(in.RequestID) || !tenancy.ValidID(in.ServerID) || !tenancy.ValidID(in.TenantID) || !tenancy.ValidID(in.ReleaseID) ||
		!adminReason(in.Reason, in.Confirmed) || in.ExpectedRevision < 1 || in.ExpectedConfigVersion < 1 || in.ExpectedAccessVersion < 1 || in.ExpectedGeneration < 0 ||
		(in.ExpectedGeneration == 0 && in.ExpectedReleaseID != "") || (in.ExpectedGeneration > 0 && !tenancy.ValidID(in.ExpectedReleaseID)) || !slices.Contains([]string{"deploy", "rollback"}, in.Action) {
		return empty, tenancy.ErrInvalid
	}
	input, _ := json.Marshal(in)
	var before deploymentBinding
	// Short transaction before network: reject active tenants and unauthenticated
	// requests without even asking the agent about its execution state.
	first, e := s.pool.Begin(ctx)
	if e != nil {
		return empty, e
	}
	defer first.Rollback(ctx)
	if _, e = first.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,142))`, actor+":"+in.RequestID); e != nil {
		return empty, e
	}
	if e = serverOperator(ctx, first, actor, token); e != nil {
		return empty, e
	}
	if j, found, e := deploymentReplay(ctx, first, actor, in.RequestID, input); e != nil || found {
		return j, e
	}
	before, e = deploymentFence(ctx, first, in.TenantID, in.ServerID)
	if e != nil {
		return empty, e
	}
	if e = noPendingDeployment(ctx, first, in.TenantID); e != nil {
		return empty, e
	}
	if e = first.Commit(ctx); e != nil {
		return empty, e
	}
	release, ok := catalog[in.ReleaseID]
	if !ok || !release.Valid() || release.Digest() != in.ReleaseDigest || release.Runtime != before.Runtime || !release.MatchesTarget(in.TenantID, in.ServerID) {
		return empty, ErrDeploymentChanged
	}
	if before.Revision != in.ExpectedRevision || before.Config != in.ExpectedConfigVersion || before.Access != in.ExpectedAccessVersion {
		return empty, ErrDeploymentChanged
	}
	if agent == nil {
		return empty, ErrAgentUnavailable
	}
	nonce, e := tenancy.Secret()
	if e != nil {
		return empty, e
	}
	inspection, e := agent.Inspect(ctx, in.ServerID, nonce)
	if e != nil || !inspection.Valid(nonce, in.ServerID, in.TenantID, before.Address) || inspection.HostFingerprint != before.Host || inspection.Runtime != before.Runtime || inspection.IsolationMode != before.Mode || !slices.Contains(inspection.Capabilities, "deploy") {
		return empty, ErrAgentUnavailable
	}
	nonce, e = tenancy.Secret()
	if e != nil {
		return empty, e
	}
	status, e := agent.Deployment(ctx, in.ServerID, "status", deployment.ControlRequest{Nonce: nonce})
	st := status.Status
	if e != nil || status.Nonce != nonce || st.ServerID != in.ServerID || st.TenantID != in.TenantID || st.HostFingerprint != before.Host || st.ActiveOperationID != "" || st.ActiveBackupID != "" || st.Generation != in.ExpectedGeneration || st.CurrentReleaseID != in.ExpectedReleaseID {
		return empty, ErrDeploymentChanged
	}
	if st.Generation == 0 {
		if st.CurrentReleaseDigest != "" {
			return empty, ErrDeploymentChanged
		}
	} else {
		old, ok := catalog[st.CurrentReleaseID]
		if !ok || !old.Valid() || old.Digest() != st.CurrentReleaseDigest || old.Runtime != release.Runtime || !old.MatchesTarget(in.TenantID, in.ServerID) {
			return empty, ErrDeploymentChanged
		}
		if in.Action == "deploy" && (release.Sequence < old.Sequence || release.SchemaVersion < old.SchemaVersion) {
			return empty, ErrDeploymentChanged
		}
		if in.Action == "rollback" && (release.Sequence >= old.Sequence || release.SchemaVersion != old.SchemaVersion || !slices.Contains(old.RollbackTo, release.ID)) {
			return empty, ErrDeploymentChanged
		}
	}
	if in.Action == "rollback" && st.Generation == 0 {
		return empty, ErrDeploymentChanged
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,142))`, actor+":"+in.RequestID); e != nil {
		return empty, e
	}
	// A rolling platform update must not concurrently assign different release
	// metadata to the same ID on two independently locked tenants.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,144))`, in.ReleaseID); e != nil {
		return empty, e
	}
	if e = serverOperator(ctx, tx, actor, token); e != nil {
		return empty, e
	}
	if j, found, e := deploymentReplay(ctx, tx, actor, in.RequestID, input); e != nil || found {
		return j, e
	}
	bound, e := deploymentFence(ctx, tx, in.TenantID, in.ServerID)
	if e != nil {
		return empty, e
	}
	if bound != before {
		return empty, ErrDeploymentChanged
	}
	if e = noPendingDeployment(ctx, tx, in.TenantID); e != nil {
		return empty, e
	}
	// Reject a reset/replaced agent journal and any redefinition of a release ID.
	var previous int64
	var priorRelease, priorDigest string
	e = tx.QueryRow(ctx, `SELECT generation,operation->>'releaseId',operation->>'releaseDigest' FROM platform_deployment_jobs WHERE server_id=$1 AND state='completed' ORDER BY generation DESC LIMIT 1`, in.ServerID).Scan(&previous, &priorRelease, &priorDigest)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return empty, e
	}
	if previous != st.Generation || priorRelease != st.CurrentReleaseID || priorDigest != st.CurrentReleaseDigest {
		return empty, ErrDeploymentChanged
	}
	var changed bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_deployment_jobs WHERE operation->>'releaseId'=$1 AND operation->>'releaseDigest'<>$2)`, in.ReleaseID, in.ReleaseDigest).Scan(&changed); e != nil {
		return empty, e
	}
	if changed {
		return empty, ErrDeploymentChanged
	}
	id, e := newID("deploy")
	if e != nil {
		return empty, e
	}
	op := deployment.Operation{ID: id, ServerID: in.ServerID, TenantID: in.TenantID, HostFingerprint: before.Host, ReleaseID: release.ID, ReleaseDigest: release.Digest(), ExpectedGeneration: st.Generation, ExpectedReleaseID: st.CurrentReleaseID, Action: in.Action}
	opJSON, _ := json.Marshal(op)
	releaseJSON, _ := json.Marshal(release)
	bindingJSON, _ := json.Marshal(before)
	j, e := scanDeployment(tx.QueryRow(ctx, `INSERT INTO platform_deployment_jobs(id,actor_id,request_id,tenant_id,server_id,input,operation,release,binding) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+deploymentColumns, id, actor, in.RequestID, in.TenantID, in.ServerID, input, opJSON, releaseJSON, bindingJSON))
	if e != nil {
		return empty, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,reason,metadata) VALUES($1,'deployment.requested',$2,$3,$4,jsonb_build_object('serverId',$5::text,'releaseId',$6::text,'releaseDigest',$7::text,'action',$8::text,'accessVersion',$9::bigint))`, actor, in.TenantID, id, in.Reason, in.ServerID, release.ID, release.Digest(), in.Action, before.Access); e != nil {
		return empty, e
	}
	return j, tx.Commit(ctx)
}

type DeploymentRetry struct {
	RequestID        string `json:"requestId"`
	ExpectedAttempts int    `json:"expectedAttempts"`
	Reason           string `json:"reason"`
	Confirmed        bool   `json:"confirmed"`
}

func (s *Store) RetryDeployment(ctx context.Context, actor, token, id string, in DeploymentRetry) (DeploymentJob, error) {
	var empty DeploymentJob
	in.Reason = strings.TrimSpace(in.Reason)
	if !tenancy.ValidID(actor) || !tenancy.ValidID(id) || !tenancy.ValidID(in.RequestID) || in.ExpectedAttempts < 1 || !adminReason(in.Reason, in.Confirmed) {
		return empty, tenancy.ErrInvalid
	}
	raw, _ := json.Marshal(in)
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,143))`, actor+":"+in.RequestID); e != nil {
		return empty, e
	}
	if e = serverOperator(ctx, tx, actor, token); e != nil {
		return empty, e
	}
	j, e := scanDeployment(tx.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM platform_deployment_jobs WHERE id=$1`, id))
	if e != nil {
		return empty, e
	}
	var prior string
	var same bool
	e = tx.QueryRow(ctx, `SELECT job_id,input=$3::jsonb FROM platform_deployment_retries WHERE actor_id=$1 AND request_id=$2`, actor, in.RequestID, raw).Scan(&prior, &same)
	if e == nil {
		if prior != id || !same {
			return empty, ErrRequestChanged
		}
		return j, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return empty, e
	}
	if _, e = deploymentFence(ctx, tx, j.TenantID, j.ServerID); e != nil {
		return empty, e
	}
	j, e = scanDeployment(tx.QueryRow(ctx, `UPDATE platform_deployment_jobs SET state='pending',phase='retry_queued',error_code='',retry_from=$2,retry_at=clock_timestamp(),updated_at=now()
 WHERE id=$1 AND state='unconfirmed' AND agent_attempts=$2 AND lease_id IS NULL RETURNING `+deploymentColumns, id, in.ExpectedAttempts))
	if errors.Is(e, pgx.ErrNoRows) {
		return empty, ErrDeploymentChanged
	}
	if e != nil {
		return empty, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_deployment_retries(actor_id,request_id,job_id,input) VALUES($1,$2,$3,$4)`, actor, in.RequestID, id, raw); e != nil {
		return empty, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,reason,metadata) VALUES($1,'deployment.retry.requested',$2,$3,$4,jsonb_build_object('expectedAttempts',$5::integer))`, actor, j.TenantID, id, in.Reason, in.ExpectedAttempts); e != nil {
		return empty, e
	}
	return j, tx.Commit(ctx)
}

type DeploymentWorker struct {
	Store     *Store
	Agent     DeploymentControl
	Catalog   map[string]deployment.Release
	Readiness func(context.Context, string, string) (tenancy.Readiness, error)
}
type deploymentWork struct {
	Job                 DeploymentJob
	Binding             deploymentBinding
	Lease               string
	RetryFrom, Attempts int
	AgentSeen           bool
}

func (w DeploymentWorker) claim(ctx context.Context) (*deploymentWork, error) {
	lease, e := tenancy.Secret()
	if e != nil {
		return nil, e
	}
	var id string
	e = w.Store.pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM platform_deployment_jobs WHERE NOT EXISTS(SELECT 1 FROM platform_recovery_holds h WHERE h.kind='platform_deployment_jobs' AND h.object_id=platform_deployment_jobs.id) AND state='pending' AND retry_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE platform_deployment_jobs j SET lease_id=$1,lease_until=clock_timestamp()+interval '90 seconds',attempts=attempts+1 FROM candidate c WHERE j.id=c.id RETURNING j.id`, lease).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	j, e := scanDeployment(w.Store.pool.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM platform_deployment_jobs WHERE id=$1 AND lease_id=$2`, id, lease))
	if e != nil {
		return nil, e
	}
	work := &deploymentWork{Job: j, Lease: lease}
	var raw []byte
	e = w.Store.pool.QueryRow(ctx, `SELECT binding,retry_from,attempts,agent_seen FROM platform_deployment_jobs WHERE id=$1 AND lease_id=$2`, id, lease).Scan(&raw, &work.RetryFrom, &work.Attempts, &work.AgentSeen)
	if e == nil {
		e = json.Unmarshal(raw, &work.Binding)
	}
	return work, e
}
func (w DeploymentWorker) fence(ctx context.Context, tx pgx.Tx, j *deploymentWork) error {
	b, e := deploymentFence(ctx, tx, j.Job.TenantID, j.Job.ServerID)
	if e != nil {
		return e
	}
	if b != j.Binding {
		return ErrDeploymentChanged
	}
	var valid bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_deployment_jobs WHERE id=$1 AND state='pending' AND lease_id=$2 AND lease_until>clock_timestamp())`, j.Job.ID, j.Lease).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return ErrDeploymentChanged
	}
	return nil
}
func matchingDeployment(j *deploymentWork, r deployment.ControlReport) bool {
	op := j.Job.Operation
	s := r.Status
	p := r.Receipt
	if s.ServerID != op.ServerID || s.TenantID != op.TenantID || s.HostFingerprint != op.HostFingerprint || s.ActiveBackupID != "" || p.Operation != op || p.Attempts < 0 || p.UpdatedAt.IsZero() {
		return false
	}
	if p.State == "completed" {
		return p.Attempts > 0 && p.Generation == op.ExpectedGeneration+1 && s.Generation == p.Generation && s.CurrentReleaseID == op.ReleaseID && s.CurrentReleaseDigest == op.ReleaseDigest && s.ActiveOperationID == ""
	}
	return slices.Contains([]string{"pending", "applying", "unconfirmed"}, p.State) && s.Generation == op.ExpectedGeneration && s.CurrentReleaseID == op.ExpectedReleaseID && s.ActiveOperationID == op.ID && p.Generation == 0 && (p.State == "pending" || p.Attempts > 0)
}
func (w DeploymentWorker) Once(ctx context.Context) (bool, error) {
	j, e := w.claim(ctx)
	if e != nil || j == nil {
		return false, e
	}
	state, phase, code, agentAttempts, generation := "pending", "contacting_agent", "", j.Job.AgentAttempts, j.Job.Generation
	workErr := func() error {
		if w.Agent == nil || w.Readiness == nil {
			return ErrAgentUnavailable
		}
		release, ok := w.Catalog[j.Job.Release.ID]
		if !ok || release.Digest() != j.Job.Operation.ReleaseDigest || release.Digest() != j.Job.Release.Digest() || !release.MatchesTarget(j.Job.TenantID, j.Job.ServerID) {
			return ErrDeploymentChanged
		}
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
		// The platform pause receipt is necessary but not sufficient: check the
		// live enterprise's durable gate before a first dispatch. Failed legacy
		// binaries without this field cannot silently bypass maintenance.
		if !j.AgentSeen && j.Binding.State == "suspended" {
			nonce, e := tenancy.Secret()
			if e != nil {
				return e
			}
			r, e := w.Readiness(ctx, j.Job.TenantID, nonce)
			if e != nil || r.Nonce != nonce || r.TenantID != j.Job.TenantID || r.HTTPBaseURL != j.Binding.Address || !r.Checks["databaseBinding"] || r.Realm == nil || r.Realm.Version != j.Binding.Access || r.Realm.Enabled || !r.Realm.SuspensionConfirmed {
				return ErrDeploymentMaintenance
			}
		}
		// Submit is idempotent and doubles as a poll: a lost acknowledgement can
		// never cause a fresh operation or automatic retry of an unknown apply.
		nonce, e := tenancy.Secret()
		if e != nil {
			return e
		}
		request := deployment.ControlRequest{Nonce: nonce, Operation: j.Job.Operation}
		action := "submit"
		if j.AgentSeen {
			action = "status"
		}
		r, e := w.Agent.Deployment(ctx, j.Job.ServerID, action, request)
		if e != nil || r.Nonce != nonce || !matchingDeployment(j, r) {
			return ErrAgentUnavailable
		}
		if r.Receipt.State != "completed" {
			expectedDigest := ""
			if j.Job.Operation.ExpectedReleaseID != "" {
				expectedDigest = w.Catalog[j.Job.Operation.ExpectedReleaseID].Digest()
			}
			if r.Status.CurrentReleaseDigest != expectedDigest {
				return ErrDeploymentChanged
			}
		}
		j.AgentSeen = true
		if r.Receipt.State == "unconfirmed" && j.RetryFrom == r.Receipt.Attempts {
			request.ExpectedAttempts = j.RetryFrom
			nonce, e = tenancy.Secret()
			if e != nil {
				return e
			}
			request.Nonce = nonce
			r, e = w.Agent.Deployment(ctx, j.Job.ServerID, "retry", request)
			if e != nil || r.Nonce != nonce || !matchingDeployment(j, r) {
				return ErrAgentUnavailable
			}
		}
		agentAttempts = r.Receipt.Attempts
		phase = r.Receipt.State
		if phase == "unconfirmed" {
			state = "unconfirmed"
			code = "DEPLOYMENT_EXECUTION_UNCONFIRMED"
			return nil
		}
		if phase != "completed" {
			return nil
		}
		phase = "verifying_business"
		nonce, e = tenancy.Secret()
		if e != nil {
			return e
		}
		ready, e := w.Readiness(ctx, j.Job.TenantID, nonce)
		if e != nil || !ready.Valid(nonce, j.Job.TenantID, j.Binding.Address) || ready.SchemaVersion != release.SchemaVersion || ready.Realm == nil || ready.Realm.Version != j.Binding.Access {
			return ErrUnavailable
		}
		if j.Binding.State == "suspended" && (ready.Realm.Enabled || !ready.Realm.SuspensionConfirmed) {
			return ErrDeploymentMaintenance
		}
		state, phase, generation = "completed", "verified", r.Receipt.Generation
		return nil
	}()
	if workErr != nil {
		code = "DEPLOYMENT_CONTROL_UNCONFIRMED"
		if phase == "verifying_business" {
			code = "DEPLOYMENT_BUSINESS_UNCONFIRMED"
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
		// Successful pending/applying polls are not failures. Counting them made
		// the first transient readiness/ACK error after a long deployment wait
		// several minutes. Retain consecutive failures durably across restarts,
		// but reset on real progress or a change of error/phase.
		failures = 1
		if j.Job.Phase == phase && j.Job.ErrorCode == code {
			failures = max(1, min(j.Attempts, 10))
		}
		delay = min(300, 1<<min(failures, 9))
	}
	tag, e := tx.Exec(ctx, `UPDATE platform_deployment_jobs SET state=$3,phase=$4,error_code=$5,agent_attempts=$6,generation=$7,lease_id=NULL,lease_until=NULL,retry_at=clock_timestamp()+($8::integer*interval '1 second'),updated_at=now(),agent_seen=$9,attempts=$10
	 WHERE id=$1 AND lease_id=$2 AND lease_until>clock_timestamp() AND state='pending'`, j.Job.ID, j.Lease, state, phase, code, agentAttempts, generation, delay, j.AgentSeen, failures)
	if e != nil {
		return true, e
	}
	if tag.RowsAffected() != 1 {
		return true, ErrDeploymentChanged
	}
	if state == "completed" || state == "unconfirmed" {
		if _, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,job_id,metadata) VALUES('system:deployment-worker',$1,$2,$3,jsonb_build_object('releaseId',$4::text,'generation',$5::bigint,'agentAttempts',$6::integer,'scope','container-and-business-readiness'))`, "deployment."+state, j.Job.TenantID, j.Job.ID, j.Job.Release.ID, generation, agentAttempts); e != nil {
			return true, e
		}
	}
	return true, tx.Commit(ctx)
}
func (w DeploymentWorker) Run(ctx context.Context) {
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
		}
	}
}
