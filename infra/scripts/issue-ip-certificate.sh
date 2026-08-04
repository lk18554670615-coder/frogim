#!/usr/bin/env bash
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${1:-$ROOT_DIR/.env.ip.production}"

"$SCRIPT_DIR/validate-production-env.sh" "$ENV_FILE"
# shellcheck disable=SC1090
source "$SCRIPT_DIR/load-env.sh"
load_env_file "$ENV_FILE"

certbot_image="${CERTBOT_IMAGE:-certbot/certbot:latest}"
certbot_version="$(docker run --rm "$certbot_image" --version | awk '{print $2}')"
if [[ "$(printf '%s\n' 5.4.0 "$certbot_version" | sort -V | head -n1)" != "5.4.0" ]]; then
  echo "Certbot 5.4 or newer is required for IP certificates with webroot; found $certbot_version" >&2
  exit 1
fi

mkdir -p "$CERTBOT_DIR" "$CERTBOT_WEBROOT/.well-known/acme-challenge"
chmod 700 "$CERTBOT_DIR" "$CERTBOT_WEBROOT"

compose=(docker compose --env-file "$ENV_FILE" -f "$ROOT_DIR/infra/compose.ip.yaml" -f "$ROOT_DIR/infra/compose.ip.production.yaml")
temporary_server=""
gateway_id="$("${compose[@]}" ps -q gateway 2>/dev/null || true)"
if [[ -z "$gateway_id" ]]; then
  temporary_server="linli-ip-acme-$$"
  docker run --detach --rm --name "$temporary_server" --publish 80:80 \
    --volume "$CERTBOT_WEBROOT:/usr/share/nginx/html:ro" nginx:1.29-alpine >/dev/null
  for _ in {1..20}; do
    curl --fail --silent http://127.0.0.1/ >/dev/null 2>&1 && break
    sleep 0.25
  done
  curl --fail --silent http://127.0.0.1/ >/dev/null
fi

cleanup() {
  if [[ -n "$temporary_server" ]]; then
    docker stop "$temporary_server" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

docker run --rm \
  --volume "$CERTBOT_DIR:/etc/letsencrypt" \
  --volume "$CERTBOT_WEBROOT:/var/www/certbot" \
  "$certbot_image" certonly \
  --non-interactive \
  --agree-tos \
  --email "$TLS_EMAIL" \
  --preferred-profile shortlived \
  --webroot \
  --webroot-path /var/www/certbot \
  --ip-address "$SERVER_IP"

certificate="$CERTBOT_DIR/live/$SERVER_IP/fullchain.pem"
private_key="$CERTBOT_DIR/live/$SERVER_IP/privkey.pem"
[[ -r "$certificate" && -r "$private_key" ]] || { echo "issued certificate files are not readable" >&2; exit 1; }
openssl x509 -in "$certificate" -noout -checkip "$SERVER_IP" >/dev/null
openssl x509 -in "$certificate" -noout -checkend 43200 >/dev/null
echo "IP certificate is ready for $SERVER_IP"
