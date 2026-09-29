# 18.163.165.233 同机多租户发布记录与操作手册

## 2026-09-30 开服范围调整

用户已明确授权本次真实账号迁移和正式切换，并选择 **Web 先正式开服，移动端后续验收**。
本次使用 `openingScope=web-first`：旧移动端业务通道关闭，升级入口指向正式 `/app/`；
Android、iOS、个推与 VoIP 的七项验收明确延期，不得填写为通过。默认三端严格门槛保持不变。
Web 首发门槛另要求用户授权回执、Web 业务实测、移动升级入口和无密码恢复流程证据；
数据库、备份、迁移、旧认证隔离及受控 `opening` 门槛继续保留。
无密码账号保留原状，不生成共享或默认密码；人工核实身份后走既有运维重置流程。
号码异常账号继续保留数据并禁用，待核实、修复号码和重置凭据。
本段是本次已确认的范围调整，实际完成状态以切换回执为准。

切换期间用户进一步明确改为直接使用服务器完整备份恢复，暂停 Windows 下载以缩短维护时间。
本次 `backupDelivery=server-local-approved`，需保留授权、加密备份解密认证和逐文件摘要校验回执；
原库和原卷保持完整，离机副本明确记为 `deferred`，不得记录为已完成异地交付。
默认发布检查仍要求离机备份，只有上述显式配置及证据齐备时才允许本次服务器本地恢复。

## 原隔离部署结论

2026-09-29 已完成服务器上的隔离并行部署，新平台和 Web 可访问，旧业务继续运行。
默认企业尚未激活，真实账号尚未迁移，详见
[服务器部署记录](SERVER_CANDIDATE_DEPLOYMENT_20260929.md)。

单一公网入口准备代码检查点为 `checkpoint/shared-edge-preparation-20260929`。
2026-09-29 已完成真实快照在本机的隔离迁移、备份恢复及开写前回退，见
[真实数据演练记录](LEGACY_SNAPSHOT_REHEARSAL.md)。尚未停服或迁移现网账号。
按 2026-09-29 后续调整，先完成真实备份、隔离迁移及回退演练，再部署隔离服务端；
Android、iOS、Web 和真实个推/VoIP 在部署后验证。客户端验收期间不开放真实账号业务写入，
使用单独验收身份/测试企业，不能绕过默认企业的停用状态。全部验收通过后再进入 `opening`。
工程测试、Caddy 配置解析和模拟请求不能替代 Apple SDK 编译或真机验收。

2026-09-29 只读复查：目标 `im-server` / `18.163.165.233`，旧项目 `qingwaim`
保持运行，根分区可用 30,347,722,752 字节。IP 证书有效期截至
2026-10-05 10:56:51 UTC。容量和证书在切换前必须重新检查。

## 入口及部署包

保留 HTTPS `https://18.163.165.233`。平台客户端认证基址为
`https://18.163.165.233/platform`，企业发现地址仍为 HTTPS 根地址。

| 路径 | 目标 |
| --- | --- |
| `/`、`/v2/*` | 企业后台、业务 API；旧认证由受管企业服务拒绝 |
| `/platform/` | 平台后台，构建资源基址 `/platform/` |
| `/platform/admin/*` | 平台管理 API，路径不变 |
| `/platform/v2/*` | 平台客户端 API，仅删除 `/platform` 前缀 |
| `/app/` | 新 Web 客户端；`/web/` 跳转到此处 |
| `/im`、`/livekit/*` | IM WebSocket、受鉴权保护的通话信令 |
| `/nexachat-media/*` | MinIO 原桶，保留原 Host、URI、查询串，不删除桶前缀 |
| `/downloads/*`、`/legal/*` | 保留安装包和协议入口 |
| `/rtc`、`/rtc/*`、内部控制及指标路径 | 拒绝公开访问 |

新配置字段：

