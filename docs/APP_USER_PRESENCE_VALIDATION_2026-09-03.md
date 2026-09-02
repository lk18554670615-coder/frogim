# App 用户在线状态：实现与验证

## 本次范围

- 好友列表、个人资料、单聊顶部、聊天信息／桌面资料面板、群成员与群管理员页使用统一状态；保留备注、角色、禁言说明及绿色主题。
- 在线显示绿色标记，离线显示中性标记；查询失败显示“状态未知”，未授权或尚未取得可信结果时隐藏标签。“正在输入”优先于单聊在线状态。
- 未修改管理后台、数据库结构、SDK、普通消息协议或版本更新策略。数据库版本仍为 60。
- 未提交、推送、打包或部署；未使用线上账号进行验证。

## 接口与权限

新增已认证接口 `POST /v2/users/presence`：

```json
{"userIds":["usr_a","usr_b"],"groupId":"本项目群会话ID"}
```

`groupId` 可省略；每批 1–200 个 ID，去重后按原顺序返回：

```json
{"items":[{"userId":"usr_a","status":"online","checkedAt":"2026-09-03T00:00:00Z"}]}
```

- `status` 为 `online / offline / unknown / hidden`。不返回连接数、设备、IP、最后活动或最后在线时间；响应禁止 HTTP 缓存。
- 本人和有效好友可见；群上下文只为该群当前群主／管理员增加查看当前成员的权限。不存在、已注销及无权限的对象统一隐藏。
- PostgreSQL 实时判权，不依赖在线状态缓存或客户端角色；上游等待结束后再次判权，覆盖等待期间的删除好友、退群与角色变化。
- 固定 WuKongIM `v2.2.5-20260422`、提交 `a888f89533d0e7d1b2030e06504ca97f1ad891d4`。依据 `internal/api/user.go`，向 `/user/onlinestatus` 发送 UID 数组，按 UID 合并多个设备结果。只有成功且格式有效的响应才将缺失用户判为离线。
- 服务端状态缓存 5 秒，上限 10000 个条目，在途 UID 也有上限；重叠查询按 UID 合并，上游并发最多 2、单批最多 200，总查询期限 5 秒。失败不回退到过期在线快照。
- 既有后台在线查询和 Webhook 投影保持不变。

## 客户端刷新

- `PresenceCoordinator` 仅保留内存数据，按登录账号与群上下文隔离；`AppUser.isOnline` 不再作为这些界面的状态来源。
- 可见路由／活动面板进入即查询，前台每 10 秒刷新；手机隐藏标签页和被覆盖页面停止订阅，桌面可见组件共享请求。
- 按上下文合并 UID，分批 200、并发最多 2；仍在请求中的目标不重复发起。快速关闭／重新打开页面也会等待原请求结束，但不接受旧页面结果。
- 切后台、页面关闭、退出账号停止刷新；返回前台和 IM 网络连接状态恢复时重新查询。
- 好友、群成员、群角色变化及重连立即清除旧观察结果并重新判权；普通新消息或会话摘要更新不引发额外在线轮询。
- 群来源个人资料显式携带群会话上下文，不复用其他群的授权结果；失败和迟到响应不能恢复旧账号的绿色状态。

## 验证结果

在 Windows / PowerShell 7.6.4、FVM Flutter 3.44.8、Go 1.26 环境执行：

1. `server`: `go test -work ./... -timeout 150s` 全量通过。外部服务依赖测试另行在下述隔离环境执行。
2. `TestPostgresPresencePermissions`：临时 PostgreSQL 17 schema 内验证本人、好友、非好友、群主、管理员、普通成员、不同群、降级、移出、拉黑、删除好友、解散与注销，全部通过；临时 schema 已清理。
3. `TestUserPresenceAuthorization`、`TestUserPresenceRechecksAfterUpstreamWait`：真实 HTTP 认证路由、200 人边界、无认证拒绝、禁止自报角色、缓存不缓存权限、在途权限收回、服务异常，全部通过。
4. WuKongIM 合约及缓存测试：多设备聚合、空列表、非法／缺字段响应、过期后故障、失败后恢复、20 个并发同 UID 合并、601 人分批、最多 2 个并发、取消等待，全部通过。
5. 真实固定镜像 `linli/wukongim:v2.2.5-20260422-linli.3-a888f895`：`tools/wukong-probe` 的 `TestPinnedPresenceMultipleDevices` 使用官方协议建立 APP 和 WEB 连接，完成“离线 → APP 在线 → APP+WEB 在线 → APP 退出但 WEB 仍在线 → 全部离线”，通过。
6. `TestPresenceRealServerAvailability` 使用生产 HTTP 客户端与缓存：容器正常运行时未连接用户为 `offline`；停止同一测试容器后为 `unknown`，通过。隔离容器及临时数据已清理，其他本机容器未修改。
7. Flutter 相关 **168 项测试全部通过**，包含在线状态新增的 17 项测试，以及好友备注、管理员操作、禁言、群历史、管理提示、会话、输入、PC 滚动、iOS 返回、账号流程等回归。
8. `fvm flutter analyze --no-pub`：无问题。

Flutter 测试文件：

```text
user_presence_test.dart                 user_presence_pages_test.dart
group_administrators_test.dart          friend_display_name_test.dart
group_send_feedback_test.dart           desktop_workspace_test.dart
initial_chat_scroll_test.dart           ios_chat_navigation_test.dart
chat_composer_test.dart                 release_contracts_test.dart
contacts_alphabet_index_test.dart       contacts_pinyin_grouping_test.dart
live_repository_test.dart               group_history_access_test.dart
group_message_policy_test.dart          auth_flow_edge_cases_test.dart
```

布局验证覆盖 390／1280 像素页面、深浅主题、页面 180% 字体与标签 200% 字体；联系人测试实际滚动到懒加载行再校验。在线接口契约分别以 Android、iOS、Web、macOS 参数验证。此处为组件和仓库自动化测试，不宣称已在四端真机安装验证。

## 上线顺序与语义

先更新服务端，再发布客户端；无需数据库迁移，旧客户端继续使用既有接口。连接状态的短缓存和 10 秒刷新意味着界面不是瞬时在线广播。任意端仍连接 WuKongIM 即在线；应用进入后台不等于断开，异常断网也需等待 WuKongIM 检测连接断开。本次不显示最后在线时间，也不新增隐身、在线排序或群在线人数。
