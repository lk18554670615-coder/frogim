# 配置中心

本文件是邻里通讯部署配置的统一索引。真实密钥只保存在服务器 `.env.production`、受控配置文件或密钥管理服务中，权限必须为 `600`，禁止提交 Git。可热更新的业务规则位于运营后台“系统设置”；基础设施与密钥项只展示配置状态，不回显明文。

## 配置分层

| 层级 | 修改入口 | 生效方式 | 示例 |
|---|---|---|---|
| 客户端构建参数 | Flutter `--dart-define` | 重新构建 App | API、WS、个推客户端参数 |
| 业务运行策略 | 运营后台“系统设置” | 保存后热更新 | 注册、撤回、群规模、好友、公告、通话、风控 |
| 基础设施参数 | `.env.production` / 密钥服务 | 校验后滚动重启 | 数据库、Redis、S3、JWT、连接上限 |
| 外部平台配置 | 云厂商/苹果/个推/TURN 控制台 | 平台发布或凭据轮换 | APNs、Android 厂商通道、短信、TURN |

## 移动端构建参数

| 参数 | 中文说明 | 规则 |
|---|---|---|
| `APP_ENV` | 构建环境 | `development`、`staging`、`production` |
| `API_BASE_URL` | HTTPS 接口地址 | 生产必须以 `https://` 开头 |
| `WS_URL` | WebSocket 地址 | 生产必须以 `wss://` 开头 |
| `ENABLE_DEMO` | 演示数据开关 | 生产强制为 `false` |
| `MEDIA_MAX_BYTES` | 客户端单文件上限 | 不得高于服务端 `IM_MEDIA_MAX_BYTES` |
| `GETUI_ENABLED` | 个推开关 | 正式包按平台接入状态设置 |
| `GETUI_APP_ID` | 个推应用编号 | 可进入客户端构建 |
| `GETUI_APP_KEY` | 个推应用键 | SDK 初始化使用 |
| `GETUI_APP_SECRET` | 个推客户端密钥 | 按官方客户端接入要求注入 |
| `TERMS_URL` | 用户协议地址 | 生产必须是已经法务审核并公开可访问的 HTTPS URL |
| `PRIVACY_URL` | 隐私政策地址 | 生产必须是已经法务审核并公开可访问的 HTTPS URL |

本地调试参数放在已忽略的 `apps/mobile/dart_defines.local.json`，通过 `--dart-define-from-file` 注入。`IM_GETUI_MASTER_SECRET` 只能存在于服务端。

## 服务、安全与管理

