import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_slidable/flutter_slidable.dart';
import 'package:pinyin/pinyin.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../../core/user_identity.dart';
import '../widgets/linli_widgets.dart';
import 'announcement_screens.dart';
import 'chat_screen.dart';
import 'moments_screen.dart';
import 'people_screens.dart';
import 'qr_tools_screen.dart';
import 'settings_screens.dart';
import 'settings_preferences.dart';
import 'sticker_store_screen.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({
    super.key,
    required this.controller,
    required this.onToggleTheme,
    this.chatBackgroundOverride,
  });

  final AppController controller;
  final VoidCallback onToggleTheme;
  final ChatBackgroundStyle? chatBackgroundOverride;

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  int index = 0;
  bool handlingPushNavigation = false;
  String? selectedConversationId;
  bool desktopDetailsVisible = true;

  @override
  Widget build(BuildContext context) => CallbackShortcuts(
    bindings: {
      const SingleActivator(LogicalKeyboardKey.keyK, meta: true):
          _openGlobalSearch,
      const SingleActivator(LogicalKeyboardKey.keyK, control: true):
          _openGlobalSearch,
      const SingleActivator(LogicalKeyboardKey.digit1, control: true): () =>
          _selectSection(0),
      const SingleActivator(LogicalKeyboardKey.digit2, control: true): () =>
          _selectSection(1),
      const SingleActivator(LogicalKeyboardKey.digit3, control: true): () =>
          _selectSection(2),
      const SingleActivator(LogicalKeyboardKey.digit4, control: true): () =>
          _selectSection(3),
    },
    child: Focus(
      autofocus: true,
      child: AnimatedBuilder(
        animation: widget.controller,
        builder: (context, _) {
          return AnnotatedRegion<SystemUiOverlayStyle>(
            value: SystemUiOverlayStyle.light,
            child: LayoutBuilder(
              builder: (context, constraints) {
                final desktop = useLinliDesktopLayout(constraints.maxWidth);
                _schedulePushNavigation(wide: desktop);
                final pages = IndexedStack(
                  index: index,
                  children: [
                    ConversationsTab(controller: widget.controller),
                    ContactsTab(controller: widget.controller),
                    DiscoverTab(controller: widget.controller),
                    MeTab(
                      controller: widget.controller,
                      onToggleTheme: widget.onToggleTheme,
                    ),
                  ],
                );
                return Scaffold(
                  body: desktop ? _desktopWorkspace(constraints, pages) : pages,
                  bottomNavigationBar: desktop
                      ? null
                      : _LinliTabBar(
                          selectedIndex: index,
                          unreadCount:
                              widget.controller.notificationUnreadCount,
                          hasMutedUnread: widget.controller.hasMutedUnread,
                          contactNotificationCount:
                              widget.controller.contactNotificationCount,
                          onSelected: _selectSection,
                        ),
                );
              },
            ),
          );
        },
      ),
    ),
  );

  void _openGlobalSearch() {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => SearchScreen(controller: widget.controller),
      ),
    );
  }

  void _selectSection(int value) {
    if (!mounted || index == value) return;
    setState(() => index = value);
  }

  Widget _desktopWorkspace(BoxConstraints viewport, Widget pages) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final horizontalGap = viewport.maxWidth >= 1440 ? 40.0 : 24.0;
    final verticalGap = viewport.maxHeight >= 900 ? 40.0 : 16.0;
    final workspaceWidth = (viewport.maxWidth - horizontalGap * 2)
        .clamp(0.0, 1360.0)
        .toDouble();
    final workspaceHeight = (viewport.maxHeight - verticalGap * 2)
        .clamp(0.0, 860.0)
        .toDouble();

    return ColoredBox(
      key: const Key('desktop-home-shell'),
      color: dark ? const Color(0xFF101613) : const Color(0xFFE9EDF1),
      child: Center(
        child: SizedBox(
          key: const Key('desktop-home-frame'),
          width: workspaceWidth,
          height: workspaceHeight,
          child: DecoratedBox(
            decoration: BoxDecoration(
              color: Theme.of(context).colorScheme.surfaceContainer,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: Theme.of(context).colorScheme.outline),
              boxShadow: [
                BoxShadow(
                  color: Colors.black.withValues(alpha: dark ? .28 : .14),
                  blurRadius: 36,
                  offset: const Offset(0, 16),
                ),
              ],
            ),
            child: ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: Row(
                key: const Key('desktop-home-workspace'),
                children: [
                  _DesktopAccountNavigation(
                    controller: widget.controller,
                    selectedIndex: index,
                    onSelected: _selectSection,
                    onSearch: _openGlobalSearch,
                    onToggleTheme: widget.onToggleTheme,
                  ),
                  VerticalDivider(
                    width: 1,
                    color: Theme.of(context).colorScheme.outline,
                  ),
                  Expanded(
                    child: switch (index) {
                      0 => _wideConversationWorkspace(workspaceWidth),
                      1 => _desktopContactsWorkspace(),
                      2 => _DesktopToolsWorkspace(
                        controller: widget.controller,
                      ),
                      3 => _DesktopAccountWorkspace(
                        controller: widget.controller,
                        onToggleTheme: widget.onToggleTheme,
                      ),
                      _ => pages,
                    },
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _wideConversationWorkspace(double width) {
    final available = widget.controller.conversations
        .where((item) => !item.archived)
        .toList();
    final selected =
        widget.controller.conversations
            .where((item) => item.id == selectedConversationId)
            .firstOrNull ??
        available.firstOrNull;
    final showDetails = desktopDetailsVisible && width >= 1200;
    return Row(
      key: const Key('wide-conversation-workspace'),
      children: [
        SizedBox(
          key: const Key('desktop-conversation-column'),
          width: 304,
          child: ConversationsTab(
            controller: widget.controller,
            desktopMode: true,
            selectedConversationId: selected?.id,
            onConversationSelected: (conversation) {
              widget.controller.markRead(conversation.id);
              setState(() => selectedConversationId = conversation.id);
            },
          ),
        ),
        VerticalDivider(width: 1, color: Theme.of(context).colorScheme.outline),
        Expanded(
          child: selected == null
              ? const _ConversationSelectionPlaceholder()
              : ChatScreen(
                  key: ValueKey('wide-chat-${selected.id}'),
                  controller: widget.controller,
                  conversation: selected,
                  chatBackgroundOverride: widget.chatBackgroundOverride,
                  showDesktopDetails: showDetails,
                  onToggleDesktopDetails: width >= 1200
                      ? () => setState(
                          () => desktopDetailsVisible = !desktopDetailsVisible,
                        )
                      : null,
                ),
        ),
      ],
    );
  }

  Widget _desktopContactsWorkspace() => Row(
    key: const Key('desktop-contacts-workspace'),
    children: [
      SizedBox(
        key: const Key('desktop-contact-column'),
        width: 320,
        child: ContactsTab(
          controller: widget.controller,
          desktopMode: true,
          onContactSelected: (user) async {
            final conversation = await widget.controller.createDirect(user);
            if (!mounted || conversation == null) return;
            setState(() {
              index = 0;
              selectedConversationId = conversation.id;
            });
          },
        ),
      ),
      VerticalDivider(width: 1, color: Theme.of(context).colorScheme.outline),
      Expanded(child: _DesktopDirectoryOverview(controller: widget.controller)),
    ],
  );

  void _schedulePushNavigation({required bool wide}) {
    final conversationId = widget.controller.pendingConversationId;
    if (conversationId == null || handlingPushNavigation) return;
    handlingPushNavigation = true;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      try {
        var conversation = widget.controller.conversations
            .where((item) => item.id == conversationId)
            .firstOrNull;
        if (conversation == null) {
          await widget.controller.refresh();
          conversation = widget.controller.conversations
              .where((item) => item.id == conversationId)
              .firstOrNull;
        }
        if (!mounted || conversation == null) return;
        widget.controller.clearPendingConversationId(conversationId);
        if (wide) {
          setState(() {
            index = 0;
            selectedConversationId = conversation!.id;
          });
          return;
        }
        setState(() => index = 0);
        await Navigator.of(context).push(
          chatScreenRoute(
            context,
            controller: widget.controller,
            conversation: conversation,
          ),
        );
      } finally {
        handlingPushNavigation = false;
      }
    });
  }
}

class _ConversationSelectionPlaceholder extends StatelessWidget {
  const _ConversationSelectionPlaceholder();

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: Theme.of(context).scaffoldBackgroundColor,
    child: const Center(
      child: StatePanel(
        icon: CupertinoIcons.chat_bubble_2,
        title: '选择一个对话',
        body: '消息会在这里打开，列表会保持在左侧。',
      ),
    ),
  );
}

class _DesktopAccountNavigation extends StatelessWidget {
  const _DesktopAccountNavigation({
    required this.controller,
    required this.selectedIndex,
    required this.onSelected,
    required this.onSearch,
    required this.onToggleTheme,
  });

  final AppController controller;
  final int selectedIndex;
  final ValueChanged<int> onSelected;
  final VoidCallback onSearch;
  final VoidCallback onToggleTheme;

  @override
  Widget build(BuildContext context) {
    final user = controller.currentUser;
    return SizedBox(
      key: const Key('home-navigation-rail'),
      width: 72,
      child: ColoredBox(
        color: LinliColors.navy,
        child: SafeArea(
          right: false,
          child: Column(
            children: [
              const SizedBox(height: 16),
              PersonAvatar(
                name: user?.name ?? '我',
                avatarUrl: user?.avatarUrl,
                online: true,
                size: 40,
              ),
              const SizedBox(height: 20),
              _DesktopNavButton(
                key: const Key('desktop-nav-messages'),
                icon: CupertinoIcons.chat_bubble_fill,
                label: '消息  Ctrl 1',
                selected: selectedIndex == 0,
                badge: controller.notificationUnreadCount,
                onPressed: () => onSelected(0),
              ),
              _DesktopNavButton(
                key: const Key('desktop-nav-contacts'),
                icon: CupertinoIcons.person_2_fill,
                label: '联系人  Ctrl 2',
                selected: selectedIndex == 1,
                badge: controller.contactNotificationCount,
                onPressed: () => onSelected(1),
              ),
              _DesktopNavButton(
                key: const Key('desktop-nav-discover'),
                icon: CupertinoIcons.square_grid_2x2_fill,
                label: '工具  Ctrl 3',
                selected: selectedIndex == 2,
                onPressed: () => onSelected(2),
              ),
              _DesktopNavButton(
                key: const Key('desktop-nav-profile'),
                icon: CupertinoIcons.person_crop_circle_fill,
                label: '我的  Ctrl 4',
                selected: selectedIndex == 3,
                onPressed: () => onSelected(3),
              ),
              const SizedBox(height: 8),
              _DesktopNavButton(
                key: const Key('desktop-global-search'),
                icon: CupertinoIcons.search,
                label: '全局搜索  Ctrl K',
                selected: false,
                onPressed: onSearch,
              ),
              const Spacer(),
              _DesktopNavButton(
                key: const Key('desktop-notification-settings'),
                icon: CupertinoIcons.bell,
                label: '通知设置',
                selected: false,
                onPressed: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) =>
                        NotificationSettingsScreen(controller: controller),
                  ),
                ),
              ),
              _DesktopNavButton(
                key: const Key('desktop-theme-toggle'),
                icon: Theme.of(context).brightness == Brightness.dark
                    ? CupertinoIcons.sun_max_fill
                    : CupertinoIcons.moon_fill,
                label: '切换外观',
                selected: false,
                onPressed: onToggleTheme,
              ),
              _DesktopNavButton(
                key: const Key('desktop-settings'),
                icon: CupertinoIcons.settings_solid,
                label: '设置',
                selected: false,
                onPressed: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => SettingsScreen(
                      controller: controller,
                      onToggleTheme: onToggleTheme,
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 12),
            ],
          ),
        ),
      ),
    );
  }
}

