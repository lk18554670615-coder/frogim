# PC 单聊：对方最近登录 IP 与归属地

## 产品约定和展示

- 所有已登录 PC 用户均可查看自己参与的单聊中对方的**完整**最近登录 IP 及大致归属地，不要求后台管理员身份。
- 展示位置为聊天顶部昵称、在线状态／正在输入提示下方，沿用现有绿色主题；Android、iOS 原生端、手机网页布局、群聊和业务频道不增加展示或查询。
- “最近登录”指服务端记录的最近一次**成功签发业务会话**，可能来自手机、Web 或 macOS，不是当前实时连接 IP。失败认证不会覆盖此信息。
- 完整 IPv4/IPv6 可选择复制。窄聊天列时 IP 可水平滚动；悬浮提示包含完整 IP、归属地及来源说明。归属地仅展示离线库已有字段，不推测缺失信息。
- 进入立即查询，当前页面在前台时每 30 秒刷新；页面被覆盖、退出账号、切后台或销毁时停止。切换会话／账号后忽略旧请求的迟到响应，不持久化 IP 到用户或消息缓存。
- 没有登录记录显示“未记录”；查询失败清除旧值并显示“暂不可用”，可重试。离线库不可用时仍显示真实 IP。

## 接口和数据边界

```http
GET /v2/channels/conversations/{conversationId}/peer-login-info
Authorization: Bearer <业务访问令牌>
```

响应示例（保留地址，仅展示字段结构）：

```json
{
  "userId": "usr_peer",
  "lastLoginIp": "192.168.1.20",
  "region": {
    "status": "private",
    "version": "ip2region-v3.17.0-cd40e3a"
  }
}
```

- 服务端根据当前会话成员关系和会话类型确定对方，不接受自报用户 ID／角色，不创建会话。非成员、非单聊和不存在会话均返回 404；未认证返回 401；数据库查询失败返回 503 / `LOGIN_IP_UNAVAILABLE`。
- 响应设置 `Cache-Control: no-store`；仅投影 `lastLoginIp` 和离线归属地，不返回注册 IP、登录时间、失败尝试或完整认证日志，不改普通 `User` 模型。
- 复用 PostgreSQL `im_user_access_profiles` 和现有固定版本 ip2region，无新增表、迁移、依赖、外部查询服务或 WuKongIM 协议改动。
- 平台限制是客户端展示规则，并非 API 的安全边界；任何通过业务认证且属于该单聊的调用者都具有此查询权限。
- `infra/legal/privacy` 补充向普通单聊对方展示完整 IP 的说明，发布功能时应同步发布政策页面。

## 验证记录

环境：Windows / PowerShell 7.6.4、Flutter 3.44.8、Go 1.26；未连接生产数据库。

- Go `internal/httpapi`、`internal/app`、`internal/ipregion` 测试通过。
- 新增 HTTP 回归：普通单聊双向查询、无关用户／群聊／未认证拒绝、仅查询对方、IPv4-mapped IPv6 规范化、完整 IPv6、无记录、数据库失败、不返回注册 IP／历史及不缓存。
- 隔离 PostgreSQL 17 + 实际认证采集 + 固定离线库：成功登录记录可读取；失败登录不覆盖；双向单聊查询和真实归属地成功；删除测试会话成员关系后立即拒绝查询。原后台 IP 功能回归仍通过。
- 固定 IPv4/IPv6 XDB 校验和真实查询、损坏数据库降级测试通过。
- Flutter 新增 12 项测试：数据映射、认证接口契约、30 秒刷新、失败重试、前后台／路由切换、会话／账号隔离、PC／手机展示边界、窄列大字体和深浅色。
- Flutter 相关 63 项回归通过，覆盖会话类型标识、桌面历史滚动、iOS 返回、输入栏和资料请求。新增两张 Windows 视觉基线并人工检查。
- `flutter analyze --no-pub` 通过。
- Web 浏览器验证**未完成**：本机 Edge headless 测试停在套件加载阶段，数分钟无进展后停止；不能将它视为 Web 实操通过。未修改生产代码来绕过测试环境问题。

复验：在 `apps/mobile` 中运行下列命令；离线库及 PostgreSQL 集成复验沿用 [用户 IP 验证文档](USER_ACCESS_IP_VALIDATION_2026-09-02.md) 的隔离环境参数。

```powershell
$env:PUB_HOSTED_URL = 'https://pub.dev'
fvm flutter test --no-pub test/peer_login_info_test.dart test/conversation_identity_test.dart test/initial_chat_scroll_test.dart test/ios_chat_navigation_test.dart test/chat_composer_test.dart test/profile_repository_test.dart
fvm flutter analyze --no-pub
```

## 发布顺序

先部署业务服务端及隐私政策，再更新 Web／macOS 客户端；服务端未发布时新客户端显示“暂不可用”，不会回退展示伪造 IP。原生手机界面不变。没有修改版本号或强制更新策略，本次未提交、推送、打包或部署。
