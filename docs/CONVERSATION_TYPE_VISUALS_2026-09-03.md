# 单聊与群聊视觉区分（2026-09-03）

## 修改

- 会话列表与聊天顶部使用共用的 `ConversationAvatar`、`ConversationTitle` 和 `ConversationTypeBadge`。
- 只有普通群聊显示“群聊”标签及绿色底、白色多人图标；依据 `Conversation.kind` 和 `isBusinessChannel` 判断，不根据名称或成员数猜测。
- 保留原头像、列表 48dp / 顶部 34dp 尺寸、绿色主题、单聊在线圆点、资料点击、正在输入、成员数及原有操作。
- 类型标签在名称之后，长名称省略、标签保持可见；角标不单独播报，会话无障碍描述包含类型。
- 在内容区域宽度不足 270dp 且字体放大时，普通群聊的置顶提示保留图标与无障碍说明，收起重复的“置顶”文字，避免群名被挤没。普通字号与单聊布局不变。
- 没有修改通用个人头像、转发选择、联系人、消息发送者、服务端或数据结构，没有新增依赖或网络请求。

## 验证

环境：Windows、PowerShell 7.6.4、FVM Flutter 3.44.8。

**160 项相关测试通过**：

```powershell
fvm flutter test --no-pub test/conversation_identity_test.dart test/widget_test.dart test/user_presence_pages_test.dart test/business_channel_send_policy_test.dart test/group_send_feedback_test.dart test/group_receipt_visibility_test.dart test/large_group_members_test.dart test/group_member_privacy_invite_test.dart test/initial_chat_scroll_test.dart --reporter expanded --timeout 60s
```

覆盖同名/同头像、业务频道排除、头像尺寸与单聊在线状态、无障碍类型、320/390 像素列表、两倍字体、深浅主题、1280 像素窗口内 360 像素聊天列、正在输入恢复，以及之前的群成员、重发和滚动修复。

**5 项截图对比通过并人工查看**：手机会话列表、手机群聊、桌面会话工作台、深色大字体群聊、窄屏大字体列表。后三种尺寸/样式之外没有批量更新无关截图。

```powershell
fvm flutter test --no-pub test/visual_regression_test.dart --name 'mobile conversations visual baseline|mobile group chat visual baseline|desktop conversation workspace visual baseline|group identity' --reporter expanded --timeout 60s
```

本轮仅修改和验证客户端共享代码；未执行真机测试、提交、推送、打包或发布。现有群成员与失败消息重发的未提交修改保持保留。
