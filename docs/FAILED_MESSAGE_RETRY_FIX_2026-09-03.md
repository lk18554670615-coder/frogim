# 失败消息重发修复（2026-09-03）

## 原因与修改

- WuKong 发送适配层会将找不到正在发送任务的回执长期暂存。原生 SDK 对已失败消息的重复刷新也会发出回执，下一次使用相同 `clientMessageId` 重发时可能立即消费上一次失败结果。
- 现在只在实际调用网关、等待取得本次消息期间暂存提前到达的回执；有发送序号时同时匹配序号与消息标识。原生适配器仅为当前发送集合发布发送结果，普通刷新不再伪装为新回执。
- 重发读取当前消息状态，并按消息标识防并发；复用原幂等键、不新增本地消息行。已成功、正在发送、非本人、已过期或已退出账号的消息不能通过旧回调再次发送。
- 失败原因保留在消息下方，操作明确为“重新发送”；普通文本发送失败不再排队弹出 SnackBar。重复失败事件不反复查询禁言原因，迟到的失败/发送中快照不覆盖已确认的结果。
- 未完成上传且没有本地路径的文件保留内存内容，供本次会话重发；已完成上传的图片、语音、视频、文件复用媒体 ID，并重新绑定授权地址，不再次上传或要求原文件仍存在。
- 保留禁言/只读限制及原来的媒体网络中断后自动恢复一次。失败不等于可绕过服务端权限。

## 验证

环境：Windows、PowerShell 7.6.4、FVM Flutter 3.44.8。

修复前新增用例成功复现：第二次发送返回 `failed` 而非 `sending`；已上传媒体重发没有获取有效媒体地址。修复后均通过。

以下 9 个测试文件共 **176 项通过**：

```powershell
fvm flutter test --no-pub test/live_repository_test.dart test/group_send_feedback_test.dart test/widget_test.dart test/business_channel_send_policy_test.dart test/group_receipt_visibility_test.dart test/large_group_members_test.dart test/group_member_directory_test.dart test/message_feedback_test.dart test/wukong_gateway_contract_test.dart --reporter expanded --timeout 60s
```

覆盖旧回执重复与乱序、成功后旧入口、连续点击、四种媒体重发、已上传文件、缓存刷新、账号退出、禁言、断线恢复一次，以及 390/1280 宽度的真实组件点击和无失败弹窗。

本轮为本地单元/组件测试与模拟网关验证，没有连接生产服务器发送测试消息，没有真机联调，也没有提交、推送、打包或发布。此前群成员修复保留不变。

限制：如果消息尚未上传、应用已退出且原始文件也不存在，无法凭空恢复附件；仍需重新选择文件。
