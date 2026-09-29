# 2026-09-29 现有服务器并行部署记录

北京时间 2026-09-29 21:16 完成最后一轮重启与入口检查。目标 `18.163.165.233`。
新平台、默认企业和共享数据服务已实际运行；本次为**隔离并行部署，尚未迁移现网账号，
未切换旧业务，也未进入 `opening`**。

## 发布版本与组件

- 源码：`c09483d59e75a4448660803f0e53c822ea13c6ed`；tag `release/server-getui-20260929`。
- 平台：`frogim/platform-bundle@sha256:af2cd68d0472ae1fe2ef0262f35147491ae45608fef02c4ccb32e1ce5f42cecc`。
- 企业：`frogim/enterprise-bundle@sha256:3737d4ac380129d65df952e77adf2f0319d1eb2b02058fd517621563f1b76eb9`。
  企业运行代码沿用 `e1d56e1` 构建，后续提交只调整发布门槛和平台个推校验。
- 私密目录：`/data/frogim/releases/server-e1d56e1`。目录保留最初制品编号，实际版本以
  `source-receipt.json` 和镜像摘要为准。

11 个新容器全部健康：共享 PostgreSQL、Redis；平台 API、平台网关；企业 API、企业网关、
MinIO、媒体初始化、IM、插件初始化和 LiveKit。部署代理为原生
`frogim-tenant-agent.service`，平台部署任务最终为 `completed / verified`。

新平台和默认企业共用 PG 的 `platform` / `enterprise` 两库、Redis DB0 / DB1。
旧服务的数据实例暂时独立运行，原库和原 Redis 未清空。最终切换后再停止旧实例并保留原数据。

## 入口与账号状态

| 地址 | 当前用途 |
| --- | --- |
| `https://18.163.165.233/platform/` | 新平台管理后台 |
| `https://18.163.165.233/platform/v2/*` | 新平台认证、版本和设备接口 |
| `https://18.163.165.233/app/` | 新生产 Web |
| `/`、`/v2/*`、`/web/`、`/im`、`/rtc`、`/nexachat-media/*` | 旧业务，待正式切换 |

公网入口仍为原 Caddy，新增路由验证后端 CA 并覆盖来源证明头。独立统一入口的最终配置已备好，
尚未接管 443；`/web/` 重定向、旧认证关闭和旧 `/rtc` 关闭留到正式切换。

后台账号为 `operator`，随机密码仅在本机受限文件
`.data/server-release-20260929/credentials.json` 保存。
默认企业在平台中为 **`provisioning`，未激活**，平台账号、候选企业业务用户均为 0。
企业内部初始 `access_enabled=true` 是新库初始状态，不代表平台已启用企业。本次没有创建
业务登录身份或执行激活/恢复。三端验收使用单独验收企业和身份。

## 验证及供应商状态

- 新组件健康，部署任务由平台提交、代理执行并持久确认。
- 从公网验证后台 HTML、`/platform/assets/`、Web 基址和启动脚本、认证/推送 JSON、协议页。
  匿名平台管理 API 返回 401；所有 TLS 检查保留证书链和主机名验证。
- 重启新平台 API 和代理后健康，原部署任务仍为 `completed / verified`，企业未激活。
- 原 11 个容器继续运行，旧库仍为 535 个用户；未导入真实用户。
- Go 全包测试/vet、平台及部署包针对性测试/vet、后台 216 项测试、后台/Web 构建通过。
- 已核对原证书续期/重载成功记录和可信公网 HTTPS；并行阶段保留原续期任务。
- 结束时磁盘可用约 25.7 GiB，没有清理原库、卷、旧镜像或发布目录。

现网 22 字符个推 MasterSecret 已通过供应商鉴权，新平台原有 24 字符下限已修复并回归。
仅获取供应商 token，没有发送推送。普通个推及 Web Push 已配置；`getui_voip` 仍关闭，
真实供应商能力、iOS 登记和实收等待验收。短信接口尚未配置，验证码登录、注册和找回保持关闭。
这些检查不替代三端功能、Apple SDK/签名安装、真实通知或锁屏来电验收。

## 入口和网络注意事项

原网关实际挂载 `/opt/nexachat-release-1684bcf/infra/Caddyfile.ip`，不是当前软链接所指的
`33d9ea2` 目录。已备份并同步实际挂载文件和当前发布目录的配置。以后操作前须核对真实 inode。
`/legal/terms.html` 和 `/legal/privacy.html` 是原协议的相同内容副本，已校验摘要；原入口保留。

内部网关 18443–18445、控制端口 19443/19444、新 IM 15100、新 RTC 17881/17882–17885
仅供候选环境。EC2 公网映射私网网卡，因此新增 `frogim-private-ingress.service` 和
`/usr/local/sbin/frogim-private-ingress`，在 `DOCKER-USER` 中拒绝 `ens5` 进入这些端口。
没有改变原业务端口。原生代理 19450 经 UFW 只允许平台 bridge `br-a0bc892937a5` 的
`172.23.0.0/16`，同时要求平台 mTLS 身份。删除重建平台网络后须核对并更新这条规则。

## 回执、备份和撤回

本机受限目录 `.data/server-release-20260929/` 保存清单、配置、部署回执、公网验证和制品。
CA 私钥没有上传服务器。新平台和企业分别导出数据库，与配置及代理日志组成
`candidate-runtime.tar.gz.fgbk`，已离机保存并通过认证解密及明文摘要复核。
密文 SHA256：`bd316dc51c01973a4a0bd758c3970f69c2ccf893d420c89d0ce102f1c6ea1725`。
CA 私钥另存 `local-ca-authorities.tar.gz.fgbk`；独立密钥在
`.data/backup-keys/server-candidate-20260929.key`，不包含在归档中。
这是空候选环境备份，不是正式停写时的一致性业务备份。

当前可撤回新增公网路由，不需要恢复原数据库：用服务器私密 `ops/` 中的
`gateway-mounted-original.Caddyfile` 恢复实际挂载文件，校验并重载；同时用
`gateway-original.Caddyfile` 恢复当前发布目录文件。确认旧入口后才能停止候选服务和代理，
保留配置、数据和回执，不删除卷。这仅适用于当前未导入、未激活状态；进入 `opening` 后改走受控恢复。

无密钥摘要：`build/tenancy-local/phase44-deployment-summary.json`；工程与部署日志：
同目录 `phase44-*.log`。真实数据演练继续见 [既有记录](LEGACY_SNAPSHOT_REHEARSAL.md)。

## 尚未完成

三端及 VoIP 验收、短信/无密码账号通路、完整切换计时、最新停写备份、真实账号导入及异常禁用、
独立入口接管、旧认证关闭、受控开服和开服后 24 小时观察仍待执行，不能标记为正式切换完成。
