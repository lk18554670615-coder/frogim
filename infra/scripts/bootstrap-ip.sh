#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "run as root" >&2
  exit 1
fi

SERVER_IP="${1:?usage: bootstrap-ip.sh PUBLIC_IP}"
DATA_ROOT="/data/linli-im"
SHARED_DIR="$DATA_ROOT/shared"
LEGACY_SHARED_DIR="/opt/nexachat/shared"
CONFIG_FILE="$SHARED_DIR/config.env"
CREDENTIAL_FILE="$SHARED_DIR/initial-credentials.txt"

# /opt/nexachat 是已部署服务器的兼容目录；不得仅为品牌统一直接改名。

mkdir -p \
  "$SHARED_DIR/letsencrypt" \
  "$SHARED_DIR/certbot-webroot" \
  "$SHARED_DIR/downloads" \
  "$SHARED_DIR/secrets" \
  "$DATA_ROOT/backups" \
  "$DATA_ROOT/logs/archive" \
  "$DATA_ROOT/logs/incidents" \
  "$DATA_ROOT/data/postgres/data" \
  "$DATA_ROOT/data/postgres/archive" \
  "$DATA_ROOT/data/redis" \
  "$DATA_ROOT/data/minio" \
  "$DATA_ROOT/data/caddy/data" \
  "$DATA_ROOT/data/caddy/config" \
  "$DATA_ROOT/data/wukongim/data/plugins" \
  "$DATA_ROOT/data/wukongim/logs"
chmod 700 "$SHARED_DIR" "$SHARED_DIR/secrets" "$DATA_ROOT/backups" "$DATA_ROOT/logs" "$DATA_ROOT/logs/archive" "$DATA_ROOT/logs/incidents"
# 保留旧命令兼容入口，但唯一权威配置和证书都位于 /data/linli-im/shared。
mkdir -p /opt/nexachat
if [[ ! -e "$LEGACY_SHARED_DIR" ]]; then
  ln -s "$SHARED_DIR" "$LEGACY_SHARED_DIR"
fi
umask 077

if [[ -s "$CONFIG_FILE" ]]; then
  echo "configuration already exists: $CONFIG_FILE"
  exit 0
fi

random_alnum() {
	python3 - "$1" <<'PY'
import secrets
import string
import sys
alphabet = string.ascii_letters + string.digits
print(''.join(secrets.choice(alphabet) for _ in range(int(sys.argv[1]))))
PY
}

postgres_password="$(random_alnum 36)"
redis_password="$(random_alnum 36)"
jwt_secret="$(random_alnum 64)"
admin_password="$(random_alnum 22)"
minio_password="$(random_alnum 36)"
dev_otp="$(shuf -i 100000-999999 -n 1)"
totp_secret="$(python3 -c 'import base64,os; print(base64.b32encode(os.urandom(20)).decode().rstrip("="))')"
admin_email="admin@nexachat.local"

admin_hash="$(printf '%s\n' "$admin_password" | docker run --rm -i httpd:2.4-alpine htpasswd -niBC 12 admin | cut -d: -f2 | tr -d '\r\n')"
if [[ "$admin_hash" != '$2'* ]]; then
  echo "failed to create bcrypt admin hash" >&2
  exit 1
fi

cat > "$CONFIG_FILE" <<EOF
# 青蛙呱呱 IP 验收测试配置。包含固定开发验证码，严禁用于生产。
# 生产必须以 .env.ip.production.example 为模板并使用 deploy-ip-production.sh。

PRODUCTION_ENDPOINT_MODE=ip
IM_ENV=development
IM_DEV_MODE=true
IM_DEV_ALLOW_CONTAINER_BIND=true
IM_IP_TEST_ONLY=true

# ===== 公网入口与证书 =====
SERVER_IP=$SERVER_IP
CERTBOT_DIR=/data/linli-im/shared/letsencrypt
CERTBOT_WEBROOT=/data/linli-im/shared/certbot-webroot
DOWNLOAD_DIR=/data/linli-im/shared/downloads
LINLI_DATA_ROOT=/data/linli-im/data
WUKONG_REQUIRE_1TIB_DISK=false
WUKONG_PERFORMANCE_EVIDENCE=
CERTBOT_IMAGE=certbot/certbot:latest

# ===== 容器运行日志 =====
# Docker 日志保留在宝塔容器日志中并自动轮转；事故快照统一归档到 LOG_ARCHIVE_DIR。
DOCKER_LOG_MAX_SIZE=20m
DOCKER_LOG_MAX_FILES=10
IM_LOG_LEVEL=info
LOG_ARCHIVE_DIR=/data/linli-im/logs/archive
IM_REPLICAS=1

# ===== PostgreSQL（已有数据后不要直接改密码） =====
POSTGRES_DB=nexachat
POSTGRES_USER=nexachat
POSTGRES_PASSWORD=$postgres_password
IM_DATABASE_URL=postgres://nexachat:$postgres_password@postgres:5432/nexachat?sslmode=disable

# ===== Redis（已有数据后按密码轮换流程修改） =====
REDIS_PASSWORD=$redis_password
IM_REDIS_URL=redis://:$redis_password@redis:6379/0

# ===== 登录与令牌 =====
IM_JWT_SECRET=$jwt_secret
IM_ACCESS_TTL=15m
IM_REFRESH_TTL=720h
# 仅限验收测试的统一验证码；该配置不能转换为生产配置。
IM_DEV_OTP_CODE=$dev_otp

# ===== 管理后台 =====
IM_ADMIN_EMAIL=$admin_email
IM_ADMIN_PASSWORD_HASH='$admin_hash'
IM_ADMIN_TOTP_SECRET=$totp_secret

# ===== 私有对象存储与上传 =====
MINIO_ROOT_USER=nexachat-storage
MINIO_ROOT_PASSWORD=$minio_password
IM_S3_BUCKET=nexachat-media
IM_S3_REGION=us-east-1
# 单文件上限，单位字节；Flutter 打包的 MEDIA_MAX_BYTES 必须保持一致。
IM_MEDIA_MAX_BYTES=104857600

# ===== 音视频通话（LiveKit） =====
IM_CALL_INVITE_TTL=30s

# ===== 备份 =====
BACKUP_DIR=/data/linli-im/backups
BACKUP_METRICS_DIR=/data/linli-im/backups/.metrics
BACKUP_OFFSITE_ENABLED=false
BACKUP_OFFSITE_ENDPOINT=
BACKUP_OFFSITE_ACCESS_KEY=
BACKUP_OFFSITE_SECRET_KEY=
BACKUP_OFFSITE_BUCKET=
BACKUP_OFFSITE_PREFIX=linli-im
EOF

cat > "$CREDENTIAL_FILE" <<EOF
青蛙呱呱初始凭据（首次登录后请修改，并继续将本文件保留为 root 600 权限）
管理后台：https://$SERVER_IP
管理员邮箱：$admin_email
管理员密码：$admin_password
TOTP 密钥：$totp_secret
App 测试登录验证码：$dev_otp
集中配置：$CONFIG_FILE
EOF

chmod 600 "$CONFIG_FILE" "$CREDENTIAL_FILE"
echo "created $CONFIG_FILE and $CREDENTIAL_FILE"
