package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/app"
)

func writeInviteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInviteRequired):
		writeError(w, http.StatusBadRequest, "INVITE_CODE_REQUIRED", "创建新账号需要填写邀请码")
	case errors.Is(err, app.ErrInviteInvalid):
		writeError(w, http.StatusBadRequest, "INVITE_CODE_INVALID", "邀请码无效、已停用或邀请人账号不可用")
	case errors.Is(err, app.ErrInviteDisabled):
		writeError(w, http.StatusConflict, "INVITE_CODE_STATUS_DISABLED", "邀请码已被后台停用，不能自行修改")
	case errors.Is(err, app.ErrInviteChangeUsed):
		writeError(w, http.StatusConflict, "INVITE_CODE_CHANGE_USED", "邀请码修改次数已用完")
	case errors.Is(err, app.ErrConflict):
		writeError(w, http.StatusConflict, "INVITE_CODE_DUPLICATE", "该邀请码已被使用或永久保留")
	default:
		handleErr(w, err)
	}
}

func (x *API) validateInviteCode(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Code string `json:"code"`
	}
	if decode(r, &payload) != nil || strings.TrimSpace(payload.Code) == "" {
		write(w, http.StatusOK, map[string]bool{"valid": false})
		return
	}
	if !x.allow(r.Context(), "invite-validate-ip:"+x.clientIP(r), 60, 10*time.Minute) {
		w.Header().Set("Retry-After", "600")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "邀请码校验过于频繁")
		return
	}
	valid, err := x.app.ValidateInviteCode(r.Context(), payload.Code)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]bool{"valid": valid})
}

func inviteCodePayload(item any) any {
	// Kept as a helper boundary for response compatibility; concrete values are
	// already JSON-safe store records.
	return item
}

func (x *API) myInviteCode(w http.ResponseWriter, r *http.Request) {
	if x.app.InviteRegistrationMode() == "disabled" {
		writeError(w, http.StatusForbidden, "INVITE_CODE_DISABLED", "邀请码功能当前未启用")
		return
	}
	item, err := x.app.UserInviteCode(r.Context(), uid(r))
	if err != nil {
		writeInviteError(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"id": item.ID, "code": item.Code, "status": item.Status, "selfChangesUsed": item.SelfChangesUsed, "selfChangesRemaining": item.SelfChangesRemaining, "createdAt": item.CreatedAt, "qrPayload": "qingwaguagua://register?invite=" + item.Code})
}

func (x *API) changeMyInviteCode(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Code      string `json:"code"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &payload) != nil || !payload.Confirmed {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed is required")
		return
	}
	item, err := x.app.ChangeUserInviteCode(r.Context(), uid(r), payload.Code)
	if err != nil {
		writeInviteError(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"id": item.ID, "code": item.Code, "status": item.Status, "selfChangesUsed": item.SelfChangesUsed, "selfChangesRemaining": item.SelfChangesRemaining, "createdAt": item.CreatedAt, "qrPayload": "qingwaguagua://register?invite=" + item.Code})
}

func inviteLimit(r *http.Request) int {
	value, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if value < 1 {
		value = 20
	}
	if value > 100 {
		value = 100
	}
	return value
}

func (x *API) adminInviteCodes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := x.app.AdminInviteCodes(r.Context(), q.Get("q"), q.Get("status"), q.Get("cursor"), inviteLimit(r))
	if err != nil {
		writeInviteError(w, err)
		return
	}
	for index := range page.Items {
		x.signAvatarURL(page.Items[index].User)
	}
	write(w, http.StatusOK, inviteCodePayload(page))
}
func (x *API) adminInviteRelations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := x.app.AdminInviteRelations(r.Context(), q.Get("q"), q.Get("method"), q.Get("from"), q.Get("to"), q.Get("cursor"), inviteLimit(r))
	if err != nil {
		writeInviteError(w, err)
		return
	}
	for index := range page.Items {
		x.signAvatarURL(page.Items[index].Inviter)
		x.signAvatarURL(page.Items[index].Invitee)
	}
	write(w, http.StatusOK, page)
}
func (x *API) adminInviteCodeStatus(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Status, Reason string
		Confirmed      bool
	}
	if decode(r, &payload) != nil || !confirmedReason(payload.Confirmed, payload.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	item, err := x.app.AdminSetInviteCodeStatus(r.Context(), uid(r), r.PathValue("id"), payload.Status, payload.Reason)
	if err != nil {
		writeInviteError(w, err)
		return
	}
	write(w, http.StatusOK, item)
}
func (x *API) adminResetInviteCode(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Reason    string
		Confirmed bool
	}
	if decode(r, &payload) != nil || !confirmedReason(payload.Confirmed, payload.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	item, err := x.app.AdminResetInviteCode(r.Context(), uid(r), r.PathValue("id"), payload.Reason)
	if err != nil {
		writeInviteError(w, err)
		return
	}
	write(w, http.StatusOK, item)
}
