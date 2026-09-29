# 独立租户本地验证环境

这里包含持久化本机环境、可丢弃集成测试环境及阶段 33 新增的离线生产配置/镜像工具。
实际运行仍只有本机合成数据，没有现网部署。早期阶段中“仅预览”的说明适用于对应预览工具；
当前生产入口、端口/身份边界和部署包规则见
[生产运行手册](../../docs/MULTITENANT_PRODUCTION_RUNBOOK.md)。

## 持久化本机环境：平台 + 默认企业

在项目根目录用 PowerShell 7 执行：

```powershell
./infra/scripts/start-tenancy-local.ps1
# 已构建且没有改代码时，只重新检查/启动：
./infra/scripts/start-tenancy-local.ps1 -SkipBuild
```

- 平台后台：`http://127.0.0.1:18900/`，平台账号 `operator`。
- 平台 HTTPS 认证入口：`https://127.0.0.1:18443`。
- 统一客户端 Web：`https://127.0.0.1:18443/app/`，仅本机预览；浏览器需要明确配置信任。
- 默认企业后台 / 业务 API：`https://127.0.0.1:18444/`，企业后台账号 `enterprise-admin`。
- 独立媒体入口：`https://127.0.0.1:18445`；IM TCP：`127.0.0.1:15180`。
- 密码均为首次启动生成的独立随机值，仅保存在 `.data/tenancy-local/credentials.json`。
  Windows 下该目录 ACL 只允许当前用户及 SYSTEM；不要提交、发送或粘贴其内容。
- `19900000001` 为本机验收账号，使用真实平台账号预留和企业身份开通任务创建，
  不是固定验证码账户；密码见同一私密文件。重复启动不会改密码或重建身份。
- 新生产认证仍禁止固定验证码。未配置真实 OTP 时，验证码登录与公开注册关闭。
- 本机推送明确关闭；中央推送已有代码和隔离测试，真实供应商实收尚未验收。

结构：平台与同机默认企业共享一个 PostgreSQL（`platform` / `enterprise`）和一个 Redis
（DB0 / DB1）。数据实例属于独立 `frogim-shared-default` 项目；两端 API 接入其内部网络，
Redis 按编号分组，不增设 ACL 用户或键前缀。企业 IM/MinIO/LiveKit 和业务卷保持原布局。
本机启动会备份并迁移旧测试数据，保留旧容器/卷；具体恢复与停止步骤见
[同机共享数据部署说明](../../docs/MULTITENANT_SHARED_HOST.md)。
两端 API 额外接入企业控制网络；控制通信验证独立 CA、证书链、主机名及 URI 身份。
平台 API 与只读默认企业检查代理另接入独立 agent-control 内部网络，使用另一套独立 CA。
代理没有主机公开端口、Docker socket、特权模式或 Shell API，只能进行认证后的身份检查。
数据库、缓存、IM 管理端口和 mTLS 控制端口不映射至主机；对外映射只绑定回环地址。
这是同一台开发机上的独立容器，**不是独立云服务器的物理隔离验收**。

脚本构建本地二进制、两个管理后台和平台认证模式 Flutter Web，不构建或发布 APK/IPA。应用镜像来自工作区，
依赖镜像固定摘要。WuKongIM 通过 `Dockerfile.im` 的固定提交、源码包 SHA256 和两份补丁摘要
构建为本机专用 `frogim/tenancy-im:workspace`；内置策略插件仍使用现有固定镜像，缺失时停止。
构建上下文分别只包含编译产物或指定补丁/测试，不包含本机私钥或业务数据。
此 IM 构建修复强制踢线先删除连接索引导致未真正关闭 socket，以及认证失败遗留连接的问题，
构建时执行对应回归测试。原生产 IM 镜像未修改，生产必须另行生成并验证包含修复的固定产物。

首次运行创建控制通道与浏览器入口 CA，并创建独立代理 CA，三者互不复用。
已有本机目录只增补 `.data/tenancy-local/ops/`，不替换原有密钥或账号。脚本**不修改 Windows 信任库**。
命令行验收显式加载 `.data/tenancy-local/browser-ca.pem` 验证 TLS，不关闭证书校验。
浏览器访问 HTTPS 入口前，需要使用者明确批准信任这张本机证书；不得绕过安全警告。
证书有效期为 30 天，过期不会被自动替换。本机公开证书使用 ECDSA P-256，
避免原 Ed25519 链与当前 Flutter TLS 握手不兼容。只换发公开证书的命令如下：

```powershell
# 从项目根目录执行；旧证书先完整备份到私密配置目录，不改控制证书和账号。
Push-Location server
go run ./cmd/tenancy-local -root ../.data/tenancy-local -renew-public-tls
Pop-Location
docker compose -f infra/tenancy/compose.local.yml -f infra/tenancy/compose.shared.local.yml restart platform-gateway enterprise-gateway
```

该工具重新生成本机公开 CA，既有浏览器信任不会自动延续；重新信任必须单独明确批准。
控制证书/生产证书自动轮换与失败回滚仍未实现，不要把该工具用于生产部署。

启动后通过已认证控制接口检查企业 ID、业务地址、数据库版本/绑定、数据库与缓存、
IM、媒体桶及 LiveKit 管理接口，再通过有审计的领域操作激活默认企业。
这是运行依赖验收，不是硬件、网络防火墙或真实部署验收。平台生产启动保护仍保留。

平台后台“服务器资源”可登记或重新校验运维预配置的代理。只检查并绑定，不执行部署。
本机代理只有 `default-local`，绑定 `default`，必须显示“本机预览（非物理隔离）”。
显式验证以下命令会新增/更新这条本机记录及审计；普通启动验收不自动登记服务器：

```powershell
Push-Location server
go run ./cmd/tenancy-local -root ../.data/tenancy-local -verify-agent
Pop-Location
```

该检查复用原请求验证幂等、拒绝未登录读取以及控制路径不公开；不是部署/备份/物理隔离验收。
代理登记与检查成功也不会自动激活新企业。平台部署任务、固定摘要目录和真实本机代理链路已有
实现；默认代理仍不启用执行权限，生产服务器配送和物理隔离须另行验收。

公开链路验收覆盖平台密码登录、60 秒票据交换、企业直连资料查询、票据重放拒绝、
平台/企业令牌不能互用、控制路径不外露，以及平台退出后刷新凭据失效。
没有把“签发了 IM 凭据”当作真实设备已连接或聊天、通话、推送全流程通过。
Flutter 登录页已连接新架构，可使用本机测试账号密码登录；未配置真实验证码时不显示
验证码登录和注册入口。中央推送路由已有代码但供应商未启用，原生后台来电仍待收尾；扫码登录
不在本轮必做范围，旧入口保持关闭。平台版本管理与公共更新查询、短信找回已接线，
但本机未配置真实短信服务，不显示找回入口，不能使用旧企业验证码作为替代。

不改变系统信任的本机验证（项目根目录，PowerShell 7）：

```powershell
$env:NODE_EXTRA_CA_CERTS = (Resolve-Path .data/tenancy-local/browser-ca.pem).Path
node apps/mobile/tool/verify_tenant_local_web.cjs
node --test apps/mobile/tool/verify_wukong_session_scope.cjs
node apps/mobile/tool/verify_wukong_web_sdk.cjs
# Flutter 认证集成测试（不使用真实 IM transport）
$env:TENANCY_LOCAL_TEST_ROOT = (Resolve-Path .data/tenancy-local).Path
Push-Location apps/mobile
$env:PUB_HOSTED_URL = 'https://pub.dev'
fvm flutter test --no-pub test/tenant_local_integration_test.dart
Pop-Location
```

