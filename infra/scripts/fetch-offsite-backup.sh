#!/usr/bin/env bash
set -euo pipefail
umask 077

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 YYYYMMDDTHHMMSSZ [production-environment-file]" >&2
  exit 2
fi

generation="$1"
if [[ ! "$generation" =~ ^[0-9]{8}T[0-9]{6}Z$ ]]; then
  echo "off-site backup generation must use YYYYMMDDTHHMMSSZ" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${2:-$ROOT_DIR/.env.production}"

# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

if [[ "${BACKUP_OFFSITE_ENABLED:-false}" != "true" ]]; then
  echo "off-site backup is not enabled in the selected environment" >&2
  exit 2
fi
if [[ ! "${BACKUP_OFFSITE_ENDPOINT:-}" =~ ^https://[^[:space:]]+$ ||
      ! "${BACKUP_OFFSITE_BUCKET:-}" =~ ^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$ ||
      "${BACKUP_OFFSITE_PREFIX:-}" == /* ||
      "${BACKUP_OFFSITE_PREFIX:-}" == */ ||
      "${BACKUP_OFFSITE_PREFIX:-}" == *..* ||
      ! "${BACKUP_OFFSITE_PREFIX:-}" =~ ^[A-Za-z0-9._/-]*$ ]]; then
  echo "off-site backup configuration is unsafe; run validate-production-env.sh" >&2
  exit 2
fi
backup_root="${BACKUP_DIR:-}"
backup_root="${backup_root%/}"
if [[ "$backup_root" != /* || "$backup_root" == "/" ]]; then
  echo "BACKUP_DIR must be an absolute non-root directory" >&2
  exit 2
fi

destination="$backup_root/$generation"
working="$backup_root/.offsite-download-$generation"
if [[ -e "$destination" || -e "$working" ]]; then
  echo "refusing to overwrite an existing local backup or download: $generation" >&2
  exit 1
fi
mkdir -p -m 700 "$working"
download_complete=false
cleanup() {
  if [[ "$download_complete" != true ]]; then
    echo "off-site download incomplete: $working" >&2
  fi
}
trap cleanup EXIT

compose=(docker compose --env-file "$ENV_FILE")
if [[ "${PRODUCTION_ENDPOINT_MODE:-domain}" == "ip" ]]; then
  compose+=(-f "$ROOT_DIR/infra/compose.ip.yaml" -f "$ROOT_DIR/infra/compose.ip.production.yaml")
else
  compose+=(-f "$ROOT_DIR/infra/compose.production.yaml")
fi
compose+=(-f "$ROOT_DIR/infra/compose.wukong.production.yaml")

"${compose[@]}" --profile ops run --rm --no-deps \
  -e "BACKUP_GENERATION=$generation" \
  -e "BACKUP_DOWNLOAD_DIRECTORY=.offsite-download-$generation" \
  minio-client -c '
    set -eu
    mc alias set offsite "$BACKUP_OFFSITE_ENDPOINT" "$BACKUP_OFFSITE_ACCESS_KEY" "$BACKUP_OFFSITE_SECRET_KEY" --api S3v4
    source_path="offsite/$BACKUP_OFFSITE_BUCKET"
    if [ -n "${BACKUP_OFFSITE_PREFIX:-}" ]; then
      source_path="$source_path/$BACKUP_OFFSITE_PREFIX"
    fi
    source_path="$source_path/$BACKUP_GENERATION"
    mc stat "$source_path/_COMPLETE" >/dev/null
    mc mirror --preserve "$source_path" "/backup/$BACKUP_DOWNLOAD_DIRECTORY"
  '

if [[ ! -s "$working/_COMPLETE" || ! -s "$working/SHA256SUMS" ]]; then
  echo "off-site generation is missing its completion marker or checksum manifest" >&2
  exit 1
fi
if ! cmp -s "$working/_COMPLETE" "$working/SHA256SUMS"; then
  echo "off-site completion marker does not match SHA256SUMS" >&2
  exit 1
fi
rm -f -- "$working/_COMPLETE"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$working" && sha256sum -c SHA256SUMS)
else
  (cd "$working" && shasum -a 256 -c SHA256SUMS)
fi
if ! cmp -s "$working/versions.lock.json" "$ROOT_DIR/third_party/wukongim/versions.lock.json"; then
  echo "off-site backup dependency lock does not match the current deployment" >&2
  exit 1
fi

chmod -R go-rwx "$working"
mv "$working" "$destination"
download_complete=true
echo "off-site backup downloaded and verified: $destination"
