import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../../core/user_identity.dart';
import '../widgets/linli_widgets.dart';
import '../widgets/user_presence.dart';
import 'chat_screen.dart';
import 'settings_screens.dart';

class FriendProfileScreen extends StatefulWidget {
  const FriendProfileScreen({
    super.key,
    required this.controller,
    required this.user,
    this.requestSource = 'search',
    this.requestSourceId,
    this.presenceGroupId,
  });

  final AppController controller;
  final AppUser user;
  final String requestSource;
  final String? requestSourceId;
  final String? presenceGroupId;

  @override
  State<FriendProfileScreen> createState() => _FriendProfileScreenState();
}

class _FriendProfileScreenState extends State<FriendProfileScreen> {
  bool busy = false;
  bool blockedThisSession = false;

  AppUser get user =>
      widget.controller.contacts
          .where((item) => item.id == widget.user.id)
          .firstOrNull ??
      widget.user;

  bool get isFriend =>
      widget.controller.contacts.any((item) => item.id == widget.user.id);

  FriendRequest? get pendingRequest =>
      widget.controller.pendingFriendRequestFor(widget.user.id);

  bool get awaitingTheirApproval =>
      widget.controller.awaitingFriendApprovalFor(widget.user.id);

  String get primaryActionLabel {
    if (isFriend) return '发消息';
    if (awaitingTheirApproval) return '等待对方通过';
    if (pendingRequest != null) return '同意添加';
    return '添加好友';
  }

  String get displayName => widget.controller.displayNameFor(user);

  String? get presenceGroupId {
    if (widget.presenceGroupId != null) return widget.presenceGroupId;
    if (widget.requestSource == 'group') return widget.requestSourceId;
    if (widget.requestSource == 'conversation' &&
        widget.controller.conversations.any(
          (c) =>
              c.id == widget.requestSourceId &&
              c.kind == ConversationKind.group,
        )) {
      return widget.requestSourceId;
    }
    return null;
  }

  String get friendRequestSource {
    final source = widget.requestSource.trim().toLowerCase();
    if (source == 'conversation') {
      return presenceGroupId == null ? 'contacts' : 'group';
    }
    return switch (source) {
      'search' || 'qr' || 'contacts' || 'group' || 'card' => source,
      _ => 'search',
    };
  }

  String? get friendRequestSourceId {
    final source = friendRequestSource;
    if (source == 'group') return presenceGroupId;
    if (source == 'qr' || source == 'card') return widget.requestSourceId;
    return null;
  }

