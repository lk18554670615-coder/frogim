# PC 聊天历史滚动修复验证

## 范围

- 只改 Flutter 聊天页的滚动状态、列表布局和桌面滚动条。
- 不修改服务端、数据库、分页接口、消息协议、版本号或更新策略。
- 不启用鼠标左键拖动消息列表，保留触摸、右键菜单、文字选择和转发功能。
- 本次没有提交、推送、发布构建或部署。

## 修复

1. 使用用户滚动方向通知和拖动开始通知区分鼠标滚轮、触控板、滑块与程序化定位。点击滚动条轨道虽然内部使用 `moveTo`，也明确标记为用户操作。
2. 主动上翻立即暂停自动跟随，取消初始贴底计时器，并使排队中的贴底/搜索回调失效。不再使用 48px 的回底判定。
3. 只有用户向下滚回底部（剩余距离不超过 2px），或本人主动发送，才恢复跟随；向上移动 1px 也保持阅读状态。
4. 桌面增加 6px 可拖动滚动条，并关闭此列表的自动滚动条。判定结合平台和整个窗口宽度，480px 的桌面聊天列仍有滚动条；短内容没有额外滚动范围。
5. 历史列表使用 `CustomScrollView.center` 固定原有消息为锚点，旧消息向上扩展、新消息向下扩展。取消请求开始时保存偏移及总高度差补偿，避免懒加载高度估算、长短消息混排和等待期间滚动引起跳位。
6. 搜索定位兼容向上扩展后的负滚动坐标；切换会话、销毁页面时使旧异步定位失效。

## 自动化结果

Windows / PowerShell 7.6.4，FVM Flutter 3.44.8。

```powershell
# 在 apps/mobile 下执行
& ./.fvm/flutter_sdk/bin/flutter.bat test --no-pub --concurrency=1 `
  test/initial_chat_scroll_test.dart `
  test/message_history_pagination_test.dart `
  test/ios_chat_navigation_test.dart `
  test/desktop_workspace_test.dart `
  test/widget_test.dart `
  test/chat_composer_test.dart `
  test/message_image_preview_interaction_test.dart `
  test/message_forward_picker_test.dart `
  test/forward_conversation_sheet_test.dart

& ./.fvm/flutter_sdk/bin/flutter.bat analyze --no-pub
```

- **129 项测试通过**；静态检查 **No issues found**；`git diff --check` 通过。
- 输入覆盖真实 Flutter `PointerScrollEvent`、触控板 `PointerPanZoom*`、鼠标滑块拖动/轨道点击，不以触屏拖动代替桌面输入。
- 覆盖首次进入期间上翻、1/24/180px 上翻、连续滚动、延迟刷新、实时消息、图片尺寸补齐、窗口高度变化、回底跟随、本人发送、程序化定位不误恢复、旧定位取消。
- 覆盖历史加载中继续滚动、等高/变高消息的屏幕锚点、失败重试及连续分页。
- 回归手机触摸、iOS 侧滑返回、桌面双栏、窄列、深浅色、大字体、搜索定位、图片预览、右键菜单及多目标转发。
- 一次并发回归中的已有图片编辑器用例出现时序失败；单独重跑和以上完整串行回归均通过，未改动图片编辑器代码。

## 本地 Web 实际鼠标验证

使用独立 QA 入口 `apps/mobile/tool/chat_scroll_preview.dart`，复用真实 `ChatScreen`。所有消息为本地合成数据，按钮只注入本地事件，不连接业务服务器、不发送真实消息。该入口不被 `lib/main.dart` 引用。

```powershell
& ./.fvm/flutter_sdk/bin/flutter.bat run --no-pub -d web-server `
  --web-hostname 127.0.0.1 --web-port 53500 `
  -t tool/chat_scroll_preview.dart
```

在浏览器 1280×900 视口完成鼠标验证，并在最终代码刷新后复验：

- 初始位置 `3960.0`，距底部 `0.0`。
- 小幅滚轮上翻后位置 `3944.0`，距底部 `16.0`；新消息后仍为 `3944.0`，距底部增加为 `131.0`，没有被旧 48px 逻辑拉回。
- 3 秒延迟刷新保持历史位置；主动回到底部后，新消息继续自动跟随。
- 点击滚动条轨道后位置 `3364.0`，没有被初始定位抢回。
- 深色 480px 窄聊天列的滑块可拖动；拖动后位置 `2560.4`，再收到新消息仍保持此偏移。
- 顶部分页读取到最初 51–100 条之前的 45–50 条历史，顺序正确，没有回跳最新消息。
- 浏览器没有捕获到 error 级日志。

真实触控板硬件和 iOS 真机未在本次操作中测试；对应输入协议和返回手势已做 Flutter 自动化回归。
