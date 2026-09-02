# 群成员呱呱号展示与快捷邀请验证（2026-09-03）

## 本次范围

- 从群成员头像、群成员列表、群内消息头像或群内名片打开资料时，仅当前群主、管理员展示呱呱号；普通成员即使已经添加对方为好友，在该群上下文内也不展示。
- 群成员搜索与 @ 选择面板使用相同规则，不通过隐藏的呱呱号匹配普通成员的搜索。角色未知或重新同步期间先隐藏，降级后即时隐藏。
- 从通讯录、私聊查看个人资料保持原行为。本次是客户端群内展示限制，不是服务端字段脱敏或全局账号隐私策略；原始用户模型、接口数据及他人明确分享的名片消息正文没有修改。
- 群聊信息的成员区增加“＋ / 邀请”，直接打开好友多选页；原群成员页面的邀请按钮复用同一页面。手机和桌面共用。
- 选择页保留备注优先、搜索和跨搜索选择，标记并禁用已入群好友。点击“完成”直接提交，不再经过群管理或额外确认页面。
- 保留原业务权限：群主、管理员直接添加；普通成员发送邀请，由对方接受。提交前重新获取完整成员列表，服务端继续执行真实权限、黑名单和入群历史边界校验。
- 提交期间阻止重复点击；普通成员多目标邀请部分失败时仅重试未完成目标。页面销毁或切换账号后不继续启动剩余邀请，不把迟到结果写入新账号。
- 无依赖变更、服务端接口变更、数据库迁移或消息协议变更。

## 自动化验证

- `test/group_member_privacy_invite_test.dart`：21 项测试通过。覆盖三种角色、390/1280 宽度、深色与大字体、资料头部与字段隐藏、成员搜索、@ 面板、角色失效、好友关系不绕过群内展示、加号直达多选、部分失败重试、重新校验成员、重复点击、失败重载及退出账号。
- 下列相关文件合计 159 项测试通过：
  - `group_member_privacy_invite_test.dart`
  - `group_administrators_test.dart`
  - `friend_display_name_test.dart`
  - `group_invitations_screen_test.dart`
  - `create_group_screen_test.dart`
  - `group_announcement_test.dart`
  - `group_send_feedback_test.dart`
  - `user_presence_test.dart`
  - `user_presence_pages_test.dart`
  - `message_feedback_test.dart`
  - `message_collaboration_widget_test.dart`
  - `widget_test.dart`
- 原群历史开关回归测试增加 `ensureVisible(toggle)`：先滚动到开关实际可点击的位置，避免点击列表缓存中仍位于视口外的控件；未改变群历史业务逻辑。
- 新增两项截图基线，已生成、人工检查并再次比较通过：
  - `apps/mobile/test/goldens/windows/mobile-group-info-quick-invite.png`
  - `apps/mobile/test/goldens/windows/mobile-group-invite-quick-invite.png`
- 截图覆盖 390 × 844 手机布局；原长昵称成员三列布局、桌面双栏以及其他聊天回归保持通过。
- `fvm flutter analyze --no-pub` 无问题；`git diff --check` 通过。

## 交付边界

测试使用隔离的演示仓库/组件夹具，没有向真实联系人发送邀请，没有修改生产群成员。本次未执行真机或真实服务端端到端邀请验收，未打包、安装、提交、推送或部署。客户端重新构建后才能在已安装应用及线上 Web 中生效。

保留已有在线状态、群公告和消息声音/振动等未提交修改。
