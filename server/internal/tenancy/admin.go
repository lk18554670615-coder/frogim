package tenancy

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func itoa(n int) string { return strconv.Itoa(n) }
func (p *Platform) signingKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("directory-ticket:" + p.cfg.JWTSecret))
	return ed25519.NewKeyFromSeed(seed[:])
}
func (p *Platform) signTicket(u User) string {
	c := jwt.MapClaims{"sub": u.ID, "aud": u.TenantID, "ver": u.Revision, "typ": "enterprise-grant", "jti": ID(), "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()}
	s, e := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c).SignedString(p.signingKey())
	if e != nil {
		panic(e)
	}
	return s
}
func (p *Platform) adminLogin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !p.allowed(r.Context(), "admin:"+r.RemoteAddr, 15) {
		fail(w, 429, "RATE_LIMITED")
		return
	}
	role := "operator"
	hash := p.cfg.AdminPasswordHash
	if b.Username != p.cfg.AdminUsername {
		role = "viewer"
		hash = os.Getenv("IM_PLATFORM_VIEWER_PASSWORD_HASH")
		if b.Username != os.Getenv("IM_PLATFORM_VIEWER_USERNAME") || hash == "" {
			fail(w, 401, "INVALID_CREDENTIALS")
			return
		}
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(b.Password)) != nil {
		fail(w, 401, "INVALID_CREDENTIALS")
		return
	}
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": b.Username, "role": role, "typ": "platform-admin", "exp": time.Now().Add(8 * time.Hour).Unix()}).SignedString([]byte(p.cfg.JWTSecret))
	http.SetCookie(w, &http.Cookie{Name: "lp_admin", Value: token, Path: "/platform/", HttpOnly: true, Secure: !p.cfg.DevMode, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	jsonResponse(w, 200, map[string]string{"role": role, "username": b.Username, "token": token})
}

type actorKey struct{}

func (p *Platform) admin(next http.HandlerFunc, write bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := bearer(r)
		if raw == "" {
			if c, e := r.Cookie("lp_admin"); e == nil {
				raw = c.Value
			}
		}
		t, e := jwt.Parse(raw, func(t *jwt.Token) (any, error) { return []byte(p.cfg.JWTSecret), nil }, jwt.WithValidMethods([]string{"HS256"}))
		if e != nil || !t.Valid {
			fail(w, 401, "UNAUTHENTICATED")
			return
		}
		c := t.Claims.(jwt.MapClaims)
		if c["typ"] != "platform-admin" {
			fail(w, 401, "UNAUTHENTICATED")
			return
		}
		if write && c["role"] != "operator" {
			fail(w, 403, "READ_ONLY")
			return
		}
		if write && r.Header.Get("Origin") != "" {
			ok := false
			for _, o := range p.cfg.AllowedOrigins {
				ok = ok || o == r.Header.Get("Origin")
			}
			if !ok {
				fail(w, 403, "ORIGIN_REJECTED")
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, c)))
	}
}
func (p *Platform) adminMe(w http.ResponseWriter, r *http.Request) {
	c := r.Context().Value(actorKey{}).(jwt.MapClaims)
	jsonResponse(w, 200, map[string]any{"username": c["sub"], "role": c["role"]})
}
func actor(r *http.Request) string {
	return r.Context().Value(actorKey{}).(jwt.MapClaims)["sub"].(string)
}

type Confirmation struct {
	Reason    string `json:"reason"`
	Confirmed bool   `json:"confirmed"`
	Version   int64  `json:"version"`
}

