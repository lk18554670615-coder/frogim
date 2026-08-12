#!/usr/bin/env bash
set -euo pipefail

SERVER_URL="${SERVER_URL:-http://127.0.0.1:8080}"
ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:8088}"

curl --fail --silent --show-error --retry 8 --retry-delay 2 "$SERVER_URL/ready" >/dev/null
curl --fail --silent --show-error --retry 8 --retry-delay 2 "$ADMIN_URL/healthz" >/dev/null
curl --fail --silent --show-error -D - "$ADMIN_URL/" -o /dev/null | grep -qi '^content-security-policy:'

status="$(curl --silent --output /dev/null --write-out '%{http_code}' "$SERVER_URL/api/v2/admin/dashboard")"
if [[ "$status" != "401" ]]; then
  echo "expected unauthenticated admin API to return 401, got $status" >&2
  exit 1
fi

echo "local full-stack smoke test passed"
