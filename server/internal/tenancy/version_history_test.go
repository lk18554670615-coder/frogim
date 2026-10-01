package tenancy

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/config"
)

func TestVersionHistory(t *testing.T) {
	dsn := os.Getenv("TEST_LIGHT_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_LIGHT_DATABASE_URL to an isolated local PostgreSQL instance")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	name := "lp_version_test_" + ID()[:16]
	if _, err = base.Exec(ctx, `CREATE DATABASE `+name); err != nil {
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
	defer func() { db.Close(); _, _ = base.Exec(ctx, `DROP DATABASE `+name) }()
	exec := func(sql string) {
		t.Helper()
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(schema)
	exec(`INSERT INTO lp_versions(platform,version,policy) VALUES('ios',4,'{"latestVersion":"1.0.8","minimumVersion":"1.0.6","releaseNotes":"existing"}')`)
	p := &Platform{DB: db, cfg: config.Config{DevMode: true, JWTSecret: strings.Repeat("h", 40)}}
	body := func(version int, latest string) map[string]any {
		return map[string]any{"version": version, "reason": "发布测试策略", "confirmed": true, "policy": map[string]any{"latestVersion": latest, "minimumVersion": "1.0.10", "forceUpdate": false, "downloadUrl": "https://example.com/app.apk", "releaseNotes": "消息修复"}}
	}
	read := func(client, query, role string) []versionPublication {
		t.Helper()
		w := testRequest(p, "GET", "/admin/versions/"+client+"/history"+query, nil, role)
		if w.Code != 200 {
			t.Fatalf("history %d %s", w.Code, w.Body)
		}
		var result struct {
			Items []versionPublication `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Items
	}
	t.Run("incremental_snapshot_is_honest_and_idempotent", func(t *testing.T) {
		exec(schema)
		exec(schema)
		rows := read("ios", "", "viewer")
		if len(rows) != 1 || rows[0].Version != 4 || rows[0].Source != "baseline" || rows[0].Actor != "" || !strings.Contains(string(rows[0].Policy), "existing") {
			t.Fatalf("baseline: %+v", rows)
		}
		if len(read("android", "", "viewer")) != 0 {
			t.Fatal("cross-client history")
		}
	})
	t.Run("immutable_snapshots_permissions_and_conflicts", func(t *testing.T) {
		if w := testRequest(p, "GET", "/admin/versions/android/history", nil, ""); w.Code != 401 {
			t.Fatal(w.Code)
		}
		if w := testRequest(p, "PUT", "/admin/versions/android", body(0, "1.0.12"), "viewer"); w.Code != 403 {
			t.Fatal(w.Code)
		}
		for i, latest := range []string{"1.0.12", "1.0.14"} {
			if w := testRequest(p, "PUT", "/admin/versions/android", body(i, latest), "operator"); w.Code != 200 {
				t.Fatalf("save: %d %s", w.Code, w.Body)
			}
		}
		if w := testRequest(p, "PUT", "/admin/versions/android", body(1, "1.0.15"), "operator"); w.Code != 409 {
			t.Fatal(w.Code)
		}
		rows := read("android", "", "viewer")
		if len(rows) != 2 || rows[0].Version != 2 || rows[1].Version != 1 || !strings.Contains(string(rows[1].Policy), "1.0.12") || rows[0].Actor != "test" || rows[0].Reason != "发布测试策略" || rows[0].Source != "publish" {
			t.Fatalf("history: %+v", rows)
		}
	})
	t.Run("audit_failure_rolls_back_policy_and_snapshot", func(t *testing.T) {
		exec(`CREATE FUNCTION fail_version_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='version.save' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_version_audit BEFORE INSERT ON lp_audit FOR EACH ROW EXECUTE FUNCTION fail_version_audit()`)
		if w := testRequest(p, "PUT", "/admin/versions/android", body(2, "1.0.15"), "operator"); w.Code == 200 {
			t.Fatal("failed audit accepted")
		}
		var version int
		var policy string
		if err := db.QueryRow(ctx, `SELECT version,policy::text FROM lp_versions WHERE platform='android'`).Scan(&version, &policy); err != nil {
			t.Fatal(err)
		}
		if version != 2 || !strings.Contains(policy, "1.0.14") || len(read("android", "", "viewer")) != 2 {
			t.Fatal("partial commit")
		}
		exec(`DROP TRIGGER fail_version_audit ON lp_audit; DROP FUNCTION fail_version_audit()`)
	})
	t.Run("pagination_and_input_validation", func(t *testing.T) {
		exec(`INSERT INTO lp_version_history(platform,version,policy,actor,reason) SELECT 'android',n,'{"latestVersion":"1.0.14","minimumVersion":"1.0.10"}', 'test','pagination fixture' FROM generate_series(3,23) n`)
		first := read("android", "?page=1", "viewer")
		second := read("android", "?page=2", "viewer")
		if len(first) != 20 || first[0].Version != 23 || len(second) != 3 || second[0].Version != 3 {
			t.Fatal("pagination order")
		}
		for _, path := range []string{"android/history?page=0", "android/history?page=abc", "android/history?page=100001", "invalid/history"} {
			if w := testRequest(p, "GET", "/admin/versions/"+path, nil, "viewer"); w.Code != 400 {
				t.Fatal(w.Code)
			}
		}
		exec(schema)
		if len(read("android", "?page=2", "viewer")) != 3 {
			t.Fatal("migration duplicated history")
		}
	})
}
