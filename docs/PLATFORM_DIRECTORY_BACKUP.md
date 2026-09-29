# 平台认证库备份与隔离恢复

本工具属于 R2 收尾代码，只处理平台认证库及其私有运行配置，不读取企业聊天、IM 或媒体。
提供加密备份、隔离恢复、实时身份校对及人工受控激活。[异地传输工具](OFFSITE_BACKUP.md)
可受控交付／下载密文；schema 18 提供每日快照与自动异地交付，schema 19 增加恢复冻结记录。
新增激活/后续账号核验流程见 [三端收尾操作说明](MULTITENANT_CLOSEOUT_OPERATIONS.md)。
不得以手工删隔离标记替代。没有执行现网备份、迁移或恢复。

## 归档与身份

- 平台 schema 17 新增单例目录 UUID 和备份记录，重复迁移不改变 UUID。
- 归档绑定 `scope=platform`、目录 UUID、备份 ID、发布 ID、Compose SHA256、平台 schema；
  不使用假企业 ID，也不能被企业恢复工具接受。企业旧归档的序列化格式保持兼容。
- `compose / release / database` 三个文件及清单分别流式加密，使用独立 32 字节密钥和
  现有 AES-GCM 分块认证。清单最后写入；中断归档不能通过校验。文件和目录禁止覆盖。
- 目录身份及预期发布摘要必须从独立运维记录选择，不能让下载的归档决定恢复目标。
- Compose 含认证、供应商配置等秘密，同数据库一起加密；明文密码不进入命令参数、日志
  或审计。只备份现有密码哈希，不导出用户明文密码。密钥在归档目录外单独保存、单独异地保管。
- 平台 Redis 是临时限流/输入状态等运行态，不作为可复活认证会话的恢复来源。

## 备份行为

`pg_dump` 使用 PostgreSQL 导出的同一个 MVCC 快照；不要求停止所有企业业务。
备份持有独立互斥锁及平台迁移共享锁，期间 schema 不能切换。持锁连接丢失会取消子进程，
不确认成功。平台存在额外业务表、其他用户 schema、Large Object 或版本不符时拒绝备份。

开始和完成审计与对应记录分别同事务提交。请求绑定输出目录哈希、操作者、理由和归档身份；
重复请求不能换输出目录。若清单已完成但 DB 确认丢失，再次执行会认证原归档并补确认，不重新
导出、不覆盖。若归档不完整，保留其证据，用新的备份 ID 和新目录发起新尝试。

## 构建与测试

PowerShell 7，仓库根目录：

```powershell
./infra/scripts/build-platform-backup.ps1 -Test
```

构建 Linux/amd64 静态运维程序及固定 PostgreSQL 17 工具镜像。测试只连一次性测试 PG，
创建随机命名新数据库，结束时精确清理测试库；不挂载默认企业卷或宿主机 Docker socket。
默认 `build-platform-backup.ps1` 只构建，不启动备份。生产使用经配送核对的镜像摘要，
不可把本机 `:local` 标签当作固定生产版本。

主机原生 CLI 也可运行，但须安装匹配的 `pg_dump` 和 `pg_restore` 并配置绝对路径。
本机 Windows 未安装 PostgreSQL 原生工具，因此真实导出/恢复验证在 Linux Docker 内完成。

## 私有运维配置

JSON 放在仓库外受限目录。以下是容器内部路径示例，所有占位值必须替换，不能直接上线：

```json
{
  "databaseUrl": "postgres://platform:REPLACE_PRIVATE_PASSWORD@platform-db:5432/platform?sslmode=disable",
  "dumpBinary": "/usr/local/bin/pg_dump",
  "restoreBinary": "/usr/local/bin/pg_restore",
  "archiveDirectory": "/archives/backup-one",
  "composeFile": "/release/compose.json",
  "releaseFile": "/release/release.json",
  "expected": {
    "scope": "platform",
    "directoryId": "REPLACE_WITH_INDEPENDENT_DIRECTORY_UUID",
    "backupId": "backup-one",
    "releaseId": "platform-r1",
    "releaseDigest": "REPLACE_WITH_COMPOSE_SHA256",
    "schemaVersion": 21
  },
  "actor": "operator-id",
  "reason": "例行平台认证库备份"
}
```

仅支持明确 PostgreSQL URL、单一库名、`sslmode=disable`（仅本机隔离网络）或
`verify-full`（显式 CA 文件）。拒绝 `host/options/service/search_path` 等隐藏覆盖。
不公开平台数据库端口来迁就运维工具；helper 加入平台私网，按需挂载配置/密钥/发布只读目录和
归档输出目录。只读根文件系统、无 capabilities、无 Docker socket，运行完退出。

