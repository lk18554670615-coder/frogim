# 邻里通讯文档中心

本文档目录按“理解系统 → 配置 → 部署 → 运维 → 测试 → 发布”组织。所有文档默认以当前仓库为准；带日期的部署报告属于历史证据，不代表当前线上状态。

## 核心文档

| 文档 | 用途 | 主要读者 |
|---|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 服务边界、消息一致性、实时同步、媒体与扩展策略 | 开发、架构、SRE |
| [MESSAGE_PROTOCOL_ZH.md](MESSAGE_PROTOCOL_ZH.md) | 消息类型、内容体、ACK、序列号与兼容规则 | 客户端、后端、测试 |
| [CONFIGURATION.md](CONFIGURATION.md) | 移动端、服务端、推送、存储与音视频配置 | 开发、运维 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | 生产拓扑、首次部署、升级与回滚 | 运维、发布负责人 |
| [OPERATIONS.md](OPERATIONS.md) | 巡检、告警、故障处理和容量管理 | SRE、值班人员 |
| [TESTING.md](TESTING.md) | 单元、集成、构建、冒烟和客户端验收 | 开发、QA |
| [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) | 发布前、中、后核对与签字记录 | 发布负责人 |
| [ACCEPTANCE.md](ACCEPTANCE.md) | 上线门槛与证据要求 | 产品、QA、安全 |
| [SECURITY.md](SECURITY.md) | 密钥、权限、日志和暴露面基线 | 安全、开发、运维 |
| [BACKUP_RESTORE.md](BACKUP_RESTORE.md) | PostgreSQL 与对象存储备份、恢复演练 | DBA、SRE |
| [COMPATIBILITY.md](COMPATIBILITY.md) | 品牌、技术名和旧持久化标识的边界 | 全体维护者 |
| [DATA_DIRECTORY.md](DATA_DIRECTORY.md) | 服务器 `/data/linli-im` 统一数据目录 | 运维、DBA |
| [LOGGING.md](LOGGING.md) | 容器日志、中文服务对照、轮转与事故归档 | 运维、研发、安全 |
| [CLUSTERING.md](CLUSTERING.md) | IM 节点横向扩展、多副本一致性和扩容步骤 | 架构、后端、运维 |

## 产品与环境资料

- [IM_FEATURE_MATRIX.md](IM_FEATURE_MATRIX.md)：功能实现状态和外部依赖。
- [IP_SERVER_OPERATIONS.md](IP_SERVER_OPERATIONS.md)：保留的 IP 直连环境运维手册。
- [DEPLOYMENT_REPORT_2026-07-31.md](DEPLOYMENT_REPORT_2026-07-31.md)：历史部署证据快照，使用前必须重新核验。
- [../PRODUCT.md](../PRODUCT.md)：产品目标与非目标。
- [../DESIGN.md](../DESIGN.md)：移动端视觉与交互规范。
- [../apps/admin/README.md](../apps/admin/README.md)：运营后台开发说明。
- [../apps/mobile/README.md](../apps/mobile/README.md)：Flutter 客户端开发说明。
- [../server/README.md](../server/README.md)：服务端接口和实现说明。

## 文档维护规则

1. 新命令必须从仓库根目录或明确写出的子目录可执行。
2. 相对链接必须通过 `infra/scripts/check-docs.sh`。
3. 不在文档中记录真实密码、令牌、TOTP、推送 CID 或私有连接串。
4. 对外产品名称统一为“邻里通讯”，新技术资源标识使用 `linli-im`。
5. `nexachat` 兼容标识不得仅为统一命名而修改，具体清单见 [COMPATIBILITY.md](COMPATIBILITY.md)。
