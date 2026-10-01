# Linux 源码一键部署

入口为 `infra/scripts/deploy-light-tenancy.sh`。只适用于全新环境，支持 Linux amd64 / systemd；Ubuntu、Debian 可以交互安装 Docker，其他 Linux 发行版先安装 Docker Engine、Buildx、Compose。服务使用 root，API、IM、LiveKit 明确配置 `0:0`。没有新增代理、平台网关服务或持续观察任务。

## 开始部署

以下命令在目标 Linux 服务器执行：

```bash
git clone https://gitee.com/fanxinet_fanxinet/newimceshi.git
cd newimceshi
sudo bash infra/scripts/deploy-light-tenancy.sh
```

也可从 GitHub 拉取 `https://github.com/lk18554670615-coder/frogim.git`。仓库认证使用 Git 凭据或 SSH，不把 token/password 写入仓库 URL。

交互选择：

- **平台＋企业**：8 个容器，平台和企业共用 PostgreSQL 实例的不同数据库，平台不使用 Redis。提供唯一 `/app/`、平台 `/platform/` 和企业 `/admin/`。
- **仅企业**：7 个容器，独立业务数据库、Redis、媒体、IM 数据。仅提供企业后台和业务接口；`/`、`/app/*`、`/web/*` 返回 404。注册及登录使用已有平台，业务直接连接企业。

脚本收集企业身份、名称、企业码、路径、公私网地址、RTC 公网 IPv4、后台凭据、推送配置和公网证书信息；平台模式还收集验证码策略。固定验证码是独立配置，不打开开发模式。企业推送必须提供真实个推或 HTTPS webhook 参数；不会用假参数绕过生产校验。

构建在目标服务器执行：浅拉取指定 `main` / tag / commit，固定实际提交，构建 Go API、内置平台后台、企业后台、IM 和插件；只有平台模式构建 Flutter Web。网关内置静态资源，不增加 Web 常驻容器。Go、Node、Flutter 工具链由构建镜像提供，主机不需要安装这些 SDK。

## 已有平台接入

仅企业模式需额外提供平台公网地址、私网 IP、平台 SSH 用户/端口/私钥、核实过的 Ed25519 主机指纹及 CA 目录。平台 SSH 用户必须具备 Docker、CA 签发、iptables 和 systemd 操作权限，默认 root。

- 本工具建立的平台 CA 在 `<平台部署目录>/config/ca`。
- 当前旧发布布局的 CA 在 `/data/frogim/releases/light-20261001-c83107e/config/certs`；接入时按真实位置填写。
- 企业私钥在企业本机生成，只传 CSR 到平台签发，CA 私钥不离开平台。
- 私网平台端口 8443；同机企业端口 8444，异机企业端口 8443。
- 本机 INPUT 和 DOCKER-USER 安装独立白名单；平台侧仅增加本企业的受限放行链，不替换原防火墙。
- 云安全组不会自动修改。需事先允许必要私网来源及 TCP 80/443/5100/7881、UDP 7882–7889；私网 mTLS 不通时不启用企业。其他企业参与互相访问时，将必要私网来源纳入输入的白名单。
- 企业先通过审计接口登记为停止登录，再启动企业 API，以便 API 使用 mTLS 获取平台签名公钥；核对企业身份、连接地址和 readiness 后才启用。首个企业由平台现有规则自动成为唯一默认企业，新增企业不改变默认或现有账号归属。

公网证书支持 ACME 自动申请（IP 或域名）及已有证书文件两种模式。ACME 模式需要公网 80 连通，证书每 12 小时检查；已有证书模式由原机制负责续期，更新原文件后执行下方 `renew` 操作。安装前检查有效期、主机名和私钥匹配，检查失败不覆盖当前证书。

## 中断续跑与运维

```bash
# 使用原配置、源码提交和密钥继续；不会重新接管其他目录
sudo bash infra/scripts/deploy-light-tenancy.sh --root /data/frogim/frogim-main

# 明确执行一次可用性检查或一致性备份
sudo python3 /data/frogim/frogim-main/ops/deploy.py --root /data/frogim/frogim-main --operation check
sudo python3 /data/frogim/frogim-main/ops/deploy.py --root /data/frogim/frogim-main --operation backup

# ACME 检查续期；已有文件模式则重新安装原路径的新证书并重载网关
sudo python3 /data/frogim/frogim-main/ops/deploy.py --root /data/frogim/frogim-main --operation renew
```

配置在 `config/deployment.json`，内部凭据及密码哈希在 `config/secrets.json`，均为 root 私有文件。初始化管理员明文密码仅在进程内使用，不保存；构建中断后需重新输入尚未生成哈希的密码。SSH 和 API token 不进入日志。阶段在 `ops/state.json`，固定镜像 ID 在 `ops/images.json`，最终回执在 `ops/completed.json`。配置改变会拒绝续跑，避免企业 ID、地址或凭据意外改变。

全新部署拒绝非空且没有本工具回执的目录及已占用端口，不导入生产数据、不删除旧卷、不覆盖现有部署，不执行升级或迁移。失败保留配置和数据；阶段未确认完成则下次重试，已完成阶段跳过。企业登记或确认丢失后重试会核对已有记录，不覆盖同 ID 的不同配置。

每日备份短暂停止写入者，分别导出企业与平台数据库，并保存 Redis、媒体、IM、配置；恢复服务后才标记完成。只清理本项目超过七份的完成备份，不清理失败备份。此版本不自动恢复生产数据，也未配置异地目标。

构建可继承 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`；代理须可从构建容器访问。Docker 拉取基础镜像的代理在 Docker daemon 中配置，不能使用目标容器中的 `127.0.0.1` 代替宿主机代理。

## 验证范围

定向检查共 13 项，通过两种容器结构、凭据范围、入口路由、参数校验、失败续跑、配置锁定、启停依赖和实际 OpenSSL 签发/证书私钥匹配；Linux Shell 语法、Compose 和 Caddy 配置解析分别通过。已实际构建 Go 服务、两套后台及企业网关，并通过平台/企业生产配置校验。

另用全新独立测试卷运行 8 个容器，不发布宿主机端口，验证全部容器健康、平台/企业 readiness、mTLS 企业身份与服务地址、IM 策略插件加载和 MinIO 受限用户初始化。该检查使用已固定版本的本机 IM/LiveKit 镜像和测试证书；不代表 Flutter Web 的新机完整构建、公网证书、外网连通或音视频通过。证据在本机 `build/source-deploy-tests/tests.log`、`runtime-result.json` 和 `container-health.json`；构建日志在 `build/source-deploy-api-build.log`、`build/source-deploy-gateway-build.log`。

首次公网 ACME、安全组及已有平台 SSH 接入需要在全新 Linux 机器验收；本轮没有部署或修改现有 A/B 服务器，不把本地模板/构建检查记作公网部署成功。音视频及供应商推送实收不在本轮检查范围。

依赖依据：[Docker 官方安装说明](https://docs.docker.com/engine/install/ubuntu/)、[Certbot 使用说明](https://eff-certbot.readthedocs.io/en/stable/using.html)。
