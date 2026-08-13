# Flutter 首发生产就绪审计（2026-08-09）

## 结论

Flutter 客户端已经达到“首发代码候选版”状态。2026-08-13增量复核中，静态检查、194项自动化测试、生产Web构建、Android Debug构建以及当前提交的Android Release APK/AAB均已验证；生产配置和签名缺失时会主动失败，不再静默生成Demo或Debug签名的正式包。

当前仓库仍不能直接宣称“商店正式版已发布”。当前Android包已使用独立4096位RSA发布密钥和目标服务器地址构建，APK v2/v3签名验证通过，不再是Debug证书；但密钥归属/异地备份、法务页面、iOS/macOS签名、生产推送/短信/对象存储凭据及实体真机验收仍属于发布方必须完成的外部条件。

当前应用版本为 `1.0.0+1`，Android applicationId 为 `com.linlitong.imapp`。发布前需要由产品/商店账号所有者确认这两个身份信息不会再修改。

## 本轮已修复的正式版本问题

### 环境与演示数据

- Release 未提供 `APP_ENV` 时默认按 `production` 校验，避免漏传参数后生成 Demo 包。
- 正式环境强制同时提供 `API_BASE_URL`、`WS_URL`，并要求 HTTPS/WSS；禁止 `ENABLE_DEMO=true`。
- Demo 仓库只在允许 Demo 的环境实例化，生产 AOT 可移除无用演示逻辑。
- 新增纯函数配置测试，覆盖安全生产配置、Demo/明文地址、法律页面缺失、非法环境和媒体大小边界。
- 统一并记录正确的 Dart Define 名称。此前模拟器命令误用了 `IM_API_BASE_URL`、`IM_WS_URL`、`IM_DEMO_MODE`，因此当时展示的是 Demo；正确名称是 `API_BASE_URL`、`WS_URL`、`ENABLE_DEMO`。

### 合规、隐私与版本信息

- 移除登录页和“关于”页中“仅占位、不可作为正式条款”的假法律文本。
- 新增 `TERMS_URL`、`PRIVACY_URL`，生产环境必须是有效 HTTPS 地址；应用通过系统浏览器打开法务审核后的页面。
- “关于”页改为读取安装包真实版本，不再硬编码版本号。
- Flutter locale 与实际文案保持一致，首发仅声明 `zh_CN`，不再虚假声明未翻译的英文界面。
- Android 禁止系统备份应用数据；iOS 声明不使用受限加密出口能力。

### Android 安全与构建

- Release 构建必须外部注入 `RELEASE_STORE_FILE`、`RELEASE_STORE_PASSWORD`、`RELEASE_KEY_ALIAS`、`RELEASE_KEY_PASSWORD`；任一缺失或配置不完整都会立即失败。
- 主清单强制 `usesCleartextTraffic=false`，并覆盖第三方 SDK 的宽松声明；只有 Debug 清单为本地 HTTP 联调显式放开明文流量。
- 启用 R8 默认优化规则，并为依赖中不可达的 Apache Tika XML 分支添加精确 `dontwarn`，Release 构建已通过。
- `.jks`、`.keystore` 和本地签名配置被 Git 忽略，避免上传密钥进入仓库。
- `USE_FULL_SCREEN_INTENT`、`MANAGE_OWN_CALLS` 等敏感权限仍保留，用于真实来电能力；上架时必须在商店后台完成用途声明与审核材料。

### iOS、macOS、Web 与部署

- macOS Release/Debug Profile 增加网络客户端、相机、麦克风、位置和用户选择文件权限，Info.plist 增加对应用途说明。
- Web manifest 和 HTML 描述改为正式产品说明，移除 Flutter 模板文案。
- Web Docker/Compose 构建传递正式环境、后端、Demo 开关和法律页面参数；生产环境校验拒绝空值、非 HTTPS 和文档示例地址。
- 为 Shell、Dockerfile 和 YAML 固定仓库换行规则，避免 Windows 检出后 Bash 脚本出现 CRLF 语法故障。
- 原有产品范围与审计文档含已过时的“未实现/已移除”结论，现已标记为历史记录，避免误导发布判断。

## 验证结果

