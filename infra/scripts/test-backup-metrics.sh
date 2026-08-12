#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
temporary_root="$(mktemp -d)"
cleanup() {
  local target="$temporary_root"
  if [[ -n "$target" && "$target" == /tmp/* && -d "$target" ]]; then
    rm -rf -- "$target"
  fi
}
trap cleanup EXIT

backup_root="$temporary_root/backups"
export BACKUP_METRICS_DIR="$backup_root/.metrics"
mkdir -p "$backup_root/.incomplete-manual"

BACKUP_METRICS_NOW=1000 bash "$SCRIPT_DIR/backup-metrics.sh" start "$backup_root"
metrics="$BACKUP_METRICS_DIR/nexachat-backup.prom"
grep -qx 'nexachat_backup_last_attempt_timestamp_seconds 1000' "$metrics"
grep -qx 'nexachat_backup_running 1' "$metrics"
grep -qx 'nexachat_backup_incomplete_generations 1' "$metrics"
grep -qx 'nexachat_backup_offsite_enabled 0' "$metrics"

rm -rf -- "$backup_root/.incomplete-manual"
BACKUP_METRICS_NOW=1037 bash "$SCRIPT_DIR/backup-metrics.sh" success "$backup_root"
grep -qx 'nexachat_backup_last_success_timestamp_seconds 1037' "$metrics"
grep -qx 'nexachat_backup_last_duration_seconds 37' "$metrics"
grep -qx 'nexachat_backup_last_status 1' "$metrics"
grep -qx 'nexachat_backup_running 0' "$metrics"
grep -qx 'nexachat_backup_incomplete_generations 0' "$metrics"

mkdir -p "$backup_root/.incomplete-failed"
BACKUP_METRICS_NOW=2000 bash "$SCRIPT_DIR/backup-metrics.sh" start "$backup_root"
BACKUP_METRICS_NOW=2012 bash "$SCRIPT_DIR/backup-metrics.sh" failure "$backup_root"
grep -qx 'nexachat_backup_last_success_timestamp_seconds 1037' "$metrics"
grep -qx 'nexachat_backup_last_duration_seconds 12' "$metrics"
grep -qx 'nexachat_backup_last_status 0' "$metrics"
grep -qx 'nexachat_backup_running 0' "$metrics"
grep -qx 'nexachat_backup_incomplete_generations 1' "$metrics"

BACKUP_OFFSITE_ENABLED=true BACKUP_METRICS_NOW=2013 bash "$SCRIPT_DIR/backup-metrics.sh" refresh "$backup_root"
grep -qx 'nexachat_backup_offsite_enabled 1' "$metrics"

bad_metrics="$temporary_root/not-the-backup-metrics-directory"
if BACKUP_METRICS_DIR="$bad_metrics" bash "$SCRIPT_DIR/backup-metrics.sh" refresh "$backup_root" >/dev/null 2>&1; then
  echo "backup metrics accepted a mismatched exporter directory" >&2
  exit 1
fi

# Exercise the authoritative backup's EXIT trap without touching Docker: a
# missing WuKong data directory must fail after publishing the running state.
failure_root="$temporary_root/failure-case"
mkdir -p "$failure_root/data" "$failure_root/backups"
failure_env="$failure_root/config.env"
cat > "$failure_env" <<EOF
LINLI_DATA_ROOT=$failure_root/data
BACKUP_DIR=$failure_root/backups
BACKUP_METRICS_DIR=$failure_root/backups/.metrics
PRODUCTION_ENDPOINT_MODE=domain
EOF
if bash "$SCRIPT_DIR/backup.sh" "$failure_env" >"$failure_root/stdout" 2>"$failure_root/stderr"; then
  echo "backup unexpectedly succeeded without WuKongIM data" >&2
  exit 1
fi
grep -q 'WuKongIM data directory is missing' "$failure_root/stderr"
grep -q 'backup incomplete:' "$failure_root/stderr"
failure_metrics="$failure_root/backups/.metrics/nexachat-backup.prom"
grep -qx 'nexachat_backup_last_status 0' "$failure_metrics"
grep -qx 'nexachat_backup_running 0' "$failure_metrics"
grep -qx 'nexachat_backup_incomplete_generations 1' "$failure_metrics"

if bash "$SCRIPT_DIR/fetch-offsite-backup.sh" invalid "$failure_env" >"$failure_root/fetch-invalid.stdout" 2>"$failure_root/fetch-invalid.stderr"; then
  echo "off-site fetch accepted an invalid generation" >&2
  exit 1
fi
grep -q 'must use YYYYMMDDTHHMMSSZ' "$failure_root/fetch-invalid.stderr"
if bash "$SCRIPT_DIR/fetch-offsite-backup.sh" 20260812T120000Z "$failure_env" >"$failure_root/fetch-disabled.stdout" 2>"$failure_root/fetch-disabled.stderr"; then
  echo "off-site fetch ran while disabled" >&2
  exit 1
fi
grep -q 'off-site backup is not enabled' "$failure_root/fetch-disabled.stderr"

unfinished="$failure_root/backups/.offsite-download-20260812T120000Z"
mkdir -p "$unfinished"
if bash "$SCRIPT_DIR/restore.sh" --confirm-destructive-restore "$unfinished" "$failure_env" >"$failure_root/restore-guard.stdout" 2>"$failure_root/restore-guard.stderr"; then
  echo "restore accepted an incomplete off-site download" >&2
  exit 1
fi
grep -q 'refusing to restore an incomplete backup generation' "$failure_root/restore-guard.stderr"
if bash "$SCRIPT_DIR/restore-drill.sh" "$unfinished" >"$failure_root/drill-guard.stdout" 2>"$failure_root/drill-guard.stderr"; then
  echo "restore drill accepted an incomplete off-site download" >&2
  exit 1
fi
grep -q 'restore drill refused an incomplete backup generation' "$failure_root/drill-guard.stderr"

grep -Fq 'exec "$APP_ROOT/infra/scripts/backup.sh" "$CONFIG_FILE"' "$SCRIPT_DIR/nexachat-ops.sh"

echo "backup metrics and failure-path verification passed"
