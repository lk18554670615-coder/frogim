# 按平台／企业隔离的异地备份传输

本工具上传现有**加密归档**，不迁移业务、不激活恢复、不修改公开媒体地址。
当前已交付传输核心和受控运维命令；每日调度、代理／平台持久任务中的自动交付与确认仍需接入。
没有配置真实异地服务，也没有创建任何云资源。

## 权限和配置边界

- 每份私有配置固定一个目标 ID、HTTPS 地址、bucket、前缀、区域以及一个平台目录或企业／主机。
  业务请求不能指定这些字段。归档绑定与配置不符时，在发送网络请求前拒绝。
- 每个企业和平台使用独立 IAM 账号／访问密钥；不能复用业务 MinIO、平台根账号或其他企业凭据。
  `policy` 子命令生成对应前缀的最小 `GetObject/PutObject` 策略，不含凭据。
- 工具不需要 `ListBucket`、建桶、管理用户、跨前缀访问或 `DeleteObject`。策略实际配置由运维完成；
  软件不会自行扩大权限。真实供应商的 IAM 生效及跨企业拒绝仍是上线门禁。
- 服务端证书正常验证，私有 CA 可显式指定。没有 `insecure` 开关；不使用环境代理，不跟随到
  其他地址的重定向，也不生成预签名下载链接。备份对象不得套用业务媒体的匿名公开策略。
- 加密密钥与对象存储密钥分别生成；加密密钥不上传、不进入日志／命令参数／完成收据。

对象路径：

```text
<prefix>/enterprise/<tenantId>/<serverId>/<密文清单SHA256>/...
<prefix>/platform/<directoryId>/<密文清单SHA256>/...
```

一个企业只能访问自己的固定前缀；默认企业与新企业没有特殊共享权限。
备份存储可使用同一供应商的不同受限凭据／前缀，这不表示企业业务数据库或 MinIO 实例共用。

## 完成、重试与下载

1. 先在本地认证整个归档及其绑定。
2. 将每个密文文件分成最多 32 MiB 的对象，使用条件 PUT，仅在对象不存在时创建。
3. 已有对象先读回比较；写入或确认丢失后再读回核对，不信任 ETag 代表完整 SHA256。
4. 所有分段校验完成后，最后写入加密的 `manifest.sealed`，作为远端完成标记。
5. 成功后写入确定性的本地完成收据；未确认不能标成成功，不能删除原归档。

支持具有原子 `If-None-Match: *` 条件 PUT 的 S3 兼容存储，当前固定 MinIO 已实际验证。
不承诺忽略该条件头的兼容服务安全可用；真实供应商必须通过相同测试。
同一归档重复上传会验证和复用已有分段，内容不同则停止调查，不覆盖。
未完成远端分段保留，不由备份凭据自动删除；保留策略和清理由独立运维权限控制。

下载需要独立选择的预期绑定、完成收据和加密密钥。先核对加密清单的 SHA256、认证标签和绑定，
再取回分段；每个完整密文文件验证大小、SHA256 和解密认证。全部通过才创建本地完成清单。
仅允许新目录，或与收据严格一致的已完成归档；部分目录不覆盖，重试下载选择新目录。
下载完成不意味着数据库／卷已恢复，更不意味着恢复账号可以上线。

**条件写和无删除权限不是 WORM 保证。** 存储管理员仍可破坏对象；客户端会拒绝损坏数据，
但不能凭空恢复被破坏内容。需要保留历史版本／对象锁时，由实际存储策略与运维验收落实。

## 运维命令

PowerShell 7，仓库根目录。私有 JSON 放在仓库外：

```powershell
go -C server run ./cmd/backup-offsite -mode policy `
  -config 'D:\private-deploy\alpha-offsite.json'

go -C server run ./cmd/backup-offsite -mode upload `
  -config 'D:\private-deploy\alpha-offsite.json' `
  -key-file 'D:\private-keys\alpha-backup.key' -confirmed
