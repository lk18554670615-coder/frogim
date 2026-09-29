# 三端多租户收尾操作说明

平台与同机默认企业的共享 PostgreSQL/Redis 布局、按库备份和新库恢复见
[同机共享数据说明](MULTITENANT_SHARED_HOST.md)；不要在该模式单独恢复整 Redis 数据卷。

本轮代码面向 Android、iOS、Web。生产切换、真实账号迁移、供应商启用和发布仍需另行安排。
命令示例在 PowerShell 7 中执行；JSON、证据和密钥放仓库外受限目录，路径为绝对路径。
本文中的域名、版本和数据库身份均为占位值，不能直接用于真实部署。

## R2：隔离恢复和人工开服

先按 [平台备份手册](PLATFORM_DIRECTORY_BACKUP.md) 验证密文并 `stage-restore` 到**空的新库**。
保留原库、原卷、原配置、完整备份及独立保管的密钥。不要删 `frogim_recovery` schema/guard。

1. 停止旧平台认证入口、控制入口、后台 worker、维护 agent 和备份调度，撤回旧主机网络权限。
   记录操作者、时间、工单、旧主机隔离检查及所有企业清单。旧平台恢复联网也不能再控制企业。
2. 使用全新 CA/密钥链签发替换平台及企业控制证书。保留完整旧 CA 集合供工具检查；不能仅重签
   同一 CA 密钥，不能把新旧根同时留在企业信任池。更新每个企业的控制 URL 和实际加载的信任，
   确認企业已暂停且撤权完成。网络隔离、旧 agent 撤权由运维执行，工具不会代改防火墙/DNS。
3. 用 `recovery-review` 收集全部企业实时清单；每个 peer 使用独立 mTLS 身份，清单分页带摘要。
   已完成的报告仍返回 `activationAllowed:false`。相同 ID/输入可重试；证据变化必须新建 review。
4. 对需要恢复的账号逐个验证身份、手机号、企业/本地 ID、分配版本、认证版本、封禁情况及
   快照后的改密/调换记录。无法证明的账号不列入 `accounts`。为列入账号生成全新 bcrypt
   密码哈希（cost 10–14，建议 12），通过受限文件输入；不复用备份哈希，不把明文放入参数。
5. 配置、prepare、activate、status 均持久化操作 ID/输入摘要/操作者/理由/结果。
   新平台尚未运行时执行下面命令；程序连接持锁，断连、审计失败、证据变化保持原隔离状态。
6. `activated` 只允许绑定的新控制证书启动平台，**所有企业仍是 suspended**。检查账号/任务
   冻结及审计后，另行通过原平台企业启用任务逐个开服；必须获得实际企业完成确认。

```powershell
& .\platform-backup.exe -mode recovery-prepare -config 'D:\operator\activation.json' -confirmed
& .\platform-backup.exe -mode recovery-activate -config 'D:\operator\activation.json' -confirmed
& .\platform-backup.exe -mode recovery-status -config 'D:\operator\activation.json'
```

`activation.json` 复制原 review 的 `expected/actor/reason/reviewId/manifest/peers/confirmed`
和 `databaseUrl/caFile/certificateFile/privateKeyFile`；删除其他模式专用字段，然后增加：

```json
{
  "activationId": "recovery-20260929-one",
  "evidenceDigest": "REPLACE_REVIEW_RESULT_EVIDENCE_DIGEST",
  "authoritySha256": "REPLACE_NEW_PLATFORM_LEAF_DER_SHA256",
  "oldAuthoritiesSha256": "REPLACE_EXACT_OLD_CA_FILE_SHA256",
  "oldCaFile": "D:\\operator\\old-control-ca.pem",
  "platformControlUrl": "https://new-control.example.com",
  "maintenanceEvidenceSha256": "REPLACE_SAVED_MAINTENANCE_RECORD_SHA256",
  "adminUsername": "recovery-operator",
  "adminCredentialSha256": "REPLACE_JSON_STRING_HASH_COMMITMENT",
  "adminPasswordHashFile": "D:\\operator\\new-admin.bcrypt",
  "accounts": [
    {
      "identity": {"accountId":"account-one","tenantId":"default","localUserId":"original-user","assignmentVersion":2},
      "authVersion": 3,
      "verificationSha256": "REPLACE_SAVED_IDENTITY_VERIFICATION_SHA256",
      "credentialSha256": "REPLACE_JSON_STRING_HASH_COMMITMENT"
    }
  ],
  "accountPasswordHashFiles": {"account-one":"D:\\operator\\account-one.bcrypt"}
}
```

