package platform

import (
	"github.com/linli/im/server/internal/tenancy"
	"net/http"
	"strings"
)

func (a *API) adminSetRealm(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID, Reason     string
		ExpectedAccessVersion int64
		Enabled               *bool
		Confirmed             bool
	}
	if readJSON(w, r, &in) != nil || in.Enabled == nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	job, err := a.Store.RequestRealm(r.Context(), r.PathValue("id"), in.RequestID, actorID(r), in.Reason, in.ExpectedAccessVersion, *in.Enabled, in.Confirmed)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 202, job)
}
func (a *API) adminRealmJobs(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT row(-extract(epoch FROM created_at),id) AS ordering,jsonb_build_object('jobId',id,'requestId',request_id,'tenantId',tenant_id,'enabled',enabled,'accessVersion',access_version,'status',state,'remaining',remaining,'errorCode',error_code,'attempts',attempts,'updatedAt',updated_at) AS data FROM platform_realm_jobs WHERE ($3='' OR tenant_id=$3) AND ($4='' OR state=$4) AND ($5='' OR id=$5 OR request_id=$5 OR tenant_id=$5)`, r.URL.Query().Get("tenantId"), r.URL.Query().Get("state"), strings.TrimSpace(r.URL.Query().Get("q")))
}