可用子命令：

- `-mode inspect -config <absolute-path>`：只返回目录 UUID 和 schema，不返回账号或凭据。
- `-mode create-key -key-file <absolute-path> -confirmed`：排他创建密钥，不覆盖已有密钥。
- `-mode backup -config <absolute-path> -key-file <absolute-path> -confirmed`：创建新归档。
- `-mode verify -config <absolute-path> -key-file <absolute-path>`：认证所有文件，输出密文清单证明。
- `-mode stage-restore -config <absolute-path> -key-file <absolute-path> -confirmed`：只做隔离恢复。

成功输出不包含下载链接、凭据或数据库连接串。错误输出故意不转发 PostgreSQL stderr。
不得把私有配置贴到工单、聊天或 Git；密钥不能位于归档内。

## 隔离恢复，不是激活

目标须为独立新数据库，不能有用户表/视图/函数/类型/扩展/大对象或其他连接。
原数据库及原卷始终保留。归档先完整认证，再以平台迁移锁保护创建持久
`frogim_recovery` 隔离 schema，**先提交隔离标记，再启动 `pg_restore`**。

恢复后：

- 撤销用户/后台会话、一次性登录票据、找回密码能力及推送设备绑定。
- 关闭旧的待发送推送和备份调度；账号、封禁和生命周期任务证据保留，不猜测完成情况。
- 新平台二进制在迁移/后台初始化/任务 worker 启动前检查隔离 schema；存在即拒绝启动。
- 相同成功恢复可重复核验；失败或中断的目标保持隔离，不能盲目重复导入或恢复登录。
- 只有完成源企业撤权、实时归属和未完成任务的专门校对后，才能由后续受控流程解除隔离。
  该激活流程尚未交付，当前工具没有解除隔离命令。

不能使用不理解隔离标记的旧平台二进制启动恢复库。恢复旧快照可能丢失快照之后的封禁、
改密或调换记录，所以“归档完整”绝不等于“身份安全可激活”。真实灾备 RPO/RTO、异地存储
权限及企业状态校对仍是上线阻断项。

## 灾后身份校对（审查，不激活）

`platform-backup` 新增 `recovery-review` 和 `recovery-review-status`，只允许已成功
`stage-restore` 的隔离新库。绑定必须匹配独立选择的原目录、备份及清单 SHA256／大小；
正常平台库、未完成恢复或错误归档不能使用。无需新增正常平台／企业数据库迁移。

企业私有 mTLS 控制接口 `POST /internal/tenancy/recovery/inventory` 只向验证过的平台
身份开放，不提供公网、App 或企业后台入口。它要求指定企业已在指定访问版本完成停用，
返回本地身份、账号／归属／认证版本、手机号、禁用状态及未完成任务计数，不读取或返回
密码哈希、令牌、昵称、好友、聊天或媒体内容。每页 500 个身份，最多 10 万；固定 nonce、
完整摘要、顺序和最终重读防止分页混合不同状态，变化时整次拒绝，不静默截断。

运维配置从独立服务器清单选择所有相关企业，不能只依赖旧快照。企业已在快照之后新增时
也应列入；未提供的旧企业明确报告 `ENTERPRISE_EVIDENCE_MISSING`。工具不自动停用企业、
不清除未知通话握手、不恢复账号、不启动旧 worker。企业不可达或未确认停用时保留
`collecting`，修复原因后重试；不降级为空清单。

独立私有配置示例（只示意结构，所有占位符必须替换，`peers` 按 tenantId 升序且无重复）：

```json
{
  "databaseUrl": "postgres://platform:PRIVATE_PASSWORD@recovery-db:5432/recovery?sslmode=disable",
  "caFile": "/operator/control-ca.pem",
  "certificateFile": "/operator/platform.pem",
  "privateKeyFile": "/operator/platform-key.pem",
  "expected": {
    "scope": "platform", "directoryId": "INDEPENDENT_DIRECTORY_UUID",
    "backupId": "backup-one", "releaseId": "platform-r1",
    "releaseDigest": "ORIGINAL_COMPOSE_SHA256", "schemaVersion": 18
  },
  "manifest": {"name": "manifest", "sha256": "VERIFIED_MANIFEST_SHA256", "size": 1234},
  "reviewId": "review-one", "actor": "operator-id",
  "reason": "灾后身份校对", "confirmed": true,
  "peers": [{
    "tenantId": "default", "httpBaseUrl": "https://tenant.example.com",
    "controlUrl": "https://tenant.control.example.com:8444", "realmVersion": 2
  }]
}
```

