#!/usr/bin/env bash
# Linux entry point. Source checkout supplies the reviewed deployment driver.
set -euo pipefail
[[ "$(uname -s)" == Linux ]] || { echo '仅支持 Linux。' >&2; exit 1; }
[[ "$(id -u)" == 0 ]] || { echo '请使用 sudo bash 或 root 执行。' >&2; exit 1; }
if ! command -v python3 >/dev/null || ! command -v git >/dev/null || ! command -v openssl >/dev/null || ! command -v iptables >/dev/null || ! command -v curl >/dev/null || ! command -v ssh >/dev/null; then
  read -r -p '安装 Python3、Git、OpenSSL、iptables、curl 等系统依赖？[Y/n] ' answer
  [[ "$answer" != n && "$answer" != N ]] || exit 1
  if command -v apt-get >/dev/null; then
    apt-get update
    apt-get install -y python3 git openssl iptables curl ca-certificates openssh-client
  elif command -v dnf >/dev/null; then
    dnf install -y python3 git openssl iptables curl ca-certificates openssh-clients
  else
    echo '请先安装上述依赖，再重新运行。' >&2; exit 1
  fi
fi
if ! command -v docker >/dev/null; then
  read -r -p '从 Docker 官方软件源安装 Engine、Buildx 和 Compose？[Y/n] ' answer
  [[ "$answer" != n && "$answer" != N ]] || exit 1
  . /etc/os-release
  if [[ "$ID" == ubuntu || "$ID" == debian ]]; then
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
    chmod a+r /etc/apt/keyrings/docker.asc
    printf 'Types: deb\nURIs: https://download.docker.com/linux/%s\nSuites: %s\nComponents: stable\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/docker.asc\n' "$ID" "${UBUNTU_CODENAME:-$VERSION_CODENAME}" "$(dpkg --print-architecture)" > /etc/apt/sources.list.d/docker.sources
    apt-get update
    apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  else
    echo '此发行版请先按 Docker 官方说明安装 Engine、Buildx 和 Compose，再运行本脚本。' >&2; exit 1
  fi
  systemctl enable --now docker
fi
docker compose version >/dev/null
docker info >/dev/null
exec python3 "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/deploy-light-tenancy.py" "$@"
