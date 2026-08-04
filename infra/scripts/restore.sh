#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" != "--confirm" || -z "${2:-}" ]]; then
  echo "usage: $0 --confirm /absolute/path/to/backup [environment-file]" >&2
  exit 2
fi

BACKUP_PATH="$(cd "$2" && pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${3:-$ROOT_DIR/.env.production}"

if [[ ! -s "$BACKUP_PATH/postgres.dump" || ! -s "$BACKUP_PATH/SHA256SUMS" ]]; then
  echo "backup is incomplete: $BACKUP_PATH" >&2
  exit 1
fi

(cd "$BACKUP_PATH" && if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi)

# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

compose=(docker compose --env-file "$ENV_FILE" -f "$ROOT_DIR/infra/compose.production.yaml")
echo "creating a safety backup before restore"
"$SCRIPT_DIR/backup.sh" "$ENV_FILE"

"${compose[@]}" stop server
"${compose[@]}" exec -T postgres pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --no-acl < "$BACKUP_PATH/postgres.dump"

if [[ -d "$BACKUP_PATH/minio" ]]; then
  export BACKUP_DIR="$(dirname "$BACKUP_PATH")"
  backup_name="$(basename "$BACKUP_PATH")"
  "${compose[@]}" --profile ops run --rm --no-deps minio-client -c '
    mc alias set target http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
    mc mirror --overwrite "/backup/'"$backup_name"'/minio" "target/$IM_S3_BUCKET"
  '
fi

"${compose[@]}" up -d server admin gateway
"$SCRIPT_DIR/smoke.sh" "$ENV_FILE"
echo "restore completed and smoke test passed"
