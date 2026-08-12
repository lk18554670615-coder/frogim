# 邻里通讯 Flutter 客户端

高完成度 Flutter 即时通讯客户端，默认可在无后端环境下使用内置 Demo；连接真实环境后使用业务 REST、WuKongIM 消息链路和 LiveKit 音视频。

## 运行

```bash
fvm flutter pub get
fvm flutter run
```

Demo 登录可直接点击「一键进入安全演示」，或使用任意有效手机号和至少 4 位验证码。演示消息通过 `shared_preferences` 轻量持久化。

连接真实服务端：

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
`--dart-define-from-file=dart_defines.local.json`。发布构建必须同时提供
`GETUI_ENABLED=true`、`GETUI_APP_ID`、`GETUI_APP_KEY` 和
`GETUI_APP_SECRET`；`MasterSecret` 只允许配置在服务端环境变量中。

开发后端验证码由部署环境配置。`staging` 和 `production` 缺少服务地址或启用 Demo 时会直接启动失败；`production` 还强制要求 HTTPS。WuKong TCP/WSS 和 LiveKit 地址只接受鉴权后的 `ImSession`/通话接口下发，不允许由客户端构建参数覆盖。开发构建未配置服务地址时可显式进入 Demo，真实请求失败时不会自动伪装成 Demo 成功结果。

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

密钥文件和密码不得提交到仓库；商店发布前应将上传密钥纳入组织级密码库和离线备份。

## 分层

- `lib/core`：环境配置、模型、主题与应用状态机。
- `lib/data`：业务仓储、WuKongIM 平台 Gateway、消息映射、本地会话缓存和可持久化 Demo。
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

测试覆盖登录退出、四端会话/消息映射、并发 401 单飞续签、WuKong 连接状态、引用消息、媒体上传顺序、图片编辑、小视频、群聊/群通话页面和深浅主题。Windows/Skia 像素基线另覆盖移动登录、会话、群聊、桌面双栏和图片编辑器；基线使用固定消息时间与显式字体加载，只有确认设计变更后才使用 `fvm flutter test test/visual_regression_test.dart --update-goldens` 更新。当前全量共 173 项测试。生产范围与外部验收门槛见 `PRODUCT_SCOPE.md`。
