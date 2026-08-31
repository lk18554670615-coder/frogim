# 青蛙呱呱 Flutter 客户端

高完成度 Flutter 即时通讯客户端。运行时只连接真实业务 REST、WuKongIM 消息链路和 LiveKit 音视频，不提供 Demo 登录，也不会在真实请求失败时回退到演示数据。

## 运行

```bash
fvm flutter pub get
fvm flutter run
```

启动前必须配置真实服务端：

```bash
fvm flutter run \
  --dart-define=APP_ENV=development \
  --dart-define=API_BASE_URL=http://127.0.0.1:8080 \
  --dart-define=ENABLE_DEMO=false
```

Android Studio 模拟器不能使用服务端下发的 `127.0.0.1` WuKong TCP 地址；该地址会指向模拟器自身。先从仓库根目录启动模拟器专用覆盖：

```bash
make infra-up-android-emulator
```

再以宿主机映射地址运行：

```bash
fvm flutter run -d emulator-5554 \
  --dart-define=APP_ENV=development \
  --dart-define=API_BASE_URL=http://10.0.2.2:8080 \
  --dart-define=ENABLE_DEMO=false
```

该覆盖只用于本地 Android Studio 模拟器。生产环境必须配置真实公网 `IM_WUKONG_TCP_URL`，不得追加此 Compose 文件。

个推本地参数放在已忽略的 `dart_defines.local.json`，运行时追加
`--dart-define-from-file=dart_defines.local.json`。需要启用个推的发布构建必须同时提供
`GETUI_ENABLED=true`、`GETUI_APP_ID`、`GETUI_APP_KEY` 和
`GETUI_APP_SECRET`；三项客户端参数未完整配置时，GitHub iOS 签名构建会保持
`GETUI_ENABLED=false`，IPA 仍可生成，但不具备个推离线推送能力。`MasterSecret`
只允许配置在服务端环境变量中。

开发后端验证码由部署环境配置。任何环境启用 Demo 都会直接启动失败；`staging` 和 `production` 还必须配置服务地址，`production` 强制要求 HTTPS。WuKong TCP/WSS 和 LiveKit 地址只接受鉴权后的 `ImSession`/通话接口下发，不允许由客户端构建参数覆盖。真实请求失败时会如实显示错误，不会伪装成成功结果。

正式构建还必须提供法务审核后的 `TERMS_URL` 和 `PRIVACY_URL` HTTPS 地址。Release 模式默认按 `production` 校验，即使遗漏 `APP_ENV` 也不会静默打出 Demo 包。

Android 正式包只接受外部注入的上传密钥，缺少任一参数会直接终止 Release 构建：

```bash
export RELEASE_STORE_FILE=/secure/path/upload-key.jks
export RELEASE_STORE_PASSWORD='replace-me'
export RELEASE_KEY_ALIAS=upload
export RELEASE_KEY_PASSWORD='replace-me'

fvm flutter build appbundle --release \
  --dart-define=APP_ENV=production \
  --dart-define=API_BASE_URL=https://chat.example.com \
  --dart-define=WS_URL=wss://chat.example.com/im \
  --dart-define=ENABLE_DEMO=false \
  --dart-define=TERMS_URL=https://chat.example.com/legal/terms \
  --dart-define=PRIVACY_URL=https://chat.example.com/legal/privacy
```

Windows developers can run
`powershell.exe -NoProfile -ExecutionPolicy Bypass -File ..\\..\\infra\\scripts\\generate-android-signing.ps1`
to create the ignored `android/key.properties` and
`android/release-upload.jks`. Back up both files offline before the first store
release.

生成实际服务器正式包时使用统一门禁脚本，禁止手工省略构建参数：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File ..\\..\\infra\\scripts\\build-android-release.ps1 `
  -ServerOrigin https://119.28.190.45 -Format all
```

脚本会先检查服务健康页、用户协议和隐私政策，再构建正式 APK/AAB、验证
APK v2 签名和 AAB JAR 签名，并在仓库根目录 `build/releases/android/` 生成
带目标服务器、Git 提交及 SHA-256 的发布清单。

把审核通过的包复制到服务器下载目录后，通过后台 API 发布版本策略：

