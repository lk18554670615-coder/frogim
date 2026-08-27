#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${1:-}" || ! -e "$1" ]]; then
  echo "usage: $0 /path/to/backup-directory-or-postgres.dump" >&2
  exit 2
fi

input="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
backup_file="$input"
backup_dir=""
temp_dir=""
wukong_files="not-checked"
minio_files="not-checked"

input_basename="$(basename "$input")"
if [[ "$input_basename" == .incomplete-* || "$input_basename" == .offsite-download-* ]]; then
  echo "restore drill refused an incomplete backup generation" >&2
  exit 1
fi

if [[ -d "$input" ]]; then
  backup_dir="$input"
  for required in postgres.dump wukongim-data.tar.gz versions.lock.json SHA256SUMS; do
    if [[ ! -s "$backup_dir/$required" ]]; then
      echo "restore drill failed: missing $required" >&2
      exit 1
    fi
  done
  (cd "$backup_dir" && if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi)
  if ! cmp -s "$backup_dir/versions.lock.json" "$root_dir/third_party/wukongim/versions.lock.json"; then
    echo "restore drill failed: dependency lock differs from the deployed fixed versions" >&2
    exit 1
  fi
  if tar -tzf "$backup_dir/wukongim-data.tar.gz" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
    echo "restore drill failed: unsafe path in WuKongIM archive" >&2
    exit 1
  fi
  temp_dir="$(mktemp -d)"
  mkdir -p "$temp_dir/wukong"
  tar -xzf "$backup_dir/wukongim-data.tar.gz" -C "$temp_dir/wukong"
  wukong_files="$(find "$temp_dir/wukong" -type f | wc -l | tr -d ' ')"
  if [[ "$wukong_files" -lt 1 ]]; then
    echo "restore drill failed: WuKongIM archive contains no files" >&2
    exit 1
  fi
  if [[ -d "$backup_dir/minio" ]]; then
    minio_files="$(find "$backup_dir/minio" -type f | wc -l | tr -d ' ')"
  else
    minio_files=0
  fi
  backup_file="$backup_dir/postgres.dump"
elif [[ ! -s "$backup_file" ]]; then
  echo "restore drill failed: PostgreSQL dump is empty" >&2
  exit 1
fi

container="nexachat-restore-drill-$$"
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  if [[ -n "$temp_dir" && -d "$temp_dir" ]]; then
    rm -rf -- "$temp_dir"
  fi
}
trap cleanup EXIT

docker run -d --name "$container" -e POSTGRES_PASSWORD=restore-drill-password -e POSTGRES_DB=restore_drill postgres:17-alpine >/dev/null
ready=false
for _ in {1..30}; do
  if docker exec "$container" pg_isready -U postgres -d restore_drill >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  echo "restore drill failed: isolated PostgreSQL did not become ready" >&2
  exit 1
fi
docker exec -i "$container" pg_restore -U postgres -d restore_drill --exit-on-error --single-transaction --no-owner --no-acl < "$backup_file"

validation="$(docker exec "$container" psql -U postgres -d restore_drill -Atc "
  SELECT
    (SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'),
    (SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public' AND tablename IN ('im_users','im_media','im_wukong_outbox','im_wukong_message_index','im_wukong_webhook_events','im_wukong_system_users','im_schema_migrations')),
    (SELECT COALESCE(max(version),0) FROM im_schema_migrations),
    (SELECT count(*) FROM pg_catalog.pg_constraint WHERE contype IN ('p','f','u','c'));
")"
IFS='|' read -r table_count critical_count schema_version constraint_count <<< "$validation"
expected_critical_count=6
if [[ "$schema_version" -ge 45 ]]; then
  expected_critical_count=7
fi
if [[ "$table_count" -lt 1 || "$critical_count" -ne "$expected_critical_count" || "$schema_version" -lt 1 || "$constraint_count" -lt 1 ]]; then
  echo "restore drill failed: tables=$table_count critical=$critical_count schema=$schema_version constraints=$constraint_count" >&2
  exit 1
fi

echo "restore drill passed: tables=$table_count critical=$critical_count schema=$schema_version constraints=$constraint_count wukong_files=$wukong_files minio_files=$minio_files"