| 参数 | 中文说明 | 要求 |
|---|---|---|
| `IM_ENV` | 服务环境 | 生产设为 `production`，需重启 |
| `IM_MODE` | `memory` 或 `full` | 生产必须为 `full` |
| `IM_ADDR` | HTTP/WS 监听地址 | 由网关和容器网络决定 |
| `IM_DATABASE_URL` | PostgreSQL 连接串 | 密钥项，需重启 |
| `IM_REDIS_URL` | Redis 连接串 | 密钥项，需重启 |
| `IM_JWT_SECRET` | 用户令牌签名密钥 | 至少 32 字节，轮换会影响会话 |
| `IM_ACCESS_TTL` | 访问令牌有效期 | 默认 `15m`，需重启 |
| `IM_REFRESH_TTL` | 刷新令牌有效期 | 默认 `720h`，需重启 |
| `IM_TRUST_PROXY` | 信任反向代理来源地址 | 仅在受控网关后开启 |
| `IM_ALLOWED_ORIGINS` | WebSocket 允许来源 | 逗号分隔，需重启 |
| `IM_WS_MAX_PER_USER` | 单用户 WS 上限 | 1–20，需重启 |
| `IM_WS_MAX_PER_IP` | 单 IP WS 上限 | 1–200，需重启 |
| `IM_WS_MAX_CONNECTIONS` | 单服务实例 WS 总上限 | 默认 10000，需按实例内存压测后调整 |
| `IM_DB_MAX_CONNS` / `IM_DB_MIN_CONNS` | 单实例 PostgreSQL 连接池 | 默认 20/2；所有实例之和必须低于数据库连接预算 |
| `IM_DB_MAX_CONN_LIFETIME` / `IM_DB_MAX_CONN_IDLE_TIME` | 数据库连接生命周期 | 默认 `1h`/`15m` |
| `IM_DB_STATEMENT_TIMEOUT` | 单条数据库语句硬超时 | 默认 `15s`，防止异常查询长期占池 |
| `IM_PUSH_WORKERS` / `IM_PUSH_BATCH_SIZE` | 推送并发与领取批次 | 默认 16/200，需结合提供商限流调整 |
| `IM_MESSAGE_FANOUT_BATCH_SIZE` | 消息同步扇出批次 | 默认 500，范围 10–5000 |
| `IM_SYNC_RETENTION` / `IM_OUTBOX_RETENTION` | 同步事件与 outbox 保留时间 | 默认 `720h`/`168h`；超期客户端收到全量刷新提示 |
| `IM_HTTP_LOG_SUCCESS_SAMPLE_RATE` | 成功请求日志采样率 | 默认 0.01；慢请求与错误始终记录 |
| `IM_ADMIN_EMAIL` | 管理员邮箱 | 生产必填 |
| `IM_ADMIN_PASSWORD_HASH` | 管理员 bcrypt 哈希 | 禁止保存明文 |
| `IM_ADMIN_TOTP_SECRET` | 管理员 TOTP 密钥 | 仅服务端，不回显 |
| `IM_ADMIN_ROLE` | 管理员角色 | 默认 `platform_admin` |

`IM_DEV_MODE` 与 `IM_DEV_OTP_CODE` 只允许本地开发。生产验证码使用 `IM_OTP_WEBHOOK_URL` 和 `IM_OTP_WEBHOOK_TOKEN`，URL 必须为 HTTPS，令牌不得进入日志或客户端。

生产 Compose 还通过 `SERVER_CPU_LIMIT`、`SERVER_MEMORY_LIMIT`、`SERVER_GO_MEMORY_LIMIT`、`POSTGRES_CPU_LIMIT`、`POSTGRES_MEMORY_LIMIT`、`REDIS_CPU_LIMIT`、`REDIS_MEMORY_LIMIT`、`REDIS_MAXMEMORY` 和统一 PID/no-file 上限约束资源。修改这些值前必须同时检查宿主机容量、PostgreSQL `max_connections` 和实际压测结果。

用户 REST 接口只从 `Authorization: Bearer <accessToken>` 读取访问令牌，不接受 query、请求体或 Cookie 回退。WebSocket 不接受 Access Token：客户端必须先用上述 REST 鉴权调用 `POST /v1/ws/ticket`，再以 30 秒、一次性 `ticket` 建连；生产 Redis 无法原子消费票据时连接失败关闭。

## 业务运行策略

运营后台按以下分组管理，并由服务端执行类型、范围校验和审计：

- 注册登录：注册开关、手机号验证、密码最小长度。
- 消息与文件：文本长度、本人撤回时限、保留天数；文件硬上限仍由环境变量控制。
- 群聊：最大成员数。
- 好友：申请开关和有效期。
- 公告推送：公告离线推送总开关。
- 音视频：全部通话开关、视频通话开关。
- 风控审核：敏感词开关、举报处理 SLA。
- 维护：维护模式和用户提示。

## 个推、APNs VoIP 与离线通知

