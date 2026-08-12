# 测试指南

所有命令默认从仓库根目录执行。测试分为快速检查、真实依赖集成、客户端构建和部署冒烟四层。

## 快速检查

```bash
(cd server && go test ./... && go vet ./...)
(cd apps/admin && npm ci && npm run lint && npm test -- --run && npm run build)
(cd apps/mobile && fvm flutter pub get && fvm flutter analyze && fvm flutter test)
infra/scripts/check-docs.sh
bash infra/scripts/verify-wukong-flutter-patch.sh
bash -n infra/scripts/*.sh
bash infra/scripts/test-backup-metrics.sh
bash infra/scripts/test-offsite-backup.sh
```

Flutter 像素回归基线单独执行：

```bash
(cd apps/mobile && fvm flutter test test/visual_regression_test.dart)
```

当前仓库保存 Windows/Skia 专用基线，覆盖移动登录、移动会话、群聊、桌面双栏和发送前图片编辑器。测试显式加载 Noto Sans SC、Material、Cupertino 和编辑器图标字体，并固定消息时间；非 Windows 主机跳过这些平台专用图片，不能据此宣称 iOS/macOS 已完成像素验收。只有人工确认设计变更后才允许使用 `--update-goldens` 更新图片，普通回归不得带该参数。

`test-offsite-backup.sh` 使用隔离的临时目录和假 Docker 边界，验证只有带 `_COMPLETE`、校验清单和当前固定依赖锁的异地代次才能原子发布；损坏代次必须保留为不可恢复的 `.offsite-download-*`，HTTPS/对象前缀不安全的配置必须在访问网络前被拒绝。真实外部桶、对象锁和跨地域恢复仍需要在选定供应商后单独演练。

服务端并发和数据竞争检查：

```bash
(cd server && go test -race ./...)
```

## 真实 PostgreSQL 集成

先启动本地依赖，再显式提供测试连接串：

```bash
docker compose -f infra/compose.yaml up -d postgres redis minio
(
  cd server
  IM_TEST_DATABASE_URL='postgres://nexachat:nexachat@127.0.0.1:5432/nexachat?sslmode=disable' \
    go test ./internal/store -count=1
)
```

测试使用随机 ID，并清理自己的数据。不得把生产连接串传给 `IM_TEST_DATABASE_URL`。

## 本地整栈冒烟

```bash
cp .env.example .env
docker compose -f infra/compose.yaml -f infra/compose.wukong.yaml up -d --build
infra/scripts/smoke-local.sh
docker compose -f infra/compose.yaml -f infra/compose.wukong.yaml ps
```

通过标准：API `/ready`、后台 `/healthz` 正常；后台安全响应头存在；未登录管理接口返回 401；所有依赖容器健康。

Android Studio 模拟器端到端测试使用 `10.0.2.2` 访问宿主机。启动本地栈时必须追加 `infra/compose.android-emulator.yaml`（或执行 `make infra-up-android-emulator`），否则业务 API 会向原生 SDK 下发仅宿主机可达的 `127.0.0.1:5100`，并签发仅宿主机可达的 `127.0.0.1:9000` 媒体地址，造成消息发送或附件加载失败。应用的 `API_BASE_URL` 同样设为 `http://10.0.2.2:8080`。该覆盖禁止用于生产。

## 本地真实产品链路验收

整栈启动后执行：

```bash
make acceptance-local
```

脚本通过真实 HTTP、PostgreSQL、Redis、MinIO、WuKongIM 与 LiveKit 链路验证注册、资料、搜索、好友申请、单聊、消息幂等、回复、已读、撤回、定时消息创建/修改/取消/真实派发、群聊、群公告、黑名单、举报、朋友圈审核隐藏/恢复、表情商店、社区/话题/资讯/直播、直播互动精确事件白名单、客服自动分配及排队/后台认领/转接/结束/评价、Android/iOS/Web/macOS 版本升级策略、LiveKit单聊及三人群视频状态机/短期Token/后台房间/结束清理、反馈和账号注销。临时账号在成功或异常结束时都会由清理钩子注销；该脚本只允许指向本地开发环境，严禁传入生产地址。

流式消息改动至少执行以下定向回归：

```bash
(cd server && go test ./internal/wukong ./internal/httpapi -run 'Stream|MessageEvent')
(
  cd apps/mobile
  fvm flutter test test/wukong_event_packet_test.dart \
    test/wukong_gateway_contract_test.dart \
    test/live_repository_test.dart \
    test/business_repository_test.dart
)
bash infra/scripts/verify-wukong-flutter-patch.sh
```

