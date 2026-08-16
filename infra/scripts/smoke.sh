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
WS_HEADERS_FILE="$(mktemp)"
trap 'rm -f "$HEADERS_FILE" "$WS_HEADERS_FILE"' EXIT

probe_websocket_upgrade() {
  local url="$1"
  : > "$WS_HEADERS_FILE"

  # A successful WebSocket remains open until either peer closes it, so curl
  # may finish with its timeout status after already receiving the 101. The
  # response status is the release gate; a bare TCP connect is insufficient.
  curl --silent --show-error --http1.1 --max-time 8 \
    -D "$WS_HEADERS_FILE" -o /dev/null \
    -H 'Connection: Upgrade' \
    -H 'Upgrade: websocket' \
    -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
    -H 'Sec-WebSocket-Version: 13' \
    "$url" || true

  if ! grep -Eq '^HTTP/1\.[01] 101([[:space:]]|$)' "$WS_HEADERS_FILE"; then
    echo "expected WebSocket upgrade at $url, received:" >&2
    sed -n '1,12p' "$WS_HEADERS_FILE" >&2
    exit 1
  fi
}

curl --fail --silent --show-error --retry 12 --retry-delay 5 "$BASE_URL/healthz" >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 5 "$BASE_URL/health" >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 5 "$BASE_URL/ready" \
  | jq -e '.status == "ready"' >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 5 "$BASE_URL/v2/config/auth" \
  | jq -e '
      (.registrationEnabled | type) == "boolean" and
      (.passwordMinLength | type) == "number" and
      .passwordMinLength >= 8 and .passwordMinLength <= 16 and
      (.passwordMaxBytes | type) == "number" and
      .passwordMaxBytes == 72
    ' >/dev/null
curl --fail --silent --show-error --retry 12 --retry-delay 5 -D "$HEADERS_FILE" "$BASE_URL/" >/dev/null

grep -qi '^strict-transport-security:' "$HEADERS_FILE"
grep -qi '^content-security-policy:' "$HEADERS_FILE"
grep -qi '^x-content-type-options: nosniff' "$HEADERS_FILE"

probe_websocket_upgrade "$BASE_URL/im"

livekit_status="$(curl --silent --output /dev/null --write-out '%{http_code}' --http1.1 \
  -H 'Connection: Upgrade' \
  -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  -H 'Sec-WebSocket-Version: 13' \
  "$BASE_URL/rtc/rtc/validate")"
if [[ "$livekit_status" != "401" ]]; then
  echo "expected unauthenticated LiveKit WebSocket route to return 401, got $livekit_status" >&2
  exit 1
fi

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