  bool get showHandle =>
      (presenceGroupId == null && widget.requestSource != 'group') ||
      widget.controller.canViewGroupMemberHandle(presenceGroupId);

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) => Scaffold(
      appBar: const GlassAppBar(title: Text('个人资料')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 40),
        children: [
          UserPresence(
            controller: widget.controller,
            userId: user.id,
            groupId: presenceGroupId,
            builder: (context, status) => _ProfileHeader(
              user: user,
              displayName: displayName,
              status: status,
              showHandle: showHandle,
            ),
          ),
          const SizedBox(height: 24),
          Row(
            children: [
              Expanded(
                child: FilledButton.icon(
                  key: const Key('friend-primary-action'),
                  onPressed: busy || blockedThisSession || awaitingTheirApproval
                      ? null
                      : isFriend
                      ? _openChat
                      : pendingRequest != null
                      ? _acceptPendingRequest
                      : _requestFriend,
                  icon: Icon(
                    isFriend
                        ? CupertinoIcons.chat_bubble_fill
                        : awaitingTheirApproval
                        ? CupertinoIcons.hourglass
                        : pendingRequest != null
                        ? CupertinoIcons.person_crop_circle_badge_checkmark
                        : CupertinoIcons.person_add_solid,
                  ),
                  label: Text(primaryActionLabel),
                ),
              ),
            ],
          ),
          const SectionHeader('资料'),
          SectionCard(
            children: [
              if (showHandle)
                SettingTile(
                  key: const Key('friend-profile-handle'),
                  icon: CupertinoIcons.at,
                  title: '呱呱号',
                  subtitle: publicUserHandleLabel(user.handle),
                ),
              SettingTile(
                icon: CupertinoIcons.quote_bubble,
                title: '个性签名',
                subtitle: user.signature?.trim().isNotEmpty == true
                    ? user.signature!
                    : user.presence,
              ),
              if (user.gender == 'male' || user.gender == 'female')
                SettingTile(
                  key: const Key('friend-profile-gender'),
                  icon: CupertinoIcons.person_crop_circle,
                  title: '性别',
                  subtitle: user.gender == 'male' ? '男' : '女',
                ),
              if (isFriend)
                SettingTile(
                  key: const Key('edit-friend-metadata'),
                  icon: CupertinoIcons.pencil,
                  title: '备注与标签',
                  subtitle: [
                    if (user.remark.isNotEmpty) user.remark,
                    ...user.tags,
                  ].join(' · ').ifEmpty('未设置'),
                  onTap: _editMetadata,
                ),
            ],
          ),
          const SectionHeader('隐私与安全'),
          SectionCard(
            children: [
              SettingTile(
                icon: CupertinoIcons.exclamationmark_triangle,
                title: '举报用户',
                subtitle: '提交后由平台审核，不会通知对方',
                onTap: () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => ReportScreen(
                      controller: widget.controller,
                      target: displayName,
                      targetId: user.id,
                      targetType: 'user',
                    ),
                  ),
                ),
              ),
              SettingTile(
                key: const Key('block-user'),
                icon: CupertinoIcons.nosign,
                title: blockedThisSession ? '已加入黑名单' : '加入黑名单',
                subtitle: blockedThisSession
                    ? '本次操作已同步到服务端'
                    : '将同时解除好友关系并取消双方未处理申请',
                destructive: true,
                onTap: busy || blockedThisSession ? null : _block,
              ),
              if (isFriend)
                SettingTile(
                  key: const Key('delete-friend'),
                  icon: CupertinoIcons.person_crop_circle_badge_minus,
                  title: '删除联系人',
                  subtitle: '聊天记录仍会保留在会话中',
                  destructive: true,
                  onTap: busy ? null : _deleteFriend,
                ),
            ],
          ),
        ],
      ),
    ),
  );

  Future<void> _openChat() async {
    final conversation = await widget.controller.createDirect(user);
    if (!mounted) return;
    if (conversation == null) {
      _feedback(widget.controller.error ?? '暂时无法开始会话，请稍后重试');
      return;
    }
    await Navigator.of(context).push(
      chatScreenRoute(
        context,
        controller: widget.controller,
        conversation: conversation,
      ),
    );
  }

  Future<void> _requestFriend() async {
    final source = friendRequestSource;
    final sourceId = friendRequestSourceId;
    if (source == 'group' && sourceId == null) {
      _feedback('群聊信息未加载完整，请返回群聊后重试');
      return;
    }
    final verification = await _FriendVerificationSheet.show(
      context,
      targetName: user.name,
      requesterName: widget.controller.currentUser?.name ?? '',
    );
    if (verification == null) return;
    setState(() => busy = true);
    final success = await widget.controller.sendFriendRequest(
      user,
      verification,
      source: source,
      sourceId: sourceId,
    );
    if (!mounted) return;
    setState(() => busy = false);
    _feedback(success ? '好友申请已发送' : widget.controller.error ?? '发送失败');
  }

  Future<void> _acceptPendingRequest() async {
    final request = pendingRequest;
    if (request == null || request.outgoing) return;
    setState(() => busy = true);
    final success = await widget.controller.acceptRequest(request);
    if (!mounted) return;
    setState(() => busy = false);
    _feedback(
      success ? '已添加 ${user.name} 为好友' : widget.controller.error ?? '添加失败',
    );
  }

  Future<void> _editMetadata() async {
    final result = await _FriendMetadataSheet.show(context, user: user);
    if (result == null) return;
    setState(() => busy = true);
    final success = await widget.controller.updateFriendMetadata(
      user,
      remark: result.$1,
      tags: result.$2,
    );
    if (!mounted) return;
    setState(() => busy = false);
    _feedback(success ? '备注与标签已保存' : widget.controller.error ?? '保存失败');
  }

  Future<void> _deleteFriend() async {
    final confirmed = await _confirm(
      title: '删除 $displayName？',
      message: '删除后对方不会收到通知。已有聊天记录不会自动清除。',
      action: '删除联系人',
    );
    if (!confirmed) return;
    setState(() => busy = true);
    final success = await widget.controller.deleteFriend(user);
    if (!mounted) return;
    if (success) {
      Navigator.pop(context);
    } else {
      setState(() => busy = false);
      _feedback(widget.controller.error ?? '删除失败');
    }
  }

  Future<void> _block() async {
    final confirmed = await _confirm(
      title: '将 $displayName 加入黑名单？',
      message: '对方将无法添加你或发起新会话，现有好友关系也会解除。',
      action: '加入黑名单',
    );
    if (!confirmed) return;
    setState(() => busy = true);
    final success = await widget.controller.blockUser(user, true);
    if (!mounted) return;
    setState(() {
      busy = false;
      blockedThisSession = success;
    });
    _feedback(success ? '已加入黑名单' : widget.controller.error ?? '操作失败');
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

  void _feedback(String message) {
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }
}