class _DesktopNavButton extends StatelessWidget {
  const _DesktopNavButton({
    super.key,
    required this.icon,
    required this.label,
    required this.selected,
    required this.onPressed,
    this.badge = 0,
  });

  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback onPressed;
  final int badge;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 3),
    child: Tooltip(
      message: label,
      waitDuration: const Duration(milliseconds: 350),
      child: Semantics(
        button: true,
        selected: selected,
        label: label,
        child: InkResponse(
          onTap: onPressed,
          radius: 26,
          child: SizedBox(
            width: 56,
            height: 48,
            child: Stack(
              alignment: Alignment.center,
              children: [
                AnimatedContainer(
                  duration: nexaMotionDuration(context),
                  width: 44,
                  height: 40,
                  decoration: BoxDecoration(
                    color: selected
                        ? LinliColors.brandGreen.withValues(alpha: .16)
                        : Colors.transparent,
                    borderRadius: BorderRadius.circular(12),
                  ),
                ),
                Icon(
                  icon,
                  size: 22,
                  color: selected
                      ? LinliColors.brandGreen
                      : const Color(0xFFCBD5E1),
                ),
                if (selected)
                  const Positioned(
                    left: 0,
                    child: DecoratedBox(
                      decoration: BoxDecoration(
                        color: LinliColors.brandGreen,
                        borderRadius: BorderRadius.horizontal(
                          right: Radius.circular(99),
                        ),
                      ),
                      child: SizedBox(width: 3, height: 24),
                    ),
                  ),
                if (badge > 0)
                  Positioned(
                    top: 3,
                    right: 4,
                    child: Badge.count(
                      count: badge,
                      backgroundColor: LinliColors.unread,
                      textColor: Colors.white,
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}

class _DesktopDirectoryOverview extends StatelessWidget {
  const _DesktopDirectoryOverview({required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: Theme.of(context).scaffoldBackgroundColor,
    child: Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 760),
        child: ListView(
          key: const Key('desktop-directory-overview'),
          shrinkWrap: true,
          padding: const EdgeInsets.all(32),
          children: [
            Text('通讯录', style: Theme.of(context).textTheme.headlineMedium),
            const SizedBox(height: 8),
            Text(
              '从左侧选择联系人开始会话，或使用下面的快捷入口管理关系与群组。',
              style: Theme.of(context).textTheme.bodyMedium,
            ),
            const SizedBox(height: 28),
            Wrap(
              spacing: 12,
              runSpacing: 12,
              children: [
                _DesktopDirectoryAction(
                  icon: CupertinoIcons.person_badge_plus,
                  title: '新的朋友',
                  subtitle:
                      '${controller.pendingIncomingFriendRequestCount} 个待处理申请',
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) =>
                          FriendRequestsScreen(controller: controller),
                    ),
                  ),
                ),
                _DesktopDirectoryAction(
                  icon: CupertinoIcons.person_2_fill,
                  title: '群聊邀请',
                  subtitle:
                      '${controller.pendingIncomingGroupInvitationCount} 个待处理邀请',
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) =>
                          GroupInvitationsScreen(controller: controller),
                    ),
                  ),
                ),
                _DesktopDirectoryAction(
                  icon: CupertinoIcons.group,
                  title: '我的群聊',
                  subtitle:
                      '${controller.conversations.where((item) => item.kind == ConversationKind.group && !item.isBusinessChannel).length} 个群聊',
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => SavedGroupsScreen(controller: controller),
                    ),
                  ),
                ),
                _DesktopDirectoryAction(
                  icon: CupertinoIcons.group_solid,
                  title: '创建群聊',
                  subtitle: '选择成员创建群组',
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => CreateGroupScreen(controller: controller),
                    ),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    ),
  );
}

class _DesktopDirectoryAction extends StatelessWidget {
  const _DesktopDirectoryAction({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.onTap,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => SizedBox(
    width: 220,
    child: Card(
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(16),
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(
                  color: LinliColors.brandGreen.withValues(alpha: .16),
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Icon(icon, size: 21, color: LinliColors.navy),
              ),
              const SizedBox(height: 16),
              Text(title, style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 4),
              Text(subtitle, style: Theme.of(context).textTheme.bodySmall),
            ],
          ),
        ),
      ),
    ),
  );
}

class _DesktopToolsWorkspace extends StatelessWidget {
  const _DesktopToolsWorkspace({required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) => _DesktopWorkspaceScaffold(
    key: const Key('desktop-tools-workspace'),
    title: '探索',
    subtitle: '把常用联系人、群组与内容工具集中到桌面工作台。',
    child: _DesktopActionGrid(
      actions: [
        _DesktopAction(
          icon: CupertinoIcons.qrcode_viewfinder,
          title: '扫一扫',
          subtitle: '添加联系人或加入群聊',
          onTap: () => _push(context, QrScannerScreen(controller: controller)),
        ),
        _DesktopAction(
          icon: CupertinoIcons.qrcode,
          title: '我的二维码',
          subtitle: '展示、保存或分享个人二维码',
          onTap: () => _push(context, MyQrCodeScreen(controller: controller)),
        ),
        _DesktopAction(
          icon: CupertinoIcons.bookmark_fill,
          title: '收藏夹',
          subtitle: '继续查看收藏的消息与文件',
          onTap: () => _push(context, FavoritesScreen(controller: controller)),
        ),
        _DesktopAction(
          icon: CupertinoIcons.search,
          title: '全局搜索',
          subtitle: '搜索联系人、群组和已同步消息',
          onTap: () => _push(context, SearchScreen(controller: controller)),
        ),
        _DesktopAction(
          icon: CupertinoIcons.person_2_fill,
          title: '朋友圈',
          subtitle: '发布动态并查看好友互动',
          onTap: () => _push(context, MomentsScreen(controller: controller)),
        ),
        _DesktopAction(
          icon: CupertinoIcons.smiley_fill,
          title: '表情商店',
          subtitle: '浏览、收藏和管理聊天表情',
          onTap: () =>
              _push(context, StickerStoreScreen(controller: controller)),
        ),
        _DesktopAction(
          icon: CupertinoIcons.person_badge_plus,
          title: '新的朋友',
          subtitle: '${controller.pendingIncomingFriendRequestCount} 个待处理申请',
          onTap: () =>
              _push(context, FriendRequestsScreen(controller: controller)),
        ),
        _DesktopAction(
          icon: CupertinoIcons.group_solid,
          title: '创建群聊',
          subtitle: '选择联系人并设置群名称',
          onTap: () =>
              _push(context, CreateGroupScreen(controller: controller)),
        ),
      ],
    ),
  );
}

class _DesktopAccountWorkspace extends StatelessWidget {
  const _DesktopAccountWorkspace({
    required this.controller,
    required this.onToggleTheme,
  });

  final AppController controller;
  final VoidCallback onToggleTheme;

