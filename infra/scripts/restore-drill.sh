#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${1:-}" || ! -s "$1" ]]; then
  echo "usage: $0 /path/to/postgres.dump" >&2
  exit 2
fi

backup_file="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
container="nexachat-restore-drill-$$"
cleanup() { docker rm -f "$container" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --name "$container" -e POSTGRES_PASSWORD=restore-drill-password -e POSTGRES_DB=restore_drill postgres:17-alpine >/dev/null
for _ in {1..30}; do
  if docker exec "$container" pg_isready -U postgres -d restore_drill >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec -i "$container" pg_restore -U postgres -d restore_drill --no-owner --no-acl < "$backup_file"
table_count="$(docker exec "$container" psql -U postgres -d restore_drill -Atc "select count(*) from pg_catalog.pg_tables where schemaname='public'")"
if [[ "$table_count" -lt 1 ]]; then
  echo "restore drill failed: no public tables restored" >&2
  exit 1
fi

echo "restore drill passed with $table_count public tables"
