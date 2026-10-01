# 青蛙呱呱

青蛙呱呱是一套可独立部署的即时通讯系统。仓库包含 Flutter 四端客户端、Go 业务 API、WuKongIM、LiveKit、React 运营后台、PostgreSQL、Redis、MinIO/S3、监控、备份与生产部署脚本。内部技术资源继续使用 `linli-im`；部分 `nexachat` 标识是现有部署资源名，详见[兼容标识](docs/COMPATIBILITY.md)。

## 目录

当前客户端版本：**1.0.5+4009**。更新内容和发布边界见 [1.0.5 发布说明](docs/RELEASE_1.0.5.md)。

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

本地首个管理员账号和用户固定验证码仅用于回环开发环境，见 `.env.example`。管理员账号初始化后从 PostgreSQL 验证；禁止把示例配置用于公网或共享环境。
本地栈默认使用真实空数据，不会创建演示账号；只有隔离开发测试需要时才可同时显式设置 `IM_DEV_MODE=true` 与 `IM_SEED_DEMO=true`。

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

## iOS 自动构建

Android 与 iOS 应用统一使用包名/Bundle ID `top.hongjinghuanqiu.app`，不能覆盖安装旧包名 `com.fd.kuailiao` 的应用。GitHub Actions 在 `main` 分支相关代码变化时自动完成 Flutter 静态分析、测试、无签名 iOS Release 编译及签名 IPA 构建；也可手动运行 `iOS Build` 并启用“使用仓库 Secrets 构建签名 IPA”。当前 iOS 使用 Ad Hoc 签名，仅描述文件登记的设备可安装，不是不限设备的企业分发包，也不能直接提交 App Store。

证书、描述文件和密码只允许保存在 GitHub 加密 Secrets 中，不得提交到仓库。工作流入口、所需 Secrets、产物下载和签名方式见 [GitHub Actions iOS 构建](docs/GITHUB_IOS_ACTIONS.md)。

## 生产部署

### 轻量多租户：从源码一键部署

适用于**全新 Linux amd64 / systemd 服务器**。在服务器上拉取源码并构建服务和静态资源，不需要传递镜像包，也不需要在主机安装 Go、Node.js 或 Flutter。以 root 或 `sudo` 运行；Ubuntu、Debian 可按脚本提示安装系统依赖及 Docker，其他 Linux 发行版需预先安装 Docker Engine、Buildx 和 Compose。

#### 1. 准备部署信息

先安装 Git，并准备以下信息。Ubuntu、Debian 尚未安装 Git 时执行：

```bash
sudo apt-get update
sudo apt-get install -y git
```

| 信息 | 填写方式 |
|---|---|
| 部署模式 | 首台选择“平台＋企业”；新增企业服务器选择“仅企业” |
| 源码版本 | 默认 `main`，也可指定包含部署脚本的 tag 或提交 |
| 项目名、部署目录 | 使用独立目录，例如 `/data/frogim/frogim-main`；不接管已有数据目录 |
| 企业资料 | 企业 ID、具体名称、企业码；例如 `enterprise-a`、客户A企业、`A` |
| 地址 | 本机公网 HTTPS 地址、私网 IPv4、RTC 公网 IPv4；公网地址不含路径 |
| 企业后台 | 初始化用户名、密码；密码在交互输入中隐藏 |
| 推送 | 真实个推 App ID / App Key / MasterSecret，或 HTTPS webhook 及 token |
| 公网证书 | `acme` 自动申请及联系邮箱，或 `files` 已有完整证书链和私钥路径 |
| 平台＋企业额外信息 | 平台管理员、验证码策略：六位固定码或短信 HTTPS webhook |
| 仅企业额外信息 | 已有平台公网地址、私网 IP、管理员，以及平台 SSH 主机、端口、用户、私钥、已核实的 Ed25519 主机指纹和控制 CA 目录 |
| 控制访问白名单 | 平台及必要企业的私网 IP / CIDR，逗号分隔 |

仅企业模式的平台公网地址填写如 `https://18.163.165.233`，不附加 `/platform`。平台 SSH 用户默认 root，需具有 Docker、CA 签发、iptables 和 systemd 操作权限。本工具建立的控制 CA 位于 `<平台部署目录>/config/ca`；其他已有平台填写其实际 CA 目录。企业私钥在企业服务器生成，只将 CSR 交给平台签发。

