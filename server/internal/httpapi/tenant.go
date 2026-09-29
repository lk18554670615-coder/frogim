package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/auth"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
)

func (x *API) ConfigureTenant(s *store.Postgres, peer *tenancy.RPC) {
	x.tenantStore = s
	x.platformControl = peer
	x.configureTenantLiveKit()
}

type tenantFenceContextKey struct{}
type tenantAuthVersionContextKey struct{}
type tenantRealmVersionContextKey struct{}

func (x *API) tenantTokenManager(ctx context.Context, uid string) (auth.Manager, error) {
	m := x.auth
	if x.cfg.TenantID == "" {
		return m, nil
	}
	if x.tenantStore == nil {
		return m, app.ErrUnavailable
	}
	i, version, err := x.tenantStore.TenantAuthIdentity(ctx, x.cfg.TenantID, uid)
	if err != nil {
		return m, app.ErrForbidden
	}
	m.Identity = i
	m.TenantAuthVersion = version
	m.TenantRealmVersion, err = x.tenantStore.TenantRealmVersion(ctx, x.cfg.TenantID)
	if err != nil {
		return m, err
	}
	if expected, ok := ctx.Value(tenantRealmVersionContextKey{}).(int64); !ok || expected != m.TenantRealmVersion {
		return m, app.ErrForbidden
	}
	return m, nil
}

func (x *API) allowTenantRoute(w http.ResponseWriter, r *http.Request) bool {
	if x.cfg.TenantID == "" {
		return true
	}
	path := r.URL.Path
	if r.Method == http.MethodPost && path == "/v2/users/me/devices" {
		writeError(w, 409, "PLATFORM_PUSH_REGISTRATION_REQUIRED", "推送设备需要通过统一平台登记，企业服务不接收设备令牌")
		return false
	}
	if path == "/v2/admin/client-versions" || strings.HasPrefix(path, "/v2/admin/client-versions/") {
		writeError(w, 409, "PLATFORM_VERSION_MANAGEMENT_REQUIRED", "客户端版本由独立平台统一管理，请前往平台运营后台；企业后台不能发布更新")
		return false
	}
	if strings.HasPrefix(path, "/v2/auth/") && path != "/v2/auth/tenant-session" && path != "/v2/auth/im-session" && path != "/v2/auth/logout" {
		writeError(w, 409, "PLATFORM_AUTH_REQUIRED", "请通过统一认证入口登录或续期")
		return false
	}
	// Ordinary user lifecycle still requires platform orchestration.
	if (r.Method == http.MethodDelete && path == "/v2/users/me") || strings.HasPrefix(path, "/v2/users/me/phone") || strings.HasPrefix(path, "/v2/users/me/deletion") {
		writeError(w, 409, "PLATFORM_ACCOUNT_MANAGEMENT_REQUIRED", "此操作需要平台账号管理")
		return false
	}
	return true
}

func (x *API) tenantSession(w http.ResponseWriter, r *http.Request) {
	if x.cfg.TenantID == "" || x.tenantStore == nil || x.platformControl == nil {
		writeError(w, 503, "TENANT_AUTH_UNAVAILABLE", "企业认证未就绪")
		return
	}
	var body struct {
		SessionTicket string `json:"sessionTicket"`
	}
	if decode(r, &body) != nil || len(body.SessionTicket) != 43 {
		writeError(w, 400, "INVALID_ARGUMENT", "需要有效登录票据")
		return
	}
	var grant tenancy.Grant
	if err := x.platformControl.Call(r.Context(), "/internal/tenancy/tickets/consume", map[string]string{"ticket": body.SessionTicket}, &grant); err != nil {
		writeError(w, 401, "TENANT_TICKET_REJECTED", "登录票据无效或已使用，请重新登录")
		return
	}
	if grant.Validate() != nil || grant.AuthVersion < 1 || grant.RealmVersion < 1 || grant.TenantID != x.cfg.TenantID || !grant.ExpiresAt.After(time.Now()) {
		writeError(w, 401, "TENANT_TICKET_REJECTED", "登录票据不属于本企业")
		return
	}
	err := x.tenantStore.WithTenantSessionFence(r.Context(), grant.LocalUserID, func() error {
		if err := x.tenantStore.ActivateTenantGrant(r.Context(), grant); err != nil {
			return err
		}
		u, err := x.app.UserContext(r.Context(), grant.LocalUserID)
		if err != nil {
			return err
		}
		ctx := context.WithValue(r.Context(), tenantAuthVersionContextKey{}, grant.AuthVersion)
		ctx = context.WithValue(ctx, tenantRealmVersionContextKey{}, grant.RealmVersion)
		x.issueUserSession(w, r.WithContext(context.WithValue(ctx, tenantFenceContextKey{}, true)), u)
		return nil
	})
	if err != nil {
		writeError(w, 403, "TENANT_IDENTITY_INACTIVE", "企业身份已失效")
	}
}

