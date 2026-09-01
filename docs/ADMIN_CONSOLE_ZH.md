# 青蛙呱呱管理后台

管理后台位于 `apps/admin`，生产模式只连接真实 Go API，不包含演示入口。后台定位是运营治理和运行观测，不提供任意读取私聊正文、执行服务器命令或直接修改好友关系的能力。

## 功能与真实数据源

| 页面 | 服务端接口 | 数据与操作 |
| --- | --- | --- |
| 运行概览 | `GET /v2/admin/dashboard` | 用户、群组、消息、举报和审计摘要 |
| 用户管理 | `GET /v2/admin/users`、`POST /users/{id}/ban`、`POST /users/{id}/unban` | 呱呱号修改次数、设备数、定时或永久封禁；封禁到期由服务端自动解除 |
| 群组管理 | `GET /v2/admin/groups`、`GET /groups/{id}`、`GET /groups/{id}/members`、`PATCH/DELETE /groups/{id}/members/{userId}`、`POST /groups/{id}/disband` | 群主、成员、消息、举报、公告、入群策略和成员分页；可对非群主成员设为/取消管理员、禁言/解禁或移出群聊，所有写操作要求二次确认、理由和审计 |
| 举报审核 | `GET /v2/admin/reports`、`POST /reports/{id}/resolve` | 按状态分页；处置动作与审核依据进入审计链 |
| 消息治理索引 | `GET /v2/admin/messages` | 仅检索消息 ID、客户端 ID、会话、发送人、序号和生命周期；正文不返回也不可搜索 |
| 文件与存储 | `GET /v2/admin/media` | 对象键、归属、MIME、大小、校验值和上传状态 |
| WuKong 运行与在线状态 | `/v2/admin/wukong/overview`、`/connections`、`/nodes` | WuKong 节点、真实连接、用户设备和健康状态；Manager Token 不返回浏览器 |
| 关系与反馈 | `GET /v2/admin/friendships`、`GET /v2/admin/feedback` | 好友关系和用户反馈只读分页核对 |
| 推送与任务 | `GET /v2/admin/push`、`GET /v2/admin/tasks`、`GET /v2/admin/access` | 推送设备/队列、定时消息、过期消息、媒体清理、管理员和角色边界 |
| 公告、通话、敏感词 | 对应 `/announcements`、`/calls`、`/sensitive-words` | 公告生命周期、通话元数据、大小写无关的包含匹配拦截规则 |
| WuKongIM 插件 | `/v2/admin/wukong/plugins/*` | 签名生命周期与审计持久保存；stdout/stderr运行日志使用独立的有界脱敏尾部面板，重启后清空 |
| 健康、审计、设置 | `/health`、`/audit-logs`、`/settings` | 服务状态，含成功/失败/IP 的审计，运行时业务策略 |
| 管理员与角色 | `/administrators`、`/roles`、`/auth/me`、`/auth/change-password` | 数据库管理员账号、自定义角色、即时权限刷新、启停与密码管理 |

所有列表接口使用 `q`、业务筛选字段、`cursor` 和 `limit`，返回 `{items,total,nextCursor}`。界面必须保留加载、空数据、错误重试和上一页/下一页状态。

## 权限边界

| 角色 | 主要边界 |
| --- | --- |
| `platform_admin` | 全平台配置、管理员级危险操作和基础设施代理 |
| `system_operator` | WuKong/LiveKit/版本/任务运维，不处理内容审核 |
| `moderator` | 用户、举报、群和敏感内容处置 |
| `content_operator` | 朋友圈、表情、资讯和直播内容运营 |
| `support_agent` | 客服队列、认领、转接、结束和评价工作台 |
| `support` | 只读支持与诊断 |

前端会隐藏或禁用无权限动作，但最终鉴权始终由服务端执行。管理员只使用账号和密码登录，邮箱仅作为可选联系方式；账号、角色和权限来自 PostgreSQL，环境变量只在管理员表为空时初始化首个账号。后台不回显口令或哈希。账号禁用、修改登录账号、改密、重置密码、角色或权限变化都会通过认证版本和实时数据库查询立即使旧权限或旧 JWT 失效。

## 管理写操作规则

- 所有 `/v2/admin/*` JSON 写请求必须同时提交 `confirmed: true` 和 1–500 字的 `reason`；缺少任一字段时统一返回 `CONFIRMATION_REQUIRED`，业务处理器不会执行。签名插件包使用 multipart，请求在专用处理器中执行同等门禁。
- 前端的封禁、解封、群组处置、举报、敏感词、公告、系统设置、版本、内容、频道、客服、WuKong 和 LiveKit 写操作都先显示确认框，并在确认框中要求填写理由。
- 用户封禁支持 24 小时、3 天、7 天、30 天和永久封禁；服务端持久化截止时间并每分钟处理到期记录。
- 所有后台写请求记录操作者、目标、结果、HTTP 状态、来源 IP 和理由；失败门禁、登录失败及权限拒绝同样写入失败审计。
- 自动化测试逐项枚举当前 37 条管理写路由，验证未确认请求全部在统一门禁拒绝；PostgreSQL 集成测试同时验证成功与失败请求的理由进入 `im_audits`。
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

部署时必须设置 `VITE_ALLOW_DEMO=false`，并通过反向代理把 `/api/v2/admin` 指向同源 API。详细安全头、容器运行方式和环境变量见 `apps/admin/README.md` 与 `docs/CONFIGURATION_ZH.md`。
