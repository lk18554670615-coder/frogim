# NexaChat 全功能测试报告

- 测试日期：2026-08-09
- 测试环境：Windows 11、FVM、Flutter 3.44.8、Docker Desktop
- 代码基线：`main` / `ba41c4bd1589ce77ae1c2b42d87681bf22cd0222`
- 最终结论：**通过（在本地可验证范围内）**

## 1. 测试范围

本次覆盖 Flutter 客户端、管理后台、Go 服务端、PostgreSQL、Redis、MinIO、Coturn、Docker Compose、本地 Web UI、Android 模拟器和主要业务验收流程。测试过程中发现的低风险、可明确定位的问题已直接修复并完成回归。

项目当前包含以下 Flutter 平台目录：

- Android
- iOS
- macOS
- Web

Windows 和 Linux 平台目录目前未生成。由于测试主机是 Windows，本次实际构建并运行验证 Android 和 Web；iOS、macOS 需在带 Xcode 的 macOS 主机上验证。

## 2. 环境检查

| 项目 | 结果 |
| --- | --- |
| FVM | 4.1.2 |
| 项目 Flutter | 3.44.8 stable |
| Dart | 3.12.2 |
| Flutter DevTools | 2.57.0 |
| Android SDK | 37.0.0 |
| Android 模拟器 | 37.1.11 |
| Java | Android Studio JBR 25.0.2 |
| Android licenses | 已接受 |
| Go（容器） | 1.26.5 |
| Node.js（容器） | 22.23.2 |
| npm（容器） | 10.9.8 |

`fvm flutter doctor -v` 最终检查中 Flutter、Android toolchain、设备和网络均通过。主机未安装 Chrome 和 Visual Studio；Web 测试使用 Edge/内置 Chromium，项目也尚无 Windows Flutter 平台目录，因此不影响本次 Android/Web 验证。

项目已经通过 `apps/mobile/.fvmrc` 固定 Flutter 3.44.8，并配置国内 Flutter/Dart/FVM 镜像源。

## 3. 自动化测试结果

| 模块 | 命令或检查 | 结果 |
| --- | --- | --- |
| Flutter 静态检查 | `fvm flutter analyze` | 通过，0 issue |
| Flutter 单元/组件测试 | `fvm flutter test` | 通过，96/96 |
| Flutter Web 发布构建 | `fvm flutter build web --release` | 通过 |
| Android Debug 构建 | `fvm flutter build apk --debug` | 通过 |
| Android APK 签名校验 | `apksigner verify --verbose --print-certs` | 通过，V2 签名有效 |
| 管理后台依赖安装 | `npm ci` | 通过 |
| 管理后台 lint | `npm run lint` | 通过 |
| 管理后台测试 | Vitest | 通过，2 个文件、16/16 |
| 管理后台生产构建 | Vite | 通过 |
| npm 安全审计 | production/all | 通过，0 vulnerability |
| Go 全量测试 | `go test ./... -count=1` | 通过，含 PostgreSQL/Redis 集成测试 |
| Go 静态检查 | `go vet ./...` | 通过 |
| Go 竞态检查 | `go test -race ./...` | 通过，未发现数据竞争 |
| Compose 配置 | `docker compose config -q` | 通过 |
| Shell 脚本语法 | `bash -n infra/scripts/*.sh` | 通过 |
| 本地冒烟测试 | `infra/scripts/smoke-local.sh` | 通过 |
| 本地完整验收 | `infra/scripts/acceptance-local.sh` | 通过 |
| 文档链接检查 | `infra/scripts/check-docs.sh` | 通过（新增本报告前共检查 36 个 Markdown 文件） |

## 4. UI 与端到端验证

### Flutter Web

在真实 Web 页面完成以下操作：

- 注册、密码登录和退出登录
- 消息、联系人、工具、个人资料和设置页面切换
- 账号删除
- WebSocket 实时连接建立与正确关闭
- 浏览器控制台无产品错误或警告

### Android

使用 API 35 Google APIs x86_64 模拟器完成：

- Debug APK 安装和冷启动
- 默认调试登录页面渲染
- 使用真实本地后端进行密码登录
- 进入消息主页并保持前台运行
- 未发现 Flutter 或 Android fatal crash
- 测试账号清理后再次登录返回预期的 401

