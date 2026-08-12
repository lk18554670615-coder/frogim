#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

python3 - "$ROOT_DIR" <<'PY'
from __future__ import annotations

import re
import sys
import os
from pathlib import Path
from urllib.parse import unquote, urlsplit

root = Path(sys.argv[1]).resolve()
# Vendored/frozen upstream sources keep their original documentation topology
# and are verified by checksums, not by this repository's relative-link rules.
ignored = {".dart_tool", ".git", ".symlinks", "Pods", "build", "dist", "node_modules", "third_party"}
link_pattern = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
failures: list[str] = []
checked = 0

markdown_files: list[Path] = []
for directory, child_directories, files in os.walk(root):
    child_directories[:] = [name for name in child_directories if name not in ignored]
    markdown_files.extend(Path(directory) / name for name in files if name.endswith(".md"))

for markdown in sorted(markdown_files):
    checked += 1
    for line_number, line in enumerate(markdown.read_text(encoding="utf-8").splitlines(), 1):
        for raw_target in link_pattern.findall(line):
            target = raw_target.strip()
            if target.startswith("<") and ">" in target:
                target = target[1:target.index(">")]
            else:
                target = target.split(maxsplit=1)[0]
            if not target or target.startswith("#"):
                continue
            parsed = urlsplit(target)
            if parsed.scheme or parsed.netloc:
                continue
            relative_path = unquote(parsed.path)
            if not relative_path:
                continue
            destination = (root / relative_path.lstrip("/")) if relative_path.startswith("/") else (markdown.parent / relative_path)
            if not destination.exists():
                source = markdown.relative_to(root)
                failures.append(f"{source}:{line_number}: missing link target: {target}")

if failures:
    print("documentation link check failed:", file=sys.stderr)
    print("\n".join(f"- {failure}" for failure in failures), file=sys.stderr)
    raise SystemExit(1)

print(f"documentation links verified: {checked} Markdown files")
PY

invalid_manager_name="WK_MANGER""TOKEN"
if grep -R -n -- "$invalid_manager_name" "$ROOT_DIR/infra" >/dev/null; then
  echo "invalid WuKongIM manager environment name: use WK_MANAGERTOKEN exactly" >&2
  exit 1
fi
for compose_file in infra/compose.wukong.yaml infra/compose.wukong.production.yaml; do
  if ! grep -q 'WK_MANAGERTOKEN:' "$ROOT_DIR/$compose_file"; then
    echo "$compose_file does not bind the pinned server managerToken environment" >&2
    exit 1
  fi
done
if ! grep -Fq 'image: ${WUKONG_IMAGE:?set WUKONG_IMAGE to the promoted repository@sha256 digest}' "$ROOT_DIR/infra/compose.wukong.production.yaml"; then
  echo "production WuKongIM must use the promoted WUKONG_IMAGE digest" >&2
  exit 1
fi
if awk '/^  wukongim:/{in_service=1; next} in_service && /^  [A-Za-z0-9_-]+:/{exit} in_service && /^[[:space:]]+build:/{found=1} END{exit found ? 0 : 1}' "$ROOT_DIR/infra/compose.wukong.production.yaml"; then
  echo "production WuKongIM must not be built on the target host" >&2
  exit 1
fi

# `app` is an internal Docker network in both production base files. Services
# that publish native client/media ports must also join `edge`; otherwise
# Docker accepts the Compose `ports` declaration but creates no host listener
# or DNAT rule (NetworkSettings.Ports remains empty).
for service in wukongim livekit; do
  if ! awk -v target="$service" '
    $0 == "  " target ":" { in_service=1; next }
    in_service && /^  [A-Za-z0-9_-]+:/ { exit }
    in_service && $0 == "      - edge" { found=1 }
    END { exit found ? 0 : 1 }
  ' "$ROOT_DIR/infra/compose.wukong.production.yaml"; then
    echo "production $service must join edge so its published ports are reachable" >&2
    exit 1
  fi
done
