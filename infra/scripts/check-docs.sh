#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

python3 - "$ROOT_DIR" <<'PY'
from __future__ import annotations

import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

root = Path(sys.argv[1]).resolve()
ignored = {".dart_tool", ".git", ".symlinks", "Pods", "build", "dist", "node_modules"}
link_pattern = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
failures: list[str] = []
checked = 0

for markdown in sorted(root.rglob("*.md")):
    if any(part in ignored for part in markdown.relative_to(root).parts):
        continue
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