提前配置云安全组：公网业务需要 TCP **80、443、5100、7881** 和 UDP **7882–7889**；ACME 申请需要公网 80 可达。控制端口 **8443 / 8444** 仅允许必要私网来源。数据库、Redis、MinIO 管理和 IM 管理端口不开放公网。脚本设置主机控制访问白名单，不自动修改云安全组。

#### 2. 拉取源码并运行

以下命令在目标 Linux 服务器执行：

```bash
git clone https://gitee.com/fanxinet_fanxinet/newimceshi.git
cd newimceshi
sudo bash infra/scripts/deploy-light-tenancy.sh
```

也可从 GitHub 拉取 `https://github.com/lk18554670615-coder/frogim.git`。按提示输入前一步准备的信息并确认，脚本固定源码提交和构建镜像 ID，完成证书、数据服务、企业登记与启用。企业先登记为停止登录，核对 mTLS 身份、服务地址和 readiness 后才启用。

| 模式 | 容器与数据 | 发布后入口 |
|---|---|---|
| 平台＋企业 | 8 个容器；PostgreSQL 一个实例、两个数据库；平台不使用 Redis | `/` 跳转 `/app/`；平台 `/platform/`；企业后台 `/admin/` |
| 仅企业 | 7 个容器；独立 PostgreSQL、Redis、媒体与 IM 数据 | 企业后台 `/admin/`；`/`、`/app/*`、`/web/*` 返回 404 |

用户统一从平台所在服务器的 `/app/` 登录，后续业务直连所属企业。新增企业不会改变已有用户归属或默认企业。固定验证码作为独立平台配置使用，不开启整套开发模式；管理员密码仅保存哈希。

#### 3. 查看结果与中断续跑

完成后使用交互设置的管理员登录对应后台。企业 readiness 为 `/ready`，平台＋企业模式还提供 `/platform-ready`。固定提交、入口和配置摘要在 `<部署目录>/ops/completed.json`，镜像 ID 在 `ops/images.json`。

中断后使用**原部署目录**继续。以下为默认平台目录示例，企业服务器替换为自己的目录：

```bash
sudo bash infra/scripts/deploy-light-tenancy.sh --root /data/frogim/frogim-main
```

续跑保持原源码、配置和密钥，跳过已完成阶段；不会重新注册同一企业。尚未生成密码哈希或需要再次登录平台时会重新询问密码。此工具不用于现网升级、账号迁移或覆盖已有部署。

#### 4. 常用运维操作

```bash
# 检查入口及容器健康
sudo python3 /data/frogim/frogim-main/ops/deploy.py --root /data/frogim/frogim-main --operation check

# 执行一次一致性备份，会短暂停止业务写入
sudo python3 /data/frogim/frogim-main/ops/deploy.py --root /data/frogim/frogim-main --operation backup

# ACME 检查续期；已有文件模式则安装原路径中的新证书并重载网关
sudo python3 /data/frogim/frogim-main/ops/deploy.py --root /data/frogim/frogim-main --operation renew
```

脚本设置每日备份，保留最近 7 份完成备份；ACME 每 12 小时检查续期。备份留在服务器，不自动配置异地目标或持续观察。配置和密钥文件仅 root 可访问，不提交到 Git。代理、回执、恢复边界及实际验证范围见[源码部署说明](docs/SOURCE_DEPLOYMENT.md)；首次公网证书申请和跨机 SSH 接入仍需在新环境验收。

### 原单企业部署方式

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
- [轻量多租户源码部署](docs/SOURCE_DEPLOYMENT.md)
- [日常运维](docs/OPERATIONS.md)
- [备份恢复](docs/BACKUP_RESTORE.md)
- [安全基线](docs/SECURITY.md)
- [测试指南](docs/TESTING.md)
- [验收门槛](docs/ACCEPTANCE.md)
- [发布清单](docs/RELEASE_CHECKLIST.md)
- [兼容标识](docs/COMPATIBILITY.md)
- [功能矩阵](docs/IM_FEATURE_MATRIX.md)
- [GitHub Actions iOS 构建](docs/GITHUB_IOS_ACTIONS.md)

## 维护原则

1. 业务策略优先通过运营后台热更新；密钥和基础设施参数通过部署环境管理。
2. 任何数据库迁移都必须向前兼容，并先完成备份与恢复演练。
3. WuKongIM 的消息 ID、频道序号、最近会话和离线同步是实时消息事实来源；PostgreSQL 保存业务资料与扩展。
4. 不直接重命名 Compose project、volume、数据库、存储桶、服务器目录或客户端持久化键。
5. 发布必须留下测试、镜像、迁移、备份、冒烟和审批证据。
