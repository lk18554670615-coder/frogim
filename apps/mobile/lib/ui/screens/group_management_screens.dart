import 'dart:typed_data';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/avatar_image.dart';
import '../../core/models.dart';
import '../../core/user_identity.dart';
import '../widgets/linli_widgets.dart';
import 'relationship_screens.dart';
import 'qr_tools_screen.dart';
import 'settings_screens.dart';

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

  GroupMember? get me => members
      .where((member) => member.user.id == widget.controller.currentUser?.id)
      .firstOrNull;
  bool get isOwner =>
      me?.isOwner == true ||
      profile?.ownerId == widget.controller.currentUser?.id;
  bool get isAdmin => me?.isAdmin == true;
  bool get canEdit => isOwner || isAdmin;

  Conversation get currentConversation =>
      widget.controller.conversations
          .where((item) => item.id == widget.conversation.id)
          .firstOrNull ??
      widget.conversation;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => loading = true);
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
    final conversation = currentConversation;
    return AnimatedBuilder(
      animation: widget.controller,
      builder: (context, _) => Scaffold(
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
                  key: const Key('group-members-entry'),
                  icon: CupertinoIcons.person_2,
                  title: '群成员',
                  subtitle: '${members.length} 位成员',
                  onTap: () async {
                    await Navigator.of(context).push(
                      MaterialPageRoute(
                        builder: (_) => GroupMembersManagementScreen(
                          controller: widget.controller,
                          conversationId: widget.conversation.id,
                          profile: value,
                          initialMembers: members,
                        ),
                      ),
                    );
                    await _load();
                  },
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
            if (isOwner) ...[
              const SectionHeader('群主管理'),
              SectionCard(
                children: [
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
                  SettingTile(
                    icon: CupertinoIcons.speaker_slash,
                    title: '全员禁言',
                    subtitle: value.allMuted ? '仅群主和管理员可发言' : '所有成员可发言',
                    trailing: CupertinoSwitch(
                      value: value.allMuted,
                      onChanged: busy ? null : _setAllMuted,
                    ),
                  ),
                ],
              ),
            ],
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
                        targetType: 'conversation',
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
      ),
    );
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
    try {
      final file = await ImagePicker().pickImage(
        source: ImageSource.gallery,
        imageQuality: 88,
        maxWidth: 1600,
        maxHeight: 1600,
      );
      if (file == null) return;
      final Uint8List bytes = await file.readAsBytes();
      if (!mounted) return;
      if (bytes.length > 8 * 1024 * 1024) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('群头像不能超过 8 MB')));
        return;
      }
      final mimeType = avatarImageMimeType(bytes);
      if (mimeType == null) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('请选择 JPG、PNG 或 WebP 格式的图片')),
        );
        return;
      }
      await _runProfileUpdate(
        () => widget.controller.updateGroupProfile(
          widget.conversation.id,
          avatar: MediaUpload(
            bytes: bytes,
            fileName: file.name,
            mimeType: mimeType,
            kind: MessageContentKind.image,
            localPath: file.path,
          ),
        ),
      );
    } catch (_) {
      if (!mounted) return;
      setState(() => busy = false);
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('无法读取群头像，请检查相册权限后重试')));
    }
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
            for (final policy in ['invite', 'qr', 'closed'])
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
    await _runProfileUpdate(
      () => widget.controller.updateGroupProfile(
        widget.conversation.id,
        joinPolicy: selected,
      ),
    );
  }

  Future<void> _setMemberFriendPermission(bool value) => _runProfileUpdate(
    () => widget.controller.updateGroupProfile(
      widget.conversation.id,
      allowMemberAddFriend: value,
    ),
  );

  Future<void> _setAllMuted(bool value) async {
    setState(() => busy = true);
    final updated = await widget.controller.setGroupAllMuted(
      widget.conversation.id,
      value,
    );
    if (!mounted) return;
    setState(() {
      busy = false;
      if (updated != null) profile = updated;
    });
    if (updated == null) _showError();
  }

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

