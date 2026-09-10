import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/avatar_image.dart';
import '../../core/models.dart';
import '../widgets/linli_widgets.dart';
import '../widgets/user_presence.dart';
import 'relationship_screens.dart';
import 'qr_tools_screen.dart';
import 'settings_screens.dart';
import 'group_invite_members_screen.dart';
import 'group_join_requests_screen.dart';

class GroupManagementScreen extends StatefulWidget {
  const GroupManagementScreen({
    super.key,
    required this.controller,
    required this.conversation,
  });

  final AppController controller;
  final Conversation conversation;

  @override
  State<GroupManagementScreen> createState() => _GroupManagementScreenState();
}

class _GroupManagementScreenState extends State<GroupManagementScreen> {
  GroupProfile? profile;
  List<GroupMember> members = const [];
  bool loading = true;
  bool busy = false;
  Timer? _externalRefreshTimer;
  String? _conversationFingerprint;
  bool _closingUnavailableGroup = false;

  GroupMember? get me => members
      .where((member) => member.user.id == widget.controller.currentUser?.id)
      .firstOrNull;
  bool get isOwner =>
      me?.isOwner == true ||
      profile?.ownerId == widget.controller.currentUser?.id;
  bool get isAdmin => me?.isAdmin == true;
  bool get canEdit => isOwner || isAdmin;

  Conversation? get liveConversation => widget.controller.conversations
      .where((item) => item.id == widget.conversation.id)
      .firstOrNull;

  Conversation get currentConversation =>
      liveConversation ?? widget.conversation;

  @override
  void initState() {
    super.initState();
    _conversationFingerprint = _fingerprint(currentConversation);
    widget.controller.addListener(_handleControllerChange);
    _load();
  }

  @override
  void dispose() {
    _externalRefreshTimer?.cancel();
    widget.controller.removeListener(_handleControllerChange);
    super.dispose();
  }

  String _fingerprint(Conversation conversation) => [
    conversation.title,
    conversation.avatarUrl ?? '',
    conversation.memberCount,
    conversation.currentUserRole ?? '',
    conversation.saved,
    conversation.pinned,
    conversation.muted,
    widget.controller.groupHistoryRevision,
    widget.controller.groupSendPolicyRevision,
  ].join('|');

