package directorybackup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
)

// Run in the explicit PostgreSQL-17 helper fixture. Creates NEW databases on
// the disposable test server, never restores over the source or test root DB.
func integrationDatabase(t *testing.T) Database {
	t.Helper()
	if os.Getenv("TENANCY_DIRECTORY_BACKUP_TEST") != "local" {
		t.Skip("explicit isolated PostgreSQL/native tools fixture not enabled")
	}
	dsn := os.Getenv("TENANCY_DIRECTORY_BACKUP_DATABASE_URL")
	u, e := url.Parse(dsn)
	if e != nil || u.User == nil || u.User.Username() != "tenancy_test" || u.Path != "/tenancy_test" {
		t.Fatal("requires disposable tenancy_test server")
	}
	if u.Hostname() != "platform-db" && u.Hostname() != "127.0.0.1" {
		t.Fatal("test host must be explicit local dependency")
	}
	password, _ := u.User.Password()
	if password != "local-disposable-tests-only" {
		t.Fatal("unexpected test credentials")
	}
	conn, e := pgx.Connect(t.Context(), dsn)
	if e != nil {
		t.Fatal("test server unavailable")
	}
	defer conn.Close(context.Background())
	var suffix [10]byte
	if _, e := rand.Read(suffix[:]); e != nil {
		t.Fatal(e)
	}
	name := "directory_backup_test_" + hex.EncodeToString(suffix[:])
	if _, e := conn.Exec(t.Context(), `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); e != nil {
		t.Fatal("new database", e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cleanup, e := pgx.Connect(ctx, dsn)
		if e != nil {
			t.Error("test cleanup connection failed")
			return
		}
		defer cleanup.Close(ctx)
		if !strings.HasPrefix(name, "directory_backup_test_") || len(name) != 42 {
			t.Fatal("unsafe cleanup target")
		}
		if _, e := cleanup.Exec(ctx, `DROP DATABASE `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`); e != nil {
			t.Error("test database cleanup failed", e)
		}
	})
	u.Path = "/" + name
	return Database{DSN: u.String(), Tools: Tools{Dump: os.Getenv("TENANCY_PG_DUMP"), Restore: os.Getenv("TENANCY_PG_RESTORE")}}
}