Web probe 严格限定本机默认企业、验证 TLS、隐藏 SDK 的原始日志，检查字体缓存和实际
WSS CONNACK/会话重建重连；会短暂登录本机测试账号的 Web IM 会话，不要与该测试账号
的人工浏览器测试同时运行。未声称聊天收发、通话、浏览器 UI 或真机已经完整验收。

停止但保留数据：

```powershell
docker compose -f infra/tenancy/compose.local.yml stop
```

不要使用 `down -v` 或清理 `.data/tenancy-local`，否则本机数据或密钥会丢失。
首次启动失败时也不覆盖已有配置；应检查错误后重试，避免丢失已创建身份。

企业后台“新增用户”已接入平台：单个和批量均需要平台完成开户任务后才能登录，
页面下方可查询本企业最近 100 笔任务；网络失败请使用原请求号重试，勿修改成新请求。
已受理任务不再保留初始密码；未确认结果的行只在当前页面内存暂存用于重试。

可选本机实际开户验证（会保留一个固定测试账号 `19900000002`，重复执行不新增账号）：

```powershell
Push-Location server
go run ./cmd/tenancy-local -root ../.data/tenancy-local -verify-admin-create
Pop-Location
```

该验证只连接硬编码回环 HTTPS 网关，使用本机私有配置中的随机密码，验证平台任务完成、
重试幂等及统一密码登录；不输出密码或 Token。它不修改现网，且不是浏览器 UI/真机验收。

企业后台“用户详情 → 账号资料 → 平台登录密码”支持统一密码重置，需要 `users.write`、
理由与二次确认；受理后显示处理中，必须等任务确认完成。超时不要更换请求号重复提交。
该功能仅针对平台普通用户；后台管理员自身密码仍通过原独立管理权限域修改。

可选本机密码任务验证（仅将上述固定测试账号重置到原随机密码，不改变配置文件中的密码）：

```powershell
Push-Location server
go run ./cmd/tenancy-local -root ../.data/tenancy-local -verify-password-reset
Pop-Location
```

该验证有固定幂等请求号，可重复运行。不同新旧密码、旧票据/Token 拒绝、撤权响应丢失的
用例在独立集成测试库中执行，不拿本机日常测试账号反复改密码。
Flutter 改密、短信找回界面及任务恢复已接线，不开放旧企业找回接口。
客户端“账号安全 → 修改登录密码”验证当前密码；受理或结果不确定时退出当前会话，
在登录页手动查询密码进度，不保存密码或自动重复提交。

可选 Flutter 真实本机改密恢复测试（先运行上面的固定测试账号创建；与其他登录探针串行执行）：

```powershell
Push-Location apps/mobile
$env:PUB_HOSTED_URL = 'https://pub.dev'
$env:TENANCY_LOCAL_TEST_ROOT = (Resolve-Path -LiteralPath '../../.data/tenancy-local').Path
$env:TENANCY_LOCAL_PASSWORD_TEST = '1'
try {
    fvm flutter test --no-pub test/tenant_local_integration_test.dart
} finally {
    Remove-Item Env:TENANCY_LOCAL_TEST_ROOT, Env:TENANCY_LOCAL_PASSWORD_TEST
    Pop-Location
}
```

仅对本机专用账号 `19900000002` 提交改密任务，新旧密码都是私有配置中的原随机密码，
不修改配置、不输出凭据。故意丢弃受理响应，重启客户端仓库后查询原任务并重新登录，
验证本地身份不变。这会递增该测试账号认证版本并撤销其原会话。
IM 在 Flutter 测试中使用 fake gateway；真实 WSS 用单独 JS SDK 探针验证。
不要同时运行这些探针：同一个账号的同类设备登录会替换旧会话，引起预期的 401。

## 平台短信找回密码

平台 schema 5 增加独立验证码挑战，企业仍使用迁移 74 的撤权任务，不新增企业表。
本机不配置短信服务，不发送实际短信；真实发送服务接入与手机收码必须另行验收。
开启需要平台 API 的私有环境配置（不可放入客户端或提交仓库）：

- `PLATFORM_PASSWORD_RESET_SMS_URL`：明确的 HTTPS 发送接口，可带路径；不能带 URL 用户名、
  密码、查询参数或 fragment。证书必须受服务器信任，不能关闭 TLS 校验。
- `PLATFORM_PASSWORD_RESET_SMS_TOKEN`：至少 32 字符的独立服务凭据，不复用企业认证密钥。
- 发送器使用 POST JSON：`phone`、`code`、`purpose=password_reset`、`requestId`；
  Authorization 为服务 Bearer，Idempotency-Key 为 requestId。只有 2xx 表示发送受理，
  10 秒超时，不跟随重定向。适配器不得记录完整请求体、验证码或认证头。
- 此接口只负责发送平台产生的随机验证码，不是验证码校验器；不复用登录 OTP webhook。

验证码为密码学随机 6 位数字，10 分钟有效，同挑战错误尝试最多 5 次；数据库仅保留
能力凭据哈希和用途绑定的验证码 HMAC，不保留明文验证码。每手机号 10 分钟最多 3 次，
连接来源 IP 10 分钟最多 10 次，另受公共 API 限流；Redis 不可用拒绝发送。
当前不信任任意转发头，因此位于网关后时 IP 预算按网关连接来源共享，偏向收紧；
生产部署还需结合固定可信代理配置完成真实来源识别和限流验收，不能直接放宽限制上线。

未注册、封禁、待开通账号的发送响应一致，不披露手机号归属；只有有效账号通过验证才可
创建重置任务。重置期间暂停登录、撤销企业 API/IM 凭据，确认后启用新密码；不改变本地身份。
提交和审计同事务，重复提交保持同一任务；超时仅表示结果未确认，不自动重发密码。
客户端二次确认后先加密保存查询凭据，登录页可在 24 小时内手动查询，不能用查询凭据登录。
SMS 验证码和新密码只在当前页面内存中，刷新前尚未提交的挑战需重新获取。

公共接口：`POST /v2/auth/password-reset/code`、`POST /v2/auth/password-reset`、
`POST /v2/auth/password-reset/status`。后两者使用用途限定的能力凭据，不是企业业务 Token。
未配置真实服务时 `passwordResetEnabled=false`；短信重置入口拒绝调用，但已受理任务仍可查询。

## 可丢弃的集成测试

PowerShell 7 执行：

```powershell
./infra/scripts/test-tenancy.ps1 -StartDependencies
```

依赖 Docker Desktop Linux 引擎。`-StartDependencies` 先使用 `Dockerfile.im` 构建本机专用
WuKongIM 镜像并执行补丁回归；基于原 `infra/wukongim/server-patch/` 的固定源码/补丁，
额外应用 `wukong-im/close-connection.patch`。PostgreSQL、Redis 和 LiveKit 固定镜像摘要。

测试使用三个独立 PostgreSQL 实例、两个独立 IM 实例、两个独立 LiveKit、一个平台 Redis；
数据库和 IM 数据为容器临时内存卷。三个网络分开，测试端口只绑定主机回环地址。
Windows Docker Desktop 的 `internal` 网络不提供所需主机端口映射，因此本地验证使用
三个普通 bridge 网络，**不将它当作生产防火墙或独立虚拟机隔离验收**。

测试启动三个 HTTPS 业务/平台监听器及三个 mTLS 控制监听器，全部使用临时本地证书。
证书校验和 URI SAN 校验均启用；没有 `InsecureSkipVerify`、固定短信验证码或现网依赖。
测试夹具独立建立测试企业；正式接口的“检查并激活”必须使用服务端预配置的
控制通道，不接受前端上传的健康报告，也不能覆盖并发变化的企业配置。