class _GroupAnnouncementScreenState extends State<GroupAnnouncementScreen> {
  late GroupProfile profile = widget.profile;
  bool editing = false;
  bool saving = false;
  late final TextEditingController controller = TextEditingController(
    text: profile.announcement,
  );

  @override
  void initState() {
    super.initState();
    if (profile.announcementUnread) {
      WidgetsBinding.instance.addPostFrameCallback((_) async {
        final success = await widget.controller.markGroupAnnouncementRead(
          profile.conversationId,
        );
        if (mounted && success) {
          setState(() {
            profile = GroupProfile(
              conversationId: profile.conversationId,
              ownerId: profile.ownerId,
              name: profile.name,
              avatarUrl: profile.avatarUrl,
              announcement: profile.announcement,
              announcementVersion: profile.announcementVersion,
              announcementReadAt: DateTime.now(),
              joinPolicy: profile.joinPolicy,
              allowMemberAddFriend: profile.allowMemberAddFriend,
              allMutedUntil: profile.allMutedUntil,
              qrToken: profile.qrToken,
              qrExpiresAt: profile.qrExpiresAt,
              dissolvedAt: profile.dissolvedAt,
              updatedAt: profile.updatedAt,
            );
          });
        }
      });
    }
  }

  @override
  void dispose() {
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: const Text('群公告'),
      actions: [
        if (widget.canEdit)
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
          if (profile.announcementReadAt != null)
            Text(
              '已读于 ${_displayDate(profile.announcementReadAt!)}',
              style: Theme.of(context).textTheme.bodySmall,
            ),
        ],
      ],
    ),
  );

  Future<void> _save() async {
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
  }
}

class GroupMembersManagementScreen extends StatefulWidget {
  const GroupMembersManagementScreen({
    super.key,
    required this.controller,
    required this.conversationId,
    required this.profile,
    required this.initialMembers,
  });
  final AppController controller;
  final String conversationId;
  final GroupProfile profile;
  final List<GroupMember> initialMembers;

  @override
  State<GroupMembersManagementScreen> createState() =>
      _GroupMembersManagementScreenState();
}

