#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "Apple release builds require a macOS runner" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
MOBILE_DIR="$ROOT_DIR/apps/mobile"
TARGET="${1:-all}"
SERVER_ORIGIN="${SERVER_ORIGIN:-}"
TERMS_URL="${TERMS_URL:-${SERVER_ORIGIN%/}/legal/terms}"
PRIVACY_URL="${PRIVACY_URL:-${SERVER_ORIGIN%/}/legal/privacy}"

if [[ "$TARGET" != "ios" && "$TARGET" != "macos" && "$TARGET" != "all" ]]; then
  echo "usage: SERVER_ORIGIN=https://host $0 [ios|macos|all]" >&2
  exit 2
fi
if [[ ! "$SERVER_ORIGIN" =~ ^https://[^/?#]+$ ]]; then
  echo "SERVER_ORIGIN must be an HTTPS origin without path, query or fragment" >&2
  exit 2
fi
for url in "$SERVER_ORIGIN/health" "$TERMS_URL" "$PRIVACY_URL"; do
  if [[ ! "$url" =~ ^https:// ]]; then
    echo "release URL must use HTTPS: $url" >&2
    exit 2
  fi
  curl --fail --silent --show-error --location --max-time 20 --output /dev/null "$url"
done

if command -v fvm >/dev/null 2>&1; then
  flutter=(fvm flutter)
elif [[ -x "$MOBILE_DIR/.fvm/flutter_sdk/bin/flutter" ]]; then
  flutter=("$MOBILE_DIR/.fvm/flutter_sdk/bin/flutter")
else
  echo "FVM Flutter is missing; run 'fvm install' in apps/mobile" >&2
  exit 2
fi

version="$("${flutter[@]}" --version --machine | python3 -c 'import json,sys; print(json.load(sys.stdin)["frameworkVersion"])')"
if [[ "$version" != "3.44.8" ]]; then
  echo "Flutter 3.44.8 is required, found $version" >&2
  exit 2
fi

defines=(
  --dart-define=APP_ENV=production
  --dart-define="API_BASE_URL=$SERVER_ORIGIN"
  --dart-define="WS_URL=${SERVER_ORIGIN/https:/wss:}/im"
  --dart-define=ENABLE_DEMO=false
  --dart-define="TERMS_URL=$TERMS_URL"
  --dart-define="PRIVACY_URL=$PRIVACY_URL"
  --dart-define=MEDIA_MAX_BYTES=104857600
)

cd "$MOBILE_DIR"
"${flutter[@]}" pub get
"${flutter[@]}" analyze
if [[ "$TARGET" == "ios" || "$TARGET" == "all" ]]; then
  "${flutter[@]}" build ios --release --no-codesign "${defines[@]}"
fi
if [[ "$TARGET" == "macos" || "$TARGET" == "all" ]]; then
  "${flutter[@]}" build macos --release "${defines[@]}"
fi

echo "Apple release build completed for $TARGET"
