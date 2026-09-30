package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
)

func actorID(r *http.Request) string {
	id, _ := r.Context().Value(adminContextKey{}).(string)
	return id
}

func (a *API) adminMe(w http.ResponseWriter, r *http.Request) {
	var username, role string
	var version int64
	err := a.Store.pool.QueryRow(r.Context(), `SELECT username,role,auth_version FROM platform_admin_accounts WHERE id=$1 AND enabled`, actorID(r)).Scan(&username, &role, &version)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"id": actorID(r), "username": username, "role": role, "authVersion": version})
}

func (a *API) adminLogout(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	_, err := a.Store.pool.Exec(r.Context(), `UPDATE platform_admin_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE token_hash=$1 AND admin_id=$2`, tenancy.Hash(token), actorID(r))
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func pagination(r *http.Request) (int, int, error) {
	page, size := 1, 25
	var err error
	if v := r.URL.Query().Get("page"); v != "" {
		page, err = strconv.Atoi(v)
		if err != nil {
			return 0, 0, tenancy.ErrInvalid
		}
	}
	if v := r.URL.Query().Get("pageSize"); v != "" {
		size, err = strconv.Atoi(v)
		if err != nil {
			return 0, 0, tenancy.ErrInvalid
		}
	}
	if page < 1 || page > 10000 || size < 1 || size > 100 {
		return 0, 0, tenancy.ErrInvalid
	}
	return page, size, nil
}

// Every query is a code-owned constant with parameterized filters. Count and
// page share one database snapshot, including the empty/last page case.
func (a *API) adminPage(w http.ResponseWriter, r *http.Request, query string, filters ...any) {
	page, size, err := pagination(r)
	if err != nil {
		failure(w, err)
		return
	}
	args := append([]any{size, (page - 1) * size}, filters...)
	var items json.RawMessage
	var total int64
	err = a.Store.pool.QueryRow(r.Context(), fmt.Sprintf(`WITH filtered AS (%s)
 SELECT COALESCE((SELECT jsonb_agg(data ORDER BY ordering) FROM
 (SELECT data,ordering FROM filtered ORDER BY ordering LIMIT $1 OFFSET $2) p),'[]'::jsonb),
 (SELECT count(*) FROM filtered)`, query), args...).Scan(&items, &total)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": size})
}

func (a *API) adminAccounts(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT id AS ordering,jsonb_build_object('id',id,'phone',phone,'state',state,
 'tenantId',tenant_id,'localUserId',local_user_id,'assignmentVersion',assignment_version,'createdAt',created_at,
 'globallyBlocked',globally_blocked,'authVersion',auth_version,'accessPending',EXISTS(SELECT 1 FROM platform_access_jobs j WHERE j.account_id=platform_accounts.id AND j.state<>'completed')) AS data
 FROM platform_accounts WHERE ($3='' OR tenant_id=$3) AND ($4='' OR state=$4 OR ($4='blocked' AND globally_blocked))
 AND ($5='' OR strpos(phone,$5)>0 OR id=$5)`, r.URL.Query().Get("tenantId"), r.URL.Query().Get("state"), strings.TrimSpace(r.URL.Query().Get("q")))
}

func (a *API) adminCodes(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state != "" && state != "enabled" && state != "disabled" {
		failure(w, tenancy.ErrInvalid)
		return
	}
	a.adminPage(w, r, `SELECT id AS ordering,jsonb_build_object('id',id,'tenantId',tenant_id,'enabled',enabled,
 'suffix',suffix,'createdAt',created_at) AS data FROM platform_enterprise_codes
 WHERE ($3='' OR tenant_id=$3) AND ($4='' OR enabled=($4='enabled'))`, r.URL.Query().Get("tenantId"), state)
}

func (a *API) adminAudits(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT -id AS ordering,jsonb_build_object('id',id::text,'actorId',actor_id,
 'action',action,'accountId',account_id,'tenantId',tenant_id,'jobId',job_id,'reason',reason,
 'metadata',metadata,'createdAt',created_at) AS data FROM platform_audits
 WHERE ($3='' OR tenant_id=$3) AND ($4='' OR action=$4)`, r.URL.Query().Get("tenantId"), r.URL.Query().Get("action"))
}

