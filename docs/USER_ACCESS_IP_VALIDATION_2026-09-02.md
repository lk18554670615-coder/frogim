# 用户 IP 记录与归属地：实现和验证

> 后续变更（2026-09-03）：按产品确认增加普通 PC 用户查看单聊对方最近登录 IP 的专用接口与展示，见 [PC 单聊登录 IP](PC_PEER_LOGIN_IP_2026-09-03.md)。以下保留原阶段的实现记录；“仅后台返回 IP／无需更新 Flutter”不适用于该后续功能。注册 IP、完整认证历史和共享用户模型的边界不变。

## 交付范围

数据库 59 → 60，纯新增 `im_user_access_profiles` 和 `im_user_access_logs`（IPv4/IPv6 使用 `inet`）。不清空业务数据，不修改 Flutter、消息协议或共享用户模型。

- 密码注册、验证码首次开户同时留下注册事实；登录摘要仅在业务会话成功签发后更新。并发写入按服务端 UTC 时间和事件 ID 排序，旧事件不能覆盖新摘要。
- 密码、验证码、扫码领取记录成功与失败；扫码使用领取电脑的请求 IP。短信发送、待确认扫码轮询、续期、IM 重连不算登录。
- 失败只存固定代码，不存密码、验证码、Token、请求正文。用于查找已存在用户的手机号只短暂留在队列内，写入时丢弃。未知账号的用户 ID 为空；失败不能作为同 IP 关联事实。
- 后台单个、批量开户显示“后台创建”，用户注册 IP 为空；既有用户注册来源/IP 显示“未记录”，不从登录 IP 补造。
- IP 摘要仅由后台专用 DTO 返回。用户列表支持精确 IP + 来源与原关键词/状态组合查询；用户详情有“登录记录”，列表工具栏有全局“登录日志”，IP 可复制或查看去重关联账号。没有新侧栏菜单。
- `GET /v2/admin/user-access-logs` 默认 30 天、20 条，最多 180 天、100 条；按 `(occurred_at,id)` 倒序游标分页。IP 查询、关联和详细日志查看写管理员审计，仅存条件和数量。
- 队列容量 1000、2 个 worker、每条最多 3 次尝试（每次 2 秒超时，间隔 1/2 秒）；日志和摘要同事务，重试 ID 不变。队列满/重试耗尽告警，不阻塞登录。队列不持久化，进程异常退出可能丢失待写记录。
- 维护任务每批清理超过 180 天的详细记录，查询提前执行相同保留窗口；摘要不跟随详细日志过期删除。没有 IP 自动封禁或风险评分。

## 离线数据和构建

固定 `ip2region v3.17.0`，源码提交 `cd40e3a1d532d645697999d646cf0e10481cef33`；Go 子模块版本为 `v0.0.0-20260709160242-cd40e3a1d532`。

