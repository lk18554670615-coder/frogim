#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" != "--confirm-destructive-restore" || -z "${2:-}" ]]; then
  echo "usage: $0 --confirm-destructive-restore /absolute/path/to/backup [environment-file]" >&2
  exit 2
fi

BACKUP_PATH="$(cd "$2" && pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${3:-$ROOT_DIR/.env.production}"

backup_basename="$(basename "$BACKUP_PATH")"
if [[ "$backup_basename" == .incomplete-* || "$backup_basename" == .offsite-download-* ]]; then
  echo "refusing to restore an incomplete backup generation" >&2
  exit 1
fi

if [[ ! -s "$BACKUP_PATH/postgres.dump" || ! -s "$BACKUP_PATH/wukongim-data.tar.gz" || ! -s "$BACKUP_PATH/versions.lock.json" || ! -s "$BACKUP_PATH/SHA256SUMS" ]]; then
  echo "backup is incomplete: $BACKUP_PATH" >&2
  exit 1
fi

(cd "$BACKUP_PATH" && if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi)

# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

case "${POSTGRES_DB:-}" in
  ""|postgres|template0|template1)
    echo "unsafe PostgreSQL restore target: ${POSTGRES_DB:-<empty>}" >&2
    exit 1
    ;;
esac

compose=(docker compose --env-file "$ENV_FILE")
if [[ "${PRODUCTION_ENDPOINT_MODE:-domain}" == "ip" ]]; then
  compose+=(-f "$ROOT_DIR/infra/compose.ip.yaml" -f "$ROOT_DIR/infra/compose.ip.production.yaml")
else
  compose+=(-f "$ROOT_DIR/infra/compose.production.yaml")
fi
compose+=(-f "$ROOT_DIR/infra/compose.wukong.production.yaml")
if ! cmp -s "$BACKUP_PATH/versions.lock.json" "$ROOT_DIR/third_party/wukongim/versions.lock.json"; then
  echo "backup dependency lock does not match the deployed fixed versions" >&2
  exit 1
fi
echo "creating a safety backup before restore"
"$SCRIPT_DIR/backup.sh" "$ENV_FILE"

data_root="$(realpath -m "${LINLI_DATA_ROOT:?LINLI_DATA_ROOT is required}")"
if [[ "$data_root" == "/" || "$data_root" != /* ]]; then
  echo "unsafe LINLI_DATA_ROOT: $data_root" >&2
  exit 1
fi
wukong_parent="$data_root/wukongim"
wukong_data="$wukong_parent/data"
mkdir -p "$wukong_parent"
restore_stage="$(mktemp -d "$wukong_parent/.restore-XXXXXXXX")"
services_stopped=false
restore_complete=false
cleanup() {
  if [[ "$restore_complete" != true ]]; then
    if [[ "$services_stopped" == true ]]; then
      "${compose[@]}" stop server wukongim >/dev/null 2>&1 || true
    fi
    echo "restore did not complete; writer services remain stopped and the safety backup plus any data.pre-restore directory were retained" >&2
  fi
}
trap cleanup EXIT
if tar -tzf "$BACKUP_PATH/wukongim-data.tar.gz" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
  echo "unsafe path in WuKongIM backup archive" >&2
  exit 1
fi
tar -xzf "$BACKUP_PATH/wukongim-data.tar.gz" -C "$restore_stage"

"${compose[@]}" stop server wukongim
services_stopped=true
"${compose[@]}" exec -T postgres sh -ceu '
  dropdb -U "$POSTGRES_USER" --if-exists --force "$POSTGRES_DB"
  createdb -U "$POSTGRES_USER" -O "$POSTGRES_USER" "$POSTGRES_DB"
'
"${compose[@]}" exec -T postgres pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --exit-on-error --single-transaction --no-owner --no-acl < "$BACKUP_PATH/postgres.dump"

previous_wukong="$wukong_parent/data.pre-restore-$(date -u +%Y%m%dT%H%M%SZ)"
if [[ -e "$wukong_data" ]]; then
  mv "$wukong_data" "$previous_wukong"
fi
mv "$restore_stage" "$wukong_data"

if [[ -d "$BACKUP_PATH/minio" ]]; then
  export BACKUP_DIR="$(dirname "$BACKUP_PATH")"
  backup_name="$(basename "$BACKUP_PATH")"
  "${compose[@]}" --profile ops run --rm --no-deps minio-client -c '
    mc alias set target http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
    mc mirror --overwrite --remove "/backup/'"$backup_name"'/minio" "target/$IM_S3_BUCKET"
  '
fi

"${compose[@]}" up -d server wukongim livekit admin gateway
"$SCRIPT_DIR/smoke.sh" "$ENV_FILE"
services_stopped=false
restore_complete=true
echo "restore completed and smoke test passed; previous WuKongIM data retained at: $previous_wukong"
