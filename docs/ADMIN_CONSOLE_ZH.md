# 邻里通讯管理后台

管理后台位于 `apps/admin`，生产模式只连接真实 Go API，不包含演示入口。后台定位是运营治理和运行观测，不提供任意读取私聊正文、执行服务器命令或直接修改好友关系的能力。

## 功能与真实数据源

| 页面 | 服务端接口 | 数据与操作 |
| --- | --- | --- |
| 运行概览 | `GET /v1/admin/dashboard` | 用户、群组、消息、举报和审计摘要 |
| 用户管理 | `GET /v1/admin/users`、`POST /users/{id}/ban`、`POST /users/{id}/unban` | 邻里号修改次数、设备数、定时或永久封禁；封禁到期由服务端自动解除 |
| 群组管理 | `GET /v1/admin/groups`、`GET /groups/{id}`、`GET /groups/{id}/members`、`POST /groups/{id}/disband` | 群主、成员、消息、举报、公告、入群策略和成员分页 |
| 举报审核 | `GET /v1/admin/reports`、`POST /reports/{id}/resolve` | 按状态分页；处置动作与审核依据进入审计链 |
| 消息治理索引 | `GET /v1/admin/messages` | 仅检索消息 ID、客户端 ID、会话、发送人、序号和生命周期；正文不返回也不可搜索 |
| 文件与存储 | `GET /v1/admin/media` | 对象键、归属、MIME、大小、校验值和上传状态 |
| 在线状态 | `GET /v1/admin/online` | 当前实例的 WebSocket 用户和连接数，不展示来源 IP |
| 关系与反馈 | `GET /v1/admin/friendships`、`GET /v1/admin/feedback` | 好友关系和用户反馈只读分页核对 |
| 推送与任务 | `GET /v1/admin/push`、`GET /v1/admin/tasks`、`GET /v1/admin/access` | 推送设备/队列、定时消息、过期消息、媒体清理、管理员和角色边界 |
| 公告、通话、敏感词 | 对应 `/announcements`、`/calls`、`/sensitive-words` | 公告生命周期、通话元数据、大小写无关的包含匹配拦截规则 |
| 健康、审计、设置 | `/health`、`/audit-logs`、`/settings` | 服务状态，含成功/失败/IP 的审计，运行时业务策略 |

所有列表接口使用 `q`、业务筛选字段、`cursor` 和 `limit`，返回 `{items,total,nextCursor}`。界面必须保留加载、空数据、错误重试和上一页/下一页状态。

## 权限边界

| 角色 | 只读 | 用户与举报 | 内容规则 | 群组、公告、设置 |
| --- | --- | --- | --- | --- |
| `platform_admin` | 是 | 可写 | 可写 | 可写 |
| `moderator` | 是 | 可写 | 可写 | 不可写 |
| `support` | 是 | 不可写 | 不可写 | 不可写 |

前端会隐藏或禁用无权限动作，但最终鉴权始终由服务端执行。生产环境禁止共享管理员密钥，只允许邮箱、密码和可选 TOTP 登录。管理员账号来自环境配置，后台不回显口令、哈希或 TOTP 种子，也不在当前版本内远程创建高权限账号。

## 高风险操作规则

- 封禁、解封、解散群组、删除敏感词、删除公告和举报处置必须提交非空理由。
- 用户封禁支持 24 小时、3 天、7 天、30 天和永久封禁；服务端持久化截止时间并每分钟处理到期记录。
- 所有后台写请求记录操作者、目标、结果、HTTP 状态和来源 IP；登录失败及权限拒绝同样写入失败审计。
- 消息治理页不返回或搜索私聊正文。涉及内容的处置必须从用户举报及其证据链进入。
- 推送与任务页只读，不包含执行命令、清空队列或手工删除媒体的按钮。

## 上线检查

```bash
cd server
go test ./...
go vet ./...

cd ../apps/admin
npm ci
npm run lint
npm test -- --run
npm run build
npm audit --audit-level=high
```

部署时必须设置 `VITE_ALLOW_DEMO=false`，并通过反向代理把 `/api/v1/admin` 指向同源 API。详细安全头、容器运行方式和环境变量见 `apps/admin/README.md` 与 `docs/CONFIGURATION_ZH.md`。
