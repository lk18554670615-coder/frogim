# 群聊输入提示权限验证（2026-09-24）

## 行为与实现

- 普通群的输入提示仅发送给仍在本群的内部用户、群主或管理员，三种身份满足任一项即可；输入者本人不接收自己的提示。
- 普通成员仍上报输入，不再收到或缓存其他群成员的提示；群聊顶部保留成员数量。单聊和业务频道沿用原行为，在线／离线状态的内部用户权限没有变更。
- PostgreSQL 用一次查询读取当前账号、群成员和群角色，排除已封禁、注销、退群或成员资格过期的接收者；禁止无效发送者和已解散群。Redis 包装层直接委托数据库，不缓存权限。
- 先检查群总人数，再筛选有权限的接收者：500 人保留提示，501 人及以上不广播。权限查询失败不向任何人广播。
- 复用原 `typing` 接口和非持久化事件；没有数据库迁移、公开接口或协议变更。
- Flutter 在接收事件和展示时使用统一判定。内部身份、群角色失效或刷新时清理旧提示；未确认成员身份时隐藏。事件订阅绑定登录会话，登出／重新登录时不保留旧提示。
- 保留原 3 秒发送节流、4 秒停止输入和 6 秒接收端过期机制，不增加输入记录或离线提示。

## 自动化验证

- `server`: `go test ./...` 通过。未配置外部环境的集成测试按原约定跳过，不代表集成验证通过。
- `server`: `go test ./internal/app -run '^TestTyping' -count=1 -v` 通过，覆盖权限矩阵、实时撤权、角色变更、退群、不可用账号、单聊、500／501 人边界、Redis 委托及查询失败不广播。
- Flutter：以下 6 组文件合计 67 项测试通过：`group_typing_visibility_test`、`conversation_identity_test`、`chat_composer_test`、`release_contracts_test`、`user_presence_pages_test`、`group_receipt_visibility_test`。
- 额外运行登录边界与退出容错回归（`auth_flow_edge_cases_test`、`logout_resilience_test`），6 项通过；本次 Flutter 合计 73 项通过。
- 组件验证覆盖 390px 手机与 1280px 桌面、深浅主题、单人／多人输入、普通成员不上屏但仍上报、无权事件不缓存、撤权后迟到事件、角色升降级／转让／加载失败、退群、提示过期和重新登录。既有单聊、在线状态及群已读详情用例通过。
- 使用项目 FVM Flutter 3.44.8：`fvm flutter analyze` 通过，无问题；`fvm flutter build web --release` 通过（含 Wasm dry run）。
- `git diff --check` 通过。

## 实测与发布边界

- 初次本地 PostgreSQL 测试因 Docker Desktop 的 `dockerInference` socket 错误跳过。收到后续发布授权后，使用服务器上的隔离 PostgreSQL 17 临时容器补测 `TestPostgresTypingRecipients`，已通过（0.97 秒）。容器无外部网络、无端口映射、无生产数据挂载，数据目录使用 tmpfs；测试结束已移除临时容器，没有连接线上数据库。
- 尚未进行真实多账号 WuKongIM 联调和 Android／iOS／macOS 真机验证。
- 初次实施未增加版本号、提交、推送或发布。后续用户已授权发布 `1.0.12+4016`，包含服务端、Web 和 Android APK；先更新服务端，再更新客户端。旧客户端已收到的提示可能在约 6 秒过期前继续显示。
