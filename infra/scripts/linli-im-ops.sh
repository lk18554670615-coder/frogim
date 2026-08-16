#!/usr/bin/env bash
set -euo pipefail

# 青蛙呱呱的新技术命名入口；委托给兼容脚本，避免破坏既有部署和自动化。
# 发布包可能经不保留 POSIX mode 的工具从 Windows 传到 Linux，因此这里
# 明确交给 bash 解释，同时兼容脚本被复制到 /usr/local/bin 的安装方式。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_ROOT="${NEXACHAT_ROOT:-/opt/nexachat/current}"
TARGET="$SCRIPT_DIR/nexachat-ops.sh"
if [[ ! -f "$TARGET" ]]; then
  TARGET="$APP_ROOT/infra/scripts/nexachat-ops.sh"
fi
exec bash "$TARGET" "$@"