  void _handleControllerChange() {
    if (!mounted) return;
    final latest = liveConversation;
    if (latest == null) {
      if (_closingUnavailableGroup) return;
      _closingUnavailableGroup = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        Navigator.of(context).popUntil((route) => route.isFirst);
      });
      return;
    }
    final fingerprint = _fingerprint(latest);
    if (fingerprint == _conversationFingerprint) return;
    _conversationFingerprint = fingerprint;
    _externalRefreshTimer?.cancel();
    _externalRefreshTimer = Timer(const Duration(milliseconds: 160), () {
      if (mounted) _load(showLoading: false);
    });
  }

  Future<void> _load({bool showLoading = true}) async {
    if (showLoading) setState(() => loading = true);
    final results = await Future.wait<Object?>([
      widget.controller.loadGroupProfile(widget.conversation.id),
      widget.controller.loadGroupMembers(widget.conversation.id),
    ]);
    if (!mounted) return;
    setState(() {
      profile = results[0] as GroupProfile?;
      members = results[1] as List<GroupMember>? ?? const [];
      loading = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    if (loading) {
      return const Scaffold(
        appBar: GlassAppBar(title: Text('群聊资料')),
        body: Center(child: CupertinoActivityIndicator()),
      );
    }
    if (profile == null) {
      return Scaffold(
        appBar: const GlassAppBar(title: Text('群聊资料')),
        body: StatePanel(
          icon: CupertinoIcons.exclamationmark_bubble,
          title: '群资料加载失败',
          body: widget.controller.error ?? '请检查网络后重试。',
          actionLabel: '重新加载',
          onAction: _load,
        ),
      );
    }
    final value = profile!;
    return AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) {
        final conversation = currentConversation;
        return Scaffold(
          appBar: const GlassAppBar(title: Text('群聊资料')),
          body: ListView(
            key: const Key('group-management-list'),
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 40),
            children: [
              _GroupHeader(
                profile: value,
                memberCount: members.length,
                canEdit: canEdit,
                busy: busy,
                onAvatarTap: canEdit && !busy ? _pickAvatar : null,
              ),
              const SectionHeader('群聊'),
              SectionCard(
                children: [
                  SettingTile(
                    key: const Key('group-save-to-contacts'),
                    icon: CupertinoIcons.book,
                    title: '保存到通讯录',
                    subtitle: conversation.saved
                        ? '可在通讯录的群聊中快速找到'
                        : '保存后可在通讯录中快速找到',
                    trailing: CupertinoSwitch(
                      value: conversation.saved,
                      onChanged: busy ? null : (_) => _toggleSaved(),
                    ),
                  ),
                  SettingTile(
                    key: const Key('group-administrators-entry'),
                    icon: CupertinoIcons.shield,
                    title: '群管理员',
                    subtitle:
                        '${members.where((member) => member.isAdmin).length} 位管理员 · 仅群主可设置',
                    onTap: () => _openMembers(administratorMode: true),
                  ),
                  SettingTile(
                    key: const Key('group-announcement-entry'),
                    icon: CupertinoIcons.doc_text,
                    title: '群公告',
                    subtitle: value.announcement.isEmpty
                        ? '暂无公告'
                        : value.announcement,
                    trailing: value.announcementUnread
                        ? const _UnreadDot()
                        : const Icon(CupertinoIcons.chevron_forward, size: 16),
                    onTap: () async {
                      await Navigator.of(context).push(
                        MaterialPageRoute(
                          builder: (_) => GroupAnnouncementScreen(
                            controller: widget.controller,
                            profile: value,
                            canEdit: canEdit,
                          ),
                        ),
                      );
                      await _load();
                    },
                  ),
                  SettingTile(
                    icon: CupertinoIcons.pencil,
                    title: '群聊名称',
                    subtitle: value.name,
                    onTap: canEdit && !busy ? _editName : null,
                  ),
                  SettingTile(
                    icon: CupertinoIcons.person_crop_circle,
                    title: '我在本群的昵称',
                    subtitle: me?.groupNickname.isNotEmpty == true
                        ? me!.groupNickname
                        : '未设置',
                    onTap: busy ? null : _editNickname,
                  ),
                  if (value.joinPolicy == 'qr')
                    SettingTile(
                      key: const Key('group-qr-entry'),
                      icon: CupertinoIcons.qrcode,
                      title: '群二维码',
                      subtitle: value.qrExpiresAt == null
                          ? '生成可加入本群的二维码'
                          : '二维码 24 小时内有效',
                      onTap: busy ? null : _openGroupQr,
                    ),
                ],
              ),
              const SectionHeader('消息设置'),
              SectionCard(
                children: [
                  SettingTile(
                    icon: CupertinoIcons.pin,
                    title: '置顶聊天',
                    trailing: CupertinoSwitch(
                      value: conversation.pinned,
                      onChanged: busy ? null : (_) => _togglePinned(),
                    ),
                  ),
                  SettingTile(
                    icon: CupertinoIcons.bell_slash,
                    title: '消息免打扰',
                    trailing: CupertinoSwitch(
                      value: conversation.muted,
                      onChanged: busy ? null : (_) => _toggleMuted(),
                    ),
                  ),
                ],
              ),
              if (canEdit) ...[
                SectionHeader(isOwner ? '群主管理' : '群管理员操作'),
                SectionCard(
                  children: [
                    SettingTile(
                      key: const Key('group-message-rate-limit'),
                      icon: CupertinoIcons.speedometer,
                      title: '成员发言频率',
                      subtitle: groupMessageRateLimitLabel(
                        value.memberMessageRateLimitPerMinute,
                      ),
                      onTap: busy ? null : _changeMessageRateLimit,
                    ),
                    if (value.canReviewJoinRequests)
                      SettingTile(
                        key: const Key('group-join-requests-entry'),
                        icon: CupertinoIcons.person_2_square_stack,
                        title: '入群审核',
                        subtitle: value.pendingJoinRequestCount > 0
                            ? '${value.pendingJoinRequestCount} 条待处理申请'
                            : '暂无待处理申请',
                        trailing: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            if (value.pendingJoinRequestCount > 0)
                              Container(
                                key: const Key('group-join-request-badge'),
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 8,
                                  vertical: 3,
                                ),
                                decoration: BoxDecoration(
                                  color: Theme.of(
                                    context,
                                  ).colorScheme.primaryContainer,
                                  borderRadius: BorderRadius.circular(999),
                                ),
                                child: Text('${value.pendingJoinRequestCount}'),
                              ),
                            const SizedBox(width: 6),
                            const Icon(
                              CupertinoIcons.chevron_forward,
                              size: 16,
                            ),
                          ],
                        ),
                        onTap: busy ? null : _openJoinRequests,
                      ),
                    if (isOwner) ...[
                      SettingTile(
                        key: const Key('group-join-policy'),
                        icon: CupertinoIcons.person_badge_plus,
                        title: '入群方式',
                        subtitle: groupJoinPolicyLabel(value.joinPolicy),
                        onTap: busy ? null : _changeJoinPolicy,
                      ),
                      SettingTile(
                        icon: CupertinoIcons.person_2_square_stack,
                        title: '允许同群成员添加好友',
                        subtitle: value.allowMemberAddFriend
                            ? '成员可从群资料发起好友申请'
                            : '群成员资料页不会提供添加入口',
                        trailing: CupertinoSwitch(
                          value: value.allowMemberAddFriend,
                          onChanged: busy ? null : _setMemberFriendPermission,
                        ),
                      ),
                    ],
                  ],
                ),
              ],
              const SectionHeader('历史消息'),
              SectionCard(
                children: [
                  SettingTile(
                    key: const Key('group-history-visibility'),
                    icon: CupertinoIcons.clock,
                    title: '新成员可查看入群前历史',
                    subtitle: value.historyVisibleToNewMembers
                        ? '当前成员可查看全部群历史'
                        : '仅可查看本人本次入群后的消息',
                    trailing: canEdit
                        ? CupertinoSwitch(
                            value: value.historyVisibleToNewMembers,
                            onChanged: busy ? null : _setHistoryVisibility,
                          )
                        : Text(
                            value.historyVisibleToNewMembers ? '已开启' : '已关闭',
                          ),
                  ),
                ],
              ),
              const SectionHeader('安全'),
              SectionCard(
                children: [
                  SettingTile(
                    icon: CupertinoIcons.exclamationmark_triangle,
                    title: '举报群聊',
                    subtitle: '提交群聊信息给平台审核',
                    onTap: () => Navigator.of(context).push(
                      MaterialPageRoute(
                        builder: (_) => ReportScreen(
                          controller: widget.controller,
                          target: value.name,
                          targetId: value.conversationId,
                          targetType: 'group',
                        ),
                      ),
                    ),
                  ),
                  SettingTile(
                    key: Key(isOwner ? 'disband-group' : 'leave-group'),
                    icon: isOwner
                        ? CupertinoIcons.delete_solid
                        : CupertinoIcons.square_arrow_right,
                    title: isOwner ? '解散群聊' : '退出群聊',
                    subtitle: isOwner ? '所有成员将被移出，消息停止同步' : '退出后无法查看后续消息',
                    destructive: true,
                    onTap: busy
                        ? null
                        : isOwner
                        ? _disband
                        : _leave,
                  ),
                ],
              ),
            ],
          ),
        );
      },
    );
  }

  Future<void> _openMembers({bool administratorMode = false}) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => GroupMembersManagementScreen(
          controller: widget.controller,
          conversationId: widget.conversation.id,
          profile: profile!,
          initialMembers: members,
          administratorMode: administratorMode,
        ),
      ),
    );
    if (mounted) await _load();
  }

  Future<void> _editName() async {
    final name = await _textDialog(
      title: '修改群聊名称',
      initial: profile!.name,
      maxLength: 80,
      action: '保存名称',
    );
    if (name == null || name.isEmpty) return;
    await _runProfileUpdate(
      () => widget.controller.updateGroupProfile(
        widget.conversation.id,
        name: name,
      ),
    );
  }

  Future<void> _pickAvatar() async {
    XFile? file;
    try {
      file = await ImagePicker().pickImage(
        source: ImageSource.gallery,
        imageQuality: 88,
        maxWidth: 1600,
        maxHeight: 1600,
      );
      if (file == null) return;
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('无法读取群头像，请检查相册权限后重试')));
      return;
    }
    final selectedFile = file;
    Uint8List bytes;
    try {
      bytes = await selectedFile.readAsBytes();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('无法读取群头像，请检查相册权限后重试')));
      return;
    }
    if (!mounted) return;
    if (bytes.length > 8 * 1024 * 1024) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('群头像不能超过 8 MB')));
      return;
    }
    final mimeType = avatarImageMimeType(bytes);
    if (mimeType == null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('请选择 JPG、PNG 或 WebP 格式的图片')));
      return;
    }
    await _runProfileUpdate(
      () => widget.controller.updateGroupProfile(
        widget.conversation.id,
        avatar: MediaUpload(
          bytes: bytes,
          fileName: selectedFile.name,
          mimeType: mimeType,
          kind: MessageContentKind.image,
        ),
      ),
    );
  }

  Future<void> _openGroupQr() async {
    var current = profile!;
    final expired = current.qrExpiresAt?.isBefore(DateTime.now()) ?? true;
    if (current.qrToken == null || current.qrToken!.isEmpty || expired) {
      if (!isOwner) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('群二维码已过期，请联系群主刷新')));
        return;
      }
      setState(() => busy = true);
      final updated = await widget.controller.updateGroupProfile(
        widget.conversation.id,
        rotateQr: true,
      );
      if (!mounted) return;
      setState(() => busy = false);
      if (updated == null || updated.qrToken == null) {
        _showError();
        return;
      }
      current = updated;
      setState(() => profile = updated);
    }
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => GroupQrCodeScreen(
          groupName: current.name,
          token: current.qrToken!,
          expiresAt: current.qrExpiresAt,
        ),
      ),
    );
  }

  Future<void> _editNickname() async {
    final nickname = await _textDialog(
      title: '我在本群的昵称',
      initial: me?.groupNickname ?? '',
      maxLength: 40,
      action: '保存群昵称',
    );
    if (nickname == null) return;
    setState(() => busy = true);
    final success = await widget.controller.setGroupNickname(
      widget.conversation.id,
      nickname,
    );
    if (!mounted) return;
    setState(() => busy = false);
    if (success) {
      await _load();
    } else {
      _showError();
    }
  }

  Future<void> _togglePinned() async {
    setState(() => busy = true);
    final success = await widget.controller.toggleConversationPinned(
      widget.conversation.id,
    );
    if (!mounted) return;
    setState(() => busy = false);
    if (!success) _showError();
  }

  Future<void> _toggleSaved() async {
    setState(() => busy = true);
    final success = await widget.controller.toggleConversationSaved(
      widget.conversation.id,
    );
    if (!mounted) return;
    setState(() => busy = false);
    if (!success) _showError();
  }

  Future<void> _toggleMuted() async {
    setState(() => busy = true);
    final success = await widget.controller.toggleConversationMuted(
      widget.conversation.id,
    );
    if (!mounted) return;
    setState(() => busy = false);
    if (!success) _showError();
  }

  Future<void> _changeJoinPolicy() async {
    final selected = await showModalBottomSheet<String>(
      context: context,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final policy in [
              'invite',
              'manager_invite',
              'member_approval',
              'qr',
              'closed',
            ])
              ListTile(
                leading: Icon(
                  profile!.joinPolicy == policy
                      ? CupertinoIcons.checkmark_circle_fill
                      : CupertinoIcons.circle,
                ),
                title: Text(groupJoinPolicyLabel(policy)),
                subtitle: Text(groupJoinPolicyDescription(policy)),
                onTap: () => Navigator.pop(context, policy),
              ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
    if (selected == null || selected == profile!.joinPolicy) return;
    final confirmed = await _confirm(
      title: '修改入群方式？',
      message:
          '将入群方式改为“${groupJoinPolicyLabel(selected)}”。所有尚未处理的入群申请会立即失效，不能继续审核。',
      action: '确认修改',
    );
    if (!confirmed || !mounted) return;
    await _runProfileUpdate(
      () => widget.controller.updateGroupProfile(
        widget.conversation.id,
        joinPolicy: selected,
      ),
    );
  }

  Future<void> _openJoinRequests() async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => GroupJoinRequestsScreen(
          controller: widget.controller,
          conversationId: widget.conversation.id,
        ),
      ),
    );
    if (mounted) await _load(showLoading: false);
  }

  Future<void> _changeMessageRateLimit() async {
    final selected = await showCupertinoModalPopup<int>(
      context: context,
      builder: (context) => CupertinoActionSheet(
        title: const Text('成员发言频率'),
        message: const Text('仅限制普通成员，群主和群管理员不受限制。'),
        actions: [
          for (final limit in const [0, 5, 10, 20])
            CupertinoActionSheetAction(
              isDefaultAction:
                  profile!.memberMessageRateLimitPerMinute == limit,
              onPressed: () => Navigator.pop(context, limit),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  if (profile!.memberMessageRateLimitPerMinute == limit) ...[
                    const Icon(CupertinoIcons.check_mark, size: 18),
                    const SizedBox(width: 8),
                  ],
                  Text(groupMessageRateLimitOptionLabel(limit)),
                ],
              ),
            ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ),
    );
    if (selected == null ||
        selected == profile!.memberMessageRateLimitPerMinute ||
        !mounted) {
      return;
    }
    await _runProfileUpdate(
      () => widget.controller.updateGroupProfile(
        widget.conversation.id,
        memberMessageRateLimitPerMinute: selected,
      ),
    );
  }

  Future<void> _setHistoryVisibility(bool value) async {
    final confirmed = await _confirm(
      title: value ? '开放入群前历史？' : '隐藏入群前历史？',
      message: value
          ? '所有当前成员将可查看全部群历史。不会将此前历史计为新未读。'
          : '对所有当前成员生效，仅保留本次入群后的消息可见。群主和管理员同样受限；已导出的内容无法收回。',
      action: '确认修改',
    );
    if (!confirmed || !mounted) return;
    await _runProfileUpdate(
      () => widget.controller.updateGroupProfile(
        widget.conversation.id,
        historyVisibleToNewMembers: value,
      ),
    );
  }

  Future<void> _setMemberFriendPermission(bool value) => _runProfileUpdate(
    () => widget.controller.updateGroupProfile(
      widget.conversation.id,
      allowMemberAddFriend: value,
    ),
  );

  Future<void> _runProfileUpdate(
    Future<GroupProfile?> Function() operation,
  ) async {
    setState(() => busy = true);
    final updated = await operation();
    if (!mounted) return;
    setState(() {
      busy = false;
      if (updated != null) profile = updated;
    });
    if (updated == null) _showError();
  }

  Future<void> _leave() async {
    final confirmed = await _confirm(
      title: '退出 ${profile!.name}？',
      message: '退出后你将无法查看新消息，如需重新加入要再次获得邀请。',
      action: '退出群聊',
    );
    if (!confirmed) return;
    setState(() => busy = true);
    final success = await widget.controller.leaveGroup(widget.conversation.id);
    if (!mounted) return;
    if (success) {
      Navigator.of(context).popUntil((route) => route.isFirst);
    } else {
      setState(() => busy = false);
      _showError();
    }
  }

  Future<void> _disband() async {
    final reason = await showDialog<String>(
      context: context,
      builder: (_) => _DisbandGroupDialog(groupName: profile!.name),
    );
    if (reason == null) return;
    setState(() => busy = true);
    final success = await widget.controller.disbandGroup(
      widget.conversation.id,
      reason,
    );
    if (!mounted) return;
    if (success) {
      Navigator.of(context).popUntil((route) => route.isFirst);
    } else {
      setState(() => busy = false);
      _showError();
    }
  }

  Future<String?> _textDialog({
    required String title,
    required String initial,
    required int maxLength,
    required String action,
  }) async {
    final controller = TextEditingController(text: initial);
    final result = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(title),
        content: TextField(
          controller: controller,
          autofocus: true,
          maxLength: maxLength,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, controller.text.trim()),
            child: Text(action),
          ),
        ],
      ),
    );
    controller.dispose();
    return result;
  }

  Future<bool> _confirm({
    required String title,
    required String message,
    required String action,
  }) async =>
      await showCupertinoDialog<bool>(
        context: context,
        builder: (context) => CupertinoAlertDialog(
          title: Text(title),
          content: Text(message),
          actions: [
            CupertinoDialogAction(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            CupertinoDialogAction(
              isDestructiveAction: true,
              onPressed: () => Navigator.pop(context, true),
              child: Text(action),
            ),
          ],
        ),
      ) ??
      false;

  void _showError() => ScaffoldMessenger.of(context).showSnackBar(
    SnackBar(content: Text(widget.controller.error ?? '操作失败，请重试')),
  );
}

