#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
container_name="${WUKONG_LOG_TEST_CONTAINER:-nexachat-wukongim-1}"
manager_token="${IM_WUKONG_MANAGER_TOKEN:-local-wukong-manager-token-change-me}"
compose=(docker compose -f "$root_dir/infra/compose.yaml" -f "$root_dir/infra/compose.wukong.yaml")

started_at="$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)"
"${compose[@]}" --profile probe build wukong-probe >/dev/null
probe_output="$("${compose[@]}" --profile probe run --rm --no-deps wukong-probe \
  -api http://wukongim:5001 \
  -manager-api http://wukongim:5300 \
  -tcp tcp://wukongim:5100 \
  -manager-token "$manager_token" \
  -redaction-only \
  -timeout 30s)"
printf '%s\n' "$probe_output"

grep -Fq '"duplicateMasterDeviceKick": true' <<<"$probe_output" || {
  echo "probe did not prove duplicate master-device eviction" >&2
  exit 1
}

raw_logs="$(docker logs --since "$started_at" "$container_name" 2>&1)"
grep -Fq 'close old conn for master' <<<"$raw_logs" || {
  echo "raw WuKongIM logs did not retain the duplicate master-device diagnostic" >&2
  exit 1
}

if grep -Eq 'WK_LOG_REDACTION_(TOKEN|MESSAGE)_MARKER' <<<"$raw_logs"; then
  echo "raw WuKongIM logs leaked a runtime token or message-body canary" >&2
  exit 1
fi
if grep -Eqi '(aesKey|aesIV|expectToken|actToken|msgKey|signStr|verifyString)' <<<"$raw_logs"; then
  echo "raw WuKongIM logs contain a forbidden cryptographic credential field" >&2
  exit 1
fi

echo "WuKongIM raw-log redaction gate passed"