class _ProfileHeader extends StatelessWidget {
  const _ProfileHeader({
    required this.user,
    required this.displayName,
    this.status = UserPresenceStatus.hidden,
    this.showHandle = true,
  });
  final AppUser user;
  final String displayName;
  final UserPresenceStatus status;
  final bool showHandle;

  @override
  Widget build(BuildContext context) => Column(
    children: [
      PersonAvatar(
        name: displayName,
        size: 88,
        avatarUrl: user.avatarUrl,
        online: status == UserPresenceStatus.online,
      ),
      const SizedBox(height: 12),
      Text(
        displayName,
        textAlign: TextAlign.center,
        style: Theme.of(context).textTheme.headlineMedium,
      ),
      if (displayName != user.name) ...[
        const SizedBox(height: 4),
        Text('昵称：${user.name}', style: Theme.of(context).textTheme.bodySmall),
      ],
      if (showHandle) ...[
        const SizedBox(height: 4),
        Text(
          publicUserHandleLabel(user.handle),
          style: Theme.of(context).textTheme.bodyMedium,
        ),
      ],
      PresenceLabel(status),
    ],
  );
}

class FriendRequestDetailsScreen extends StatefulWidget {
  const FriendRequestDetailsScreen({
    super.key,
    required this.controller,
    required this.request,
  });

  final AppController controller;
  final FriendRequest request;

  @override
  State<FriendRequestDetailsScreen> createState() =>
      _FriendRequestDetailsScreenState();
}

