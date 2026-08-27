#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "run as root" >&2
  exit 1
fi
command -v firewall-cmd >/dev/null || { echo "firewalld is required on this host" >&2; exit 1; }
[[ "$(firewall-cmd --state)" == running ]] || { echo "firewalld is not running" >&2; exit 1; }

zone="${FIREWALL_ZONE:-$(firewall-cmd --get-default-zone)}"
required=(80/tcp 443/tcp 5100/tcp 7881/tcp 7882-7889/udp)
retired=(3478/tcp 3478/udp 49160-49200/udp)

for port in "${required[@]}"; do
  if ! firewall-cmd --permanent --zone="$zone" --query-port="$port" >/dev/null; then
    firewall-cmd --permanent --zone="$zone" --add-port="$port" >/dev/null
  fi
done
for port in "${retired[@]}"; do
  if firewall-cmd --permanent --zone="$zone" --query-port="$port" >/dev/null; then
    firewall-cmd --permanent --zone="$zone" --remove-port="$port" >/dev/null
  fi
done
firewall-cmd --reload >/dev/null

for port in "${required[@]}"; do
  firewall-cmd --zone="$zone" --query-port="$port" >/dev/null || {
    echo "required port was not activated: $port" >&2
    exit 1
  }
done
for port in "${retired[@]}"; do
  if firewall-cmd --zone="$zone" --query-port="$port" >/dev/null; then
    echo "retired port is still active: $port" >&2
    exit 1
  fi
done

echo "firewalld reconciled: zone=$zone required=${required[*]} retired=${retired[*]}"
