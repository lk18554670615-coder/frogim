# 平台与同机默认企业共享 PostgreSQL / Redis

范围仅限平台和同机的 `default` 企业。其他企业沿用独立数据实例。共享模式为
`shared_host`；Redis 使用同一密码、不同编号，不新增 ACL 用户或键前缀。
本机运行是 development/local_preview；生产运行模式、mTLS、账号归属校验保持原要求。

## 部署结构

| Compose 项目 | 组件 | 数据归属 |
|---|---|---|
| `frogim-shared-default` | 一个 PostgreSQL 17、一个 Redis 8 | PG：`platform` / `enterprise`；Redis：DB0 / DB1 |
| 平台项目 | 平台 API、平台网关及静态客户端/后台 | PG `platform`，Redis DB0 |
| 默认企业项目 | 企业 API、网关、WuKongIM、MinIO、LiveKit、插件/媒体初始化 | PG `enterprise`，Redis DB1；媒体、IM、日志、插件仍为企业卷 |
| 主机运维 | 部署代理、备份工具 | 复用已有受控运维入口 |

数据项目单独管理生命周期，只提供内部网络 `frogim-shared-default_data`，不公开
5432/6379。只有两端 API 接入该共享网络。PostgreSQL 使用各自数据库和普通角色
`platform`、`enterprise`；`shared_admin` 只用于初始化、迁移及人工恢复。Redis DB
编号是数据分组，不是访问权限隔离；此次按已确认要求不增加 Redis 权限隔离。

同机共享会共同承担数据库进程故障和资源用量。企业备份/冷维护不停止共享 PostgreSQL
或 Redis，不复制它们的整个数据卷。整机维护才同时停止应用项目和数据项目。

## 本机启动与保留数据

项目根目录使用 PowerShell 7：

```powershell
./infra/scripts/start-tenancy-local.ps1
# 当前源码镜像和前端产物已构建时：
./infra/scripts/start-tenancy-local.ps1 -SkipBuild
```

首次启动自动执行**本机测试数据**迁移：停止原应用写入 → 保留原四个数据容器/卷 →
分别导出两份 PG custom dump 和两份 Redis DB 快照 → 导入新共享实例 → 核对两端
用户 ID → 停止旧四个数据容器 → 启动应用并验证登录、刷新及企业依赖。
IM/MinIO/插件原卷继续使用，不搬移媒体地址。原凭据文件不改写。

生成的私密配置、原数据备份和阶段回执都在 `.data/tenancy-local/shared/`，不进 Git。
`migration/receipt.json` 记录源/目标容器、目标卷创建身份、文件 SHA256 和已完成阶段。
重复启动不再次导入；目标卷消失或被替换会拒绝开服，避免创建空库假装迁移成功。
已确认阶段可跳过；导入提交后回执丢失、非空目标或文件摘要变化须人工核对，脚本不会
清库重试。保留完整错误回执和快照，确认前不要启用旧应用写入。

| 本机入口 | 地址 |
|---|---|
| 平台后台 | `http://127.0.0.1:18900/` |
| 统一 Web 客户端 | `https://127.0.0.1:18443/app/` |
| 默认企业后台 / API | `https://127.0.0.1:18444/` |
| 媒体 | `https://127.0.0.1:18445/` |

密码继续使用 `.data/tenancy-local/credentials.json`。脚本不自动安装浏览器 CA。
停应用时必须同时带共享 overlay，避免下次误启旧数据库配置：

```powershell
docker compose -f infra/tenancy/compose.local.yml -f infra/tenancy/compose.shared.local.yml stop
# 仅在整机维护、应用均已停止后：
docker compose -f .data/tenancy-local/shared/compose.json stop
```

保留所有卷，不执行 `down -v`。不要单独用基础 `compose.local.yml up` 重启旧布局。

## 生产包配置入口

1. 在私密 shared 配置中提供 `serverId`（必须等于默认企业部署的 serverId）、
   `adminSecret`、`platformDatabaseSecret`、`enterpriseDatabaseSecret`、`redisSecret`。
   四个密码是互不相同的 32 字节随机值的 base64url 编码。
2. `go run ./cmd/shared-data-bundle -config <绝对私密路径> -out <绝对新文件路径>`
   在 `server/` 下生成基础设施 Compose JSON。工具只生成文件，不部署。
