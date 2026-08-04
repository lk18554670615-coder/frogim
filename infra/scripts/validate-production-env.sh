#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV_FILE="${1:-$ROOT_DIR/.env.production}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "missing production environment file: $ENV_FILE" >&2
  exit 1
fi

while IFS= read -r line || [[ -n "$line" ]]; do
  line="${line%$'\r'}"
  [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
  if [[ "$line" != *=* ]]; then
    echo "invalid environment line (expected KEY=VALUE)" >&2
    exit 1
  fi
  key="${line%%=*}"
  value="${line#*=}"
  if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    echo "invalid environment variable name: $key" >&2
    exit 1
  fi
  if (( ${#value} >= 2 )) && { [[ "$value" == \'*\' ]] || [[ "$value" == \"*\" ]]; }; then
    value="${value:1:${#value}-2}"
  fi
  # Assign literally: password hashes and secrets may contain dollar signs.
  export "$key=$value"
done < "$ENV_FILE"

required=(
  IM_ENV POSTGRES_DB POSTGRES_USER POSTGRES_PASSWORD REDIS_PASSWORD
  IM_DATABASE_URL IM_REDIS_URL IM_JWT_SECRET IM_ADMIN_EMAIL
  IM_ADMIN_PASSWORD_HASH IM_ADMIN_TOTP_SECRET IM_ADMIN_SHARED_KEY_ENABLED
  IM_PUSH_PROVIDER IM_OTP_WEBHOOK_URL IM_OTP_WEBHOOK_TOKEN
  MINIO_ROOT_USER MINIO_ROOT_PASSWORD MINIO_APP_USER MINIO_APP_PASSWORD
)

endpoint_mode="${PRODUCTION_ENDPOINT_MODE:-domain}"
if [[ "$endpoint_mode" == "ip" ]]; then
  required+=(SERVER_IP TLS_EMAIL CERTBOT_DIR CERTBOT_WEBROOT)
else
  required+=(DOMAIN TLS_EMAIL GRAFANA_ADMIN_PASSWORD)
fi

failed=0
for key in "${required[@]}"; do
  value="${!key:-}"
  if [[ -z "$value" ]]; then
    echo "missing required variable: $key" >&2
    failed=1
  elif [[ "$value" == *REPLACE_WITH* || "$value" == *change-this* || "$value" == *local-development* || "$value" == *ChangeMe* ]]; then
    echo "placeholder value is not allowed: $key" >&2
    failed=1
  fi
done

if [[ "$endpoint_mode" == "ip" ]]; then
  if [[ ! "${SERVER_IP:-}" =~ ^[0-9A-Fa-f:.]+$ ]]; then
    echo "SERVER_IP must be a literal IPv4 or IPv6 address" >&2
    failed=1
  fi
elif [[ "${DOMAIN:-}" == "localhost" || "${DOMAIN:-}" == *.example.com || ! "${DOMAIN:-}" =~ ^[A-Za-z0-9.-]+$ ]]; then
    echo "DOMAIN must be a real DNS name" >&2
    failed=1
fi

if [[ "$endpoint_mode" != "ip" && "$endpoint_mode" != "domain" ]]; then
  echo "PRODUCTION_ENDPOINT_MODE must be ip or domain" >&2
  failed=1
fi

if [[ ! "${TLS_EMAIL:-}" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; then
  echo "TLS_EMAIL must be a valid certificate contact email address" >&2
  failed=1
fi

if [[ "${IM_ENV:-}" != "production" ]]; then
  echo "IM_ENV must be production" >&2
  failed=1
fi

for key in IM_JWT_SECRET; do
  value="${!key:-}"
  if (( ${#value} < 32 )); then
    echo "$key must contain at least 32 characters" >&2
    failed=1
  fi
done

if [[ ! "${IM_ADMIN_EMAIL:-}" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; then
  echo "IM_ADMIN_EMAIL must be a valid administrator email address" >&2
  failed=1
fi

if [[ ! "${IM_ADMIN_PASSWORD_HASH:-}" =~ ^\$2[aby]\$ ]]; then
  echo "IM_ADMIN_PASSWORD_HASH must be a bcrypt hash, never a plaintext password" >&2
  failed=1
fi

if ! grep -Eq "^IM_ADMIN_PASSWORD_HASH='\\\$2[aby]\\\$" "$ENV_FILE"; then
  echo "IM_ADMIN_PASSWORD_HASH must be wrapped in single quotes so Docker Compose preserves dollar signs" >&2
  failed=1
fi

totp_secret="${IM_ADMIN_TOTP_SECRET:-}"
if (( ${#totp_secret} < 16 )) || [[ ! "$totp_secret" =~ ^[A-Z2-7]+=*$ ]]; then
  echo "IM_ADMIN_TOTP_SECRET must be a Base32 secret containing at least 16 characters" >&2
  failed=1
fi

if [[ "${IM_ADMIN_SHARED_KEY_ENABLED:-}" != "false" ]]; then
  echo "IM_ADMIN_SHARED_KEY_ENABLED must be false in production" >&2
  failed=1
fi

if [[ -n "${IM_DEV_ALLOW_CONTAINER_BIND:-}" && "${IM_DEV_ALLOW_CONTAINER_BIND:-}" != "false" ]]; then
  echo "IM_DEV_ALLOW_CONTAINER_BIND must be false or unset in production" >&2
  failed=1
fi
if [[ -n "${IM_DEV_MODE:-}" && "${IM_DEV_MODE:-}" != "false" ]]; then
  echo "IM_DEV_MODE must be false or unset in production" >&2
  failed=1
fi
if [[ -n "${IM_IP_TEST_ONLY:-}" && "${IM_IP_TEST_ONLY:-}" != "false" ]]; then
  echo "IM_IP_TEST_ONLY must be false or unset in production" >&2
  failed=1
fi

for key in POSTGRES_PASSWORD REDIS_PASSWORD MINIO_ROOT_PASSWORD MINIO_APP_PASSWORD; do
  value="${!key:-}"
  if (( ${#value} < 20 )); then
    echo "$key must contain at least 20 characters" >&2
    failed=1
  fi
done
if [[ "${MINIO_APP_USER:-}" == "${MINIO_ROOT_USER:-}" || "${MINIO_APP_PASSWORD:-}" == "${MINIO_ROOT_PASSWORD:-}" ]]; then
  echo "MinIO application credentials must be distinct from root credentials" >&2
  failed=1
fi
grafana_password="${GRAFANA_ADMIN_PASSWORD:-}"
if [[ "$endpoint_mode" != "ip" && ${#grafana_password} -lt 20 ]]; then
  echo "GRAFANA_ADMIN_PASSWORD must contain at least 20 characters" >&2
  failed=1
fi

validate_getui() {
  local key value master_secret="${IM_GETUI_MASTER_SECRET:-}"
  for key in IM_GETUI_APP_ID IM_GETUI_APP_KEY IM_GETUI_MASTER_SECRET; do
    value="${!key:-}"
    if [[ -z "$value" || "$value" == *REPLACE_WITH* ]]; then
      echo "valid Getui credential is required: $key" >&2
      failed=1
    fi
  done
  if (( ${#master_secret} < 16 )); then
    echo "IM_GETUI_MASTER_SECRET must contain at least 16 characters" >&2
    failed=1
  fi
}

validate_apns_voip() {
  local key_file="${APNS_VOIP_PRIVATE_KEY_HOST_FILE:-}" mode uid gid
  if [[ ! "${IM_APNS_VOIP_KEY_ID:-}" =~ ^[A-Z0-9]{10}$ || ! "${IM_APNS_VOIP_TEAM_ID:-}" =~ ^[A-Z0-9]{10}$ ]]; then
    echo "APNs key id and team id must contain exactly 10 uppercase letters or digits" >&2
    failed=1
  fi
  if [[ -z "${IM_APNS_VOIP_BUNDLE_ID:-}" || "${IM_APNS_VOIP_BUNDLE_ID:-}" == *.voip ]]; then
    echo "IM_APNS_VOIP_BUNDLE_ID must be the app bundle id without .voip" >&2
    failed=1
  fi
  if [[ ! -f "$key_file" ]]; then
    echo "APNs private key file is missing: $key_file" >&2
    failed=1
    return
  fi
  if stat -f '%Lp' "$key_file" >/dev/null 2>&1; then
    mode="$(stat -f '%Lp' "$key_file")"; uid="$(stat -f '%u' "$key_file")"; gid="$(stat -f '%g' "$key_file")"
  else
    mode="$(stat -c '%a' "$key_file")"; uid="$(stat -c '%u' "$key_file")"; gid="$(stat -c '%g' "$key_file")"
  fi
  if [[ "$mode" != "400" && "$mode" != "440" ]] || [[ "$uid" != "10001" && "$gid" != "10001" ]]; then
    echo "APNs private key must be mode 400/440 and owned by uid or gid 10001; current mode=$mode uid=$uid gid=$gid" >&2
    failed=1
  fi
}

for key in IM_OTP_WEBHOOK_URL; do
  value="${!key:-}"
  if [[ ! "$value" =~ ^https:// ]]; then
    echo "$key must use HTTPS" >&2
    failed=1
  fi
done

for key in IM_OTP_WEBHOOK_TOKEN; do
  value="${!key:-}"
  if (( ${#value} < 24 )); then
    echo "$key must contain at least 24 characters" >&2
    failed=1
  fi
done

case "${IM_PUSH_PROVIDER:-}" in
  webhook)
    if [[ ! "${IM_PUSH_WEBHOOK_URL:-}" =~ ^https:// ]]; then
      echo "IM_PUSH_WEBHOOK_URL must use HTTPS" >&2
      failed=1
    fi
    push_webhook_token="${IM_PUSH_WEBHOOK_TOKEN:-}"
    if (( ${#push_webhook_token} < 24 )); then
      echo "IM_PUSH_WEBHOOK_TOKEN must contain at least 24 characters" >&2
      failed=1
    fi
    ;;
  getui)
    validate_getui
    ;;
  apns_voip)
    validate_apns_voip
    ;;
  getui_apns_voip)
    validate_getui
    validate_apns_voip
    ;;
  *)
    echo "IM_PUSH_PROVIDER must be webhook, getui, apns_voip, or getui_apns_voip in production" >&2
    failed=1
    ;;
esac

permissions=""
if stat -f '%Lp' "$ENV_FILE" >/dev/null 2>&1; then
  permissions="$(stat -f '%Lp' "$ENV_FILE")"
elif stat -c '%a' "$ENV_FILE" >/dev/null 2>&1; then
  permissions="$(stat -c '%a' "$ENV_FILE")"
fi
if [[ -n "$permissions" && "$permissions" != "600" && "$permissions" != "400" ]]; then
  echo "production environment file must use chmod 600 or 400, current mode: $permissions" >&2
  failed=1
fi

if (( failed != 0 )); then
  exit 1
fi

if [[ "$endpoint_mode" == "ip" ]]; then
  echo "production environment validation passed for IP $SERVER_IP"
else
  echo "production environment validation passed for domain $DOMAIN"
fi
