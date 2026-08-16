# 青蛙呱呱 WuKongIM 消息协议

本文是当前客户端与业务服务使用的消息内容约定。网络握手、ACK、消息 ID、频道序号、去重、离线消息和最近会话遵循固定 WuKongIM Server `v2.2.5-20260422` 及对应官方 SDK，不再维护自研 `/v1/ws` 包络或 `/v1/sync` 游标协议。

## 1. 传输与会话

1. 客户端先通过 `/v2/auth/*` 登录并取得业务 Access Token 与 `ImSession`。
2. Android/iOS 使用官方 Flutter SDK 走 TCP；Web 使用官方 JS SDK 走 WSS；macOS 使用独立平台 Gateway。
3. `ImSession` 中的用户 ID、设备标识和 IM Token 只用于 WuKongIM；REST Access Token 不进入 IM URL。
4. 页面统一依赖 `ImRepository`，平台 SDK 差异封装在 `WukongGateway` 下。
5. CMD 内容类型固定为 `99`，只作为重新同步通知，不作为业务权威副本。

## 2. 内容类型

| `type` | 名称 | 关键字段 | 产生方 |
|---:|---|---|---|
| 1 | 文本 | `content` | 客户端 |
| 2 | 图片 | `url` 或 `mediaId` | 客户端 |
| 3 | GIF | `url` 或 `mediaId` | 客户端 |
| 4 | 语音 | `url` 或 `mediaId`、时长 | 客户端 |
| 5 | 视频 | `url` 或 `mediaId`、时长 | 客户端 |
| 6 | 位置 | `latitude`、`longitude`、名称/地址 | 客户端 |
| 7 | 名片 | `userId`，展示字段由服务端校准 | 客户端 |
| 8 | 文件 | `url` 或 `mediaId` | 客户端 |
| 1001 | 合并聊天记录 | `schemaVersion: 1`、`entries`（1–100项） | 业务服务 |
| 1002 | 系统事件 | `schemaVersion: 1`、`event` | 业务服务 |
| 1003 | 商店贴纸 | `schemaVersion: 1`、`stickerId` | 客户端/业务服务 |
| 1004 | 朋友圈分享 | `schemaVersion: 1`、`momentId` | 客户端/业务服务 |
| 1005 | 通话事件 | `schemaVersion: 1`、`event: call.*`、通话路由字段 | 业务服务 |
| 1006 | 直播互动 | `schemaVersion: 1`、`event: live.*` | 受策略校验的客户端/业务服务 |
| 1007 | 客服事件 | `schemaVersion: 1`、`event: support.*`、`sessionId` | 业务服务 |
| 1008 | 截屏提示 | `schemaVersion: 1`、`event: screenshot.taken` | 支持真实检测的平台 |

所有 `1001–1008` 自定义正文必须包含整数 `schemaVersion: 1`。未知内容类型不能发送；接收时显示“当前版本暂不支持此消息”，但不得阻塞频道序号推进。

`1001`、`1002`、`1005`、`1007` 等服务端拥有的类型不能由普通客户端伪造；合并转发必须经过业务 API 校验每条来源消息的访问权。策略插件会检查频道成员、好友关系、禁言/封禁、敏感词、频道类型、位置范围、名片标识、贴纸/朋友圈持久引用、自定义版本和事件名。截屏提示的显示文案根据真实发送者生成，不信任正文中的自填姓名。

## 3. 客户端消息幂等与媒体

- 每次发送生成稳定的 `clientMsgNo`；超时重试必须复用，不能用重复正文判断幂等。
- WuKongIM 返回的 `messageId` 与 `messageSeq` 是消息标识和频道顺序来源。
- 发送状态映射为发送中、成功、失败和可重试；送达、已读、撤回及远程扩展从业务 DataSource 合并。
- 图片、GIF、语音、视频和文件先向业务 API 申请预签名地址，直接上传 MinIO，完成 size/SHA-256/MIME 校验，再绑定到 WuKongIM 消息。SDK 直发策略以数据库中的真实 MIME 为权威，拒绝图片/GIF/语音/视频类型伪装以及与持久元数据不一致的 MIME 声明。
- 推送载荷只包含路由 ID、内容类型和未读/提及标识，不携带聊天正文、文件名、位置或联系人隐私字段。

## 4. 扩展、回应、提醒与 CMD

