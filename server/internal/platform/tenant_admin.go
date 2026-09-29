package platform

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

// This contract is only served on the mutually authenticated control listener.
// Tenant identity never comes from the JSON body or a forwarded HTTP header.
type TenantAdminCreate struct {
	RequestID string `json:"requestId"`
	Actor     string `json:"actor"`
	Phone     string `json:"phone"`
	Name      string `json:"name"`
	Password  string `json:"password"`
	Gender    string `json:"gender"`
	Reason    string `json:"reason"`
	Confirmed bool   `json:"confirmed"`
}

type TenantAccountJob struct {
	JobID       string    `json:"jobId"`
	RequestID   string    `json:"requestId"`
	LocalUserID string    `json:"localUserId"`
	Phone       string    `json:"phone"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	ErrorCode   string    `json:"errorCode,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type TenantAdminResult struct {
	Item      *TenantAccountJob  `json:"item,omitempty"`
	Items     []TenantAccountJob `json:"items,omitempty"`
	ErrorCode string             `json:"errorCode,omitempty"`
}

func controlTenant(r *http.Request) string {
	identity := tenancy.PeerIdentity(r)
	tenant := strings.TrimPrefix(identity, "spiffe://frogim/tenant/")
	if tenant == identity || !tenancy.ValidID(tenant) {
		return ""
	}
	return tenant
}

func (s *Store) TenantAccountJobs(ctx context.Context, tenant, jobID string) ([]TenantAccountJob, error) {
	if !tenancy.ValidID(tenant) || (jobID != "" && !tenancy.ValidID(jobID)) {
		return nil, tenancy.ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `SELECT j.id,j.request_id,j.target_local_user_id,j.input->>'phone',j.input->>'name',CASE WHEN j.blocked THEN 'blocked' WHEN j.step='completed' THEN 'completed' ELSE 'pending' END,j.error_code,j.created_at FROM platform_jobs j WHERE j.target_tenant_id=$1 AND j.kind='registration' AND j.input->>'method'='admin' AND j.request_id IS NOT NULL AND ($2='' OR j.id=$2) ORDER BY j.created_at DESC,j.id DESC LIMIT 100`, tenant, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TenantAccountJob{}
	for rows.Next() {
		var j TenantAccountJob
		if err = rows.Scan(&j.JobID, &j.RequestID, &j.LocalUserID, &j.Phone, &j.Name, &j.Status, &j.ErrorCode, &j.CreatedAt); err != nil {
			return nil, err
		}
		// Error codes, not upstream error text or authentication material.
		items = append(items, j)
	}
	return items, rows.Err()
}

func (a *API) tenantAdminCreate(w http.ResponseWriter, r *http.Request) {
	tenant := controlTenant(r)
	if tenant == "" {
		failure(w, ErrDenied)
		return
	}
	var p TenantAdminCreate
	if readJSON(w, r, &p) != nil || !tenancy.ValidID(p.RequestID) || !tenancy.ValidID(p.Actor) || !adminReason(p.Reason, p.Confirmed) {
		failure(w, tenancy.ErrInvalid)
		return
	}
	reservation, err := a.Store.Reserve(r.Context(), Registration{Phone: p.Phone, Password: p.Password, Name: p.Name, Gender: p.Gender, Method: "admin", ForcedTenantID: tenant, Actor: "tenant:" + tenant + ":admin:" + p.Actor, RequestID: p.RequestID, Reason: p.Reason})
	if err != nil {
		// Known domain rejections travel in a typed envelope. A transport/storage
		// failure remains HTTP 503: callers must retain the same request ID.
		code := ""
		switch {
		case errors.Is(err, ErrConflict), errors.Is(err, ErrDenied):
			code = "ACCOUNT_UNAVAILABLE"
		case errors.Is(err, ErrRequestChanged):
			code = "ACCOUNT_REQUEST_CHANGED"
		case errors.Is(err, tenancy.ErrInvalid):
			code = "INVALID_ARGUMENT"
		default:
			failure(w, err)
			return
		}
		respond(w, 200, TenantAdminResult{ErrorCode: code})
		return
	}
	items, err := a.Store.TenantAccountJobs(r.Context(), tenant, reservation.JobID)
	if err != nil || len(items) != 1 {
		failure(w, ErrUnavailable)
		return
	}
	respond(w, 200, TenantAdminResult{Item: &items[0]})
}

func (a *API) tenantAdminJobs(w http.ResponseWriter, r *http.Request) {
	tenant := controlTenant(r)
	if tenant == "" {
		failure(w, ErrDenied)
		return
	}
	var p struct {
		JobID string `json:"jobId"`
	}
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	items, err := a.Store.TenantAccountJobs(r.Context(), tenant, p.JobID)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, TenantAdminResult{Items: items})
}
