package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestUserAccessPostgresHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("IM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("IM_TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)
	schema := fmt.Sprintf("ip_http_%d", time.Now().UnixNano())
	if _, err = connection.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := connection.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	u, _ := url.Parse(dsn)
	params := u.Query()
	params.Set("search_path", schema)
	u.RawQuery = params.Encode()
	p, err := store.NewPostgres(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	hash, _ := bcrypt.GenerateFromPassword([]byte("InitialPassword123!"), bcrypt.MinCost)
	_, err = p.BootstrapAdmin(ctx, store.AdminAccountCreate{ID: "ip-admin", Username: "admin", DisplayName: "Test Admin", RoleID: "platform_admin", PasswordHash: string(hash), At: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	x := New(config.Config{JWTSecret: strings.Repeat("s", 32), DevMode: true, DevOTPCode: "654321", AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour}, a)
	defer x.Close()
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go x.RunUserAccess(workerCtx)
	w := accessRequest(x, "/v2/auth/register", `{"phone":"13900008888","name":"IP Integration","password":"StrongPass123!","code":"654321"}`, "android", "203.0.113.9", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var login struct {
		User        struct{ ID string }
		AccessToken string
	}
	json.Unmarshal(w.Body.Bytes(), &login)
	waitAccess(t, x)
	profile, err := p.UserAccessProfiles(ctx, []string{login.User.ID}, "")
	if err != nil || profile[login.User.ID].RegistrationIP != "203.0.113.9" || profile[login.User.ID].LastLoginIP != "203.0.113.9" {
		t.Fatal(profile, err)
	}
	w = accessRequest(x, "/v2/auth/password-login", `{"phone":"13900008888","password":"wrong"}`, "ios", "203.0.113.10", "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	waitAccess(t, x)
	adminToken, _ := x.auth.IssueAdmin("ip-admin", "platform_admin", time.Hour, 1)
	get := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		x.Handler().ServeHTTP(w, r)
		return w
	}
	w = get("/v2/admin/users?ip=203.0.113.9&ipSource=any", adminToken)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"registrationIp":"203.0.113.9"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = get("/v2/admin/users?ip=203.0.113.10", adminToken)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatal("failed IP association", w.Body.String())
	}
	w = get("/v2/admin/user-access-logs?userId="+login.User.ID+"&result=failed", adminToken)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"failureCode":"INVALID_CREDENTIALS"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = get("/v2/users/me", login.AccessToken)
	if w.Code != 200 || strings.Contains(w.Body.String(), "203.0.113") || strings.Contains(w.Body.String(), "registrationIp") {
		t.Fatal("public IP leak", w.Code, w.Body.String())
	}
	// Both admin entry points must mark the source without treating the operator's IP as a registration IP.
	w = accessRequest(x, "/v2/admin/users", `{"phone":"13900008889","name":"Admin created","password":"StrongPass123!","gender":"male","confirmed":true,"reason":"IP integration test"}`, "web", "203.0.113.50", adminToken)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = accessRequest(x, "/v2/admin/users/batch", `{"items":[{"clientRow":2,"phone":"13900008890","name":"Batch created","password":"StrongPass123!","gender":"female"}],"confirmed":true,"reason":"IP integration test"}`, "web", "203.0.113.50", adminToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	waitAccess(t, x)
	page, err := p.ListUserAccessLogs(ctx, store.UserAccessQuery{Method: "admin", From: time.Now().Add(-time.Hour), To: time.Now(), Limit: 20})
	if err != nil || len(page.Items) != 2 {
		t.Fatal(page, err)
	}
	for _, e := range page.Items {
		profiles, err := p.UserAccessProfiles(ctx, []string{e.UserID}, "")
		if err != nil || e.IP != "" || profiles[e.UserID].RegistrationSource != "admin" || profiles[e.UserID].LastLoginIP != "" {
			t.Fatal(e, profiles, err)
		}
	}
	audits, _, _, err := p.ListAdminAudits(ctx, "user.access_logs.viewed", "", "", 100)
	if err != nil || len(audits) == 0 {
		t.Fatal("missing persisted access audit", err)
	}
	if audits[0].Metadata["returned"] == nil {
		t.Fatal("missing query count")
	}
}
