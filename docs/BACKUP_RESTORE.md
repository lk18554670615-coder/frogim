# Backup and restore

## Scope and targets

`infra/scripts/backup.sh` captures a PostgreSQL custom-format dump and a preserved MinIO mirror, then writes checksums. Redis is treated as rebuildable cache/ephemeral state; acknowledged message correctness remains in PostgreSQL. WebRTC SDP/ICE is intentionally ephemeral and is neither present in Redis after delivery nor included in a backup/replay log.

Define business-approved targets before launch. A reasonable initial objective is RPO 15 minutes and RTO 60 minutes, but the schedule and infrastructure must be sized and tested against actual traffic.

## Create a backup

```bash
make backup PROD_ENV=.env.production
```

The script starts with `umask 077` and writes into `BACKUP_DIR/.incomplete-<UTC timestamp>/`. It creates the dump, mirrors objects, writes `SHA256SUMS`, recursively removes group/other permissions, and only then uses a same-filesystem rename to publish `BACKUP_DIR/<UTC timestamp>/`:

```text
postgres.dump
minio/
SHA256SUMS
```

Copy the directory to encrypted off-host storage with immutability/object lock. Keep daily, weekly and monthly generations according to policy. Monitor both the backup command and off-host copy.

A `.incomplete-*` directory is explicit evidence of an interrupted or failed backup, not a restore point. Monitoring must alert on it. Preserve it only while investigating, then remove it through an approved, path-checked cleanup; never copy or promote it as a successful generation. The rename is atomic only because working and destination directories are created under the same `BACKUP_DIR` filesystem.

## Restore drill

Test the database dump without touching production:

```bash
make restore-drill BACKUP=/absolute/path/to/postgres.dump
```

The script starts an isolated temporary PostgreSQL container, restores the dump, verifies public tables exist and removes the container. A complete quarterly drill must additionally restore object storage in an isolated environment and exercise login, history, media retrieval and message send/receive.

## Production restore

Restoration is destructive and requires an incident owner, approved restore point and maintenance window.

```bash
infra/scripts/restore.sh --confirm /absolute/path/to/backup .env.production
```

The script accepts only the published backup directory, verifies checksums, creates a pre-restore safety backup, stops the server writer, restores PostgreSQL, mirrors MinIO data, restarts the application and runs HTTPS smoke tests. Do not pass a `.incomplete-*` directory even if some expected files exist.

After restoration:

1. Validate the selected point in time and account/message counts.
2. Test direct/group history and recent media from two accounts.
3. Confirm new messages, sync cursors and push delivery.
4. Review audit logs for actions between the restored point and incident time.
5. Record actual RPO/RTO and corrective actions.

For low-RPO production, add continuous PostgreSQL WAL archiving and point-in-time recovery through the chosen managed database or backup system. Logical dumps alone do not provide 15-minute RPO at scale.