class _GroupHeader extends StatelessWidget {
  const _GroupHeader({
    required this.profile,
    required this.memberCount,
    required this.canEdit,
    required this.busy,
    this.onAvatarTap,
  });
  final GroupProfile profile;
  final int memberCount;
  final bool canEdit;
  final bool busy;
  final VoidCallback? onAvatarTap;

  @override
  Widget build(BuildContext context) => Column(
    children: [
      Semantics(
        key: const Key('group-avatar-button'),
        button: canEdit,
        label: canEdit ? '修改群头像' : '${profile.name}群头像',
        child: GestureDetector(
          onTap: onAvatarTap,
          child: Stack(
            clipBehavior: Clip.none,
            children: [
              PersonAvatar(
                name: profile.name,
                size: 84,
                avatarUrl: profile.avatarUrl,
              ),
              if (canEdit)
                Positioned(
                  right: -2,
                  bottom: -2,
                  child: DecoratedBox(
                    decoration: BoxDecoration(
                      color: Theme.of(context).colorScheme.primary,
                      shape: BoxShape.circle,
                      border: Border.all(
                        color: Theme.of(context).scaffoldBackgroundColor,
                        width: 2,
                      ),
                    ),
                    child: SizedBox.square(
                      dimension: 28,
                      child: Center(
                        child: busy
                            ? const CupertinoActivityIndicator(radius: 7)
                            : const Icon(
                                CupertinoIcons.camera_fill,
                                color: Colors.white,
                                size: 15,
                              ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
      const SizedBox(height: 12),
      Text(
        profile.name,
        textAlign: TextAlign.center,
        style: Theme.of(context).textTheme.headlineMedium,
      ),
      const SizedBox(height: 4),
      Text('$memberCount 位成员', style: Theme.of(context).textTheme.bodyMedium),
    ],
  );
}

class _UnreadDot extends StatelessWidget {
  const _UnreadDot();
  @override
  Widget build(BuildContext context) => Container(
    width: 9,
    height: 9,
    decoration: const BoxDecoration(
      color: LinliColors.unread,
      shape: BoxShape.circle,
    ),
  );
}

class GroupAnnouncementScreen extends StatefulWidget {
  const GroupAnnouncementScreen({
    super.key,
    required this.controller,
    required this.profile,
    required this.canEdit,
  });
  final AppController controller;
  final GroupProfile profile;
  final bool canEdit;

  @override
  State<GroupAnnouncementScreen> createState() =>
      _GroupAnnouncementScreenState();
}

class _GroupAnnouncementScreenState extends State<GroupAnnouncementScreen>
    with WidgetsBindingObserver {
  late GroupProfile profile = widget.profile;
  bool editing = false;
  bool saving = false;
  bool refreshing = true;
  String? loadError;
  int _request = 0;
  late int _revision;
  Timer? _refreshTimer;
  int? _readVersion;
  DateTime? _readAt;
  late final TextEditingController controller = TextEditingController(
    text: profile.announcement,
  );

  bool get canEdit {
    final conversation = widget.controller.conversations
        .where((item) => item.id == profile.conversationId)
        .firstOrNull;
    final role = conversation?.currentUserRole;
    return role == null ? widget.canEdit : role == 'owner' || role == 'admin';
  }

  @override
  void initState() {
    super.initState();
    _revision = widget.controller.groupSendPolicyRevision;
    WidgetsBinding.instance.addObserver(this);
    widget.controller.addListener(_handleChange);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) unawaited(_refresh());
    });
  }

  void _handleChange() {
    if (_revision == widget.controller.groupSendPolicyRevision) return;
    _revision = widget.controller.groupSendPolicyRevision;
    _refreshTimer?.cancel();
    _refreshTimer = Timer(const Duration(milliseconds: 160), () {
      if (mounted) unawaited(_refresh());
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(_refresh());
  }

  Future<void> _refresh() async {
    if (saving) return; // A save always refreshes again when it completes.
    final request = ++_request;
    final userId = widget.controller.currentUser?.id;
    setState(() {
      refreshing = true;
      loadError = null;
    });
    final latest = await widget.controller.loadGroupProfile(
      profile.conversationId,
    );
    if (!mounted ||
        request != _request ||
        userId != widget.controller.currentUser?.id) {
      return;
    }
    setState(() {
      refreshing = false;
      if (latest == null) {
        loadError = widget.controller.error ?? '群公告加载失败，请重试';
      } else {
        profile = latest;
        // A remote update must never overwrite an administrator's edit draft.
        if (!editing) controller.text = latest.announcement;
      }
    });
    if (latest == null ||
        !latest.announcementUnread ||
        editing ||
        _readVersion == latest.announcementVersion) {
      return;
    }
    final success = await widget.controller.markGroupAnnouncementRead(
      profile.conversationId,
    );
    if (!mounted ||
        request != _request ||
        userId != widget.controller.currentUser?.id) {
      return;
    }
    if (success) {
      setState(() {
        _readVersion = latest.announcementVersion;
        _readAt = DateTime.now();
      });
    }
  }

  @override
  void dispose() {
    _request++;
    _refreshTimer?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    widget.controller.removeListener(_handleChange);
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: const Text('群公告'),
      actions: [
        IconButton(
          key: const Key('refresh-group-announcement'),
          tooltip: '刷新公告',
          onPressed: refreshing || saving ? null : _refresh,
          icon: const Icon(CupertinoIcons.refresh, size: 20),
        ),
        if (canEdit)
          TextButton(
            onPressed: saving
                ? null
                : editing
                ? _save
                : () => setState(() => editing = true),
            child: Text(editing ? '发布' : '编辑'),
          ),
      ],
    ),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 40),
      children: [
        if (refreshing) const LinearProgressIndicator(minHeight: 2),
        if (loadError != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: Text(
              loadError!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ),
        if (editing)
          TextField(
            key: const Key('group-announcement-input'),
            controller: controller,
            autofocus: true,
            maxLength: 2000,
            minLines: 8,
            maxLines: 20,
            decoration: const InputDecoration(hintText: '填写群公告内容'),
          )
        else if (profile.announcement.isEmpty)
          const StatePanel(
            icon: CupertinoIcons.doc_text,
            title: '暂无群公告',
            body: '群主或管理员发布后会显示在这里。',
          )
        else ...[
          SelectableText(
            profile.announcement,
            style: Theme.of(context).textTheme.bodyLarge,
          ),
          const SizedBox(height: 24),
          Text(
            '版本 ${profile.announcementVersion} · 更新于 ${_displayDate(profile.updatedAt)}',
            style: Theme.of(context).textTheme.bodySmall,
          ),
          if (profile.announcementReadAt != null ||
              _readVersion == profile.announcementVersion)
            Text(
              '已读于 ${_displayDate(profile.announcementReadAt ?? _readAt!)}',
              style: Theme.of(context).textTheme.bodySmall,
            ),
        ],
      ],
    ),
  );

  Future<void> _save() async {
    if (saving || !canEdit) return;
    _request++; // Ignore refreshes started before this publication.
    setState(() => saving = true);
    final updated = await widget.controller.saveGroupAnnouncement(
      profile.conversationId,
      controller.text,
    );
    if (!mounted) return;
    setState(() {
      saving = false;
      if (updated != null) {
        profile = updated;
        editing = false;
      }
    });
    if (updated == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '发布失败')),
      );
    }
    unawaited(_refresh());
  }
}

/// Direct entry from the chat-info preview, without going through settings.
class GroupMembersOverviewScreen extends StatefulWidget {
  const GroupMembersOverviewScreen({
    super.key,
    required this.controller,
    required this.conversationId,
  });
  final AppController controller;
  final String conversationId;
  @override
  State<GroupMembersOverviewScreen> createState() =>
      _GroupMembersOverviewScreenState();
}

class _GroupMembersOverviewScreenState
    extends State<GroupMembersOverviewScreen> {
  GroupProfile? _profile;
  List<GroupMember>? _members;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final accountId = widget.controller.currentUser?.id;
    setState(() => _loading = true);
    final results = await Future.wait<Object?>([
      widget.controller.loadGroupProfile(widget.conversationId),
      widget.controller.loadGroupMembers(widget.conversationId),
    ]);
    if (!mounted || accountId != widget.controller.currentUser?.id) return;
    setState(() {
      _profile = results[0] as GroupProfile?;
      _members = results[1] as List<GroupMember>?;
      _loading = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    if (!_loading && _profile != null && _members != null) {
      return GroupMembersManagementScreen(
        controller: widget.controller,
        conversationId: widget.conversationId,
        profile: _profile!,
        initialMembers: _members!,
      );
    }
    return Scaffold(
      appBar: const GlassAppBar(title: Text('群成员')),
      body: _loading
          ? const Center(child: CupertinoActivityIndicator())
          : StatePanel(
              icon: CupertinoIcons.exclamationmark_bubble,
              title: '群成员加载失败',
              body: '请检查网络后重试。',
              actionLabel: '重新加载',
              onAction: _load,
            ),
    );
  }
}

class GroupMembersManagementScreen extends StatefulWidget {
  const GroupMembersManagementScreen({
    super.key,
    required this.controller,
    required this.conversationId,
    required this.profile,
    required this.initialMembers,
    this.administratorMode = false,
  });
  final AppController controller;
  final String conversationId;
  final GroupProfile profile;
  final List<GroupMember> initialMembers;
  final bool administratorMode;

  @override
  State<GroupMembersManagementScreen> createState() =>
      _GroupMembersManagementScreenState();
}

class _GroupMembersManagementScreenState
    extends State<GroupMembersManagementScreen> {
  late GroupProfile profile = widget.profile;
  late List<GroupMember> members = widget.initialMembers;
  bool loading = false;
  bool changingRole = false;
  bool submittingRole = false;
  int _reloadRequest = 0;
  String query = '';
  Timer? _externalRefreshTimer;
  String? _conversationFingerprint;

  GroupMember? get me => members
      .where((member) => member.user.id == widget.controller.currentUser?.id)
      .firstOrNull;
  bool get isOwner => me?.isOwner == true;
  bool get isAdmin => me?.isAdmin == true;
  bool get canInviteMembers =>
      profile.canDirectInvite || profile.canSubmitJoinRequest;
  List<GroupMember> get visibleMembers {
    final candidates = widget.administratorMode
        ? [
            ...members.where((member) => member.isOwner),
            ...members.where((member) => member.isAdmin),
            if (isOwner)
              ...members.where((member) => !member.isOwner && !member.isAdmin),
          ]
        : members;
    final normalized = query.trim().toLowerCase();
    if (normalized.isEmpty) return candidates;
    return candidates.where((member) {
      final values = [
        member.user.name,
        if (widget.controller.canViewGroupMemberHandle(widget.conversationId))
          member.user.handle,
        member.groupNickname,
        widget.controller.displayNameFor(
          member.user,
          groupNickname: member.groupNickname,
        ),
      ];
      return values.any((value) => value.toLowerCase().contains(normalized));
    }).toList();
  }

  Conversation? get liveConversation => widget.controller.conversations
      .where((item) => item.id == widget.conversationId)
      .firstOrNull;

  @override
  void initState() {
    super.initState();
    _conversationFingerprint = _fingerprint(liveConversation);
    widget.controller.addListener(_handleControllerChange);
  }

  @override
  void dispose() {
    _externalRefreshTimer?.cancel();
    widget.controller.removeListener(_handleControllerChange);
    super.dispose();
  }

  String _fingerprint(Conversation? conversation) => conversation == null
      ? 'removed'
      : [
          conversation.memberCount,
          conversation.currentUserRole ?? '',
          conversation.title,
          conversation.avatarUrl ?? '',
          widget.controller.groupSendPolicyRevision,
        ].join('|');

  void _handleControllerChange() {
    if (!mounted) return;
    final fingerprint = _fingerprint(liveConversation);
    if (fingerprint == _conversationFingerprint) return;
    _conversationFingerprint = fingerprint;
    if (fingerprint == 'removed') return;
    _externalRefreshTimer?.cancel();
    _externalRefreshTimer = Timer(const Duration(milliseconds: 160), () {
      if (mounted) _reload();
    });
  }

  Future<void> _reload() async {
    _externalRefreshTimer?.cancel();
    final request = ++_reloadRequest;
    setState(() => loading = true);
    final results = await Future.wait([
      widget.controller.loadGroupProfile(widget.conversationId),
      widget.controller.loadGroupMembers(widget.conversationId),
    ]);
    if (!mounted || request != _reloadRequest) return;
    final updatedProfile = results[0] as GroupProfile?;
    final updatedMembers = results[1] as List<GroupMember>?;
    setState(() {
      loading = false;
      if (updatedProfile != null) profile = updatedProfile;
      if (updatedMembers != null) members = updatedMembers;
    });
    if (updatedProfile == null || updatedMembers == null) {
      _showFeedback(widget.controller.error ?? '群资料加载失败，请稍后重试');
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) => _buildContent(context),
  );

  Widget _buildContent(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: Text(
        widget.administratorMode
            ? '群管理员 · ${members.where((member) => member.isAdmin).length}'
            : '群成员 · ${members.length}',
      ),
      actions: [
        IconButton(
          tooltip: '刷新群成员',
          onPressed: loading || changingRole ? null : _reload,
          icon: const Icon(CupertinoIcons.refresh),
        ),
      ],
    ),
    body: ListView(
      padding: const EdgeInsets.only(bottom: 32),
      children: [
        if (loading || submittingRole)
          const LinearProgressIndicator(minHeight: 2),
        if (widget.administratorMode)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
            child: Text(
              isOwner
                  ? '开启成员右侧开关可设为管理员，关闭可取消。管理员可协助管理群聊；只有群主能设置管理员。'
                  : '仅群主可设置或取消管理员，以下为本群的群主和管理员。',
              style: Theme.of(context).textTheme.bodyMedium,
            ),
          ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: CupertinoSearchTextField(
            style: TextStyle(color: context.linli.text),
            placeholderStyle: TextStyle(color: context.linli.secondaryText),
            itemColor: context.linli.secondaryText,
            backgroundColor: context.linli.elevated,
            key: const Key('group-member-search'),
            placeholder:
                widget.controller.canViewGroupMemberHandle(
                  widget.conversationId,
                )
                ? '搜索昵称、备注或呱呱号'
                : '搜索昵称或备注',
            onChanged: (value) => setState(() => query = value),
          ),
        ),
        if (!widget.administratorMode && canInviteMembers)
          ListTile(
            key: const Key('add-group-members'),
            contentPadding: const EdgeInsets.symmetric(horizontal: 16),
            leading: Container(
              width: 48,
              height: 48,
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHigh,
                borderRadius: BorderRadius.circular(14),
              ),
              child: const Icon(CupertinoIcons.person_add),
            ),
            title: Text(isOwner || isAdmin ? '添加群成员' : '邀请好友入群'),
            subtitle: Text(
              isOwner || isAdmin || profile.joinPolicy == 'invite'
                  ? '选择好友后直接加入群聊'
                  : '提交后由群主或管理员审核',
            ),
            onTap: loading || changingRole ? null : _pickMembers,
          ),
        if (!widget.administratorMode && !canInviteMembers)
          ListTile(
            key: const Key('group-invite-restricted'),
            contentPadding: const EdgeInsets.symmetric(horizontal: 16),
            leading: const Icon(CupertinoIcons.lock),
            title: const Text('当前无法邀请成员'),
            subtitle: Text(groupJoinPolicyDescription(profile.joinPolicy)),
          ),
        const Divider(),
        if (visibleMembers.isEmpty)
          const Padding(
            padding: EdgeInsets.all(32),
            child: Center(child: Text('没有匹配的群成员')),
          )
        else
          for (final member in visibleMembers) _memberTile(member),
      ],
    ),
  );

  Widget _memberTile(GroupMember member) => ListTile(
    key: ValueKey('group-member-${member.user.id}'),
    contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
    leading: PersonAvatar(
      name: widget.controller.displayNameFor(
        member.user,
        groupNickname: member.groupNickname,
      ),
      avatarUrl: member.user.avatarUrl,
    ),
    title: Text(
      widget.controller.displayNameFor(
        member.user,
        groupNickname: member.groupNickname,
      ),
    ),
    subtitle:
        widget.controller.canViewGroupMemberPresence(widget.conversationId)
        ? UserPresence(
            controller: widget.controller,
            userId: member.user.id,
            groupId: widget.conversationId,
            builder: (context, status) => Wrap(
              spacing: 8,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                Text(_memberStatusLabel(member)),
                PresenceLabel(status),
              ],
            ),
          )
        : Text(_memberStatusLabel(member)),
    trailing: widget.administratorMode
        ? isOwner && !member.isOwner
              ? CupertinoSwitch(
                  key: ValueKey('group-admin-switch-${member.user.id}'),
                  value: member.isAdmin,
                  onChanged: loading || changingRole
                      ? null
                      : (_) => _setAdministrator(member),
                )
              : Text(_roleLabel(member.role))
        : _canManage(member)
        ? IconButton(
            tooltip: '管理成员',
            onPressed: loading || changingRole
                ? null
                : () => _memberActions(member),
            icon: const Icon(CupertinoIcons.ellipsis),
          )
        : const Icon(CupertinoIcons.chevron_forward, size: 16),
    onTap: () => Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => FriendProfileScreen(
          controller: widget.controller,
          user: member.user,
          requestSource: 'group',
          requestSourceId: widget.conversationId,
        ),
      ),
    ),
  );

  bool _canManage(GroupMember member) {
    if (member.user.id == widget.controller.currentUser?.id) return false;
    if (isOwner) return true;
    return isAdmin && !member.isOwner && !member.isAdmin;
  }

  String _memberStatusLabel(GroupMember member) {
    final role = _roleLabel(member.role);
    if (!member.isMuted) return role;
    if (member.mutedPermanently) return '$role · 已永久禁言';
    final until = member.mutedUntil?.toLocal();
    if (until == null) return '$role · 已禁言';
    final localizations = MaterialLocalizations.of(context);
    final date = localizations.formatShortDate(until);
    final time = localizations.formatTimeOfDay(TimeOfDay.fromDateTime(until));
    return '$role · 禁言至 $date $time';
  }

  Future<void> _pickMembers() async {
    await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => GroupInviteMembersScreen(
          controller: widget.controller,
          conversationId: widget.conversationId,
        ),
      ),
    );
    if (mounted) await _reload();
  }

  Future<void> _memberActions(GroupMember member) async {
    if (loading || changingRole || !_canManage(member)) return;
    final action = await showModalBottomSheet<String>(
      context: context,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (isOwner && !member.isOwner)
              ListTile(
                leading: const Icon(CupertinoIcons.shield),
                title: Text(member.isAdmin ? '取消管理员' : '设为管理员'),
                onTap: () =>
                    Navigator.pop(context, member.isAdmin ? 'member' : 'admin'),
              ),
            if (isOwner && !member.isOwner)
              ListTile(
                leading: const Icon(
                  CupertinoIcons.person_crop_circle_badge_checkmark,
                ),
                title: const Text('转让群主'),
                onTap: () => Navigator.pop(context, 'transfer'),
              ),
            ListTile(
              key: const Key('group-member-mute-action'),
              leading: const Icon(CupertinoIcons.speaker_slash),
              title: Text(member.isMuted ? '调整禁言时长' : '设置禁言'),
              onTap: () => Navigator.pop(context, 'mute'),
            ),
            if (member.isMuted)
              ListTile(
                key: const Key('group-member-unmute-action'),
                leading: const Icon(CupertinoIcons.speaker_2),
                title: const Text('解除禁言'),
                onTap: () => Navigator.pop(context, 'unmute'),
              ),
            ListTile(
              leading: const Icon(
                CupertinoIcons.person_crop_circle_badge_minus,
                color: LinliColors.systemRed,
              ),
              title: const Text(
                '移出群聊',
                style: TextStyle(color: LinliColors.systemRed),
              ),
              onTap: () => Navigator.pop(context, 'remove'),
            ),
          ],
        ),
      ),
    );
    if (action == null || !mounted) return;
    if (action == 'admin' || action == 'member') {
      await _setAdministrator(member);
      return;
    }
    DateTime? muteUntil;
    var mutePermanently = false;
    if (action == 'mute') {
      final selection = await _pickMuteSelection(member);
      if (selection == null || !mounted) return;
      muteUntil = selection.until;
      mutePermanently = selection.permanently;
    }
    if (action == 'transfer') {
      if (!mounted) return;
      final confirm = await showCupertinoDialog<bool>(
        context: context,
        builder: (context) => CupertinoAlertDialog(
          title: Text(
            '将群主转让给 ${widget.controller.displayNameFor(member.user, groupNickname: member.groupNickname)}？',
          ),
          content: const Text('转让后你将变为普通成员，此操作需要新群主再次转让才能恢复。'),
          actions: [
            CupertinoDialogAction(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            CupertinoDialogAction(
              isDestructiveAction: true,
              onPressed: () => Navigator.pop(context, true),
              child: const Text('确认转让'),
            ),
          ],
        ),
      );
      if (confirm != true) return;
    }
    setState(() => loading = true);
    final success = switch (action) {
      'transfer' => await widget.controller.transferGroupOwner(
        widget.conversationId,
        member.user,
      ),
      'remove' => await widget.controller.removeGroupMember(
        widget.conversationId,
        member.user,
      ),
      'mute' => await widget.controller.setGroupMemberMuted(
        widget.conversationId,
        member.user,
        muteUntil,
        permanently: mutePermanently,
      ),
      'unmute' => await widget.controller.setGroupMemberMuted(
        widget.conversationId,
        member.user,
        null,
      ),
      _ => false,
    };
    if (!mounted) return;
    if (success) {
      await _reload();
    } else {
      setState(() => loading = false);
      _showFeedback(widget.controller.error ?? '操作失败，请稍后重试');
    }
  }

  Future<_MemberMuteSelection?> _pickMuteSelection(GroupMember member) async {
    final option = await showModalBottomSheet<_MemberMuteOption>(
      context: context,
      isScrollControlled: true,
      builder: (sheetContext) => SafeArea(
        child: ConstrainedBox(
          constraints: BoxConstraints(
            maxHeight: MediaQuery.sizeOf(sheetContext).height * 0.82,
          ),
          child: ListView(
            shrinkWrap: true,
            children: [
              ListTile(
                title: Text(
                  '禁言 ${widget.controller.displayNameFor(member.user, groupNickname: member.groupNickname)}',
                  style: Theme.of(sheetContext).textTheme.titleMedium,
                ),
                subtitle: const Text('请选择禁言时长'),
              ),
              _muteOptionTile(
                sheetContext,
                key: 'group-member-mute-10-minutes',
                option: _MemberMuteOption.tenMinutes,
                title: '10 分钟',
              ),
              _muteOptionTile(
                sheetContext,
                key: 'group-member-mute-1-hour',
                option: _MemberMuteOption.oneHour,
                title: '1 小时',
              ),
              _muteOptionTile(
                sheetContext,
                key: 'group-member-mute-24-hours',
                option: _MemberMuteOption.twentyFourHours,
                title: '24 小时',
              ),
              _muteOptionTile(
                sheetContext,
                key: 'group-member-mute-7-days',
                option: _MemberMuteOption.sevenDays,
                title: '7 天',
              ),
              _muteOptionTile(
                sheetContext,
                key: 'group-member-mute-custom',
                option: _MemberMuteOption.custom,
                title: '自定义结束时间',
                icon: CupertinoIcons.calendar,
              ),
              _muteOptionTile(
                sheetContext,
                key: 'group-member-mute-permanent',
                option: _MemberMuteOption.permanent,
                title: '永久禁言',
                icon: CupertinoIcons.infinite,
                destructive: true,
              ),
            ],
          ),
        ),
      ),
    );
    if (option == null || !mounted) return null;
    final now = DateTime.now();
    return switch (option) {
      _MemberMuteOption.tenMinutes => _MemberMuteSelection(
        until: now.add(const Duration(minutes: 10)),
      ),
      _MemberMuteOption.oneHour => _MemberMuteSelection(
        until: now.add(const Duration(hours: 1)),
      ),
      _MemberMuteOption.twentyFourHours => _MemberMuteSelection(
        until: now.add(const Duration(hours: 24)),
      ),
      _MemberMuteOption.sevenDays => _MemberMuteSelection(
        until: now.add(const Duration(days: 7)),
      ),
      _MemberMuteOption.permanent => const _MemberMuteSelection(
        permanently: true,
      ),
      _MemberMuteOption.custom => await _pickCustomMuteUntil(now),
    };
  }

  Widget _muteOptionTile(
    BuildContext sheetContext, {
    required String key,
    required _MemberMuteOption option,
    required String title,
    IconData icon = CupertinoIcons.clock,
    bool destructive = false,
  }) => ListTile(
    key: Key(key),
    leading: Icon(icon, color: destructive ? LinliColors.systemRed : null),
    title: Text(
      title,
      style: destructive ? const TextStyle(color: LinliColors.systemRed) : null,
    ),
    onTap: () => Navigator.pop(sheetContext, option),
  );

  Future<_MemberMuteSelection?> _pickCustomMuteUntil(DateTime now) async {
    final initial = now.add(const Duration(days: 1));
    final date = await showDatePicker(
      context: context,
      initialDate: DateUtils.dateOnly(initial),
      firstDate: DateUtils.dateOnly(now),
      lastDate: DateTime(now.year + 10, 12, 31),
      helpText: '选择禁言结束日期',
    );
    if (date == null || !mounted) return null;
    final time = await showTimePicker(
      context: context,
      initialTime: TimeOfDay.fromDateTime(initial),
      helpText: '选择禁言结束时间',
    );
    if (time == null || !mounted) return null;
    final until = DateTime(
      date.year,
      date.month,
      date.day,
      time.hour,
      time.minute,
    );
    if (!until.isAfter(DateTime.now())) {
      _showFeedback('禁言结束时间必须晚于当前时间');
      return null;
    }
    return _MemberMuteSelection(until: until);
  }

  void _showFeedback(String message) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _setAdministrator(GroupMember member) async {
    if (loading || changingRole || !isOwner || member.isOwner) return;
    final makeAdmin = !member.isAdmin;
    final name = widget.controller.displayNameFor(
      member.user,
      groupNickname: member.groupNickname,
    );
    setState(() => changingRole = true);
    try {
      final confirmed = await showCupertinoDialog<bool>(
        context: context,
        builder: (context) => CupertinoAlertDialog(
          title: Text(makeAdmin ? '设为管理员' : '取消管理员'),
          content: Text(
            makeAdmin
                ? '将“$name”设为管理员？对方将获得群管理权限。'
                : '取消“$name”的管理员身份？对方仍会保留在群聊中。',
          ),
          actions: [
            CupertinoDialogAction(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            CupertinoDialogAction(
              key: const Key('confirm-group-administrator'),
              isDestructiveAction: !makeAdmin,
              onPressed: () => Navigator.pop(context, true),
              child: const Text('确认'),
            ),
          ],
        ),
      );
      if (!mounted || confirmed != true) return;
      final current = members
          .where((item) => item.user.id == member.user.id)
          .firstOrNull;
      if (!isOwner ||
          current == null ||
          current.isOwner ||
          liveConversation == null) {
        _showFeedback('成员身份已变化，请刷新后重试');
        return;
      }
      setState(() => submittingRole = true);
      final success = await widget.controller.setGroupRole(
        widget.conversationId,
        member.user,
        makeAdmin ? 'admin' : 'member',
      );
      if (!mounted) return;
      if (!success) {
        _showFeedback(widget.controller.error ?? '群管理员设置失败');
        return;
      }
      _showFeedback(makeAdmin ? '已将“$name”设为管理员' : '已取消“$name”的管理员身份');
      await _reload();
    } finally {
      if (mounted) {
        setState(() {
          changingRole = false;
          submittingRole = false;
        });
      }
    }
  }
}

