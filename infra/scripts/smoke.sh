#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${1:-$ROOT_DIR/.env.production}"

# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

BASE_URL="${BASE_URL:-https://$DOMAIN}"
HEADERS_FILE="$(mktemp)"
trap 'rm -f "$HEADERS_FILE"' EXIT

curl --fail --silent --show-error --retry 12 --retry-delay 5 "$BASE_URL/healthz" >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 5 "$BASE_URL/health" >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 5 -D "$HEADERS_FILE" "$BASE_URL/" >/dev/null

grep -qi '^strict-transport-security:' "$HEADERS_FILE"
grep -qi '^content-security-policy:' "$HEADERS_FILE"
grep -qi '^x-content-type-options: nosniff' "$HEADERS_FILE"

status="$(curl --silent --output /dev/null --write-out '%{http_code}' "$BASE_URL/api/v1/admin/dashboard")"
if [[ "$status" != "401" ]]; then
  echo "expected unauthenticated admin API to return 401, got $status" >&2
  exit 1
fi

echo "production smoke test passed: $BASE_URL"
