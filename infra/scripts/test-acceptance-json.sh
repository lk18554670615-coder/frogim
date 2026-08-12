#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

expected='验收用户甲已更新'
payload="$(python3 "$SCRIPT_DIR/acceptance-json.py" -nc --arg name "$expected" '{name:$name}')"
printf '%s' "$payload" > "$TMP_DIR/payload.json"

actual="$(python3 "$SCRIPT_DIR/acceptance-json.py" -r '.name' "$TMP_DIR/payload.json")"
if [[ "$actual" != "$expected" ]]; then
  echo "acceptance JSON UTF-8 round trip failed: expected=$expected actual=$actual" >&2
  exit 1
fi

python3 - "$TMP_DIR/payload.json" <<'PY'
import json
import sys
from pathlib import Path

payload = Path(sys.argv[1]).read_bytes()
decoded = json.loads(payload.decode("utf-8"))
if decoded != {"name": "验收用户甲已更新"}:
    raise SystemExit(f"unexpected acceptance JSON payload: {decoded!r}")
PY

echo "acceptance JSON UTF-8 round trip verified"
