# 多目标转发验证记录

日期：2026-09-02

## 实现范围

- 单条、逐条、合并及图片预览转发共用 `ForwardConversationSheet`。
- 最近会话复选；全选/取消全选仅影响当前搜索结果，保留搜索范围外的选择，不包含归档会话。
- 接收会话数量不额外设限；源消息超过 100 条明确提示，不截断。
- 确认后固定源消息、顺序、转发方式、目标及每个目标独立的批次 ID。
- `ForwardBatchTask` 最多并发 3 个目标；停止只停止排队，在途请求完成后显示结果。
- 失败和未发送目标可重试，保留原批次 ID，成功目标不重发。
- 会话退出、认证失效和页面销毁停止剩余队列。发送期间禁止返回和手势关闭。
- 服务端已返回成功时，本地缓存写入失败不会将其改为发送失败。
- 未新增依赖、服务端接口、数据库变更或后台任务；未改变聊天消息多选方式。

## 自动化结果

运行环境：Windows / PowerShell 7.6.4，项目 FVM Flutter 3.44.8。

Flutter 静态检查：`flutter analyze --no-pub`，无问题。

Flutter 相关测试共 **143 项通过**：

```powershell
& ./.fvm/flutter_sdk/bin/flutter.bat test --no-pub `
  test/forward_batch_test.dart `
  test/forward_conversation_sheet_test.dart `
  test/message_forward_picker_test.dart `
  test/structured_messages_repository_test.dart `
  test/structured_message_widget_test.dart `
  test/message_collaboration_repository_test.dart `
  test/message_collaboration_widget_test.dart `
  test/ios_chat_navigation_test.dart `
  test/chat_composer_test.dart `
  test/voice_composer_controller_test.dart `
  test/voice_composer_widgets_test.dart `
  test/voice_queue_test.dart `
  test/widget_test.dart --reporter expanded
```

关键覆盖：

- 125 个目标（含屏幕外项目）、并发上限 3、防重复确认。
- 搜索前后选择保留、全选/取消全选、归档过滤、空列表、取消不发送。
- 停止、继续、逐目标失败原因、成功不重发。
- 模拟超时及逐条部分完成，同批重试批次 ID 和源消息顺序不变。
- 401 停止队列，403 不阻塞其他目标；退出登录和销毁停止剩余请求。
- 服务端成功后缓存失败不触发重发。
- 100/101 条源消息边界，单目标旧调用兼容。
- 图片预览单目标/多目标转发，保留预览页。
- 手机 320 像素、桌面 1280 像素、深浅主题、双倍字体。
- 320 × 568、双倍字体、280 像素键盘占用的组合下可操作，无布局异常。
- 既有消息展示、聊天输入、语音和 iOS 导航回归。

服务端既有 HTTP 契约/幂等测试通过：

```powershell
& 'C:/Users/lee/.g/go/bin/go.exe' test ./internal/httpapi `
  -run TestForwardMessagesSeparateMergedAndIdempotent -count=1
```

此 Go 测试使用测试存储与 WuKong 测试运行时；Flutter 测试使用模拟仓库/测试数据，不代表真机或真实 WuKong 容器端到端验收。

## 交付边界

- 未向生产真实会话群发，未执行真机真实发送验收。
- 本次未打包、提交、推送、部署或修改版本与强制更新策略。
- 保留工作区中原有语音动效、iOS 导航和其他未提交修改。
