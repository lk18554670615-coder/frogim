# 前台新消息提示音与振动修复（2026-09-03）

## 定位结果与范围

原 `AppController` 收到 WuKong 新消息只合并列表、未读数、回执和缓存，没有调用前台提示音或振动。通知设置的音效开关此前主要上传为离线推送偏好，不能让在线 IM 消息自动响铃。

本次补齐 Android/iOS 前台提醒，不修改服务器、消息协议、数据库、Getui/APNs 后台通知模板或 Web/macOS 浏览器通知。保留工作区已有在线状态、群公告等未提交修改；未提交、推送、发布或调整强制更新。

## 实现

- `MessageFeedback` 复用 `settings.notification.enabled/sound/vibration`，音效独立可控，读取设置失败或平台播放失败不影响消息处理及持久化。
- 本次打开的 App 接收其他用户的新消息时提醒，包括正在查看的聊天；会话资料未确认、免打扰、通话进行中、自己发送、撤回、过期、系统/截屏提示和无权读取的历史不提醒。
- 只对连接完成后的新收包启用提醒。显式补拉历史标记 `historySync`；SDK 延迟消息以服务器时间与当前时间相差不超过 30 秒过滤，因此设备系统时钟需要准确。ACK、SDK 刷新和已存在消息不重复提醒。
- 当前账号内去重，保留至多 1000 个去重键；900ms 内多条消息合并为一次提醒。后台不生成第二份本地通知。退出、换号、切后台、销毁时使待执行提醒失效。
- Android 使用通知用途的系统铃声及 160ms 振动，检查系统通知权限、静音、勿扰、现有 `messages` 通知渠道设置和录音/通话状态；不创建新渠道绕过静音设置。
- iOS 使用公共 System Sound Services、0.24 秒自制 PCM WAV 和系统振动，检查前台及通知授权/声音许可，不修改语音录制和通话使用的音频会话。
- 音频无第三方素材，生成脚本为 `tools/generate-message-tone.ps1`。文件 10628 字节，SHA-256：`3C994E7F9A0CD9713268C214EBA3030A32C46A12FE94018E2B412E486BA69675`。
- 通知设置说明补充手机前台提醒和系统限制；界面布局保持不变。

## 已执行验证

环境：Windows，PowerShell 7.6.4，FVM Flutter 3.44.8 / Dart 3.12.2。

- 15 个 Flutter 测试文件合计 **229 项通过**，包含新增提醒测试及通知设置、消息映射、聊天滚动、公告、在线状态、回执和 iOS 返回手势回归。
- 新增测试覆盖独立音效开关、总开关、后台/退出/换号/销毁的异步失效、重复消息和突发消息合并、平台通道参数、单聊和群聊、免打扰、历史权限、历史补拉与实时事件区分、播放失败不影响持久化。
- iOS WAV 通过 Flutter 资源包实际加载，校验 WAV 头、PCM 格式、采样率和长度。
- Android `:app:compileDebugKotlin`：**BUILD SUCCESSFUL**，现有 Getui 废弃 API 和 Gradle 弃用警告不影响编译。
- 最终 `fvm flutter analyze --no-pub`：**No issues found**。
- `git diff --check` 通过。

复跑命令（工作目录 `apps/mobile`，PowerShell）：

```powershell
fvm flutter analyze --no-pub
fvm flutter test --no-pub test/message_feedback_test.dart test/live_repository_test.dart test/settings_screens_test.dart test/group_announcement_test.dart test/group_message_policy_test.dart test/group_administrators_test.dart test/group_send_feedback_test.dart test/message_mapper_test.dart test/message_content_registry_test.dart test/structured_messages_repository_test.dart test/initial_chat_scroll_test.dart test/user_presence_test.dart test/user_presence_pages_test.dart test/group_receipt_visibility_test.dart test/ios_chat_navigation_test.dart --reporter expanded --timeout 60s
```

## 未执行与交付边界

- ADB 未发现连接的手机或模拟器，**没有听音或感受振动的真机验收**。
- Windows 无 Xcode，**未编译或运行 iOS 原生修改**。Flutter 的 iOS 平台通道测试不是 iOS 真机测试。
- 没有发布 APK/IPA、安装客户端或修改生产配置。本次修复需要重新打包客户端。
- 后台/杀进程后的 Getui/APNs 到达、声音和振动仍须在真实设备独立验收；Android 已有静音通知渠道、系统静音/勿扰、iOS 专注模式、授权状态和硬件能力须同时检查。未重置用户系统偏好。
- 仅做 IM 收包去重，尚未真机验证离线推送恰好与回到前台的 IM 收包重叠时的跨链路提醒效果。

建议真机验收：测试账号互发单聊及群消息，分别处于会话内、会话外、后台和锁屏；切换声音/振动/免打扰/系统通知权限；验证录音和通话不受干扰。只使用明确的测试会话。

## 原生 API 依据

- [Android Ringtone](https://developer.android.com/reference/android/media/Ringtone)：系统铃声和 AudioAttributes。
- [Android NotificationManager](https://developer.android.com/reference/android/app/NotificationManager)：通知渠道与中断过滤状态。
- [Android Haptics APIs](https://developer.android.com/develop/ui/views/haptics/haptics-apis)：振动用途和系统策略。
- [Apple AudioServicesPlayAlertSound](https://developer.apple.com/documentation/audiotoolbox/audioservicesplayalertsound(_:))：短提示音和振动。
- [Apple Core Audio Essentials](https://developer.apple.com/library/archive/documentation/MusicAudio/Conceptual/CoreAudioOverview/CoreAudioEssentials/CoreAudioEssentials.html)：System Sound Services 的适用范围和音频会话限制。
