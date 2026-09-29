package directorybackup

import (
	"context"
	"encoding/json"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

// Stage restores into an EMPTY independent database only. The persistent guard
// is committed BEFORE invoking pg_restore. Failure, cancellation, or process
// death leaves it in place. No code here can remove it or enable login/jobs.
func (d Database) Stage(ctx context.Context, r Request, path string, key []byte) error {
	reader, e := openArchive(path, r, key)
	if e != nil {
		return e
	}
	defer reader.Close()
	proof := reader.ManifestProof()
	conn, e := d.connect(ctx)
	if e != nil {
		return e
	}
	defer conn.Close(context.Background())
	var locked bool
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, tenancy.PlatformMigrationLock).Scan(&locked); e != nil || !locked {
		return ErrUnconfirmed
	}
	ready, e := quarantine(ctx, conn, r, proof)
	if e != nil {
		return e
	}
	if ready {
		return checkRestored(ctx, conn, r)
	}
	e = watched(ctx, conn, func(work context.Context) error {
		input, output := io.Pipe()
		finished := make(chan error, 1)
		go func() { e := reader.Read("database", output); _ = output.CloseWithError(e); finished <- e }()
		e := d.run(work, true, input, io.Discard, "--exit-on-error", "--single-transaction", "--no-owner", "--no-acl", "--schema=public", "--dbname="+databaseName(d.DSN))
		_ = input.CloseWithError(e)
		if <-finished != nil || e != nil {
			return ErrUnconfirmed
		}
		return nil
	})
	if e != nil || checkDirectory(ctx, conn, r.Expected, true) != nil {
		return ErrUnconfirmed
	}
	tx, e := conn.Begin(ctx)
	if e != nil {
		return ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	// Preserve accounts, bans and assignment history; never derive authority
	// from this stale snapshot. Enterprise reconciliation is a separate gate.
	_, e = tx.Exec(ctx, `UPDATE public.platform_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp());
UPDATE public.platform_admin_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp());
UPDATE public.platform_tickets SET consumed_at=COALESCE(consumed_at,clock_timestamp());
UPDATE public.platform_password_recovery SET consumed_at=COALESCE(consumed_at,clock_timestamp());
UPDATE public.platform_jobs SET poll_hash=NULL;
UPDATE public.platform_push_devices SET revoked_at=COALESCE(revoked_at,clock_timestamp());
UPDATE public.platform_push_deliveries SET status='invalid' WHERE status='pending';
UPDATE public.platform_backup_schedules SET enabled=false;
UPDATE frogim_recovery.guard SET phase='staged',updated_at=clock_timestamp() WHERE singleton`)
	if e != nil {
		return ErrUnconfirmed
	}
	if r.Expected.SchemaVersion >= 18 {
		// A restored daily worker must also remain disabled; it must not create
		// a plausible "new" backup from an unreconciled historical directory.
		if _, e = tx.Exec(ctx, `UPDATE public.platform_directory_schedule SET enabled=false`); e != nil {
			return ErrUnconfirmed
		}
	}
	metadata, _ := json.Marshal(struct {
		Binding  backup.Binding `json:"binding"`
		Manifest backup.File    `json:"manifest"`
	}{r.Expected, proof})
	_, e = tx.Exec(ctx, `INSERT INTO public.platform_audits(actor_id,action,job_id,reason,metadata) VALUES($1,'platform.recovery.staged',$2,$3,$4)`, r.Actor, r.Expected.BackupID, r.Reason, metadata)
	if e != nil || tx.Commit(ctx) != nil {
		return ErrUnconfirmed
	}
	return checkRestored(ctx, conn, r)
}

// Database name is a plain libpq parameter, never a connection string. It is
// taken from the already validated URL and must not contain '=' or whitespace.
func databaseName(dsn string) string {
	c, _, e := connectionConfig(dsn)
	if e != nil {
		return ""
	}
	return c.Database
}

func quarantine(ctx context.Context, conn *pgx.Conn, r Request, proof backup.File) (bool, error) {
	var exists bool
	e := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, tenancy.PlatformRecoverySchema).Scan(&exists)
	if e != nil {
		return false, ErrUnconfirmed
	}
	binding, _ := json.Marshal(r.Expected)
	if exists {
		var same bool
		var phase string
		e = conn.QueryRow(ctx, `SELECT binding=$1::jsonb AND manifest_sha256=$2 AND manifest_size=$3 AND actor_id=$4 AND reason=$5,phase FROM frogim_recovery.guard WHERE singleton`, binding, proof.SHA256, proof.Size, r.Actor, r.Reason).Scan(&same, &phase)
		if e != nil || !same || phase != "staged" {
			return false, ErrQuarantined
		}
		return true, nil
	}
	// Empty means no user tables, views, sequences, functions, extensions or
	// alternate schemas. The source/any existing application DB cannot pass.
	var dirty bool
	e = conn.QueryRow(ctx, `SELECT
EXISTS(SELECT 1 FROM pg_namespace WHERE nspname NOT IN ('public','information_schema') AND nspname NOT LIKE 'pg_%') OR
EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public') OR
EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public') OR
EXISTS(SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='public') OR
EXISTS(SELECT 1 FROM pg_largeobject_metadata) OR
EXISTS(SELECT 1 FROM pg_extension WHERE extname<>'plpgsql') OR
EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend' AND pid<>pg_backend_pid())`).Scan(&dirty)
	if e != nil || dirty {
		return false, ErrInvalid
	}
	tx, e := conn.Begin(ctx)
	if e != nil {
		return false, ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `CREATE SCHEMA frogim_recovery;
REVOKE ALL ON SCHEMA frogim_recovery FROM PUBLIC;
CREATE TABLE frogim_recovery.guard (
 singleton boolean PRIMARY KEY CHECK(singleton), binding jsonb NOT NULL,
 manifest_sha256 text NOT NULL,manifest_size bigint NOT NULL,
 actor_id text NOT NULL,reason text NOT NULL,
 phase text NOT NULL CHECK(phase IN ('restoring','staged')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`)
	if e != nil {
		return false, ErrUnconfirmed
	}
	_, e = tx.Exec(ctx, `INSERT INTO frogim_recovery.guard(singleton,binding,manifest_sha256,manifest_size,actor_id,reason,phase) VALUES(true,$1,$2,$3,$4,$5,'restoring')`, binding, proof.SHA256, proof.Size, r.Actor, r.Reason)
	if e != nil || tx.Commit(ctx) != nil {
		return false, ErrUnconfirmed
	}
	return false, nil
}
func checkRestored(ctx context.Context, conn *pgx.Conn, r Request) error {
	if checkDirectory(ctx, conn, r.Expected, true) != nil {
		return ErrUnconfirmed
	}
	var unsafe bool
	e := conn.QueryRow(ctx, `SELECT
EXISTS(SELECT 1 FROM public.platform_sessions WHERE revoked_at IS NULL) OR
EXISTS(SELECT 1 FROM public.platform_admin_sessions WHERE revoked_at IS NULL) OR
EXISTS(SELECT 1 FROM public.platform_tickets WHERE consumed_at IS NULL) OR
EXISTS(SELECT 1 FROM public.platform_password_recovery WHERE consumed_at IS NULL) OR
EXISTS(SELECT 1 FROM public.platform_push_devices WHERE revoked_at IS NULL) OR
EXISTS(SELECT 1 FROM public.platform_push_deliveries WHERE status='pending') OR
EXISTS(SELECT 1 FROM public.platform_backup_schedules WHERE enabled)`).Scan(&unsafe)
	if e != nil || unsafe {
		return ErrUnconfirmed
	}
	if r.Expected.SchemaVersion >= 18 {
		if e = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.platform_directory_schedule WHERE enabled)`).Scan(&unsafe); e != nil || unsafe {
			return ErrUnconfirmed
		}
	}
	return nil
}
