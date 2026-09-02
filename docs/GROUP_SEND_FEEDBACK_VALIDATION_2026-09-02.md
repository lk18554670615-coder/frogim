# 群禁言发送反馈验证（2026-09-02）

## 修改范围

- Flutter 普通群聊读取既有群资料和群成员接口，按当前用户的个人禁言及全员禁言状态显示输入区提示。
- 个人禁言：`你已被禁言，暂时无法在该群发送消息`。全员禁言：`群聊已开启全员禁言，仅群主和管理员可发言`。
- 个人禁言优先；全员禁言豁免群主、管理员，与当前服务端校验一致。
- 群资料/成员 CMD 和重连触发状态刷新；收到解禁状态或本地禁言时限到期后恢复输入，保留文字草稿。
- 文本、语音和附件失败保存本地错误提示；异步失败回执也补齐提示。权限查询失败时不凭空判断为禁言。
- `sendError` 仅用于客户端本地缓存及展示，不进入 WuKongIM 消息正文；重新发送或成功状态清除旧错误。
- 保留已有失败重试、业务频道权限、绿色主题及聊天布局。未修改服务端、数据库或禁言权限规则。

## 原因

原聊天页的发言限制仅覆盖部分业务频道，普通群没有禁言提示；发送流程丢弃异常原因，失败 ACK 也没有转换成可读提示。因此用户只能看到发送失败状态。

协议错误码按仓库固定版本 `server/internal/wukong/policy.go` 对齐。11/13 是一般权限拒绝，不能直接当作个人禁言；群发送失败后结合现有接口返回的实际状态显示原因。

## 自动化验证

环境：Windows PowerShell 7.6.4，FVM Flutter 3.44.8 / Dart 3.12.2。

以下 10 个测试文件共 **131 项通过**：

- `group_send_feedback_test.dart`：个人/全员禁言、角色豁免、到期边界、同步及延迟失败回执、语音/附件、错误缓存、重试、网络故障降级、手机/桌面解禁及草稿恢复、窄屏大字体提示。
- `message_mapper_test.dart`：ACK 原因映射、本地错误不进入消息正文及既有消息类型映射。
- `live_repository_test.dart`：真实业务 CMD 结构触发权限刷新；其他仓库契约回归。
- `group_member_mute_test.dart`、`business_channel_send_policy_test.dart`。
- `message_mutation_failure_test.dart`、`group_receipt_visibility_test.dart`。
- `desktop_workspace_test.dart`、`initial_chat_scroll_test.dart`、`ios_chat_navigation_test.dart`。

测试日志位于 `apps/mobile/build/group-send-feedback-regression.log`（构建目录，不提交）。测试环境中的设备信息上报提示为插件未注册后的正常降级，不是群发送测试失败。

本次涉及的 9 个 Dart 文件静态检查通过（`No issues found`）；已跟踪修改的 `git diff --check` 通过。

## 交付边界

- 本轮为单元、组件及模拟网关契约测试，未执行真实手机或线上群禁言操作。
- 未提交、推送、构建安装包、部署或修改更新策略。线上效果需要发布更新后的客户端；Web 需重新构建发布。
- 此前未提交的其他功能修改保持保留。
