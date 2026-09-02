# 群聊回执按角色显示

日期：2026-09-02。环境：Windows / PowerShell 7.6.4、FVM Flutter 3.44.8。

## 实现

- 群主和管理员保留现有群消息回执：最新一条本人发送的消息显示“已送达 X · 已读 Y”；不新增回执详情页或未读人数计算。
- 普通成员不显示回执人数、“已读”或“已送达”。已确认的发送状态统一展示“已发送”，保留发送中、上传进度、失败及重试。
- 私聊送达/已读状态、本人会话未读红点和未读消息数保持不变。
- 统一通过控制器当前会话角色判断；未知角色、断线或角色重新同步期间先隐藏，角色确认后恢复。降级时立即替换语音状态动画子树，避免旧人数在淡出动画中继续可见。
- 不修改回执模型、缓存、ACK 或消息已读上报，不改服务器、数据库或 WuKongIM 协议。这是客户端展示限制，原始回执仍会同步到普通成员设备；不是服务端数据隔离。
- Android、iOS、Web、macOS 共用这套 Flutter 展示逻辑。保留此前未提交的撤回、群提示、备注优先及其他修改。

## 自动化验证

- 新增 `group_receipt_visibility_test.dart`，17 项通过：角色/可信状态、390px 浅色与 1280px 深色、发送状态及重试、私聊不变、无授权群气泡、最新消息回执、角色降级/晋升第一帧行为、回执缓存继续更新，以及自己的未读计数不变。
- 联合运行 7 个测试文件，共 129 项通过：`group_receipt_visibility_test.dart`、`release_contracts_test.dart`、`group_message_policy_test.dart`、`friend_display_name_test.dart`、`message_collaboration_widget_test.dart`、`widget_test.dart`、`initial_chat_scroll_test.dart`。
- `flutter analyze --no-pub`：无问题。
- 语音测试仅模拟平台播放器初始化，用于验证状态展示和动画，不代表实际录音/播放验收。

## 交付范围

未在真实设备或生产环境执行验证，未打包、提交、推送、部署或修改更新策略。已安装客户端需更新后生效。
