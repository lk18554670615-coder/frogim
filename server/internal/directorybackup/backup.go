package directorybackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/backup"
	"github.com/linli/im/server/internal/tenancy"
)

// Keep backup serialization separate from live authentication/recovery locks.
const directoryBackupLock int64 = 490740001

// The source has exactly one platform directory in public. Extra schemas or
// non-platform tables are rejected rather than silently omitted from a dump.
func checkDirectory(ctx context.Context, conn *pgx.Conn, expected backup.Binding, recovering bool) error {
	if !recovering {
		var hasGuard bool
		if conn.QueryRow(ctx, `SELECT to_regclass('frogim_recovery.guard') IS NOT NULL`).Scan(&hasGuard) != nil {
			return ErrInvalid
		}
		if hasGuard {
			var activated bool
			if conn.QueryRow(ctx, `SELECT phase='activated' AND EXISTS(SELECT 1 FROM frogim_recovery.activations WHERE id=activation_id AND state='activated') FROM frogim_recovery.guard WHERE singleton`).Scan(&activated) != nil || !activated {
				return ErrQuarantined
			}
			recovering = true // The historical guard is preserved, not dropped.
		}
	}
	var id string
	var version int
	var foreign bool
	e := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname NOT IN ('public','information_schema') AND nspname NOT LIKE 'pg_%' AND NOT ($1 AND nspname='frogim_recovery')) OR EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename NOT LIKE 'platform\_%' ESCAPE '\') OR EXISTS(SELECT 1 FROM pg_largeobject_metadata)`, recovering).Scan(&foreign)
	if e != nil || foreign {
		return ErrInvalid
	}
	e = conn.QueryRow(ctx, `SELECT directory_id::text,(SELECT MAX(version) FROM public.platform_schema_migrations) FROM public.platform_directory_identity WHERE singleton`).Scan(&id, &version)
	if e != nil || id != expected.DirectoryID || version != expected.SchemaVersion {
		return ErrInvalid
	}
	return nil
}