class _DisbandGroupDialog extends StatefulWidget {
  const _DisbandGroupDialog({required this.groupName});
  final String groupName;

  @override
  State<_DisbandGroupDialog> createState() => _DisbandGroupDialogState();
}

class _DisbandGroupDialogState extends State<_DisbandGroupDialog> {
  final confirm = TextEditingController();
  final reason = TextEditingController();

  @override
  void dispose() {
    confirm.dispose();
    reason.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('解散群聊'),
    content: Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('此操作不可恢复。请输入完整群名“${widget.groupName}”确认。'),
        const SizedBox(height: 12),
        TextField(
          key: const Key('disband-group-name-confirm'),
          controller: confirm,
          onChanged: (_) => setState(() {}),
          decoration: const InputDecoration(labelText: '完整群名'),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: reason,
          maxLength: 200,
          decoration: const InputDecoration(labelText: '解散原因（可选）'),
        ),
      ],
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        key: const Key('confirm-disband-group'),
        onPressed: confirm.text == widget.groupName
            ? () => Navigator.pop(context, reason.text.trim())
            : null,
        style: FilledButton.styleFrom(
          backgroundColor: LinliColors.systemRed,
          foregroundColor: Colors.white,
        ),
        child: const Text('确认解散'),
      ),
    ],
  );
}

