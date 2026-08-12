#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="${REMOTE_TEST_ENV:-$ROOT_DIR/.env.remote-test}"

"$ROOT_DIR/infra/scripts/validate-remote-test-env.sh" "$ENV_FILE"
# shellcheck disable=SC1091
source "$ROOT_DIR/infra/scripts/load-env.sh"
load_env_file "$ENV_FILE"

cd "$ROOT_DIR/apps/mobile"
exec flutter run \
  --dart-define=APP_ENV=staging \
  --dart-define=API_BASE_URL="$REMOTE_API_BASE_URL" \
  --dart-define=ENABLE_DEMO=false \
  "$@"