// Capture uses a PostgreSQL exported MVCC snapshot. Normal authentication can
// continue while the dump is made. A restored snapshot is NEVER itself proof
// that enterprise credentials or in-flight cross-host jobs are current.
func (d Database) Capture(ctx context.Context, r Request, bundle Bundle, path string, key []byte) (backup.File, error) {
	var empty backup.File
	if !r.valid() || bundle.validate(r.Expected, key) != nil || !filepath.IsAbs(path) {
		return empty, ErrInvalid
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		return empty, ErrInvalid
	}
	path = filepath.Join(parent, filepath.Base(path))
	if info, e := os.Lstat(path); e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return empty, ErrInvalid
	}
	conn, e := d.connect(ctx)
	if e != nil {
		return empty, e
	}
	defer conn.Close(context.Background())
	var locked bool
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, directoryBackupLock).Scan(&locked); e != nil || !locked {
		return empty, ErrUnconfirmed
	}
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock_shared($1)`, tenancy.PlatformRecoveryLock).Scan(&locked); e != nil || !locked {
		return empty, ErrUnconfirmed
	}
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock_shared($1)`, tenancy.PlatformMigrationLock).Scan(&locked); e != nil || !locked {
		return empty, ErrUnconfirmed
	}
	if checkDirectory(ctx, conn, r.Expected, false) != nil {
		return empty, ErrInvalid
	}
	if e = reserve(ctx, conn, r, path); e != nil {
		return empty, e
	}
	if _, e = os.Lstat(path); e == nil {
		// A crash may happen after authenticated finalization and before the DB
		// acknowledgment. Never overwrite it; recover only the matching proof.
		proof, err := Verify(path, r, key)
		if err != nil {
			return empty, ErrUnconfirmed
		}
		return proof, complete(ctx, conn, r, proof)
	} else if !errors.Is(e, os.ErrNotExist) {
		return empty, ErrInvalid
	}
	var completed bool
	if e = conn.QueryRow(ctx, `SELECT state='completed' FROM platform_directory_backups WHERE id=$1`, r.Expected.BackupID).Scan(&completed); e != nil || completed {
		return empty, ErrUnconfirmed
	}
	w, e := backup.Create(path, r.Expected, key)
	if e != nil {
		return empty, ErrInvalid
	}
	defer w.Close()
	tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return empty, ErrUnconfirmed
	}
	defer tx.Rollback(context.Background())
	var snapshot string
	if e = tx.QueryRow(ctx, `SELECT pg_export_snapshot()`).Scan(&snapshot); e != nil {
		return empty, ErrUnconfirmed
	}
	e = watched(ctx, conn, func(work context.Context) error {
		if e := w.Add("compose", func(out io.Writer) error { _, e := out.Write(bundle.Compose); return e }); e != nil {
			return e
		}
		if e := w.Add("release", func(out io.Writer) error { _, e := out.Write(bundle.Release); return e }); e != nil {
			return e
		}
		return w.Add("database", func(out io.Writer) error {
			return d.run(work, false, nil, out, "--format=custom", "--no-owner", "--no-acl", "--schema=public", "--strict-names", "--snapshot="+snapshot)
		})
	})
	if e != nil || tx.Commit(ctx) != nil || w.Finalize() != nil {
		return empty, ErrUnconfirmed
	}
	proof, e := Verify(path, r, key)
	if e != nil {
		return empty, e
	}
	return proof, complete(ctx, conn, r, proof)
}
func reserve(ctx context.Context, conn *pgx.Conn, r Request, path string) error {
	tx, e := conn.Begin(ctx)
	if e != nil {
		return ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	binding, _ := json.Marshal(r.Expected)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	hash := sha256.Sum256([]byte(path))
	pathHash := hex.EncodeToString(hash[:])
	inserted, e := tx.Exec(ctx, `INSERT INTO platform_directory_backups(id,directory_id,binding,actor_id,reason,archive_path_hash) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, r.Expected.BackupID, r.Expected.DirectoryID, binding, r.Actor, r.Reason, pathHash)
	if e != nil {
		return ErrUnconfirmed
	}
	var matches bool
	if e = tx.QueryRow(ctx, `SELECT binding=$2::jsonb AND actor_id=$3 AND reason=$4 AND archive_path_hash=$5 FROM platform_directory_backups WHERE id=$1 FOR UPDATE`, r.Expected.BackupID, binding, r.Actor, r.Reason, pathHash).Scan(&matches); e != nil || !matches {
		return ErrInvalid
	}
	if inserted.RowsAffected() == 1 {
		_, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,job_id,reason,metadata) VALUES($1,'platform.directory_backup.requested',$2,$3,$4)`, r.Actor, r.Expected.BackupID, r.Reason, binding)
		if e != nil {
			return ErrUnconfirmed
		}
	}
	if tx.Commit(ctx) != nil {
		return ErrUnconfirmed
	}
	return nil
}
func complete(ctx context.Context, conn *pgx.Conn, r Request, proof backup.File) error {
	tx, e := conn.Begin(ctx)
	if e != nil {
		return ErrUnconfirmed
	}
	defer tx.Rollback(ctx)
	var state, hash string
	var size int64
	e = tx.QueryRow(ctx, `SELECT state,manifest_sha256,manifest_size FROM platform_directory_backups WHERE id=$1 FOR UPDATE`, r.Expected.BackupID).Scan(&state, &hash, &size)
	if e != nil {
		return ErrUnconfirmed
	}
	if state == "completed" {
		if hash != proof.SHA256 || size != proof.Size {
			return ErrInvalid
		}
		return nil
	}
	_, e = tx.Exec(ctx, `UPDATE platform_directory_backups SET state='completed',manifest_sha256=$2,manifest_size=$3,completed_at=clock_timestamp() WHERE id=$1`, r.Expected.BackupID, proof.SHA256, proof.Size)
	if e != nil {
		return ErrUnconfirmed
	}
	metadata, _ := json.Marshal(proof)
	_, e = tx.Exec(ctx, `INSERT INTO platform_audits(actor_id,action,job_id,reason,metadata) VALUES($1,'platform.directory_backup.completed',$2,$3,$4)`, r.Actor, r.Expected.BackupID, r.Reason, metadata)
	if e != nil || tx.Commit(ctx) != nil {
		return ErrUnconfirmed
	}
	return nil
}