String groupJoinPolicyLabel(String policy) => switch (policy) {
  'manager_invite' => '仅群主/管理员邀请',
  'member_approval' => '成员邀请需管理员验证',
  'qr' => '二维码加入',
  'closed' => '暂停加入',
  _ => '仅成员邀请',
};

String groupMessageRateLimitOptionLabel(int limit) =>
    limit <= 0 ? '不限' : '每分钟最多 $limit 条';

String groupMessageRateLimitLabel(int limit) =>
    limit <= 0 ? '不限制普通成员的发言频率' : '普通成员每分钟最多 $limit 条';

enum _MemberMuteOption {
  tenMinutes,
  oneHour,
  twentyFourHours,
  sevenDays,
  custom,
  permanent,
}

class _MemberMuteSelection {
  const _MemberMuteSelection({this.until, this.permanently = false});

  final DateTime? until;
  final bool permanently;
}

String groupJoinPolicyDescription(String policy) => switch (policy) {
  'manager_invite' => '只有群主和管理员可直接添加好友',
  'member_approval' => '成员提交申请，审核通过后好友直接加入',
  'qr' => '持有效群二维码可加入',
  'closed' => '暂停成员邀请和扫码；群主、管理员仍可直接添加',
  _ => '所有群成员都可直接邀请自己的好友加入',
};

String _roleLabel(String role) => switch (role) {
  'owner' => '群主',
  'admin' => '管理员',
  _ => '群成员',
};

String _displayDate(DateTime value) {
  final local = value.toLocal();
  return '${local.year}-${local.month.toString().padLeft(2, '0')}-'
      '${local.day.toString().padLeft(2, '0')} '
      '${local.hour.toString().padLeft(2, '0')}:'
      '${local.minute.toString().padLeft(2, '0')}';
}
