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
if [[ "${WUKONG_DEV_PUBLIC_REPLACEMENT:-false}" == "true" ]]; then
  compose+=(-f "$APP_ROOT/infra/compose.wukong.production.yaml" -f "$APP_ROOT/infra/compose.ip.wukong-dev.yaml")
elif [[ "${IM_ENV:-development}" == "production" ]]; then
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
    "${compose[@]}" config -q
    if [[ "${WUKONG_DEV_PUBLIC_REPLACEMENT:-false}" == "true" ]]; then
      "${compose[@]}" up -d --no-build --remove-orphans --scale "server=${IM_REPLICAS:-1}"
    else
      "${compose[@]}" up -d --build --scale "server=${IM_REPLICAS:-1}"
    fi
    minio_init_id="$("${compose[@]}" ps -aq minio-init)"
    if [[ -z "$minio_init_id" ]]; then
      echo "minio-init 容器不存在，无法确认存储初始化结果" >&2
      exit 1
    fi
    minio_init_status="$(docker wait "$minio_init_id")"
    if [[ "$minio_init_status" != "0" ]]; then
      echo "minio-init 执行失败，退出码：$minio_init_status" >&2
      exit "$minio_init_status"
    fi
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
    exec "$APP_ROOT/infra/scripts/smoke.sh" "$CONFIG_FILE"
    ;;
  backup)
    # The installed systemd/CLI compatibility entrypoint delegates to the
    # authoritative implementation. The removed inline path omitted WuKongIM
    # data and the fixed dependency lock.
    exec "$APP_ROOT/infra/scripts/backup.sh" "$CONFIG_FILE"
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
