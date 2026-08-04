# 邻里通讯消息内容协议 v1

本文只描述聊天消息的类型和内容结构。网络层使用 HTTPS/WSS，消息正文统一使用 UTF-8 JSON；当前线上协议版本为 `1`。

## 0. 完整类型表（实现白名单）

协议只保留 9 种内容类型；回复、转发、撤回、已读和通话不再重复造消息类型，因此覆盖完整但结构保持精简。

| `type` | `body` 最小字段 | 谁可产生 | 用途 |
|---|---|---|---|
| `text` | `text` | 客户端 | 文字与 Unicode Emoji |
| `image` | `mediaId` | 客户端 | 图片 |
| `audio` | `mediaId`, `duration?` | 客户端 | 语音/音频 |
| `video` | `mediaId`, `duration?` | 客户端 | 视频 |
| `file` | `mediaId` | 客户端 | 任意允许文件 |
| `location` | `latitude`, `longitude`, `name`, `address` | 客户端 | 位置 |
| `contact` | `userId` | 客户端 | 联系人名片 |
| `chat_history` | `mode`, `entries` | 服务端 | 合并转发记录卡片 |
| `system` | 服务端业务字段 | 服务端 | 入退群、群管理等可信提示 |

客户端发送白名单为前 7 种。`chat_history` 和 `system` 只能由服务端产生；未知类型必须拒绝发送，但旧客户端接收时仍推进同步游标。

## 1. 发送消息

WebSocket 请求外层：

```json
{
  "version": 1,
  "requestId": "req_01J...",
  "type": "message.send",
  "payload": {
    "conversationId": "conv_01J...",
    "clientMsgId": "ios-A1B2-000001",
    "messageType": "text",
    "body": {
      "text": "你好"
    },
    "replyToId": ""
  }
}
```

同一结构也可通过 `POST /v1/conversations/{conversationId}/messages` 发送，但 HTTP 请求体不需要 WebSocket 外层，只提交 `clientMsgId`、`type`、`body`、`replyToId`。

字段规则：

| 字段 | 必填 | 说明 |
|---|---:|---|
| `version` | 是 | 协议版本，当前固定为 `1` |
| `requestId` | 是 | 单次请求关联号，用来匹配 ACK；重试时可以变化 |
| `conversationId` | 是 | 单聊或群聊会话 ID |
| `clientMsgId` | 是 | 客户端生成的消息幂等号，同一发送者内唯一；重试必须保持不变，最长 100 字符 |
| `messageType` | 是 | 内容类型，见下表 |
| `body` | 是 | 对应消息类型的内容体 |
| `replyToId` | 否 | 被引用的服务端消息 ID；回复不是独立消息类型 |

## 2. 消息类型和 body

### 2.1 文本 `text`

```json
{
  "text": "你好，今晚一起吃饭吗？",
  "mentions": ["usr_01J..."],
  "mentionAll": false
}
```

- `text` 必填，去除首尾空白后不能为空。
- 默认最多 5000 个 Unicode 字符，可在后台设置。
- 普通 Emoji 直接作为 Unicode 文本发送，不另外定义 Emoji 类型。
- `mentions` 可选，只能是最多 50 个去重后的用户 ID 字符串；不能提交昵称、手机号或用户对象。仅群消息可用，且每个 ID 必须是当前群成员。
- `mentionAll` 可选，默认 `false`；仅群主和管理员可设为 `true`。不要使用 `{"userId":"all"}` 等伪用户占位。
- 转发消息会保留原正文用于展示，但不会再次触发 @ 推送。

### 2.2 图片 `image`

```json
{
  "mediaId": "med_01J...",
  "mime": "image/jpeg",
  "fileName": "IMG_0001.jpg",
  "size": 2384102,
  "checksum": "sha256-hex"
}
```

### 2.3 语音 `audio`

```json
{
  "mediaId": "med_01J...",
  "mime": "audio/mp4",
  "fileName": "voice.m4a",
  "size": 48122,
  "checksum": "sha256-hex",
  "duration": 8
}
```

- `duration` 单位为秒。
- 协议统一使用 `audio`；客户端可把旧值 `voice` 作为兼容输入，但不得再产生新 `voice` 消息。

### 2.4 视频 `video`

```json
{
  "mediaId": "med_01J...",
  "mime": "video/mp4",
  "fileName": "video.mp4",
  "size": 18348102,
  "checksum": "sha256-hex",
  "duration": 23
}
```

### 2.5 文件 `file`

```json
{
  "mediaId": "med_01J...",
  "mime": "application/pdf",
  "fileName": "项目说明.pdf",
  "size": 834102,
  "checksum": "sha256-hex"
}
```

图片、语音、视频、文件都必须先完成三步：

1. `POST /v1/media/presign` 取得 `mediaId` 和上传地址。
2. 客户端直传 MinIO/S3。
3. `POST /v1/media/{mediaId}/complete` 校验完成后，再发送聊天消息。

数据库只保存 `mediaId` 和安全元数据，不保存短期下载签名。接收方读取历史或同步时，服务端在鉴权通过后临时补充 `downloadUrl`。

