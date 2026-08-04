# 生产部署

本指南适用于域名 + Caddy 的标准生产环境。IP 直连兼容环境见 [IP_SERVER_OPERATIONS.md](IP_SERVER_OPERATIONS.md)。不要在未完成备份、密钥和外部服务配置时执行生产部署。

## 拓扑与前置条件

`infra/compose.production.yaml` 与本地开发隔离。Caddy 是唯一 Web/API 公网入口；Coturn 独立承载 WebRTC 媒体中继。后台、API、PostgreSQL、Redis、MinIO、Prometheus 和 Grafana 均在内部网络。

主机要求：

- Linux、Docker Engine、Docker Compose v2。
- `DOMAIN` 的 A/AAAA 记录已经指向主机。
- 入站 TCP 80/443；自建 Coturn 还需放行 3478 TCP/UDP 和 49160–49200 UDP。数据库、缓存、对象存储和监控端口不开放。
- 出站可访问镜像仓库、证书、短信、个推及所需第三方服务。
- 系统时间同步，磁盘容量满足数据库、对象、日志和监控保留。
- 已准备异机加密备份位置和可联系的发布/回滚负责人。

## 准备配置

```bash
cp .env.production.example .env.production
chmod 600 .env.production
```

替换所有 `REPLACE_WITH_*`。数据库/Redis 密码进入 URL 前需要编码；JWT 至少 32 字节；管理员只保存 bcrypt 哈希和独立 Base32 TOTP。生产必须关闭共享管理员密钥、开发模式和容器公网开发豁免。

生产 MinIO 必须同时配置 root 管理凭据和独立的 `MINIO_APP_USER` / `MINIO_APP_PASSWORD`。初始化容器以 root 创建桶和 bucket-scoped 最小权限策略，API 服务只接收应用凭据；不得为了省事让应用使用 root。

若 `IM_PUSH_PROVIDER` 为 `apns_voip` 或 `getui_apns_voip`，先把 `.p8` 安装为容器 uid/gid `10001` 可读，权限固定为 `0400` 或 `0440`。示例：

```bash
sudo install -o 10001 -g 10001 -m 0400 \
  AuthKey_XXXXXXXXXX.p8 /data/linli-im/shared/secrets/apns-auth-key.p8
```

校验器会拒绝其他权限/属主组合。`infra/scripts/deploy.sh` 会根据 `IM_PUSH_PROVIDER` 自动叠加 `infra/compose.apns-voip.yaml` 并在启动前验证容器可读，无需在部署命令中手工增加 `-f`。

先验证，不部署：

```bash
make production-validate PROD_ENV=.env.production
make production-config PROD_ENV=.env.production
```

校验器会拒绝占位符、示例域名、弱密钥、危险权限以及 `noop`/`log` 推送。配置字段详见 [CONFIGURATION.md](CONFIGURATION.md)。IP 证书部署不能直接运行测试用的 `infra/compose.ip.yaml`；必须使用 `.env.ip.production.example` 生成 `.env.ip.production`，先通过 `infra/scripts/issue-ip-certificate.sh` 签发 Certbot 5.4+ short-lived IP 证书，再由 `infra/scripts/deploy-ip-production.sh` 启动生产 overlay，详见 [IP_SERVER_OPERATIONS.md](IP_SERVER_OPERATIONS.md)。

## 首次部署

```bash
make production-deploy PROD_ENV=.env.production
```

部署脚本依次校验配置、渲染 Compose、构建应用镜像、拉取基础镜像、启动服务并执行 HTTPS 冒烟；不会删除 volume。

认证冒烟必须确认：普通 REST 仅接受 `Authorization: Bearer`，URL query 中的 Access Token 被拒绝；WebSocket 必须先交换 30 秒一次性 ticket，Access Token query 或升级请求 `Authorization` 均被拒绝，ticket 重放也被拒绝。

部署后必须完成：

1. 管理员通过独立安全渠道获取 TOTP，并使用命名账号登录。
2. 验证 `/health`、HTTPS/WSS、媒体上传、真实短信和真实离线推送。
3. 用真机验证 iOS 杀进程、Android 厂商通道和通知点击跳转。
4. 验证 STUN/TURN 下的跨网络语音和视频。
5. 检查 Prometheus target、告警送达和日志脱敏。
6. 执行备份并完成异机复制；确认只出现最终时间戳目录，不把 `.incomplete-*` 当作成功备份；按周期执行恢复演练。
7. 记录镜像 ID、配置修订、迁移版本、冒烟输出和审批人。

## 升级

1. 按 [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) 完成测试和备份。
2. 阅读全部新增迁移，确认旧应用与新 schema 的兼容窗口；数据库需允许 `pg_trgm`，并验证 `im_messages_text_search_trgm_idx` 已建立。
3. 执行受控部署，观察迁移、健康、错误率和队列。
4. 完成双用户消息、后台、媒体、公告和推送冒烟。媒体完成接口必须验证 size、SHA-256 和 magic MIME；通用文件恶意扫描仍需外部扫描服务上线后才能开放。
5. 保留旧镜像与配置，直到观察窗口结束。

## 回滚

应用回滚优先重新部署上一版本镜像/源码，不回退数据。禁止在紧急情况下直接执行 down migration。只有在确认恢复点、停止写入并按 [BACKUP_RESTORE.md](BACKUP_RESTORE.md) 演练过的流程操作时，才允许恢复数据库或对象存储。

## TLS 与路由

Caddy 使用 `DOMAIN` 和 `TLS_EMAIL` 自动签发、续期证书，并设置 HSTS、CSP、防嵌入、MIME sniffing、Referrer 与 Permissions Policy。若改用云负载均衡，必须保持：

- `/v1/*`、`/api/*`、`/health` → `server:8080`
- 其余管理站点路径 → `admin:8080`
- `/metrics`、`/ready`、Grafana、Prometheus、MinIO 控制台和数据服务不对公网路由

## 实时通话与 Redis 故障语义

Redis Pub/Sub 只负责在线、跨节点的通话信令转发。`call.offer`、`call.answer`、`call.ice` 和确认帧不写入 PostgreSQL，没有服务端 replay；Redis publish 失败时请求 fail-closed，不能向客户端伪装成已送达。Flutter 端使用 `signalId` 重试和接收去重。`accepted`、`rejected`、`cancelled`、`ended`、`timeout` 属于安全状态元数据，在状态事务内写入双方 `userSyncSeq`，重连后可经 `/v1/sync` 收敛。发布后必须分别验证同节点、跨节点、短时 Redis 故障和重连场景；SDP/ICE 仍不在持久同步范围内。

## 对象与备份安全

应用只能使用 MinIO 的独立 bucket-scoped 凭据；root 凭据仅供 `minio-init`、备份和受控运维。上传 `complete` 的 SHA-256、size 和 magic MIME 校验不是杀毒能力，任意文件上传上线前仍需接入恶意文件扫描、隔离和处置服务。

备份脚本以 `umask 077` 在 `.incomplete-<UTC timestamp>` 中生成数据库、对象镜像和校验和，收紧权限后通过同一文件系统内的 `mv` 原子发布为最终时间戳目录。脚本失败时保留 `.incomplete-*` 供排障，但该目录不是可恢复备份，监控必须把它识别为失败。

## 兼容资源警告

Compose project、volume、数据库、存储桶和 `/opt/nexachat` 路径属于兼容标识。不要为了品牌统一修改；原因和迁移要求见 [COMPATIBILITY.md](COMPATIBILITY.md)。
