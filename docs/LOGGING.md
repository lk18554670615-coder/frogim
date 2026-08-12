# 容器日志与审计说明

## 日志分层

邻里通讯把“运行日志”和“重要业务记录”严格分开：

- Docker 运行日志：启动、健康、请求状态、耗时、错误和组件告警，可在宝塔容器页面直接查看，自动轮转。
- PostgreSQL 审计记录：管理员操作、群管理、公告、封禁、举报处理、设置修改和账号注销，不能因为容器日志轮转而消失。
- WuKongIM 数据：消息正文、消息 ID、频道序号、最近会话和离线消息，是消息真相源。
- PostgreSQL 业务数据：账号、权限、消息扩展、通话状态和 Outbox，是业务真相源。
- `/data/linli-im/logs/archive`：发布、故障或人工检查时导出的分服务压缩日志快照。

## 容器中文对照

| 容器服务 | 中文用途 | 主要检查内容 |
|---|---|---|
| `gateway` | HTTPS/WSS 网关 | 证书、状态码、上游连接、请求耗时 |
| `server` | Go 业务服务 | 登录、权限、Webhook/DataSource、Outbox、推送和通话记录 |
| `wukongim` | 实时消息节点 | TCP/WSS 连接、ACK、频道、离线消息、插件和节点错误 |
| `livekit` | 音视频 SFU | 房间、参与者、轨道、重连、TCP/UDP 和资源异常 |
| `admin` | 运营管理后台 | 静态资源、后台页面健康 |
| `postgres` | 主数据库 | 启动恢复、连接、事务、WAL、慢查询 |
| `redis` | 业务缓存与任务协调 | 限流、缓存、任务租约和 AOF；不保存唯一消息副本 |
| `minio` | 私有媒体存储 | 头像、图片、语音、视频、文件和磁盘容量 |
| `prometheus` | 指标采集 | 抓取失败、存储和规则 |
| `grafana` | 监控展示 | 登录、数据源和仪表盘加载 |
| `minio-init` | 一次性桶初始化 | 成功后自动删除，不是异常停止的长期容器 |

## 常用命令

```bash
# 查看某个服务最近 200 行；服务不填时默认 server
linli-im logs 200 server

# 查看所有服务最近 300 行
linli-im logs-all 300

# 筛选最近 30 分钟常见错误
linli-im logs-errors 30m

# 把最近 24 小时日志按服务压缩归档到统一数据目录
linli-im logs-export 24h
```

每个归档目录包含 `服务名.log.gz`、`containers.txt` 和 `SHA256SUMS`，权限为 `700`。日志归档用于短期排障，不代替数据库备份和审计记录。

## 轮转与保留

集中配置项：

- `DOCKER_LOG_MAX_SIZE=20m`：单个日志分片上限。
- `DOCKER_LOG_MAX_FILES=10`：每个容器最多保留的分片数量。
- `IM_LOG_LEVEL=info`：IM 服务最低日志级别，可选 `debug/info/warn/error`。
- `LOG_ARCHIVE_DIR=/data/linli-im/logs/archive`：人工日志快照目录。

生产环境默认每个容器最多约 200 MB 运行日志。修改后执行 `linli-im deploy` 重新创建容器，旧容器日志不会自动转换。

## IM 服务结构化字段

IM 服务使用一行一个 JSON 对象，核心字段如下：

| 字段 | 含义 |
|---|---|
| `time` | UTC 时间 |
| `level` | 日志级别 |
| `msg` | 中文事件说明 |
| `event` | 稳定的机器事件名 |
| `requestId` | 网关到服务端的请求追踪编号 |
| `method/path/status` | HTTP 方法、路径与状态码，不记录查询参数 |
| `durationMs/bytes` | 处理耗时与响应字节数 |

严禁写入消息正文、手机号验证码、访问/刷新令牌、WuKong IM/Manager Token、连接AES Key/IV、消息签名或验签摘要、TOTP、推送 CID、数据库密码、MinIO 密钥和 LiveKit API Secret。需要业务追溯时使用管理后台“审计日志”，不能通过扩大运行日志内容解决。WuKongIM候选镜像必须真实触发重复主设备登录，并检查原始容器日志而非只检查采集后的脱敏副本。