```bash
bash infra/scripts/publish-client-version.sh android 1.0.0 1.0.0 \
  https://119.28.190.45/downloads/linli-im-android-latest.apk \
  '首个 WuKongIM 开发验收版本'
```

脚本从服务器配置读取数据库管理员凭据，经带理由和二次确认的后台接口写入，
随后用低版本客户端调用公开配置接口验证强制升级决策；它不会输出管理员凭据。

密钥文件和密码不得提交到仓库；商店发布前应将上传密钥纳入组织级密码库和离线备份。

## Android 与 iOS 应用标识

Android `applicationId` 与 iOS Bundle ID 统一为 `com.fd.kuailiao`。修改该值会被系统和应用商店视为另一款应用，旧包名客户端不能原地升级到新包名。

## iOS 签名构建

iOS Release/Profile 使用 App Store 手动分发签名。Windows 不直接构建 IPA，统一通过仓库的 GitHub Actions `iOS Build` 工作流在 `macos-26` / Xcode 26 Runner 上构建：

- 推送 `main` 中的 Flutter、iOS 或工作流变化时，自动执行静态分析、完整测试和无签名 Release 编译。
- 在 GitHub Actions 页面手动运行工作流，并启用“使用仓库 Secrets 构建签名 IPA”，构建成功后下载 `ios-signed-<run_number>` Artifact。
- Apple Distribution P12、密码、描述文件、ExportOptions 和临时 Keychain 密码必须使用 GitHub 加密 Secrets；不得写入 Git、构建参数或日志。
- App Store 描述文件生成的 IPA 用于 App Store Connect/TestFlight；如需直接安装到指定设备，必须改用包含设备 UDID 的 Ad Hoc 描述文件。

完整配置与操作步骤见 [GitHub Actions iOS 构建](../../docs/GITHUB_IOS_ACTIONS.md)。

## 分层

- `lib/core`：环境配置、模型、主题与应用状态机。
- `lib/data`：业务仓储、WuKongIM 平台 Gateway、消息映射和本地会话缓存；演示仓储仅供自动化测试与组件预览使用，不进入运行时。
- `lib/ui/screens`：登录、消息、联系人、搜索、聊天、群组、隐私和安全流程。
- `lib/ui/widgets`：头像、搜索、状态面板、设置项、消息回执等设计组件。
- `lib/previews.dart`：Flutter Widget Previewer 的浅色/深色页面与关键组件预览。

登录返回业务令牌和 `ImSession`。Android/iOS 通过官方完整 Flutter SDK 1.7.9连接 WuKongIM，Web通过隔离的官方 JS SDK 1.3.5 Gateway，macOS通过官方 Easy SDK 1.0.3 Gateway；页面只依赖统一的 `ImRepository`。受保护 REST 请求遇到 401时单飞刷新凭据，并只重放原请求一次；WuKongIM SDK负责 ACK、重连、离线消息和去重，最近会话及高级同步由对应 SDK或业务 DataSource补齐。媒体采用预签名、对象存储 PUT、完成确认、绑定 WuKong消息四阶段链路；音视频房间及短期 Token由业务 API创建，媒体由 LiveKit传输。

## 验证

```bash
fvm flutter analyze
fvm flutter test
fvm flutter build ios --simulator --no-codesign
fvm flutter build apk --debug
flutter widget-preview start
```

正式四端构建统一入口见 [Flutter 四端构建入口](../../docs/FLUTTER_BUILD_AUTOMATION.md)。

测试覆盖登录退出、四端会话/消息映射、并发 401 单飞续签、WuKong 连接状态、引用消息、媒体上传顺序、图片编辑、小视频、群聊/群通话页面和深浅主题。Windows/Skia 像素基线另覆盖移动登录、会话、群聊、桌面双栏和图片编辑器；基线使用固定消息时间与显式字体加载，只有确认设计变更后才使用 `fvm flutter test test/visual_regression_test.dart --update-goldens` 更新。当前 GitHub Actions 完整任务结果为 376 项通过、21 项按环境跳过。生产范围与外部验收门槛见 `PRODUCT_SCOPE.md`。