func TestDirectoryBackupPostgresPreviousSchemaRestore(t *testing.T) {
	d := integrationDatabase(t)
	seedDirectory(t, d)
	conn, e := d.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	// Model the previously shipped platform schema in this disposable database
	// only. No live source or default enterprise is used by this fixture.
	_, e = conn.Exec(t.Context(), `DROP TABLE platform_directory_schedule_operations; DROP TABLE platform_directory_daily_runs; DROP TABLE platform_directory_schedule; DELETE FROM platform_schema_migrations WHERE version>=18`)
	conn.Close(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	id, e := d.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r, b, key := fixture()
	r.Expected.DirectoryID = id.DirectoryID
	r.Expected.SchemaVersion = 17
	b.Release, _ = json.Marshal(deployment.PlatformRelease{ID: r.Expected.ReleaseID, Runtime: "linux/amd64", SchemaVersion: 17, ComposeSHA256: r.Expected.ReleaseDigest})
	path := filepath.Join(t.TempDir(), "old-schema")
	if _, e = d.Capture(t.Context(), r, b, path, key); e != nil {
		t.Fatal("old capture", e)
	}
	target := integrationDatabase(t)
	if e = target.Stage(t.Context(), r, path, key); e != nil {
		t.Fatal("old stage", e)
	}
	if _, e = target.Inspect(t.Context()); !errors.Is(e, ErrQuarantined) {
		t.Fatal("old restore not quarantined")
	}
}

func seedDirectory(t *testing.T, d Database) {
	t.Helper()
	s, e := platform.Open(t.Context(), d.DSN)
	if e != nil {
		t.Fatal("migrate", e)
	}
	s.Close()
	conn, e := d.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	_, e = conn.Exec(t.Context(), `INSERT INTO platform_tenants(id,display_name,http_base_url,status,is_default) VALUES('default','fixture','https://fixture.example.test','active',true);
INSERT INTO platform_accounts(id,phone,password_hash,state,tenant_id,local_user_id) VALUES('account-one','19911112222','fixture-hash-not-a-real-password','active','default','user-one');
INSERT INTO platform_sessions(token_hash,account_id,assignment_version,auth_version,expires_at) VALUES('\x01','account-one',1,1,now()+interval '1 day');
INSERT INTO platform_tickets(ticket_hash,session_hash,account_id,tenant_id,local_user_id,assignment_version,expires_at) VALUES('\x02','\x01','account-one','default','user-one',1,now()+interval '60 seconds');
INSERT INTO platform_admin_accounts(id,username,password_hash,role) VALUES('operator','operator','test-hash','operator');
INSERT INTO platform_admin_sessions(token_hash,admin_id,auth_version,expires_at) VALUES('\x03','operator',1,now()+interval '1 day');
INSERT INTO platform_password_recovery(id,capability_hash,phone,account_id,code_hash,expires_at) VALUES('recovery','\x04','19911112222','account-one','\x05',now()+interval '1 hour');
INSERT INTO platform_push_devices(id,provider,token_hash,token_cipher,device_id,platform,account_id,tenant_id,local_user_id,assignment_version,auth_version,realm_version,session_hash,notifications_enabled,preview_enabled,sound_enabled,vibration_enabled,credential_hash)
VALUES('device','getui','\x06','\x07','device','android','account-one','default','user-one',1,1,1,'\x01',true,true,true,true,'\x08');
INSERT INTO platform_push_requests(tenant_id,request_id,account_id,input_hash,expires_at) VALUES('default','request','account-one','\x09',now()+interval '1 hour');
INSERT INTO platform_push_deliveries(request_id,device_id,binding_revision) SELECT id,'device',1 FROM platform_push_requests;
INSERT INTO platform_backup_schedules(tenant_id,enabled,start_minute_utc,window_minutes,version,actor_id,reason) VALUES('default',true,10,30,1,'operator','fixture');
INSERT INTO platform_jobs(id,kind,account_id,target_tenant_id,target_local_user_id,target_version,step,actor_id,reason) VALUES('pending-job','registration','account-one','default','user-one',1,'activate','operator','fixture');`)
	if e != nil {
		t.Fatal("seed", e)
	}
}

func TestDirectoryBackupPostgresRoundTripQuarantine(t *testing.T) {
	d := integrationDatabase(t)
	seedDirectory(t, d)
	r, b, key := fixture()
	identity, e := d.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r.Expected.DirectoryID = identity.DirectoryID
	// Repeated migration preserves the directory identity.
	s, e := platform.Open(t.Context(), d.DSN)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	again, e := d.Inspect(t.Context())
	if e != nil || again != identity {
		t.Fatal("migration changed identity")
	}
	path := filepath.Join(t.TempDir(), "archive")
	proof, e := d.Capture(t.Context(), r, b, path, key)
	if e != nil {
		t.Fatal("capture", e)
	}
	repeated, e := d.Capture(t.Context(), r, b, path, key)
	if e != nil || repeated != proof {
		t.Fatal("capture idempotency", e)
	}
	conn, e := d.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	// Same request cannot silently reserve a different output, even when its
	// previous attempt was incomplete or its database acknowledgment was lost.
	if _, e = d.Capture(t.Context(), r, b, filepath.Join(t.TempDir(), "elsewhere"), key); e == nil {
		t.Fatal("request changed archive path")
	}
	var count int
	if e = conn.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action LIKE 'platform.directory_backup.%'`).Scan(&count); e != nil || count != 2 {
		t.Fatal("duplicate audit")
	}
	if e = conn.QueryRow(t.Context(), `SELECT count(*) FROM platform_sessions WHERE revoked_at IS NULL`).Scan(&count); e != nil || count != 1 {
		t.Fatal("source modified")
	}
	// Finalized ciphertext survived but the completion transaction did not.
	if _, e = conn.Exec(t.Context(), `UPDATE platform_directory_backups SET state='pending',manifest_sha256='',manifest_size=0,completed_at=NULL;
DELETE FROM platform_audits WHERE action='platform.directory_backup.completed'`); e != nil {
		t.Fatal(e)
	}
	if recovered, e := d.Capture(t.Context(), r, b, path, key); e != nil || recovered != proof {
		t.Fatal("lost completion acknowledgment", e)
	}
	if e = conn.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action LIKE 'platform.directory_backup.%'`).Scan(&count); e != nil || count != 2 {
		t.Fatal("ack recovery duplicated audit")
	}
	// A post-backup block is not in the stale snapshot. Quarantine is mandatory
	// regardless of whether the restored account appears active.
	if _, e = conn.Exec(t.Context(), `UPDATE platform_accounts SET globally_blocked=true`); e != nil {
		t.Fatal(e)
	}
	if e = d.Stage(t.Context(), r, path, key); e == nil {
		t.Fatal("restored over source")
	}
	target := integrationDatabase(t)
	if e = target.Stage(t.Context(), r, path, key); e != nil {
		t.Fatal("stage", e)
	}
	if e = target.Stage(t.Context(), r, path, key); e != nil {
		t.Fatal("stage retry", e)
	}
	if _, e = target.Inspect(t.Context()); !errors.Is(e, ErrQuarantined) {
		t.Fatal("target not quarantined")
	}
	if resumed, e := platform.Open(t.Context(), target.DSN); e == nil {
		resumed.Close()
		t.Fatal("restored service started")
	}
	restored, e := target.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close(context.Background())
	if e = restored.QueryRow(t.Context(), `SELECT count(*) FROM platform_accounts`).Scan(&count); e != nil || count != 1 {
		t.Fatal("accounts lost")
	}
	if e = restored.QueryRow(t.Context(), `SELECT count(*) FROM platform_jobs WHERE step='activate'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("pending lifecycle lost")
	}
	if e = restored.QueryRow(t.Context(), `SELECT count(*) FROM platform_audits WHERE action='platform.recovery.staged'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("restore audit/idempotency")
	}
	if e = checkRestored(t.Context(), restored, r); e != nil {
		t.Fatal("live credentials survived", e)
	}
	changed := r
	changed.Expected.BackupID = "other"
	if e = target.Stage(t.Context(), changed, path, key); e == nil {
		t.Fatal("wrong archive accepted")
	}
	// No plaintext auth material appears in the archive files.
	for _, name := range []string{"database", "compose", "release", "manifest"} {
		data, e := os.ReadFile(filepath.Join(path, name+".sealed"))
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(data), "19911112222") || strings.Contains(string(data), "fixture-hash-not-a-real-password") {
			t.Fatal("plaintext archive")
		}
	}
}

