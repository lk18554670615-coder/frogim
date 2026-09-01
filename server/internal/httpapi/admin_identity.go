package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type optionalAdminEmail struct {
	Set   bool
	Value string
}

func (value *optionalAdminEmail) UnmarshalJSON(raw []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		value.Value = ""
		return nil
	}
	return json.Unmarshal(raw, &value.Value)
}

func (x *API) adminMe(w http.ResponseWriter, r *http.Request) {
	account, err := x.app.AdminAccount(r.Context(), uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, account)
}

func (x *API) changeAdminPassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if decode(r, &request) != nil {
		x.app.RecordAdminAudit(uid(r), "admin.password.change", "administrator", uid(r), "failed", x.clientIP(r), map[string]any{"reason": "invalid request"})
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "currentPassword and newPassword are required")
		return
	}
	if err := x.app.ChangeAdminPassword(r.Context(), uid(r), request.CurrentPassword, request.NewPassword); err != nil {
		x.app.RecordAdminAudit(uid(r), "admin.password.change", "administrator", uid(r), "failed", x.clientIP(r), map[string]any{"reason": "password verification or validation failed"})
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "admin.password.change", "administrator", uid(r), "success", x.clientIP(r), map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) adminAdministrators(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, total, next, err := x.app.AdminAccounts(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("status"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": total, "nextCursor": next})
}

func (x *API) createAdministrator(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username, DisplayName, RoleID, Password, Reason string
		Email                                           optionalAdminEmail
		Confirmed                                       bool
	}
	if decode(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid administrator request")
		return
	}
	var email *string
	if request.Email.Set {
		email = &request.Email.Value
	}
	item, err := x.app.CreateAdminAccount(r.Context(), uid(r), request.Username, email, request.DisplayName, request.RoleID, request.Password)
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "administrator.create", "administrator", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason), "roleId": item.RoleID, "username": item.Username})
	write(w, http.StatusCreated, item)
}

func (x *API) updateAdministrator(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username, DisplayName, RoleID, Status *string
		Email                                 optionalAdminEmail
		Reason                                string
		Confirmed                             bool
	}
	if decode(r, &request) != nil || (request.Username == nil && !request.Email.Set && request.DisplayName == nil && request.RoleID == nil && request.Status == nil) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "at least one administrator field is required")
		return
	}
	var email *string
	if request.Email.Set {
		email = &request.Email.Value
	}
	item, err := x.app.UpdateAdminAccount(r.Context(), uid(r), r.PathValue("id"), request.Username, email, request.DisplayName, request.RoleID, request.Status)
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "administrator.update", "administrator", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason), "roleId": item.RoleID, "status": item.Status, "username": item.Username})
	write(w, http.StatusOK, item)
}

func (x *API) resetAdministratorPassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password  string
		Reason    string
		Confirmed bool
	}
	if decode(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "password is required")
		return
	}
	if err := x.app.ResetAdminPassword(r.Context(), r.PathValue("id"), request.Password); err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "administrator.password.reset", "administrator", r.PathValue("id"), "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) adminRoles(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.AdminRoles(r.Context())
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}

func (x *API) createAdminRole(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name, Description, Reason string
		Permissions               []string
		Confirmed                 bool
	}
	if decode(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid role request")
		return
	}
	item, err := x.app.CreateAdminRole(r.Context(), uid(r), request.Name, request.Description, request.Permissions)
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "administrator.role.create", "administrator_role", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason), "permissions": item.Permissions})
	write(w, http.StatusCreated, item)
}

func (x *API) updateAdminRole(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name, Description, Reason string
		Permissions               []string
		Confirmed                 bool
	}
	if decode(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid role request")
		return
	}
	item, err := x.app.UpdateAdminRole(r.Context(), uid(r), r.PathValue("id"), request.Name, request.Description, request.Permissions)
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "administrator.role.update", "administrator_role", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason), "permissions": item.Permissions})
	write(w, http.StatusOK, item)
}

func (x *API) deleteAdminRole(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Reason    string
		Confirmed bool
	}
	if decode(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid role request")
		return
	}
	if err := x.app.DeleteAdminRole(r.Context(), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "administrator.role.delete", "administrator_role", r.PathValue("id"), "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	w.WriteHeader(http.StatusNoContent)
}