func (s *Store) SetEnterpriseCodeStatus(ctx context.Context, id string, enabled bool, actor, reason string, confirmed bool) error {
	if id == "" || actor == "" || !adminReason(reason, confirmed) {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var tenant string
	var before bool
	err = tx.QueryRow(ctx, `SELECT tenant_id FROM platform_enterprise_codes WHERE id=$1`, id).Scan(&tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	var archived bool
	if err = tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM platform_tenants WHERE id=$1 FOR SHARE`, tenant).Scan(&archived); err != nil {
		return err
	}
	if archived {
		return ErrTenantArchiveBlocked
	}
	if err = tx.QueryRow(ctx, `SELECT enabled FROM platform_enterprise_codes WHERE id=$1 FOR UPDATE`, id).Scan(&before); err != nil {
		return err
	}
	if before == enabled {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_enterprise_codes SET enabled=$2 WHERE id=$1`, id, enabled); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,tenant_id,reason,metadata)
 VALUES($1,'tenant.code.status.updated',$2,$3,jsonb_build_object('codeId',$4::text,'before',$5::boolean,'after',$6::boolean))`, actor, tenant, reason, id, before, enabled)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *API) adminCodeStatus(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Enabled   *bool
		Reason    string
		Confirmed bool
	}
	if readJSON(w, r, &p) != nil || p.Enabled == nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if err := a.Store.SetEnterpriseCodeStatus(r.Context(), r.PathValue("id"), *p.Enabled, actorID(r), p.Reason, p.Confirmed); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]bool{"enabled": *p.Enabled})
}

type JobRepair struct {
	ExpectedStep       string  `json:"expectedStep"`
	PersonalInviteCode *string `json:"personalInviteCode,omitempty"`
	Reason             string  `json:"reason"`
	Confirmed          bool    `json:"confirmed"`
}

// Retry never changes the step or the target identity. Repairing a referral is
// allowed only after a known transactionally rejected prepare, not an uncertain
// network timeout (the enterprise might already have committed that prepare).
func (s *Store) RepairJob(ctx context.Context, id, actor string, in JobRepair, correct bool) error {
	if !tenancy.ValidID(id) || actor == "" || !adminReason(in.Reason, in.Confirmed) {
		return tenancy.ErrInvalid
	}
	if in.ExpectedStep != "revoke_source" && in.ExpectedStep != "prepare_target" && in.ExpectedStep != "activate" {
		return tenancy.ErrInvalid
	}
	if correct && (in.PersonalInviteCode == nil || len(*in.PersonalInviteCode) > 80) {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var accountID string
	if err = tx.QueryRow(ctx, `SELECT account_id FROM platform_jobs WHERE id=$1`, id).Scan(&accountID); err != nil {
		return err
	}
	a, err := readAccount(ctx, tx, accountID)
	if err != nil {
		return err
	}
	var kind, step, code string
	var leased, blocked bool
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT kind,step,error_code,COALESCE(lease_until>clock_timestamp(),false),blocked,input FROM platform_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&kind, &step, &code, &leased, &blocked, &raw)
	if err != nil {
		return err
	}
	if leased || step != in.ExpectedStep || (a.State != "provisioning" && a.State != "transferring") {
		return ErrConflict
	}
	action := "identity.job.retry.requested"
	if correct {
		if kind != "registration" || step != "prepare_target" || !blocked || !strings.HasPrefix(code, "INVITE_") {
			return ErrConflict
		}
		var input ProvisionInput
		if err = json.Unmarshal(raw, &input); err != nil {
			return err
		}
		input.PersonalInviteCode = strings.ToUpper(strings.TrimSpace(*in.PersonalInviteCode))
		raw, err = json.Marshal(input)
		if err != nil {
			return err
		}
		action = "identity.job.registration.corrected"
	}
	if _, err = tx.Exec(ctx, `UPDATE platform_jobs SET input=$2,blocked=false,error_code='',retry_at=now(),lease_id=NULL,lease_until=NULL,updated_at=now() WHERE id=$1`, id, raw); err != nil {
		return err
	}
	// Never audit passwords, invite code contents or remote error bodies.
	if _, err = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,account_id,job_id,reason,metadata) VALUES($1,$2,$3,$4,$5,jsonb_build_object('step',$6::text,'previousError',$7::text))`, actor, action, accountID, id, in.Reason, step, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *API) repairJob(w http.ResponseWriter, r *http.Request, correct bool) {
	var p JobRepair
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if err := a.Store.RepairJob(r.Context(), r.PathValue("id"), actorID(r), p, correct); err != nil {
		failure(w, err)
		return
	}
	respond(w, 202, map[string]string{"jobId": r.PathValue("id"), "status": "pending"})
}
func (a *API) adminRetryJob(w http.ResponseWriter, r *http.Request) { a.repairJob(w, r, false) }
func (a *API) adminCorrectRegistration(w http.ResponseWriter, r *http.Request) {
	a.repairJob(w, r, true)
}