| 验证项 | 结果 |
|---|---|
| `fvm flutter analyze` | 通过，0 issue |
| `fvm flutter test --reporter compact` | 通过，194/194 |
| Android Debug APK（真实本地后端参数、Demo 关闭） | 通过 |
| Android Release APK | 通过，提交`aa00d54db67faf70fca244e7681bfe7de3478bac`、目标服务器配置，153,097,408字节，SHA-256 `d0bba6e09ae317fc9304ee3ba94451ce82a4dfe0c604d0fcd736fc413e9a7495`；两台API 35模拟器覆盖安装后保留登录态，双方均读到服务器持久化的“视频通话已拒绝”会话预览，其中一台进入会话确认同一通话记录历史正文；公网版本化地址与latest地址全量字节流校验一致 |
| Android Release AAB | 通过，与APK同为提交`aa00d54db67faf70fca244e7681bfe7de3478bac`和目标服务器配置，125,820,429字节，SHA-256 `11bdf397a94fad1159dd6afe1178607c1603ea7f7b1d77bdeaa25c347b8e15d2`；公网版本化地址与latest地址全量字节流校验一致 |
| Android Release 合并 Manifest | `allowBackup=false`、`fullBackupContent=false`、`usesCleartextTraffic=false`、非 debuggable |
| Android Release 签名验证 | APK Signature Scheme v2/v3通过；4096位RSA发布证书SHA-256为`11fcd730e1fcf1e1fcdb7947b615a51179a4794d30e59000c38a19106a43072e` |
| 缺少 Release 签名时构建 | 按设计失败并给出明确错误 |
| Flutter Web production build | 通过，WASM dry-run 通过 |
| iOS/macOS plist 与 entitlements 解析 | 5 个文件均通过 |
| `bash -n infra/scripts/*.sh` | 通过 |
| 本地、域名生产、IP 生产 Compose 配置解析 | 均通过 |

当前Android Release APK约146.0 MiB，AAB约120.0 MiB。APK增加了固定版本的Web Emoji字体资产，商店按ABI/设备拆分后的下载大小会更小，实际下载大小仍应以商店报告为准。

## 正式发布前的硬阻断项

1. 当前IP证书下的`https://` API、WuKong WSS和LiveKit信令已连通；正式发布前仍须确认长期公开入口，并验证短期IP证书或正式域名证书的自动续期。
2. 发布并经运营主体/法务审核正式用户协议、隐私政策；必须覆盖运营主体、联系方式、数据收集/共享/删除、第三方 SDK 清单和未成年人规则，并提供两个公开 HTTPS URL。
3. 由发布所有者接管当前Android上传密钥，完成安全异地备份并确认Play App Signing策略；仓库和服务器不得保存明文口令。
4. 在 macOS/Xcode 环境完成 iOS/macOS 正式签名、Provisioning Profile、Bundle ID、Associated/Push/VoIP 能力和 Archive 验证。
5. 配置并端到端验收真实短信 webhook、个推/APNs、对象存储和 LiveKit/TLS；验证码、通知到达、前后台/杀进程来电必须用真实服务验证。
6. 使用至少一台 Android 真机和一台 iPhone 完成权限拒绝/恢复、相机/相册/文件、语音/视频通话、弱网重连、后台推送、账号注销、100 MB 媒体边界和深色/大字体回归。
7. 在 Play/App Store 后台完成隐私标签、数据安全、账号删除 URL、全屏来电及电话账户敏感权限说明、内容分级和客服信息。

## 非阻断但建议首发前处理

- Demo 头像资源仍由 `pubspec.yaml` 打入生产包，约 5.2 MB；不影响正确性，但增加包体。后续可拆分 Demo flavor 或改为按需测试资源。
- 尚未接入生产崩溃收集/告警平台。至少应确定崩溃、API 错误、WuKong 重连、推送失败和 LiveKit 通话失败的可观测方案，并遵守隐私政策。
- `file_picker`、`flutter_webrtc`、`mobile_scanner`、`package_info_plus`等原生插件仍需持续跟踪Flutter/Android构建链兼容性；`flutter_callkit_incoming`已升级至3.1.5并通过Android 15前台服务、后台及安全锁屏来电回归。
- 多个直接依赖存在新主版本。首发冻结期不建议无验证地集中升级；应在独立升级周期完成迁移和真机回归。
- Windows 无法完成 iOS/macOS 编译与签名，本轮仅验证配置文件语法；这两端仍需在 macOS CI 或开发机验证。

## 正式 Android AAB 命令模板

以下变量必须替换为真实值，并通过安全环境变量或 CI Secret 注入：

```bash
export RELEASE_STORE_FILE=/secure/path/upload-key.jks
export RELEASE_STORE_PASSWORD='***'
export RELEASE_KEY_ALIAS=upload
export RELEASE_KEY_PASSWORD='***'

fvm flutter build appbundle --release \
  --dart-define=APP_ENV=production \
  --dart-define=API_BASE_URL=https://im.your-domain.example \
  --dart-define=WS_URL=wss://im.your-domain.example/im \
  --dart-define=ENABLE_DEMO=false \
  --dart-define=TERMS_URL=https://www.your-domain.example/legal/terms \
  --dart-define=PRIVACY_URL=https://www.your-domain.example/legal/privacy
```

生产材料齐全后，应重新执行本报告全部验证项，并记录最终 AAB SHA-256、上传证书 SHA-256、构建日志、商店预检查结果和真机验收单；只有那一版才是可对外交付的 `1.0.0` 正式包。
