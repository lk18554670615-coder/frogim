package tenancy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/config"
	"golang.org/x/crypto/bcrypt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

//go:embed schema.sql
var schema string

type Platform struct {
	DB     *pgxpool.Pool
	cfg    config.Config
	o      Options
	client *http.Client
}

func NewPlatform(ctx context.Context, c config.Config, o Options) (*Platform, error) {
	if e := o.Validate(); e != nil {
		return nil, e
	}
	if len(c.JWTSecret) < 32 || c.DatabaseURL == "" || c.AdminUsername == "" || c.AdminPasswordHash == "" {
		return nil, errors.New("platform requires database, signing secret and administrator")
	}
	if !c.DevMode && (!strings.HasPrefix(c.OTPWebhookURL, "https://") || len(c.OTPWebhookToken) < 24) {
		return nil, errors.New("production platform requires HTTPS SMS provider")
	}
	db, e := openPool(ctx, c.DatabaseURL)
	if e != nil {
		return nil, e
	}
	if _, e = db.Exec(ctx, schema); e != nil {
		db.Close()
		return nil, e
	}
	h, e := ControlClient(o)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Platform{db, c, o, h}, nil
}
func (p *Platform) Close() { p.DB.Close() }
func (p *Platform) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		if p.DB.Ping(r.Context()) != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		jsonResponse(w, 200, map[string]string{"status": "ready", "mode": "platform"})
	})
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("POST /v2/auth/register", p.register)
	m.HandleFunc("POST /v2/auth/password-login", p.login)
	m.HandleFunc("POST /v2/auth/login", p.login)
	m.HandleFunc("POST /v2/auth/code", p.code)
	m.HandleFunc("POST /v2/auth/password/reset-code", p.code)
	m.HandleFunc("POST /v2/auth/password/reset", p.resetPassword)
	m.HandleFunc("POST /v2/auth/refresh", p.refresh)
	m.HandleFunc("POST /v2/auth/logout", p.logout)
	m.HandleFunc("POST /v2/users/me/phone/code", p.phoneCode)
	m.HandleFunc("PATCH /v2/users/me/phone", p.changePhone)
	m.HandleFunc("GET /v2/auth/enterprise", p.route)
	m.HandleFunc("POST /v2/auth/invite-codes/validate", p.validateCode)
	m.HandleFunc("GET /v2/config/auth", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"allowRegistration": true, "registrationEnabled": true, "passwordLoginEnabled": true, "otpLoginEnabled": true, "platformMode": true})
	})
	m.HandleFunc("GET /v2/config/version", p.version)
	m.HandleFunc("POST /admin/auth/login", p.adminLogin)
	m.HandleFunc("POST /admin/auth/logout", p.admin(p.adminLogout, false))
	m.HandleFunc("POST /admin/users/query", p.admin(p.users, false))
	m.HandleFunc("GET /admin/users/{id}/avatar/{tenant}", p.admin(p.adminAvatar, false))
	m.HandleFunc("GET /admin/auth/me", p.admin(p.adminMe, false))
	m.HandleFunc("GET /admin/tenants", p.admin(p.tenants, false))
	m.HandleFunc("POST /admin/tenants", p.admin(p.saveTenant, true))
	m.HandleFunc("PATCH /admin/tenants/{id}", p.admin(p.saveTenant, true))
	m.HandleFunc("POST /admin/tenants/{id}/default", p.admin(p.defaultTenant, true))
	m.HandleFunc("GET /admin/users", p.admin(p.users, false))
	m.HandleFunc("GET /admin/users/{id}", p.admin(p.userDetails, false))
	m.HandleFunc("POST /admin/users", p.admin(p.createUser, true))
	m.HandleFunc("POST /admin/users/{id}/switch", p.admin(p.switchUser, true))
	m.HandleFunc("POST /admin/users/{id}/ban", p.admin(p.banUser, true))
	m.HandleFunc("POST /admin/users/{id}/reset-password", p.admin(p.adminPassword, true))
	m.HandleFunc("GET /admin/operations", p.admin(p.operations, false))
	m.HandleFunc("POST /admin/operations/{id}/retry", p.admin(p.retry, true))
	m.HandleFunc("GET /admin/versions", p.admin(p.versions, false))
	m.HandleFunc("GET /admin/versions/{id}/history", p.admin(p.versionHistory, false))
	m.HandleFunc("PUT /admin/versions/{id}", p.admin(p.saveVersion, true))
	if p.o.StaticDir != "" {
		m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v2/") || strings.HasPrefix(r.URL.Path, "/admin/") {
				fail(w, 404, "NOT_FOUND")
				return
			}
			f := filepath.Join(p.o.StaticDir, filepath.Clean("/"+r.URL.Path))
			if _, e := os.Stat(f); e != nil || r.URL.Path == "/" {
				f = filepath.Join(p.o.StaticDir, "index.html")
			}
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, f)
		})
	}
	return p.publicMiddleware(m)
}
func (p *Platform) publicMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		for _, o := range p.cfg.AllowedOrigins {
			if origin == o {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Client-Platform")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,OPTIONS")
			}
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
func (p *Platform) allowed(ctx context.Context, key string, max int) bool {
	var n int
	e := p.DB.QueryRow(ctx, `INSERT INTO lp_rate(key,attempts) VALUES($1,1) ON CONFLICT(key) DO UPDATE SET attempts=CASE WHEN lp_rate.window_at<now()-interval '10 minutes' THEN 1 ELSE lp_rate.attempts+1 END,window_at=CASE WHEN lp_rate.window_at<now()-interval '10 minutes' THEN now() ELSE lp_rate.window_at END RETURNING attempts`, key).Scan(&n)
	return e == nil && n <= max
}

