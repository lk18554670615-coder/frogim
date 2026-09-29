package legacyimport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/store"
)

// Only the explicitly configured, disposable local database can be mutated by
// test setup. Preflight itself always connects through its read-only pool.
func isolatedDB(t *testing.T, env string) (string, *pgxpool.Pool) {
	t.Helper()
	raw := os.Getenv(env)
	if raw == "" {
		t.Skip("disposable PostgreSQL not configured")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "postgres" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Path != "/tenancy_test" || u.RawQuery != "sslmode=disable" {
		t.Fatal("integration setup requires explicit loopback tenancy_test database")
	}
	ctx := context.Background()
	admin, e := pgx.Connect(ctx, raw)
	if e != nil {
		t.Fatal("cannot connect disposable test database")
	}
	var random [12]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	schema := "preflight_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+quoted); e != nil {
		admin.Close(ctx)
		t.Fatal("create fixture schema failed")
	}
	t.Cleanup(func() {
		defer admin.Close(context.Background())
		if _, e := admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); e != nil {
			t.Error("fixture schema cleanup failed")
		}
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	pool, e := pgxpool.New(ctx, u.String())
	if e != nil {
		t.Fatal("fixture pool failed")
	}
	t.Cleanup(pool.Close)
	return u.String(), pool
}

type pgFixture struct {
	sourceURL, targetURL string
	source, target       *pgxpool.Pool
}

func setupPG(t *testing.T, legacy bool) pgFixture {
	t.Helper()
	sourceURL, source := isolatedDB(t, "IM_TEST_DATABASE_URL")
	targetURL, target := isolatedDB(t, "PLATFORM_TEST_DATABASE_URL")
	ctx := context.Background()
	if legacy {
		execFixture(t, source, `CREATE TABLE im_schema_migrations(version integer PRIMARY KEY);
INSERT INTO im_schema_migrations VALUES(72);
CREATE TABLE im_users(id text PRIMARY KEY,phone text NOT NULL UNIQUE,password_hash text NOT NULL DEFAULT '',banned boolean NOT NULL DEFAULT false,banned_until timestamptz,deleted_at timestamptz);`)
	} else {
		s, err := store.NewPostgresWithOptions(ctx, sourceURL, store.PostgresOptions{TenantID: "default"})
		if err != nil {
			t.Fatal("enterprise migration fixture failed")
		}
		s.Close()
	}
	p, err := platform.Open(ctx, targetURL)
	if err != nil {
		t.Fatal("platform migration fixture failed")
	}
	t.Cleanup(p.Close)
	if p.PutTenant(ctx, "default", "Default fixture", "https://default.example", "fixture", "isolated test setup", true) != nil {
		t.Fatal("default tenant fixture failed")
	}
	return pgFixture{sourceURL, targetURL, source, target}
}
func execFixture(t *testing.T, p *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := p.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal("fixture SQL failed", sql)
	}
}
func (f pgFixture) check(t *testing.T, tenant string) (Report, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := OpenLocalReadOnly(ctx, f.sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := OpenLocalReadOnly(ctx, f.targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	return Preflight(ctx, s, d, tenant, testTime)
}
func tableSnapshot(t *testing.T, p *pgxpool.Pool, table string) string {
	t.Helper()
	var data string
	if p.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb)::text FROM `+pgx.Identifier{table}.Sanitize()+` r`).Scan(&data) != nil {
		t.Fatal("snapshot fixture failed")
	}
	return data
}

func TestPostgresPreflightLegacyIsReadOnlyAndDoesNotAdopt(t *testing.T) {
	f := setupPG(t, true)
	u := candidate(t)
	execFixture(t, f.source, `INSERT INTO im_users(id,phone,password_hash) VALUES($1,$2,$3)`, u.id, "+86"+u.phone, u.passwordHash)
	execFixture(t, f.source, `INSERT INTO im_users(id,phone,password_hash,deleted_at) VALUES('deleted-fixture','deleted-phone','invalid-secret',now())`)
	beforeSource, beforeMigrations := tableSnapshot(t, f.source, "im_users"), tableSnapshot(t, f.source, "im_schema_migrations")
	beforeTarget, beforeAudits := tableSnapshot(t, f.target, "platform_accounts"), tableSnapshot(t, f.target, "platform_audits")
	r, err := f.check(t, "default")
	if err != nil || !r.DataChecksPassed || r.SourceSchemaVersion != 72 || r.TargetSchemaVersion != platform.SchemaVersion || r.SourceBound || r.Counts.Candidates != 1 || r.Counts.DeletedUsers != 1 || !hasIssue(r, "SOURCE_ADOPTION_REQUIRED") {
		t.Fatal("legacy inventory failed")
	}
	r2, err := f.check(t, "default")
	if err != nil || r2.Fingerprint != r.Fingerprint {
		t.Fatal("read-only rerun changed inventory")
	}
	if beforeSource != tableSnapshot(t, f.source, "im_users") || beforeMigrations != tableSnapshot(t, f.source, "im_schema_migrations") || beforeTarget != tableSnapshot(t, f.target, "platform_accounts") || beforeAudits != tableSnapshot(t, f.target, "platform_audits") {
		t.Fatal("preflight mutated database")
	}
	var adopted bool
	if f.source.QueryRow(context.Background(), `SELECT to_regclass('im_tenant_identity') IS NOT NULL`).Scan(&adopted) != nil || adopted {
		t.Fatal("preflight adopted/migrated legacy database")
	}
	data, _ := json.Marshal(r)
	for _, secret := range []string{u.passwordHash, u.phone, "invalid-secret"} {
		if strings.Contains(string(data), secret) || strings.Contains(r.Summary(), secret) {
			t.Fatal("preflight leaked fixture authentication data")
		}
	}
	s, err := OpenLocalReadOnly(context.Background(), f.sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Exec(context.Background(), `UPDATE im_users SET password_hash='must-not-change'`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "25006" {
		t.Fatal("pool allowed an accidental write")
	}
	if beforeSource != tableSnapshot(t, f.source, "im_users") {
		t.Fatal("read-only protection failed")
	}
}

func TestPostgresPreflightActualSchemasDetectConflictsAndLinkedIdentities(t *testing.T) {
	f := setupPG(t, false)
	u := candidate(t)
	execFixture(t, f.source, `INSERT INTO im_users(id,phone,name,password_hash,created_at) VALUES($1,$2,'fixture',$3,now())`, u.id, u.phone, u.passwordHash)
	r, err := f.check(t, "default")
	if err != nil || !r.DataChecksPassed || r.SourceSchemaVersion != 79 || !r.SourceBound || r.Counts.ValidCandidates != 1 {
		t.Fatal("current schema inventory failed")
	}
	execFixture(t, f.source, `INSERT INTO im_users(id,phone,name,created_at) VALUES('collision',$1,'fixture',now())`, "+86"+u.phone)
	r, err = f.check(t, "default")
	if err != nil || r.DataChecksPassed || !hasIssue(r, "SOURCE_PHONE_COLLISION") || r.Counts.ValidCandidates != 0 {
		t.Fatal("normalization collision was not blocked")
	}
	execFixture(t, f.source, `UPDATE im_users SET deleted_at=now() WHERE id='collision'`)
	execFixture(t, f.target, `INSERT INTO platform_accounts(id,phone,password_hash,state,tenant_id,local_user_id) VALUES('mapped',$1,'','active','default',$2)`, u.phone, u.id)
	r, err = f.check(t, "default")
	if err != nil || r.DataChecksPassed || !hasIssue(r, "TARGET_PHONE_OCCUPIED") || !hasIssue(r, "TARGET_LOCAL_ID_OCCUPIED") {
		t.Fatal("unlinked occupied mapping accepted")
	}
	execFixture(t, f.source, `UPDATE im_users SET platform_account_id='mapped' WHERE id=$1`, u.id)
	r, err = f.check(t, "default")
	if err != nil || !r.DataChecksPassed || r.Counts.LinkedUsers != 1 || r.Counts.Candidates != 0 || r.Counts.Passwordless != 0 {
		t.Fatal("stable mapping rejected")
	}
	execFixture(t, f.target, `UPDATE platform_accounts SET auth_version=2 WHERE id='mapped'`)
	r, err = f.check(t, "default")
	if err != nil || r.DataChecksPassed || !hasIssue(r, "LINKED_IDENTITY_MISMATCH") {
		t.Fatal("auth generation mismatch accepted")
	}
}

func TestPostgresPreflightRealmSchemaAndTargetFailClosed(t *testing.T) {
	f := setupPG(t, true)
	before := tableSnapshot(t, f.target, "platform_accounts")
	if _, e := (pgFixture{sourceURL: f.targetURL, targetURL: f.sourceURL}).check(t, "default"); !errors.Is(e, ErrRealm) {
		t.Fatal("reversed realms accepted")
	}
	if _, e := f.check(t, "missing"); !errors.Is(e, ErrTarget) {
		t.Fatal("missing default tenant accepted")
	}
	execFixture(t, f.target, `UPDATE platform_tenants SET is_default=false WHERE id='default'`)
	if _, e := f.check(t, "default"); !errors.Is(e, ErrTarget) {
		t.Fatal("nondefault tenant accepted")
	}
	execFixture(t, f.target, `UPDATE platform_tenants SET is_default=true WHERE id='default'`)
	execFixture(t, f.source, `UPDATE im_schema_migrations SET version=80`)
	if _, e := f.check(t, "default"); !errors.Is(e, ErrSchema) {
		t.Fatal("future source schema accepted")
	}
	execFixture(t, f.source, `UPDATE im_schema_migrations SET version=72`)
	execFixture(t, f.target, `INSERT INTO platform_schema_migrations(version) VALUES($1)`, platform.SchemaVersion+1)
	if _, e := f.check(t, "default"); !errors.Is(e, ErrSchema) {
		t.Fatal("future target schema accepted")
	}
	execFixture(t, f.target, `DELETE FROM platform_schema_migrations WHERE version=$1`, platform.SchemaVersion+1)
	execFixture(t, f.source, `ALTER TABLE im_users RENAME COLUMN password_hash TO unsupported_hash`)
	if _, e := f.check(t, "default"); !errors.Is(e, ErrSchema) {
		t.Fatal("partial source schema accepted")
	}
	if before != tableSnapshot(t, f.target, "platform_accounts") {
		t.Fatal("error path mutated target")
	}
}

func TestPostgresPreflightDefaultStateAndSourceBinding(t *testing.T) {
	f := setupPG(t, false)
	execFixture(t, f.target, `UPDATE platform_tenants SET status='active' WHERE id='default'`)
	r, e := f.check(t, "default")
	if e != nil || !hasIssue(r, "TARGET_MAINTENANCE_REQUIRED") {
		t.Fatal("active target warning missing")
	}
	execFixture(t, f.source, `UPDATE im_tenant_identity SET tenant_id='other' WHERE singleton`)
	r, e = f.check(t, "default")
	if e != nil || r.DataChecksPassed || !hasIssue(r, "SOURCE_TENANT_MISMATCH") {
		t.Fatal("foreign enterprise accepted")
	}
}

func TestPostgresPreflightOversizedInventoryIsNotPartialSuccess(t *testing.T) {
	f := setupPG(t, true)
	execFixture(t, f.source, `INSERT INTO im_users(id,phone) SELECT 'fixture-'||n::text,(19000000000+n)::text FROM generate_series(1,$1::integer) n`, MaxRows+1)
	r, err := f.check(t, "default")
	if !errors.Is(err, ErrLimit) || r.DataChecksPassed || r.Fingerprint != "" {
		t.Fatal("partial oversized inventory accepted")
	}
	var n int
	if f.source.QueryRow(context.Background(), `SELECT count(*) FROM im_users`).Scan(&n) != nil || n != MaxRows+1 {
		t.Fatal("oversized source modified")
	}
}

func TestReadOnlyConnectionRejectsRemoteAndOverrideConfiguration(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:secret@remote.example/db", "postgres://user:secret@localhost/db", "host=127.0.0.1 dbname=db password=secret",
		"postgres://127.0.0.1/db?host=remote.example", "postgres://127.0.0.1/db?port=99", "postgres://127.0.0.1/db?service=remote",
		"postgres://127.0.0.1/db?passfile=private", "postgres://127.0.0.1/db#private", " postgres://127.0.0.1/db",
	} {
		pool, err := OpenLocalReadOnly(context.Background(), dsn)
		if pool != nil {
			pool.Close()
			t.Fatal("unsafe connection opened")
		}
		if !errors.Is(err, ErrConfig) {
			t.Fatal("unsafe configuration not rejected before connection")
		}
	}
}