这是需要合并的字段示例，不是完整可运行配置。`accounts` 按 accountId 升序；空数组允许只恢复
平台管理能力，所有用户继续冻结。哈希文件只含一条 bcrypt 哈希，可带末尾换行。
credential 承诺是 **SHA256(JSON 编码后的哈希字符串，UTF-8，无 BOM/末尾换行)**，可用：

```powershell
$newHash = (Get-Content -LiteralPath 'D:\operator\account-one.bcrypt' -Raw).Trim()
$encodedHash = ConvertTo-Json -InputObject $newHash -Compress
[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($encodedHash))).ToLowerInvariant()
```

不要把生成的新凭据、DSN 或私有配置放入测试日志。`verificationSha256` 和维护证据为人工记录
承诺，运维负责保存并审核其内容；工具另外验证真实企业证据、控制身份及旧根隔离，二者不可替代。

后续核实冻结账号：停止新平台及调度，重新暂停并核对全部企业，创建新 review/activation ID，
使用 `mode:"accounts"`，移除三个 admin 字段，提供需恢复账号及新哈希；仍执行相同三个命令。
工具拒绝在线恢复、已删除/版本倒退/重复活跃身份、企业封禁、未完成撤权或旧未完成账号任务。
已知全局封禁继续保留，解除封禁必须另走正常运维任务；未知封禁历史必须继续冻结。

旧任务及原状态保留于原表，`platform_recovery_holds` 阻止自动执行，不会凭旧快照完成或跳过它们。
旧未完成任务可能继续阻止对应账号或企业开服，这是有意的安全边界；异常由运维核查原任务和
实际撤权结果。不能删除 hold、改任务为 completed 或删除隔离标记来绕过。工具不提供通用取消。
只读核查可查询 `kind,object_id,recovery_id,created_at`；账号资料通过原管理状态查询查看。
企业实时存在 pending 撤权/未关联身份时还有独立 tenant hold。

已激活库的控制证书指纹固定在 guard 中；启动配置必须与该证书一致，不可在发布时随意换证书。
保留私有 `frogim_recovery` 审查/激活账本的受限运维副本和外部证据；日常认证库归档只导出 public
schema，不能用它代替这些恢复操作证据。新恢复仍须完整重做隔离/核验，不能复用旧激活许可。

## R3：旧默认企业迁移及强制升级

使用已有预检、冷备份、新卷恢复、显式接管和 `tenant-import`；新增 `tenant-migrate` 记录每阶段
回执并限制开写。它不执行任意 shell，不复制/覆盖原卷，也不声称已经替运维完成路由或防火墙操作。

```powershell
go -C server build -o ..\build\tenant-migrate.exe ./cmd/tenant-migrate
& .\build\tenant-migrate.exe -mode start -config 'D:\operator\cutover.json' -confirmed
& .\build\tenant-migrate.exe -mode advance -config 'D:\operator\cutover.json' -confirmed
& .\build\tenant-migrate.exe -mode status -config 'D:\operator\cutover.json'
```

配置包含 `request`、`step`、`platformDatabaseUrl`、`adoptedDatabaseUrl`、`evidenceFile`。
数据库 URL 必须明确指向本机隔离/隧道地址，原部署与新部署必须不同。身份取
`SELECT system_identifier::text||'/'||current_database() FROM pg_control_system()`，不能填 DSN。

