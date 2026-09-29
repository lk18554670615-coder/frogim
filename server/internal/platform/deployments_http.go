package platform

import (
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/tenancy"
	"net/http"
	"sort"
	"strings"
)

func (a *API) adminDeploymentReleases(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		deployment.Release
		Digest string `json:"digest"`
	}
	items := []entry{}
	for _, v := range a.DeploymentCatalog {
		items = append(items, entry{v, v.Digest()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	respond(w, 200, map[string]any{"items": items})
}
func (a *API) adminDeployments(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT j.created_at::text||j.id AS ordering,jsonb_build_object('id',j.id,'requestId',j.request_id,'tenantId',j.tenant_id,'serverId',j.server_id,
 'state',j.state,'phase',j.phase,'errorCode',j.error_code,'agentAttempts',j.agent_attempts,'generation',j.generation,'operation',j.operation,'release',j.release,'updatedAt',j.updated_at) AS data
 FROM platform_deployment_jobs j WHERE ($3='' OR j.tenant_id=$3 OR j.id=$3 OR j.request_id=$3)`, strings.TrimSpace(r.URL.Query().Get("q")))
}
func (a *API) adminRequestDeployment(w http.ResponseWriter, r *http.Request) {
	var in DeploymentRequest
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	out, e := a.Store.RequestDeployment(r.Context(), actorID(r), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), in, a.DeploymentCatalog, a.Agents)
	if e != nil {
		failure(w, e)
		return
	}
	respond(w, 200, out)
}
func (a *API) adminRetryDeployment(w http.ResponseWriter, r *http.Request) {
	var in DeploymentRetry
	if readJSON(w, r, &in) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	out, e := a.Store.RetryDeployment(r.Context(), actorID(r), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), r.PathValue("id"), in)
	if e != nil {
		failure(w, e)
		return
	}
	respond(w, 200, out)
}
