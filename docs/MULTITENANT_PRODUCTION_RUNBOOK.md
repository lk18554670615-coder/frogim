# 独立多租户：生产配置与离线部署包

本文件描述代码提供的生产配置入口，不是现网发布批准。平台认证库备份、异地交付、
默认企业切换等必做项仍以 `MULTITENANT_REQUIRED_CLOSEOUT.md` 为准。
本轮只在 Docker Desktop 的隔离测试项目运行生产模式二进制，没有连接生产服务器。

## 运行模式不可混用

- 本机默认平台＋默认企业继续使用 `development/local_preview`，公网绑定始终关闭。
- 新生产平台必须为 `PLATFORM_ENV=production`、`PLATFORM_DEPLOYMENT_MODE=dedicated_host`。
- 生产企业必须为 `IM_ENV=production`、`IM_TENANCY_PREVIEW=false`、
  `IM_TENANT_DEPLOYMENT_MODE=dedicated_host`。
- 生产主机代理必须在 Linux 运行，使用 `AGENT_ENV=production`、
  `AGENT_ISOLATION_MODE=dedicated_host`、`AGENT_HOST_ID_FILE=/etc/machine-id`。
  代理控制监听绑定具体私网 IP，不得为 `0.0.0.0` 或 `[::]`。
- 专用主机代理只能加载 `dedicated_host` 发布目录，预览代理不能加载生产包；
  回退目标也不能跨运行模式、企业、服务器或数据库版本。

不能仅修改 ENV 字段把旧单企业部署变成新架构。生产校验还包括 mTLS 身份和有效期、
数据库地址、独立密钥、精确 CORS、Web/媒体/IM/通话地址、供应商归属和端口边界。
数据库首次绑定企业后会拒绝其他企业配置；已有业务卷的接管属于受控迁移流程。

## 同机默认企业的数据层例外

平台和同机 `default` 企业支持共享一个 PG 实例（分库）及一个 Redis 实例（DB0/DB1）。
其他企业继续按独立实例部署。共享数据项目独立于企业维护生命周期，使用按库备份恢复。
配置、迁移边界和操作步骤见 [共享数据部署说明](MULTITENANT_SHARED_HOST.md)。
下表的独立数据实例是未启用共享模式的默认布局。

## 拓扑和端口

| 主机 | 服务 | 允许的宿主机入口 |
|---|---|---|
| 平台 | gateway、platform-api、独立 PostgreSQL/Redis | gateway 的 HTTPS；私网 mTLS 控制端口 |
| 每个企业 | gateway、业务 API、独立 PostgreSQL/Redis/MinIO、WuKongIM、LiveKit、两项初始化服务 | HTTPS 业务、HTTPS 媒体、IM TCP、RTC TCP/UDP；私网业务控制端口 |
| 每个企业 | 原生部署代理 | 私网独立 mTLS 代理端口 |

数据库、Redis、MinIO 控制台、IM 管理接口、原始 LiveKit/Twirp、健康检查端口不公开。
平台网关只分发认证及管理 API 和统一静态入口，不代理企业业务请求。

企业第一版生产包的业务与媒体使用不同宿主机端口，例如业务 `443`、媒体 `9443`；
证书必须覆盖两者的域名。不能把两个发布映射写成同一端口。IM 和 RTC 另行配置，
RTC 地址不能继续使用回环候选。真实云 NAT/防火墙、私网 DNS 和证书信任链需要外部验收。

## 1. 准备固定镜像（PowerShell 7）

使用项目锁定的 FVM Flutter；不要修改版本号。以下命令只生成本地镜像，不推送或部署：

```powershell
./infra/scripts/build-platform-bundle.ps1 `
  -PlatformUrl 'https://app.example.com' `
  -TermsUrl 'https://legal.example.com/terms' `
  -PrivacyUrl 'https://legal.example.com/privacy'