### 2.6 位置 `location`

```json
{
  "latitude": 39.9042,
  "longitude": 116.4074,
  "name": "社区东门",
  "address": "北京市东城区示例路 1 号"
}
```

- 纬度范围 `-90..90`，经度范围 `-180..180`。
- `name` 必填，最多 80 字符；`address` 为字符串，最多 240 字符，可以为空字符串。
- 不允许附带手机号、设备定位权限等额外字段。

### 2.7 联系人名片 `contact`

客户端最小请求：

```json
{
  "userId": "usr_01J..."
}
```

服务端校验用户后保存规范化内容：

```json
{
  "userId": "usr_01J...",
  "name": "林小满",
  "handle": "linxiaoman",
  "avatarUrl": "https://..."
}
```

名片严禁携带手机号、备注、标签等隐私字段。昵称、账号和头像由服务端读取，客户端不能伪造。

### 2.8 合并转发 `chat_history`

该类型只能由服务端的转发接口生成，客户端不能直接构造：

```json
{
  "forwarded": true,
  "mode": "merged",
  "entries": [
    {
      "sourceMessageId": "msg_01J...",
      "senderId": "usr_01J...",
      "createdAt": "2026-07-31T15:20:00Z",
      "type": "text",
      "summary": "你好"
    }
  ]
}
```

逐条转发保持原消息类型，并由服务端在 `body` 中增加：

```json
{
  "forwarded": true,
  "sourceMessageId": "msg_01J..."
}
```

### 2.9 系统消息 `system`

入群、退群、撤人、群主变更等系统提示只能由服务端可信业务生成。普通客户端提交 `system` 会被拒绝，防止伪造“管理员通知”。

## 3. 服务端标准消息对象

发送成功、历史记录和离线同步都返回同一种消息对象：

```json
{
  "id": "msg_01J...",
  "clientMsgId": "ios-A1B2-000001",
  "conversationId": "conv_01J...",
  "senderId": "usr_01J...",
  "conversationSeq": 1052,
  "type": "text",
  "body": {
    "text": "你好"
  },
  "replyToId": "",
  "editedAt": "2026-07-31T15:21:00.123Z",
  "editVersion": 1,
  "reactions": [
    {"emoji": "👍", "count": 3, "reactedByMe": true}
  ],
  "createdAt": "2026-07-31T15:20:00.123Z"
}
```

| 字段 | 说明 |
|---|---|
| `id` | 服务端全局消息 ID，以它执行撤回、引用和管理操作 |
| `clientMsgId` | 发送幂等号；网络超时重发相同值不会产生第二条消息 |
| `conversationSeq` | 会话内严格递增序号，用于排序和检查缺口 |
| `type` / `body` | 消息内容类型及内容体 |
| `replyToId` | 被引用消息 ID，没有引用时为空或省略 |
| `recalledAt` | 被撤回时出现；撤回后正文会按权限清理 |
| `editedAt` / `editVersion` | 正文被编辑后出现的最后编辑时间和单调递增版本 |
| `reactions` | Emoji 聚合；`reactedByMe` 按当前登录用户计算，不是公共缓存字段 |
| `createdAt` | 服务端入库时间，客户端时间不能决定消息顺序 |

## 4. ACK、实时通知和补偿

持久化事务成功后才回复 ACK：

```json
{
  "version": 1,
  "requestId": "req_01J...",
  "type": "message.ack",
  "payload": {
    "duplicate": false,
    "message": {
      "id": "msg_01J...",
      "clientMsgId": "ios-A1B2-000001",
      "conversationId": "conv_01J...",
      "senderId": "usr_01J...",
      "conversationSeq": 1052,
      "type": "text",
      "body": {"text": "你好"},
      "createdAt": "2026-07-31T15:20:00.123Z"
    }
  }
}
```

接收方在线时收到 `message.created`；断线、切后台或节点切换后，用用户同步游标补齐：

```json
{
  "version": 1,
  "type": "sync.result",
  "payload": {
    "cursor": 8891,
    "hasMore": false,
    "events": [
      {
        "userSyncSeq": 8891,
        "type": "message.created",
        "payload": {"message": {}}
      }
    ]
  }
}
```

`requestId` 只负责一次请求关联，`clientMsgId` 负责消息幂等，`conversationSeq` 负责会话顺序，`userSyncSeq` 负责跨会话离线补偿，四者用途不能混用。

## 5. 不属于消息类型的动作

下列内容使用事件/命令，不混进 `messageType`：

| 功能 | 协议类型 |
|---|---|
| 已读 | `message.read` / `message.read.ack` |
| 撤回 | REST 撤回接口 + `message.recalled` 事件 |
| 编辑 | REST 编辑接口 + `message.edited` 事件 |
| Emoji 回应 | REST 增删接口 + `message.reaction.updated` 事件 |
| 群消息置顶 | REST 置顶接口 + `group.message.pinned` / `group.message.unpinned`，并生成持久系统消息 |
| 正在输入 | `typing` / `typing.ack`，瞬时事件，不入库 |
| 音视频通话 | `call.offer`、`call.answer`、`call.ice`、`call.end`、`call.signal.received`，只传 WebRTC 信令 |
| 好友/群变更 | 独立业务事件，不伪装成用户聊天消息 |
| 离线推送 | 只带事件类型、未读数和路由 ID，不携带私聊正文 |