  @override
  Widget build(BuildContext context) {
    final user = controller.currentUser;
    return _DesktopWorkspaceScaffold(
      key: const Key('desktop-account-workspace'),
      title: '我的',
      subtitle: '管理个人资料、账号安全、通知、隐私和本机存储。',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Material(
            color: Theme.of(context).colorScheme.surfaceContainer,
            borderRadius: BorderRadius.circular(16),
            child: Padding(
              padding: const EdgeInsets.all(22),
              child: Row(
                children: [
                  PersonAvatar(
                    name: user?.name ?? '我',
                    size: 72,
                    avatarUrl: user?.avatarUrl,
                    online: true,
                  ),
                  const SizedBox(width: 18),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          user?.name ?? '我的账户',
                          style: Theme.of(context).textTheme.headlineMedium,
                        ),
                        const SizedBox(height: 5),
                        Text(
                          _profileIdentityLabel(user),
                          style: Theme.of(context).textTheme.bodyMedium,
                        ),
                        if ((user?.signature ?? '').trim().isNotEmpty) ...[
                          const SizedBox(height: 5),
                          Text(
                            user!.signature!,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ],
                      ],
                    ),
                  ),
                  OutlinedButton.icon(
                    onPressed: () => _push(
                      context,
                      EditProfileScreen(controller: controller),
                    ),
                    icon: const Icon(CupertinoIcons.pencil, size: 18),
                    label: const Text('编辑资料'),
                  ),
                  const SizedBox(width: 10),
                  IconButton.filledTonal(
                    tooltip: '我的二维码',
                    onPressed: () =>
                        _push(context, MyQrCodeScreen(controller: controller)),
                    icon: const Icon(CupertinoIcons.qrcode),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 18),
          _DesktopActionGrid(
            actions: [
              _DesktopAction(
                icon: CupertinoIcons.lock_shield_fill,
                title: '账号与安全',
                subtitle: '手机号、密码与账号注销',
                onTap: () => _push(
                  context,
                  AccountSecurityScreen(controller: controller),
                ),
              ),
              _DesktopAction(
                icon: CupertinoIcons.device_phone_portrait,
                title: '登录设备',
                subtitle: '查看并管理当前登录会话',
                onTap: () =>
                    _push(context, DevicesScreen(controller: controller)),
              ),
              _DesktopAction(
                icon: CupertinoIcons.bookmark_fill,
                title: '我的收藏',
                subtitle: '查看收藏的消息、图片与文件',
                onTap: () =>
                    _push(context, FavoritesScreen(controller: controller)),
              ),
              _DesktopAction(
                icon: CupertinoIcons.bell_fill,
                title: '消息通知',
                subtitle: '声音、振动、预览和推送偏好',
                onTap: () => _push(
                  context,
                  NotificationSettingsScreen(controller: controller),
                ),
              ),
              _DesktopAction(
                icon: CupertinoIcons.hand_raised_fill,
                title: '隐私与安全',
                subtitle: '黑名单、搜索权限与本机保护',
                onTap: () =>
                    _push(context, PrivacyScreen(controller: controller)),
              ),
              _DesktopAction(
                icon: CupertinoIcons.archivebox_fill,
                title: '存储空间',
                subtitle: '查看并清理本机消息与媒体缓存',
                onTap: () =>
                    _push(context, StorageScreen(controller: controller)),
              ),
              _DesktopAction(
                icon: CupertinoIcons.paintbrush_fill,
                title: '外观与通用',
                subtitle: '切换深浅外观与通用偏好',
                onTap: () => _push(
                  context,
                  GeneralSettingsScreen(onToggleTheme: onToggleTheme),
                ),
              ),
              _DesktopAction(
                icon: CupertinoIcons.photo_on_rectangle,
                title: '聊天背景',
                subtitle: '设置会话背景与显示风格',
                onTap: () =>
                    _push(context, const ChatBackgroundSettingsScreen()),
              ),
              _DesktopAction(
                icon: CupertinoIcons.question_circle_fill,
                title: '帮助与反馈',
                subtitle: '常见问题和真实反馈工单',
                onTap: () =>
                    _push(context, HelpFeedbackScreen(controller: controller)),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _DesktopWorkspaceScaffold extends StatelessWidget {
  const _DesktopWorkspaceScaffold({
    super.key,
    required this.title,
    required this.subtitle,
    required this.child,
  });

  final String title;
  final String subtitle;
  final Widget child;

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: Theme.of(context).scaffoldBackgroundColor,
    child: ListView(
      padding: const EdgeInsets.fromLTRB(32, 28, 32, 40),
      children: [
        Text(title, style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 7),
        Text(subtitle, style: Theme.of(context).textTheme.bodyMedium),
        const SizedBox(height: 24),
        child,
      ],
    ),
  );
}

class _DesktopAction {
  const _DesktopAction({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.onTap,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final VoidCallback onTap;
}

class _DesktopActionGrid extends StatelessWidget {
  const _DesktopActionGrid({required this.actions});

  final List<_DesktopAction> actions;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final supportsFourColumns = constraints.maxWidth >= 1080;
      final wouldLeaveSingleCard =
          actions.length > 4 && actions.length % 4 == 1;
      final columns = supportsFourColumns && !wouldLeaveSingleCard ? 4 : 3;
      const gap = 12.0;
      final width = (constraints.maxWidth - gap * (columns - 1)) / columns;
      return Wrap(
        spacing: gap,
        runSpacing: gap,
        children: [
          for (final action in actions)
            SizedBox(
              width: width,
              child: Material(
                color: Theme.of(context).colorScheme.surfaceContainer,
                borderRadius: BorderRadius.circular(14),
                clipBehavior: Clip.antiAlias,
                child: InkWell(
                  onTap: action.onTap,
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(minHeight: 128),
                    child: Padding(
                      padding: const EdgeInsets.all(18),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Container(
                            width: 40,
                            height: 40,
                            decoration: BoxDecoration(
                              color: LinliColors.brandGreen.withValues(
                                alpha: .18,
                              ),
                              borderRadius: BorderRadius.circular(11),
                            ),
                            child: Icon(
                              action.icon,
                              size: 21,
                              color:
                                  Theme.of(context).brightness ==
                                      Brightness.dark
                                  ? LinliColors.brandGreen
                                  : LinliColors.navy,
                            ),
                          ),
                          const SizedBox(height: 15),
                          Text(
                            action.title,
                            style: Theme.of(context).textTheme.titleMedium,
                          ),
                          const SizedBox(height: 4),
                          Text(
                            action.subtitle,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ),
        ],
      );
    },
  );
}

void _push(BuildContext context, Widget screen) {
  Navigator.of(context).push(MaterialPageRoute(builder: (_) => screen));
}

class _LinliTabBar extends StatelessWidget {
  const _LinliTabBar({
    required this.selectedIndex,
    required this.unreadCount,
    required this.hasMutedUnread,
    required this.contactNotificationCount,
    required this.onSelected,
  });

  final int selectedIndex;
  final int unreadCount;
  final bool hasMutedUnread;
  final int contactNotificationCount;
  final ValueChanged<int> onSelected;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final inactiveColor = dark
        ? LinliColors.darkPreview
        : LinliColors.tertiaryLabel;
    final selectedColor = dark
        ? LinliColors.brandGreen
        : LinliColors.brandGreenDeep;
    const items = [
      ('消息', CupertinoIcons.chat_bubble, CupertinoIcons.chat_bubble_fill),
      ('联系人', CupertinoIcons.person_2, CupertinoIcons.person_2_fill),
      ('探索', CupertinoIcons.compass, CupertinoIcons.compass_fill),
      ('我的', CupertinoIcons.person, CupertinoIcons.person_fill),
    ];
    return DecoratedBox(
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainer,
        border: Border(
          top: BorderSide(color: Theme.of(context).colorScheme.outline),
        ),
      ),
      child: SafeArea(
        top: false,
        child: ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 62),
          child: Row(
            children: [
              for (var i = 0; i < items.length; i++)
                Expanded(
                  child: Semantics(
                    selected: selectedIndex == i,
                    button: true,
                    label: items[i].$1,
                    child: CupertinoButton(
                      key: Key('home-tab-$i'),
                      minimumSize: const Size(44, 44),
                      padding: const EdgeInsets.only(top: 4, bottom: 4),
                      // The default Cupertino pressed opacity fades the whole
                      // item to 40% before the selected state is painted. On a
                      // bottom navigation bar this reads as a visible flash.
                      pressedOpacity: 1,
                      onPressed: () => onSelected(i),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          _MessageNavIcon(
                            key: Key('home-tab-icon-$i'),
                            icon: selectedIndex == i
                                ? items[i].$3
                                : items[i].$2,
                            count: i == 0 ? unreadCount : 0,
                            showDot: i == 0 && hasMutedUnread,
                            badgeCount: i == 1 ? contactNotificationCount : 0,
                            color: selectedIndex == i
                                ? selectedColor
                                : inactiveColor,
                          ),
                          const SizedBox(height: 4),
                          Text(
                            items[i].$1,
                            maxLines: 1,
                            style: TextStyle(
                              height: 1.05,
                              fontSize: 11,
                              color: selectedIndex == i
                                  ? selectedColor
                                  : inactiveColor,
                              fontWeight: selectedIndex == i
                                  ? FontWeight.w600
                                  : FontWeight.w400,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _MessageNavIcon extends StatelessWidget {
  const _MessageNavIcon({
    super.key,
    required this.icon,
    required this.count,
    required this.showDot,
    this.badgeCount = 0,
    this.color,
  });

  final IconData icon;
  final int count;
  final bool showDot;
  final int badgeCount;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final child = Icon(icon, size: 24, color: color);
    final visibleCount = count > 0 ? count : badgeCount;
    if (visibleCount > 0) {
      return Badge.count(
        count: visibleCount,
        backgroundColor: LinliColors.unread,
        textColor: Colors.white,
        child: child,
      );
    }
    if (showDot) {
      return Badge(backgroundColor: LinliColors.unread, child: child);
    }
    return child;
  }
}

enum _HeaderAction { group, addFriend, scan, myQr }

class _HeaderMenuItem extends StatelessWidget {
  const _HeaderMenuItem({required this.icon, required this.title});

  final IconData icon;
  final String title;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return Row(
      children: [
        Container(
          key: Key('header-menu-icon-$title'),
          width: 30,
          height: 30,
          decoration: BoxDecoration(
            color: dark
                ? LinliColors.brandGreen.withValues(alpha: .16)
                : LinliColors.brandMintStrong,
            borderRadius: BorderRadius.circular(9),
          ),
          child: Icon(
            icon,
            color: dark ? LinliColors.brandGreen : LinliColors.brandGreenDeep,
            size: 17,
          ),
        ),
        const SizedBox(width: 11),
        Expanded(
          child: Text(
            title,
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
              color: dark ? LinliColors.darkLabel : LinliColors.label,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ],
    );
  }
}

enum _ConversationFilter { all, direct, group, unread, mentioned, archived }

class ConversationsTab extends StatefulWidget {
  const ConversationsTab({
    super.key,
    required this.controller,
    this.selectedConversationId,
    this.onConversationSelected,
    this.desktopMode = false,
  });
  final AppController controller;
  final String? selectedConversationId;
  final ValueChanged<Conversation>? onConversationSelected;
  final bool desktopMode;

  @override
  State<ConversationsTab> createState() => _ConversationsTabState();
}

class _ConversationsTabState extends State<ConversationsTab> {
  _ConversationFilter filter = _ConversationFilter.all;

  List<Conversation> get conversations => widget.controller.conversations
      .where(
        (item) => switch (filter) {
          _ConversationFilter.all => !item.archived,
          _ConversationFilter.direct =>
            !item.archived && item.kind == ConversationKind.direct,
          _ConversationFilter.group =>
            !item.archived && item.kind == ConversationKind.group,
          _ConversationFilter.unread => !item.archived && item.unread > 0,
          _ConversationFilter.mentioned =>
            !item.archived && (item.mentionUnreadCount ?? 0) > 0,
          _ConversationFilter.archived => item.archived,
        },
      )
      .toList();

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    if (widget.desktopMode) {
      return ColoredBox(
        color: Theme.of(context).colorScheme.surfaceContainer,
        child: Column(
          children: [
            _DesktopMessagesHeader(
              controller: widget.controller,
              filter: filter,
              onFilterChanged: (value) => setState(() => filter = value),
            ),
            Divider(height: 1, color: Theme.of(context).colorScheme.outline),
            if (widget.controller.messagingUnavailable)
              MessagingConnectionBanner(
                retrying: widget.controller.connectionRetrying,
                onRetry: widget.controller.retryConnection,
              ),
            Expanded(
              child: _ConversationBody(
                controller: widget.controller,
                conversations: conversations,
                showSystemNotifications:
                    filter == _ConversationFilter.all ||
                    (filter == _ConversationFilter.unread &&
                        widget.controller.systemNotificationUnreadCount > 0),
                selectedConversationId: widget.selectedConversationId,
                onConversationSelected: widget.onConversationSelected,
                emptyTitle: filter == _ConversationFilter.archived
                    ? '没有已归档会话'
                    : '这里还没有对话',
                emptyBody: filter == _ConversationFilter.archived
                    ? '在会话菜单中选择“归档”，稍后可在这里恢复。'
                    : '点击新建按钮，从单聊或群聊开始。',
              ),
            ),
          ],
        ),
      );
    }
    return ColoredBox(
      color: LinliColors.navy,
      child: SafeArea(
        bottom: false,
        child: Column(
          children: [
            _MessagesHeader(
              controller: widget.controller,
              filter: filter,
              onFilterChanged: (value) => setState(() => filter = value),
            ),
            Expanded(
              child: ClipRRect(
                borderRadius: const BorderRadius.vertical(
                  top: Radius.circular(20),
                ),
                child: ColoredBox(
                  color: dark
                      ? LinliColors.darkBackground
                      : LinliColors.background,
                  child: Column(
                    children: [
                      if (widget.controller.messagingUnavailable)
                        MessagingConnectionBanner(
                          retrying: widget.controller.connectionRetrying,
                          onRetry: widget.controller.retryConnection,
                        ),
                      Expanded(
                        child: _ConversationBody(
                          controller: widget.controller,
                          conversations: conversations,
                          showSystemNotifications:
                              filter == _ConversationFilter.all ||
                              (filter == _ConversationFilter.unread &&
                                  widget
                                          .controller
                                          .systemNotificationUnreadCount >
                                      0),
                          selectedConversationId: widget.selectedConversationId,
                          onConversationSelected: widget.onConversationSelected,
                          emptyTitle: filter == _ConversationFilter.archived
                              ? '没有已归档会话'
                              : '这里还没有对话',
                          emptyBody: filter == _ConversationFilter.archived
                              ? '向左滑动普通会话并选择“归档”，稍后可在这里恢复。'
                              : '点击右上角加号，从单聊或群聊开始。',
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _DesktopMessagesHeader extends StatelessWidget {
  const _DesktopMessagesHeader({
    required this.controller,
    required this.filter,
    required this.onFilterChanged,
  });

  final AppController controller;
  final _ConversationFilter filter;
  final ValueChanged<_ConversationFilter> onFilterChanged;

  @override
  Widget build(BuildContext context) => Padding(
    key: const Key('desktop-messages-header'),
    padding: const EdgeInsets.fromLTRB(16, 14, 12, 10),
    child: Column(
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                '消息',
                style: Theme.of(
                  context,
                ).textTheme.titleLarge?.copyWith(fontWeight: FontWeight.w700),
              ),
            ),
            IconButton(
              key: const Key('desktop-new-group'),
              tooltip: '发起群聊',
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => CreateGroupScreen(controller: controller),
                ),
              ),
              icon: const Icon(CupertinoIcons.group_solid),
            ),
          ],
        ),
        const SizedBox(height: 8),
        LinliSearchBar(
          hint: '搜索会话与消息  Ctrl K',
          onTap: () => Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) => SearchScreen(controller: controller),
            ),
          ),
        ),
        const SizedBox(height: 10),
        SingleChildScrollView(
          key: const Key('desktop-conversation-filters'),
          scrollDirection: Axis.horizontal,
          child: Row(
            children: [
              _DesktopFilterChip(
                label: '全部',
                selected: filter == _ConversationFilter.all,
                onPressed: () => onFilterChanged(_ConversationFilter.all),
              ),
              _DesktopFilterChip(
                label: '未读',
                count: controller.conversationUnreadCount,
                selected: filter == _ConversationFilter.unread,
                onPressed: () => onFilterChanged(_ConversationFilter.unread),
              ),
              _DesktopFilterChip(
                label: '群聊',
                selected: filter == _ConversationFilter.group,
                onPressed: () => onFilterChanged(_ConversationFilter.group),
              ),
              _DesktopFilterChip(
                key: const Key('desktop-archived-filter'),
                label: '归档',
                count: controller.archivedConversationCount,
                selected: filter == _ConversationFilter.archived,
                onPressed: () => onFilterChanged(_ConversationFilter.archived),
              ),
            ],
          ),
        ),
      ],
    ),
  );
}

class _DesktopFilterChip extends StatelessWidget {
  const _DesktopFilterChip({
    super.key,
    required this.label,
    this.count,
    required this.selected,
    required this.onPressed,
  });

  final String label;
  final int? count;
  final bool selected;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(right: 6),
    child: Semantics(
      button: true,
      selected: selected,
      child: InkWell(
        onTap: onPressed,
        borderRadius: BorderRadius.circular(10),
        child: Container(
          constraints: const BoxConstraints(minWidth: 48, minHeight: 44),
          padding: const EdgeInsets.symmetric(horizontal: 10),
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: selected
                ? LinliColors.brandGreen
                : Theme.of(context).colorScheme.surfaceContainerHigh,
            borderRadius: BorderRadius.circular(10),
          ),
          child: Text(
            count == null || count == 0 ? label : '$label $count',
            style: Theme.of(context).textTheme.labelMedium?.copyWith(
              color: selected
                  ? LinliColors.navy
                  : Theme.of(context).colorScheme.onSurface,
              fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            ),
          ),
        ),
      ),
    ),
  );
}

class _MessagesHeader extends StatelessWidget {
  const _MessagesHeader({
    required this.controller,
    required this.filter,
    required this.onFilterChanged,
  });

  final AppController controller;
  final _ConversationFilter filter;
  final ValueChanged<_ConversationFilter> onFilterChanged;

  @override
  Widget build(BuildContext context) => Stack(
    key: const Key('messages-header'),
    clipBehavior: Clip.none,
    children: [
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 12, 3),
        child: Column(
          children: [
            Row(
              crossAxisAlignment: CrossAxisAlignment.center,
              children: [
                Expanded(
                  child: Text(
                    '消息',
                    style: Theme.of(
                      context,
                    ).textTheme.headlineMedium?.copyWith(color: Colors.white),
                  ),
                ),
                SizedBox.square(
                  dimension: 44,
                  child: PopupMenuButton<_HeaderAction>(
                    key: const Key('messages-plus-menu'),
                    tooltip: '更多操作',
                    padding: EdgeInsets.zero,
                    splashRadius: 22,
                    color: Theme.of(context).brightness == Brightness.dark
                        ? LinliColors.darkSurfaceElevated
                        : LinliColors.surface,
                    surfaceTintColor: Colors.transparent,
                    shadowColor: LinliColors.navy.withValues(alpha: .14),
                    elevation: 8,
                    offset: const Offset(0, 6),
                    menuPadding: const EdgeInsets.symmetric(vertical: 6),
                    constraints: const BoxConstraints.tightFor(width: 184),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(16),
                      side: BorderSide(
                        color: Theme.of(context).brightness == Brightness.dark
                            ? const Color(0xFF315044)
                            : LinliColors.separator,
                        width: .75,
                      ),
                    ),
                    icon: Container(
                      key: const Key('messages-plus-icon'),
                      width: 28,
                      height: 28,
                      decoration: BoxDecoration(
                        color: LinliColors.brandGreen,
                        shape: BoxShape.circle,
                      ),
                      child: const Icon(
                        CupertinoIcons.add,
                        color: LinliColors.navy,
                        size: 16,
                      ),
                    ),
                    onSelected: (value) {
                      HapticFeedback.selectionClick();
                      final screen = switch (value) {
                        _HeaderAction.group => CreateGroupScreen(
                          controller: controller,
                        ),
                        _HeaderAction.addFriend => SearchScreen(
                          controller: controller,
                        ),
                        _HeaderAction.scan => QrScannerScreen(
                          controller: controller,
                        ),
                        _HeaderAction.myQr => MyQrCodeScreen(
                          controller: controller,
                        ),
                      };
                      Navigator.of(
                        context,
                      ).push(MaterialPageRoute(builder: (_) => screen));
                    },
                    itemBuilder: (_) => const [
                      PopupMenuItem(
                        key: Key('header-action-group'),
                        value: _HeaderAction.group,
                        height: 48,
                        padding: EdgeInsets.symmetric(horizontal: 12),
                        child: _HeaderMenuItem(
                          icon: CupertinoIcons.group_solid,
                          title: '发起群聊',
                        ),
                      ),
                      PopupMenuItem(
                        key: Key('header-action-add-friend'),
                        value: _HeaderAction.addFriend,
                        height: 48,
                        padding: EdgeInsets.symmetric(horizontal: 12),
                        child: _HeaderMenuItem(
                          icon: CupertinoIcons.person_add_solid,
                          title: '添加朋友',
                        ),
                      ),
                      PopupMenuItem(
                        key: Key('header-action-scan'),
                        value: _HeaderAction.scan,
                        height: 48,
                        padding: EdgeInsets.symmetric(horizontal: 12),
                        child: _HeaderMenuItem(
                          icon: CupertinoIcons.qrcode_viewfinder,
                          title: '扫一扫',
                        ),
                      ),
                      PopupMenuItem(
                        key: Key('header-action-my-qr'),
                        value: _HeaderAction.myQr,
                        height: 48,
                        padding: EdgeInsets.symmetric(horizontal: 12),
                        child: _HeaderMenuItem(
                          icon: CupertinoIcons.qrcode,
                          title: '我的二维码',
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
            const SizedBox(height: 4),
            Semantics(
              button: true,
              label: '搜索联系人、群组或消息',
              child: CupertinoButton(
                minimumSize: const Size.fromHeight(44),
                padding: EdgeInsets.zero,
                onPressed: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => SearchScreen(controller: controller),
                  ),
                ),
                child: Container(
                  key: const Key('messages-search-field'),
                  constraints: const BoxConstraints(minHeight: 40),
                  padding: const EdgeInsets.symmetric(horizontal: 12),
                  decoration: BoxDecoration(
                    color: Colors.white.withValues(alpha: .1),
                    borderRadius: BorderRadius.circular(12),
                    border: Border.all(
                      color: Colors.white.withValues(alpha: .12),
                    ),
                  ),
                  child: const Row(
                    children: [
                      Icon(CupertinoIcons.search, color: Color(0xFFCBD5E1)),
                      SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          '搜索联系人、群组或消息',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: Color(0xFFCBD5E1),
                            fontSize: 15,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
            const SizedBox(height: 6),
            _ConversationFilterStrip(
              children: [
                _StatusFilterButton(
                  label: '全部',
                  selected: filter == _ConversationFilter.all,
                  onPressed: () => onFilterChanged(_ConversationFilter.all),
                ),
                const SizedBox(width: 6),
                _StatusFilterButton(
                  label: '单聊',
                  selected: filter == _ConversationFilter.direct,
                  onPressed: () => onFilterChanged(_ConversationFilter.direct),
                ),
                const SizedBox(width: 6),
                _StatusFilterButton(
                  label: '群聊',
                  selected: filter == _ConversationFilter.group,
                  onPressed: () => onFilterChanged(_ConversationFilter.group),
                ),
                const SizedBox(width: 6),
                _StatusFilterButton(
                  key: const Key('unread-conversation-filter'),
                  label: '未读',
                  count: controller.conversationUnreadCount,
                  selected: filter == _ConversationFilter.unread,
                  onPressed: () => onFilterChanged(
                    filter == _ConversationFilter.unread
                        ? _ConversationFilter.all
                        : _ConversationFilter.unread,
                  ),
                ),
                if (controller.supportsMentionUnread) ...[
                  const SizedBox(width: 6),
                  _StatusFilterButton(
                    key: const Key('mentioned-conversation-filter'),
                    label: '@我',
                    count: controller.mentionUnreadCount,
                    selected: filter == _ConversationFilter.mentioned,
                    onPressed: () => onFilterChanged(
                      filter == _ConversationFilter.mentioned
                          ? _ConversationFilter.all
                          : _ConversationFilter.mentioned,
                    ),
                  ),
                ],
                const SizedBox(width: 6),
                _StatusFilterButton(
                  key: const Key('archived-conversation-filter'),
                  label: '已归档',
                  count: controller.archivedConversationCount,
                  selected: filter == _ConversationFilter.archived,
                  onPressed: () => onFilterChanged(
                    filter == _ConversationFilter.archived
                        ? _ConversationFilter.all
                        : _ConversationFilter.archived,
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    ],
  );
}

class _ConversationFilterStrip extends StatefulWidget {
  const _ConversationFilterStrip({required this.children});

  final List<Widget> children;

  @override
  State<_ConversationFilterStrip> createState() =>
      _ConversationFilterStripState();
}

class _ConversationFilterStripState extends State<_ConversationFilterStrip> {
  final ScrollController controller = ScrollController();
  bool canScrollLeft = false;
  bool canScrollRight = false;

  @override
  void initState() {
    super.initState();
    controller.addListener(_updateEdges);
    WidgetsBinding.instance.addPostFrameCallback((_) => _updateEdges());
  }

  @override
  void didUpdateWidget(covariant _ConversationFilterStrip oldWidget) {
    super.didUpdateWidget(oldWidget);
    WidgetsBinding.instance.addPostFrameCallback((_) => _updateEdges());
  }

  void _updateEdges() {
    if (!mounted || !controller.hasClients) return;
    final nextLeft = controller.offset > 2;
    final nextRight =
        controller.offset < controller.position.maxScrollExtent - 2;
    if (nextLeft == canScrollLeft && nextRight == canScrollRight) return;
    setState(() {
      canScrollLeft = nextLeft;
      canScrollRight = nextRight;
    });
  }

  @override
  void dispose() {
    controller
      ..removeListener(_updateEdges)
      ..dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => SizedBox(
    height: 44,
    child: Stack(
      children: [
        SingleChildScrollView(
          key: const Key('messages-filter-control'),
          controller: controller,
          scrollDirection: Axis.horizontal,
          child: Padding(
            padding: const EdgeInsets.only(right: 12),
            child: Row(children: widget.children),
          ),
        ),
        if (canScrollLeft)
          const Positioned(
            left: 0,
            top: 0,
            bottom: 0,
            child: _FilterEdgeCue(left: true),
          ),
        if (canScrollRight)
          const Positioned(
            right: 0,
            top: 0,
            bottom: 0,
            child: _FilterEdgeCue(left: false),
          ),
      ],
    ),
  );
}

class _FilterEdgeCue extends StatelessWidget {
  const _FilterEdgeCue({required this.left});

  final bool left;

  @override
  Widget build(BuildContext context) => IgnorePointer(
    child: Container(
      width: 24,
      alignment: left ? Alignment.centerLeft : Alignment.centerRight,
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: left ? Alignment.centerLeft : Alignment.centerRight,
          end: left ? Alignment.centerRight : Alignment.centerLeft,
          colors: const [LinliColors.navy, Color(0x00123B32)],
        ),
      ),
      child: Padding(
        padding: EdgeInsets.only(left: left ? 1 : 0, right: left ? 0 : 1),
        child: Icon(
          left ? CupertinoIcons.chevron_left : CupertinoIcons.chevron_right,
          size: 11,
          color: Colors.white70,
        ),
      ),
    ),
  );
}

class _StatusFilterButton extends StatelessWidget {
  const _StatusFilterButton({
    super.key,
    required this.label,
    this.count,
    required this.selected,
    required this.onPressed,
  });

  final String label;
  final int? count;
  final bool selected;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) => Semantics(
    button: true,
    selected: selected,
    label: '$label筛选${count == null ? '' : '，$count条'}',
    child: CupertinoButton(
      minimumSize: const Size(44, 44),
      padding: EdgeInsets.zero,
      onPressed: onPressed,
      child: AnimatedContainer(
        duration: nexaMotionDuration(context),
        constraints: const BoxConstraints(minHeight: 36, minWidth: 48),
        padding: const EdgeInsets.symmetric(horizontal: 10),
        decoration: BoxDecoration(
          color: selected
              ? LinliColors.brandGreen
              : Colors.white.withValues(alpha: .08),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(
            color: selected
                ? LinliColors.brandGreen
                : Colors.white.withValues(alpha: .12),
          ),
        ),
        alignment: Alignment.center,
        child: Text(
          count == null || count == 0 ? label : '$label $count',
          style: TextStyle(
            color: selected ? LinliColors.navy : Colors.white,
            fontSize: 14,
            fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
          ),
        ),
      ),
    ),
  );
}

class _ConversationBody extends StatelessWidget {
  const _ConversationBody({
    required this.controller,
    required this.conversations,
    this.showSystemNotifications = true,
    this.selectedConversationId,
    this.onConversationSelected,
    this.emptyTitle = '这里还没有对话',
    this.emptyBody = '点击右上角加号，从单聊或群聊开始。',
  });
  final AppController controller;
  final List<Conversation> conversations;
  final bool showSystemNotifications;
  final String? selectedConversationId;
  final ValueChanged<Conversation>? onConversationSelected;
  final String emptyTitle;
  final String emptyBody;

  @override
  Widget build(BuildContext context) {
    if (controller.loading && controller.conversations.isEmpty) {
      return const Center(child: CupertinoActivityIndicator());
    }
    if (controller.error != null && controller.conversations.isEmpty) {
      return StatePanel(
        icon: CupertinoIcons.cloud,
        title: '暂时无法连接',
        body: controller.error!,
        actionLabel: '重新加载',
        onAction: controller.refresh,
      );
    }
    final showConversationEmpty = conversations.isEmpty;
    final canStartConversation = emptyTitle == '这里还没有对话';
    final leadingItemCount = showSystemNotifications ? 1 : 0;
    return RefreshIndicator(
      onRefresh: controller.refresh,
      color: LinliColors.navy,
      child: SlidableAutoCloseBehavior(
        child: ListView.builder(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: EdgeInsets.zero,
          itemCount:
              leadingItemCount +
              conversations.length +
              (showConversationEmpty ? 1 : 0),
          itemBuilder: (context, index) {
            if (showSystemNotifications && index == 0) {
              return KeyedSubtree(
                key: const Key('system-notification-section'),
                child: SystemNotificationTile(controller: controller),
              );
            }
            final contentIndex = index - leadingItemCount;
            if (showConversationEmpty && contentIndex == 0) {
              return StatePanel(
                icon: CupertinoIcons.archivebox,
                title: emptyTitle,
                body: emptyBody,
                actionLabel: canStartConversation ? '发起会话' : null,
                onAction: canStartConversation
                    ? () => Navigator.of(context).push(
                        MaterialPageRoute(
                          builder: (_) => SearchScreen(controller: controller),
                        ),
                      )
                    : null,
              );
            }
            final itemIndex = contentIndex;
            return ConversationTile(
              key: ValueKey(conversations[itemIndex].id),
              conversation: conversations[itemIndex],
              controller: controller,
              highlighted: conversations[itemIndex].pinned,
              selected: conversations[itemIndex].id == selectedConversationId,
              onSelected: onConversationSelected,
            );
          },
        ),
      ),
    );
  }
}

class ConversationTile extends StatelessWidget {
  const ConversationTile({
    super.key,
    required this.conversation,
    required this.controller,
    this.highlighted = false,
    this.selected = false,
    this.onSelected,
  });
  final Conversation conversation;
  final AppController controller;
  final bool highlighted;
  final bool selected;
  final ValueChanged<Conversation>? onSelected;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final draft = controller.draftFor(conversation.id);
    final subtitle = draft.isEmpty ? conversation.subtitle : '[草稿] $draft';
    final directPeer = conversation.directPeerFor(controller.currentUser?.id);
    return Slidable(
      key: ValueKey('conversation-slidable-${conversation.id}'),
      endActionPane: ActionPane(
        extentRatio: .95,
        motion: const DrawerMotion(),
        children: [
          SlidableAction(
            key: ValueKey('conversation-pin-${conversation.id}'),
            onPressed: (_) =>
                controller.toggleConversationPinned(conversation.id),
            backgroundColor: const Color(0xFF475569),
            foregroundColor: Colors.white,
            icon: conversation.pinned
                ? CupertinoIcons.pin_slash
                : CupertinoIcons.pin,
            label: conversation.pinned ? '取消置顶' : '置顶',
          ),
          SlidableAction(
            key: ValueKey('conversation-unread-${conversation.id}'),
            onPressed: (_) => conversation.unread > 0
                ? controller.markRead(conversation.id)
                : controller.markUnread(conversation.id),
            backgroundColor: const Color(0xFF2563EB),
            foregroundColor: Colors.white,
            icon: conversation.unread > 0
                ? CupertinoIcons.envelope_open
                : CupertinoIcons.envelope_badge,
            label: conversation.unread > 0 ? '标为已读' : '标为未读',
          ),
          SlidableAction(
            key: ValueKey('conversation-mute-${conversation.id}'),
            onPressed: (_) =>
                controller.toggleConversationMuted(conversation.id),
            backgroundColor: const Color(0xFF64748B),
            foregroundColor: Colors.white,
            icon: conversation.muted
                ? CupertinoIcons.bell
                : CupertinoIcons.bell_slash,
            label: conversation.muted ? '开启提醒' : '免打扰',
          ),
          SlidableAction(
            key: ValueKey('conversation-archive-${conversation.id}'),
            onPressed: (_) =>
                controller.toggleConversationArchived(conversation.id),
            backgroundColor: const Color(0xFF7C3AED),
            foregroundColor: Colors.white,
            icon: conversation.archived
                ? CupertinoIcons.arrow_up_bin_fill
                : CupertinoIcons.archivebox_fill,
            label: conversation.archived ? '恢复' : '归档',
          ),
          SlidableAction(
            key: ValueKey('conversation-delete-${conversation.id}'),
            onPressed: (_) => _confirmHide(context),
            backgroundColor: LinliColors.systemRed,
            foregroundColor: Colors.white,
            icon: CupertinoIcons.delete,
            label: '删除',
          ),
        ],
      ),
      child: Semantics(
        button: true,
        label:
            '${conversation.archived ? '已归档，' : ''}${highlighted ? '已置顶，' : ''}${conversation.title}，$subtitle${(conversation.mentionUnreadCount ?? 0) > 0 ? '，${conversation.mentionUnreadCount}条提到我' : ''}${conversation.unread > 0 ? '，${conversation.unread}条未读' : ''}',
        child: Material(
          color: highlighted
              ? (dark
                    ? LinliColors.darkPinnedSurface
                    : LinliColors.pinnedSurface)
              : selected
              ? Theme.of(context).colorScheme.surfaceContainerHighest
              : Theme.of(context).colorScheme.surfaceContainer,
          child: InkWell(
            onSecondaryTapDown: (details) =>
                _showDesktopConversationMenu(context, details.globalPosition),
            onTap: () {
              controller.markRead(conversation.id);
              if (onSelected != null) {
                onSelected!(conversation);
                return;
              }
              Navigator.of(context).push(
                chatScreenRoute(
                  context,
                  controller: controller,
                  conversation: conversation,
                ),
              );
            },
            child: Container(
              constraints: const BoxConstraints(minHeight: 74),
              child: Row(
                children: [
                  Padding(
                    padding: const EdgeInsets.only(left: 16, right: 12),
                    child: PersonAvatar(
                      key: ValueKey('conversation-avatar-${conversation.id}'),
                      name: conversation.title,
                      size: 48,
                      avatarUrl:
                          conversation.avatarUrl ?? directPeer?.avatarUrl,
                      online:
                          conversation.kind == ConversationKind.direct &&
                          (directPeer?.isOnline ?? false),
                    ),
                  ),
                  Expanded(
                    child: Container(
                      key: ValueKey('conversation-content-${conversation.id}'),
                      constraints: const BoxConstraints(minHeight: 74),
                      padding: const EdgeInsets.fromLTRB(0, 9, 14, 9),
                      decoration: BoxDecoration(
                        border: Border(
                          bottom: BorderSide(
                            color: dark
                                ? const Color(0xFF29443A)
                                : const Color(0xFFD7E0DB),
                            width: .75,
                          ),
                        ),
                      ),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          Row(
                            children: [
                              if (highlighted) ...[
                                Icon(
                                  CupertinoIcons.pin_fill,
                                  key: ValueKey(
                                    'conversation-pinned-indicator-${conversation.id}',
                                  ),
                                  size: 12,
                                  color: dark
                                      ? LinliColors.darkPreview
                                      : LinliColors.preview,
                                ),
                                const SizedBox(width: 4),
                                Text(
                                  '置顶',
                                  style: Theme.of(context).textTheme.labelSmall
                                      ?.copyWith(fontWeight: FontWeight.w600),
                                ),
                                const SizedBox(width: 7),
                              ],
                              Expanded(
                                child: Text(
                                  key: ValueKey(
                                    'conversation-title-${conversation.id}',
                                  ),
                                  conversation.title,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: Theme.of(
                                    context,
                                  ).textTheme.titleMedium,
                                ),
                              ),
                              const SizedBox(width: 8),
                              Text(
                                _relativeTime(conversation.updatedAt),
                                style: Theme.of(context).textTheme.labelSmall,
                              ),
                            ],
                          ),
                          const SizedBox(height: 5),
                          Row(
                            children: [
                              if (conversation.muted) ...[
                                const Icon(
                                  CupertinoIcons.bell_slash_fill,
                                  size: 13,
                                  color: LinliColors.tertiaryLabel,
                                ),
                                const SizedBox(width: 4),
                              ],
                              Expanded(
                                child: Text(
                                  subtitle,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: Theme.of(context).textTheme.bodyMedium
                                      ?.copyWith(
                                        color: draft.isEmpty
                                            ? LinliColors.preview
                                            : LinliColors.systemRed,
                                        fontSize: 14,
                                      ),
                                ),
                              ),
                              if ((conversation.mentionUnreadCount ?? 0) >
                                  0) ...[
                                const SizedBox(width: 8),
                                Container(
                                  key: ValueKey(
                                    'conversation-mention-${conversation.id}',
                                  ),
                                  constraints: const BoxConstraints(
                                    minHeight: 21,
                                    minWidth: 34,
                                  ),
                                  padding: const EdgeInsets.symmetric(
                                    horizontal: 6,
                                  ),
                                  alignment: Alignment.center,
                                  decoration: BoxDecoration(
                                    color: LinliColors.brandGreen,
                                    borderRadius: BorderRadius.circular(999),
                                  ),
                                  child: const Text(
                                    '@我',
                                    style: TextStyle(
                                      color: LinliColors.navy,
                                      fontSize: 11,
                                      fontWeight: FontWeight.w700,
                                    ),
                                  ),
                                ),
                              ],
                              if (conversation.unread > 0) ...[
                                const SizedBox(width: 8),
                                if (conversation.muted)
                                  Container(
                                    width: 9,
                                    height: 9,
                                    decoration: const BoxDecoration(
                                      color: LinliColors.unread,
                                      shape: BoxShape.circle,
                                    ),
                                  )
                                else
                                  Container(
                                    constraints: const BoxConstraints(
                                      minWidth: 21,
                                      minHeight: 21,
                                    ),
                                    padding: const EdgeInsets.symmetric(
                                      horizontal: 6,
                                    ),
                                    alignment: Alignment.center,
                                    decoration: BoxDecoration(
                                      color: LinliColors.unread,
                                      borderRadius: BorderRadius.circular(999),
                                    ),
                                    child: Text(
                                      conversation.unread > 99
                                          ? '99+'
                                          : '${conversation.unread}',
                                      style: const TextStyle(
                                        color: Colors.white,
                                        fontSize: 11,
                                        fontWeight: FontWeight.w700,
                                      ),
                                    ),
                                  ),
                              ],
                            ],
                          ),
                        ],
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _confirmHide(BuildContext context) async {
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (dialogContext) => CupertinoAlertDialog(
        title: Text('删除“${conversation.title}”会话？'),
        content: const Text('会话会从本机列表移除，但不会退出群聊或删除对方记录；收到新消息后会重新出现。'),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          CupertinoDialogAction(
            isDestructiveAction: true,
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('删除会话'),
          ),
        ],
      ),
    );
    if (confirmed == true) await controller.hideConversation(conversation.id);
  }

  Future<void> _showDesktopConversationMenu(
    BuildContext context,
    Offset position,
  ) async {
    final overlay = Overlay.of(context).context.findRenderObject() as RenderBox;
    final action = await showMenu<String>(
      context: context,
      position: RelativeRect.fromRect(
        Rect.fromLTWH(position.dx, position.dy, 1, 1),
        Offset.zero & overlay.size,
      ),
      items: [
        PopupMenuItem(
          value: 'pin',
          child: Text(conversation.pinned ? '取消置顶' : '置顶会话'),
        ),
        PopupMenuItem(
          value: 'unread',
          child: Text(conversation.unread > 0 ? '标为已读' : '标为未读'),
        ),
        PopupMenuItem(
          value: 'mute',
          child: Text(conversation.muted ? '开启提醒' : '消息免打扰'),
        ),
        PopupMenuItem(
          value: 'archive',
          child: Text(conversation.archived ? '移出归档' : '归档会话'),
        ),
        const PopupMenuItem(
          value: 'delete',
          child: Text('删除会话', style: TextStyle(color: LinliColors.systemRed)),
        ),
      ],
    );
    switch (action) {
      case 'pin':
        await controller.toggleConversationPinned(conversation.id);
      case 'unread':
        if (conversation.unread > 0) {
          await controller.markRead(conversation.id);
        } else {
          await controller.markUnread(conversation.id);
        }
      case 'mute':
        await controller.toggleConversationMuted(conversation.id);
      case 'archive':
        await controller.toggleConversationArchived(conversation.id);
      case 'delete':
        if (context.mounted) await _confirmHide(context);
      case null:
        return;
    }
  }
}

String _relativeTime(DateTime time) {
  final difference = DateTime.now().difference(time);
  if (difference.inMinutes < 1) return '刚刚';
  if (difference.inMinutes < 60) return '${difference.inMinutes} 分钟';
  if (difference.inHours < 24) return '${difference.inHours} 小时';
  if (difference.inDays == 1) return '昨天';
  return '${time.month}/${time.day}';
}

class ContactsTab extends StatefulWidget {
  const ContactsTab({
    super.key,
    required this.controller,
    this.desktopMode = false,
    this.onContactSelected,
  });
  final AppController controller;
  final bool desktopMode;
  final Future<void> Function(AppUser)? onContactSelected;

  @override
  State<ContactsTab> createState() => _ContactsTabState();
}

class _ContactsTabState extends State<ContactsTab> {
  final ScrollController _scrollController = ScrollController();
  final GlobalKey _scrollViewportKey = GlobalKey();
  final GlobalKey _contactsSectionKey = GlobalKey();
  final Map<String, GlobalKey> _groupKeys = {};
  double? _contactsStartOffset;
  String? _activeLetter;

  @override
  void dispose() {
    _scrollController.dispose();
    super.dispose();
  }

  List<_ContactGroup> get _groups {
    final byLetter = <String, List<AppUser>>{};
    for (final user in widget.controller.contacts) {
      byLetter.putIfAbsent(_contactInitial(user.name), () => []).add(user);
    }
    final letters = byLetter.keys.toList()
      ..sort((a, b) {
        if (a == '#') return 1;
        if (b == '#') return -1;
        return a.compareTo(b);
      });
    return [
      for (final letter in letters)
        _ContactGroup(
          letter,
          byLetter[letter]!..sort(
            (a, b) =>
                _contactSortName(a.name).compareTo(_contactSortName(b.name)),
          ),
        ),
    ];
  }

  void _jumpToGroup(String letter, List<_ContactGroup> groups) {
    final groupIndex = groups.indexWhere((group) => group.letter == letter);
    if (groupIndex < 0 || !_scrollController.hasClients) return;
    setState(() => _activeLetter = letter);

    final sectionContext = _contactsSectionKey.currentContext;
    final viewportContext = _scrollViewportKey.currentContext;
    final sectionBox = sectionContext?.findRenderObject() as RenderBox?;
    final viewportBox = viewportContext?.findRenderObject() as RenderBox?;
    if (_contactsStartOffset == null &&
        sectionBox != null &&
        viewportBox != null) {
      _contactsStartOffset =
          _scrollController.offset +
          sectionBox.localToGlobal(Offset.zero, ancestor: viewportBox).dy +
          sectionBox.size.height;
    }
    final contactsStart = _contactsStartOffset;
    if (contactsStart == null) return;
    var target = contactsStart;
    for (var index = 0; index < groupIndex; index++) {
      target += 32 + groups[index].users.length * 68;
    }
    target = target.clamp(
      _scrollController.position.minScrollExtent,
      _scrollController.position.maxScrollExtent,
    );
    // Alphabet rails are direct-manipulation controls. An immediate jump stays
    // responsive while the finger is still down and while it moves across
    // several letters; an animation here can be cancelled by the next update.
    _scrollController.jumpTo(target);
  }

  void _clearActiveLetter() {
    if (!mounted || _activeLetter == null) return;
    setState(() => _activeLetter = null);
  }

  @override
  Widget build(BuildContext context) => _TopLevelShell(
    title: '联系人',
    desktopMode: widget.desktopMode,
    action: IconButton(
      tooltip: '添加联系人',
      color: widget.desktopMode
          ? Theme.of(context).colorScheme.onSurface
          : Colors.white,
      onPressed: () => Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => SearchScreen(controller: widget.controller),
        ),
      ),
      icon: const Icon(CupertinoIcons.person_add),
    ),
    child: Builder(
      builder: (context) {
        final controller = widget.controller;
        final groups = _groups;
        for (final group in groups) {
          _groupKeys.putIfAbsent(group.letter, GlobalKey.new);
        }
        return Stack(
          children: [
            CustomScrollView(
              key: _scrollViewportKey,
              controller: _scrollController,
              slivers: [
                SliverPadding(
                  padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
                  sliver: SliverToBoxAdapter(
                    child: LinliSearchBar(
                      hint: '搜索联系人或呱呱号',
                      onTap: () => Navigator.of(context).push(
                        MaterialPageRoute(
                          builder: (_) => SearchScreen(controller: controller),
                        ),
                      ),
                    ),
                  ),
                ),
                SliverPadding(
                  padding: const EdgeInsets.symmetric(horizontal: 16),
                  sliver: SliverToBoxAdapter(
                    child: SectionCard(
                      children: [
                        SettingTile(
                          icon: CupertinoIcons.person_badge_plus,
                          title: '新的朋友',
                          subtitle:
                              controller.pendingIncomingFriendRequestCount == 0
                              ? '暂无新申请'
                              : '${controller.pendingIncomingFriendRequestCount} 个待处理申请',
                          trailing:
                              controller.pendingIncomingFriendRequestCount > 0
                              ? Badge.count(
                                  count: controller
                                      .pendingIncomingFriendRequestCount,
                                  backgroundColor: LinliColors.unread,
                                  textColor: Colors.white,
                                )
                              : null,
                          onTap: () => Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) =>
                                  FriendRequestsScreen(controller: controller),
                            ),
                          ),
                        ),
                        SettingTile(
                          icon: CupertinoIcons.person_2_fill,
                          title: '群聊邀请',
                          subtitle:
                              controller.pendingIncomingGroupInvitationCount ==
                                  0
                              ? '暂无待处理邀请'
                              : '${controller.pendingIncomingGroupInvitationCount} 个待处理邀请',
                          trailing:
                              controller.pendingIncomingGroupInvitationCount > 0
                              ? Badge.count(
                                  count: controller
                                      .pendingIncomingGroupInvitationCount,
                                  backgroundColor: LinliColors.unread,
                                  textColor: Colors.white,
                                )
                              : null,
                          onTap: () => Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) => GroupInvitationsScreen(
                                controller: controller,
                              ),
                            ),
                          ),
                        ),
                        SettingTile(
                          key: const Key('contacts-saved-groups'),
                          icon: CupertinoIcons.book,
                          title: '保存的群聊',
                          subtitle:
                              '${controller.conversations.where((item) => item.kind == ConversationKind.group && !item.isBusinessChannel && item.saved).length} 个群聊',
                          onTap: () => Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) =>
                                  SavedGroupsScreen(controller: controller),
                            ),
                          ),
                        ),
                        SettingTile(
                          icon: CupertinoIcons.person_2_fill,
                          title: '创建群聊',
                          subtitle: '选择联系人开始群组对话',
                          onTap: () => Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) =>
                                  CreateGroupScreen(controller: controller),
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
                SliverToBoxAdapter(
                  child: SectionHeader(
                    '联系人',
                    key: _contactsSectionKey,
                    horizontalInset: 16,
                  ),
                ),
                if (controller.contactsLoadError != null &&
                    controller.contacts.isNotEmpty)
                  SliverPadding(
                    padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                    sliver: SliverToBoxAdapter(
                      child: Card(
                        child: ListTile(
                          key: const Key('contacts-load-error'),
                          leading: const Icon(
                            CupertinoIcons.exclamationmark_circle,
                            color: LinliColors.systemRed,
                          ),
                          title: const Text('联系人同步失败'),
                          subtitle: Text(controller.contactsLoadError!),
                          trailing: TextButton(
                            onPressed: controller.refreshContacts,
                            child: const Text('重试'),
                          ),
                        ),
                      ),
                    ),
                  ),
                if (controller.contacts.isEmpty)
                  SliverToBoxAdapter(
                    child: SizedBox(
                      height: 300,
                      child: StatePanel(
                        icon: controller.contactsLoadError == null
                            ? CupertinoIcons.person_2
                            : CupertinoIcons.cloud_download,
                        title: controller.contactsLoadError == null
                            ? '还没有联系人'
                            : '联系人暂时无法加载',
                        body: controller.contactsLoadError ?? '可以通过呱呱号查找并添加朋友。',
                        actionLabel: controller.contactsLoadError == null
                            ? null
                            : '重新加载',
                        onAction: controller.contactsLoadError == null
                            ? null
                            : controller.refreshContacts,
                      ),
                    ),
                  )
                else
                  for (final group in groups) ...[
                    SliverToBoxAdapter(
                      child: _ContactGroupHeader(
                        key: _groupKeys[group.letter],
                        letter: group.letter,
                      ),
                    ),
                    SliverList.builder(
                      itemCount: group.users.length,
                      itemBuilder: (context, index) => _ContactListTile(
                        user: group.users[index],
                        onTap: () async {
                          final user = group.users[index];
                          if (widget.onContactSelected != null) {
                            await widget.onContactSelected!(user);
                            return;
                          }
                          final conversation = await controller.createDirect(
                            user,
                          );
                          if (conversation != null && context.mounted) {
                            await Navigator.of(context).push(
                              chatScreenRoute(
                                context,
                                controller: controller,
                                conversation: conversation,
                              ),
                            );
                          }
                        },
                      ),
                    ),
                  ],
              ],
            ),
            if (groups.length > 1)
              Positioned.fill(
                child: Align(
                  alignment: Alignment.centerRight,
                  child: _ContactAlphabetRail(
                    letters: groups.map((group) => group.letter).toList(),
                    activeLetter: _activeLetter,
                    onLetterSelected: (letter) => _jumpToGroup(letter, groups),
                    onInteractionEnd: _clearActiveLetter,
                  ),
                ),
              ),
            if (_activeLetter case final letter?)
              IgnorePointer(
                child: Center(
                  child: Container(
                    key: const Key('contact-index-bubble'),
                    width: 64,
                    height: 64,
                    alignment: Alignment.center,
                    decoration: BoxDecoration(
                      color: LinliColors.navy.withValues(alpha: .88),
                      borderRadius: BorderRadius.circular(18),
                    ),
                    child: Text(
                      letter,
                      style: const TextStyle(
                        color: Colors.white,
                        fontSize: 28,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                ),
              ),
          ],
        );
      },
    ),
  );
}

class _ContactGroup {
  const _ContactGroup(this.letter, this.users);

  final String letter;
  final List<AppUser> users;
}

String _contactSortName(String name) => PinyinHelper.getPinyinE(
  name.trim(),
  separator: '',
  defPinyin: '#',
  format: PinyinFormat.WITHOUT_TONE,
).toUpperCase();

String _contactInitial(String name) {
  final sortName = _contactSortName(name);
  if (sortName.isEmpty) return '#';
  final initial = sortName[0];
  return RegExp(r'[A-Z]').hasMatch(initial) ? initial : '#';
}

class _ContactGroupHeader extends StatelessWidget {
  const _ContactGroupHeader({super.key, required this.letter});

  final String letter;

  @override
  Widget build(BuildContext context) => Container(
    key: Key('contact-group-$letter'),
    height: 32,
    alignment: Alignment.centerLeft,
    padding: const EdgeInsets.only(left: 16, right: 40),
    color: Theme.of(context).brightness == Brightness.dark
        ? Theme.of(context).colorScheme.surfaceContainerHigh
        : LinliColors.background,
    child: Text(
      letter,
      style: Theme.of(context).textTheme.labelLarge?.copyWith(
        color: LinliColors.brandGreenDeep,
        fontWeight: FontWeight.w700,
      ),
    ),
  );
}

class _ContactListTile extends StatelessWidget {
  const _ContactListTile({required this.user, required this.onTap});

  final AppUser user;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => ListTile(
    key: Key('contact-${user.id}'),
    minTileHeight: 68,
    contentPadding: const EdgeInsets.only(left: 16, right: 36),
    leading: PersonAvatar(
      name: user.name,
      avatarUrl: user.avatarUrl,
      online: user.isOnline,
    ),
    title: Text(user.name, maxLines: 1, overflow: TextOverflow.ellipsis),
    subtitle: Text(user.presence, maxLines: 1, overflow: TextOverflow.ellipsis),
    onTap: onTap,
  );
}

class _ContactAlphabetRail extends StatelessWidget {
  const _ContactAlphabetRail({
    required this.letters,
    required this.activeLetter,
    required this.onLetterSelected,
    required this.onInteractionEnd,
  });

  final List<String> letters;
  final String? activeLetter;
  final ValueChanged<String> onLetterSelected;
  final VoidCallback onInteractionEnd;

  String _letterFor(double dy, double height) {
    final index = (dy / height * letters.length).floor().clamp(
      0,
      letters.length - 1,
    );
    return letters[index];
  }

  @override
  Widget build(BuildContext context) {
    // Keep sparse indexes comfortably tappable. Dense A-Z indexes retain the
    // familiar drag-to-scrub interaction within the available screen height.
    final height = (letters.length * 44).clamp(48, 480).toDouble();
    return Semantics(
      label: '联系人字母索引',
      child: GestureDetector(
        key: const Key('contact-alphabet-index'),
        behavior: HitTestBehavior.opaque,
        onTapDown: (details) =>
            onLetterSelected(_letterFor(details.localPosition.dy, height)),
        onTapUp: (_) => onInteractionEnd(),
        onTapCancel: onInteractionEnd,
        onVerticalDragStart: (details) =>
            onLetterSelected(_letterFor(details.localPosition.dy, height)),
        onVerticalDragUpdate: (details) =>
            onLetterSelected(_letterFor(details.localPosition.dy, height)),
        onVerticalDragEnd: (_) => onInteractionEnd(),
        onVerticalDragCancel: onInteractionEnd,
        child: Container(
          width: 44,
          height: height,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: activeLetter == null
                ? Colors.transparent
                : LinliColors.brandMint.withValues(alpha: .88),
            borderRadius: BorderRadius.circular(16),
          ),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.spaceEvenly,
            children: [
              for (final letter in letters)
                Expanded(
                  child: Semantics(
                    button: true,
                    selected: activeLetter == letter,
                    label: '跳转到 $letter',
                    onTap: () {
                      onLetterSelected(letter);
                      onInteractionEnd();
                    },
                    child: ExcludeSemantics(
                      child: Center(
                        child: Text(
                          letter,
                          style: TextStyle(
                            color: activeLetter == letter
                                ? LinliColors.brandGreenDeep
                                : Theme.of(
                                    context,
                                  ).colorScheme.onSurfaceVariant,
                            fontSize: 11,
                            height: 1,
                            fontWeight: activeLetter == letter
                                ? FontWeight.w800
                                : FontWeight.w600,
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class DiscoverTab extends StatelessWidget {
  const DiscoverTab({super.key, required this.controller});
  final AppController controller;

  @override
  Widget build(BuildContext context) => _TopLevelShell(
    title: '探索',
    child: ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
      children: [
        const SectionHeader('常用工具', horizontalInset: 0),
        SectionCard(
          children: [
            SettingTile(
              icon: CupertinoIcons.qrcode_viewfinder,
              title: '扫一扫',
              subtitle: '安全添加联系人和群组',
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => QrScannerScreen(controller: controller),
                ),
              ),
            ),
            SettingTile(
              icon: CupertinoIcons.bookmark,
              title: '收藏夹',
              subtitle: '稍后继续阅读的重要内容',
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => FavoritesScreen(controller: controller),
                ),
              ),
            ),
          ],
        ),
        const SectionHeader('内容与服务', horizontalInset: 0),
        SectionCard(
          children: [
            SettingTile(
              icon: CupertinoIcons.person_2_fill,
              title: '朋友圈',
              subtitle: '发布动态并查看好友互动',
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => MomentsScreen(controller: controller),
                ),
              ),
            ),
            SettingTile(
              icon: CupertinoIcons.smiley_fill,
              title: '表情商店',
              subtitle: '浏览、收藏和管理聊天表情',
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => StickerStoreScreen(controller: controller),
                ),
              ),
            ),
          ],
        ),
      ],
    ),
  );
}

class MeTab extends StatelessWidget {
  const MeTab({
    super.key,
    required this.controller,
    required this.onToggleTheme,
  });
  final AppController controller;
  final VoidCallback onToggleTheme;

  @override
  Widget build(BuildContext context) {
    final user = controller.currentUser;
    return _TopLevelShell(
      title: '我的',
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
        children: [
          SectionCard(
            children: [
              ListTile(
                minTileHeight: 88,
                contentPadding: const EdgeInsets.symmetric(horizontal: 16),
                leading: PersonAvatar(
                  name: user?.name ?? '我',
                  size: 60,
                  avatarUrl: user?.avatarUrl,
                  online: true,
                ),
                title: Text(
                  user?.name ?? '我的账户',
                  style: Theme.of(context).textTheme.titleLarge,
                ),
                subtitle: Text(_profileIdentityLabel(user)),
                trailing: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    IconButton(
                      tooltip: '我的二维码',
                      onPressed: () => Navigator.of(context).push(
                        MaterialPageRoute(
                          builder: (_) =>
                              MyQrCodeScreen(controller: controller),
                        ),
                      ),
                      icon: const Icon(CupertinoIcons.qrcode),
                      style: IconButton.styleFrom(
                        backgroundColor:
                            Theme.of(context).brightness == Brightness.dark
                            ? LinliColors.brandGreen.withValues(alpha: .14)
                            : LinliColors.brandMintStrong,
                        foregroundColor: LinliColors.navy,
                      ),
                    ),
                    const SizedBox(width: 2),
                    Icon(
                      CupertinoIcons.chevron_forward,
                      size: 16,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                  ],
                ),
                onTap: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => EditProfileScreen(controller: controller),
                  ),
                ),
              ),
            ],
          ),
          const SectionHeader('常用'),
          SectionCard(
            children: [
              SettingTile(
                key: const Key('profile-favorites'),
                icon: CupertinoIcons.bookmark_fill,
                title: '我的收藏',
                subtitle: '查看已收藏的消息与文件',
                onTap: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => FavoritesScreen(controller: controller),
                  ),
                ),
              ),
              SettingTile(
                key: const Key('profile-devices'),
                icon: CupertinoIcons.device_phone_portrait,
                title: '登录设备',
                subtitle: '管理当前设备与其他登录会话',
                onTap: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => DevicesScreen(controller: controller),
                  ),
                ),
              ),
              SettingTile(
                key: const Key('profile-settings'),
                icon: CupertinoIcons.settings_solid,
                title: '设置',
                subtitle: '通知、隐私、外观与存储',
                onTap: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => SettingsScreen(
                      controller: controller,
                      onToggleTheme: onToggleTheme,
                    ),
                  ),
                ),
              ),
              SettingTile(
                key: const Key('profile-help'),
                icon: CupertinoIcons.question_circle_fill,
                title: '帮助与反馈',
                subtitle: '常见问题、问题反馈与客服',
                onTap: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => HelpFeedbackScreen(controller: controller),
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _TopLevelShell extends StatelessWidget {
  const _TopLevelShell({
    required this.title,
    required this.child,
    this.action,
    this.desktopMode = false,
  });

  final String title;
  final Widget child;
  final Widget? action;
  final bool desktopMode;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    if (desktopMode) {
      return Material(
        color: Theme.of(context).colorScheme.surfaceContainer,
        child: Column(
          children: [
            SizedBox(
              key: ValueKey('desktop-top-level-header-$title'),
              height: 64,
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 8, 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        title,
                        style: Theme.of(context).textTheme.titleLarge?.copyWith(
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                    ),
                    ?action,
                  ],
                ),
              ),
            ),
            Divider(height: 1, color: Theme.of(context).colorScheme.outline),
            Expanded(child: child),
          ],
        ),
      );
    }
    return ColoredBox(
      color: LinliColors.navy,
      child: SafeArea(
        bottom: false,
        child: Column(
          children: [
            SizedBox(
              key: ValueKey('top-level-header-$title'),
              height: 60,
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 2, 12, 6),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        title,
                        style: Theme.of(context).textTheme.headlineMedium
                            ?.copyWith(color: Colors.white),
                      ),
                    ),
                    ?action,
                  ],
                ),
              ),
            ),
            Expanded(
              child: ClipRRect(
                key: ValueKey('top-level-content-$title'),
                borderRadius: const BorderRadius.vertical(
                  top: Radius.circular(20),
                ),
                child: Material(
                  color: dark
                      ? LinliColors.darkBackground
                      : LinliColors.background,
                  child: child,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

String _profileIdentityLabel(AppUser? user) {
  final handle = publicUserHandle(user?.handle);
  if (handle == null) return '点击完善呱呱号';
  return '呱呱号 · $handle';
}
