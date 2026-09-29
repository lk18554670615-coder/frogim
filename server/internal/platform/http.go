package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/netutil"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/redis/go-redis/v9"
)

type Limiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}
type RedisLimiter struct{ Client *redis.Client }

var limitScript = redis.NewScript(`local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]) end; return n`)

func (l RedisLimiter) Allow(ctx context.Context, key string, max int, window time.Duration) (bool, error) {
	n, err := limitScript.Run(ctx, l.Client, []string{"platform:limit:" + key}, window.Milliseconds()).Int()
	return n <= max, err
}

type OTP interface {
	Request(context.Context, string) error
	Verify(context.Context, string, string) error
}
type API struct {
	Store             *Store
	Limiter           Limiter
	OTP               OTP
	RecoverySMS       RecoverySMS
	WebOrigin         string
	WebOrigins        []string
	Peers             EnterpriseRPC
	Agents            AgentRPC
	DeploymentCatalog map[string]deployment.Release
	Push              *PushService
	GatewaySecret     string
}

func (a *API) PublicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.Ping(r.Context()); err != nil {
			failure(w, ErrUnavailable)
			return
		}
		respond(w, 200, map[string]any{"status": "ok", "service": "platform-auth"})
	})
	mux.HandleFunc("GET /v2/config/auth", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"passwordLoginEnabled": true, "passwordChangeEnabled": true, "passwordResetEnabled": a.RecoverySMS != nil, "otpLoginEnabled": a.OTP != nil, "registrationEnabled": a.OTP != nil, "tenantAuthentication": true})
	})
	mux.HandleFunc("POST /v2/auth/password-login", a.passwordLogin)
	mux.HandleFunc("GET /v2/config/version", a.clientVersion)
	mux.HandleFunc("GET /v2/config/push", a.pushConfig)
	mux.HandleFunc("POST /v2/push/devices", a.bindPushDevice)
	mux.HandleFunc("POST /v2/push/devices/unbind", a.unbindPushDevice)
	mux.HandleFunc("POST /v2/auth/login", a.otpLogin)
	mux.HandleFunc("POST /v2/auth/enterprise-codes/validate", a.validateEnterpriseCode)
	mux.HandleFunc("POST /v2/auth/refresh", a.refresh)
	mux.HandleFunc("POST /v2/auth/logout", a.logout)
	mux.HandleFunc("POST /v2/auth/password-change", a.changePassword)
	mux.HandleFunc("POST /v2/auth/password-change/status", a.passwordChangeStatus)
	mux.HandleFunc("POST /v2/auth/password-reset/code", a.recoveryCode)
	mux.HandleFunc("POST /v2/auth/password-reset", a.recoverPassword)
	mux.HandleFunc("POST /v2/auth/password-reset/status", a.recoveryStatus)
	mux.HandleFunc("GET /v2/auth/credential-jobs/{id}", a.credentialStatus)
	mux.HandleFunc("POST /v2/auth/register", a.register)
	mux.HandleFunc("POST /v2/auth/code", a.requestCode)
	mux.HandleFunc("GET /v2/auth/registration-jobs/{id}", a.poll)
	mux.HandleFunc("POST /v2/auth/registration-jobs/{id}/complete", a.completeRegistration)
	a.adminRoutes(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := origin == a.WebOrigin
			for _, configured := range a.WebOrigins {
				allowed = allowed || origin == configured
			}
			if !allowed {
				failure(w, ErrDenied)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Client-Platform")
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodGet && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			failure(w, tenancy.ErrInvalid)
			return
		}
		if r.URL.Path != "/health" {
			ip := netutil.GatewayClientIP(r, a.GatewaySecret)
			if !a.allow(w, r, "ip:"+ip, 120, time.Minute) {
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *API) otpLogin(w http.ResponseWriter, r *http.Request) {
	if a.OTP == nil {
		failure(w, ErrUnavailable)
		return
	}
	var p struct{ Phone, Code, Name, EnterpriseCode, InviteCode string }
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	phone, err := NormalizePhone(p.Phone)
	if err != nil || p.Code == "" {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if !a.allow(w, r, "login:"+phone, 8, 10*time.Minute) {
		return
	}
	if err = a.OTP.Verify(r.Context(), phone, p.Code); err != nil {
		failure(w, err)
		return
	}
	result, found, err := a.Store.LoginVerified(r.Context(), phone)
	if err != nil {
		failure(w, err)
		return
	}
	if found {
		respond(w, 200, result)
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = "新用户"
	}
	reservation, err := a.Store.Reserve(r.Context(), Registration{Phone: phone, Name: p.Name, EnterpriseCode: p.EnterpriseCode, PersonalInviteCode: p.InviteCode, Method: "otp"})
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 202, reservation)
}
func (a *API) validateEnterpriseCode(w http.ResponseWriter, r *http.Request) {
	var p struct{ Code string }
	if readJSON(w, r, &p) != nil || len(p.Code) > 80 {
		failure(w, tenancy.ErrInvalid)
		return
	}
	var valid bool
	err := a.Store.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM platform_enterprise_codes c JOIN platform_tenants t ON t.id=c.tenant_id WHERE c.code_hash=$1 AND c.enabled AND t.status='active')`, tenancy.Hash(strings.ToUpper(strings.TrimSpace(p.Code)))).Scan(&valid)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]bool{"valid": valid})
}

// This handler is only installed on the dedicated mutual-TLS listener. Public
// routes never forward /internal, and no HTTP header can impersonate a tenant.
func (a *API) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/tenancy/client-version", a.tenantClientVersion)
	mux.HandleFunc("POST /internal/tenancy/push/deliver", a.deliverPush)
	mux.HandleFunc("POST /internal/tenancy/admin/accounts", a.tenantAdminCreate)
	mux.HandleFunc("POST /internal/tenancy/admin/account-jobs", a.tenantAdminJobs)
	mux.HandleFunc("POST /internal/tenancy/admin/password-reset", a.tenantAdminPasswordReset)
	mux.HandleFunc("POST /internal/tenancy/admin/credential-job", a.tenantAdminCredentialStatus)
	mux.HandleFunc("POST /internal/tenancy/tickets/consume", func(w http.ResponseWriter, r *http.Request) {
		identity := tenancy.PeerIdentity(r)
		tenant := strings.TrimPrefix(identity, "spiffe://frogim/tenant/")
		if tenant == identity || !tenancy.ValidID(tenant) {
			failure(w, ErrDenied)
			return
		}
		var p struct {
			Ticket string `json:"ticket"`
		}
		if readJSON(w, r, &p) != nil || len(p.Ticket) != 43 {
			failure(w, tenancy.ErrInvalid)
			return
		}
		grant, err := a.Store.Consume(r.Context(), tenant, p.Ticket)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, grant)
	})
	return mux
}

func (a *API) allow(w http.ResponseWriter, r *http.Request, key string, max int, window time.Duration) bool {
	if a.Limiter == nil {
		failure(w, ErrUnavailable)
		return false
	}
	ok, err := a.Limiter.Allow(r.Context(), key, max, window)
	if err != nil {
		failure(w, ErrUnavailable)
		return false
	}
	if !ok {
		w.Header().Set("Retry-After", "60")
		respond(w, 429, map[string]any{"error": map[string]string{"code": "RATE_LIMITED", "message": "请稍后重试"}})
		return false
	}
	return true
}
func (a *API) passwordLogin(w http.ResponseWriter, r *http.Request) {
	var p struct{ Phone, Password string }
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	phone, err := NormalizePhone(p.Phone)
	if err != nil {
		failure(w, ErrDenied)
		return
	}
	if !a.allow(w, r, "login:"+phone, 8, 10*time.Minute) {
		return
	}
	result, err := a.Store.Login(r.Context(), phone, p.Password)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	var p struct {
		RefreshToken string `json:"refreshToken"`
	}
	if readJSON(w, r, &p) != nil || len(p.RefreshToken) != 43 {
		failure(w, tenancy.ErrInvalid)
		return
	}
	result, err := a.Store.Refresh(r.Context(), p.RefreshToken)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	var p struct {
		RefreshToken string `json:"refreshToken"`
	}
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if err := a.Store.Logout(r.Context(), p.RefreshToken); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *API) requestCode(w http.ResponseWriter, r *http.Request) {
	if a.OTP == nil {
		failure(w, ErrUnavailable)
		return
	}
	var p struct{ Phone string }
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	phone, err := NormalizePhone(p.Phone)
	if err != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if !a.allow(w, r, "otp:"+phone, 3, 10*time.Minute) {
		return
	}
	if err = a.OTP.Request(r.Context(), phone); err != nil {
		failure(w, ErrUnavailable)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
func (a *API) register(w http.ResponseWriter, r *http.Request) {
	if a.OTP == nil {
		failure(w, ErrUnavailable)
		return
	}
	var p struct{ Phone, Code, Password, Name, EnterpriseCode, InviteCode string }
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	phone, err := NormalizePhone(p.Phone)
	if err != nil || p.Code == "" {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if !a.allow(w, r, "register:"+phone, 5, 10*time.Minute) {
		return
	}
	if err = a.OTP.Verify(r.Context(), phone, p.Code); err != nil {
		failure(w, ErrDenied)
		return
	}
	reservation, err := a.Store.Reserve(r.Context(), Registration{Phone: phone, Password: p.Password, Name: p.Name, EnterpriseCode: p.EnterpriseCode, PersonalInviteCode: p.InviteCode, Method: "password"})
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, http.StatusAccepted, reservation)
}
func (a *API) poll(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(token) != 43 {
		failure(w, ErrDenied)
		return
	}
	step, code, err := a.Store.Poll(r.Context(), r.PathValue("id"), token)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"status": step, "errorCode": code})
}

func (a *API) completeRegistration(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if readJSON(w, r, &body) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(token) != 43 || !tenancy.ValidID(r.PathValue("id")) {
		failure(w, ErrDenied)
		return
	}
	result, err := a.Store.CompleteRegistration(r.Context(), r.PathValue("id"), token)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, result)
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return tenancy.ErrInvalid
	}
	return nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrAccessPending) {
		respond(w, 409, map[string]any{"error": map[string]string{"code": "ACCOUNT_OPERATION_PENDING", "message": "account operation is still pending"}})
		return
	}
	status, code, message := 503, "PLATFORM_UNAVAILABLE", "认证服务暂不可用"
	var rejected *tenancy.OperationRejected
	switch {
	case errors.Is(err, ErrMaintenanceChanged):
		status, code, message = 409, "MAINTENANCE_STATE_CHANGED", "维护计划或任务状态已变化，请查询原记录并重新确认"
	case errors.Is(err, ErrBackupChanged):
		status, code, message = 409, "BACKUP_STATE_CHANGED", "备份状态、发布版本或服务器绑定已变化，请查询原任务后重新确认"
	case errors.Is(err, ErrDeploymentMaintenance):
		status, code, message = 409, "DEPLOYMENT_MAINTENANCE_REQUIRED", "企业须处于允许的维护状态，且没有冲突任务；部署或备份未确认前不能恢复访问，备份不适用于首次开通状态"
	case errors.Is(err, ErrDeploymentChanged):
		status, code, message = 409, "DEPLOYMENT_STATE_CHANGED", "部署状态、发布目录或服务器绑定已变化，请先查询原任务后刷新确认"
	case errors.Is(err, ErrServerChanged):
		status, code, message = 409, "SERVER_BINDING_CHANGED", "服务器绑定或企业配置已变化，请刷新后确认；不能重复绑定主机"
	case errors.Is(err, ErrAgentUnavailable):
		status, code, message = 503, "HOST_AGENT_UNAVAILABLE", "服务器代理暂不可用或身份校验未通过，未保存本次操作"
	case errors.Is(err, ErrAdminPassword):
		status, code, message = 400, "ADMIN_CURRENT_PASSWORD_INVALID", "当前密码不正确，请关闭窗口后重新填写"
	case errors.Is(err, ErrAdminSelf):
		status, code, message = 409, "ADMIN_SELF_ACCESS_CHANGE", "不能停用或调整本人的管理角色，请由另一名运营管理员操作"
	case errors.Is(err, ErrLastOperator):
		status, code, message = 409, "LAST_PLATFORM_OPERATOR", "必须保留至少一名启用的运营管理员"
	case errors.Is(err, ErrAdminChanged):
		status, code, message = 409, "ADMIN_ACCOUNT_CHANGED", "管理员状态已变化，请重新加载后确认"
	case errors.Is(err, ErrReleaseChanged):
		status, code, message = 409, "CLIENT_VERSION_POLICY_CHANGED", "版本策略已由其他管理员修改，请重新加载后确认"
	case errors.As(err, &rejected) && rejected.Code == "TENANT_PASSWORD_POLICY_REJECTED":
		status, code, message = 400, rejected.Code, "密码不符合所属企业的密码长度要求"
	case errors.Is(err, ErrRequestChanged):
		status, code, message = 409, "REQUEST_CHANGED", "该请求号已用于其他内容，请先查询原任务"
	case errors.As(err, &rejected) && rejected.Code == "GROUP_OWNERSHIP_TRANSFER_REQUIRED":
		status, code, message = 409, rejected.Code, "请先转让该用户在源企业拥有的群，再调换企业"
	case errors.Is(err, ErrDenied):
		status, code, message = 401, "INVALID_CREDENTIALS", "账号或凭据不可用"
	case errors.Is(err, ErrConflict):
		status, code, message = 409, "ACCOUNT_UNAVAILABLE", "此账号无法开户"
	case errors.Is(err, tenancy.ErrInvalid):
		status, code, message = 400, "INVALID_ARGUMENT", "请求参数不正确"
	}
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