### 5.1 消息协作 REST 接口

| 方法与路径 | 请求/查询 | 规则 |
|---|---|---|
| `PATCH /v1/messages/{id}` | `{"editId":"edit_...","text":"...","mentions":["usr_..."],"mentionAll":false}` | 仅原作者、仅文本、未撤回，编辑窗口与撤回窗口使用同一后台配置；`editId` 建议客户端生成并在重试时复用，缺省时服务端仍会对相同正文重试判重 |
| `GET /v1/messages/{id}/edits` | 无 | 当前会话成员可读取版本 `0..N`；撤回后禁止读取历史正文 |
| `PUT /v1/messages/{id}/reactions/{emoji}` | 无 | 添加回应；目前 Emoji 白名单为 `👍 ❤️ 😂 😮 😢 😡 👏 🎉 🙏` |
| `DELETE /v1/messages/{id}/reactions/{emoji}` | 无 | 移除自己的回应；增删都幂等，响应含更新后的 `message` |
| `PUT /v1/conversations/{cid}/pinned-messages/{mid}` | 无 | 仅群主/管理员，可重复调用 |
| `DELETE /v1/conversations/{cid}/pinned-messages/{mid}` | 无 | 仅群主/管理员，可重复调用 |
| `GET /v1/conversations/{cid}/pinned-messages?before=&limit=` | `before` 为置顶时间的毫秒时间戳 | 当前群成员可读；另有等价 `/v1/groups/{cid}/pinned-messages` 路径 |
| `GET /v1/conversations/{cid}/messages/search?q=&beforeSeq=&limit=` | `q` 最多 100 字符 | 仅搜索当前会话、当前未撤回文本；按会话序号倒序分页 |

编辑、回应和置顶写入与同步事件在同一数据库事务内提交。编辑保留原始版本和每次新版本；回应以 `(messageId,userId,emoji)` 唯一；置顶以 `(conversationId,messageId)` 唯一。审计记录不保存编辑正文。

离线推送不包含 `text`、`mentions` 数组或用户资料。服务端最多增加当前接收者自己的 `mentioned: true/false` 布尔值，客户端收到后仍以同步接口中的权威消息为准。

### 5.2 通话信令最小结构

所有信令都必须带 `conversationId`、`callId` 和客户端生成的 `signalId`。服务端鉴权参与者、限制通话状态并补充 `fromUserId`、`serverTime`；不保存 SDP、ICE 或媒体内容。

| 信令 | 额外字段 |
|---|---|
| `call.offer` | `calleeUserId`, `mediaType` (`audio`/`video`), `sdp`, `type` |
| `call.answer` | `sdp`, `type` |
| `call.ice` | `candidate`（包含 candidate、sdpMid、sdpMLineIndex） |
| `call.end` | `reason` |
| `call.signal.received` | 被确认的 `signalId` |

接收端对每个信令回复 `call.signal.received`；发送端收到 `call.signal.ack` 后停止有界重试。服务端对请求本身另回 `<原信令>.ack`，其 `requestId` 与请求一致。

### 5.3 完整实时事件集合

| 分类 | 事件 |
|---|---|
| 会话 | `message.created`, `message.edited`, `message.reaction.updated`, `message.recalled`, `message.read`, `typing` |
| 好友 | `friend.request`, `friend.request.sent`, `friend.request.updated`, `friend.accepted`, `friend.removed`, `friend.metadata.updated`, `friend.account_deleted` |
| 群组 | `group.created`, `group.joined`, `group.left`, `group.members.updated`, `group.profile.updated`, `group.announcement.updated`, `group.invite`, `group.invite.updated`, `group.message.pinned`, `group.message.unpinned`, `group.disbanded`, `group.system` |
| 通话 | `call.invited`, `call.offer`, `call.answer`, `call.ice`, `call.accepted`, `call.rejected`, `call.cancelled`, `call.timeout`, `call.ended`, `call.signal.ack` |

业务事件的权威副本进入用户同步流；其中 `call.accepted/rejected/cancelled/timeout/ended` 与通话状态变更在同一数据库事务内写入双方同步流。`typing`、SDP、ICE 与信令确认仅瞬时分发，不进入持久 payload。瞬时信令由客户端按 `signalId` 确认、重试和去重；断线后以 `sync` 补偿持久状态事件。

## 6. 版本兼容规则

1. 新增可选字段时，旧客户端必须忽略未知字段。
2. 删除字段、改变字段含义或改变必填规则时，升级协议版本。
3. 未识别 `messageType` 时客户端显示“当前版本暂不支持此消息”，但仍推进 `userSyncSeq`，避免同步卡死。
4. JSON 是 v1 的实际传输格式；`packages/protocol/im/v1/envelope.proto` 是等价的中立结构，为以后协商 Protobuf 二进制 v2 保留升级入口。
