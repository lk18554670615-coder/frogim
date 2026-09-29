package platform

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/tenancy"
)

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var p struct{ RequestID, CurrentPassword, NewPassword string }
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if readJSON(w, r, &p) != nil || len(token) != 43 {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if !a.allow(w, r, "password-change:"+hex.EncodeToString(tenancy.Hash(token)), 5, 10*time.Minute) {
		return
	}
	j, err := a.Store.ChangePassword(r.Context(), p.RequestID, token, p.CurrentPassword, p.NewPassword, a.Peers)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, http.StatusAccepted, j)
}

func (a *API) credentialStatus(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	j, err := a.Store.CredentialStatus(r.Context(), r.PathValue("id"), token)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, j)
}

// The client records the request ID before submitting, so a lost acceptance
// response can be reconciled without storing/retransmitting either password.
func (a *API) passwordChangeStatus(w http.ResponseWriter, r *http.Request) {
	var p struct {
		RequestID string `json:"requestId"`
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	j, err := a.Store.PasswordChangeStatus(r.Context(), p.RequestID, token)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, j)
}

type TenantPasswordReset struct {
	RequestID   string `json:"requestId"`
	Actor       string `json:"actor"`
	LocalUserID string `json:"localUserId"`
	NewPassword string `json:"newPassword"`
	Reason      string `json:"reason"`
	Confirmed   bool   `json:"confirmed"`
}
type TenantCredentialQuery struct {
	LocalUserID string `json:"localUserId"`
	JobID       string `json:"jobId"`
}
type TenantCredentialResult struct {
	Item      *CredentialJob  `json:"item,omitempty"`
	Items     []CredentialJob `json:"items,omitempty"`
	ErrorCode string          `json:"errorCode,omitempty"`
}

func credentialControlResult(w http.ResponseWriter, j CredentialJob, err error) {
	if err == nil {
		respond(w, 200, TenantCredentialResult{Item: &j})
		return
	}
	code := ""
	var rejected *tenancy.OperationRejected
	switch {
	case errors.Is(err, ErrDenied), errors.Is(err, ErrConflict):
		code = "ACCOUNT_UNAVAILABLE"
	case errors.Is(err, ErrRequestChanged):
		code = "REQUEST_CHANGED"
	case errors.Is(err, tenancy.ErrInvalid):
		code = "INVALID_ARGUMENT"
	case errors.As(err, &rejected) && rejected.Code == "TENANT_PASSWORD_POLICY_REJECTED":
		code = rejected.Code
	default:
		failure(w, err)
		return
	}
	respond(w, 200, TenantCredentialResult{ErrorCode: code})
}

func (a *API) tenantAdminPasswordReset(w http.ResponseWriter, r *http.Request) {
	tenant := controlTenant(r)
	if tenant == "" {
		failure(w, ErrDenied)
		return
	}
	var p TenantPasswordReset
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	j, err := a.Store.AdminResetPassword(r.Context(), tenant, p.LocalUserID, p.Actor, p.RequestID, p.NewPassword, p.Reason, p.Confirmed, a.Peers)
	credentialControlResult(w, j, err)
}

func (a *API) tenantAdminCredentialStatus(w http.ResponseWriter, r *http.Request) {
	tenant := controlTenant(r)
	if tenant == "" {
		failure(w, ErrDenied)
		return
	}
	var p TenantCredentialQuery
	if readJSON(w, r, &p) != nil || !tenancy.ValidID(p.LocalUserID) || (p.JobID != "" && !tenancy.ValidID(p.JobID)) {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if p.JobID == "" {
		items, err := a.Store.TenantCredentialJobs(r.Context(), tenant, p.LocalUserID)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, TenantCredentialResult{Items: items})
		return
	}
	j, err := a.Store.TenantCredentialStatus(r.Context(), tenant, p.LocalUserID, p.JobID)
	credentialControlResult(w, j, err)
}