var phonePattern = regexp.MustCompile(`^[0-9]{11}$`)

func (p *Platform) verifyOTP(ctx context.Context, phone, code, purpose string) bool {
	if purpose == "login" || purpose == "register" {
		purpose = "auth"
	}
	if !phonePattern.MatchString(phone) || !p.allowed(ctx, "verify:"+phone, 20) {
		return false
	}
	if p.cfg.DevMode {
		return len(p.cfg.DevOTPCode) > 0 && subtle.ConstantTimeCompare([]byte(code), []byte(p.cfg.DevOTPCode)) == 1
	}
	var stored string
	e := p.DB.QueryRow(ctx, `UPDATE lp_otp SET attempts=attempts+1 WHERE phone=$1 AND purpose=$2 AND expires_at>now() AND attempts<5 RETURNING code_hash`, phone, purpose).Scan(&stored)
	if e != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(digest(code))) != 1 {
		return false
	}
	res, e := p.DB.Exec(ctx, `DELETE FROM lp_otp WHERE phone=$1 AND purpose=$2 AND code_hash=$3`, phone, purpose, stored)
	return e == nil && res.RowsAffected() == 1
}
func (p *Platform) code(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Phone   string `json:"phone"`
		Purpose string `json:"purpose"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Purpose == "" {
		b.Purpose = "login"
	}
	if strings.Contains(r.URL.Path, "reset") {
		b.Purpose = "reset"
	}
	if b.Purpose == "login" || b.Purpose == "register" {
		b.Purpose = "auth"
	}
	if b.Purpose != "auth" && b.Purpose != "reset" && b.Purpose != "phone" {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	if !phonePattern.MatchString(b.Phone) || !p.allowed(r.Context(), "code:"+b.Phone, 5) {
		fail(w, 429, "RATE_LIMITED")
		return
	}
	if !p.cfg.DevMode {
		if !serviceURL(p.cfg.OTPWebhookURL, "http", false) || p.cfg.OTPWebhookToken == "" {
			fail(w, 503, "SMS_UNAVAILABLE")
			return
		}
		code := numericCode()
		body, _ := json.Marshal(map[string]string{"phone": b.Phone, "code": code, "purpose": b.Purpose})
		q, _ := http.NewRequestWithContext(r.Context(), "POST", p.cfg.OTPWebhookURL, bytes.NewReader(body))
		q.Header.Set("Authorization", "Bearer "+p.cfg.OTPWebhookToken)
		q.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}
		resp, e := client.Do(q)
		if e != nil {
			fail(w, 503, "SMS_UNAVAILABLE")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			fail(w, 503, "SMS_UNAVAILABLE")
			return
		}
		if _, e = p.DB.Exec(r.Context(), `INSERT INTO lp_otp(phone,purpose,code_hash,expires_at) VALUES($1,$2,$3,now()+interval '5 minutes') ON CONFLICT(phone,purpose) DO UPDATE SET code_hash=$3,expires_at=EXCLUDED.expires_at,attempts=0`, b.Phone, b.Purpose, digest(code)); e != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
	}
	jsonResponse(w, 200, map[string]any{"sent": true, "retryAfter": 60})
}
func numericCode() string {
	n, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		panic(e)
	}
	v := itoa(int(n.Int64()))
	return strings.Repeat("0", 6-len(v)) + v
}

func (p *Platform) user(ctx context.Context, id string) (User, error) {
	var u User
	var b []byte
	e := p.DB.QueryRow(ctx, `SELECT id,phone,tenant_id,revision,banned,pending,profile FROM lp_users WHERE id=$1`, id).Scan(&u.ID, &u.Phone, &u.TenantID, &u.Revision, &u.Banned, &u.Pending, &b)
	_ = json.Unmarshal(b, &u.Profile)
	return u, e
}
func (p *Platform) tenant(ctx context.Context, id string) (Tenant, error) {
	var t Tenant
	var b []byte
	e := p.DB.QueryRow(ctx, `SELECT id,name,code,enabled,is_default,version,services,control_url FROM lp_tenants WHERE id=$1`, id).Scan(&t.ID, &t.Name, &t.Code, &t.Enabled, &t.Default, &t.Version, &b, &t.ControlURL)
	_ = json.Unmarshal(b, &t.Services)
	return t, e
}

var errInvalidEnterpriseCode = errors.New("invalid enterprise code")
var errDefaultEnterpriseUnavailable = errors.New("default enterprise unavailable")

func (p *Platform) registrationTenant(ctx context.Context, code string) (Tenant, error) {
	code = strings.TrimSpace(code)
	var id string
	e := p.DB.QueryRow(ctx, `SELECT id FROM lp_tenants WHERE enabled AND (($1<>'' AND upper(code)=upper($1)) OR ($1='' AND is_default))`, code).Scan(&id)
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			if code == "" {
				return Tenant{}, errDefaultEnterpriseUnavailable
			}
			return Tenant{}, errInvalidEnterpriseCode
		}
		return Tenant{}, e
	}
	return p.tenant(ctx, id)
}

type loginBody struct {
	Phone      string `json:"phone"`
	Password   string `json:"password"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	InviteCode string `json:"inviteCode"`
}