func (b Confirmation) valid() bool {
	return b.Confirmed && len(strings.TrimSpace(b.Reason)) > 0 && len([]rune(b.Reason)) <= 1000
}
func auditTx(ctx context.Context, tx pgx.Tx, a, action, id, reason, result string) error {
	_, e := tx.Exec(ctx, `INSERT INTO lp_audit(actor,action,object_id,reason,result) VALUES($1,$2,$3,$4,$5)`, a, action, id, reason, result)
	return e
}
func (p *Platform) tenants(w http.ResponseWriter, r *http.Request) {
	rows, e := p.DB.Query(r.Context(), `SELECT id FROM lp_tenants ORDER BY name,id`)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	items := []Tenant{}
	for _, id := range ids {
		t, e := p.tenant(r.Context(), id)
		if e != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		items = append(items, t)
	}
	jsonResponse(w, 200, items)
}
func (p *Platform) saveTenant(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Tenant
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !(Confirmation{Reason: b.Reason, Confirmed: b.Confirmed}).valid() || len([]rune(strings.TrimSpace(b.Name))) < 1 || len([]rune(b.Name)) > 120 || !regexpCode.MatchString(b.Code) || !serviceURL(b.ControlURL, "https", false) || !serviceURL(b.Services.API, "http", p.cfg.DevMode) || !serviceURL(b.Services.IMWS, "ws", p.cfg.DevMode) || !serviceURL(b.Services.IMTCP, "tcp", false) || !serviceURL(b.Services.RTC, "ws", p.cfg.DevMode) || !serviceURL(b.Services.Media, "http", p.cfg.DevMode) {
		fail(w, 400, "INVALID_ENTERPRISE")
		return
	}
	b.Code = strings.ToUpper(b.Code)
	ctx := r.Context()
	tx, e := p.DB.Begin(ctx)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(712345)`); e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	services, _ := json.Marshal(b.Services)
	id := r.PathValue("id")
	if id == "" {
		id = b.ID
		if !regexpCode.MatchString(id) {
			fail(w, 400, "INVALID_ID")
			return
		}
		var count int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM lp_tenants`).Scan(&count); e == nil {
			_, e = tx.Exec(ctx, `INSERT INTO lp_tenants(id,name,code,enabled,is_default,services,control_url) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, b.Name, b.Code, b.Enabled, count == 0, services, b.ControlURL)
		}
	} else {
		var prior Tenant
		prior, e = p.tenant(ctx, id)
		if e == nil && prior.Default && !b.Enabled {
			fail(w, 409, "DEFAULT_MUST_REMAIN_ENABLED")
			return
		}
		if e == nil {
			res, err := tx.Exec(ctx, `UPDATE lp_tenants SET name=$2,code=$3,enabled=$4,services=$5,control_url=$6,version=version+1 WHERE id=$1 AND version=$7`, id, b.Name, b.Code, b.Enabled, services, b.ControlURL, b.Tenant.Version)
			e = err
			if e == nil && res.RowsAffected() != 1 {
				e = errors.New("version conflict")
			}
		}
	}
	if e == nil {
		e = auditTx(ctx, tx, actor(r), "tenant.save", id, b.Reason, "success")
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		fail(w, 409, "CONFLICT")
		return
	}
	t, _ := p.tenant(ctx, id)
	jsonResponse(w, 200, t)
}
func (p *Platform) defaultTenant(w http.ResponseWriter, r *http.Request) {
	var b Confirmation
	if !decode(w, r, &b) {
		return
	}
	if !b.valid() {
		fail(w, 400, "CONFIRMATION_REQUIRED")
		return
	}
	ctx := r.Context()
	tx, e := p.DB.Begin(ctx)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(712345)`)
	var enabled bool
	var ver int64
	if e == nil {
		e = tx.QueryRow(ctx, `SELECT enabled,version FROM lp_tenants WHERE id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&enabled, &ver)
	}
	if e != nil || !enabled || ver != b.Version {
		fail(w, 409, "CONFLICT")
		return
	}
	_, e = tx.Exec(ctx, `UPDATE lp_tenants SET is_default=false,version=version+1 WHERE is_default AND id<>$1`, r.PathValue("id"))
	if e == nil {
		_, e = tx.Exec(ctx, `UPDATE lp_tenants SET is_default=true,version=version+1 WHERE id=$1`, r.PathValue("id"))
	}
	if e == nil {
		e = auditTx(ctx, tx, actor(r), "tenant.default", r.PathValue("id"), b.Reason, "success")
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (p *Platform) users(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Query  string `json:"query"`
		Tenant string `json:"tenant"`
		Page   int    `json:"page"`
	}
	if r.Method == "POST" {
		if !decode(w, r, &b) {
			return
		}
	} else {
		b.Tenant = r.URL.Query().Get("tenant")
		b.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	}
	if b.Page < 1 {
		b.Page = 1
	}
	if b.Page > 100000 || len(b.Query) > 120 {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	rows, e := p.DB.Query(r.Context(), `SELECT id FROM lp_users WHERE ($1='' OR id=$1 OR phone LIKE '%'||$1||'%' OR profile->>'name' ILIKE '%'||$1||'%') AND ($2='' OR tenant_id=$2) ORDER BY created_at DESC,id LIMIT 101 OFFSET $3`, b.Query, b.Tenant, (b.Page-1)*100)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	items := []User{}
	for _, id := range ids {
		u, e := p.user(r.Context(), id)
		if e != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		items = append(items, u)
	}
	more := len(items) > 100
	if more {
		items = items[:100]
	}
	jsonResponse(w, 200, map[string]any{"items": items, "page": b.Page, "hasMore": more})
}
func (p *Platform) userDetails(w http.ResponseWriter, r *http.Request) {
	u, e := p.user(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, 404, "NOT_FOUND")
		return
	}
	rows, e := p.DB.Query(r.Context(), `SELECT tenant_id,profile,profile_version,synced_at,sync_error FROM lp_memberships WHERE user_id=$1 ORDER BY tenant_id`, u.ID)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, err string
		var profile []byte
		var v int64
		var at *time.Time
		if rows.Scan(&id, &profile, &v, &at, &err) != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		var pr Profile
		_ = json.Unmarshal(profile, &pr)
		items = append(items, map[string]any{"tenantId": id, "profile": pr, "profileVersion": v, "syncedAt": at, "syncError": err, "current": id == u.TenantID})
	}
	rows.Close()
	for _, item := range items {
		tenant, err := p.tenant(r.Context(), item["tenantId"].(string))
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		var status struct {
			Failed bool `json:"failed"`
		}
		err = p.control(ctx, tenant, "/internal/directory/profile-status", map[string]string{"userId": u.ID}, &status)
		cancel()
		if err != nil {
			item["syncError"] = "企业连接不可达，无法确认最新资料"
		} else if status.Failed {
			item["syncError"] = "资料同步失败，请检查企业与平台的私网连接"
		}
	}
	jsonResponse(w, 200, map[string]any{"user": u, "memberships": items})
}
func (p *Platform) createUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		loginBody
		Confirmation
	}
	if !decode(w, r, &b) {
		return
	}
	if !b.Confirmation.valid() || !phonePattern.MatchString(b.Phone) || len(b.Password) < 6 || len(b.Password) > 72 || b.Name == "" {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	u, e := p.addUser(r.Context(), b.loginBody, actor(r), b.Reason)
	if e != nil {
		fail(w, 409, "CONFLICT")
		return
	}
	jsonResponse(w, 200, u)
}
func (p *Platform) operations(w http.ResponseWriter, r *http.Request) {
	rows, e := p.DB.Query(r.Context(), `SELECT id,user_id,actor,kind,source,target,revision,phase,reason,error,updated_at FROM lp_operations UNION ALL SELECT 'audit_'||id::text,object_id,actor,action,'','',0,'done',reason,CASE WHEN result='success' THEN '' ELSE result END,at FROM lp_audit WHERE action IN ('tenant.save','tenant.default','version.save','user.create','operation.retry') ORDER BY 11 DESC LIMIT 100`)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, uid, a, kind, src, dst, phase, reason, err string
		var ver int64
		var at time.Time
		if rows.Scan(&id, &uid, &a, &kind, &src, &dst, &ver, &phase, &reason, &err, &at) != nil {
			fail(w, 503, "DATABASE_UNAVAILABLE")
			return
		}
		items = append(items, map[string]any{"id": id, "userId": uid, "actor": a, "kind": kind, "source": src, "target": dst, "version": ver, "phase": phase, "reason": reason, "error": err, "updatedAt": at})
	}
	jsonResponse(w, 200, items)
}
func (p *Platform) versions(w http.ResponseWriter, r *http.Request) {
	rows, e := p.DB.Query(r.Context(), `SELECT platform,policy,version FROM lp_versions ORDER BY platform`)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var name string
		var b []byte
		var v int64
		_ = rows.Scan(&name, &b, &v)
		items = append(items, map[string]any{"platform": name, "policy": json.RawMessage(b), "version": v})
	}
	jsonResponse(w, 200, items)
}
func (p *Platform) saveVersion(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Confirmation
		Policy map[string]any `json:"policy"`
	}
	if !decode(w, r, &b) {
		return
	}
	id := r.PathValue("id")
	if !b.valid() || (id != "web" && id != "ios" && id != "android") {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	minimum, _ := b.Policy["minimumVersion"].(string)
	latest, _ := b.Policy["latestVersion"].(string)
	order, valid := versionCompare(minimum, latest)
	download, _ := b.Policy["downloadUrl"].(string)
	if !valid || order > 0 || (download != "" && !serviceURL(download, "http", p.cfg.DevMode)) {
		fail(w, 400, "INVALID_VERSION_POLICY")
		return
	}
	b.Policy["platform"] = id
	policy, _ := json.Marshal(b.Policy)
	ctx := r.Context()
	tx, e := p.DB.Begin(ctx)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	res, e := tx.Exec(ctx, `UPDATE lp_versions SET policy=$2,version=version+1 WHERE platform=$1 AND version=$3`, id, policy, b.Version)
	if b.Version == 0 {
		res, e = tx.Exec(ctx, `INSERT INTO lp_versions(platform,policy) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, policy)
	}
	if e == nil && res.RowsAffected() == 0 {
		e = errors.New("version conflict")
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO lp_version_history(platform,version,policy,actor,reason) SELECT platform,version,policy,$2,$3 FROM lp_versions WHERE platform=$1`, id, actor(r), b.Reason)
	}
	if e == nil {
		e = auditTx(ctx, tx, actor(r), "version.save", id, b.Reason, "success")
	}
	if e == nil {
		e = tx.Commit(ctx)
	}
	if e != nil {
		fail(w, 409, "CONFLICT")
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (p *Platform) adminPassword(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Confirmation
		Password string `json:"password"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !b.valid() || len(b.Password) < 6 || len(b.Password) > 72 {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	u, e := p.user(r.Context(), r.PathValue("id"))
	if e != nil || u.Revision != b.Version {
		fail(w, 409, "CONFLICT")
		return
	}
	if e = p.password(r.Context(), u.ID, b.Password, actor(r), b.Reason); e != nil {
		fail(w, 503, "RESET_INCOMPLETE")
		return
	}
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (p *Platform) password(ctx context.Context, id, password string, audit ...string) error {
	u, e := p.user(ctx, id)
	if e != nil {
		return e
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return e
	}
	a, reason := "user:"+id, "本人验证码重置凭据"
	if len(audit) == 2 {
		a, reason = audit[0], audit[1]
	}
	o, e := p.startOperation(ctx, id, u.TenantID, "password", a, reason, u.Revision, OperationValues{PasswordHash: string(hash)})
	if e != nil {
		return e
	}
	e = p.runOperation(ctx, o)
	if e != nil {
		_, _ = p.DB.Exec(context.Background(), `UPDATE lp_operations SET error=$2,updated_at=now() WHERE id=$1`, o.ID, e.Error())
	}
	return e
}
func publicKey(p *Platform) string {
	return base64.RawURLEncoding.EncodeToString(p.signingKey().Public().(ed25519.PublicKey))
}
