package platform

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/tenancy"
	"golang.org/x/crypto/bcrypt"
)

// BootstrapAdmin belongs to a separate realm. No enterprise platform_admin
// role, JWT secret, or user ID is consulted by any platform operation.
func (s *Store) BootstrapAdmin(ctx context.Context, username, hash string) error {
	if username == "" && hash == "" {
		return nil
	}
	if !tenancy.ValidID(username) {
		return tenancy.ErrInvalid
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil || cost < 12 {
		return tenancy.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(490739175)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_admin_accounts(id,username,password_hash,role) SELECT 'bootstrap',$1,$2,'operator' WHERE NOT EXISTS(SELECT 1 FROM platform_admin_accounts)`, username, hash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *API) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /platform/admin/backup-schedules", a.admin(false, a.adminBackupSchedules))
	mux.HandleFunc("PUT /platform/admin/backup-schedules/{id}", a.admin(true, a.adminSetBackupSchedule))
	mux.HandleFunc("GET /platform/admin/maintenance", a.admin(false, a.adminMaintenance))
	mux.HandleFunc("GET /platform/admin/backups", a.admin(false, a.adminBackups))
	mux.HandleFunc("POST /platform/admin/backups", a.admin(true, a.adminRequestBackup))
	mux.HandleFunc("POST /platform/admin/backups/{id}/control", a.admin(true, a.adminControlBackup))
	mux.HandleFunc("GET /platform/admin/deployment-releases", a.admin(false, a.adminDeploymentReleases))
	mux.HandleFunc("GET /platform/admin/deployments", a.admin(false, a.adminDeployments))
	mux.HandleFunc("POST /platform/admin/deployments", a.admin(true, a.adminRequestDeployment))
	mux.HandleFunc("POST /platform/admin/deployments/{id}/retry", a.admin(true, a.adminRetryDeployment))
	mux.HandleFunc("POST /platform/admin/auth/login", a.adminLogin)
	mux.HandleFunc("GET /platform/admin/auth/me", a.admin(false, a.adminMe))
	mux.HandleFunc("POST /platform/admin/auth/logout", a.admin(false, a.adminLogout))
	mux.HandleFunc("GET /platform/admin/tenants", a.admin(false, a.adminTenants))
	mux.HandleFunc("POST /platform/admin/tenants", a.admin(true, a.adminCreateTenant))
	mux.HandleFunc("GET /platform/admin/tenants/{id}", a.admin(false, a.adminTenantDetail))
	mux.HandleFunc("PATCH /platform/admin/tenants/{id}", a.admin(true, a.adminUpdateTenant))
	mux.HandleFunc("POST /platform/admin/tenants/{id}/archive", a.admin(true, a.adminArchiveTenant))
	mux.HandleFunc("POST /platform/admin/tenants/{id}/unarchive", a.admin(true, a.adminUnarchiveTenant))
	mux.HandleFunc("POST /platform/admin/tenants/{id}/make-default", a.admin(true, a.adminMakeDefaultTenant))
	mux.HandleFunc("POST /platform/admin/tenants/{id}/activate", a.admin(true, a.adminActivateTenant))
	mux.HandleFunc("POST /platform/admin/tenants/{id}/codes", a.admin(true, a.adminCreateCode))
	mux.HandleFunc("POST /platform/admin/accounts/{id}/transfer", a.admin(true, a.adminTransfer))
	mux.HandleFunc("GET /platform/admin/jobs", a.admin(false, a.adminJobs))
	mux.HandleFunc("POST /platform/admin/jobs/{id}/retry", a.admin(true, a.adminRetryJob))
	mux.HandleFunc("PUT /platform/admin/jobs/{id}/registration-input", a.admin(true, a.adminCorrectRegistration))
	mux.HandleFunc("GET /platform/admin/accounts", a.admin(false, a.adminAccounts))
	mux.HandleFunc("POST /platform/admin/accounts/{id}/access", a.admin(true, a.adminSetAccess))
	mux.HandleFunc("GET /platform/admin/access-jobs", a.admin(false, a.adminAccessJobs))
	mux.HandleFunc("POST /platform/admin/tenants/{id}/access", a.admin(true, a.adminSetRealm))
	mux.HandleFunc("GET /platform/admin/realm-jobs", a.admin(false, a.adminRealmJobs))
	mux.HandleFunc("GET /platform/admin/codes", a.admin(false, a.adminCodes))
	mux.HandleFunc("PUT /platform/admin/codes/{id}/status", a.admin(true, a.adminCodeStatus))
	mux.HandleFunc("GET /platform/admin/audits", a.admin(false, a.adminAudits))
	mux.HandleFunc("GET /platform/admin/client-versions", a.admin(false, a.adminClientVersions))
	mux.HandleFunc("GET /platform/admin/client-versions/{platform}/history", a.admin(false, a.adminClientVersionHistory))
	mux.HandleFunc("PUT /platform/admin/client-versions/{platform}", a.admin(true, a.adminPublishClientVersion))
	mux.HandleFunc("GET /platform/admin/administrators", a.admin(false, a.adminAdministrators))
	mux.HandleFunc("POST /platform/admin/administrators/operations", a.admin(false, a.adminManageAdministrator))
	mux.HandleFunc("GET /platform/admin/administrators/operations/{requestId}", a.admin(false, a.adminOperationResult))
	mux.HandleFunc("GET /platform/admin/servers", a.admin(false, a.adminServers))
	mux.HandleFunc("GET /platform/admin/servers/configured", a.admin(false, a.adminServerChoices))
	mux.HandleFunc("POST /platform/admin/servers/operations", a.admin(true, a.adminManageServer))
	mux.HandleFunc("GET /platform/admin/servers/operations/{requestId}", a.admin(false, a.adminServerOperation))
}

type adminContextKey struct{}

const adminSessionCookie = "frogim_platform_admin"

func (a *API) adminCookieOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") {
		return false
	}
	if origin == a.WebOrigin && origin != "" {
		return true
	}
	for _, configured := range a.WebOrigins {
		if origin == configured {
			return true
		}
	}
	return false
}

func adminCookie(w http.ResponseWriter, token string, secure bool, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: adminSessionCookie, Value: token, Path: "/platform/admin",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge,
	})
}

func adminToken(r *http.Request) (string, bool) {
	if authorization := r.Header.Get("Authorization"); authorization != "" {
		if !strings.HasPrefix(authorization, "Bearer ") {
			return "", false
		}
		return strings.TrimPrefix(authorization, "Bearer "), false
	}
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil {
		return "", false
	}
	return cookie.Value, true
}

func (a *API) admin(write bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, fromCookie := adminToken(r)
		if len(token) != 43 || (fromCookie && r.Method != http.MethodGet && !a.adminCookieOriginAllowed(r)) {
			failure(w, ErrDenied)
			return
		}
		var id, role string
		err := a.Store.pool.QueryRow(r.Context(), `SELECT a.id,a.role FROM platform_admin_sessions s JOIN platform_admin_accounts a ON a.id=s.admin_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND a.enabled AND a.auth_version=s.auth_version`, tenancy.Hash(token)).Scan(&id, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrDenied
		}
		if err != nil {
			failure(w, err)
			return
		}
		if write && role != "operator" {
			failure(w, ErrDenied)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), adminContextKey{}, id)))
	}
}
func (a *API) adminLogin(w http.ResponseWriter, r *http.Request) {
	browserSession := r.Header.Get("X-Platform-Session") == "cookie"
	if browserSession && !a.adminCookieOriginAllowed(r) {
		failure(w, ErrDenied)
		return
	}
	var p struct{ Username, Password string }
	if readJSON(w, r, &p) != nil {
		failure(w, tenancy.ErrInvalid)
		return
	}
	if !a.allow(w, r, "admin:"+strings.ToLower(p.Username), 5, 10*time.Minute) {
		return
	}
	var id, hash string
	var version int64
	err := a.Store.pool.QueryRow(r.Context(), `SELECT id,password_hash,auth_version FROM platform_admin_accounts WHERE lower(username)=lower($1) AND enabled`, strings.TrimSpace(p.Username)).Scan(&id, &hash, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		failure(w, err)
		return
	}
	if hash == "" {
		hash = "$2a$12$zBHkYZDKBOCMhbEBxpfsFeqYHyCxLKKS.XpmGcAEv9HsjfkH09hQe"
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(p.Password)) != nil || id == "" {
		failure(w, ErrDenied)
		return
	}
	token, err := tenancy.Secret()
	if err != nil {
		failure(w, err)
		return
	}
	tag, err := a.Store.pool.Exec(r.Context(), `INSERT INTO platform_admin_sessions(token_hash,admin_id,auth_version,expires_at) SELECT $1,id,auth_version,now()+interval '8 hours' FROM platform_admin_accounts WHERE id=$2 AND enabled AND auth_version=$3`, tenancy.Hash(token), id, version)
	if err != nil {
		failure(w, err)
		return
	}
	if tag.RowsAffected() != 1 {
		failure(w, ErrDenied)
		return
	}
	if browserSession {
		adminCookie(w, token, strings.HasPrefix(r.Header.Get("Origin"), "https://"), int((8 * time.Hour).Seconds()))
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	respond(w, 200, map[string]string{"accessToken": token})
}
func adminReason(reason string, confirmed bool) bool {
	return confirmed && len(strings.TrimSpace(reason)) >= 2 && len(reason) <= 1000
}
func (a *API) adminCreateTenant(w http.ResponseWriter, r *http.Request) {
	var p struct {
		ID, DisplayName, HTTPBaseURL, Reason string
		IsDefault, Confirmed                 bool
	}
	if readJSON(w, r, &p) != nil || !adminReason(p.Reason, p.Confirmed) {
		failure(w, tenancy.ErrInvalid)
		return
	}
	actor, _ := r.Context().Value(adminContextKey{}).(string)
	if err := a.Store.PutTenant(r.Context(), p.ID, p.DisplayName, p.HTTPBaseURL, actor, p.Reason, p.IsDefault); err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, map[string]string{"id": p.ID, "status": "provisioning"})
}
func (a *API) adminTenants(w http.ResponseWriter, r *http.Request) {
	archive := r.URL.Query().Get("archive")
	if archive != "" && archive != "active" && archive != "archived" && archive != "all" {
		failure(w, tenancy.ErrInvalid)
		return
	}
	a.adminPage(w, r, `SELECT id AS ordering,jsonb_build_object('id',id,'displayName',display_name,
 'httpBaseUrl',http_base_url,'status',status,'isDefault',is_default,'configVersion',config_version,'accessVersion',access_version,
 'directoryVersion',directory_version,'archivedAt',archived_at) AS data
 FROM platform_tenants WHERE ($3='' OR status=$3) AND ($4='' OR strpos(display_name,$4)>0 OR id=$4)
 AND ($5='all' OR ($5='archived' AND archived_at IS NOT NULL) OR ($5<>'archived' AND archived_at IS NULL))`, r.URL.Query().Get("state"), strings.TrimSpace(r.URL.Query().Get("q")), archive)
}
func (a *API) adminCreateCode(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Reason    string
		Confirmed bool
	}
	if readJSON(w, r, &p) != nil || !adminReason(p.Reason, p.Confirmed) {
		failure(w, tenancy.ErrInvalid)
		return
	}
	code, err := tenancy.Secret()
	if err != nil {
		failure(w, err)
		return
	}
	code = strings.ToUpper(code)
	tx, err := a.Store.pool.Begin(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var codeID string
	err = tx.QueryRow(r.Context(), `INSERT INTO platform_enterprise_codes(code_hash,tenant_id,suffix) SELECT $1,id,$3 FROM platform_tenants WHERE id=$2 AND status='active' RETURNING id`, tenancy.Hash(code), r.PathValue("id"), code[len(code)-4:]).Scan(&codeID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrDenied
	}
	if err != nil {
		failure(w, err)
		return
	}
	actor, _ := r.Context().Value(adminContextKey{}).(string)
	if _, err = tx.Exec(r.Context(), `INSERT INTO platform_audits(actor_id,action,tenant_id,reason,metadata) VALUES($1,'tenant.code.created',$2,$3,jsonb_build_object('codeId',$4::text))`, actor, r.PathValue("id"), p.Reason, codeID); err != nil {
		failure(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, map[string]string{"id": codeID, "code": code})
}
func (a *API) adminTransfer(w http.ResponseWriter, r *http.Request) {
	var p struct {
		TargetTenantID, Reason string
		Confirmed              bool
	}
	if readJSON(w, r, &p) != nil || !adminReason(p.Reason, p.Confirmed) {
		failure(w, tenancy.ErrInvalid)
		return
	}
	actor, _ := r.Context().Value(adminContextKey{}).(string)
	job, err := a.Store.RequestTransfer(r.Context(), r.PathValue("id"), p.TargetTenantID, actor, p.Reason, p.Confirmed, a.Peers)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 202, map[string]string{"jobId": job, "status": "pending"})
}
func (a *API) adminJobs(w http.ResponseWriter, r *http.Request) {
	a.adminPage(w, r, `SELECT (created_at::text||id) AS ordering,jsonb_build_object('id',id,'kind',kind,
 'accountId',account_id,'sourceTenantId',source_tenant_id,'targetTenantId',target_tenant_id,
 'assignmentVersion',target_version,'step',step,'errorCode',error_code,'attempts',attempts,
 'blocked',blocked,'leased',COALESCE(lease_until>clock_timestamp(),false),'updatedAt',updated_at) AS data
 FROM platform_jobs WHERE ($3='' OR target_tenant_id=$3 OR source_tenant_id=$3)
 AND ($4='' OR ($4='blocked' AND blocked) OR ($4='pending' AND step<>'completed' AND NOT blocked)
 OR ($4='completed' AND step='completed'))`, r.URL.Query().Get("tenantId"), r.URL.Query().Get("state"))
}