覆盖：数据库重复迁移、并发注册唯一、票据单次消费、一次性注册完成、权限域隔离、
邀请码启停与审计、任务受控重试、不同企业票据/JWT拒绝、实际 IM 凭据签发/替换、
调换后的源 API 撤权及目标新身份。真实 LiveKit WSS 验证调换/改密踢线、旧 Token
重连拒绝、其他参会者保持连接及踢线故障时不提前激活目标；未发送音视频轨道。
企业停用用例同时连接两企业的 TCP 和 WSS，验证目标企业全部断开、另一企业继续响应心跳，
旧凭据重连被拒绝、恢复后新凭据可连接。WSS 由临时受信 TLS 测试网关代理真实 IM 二进制连接；
默认本机 Caddy 与固定 JS SDK 的 WSS 路径由另一个探针验证。
客户端收到 DISCONNECT 不主动退出，必须观察到服务端实际关闭连接；独立 transport 测试
也覆盖 TCP/WSS。单账号调换/改密/封禁活跃 IM 的完整矩阵、网络分区、锁连接失效、IM 多节点、
中央推送和真实四端缓存隔离仍未验收，不能用这些本机结果替代生产验收。

托管模式下 `/livekit/*` 由企业 API 的身份校验入口接管，只转发允许的 RTC 路径，
不公开 Twirp，原始 LiveKit 信令监听器不映射到主机公网。媒体 UDP/TCP 仍直达企业 LiveKit。
通话 JWT 的不可自行修改元数据绑定企业、本地用户、归属代次及认证版本；自托管 LiveKit
踢线本身不会作废已签发 JWT，因此每次重连仍需实时校验。握手与撤权共享数据库锁。
企业迁移 75 保存未确认握手：网络/持久化失败时阻止后续通话握手及撤权完成，平台继续
显示处理中，而不是假报成功。记录不自动过期；当前还没有完成可审计的受控恢复工具，
禁止直接删除记录或通过切换 `LiveKitEnabled` 绕过检查。这仍是生产启用阻断项。

脚本不会自动删除容器。确认只用于这些测试后，可以停止并删除本次测试栈：

```powershell
docker compose -f infra/tenancy/compose.test.yml --profile im down
```

只清理 `frogim-tenancy-test` 项目；临时测试数据不可恢复，不涉及其他 Docker 项目。

## 平台后台

账号归属页提供全局封禁/解封，需独立平台运营管理员、操作理由和二次确认。
受理后暂停平台登录，企业完成业务凭据、IM 和 LiveKit 撤权后才显示任务完成；
若已有开户、调换或改密任务，先等待该任务安全结束，再对最终身份执行。
“封禁任务”页可按账号/请求/任务 ID、企业和阶段查询。网络结果不确定时原请求号重试，
不要把关闭确认窗口理解为取消。解封不会恢复旧会话，也不清除企业自己的封禁或聊天历史。
企业目录提供停用/恢复操作及“企业启停任务”页，仍需理由、确认和观察到的访问版本。
停用先暂停平台认证，再由企业冻结访问并分批撤销会话、IM 与 LiveKit 连接，全部确认后完成；
失败重试不换操作号。恢复重新检查运行依赖并换访问代次，不恢复旧会话、不解除个人封禁。
停用不删除企业数据，也不追溯撤销已分享的固定媒体链接；企业后台/控制入口保留用于运维。
“客户端版本”页管理 Android/iOS/Web/macOS 的更新策略和发布历史，初始全部停用。
需要独立运营管理员、理由、二次确认及当前策略版本；并发冲突不覆盖，网络失败复用原请求号。
页面登记长期 HTTPS 分发地址，不负责打包、签名、上传或验证安装包；不允许带凭据、查询参数、片段的地址。
公共查询位于平台 `/v2/config/version`，客户端无需登录即可检查；企业后台的旧管理入口明确拒绝，
不能在某一个企业发布全平台更新。低于最低版本时强制更新优先于灰度；停用仅停止更新提示。
本机网关验证只读取四种平台策略，并验证企业的版本管理接口拒绝，不在默认栈发布测试策略。
“平台管理员”页管理独立平台账号：账号/状态搜索、分页、新增、启停、角色与密码。
运营管理员可管理其他账号，不能变更本人的启停或角色；只读账号可查看列表和修改自己的密码。
本人改密必须校验当前密码。所有变更要求理由、确认和已观察的版本；结果不明时复用请求号，
也可以只查询原操作结果，不再次传送密码。新密码至少 12 字符、最多 72 个 UTF-8 字节，
页面不写入浏览器持久缓存；服务端只保存 bcrypt 哈希，不把密码/哈希写入审计或查询响应。
密码、启停或角色发生变化时使全部旧会话失效，重新启用不会恢复它们。管理员停用不取消之前
已受理的跨企业持久任务。默认本机验收仅读取管理员列表，不改初始 operator 的密码或角色。
当前平台 schema 13、企业迁移 79；这不解除生产启动保护，也不代表实际音视频轨道或真机验收。

## 统一推送平台、企业队列与客户端登记

平台设备凭据使用独立 AES-256-GCM 密钥加密；供应商令牌不发给企业。
注册使用平台会话，刷新时原子更新绑定，退出时清除可解密令牌；换账号、归属/认证/企业访问
代次变化后旧通知不能选中新绑定。平台接收的通知只有白名单路由字段，不收聊天正文。
企业队列转发、Flutter 个推和 Web Push 登记已接入；原生后台来电尚未完成，实际供应商尚未验收，因此本机继续明确关闭，
不复制现网推送密钥。Flutter 仅根据平台公开配置启用已支持供应商，关闭/故障不回退企业登记接口。
平台登记和续期串行使用当前凭据，改密等待在途轮换；退出阻断本机迟到回调并撤销平台会话下全部绑定。
通知包含归属/认证/企业访问代次与有效期，客户端对缺少、错误、已过期上下文拒绝导航和来电处理。
托管 iOS 的 APNs VoIP 登记继续关闭，直至终止进程下的原生 CallKit 路径完成身份校验；macOS 不使用个推。
未知网络结果下的撤销仍为尽力处理，不能撤回已被供应商接受或系统展示的通知；不声称手机已收到。

企业可配置 `IM_PUSH_PROVIDER=platform`，复用独立 mTLS 平台控制地址/证书；企业不能设置个推、
APNs、Webhook 或 Web Push 的共享凭据。默认本机仍为 `noop`，平台供应商仍为 disabled。
托管企业旧 `POST /v2/users/me/devices` 明确拒绝，不转交企业 Token、不代替客户端登记到平台；
原单企业模式保持现有行为，旧绑定删除和本机登录设备资料不受影响。
企业迁移 78 自动快照新入队通知，历史空快照不会用当前归属补写；其后被安全跳过，不补发。
普通通知到期为入队后 23 小时 59 分钟，来电最多 45 秒且不晚于原邀请期限；预留服务时钟微差，
不在重试时延长。未知/系统/删除通知不产生展示推送；正文、验证消息及设备令牌不进入控制请求。
投递前检查当前身份、会话成员、免打扰、历史边界、撤回/删除/过期以及有效邀请；索引未就绪重试。
平台 429/503、网络异常和不完整确认保留安全错误并退避；明确的身份或请求拒绝停止重试。
仍沿用企业 Outbox 最多 10 次尝试；失效/不再可见的通知完成处理不表示供应商收到或设备展示。

平台可执行文件的专用配置（仅完成后续链路及隔离验收后使用，不属于当前开通步骤）：

- `PLATFORM_PUSH_PROVIDER`：默认 `disabled`；移动端仅支持 `getui`。
- `PLATFORM_PUSH_ENCRYPTION_KEY`：独立随机 32 字节密钥的无填充 Base64URL；不得复用 JWT、
  媒体签名或控制证书密钥。未提供或长度不符时拒绝启用，不生成临时密钥替代已有密钥。
