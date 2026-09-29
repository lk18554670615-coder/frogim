# 现有服务器真实数据演练记录（2026-09-29）

## 结论和范围

**本机隔离迁移、加密备份恢复和开写前回退已通过。现网没有停服或切换。**
Android、iOS、Web 和 VoIP 改在服务端隔离部署后验收，正式开放业务前仍必须通过。
本次记录不是发布许可；工作区还需冻结提交/制品，服务器配置、证书续期、最终停写快照和
发布门槛仍需按 [发布手册](EXISTING_SERVER_RELEASE.md) 执行。

数据来自 `im-server`（18.163.165.233）的完整定时备份 `20260928T204410Z`，
即北京时间 9 月 29 日 04:44:10。它不是切换时的最新账号名单；演练结束只读复查现网有 535 个用户，全部旧服务仍正常运行。
演练使用独立 Docker 项目 `frogim-legacy-rehearsal-20260929`、新数据库和新卷。
全部容器处于无外网出口的内部网络，没有发布宿主端口；原有本机共享部署不受影响。
同一演练 PostgreSQL 内的 `rehearsal_original`、`rehearsal_enterprise`、
`rehearsal_platform`、`rehearsal_rollback` 分别保存原始、候选、平台及回退副本。

## 真实数据结果

| 项目 | 结果 |
| --- | --- |
| 快照用户 | 522 个；507 个未删除，15 个已删除 |
| 初次预检 | 正确拒绝 32 个不符合平台 11 位号码规则的账号；169 个未删除账号无密码哈希 |
| 经用户确认的异常处理 | 32 个账号保留原 ID、原号码及历史关系，禁用认证，等待核实后修复 |
| 身份导入 | 475 个成功；原密码哈希和用户 ID 保留，其中 145 个仍没有密码哈希 |
| 删除记录 | 15 个原删除记录及限制状态保留 |
| 企业升级 | schema 72→79；平台使用独立数据库；企业始终停用 |
| 中断续跑 | 完成第一个账号后退出进程；新进程继续同一批次并完成，未重复开户 |
| 旧认证 | refresh 撤销、设备绑定清除、旧登录/续期/设备登记路由拒绝；IM/RTC 撤权走真实控制接口 |
| 历史关系 | 好友、群及成员、媒体、消息索引/扩展等 13 张业务表的旧列摘要一致 |
| 媒体 | 630 个对象逐个通过 S3 读取并核对 SHA256；保留 `nexachat-media` 和对象名 |
| 历史消息 | 从恢复后的真实 IM 抽查 100 条群消息，与原数据库索引一致 |
| 数据库恢复 | 重新从备份恢复回退库，73 张原表逐表摘要一致，原 schema 72 保留 |
| Redis | 原 RDB 校验及独立启动通过；新共享 Redis DB0/DB1 均为空，未继承旧会话 |
| 回退 | 原 API/IM 镜像、原数据副本启动成功；真实 Caddy 从维护响应切回旧 API，readiness 200 |
| 开写边界 | 回执 `planned → stopped → backed_up → adopted → imported → rolled_back`；从未进入 `opening`，提前启用被拒绝 |

145 个无密码账号没有被补造密码。演练企业停用，因此不会开放登录；正式开放前需验证平台
短信登录/找回或受控凭据重置通路，并填写 `passwordlessAccess` 验收记录。
32 个异常账号不能通过简单取消封禁或手改身份状态恢复，必须核实身份、修复号码、重新绑定并重置凭据。

## 加密备份与证据

完整旧备份在服务器通过 `SHA256SUMS` 校验，本机解密后再次验证全部 **633 个文件**，
合计 **589,508,275 字节**。离机密文约 590 MB：

```text
.data/release-rehearsal-20260929/legacy-source-verified.tar.fgbk
SHA256 49d8c345a77a5f7465c6fd243a6af81ba53d93dcd6890574c6b1311c5a558036
```

另外备份了 Redis、部署配置、容器定义、证书目录和 systemd 定时任务，密文为
`supplement.tar.fgbk`，SHA256：
`5dab2900e730f3f4b9aec65e23a9c4c4951b696711ba8aff5cf2e2d9344d28b6`。
补充采集时间是 **2026-09-29 11:57:00 UTC**，与凌晨快照不同；只能作为演练补充，
**不能声称它们是同一停写时刻的正式切换备份**。

现网原 API 和 IM 镜像另存为 `legacy-images.tar.fgbk`。加载到本机后，逐个核对镜像 ID
与加密容器清单一致；没有用重新编译的新镜像冒充旧环境。

