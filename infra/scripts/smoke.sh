#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${1:-$ROOT_DIR/.env.production}"

# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

BASE_URL="${BASE_URL:-https://${DOMAIN:-${SERVER_IP:?DOMAIN or SERVER_IP is required}}}"
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
HEADERS_FILE="$(mktemp)"
trap 'rm -f "$HEADERS_FILE"' EXIT

curl --fail --silent --show-error --retry 12 --retry-delay 5 "$BASE_URL/healthz" >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 5 "$BASE_URL/health" >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 5 -D "$HEADERS_FILE" "$BASE_URL/" >/dev/null

grep -qi '^strict-transport-security:' "$HEADERS_FILE"
grep -qi '^content-security-policy:' "$HEADERS_FILE"
grep -qi '^x-content-type-options: nosniff' "$HEADERS_FILE"

status="$(curl --silent --output /dev/null --write-out '%{http_code}' "$BASE_URL/api/v2/admin/dashboard")"
if [[ "$status" != "401" ]]; then
  echo "expected unauthenticated admin API to return 401, got $status" >&2
  exit 1
fi

"${compose[@]}" exec -T wukongim sh -c 'wget -q --header="token: $WK_MANAGERTOKEN" -O /dev/null http://127.0.0.1:5001/health'
if "${compose[@]}" exec -T wukongim wget -q -O /dev/null http://127.0.0.1:5001/health; then
  echo "WuKongIM internal REST unexpectedly accepted a request without the manager token" >&2
  exit 1
fi
"${compose[@]}" exec -T wukongim sh -c 'wget -q --header="token: $WK_MANAGERTOKEN" -O /dev/null http://127.0.0.1:5300/cluster/nodes'
"${compose[@]}" exec -T livekit wget -q -O /dev/null http://127.0.0.1:7880/

echo "production smoke test passed: $BASE_URL"