- 个推：`PLATFORM_GETUI_APP_ID`、`PLATFORM_GETUI_APP_KEY`、`PLATFORM_GETUI_MASTER_SECRET`。
- 个推 VoIP：供应商侧配置完成后显式设置 `PLATFORM_GETUI_VOIP_ENABLED=true`，默认关闭。
  普通通知与 VoIP 使用独立能力/绑定，平台只保存加密 CID。旧 `PLATFORM_APNS_VOIP_*`
  直连配置和 `apnsPrivateKey` 必须移除，否则配置验证失败；不自动回退直连。
- 浏览器：单独设置 `PLATFORM_WEB_PUSH_ENABLED=true`，提供
  `PLATFORM_WEB_PUSH_PUBLIC_KEY`、`PLATFORM_WEB_PUSH_PRIVATE_KEY`、`PLATFORM_WEB_PUSH_SUBJECT`、
  `PLATFORM_WEB_PUSH_ALLOWED_HOSTS`。VAPID 密钥必须配对，联系地址为 HTTPS 或 mailto，
  域名名单用逗号分隔的精确 DNS 名称，不接受通配、端口或 IP 字面量；应根据所支持浏览器的
  实际推送供应商配置，不复制订阅端点中的令牌。独立加密密钥仍必需，可以只启用 Web Push。
  平台只向 HTTPS 443 白名单域名解析出的公网 IP 发送加密通知，不使用环境代理或重定向。

托管 Web 使用独立 `tenant_push_worker.js`/`tenant-push-scope/`，不覆盖旧单企业推送脚本。
后台绑定保存非密钥身份、绑定版本和平台会话有效期；每次展示及点击均校验，不靠 URL 猜测归属。
关闭原窗口后通过五分钟、一次性的随机交接号打开 App，登录后再判断路由；错误账号不导航。
同一个浏览器端点只归属最后成功登记的会话，不能保证多个不同账号标签页分别后台收取。
通知权限、浏览器订阅轮换/关闭重启实收及跨标签页刷新协调仍需真实浏览器验收。
可在 `apps/mobile` 执行 `node --test tool/verify_tenant_push_worker.cjs` 运行隔离协议回归；
本机完整构建脚本也会执行，不会请求通知权限或向真实供应商发送消息。

供应商请求不跟随重定向，失败只返回安全错误。iOS 有同会话有效 VoIP 登记时，来电不重复
走个推普通通道；普通消息不发给 VoIP。平台投递成功只表示供应商受理，不表示设备已展示。
超时可能已被供应商接受，重试保留稳定 ID；不保证恰好一次，也不能撤回供应商已接受的通知。
当前推送测试使用捕获供应商，没有发送真实手机通知；密钥轮换、失效记录清理、数据库锁丢失
故障注入和完整客户端链路仍待完成，不能删除生产保护直接上线。

在 `apps/admin` 下执行 `npm run dev:platform`，访问
`http://127.0.0.1:4177/platform.html`。登录需要另行启动开发平台 API
（`127.0.0.1:8090`）以及独立平台管理员；不接受企业后台 token。
本地开发配置 `PLATFORM_WEB_ORIGIN=http://127.0.0.1:4177`；非回环地址仍必须使用 HTTPS。
`npm run build:platform` 输出到 `apps/admin/dist-platform`，不覆盖现有企业后台构建。
部署时需要将 `/platform/admin/*` 路由到平台服务，不要路由到企业 API，
也不要公开任何 `/internal/*` 控制接口。当前仍有生产启动保护。

## 历史账号导入工具（仅本机隔离演练）

`server/cmd/tenant-preflight` 只读校验源/目标库存，`server/cmd/tenant-import` 执行可恢复导入。
完整规则和验证边界见 `docs/MULTITENANT_IMPLEMENTATION.md` 的第二十阶段。
工具保留原用户 ID，将原 bcrypt 哈希放入平台认证账号；不迁移或复制业务历史。
现有本机默认企业已经是合成平台账号，不应为了演示对它重新导入。

- 不接受生产地址，不自动接管旧库、升级 schema、暂停/恢复企业、改路由或开放内部端口。
- 源企业 79 / 平台 15 必须已经确认同一停用操作，且无未完成部署或备份任务；预检指纹必须未变化。
- 两个数据库 URL 从 `TENANCY_IMPORT_SOURCE_DATABASE_URL`、`TENANCY_IMPORT_PLATFORM_DATABASE_URL`
  读取，只允许回环 IP；`TENANCY_IMPORT_ENV=development`。写入另需
  `TENANCY_IMPORT_OFFLINE_CUTOVER_CONFIRMED=true`，该声明不能替代备份及旧服务下线检查。
- `resume` 使用私密 `TENANCY_IMPORT_CONTROL_FILE`：tenantId、HTTPS 回环 baseUrl、绝对路径
  caFile/certFile/keyFile。平台及企业身份双向校验；不要将真实配置、密钥或 DSN 提交到 Git。
- 在 `server` 运行 `go run ./cmd/tenant-import -h` 查看参数。默认 status 只读，start 全批预留，
  resume 每次最多推进一个账号。退出码 0=完成、3=仍有工作、2=错误/未确认；失败后查询并使用
  原批次续跑，不另建批次。已提交步骤不会因后续失败自动回滚，未完成批次禁止恢复企业。
- 完成后仍需独立检查并显式恢复企业；实际旧部署接管、备份/网络切换及旧客户端兼容尚未完成。

构建脚本只构建工具，不自动导入账号。`test-tenancy.ps1` 使用可丢弃实例及随机 schema 验证；
实际默认栈不导入这些测试账号，也不会被测试脚本停用。

## 部署执行内核的隔离验证

`internal/deployment` 已包含固定发布目录、持久操作回执、显式重试和受限 Compose 执行器。
平台持久部署任务、维护互斥、独立代理 mTLS 控制和后台部署页已接线。现有本机代理仍只有
inspect，未提供执行配置或 Docker socket；平台发布目录默认为空，不能从后台部署。
不要为了试用直接给默认代理挂载 Docker socket；完整本机企业包已有隔离验证，真实主机开通、
备份恢复和受控接管仍未验收。
完成状态要求代理容器回执及企业数据库版本、依赖、停用状态核验，不只看容器健康。

部署内核的实际 Docker 回归需显式启用 `TENANCY_DEPLOYMENT_DOCKER_TEST=local` 后，在 `server`
执行 `go test ./internal/deployment -count=1 -v`。它使用本机已加载的固定摘要 Alpine 镜像，
创建两个随机 `frogim-deploy-fixture-*` 项目，不开放端口或宿主目录，不接触默认企业栈；
完成后仅清理自身容器、网络及合成数据卷。未启用时该集成用例跳过，其他内核测试仍运行。
完整测试脚本也支持 `-DeploymentDocker`，其余数据库/IM 测试仍需要原隔离依赖。

发布目录必须由运维准备并保护，包含发布序号、镜像/Compose 摘要及显式回滚关系。执行器不
自行构建/拉取镜像、不执行 Shell、不删除业务卷；回滚只允许已声明的相同数据库 schema。
私密状态目录必须与企业目标永久绑定，不得通过更换目录绕过未确认任务或部署历史。

## 持久部署控制（开发预览，显式启用）

- 平台 `PLATFORM_DEPLOYMENT_CATALOG_FILE` 为运维管理的绝对文件路径，内容为 Release 数组：
  `id / sequence / runtime / composeSha256 / schemaVersion / rollbackTo / tenantId / serverId`。
  企业包必须精确绑定企业及服务器；平台拒绝未绑定或其他目标的包，后台只展示匹配候选。
  序号在同一企业/服务器/runtime 内唯一，回滚不能跨目标。旧 ID 的元数据不得修改；
  同名发布摘要变更会被平台历史和代理 journal 拒绝。没有公开写入发布目录的接口。