func (x *API) TenantControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/tenancy/readiness", x.tenantReadiness)
	mux.HandleFunc("POST /internal/tenancy/identities/prepare", x.prepareTenantIdentity)
	mux.HandleFunc("POST /internal/tenancy/identities/revoke", x.revokeTenantIdentity)
	mux.HandleFunc("POST /internal/tenancy/identities/check-transfer", x.checkTenantTransfer)
	mux.HandleFunc("POST /internal/tenancy/credentials/check-password", x.checkTenantPassword)
	mux.HandleFunc("POST /internal/tenancy/credentials/revoke", x.revokeTenantCredentials)
	mux.HandleFunc("POST /internal/tenancy/access", x.setTenantAccess)
	mux.HandleFunc("POST /internal/tenancy/realm", x.setTenantRealm)
	mux.HandleFunc("POST /internal/tenancy/recovery/inventory", x.tenantRecoveryInventory)
	mux.HandleFunc("POST /internal/tenancy/recovery/authority", x.tenantRecoveryAuthority)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if x.cfg.TenantID == "" || x.tenantStore == nil || tenancy.PeerIdentity(r) != tenancy.PlatformIdentity {
			writeError(w, 403, "FORBIDDEN", "invalid control identity")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (x *API) tenantReadiness(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Nonce string `json:"nonce"`
	}
	if decode(r, &body) != nil || len(body.Nonce) != 43 {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid readiness challenge")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	version, err := x.tenantStore.TenantReadiness(ctx, x.cfg.TenantID)
	checks := map[string]bool{"databaseBinding": err == nil && version >= 73}
	checks["databaseAndCache"] = x.app.Ready(ctx) == nil
	checks["im"] = x.imSessions != nil && x.imSessions.Ready(ctx) == nil
	if media, ok := x.media.(interface{ Ready(context.Context) error }); ok {
		checks["media"] = media.Ready(ctx) == nil
	}
	if calls, ok := x.livekit.(livekitAdminControl); ok {
		_, err := calls.ListRooms(ctx)
		checks["calls"] = err == nil
	}
	realm, realmErr := x.tenantStore.TenantRealmSnapshot(ctx, x.cfg.TenantID)
	if realmErr != nil {
		checks["databaseBinding"] = false
	}
	write(w, 200, tenancy.Readiness{Nonce: body.Nonce, TenantID: x.cfg.TenantID, HTTPBaseURL: x.cfg.TenantPublicURL, SchemaVersion: version, Checks: checks, Realm: realm})
}

func (x *API) checkTenantTransfer(w http.ResponseWriter, r *http.Request) {
	op, ok := x.readIdentityOperation(w, r)
	if !ok {
		return
	}
	if err := x.tenantStore.CheckTenantTransfer(r.Context(), op.Identity); err != nil {
		if !writeTenantRejection(w, err) {
			writeError(w, 409, "TENANT_REVOKE_UNCONFIRMED", "source identity could not be verified")
		}
		return
	}
	write(w, 200, platform.IdentityAck{OperationID: op.OperationID, Identity: op.Identity, State: "eligible"})
}

func (x *API) readIdentityOperation(w http.ResponseWriter, r *http.Request) (platform.IdentityOperation, bool) {
	var op platform.IdentityOperation
	if decode(r, &op) != nil || op.Identity.Validate() != nil || op.Identity.TenantID != x.cfg.TenantID || !tenancy.ValidID(op.OperationID) {
		writeError(w, 400, "INVALID_ARGUMENT", "invalid enterprise operation")
		return op, false
	}
	return op, true
}
func (x *API) prepareTenantIdentity(w http.ResponseWriter, r *http.Request) {
	op, ok := x.readIdentityOperation(w, r)
	if !ok {
		return
	}
	err := x.tenantStore.PrepareTenantIdentity(r.Context(), store.TenantProvision{OperationID: op.OperationID, Identity: op.Identity, Phone: op.Input.Phone, Name: op.Input.Name, Gender: op.Input.Gender, Method: op.Input.Method, PersonalInviteCode: op.Input.PersonalInviteCode, PasswordRuneCount: op.Input.PasswordRuneCount})
	if err != nil {
		if writeTenantRejection(w, err) {
			return
		}
		writeError(w, 409, "TENANT_PROVISION_REJECTED", "enterprise identity could not be prepared")
		return
	}
	write(w, 200, platform.IdentityAck{OperationID: op.OperationID, Identity: op.Identity, State: "prepared"})
}
func (x *API) revokeTenantIdentity(w http.ResponseWriter, r *http.Request) {
	op, ok := x.readIdentityOperation(w, r)
	if !ok {
		return
	}
	if x.wukongClient == nil {
		writeError(w, 503, "IM_UNAVAILABLE", "IM revocation unavailable")
		return
	}
	err := x.tenantStore.WithTenantSessionFence(r.Context(), op.Identity.LocalUserID, func() error {
		if err := x.tenantStore.BeginTenantRevocation(r.Context(), op.OperationID, op.Identity); err != nil {
			return err
		}
		if err := x.disconnectTenantSessions(r.Context(), op.Identity.LocalUserID); err != nil {
			return err
		}
		return x.tenantStore.FinishTenantRevocation(r.Context(), op.OperationID, op.Identity)
	})
	if err != nil {
		if writeTenantRejection(w, err) {
			return
		}
		writeError(w, 409, "TENANT_REVOKE_UNCONFIRMED", "source enterprise revocation has not completed")
		return
	}
	write(w, 200, platform.IdentityAck{OperationID: op.OperationID, Identity: op.Identity, State: "revoked"})
}

func writeTenantRejection(w http.ResponseWriter, err error) bool {
	var rejection *tenancy.OperationRejected
	code := ""
	if errors.As(err, &rejection) && tenancy.RepairableCode(rejection.Code) {
		code = rejection.Code
	}
	switch {
	case errors.Is(err, store.ErrInviteRequired):
		code = "INVITE_REQUIRED"
	case errors.Is(err, store.ErrInviteInvalid):
		code = "INVITE_INVALID"
	case errors.Is(err, store.ErrInviteDisabled):
		code = "INVITE_DISABLED"
	}
	if code == "" {
		return false
	}
	writeError(w, http.StatusConflict, code, "enterprise operation requires correction")
	return true
}
