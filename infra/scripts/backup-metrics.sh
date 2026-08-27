#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 {start|success|failure|refresh} /absolute/backup/root" >&2
  exit 2
fi

action="$1"
backup_root="${2%/}"
if [[ "$backup_root" != /* || "$backup_root" == "/" ]]; then
  echo "backup root must be an absolute non-root directory" >&2
  exit 2
fi

metrics_dir="${BACKUP_METRICS_DIR:-$backup_root/.metrics}"
if [[ "$metrics_dir" != "$backup_root/.metrics" ]]; then
  echo "BACKUP_METRICS_DIR must be exactly $backup_root/.metrics" >&2
  exit 2
fi

now="${BACKUP_METRICS_NOW:-$(date +%s)}"
if [[ ! "$now" =~ ^[0-9]+$ ]]; then
  echo "BACKUP_METRICS_NOW must be a Unix timestamp" >&2
  exit 2
fi

mkdir -p "$metrics_dir/.state"
chmod 755 "$metrics_dir"
chmod 700 "$metrics_dir/.state"

atomic_state_write() {
  local name="$1" value="$2" temporary
  temporary="$(mktemp "$metrics_dir/.state/.${name}.XXXXXX")"
  printf '%s\n' "$value" > "$temporary"
  chmod 600 "$temporary"
  mv -f "$temporary" "$metrics_dir/.state/$name"
}

read_state() {
  local name="$1" fallback="$2" value
  if [[ -r "$metrics_dir/.state/$name" ]]; then
    value="$(tr -d '\r\n' < "$metrics_dir/.state/$name")"
    if [[ "$value" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
      printf '%s' "$value"
      return
    fi
  fi
  printf '%s' "$fallback"
}

case "$action" in
  start)
    atomic_state_write last_attempt "$now"
    atomic_state_write started_at "$now"
    atomic_state_write running 1
    ;;
  success|failure)
    started_at="$(read_state started_at "$now")"
    if (( now >= started_at )); then
      duration=$((now - started_at))
    else
      duration=0
    fi
    atomic_state_write last_duration "$duration"
    atomic_state_write last_status "$([[ "$action" == success ]] && printf 1 || printf 0)"
    atomic_state_write running 0
    if [[ "$action" == success ]]; then
      atomic_state_write last_success "$now"
    fi
    ;;
  refresh)
    ;;
  *)
    echo "unknown backup metrics action: $action" >&2
    exit 2
    ;;
esac

last_attempt="$(read_state last_attempt 0)"
last_success="$(read_state last_success 0)"
last_duration="$(read_state last_duration 0)"
last_status="$(read_state last_status 0)"
running="$(read_state running 0)"
incomplete_generations="$(find "$backup_root" -mindepth 1 -maxdepth 1 -type d -name '.incomplete-*' -print 2>/dev/null | wc -l | tr -d '[:space:]')"
offsite_enabled=0
if [[ "${BACKUP_OFFSITE_ENABLED:-false}" == "true" ]]; then
  offsite_enabled=1
fi

temporary_metrics="$(mktemp "$metrics_dir/.nexachat-backup.prom.XXXXXX")"
cat > "$temporary_metrics" <<EOF
# HELP nexachat_backup_last_attempt_timestamp_seconds Unix timestamp of the latest backup attempt.
# TYPE nexachat_backup_last_attempt_timestamp_seconds gauge
nexachat_backup_last_attempt_timestamp_seconds $last_attempt
# HELP nexachat_backup_last_success_timestamp_seconds Unix timestamp of the latest completed backup.
# TYPE nexachat_backup_last_success_timestamp_seconds gauge
nexachat_backup_last_success_timestamp_seconds $last_success
# HELP nexachat_backup_last_duration_seconds Duration of the latest finished backup attempt.
# TYPE nexachat_backup_last_duration_seconds gauge
nexachat_backup_last_duration_seconds $last_duration
# HELP nexachat_backup_last_status Whether the latest finished backup attempt succeeded (1) or failed (0).
# TYPE nexachat_backup_last_status gauge
nexachat_backup_last_status $last_status
# HELP nexachat_backup_running Whether a backup attempt is currently running.
# TYPE nexachat_backup_running gauge
nexachat_backup_running $running
# HELP nexachat_backup_incomplete_generations Number of unpublished .incomplete backup generations.
# TYPE nexachat_backup_incomplete_generations gauge
nexachat_backup_incomplete_generations $incomplete_generations
# HELP nexachat_backup_offsite_enabled Whether the complete backup job includes an HTTPS off-site copy.
# TYPE nexachat_backup_offsite_enabled gauge
nexachat_backup_offsite_enabled $offsite_enabled
EOF
chmod 644 "$temporary_metrics"
mv -f "$temporary_metrics" "$metrics_dir/nexachat-backup.prom"