- 代理仅在配置 `AGENT_EXECUTOR_FILE`（绝对私密路径）时启用部署路由。配置字段为
  `stateDirectory / bundleDirectory / dockerBinary / dockerEndpoint / catalog`；前三项为绝对路径，
  本地 Linux Docker 地址仅允许 `unix:///var/run/docker.sock`。状态目录必须预建并永久绑定目标。
  平台只选发布 ID，不传主机路径、命令、环境变量或 Compose 内容。代理配置不经后台上传。
- 包文件固定为 `bundleDirectory/<releaseId>/compose.json`，使用第二十一阶段定义的受限格式。
  固定镜像必须已在执行宿主机加载；首期不自动下载镜像、不采用当前默认企业数据卷作模板。
  发布元数据和发布包由受信运维预置；允许的容器入口命令仍属于受信代码，不是任意租户输入。
- 控制接口（仅平台 mTLS 身份，永不路由到公网）：
  `POST /internal/agent/deployment/status|submit|retry`。状态 nonce、目标绑定、不可变操作及代次
  均由平台核对；代理仅处理固定项目。默认 inspect-only 代理没有这些路由。
- 平台后台：`GET /platform/admin/deployment-releases`、`GET/POST /platform/admin/deployments`、
  `POST /platform/admin/deployments/{id}/retry`。写操作必须独立 operator 会话、理由和确认；
  注册服务器不等于允许自动部署。页面读取已登记企业、服务器及历史代次，不接受 Shell 或地址。
- 已有企业必须先停用并等待撤权确认；首次开通允许 provisioning。受理、维护锁与审计同事务，
  网络检查不持有数据库锁。再次确认会话、企业/服务器版本及代理代次后才持久受理任务。
  有部署任务时拒绝恢复、激活、修改服务器登记和历史导入；部署也拒绝与未完成历史导入并行。
- 容器执行未确认只允许对同一任务、同一目标、匹配尝试次数显式重试。网络错误可自动重查回执，
  不自动重跑未知执行或回滚。已确认代理接单后只查询原回执；回执丢失/日志重置时不重新建单。
  首次发送与平台记录 ACK 之间的主机磁盘永久丢失仍须人工恢复原 journal，不可新建空目录尝试。
- 部署完成不激活企业、不启用邀请码；操作者仍需单独执行现有激活/恢复流程。核验不证明
  独立物理服务器隔离，也不替代实际备份恢复和真机验收。

## 完整企业发布包（仅本机预览）

`build-enterprise-bundle.ps1` 从当前 Go 业务服务、容器入口和企业后台构建本机 tools 镜像，
复用已加载的固定补丁 IM 镜像；Caddy、LiveKit、插件与数据服务使用固定 SHA256 镜像。
它不部署、不推送镜像、不修改平台发布目录、不加载默认企业数据，不自动生成生产证书。
Dockerfile 使用严格构建上下文白名单，不带 `.data/tenancy-local` 私钥或账号配置。

生成包由九个独立容器组成：PostgreSQL、Redis、MinIO、媒体桶初始化、插件校验、业务 API、
WuKongIM、LiveKit、Caddy/企业后台。数据服务只连接私有网络，不映射主机端口；所有允许
映射的预览端口限定 `127.0.0.1`。数据库、缓存、媒体、IM、插件卷均为项目内命名卷，
不存在宿主目录、外部卷或 Docker socket。它们仍处在同一 Docker Desktop，非物理隔离证明。

发布生成工具：在 `server` 执行 `go run ./cmd/tenant-bundle -h`。输入是运维持有的私密
`EnterpriseConfig` JSON（结构见 `internal/deployment/enterprise.go`），不是 HTTP 请求：

- `tenantId / serverId / toolsImage / platformControlUrl / platformWebOrigin / ports`；默认仍为
  local-preview、linux/amd64、当前企业 schema 79，不接受可变镜像 tag。阶段 33 增加显式
  `production` 对象；其端口、证书、代理模式与配置限制见生产运行手册，不按 URL 自动放宽。
- `secrets` 包含独立的数据库、Redis、MinIO、JWT、媒体签名、IM 管理/Token/策略及 LiveKit
  随机 32 字节密钥（无填充 Base64URL）、bcrypt 成本至少 12 的后台密码哈希、插件信任公钥。
  `NewEnterpriseSecrets` 可产生全新企业材料，但构建发布时只读取既有私密配置；不自动重新
  生成或轮换密钥。绝不可把默认企业的材料或业务数据复制为新企业模板。
- `controlTls / publicTls` 各含 CA、公钥证书和对应服务私钥。控制证书必须属于目标企业，
  同时具备服务端/客户端用途，私钥匹配、证书在有效期且符合回环主机名；公开/控制私钥不得
  复用。企业包不包含平台私钥或 CA 签发私钥。签发与安全分发仍是独立运维步骤。
- 输出根目录须预先创建并保护；生成 `<releaseId>/compose.json` 和最后写入的 `release.json`。
  私密 Compose 用 POSIX 0600 或 Windows 当前用户+SYSTEM DACL 创建，绝不覆盖不同内容；
  完全相同的完整产物可重放，残缺产物不自动接管。回滚目标必须已有、摘要正确且同目标/schema。
- 只有 `release.json` 元数据进入平台发布目录数组；**不要把含环境凭据的 compose.json 或
  输入配置上传到后台、提交 Git 或写入报告**。代理由运维预置私密包及只含自身目标的 catalog。
  密钥在 Docker 环境配置中对宿主运维可见，这不是外部密钥保险库；非可信人员不得有 Docker 权限。
- 容器入口只接受固定角色，按文件白名单把该角色配置写入私有 tmpfs，然后启动固定可执行文件；
  健康检查不输出凭据。IM 所需插件运行目录可写，但根目录保持只读；Caddy 使用非特权端口，
  移除了上游不需要的文件 capability，不通过增加容器特权来修复启动。

可重复验证（PowerShell 7，仓库根目录）：

```powershell
./infra/scripts/build-enterprise-bundle.ps1
./infra/scripts/test-tenancy.ps1 -DeploymentDocker -EnterpriseBundleDocker
```

第二条还要求原 `compose.test.yml` 的隔离数据库/IM/LiveKit 已运行。完整企业测试使用随机
`frogim-deploy-stack-*`、新随机凭据和临时证书，实际调用 mTLS 代理部署、升级、回滚及恢复
journal，检查真实五项依赖和测试身份持久性；同 CA 的错误服务身份仍会拒绝。最后仅清理
归属已核对的测试容器/网络/合成卷，默认企业容器 ID 不变。它没有从真实平台持久任务启动
整套链路，没有验证用户音视频轨道、推送供应商、备份恢复或公网物理主机，不能据此上线。

## 平台到完整企业栈的一体化验证

新增独立用例补齐上一节的控制面边界。PowerShell 7、Docker Desktop Linux engine：

```powershell
./infra/scripts/build-tenant-agent.ps1
./infra/scripts/test-tenancy.ps1 -DeploymentStackDocker
```

依赖原 `compose.test.yml` 测试栈，以及 `build-enterprise-bundle.ps1` 已生成的固定摘要镜像。
测试脚本也可以同时指定 `-DeploymentDocker -EnterpriseBundleDocker` 运行全部部署回归。
不自动更新默认企业，不自动给默认代理挂载 socket 或修改默认发布目录。
先完成镜像构建，再启动测试；不要在运行中覆盖 `:local` 镜像标签。本机已观察到重新构建后
旧的未发布仓库摘要不可再解析，测试不会自动联网拉取或换用新摘要。
当前升级/回滚演练使用同 schema、同工具镜像的不同发布记录，验证的是编排和数据保留；
不同业务二进制版本之间的升级/回滚兼容性仍需独立验收。

