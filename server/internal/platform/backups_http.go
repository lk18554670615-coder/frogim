package platform

import (
	"net/http"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
)

func (a *API) adminBackups(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT j.created_at::text||j.id AS ordering,jsonb_build_object('id',j.id,'requestId',j.request_id,'tenantId',j.tenant_id,'serverId',j.server_id,
 'state',j.state,'phase',j.phase,'errorCode',j.error_code,'operation',j.operation,'release',j.release,'receipt',j.receipt,'updatedAt',j.updated_at,
 'controlPending',j.control_action<>'','leased',j.lease_until>clock_timestamp()) AS data
 FROM platform_backup_jobs j WHERE ($3='' OR j.tenant_id=$3 OR j.id=$3 OR j.request_id=$3)`, strings.TrimSpace(r.URL.Query().Get("q")))
}

func (a *API) adminRequestBackup(w http.ResponseWriter, r *http.Request) {
	var in BackupRequest
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	out, e := a.Store.RequestBackup(r.Context(), actorID(r), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), in, a.DeploymentCatalog, a.Agents)
	if e != nil {
		failure(w, e)
		return
	}
	respond(w, 200, out)
}

func (a *API) adminControlBackup(w http.ResponseWriter, r *http.Request) {
	var in BackupAction
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	out, e := a.Store.ControlBackup(r.Context(), actorID(r), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), r.PathValue("id"), in)
	if e != nil {
		failure(w, e)
		return
	}
	respond(w, 200, out)
}