- `EnterpriseConfig.mediaBucket`：本次填 `nexachat-media`；其他部署不填仍为 `enterprise-media`。
- `EnterpriseProduction.sharedIngress.secret`：仅 `default` 且启用共享数据时有效。
- `PlatformConfig.sharedIngress`：`secret` 和 `httpsPort`。本次建议平台内部 HTTPS 18443，
  企业 `ports.http=18444`、`ports.media=18445`；控制面端口另设，不能重叠。
- 平台和企业公网地址均为原 HTTPS 根地址。两边内部网关绑定各自 `controlBindIp`，
  必须填实际私网 IP。IM/RTC 对外绑定及控制面 mTLS 保持各自配置。
- 两个入口密钥各自生成，不与 API 网关密钥复用。共享入口模式下，两端 `publicTls` 用作内部 TLS，
  使用内部 CA 签发覆盖原 IP 的服务证书，并单独安排到期前换包；不要烘焙六天有效的公网 IP 证书。
  公网短期证书仅由统一入口读取和续期。统一入口验证内部服务证书的 CA 和公网主机名，
  不使用 `tls_insecure_skip_verify`；内部网关先验证入口密钥，再接收真实源地址。
- 发布元数据新增 `ingressMode=shared_edge`，纳入摘要与普通回退校验，灾备恢复保留此模式。

统一入口由主机运维独立管理，Compose 项目为 `frogim-edge`。仅该项目使用 Linux host 网络；
企业执行器仍禁止 host 网络和任意宿主目录挂载。入口只有 80/443 及本机 2019 管理监听，
80 用于 ACME 和 HTTPS 跳转，2019 不得开放公网。业务数据库和 Redis 均不公开端口。

新增离线构建命令（不连接 Docker、不部署）：

```powershell
go -C server run ./cmd/edge-bundle -config C:\private\edge.json -out C:\private\edge-compose.json
```

`edge.json` 包含完整的 `platform`、`enterprise` 私密配置，以及 Linux 目录
`certificateDirectory`、`downloadsDirectory`、`legalDirectory`。
证书目录本次为 `/data/linli-im/shared/letsencrypt`；ACME webroot 为该目录同级的
`certbot-webroot`。整个证书树只读挂载，以兼容 `live` 指向 `archive` 的软链接。
输出 Compose 含密钥，只能保存在受限目录，不能提交 Git 或放到下载目录。
独立平台、独立企业模式不配置 `sharedIngress`，继续使用原入口规则。

平台 Web/后台使用 `build-platform-bundle.ps1 -PlatformUrl https://18.163.165.233/platform` 构建。
Android 发布脚本默认同一平台路径，也可显式传 `-PlatformAuthUrl`；`-BuildOnly` 只构建候选包，
记录 `onlinePreflightPassed=false`，不代表可切换。iOS 两种构建流程均使用平台认证。
最终版本号必须高于当前正式版本，并在三端验收前固定；本轮没有发布新版本或修改现网版本策略。

## 发布证据门槛

复制 `infra/tenancy/existing-server-release.example.json` 到私密工作目录。
填入最终提交 SHA、tag、六类制品（edge/platform/enterprise/android/ios/web）的绝对文件路径及 SHA256。
`evidence` 每项格式为：

```json
{
  "status": "passed",
  "operator": "实际验收人",
  "commit": "最终40位提交SHA",
  "checkedAt": "实际UTC时间",
  "path": "验收记录绝对路径",
  "sha256": "记录文件SHA256"
}
```

执行：

```powershell
./infra/scripts/test-existing-server-release.ps1 -Manifest C:\private\release.json -Stage server-deploy
# 服务端部署后三端及 VoIP 验收齐备，再执行默认严格开服检查：
./infra/scripts/test-existing-server-release.ps1 -Manifest C:\private\release.json -Stage open-business
```

