#!/usr/bin/env bash
set -euo pipefail

# Read-only WuKongIM production-cutover audit. This script never copies files,
# starts/stops containers, changes configuration, or writes to the target host.
TARGET_HOST="${1:?usage: $0 HOST [CONFIG_FILE] [CURRENT_DIR]}"
CONFIG_FILE="${2:-/data/linli-im/shared/config.env}"
CURRENT_DIR="${3:-/opt/nexachat/current}"
SSH_USER="${SSH_USER:-root}"

if [[ ! "$TARGET_HOST" =~ ^[A-Za-z0-9._:-]+$ || ! "$SSH_USER" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]]; then
  echo "invalid SSH target" >&2
  exit 2
fi
for path in "$CONFIG_FILE" "$CURRENT_DIR"; do
  if [[ "$path" != /* || "$path" == *$'\n'* || "$path" == *$'\r'* ]]; then
    echo "remote paths must be absolute single-line paths" >&2
    exit 2
  fi
done

ssh_bin="${SSH_BIN:-ssh}"
ssh_options=(
  -o BatchMode=yes
  -o ConnectTimeout=10
  -o ServerAliveInterval=15
  -o ServerAliveCountMax=2
)
# Git Bash may use /home/<user> while Windows OpenSSH keys live under
# C:/Users/<user>. Reuse that existing trust store when it is available.
windows_user="${USERNAME:-$(id -un)}"
windows_ssh_dir=""
for candidate in "/mnt/c/Users/$windows_user/.ssh" "/c/Users/$windows_user/.ssh"; do
  if [[ -d "$candidate" ]]; then
    windows_ssh_dir="$candidate"
    break
  fi
done
if [[ -n "$windows_ssh_dir" ]]; then
  if [[ -x /mnt/c/Windows/System32/OpenSSH/ssh.exe && -z "${SSH_BIN:-}" ]]; then
    ssh_bin=/mnt/c/Windows/System32/OpenSSH/ssh.exe
    windows_ssh_native="C:/Users/$windows_user/.ssh"
    known_hosts_file="${SSH_KNOWN_HOSTS_FILE:-$windows_ssh_native/known_hosts}"
    identity_file="${SSH_IDENTITY_FILE:-$windows_ssh_native/id_ed25519_$(printf '%s' "$TARGET_HOST" | tr -c 'A-Za-z0-9' '_')}"
  else
    known_hosts_file="${SSH_KNOWN_HOSTS_FILE:-$windows_ssh_dir/known_hosts}"
    identity_file="${SSH_IDENTITY_FILE:-}"
  fi
  if [[ "$ssh_bin" == *.exe || -f "$known_hosts_file" ]]; then
    ssh_options+=(-o "UserKnownHostsFile=$known_hosts_file")
  fi
  if [[ -z "$identity_file" ]]; then
    identity_suffix="$(printf '%s' "$TARGET_HOST" | tr -c 'A-Za-z0-9' '_')"
    candidate="$windows_ssh_dir/id_ed25519_${identity_suffix}"
    [[ -f "$candidate" ]] && identity_file="$candidate"
  fi
  [[ -n "$identity_file" ]] && ssh_options+=(-i "$identity_file" -o IdentitiesOnly=yes)
fi

set +e
"$ssh_bin" "${ssh_options[@]}" "$SSH_USER@$TARGET_HOST" bash -s -- "$CONFIG_FILE" "$CURRENT_DIR" <<'REMOTE'
set -uo pipefail

config_file="$1"
current_dir="$2"
blockers=0
warnings=0

pass() { printf 'PASS  %s\n' "$1"; }
warn() { printf 'WARN  %s\n' "$1"; warnings=$((warnings + 1)); }
block() { printf 'BLOCK %s\n' "$1"; blockers=$((blockers + 1)); }
info() { printf 'INFO  %s\n' "$1"; }

printf 'WuKongIM production cutover preflight (read-only)\n'
info "host=$(hostname) kernel=$(uname -r)"

if ! command -v docker >/dev/null 2>&1; then
  block "Docker is not installed"
else
  pass "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo unknown) is reachable"
fi
if docker compose version >/dev/null 2>&1; then
  pass "Docker Compose $(docker compose version --short 2>/dev/null || echo unknown) is reachable"
else
  block "Docker Compose is not reachable"
fi

declare -A values=()
if [[ ! -f "$config_file" ]]; then
  block "production environment file is missing: $config_file"
else
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# || "$line" != *=* ]] && continue
    key="${line%%=*}"
    value="${line#*=}"
    if (( ${#value} >= 2 )) && { [[ "$value" == \'*\' ]] || [[ "$value" == \"*\" ]]; }; then
      value="${value:1:${#value}-2}"
    fi
    values["$key"]="$value"
  done < "$config_file"
  pass "environment file exists: $config_file"
fi

required_keys=(
  PRODUCTION_ENDPOINT_MODE SERVER_IP TLS_EMAIL TERMS_URL PRIVACY_URL
  IM_ENV IM_DEV_MODE LINLI_DATA_ROOT BACKUP_DIR BACKUP_METRICS_DIR BACKUP_OFFSITE_ENABLED
  WUKONG_IMAGE WUKONG_EXTERNAL_IP IM_WUKONG_MANAGER_TOKEN IM_WUKONG_TOKEN_SECRET
  IM_WUKONG_POLICY_SECRET IM_WUKONG_TCP_URL IM_WUKONG_WS_URL
  IM_WUKONG_PLUGIN_TRUSTED_KEYS IM_WUKONG_PLUGIN_ALLOWLIST
  LIVEKIT_API_KEY LIVEKIT_API_SECRET IM_LIVEKIT_URL
  MINIO_APP_USER MINIO_APP_PASSWORD IM_OTP_WEBHOOK_URL IM_OTP_WEBHOOK_TOKEN
)
missing=()
for key in "${required_keys[@]}"; do
  value="${values[$key]:-}"
  if [[ -z "$value" || "$value" == *REPLACE_WITH* || "$value" == *change-this* || "$value" == *example.com* || "$value" == 203.0.113.* ]]; then
    missing+=("$key")
  fi
done
if (( ${#missing[@]} == 0 )); then
  pass "all cutover configuration categories are populated"
else
  block "missing or placeholder configuration keys: ${missing[*]}"
fi

[[ "${values[IM_ENV]:-}" == production ]] && pass "IM_ENV=production" || block "IM_ENV must be production"
[[ "${values[IM_DEV_MODE]:-}" == false ]] && pass "development mode is disabled" || block "IM_DEV_MODE must be false"
[[ "${values[WUKONG_IMAGE]:-}" =~ @sha256:[0-9a-f]{64}$ ]] && pass "WuKongIM image uses an immutable digest" || block "WUKONG_IMAGE is not an immutable digest"
[[ "${values[IM_WUKONG_WS_URL]:-}" == wss://* ]] && pass "WuKongIM WSS endpoint is configured" || block "WuKongIM WSS endpoint is missing"
[[ "${values[IM_LIVEKIT_URL]:-}" == wss://* ]] && pass "LiveKit WSS endpoint is configured" || block "LiveKit WSS endpoint is missing"

data_root="${values[LINLI_DATA_ROOT]:-/data/linli-im/data}"
if [[ ! -d "$data_root" ]]; then
  block "data root is missing: $data_root"
else
  read -r disk_total disk_used disk_available disk_percent < <(df -PB1 "$data_root" | awk 'NR==2 {gsub(/%/,"",$5); print $2,$3,$4,$5}')
  info "data filesystem total=$disk_total used=$disk_used available=$disk_available usedPercent=$disk_percent"
  require_1tib_disk="${values[WUKONG_REQUIRE_1TIB_DISK]:-true}"
  if [[ "$require_1tib_disk" != true && "$require_1tib_disk" != false ]]; then
    block "WUKONG_REQUIRE_1TIB_DISK must be true or false"
    require_1tib_disk=true
  fi
  if [[ "$disk_total" =~ ^[0-9]+$ ]] && (( disk_total >= 1099511627776 )); then
    pass "data filesystem is at least 1 TiB"
  elif [[ "$require_1tib_disk" == false ]]; then
    warn "data filesystem is below 1 TiB; the deployment owner explicitly waived this capacity gate"
  else
    block "data filesystem is below the approved 1 TiB gate"
  fi
  if [[ "$disk_percent" =~ ^[0-9]+$ ]] && (( disk_percent < 70 )); then
    pass "data filesystem usage is below 70%"
  else
    block "data filesystem usage is at or above 70%"
  fi
fi

if command -v free >/dev/null 2>&1; then
  read -r memory_total memory_used < <(free -b | awk '/^Mem:/ {print $2,$3}')
  memory_percent=$((memory_total > 0 ? memory_used * 100 / memory_total : 100))
  info "memory total=$memory_total used=$memory_used usedPercent=$memory_percent"
  (( memory_percent < 80 )) && pass "host memory usage is below 80%" || block "host memory usage is at or above 80%"
fi

if [[ -L "$current_dir" || -d "$current_dir" ]]; then
  resolved_current="$(readlink -f "$current_dir" 2>/dev/null || printf '%s' "$current_dir")"
  pass "current release exists: $resolved_current"
else
  block "current release is missing: $current_dir"
  resolved_current="$current_dir"
fi
for file in infra/compose.ip.yaml infra/compose.ip.production.yaml infra/compose.wukong.production.yaml infra/scripts/deploy-ip-production.sh; do
  [[ -f "$resolved_current/$file" ]] && pass "release contains $file" || block "release does not contain $file"
done

if command -v docker >/dev/null 2>&1; then
  mapfile -t running_services < <(docker ps --format '{{.Label "com.docker.compose.project"}}|{{.Label "com.docker.compose.service"}}' | sort -u)
  info "running Compose services: ${running_services[*]:-none}"
  service_names="$(printf '%s\n' "${running_services[@]:-}" | cut -d'|' -f2 | sort -u | tr '\n' ' ')"
  for expected in gateway web server admin postgres redis minio; do
    [[ " $service_names " == *" $expected "* ]] && pass "current $expected service is running" || block "current $expected service is not running"
  done
  [[ " $service_names " == *" coturn "* ]] && warn "legacy Coturn is still running; remove only after new RTC acceptance" || pass "legacy Coturn is not running"
  [[ " $service_names " == *" wukongim "* ]] && pass "WuKongIM container is running" || warn "WuKongIM container is not running yet"
  [[ " $service_names " == *" livekit "* ]] && pass "LiveKit container is running" || warn "LiveKit container is not running yet"
  [[ " $service_names " == *" prometheus "* ]] && pass "Prometheus container is running" || warn "Prometheus container is not running yet"
fi

backup_root="${values[BACKUP_DIR]:-/data/linli-im/backups}"
if [[ ! -d "$backup_root" && -d /data/linli-im/backups ]]; then
  warn "configured BACKUP_DIR does not exist: $backup_root; auditing discovered /data/linli-im/backups instead"
  backup_root=/data/linli-im/backups
fi
latest_backup=""
if [[ -d "$backup_root" ]]; then
  latest_backup="$(find "$backup_root" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | grep -E '^[0-9]{8}T[0-9]{6}Z$' | sort | tail -n 1 || true)"
fi
if [[ -z "$latest_backup" && "$backup_root" != /data/linli-im/backups && -d /data/linli-im/backups ]]; then
  discovered_backup="$(find /data/linli-im/backups -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | grep -E '^[0-9]{8}T[0-9]{6}Z$' | sort | tail -n 1 || true)"
  if [[ -n "$discovered_backup" ]]; then
    warn "configured BACKUP_DIR has no published generation: $backup_root; auditing discovered /data/linli-im/backups instead"
    backup_root=/data/linli-im/backups
    latest_backup="$discovered_backup"
  fi
fi
if [[ -z "$latest_backup" ]]; then
  block "no published backup generation exists under $backup_root"
else
  latest_path="$backup_root/$latest_backup"
  info "latest published backup=$latest_path"
  [[ -s "$latest_path/postgres.dump" ]] && pass "latest backup contains PostgreSQL" || block "latest backup lacks postgres.dump"
  [[ -s "$latest_path/SHA256SUMS" ]] && pass "latest backup contains checksums" || block "latest backup lacks SHA256SUMS"
  if [[ -s "$latest_path/SHA256SUMS" ]] && (cd "$latest_path" && sha256sum -c SHA256SUMS --status); then
    pass "latest backup checksum verification passed"
  else
    block "latest backup checksum verification failed"
  fi
  postgres_container="$(docker ps --filter label=com.docker.compose.service=postgres --format '{{.ID}}' | head -n 1)"
  if command -v pg_restore >/dev/null 2>&1 && pg_restore -l "$latest_path/postgres.dump" >/dev/null 2>&1; then
    pass "latest PostgreSQL archive is readable"
  elif [[ -n "$postgres_container" ]] && docker exec -i "$postgres_container" pg_restore -l < "$latest_path/postgres.dump" >/dev/null 2>&1; then
    pass "latest PostgreSQL archive is readable through the running PostgreSQL container"
  else
    block "latest PostgreSQL archive is unreadable"
  fi
  backup_age=$(( $(date +%s) - $(stat -c %Y "$latest_path") ))
  (( backup_age <= 86400 )) && pass "latest backup is no older than 24 hours" || block "latest backup is older than 24 hours"
  if [[ -s "$latest_path/wukongim-data.tar.gz" && -s "$latest_path/versions.lock.json" ]]; then
    pass "latest backup includes WuKongIM data and dependency lock"
  else
    warn "latest backup is a legacy pre-WuKong snapshot; create and restore-check a final snapshot in the maintenance window"
  fi
fi

if [[ -n "${values[WUKONG_PERFORMANCE_EVIDENCE]:-}" && -s "${values[WUKONG_PERFORMANCE_EVIDENCE]}" ]]; then
  pass "formal 10k/1k performance evidence is referenced"
else
  block "formal 10k connections / 1k msg/s performance evidence is not present"
fi

printf 'SUMMARY blockers=%d warnings=%d\n' "$blockers" "$warnings"
(( blockers == 0 ))
REMOTE
status=$?
set -e
exit "$status"