- 只允许专用回环测试 PG 的随机 schema；真实 Linux 代理生成随机主机指纹，明确标记
  `local_preview`。平台与代理、企业之间使用临时 CA 签发的独立 mTLS 身份。
- 通过真实平台 HTTP 管理接口登记、绑定和创建部署任务；真实代理消费固定目录，启动完整
  九服务。确认丢失仅注入传输返回，不伪造健康、身份或执行结果。代理重启后使用原 journal。
- 从 provisioning 经独立确认激活，继续验证企业开户、平台登录、票据消费与重放拒绝、
  业务直连，再验证停用、升级、回滚及恢复后原身份和旧凭据拒绝。不发送真实短信/推送。
- `frogim-agent-full-*` 是可信宿主运维测试进程，显式持有 Docker socket；只读根目录和
  capability 限制不能把 Docker socket 变成安全沙箱。企业证书不能调用代理，企业容器不
  获得 socket。真实主机仍需独立安全边界和运维权限管理，不能把该测试当成物理隔离证明。
- 最后按确切身份和归属标签清理临时代理、企业容器、合成数据卷及网络；不删除其他项目。
  在测试运行期间不要同时重建默认栈，否则“默认容器 ID 不变”的保护断言会正确失败。

## 加密冷备份与新数据卷恢复（本机预览）

新增离线命令 `server/cmd/tenant-backup` 和镜像内固定路径助手 `tenant-volume`。
当前只接受 schema 79、已经由原执行器确认部署的九服务本机预览包；不是旧服务器接管工具。
不自动停止服务、删除原卷、更新部署目录/平台任务、激活企业或恢复业务访问。
默认本机 `frogim-tenancy-local` 没有执行器 journal，**不能直接套用此工具**。

操作前必须完成：平台停用确认、维护互斥、停止代理、正常停止除 PostgreSQL 外的全部服务。
命令持有原执行器文件锁，并验证当前代次、发布摘要、容器实际镜像/归属/挂载、停机退出码、
数据库企业身份/schema/访问代次及停用完成记录。源 PG 不允许另有业务连接或第二个运行容器
挂载其数据卷。仅声明“已停用”或健康检查成功不能替代这些验证。
当前借用企业停用流程，会撤销既有会话；不是无感的在线每日备份，不能直接挂到生产日程。
正式每日任务仍需完成维护窗口、停止/恢复顺序、用户重连影响、失败恢复与平台审计编排。

- 独立 32 字节随机备份密钥，禁止复用认证、媒体等运行密钥，不在备份目录内存放。
  每个文件使用随机盐派生 AES-256-GCM 密钥、按序号认证分块和必需的终止记录。
  加密清单包含企业、服务器、发布摘要、执行代次、停用代次、schema、时间和文件密文 SHA256。
  清单最后写入；中断备份没有完成清单，不覆盖或复用失败目录。
- 包含 PostgreSQL custom dump、IM 数据和日志、MinIO、Redis、插件，以及原发布配置和元数据。
  配置内的证书和服务密钥也加密；不打包平台数据库、平台 CA 私钥或其他企业数据。
- 恢复先验证全部密文、清单绑定、卷归档路径和类型；只接受普通文件与目录，不接受符号链接、
  硬链接归档、设备、xattr、set-ID、越界路径、重复条目或不完整归档。
- 所有目标卷均为新建、企业范围命名和带归属标签；不合并或覆盖旧卷。
  PG 在不联网、不发布端口、使用原随机数据库密码的临时容器恢复，单事务失败即不确认。
  恢复后核对身份、schema、停用代次；所有步骤成功后才写出 staged release 元数据。
  **staged 不等于已切换或已恢复业务**，后续仍需审核发布目录与平台部署/恢复流程。
- 失败保留未确认的新卷和 intent，供运维检查，不自动清理原卷。当前同 ID 失败操作不自动续跑。
  当前要求原 journal 和同一已停用发布/代次仍在，不能用于主机磁盘丢失、跨版本或跨企业恢复。
  每个明文流上限 64 GiB（卷 tar 含归档开销），超限明确失败，不截断为成功。

操作命令示意（PowerShell 7，示例目录须由运维预先建立；不要复制到默认本机栈直接执行）：

```powershell
go -C server run ./cmd/tenant-backup -mode create-key -key-file 'D:\enterprise-keys\alpha.key' -confirmed
go -C server run ./cmd/tenant-backup -mode backup -config 'D:\enterprise-private\backup.json' -key-file 'D:\enterprise-keys\alpha.key' -confirmed
go -C server run ./cmd/tenant-backup -mode stage-restore -config 'D:\enterprise-private\restore.json' -key-file 'D:\enterprise-keys\alpha.key' -confirmed
```

私密配置为严格 JSON，字段与 `cmd/tenant-backup.configuration` 一致：`expected` 指定
`tenantId/serverId/releaseId/releaseDigest/generation/accessVersion/schemaVersion`；另指定
`hostFingerprint/runtime/stateDirectory/bundleDirectory/dockerBinary/dockerEndpoint/catalog/archiveDirectory`。
`catalog` 必须来自原已审核发布目录；`dockerEndpoint` 只支持本机 Unix socket 或 Windows
Docker Desktop Linux named pipe。恢复另需新的 `restoreReleaseId` 与递增 `restoreSequence`。
命令不打印配置、密钥或数据库错误正文，也不继承远程 Docker context；原 journal 缺失直接拒绝。

真实演练（依赖前述测试数据库栈；仅生成可丢弃测试企业，不操作默认企业）：

```powershell
./infra/scripts/build-enterprise-bundle.ps1
./infra/scripts/test-tenancy.ps1 -BackupDocker
```

演练写入真实 WuKongIM 消息、MinIO 对象和 PG 身份，停用后冷备份，恢复至新卷，显式测试部署
后逐项读取对照；验证原卷保留、访问仍停用、错企业/错密钥拒绝、重复恢复拒绝和再次备份。
新增测试还暴露并修复了媒体初始化容器忽略 SIGTERM 的问题；插件等待进程同样正常处理 SIGTERM。
这些离线原语本身不提供自动调度。平台每日调度见下文；异地存储交付/凭据管理、备份密钥轮换
和丢失主机恢复控制面仍未完成。

## 代理端持久备份任务（显式开启）

在原代理私密执行配置中可增加 `backup`，包含运维预建的绝对 `directory` 和独立 32 字节
`keyFile` 路径。密钥必须在备份目录外；目录与密钥指纹固定在 journal，重启时不能悄悄换掉。
未配置不提供备份路由；已有备份配置记录的代理重启时省略配置会拒绝启动，避免遗留恢复任务。
默认本机代理依然只有 inspect 能力，不修改其配置、不挂载 Docker socket。

生产 `dedicated_host` 模式启用备份还须提供 `backup.offsite`，具体配置、重试与收据说明见
[异地备份](../../docs/OFFSITE_BACKUP.md)。预览模式可仅本地归档；已配置异地目标的代理重启
不能省略或更换目标。可以轮换同范围存储凭据，不能复用加密密钥。平台只传绑定后的目标 ID，
不接收桶、端点、路径或秘密。异地交付不持有业务维护锁，也不再次停止企业服务。

平台独立 mTLS 身份可访问 `POST /internal/agent/backup/submit|status|retry|cancel`。只传不可变的
任务 ID、企业/服务器/主机指纹、发布摘要、执行代次、停用代次和 schema，不接受路径、密钥、
Shell 或远程地址。重试另要求 `expectedRevision`；旧确认不能重启新阶段。
取消同样要求版本一致，且只允许尚未进入停止写服务阶段的任务；不能通过取消跳过服务恢复。