```json
{
  "request": {
    "id":"default-cutover-one", "tenantId":"default", "batchId":"legacy-import-one",
    "actor":"migration-operator", "reason":"默认企业维护窗口迁移",
    "originalDatabase":"1111111111111111111/old_db",
    "adoptedDatabase":"2222222222222222222/new_db",
    "oldVersions":{"android":"1.0.0","ios":"1.0.0","web":"1.0.0"}
  },
  "step": {"expectedPhase":"planned","phase":"stopped","reason":"已核对停写与旧认证隔离","evidenceSha256":"REPLACE_FILE_SHA256","confirmed":true},
  "platformDatabaseUrl":"postgres://PRIVATE@127.0.0.1:15432/platform?sslmode=disable",
  "adoptedDatabaseUrl":"postgres://PRIVATE@127.0.0.1:15433/new_db?sslmode=disable",
  "evidenceFile":"D:\\operator\\stopped.json"
}
```

| 回执阶段 | 人工操作及工具核验 |
|---|---|
| planned | 默认企业处于 provisioning/suspended；绑定原库/新库、批次和三端旧版本。 |
| stopped | 停写并隔离旧认证/控制入口，新栈不对外；保存检查记录。 |
| backed_up | 保存数据库、媒体、IM、Redis、部署配置的完整备份，核验各文件大小和 SHA256。 |
| adopted | 只在新库/新卷用既有显式接管流程；新企业已暂停且平台/企业暂停回执一致。 |
| imported | 既有导入批次和逐账号任务全部完成，企业没有未关联的非删除用户。 |
| routed | 新客户端及升级渠道可用；平台三端策略明确强制升级给定旧版本，下载地址为 HTTPS；旧地址仅放行升级/必要媒体路由。 |
| opening | 再次验证隔离、导入、版本策略，记录不可回退的开写意图；此后才能请求平台企业启用。 |
| completed | 企业已 active 且对应 resume 任务有完成确认；归档最终路由和验收记录。 |

每次 advance 的 `evidenceFile` 内容：`phase/originalDatabase/adoptedDatabase` 必须匹配；
`oldStackStopped:true,oldAuthIsolated:true`，除 completed 外必须 `newStackPrivate:true`；
`verificationSha256` 指向人工验收记录。`files` 为 `{kind,path,size,sha256}` 数组。
backed_up 必须包含 `database/media/im/redis/deployment` 五种完整文件；工具逐个读全并验证，
不接受同长度篡改、相对路径、缺项或重复 kind。其他阶段可附其相关证据文件。

导入命令沿用原配置，维护场景显式设置 `TENANCY_IMPORT_ENV=maintenance` 和
`TENANCY_IMPORT_OFFLINE_CUTOVER_CONFIRMED=true`；仍要求原有预检摘要、mTLS 和逐账号确认。
原用户 ID/业务关联不重建。更换认证 JWT/IM 凭据使旧登录和长连接失效；历史媒体原签名密钥
仅放企业配置 `secrets.legacyMediaSigning`，生成 `IM_LEGACY_MEDIA_SIGNING_SECRET`，必须不同于
新 JWT 密钥。旧域名所需媒体路由继续指向保留数据，不能把旧认证服务一起暴露。

旧地址 `/v2/config/version` 在托管模式通过 mTLS 查询平台统一策略，失败返回不可用，不回落本地策略。
旧登录、刷新和设备登记继续被拒绝。App Store/安装渠道可用性需要人工验收，不凭 HTTPS 字符串
判定真实安装成功；Web 更新需核查站点缓存/Service Worker 和旧页刷新提示。

中断后先 status，再以**相同输入和证据**重试原阶段。`opening` 之前可提交 `rolled_back`，保持
候选环境暂停后由运维恢复经核验的原部署；工具不会自动开启原目录。`opening` 起禁止直接回退，
即便 resume 确认丢失也视为可能已写入；必须先停写、备份新数据，再走受控恢复。

## R4/R6：原任务重试和客户端隔离