使用官方双协议并发查询服务，IPv4/IPv6 各 4 个查询器和 VectorIndex 缓存，仅后台读取时查询，不进入登录路径。[对应官方用法](https://github.com/lionsoul2014/ip2region/blob/cd40e3a1d532d645697999d646cf0e10481cef33/binding/golang/README_zh.md)

`server/ipregion.lock.json` 固定数据和许可证 SHA-256：

| 文件 | SHA-256 |
| --- | --- |
| ip2region_v4.xdb | `6307a9696f5711f84bcb8b25f07894de68a64a0ed4a1cc7e990562dd3084f210` |
| ip2region_v6.xdb | `5b93da35ac28bc316dccc54a758381f7a874ae0461dd51ff5df5e34815586f11` |
| LICENSE.md | `fe01f2f8fcaafac539154e6aa80b0b7f8af54e01dc4d52322f72971991c6280e` |

PowerShell 7，本地从 `server/` 执行：

```powershell
go run ./cmd/prepare-ipregion
$env:IM_IP_REGION_DIR = (Resolve-Path -LiteralPath '.data/ip2region').Path
```

工具只从固定提交下载，缓存已校验文件，失败直接报错，不使用未校验数据。约 48 MB 数据保存在被 Git 忽略的 `.data/ip2region`；来源清单和 Apache-2.0 OR MIT 完整许可证随数据保留。Go 构建工具可使用标准 `HTTPS_PROXY`；下载需要能访问 GitHub Raw。

`server/Dockerfile` 在构建阶段准备并校验，复制进 `/opt/ip2region`，自动设置 `IM_IP_REGION_DIR`。构建阶段需要网络；运行期不联网查询、不自动更新。损坏/缺失数据仅使归属地“暂不可用”，原 IP 记录及认证不受影响。

手动更新须核对上游版本/提交/许可证、计算两个 XDB 和许可证 SHA-256，更新 lock 和 `internal/ipregion/resolver.go` 的校验值/版本；执行真实双协议查询测试，再构建并重启。不要只替换文件或跳过校验。归属地仅供参考，历史记录使用当前加载的数据解析，缺失字段不推断。

## 验证记录

2026-09-02，本地 Windows / PowerShell 7.6.4，Go 1.26，Node 22；使用独立 PostgreSQL 17 容器与随机隔离 schema，未连接生产数据库。

- `go test ./...`、`go vet ./...`：全部包通过；需显式环境变量的外部集成测试默认跳过。
- 真实 PostgreSQL：新表创建、59→60 升级及重复迁移、IPv4-mapped IPv6、并发最后登录、注册 IP 不覆盖、日志幂等、180 天清理及摘要保留、IP 去重及失败排除、稳定分页通过。
- HTTP + 真实 PostgreSQL：注册/成功登录/失败、后台 IP 查询和持久审计、普通 `/users/me` 不返回 IP、后台单个与批量开户来源/空注册 IP 通过。
- HTTP：验证码首次开户判定、二维码等待不记登录、扫码使用 Web 领取 IP、刷新不新增、错误代码脱敏、记录故障不影响登录、只读管理员可查/禁用管理员拒绝、过滤参数校验通过。
- 队列：容量、有限重试、稳定 ID、失败计数及两个 worker 上限测试。
- 离线数据：校验下载、真实 IPv4/IPv6 查询、内网/回环、缺失/损坏库降级通过。
- 真实 Caddy 2.10 容器：直连来源及伪造 IPv4、IPv6、转发链测试通过。Caddy 清理转发头后服务端记录真实代理观察的客户端地址。本机 Docker NAT 测试地址为 `172.17.0.1`；不将其宣称为公网部署实测。
- 后台 128 项测试、TypeScript 检查、生产构建通过；已有 ExcelJS 按需块体积警告仍存在。
- 浏览器技能实测真实后台 + 隔离数据库：列表 IP 筛选、全局日志翻页/筛选、详情资料/日志、同 IP 两账号去重、失败身份提示。390px 窄屏和桌面下 IPv6 换行及弹窗检查通过；修正了继承表格 nowrap 导致的长 IP 溢出。

复验（仅将数据库变量指向可创建/删除测试 schema 的隔离 PostgreSQL）：

```powershell
$env:IM_TEST_DATABASE_URL = 'postgres://postgres:TEST_PASSWORD@127.0.0.1:TEST_PORT/TEST_DB?sslmode=disable'
$env:IM_TEST_IP_REGION_DIR = (Resolve-Path -LiteralPath '.data/ip2region').Path
go test ./internal/store ./internal/httpapi ./internal/ipregion -run 'Test(UserAccess|PostgresUserAccess|Region|PinnedDataLookup)' -count=1
$env:IM_TEST_CADDY = '1'
go test ./internal/netutil -run TestClientIPRealCaddy -v -count=1
```

## 发布与运行注意

本次没有提交、推送、部署或打包；保留原未提交修改。正式发布前备份 PostgreSQL，服务端启动自动升级，随后发布后台及更新后的 `infra/legal/privacy`。无需重打 Flutter。

临时预览服务、测试 PostgreSQL/Caddy 容器及生成的测试账号/记录已清理；只保留已校验的本地离线库缓存。未删除用户业务数据。

直连使用 `IM_TRUST_PROXY=false`；仅当业务服务只能由受控 Caddy 访问且它替换转发头时启用 `true`。不要把信任转发头的业务端口直接暴露公网，亦不要信任客户端正文自报的 IP。

监控 `/metrics` 的 `im_user_access_pending`、`im_user_access_written_total`、`im_user_access_retried_total`、`im_user_access_dropped_total`。丢弃计数增加应排查队列压力/数据库故障；已有成功认证不会因记录补写失败而回滚。
