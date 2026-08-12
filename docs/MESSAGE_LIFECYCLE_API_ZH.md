# 消息生命周期 API

本页描述会话归档、送达游标、定时消息、限时消息和安全链接预览。所有接口都要求 `Authorization: Bearer <access-token>`，正文为 JSON。

## 会话偏好与送达游标

- `PATCH /v2/channels/conversations/{id}/preferences`：可提交 `pinned`、`archived`、`notificationsMuted`、`manualUnread`。偏好写入 PostgreSQL，并通过 WuKongIM CMD 通知该用户的设备重新拉取。
- `PUT /v2/channels/conversations/{id}/delivered`，正文 `{"seq": 123}`：服务端按已索引的 WuKongIM 频道序号截断并单调更新，返回实际 `seq`。变化时通过 WuKongIM CMD 通知成员；重复或倒退请求不会重复发事件。

## 定时消息

- `POST /v2/messages/scheduled`：字段为 `conversationId`、`clientMsgId`、`type`、`body`、可选 `replyToId`、可选 `expiresInSeconds`、`scheduledAt`。
- `GET /v2/messages/scheduled?status=pending&limit=50`：列出当前用户的任务。
- `PATCH /v2/messages/scheduled/{id}`：只允许修改仍为 `pending` 的任务。
- `DELETE /v2/messages/scheduled/{id}`：只允许取消仍为 `pending` 的任务。

`scheduledAt` 必须位于当前时间 5 秒之后、365 天以内。`clientMsgId` 在用户范围内唯一；重复创建相同内容返回原任务，不同内容返回冲突。worker 使用数据库租约和 `FOR UPDATE SKIP LOCKED`，崩溃后会重新租约；最终通过 WuKongIM 服务端发送接口并沿用稳定 `clientMsgId`，因此“已发送但任务尚未确认”的重启场景不会产生双消息。失败最多尝试 10 次。

## 限时消息

客户端通过 WuKongIM SDK 发送时可在消息正文中携带服务端校验过的 `expiresInSeconds`；服务端代发接口也接受该字段。允许值为 `0`（永不过期）或 60 秒至 30 天。

消息正文包含跨 SDK 一致的绝对 `expiresAt`；支持原生字段的平台同时发送 WuKong `expire`。到期 worker 在同一个数据库事务中标记业务索引、删除回应/置顶/收藏/扩展/提醒和失效媒体授权，并通过 CMD 发送 `message.expired`；WuKongIM 原始消息不物理改写。服务重启后仍会继续处理未完成的到期任务。

## 安全链接预览

`POST /v2/link-preview`，正文 `{"url":"https://example.com/article"}`。

服务仅接受 HTTP 80 和 HTTPS 443；拒绝用户信息、私网/回环/链路本地/云元数据和特殊转换地址。每次重定向都会重新做 DNS 与目标地址验证，禁止 HTTPS 降级，最多 3 次跳转。只读取 `text/html`，解压后的正文上限 512 KiB，具有总超时、缓存和并发请求合并。返回值仅含清洗后的标题、摘要及再次安全验证过的绝对图片 URL。

## 媒体清理任务

后台每 10 分钟扫描一次：删除超过 1 小时仍未完成的上传，以及超过 24 小时且未被有效消息或用户头像引用的成品。任务使用数据库租约，可多实例运行；对象删除成功后才删除元数据，失败会保留错误并重试。管理员可通过只读接口 `GET /v2/admin/tasks/media-cleanup` 查看待清理、处理中、失败次数和最近运行时间；该接口不接受命令或脚本，也不能触发删除。