- `-mode recovery-review -config <absolute> -confirmed`：先持久记录意图和审计，再读取企业。
  不传 `-key-file`（归档已验证并隔离恢复）；配置中的证书必须具有平台 mTLS 身份。
- `-mode recovery-review-status -config <absolute>`：只读取同一请求的历史报告，不联系企业。
  `observedAt` 只是本次采集完成时间，不表示各企业形成跨库原子快照，也不是当前授权凭证。
- 校对记录位于私有 `frogim_recovery.reviews`，不进入普通账号资料或后台查询。CLI 只返回
  问题代码、必要身份 ID、计数与摘要，不输出手机号／配置／凭据；审计只保存摘要和问题计数。
- 相同请求和未变证据重复完成不产生重复审计；网络失败可重试。来源或企业证据变化时，
  原报告保留，须用新 `reviewId` 重新审查，不能覆盖旧证据。迁移锁连接丢失会取消采集。
  任何审计失败都不能写成功状态。

报告识别新增／缺失企业和账号、归属／认证版本变化、手机号关联冲突、多个未退休身份、
禁用／删除／未完成身份、未绑定旧用户，以及改密／封禁／调换／部署／备份／维护等未完成
任务。历史退休身份保留，但不当成当前有效身份。**即便身份版本完全相同，旧快照也不能证明
其后没有平台独有的改密／封禁记录**，所以报告始终要求 `CREDENTIAL_REVERIFICATION_REQUIRED`
和 `OLD_AUTHORITY_FENCING_REQUIRED`，始终返回 `activationAllowed:false`。

`state=completed` 仅表示本次校对和审计完成，**不是灾备完成或允许启动认证**。必须另行
执行受控 `recovery-prepare` 和 `recovery-activate`；旧源隔离、人工核验及新凭据缺一不可。

## 每日快照和自动异地交付（schema 18）

`platform-backup` 增加独立守护模式，不在认证 API 内执行 pg_dump。计划默认不存在／关闭，
只能由持有主机私有配置的运维配置；没有接收任意数据库地址、命令或存储凭据的后台接口。
平台身份 UUID、计划版本、UTC 日期任务、独立尝试、密文证明、异地收据及审计均持久化在
平台库。每天同一 UTC 日期只创建一个任务，配置变更不导致当天重复备份。

- 开始分钟为 UTC `0–1439`，允许开始窗口 `15–180` 分钟。快照不停止认证服务。
- 窗口之外不补做未开始的快照；恢复运行时把最近错过的窗口标为 `WINDOW_MISSED`，不得
  将没有新备份视为成功。已开始的任务继续恢复；人工新尝试视为明确的即时运维授权。
- 捕获与异地上传分别持有互斥锁和迁移共享锁。上传慢、失败或退避不阻塞下一天的快照。
  失去持锁连接即取消外部工作，不写成功确认。两个进程不能重复处理同一个阶段。
- 清单已完成而数据库确认丢失时，校验原归档补确认；即使运行包已升级也不重生成。
  半成品进入 `needs_attention`，保留旧目录。确认原因后用新 ID／目录做新尝试。
- 异地失败按 30 秒递增、最多 15 分钟退避；始终重新认证和重传同一归档，不重新导出数据库。
  本地 `captured` 不代表异地完成；仅密文交付、日期索引读回和数据库审计均成功才为 `completed`。
- 路径、加密密钥和存储目标指纹固定；允许同目标凭据／CA 路径轮换及发布升级。有未完成
  任务时不允许把计划换到其他目录、密钥或存储目标，避免旧任务被遗弃。
- 关闭计划只阻止新的／尚未开始任务，已捕获的异地交付继续处理。恢复出的库先关闭两类
  调度且保留隔离标记。schema 17 的旧归档仍可验证和隔离恢复，不自动升级或激活恢复库。

独立私有 `worker.json` 示例（容器内路径，全部占位符必须由运维替换）：

```json
{
  "databaseUrl": "postgres://platform:PRIVATE_PASSWORD@platform-db:5432/platform?sslmode=disable",
  "dumpBinary": "/usr/local/bin/pg_dump",
  "archiveRoot": "/archives",
  "composeFile": "/release/compose.json",
  "releaseFile": "/release/release.json",
  "expected": {
    "scope": "platform",
    "directoryId": "INDEPENDENT_DIRECTORY_UUID",
    "backupId": "daily-template",
    "releaseId": "CURRENT_PLATFORM_RELEASE_ID",
    "releaseDigest": "CURRENT_COMPOSE_SHA256",
    "schemaVersion": 21
  },
  "destination": {
    "id": "platform-offsite", "scope": "platform",
    "directoryId": "INDEPENDENT_DIRECTORY_UUID",
    "endpoint": "https://backup.example.com", "bucket": "platform-backups",
    "prefix": "frogim", "region": "us-east-1",
    "accessKey": "PRIVATE_SCOPED_ACCESS_KEY", "secretKey": "PRIVATE_SCOPED_STORAGE_SECRET"
  }
}
```