### 管理后台

使用真实后台页面完成：

- 管理员邮箱、密码和 TOTP 登录
- 总览、用户、群组、举报、健康和设置页面访问
- 退出登录
- 无页面告警和控制台错误
- 无效登录经 Nginx 代理返回后端预期的 401

### 服务端完整验收

验收脚本覆盖了注册、验证码和密码登录、密码重置、资料、好友、私聊、会话设置、消息幂等、编辑、表态、收藏、转发、搜索、文件上传、回复、已读、撤回、音视频通话状态、群组生命周期、成员与角色、禁言、群昵称、群主转让、提及、置顶、公告、黑名单、举报、反馈、管理员 RBAC、封禁与解封、审计和账号删除等流程。所有临时业务账号均已清理。

## 5. 本次修复

| 问题 | 修复 | 回归结果 |
| --- | --- | --- |
| Flutter Web 退出时可能被失败或未关闭的 WebSocket 卡住 | 为订阅和通道关闭增加分离、超时及异常隔离 | 退出约 1.2 秒返回登录页，11/11 仓储专项测试及 96/96 全量测试通过 |
| 本地 Web Origin 未被服务端允许 | 在 Compose 中加入本地 Web/Admin Origin | WebSocket 成功升级为 101，连接指标正常 |
| Nginx 将部分 `/api/` POST 请求错误交给 SPA fallback | 将 API location 设为 `^~ /api/` | 无效登录从 405 恢复为预期 401 |
| 管理后台 TypeScript 版本与 React 插件类型不兼容 | TypeScript 升级到 5.9.3 | lint、测试和生产构建通过 |
| `nanoid` 开发依赖存在高危审计项 | 锁文件更新至 3.3.18 | npm production/all 审计均为 0 vulnerability |
| Go outbox 集成测试存在时序竞争 | 测试改为轮询等待发布完成 | 全量及 race 测试通过 |
| Schema migration 测试与公共 schema 相互污染 | 使用独立 schema、清理和正确 search path | 集成测试稳定通过 |
| FVM 缓存可能进入 Git | 将 `.fvm/` 加入移动端 `.gitignore` | 工作区仅保留 `.fvmrc` 配置文件 |

## 6. 构建产物

Android Debug APK：

- 路径：`apps/mobile/build/app/outputs/flutter-apk/app-debug.apk`
- 大小：264,711,965 字节
- SHA-256：`c681bb62d6f20243b0b78dac0a4f8bab3162aa9918059d75f06af4e79393bb22`
- 签名：Android Debug RSA 2048，APK Signature Scheme v2 验证通过

Docker Compose 的 7 个服务最终全部健康：Admin、Server、Web、PostgreSQL、Redis、MinIO、Coturn。

## 7. 已知边界与剩余风险

- iOS 和 macOS 已有工程目录，但本次无法在 Windows 主机上构建；发布前应在 macOS/Xcode 与真实设备上复测。
- Windows 和 Linux Flutter 平台目录尚未生成；如需要支持，应先补齐平台工程和对应 CI。
- 生产短信供应商回调、APNs VoIP、个推、真实相机/麦克风、真实设备推送，以及跨公网 NAT/TLS 的 TURN 未在本地端到端验证；相关本地接口、状态机和 Coturn 健康检查已通过。
- 本地 Coturn 使用非 TLS 测试配置；生产环境需配置有效证书和 TLS。
- Flutter 构建提示若干插件仍使用旧 Kotlin Gradle Plugin 应用方式。当前构建通过，但后续 Flutter 升级前应迁移到 Built-in Kotlin。
- Android 模拟器覆盖安装两种不同配置的 Debug APK 时出现一次 FlutterSecureStorage 密钥不匹配恢复日志；应用已自动恢复并成功登录，真实发布包升级仍建议纳入回归。

## 8. 结论

在当前代码实现和本地可验证环境内，Flutter Android/Web、管理后台、服务端及基础设施的主要功能均已完成自动化与真实 UI 验证，发现的问题已修复并通过回归。项目可以继续进入真实移动设备、生产第三方服务和发布环境验证阶段。