备份密钥位于独立受限目录 `.data/backup-keys/`，不包含在密文归档里。
本机密文、解密工作副本及私密配置均不进入 Git。失败的传输文件不属于有效备份。
SSH 直连曾多次超时，最终通过本机已配置代理传输，仍校验原有 SSH known_hosts。

私密回执位于 `.data/release-rehearsal-20260929/`：

- `backup-verified.json`：文件数量、传输及密文摘要。
- `preflight-before-adoption.json`：原始异常清单；`quarantine-request.json`：已批准的 32 个用户 ID。
- `interrupted-import.json`、`migration-completed.json`：中断点与最终导入状态。
- `database-restore.json`：73 表恢复摘要。
- `media-history-restore.json`：实际 S3/IM 校验结果；`membership-relations.json`：群成员、会话及单聊映射补充核对。
- `rollback-completed.json`：持久回退回执、实际旧 API/Caddy 检查。

工程日志位于 `build/tenancy-local/phase43-*.log`：Go 全包测试/vet、最终相关包测试/vet、
真实 PostgreSQL 迁移回归、隔离操作故障测试和两阶段发布门槛测试均通过。
`phase43-rehearsal-interrupt.log` 为受控中断检查，不能独自作为迁移完成证据；
必须同时检查 `phase43-rehearsal-resume.log` 和最终 JSON。

## 可重复的运维入口

`backup-file` 复用现有 `tenant-backup` 的分块 AES-256-GCM 格式，检测截断和篡改。
输出只允许新建私密文件，不覆盖既有文件，解密失败删除本次未完成输出。

```powershell
go -C server build -o C:\private\backup-file.exe ./cmd/backup-file
C:\private\backup-file.exe -mode decrypt -input C:\private\snapshot.tar.fgbk -output C:\private\restored.tar -key C:\private-keys\backup.key
```

异常账号处理在 schema 升级、企业实际撤权和双方停用确认后执行；先重新运行预检取得
当前指纹，再将审核过的用户 ID 保存为私密 JSON 数组。使用原来的 loopback 数据库环境变量：

```powershell
$env:TENANCY_IMPORT_ENV = 'maintenance'
$env:TENANCY_IMPORT_OFFLINE_CUTOVER_CONFIRMED = 'true'
go -C server run ./cmd/tenant-import -mode quarantine -batch reviewed-invalid-phones -tenant default -actor OPERATOR_ID -reason '保留历史并禁用异常号码账号，待核实后恢复' -confirmed -expected-fingerprint CURRENT_FINGERPRINT -quarantine-users C:\private\reviewed-user-ids.json
```

此操作只接受未绑定、号码无效的指定账号。它复用 `retired` 认证状态，增加永久封禁并清除
旧密码，保留用户/业务记录；同一事务写入 `identity.legacy.quarantined` 审计及输入摘要。
审计失败会回滚，旧证据、不同输入重放、未确认停用或正常号码均拒绝。
随后重新预检、创建原有导入批次、逐账号续跑；`-passwordless-ack` 只确认无密码事实，
不代表登录通路已通过验收，也不会自动启用企业。

真实演练测试入口在 `server/internal/httpapi/legacy_snapshot_rehearsal_test.go`。
必须显式设置 `TENANCY_LEGACY_SNAPSHOT_DRILL=isolated-real-copy` 和私密绝对配置路径，
只接受规定的 loopback 副本库名；不能直接指向现网库。
测试在 Linux 容器内执行，相关配置及 Compose 保留于本次私密 `runtime/` 目录。
`TestLegacySnapshotRehearsal` 首次以 `BatchLimit=1` 运行，退出后以 `0` 续跑；
数据库、媒体/IM、回退测试分别留证。回退测试只操作独立网关的 loopback 2018/18088 端口。

## 正式切换仍需做的事

1. 固定包含本次异常账号处理代码的提交、tag、镜像和配置；现有阶段 42 镜像不包含新增运维代码。
2. 准备生产凭据和内部 TLS、验证证书续期重载、重新检查空间及端口，再通过 `server-deploy` 门槛。
3. 部署隔离服务端后完成三端、Apple SDK/签名安装、短信和个推/VoIP 验收；未通过不进入 `opening`。
4. 切换窗口停写并断开旧连接，生成最新同批次完整备份和实际账号清单，重新校验异常账号及快照指纹。
5. 复核完整切换用时与传输稳定性。本次约 96 秒停用/首账号检查、48 秒续跑只是局部耗时，
   不能将其当作两小时维护窗口的完整耗时承诺。
6. 按回执受控切换；进入 `opening` 后不允许直接恢复旧认证。生产 24 小时观察仍待上线执行。
