# Backup and restore

## Scope and targets

`infra/scripts/backup.sh` captures the three durable stores: a WuKongIM data archive, a PostgreSQL custom-format dump, and a preserved MinIO mirror. It also saves the fixed dependency lock and checksums every file. Redis remains a rebuildable cache/task accelerator; LiveKit room state and SDP/ICE are intentionally ephemeral.

Define business-approved targets before launch. A reasonable initial objective is RPO 15 minutes and RTO 60 minutes, but the schedule and infrastructure must be sized and tested against actual traffic.

## Create a backup

```bash
make backup PROD_ENV=.env.production
```

The script starts with `umask 077` and writes into `BACKUP_DIR/.incomplete-<UTC timestamp>/`. It freezes WuKongIM, archives its durable data, takes the PostgreSQL snapshot, starts WuKongIM again, and then mirrors MinIO. This ordering ensures the database snapshot cannot reference a newer WuKong state, while every media object referenced by the database existed before the later object mirror. It then writes `SHA256SUMS`, removes group/other permissions, and uses a same-filesystem rename to publish `BACKUP_DIR/<UTC timestamp>/`:

```text
postgres.dump
minio/
wukongim-data.tar.gz
versions.lock.json
SHA256SUMS
```

The installed compatibility command and systemd timer call `nexachat backup`. That command now delegates to this same authoritative script; it must not contain a second inline backup implementation. This is enforced by `infra/scripts/test-backup-metrics.sh` because the previous inline path omitted both WuKongIM data and `versions.lock.json`.

Set `BACKUP_METRICS_DIR` to exactly `BACKUP_DIR/.metrics`. Every attempt atomically publishes Prometheus textfile metrics for the latest attempt/success timestamps, duration, final status, running state and `.incomplete-*` count. Production Compose exposes only this directory through a read-only node-exporter and Prometheus evaluates:

- `NexaChatBackupMetricsMissing`
- `NexaChatBackupFailed`
- `NexaChatBackupStale`
- `NexaChatBackupRunningTooLong`
- `NexaChatIncompleteBackupGenerations`
- `NexaChatBackupOffsiteDisabled`

The failure-path test intentionally starts a backup with a missing WuKongIM directory and proves a non-zero exit, retained incomplete generation, `last_status=0`, `running=0` and an incomplete count of one. Prometheus configuration and all 19 rules are validated with the pinned deployment image's `promtool`.

For an approved HTTPS S3-compatible target, set `BACKUP_OFFSITE_ENABLED=true` together with the endpoint, scoped access key/secret, existing bucket and optional safe prefix. After atomically publishing the local generation, the same job mirrors that generation to `<bucket>/<prefix>/<timestamp>/` and uploads a duplicate of `SHA256SUMS` as `_COMPLETE` last. A remote-copy failure marks the whole job failed without deleting the usable local restore point. `NexaChatBackupOffsiteDisabled` remains active while this is not configured.

The repository does not select or silently purchase an off-host provider; credentials, retention and bucket object-lock policy remain an explicit production decision. Keep daily, weekly and monthly generations according to that policy. Use a bucket-scoped identity that cannot read unrelated data, and enforce retention/object-lock outside this application account so the application credentials cannot shorten it.

Retrieve a completed generation without restoring it:

```bash
bash infra/scripts/fetch-offsite-backup.sh 20260812T042300Z .env.production
make restore-drill BACKUP=/absolute/BACKUP_DIR/20260812T042300Z
```

The fetch script refuses an existing destination, requires `_COMPLETE`, compares it byte-for-byte with `SHA256SUMS`, validates every downloaded file and the fixed dependency lock, and only then atomically publishes the local directory. A failed transfer remains named `.offsite-download-*` and cannot be selected by the restore script.

`infra/scripts/test-offsite-backup.sh` exercises this boundary without external credentials: a valid generation publishes, a checksum-corrupted generation remains unpublished, and non-HTTPS or traversal-like prefixes are rejected before a network call. This contract test does not replace a restore drill against the selected external provider.

A `.incomplete-*` or `.offsite-download-*` directory is explicit evidence of an interrupted operation, not a restore point. Monitoring alerts on local `.incomplete-*`; operational review must also handle incomplete downloads. Preserve either only while investigating, then remove it through an approved, path-checked cleanup; never copy or promote it as a successful generation. The rename is atomic only because working and destination directories are created under the same `BACKUP_DIR` filesystem.

## Restore drill

Test the database dump without touching production:

```bash
make restore-drill BACKUP=/absolute/path/to/published-backup-directory
```

The script verifies all checksums and the fixed dependency lock, rejects unsafe WuKong archive paths, extracts the archive in a temporary directory, and restores PostgreSQL into an isolated temporary container with `--single-transaction`. It verifies the migration version, critical WuKong/media tables, and database constraints before removing all temporary resources. Passing this structural drill does not replace a quarterly isolated application drill covering MinIO restoration, login, WuKong direct/group history, media retrieval, new messages and offline recovery.

2026-08-11本地 schema 43快照执行了上述完整目录校验和与一次性容器恢复，结果为：62张公共表、6张关键表、336项约束、177个 WuKong数据文件和21个 MinIO文件。该结果仅证明当前脚本和快照结构可恢复，不替代异地副本或生产恢复演练。

2026-08-12新增系统账号权威表后，使用当前 schema 45 的真实 PostgreSQL 自定义格式快照再次完成隔离容器恢复：64张公共表、7张关键表（包含`im_wukong_system_users`）、schema 45、342项约束。恢复门禁会对 schema 45及更高版本强制要求系统账号表；旧 schema快照继续按当时的6张关键表验证。本次增量复验只覆盖数据库，完整 WuKongIM/MinIO目录恢复仍以上一条完整演练为证据。

## Production restore

Restoration is destructive and requires an incident owner, approved restore point and maintenance window.

```bash
infra/scripts/restore.sh --confirm-destructive-restore /absolute/path/to/backup .env.production
```

The script accepts only the published backup directory, verifies checksums and the fixed dependency lock, creates a pre-restore safety backup, rejects unsafe WuKong archive paths, stops the server and WuKong writers, restores PostgreSQL/WuKong/MinIO, restarts the application and runs smoke tests. Do not pass a `.incomplete-*` directory even if some expected files exist. Production execution still requires the separate cutover confirmation record defined by the migration plan.

After restoration:

1. Validate the selected point in time and account/message counts.
2. Test direct/group history and recent media from two accounts.
3. Confirm new WuKong messages, recent conversations, offline recovery and push delivery.
4. Review audit logs for actions between the restored point and incident time.
5. Record actual RPO/RTO and corrective actions.

For low-RPO production, add continuous PostgreSQL WAL archiving and point-in-time recovery through the chosen managed database or backup system. Logical dumps alone do not provide 15-minute RPO at scale.
