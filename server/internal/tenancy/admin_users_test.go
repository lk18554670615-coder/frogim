package tenancy

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/config"
	"net/http"
	"os"
	"strings"
	"testing"
)

type profileCounter struct {
	calls       int
	unreachable bool
}

func (s *profileCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls++
	if s.unreachable {
		return nil, context.DeadlineExceeded
	}
	return response(map[string]bool{"failed": false}), nil
}

func TestAdminUserInformation(t *testing.T) {
	dsn := os.Getenv("TEST_LIGHT_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated local PostgreSQL")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	name := "lp_admin_users_" + ID()[:16]
	if _, err = base.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close(); _, _ = base.Exec(ctx, "DROP DATABASE "+name) }()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err = db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(schema)
	exec(`INSERT INTO lp_tenants(id,name,code,services,control_url,is_default) VALUES('a','企业A','A','{}','https://a:8443',true),('b','企业B','B','{}','https://b:8443',false)`)
	exec(`INSERT INTO lp_users(id,phone,password_hash,tenant_id,profile,banned,pending,created_at) VALUES
 ('u-a','13800000001','secret-must-never-leak','a','{"name":"用户甲","handle":"alpha"}',true,'unfinished','2026-01-03T00:00:00Z'),
 ('u-b','13800000002','secret-must-never-leak','b','{"name":"用户乙","handle":"beta"}',false,'','2026-01-02T00:00:00Z'),
 ('missing','13800000003','','a','{}',false,'','2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO lp_memberships(user_id,tenant_id,profile,profile_version,synced_at) SELECT id,tenant_id,profile,4,now() FROM lp_users WHERE id<>'missing'`)
	exec(`INSERT INTO lp_memberships(user_id,tenant_id,profile,profile_version,synced_at) VALUES('u-a','b','{"name":"甲在B"}',2,now()),('u-b','a','{"name":"乙在A"}',3,now())`)
	stub := &profileCounter{}
	p := &Platform{DB: db, cfg: config.Config{DevMode: true, JWTSecret: strings.Repeat("x", 40)}, client: &http.Client{Transport: stub}}
	for _, tc := range []struct {
		name string
		body map[string]any
		ids  []string
	}{
		{"current enterprise", map[string]any{"tenant": "a"}, []string{"u-a", "missing"}},
		{"membership includes other current enterprise", map[string]any{"tenant": "a", "tenantScope": "membership"}, []string{"u-a", "u-b"}},
		{"current nickname", map[string]any{"query": "用户乙"}, []string{"u-b"}},
		{"current handle", map[string]any{"query": "beta"}, []string{"u-b"}},
		{"ID", map[string]any{"query": "u-a"}, []string{"u-a"}},
		{"phone", map[string]any{"query": "13800000002"}, []string{"u-b"}},
		{"banned", map[string]any{"banned": true}, []string{"u-a"}},
		{"pending", map[string]any{"pending": true}, []string{"u-a"}},
		{"not banned and no pending", map[string]any{"banned": false, "pending": false}, []string{"u-b", "missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := testRequest(p, "POST", "/admin/users/query", tc.body, "viewer")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var result struct {
				Items []AdminUser `json:"items"`
			}
			if json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatal("invalid result")
			}
			if len(result.Items) != len(tc.ids) {
				t.Fatal("unexpected count", len(result.Items))
			}
			for i, u := range result.Items {
				if u.ID != tc.ids[i] {
					t.Fatal("wrong filter result")
				}
				if u.RegisteredAt.IsZero() {
					t.Fatal("registration missing")
				}
				if u.ID == "missing" {
					if u.CurrentMembership != nil || u.MembershipCount != 0 {
						t.Fatal("missing profile misrepresented")
					}
				} else if u.MembershipCount != 2 || u.CurrentMembership.Version != 4 {
					t.Fatal("summary incorrect")
				}
			}
			if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "secret-must-never-leak") || strings.Contains(w.Body.String(), "token") {
				t.Fatal("credential exposed")
			}
		})
	}
	if stub.calls != 0 {
		t.Fatal("list called enterprise service")
	}
	if w := testRequest(p, "GET", "/admin/users?tenant=a", nil, "viewer"); w.Code != 200 {
		t.Fatal("GET compatibility")
	}
	if w := testRequest(p, "POST", "/admin/users/query", map[string]any{"tenantScope": "unknown"}, "viewer"); w.Code != 400 {
		t.Fatal("invalid scope accepted")
	}
	if w := testRequest(p, "POST", "/admin/users/query", map[string]any{}, ""); w.Code != 401 {
		t.Fatal("unauthenticated read allowed")
	}
	if w := testRequest(p, "POST", "/admin/users/u-a/ban", map[string]any{}, "viewer"); w.Code != 403 {
		t.Fatal("viewer write allowed")
	}
	stub.unreachable = true
	w := testRequest(p, "GET", "/admin/users/u-a", nil, "viewer")
	var detail struct {
		User        AdminUser        `json:"user"`
		Memberships []map[string]any `json:"memberships"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.User.RegisteredAt.IsZero() || len(detail.Memberships) != 2 {
		t.Fatal("cached detail lost")
	}
	if !strings.Contains(w.Body.String(), "无法确认最新资料") {
		t.Fatal("unreachable enterprise not disclosed")
	}
	// Management summaries must not change the shared login user type.
	user, err := p.user(ctx, "u-a")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(user)
	if strings.Contains(string(data), "registeredAt") || strings.Contains(string(data), "membershipCount") {
		t.Fatal("management fields leaked into login user")
	}
	exec(`INSERT INTO lp_users(id,phone,tenant_id) SELECT 'page-'||lpad(n::text,3,'0'),'139'||lpad(n::text,8,'0'),'a' FROM generate_series(1,101) n`)
	for page, wanted := range map[int]int{1: 100, 2: 1} {
		w := testRequest(p, "POST", "/admin/users/query", map[string]any{"query": "139", "page": page}, "viewer")
		var result struct {
			Items   []AdminUser `json:"items"`
			HasMore bool        `json:"hasMore"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != wanted || result.HasMore != (page == 1) {
			t.Fatal("pagination changed")
		}
	}
}
