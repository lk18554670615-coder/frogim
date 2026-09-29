package httpapi

import (
	"context"
	"errors"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/linli/im/server/internal/wukong"
	"net/http"
)

// Shared by credential and platform access tasks. Identity is already frozen;
// an external failure must never be mistaken for a completed disconnection.
func (x *API) disconnectTenantSessions(ctx context.Context, user string) error {
	if x.wukongClient == nil {
		return errors.New("IM revocation unavailable")
	}
	for flag := wukong.DeviceApp; flag <= wukong.DeviceDesktop; flag++ {
		token, err := tenancy.Secret()
		if err != nil {
			return err
		}
		if err = x.wukongClient.ProvisionUser(ctx, wukong.UserTokenRequest{UID: user, Token: token, DeviceFlag: flag, DeviceLevel: wukong.DeviceLevelMaster}); err != nil {
			return err
		}
	}
	if err := x.wukongClient.QuitDevice(ctx, user, -1); err != nil {
		return err
	}
	online, err := x.wukongClient.OnlineUsers(ctx, []string{user})
	if err != nil {
		return err
	}
	if online[user] {
		return errors.New("IM disconnect not confirmed")
	}
	return x.revokeTenantMedia(ctx, user)
}

func (x *API) checkTenantPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Identity  tenancy.Identity `json:"identity"`
		RuneCount int              `json:"runeCount"`
	}
	if decode(r, &body) != nil || body.Identity.Validate() != nil || body.Identity.TenantID != x.cfg.TenantID {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid identity")
		return
	}
	if err := x.tenantStore.CheckTenantPassword(r.Context(), body.Identity, body.RuneCount); err != nil {
		if !writeTenantRejection(w, err) {
			writeError(w, 409, "TENANT_PASSWORD_UNAVAILABLE", "password policy unavailable")
		}
		return
	}
	write(w, 200, map[string]any{"identity": body.Identity, "accepted": true})
}

func (x *API) revokeTenantCredentials(w http.ResponseWriter, r *http.Request) {
	var op tenancy.CredentialOperation
	if decode(r, &op) != nil || op.Identity.Validate() != nil || op.Identity.TenantID != x.cfg.TenantID || !tenancy.ValidID(op.OperationID) || op.AuthVersion < 2 {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid operation")
		return
	}
	if x.wukongClient == nil {
		writeError(w, 503, "IM_UNAVAILABLE", "IM revocation unavailable")
		return
	}
	err := x.tenantStore.WithTenantSessionFence(r.Context(), op.Identity.LocalUserID, func() error {
		done, err := x.tenantStore.BeginTenantCredentialRevocation(r.Context(), op)
		if err != nil || done {
			return err
		}
		if err := x.disconnectTenantSessions(r.Context(), op.Identity.LocalUserID); err != nil {
			return err
		}
		return x.tenantStore.FinishTenantCredentialRevocation(r.Context(), op)
	})
	if err != nil {
		writeError(w, 503, "TENANT_CREDENTIALS_UNCONFIRMED", "credential revocation is not confirmed")
		return
	}
	write(w, 200, tenancy.CredentialAck{CredentialOperation: op, State: "completed"})
}
