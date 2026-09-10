package httpapi

import "net/http"

func (x *API) setInternalUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IsInternalUser *bool  `json:"isInternalUser"`
		Reason         string `json:"reason"`
		Confirmed      bool   `json:"confirmed"`
	}
	if decode(r, &input) != nil || input.IsInternalUser == nil || !confirmedReason(input.Confirmed, input.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "internal-user status, confirmation and reason are required")
		return
	}
	result, err := x.app.SetInternalUser(r.Context(), uid(r), r.PathValue("id"), *input.IsInternalUser, input.Reason, x.clientIP(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, result)
}