class _FriendRequestDetailsScreenState
    extends State<FriendRequestDetailsScreen> {
  bool busy = false;
  FriendRequest get request =>
      widget.controller.requests
          .where((item) => item.id == widget.request.id)
          .firstOrNull ??
      widget.request;

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) => Scaffold(
      appBar: const GlassAppBar(title: Text('好友申请')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 36),
        children: [
          _ProfileHeader(user: request.user, displayName: request.user.name),
          const SizedBox(height: 24),
          _RequestStatusBanner(request: request),
          const SectionHeader('申请信息'),
          SectionCard(
            children: [
              SettingTile(
                icon: CupertinoIcons.text_bubble,
                title: '验证消息',
                subtitle: request.note.trim().isEmpty ? '对方未填写' : request.note,
              ),
              SettingTile(
                icon: CupertinoIcons.link,
                title: '申请来源',
                subtitle: friendRequestSourceLabel(request.source),
              ),
              if (request.createdAt != null)
                SettingTile(
                  icon: CupertinoIcons.clock,
                  title: '申请时间',
                  subtitle: _formatDate(request.createdAt!),
                ),
              if (request.status == 'pending' && request.expiresAt != null)
                SettingTile(
                  icon: CupertinoIcons.hourglass,
                  title: '有效期至',
                  subtitle: _formatDate(request.expiresAt!),
                ),
            ],
          ),
          const SizedBox(height: 20),
          if (request.status == 'pending')
            request.outgoing
                ? OutlinedButton(
                    key: const Key('cancel-friend-request'),
                    onPressed: busy ? null : () => _transition('cancel'),
                    child: const Text('撤回好友申请'),
                  )
                : Row(
                    children: [
                      Expanded(
                        child: OutlinedButton(
                          key: const Key('reject-friend-request'),
                          onPressed: busy ? null : () => _transition('reject'),
                          child: const Text('拒绝'),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: FilledButton(
                          key: const Key('accept-friend-request'),
                          onPressed: busy ? null : () => _transition('accept'),
                          child: const Text('同意'),
                        ),
                      ),
                    ],
                  ),
          const SizedBox(height: 12),
          TextButton(
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(
                builder: (_) => FriendProfileScreen(
                  controller: widget.controller,
                  user: request.user,
                  requestSource: request.source,
                  requestSourceId: request.sourceId,
                ),
              ),
            ),
            child: const Text('查看个人资料'),
          ),
        ],
      ),
    ),
  );

  Future<void> _transition(String action) async {
    setState(() => busy = true);
    final success = switch (action) {
      'accept' => await widget.controller.acceptRequest(request),
      'reject' => await widget.controller.rejectRequest(request),
      _ => await widget.controller.cancelRequest(request),
    };
    if (!mounted) return;
    setState(() => busy = false);
    final message = success
        ? switch (action) {
            'accept' => '已同意好友申请',
            'reject' => '已拒绝好友申请',
            _ => '好友申请已撤回',
          }
        : widget.controller.error ?? '操作失败';
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }
}

class _RequestStatusBanner extends StatelessWidget {
  const _RequestStatusBanner({required this.request});
  final FriendRequest request;

