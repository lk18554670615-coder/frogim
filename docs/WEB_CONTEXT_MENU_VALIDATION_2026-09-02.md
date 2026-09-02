# Flutter Web 右键菜单验证（2026-09-02）

## 改动

- 在 Web 首帧前等待 `BrowserContextMenu.disableContextMenu()` 完成。
- 不拦截鼠标次键手势；保留已有消息、会话操作及 Flutter 文字编辑菜单。
- 非 Web 平台跳过配置；不修改服务端、React 后台或发布状态。

## 验证

- `fvm flutter test test/web_accessibility_bootstrap_test.dart test/desktop_workspace_test.dart test/initial_chat_scroll_test.dart`：35 项通过。
- `fvm flutter test --platform chrome --timeout 60s test/web_context_menu_browser_test.dart`：1 项通过，真实 DOM `contextmenu` 事件的 `defaultPrevented` 为 `true`。
- 本次改动的 4 个 Dart 文件通过 `flutter analyze`。

## Windows 测试环境说明

使用项目固定的 Flutter 3.44.8 和本机独立无头 Chromium。该 Flutter 版本的浏览器测试器在 Windows 下将 URL 转为反斜线路径，导致子目录测试选择错误、CanvasKit 路由返回 404。本次测试放在 `test` 根目录，并临时将 SDK `bin/cache/flutter_web_sdk/canvaskit/chromium` 中的 `canvaskit.js`、`canvaskit.wasm` 复制到 `test/canvaskit/chromium`，由测试器静态目录提供；验证后清除这两份临时文件。未修改 Flutter SDK，未将 CanvasKit 测试资源加入 Git。

浏览器用例临时启用引擎平台通道，避免 Flutter 测试环境默认忽略通道消息；测试结束恢复原环境和原生菜单。

未提交、推送或部署。线上生效需重新构建并发布 Web 客户端。
