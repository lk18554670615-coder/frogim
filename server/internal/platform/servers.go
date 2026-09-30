package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
)

var ErrServerChanged = errors.New("server or tenant binding changed")
var ErrAgentUnavailable = errors.New("configured host agent verification unavailable")

type AgentRPC struct{ Peers map[string]*tenancy.RPC }

func (a AgentRPC) Inspect(ctx context.Context, id, nonce string) (deployment.Inspection, error) {
	var report deployment.Inspection
	rpc := a.Peers[id]
	if rpc == nil {
		return report, ErrAgentUnavailable
	}
	err := rpc.Call(ctx, "/internal/agent/inspect", map[string]string{"nonce": nonce}, &report)
	if err != nil {
		return report, ErrAgentUnavailable
	}
	return report, nil
}

type AgentInspector interface {
	Inspect(context.Context, string, string) (deployment.Inspection, error)
}
type ServerOperation struct {
	RequestID             string `json:"requestId"`
	Action                string `json:"action"`
	ServerID              string `json:"serverId"`
	TenantID              string `json:"tenantId"`
	DisplayName           string `json:"displayName"`
	ExpectedRevision      int64  `json:"expectedRevision"`
	ExpectedConfigVersion int64  `json:"expectedConfigVersion"`
	Reason                string `json:"reason"`
	Confirmed             bool   `json:"confirmed"`
}
type Server struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenantId"`
	DisplayName     string    `json:"displayName"`
	HostFingerprint string    `json:"hostFingerprint"`
	IsolationMode   string    `json:"isolationMode"`
	Runtime         string    `json:"runtime"`
	Revision        int64     `json:"revision"`
	VerifiedAt      time.Time `json:"verifiedAt"`
}

