# 运维手册

## 日常巡检

```bash
docker compose --env-file .env.production -f infra/compose.production.yaml ps
make production-smoke PROD_ENV=.env.production
docker compose --env-file .env.production -f infra/compose.production.yaml logs --since=15m server admin gateway
```

检查：容器健康、HTTP 错误率与延迟、WebSocket 连接、数据库连接/WAL、Redis 内存、推送失败与积压、对象存储容量、磁盘、证书和最近备份。

容器日志启用大小轮转，详细命令、字段和中文服务对照见 [LOGGING.md](LOGGING.md)。禁止记录访问令牌、验证码、TOTP、推送 token/CID、消息正文和私有连接串。管理员审计需要独立保留。

## 监控与告警

Prometheus 每 15 秒抓取内部 `/metrics`，规则文件目前保留兼容名 `infra/monitoring/nexachat.rules.yml`。该文件名、job 和告警 ID 可能被现有仪表盘/路由引用，不代表对外品牌，不能直接改名。

至少配置并实测：

- `NexaChatServerDown`（兼容告警 ID）：立即通知值班人员。
- 错误率持续上升：15 分钟内开始调查。
- 长时间无 HTTP 流量：核对业务时段、入口和客户端。
- `NexaChatHighHTTPP95Latency`：检查慢请求、数据库池等待和依赖延迟。
- `NexaChatDatabasePoolSaturated`：检查实例连接总预算和慢 SQL。
- `NexaChatPushBacklogOld` / `NexaChatMessageFanoutBacklog`：检查推送提供商、后台 worker 和数据库写入能力。
- `NexaChatWebSocketDrops`：检查慢客户端、实例内存和网络拥塞。
- 主机磁盘、PostgreSQL 连接/WAL、Redis 内存、MinIO 容量、证书到期和备份失败。

每条告警必须有负责人、升级路径和本手册链接。上线前、变更后和每季度测试送达。

## 故障处置

1. 指定事件负责人，建立带时间戳的记录。
2. 保存当前日志、容器状态、镜像 ID、配置修订和迁移版本。
3. 先控制影响，再定位根因；不要在故障中顺手升级其他组件。
4. 凭据泄露：轮换凭据、撤销会话、检查日志，只重启依赖服务。
5. 滥用流量：在网关和服务端限流/封禁，不依赖隐藏 UI。
6. 数据异常：停止写入，做安全备份，按恢复手册处理。
7. 修复后运行冒烟并记录用户影响、证据和后续措施。

## 容量与可用性

仓库提供的是加固单机基线，不是跨可用区高可用。需要 HA 时，应使用高可用 PostgreSQL、Redis 和对象存储，在网关后运行多个无状态服务实例，并验证 Pub/Sub 恢复、慢连接、重连风暴和同步游标。

扩容前收集：峰值在线、消息 TPS、P95/P99 延迟、数据库连接、队列等待、Redis 带宽、对象增长、CPU/内存和 24 小时泄漏趋势。不要以理论并发替代压测证据。

## 周期任务

- 每日：告警、备份、磁盘、证书和推送失败。
- 每周：错误趋势、Redis、数据库慢查询、审核积压和对象增长。
- 每月：升级基础镜像和依赖，轮换一项非关键密钥，验证证书续期。
- 每季度：恢复、故障响应、告警送达和权限审计演练。
- 每次发布：配置校验、测试、备份、镜像记录、部署、冒烟和审批。

IP 环境保留命令 `nexachat` 与 `/opt/nexachat` 目录，详见 [IP_SERVER_OPERATIONS.md](IP_SERVER_OPERATIONS.md) 和 [COMPATIBILITY.md](COMPATIBILITY.md)。
