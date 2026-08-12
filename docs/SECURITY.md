# Security baseline

## 消息加密边界

- 当前首发模式不是端到端加密，不得在产品文案中标注为 E2EE。消息在客户端与网关之间使用 HTTPS/WSS，客户端缓存使用 AES-256-GCM，服务器依靠网络隔离、访问控制、备份加密和最小权限保护数据。
- 用户 REST Access Token 只允许放在 `Authorization: Bearer`；query、请求体和 Cookie 不作为认证来源。登录后的 `ImSession` 返回独立 WuKong UID、派生 Token、设备标识和 TCP/WSS 地址；业务 Access Token 不能直接用于 WuKong 握手。
- Caddy 和应用结构化日志不得记录 query 中的 token、`ImSession.token`、WuKong Manager Token、LiveKit Secret 或 LiveKit 参与者 Token。所有短期 Token 仍按凭据处理，不得进入指标标签、审计元数据或错误上报。
- WuKongIM固定上游曾在连接字符串、认证失败、消息验签、消息发送、Token更新和首帧解析错误中输出AES Key/IV、Token、签名、消息正文、订阅者列表或原始协议帧；仓库内`linli.3`可审计补丁已从源头移除这些值。发布门禁同时使用源码静态扫描、Linux单测和真实日志检查，不能仅依赖日志采集端二次脱敏。
- WuKongIM固定源码的`managerToken`环境名是精确的`WK_MANAGERTOKEN`。生产冒烟必须证明5001无Token返回401，并用同一服务端私密Token验证5001健康和5300节点查询；仅检查容器存活不能证明管理面鉴权已启用。
- 图片、语音、视频和文件只保存私有对象 ID；下载前再次校验会话成员关系并签发短期 URL。退群、封禁或账号删除后权限立即失效。
- 媒体 `complete` 会核验预声明 size、完整 SHA-256 和 magic MIME 后才标记 ready。这是完整性与类型校验，不是恶意内容检测；通用文件仍需外部杀毒、压缩包递归检查、媒体安全审核和隔离处置。
- 推送只包含事件类型、未读数和有限路由 ID，不包含私聊正文、好友验证文字、文件名、位置或联系人信息。
- 服务端应用日志禁止记录请求查询串、Authorization、验证码、推送 CID、消息正文、SDP/ICE 和数据库连接串。
- 若以后增加端到端密聊，必须采用经过公开审计的 Signal Protocol 或 IETF MLS 实现，完成多设备密钥、前向保密、失陷后安全、设备验证、换机恢复、群成员变更和密钥销毁测试；禁止自研密码算法。

## 国内发行边界

- 首发普通单聊和群聊保留平台举报、风控与依法处置能力，并按国内 APP 备案、后台实名、群组管理和网络日志要求落地。
- 端到端密聊若启用，应作为独立且明确标识的能力，在备案、安全评估和法律意见确认后开放；不能用“服务器看不到”规避平台主体责任。

## Required before production

- Replace all example secrets; production has no shared administrator bootstrap key.
- Terminate TLS at a managed ingress and use TLS inside untrusted networks.
- Configure short access-token and rotating refresh-token lifetimes.
- Enable PostgreSQL backups with point-in-time recovery and test restoration.
- Restrict object storage to signed URLs and block public bucket access.
- Configure the HTTPS OTP/push gateways with scoped bearer tokens; keep their SMS, APNs and FCM credentials in a secret manager.
- Run MinIO with separate root and application credentials. The API receives only the bucket-scoped least-privilege application identity; root is reserved for bootstrap, backup and controlled administration.
- For APNs VoIP, keep the host `.p8` at mode `0400` or `0440` and owned by container uid or gid `10001`. Production validation must fail for broader permissions or unrelated ownership.
- Connect a production content-moderation provider and staffed appeal workflow.
- Publish privacy, retention, deletion and community-policy documents.
- Run dependency, static, dynamic and abuse-case security testing.
- Run `infra/scripts/validate-production-env.sh`; production must reject placeholder secrets, development verification codes and noop/log push providers.
- Keep only the TLS gateway public. PostgreSQL, Redis, MinIO, Prometheus, Grafana, `/metrics` and `/ready` stay private.
- Enable `IM_TRUST_PROXY` only when the API is reachable exclusively through the managed gateway; this lets per-IP limits use Caddy's sanitized forwarded address without trusting client-supplied headers on direct binds.
- Use named administrator accounts, a bcrypt password hash, TOTP, short-lived JWTs and server-side RBAC. Never place passwords, hashes or TOTP seeds in `VITE_*` variables; the console stores only the active JWT in `sessionStorage`.

## Trust boundaries

Clients are untrusted. Conversation membership, administrative permissions, message sizes, MIME types and sequence ownership are always validated by the server. Redis and log storage contain operational data and must not expose message bodies unnecessarily.

Call lifecycle commands are delivered through WuKongIM and durable call metadata is stored in PostgreSQL. LiveKit alone carries SDP/ICE and media; neither Redis nor the Go API is a signaling transcript or replay log. Clients must not treat a business API response as proof that the peer received a call event.

Backup jobs use `umask 077`, write to `.incomplete-*`, checksum and permission-tighten all content, then atomically rename within `BACKUP_DIR`. An incomplete directory is a failed backup and must never be selected for restore.

## Abuse controls

Rate limits apply independently to login, search, friend requests, group creation, message sending, media uploads and reporting. Administrator actions produce immutable audit records. User-facing blocking takes effect before delivery rather than merely hiding content in the UI.

Client-side permission states are usability controls, not an authorization boundary. Every administrative endpoint must validate the short-lived administrator JWT and permission server-side. Production rejects the legacy shared key; named identities, MFA, revocation and immutable audit logs are mandatory.

The IP/certbot `infra/compose.ip.yaml` stack is test-only because it fixes development mode and a static OTP. Production must use `.env.ip.production.example`, `infra/compose.ip.production.yaml`, and `infra/scripts/deploy-ip-production.sh`; production validation requires `DevMode=false` and a real HTTPS OTP provider.