func serverOperator(ctx context.Context, tx pgx.Tx, actor, token string) error {
	var role string
	err := tx.QueryRow(ctx, `SELECT a.role FROM platform_admin_accounts a JOIN platform_admin_sessions s ON s.admin_id=a.id
 WHERE a.id=$1 AND s.token_hash=$2 AND a.enabled AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
 AND s.auth_version=a.auth_version FOR SHARE OF a,s`, actor, tenancy.Hash(token)).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	if role != "operator" {
		return ErrDenied
	}
	return nil
}
func serverReplay(ctx context.Context, tx pgx.Tx, actor, request string, input []byte) (Server, bool, error) {
	var result Server
	var same bool
	var snapshot []byte
	err := tx.QueryRow(ctx, `SELECT input=$3::jsonb,snapshot FROM platform_server_operations WHERE actor_id=$1 AND request_id=$2`, actor, request, input).Scan(&same, &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if !same {
		return result, false, ErrRequestChanged
	}
	err = json.Unmarshal(snapshot, &result)
	return result, true, err
}

// Registration is a binding and an authenticated inspection, NOT a deployment.
// No database locks are held while contacting a preconfigured agent. After the
// response, session, tenant revision and server revision are checked again.
func (s *Store) ManageServer(ctx context.Context, actor, token string, in ServerOperation, agent AgentInspector) (Server, error) {
	var result Server
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Reason = strings.TrimSpace(in.Reason)
	if !tenancy.ValidID(actor) || len(token) != 43 || !tenancy.ValidID(in.RequestID) || !tenancy.ValidID(in.ServerID) || !tenancy.ValidID(in.TenantID) || len(in.DisplayName) < 1 || len(in.DisplayName) > 240 || !adminReason(in.Reason, in.Confirmed) || in.ExpectedConfigVersion < 1 ||
		(in.Action != "register" && in.Action != "inspect") || (in.Action == "register" && in.ExpectedRevision != 0) || (in.Action == "inspect" && in.ExpectedRevision < 1) {
		return result, tenancy.ErrInvalid
	}
	input, err := json.Marshal(in)
	if err != nil {
		return result, err
	}
	// Authenticate even replay requests; replay remains possible if the agent
	// later goes offline, without creating another verification or audit record.
	first, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer first.Rollback(ctx)
	if err = serverOperator(ctx, first, actor, token); err != nil {
		return result, err
	}
	replay, found, err := serverReplay(ctx, first, actor, in.RequestID, input)
	if err != nil {
		return result, err
	}
	if found {
		return replay, first.Commit(ctx)
	}
	var address string
	var archived bool
	var configVersion int64
	err = first.QueryRow(ctx, `SELECT http_base_url,config_version,archived_at IS NOT NULL FROM platform_tenants WHERE id=$1`, in.TenantID).Scan(&address, &configVersion, &archived)
	if errors.Is(err, pgx.ErrNoRows) || configVersion != in.ExpectedConfigVersion || archived {
		if err == nil || errors.Is(err, pgx.ErrNoRows) {
			return result, ErrServerChanged
		}
	}
	if err != nil {
		return result, err
	}
	if err = first.Commit(ctx); err != nil {
		return result, err
	}
	nonce, err := tenancy.Secret()
	if err != nil {
		return result, err
	}
	if agent == nil {
		return result, ErrAgentUnavailable
	}
	report, err := agent.Inspect(ctx, in.ServerID, nonce)
	if err != nil || !report.Valid(nonce, in.ServerID, in.TenantID, address) {
		return result, ErrAgentUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	// One short registry write lock serializes host uniqueness and immutable
	// actor/request receipts. Network calls are deliberately outside this lock.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739181)`); err != nil {
		return result, err
	}
	if err = serverOperator(ctx, tx, actor, token); err != nil {
		return result, err
	}
	replay, found, err = serverReplay(ctx, tx, actor, in.RequestID, input)
	if err != nil {
		return result, err
	}
	if found {
		return replay, tx.Commit(ctx)
	}
	var currentAddress string
	err = tx.QueryRow(ctx, `SELECT http_base_url,config_version,archived_at IS NOT NULL FROM platform_tenants WHERE id=$1 FOR SHARE`, in.TenantID).Scan(&currentAddress, &configVersion, &archived)
	if err != nil {
		return result, err
	}
	if archived || currentAddress != address || configVersion != in.ExpectedConfigVersion {
		return result, ErrServerChanged
	}
	if err = noPendingDeployment(ctx, tx, in.TenantID); err != nil {
		return result, err
	}
	scan := func(row pgx.Row) error {
		return row.Scan(&result.ID, &result.TenantID, &result.DisplayName, &result.HostFingerprint, &result.IsolationMode, &result.Runtime, &result.Revision, &result.VerifiedAt)
	}
	const returning = ` RETURNING id,tenant_id,display_name,host_fingerprint,isolation_mode,runtime,revision,verified_at`
	if in.Action == "register" {
		err = scan(tx.QueryRow(ctx, `INSERT INTO platform_servers(id,tenant_id,display_name,host_fingerprint,isolation_mode,runtime,revision,verified_at)
  VALUES($1,$2,$3,$4,$5,$6,1,clock_timestamp())`+returning, in.ServerID, in.TenantID, in.DisplayName, report.HostFingerprint, report.IsolationMode, report.Runtime))
	} else {
		err = scan(tx.QueryRow(ctx, `UPDATE platform_servers SET revision=revision+1,verified_at=clock_timestamp()
  WHERE id=$1 AND tenant_id=$2 AND display_name=$3 AND host_fingerprint=$4 AND isolation_mode=$5 AND runtime=$6 AND revision=$7`+returning,
			in.ServerID, in.TenantID, in.DisplayName, report.HostFingerprint, report.IsolationMode, report.Runtime, in.ExpectedRevision))
	}
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "23505") {
		return result, ErrServerChanged
	}
	if err != nil {
		return result, err
	}
	snapshot, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_server_operations(actor_id,request_id,server_id,input,snapshot) VALUES($1,$2,$3,$4,$5)`, actor, in.RequestID, in.ServerID, input, snapshot); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,reason,metadata)
 VALUES($1,$2,$3,$4,jsonb_build_object('serverId',$5::text,'requestId',$6::text,'revision',$7::bigint,'isolationMode',$8::text,'capability','inspect'))`, actor, "server."+in.Action, in.TenantID, in.Reason, in.ServerID, in.RequestID, result.Revision, result.IsolationMode); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (a *API) adminServers(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT s.id AS ordering,jsonb_build_object('id',s.id,'tenantId',s.tenant_id,'displayName',s.display_name,
 'hostFingerprint',s.host_fingerprint,'isolationMode',s.isolation_mode,'runtime',s.runtime,'revision',s.revision,
 'verifiedAt',s.verified_at,'configVersion',t.config_version) AS data FROM platform_servers s JOIN platform_tenants t ON t.id=s.tenant_id
 WHERE ($3='' OR strpos(s.display_name,$3)>0 OR s.id=$3 OR s.tenant_id=$3)`, strings.TrimSpace(r.URL.Query().Get("q")))
}
func (a *API) adminServerChoices(w http.ResponseWriter, r *http.Request) {
	ids := make([]string, 0, len(a.Agents.Peers))
	for id := range a.Agents.Peers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	respond(w, 200, map[string]any{"serverIds": ids, "capabilities": []string{"inspect"}})
}
func (a *API) adminManageServer(w http.ResponseWriter, r *http.Request) {
	var in ServerOperation
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	out, err := a.Store.ManageServer(r.Context(), actorID(r), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), in, a.Agents)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, out)
}
func (a *API) adminServerOperation(w http.ResponseWriter, r *http.Request) {
	var snapshot json.RawMessage
	err := a.Store.pool.QueryRow(r.Context(), `SELECT snapshot FROM platform_server_operations WHERE actor_id=$1 AND request_id=$2`, actorID(r), r.PathValue("requestId")).Scan(&snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		respond(w, 404, map[string]any{"error": map[string]string{"code": "SERVER_OPERATION_NOT_FOUND"}})
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, snapshot)
}