  @override
  Widget build(BuildContext context) {
    final pending = request.status == 'pending';
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: pending
            ? LinliColors.brandMint
            : Theme.of(context).colorScheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(14),
      ),
      child: Row(
        children: [
          Icon(
            pending ? CupertinoIcons.hourglass : CupertinoIcons.checkmark_seal,
            color: pending ? LinliColors.navy : LinliColors.preview,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  friendRequestStatusLabel(request.status),
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                const SizedBox(height: 2),
                Text(
                  friendRequestStatusDescription(request),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

String friendRequestStatusLabel(String status) => switch (status) {
  'pending' => '等待处理',
  'accepted' => '已成为好友',
  'rejected' => '已拒绝',
  'cancelled' => '已撤回',
  'expired' => '已过期',
  _ => '状态未知',
};

String friendRequestStatusDescription(FriendRequest request) =>
    switch (request.status) {
      'pending' => request.outgoing ? '等待对方处理申请' : '请确认是否认识对方',
      'accepted' => '申请已同意，可以开始聊天',
      'rejected' => request.outgoing ? '对方未同意本次申请' : '你已拒绝本次申请',
      'cancelled' => request.outgoing ? '你已撤回本次申请' : '对方已撤回本次申请',
      'expired' => '申请已超过服务端有效期，需要重新发起',
      _ => '请刷新后查看最新状态',
    };

String friendRequestSourceLabel(String source) => switch (source) {
  'qr' => '扫描二维码',
  'contacts' => '手机通讯录',
  'group' => '共同群聊',
  'card' => '好友名片',
  _ => '呱呱号或姓名搜索',
};

String _formatDate(DateTime value) {
  final local = value.toLocal();
  return '${local.year}年${local.month}月${local.day}日 '
      '${local.hour.toString().padLeft(2, '0')}:'
      '${local.minute.toString().padLeft(2, '0')}';
}

class _FriendVerificationSheet extends StatefulWidget {
  const _FriendVerificationSheet({
    required this.targetName,
    required this.requesterName,
  });
  final String targetName;
  final String requesterName;

  static Future<String?> show(
    BuildContext context, {
    required String targetName,
    required String requesterName,
  }) => showModalBottomSheet<String>(
    context: context,
    isScrollControlled: true,
    builder: (_) => _FriendVerificationSheet(
      targetName: targetName,
      requesterName: requesterName,
    ),
  );

  @override
  State<_FriendVerificationSheet> createState() =>
      _FriendVerificationSheetState();
}

class _FriendVerificationSheetState extends State<_FriendVerificationSheet> {
  final controller = TextEditingController();

  @override
  void initState() {
    super.initState();
    final requesterName = widget.requesterName.trim();
    controller.text = requesterName.isEmpty ? '' : '你好，我是$requesterName';
  }

  @override
  void dispose() {
    controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => SafeArea(
    child: Padding(
      padding: EdgeInsets.fromLTRB(
        16,
        16,
        16,
        MediaQuery.viewInsetsOf(context).bottom + 16,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            '添加 ${widget.targetName}',
            style: Theme.of(context).textTheme.titleLarge,
          ),
          const SizedBox(height: 6),
          Text(
            '填写对方能识别你的信息，最多 300 字。',
            style: Theme.of(context).textTheme.bodySmall,
          ),
          const SizedBox(height: 16),
          TextField(
            key: const Key('friend-verification-input'),
            controller: controller,
            autofocus: true,
            maxLength: 300,
            minLines: 3,
            maxLines: 5,
          ),
          const SizedBox(height: 8),
          FilledButton(
            onPressed: () => Navigator.pop(context, controller.text.trim()),
            child: const Text('发送好友申请'),
          ),
        ],
      ),
    ),
  );
}

class _FriendMetadataSheet extends StatefulWidget {
  const _FriendMetadataSheet({required this.user});
  final AppUser user;

  static Future<(String, List<String>)?> show(
    BuildContext context, {
    required AppUser user,
  }) => showModalBottomSheet<(String, List<String>)>(
    context: context,
    isScrollControlled: true,
    builder: (_) => _FriendMetadataSheet(user: user),
  );

  @override
  State<_FriendMetadataSheet> createState() => _FriendMetadataSheetState();
}

class _FriendMetadataSheetState extends State<_FriendMetadataSheet> {
  late final remark = TextEditingController(text: widget.user.remark);
  late final tags = TextEditingController(text: widget.user.tags.join('、'));

  @override
  void dispose() {
    remark.dispose();
    tags.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => SafeArea(
    child: Padding(
      padding: EdgeInsets.fromLTRB(
        16,
        16,
        16,
        MediaQuery.viewInsetsOf(context).bottom + 16,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text('备注与标签', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 16),
          TextField(
            controller: remark,
            maxLength: 40,
            decoration: const InputDecoration(labelText: '备注名'),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: tags,
            maxLength: 120,
            decoration: const InputDecoration(
              labelText: '标签',
              hintText: '用逗号或顿号分隔，最多 10 个',
            ),
          ),
          const SizedBox(height: 12),
          FilledButton(
            onPressed: () {
              final values = tags.text
                  .split(RegExp(r'[,，、]'))
                  .map((item) => item.trim())
                  .where((item) => item.isNotEmpty)
                  .toSet()
                  .take(10)
                  .toList();
              Navigator.pop(context, (remark.text.trim(), values));
            },
            child: const Text('保存备注与标签'),
          ),
        ],
      ),
    ),
  );
}

extension on String {
  String ifEmpty(String fallback) => isEmpty ? fallback : this;
}
