package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/legacyimport"
)

func commandTestDB(t *testing.T, key string) (string, *pgx.Conn) {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		t.Skip("disposable PostgreSQL not configured")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "postgres" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Path != "/tenancy_test" || u.RawQuery != "sslmode=disable" {
		t.Fatal("command test requires disposable loopback tenancy_test")
	}
	ctx := context.Background()
	c, e := pgx.Connect(ctx, raw)
	if e != nil {
		t.Fatal("test database unavailable")
	}
	var id [12]byte
	if _, e = rand.Read(id[:]); e != nil {
		t.Fatal(e)
	}
	schema := "preflight_cmd_" + hex.EncodeToString(id[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, e = c.Exec(ctx, "CREATE SCHEMA "+quoted); e != nil {
		c.Close(ctx)
		t.Fatal("fixture schema failed")
	}
	t.Cleanup(func() {
		defer c.Close(context.Background())
		if _, e := c.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); e != nil {
			t.Error("fixture cleanup failed")
		}
	})
	if _, e = c.Exec(ctx, "SET search_path TO "+quoted); e != nil {
		t.Fatal("fixture schema scope failed")
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), c
}

func TestPostgresPreflightCommandReportAndExitCodes(t *testing.T) {
	sourceURL, s := commandTestDB(t, "IM_TEST_DATABASE_URL")
	targetURL, p := commandTestDB(t, "PLATFORM_TEST_DATABASE_URL")
	ctx := context.Background()
	if _, e := s.Exec(ctx, `CREATE TABLE im_schema_migrations(version integer PRIMARY KEY);INSERT INTO im_schema_migrations VALUES(72);
CREATE TABLE im_users(id text PRIMARY KEY,phone text NOT NULL,password_hash text NOT NULL DEFAULT '',banned bool NOT NULL DEFAULT false,banned_until timestamptz,deleted_at timestamptz);
INSERT INTO im_users(id,phone) VALUES('command-user','19900000777');`); e != nil {
		t.Fatal("source fixture failed")
	}
	if _, e := p.Exec(ctx, `CREATE TABLE platform_schema_migrations(version integer PRIMARY KEY);INSERT INTO platform_schema_migrations VALUES(12);
CREATE TABLE platform_tenants(id text PRIMARY KEY,is_default bool,status text);INSERT INTO platform_tenants VALUES('default',true,'provisioning');
CREATE TABLE platform_accounts(id text PRIMARY KEY,phone text,tenant_id text,local_user_id text,state text,assignment_version bigint,auth_version bigint);`); e != nil {
		t.Fatal("target fixture failed")
	}
	env := func(key string) string {
		switch key {
		case "TENANCY_PREFLIGHT_ENV":
			return "development"
		case "TENANCY_PREFLIGHT_SOURCE_DATABASE_URL":
			return sourceURL
		case "TENANCY_PREFLIGHT_PLATFORM_DATABASE_URL":
			return targetURL
		}
		return ""
	}
	var out, diagnostics bytes.Buffer
	path := filepath.Join(t.TempDir(), "report.json")
	if code := run(ctx, []string{"-report", path}, env, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatal("clean command failed")
	}
	var summary map[string]any
	if json.Unmarshal([]byte(strings.SplitN(out.String(), "\n", 2)[0]), &summary) != nil || summary["readOnly"] != true || summary["dataChecksPassed"] != true {
		t.Fatal("command summary invalid")
	}
	for _, v := range []string{sourceURL, targetURL, "command-user", "19900000777", "local-disposable-tests-only"} {
		if strings.Contains(out.String(), v) || strings.Contains(diagnostics.String(), v) {
			t.Fatal("command exposed private data")
		}
	}
	saved, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var report legacyimport.Report
	if json.Unmarshal(saved, &report) != nil || report.Counts.Candidates != 1 || !report.RequiresCutoverValidation {
		t.Fatal("private report invalid")
	}
	out.Reset()
	diagnostics.Reset()
	if code := run(ctx, []string{"-report", path}, env, &out, &diagnostics); code != 2 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "PREFLIGHT_REPORT_NOT_SAVED") {
		t.Fatal("report replacement accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(saved, after) {
		t.Fatal("existing report changed")
	}
	if _, e = s.Exec(ctx, `INSERT INTO im_users(id,phone) VALUES('collision-user','+8619900000777')`); e != nil {
		t.Fatal("collision setup failed")
	}
	out.Reset()
	diagnostics.Reset()
	if code := run(ctx, nil, env, &out, &diagnostics); code != 1 || diagnostics.Len() != 0 || !strings.Contains(out.String(), `"dataChecksPassed":false`) {
		t.Fatal("blocking data did not return failure status")
	}
	var count int
	if p.QueryRow(ctx, `SELECT count(*) FROM platform_accounts`).Scan(&count) != nil || count != 0 {
		t.Fatal("command imported accounts")
	}
	if s.QueryRow(ctx, `SELECT max(version) FROM im_schema_migrations`).Scan(&count) != nil || count != 72 {
		t.Fatal("command migrated source")
	}
}
