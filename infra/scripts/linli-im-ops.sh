#!/usr/bin/env bash
set -euo pipefail

# 邻里通讯的新技术命名入口；委托给兼容脚本，避免破坏既有部署和自动化。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$SCRIPT_DIR/nexachat-ops.sh" "$@"