./infra/scripts/build-enterprise-bundle.ps1
./infra/scripts/build-tenant-agent.ps1
```

`example.com` 是说明占位，执行前须换成已准备的真实地址。平台 Web 使用 production、
`PLATFORM_AUTH_URL`、`/app/` 和本地 CanvasKit 资源构建。镜像内记录编译时的平台地址；
网关启动会拒绝服务于其他平台地址的客户端镜像。法律页面由运营方提供，工具不创建虚假条款。

企业构建要求已准备固定版本的补丁 IM 镜像和离线 IP 库；不会拷贝现网数据。
镜像使用 `image@sha256:...`，拒绝 `latest`/普通标签。`local` 仅是本机构建时的暂存标签，
生产配置必须填脚本返回的不可变摘要。镜像配送、仓库访问权与真实主机预装属于外部步骤。

## 2. 准备私有配置

配置和渲染后的 Compose 文件包含密钥，仅保存在运维私有目录：Linux 目录 `0700`、
文件 `0600`；Windows 使用仅本人和 SYSTEM 可读写的 ACL。不要粘贴到聊天、Git、工单或日志。
CA 私钥不进入镜像或运行包。公网站点证书与控制身份使用不同密钥。
公网 origin 使用小写域名、无末尾斜杠，HTTPS 默认 443 端口省略，避免浏览器 Origin
与 CORS 配置字面值不一致；非默认端口必须明确填写。

平台 `PlatformConfig` 必需字段：

- `toolsImage`、`publicUrl`、`controlUrl`、`publicBindIp`、`controlBindIp`。
- `databaseSecret`、`redisSecret`、`gatewaySecret`：分别使用独立随机 32 字节的
  base64url（无 padding），不能相等。
- `adminUsername`、`adminPasswordHash`：合法 ID 和 cost≥12 的 bcrypt 哈希。
- `publicTls` / `controlTls`：各包含 `ca`、`certificate`、`privateKey` PEM。
  控制证书含平台 SPIFFE 身份；有效期、链、服务端/客户端用途和域名均须有效。
- `peers`：`tenantId`、`serverId`、企业私网 `controlUrl`、代理私网 `agentUrl`。
  同一企业/服务器/控制地址不允许重复。客户端或管理 HTTP 请求不能写入任意控制 URL。
- `catalog`：经过验证的企业 `Release` 数组，每项必须同时匹配 peer 中的企业、服务器及
  `dedicated_host`。首次部署平台时可以为空，此时不启用企业部署目录。
- 可选 `suppliers` 只允许既有短信/Getui/APNs/Web Push 变量；不可借此覆盖身份、数据库、
  路径或开发模式。APNs 密钥使用独立 `apnsPrivateKey` 字段，运行时只写入私有 tmpfs。

企业沿用 `EnterpriseConfig`，新增必需生产对象：

```json
{
  "production": {
    "apiOrigin": "https://tenant-a.example.com",
    "mediaOrigin": "https://media-a.example.com:9443",
    "controlUrl": "https://tenant-a.control.example.com:8444",
    "publicBindIp": "0.0.0.0",
    "controlBindIp": "10.40.0.12",
    "imHost": "tenant-a.example.com",
    "rtcNodeIp": "203.0.113.12"
  }
}
```

示例中的文档 IP/域名不能用于真实上线。该对象只是完整配置的一部分；`ports` 必须与
这些地址一致，`platformControlUrl` 为平台私网控制地址，`platformWebOrigin` 为统一入口。
`secrets` 中生产包额外要求独立 `gateway` 密钥，其余数据库、缓存、媒体、JWT、媒体签名、
IM 和 LiveKit 密钥继续各自独立。`NewEnterpriseSecrets` 可生成满足格式要求的新企业密钥；
不要为既有企业每次发布重新生成密钥。历史固定媒体签名兼容和旧卷接管属于 R3，不能套用新企业包。

`production` 缺失仍严格表示本机预览，不会根据 URL 猜测生产模式。

## 3. 离线渲染

在仓库根目录的 PowerShell 7 执行，路径指向已经存在的运维私有目录：

```powershell
go -C server run ./cmd/platform-bundle `
  -config 'D:\private-deploy\platform.json' `
  -output 'D:\private-deploy\platform-releases' -release 'platform-r1'

go -C server run ./cmd/tenant-bundle `
  -config 'D:\private-deploy\tenant-a.json' `
  -output 'D:\private-deploy\tenant-a-releases' -release 'tenant-a-r1' -sequence 1