3. 平台 `PlatformConfig` 增加 `"sharedDatastores":true`；平台 PG/Redis 密码必须
   与 shared 配置对应。默认企业 `EnterpriseConfig` 增加
   `"sharedDatastores":{"database":"enterprise","redisDb":1}`，数据库密码与
   shared 中企业密码一致，Redis 密码与平台相同。其他业务密钥继续独立。
4. 先由运维启动数据项目并确认健康，再启动平台、默认企业。企业 release 的
   `datastoreMode` 参与摘要和回退校验，不能跨共享/独立模式执行普通回退。

运行时分别生成 `PLATFORM_DATASTORE_MODE=shared_host`、`IM_DATASTORE_MODE=shared_host`，
连接主机固定为 `shared-postgres:5432` / `shared-redis:6379`。企业模式只接受 default；
平台固定 `/platform` + Redis DB0，企业接受 `/enterprise` + DB1，或受控恢复分配的
`enterprise_r_*` + DB2..15。不是任意外部数据库 URL 的放行开关。

该配置只改变数据层。同机公网网关的端口、原域名路由和证书仍需按真实主机拓扑确认，
不要把平台和企业两个网关都直接绑定同一 IP 的 443。本机使用现有不同回环端口。
本机迁移脚本明确拒绝生产配置，不用它迁移真实账号。

## 备份与受控恢复

- 复用企业暂停、身份校验、加密归档、持久任务及原容器恢复流程。共享模式归档仍是
  八类文件：数据库、Redis、四类企业卷、Compose、release。Redis 项为单 DB 逻辑
  快照，含二进制键值、类型、原绝对过期时间及终止计数；不保存整实例 RDB/AOF。
- Redis 导入先校验完整流，只写空目标 DB，不执行 FLUSHDB / FLUSHALL / RESTORE REPLACE。
  已过期记录不恢复，其他 TTL 不因停机延长。快照期间企业写入方必须停止。
- `tenant-backup restore` 为企业创建 `enterprise_r_<restore-id>` 新库，由 PG 内部
  保留表分配一个未占用的 Redis DB2..15，再恢复到新企业卷；旧库、DB1、平台 DB0
  和平台 PG 均保留。用完空闲编号时明确失败，编号清理由运维确认后处理，不自动回收。
- 失败的 staging 保留目标及回执，不开服、不覆盖、不自动释放编号。重新恢复须用新
  restore ID；人工核对后才能清理明确无用的目标。恢复后的企业仍暂停，另走现有受控激活。
- PostgreSQL 扩展由管理员预建，业务角色保持非 superuser；恢复不导入原 owner/ACL/
  COMMENT，业务表及关系保留。共享模式备份后的下一次备份会跟随新库/新 Redis DB。
- 平台仍使用现有 `platform-backup` 的逻辑快照和人工隔离激活流程。完整灾备需另外
  保管共享基础设施配置/凭据及企业卷；不要用恢复整 Redis 卷的方式单独回退一个角色。

## 验证入口

`go -C server test ./...`、`go -C server vet ./...` 覆盖配置及代码回归。
先构建 `build-enterprise-bundle.ps1`，再运行 `test-shared-datastores.ps1` 做真实 Docker
冷修复、备份确认丢失、新数据库/卷恢复、再次备份演练。它要求固定共享项目/卷不存在，
发现已初始化的本机共享数据会拒绝运行；应使用干净的可丢弃 Docker 环境，不删除持久数据
来腾出测试位置。Redis 独立 DB 往返实测入口为 `internal/redisbackup` 的
`TestRedisLogicalDatabaseRoundTrip`，仅允许新建一次性 Redis 实例。

真实服务器、域名、供应商和移动端真机仍按原清单单列验收。


## 同机单入口发布补充

阶段 42 新增显式 `shared_edge` 入口模式，支持原地址单一 443。公网 URL 与内部端口分别配置，
共享数据方式不变。实现和剩余发布门槛见 [现有服务器发布手册](EXISTING_SERVER_RELEASE.md)。
真实停服迁移必须等待三端及真机/迁移演练证据齐备，不能直接把本机 overlay 用于现网。
