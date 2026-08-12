# 邻里通讯

邻里通讯是一套可独立部署的即时通讯系统。仓库包含 Flutter 四端客户端、Go 业务 API、WuKongIM、LiveKit、React 运营后台、PostgreSQL、Redis、MinIO/S3、监控、备份与生产部署脚本。新技术资源统一使用 `linli-im`；部分 `nexachat` 标识是现有部署资源名，详见[兼容标识](docs/COMPATIBILITY.md)。

## 目录

```text
apps/mobile        Flutter iOS、Android、Web 与 macOS 客户端
apps/admin         React 运营、审核与系统配置后台
server             Go 业务 API、WuKong 适配与数据库迁移
packages/getuiflut 个推 Flutter 插件兼容包
infra              Compose、网关、监控、备份与运维脚本
docs               架构、配置、部署、运维、测试和发布文档
artifacts          设计参考与本地验收截图
```

## 快速开始

要求：Docker Engine + Compose v2、Go 1.26+、Flutter 3.44+、Node.js 22.12+。管理后台不支持 Node.js 23。

```bash
cp .env.example .env
make infra-up
infra/scripts/smoke-local.sh
```

本地服务只监听回环地址：

- 运营后台：`http://127.0.0.1:8088`
- API：`http://127.0.0.1:8080`
- WuKongIM TCP：`tcp://127.0.0.1:5100`
- WuKongIM WSS：`ws://127.0.0.1:5200`
- MinIO 控制台：`http://127.0.0.1:9001`

本地管理员账号、固定验证码和 TOTP 种子仅用于回环开发环境，见 `.env.example`。禁止把示例配置用于公网或共享环境。

## 常用验证

```bash
# 服务端
(cd server && go test ./... && go vet ./...)

# 运营后台
(cd apps/admin && npm ci && npm run lint && npm test -- --run && npm run build)

# Flutter
(cd apps/mobile && fvm flutter pub get && fvm flutter analyze && fvm flutter test)

# 文档、脚本和 Compose
infra/scripts/check-docs.sh
bash -n infra/scripts/*.sh
docker compose -f infra/compose.yaml -f infra/compose.wukong.yaml config -q
```

完整测试范围与真实 PostgreSQL 测试方法见[测试指南](docs/TESTING.md)。

## 生产部署

生产定义默认拒绝弱密钥、示例域名、开发验证码和 `noop`/`log` 推送。Web/API 只有 Caddy 暴露 80/443；LiveKit 按配置暴露 7881/TCP 与 7882–7889/UDP。API、后台、数据库、缓存、对象存储和监控均位于内部网络。

```bash
cp .env.production.example .env.production
chmod 600 .env.production
# 填完全部 REPLACE_WITH_* 后再执行：
make production-validate
make production-config
make production-deploy
```

部署前必须完成短信、个推/APNs/Android 厂商通道、LiveKit、隐私政策、内容治理、备份恢复和发布审批。仓库能验证软件与配置，但不能代替云账号、证书、商店签名和合规主体。

## 文档导航

- [文档总览](docs/README.md)
- [系统架构](docs/ARCHITECTURE.md)
- [配置中心](docs/CONFIGURATION.md)
- [生产部署](docs/DEPLOYMENT.md)
- [日常运维](docs/OPERATIONS.md)
- [备份恢复](docs/BACKUP_RESTORE.md)
- [安全基线](docs/SECURITY.md)
- [测试指南](docs/TESTING.md)
- [验收门槛](docs/ACCEPTANCE.md)
- [发布清单](docs/RELEASE_CHECKLIST.md)
- [兼容标识](docs/COMPATIBILITY.md)
- [功能矩阵](docs/IM_FEATURE_MATRIX.md)

## 维护原则

1. 业务策略优先通过运营后台热更新；密钥和基础设施参数通过部署环境管理。
2. 任何数据库迁移都必须向前兼容，并先完成备份与恢复演练。
3. WuKongIM 的消息 ID、频道序号、最近会话和离线同步是实时消息事实来源；PostgreSQL 保存业务资料与扩展。
4. 不直接重命名 Compose project、volume、数据库、存储桶、服务器目录或客户端持久化键。
5. 发布必须留下测试、镜像、迁移、备份、冒烟和审批证据。