- journal 升级至 v2，旧 v1 原子迁移；旧执行器无法忽略新备份任务继续部署。与部署、离线
  冷备份和 staged restore 共用原文件锁及活动任务互斥，不修改数据库版本。
- 顺序为准备、停止写服务、加密归档、恢复原服务、验证。准备先冻结九个容器 ID 和六个卷的
  归属/创建信息；恢复只能 `start` 原 ID，不调用 `compose up`，不会补建缺失卷或替换容器。
- 已完成但确认丢失的归档须重新认证全套密文；不覆盖。部分归档保留，先恢复原服务，再把
  本次任务标为失败。显式重试使用新的 attempt 目录，不覆盖旧证据。
- 服务恢复/验证失败保留 `unconfirmed` 和互斥，必须显式重试；不能仅凭归档完成就宣称
  备份任务成功。只回收本次任务、同 attempt、同镜像/入口、无网络、只读卷的导出助手。
- 始终保持企业已停用状态，不自动恢复访问、不恢复旧会话。状态接口不返回私密路径、密钥、
  容器/卷内部快照或子进程错误原文。阻塞 IO 不占状态查询锁。

平台任务已接入这些原语，见下节。平台认证库手动备份及企业自动异地交付已经接入；平台日
调度／自动交付、丢失主机恢复校对与受控激活仍待补齐。
即使容器恢复健康，企业访问仍需另外受控恢复；独立备份任务不自行恢复企业访问。

## 平台备份任务与维护互斥

平台 schema 15 增加备份任务和控制请求记录。后台“备份任务”提供创建、查看、检查后重试，
以及尚未开始时的取消。仅独立平台 operator 可写；reader 只读。所有写入均要求理由、二次
确认、不可变请求号和当前版本；确认丢失应复用原请求，不另建任务。

- `GET/POST /platform/admin/backups` 查询/创建；`POST /platform/admin/backups/{id}/control`
  接收 `action=retry|cancel`、`expectedRevision`、`requestId`、`reason`、`confirmed`。
- 创建只接受平台已核验部署、已确认停用的企业。两次短事务在代理预检前后验证管理员、
  企业/服务器绑定、发布摘要、部署代次和停用代次。与部署、服务器重绑定、导入及企业恢复互斥。
- 首次业务 readiness 之后、提交代理之前，持久保存分发意图。代理停机期间的确认丢失不再
  重做依赖业务 API 的首次检查；继续以相同任务号查询/提交。未知结果不解锁、不自动重试执行。
- 代理回执必须匹配冻结目标、代次、修订号及加密清单摘要。完成、失败和取消都还须确认原
  企业服务恢复且访问仍停用；审计失败或 worker 租约丢失不提交完成状态。
- 取消与代理开始执行竞争时，以代理回执为准，不声称已取消；恢复阶段不能取消。失败归档
  不覆盖，若服务恢复并核验完成，可创建新的备份任务。执行状态不确定须人工检查后重试原阶段。
- 后台仅展示状态和加密清单摘要/大小，不返回路径、密钥、卷/容器快照或子进程私密错误。
  归档核验完成不表示异地交付完成，更不等于生产灾难恢复已验收。

本机默认代理未启用执行/备份能力，不会因增加页面自动停止默认企业。真实链路演练使用
`test-tenancy.ps1 -DeploymentStackDocker` 新建的隔离测试企业；不会复用默认企业的业务卷。

## 每日维护窗口（默认关闭）

平台 schema 16 增加每日计划、不可变设置请求与维护运行记录。后台“每日维护”由 operator
填写理由并确认重复停机影响后配置；reader 只读。没有计划的企业不执行任何自动备份；不自动
为默认企业配置，不生成代理执行凭据，也不将尚未完成部署核验的服务器纳入维护。

- `GET /platform/admin/backup-schedules` 查询；`PUT /platform/admin/backup-schedules/{tenantId}`
  设置 `enabled/startMinuteUtc/windowMinutes/expectedVersion/requestId/reason/confirmed`。
  起始时间为 UTC 当天分钟数（0–1439），窗口 15–180 分钟；北京时间比 UTC 晚 8 小时。
  后台同时提供北京时间参考。窗口可以跨 UTC 午夜，运行归属开始日。
- `GET /platform/admin/maintenance` 查看执行状态和停用/备份/恢复的关联任务号。
  配置更新要求当前版本；相同请求返回原结果，不覆盖新设置。关闭计划仅阻止未来任务，已经
  接受的维护仍须恢复到安全状态；原配置者停用或失去 operator 身份时不创建新任务。
- 启用会授权每日在窗口内临时停用企业、撤销原会话、备份后恢复访问，用户须重新登录。
  每个 UTC 窗口开始日最多创建一条记录；数据库锁和唯一约束约束多平台实例。平台停机错过的
  窗口不补执行，未开始的冲突任务只在窗口内重试。人工已停用的企业记录为跳过，不代其恢复。
- 执行前通过独立代理和业务 readiness 再核对主机、企业、发布、部署及访问代次。状态依次为
  prepare / pause / backup / resume / finished。调度器不保存浏览器 token、不冒充后台登录；
  已审计的运行及有效 worker 租约仅允许该企业的三个固定子任务。
- 维护、部署、人工备份、服务器绑定、导入和人工恢复互斥。只有本运行精确的停用任务及版本
  才允许恢复；不能以“当前恰好是 suspended”推断恢复权。已受理子任务确认丢失后按原 ID 重查，
  关闭窗口不重建它，也不取消服务恢复。备份 `unconfirmed` 必须在原备份任务中人工检查后重试。
- 窗口结束不再新建停用/备份子任务；已受理任务继续。若已经停用但来不及创建备份，仍受控
  恢复企业并标记本次跳过。备份失败但服务已确认恢复时，恢复访问后标记失败；不宣称备份成功。
  租约、审计、版本或服务恢复未确认时不释放维护锁。

这不是完整生产备份方案：平台认证数据库、异地交付/隔离凭据、丢失主机恢复与真实维护时长
验收仍是上线阻断项。预览/生产启动限制继续保留；真实演练只使用临时隔离企业。

## 中央推送提交的持锁检查

平台持有账号、企业访问代次、会话及设备绑定的数据库锁期间，使用原连接每 250 毫秒检查
事务是否仍有效；单次检查超时 1 秒。连接丢失、会话/通知到期取消供应商请求，不将未知
结果提交为成功，也不把取消错误误记为设备令牌失效。每次实际供应商 HTTP 请求前再次
检查，包括个推认证刷新后的重试、APNs VoIP 及 Web Push 编码后的发送。检查能力仅存在
进程内，发送结束后不可复用，不进入消息或日志。

