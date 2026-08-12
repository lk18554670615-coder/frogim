# IP 服务器配置与运维

> 兼容命名说明：远端目录 `/opt/nexachat`、Compose project、数据库和对象存储标识属于已部署资源，迁移前继续保留；产品名称统一为“邻里通讯”。

## 生产与测试边界

`infra/compose.ip.yaml` 是公网验收测试栈，内部固定 `IM_ENV=development`、`IM_DEV_MODE=true`、`IM_IP_TEST_ONLY=true` 和静态验证码。它严禁单独用于生产，也不得通过覆盖少量 shell 环境变量“改造成生产”。仓库当前的 `nexachat` 运维脚本会按 `IM_ENV=production` 自动校验并叠加生产配置；服务器尚未更新到该版本前，不要用旧脚本创建或重建生产容器。

IP 生产部署唯一受支持的组合是：

- 配置模板：`.env.ip.production.example` → `.env.ip.production`
- Compose：`infra/compose.ip.yaml` + `infra/compose.ip.production.yaml`
- 部署入口：`infra/scripts/deploy-ip-production.sh`
- 运行状态：`IM_ENV=production`、`IM_DEV_MODE=false`、`IM_DEV_ALLOW_CONTAINER_BIND=false`、`IM_IP_TEST_ONLY=false`
- 验证码：真实 HTTPS OTP webhook；生产没有固定 `IM_DEV_OTP_CODE`

## 准备生产配置

在受控发布目录执行：

```bash
cp .env.ip.production.example .env.ip.production
chmod 600 .env.ip.production
```

替换全部 `REPLACE_WITH_*` 和示例 IP。数据库、Redis、JWT、管理员、OTP、个推、TURN、MinIO 与备份路径均为敏感或关键配置；不得提交 Git、发送到群聊或写入工单。

生产必须确认：

- `PRODUCTION_ENDPOINT_MODE=ip`
- `IM_ENV=production`
- `IM_DEV_MODE=false`
- `IM_DEV_ALLOW_CONTAINER_BIND=false`
- 配置真实 HTTPS `IM_OTP_WEBHOOK_URL` 与高熵 `IM_OTP_WEBHOOK_TOKEN`
- 配置真实证书联系邮箱 `TLS_EMAIL`
- 不配置、也不保留静态 `IM_DEV_OTP_CODE`
- `MINIO_APP_USER` / `MINIO_APP_PASSWORD` 与 root 凭据相互独立

先校验，不部署：

```bash
infra/scripts/validate-production-env.sh .env.ip.production
docker compose --env-file .env.ip.production \
  -f infra/compose.ip.yaml \
  -f infra/compose.ip.production.yaml config -q
```

第二条命令仅检查基础生产组合。若使用 APNs VoIP，正式部署脚本会自动加入专用 overlay。

首次部署前签发 IP 证书；后续也可用同一命令主动续期：

```bash
infra/scripts/issue-ip-certificate.sh .env.ip.production
```

脚本要求 Certbot 5.4 或更高版本，使用 `shortlived` profile、`webroot` 和 `--ip-address`。没有运行中的网关时会临时启动仅服务 ACME webroot 的 HTTP 容器，并保证退出时清理。签发完成后会核验证书确实覆盖 `SERVER_IP` 且剩余有效期超过 12 小时。

## APNs VoIP 私钥

当 `IM_PUSH_PROVIDER=apns_voip` 或 `getui_apns_voip` 时，`.p8` 必须由容器 uid 或 gid `10001` 持有，权限只能为 `0400` 或 `0440`：

```bash
sudo install -o 10001 -g 10001 -m 0400 \
  AuthKey_XXXXXXXXXX.p8 /data/linli-im/shared/secrets/apns-auth-key.p8
```

也可使用 `root:10001` + `0440`。`0600`、宽于 `0440` 或 uid/gid 均非 `10001` 会被生产校验器拒绝。`deploy-ip-production.sh` 会自动叠加 `infra/compose.apns-voip.yaml`，并在启动前检查容器内私钥可读性。

## 生产部署与检查

```bash
infra/scripts/deploy-ip-production.sh .env.ip.production
```

脚本会校验生产环境、叠加 production overlay，按推送模式自动叠加 APNs overlay，构建/拉取镜像、启动服务、等待 MinIO 初始化并执行 HTTPS 冒烟。公网冒烟必须同时取得 WuKongIM `/im` 的 WebSocket `101`，以及 LiveKit `/rtc/rtc/validate` 无 Token 请求的 `401`；任一实时路由失败都会阻止发布完成。

只读检查可以使用完整生产组合：