`server-deploy` 要求 `deploymentMode=isolated`、`businessWritesEnabled=false`，核验服务端/Web 制品、
Go/后台/Web 工程检查、真实迁移与回退、离机备份/恢复、证书续期及主机预检。
三端工程检查、签名安装、真实供应商与锁屏来电可在部署后补齐；无密码账号登录/找回通路
通过 `passwordlessAccess` 单独留证。`open-business` 额外核验这些项目及
Android/iOS 制品，缺项返回失败。默认仍是严格开服门槛，隔离部署通过不构成开服授权。
两阶段均拒绝脏工作区、tag 与提交不符、证据或制品被修改及过期主机预检。
开放业务前，完整演练须不超过 90 分钟，切换前回退不超过 30 分钟。并行隔离部署不执行停写或
正式数据切换，完整维护窗口计时可待开服前核验；已经完成的数据迁移、备份和回退证据仍必需。
两阶段均要求预估复制/镜像用量之外至少保留 10 GiB。
该检查只读，不会因“通过”而自动停服。空模板必须失败。

## 正式切换与回退

1. 隔离部署门槛通过后，先部署候选服务，完成三端/VoIP 验收，再确定正式数据切换窗口。
   测试使用独立身份及测试数据，候选默认企业仍隔离。记录操作者、窗口、旧 release、配置摘要、备份密钥位置。
   切换前完成镜像预载；不在维护窗口临时编译。旧部署路径、数据库、卷均保留。
2. 0—20 分钟：维护入口；停止所有旧写入、任务、IM 及 RTC；完成最终数据库、Redis、媒体、
   IM 和配置备份。加密离机副本保存到 Windows，密钥单独保管，校验传输前后摘要。
3. 20—65 分钟：旧库恢复到新企业数据库，新卷接收媒体/IM 副本；用现有 `tenant-preflight`、
   显式接管、`tenant-import` 完成迁移。保留 ID、关系、封禁状态和当前有效密码哈希；未知账号禁用。
   PG 仍为一个实例两库；Redis DB0/DB1 新建为空，不导入旧登录令牌、验证码、锁或设备绑定。
4. 65—90 分钟：核对计数、关系、历史媒体、身份及禁用状态；确认旧认证与控制权限已隔离；
   检查升级策略、下载入口和新服务 readiness。所有候选企业仍保持隔离。
5. 完成现有 `tenant-migrate` 的 `planned → stopped → backed_up → adopted → imported → routed`
   阶段及回执，再记录 `opening`，然后才发起平台启用。中断先查询状态，原任务原参数重试。
6. 90—120 分钟：三端冒烟，验证登录、续期、退出、通知、上传、消息和来电；记录完成回执。
   开启 24 小时观察并安排实际值守，不能在未上线时宣称观察完成。

90 分钟检查点前失败：隔离新环境，按已演练回退步骤恢复旧部署；先保证新环境未进入 `opening`。
一旦进入 `opening`，即使激活响应丢失，也禁止直接启用旧认证。先停写、保存新数据，再受控修复/恢复。
此时不能承诺仍在两小时内恢复。保留旧部署至少七天，删除另行确认。

## 续期、备份与后续验收

证书续期成功后，Linux 续期 hook 调用：

```sh
sh infra/scripts/reload-shared-edge-certificate.sh /absolute/private/edge-compose.json
```

先校验再强制重载，失败保留当前运行配置并由现有告警渠道报告。演练证书轮换且从外网核对新序列号后，
才能接管旧 `qingwa-cert-renew.timer` 的重载步骤。不得同时保留指向旧网关的 reload hook。

平台使用现有 `platform-backup` 调度/校验/恢复工具；企业使用平台的企业备份任务及现有 `tenant-backup`。
切换旧 `qingwa-backup.timer` 前，分别完成首次备份和恢复验证。企业恢复只能恢复企业库及指定 Redis DB，
不能回滚共享 PostgreSQL 数据目录或整个 Redis 卷。恢复后再验证平台库未变化。

工程及演练证据见实施记录第 42、43 阶段。真实快照演练和离机加密备份已完成；最终停写后的
一致性备份仍需在切换窗口生成。真实供应商、签名安装、Apple SDK 和切换后 24 小时观察
仍由对应环境提供，不能用模拟结果填 `passed`。
