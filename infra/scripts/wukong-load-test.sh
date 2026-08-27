#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
CONNECTIONS="${WUKONG_LOAD_CONNECTIONS:-10000}"
CONNECTION_WORKERS="${WUKONG_LOAD_CONNECTION_WORKERS:-300}"
MESSAGE_PAIRS="${WUKONG_LOAD_MESSAGE_PAIRS:-20}"
MESSAGE_RATE="${WUKONG_LOAD_MESSAGE_RATE:-1000}"
DURATION="${WUKONG_LOAD_DURATION:-30s}"
WARMUP="${WUKONG_LOAD_WARMUP:-10s}"
ACK_WAIT="${WUKONG_LOAD_ACK_WAIT:-30s}"
MINIMUM_ACK_RATIO="${WUKONG_LOAD_MINIMUM_ACK_RATIO:-1}"
MAX_P95="${WUKONG_LOAD_MAX_P95:-300ms}"
MAX_P99="${WUKONG_LOAD_MAX_P99:-800ms}"
EVIDENCE_KIND="${WUKONG_LOAD_EVIDENCE_KIND:-local-engineering}"

for value in "$CONNECTIONS" "$CONNECTION_WORKERS" "$MESSAGE_PAIRS"; do
  if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "connection and pair counts must be positive integers" >&2
    exit 2
  fi
done
if [[ ! "$MESSAGE_RATE" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  echo "message rate must be a non-negative number" >&2
  exit 2
fi
if [[ "$EVIDENCE_KIND" != "local-engineering" && "$EVIDENCE_KIND" != "release-candidate" ]]; then
  echo "WUKONG_LOAD_EVIDENCE_KIND must be local-engineering or release-candidate" >&2
  exit 2
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
artifact_dir="${WUKONG_LOAD_ARTIFACT_DIR:-$ROOT_DIR/build/qa/wukong-load-$timestamp}"
mkdir -p "$artifact_dir"

compose=(docker compose -f "$ROOT_DIR/infra/compose.yaml" -f "$ROOT_DIR/infra/compose.wukong.yaml" --profile loadtest)
services=(server wukongim postgres redis minio)

capture_metrics() {
  local destination="$1"
  if command -v curl >/dev/null 2>&1; then
    curl --fail --silent --show-error --max-time 10 http://127.0.0.1:8080/metrics >"$destination"
  else
    : >"$destination"
  fi
}

capture_state() {
  local prefix="$1" service container
  "${compose[@]}" ps --format json >"$artifact_dir/$prefix-compose-ps.json"
  docker info --format '{{json .}}' >"$artifact_dir/$prefix-docker-info.json"
  docker system df >"$artifact_dir/$prefix-docker-df.txt"
  for service in "${services[@]}"; do
    container="$("${compose[@]}" ps -q "$service")"
    if [[ -n "$container" ]]; then
      docker inspect --format '{{json .}}' "$container" >>"$artifact_dir/$prefix-container-inspect.jsonl"
    fi
  done
}

capture_metrics "$artifact_dir/before-business-prometheus.txt"
capture_state before

load_log="$artifact_dir/load.log"
set +e
"${compose[@]}" run --rm wukong-load \
  -connections "$CONNECTIONS" \
  -connection-workers "$CONNECTION_WORKERS" \
  -connection-attempts 3 \
  -connection-retry-delay 100ms \
  -message-pairs "$MESSAGE_PAIRS" \
  -messages-per-second "$MESSAGE_RATE" \
  -duration "$DURATION" \
  -warmup "$WARMUP" \
  -ack-wait "$ACK_WAIT" \
  -max-p95 "$MAX_P95" \
  -max-p99 "$MAX_P99" \
  -minimum-ack-ratio "$MINIMUM_ACK_RATIO" \
  >"$load_log" 2>&1 &
load_pid="$!"
set -e

while kill -0 "$load_pid" >/dev/null 2>&1; do
  {
    printf '{"capturedAt":"%s","containers":[' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    first=true
    for service in "${services[@]}"; do
      container="$("${compose[@]}" ps -q "$service")"
      if [[ -n "$container" ]]; then
        if [[ "$first" == true ]]; then first=false; else printf ','; fi
        docker stats --no-stream --format '{{json .}}' "$container"
      fi
    done
    printf ']}'
    printf '\n'
  } >>"$artifact_dir/container-stats.jsonl"
  sleep 2
done

set +e
wait "$load_pid"
load_status="$?"
set -e

awk '
  /^\{$/ { if (!report) { report=1 } }
  report { print }
  report && /^\}$/ { exit }
' "$load_log" >"$artifact_dir/report.json"

capture_metrics "$artifact_dir/after-business-prometheus.txt"
capture_state after

before_failed="$(awk '$1 == "im_wukong_outbox_failed" {print $2}' "$artifact_dir/before-business-prometheus.txt" | tail -1)"
after_failed="$(awk '$1 == "im_wukong_outbox_failed" {print $2}' "$artifact_dir/after-business-prometheus.txt" | tail -1)"
before_failed="${before_failed:-unknown}"
after_failed="${after_failed:-unknown}"

cat >"$artifact_dir/summary.txt" <<EOF
evidence_kind=$EVIDENCE_KIND
connections=$CONNECTIONS
connection_workers=$CONNECTION_WORKERS
message_pairs=$MESSAGE_PAIRS
messages_per_second=$MESSAGE_RATE
duration=$DURATION
warmup=$WARMUP
ack_wait=$ACK_WAIT
max_p95=$MAX_P95
max_p99=$MAX_P99
minimum_ack_ratio=$MINIMUM_ACK_RATIO
load_exit_status=$load_status
outbox_failed_before=$before_failed
outbox_failed_after=$after_failed
artifact_dir=$artifact_dir
EOF

if [[ "$load_status" -ne 0 ]]; then
  echo "WuKong message load failed; evidence: $artifact_dir" >&2
  tail -80 "$load_log" >&2
  exit "$load_status"
fi
if [[ ! -s "$artifact_dir/report.json" ]] || ! grep -q '"passed": true' "$artifact_dir/report.json"; then
  echo "WuKong load did not produce a passing JSON report; evidence: $artifact_dir" >&2
  exit 1
fi
if [[ "$before_failed" != "unknown" && "$after_failed" != "$before_failed" ]]; then
  echo "WuKong load created a new permanently failed Outbox row; evidence: $artifact_dir" >&2
  exit 1
fi
if awk '
  $1 ~ /^im_wukong_(outbox|webhook)_pending$/ && ($2 + 0) != 0 { bad=1 }
  $1 == "im_wukong_webhook_failed" && ($2 + 0) != 0 { bad=1 }
  END { exit bad ? 0 : 1 }
' "$artifact_dir/after-business-prometheus.txt"; then
  echo "WuKong load left a pending or failed synchronization queue; evidence: $artifact_dir" >&2
  exit 1
fi
if [[ "$EVIDENCE_KIND" == "release-candidate" && "$after_failed" != "0" ]]; then
  echo "release-candidate evidence requires a fresh database with zero failed gauges" >&2
  exit 1
fi

echo "WuKong message load verification passed; evidence: $artifact_dir"
