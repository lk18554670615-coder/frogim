package httpapi

import (
	"net/http"
	"strings"
)

type friendLoginIPPermissionInput struct {
	UserIDs   []string `json:"userIds"`
	Allowed   *bool    `json:"allowed"`
	Reason    string   `json:"reason"`
	Confirmed bool     `json:"confirmed"`
}

func (x *API) setFriendLoginIPPermission(w http.ResponseWriter, r *http.Request) {
	var input friendLoginIPPermissionInput
	if decode(r, &input) != nil || input.Allowed == nil || !confirmedReason(input.Confirmed, input.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "permission, confirmation and reason are required")
		return
	}
	result, err := x.app.SetFriendLoginIPPermission(r.Context(), uid(r), []string{strings.TrimSpace(r.PathValue("id"))}, *input.Allowed, input.Reason, x.clientIP(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, result)
}

func (x *API) setFriendLoginIPPermissions(w http.ResponseWriter, r *http.Request) {
	var input friendLoginIPPermissionInput
	if decode(r, &input) != nil || input.Allowed == nil || len(input.UserIDs) == 0 || len(input.UserIDs) > 100 || !confirmedReason(input.Confirmed, input.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "1-100 users, permission, confirmation and reason are required")
		return
	}
	result, err := x.app.SetFriendLoginIPPermission(r.Context(), uid(r), input.UserIDs, *input.Allowed, input.Reason, x.clientIP(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, result)
}