使用现有管理查询、错误码、审计、原任务 retry/冷修复入口。重复请求需保持 requestId 和原输入；
状态未知或 lease 丢失时保留待确认，不能换 ID 重复开户/调换，不能跳过撤权。查看同一原任务的
目标企业、账号版本及错误码，先排查数据库/mTLS/企业就绪，再重试。无通用取消或自动补偿。
冻结的灾备旧任务不属于普通 retry 的可运行任务，继续按 R2 保留隔离。

客户端切账号后按身份范围重建请求、媒体凭据、IM/通话连接、缓存、推送和待发送队列；迟到加载、
删除、输入状态、送达确认不会写入新会话。原账号现有重试保留，不增加跨账号续传/草稿同步。
第 39 阶段通话握手冷修复继续使用原操作工具，属于回归项目。

## R5：个推普通通知和 VoIP

平台配置仅 `PLATFORM_PUSH_PROVIDER=disabled|getui`。配置个推 AppID/AppKey/MasterSecret 和
独立 `PLATFORM_PUSH_ENCRYPTION_KEY` 后，普通消息能力为 `getui`。供应商 VoIP 配置和权益完成
核验后再设置 `PLATFORM_GETUI_VOIP_ENABLED=true`；关闭时不声明 `getui_voip`。企业不持有供应商
凭据、APNs token 或 CID，只向平台发受控事件。Web Push 独立配置沿用现有接口。

iOS 的 PushKit token 只交给本机个推 SDK；必须收到当前 CID 回调并成功调用
`registerVoipTokenCredentials`，才以 CID 和本机代次登记 `getui_voip`。token/CID 变化立即清除
原生旧绑定，再注销旧平台绑定并重新登记；退出及调换仍执行原有撤权。原生写入还核对当前 SDK
代次，防止迟到 Flutter 回调复活旧绑定。通知设置页显示未就绪/已登记/已关闭，已登记不是实收证明。

来电请求只走个推 iOS VoIP channel，`strategy.ios=2`，不带普通 `push_message`/Android 通道，
按平台 requestId 生成稳定供应商请求 ID。iOS 普通 getui 绑定不接收 call.invited，避免普通通知
重复响铃；旧 `apns_voip` 绑定在 schema 21 撤销并清除密文令牌，旧待发送投递失效。
配置旧直连 APNs 参数会启动失败；没有自动直连回退。缺绑定返回 `capability_not_ready`，不报 sent。

参考个推官方 [iOS 接口](https://docs.getui.com/getui/mobile/ios/api/)、
[VoIP 接入](https://docs.getui.com/getui/mobile/ios/xcode/)、
[REST 通道参数](https://docs.getui.com/getui/server/rest_v2/common_args/)。
个推 VoIP 权益、APNs 证书/环境、实际 CID/token 生效、PushKit/CallKit 约束均需供应商及真机确认。

## R7：开服前验收单

- Go 测试/vet、后台测试/两种构建、Flutter analyze/test、Web 构建、Android JVM 测试/Kotlin 编译
  分开记录；回归证据见 [实施记录](MULTITENANT_IMPLEMENTATION.md) 阶段 40。
- Windows 无 Apple SDK；Linux Swift 策略和语法解析不能替代 `flutter build ios --no-codesign`
  与 Xcode 编译。沿用 `.github/workflows/ios-build.yml` 或受控 Mac，未运行即待验证。
- 在隔离环境验证两企业请求、上传、缓存、IM、通知/来电切换，原 ID/关系/媒体链接，旧凭据拒绝，
  停写/备份/续跑/切换前回退，以及开写后拒绝回退。再用真实迁移样本核对数量和媒体抽样。
- 真机逐项登记：供应商实收、重复/过期/取消邀请、退出/调换、锁屏接听、后台/杀进程启动、
  CallKit/Android 接听与挂断、音频路由、网络切换、权限拒绝和供应商失败。模拟通过不替代此清单。
- macOS 本轮延期。没有自动发布、资源购买或真实账号迁移步骤。
