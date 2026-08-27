#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="${1:-$ROOT_DIR/.env.remote-test}"

# shellcheck disable=SC1091
source "$ROOT_DIR/infra/scripts/load-env.sh"
load_env_file "$ENV_FILE"

: "${REMOTE_SERVER_ORIGIN:?缺少 REMOTE_SERVER_ORIGIN}"
: "${REMOTE_API_BASE_URL:?缺少 REMOTE_API_BASE_URL}"

[[ "$REMOTE_SERVER_ORIGIN" == https://* ]] || { echo "远程地址必须使用 HTTPS" >&2; exit 2; }
[[ "$REMOTE_API_BASE_URL" == "$REMOTE_SERVER_ORIGIN" ]] || { echo "API 地址必须与远程服务入口一致" >&2; exit 2; }

curl --fail --silent --show-error --max-time 10 "$REMOTE_SERVER_ORIGIN/health" >/dev/null
curl --fail --silent --show-error --max-time 10 "$REMOTE_SERVER_ORIGIN/ready" >/dev/null

echo "远程前端联调配置有效：HTTPS API 与健康检查均通过；WuKongIM 地址由登录 ImSession 下发"