| 参数 | 中文说明 | 要求 |
|---|---|---|
| `IM_PUSH_PROVIDER` | 推送提供方 | iOS 来电生产推荐 `getui_apns_voip`；也支持 `getui`、`apns_voip` 或受控 `webhook` |
| `IM_GETUI_APP_ID` | 个推应用编号 | 服务端环境变量 |
| `IM_GETUI_APP_KEY` | 个推应用键 | 服务端环境变量 |
| `IM_GETUI_MASTER_SECRET` | 个推主密钥 | 仅服务端，后台只显示配置状态 |
| `IM_PUSH_WEBHOOK_URL` | 自建推送网关 | 仅 webhook 模式，必须 HTTPS |
| `IM_PUSH_WEBHOOK_TOKEN` | 推送网关令牌 | 密钥项，不回显 |
| `IM_APNS_VOIP_KEY_ID` | Apple APNs Key ID | 10 位大写字母或数字 |
| `IM_APNS_VOIP_TEAM_ID` | Apple Developer Team ID | 10 位大写字母或数字 |
| `IM_APNS_VOIP_BUNDLE_ID` | App Bundle ID | 填 `com.linlitong.imapp`，不要自行追加 `.voip` |
| `IM_APNS_VOIP_PRIVATE_KEY_FILE` | APNs `.p8` 私钥容器内路径 | 只读挂载，禁止进入镜像、Git、日志或后台接口 |
| `IM_APNS_VOIP_SANDBOX` | APNs 环境 | Debug 开发包为 `true`；TestFlight/App Store 为 `false` |

`getui_apns_voip` 会继续通过个推发送 Android 和普通消息；iOS `call.invited` 改走 PushKit，避免同一来电同时出现 CallKit 和普通通知。VoIP 请求固定使用 HTTP/2、TLS 1.2 以上、`apns-push-type: voip`、`apns-topic: com.linlitong.imapp.voip`、优先级 10 和过期时间 0。payload 只包含 `callId`、`conversationId`、`mediaType` 和通用展示文案，不包含姓名、手机号、消息、SDP/ICE 或凭据。

Apple 返回 410、`BadDeviceToken` 或 `DeviceTokenNotForTopic` 后，服务端会清空并停用对应 `apns_voip` token；429、500、503 和网络错误进入耐久队列重试；认证、topic、payload 等永久错误直接进入失败态。生产选择 `apns_voip` 或 `getui_apns_voip` 时，任一配置或 `.p8` 解析失败都会拒绝启动。

推荐把 Apple 下载的 `AuthKey_XXXXXXXXXX.p8` 保存到主机受控目录。服务容器固定以 uid/gid `10001` 运行，因此私钥必须由 uid 或 gid `10001` 持有，权限只能为 `0400` 或 `0440`：

```bash
sudo install -o 10001 -g 10001 -m 0400 \
  AuthKey_XXXXXXXXXX.p8 /data/linli-im/shared/secrets/apns-auth-key.p8
export APNS_VOIP_PRIVATE_KEY_HOST_FILE=/data/linli-im/shared/secrets/apns-auth-key.p8
stat -c '%a %u %g %n' "$APNS_VOIP_PRIVATE_KEY_HOST_FILE"
infra/scripts/validate-production-env.sh .env.production
```

也可使用 `root:10001` + `0440`。不得使用 `0600`、宽于 `0440` 的权限或与容器无关的属主/属组。`infra/scripts/deploy.sh` 和 `infra/scripts/deploy-ip-production.sh` 在 `IM_PUSH_PROVIDER=apns_voip|getui_apns_voip` 时会自动叠加 `infra/compose.apns-voip.yaml` 并在启动前验证容器内可读性；无需手工追加 overlay。若只是手动执行 `docker compose config`，则必须自行包含该 overlay。

正式上线必须同时验证个推在线透传、iOS PushKit 被杀进程来电、Android 厂商离线通道、锁屏接听/拒接、角标、声音和点击跳转。客户端打开或接听后通过鉴权接口读取权威通话数据。

