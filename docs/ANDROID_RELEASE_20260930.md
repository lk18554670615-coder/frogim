# Android 1.0.14 强制更新发布记录

北京时间 2026-09-30 06:06:54，按用户“发布 APK 强制更新”的授权完成发布。只更新 Android 安装包及平台 Android 版本策略，无需停止服务或重复迁移账号。

## 固定资产

- 版本：`1.0.14+8020`；包名：`top.hongjinghuanqiu.app`。
- 源码提交：`5b3b366506fc6dacdbf78679ca201718efbb7c11`；发布 tag：`release/android-1.0.14-8020`。
- [正式 APK](https://18.163.165.233/downloads/qingwaguagua-im-1.0.14-8020-18.163.165.233.apk)，174,584,431 字节，支持 arm64-v8a、armeabi-v7a、x86_64；最低 Android API 24。
- APK SHA-256：`c77732f263a967d3a85ca2da06d2529597e5ff9eb7f4985637dfedf475a84857`。
- 签名证书 SHA-256：`11fcd730e1fcf1e1fcdb7947b615a51179a4794d30e59000c38a19106a43072e`，与原正式 APK 一致，v2/v3 签名验证通过。
- 平台认证地址：`https://18.163.165.233/platform`；开启原生多租户认证和个推。客户端 SDK 配置与平台应用 ID/key 相同，未包含服务端 master secret。

构建脚本的在线预检改为核对平台认证能力，并把个推 Dart 配置中的应用 ID 同步到 Android 原生清单。协议页面使用正式 `.html` 地址。提高 Android versionCode 至 8020，避免旧 ABI 分包的版本号阻止覆盖安装。

## 强制更新策略

Android 策略 revision 为 **2**：enabled=true，minimumVersion/latestVersion=`1.0.14`，forceUpdate=true，rolloutPercentage=100。安装包上传、服务器文件摘要及公网完整下载摘要验证成功后，才通过平台管理 API 发布策略。

- 旧 `/v2/config/version` 与新 `/platform/v2/config/version` 均验证：`1.0.0`、`1.0.11`、`1.0.12`、`1.0.13` 强制更新；`1.0.14` 无更新提示。
- Windows 本机再次经公网核对旧版、新版决策、APK HTTP 200、MIME 类型和文件长度。
- iOS、Web、macOS 策略保持原值。Web 继续运行 `1.0.13+4019`，不会因本次 Android 发布要求 Web 更新。
- 更新后重新登录平台；原无密码账号仍需管理员核实身份后重置，短信未配置。

## 验证证据与边界

- Flutter 静态检查无问题；68 项相关测试通过，覆盖平台 URL、认证、强制更新及下载界面、缓存隔离、迟到回调、推送与来电身份校验。
- Android release 编译、APK 资产检查、签名及原生配置核对通过。
- Android `:app:testDebugUnitTest` Gradle 检查通过；8 项原生测试复用未变化输入的已有结果（UP-TO-DATE，原执行时间 2026-09-29 11:06 UTC），不登记为本次重新执行。项目不存在 `testReleaseUnitTest`，首次调用该任务失败后改用实际存在的 Debug 单测任务。
- 独立 Android API 35 模拟器从 `1.0.11+8015` 直接覆盖安装 `1.0.14+8020` 成功；冷启动成功、进程保持存活，未发现该进程的致命异常。模拟器使用独立数据目录，检查结束后已关闭。
- `/ready`、`/app/`、`/platform/` 发布后均为 200。
- **Android 真机登录、消息、音视频通话及个推实收仍待验收**。模拟器覆盖安装和启动不替代真机验证；iOS/VoIP 原待验收状态不变。

本机证据在 `.data/android-release-20260930/`；正式包及清单在 `build/releases/android/1.0.14-8020-18.163.165.233/`。服务器持久化回执在 `/data/frogim/releases/cutover-20260930/ops/android-1.0.14-8020/`，包含发布前策略、请求、响应、构建清单及 `published.json`。凭据和含设备日志的私有材料不提交 Git。

## 后续处置

若安装包发现阻塞问题，保留版本文件和回执，通过平台管理 API 使用新 requestId、当前 revision 发布修订策略；可临时把 Android 下载入口指向已验收 Web，修复后发布更高版本 APK。不要降低原生 versionCode 试图覆盖已安装的新包，也不要恢复旧认证服务。服务器账号、数据和旧系统保留规则继续遵循切换记录。
