# 消息生命周期 API

本页描述会话归档、送达游标、定时消息、限时消息和安全链接预览。所有接口都要求 `Authorization: Bearer <access-token>`，正文为 JSON。

## 会话偏好与送达游标

- `PATCH /v1/conversations/{id}/preferences`：可提交 `pinned`、`archived`、`notificationsMuted`、`manualUnread`。偏好写入成员记录，并通过 `conversation.preferences.updated` 同步到该用户的其他设备。
- `PUT /v1/conversations/{id}/delivered`，正文 `{"seq": 123}`：服务端按会话最新序号截断并单调更新，返回实际 `seq`。游标变化会产生持久化 `message.delivered` 事件；重复或倒退请求不会重复发事件。

## 定时消息

- `POST /v1/scheduled-messages`：字段为 `conversationId`、`clientMsgId`、`type`、`body`、可选 `replyToId`、可选 `expiresInSeconds`、`scheduledAt`。
- `GET /v1/scheduled-messages?status=pending&limit=50`：列出当前用户的任务。
- `PATCH /v1/scheduled-messages/{id}`：只允许修改仍为 `pending` 的任务。
- `DELETE /v1/scheduled-messages/{id}`：只允许取消仍为 `pending` 的任务。

`scheduledAt` 必须位于当前时间 5 秒之后、365 天以内。`clientMsgId` 在用户范围内唯一；重复创建相同内容返回原任务，不同内容返回冲突。worker 使用数据库租约和 `FOR UPDATE SKIP LOCKED`，崩溃后会重新租约；最终发送仍使用普通消息的 `clientMsgId` 幂等约束，因此“已发送但任务尚未确认”的重启场景不会产生双消息。失败最多尝试 10 次。

## 限时消息

普通发送 `POST /v1/conversations/{id}/messages` 可增加 `expiresInSeconds`。允许值为 `0`（永不过期）或 60 秒至 30 天。

响应消息包含 `expiresAt`。到期 worker 在同一个数据库事务中清空消息正文、写入 `expiredAt`、删除表情/置顶/收藏引用，并为会话成员写入持久化 `message.expired` 事件。历史记录保留消息壳、顺序和审计标识，但不再返回正文。服务重启后仍会继续处理未过期清理的记录。

## 安全链接预览

`POST /v1/link-preview`，正文 `{"url":"https://example.com/article"}`。

服务仅接受 HTTP 80 和 HTTPS 443；拒绝用户信息、私网/回环/链路本地/云元数据和特殊转换地址。每次重定向都会重新做 DNS 与目标地址验证，禁止 HTTPS 降级，最多 3 次跳转。只读取 `text/html`，解压后的正文上限 512 KiB，具有总超时、缓存和并发请求合并。返回值仅含清洗后的标题、摘要及再次安全验证过的绝对图片 URL。

## 媒体清理任务

后台每 10 分钟扫描一次：删除超过 1 小时仍未完成的上传，以及超过 24 小时且未被有效消息或用户头像引用的成品。任务使用数据库租约，可多实例运行；对象删除成功后才删除元数据，失败会保留错误并重试。管理员可通过只读接口 `GET /v1/admin/tasks/media-cleanup` 查看待清理、处理中、失败次数和最近运行时间；该接口不接受命令或脚本，也不能触发删除。
