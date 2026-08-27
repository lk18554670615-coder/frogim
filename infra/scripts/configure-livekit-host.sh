#!/usr/bin/env bash
set -euo pipefail

readonly CONFIG_PATH="/etc/sysctl.d/99-linli-livekit.conf"
readonly MINIMUM_RMEM_BYTES=5000000
readonly TARGET_RMEM_BYTES="${LIVEKIT_UDP_RMEM_MAX:-$MINIMUM_RMEM_BYTES}"

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "configure-livekit-host.sh must run as root" >&2
  exit 1
fi
if [[ ! "$TARGET_RMEM_BYTES" =~ ^[0-9]+$ ]] ||
   (( TARGET_RMEM_BYTES < MINIMUM_RMEM_BYTES )); then
  echo "LIVEKIT_UDP_RMEM_MAX must be an integer >= $MINIMUM_RMEM_BYTES" >&2
  exit 1
fi

temporary="$(mktemp)"
trap 'rm -f "$temporary"' EXIT
printf '%s\n' \
  '# Managed by linli-im for LiveKit/Pion UDP media.' \
  "net.core.rmem_max = $TARGET_RMEM_BYTES" >"$temporary"
install -o root -g root -m 0644 "$temporary" "$CONFIG_PATH"
sysctl -p "$CONFIG_PATH"

actual="$(sysctl -n net.core.rmem_max)"
if (( actual < TARGET_RMEM_BYTES )); then
  echo "net.core.rmem_max remained $actual; expected >= $TARGET_RMEM_BYTES" >&2
  exit 1
fi
printf 'LiveKit host UDP receive buffer configured: %s bytes\n' "$actual"