func (p *Platform) register(w http.ResponseWriter, r *http.Request) {
	var b loginBody
	if !decode(w, r, &b) {
		return
	}
	if !p.verifyOTP(r.Context(), b.Phone, b.Code, "register") || len(b.Password) < 6 || len(b.Password) > 72 || strings.TrimSpace(b.Name) == "" || len([]rune(b.Name)) > 40 {
		fail(w, 400, "INVALID_REGISTRATION")
		return
	}
	u, e := p.addUser(r.Context(), b)
	if e != nil {
		if errors.Is(e, errInvalidEnterpriseCode) {
			fail(w, 400, "INVALID_ENTERPRISE_CODE")
			return
		}
		if errors.Is(e, errDefaultEnterpriseUnavailable) {
			fail(w, 503, "DEFAULT_ENTERPRISE_UNAVAILABLE")
			return
		}
		fail(w, 409, "REGISTRATION_FAILED")
		return
	}
	p.session(w, r, u)
}
func (p *Platform) addUser(ctx context.Context, b loginBody, audit ...string) (User, error) {
	t, e := p.registrationTenant(ctx, b.InviteCode)
	if e != nil {
		return User{}, e
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
	if e != nil {
		return User{}, e
	}
	u := User{ID: ID(), Phone: b.Phone, TenantID: t.ID, Revision: 1, Profile: Profile{Name: b.Name, Gender: "unspecified", CreatedAt: time.Now().UTC()}}
	pr, _ := json.Marshal(u.Profile)
	tx, e := p.DB.Begin(ctx)
	if e != nil {
		return u, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "phone:"+u.Phone); e != nil {
		return u, e
	}
	var reserved bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lp_phone_reservations WHERE phone=$1)`, u.Phone).Scan(&reserved); e != nil || reserved {
		return u, errors.New("phone reserved")
	}
	_, e = tx.Exec(ctx, `INSERT INTO lp_users(id,phone,password_hash,tenant_id,profile) VALUES($1,$2,$3,$4,$5)`, u.ID, u.Phone, string(hash), t.ID, pr)
	if e != nil {
		return u, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO lp_memberships(user_id,tenant_id,profile,assignment_version) VALUES($1,$2,$3,1)`, u.ID, t.ID, pr)
	if e != nil {
		return u, e
	}
	if len(audit) == 2 {
		if e = auditTx(ctx, tx, audit[0], "user.create", u.ID, audit[1], "success"); e != nil {
			return u, e
		}
	}
	return u, tx.Commit(ctx)
}
func (p *Platform) login(w http.ResponseWriter, r *http.Request) {
	var b loginBody
	if !decode(w, r, &b) {
		return
	}
	if !p.allowed(r.Context(), "login:"+b.Phone, 20) {
		fail(w, 429, "RATE_LIMITED")
		return
	}
	var id, hash string
	e := p.DB.QueryRow(r.Context(), `SELECT id,password_hash FROM lp_users WHERE phone=$1`, b.Phone).Scan(&id, &hash)
	valid := false
	if strings.HasSuffix(r.URL.Path, "password-login") {
		valid = bcrypt.CompareHashAndPassword([]byte(hash), []byte(b.Password)) == nil
	} else {
		valid = p.verifyOTP(r.Context(), b.Phone, b.Code, "login")
	}
	if e != nil || !valid {
		fail(w, 401, "INVALID_CREDENTIALS")
		return
	}
	u, e := p.user(r.Context(), id)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	p.session(w, r, u)
}
func (p *Platform) session(w http.ResponseWriter, r *http.Request, u User) {
	if u.Banned {
		fail(w, 403, "ACCOUNT_UNAVAILABLE")
		return
	}
	if u.Pending != "" {
		fail(w, 409, "OPERATION_PENDING")
		return
	}
	g, e := p.grant(r.Context(), u)
	if e != nil {
		fail(w, 503, "ENTERPRISE_UNAVAILABLE")
		return
	}
	a, refresh := ID(), ID()
	_, e = p.DB.Exec(r.Context(), `INSERT INTO lp_sessions(token_hash,user_id,expires_at) VALUES($1,$3,now()+interval '15 minutes'),($2,$3,now()+interval '30 days')`, "a:"+digest(a), "r:"+digest(refresh), u.ID)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	jsonResponse(w, 200, map[string]any{"accessToken": a, "refreshToken": refresh, "enterprise": g, "expiresIn": 900})
}
func (p *Platform) grant(ctx context.Context, u User) (Grant, error) {
	t, e := p.tenant(ctx, u.TenantID)
	if e != nil || !t.Enabled || u.Banned || u.Pending != "" {
		return Grant{}, errors.New("not available")
	}
	if e = p.checkDeployment(ctx, t); e != nil {
		return Grant{}, e
	}
	token := p.signTicket(u)
	_, e = p.DB.Exec(ctx, `INSERT INTO lp_tickets(token_hash,user_id,tenant_id,revision,expires_at) VALUES($1,$2,$3,$4,now()+interval '60 seconds')`, digest(token), u.ID, t.ID, u.Revision)
	return Grant{token, u, t}, e
}
func (p *Platform) authenticated(r *http.Request, token, kind string) (User, error) {
	var id string
	e := p.DB.QueryRow(r.Context(), `SELECT user_id FROM lp_sessions WHERE token_hash=$1 AND expires_at>now() AND NOT revoked`, kind+":"+digest(token)).Scan(&id)
	if e != nil {
		return User{}, e
	}
	return p.user(r.Context(), id)
}
func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func (p *Platform) route(w http.ResponseWriter, r *http.Request) {
	u, e := p.authenticated(r, bearer(r), "a")
	if e != nil {
		fail(w, 401, "UNAUTHENTICATED")
		return
	}
	g, e := p.grant(r.Context(), u)
	if e != nil {
		fail(w, 403, "ACCOUNT_UNAVAILABLE")
		return
	}
	jsonResponse(w, 200, g)
}
func (p *Platform) refresh(w http.ResponseWriter, r *http.Request) {
	var b struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !decode(w, r, &b) {
		return
	}
	u, e := p.authenticated(r, b.RefreshToken, "r")
	if e != nil {
		fail(w, 401, "UNAUTHENTICATED")
		return
	}
	p.session(w, r, u) /* device business sessions are fenced by the enterprise, independent of directory refresh */
}
func (p *Platform) logout(w http.ResponseWriter, r *http.Request) {
	var b struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !decode(w, r, &b) {
		return
	}
	_, e := p.DB.Exec(r.Context(), `UPDATE lp_sessions SET revoked=true WHERE token_hash=$1 OR token_hash=$2`, "r:"+digest(b.RefreshToken), "a:"+digest(bearer(r)))
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (p *Platform) validateCode(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Code       string `json:"code"`
		InviteCode string `json:"inviteCode"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Code == "" {
		b.Code = b.InviteCode
	}
	t, e := p.registrationTenant(r.Context(), b.Code)
	if e != nil {
		if errors.Is(e, errInvalidEnterpriseCode) {
			fail(w, 400, "INVALID_ENTERPRISE_CODE")
		} else if errors.Is(e, errDefaultEnterpriseUnavailable) {
			fail(w, 503, "DEFAULT_ENTERPRISE_UNAVAILABLE")
		} else {
			fail(w, 503, "DATABASE_UNAVAILABLE")
		}
		return
	}
	jsonResponse(w, 200, map[string]any{"valid": true, "tenantName": t.Name})
}
func (p *Platform) resetPassword(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Phone    string `json:"phone"`
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.Password) < 6 || len(b.Password) > 72 || !p.verifyOTP(r.Context(), b.Phone, b.Code, "reset") {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	var id string
	if p.DB.QueryRow(r.Context(), `SELECT id FROM lp_users WHERE phone=$1`, b.Phone).Scan(&id) != nil {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	if e := p.password(r.Context(), id, b.Password); e != nil {
		fail(w, 503, "RESET_INCOMPLETE")
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (p *Platform) version(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		platform = r.Header.Get("X-Client-Platform")
	}
	current := r.URL.Query().Get("version")
	if current == "" {
		current = "1.0.12"
	}
	if platform != "web" && platform != "ios" && platform != "android" && platform != "macos" {
		fail(w, 400, "INVALID_PLATFORM")
		return
	}
	if _, valid := versionCompare(current, current); !valid {
		fail(w, 400, "INVALID_VERSION")
		return
	}
	var b []byte
	err := p.DB.QueryRow(r.Context(), `SELECT policy FROM lp_versions WHERE platform=$1`, platform).Scan(&b)
	if err != nil && err != pgx.ErrNoRows {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	policy := map[string]any{}
	if err == nil {
		_ = json.Unmarshal(b, &policy)
	}
	jsonResponse(w, 200, versionDecision(platform, current, policy))
}
func (p *Platform) control(ctx context.Context, t Tenant, path string, in, out any) error {
	b, _ := json.Marshal(in)
	r, e := http.NewRequestWithContext(ctx, "POST", t.ControlURL+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	resp, e := p.client.Do(r)
	if e != nil {
		return errors.New("enterprise control unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return errors.New("enterprise rejected operation")
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	return nil
}
