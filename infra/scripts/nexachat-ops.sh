#!/usr/bin/env bash
set -euo pipefail
umask 077

# 兼容入口：已部署服务器仍以 nexachat 作为命令和路径标识；新入口 linli-im 委托到本脚本。

APP_ROOT="${NEXACHAT_ROOT:-/opt/nexachat/current}"
DEFAULT_CONFIG="/data/linli-im/shared/config.env"
if [[ ! -f "$DEFAULT_CONFIG" && -f /opt/nexachat/shared/config.env ]]; then
  DEFAULT_CONFIG="/opt/nexachat/shared/config.env"
fi
CONFIG_FILE="${NEXACHAT_CONFIG:-$DEFAULT_CONFIG}"
COMPOSE_FILE="$APP_ROOT/infra/compose.ip.yaml"

if [[ ! -s "$CONFIG_FILE" ]]; then
  echo "missing configuration: $CONFIG_FILE" >&2
  exit 1
fi

# shellcheck disable=SC1090
source "$APP_ROOT/infra/scripts/load-env.sh"
load_env_file "$CONFIG_FILE"

compose=(docker compose --env-file "$CONFIG_FILE" -f "$COMPOSE_FILE")
if [[ "${IM_ENV:-development}" == "production" ]]; then
  "$APP_ROOT/infra/scripts/validate-production-env.sh" "$CONFIG_FILE"
  compose+=(-f "$APP_ROOT/infra/compose.ip.production.yaml")
  if [[ "${IM_PUSH_PROVIDER:-}" == "apns_voip" || "${IM_PUSH_PROVIDER:-}" == "getui_apns_voip" ]]; then
    compose+=(-f "$APP_ROOT/infra/compose.apns-voip.yaml")
  fi
fi

case "${1:-help}" in
  deploy)
    if [[ "${IM_ENV:-development}" == "production" ]]; then
      exec "$APP_ROOT/infra/scripts/deploy-ip-production.sh" "$CONFIG_FILE"
    fi
    "${compose[@]}" up -d --build --scale "server=${IM_REPLICAS:-1}"
    "${compose[@]}" wait minio-init
    "${compose[@]}" rm -f minio-init
    ;;
  restart)
    "${compose[@]}" up -d --force-recreate --scale "server=${IM_REPLICAS:-1}" server admin gateway
    ;;
  status)
    "${compose[@]}" ps
    ;;
  logs)
    "${compose[@]}" logs --tail="${2:-200}" "${3:-server}"
    ;;
  logs-all)
    "${compose[@]}" logs --timestamps --tail="${2:-300}"
    ;;
  logs-errors)
    "${compose[@]}" logs --timestamps --since="${2:-30m}" 2>&1 | grep -Ei 'error|failed|fatal|panic|timeout|unhealthy|拒绝|失败|异常|超时' || true
    ;;
  logs-export)
    timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
    destination="${LOG_ARCHIVE_DIR:-/data/linli-im/logs/archive}/$timestamp"
    mkdir -p "$destination"
    chmod 700 "$destination"
    while IFS= read -r service; do
      "${compose[@]}" logs --no-color --timestamps --since="${2:-24h}" "$service" > "$destination/$service.log" 2>&1 || true
      gzip -9 "$destination/$service.log"
    done < <("${compose[@]}" config --services)
    "${compose[@]}" ps --format 'table {{.Name}}\t{{.Image}}\t{{.Status}}' > "$destination/containers.txt"
    (cd "$destination" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum) > "$destination/SHA256SUMS"
    chmod -R go-rwx "$destination"
    echo "日志归档已完成：$destination"
    ;;
  smoke)
    curl --fail --silent --show-error "https://$SERVER_IP/health"
    echo
    curl --fail --silent --show-error "https://$SERVER_IP/healthz"
    echo
    ;;
  backup)
    timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
    destination="${BACKUP_DIR:-/opt/nexachat/backups}/$timestamp"
    working="${BACKUP_DIR:-/opt/nexachat/backups}/.incomplete-$timestamp"
    [[ ! -e "$destination" && ! -e "$working" ]] || { echo "backup destination already exists" >&2; exit 1; }
    mkdir -p -m 700 "$working/minio"
    backup_complete=false
    trap 'if [[ "$backup_complete" != true ]]; then echo "backup incomplete: $working" >&2; fi' EXIT
    "${compose[@]}" exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl > "$working/postgres.dump"
    "${compose[@]}" --profile ops run --rm --no-deps minio-client -c '
      mc alias set source http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
      mc mirror --preserve "source/$IM_S3_BUCKET" "/backup/.incomplete-'"$timestamp"'/minio"
    '
    (cd "$working" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum) > "$working/SHA256SUMS"
    chmod -R go-rwx "$working"
    mv "$working" "$destination"
    backup_complete=true
    echo "backup completed: $destination"
    ;;
  renew-cert)
    docker run --rm \
      -v "$CERTBOT_DIR:/etc/letsencrypt" \
      -v "$CERTBOT_WEBROOT:/var/www/certbot" \
      "${CERTBOT_IMAGE:-certbot/certbot:latest}" renew --quiet
    "${compose[@]}" exec -T gateway caddy reload --config /etc/caddy/Caddyfile
    ;;
  issue-cert)
    exec "$APP_ROOT/infra/scripts/issue-ip-certificate.sh" "$CONFIG_FILE"
    ;;
  help|*)
    echo "用法：linli-im {deploy|restart|status|logs [行数] [服务]|logs-all [行数]|logs-errors [时间]|logs-export [时间]|smoke|backup|issue-cert|renew-cert}"
    ;;
esac
