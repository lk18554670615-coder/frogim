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
  TERMS_URL PRIVACY_URL
  MINIO_ROOT_USER MINIO_ROOT_PASSWORD MINIO_APP_USER MINIO_APP_PASSWORD
  LINLI_DATA_ROOT BACKUP_DIR BACKUP_METRICS_DIR BACKUP_OFFSITE_ENABLED WUKONG_IMAGE IM_WUKONG_MANAGER_TOKEN IM_WUKONG_TOKEN_SECRET IM_WUKONG_POLICY_SECRET
  IM_WUKONG_PLUGIN_TRUSTED_KEYS IM_WUKONG_PLUGIN_ALLOWLIST IM_WUKONG_PLUGIN_MAX_BYTES
  IM_WUKONG_TCP_URL IM_WUKONG_WS_URL WUKONG_EXTERNAL_IP
  LIVEKIT_API_KEY LIVEKIT_API_SECRET IM_LIVEKIT_URL
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

for key in IM_WUKONG_TOKEN_SECRET IM_WUKONG_POLICY_SECRET LIVEKIT_API_SECRET; do
  value="${!key:-}"
  if (( ${#value} < 32 )); then
    echo "$key must contain at least 32 characters" >&2
    failed=1
  fi
done
if [[ "${IM_WUKONG_POLICY_SECRET:-}" == "${IM_WUKONG_TOKEN_SECRET:-}" || "${IM_WUKONG_POLICY_SECRET:-}" == "${IM_WUKONG_MANAGER_TOKEN:-}" ]]; then
  echo "IM_WUKONG_POLICY_SECRET must be independent from WuKongIM token and manager secrets" >&2
  failed=1
fi
manager_token="${IM_WUKONG_MANAGER_TOKEN:-}"
if (( ${#manager_token} < 24 )); then
  echo "IM_WUKONG_MANAGER_TOKEN must contain at least 24 characters" >&2
  failed=1
fi
livekit_key="${LIVEKIT_API_KEY:-}"
if (( ${#livekit_key} < 3 )); then
  echo "LIVEKIT_API_KEY must contain at least 3 characters" >&2
  failed=1
fi

decode_base64() {
  if base64 --help 2>&1 | grep -q -- '--decode'; then
    base64 --decode
  else
    base64 -D
  fi
}

validate_wukong_plugin_trust() {
  local trusted_keys="${IM_WUKONG_PLUGIN_TRUSTED_KEYS:-}"
  local allowlist="${IM_WUKONG_PLUGIN_ALLOWLIST:-}"
  local max_bytes="${IM_WUKONG_PLUGIN_MAX_BYTES:-}"
  local entry key_id encoded decoded_size plugin_no policy_allowed=0
  local -a entries plugins

  if [[ ! "$max_bytes" =~ ^[0-9]+$ ]] || (( max_bytes < 1048576 || max_bytes > 536870912 )); then
    echo "IM_WUKONG_PLUGIN_MAX_BYTES must be an integer between 1048576 and 536870912" >&2
    failed=1
  fi

  if [[ "$trusted_keys" == ,* || "$trusted_keys" == *, || "$trusted_keys" == *,,* ]]; then
    echo "IM_WUKONG_PLUGIN_TRUSTED_KEYS must not contain empty entries" >&2
    failed=1
  fi
  IFS=',' read -r -a entries <<< "$trusted_keys"
  for entry in "${entries[@]}"; do
    key_id="${entry%%:*}"
    encoded="${entry#*:}"
    if [[ "$entry" != *:* || ! "$key_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{2,127}$ ]]; then
      echo "IM_WUKONG_PLUGIN_TRUSTED_KEYS contains an invalid key id or entry" >&2
      failed=1
      continue
    fi
    if [[ ! "$encoded" =~ ^[A-Za-z0-9+/]+={0,2}$ ]] || (( ${#encoded} % 4 != 0 )); then
      echo "IM_WUKONG_PLUGIN_TRUSTED_KEYS key $key_id is not canonical Base64" >&2
      failed=1
      continue
    fi
    if ! decoded_size="$(printf '%s' "$encoded" | decode_base64 2>/dev/null | wc -c | tr -d '[:space:]')"; then
      echo "IM_WUKONG_PLUGIN_TRUSTED_KEYS key $key_id cannot be decoded" >&2
      failed=1
    elif [[ "$decoded_size" != "32" ]]; then
      echo "IM_WUKONG_PLUGIN_TRUSTED_KEYS key $key_id must decode to exactly 32 bytes" >&2
      failed=1
    fi
  done

  if [[ "$allowlist" == ,* || "$allowlist" == *, || "$allowlist" == *,,* ]]; then
    echo "IM_WUKONG_PLUGIN_ALLOWLIST must not contain empty entries" >&2
    failed=1
  fi
  IFS=',' read -r -a plugins <<< "$allowlist"
  for plugin_no in "${plugins[@]}"; do
    if [[ ! "$plugin_no" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{2,127}$ ]]; then
      echo "IM_WUKONG_PLUGIN_ALLOWLIST contains an invalid plugin number" >&2
      failed=1
    elif [[ "$plugin_no" == "wk.plugin.im-policy" ]]; then
      policy_allowed=1
    fi
  done
  if (( policy_allowed == 0 )); then
    echo "IM_WUKONG_PLUGIN_ALLOWLIST must include the mandatory wk.plugin.im-policy plugin" >&2
    failed=1
  fi
}

validate_wukong_plugin_trust

if [[ ! "${IM_WUKONG_TCP_URL:-}" =~ ^tcp://[^[:space:]]+$ ]]; then
  echo "IM_WUKONG_TCP_URL must be an absolute tcp:// URL" >&2
  failed=1
fi
if [[ ! "${IM_WUKONG_WS_URL:-}" =~ ^wss://[^[:space:]]+$ ]]; then
  echo "IM_WUKONG_WS_URL must be an absolute wss:// URL" >&2
  failed=1
fi
if [[ ! "${IM_LIVEKIT_URL:-}" =~ ^wss://[^[:space:]]+$ ]]; then
  echo "IM_LIVEKIT_URL must be an absolute wss:// URL" >&2
  failed=1
fi
if [[ ! "${WUKONG_EXTERNAL_IP:-}" =~ ^[0-9A-Fa-f:.]+$ ]]; then
  echo "WUKONG_EXTERNAL_IP must be a literal public IP address" >&2
  failed=1
fi
if [[ ! "${WUKONG_IMAGE:-}" =~ ^[^@[:space:]]+@sha256:[0-9a-f]{64}$ ]]; then
  echo "WUKONG_IMAGE must be an immutable promoted repository@sha256 digest" >&2
  failed=1
fi

require_1tib_disk="${WUKONG_REQUIRE_1TIB_DISK:-true}"
case "$require_1tib_disk" in
  true|false) ;;
  *)
    echo "WUKONG_REQUIRE_1TIB_DISK must be true or false" >&2
    failed=1
    require_1tib_disk=true
    ;;
esac

if [[ "${LINLI_DATA_ROOT:-}" != /* || "${LINLI_DATA_ROOT:-}" == "/" ]]; then
  echo "LINLI_DATA_ROOT must be an absolute non-root directory" >&2
  failed=1
elif [[ ! -d "$LINLI_DATA_ROOT" ]]; then
  echo "LINLI_DATA_ROOT does not exist: $LINLI_DATA_ROOT" >&2
  failed=1
else
  disk_kib="$(df -Pk "$LINLI_DATA_ROOT" | awk 'NR==2 {print $2}')"
  if [[ ! "$disk_kib" =~ ^[0-9]+$ ]]; then
    echo "could not determine the production data filesystem size" >&2
    failed=1
  elif (( disk_kib < 1073741824 )); then
    if [[ "$require_1tib_disk" == true ]]; then
      echo "the production data filesystem must contain at least 1 TiB before WuKongIM cutover" >&2
      failed=1
    else
      echo "warning: the 1 TiB data filesystem gate is explicitly waived for this deployment" >&2
    fi
  fi
fi

normalized_backup_dir="${BACKUP_DIR:-}"
normalized_backup_dir="${normalized_backup_dir%/}"
if [[ "$normalized_backup_dir" != /* || "$normalized_backup_dir" == "/" ]]; then
  echo "BACKUP_DIR must be an absolute non-root directory" >&2
  failed=1
elif [[ "${BACKUP_METRICS_DIR:-}" != "$normalized_backup_dir/.metrics" ]]; then
  echo "BACKUP_METRICS_DIR must be exactly $normalized_backup_dir/.metrics" >&2
  failed=1
fi

case "${BACKUP_OFFSITE_ENABLED:-}" in
  true)
    offsite_access_key="${BACKUP_OFFSITE_ACCESS_KEY:-}"
    offsite_secret_key="${BACKUP_OFFSITE_SECRET_KEY:-}"
    if [[ ! "${BACKUP_OFFSITE_ENDPOINT:-}" =~ ^https://[^[:space:]]+$ ]]; then
      echo "BACKUP_OFFSITE_ENDPOINT must use HTTPS when off-site backup is enabled" >&2
      failed=1
    fi
    if (( ${#offsite_access_key} < 8 )); then
      echo "BACKUP_OFFSITE_ACCESS_KEY must contain at least 8 characters" >&2
      failed=1
    fi
    if (( ${#offsite_secret_key} < 16 )); then
      echo "BACKUP_OFFSITE_SECRET_KEY must contain at least 16 characters" >&2
      failed=1
    fi
    if [[ ! "${BACKUP_OFFSITE_BUCKET:-}" =~ ^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$ ]]; then
      echo "BACKUP_OFFSITE_BUCKET must be a valid lowercase S3 bucket name" >&2
      failed=1
    fi
    offsite_prefix="${BACKUP_OFFSITE_PREFIX:-}"
    if [[ "$offsite_prefix" == /* || "$offsite_prefix" == */ || "$offsite_prefix" == *..* || ! "$offsite_prefix" =~ ^[A-Za-z0-9._/-]*$ ]]; then
      echo "BACKUP_OFFSITE_PREFIX must be a relative safe object prefix" >&2
      failed=1
    fi
    ;;
  false) ;;
  *)
    echo "BACKUP_OFFSITE_ENABLED must be true or false" >&2
    failed=1
    ;;
esac

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

for key in TERMS_URL PRIVACY_URL; do
  value="${!key:-}"
  if [[ ! "$value" =~ ^https:// ]]; then
    echo "$key must use HTTPS" >&2
    failed=1
  elif [[ "$value" == *example.com* || "$value" == *203.0.113.* ]]; then
    echo "$key must not use a documentation placeholder address" >&2
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