编辑、撤回、Emoji 回应、置顶、收藏、送达/已读、提醒和定时状态的权威副本位于 PostgreSQL。原始消息正文不做物理覆盖。

业务事务写入扩展状态与持久 Outbox；Outbox 向目标用户发送 WuKongIM CMD。客户端收到 CMD 后调用 `/v2/im/datasource/extensions`、`message-extras`、`reminders` 或对应业务接口重新读取权威状态。重复 CMD 必须幂等，漏掉 CMD 也可在重连同步时收敛。

## 5. 流式消息与事件消息

流式文本使用固定 WuKongIM Server v2.2.5 的消息事件协议，不用连续普通消息模拟：

1. `POST /v2/messages/conversations/{id}/streams` 经业务权限校验后调用 WuKong `/message/send`，发送正文类型 `1` 的锚点并设置 `is_stream: 1`。锚点正文仅是可选占位文案，不属于事件快照；客户端收到首个 `stream.delta` 时替换占位文案，之后才顺序追加。服务端对应消息 `setting` 包含位 `2`（`1 << 1`）；位 `32` 是 Signal，不能混用。
2. `POST /v2/messages/conversations/{id}/streams/{clientMsgNo}/events` 追加事件；`eventId` 是幂等键，发送者、锚点所有权、频道和消息策略必须再次校验。
3. `GET /v2/messages/conversations/{id}/streams/{clientMsgNo}/events` 从 `fromMsgEventSeq` 增量同步事件；历史消息中的 `event_meta.events[].snapshot` 可直接恢复最终文本。
4. 实时事件按固定协议包类型 `12` 解码（`10/11`分别是SUB/SUBACK）；字段顺序为事件 ID、事件类型、64 位时间戳和剩余 JSON 对象。Android/iOS 由本地锁定的 Flutter SDK 最小补丁处理，Web 使用 JS SDK `eventManager`，macOS 使用 Easy SDK `customEvent`。

当前只开放通用文本流：`stream.delta`、`stream.snapshot`、`stream.close`、`stream.error`、`stream.cancel` 和 `stream.finish`。事件可见性固定为 `public`，事件键仅允许受限字符；`stream.close` 只接受 `end_reason`，`stream.finish` 由 WuKongIM 强制使用 `__finish__` 且无业务载荷。客户端按 `eventId` 去重、串行处理高频事件，并支持事件先于锚点到达。delta是内存事件，在已有持久snapshot后可复用当前事件序号，因此不能仅依赖序号去重。固定提交上的可审计服务端补丁已经修复`delta → snapshot → delta → close`的内存快照回退，并通过Linux单测和真实容器终态投影验证；混合snapshot流可以进入候选版本，但仍须完成各目标端实时事件验收。AI Agent、工具调用和私有事件正文均不启用。

## 6. 频道类型

| 类型 | 用途 |
|---:|---|
| 1 | 单聊 |
| 2 | 群聊 |
| 3 | 客服 |
| 4 | 社区 |
| 5 | 社区话题 |
| 6 | 资讯 |
| 9 | 直播互动 |
| 10 | 访客 |
| 11/12 | AI Agent 预留，当前禁用 |

频道资料、成员、黑白名单、临时成员、禁言和发言策略以 PostgreSQL 业务状态为准，通过持久 Outbox 同步到 WuKongIM，并由 Reconciler 修复遗漏。固定服务端对社区话题频道 ID 的 Manager API 限制记录在功能矩阵中；未获确认前不修改固定服务端或伪造支持。

## 7. 音视频

音视频不传递 `call.offer`、`call.answer` 或 `call.ice` 自研消息。业务 API 创建 2–9 人通话并签发短期、参与者范围的 LiveKit Token；LiveKit SDK/Server 负责媒体协商、音视频、屏幕共享、活动发言人和重连。PostgreSQL 只保存通话及成员状态、时间和结束原因，WuKongIM CMD 通知客户端重新同步。

## 8. 验证依据

- 固定版本、镜像摘要和源码提交：`docs/WUKONGIM_PROTOCOL_BASELINE.md`
- 功能入口与验收状态：`docs/WUKONGIM_FEATURE_MATRIX.md`
- 真实协议探针：`tools/wukong-probe`
- Flutter 编解码注册表：`apps/mobile/lib/im/message_content_registry.dart`
- 服务端策略入口：`server/internal/httpapi/wukong_policy.go`