```bash
docker compose --env-file .env.ip.production \
  -f infra/compose.ip.yaml \
  -f infra/compose.ip.production.yaml ps

docker compose --env-file .env.ip.production \
  -f infra/compose.ip.yaml \
  -f infra/compose.ip.production.yaml logs --tail=200 server

infra/scripts/smoke.sh .env.ip.production
```

当前仓库的 `nexachat deploy` 在生产配置下会委托 `deploy-ip-production.sh`，`restart`、`status`、`logs` 和 `backup` 也会自动加入 production/APNs overlay。升级远端运维脚本前仍以直接调用 `deploy-ip-production.sh` 为准，避免旧版本误用测试栈。

## 认证与实时验收

- REST Access Token 只允许 `Authorization: Bearer <token>`；query、请求体或 Cookie 中的 token 应返回未认证。
- `/v2/auth/im-session` 返回独立、短期的 WuKongIM Token；REST Access Token 不得进入 WSS/TCP URL。
- 使用 `tools/wukong-probe` 验证真实 TCP 握手、ACK、互发、离线/历史同步、DataSource、CMD 和策略插件；生产 Compose 探针必须设置短期 `WUKONG_PROBE_OTP` 并通过业务 API 创建真实好友/频道，仅做 HTTP 101 升级或绕过业务策略创建 WuKong 原生临时频道都不算消息链路验收。
- LiveKit 通话必须真机验证房间鉴权、2–9 人音视频、屏幕共享、成员离开和网络重连；业务服务与 PostgreSQL 不接收 SDP/ICE。

## MinIO 与媒体

生产 overlay 会让 API 使用独立的 `MINIO_APP_USER` / `MINIO_APP_PASSWORD`；`minio-init` 以 root 创建 bucket-scoped 最小权限策略。root 只用于初始化、备份和管理，不得交给应用。

上传完成接口会核验对象 size、完整 SHA-256 与 magic MIME，验证通过后才标记 ready。这不是通用恶意文件扫描；上线任意文件上传前仍必须接入外部杀毒、压缩包检查、媒体审核与隔离处置。

## 备份与恢复点

备份过程使用 `umask 077`，先写入 `.incomplete-<UTC timestamp>`，依次生成 WuKong 数据归档、`postgres.dump`、MinIO 镜像、固定依赖锁和 `SHA256SUMS`，收紧权限后再原子重命名为最终时间戳目录。只有最终目录是有效恢复点；残留 `.incomplete-*` 表示失败，必须告警和排查，不能直接恢复。

生产部署后应立即执行一次备份并复制到异机加密存储。恢复流程与演练要求见 [BACKUP_RESTORE.md](BACKUP_RESTORE.md)。

## HTTPS IP 证书与 Flutter 真机包

证书保存在 `CERTBOT_DIR`，ACME 校验目录由 `CERTBOT_WEBROOT` 指定。Let's Encrypt 的 IP 证书是约 6 天的短期证书，必须启用 `nexachat-cert-renew.timer` 每日续期；续期命令通过共享 webroot 完成验证并热加载 Caddy。不要手工移动 `live/` 下的软链接。公网暴露 Caddy `80/443`、WuKongIM 原生客户端 `5100/TCP`、LiveKit TCP 回退 `7881/TCP` 与媒体 `7882–7889/UDP`；WuKongIM API/Manager `5001/5200/5300`、LiveKit 管理面 `7880`、数据库、Redis、MinIO 与监控端口保持私网。生产云安全组和主机防火墙必须与此端口清单一致。

TencentOS/firewalld 主机使用仓库脚本幂等校准规则：

```bash
sudo bash infra/scripts/configure-ip-firewall.sh
```

脚本仅补充 `80/443/5100/7881/TCP` 与 `7882–7889/UDP`，并移除本项目已经
弃用的 Coturn `3478/TCP+UDP`、`49160–49200/UDP`；不会改动 SSH、宝塔、
FTP 或其他非本项目规则。云安全组仍需在云平台单独保持同一入口清单。

正式 IP HTTPS 包示例：

```bash
fvm flutter build apk --release \
  --dart-define=APP_ENV=production \
  --dart-define=API_BASE_URL=https://203.0.113.10 \
  --dart-define=WS_URL=wss://203.0.113.10/im \
  --dart-define=ENABLE_DEMO=false \
  --dart-define=TERMS_URL=https://203.0.113.10/legal/terms \
  --dart-define=PRIVACY_URL=https://203.0.113.10/legal/privacy \
  --dart-define=MEDIA_MAX_BYTES=104857600
```

把示例 IP 替换为真实证书覆盖的地址。生产模式缺少 HTTPS/WSS 或开启演示数据时客户端会拒绝启动。
