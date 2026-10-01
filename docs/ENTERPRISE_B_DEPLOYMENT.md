# 企业 B 独立部署

目标：客户B企业 / enterprise-b / 企业码 B。A 保持唯一默认企业，不调换已有用户。
企业 B 管理员用户名 admin，密码通过部署输入传入，仅在配置中保存 bcrypt 哈希。

## 结构与入口

独立 Compose 项目 frogim-enterprise-b，根目录 `/data/frogim/enterprise-b`。
常驻服务仅 gateway、api、im、livekit、minio、postgres、redis 七个。
企业 PostgreSQL 数据库 enterprise_b，Redis DB0，均使用新的本机数据目录。
部署不导入 A 或本机业务数据，不接触 `/data/linli-im` 旧数据。

- 统一 Web 聊天入口仅为 `https://18.163.165.233/app/`，平台返回当前企业地址，用户直接连接其业务服务。
- B 的 `/`、`/app`、`/app/*`、`/web`、`/web/*` 返回 404，不跳转至 A；HTTP 和 HTTPS 均关闭聊天入口。
- `/admin/` 是企业 B 后台；`/v2/`、`/im`、`/livekit/`、媒体入口直连 B。
- 媒体桶保持 nexachat-media；MinIO 管理及数据库端口不对外映射。
- 旧 `/rtc` 关闭，内部接口不经公网网关公开。

## 私网与证书

平台 172.31.36.243:8443，A 172.31.36.243:8444，B 172.31.37.107:8443。
平台/A 保留原 CN、DNS 和私钥，叶证书增加自己的私网 IP SAN。
B 私钥在 B 生成；仅 CSR 交给 A 签发，CA 私钥始终在 A。
平台、A、B 仅允许两个正式 HTTPS Web 来源。

AWS 安全组要求（如无法修改，必须先验证已有规则满足私网连通）：

| 目标 | 协议/端口 | 来源 |
|---|---|---|
| B | TCP 80、443、5100、7881；UDP 7882–7889 | 业务客户端 |
| A | TCP 8443、8444 | 172.31.37.107/32 |
| B | TCP 8443 | 172.31.36.243/32 |

不新增 SSH/宝塔权限，已有管理规则保持原状。
Docker-USER 的 FROGIM_CONTROL 按 DNAT 前目的 IP/端口校验来源；允许对应私网对端与本机容器子网，其余拒绝。
同一 Docker 网桥通过主机发布端口访问同机服务时可能走 userland proxy，流量经过 INPUT 而不是 DOCKER-USER；INPUT 也挂接同一个 FROGIM_CONTROL 白名单，防止 UFW 拦截合法的同机控制请求。两条路径都只放行指定私网来源和控制端口，仍要求 mTLS。
防火墙单次 systemd 单元在 Docker 启动前恢复规则；不是新的常驻服务。
启用 B 需要私网 mTLS 成功及外部控制端口拒绝的证据。

## 执行阶段

使用 PowerShell 7、严格主机验证 SSH；远端 Python 在 Linux 执行。
`enterprise-b-release.py` 分阶段 package-a、prepare-b、permissions-b、connect-a。
`enterprise-b-ops.py` 分阶段 sign-b、issue、init-media、register、enable、backup、restore-check、timers。
prepare-b 从 stdin 接收 B 后台密码；register、enable 从 stdin 接收已有平台管理员 JSON 凭据。
明文输入不保存在服务器配置目录。所有私有错误保存服务端，不打印环境和令牌。

1. A package-a 保存平台数据库、原容器配置、证书、防火墙和定时任务，打包现有不可变镜像、正式 Web 和签名插件。
2. 通过严格验证的 SSH 将包传到 B，校验摘要后 prepare-b。生产内部凭据全部重新生成；同一 App 的供应商配置复用，实收尚未验收。
3. 签发 B 私网证书和独立 IP 公网证书。按用户补充要求，API、IM、LiveKit 使用 root，permissions-b 根据实际运行 UID 分配证书、插件、IM 数据及配置所有者，保持私钥仅所有者可读；B 目录尚不开放登录。
4. connect-a 仅短暂重建平台与 A API，保持镜像、业务地址、数据库和 JWT 不变。失败恢复原配置及叶证书。
5. 审计接口更新 A 控制地址、登记停止登录的 B；初始化 B 七项服务和媒体应用权限。
6. 核对私网身份、服务地址、public readiness、静态摘要及端口隔离后，审计启用 B。

## 备份与回退

B 原证书定时任务改为每 12 小时检查，验证 IP 和至少 12 小时有效期后重载网关。
B 原备份任务每日执行，仅保存新的 B 数据、配置和证书，保留最近七份完成备份。
一致性备份短暂停止 B API/IM，并在持久化 Redis 后停 Redis/MinIO，完成后恢复原来运行的服务。
完成标记记录文件摘要；未完成备份不参与保留策略清理。
隔离恢复使用独立无网络 PostgreSQL 容器和独立恢复目录；不覆盖生产数据库、媒体或共享平台数据。
备份目前留在 B；异机备份待完善。

启用前失败保持 B 停止登录，按快照和审计接口恢复本次目录/接入变更，保留 B 数据排查。
启用后失败先审计停用 B 新登录，必要时暂停 B 写入并保存新增数据，受控修复。
禁止自动将用户切回 A、启动旧认证、覆盖平台库或删除 B 账号。
恢复原 A 端口/证书前，先撤销目录中对应新控制地址，避免目录和服务配置不一致。