测试必须覆盖事件幂等、事件先于锚点、锚点占位文案被首个delta替换、连续delta串行合并、snapshot后复用事件序号的delta、历史快照恢复、非法非文本/close/finish载荷拒绝和无权用户拒绝；只构造 Dart 对象不能替代二进制 EventPacket 帧测试。二进制测试必须使用字面量帧类型12，并覆盖协议v2–v4的`flag/string streamNo/uint64 streamId`旧流字段，不能从被测枚举反向生成全部期望值。固定提交上的可审计补丁现已通过真实容器`delta → snapshot → delta → close`回归，最终终态快照严格为`replacement after`且不再混入snapshot前的旧delta；`server/internal/wukong/patched_server_integration_test.go`同时验证类型5创建、订阅者和白名单。

WuKongIM服务端补丁使用`make verify-wukong-server-patch`验证：原始源码包、补丁和Dockerfile均有SHA-256锁，Linux构建阶段运行`wkcache`、`internal/plugin`、`internal/api`、`internal/eventbus`和`internal/user/handler`测试并编译固定amd64服务端。真实容器回归需显式提供`IM_TEST_WUKONG_PATCH_URL`与`IM_TEST_WUKONG_MANAGER_TOKEN`，默认测试不会连接外部服务。补丁候选镜像还必须触发重复主设备登录，并断言日志保留连接诊断但不出现Token、AES Key/IV、消息签名或验签摘要。

本地固定栈可在Windows运行`powershell -ExecutionPolicy Bypass -File infra/scripts/test-wukong-sensitive-logs.ps1`，在Linux/CI运行`bash infra/scripts/test-wukong-sensitive-logs.sh`。该门禁会重新构建协议探针、以同一App主设备建立两条真实加密TCP连接、确认旧连接收到`ReasonConnectKick`，随后读取未经采集器处理的WuKongIM容器原始日志；日志必须保留`close old conn for master`，同时不得包含探针Token/消息正文金丝雀或AES/签名校验字段。

Web目标端的官方JS SDK连接、EventPacket和断网恢复使用无第三方Node依赖的真实浏览器探针：

```bash
make wukong-web-probe
# 指定浏览器或留存脱敏JSON证据：
make wukong-web-probe WEB_PROBE_ARGS="--browser /path/to/chrome --output build/qa/wukong-web-probe.json"
```

探针只允许用于已启动的开发/测试栈：它创建两个临时开发账号和好友关系，在隔离浏览器配置目录中加载固定`wukongimjssdk 1.3.5`，验证WSS初连、流锚点和实时delta，模拟网络中断后要求10秒内恢复，再验证恢复后的新消息与EventPacket。输出不包含Access Token、IM Token、手机号或用户ID；不得把生产OTP或生产API传给该工具。

Easy SDK桌面传输探针执行：

```bash
(cd apps/mobile && fvm dart run tool/wukong_easy_probe.dart)
```

它使用业务API签发的macOS会话与Desktop device flag，真实验证服务端到SDK普通消息、SDK发送ACK、绝对过期时间回退以及`customEvent`接收`stream.delta`。该跨平台Dart传输验证不替代macOS目标机的应用打包、钥匙串、网络权限、前后台与界面验收。

LiveKit媒体门槛使用`infra/scripts/livekit-load-test.sh`和固定官方CLI摘要。默认创建10个房间，每房8个medium（640×360/20fps）非simulcast发布者和1个订阅者，并使用`3x3`布局。验收必须同时满足活跃快照9/9、8/8轨、零订阅错误和零丢包；默认speaker布局只选择6路，不得用于9人门槛。

## Flutter 构建与设备验收

```bash
(cd apps/mobile && fvm flutter build ios --simulator --no-codesign)
(cd apps/mobile && fvm flutter build apk --debug)
```

真机至少覆盖：注册/登录、单聊、群聊、离线同步、撤回、转发、图片/语音/视频/文件、好友与屏蔽、公告、通知点击、前后台切换、弱网恢复、权限拒绝、深浅色、文字缩放和小屏布局。APNs、Android 厂商离线通道、短信和 TURN 必须在生产同配置的外部服务上验收。

## 生产配置静态验证

```bash
make production-validate PROD_ENV=.env.production
make production-config PROD_ENV=.env.production
```

验证配置不会部署。真实部署和远端冒烟由发布负责人按 [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) 执行。

## 证据留存

每次候选版本保存：提交号、测试时间、命令输出、失败重跑说明、容器镜像 ID、迁移版本、真机型号/系统、外部推送与短信证据、备份校验值和审批人。日志与截图必须脱敏。
