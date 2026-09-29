package platform

import (
	"net/http"
	"strings"

	"github.com/linli/im/server/internal/tenancy"
)

func (a *API) adminSetAccess(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID, Reason   string
		ExpectedAuthVersion int64
		Blocked             *bool
		Confirmed           bool
	}
	if readJSON(w, r, &in) != nil || in.Blocked == nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	job, err := a.Store.RequestAccess(r.Context(), r.PathValue("id"), in.RequestID, actorID(r), in.Reason, in.ExpectedAuthVersion, *in.Blocked, in.Confirmed)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 202, job)
}

func (a *API) adminAccessJobs(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT row(-extract(epoch FROM j.created_at),j.id) AS ordering,jsonb_build_object('jobId',j.id,'requestId',j.request_id,'accountId',j.account_id,'blocked',j.blocked,'status',j.state,'tenantId',COALESCE(j.tenant_id,a.tenant_id),'errorCode',j.error_code,'attempts',j.attempts,'updatedAt',j.updated_at) AS data FROM platform_access_jobs j JOIN platform_accounts a ON a.id=j.account_id WHERE ($3='' OR COALESCE(j.tenant_id,a.tenant_id)=$3) AND ($4='' OR j.state=$4) AND ($5='' OR j.account_id=$5) AND ($6='' OR j.account_id=$6 OR j.request_id=$6 OR j.id=$6)`, r.URL.Query().Get("tenantId"), r.URL.Query().Get("state"), r.URL.Query().Get("accountId"), strings.TrimSpace(r.URL.Query().Get("q")))
}
