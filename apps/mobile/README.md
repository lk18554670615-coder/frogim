# 邻里通讯 Flutter 客户端

高完成度 Flutter 即时通讯客户端，默认可在无后端环境下使用内置 Demo，配置服务地址后自动启用 REST + WebSocket 实时通讯。

## 运行

```bash
flutter pub get
flutter run
```

Demo 登录可直接点击「一键进入安全演示」，或使用任意有效手机号和至少 4 位验证码。演示消息通过 `shared_preferences` 轻量持久化。

连接真实服务端：

```bash
flutter run \
  --dart-define=APP_ENV=staging \
  --dart-define=API_BASE_URL=http://127.0.0.1:8080 \
  --dart-define=WS_URL=ws://127.0.0.1:8080/v1/ws
```

个推本地参数放在已忽略的 `dart_defines.local.json`，运行时追加
`--dart-define-from-file=dart_defines.local.json`。发布构建必须同时提供
`GETUI_ENABLED=true`、`GETUI_APP_ID`、`GETUI_APP_KEY` 和
`GETUI_APP_SECRET`；`MasterSecret` 只允许配置在服务端环境变量中。

开发后端验证码由部署环境配置。`staging` 和 `production` 缺少服务地址或启用 Demo 时会直接启动失败；`production` 还强制要求 HTTPS/WSS。开发构建未配置服务地址时可显式进入 Demo，真实请求失败时不会自动伪装成 Demo 成功结果。

## 分层

- `lib/core`：环境配置、模型、主题与应用状态机。
- `lib/data`：仓储接口、可持久化 Demo、REST/WebSocket Live 实现和容错切换。
- `lib/ui/screens`：登录、消息、联系人、搜索、聊天、群组、隐私和安全流程。
- `lib/ui/widgets`：头像、搜索、状态面板、设置项、消息回执等设计组件。
- `lib/previews.dart`：Flutter Widget Previewer 的浅色/深色页面与关键组件预览。

Live 层使用 REST + `/v1/ws?ticket=`。连接前通过受保护的 `POST /v1/ws/ticket` 换取 30 秒、一次性实时连接票据，长期 Access Token 不进入 URL。受保护 REST 请求遇到 401 时单飞刷新凭据，并只重放原请求一次；WebSocket 鉴权过期会刷新后重连，普通断线按 1、2、4、8、16、32 秒退避。文本、引用和媒体消息均使用客户端 ID 幂等发送；媒体采用预签名、对象存储 PUT、完成确认、发送消息四阶段链路。

## 验证

```bash
flutter analyze
flutter test
flutter build ios --simulator --no-codesign
flutter build apk --debug
flutter widget-preview start
```

测试覆盖登录退出、会话和消息状态、并发 401 单飞续签、WebSocket 凭据失效重连、引用消息、媒体上传顺序、多选交互、群聊页面和深浅主题。生产范围与外部验收门槛见 `PRODUCT_SCOPE.md`。
