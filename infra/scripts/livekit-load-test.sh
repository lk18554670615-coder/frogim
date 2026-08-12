#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ROOM_COUNT="${LIVEKIT_LOAD_ROOMS:-10}"
VIDEO_PUBLISHERS="${LIVEKIT_LOAD_VIDEO_PUBLISHERS:-8}"
SUBSCRIBERS="${LIVEKIT_LOAD_SUBSCRIBERS:-1}"
DURATION="${LIVEKIT_LOAD_DURATION:-30s}"
RESOLUTION="${LIVEKIT_LOAD_RESOLUTION:-medium}"
EXPECTED_PARTICIPANTS=$((VIDEO_PUBLISHERS + SUBSCRIBERS))

for value in "$ROOM_COUNT" "$VIDEO_PUBLISHERS" "$SUBSCRIBERS"; do
  if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
    echo "room, publisher and subscriber counts must be positive integers" >&2
    exit 2
  fi
done
if (( EXPECTED_PARTICIPANTS > 9 )); then
  echo "the product limit is nine participants per room" >&2
  exit 2
fi
if [[ "$RESOLUTION" != "medium" ]]; then
  echo "the release gate requires LiveKit's medium (360p) video layer" >&2
  exit 2
fi

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
prefix="linli-load-${timestamp,,}"
artifact_dir="${LIVEKIT_LOAD_ARTIFACT_DIR:-$ROOT_DIR/build/qa/livekit-load-$timestamp}"
mkdir -p "$artifact_dir"

compose=(docker compose -f "$ROOT_DIR/infra/compose.yaml" -f "$ROOT_DIR/infra/compose.wukong.yaml" --profile loadtest)
rooms=()
pids=()

cleanup() {
  local exit_status="$?" room pid
  trap - EXIT INT TERM
  set +e
  for pid in "${pids[@]:-}"; do
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  for room in "${rooms[@]:-}"; do
    "${compose[@]}" run --rm --no-deps -T livekit-load room delete "$room" >/dev/null 2>&1 || true
  done
  exit "$exit_status"
}
trap cleanup EXIT INT TERM

for index in $(seq 1 "$ROOM_COUNT"); do
  room="$prefix-$index"
  rooms+=("$room")
  "${compose[@]}" run --rm --no-deps -T livekit-load room create \
    --empty-timeout 60 --departure-timeout 20 "$room" \
    >"$artifact_dir/room-$index-create.json"
done

for index in $(seq 1 "$ROOM_COUNT"); do
  room="${rooms[$((index - 1))]}"
  "${compose[@]}" run --rm --no-deps -T livekit-load perf load-test \
    --room "$room" \
    --duration "$DURATION" \
    --video-publishers "$VIDEO_PUBLISHERS" \
    --subscribers "$SUBSCRIBERS" \
    --video-resolution "$RESOLUTION" \
    --layout 3x3 \
    --no-simulcast \
    --num-per-second "$EXPECTED_PARTICIPANTS" \
    >"$artifact_dir/room-$index.log" 2>&1 &
  pids+=("$!")
done

python_command="${LIVEKIT_LOAD_PYTHON:-}"
if [[ -n "$python_command" ]]; then
  if ! "$python_command" --version >/dev/null 2>&1; then
    echo "LIVEKIT_LOAD_PYTHON is not executable" >&2
    exit 1
  fi
elif command -v python3 >/dev/null 2>&1 && python3 --version >/dev/null 2>&1; then
  python_command=python3
elif command -v python >/dev/null 2>&1 && python --version >/dev/null 2>&1; then
  python_command=python
else
  echo "python3 or python is required to validate the LiveKit room snapshot" >&2
  exit 1
fi

active_snapshot="$artifact_dir/active-rooms.json"
active_snapshot_python="$active_snapshot"
if [[ "$python_command" == *.exe ]] && command -v cygpath >/dev/null 2>&1; then
  active_snapshot_python="$(cygpath -w "$active_snapshot")"
fi
active_verified=false
for _ in $(seq 1 40); do
  if "${compose[@]}" run --rm --no-deps -T livekit-load room list -j "${rooms[@]}" >"$active_snapshot" 2>"$artifact_dir/room-list.stderr"; then
    if "$python_command" - "$active_snapshot_python" "$ROOM_COUNT" "$EXPECTED_PARTICIPANTS" <<'PY'
import json
import sys

path, expected_rooms, expected_participants = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
with open(path, encoding="utf-8") as handle:
    rooms = json.load(handle).get("rooms", [])
if len(rooms) != expected_rooms:
    raise SystemExit(1)
for room in rooms:
    if int(room.get("maxParticipants", 0)) != 9:
        raise SystemExit(1)
    if int(room.get("numParticipants", 0)) != expected_participants:
        raise SystemExit(1)
PY
    then
      active_verified=true
      break
    fi
  fi
  sleep 1
done

if [[ "$active_verified" != true ]]; then
  echo "LiveKit rooms did not reach the expected concurrent participant count" >&2
  exit 1
fi

livekit_container="$("${compose[@]}" ps -q livekit)"
docker stats --no-stream --format '{{json .}}' "$livekit_container" >"$artifact_dir/livekit-container-stats.json"
"${compose[@]}" exec -T livekit wget -qO- http://127.0.0.1:6789/metrics >"$artifact_dir/livekit-prometheus.txt"

failed=0
for index in $(seq 1 "$ROOM_COUNT"); do
  pid="${pids[$((index - 1))]}"
  if ! wait "$pid"; then
    echo "room $index load process failed" >&2
    failed=1
  fi
  log="$artifact_dir/room-$index.log"
  if grep -Eqi 'could not connect|not found|panic|fatal' "$log"; then
    echo "room $index contains a connection error" >&2
    failed=1
  fi
  if ! grep -Eq "Total.*$VIDEO_PUBLISHERS/$VIDEO_PUBLISHERS" "$log"; then
    echo "room $index did not subscribe to all video tracks" >&2
    failed=1
  fi
  if ! grep -Eq 'Total.*[0-9]+/[0-9]+.*0 \(0%\).*0' "$log"; then
    echo "room $index reported packet loss or subscriber errors" >&2
    failed=1
  fi
done
pids=()

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi

cat >"$artifact_dir/summary.txt" <<EOF
rooms=$ROOM_COUNT
participants_per_room=$EXPECTED_PARTICIPANTS
video_publishers_per_room=$VIDEO_PUBLISHERS
subscribers_per_room=$SUBSCRIBERS
video_resolution=$RESOLUTION
source_fps=20
simulcast=false
layout=3x3
duration=$DURATION
active_snapshot_verified=true
packet_loss=0
subscriber_errors=0
artifact_dir=$artifact_dir
EOF

echo "LiveKit media load verification passed: $ROOM_COUNT rooms, $EXPECTED_PARTICIPANTS participants per room, artifacts: $artifact_dir"