class _GroupMembersManagementScreenState
    extends State<GroupMembersManagementScreen> {
  late List<GroupMember> members = widget.initialMembers;
  bool loading = false;
  String query = '';

  GroupMember? get me => members
      .where((member) => member.user.id == widget.controller.currentUser?.id)
      .firstOrNull;
  bool get isOwner => me?.isOwner == true;
  bool get isAdmin => me?.isAdmin == true;
  List<GroupMember> get visibleMembers {
    final normalized = query.trim().toLowerCase();
    if (normalized.isEmpty) return members;
    return members.where((member) {
      final values = [
        member.user.name,
        member.user.handle,
        member.groupNickname,
      ];
      return values.any((value) => value.toLowerCase().contains(normalized));
    }).toList();
  }

  Future<void> _reload() async {
    setState(() => loading = true);
    final updated = await widget.controller.loadGroupMembers(
      widget.conversationId,
    );
    if (!mounted) return;
    setState(() {
      loading = false;
      if (updated != null) members = updated;
    });
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(title: Text('群成员 · ${members.length}')),
    body: ListView(
      padding: const EdgeInsets.only(bottom: 32),
      children: [
        if (loading) const LinearProgressIndicator(minHeight: 2),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: CupertinoSearchTextField(
            key: const Key('group-member-search'),
            placeholder: '搜索昵称或呱呱号',
            onChanged: (value) => setState(() => query = value),
          ),
        ),
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
          subtitle: Text(isOwner || isAdmin ? '选择后直接加入群聊' : '对方同意邀请后加入'),
          onTap: _pickMembers,
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
    contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
    leading: PersonAvatar(
      name: member.user.name,
      avatarUrl: member.user.avatarUrl,
    ),
    title: Text(
      member.groupNickname.isNotEmpty ? member.groupNickname : member.user.name,
    ),
    subtitle: Text(
      member.isMuted
          ? '${_roleLabel(member.role)} · 已禁言'
          : _roleLabel(member.role),
    ),
    trailing: _canManage(member)
        ? IconButton(
            tooltip: '管理成员',
            onPressed: () => _memberActions(member),
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

  Future<void> _pickMembers() async {
    final existing = members.map((member) => member.user.id).toSet();
    final available = widget.controller.contacts
        .where((user) => !existing.contains(user.id))
        .toList();
    if (available.isEmpty) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('没有可添加的联系人')));
      return;
    }
    final selected = await Navigator.of(context).push<List<AppUser>>(
      MaterialPageRoute(builder: (_) => _GroupMemberPicker(users: available)),
    );
    if (selected == null || selected.isEmpty) return;
    setState(() => loading = true);
    bool success;
    if (isOwner || isAdmin) {
      success = await widget.controller.addGroupMembers(
        widget.conversationId,
        selected,
      );
    } else {
      final results = await Future.wait(
        selected.map(
          (user) =>
              widget.controller.inviteGroupMember(widget.conversationId, user),
        ),
      );
      success = results.every((value) => value);
    }
    if (!mounted) return;
    if (success) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(isOwner || isAdmin ? '群成员已添加' : '群邀请已发送')),
      );
      await _reload();
    } else {
      setState(() => loading = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '操作失败')),
      );
    }
  }

  Future<void> _memberActions(GroupMember member) async {
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
              leading: Icon(
                member.isMuted
                    ? CupertinoIcons.speaker_2
                    : CupertinoIcons.speaker_slash,
              ),
              title: Text(member.isMuted ? '解除禁言' : '禁言 1 小时'),
              onTap: () =>
                  Navigator.pop(context, member.isMuted ? 'unmute' : 'mute'),
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
    if (action == null) return;
    if (action == 'transfer') {
      if (!mounted) return;
      final confirm = await showCupertinoDialog<bool>(
        context: context,
        builder: (context) => CupertinoAlertDialog(
          title: Text('将群主转让给 ${member.user.name}？'),
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
        DateTime.now().add(const Duration(hours: 1)),
      ),
      'unmute' => await widget.controller.setGroupMemberMuted(
        widget.conversationId,
        member.user,
        null,
      ),
      _ => await widget.controller.setGroupRole(
        widget.conversationId,
        member.user,
        action,
      ),
    };
    if (!mounted) return;
    if (success) {
      await _reload();
    } else {
      setState(() => loading = false);
    }
  }
}

class _GroupMemberPicker extends StatefulWidget {
  const _GroupMemberPicker({required this.users});
  final List<AppUser> users;

  @override
  State<_GroupMemberPicker> createState() => _GroupMemberPickerState();
}

class _GroupMemberPickerState extends State<_GroupMemberPicker> {
  final selected = <String>{};

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: const Text('选择联系人'),
      actions: [
        TextButton(
          onPressed: selected.isEmpty
              ? null
              : () => Navigator.pop(
                  context,
                  widget.users
                      .where((user) => selected.contains(user.id))
                      .toList(),
                ),
          child: Text('完成 ${selected.length}'),
        ),
      ],
    ),
    body: ListView.builder(
      itemCount: widget.users.length,
      itemBuilder: (context, index) {
        final user = widget.users[index];
        return CheckboxListTile(
          value: selected.contains(user.id),
          secondary: PersonAvatar(name: user.name, avatarUrl: user.avatarUrl),
          title: Text(user.name),
          subtitle: Text(publicUserHandleLabel(user.handle)),
          onChanged: (value) => setState(
            () => value == true
                ? selected.add(user.id)
                : selected.remove(user.id),
          ),
        );
      },
    ),
  );
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
  'qr' => '二维码加入',
  'closed' => '暂停加入',
  _ => '仅成员邀请',
};

String groupJoinPolicyDescription(String policy) => switch (policy) {
  'qr' => '持有效群二维码可加入',
  'closed' => '不接受新的成员或申请',
  _ => '由现有群成员发起邀请',
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