## 验收边界

本地执行 A→B→A、头像复制失败、旧凭据撤销、进程重启及原操作重试；隔离 Go 测试包含确认丢失后的重试。
Web 企业码 B 注册、消息和上传使用合成账号，不在生产重复功能测试或调换真实用户。
线上只检查容器健康、readiness、入口、静态资源、证书和私网身份/地址一致性。
音视频按约定跳过；移动端、供应商推送实收及跨公网功能表现待验收。
部署实际结果、摘要、备份恢复证据和观察记录在服务端 ops 及本机 build/enterprise-b-release。

## 2026-10-01 实际发布记录

- B 于北京时间 2026-10-01 20:56:33 通过审计接口启用；A 仍为唯一默认企业，没有调换现有用户。B 从空数据开始，启用时用户和身份映射均为零。
- 发布提交 `b1ae89ad7fb2251d3db4639ab93ce5b7ceb9acbd`，tag `release/enterprise-b-ready-20261001`，均已推送。复用正式 Web 1.0.16+8024，资源版本 `67444ea69853fd11`；实际镜像、配置、证书和静态资源摘要见部署回执。
- A 回执：`/data/frogim/releases/light-20261001-c83107e/ops/enterprise-b-release/deployment-completed.json`；B 回执：`/data/frogim/enterprise-b/ops/deployment-completed.json`。两份无凭据回执已保存至本机 `build/enterprise-b-release`。
- 本地定向故障测试覆盖 A→B→A、首次建号和头像复制、旧凭据失效、不可达、复制失败、进程重启和原操作重试；隔离数据库测试覆盖确认丢失后重试。本地 Web 使用合成账号完成企业码 B 注册、双向消息及文件上传，音视频未测试。证据为 `build/light-tenancy/evidence/direct-enterprise.json` 和 `build/enterprise-b-release/local-evidence.json`，另有双方页面截图。
- 上线可用性检查：A 八项、B 七项容器均健康，入口/readiness 返回 200；A 四项、B 六项静态资源核对摘要及类型。私网 mTLS 企业身份和服务地址一致，公网 8443/8444 无 TLS 响应。没有在线重复业务功能测试。
- B 完整备份：`/data/frogim/enterprise-b/backups/20261001T124206Z`。摘要校验、独立无网络 PostgreSQL 恢复和归档解包通过，未覆盖生产数据；没有宣称完整七容器恢复启动已验收。隔离记录在 `ops/restore-check.json`。
- B 每日备份保留七份完成备份，证书每十二小时检查；定时任务目标已核对，证书服务实际执行成功。原 B 旧目录保留，临时传输授权和隔离数据库容器已清除。
- 24 小时观察截止北京时间 2026-10-02 20:56:33，自动观察 ID `a-b`。逐次聚合记录保存在 `build/enterprise-b-release/observation`。首次持久化采样为 UTC 2026-10-01 13:12:32—13:12:34；日志查询覆盖 B 启用后至该次采样，但不能代表持续监测全部指标。A 累计失败推送一条来自 UTC 07:57:31，B 启用后新增失败为零；少量网关 503 位于正式业务路径之外。观察结束需列出实际样本、日志覆盖与缺失时段。
- 移动端、供应商推送实收、跨公网业务功能和异机备份待验收；音视频按用户要求跳过。本轮未修改企业 A 业务数据、账号归属或平台默认企业。

### 观察停止

北京时间 2026-10-01 21:18，用户明确要求无需持续观察，自动观察 `a-b` 已设为 PAUSED，不再定时检查。保留上述实际采样和诊断；未完成 24 小时观察，其余时段未验证。部署、备份和证书定时任务不受此调整影响。

### 同机资料查询修复

用户反馈平台资料抽屉提示企业不可达。针对性诊断确认：主机到 A/B 的 profile-status 均返回 200；使用平台容器的同一网络命名空间、UID、证书和 Go TLS 客户端，访问 B 返回 200，访问同机 A 的主机发布端口超时。Docker NAT 对同一网桥排除 DNAT，改走主机 userland proxy；原控制白名单只挂接 DOCKER-USER，遗漏 INPUT。修复为 INPUT 复用现有受限白名单，不扩大端口或来源，不修改平台接口、企业数据或镜像，不重启业务服务。后续实际检查结果以本机 `build/enterprise-b-release/profile-*` 回执为准；持续观察仍保持停用。

### 统一 Web 入口

北京时间 2026-10-01 21:48 按用户要求仅保留 A 的 `/app/`。变更提交 `cbd8ec7d4a8a953041d9f9f1b79409ce59604c16` 已推送。B 网关配置验证通过后热重载，HTTP/HTTPS 的根路径、`/app` 及其全部资源、`/web` 均返回 404，无重定向；B `/admin/`、`/ready` 和 A `/app/` 返回 200。网关保持健康且未重启，业务服务未变。实际回执为 B `ops/web-entry-disabled/completed.json` 和本机 `build/enterprise-b-release/b-chat-entry-disabled.json`；之前部署回执中的双 Web 入口属于发布时历史状态。观察脚本预期已同步为 B `/app/` 返回 404，自动观察继续停用。Web 文件仅保留为既有发布/备份材料，B 网关不再提供这些文件。
