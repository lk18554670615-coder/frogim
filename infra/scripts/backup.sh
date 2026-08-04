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
trap 'if [[ "$backup_complete" != true ]]; then echo "backup incomplete: $working" >&2; fi' EXIT

compose=(docker compose --env-file "$ENV_FILE" -f "$ROOT_DIR/infra/compose.production.yaml")
"${compose[@]}" exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl > "$working/postgres.dump"

"${compose[@]}" --profile ops run --rm --no-deps minio-client -c '
  mc alias set source http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
  mc mirror --preserve "source/$IM_S3_BUCKET" "/backup/.incomplete-'"$timestamp"'/minio"
'

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$working" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum) > "$working/SHA256SUMS"
else
  (cd "$working" && find . -type f ! -name SHA256SUMS -print | sort | xargs shasum -a 256) > "$working/SHA256SUMS"
fi

chmod -R go-rwx "$working"
mv "$working" "$destination"
backup_complete=true
echo "backup completed: $destination"