```

配置示例（占位值必须替换，不能直接上线）：

```json
{
  "destination": {
    "id": "alpha-offsite",
    "scope": "enterprise",
    "tenantId": "alpha",
    "serverId": "host-alpha",
    "endpoint": "https://backup.example.com",
    "bucket": "private-backups",
    "prefix": "frogim",
    "region": "us-east-1",
    "accessKey": "REPLACE_PRIVATE_ACCESS_KEY",
    "secretKey": "REPLACE_PRIVATE_SECRET_KEY"
  },
  "expected": {
    "tenantId": "alpha",
    "serverId": "host-alpha",
    "releaseId": "release-one",
    "releaseDigest": "REPLACE_VERIFIED_RELEASE_DIGEST",
    "generation": 1,
    "accessVersion": 2,
    "schemaVersion": 79
  },
  "archiveDirectory": "D:/private-backups/alpha/archive-one",
  "journalDirectory": "D:/private-backups/alpha-transfer-one",
  "actor": "operator-id",
  "reason": "异地保存已确认企业备份"
}
```

平台配置改为 `scope: platform`、独立 `directoryId`，不填写 tenant/server；预期绑定采用
[平台备份说明](PLATFORM_DIRECTORY_BACKUP.md)中的 platform binding。平台与企业不能混用凭据。

下载使用 `-mode download`，配置中的 `archiveDirectory` 改为新的目标，`journalDirectory`
改为本次下载的新运维日志目录，增加 `deliveryFile` 指向独立保存的上传 `delivery.json`。
密钥必须在归档和运维日志目录之外。目录的父目录须已存在，工具不递归发现任何文件。

`intent.json` 保存 UTC 时间、操作者、理由、绑定、目标配置和路径哈希；不含存储凭据、加密密钥
或业务正文。`completed.json` 保存完成审计和证明；之后才创建独立 `delivery.json` 收据。
重复操作保留首次记录时间。已存在记录内容不一致或部分写入时拒绝覆盖；保留证据，
使用新的运维日志目录重新发起。收据也必须独立异地保存，否则源主机丢失后不能依赖受限凭据
枚举寻找备份。企业代理自动交付的收据现在另存入平台持久任务，见下一节；平台自身备份的
日调度／自动交付及平台丢失后的收据恢复仍属于 R2，不把手工保存当成已完成的自动闭环。

Linux/amd64 运维程序已包含在 `build-platform-backup.ps1` 生成的固定 helper 镜像中，
入口 `/opt/frogim/backup-offsite`。只读挂载私有配置、密钥、源归档；上传日志目录或下载目标
按需可写。不要挂载业务数据库原卷或 Docker socket。镜像构建不等于部署，生产使用固定摘要。

## 企业代理自动交付与平台收据

阶段 36 将传输核心接入现有企业冷备任务，包含手动任务和每日维护产生的任务。无需新增数据库
迁移。代理私有执行文件的 `backup` 中增加 `offsite`，内容与上例 `destination` 完全一致；
只接受 `scope: enterprise` 且 tenant/server 与代理绑定相符，不从平台或浏览器接收存储地址。
`directory`、`keyFile` 仍是代理本机绝对路径，解密密钥不能兼作存储凭据。

- `dedicated_host` 代理启用备份必须配置 offsite；平台手动请求和定时维护在停用企业前检查
  目标存在。`local_preview` 可显式只做本地备份，后台必须显示“仅本地归档”。
- 目标 ID、HTTPS 端点、桶、前缀、区域及企业身份的指纹写入私有 journal；重启不能悄悄
  更换目标。可以轮换同一目标的凭据或 CA 文件，实际 IAM 范围仍需运维验证。
- 平台创建任务时绑定 `offsiteTargetId`。升级前已分发的旧任务继续保持原本地范围，不重写
  或重新停机；其恢复可继续，新增任务必须使用已配置的异地目标。旧归档不会追溯标为异地成功。
- 只有归档认证、原九服务恢复和核验完成后，才将异地子状态置为 `pending`。独立 worker
  重新认证原归档及其摘要，再调用分段交付；不执行 Shell，不停业务、不持有维护锁。
- 异地状态为 `pending/running/unconfirmed/completed`；失败自动指数退避（最多 15 分钟），
  单次最多 30 分钟，重启从同一归档重试。已有分段严格读回，不删除/覆盖、不重新做冷备。
- 平台以数据库租约轮询原代理的 mTLS 状态，收据必须匹配原主机、操作、目标、绑定和清单
  摘要，拒绝降版与同版本改内容。收据与 `backup.offsite.*` 审计同事务，审计失败不能确认成功。
  普通本地 `completed` 只代表归档和服务恢复；必须另看 `receipt.offsite.state=completed`。
- 定时维护在本地核验后恢复企业访问，不等待存储恢复；手动备份仍需另外恢复企业。企业
  恢复访问或后续升级不改变旧归档绑定。代理失联显示未确认，不能据此回滚本地成功或重复停机。
- 后台“备份任务”独立显示本地进度、异地目标、交付状态、尝试次数及确认清单摘要，不返回
  端点、桶、密钥、私有路径或底层错误。刷新页面不重新提交任务。

平台归档每日调度、平台自动异地交付、灾后企业状态校对/受控激活仍未完成。即使所有企业
收据已存入平台，也必须让平台归档和独立解密密钥可从异地恢复；不能声称单靠此处已完成灾备。

## 已有自动化证据与外部验收

```powershell
./infra/scripts/test-tenancy.ps1 -StartDependencies -OffsiteDocker
```

显式测试仅运行本机新建的 TLS MinIO：三个独立 IAM 用户分别绑定企业 A、企业 B、平台前缀。
验证真实上传／下载、CLI 和日志、重复操作、条件 PUT、已知存在的跨范围对象读取拒绝、
跨范围写入拒绝、删除拒绝、匿名访问拒绝、远端篡改拒绝以及不可信 TLS 拒绝。
单元测试另覆盖确认丢失、分段边界、部分归档、错误密钥和本地不覆盖。临时容器验证归属后清理。

这不是实际跨机房验收。仍须验证真实供应商可用性、账号隔离、存储保留期、加密密钥异地保管、
完成收据可恢复性、带宽／费用和恢复时间；不能只凭本机通过就开启自动任务。