实现约束依据 Apple 官方文档：[Responding to VoIP Notifications from PushKit](https://developer.apple.com/documentation/pushkit/responding-to-voip-notifications-from-pushkit)、[Establishing a token-based connection to APNs](https://developer.apple.com/documentation/usernotifications/establishing-a-token-based-connection-to-apns) 和 [Handling notification responses from APNs](https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns)。

## 对象存储与音视频

| 参数 | 中文说明 | 要求 |
|---|---|---|
| `IM_S3_ENDPOINT` / `IM_S3_PUBLIC_ENDPOINT` | S3/MinIO 内外网地址 | 需重启 |
| `IM_S3_ACCESS_KEY` / `IM_S3_SECRET_KEY` | 对象存储密钥 | 仅服务端，不回显 |
| `IM_S3_BUCKET` / `IM_S3_REGION` | 存储桶与区域 | 兼容存储桶不可直接重命名 |
| `IM_MEDIA_MAX_BYTES` | 服务端单文件上限 | 1 MiB–2 GiB，需重启 |
| `IM_CALL_INVITE_TTL` | 来电邀请超时 | 15 秒–2 分钟，需重启 |
| `IM_RTC_STUN_URLS` | STUN 地址 | 生产必填 |
| `IM_RTC_TURN_URLS` | TURN/TURNS 地址 | 生产真实通话必填 |
| `IM_RTC_TURN_USERNAME` / `IM_RTC_TURN_CREDENTIAL` | TURN 凭据 | 仅服务端，不回显 |

使用仓库内自建 Coturn 时，系统防火墙与云安全组必须同时放行 3478 TCP/UDP 和 49160–49200 UDP；若使用外部托管 TURN，则只开放该服务商明确要求的端口，不应额外暴露应用数据服务。

生产 Compose 不把 MinIO root 凭据交给应用服务。`minio-init` 使用 root 凭据创建独立的 `MINIO_APP_USER` / `MINIO_APP_PASSWORD`，并只授予指定 `IM_S3_BUCKET` 的定位、列举、读取、写入和 multipart 必需权限；应用容器的 `IM_S3_ACCESS_KEY` / `IM_S3_SECRET_KEY` 来自这组最小权限凭据。root 仅用于初始化、备份和受控管理，两组密码必须不同并独立轮换。

`POST /v1/media/{id}/complete` 会读取对象并核验预声明 size、完整 SHA-256 和文件 magic MIME，全部一致后才标记 `ready`。这能拦截传输损坏和常见类型伪装，但不是恶意内容扫描：通用文件的杀毒、压缩包递归检查、媒体安全审核和隔离流程仍须接入外部扫描服务，属于正式开放任意文件前的上线依赖。

会话文本搜索使用 PostgreSQL `pg_trgm` 与 `lower(body->>'text')` 部分 GIN 索引。自建 Compose 会由 schema 初始化扩展；托管 PostgreSQL 上线前必须由数据库管理员预先允许并安装 `pg_trgm`，否则 schema 升级应失败关闭，不能静默退回无索引全表扫描。

通话 SDP/ICE 只经 WebSocket 与 Redis Pub/Sub 在线转发，不写入 PostgreSQL，也不存在服务端 replay。Redis 跨节点发布失败时服务端 fail-closed 并向发送端返回错误；Flutter 端为每条信令生成 `signalId`，在收到对端确认前定时重试，并对已接收的 `signalId` 去重。该重试降低瞬时丢包影响，但不能替代持久信令存储。通话的 accepted/rejected/cancelled/ended/timeout 状态不含 SDP/ICE，会在数据库状态事务内同步写入双方 `userSyncSeq`，供客户端重连收敛。

## 修改流程

1. 热更新业务项：后台修改 → 服务端校验 → 写审计 → 验证真实业务。
2. 基础设施项：备份配置 → 修改 `.env.production` → `make production-validate` → `make production-config` → 滚动重启。
3. 密钥轮换：准备新密钥 → 验证并行窗口 → 切换 → 撤销旧密钥 → 检查日志与会话影响。
4. 发布后验证健康、登录、消息、媒体、群聊、公告、推送、音视频、审计与备份。