如使用独立 CA，在 destination 添加 `/operator/storage-ca.pem` 的 `caFile`。密钥固定挂载在
`/keys/platform-backup.key`，必须为独立 32 字节文件，不能放入归档、配置或发布目录。

运维模式（均传入绝对 `-config` 和 `-key-file`）：

- `schedule-configure`：另复制一份配置，添加 `change`；要求命令行 `-confirmed`。
  `change` 含 `requestId`、`expectedVersion`（首次为 0）、`enabled`、`startMinuteUtc`、
  `windowMinutes`、`actor`、`reason`、`confirmed:true`。重复同请求返回原结果，旧版本／
  同 ID 不同输入拒绝，审计失败整笔回滚。
- `schedule-status`：返回计划及最近 31 个日期的状态、错误码、尝试数、密文证明和收据。
  不包含连接串、密钥或文件路径。监控必须检查最新日期与 `completed`，不能只看容器运行。
- `schedule-once`：处理一个到期捕获和一个可重试上传，输出状态，不绕过计划窗口。
  退出码 0 表示本次协调／持久记录成功，**不等于异地任务全部成功**，须检查 JSON 状态。
- `schedule-run`：每 30 秒分别协调捕获和交付，单次工作上限 60 分钟；信号退出保留任务。
  此模式的配置必须不含 `change/retry`，避免启动时意外修改计划。
- `schedule-retry`：另复制配置添加 `retry`，含 `runId`、`expectedAttempt`、`requestId`、
  `actor`、`reason`、`confirmed:true`，同时传入命令行 `-confirmed`。只允许已核查的
  `needs_attention`，不允许覆盖完整备份／异地收据，重复请求不产生第二次新尝试。

可选服务模板：`infra/tenancy/compose.platform-backup.yml`。生产在对应 Linux 主机使用固定
helper `@sha256` 镜像、`frogim-platform_platform` 私网及**既有**四个私有目录；模板不
创建缺失挂载目录、不开放端口、不挂 Docker socket。额外 egress 网络仅为异地 TLS 出站。
通过 `PLATFORM_BACKUP_IMAGE/NETWORK/OPERATOR_DIR/KEY_DIR/RELEASE_DIR/ARCHIVE_DIR` 显式
配置；环境变量中只放镜像、网络名及路径，不放凭据。主机目录须给予容器 UID 10001 必要的
读权限／归档写权限，禁止全局可写。只读挂载配置/密钥/发布；归档盘容量和保留期必须监控。
模板未加入本机默认 Compose，本次不启用默认计划或新增真实异地资源。

先核对镜像摘要、挂载目录和网络，再用单次 helper `schedule-configure` 设置计划；使用
`worker.json` 启动守护进程。平台 schema／发布变更时，先停止维护进程，保留原归档和配置，
更新 helper 与挂载发布的摘要，复核新 `expected` 再启动。不能使用旧 schema 的配置继续导出
新库；已完成旧归档不受发布升级影响。手动恢复始终是另一个明确确认的流程。

## 主机丢失后的日期收据发现

自动交付在密文清单后写入加密索引：
`<prefix>/platform/<目录 UUID>/daily/<UTC YYYY-MM-DD>/receipt.sealed`。
索引包含受认证的日期和原交付证明，禁止覆盖；确认丢失时读回比较逻辑内容。无需授予列桶
或删除权限，也不创建“latest”这种可覆盖指针。密钥与目录 UUID 要独立异地保管。

`backup-offsite -mode discover-daily -config <absolute> -key-file <absolute> -confirmed`
使用 `destination`、`dailyDate`、`journalDirectory`、`actor`、`reason`，不填 `expected`、
`archiveDirectory`、`deliveryFile`。命令写私有意图、加密索引的核验结果和 `delivery.json`；
不同请求不能复用同一日志目录。只发现收据，不下载数据库、不授予恢复身份、不解除隔离。
运维核对收据中的绑定后，另行配置现有 `download` 和 `stage-restore`。若没有当天索引，
明确检查指定其他日期，不猜测、不自动回退到未知历史快照。
