#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${1:-$ROOT_DIR/.env.production}"

"$SCRIPT_DIR/validate-production-env.sh" "$ENV_FILE"

# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

compose=(docker compose --env-file "$ENV_FILE" -f "$ROOT_DIR/infra/compose.production.yaml" -f "$ROOT_DIR/infra/compose.wukong.production.yaml")
if [[ "$IM_PUSH_PROVIDER" == "apns_voip" || "$IM_PUSH_PROVIDER" == "getui_apns_voip" ]]; then
  compose+=(-f "$ROOT_DIR/infra/compose.apns-voip.yaml")
fi
"${compose[@]}" config -q
"${compose[@]}" build --pull admin server wukong-policy-plugin-init
if [[ "$IM_PUSH_PROVIDER" == "apns_voip" || "$IM_PUSH_PROVIDER" == "getui_apns_voip" ]]; then
  "${compose[@]}" run --rm --no-deps --entrypoint /bin/sh server -c 'test -r "$IM_APNS_VOIP_PRIVATE_KEY_FILE"'
fi
"${compose[@]}" pull gateway postgres redis minio prometheus grafana backup-metrics wukongim livekit
"${compose[@]}" up -d --remove-orphans --scale "server=${IM_REPLICAS:-1}"
"${compose[@]}" wait minio-init
"${compose[@]}" rm -f minio-init
"${compose[@]}" ps

"$SCRIPT_DIR/smoke.sh" "$ENV_FILE"
