#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
archive="$repo_root/third_party/wukongim/cache/wukongim-server-a888f895.tar.gz"
patch_file="$repo_root/infra/wukongim/server-patch/wukongim-v2.2.5-linli.patch"
dockerfile="$repo_root/infra/wukongim/server-patch/Dockerfile"
lock_file="$repo_root/third_party/wukongim/versions.lock.json"
archive_sha="a6c225123ea99130f38c4ef828f85ff6648ccfda4e9a9acba50354ddb2965306"
patch_sha="6163ffc38a5bf4fbed2d7c94610c336708066f05df5a2bb5f49e20951f16b01a"
source_url="https://codeload.github.com/WuKongIM/WuKongIM/tar.gz/a888f89533d0e7d1b2030e06504ca97f1ad891d4"

if [[ ! -f "$archive" ]]; then
  mkdir -p "$(dirname "$archive")"
  curl --fail --location --retry 3 --output "$archive" "$source_url"
fi

echo "$archive_sha  $archive" | sha256sum -c -
echo "$patch_sha  $patch_file" | sha256sum -c -

verify_dir="$(mktemp -d)"
trap 'rm -rf -- "$verify_dir"' EXIT
tar -xzf "$archive" --strip-components=1 -C "$verify_dir"
python3 - "$lock_file" "$archive" "$patch_file" "$dockerfile" "$verify_dir" "$archive_sha" "$patch_sha" <<'PY'
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

lock_path = Path(sys.argv[1])
archive_path = Path(sys.argv[2])
patch_path = Path(sys.argv[3])
dockerfile_path = Path(sys.argv[4])
source_root = Path(sys.argv[5])
archive_sha = sys.argv[6]
patch_sha = sys.argv[7]
lock = json.loads(lock_path.read_text(encoding="utf-8"))
artifacts = {artifact["id"]: artifact for artifact in lock["artifacts"]}
source = artifacts["wukongim-server-source"]
custom = artifacts["linli-wukongim-server-image-amd64"]

def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()

def require(actual: str, expected: str, label: str) -> None:
    if actual != expected:
        raise SystemExit(f"{label} hash mismatch: expected {expected}, got {actual}")

require(digest(archive_path), str(archive_sha), "server archive")
require(digest(patch_path), str(patch_sha), "server patch")
require(source["sha256"], str(archive_sha), "versions.lock server archive")
require(source["localPatch"]["sha256"], str(patch_sha), "versions.lock server patch")
require(custom["sourceSha256"], str(archive_sha), "custom image source")
require(custom["patchSha256"], str(patch_sha), "custom image patch")
require(digest(dockerfile_path), custom["dockerfileSha256"], "custom image Dockerfile")
for relative, expected in source["localPatch"]["upstreamFiles"].items():
    require(digest(Path(source_root) / relative), expected, f"upstream {relative}")
PY
git -C "$verify_dir" apply --check "$patch_file"
git -C "$verify_dir" apply "$patch_file"
python3 - "$lock_file" "$verify_dir" <<'PY'
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

lock_path = Path(sys.argv[1])
source_root = Path(sys.argv[2])
lock = json.loads(lock_path.read_text(encoding="utf-8"))
source = next(artifact for artifact in lock["artifacts"] if artifact["id"] == "wukongim-server-source")
for relative, expected in source["localPatch"]["patchedFiles"].items():
    path = source_root / relative
    # `git apply` preserves LF on Linux/WSL but materializes the same Go text
    # as CRLF with the repository's Windows Git configuration. The lock was
    # generated from that Windows checkout, so canonicalize text files to
    # CRLF before hashing and keep the verification independent of the host.
    raw = path.read_bytes().replace(b"\r\n", b"\n").replace(b"\n", b"\r\n")
    actual = hashlib.sha256(raw).hexdigest()
    if actual != expected:
        raise SystemExit(f"patched {relative} hash mismatch: expected {expected}, got {actual}")
PY
if grep -R -E 'zap\.String\("(token|expectToken|actToken|aesKey|aesIV|sign)"' \
  "$verify_dir/internal/user/handler" >/dev/null; then
  echo "WuKongIM user handler logs must not emit tokens, message signatures, AES keys, or IVs" >&2
  exit 1
fi
if sed -n '/func (m \*message) send(c \*wkhttp.Context)/,/func (m \*message) requestSetSubscribersForTmpChannel/p' \
  "$verify_dir/internal/api/message.go" | grep -E 'wkutil\.ToJSON\(req\)|zap\.Any\("req", req\)|zap\.Strings\("subscribers"' >/dev/null; then
  echo "WuKongIM send API logs must not serialize message payloads or subscriber IDs" >&2
  exit 1
fi
if grep -E 'zap\.Any\("req", req\)' "$verify_dir/internal/api/user.go" >/dev/null; then
  echo "WuKongIM token update logs must not serialize the request token" >&2
  exit 1
fi
if grep -E 'zap\.(ByteString|Binary)\("data", frameData\)' "$verify_dir/internal/server/proto.go" >/dev/null; then
  echo "WuKongIM protocol logs must not emit raw unauthenticated frames" >&2
  exit 1
fi

windows_docker="/mnt/c/Program Files/Docker/Docker/resources/bin/docker.exe"
if grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null && [[ -x "$windows_docker" ]] && command -v wslpath >/dev/null 2>&1; then
  "/mnt/c/Program Files/Docker/Docker/resources/bin/docker.exe" build \
    --platform linux/amd64 \
    --target verify \
    --file "$(wslpath -w "$dockerfile")" \
    "$(wslpath -w "$repo_root")"
elif command -v docker >/dev/null 2>&1; then
  docker build --platform linux/amd64 --target verify --file "$dockerfile" "$repo_root"
else
  echo "Docker CLI is required to run the Linux patch tests" >&2
  exit 1
fi

echo "WuKongIM pinned source patch verification passed"