通知载荷期限取通知和所属平台会话期限的较早值。托管个推离线 TTL 使用剩余毫秒数并
限制不超过一天，重试重新计算；原单企业通知的默认值不变。参数单位依据
[个推 REST v2 文档](https://docs.getui.com/getui/server/rest_v2/common_args/)。

数据库事务与外部推送不是分布式原子提交：检测之前已被供应商接收的通知不可追溯撤回。
失败重试保留稳定的投递 ID，不承诺供应商或设备端恰好一次。阶段 29 的供应商测试使用
本地模拟 HTTP 服务，只有数据库故障注入使用真实 PostgreSQL；不代表 APNs/个推实收。
原生后台来电及真实供应商配置仍须收尾，本机不启用任何供应商。Web 多标签页协调见下节。

## Web 同浏览器多标签页凭据

托管客户端要求安全上下文和 Web Locks。平台认证地址决定协调范围，不接受用户手填企业
地址。轮转、推送登记、改密与退出在锁内重读加密平台凭据；新登录产生新的随机登录代次，
旧页面不能清除或注销新登录。共享标记不包含 token，存储事件/前台恢复/3 秒补查只触发
重新核验；暂时无法协调时拒绝相关认证操作，不使用旧令牌绕过。

业务 token、IM 会话及本页待办能力保存在加密 sessionStorage，共享业务缓存仍按企业/
本地用户/归属版本隔离。刷新页面可重新认证；新标签页不能仅凭共享平台元数据在认证故障
时读取其他标签页的业务凭据。平台暂不可用时，本页离线恢复还必须匹配登录代次、业务
版本和原 JWT 有效期；其他标签页的续期不会替它延长。相同浏览器的新登录使旧登录页面
失效，这不是“每个标签页独立登录不同账号”的功能。

验证：`fvm flutter test --no-pub test/platform_multitab_test.dart`；浏览器用
`fvm flutter test --no-pub --platform chrome test/session_coordination_browser_test.dart`。
Windows Flutter 3.44.8 的测试服务器可能对 CanvasKit 返回 404。保留该测试进程时，可把
**该次隔离测试浏览器**的调试端口传给 `node tool/serve_windows_flutter_test_assets.cjs <port>`。
工具严格检查 Flutter 测试 host、指定用例和 SDK 资源路径，只临时提供本机 FVM 资源，不改
SDK、不访问生产地址、不关闭证书检查。成功判据仍是 Flutter runner 的全部测试通过。
这是测试工具兼容处理，正常应用构建/部署不需要此脚本。

## Android / iOS 原生来电隔离

Android 托管构建从传入的 `PLATFORM_AUTH_URL` 生成 native manifest 布尔标志。不要单独
修改该标志或把托管客户端构建成旧单企业模式。后台来电要求平台登记返回的绑定 ID、绑定
版本和租约，同时匹配本机企业、本地用户、归属/认证/企业版本及有效期；不存在已确认的
本地身份/绑定时不弹来电。私有存储不包含 API token、IM token、CID 或供应商密钥。

系统来电 ID 额外包含本次登录代次。退出、调换或新登录清除原生身份并结束旧原生来电，
接听动作恢复到 Flutter 后仍须向当前企业验证真实通话。桥接失败不会静默启用旧身份；
没有网络时也不因已收到推送而延长业务授权。已被系统或供应商接收的通知不承诺追溯撤回。

验证入口：`fvm flutter test --no-pub test/tenant_call_scope_test.dart test/native_call_state_test.dart test/native_call_session_test.dart test/call_controller_test.dart`；
Android 运行 `:app:testDebugUnitTest`，并检查该次 Manifest 的托管布尔值。原生策略测试在
`LinliTenantCallPolicyTest`。不需要生成或安装 APK 即可做 JVM/编译验证，但这不能替代
Android 后台/锁屏/杀进程实收测试。默认本机继续不配置真实推送供应商。

iOS 在 PushKit 注册前创建应用自有 CallKit provider，避免第三方插件按最近一次 PushKit
来电结束错误 UUID。构建阶段使用 Flutter 自带 Dart 从 `PLATFORM_AUTH_URL` 生成只含
schema/managed 的 `linli_call_mode.json`，不把其他 Dart defines 写入资源。缺失或损坏标志
默认收紧为托管模式。应用私有身份及最小来电账本原子保存、排除备份，不持久化推送令牌、
API/IM 凭据、媒体凭据、联系人名称或头像。

无效/旧身份 VoIP 推送仍按 PushKit 要求匿名上报后立即结束，不执行旧账号业务动作；不承诺
系统界面完全不闪现。接听动作等待当前企业真实通话与媒体连接，失败、超时、退出及迟到
回调只清理相应 UUID。CallKit 音频激活/停用通知转交现有 WebRTC 音频会话。
参考 [Apple PushKit 文档](https://developer.apple.com/documentation/pushkit/responding-to-voip-notifications-from-pushkit)
及 [WebRTC 音频激活接口](https://webrtc.googlesource.com/src/+/refs/heads/main/sdk/objc/components/audio/RTCAudioSession.h)。

PushKit/CallKit 初始化继续独立；托管模式只有个推已返回 CID、SDK 接受 VoIP token、平台
允许 `getui_voip` 且当前会话/原生绑定写入确认后，才登记可用来电能力。token 刷新/失效会
立即撤销原生旧绑定并重新登记；能力未就绪会显示状态，不走普通通知或 APNs 直连。
平台 schema 21 撤销旧直连绑定；迁移后必须重新登记。详见
[收尾操作说明](../../docs/MULTITENANT_CLOSEOUT_OPERATIONS.md)。

Flutter 回归入口：`test/ios_system_call_service_test.dart`、`test/native_push_startup_test.dart`、
`test/native_call_mode_test.dart`。纯 Swift 策略位于 `tool/native_calls/TenantCallPolicyTests.swift`，
可以在 macOS 用 Swift 与 `ios/Runner/LinliTenantCallPolicy.swift` 一起编译执行；已加入 iOS
工作流的无签名编译任务。Linux 隔离容器的策略测试/语法解析不是 Xcode 类型检查或真机验收。
上线前还必须验证锁屏、冷启动、重复 VoIP、账号切换、CallKit 接听/挂断和实际音频轨道。

## 平台认证库备份与隔离恢复

`build-platform-backup.ps1 -Test` 构建固定 PostgreSQL 17 运维 helper，并在一次性独立数据库
验证加密快照、重复确认、失败归档、恢复隔离及持锁连接中断。也可在总回归脚本传入
`-PlatformBackupDocker`。新工具不开放 HTTP、不挂 Docker socket、不触碰默认企业数据。
平台 schema 17 增加目录 UUID 和备份记录；企业 schema 不变。

恢复目标必须为空的新库，成功后旧会话/票据/找回能力/推送绑定均撤销，数据库仍保持隔离，
平台启动会拒绝它。`recovery-review/recovery-review-status` 完成不授予启动权限；新增
`recovery-prepare/recovery-activate/recovery-status` 在维护窗口核对替换认证源、实时证据、
显式账号身份核验及全新密码，持久审计后激活平台。企业仍暂停，未知账号/旧任务仍冻结，
不允许删除隔离标记开服。具体限制见 [收尾操作说明](../../docs/MULTITENANT_CLOSEOUT_OPERATIONS.md)。
schema 18 已补平台日调度、自动异地确认及日期收据发现，独立 helper 维护服务模板为
`compose.platform-backup.yml`，不加入默认本机启动。详见
[平台备份操作说明](../../docs/PLATFORM_DIRECTORY_BACKUP.md)。

阶段 35 新增 `backup-offsite` 运维工具，按目录／企业范围使用独立 S3 IAM 配置传输密文，
32 MiB 分段、条件 PUT、完整认证及最后完成标记。支持上传中断后核对已有分段，下载只写新
目录，拒绝不同内容覆盖；不上传解密密钥，不开匿名权限、不跟随跨地址重定向。
`test-tenancy.ps1 -OffsiteDocker` 显式启用本机 TLS MinIO＋三个独立 IAM 用户及真实 CLI 测试。
命令和边界见 [异地备份说明](../../docs/OFFSITE_BACKUP.md)；当前工具不自动开启任何供应商或计划任务。

## 通话握手确认丢失的冷修复

`tenant-backup -mode repair-media` 是私有运维工具，不是公开或代理 HTTP 命令。
它要求企业访问已关闭、原 journal 排他锁、原写入者和 LiveKit 全部停止，只留下 PostgreSQL，
然后在同一事务清理不确定握手并写审计。不会启动服务、解除企业停用或替代正常撤权确认。
`test-tenancy.ps1 -BackupDocker` 编译真实 CLI 并在随机九服务栈验证；默认本机企业不会被
停写。部署和维护步骤见 [通话握手冷修复说明](../../docs/TENANT_MEDIA_RECOVERY.md)。
