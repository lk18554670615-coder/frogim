#!/usr/bin/env bash
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${1:-$ROOT_DIR/.env.production}"

# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

if [[ "${BACKUP_DIR:-}" = /* ]]; then
  BACKUP_ROOT="$BACKUP_DIR"
else
  BACKUP_ROOT="$ROOT_DIR/${BACKUP_DIR:-backups}"
fi
export BACKUP_DIR="$BACKUP_ROOT"

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
destination="$BACKUP_ROOT/$timestamp"
working="$BACKUP_ROOT/.incomplete-$timestamp"
if [[ -e "$destination" || -e "$working" ]]; then
  echo "backup destination already exists for $timestamp" >&2
  exit 1
fi
mkdir -p -m 700 "$working/minio"
backup_complete=false
wukong_stopped=false
backup_metrics_started=false
compose=(docker compose --env-file "$ENV_FILE")
if [[ "${WUKONG_DEV_PUBLIC_REPLACEMENT:-false}" == "true" ]]; then
  compose+=(-f "$ROOT_DIR/infra/compose.ip.yaml" -f "$ROOT_DIR/infra/compose.wukong.production.yaml" -f "$ROOT_DIR/infra/compose.ip.wukong-dev.yaml")
elif [[ "${PRODUCTION_ENDPOINT_MODE:-domain}" == "ip" ]]; then
  compose+=(-f "$ROOT_DIR/infra/compose.ip.yaml" -f "$ROOT_DIR/infra/compose.ip.production.yaml")
else
  compose+=(-f "$ROOT_DIR/infra/compose.production.yaml")
fi
if [[ "${WUKONG_DEV_PUBLIC_REPLACEMENT:-false}" != "true" ]]; then
  compose+=(-f "$ROOT_DIR/infra/compose.wukong.production.yaml")
fi

cleanup() {
  local exit_status="$?" metrics_status=0
  trap - EXIT
  set +e
  if [[ "$wukong_stopped" == true ]]; then
    "${compose[@]}" start wukongim >/dev/null 2>&1 || true
  fi
  if [[ "$backup_complete" != true ]]; then
    echo "backup incomplete: $working" >&2
  fi
  if [[ "$backup_metrics_started" == true ]]; then
    if [[ "$backup_complete" == true && "$exit_status" -eq 0 ]]; then
      bash "$SCRIPT_DIR/backup-metrics.sh" success "$BACKUP_ROOT"
    else
      bash "$SCRIPT_DIR/backup-metrics.sh" failure "$BACKUP_ROOT"
    fi
    metrics_status=$?
    if [[ "$metrics_status" -ne 0 ]]; then
      echo "failed to publish backup metrics" >&2
      [[ "$exit_status" -ne 0 ]] || exit_status="$metrics_status"
    fi
  fi
  exit "$exit_status"
}
trap cleanup EXIT

bash "$SCRIPT_DIR/backup-metrics.sh" start "$BACKUP_ROOT"
backup_metrics_started=true

wukong_data="${LINLI_DATA_ROOT:?LINLI_DATA_ROOT is required}/wukongim/data"
if [[ ! -d "$wukong_data" ]]; then
  echo "WuKongIM data directory is missing: $wukong_data" >&2
  exit 1
fi
if "${compose[@]}" ps --status running --services | grep -qx wukongim; then
  "${compose[@]}" stop -t 30 wukongim
  wukong_stopped=true
fi
# Freeze WuKongIM before taking the PostgreSQL snapshot. Business mutations
# committed while it is stopped remain pending in the durable outbox, so the
# database can never point at a newer WuKong state than this archive.
tar -C "$wukong_data" -czf "$working/wukongim-data.tar.gz" .

"${compose[@]}" exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl > "$working/postgres.dump"

if [[ "$wukong_stopped" == true ]]; then
  "${compose[@]}" start wukongim
  wukong_stopped=false
fi

# Mirror objects after the database snapshot: an object referenced by that
# snapshot already existed before its metadata transaction committed. Writes
# that finish later can only add harmless orphan files to this backup.
"${compose[@]}" --profile ops run --rm --no-deps minio-client -c '
  mc alias set source http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
  mc mirror --preserve "source/$IM_S3_BUCKET" "/backup/.incomplete-'"$timestamp"'/minio"
'

cp "$ROOT_DIR/third_party/wukongim/versions.lock.json" "$working/versions.lock.json"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$working" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum) > "$working/SHA256SUMS"
else
  (cd "$working" && find . -type f ! -name SHA256SUMS -print | sort | xargs shasum -a 256) > "$working/SHA256SUMS"
fi

chmod -R go-rwx "$working"
mv "$working" "$destination"
backup_complete=true

if [[ "${BACKUP_OFFSITE_ENABLED:-false}" == "true" ]]; then
  if [[ ! "${BACKUP_OFFSITE_ENDPOINT:-}" =~ ^https://[^[:space:]]+$ ||
        ! "${BACKUP_OFFSITE_BUCKET:-}" =~ ^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$ ||
        "${BACKUP_OFFSITE_PREFIX:-}" == /* ||
        "${BACKUP_OFFSITE_PREFIX:-}" == */ ||
        "${BACKUP_OFFSITE_PREFIX:-}" == *..* ||
        ! "${BACKUP_OFFSITE_PREFIX:-}" =~ ^[A-Za-z0-9._/-]*$ ]]; then
    echo "off-site backup configuration is unsafe; run validate-production-env.sh" >&2
    exit 1
  fi
  "${compose[@]}" --profile ops run --rm --no-deps \
    -e "BACKUP_GENERATION=$timestamp" minio-client -c '
      set -eu
      mc alias set offsite "$BACKUP_OFFSITE_ENDPOINT" "$BACKUP_OFFSITE_ACCESS_KEY" "$BACKUP_OFFSITE_SECRET_KEY" --api S3v4
      mc stat "offsite/$BACKUP_OFFSITE_BUCKET" >/dev/null
      target="offsite/$BACKUP_OFFSITE_BUCKET"
      if [ -n "${BACKUP_OFFSITE_PREFIX:-}" ]; then
        target="$target/$BACKUP_OFFSITE_PREFIX"
      fi
      target="$target/$BACKUP_GENERATION"
      if mc stat "$target/_COMPLETE" >/dev/null 2>&1; then
        echo "off-site backup generation already has a completion marker: $BACKUP_GENERATION" >&2
        exit 1
      fi
      mc mirror --preserve "/backup/$BACKUP_GENERATION" "$target"
      mc cp "/backup/$BACKUP_GENERATION/SHA256SUMS" "$target/_COMPLETE"
      mc stat "$target/_COMPLETE" >/dev/null
    '
fi

echo "backup completed: $destination"