func TestDirectoryBackupPostgresRejectsForeignAndPartialArchives(t *testing.T) {
	d := integrationDatabase(t)
	seedDirectory(t, d)
	r, b, key := fixture()
	identity, e := d.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "archive")
	if _, e = d.Capture(t.Context(), r, b, path, key); e == nil {
		t.Fatal("foreign directory accepted")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("foreign backup left artifact")
	}
	r.Expected.DirectoryID = identity.DirectoryID
	broken := d
	broken.Tools.Dump = "/missing-pg-dump"
	if _, e = broken.Capture(t.Context(), r, b, path, key); e == nil {
		t.Fatal("failed dump finalized")
	}
	if _, e = Verify(path, r, key); e == nil {
		t.Fatal("partial archive authenticated")
	}
	if _, e = d.Capture(t.Context(), r, b, path, key); e == nil {
		t.Fatal("partial archive overwritten")
	}
	if _, e = d.Capture(t.Context(), r, b, filepath.Join(t.TempDir(), "other"), key); e == nil {
		t.Fatal("partial request changed output")
	}
	r.Expected.BackupID = "backup-new-attempt"
	path = filepath.Join(t.TempDir(), "retry")
	if _, e = d.Capture(t.Context(), r, b, path, key); e != nil {
		t.Fatal("explicit new attempt failed", e)
	}
	data, e := os.ReadFile(filepath.Join(path, "database.sealed"))
	if e != nil {
		t.Fatal(e)
	}
	data[len(data)-1] ^= 1
	if e = os.WriteFile(filepath.Join(path, "database.sealed"), data, 0600); e != nil {
		t.Fatal(e)
	}
	target := integrationDatabase(t)
	if e = target.Stage(t.Context(), r, path, key); e == nil {
		t.Fatal("tampered archive restored")
	}
	conn, e := target.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	var exists bool
	if e = conn.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='frogim_recovery')`).Scan(&exists); e != nil || exists {
		t.Fatal("tampered archive mutated target")
	}
}

func TestDirectoryBackupPostgresSourceLockAndFutureSchema(t *testing.T) {
	d := integrationDatabase(t)
	seedDirectory(t, d)
	r, b, key := fixture()
	identity, e := d.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r.Expected.DirectoryID = identity.DirectoryID
	conn, e := d.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	if _, e = conn.Exec(t.Context(), `SELECT pg_advisory_lock($1)`, directoryBackupLock); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "archive")
	if _, e = d.Capture(t.Context(), r, b, path, key); e == nil {
		t.Fatal("concurrent backup bypassed lock")
	}
	if _, e = conn.Exec(t.Context(), `SELECT pg_advisory_unlock($1)`, directoryBackupLock); e != nil {
		t.Fatal(e)
	}
	if _, e = conn.Exec(t.Context(), `CREATE SCHEMA unrelated`); e != nil {
		t.Fatal(e)
	}
	if _, e = d.Capture(t.Context(), r, b, path, key); e == nil {
		t.Fatal("silently omitted foreign schema")
	}
	if _, e = conn.Exec(t.Context(), `INSERT INTO platform_schema_migrations(version) VALUES($1)`, tenancy.PlatformSchemaVersion+1); e != nil {
		t.Fatal(e)
	}
	if older, e := platform.Open(t.Context(), d.DSN); e == nil {
		older.Close()
		t.Fatal("older binary accepted newer schema")
	}
}

func TestDirectoryBackupPostgresInterruptedRestoreCannotBoot(t *testing.T) {
	d := integrationDatabase(t)
	seedDirectory(t, d)
	r, b, key := fixture()
	identity, e := d.Inspect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r.Expected.DirectoryID = identity.DirectoryID
	path := filepath.Join(t.TempDir(), "archive")
	if _, e = d.Capture(t.Context(), r, b, path, key); e != nil {
		t.Fatal(e)
	}
	target := integrationDatabase(t)
	target.Tools.Restore = "/does-not-exist"
	if e = target.Stage(t.Context(), r, path, key); e == nil {
		t.Fatal("missing restore tool succeeded")
	}
	if resumed, e := platform.Open(t.Context(), target.DSN); e == nil {
		resumed.Close()
		t.Fatal("partial restore booted")
	}
	target.Tools.Restore = d.Tools.Restore
	if e = target.Stage(t.Context(), r, path, key); !errors.Is(e, ErrQuarantined) {
		t.Fatal("ambiguous restore silently repeated", e)
	}
	conn, e := target.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(context.Background())
	var phase string
	if e = conn.QueryRow(t.Context(), `SELECT phase FROM frogim_recovery.guard`).Scan(&phase); e != nil || phase != "restoring" {
		t.Fatal("lost quarantine")
	}
}

func TestDirectoryBackupPostgresLostLockCancelsWork(t *testing.T) {
	d := integrationDatabase(t)
	seedDirectory(t, d)
	lock, e := d.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close(context.Background())
	other, e := d.connect(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close(context.Background())
	if _, e = lock.Exec(t.Context(), `SELECT pg_advisory_lock($1)`, tenancy.PlatformMigrationLock); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	e = watched(ctx, lock, func(work context.Context) error {
		if _, e := other.Exec(t.Context(), `SELECT pg_terminate_backend($1)`, int64(lock.PgConn().PID())); e != nil {
			t.Fatal(e)
		}
		<-work.Done()
		return nil
	})
	if !errors.Is(e, ErrUnconfirmed) || ctx.Err() != nil {
		t.Fatal("lost lock not detected promptly", e)
	}
}
