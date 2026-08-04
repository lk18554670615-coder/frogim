#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${1:-$ROOT_DIR/.env.ip.production}"

"$SCRIPT_DIR/validate-production-env.sh" "$ENV_FILE"
# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

certificate="$CERTBOT_DIR/live/$SERVER_IP/fullchain.pem"
private_key="$CERTBOT_DIR/live/$SERVER_IP/privkey.pem"
if [[ ! -r "$certificate" || ! -r "$private_key" ]]; then
  echo "missing IP certificate; run infra/scripts/issue-ip-certificate.sh $ENV_FILE first" >&2
  exit 1
fi
openssl x509 -in "$certificate" -noout -checkip "$SERVER_IP" >/dev/null || {
  echo "certificate does not contain IP address $SERVER_IP" >&2
  exit 1
}
openssl x509 -in "$certificate" -noout -checkend 43200 >/dev/null || {
  echo "IP certificate expires within 12 hours; renew it before deploying" >&2
  exit 1
}

compose=(docker compose --env-file "$ENV_FILE" -f "$ROOT_DIR/infra/compose.ip.yaml" -f "$ROOT_DIR/infra/compose.ip.production.yaml")
if [[ "$IM_PUSH_PROVIDER" == "apns_voip" || "$IM_PUSH_PROVIDER" == "getui_apns_voip" ]]; then
  compose+=(-f "$ROOT_DIR/infra/compose.apns-voip.yaml")
fi

"${compose[@]}" config -q
"${compose[@]}" build --pull admin server
if [[ "$IM_PUSH_PROVIDER" == "apns_voip" || "$IM_PUSH_PROVIDER" == "getui_apns_voip" ]]; then
  "${compose[@]}" run --rm --no-deps --entrypoint /bin/sh server -c 'test -r "$IM_APNS_VOIP_PRIVATE_KEY_FILE"'
fi
"${compose[@]}" pull gateway postgres redis minio coturn
"${compose[@]}" up -d --remove-orphans --scale "server=${IM_REPLICAS:-1}"
"${compose[@]}" wait minio-init
"${compose[@]}" rm -f minio-init
"${compose[@]}" ps

"$SCRIPT_DIR/smoke.sh" "$ENV_FILE"