```

工具不调用 Docker、不部署、不迁移账号、不联系供应商。每个目录含私有 `compose.json`
和无密钥 `release.json`。重复生成相同内容幂等；同 ID 内容变化、部分写入或不匹配摘要时
拒绝覆盖。平台 Compose 已转义 `$`，不可再次手工替换密码；企业包由受控代理加载时处理
Compose 转义，不应绕过代理直接当作普通 Compose 执行。

平台作为独立引导设施，由运维在指定 Linux 主机按已核对摘要的文件启动固定项目
`frogim-platform`；必须使用 `--no-build --pull never`，不使用临时标签、不读取未知 `.env`。
首次启动前核对现有同名网络/卷为空或确属本平台，禁止覆盖其他业务卷。禁止复制默认企业数据
作为平台或新企业模板。平台升级/恢复仍须按维护与备份流程执行，本渲染器不提供数据库降级。

企业由独立代理加载同一 release 目录和目录摘要，平台后台只能提交已知发布 ID。
代理先校验自身证书再打开执行日志；未配置 executor 时仍只是检查代理，不具有执行能力。
服务端/代理返回健康不等于物理隔离、公网可达、真机可用或供应商配置已通过。

## 4. 默认关闭和来源 IP

平台默认未配置短信时 `registrationEnabled/otpLoginEnabled/passwordResetEnabled=false`，供应商失败不会回退到固定验证码。
若运维明确选择原开发验证码行为，可在私有平台配置设置 `fixedVerificationCode: "123456"`，
渲染为 `PLATFORM_FIXED_VERIFICATION_CODE=123456`。它同时启用注册、验证码登录和密码找回，
不发送真实短信，不验证手机归属；不能与登录或找回密码的短信 webhook 配置混用。
错误验证码、限流、封禁、凭据撤权、找回挑战有效期及企业隔离仍生效；未接入平台的自助换绑和注销不因此开放。
移除此字段并配置真实短信适配器即可切回手机收码，不应开启整个生产服务的开发模式。
`suppliers` 缺失时所有真实推送关闭。配置不全或 APNs 生产模式误用 sandbox 会拒绝生成。
企业只使用中央推送，不能持有共享 App 供应商密钥。

生产网关重写来源 IP 和独立网关凭据；后端仅在凭据匹配时信任单个 `X-Real-IP`。
客户端 `Forwarded/X-Forwarded-For` 不能伪造限流身份。企业生产配置禁止旧的全信任代理开关。
后端普通 HTTP 端口仍须不公开；网关凭据不是替代网络隔离的登录凭据。

## 5. 本机自动化验证

生产模式测试仍将**测试副本**的宿主机端口改为 `127.0.0.1`，只创建随机命名项目、
合成账号和新卷，结束时验证归属后移除。不会把本机代理改成专用主机代理。
先用 `https://app.example.test` 编译测试平台镜像，再执行：

```powershell
./infra/scripts/test-tenancy.ps1 -StartDependencies -ProductionProfilesDocker
```

测试覆盖平台四服务、企业九服务真实启动，校验 TLS、后台登录、关闭短信入口、私有路由、
匿名业务拒绝、CSP 和默认栈未受影响。其余单元/集成测试覆盖绑定、重放、配置和任务故障。
公共域名、系统 CA 信任、真实独立主机、真实供应商、四端设备和默认企业真实迁移仍须另验。

## 6. 平台认证库备份

平台 schema 17 增加独立目录身份及备份审计。离线运维工具、加密归档和隔离恢复说明见
[平台备份说明](PLATFORM_DIRECTORY_BACKUP.md)。已实现真实 PostgreSQL 快照导出与新库恢复，
恢复库保持隔离，不能直接启动认证或旧任务。[异地传输工具](OFFSITE_BACKUP.md)已支持
平台／企业密文交付和严格下载。企业代理已接入自动异地交付与平台收据，生产启用企业备份
必须在代理私有 `backup.offsite` 中配置独立目标；本地备份成功与异地交付成功分开核验。
schema 18 已补平台自身日调度／自动交付，以及独立的 `compose.platform-backup.yml` 维护
服务模板和按 UTC 日期找回加密收据；默认不启用。配置、确认及失败处理见平台备份说明。
恢复后的企业实时身份清单和持久校对报告已实现，操作说明见平台备份文档。
报告不授权恢复登录：差异处理、旧认证源隔离及受控激活仍在 R2 必做清单，不能手工移除
隔离标记或将当前阶段当成完整灾备上线验收。

## 7. 无法确认的通话握手

握手确认丢失会阻止撤权完成，不能靠等待若干秒后自动删除阻断记录。
已提供仅运维可执行的冷修复工具：必须先通过平台关闭企业访问，核对并停止原写入者及
LiveKit，再使用原部署 journal、明确停用操作 ID、理由和确认执行固定事务。
它不自动启动服务、不开放访问、不替代后续正常撤权检查。完整前置条件、命令、重试和
维护后恢复步骤见 [通话握手冷修复](TENANT_MEDIA_RECOVERY.md)。真实生产维护须另外批准。


### 原地址单一 443 发布准备（阶段 42）

同机默认企业的 `shared_edge` 代码、平台 `/platform` 客户端路径、可配置媒体桶和发布证据门槛
已实现；[现有服务器发布手册](EXISTING_SERVER_RELEASE.md) 给出构建、切换、回退及续期步骤。
工程回归见实施记录阶段 42。Apple SDK/签名安装、供应商真机实收、真实迁移/回退/备份演练
仍为 **待环境验证**；现网迁移、版本策略、最终发布冻结及 24 小时观察尚未执行，不能关闭 R7。
