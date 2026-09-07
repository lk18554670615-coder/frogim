import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/models.dart';
import '../widgets/linli_widgets.dart';

class GroupJoinRequestsScreen extends StatefulWidget {
  const GroupJoinRequestsScreen({
    super.key,
    required this.controller,
    required this.conversationId,
  });

  final AppController controller;
  final String conversationId;

  @override
  State<GroupJoinRequestsScreen> createState() =>
      _GroupJoinRequestsScreenState();
}

class _GroupJoinRequestsScreenState extends State<GroupJoinRequestsScreen> {
  List<GroupJoinRequest> _requests = const [];
  bool _loading = true;
  String? _error;
  String? _processingId;
  int _loadRequest = 0;
  late int _revision;
  Timer? _refreshTimer;

  @override
  void initState() {
    super.initState();
    _revision = widget.controller.groupSendPolicyRevision;
    widget.controller.addListener(_handleControllerChange);
    _load();
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    widget.controller.removeListener(_handleControllerChange);
    super.dispose();
  }

  void _handleControllerChange() {
    final next = widget.controller.groupSendPolicyRevision;
    if (next == _revision || _processingId != null) return;
    _revision = next;
    _refreshTimer?.cancel();
    _refreshTimer = Timer(const Duration(milliseconds: 180), () {
      if (mounted) _load(showProgress: false);
    });
  }

  Future<void> _load({bool showProgress = true}) async {
    final request = ++_loadRequest;
    if (showProgress) setState(() => _loading = true);
    final rows = await widget.controller.loadGroupJoinRequests(
      widget.conversationId,
    );
    if (!mounted || request != _loadRequest) return;
    setState(() {
      _loading = false;
      if (rows == null) {
        _error = widget.controller.error ?? '入群审核加载失败';
      } else {
        _requests = rows.where((item) => item.pending).toList(growable: false);
        _error = null;
      }
    });
  }

  Future<void> _respond(GroupJoinRequest request, String action) async {
    if (_processingId != null) return;
    final inviteeName = widget.controller.displayNameFor(
      request.invitee ??
          AppUser(id: request.inviteeId, name: '该用户', handle: '', presence: ''),
    );
    final confirmed = await showCupertinoDialog<bool>(
      context: context,
      builder: (context) => CupertinoAlertDialog(
        title: Text(action == 'approve' ? '同意入群申请？' : '拒绝入群申请？'),
        content: Text(
          action == 'approve'
              ? '同意后“$inviteeName”将直接加入群聊，不再等待对方确认。'
              : '拒绝“$inviteeName”的本次入群申请？',
        ),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          CupertinoDialogAction(
            key: Key('group-join-request-confirm-$action'),
            isDestructiveAction: action == 'reject',
            onPressed: () => Navigator.pop(context, true),
            child: Text(action == 'approve' ? '同意' : '拒绝'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    setState(() => _processingId = request.id);
    final success = await widget.controller.respondGroupJoinRequest(
      widget.conversationId,
      request.id,
      action,
    );
    if (!mounted) return;
    setState(() => _processingId = null);
    if (!success) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.controller.error ?? '审核操作失败')),
      );
      await _load(showProgress: false);
      return;
    }
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(action == 'approve' ? '已同意，对方已加入群聊' : '已拒绝入群申请')),
    );
    await _load(showProgress: false);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: GlassAppBar(
      title: Text('入群审核 · ${_requests.length}'),
      actions: [
        IconButton(
          tooltip: '刷新',
          onPressed: _loading || _processingId != null ? null : _load,
          icon: const Icon(CupertinoIcons.refresh),
        ),
      ],
    ),
    body: _loading && _requests.isEmpty
        ? const Center(child: CupertinoActivityIndicator())
        : _error != null && _requests.isEmpty
        ? StatePanel(
            icon: CupertinoIcons.exclamationmark_bubble,
            title: '入群审核加载失败',
            body: _error!,
            actionLabel: '重新加载',
            onAction: _load,
          )
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView(
              key: const Key('group-join-requests-list'),
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
              children: [
                if (_loading) const LinearProgressIndicator(minHeight: 2),
                if (_requests.isEmpty)
                  const Padding(
                    padding: EdgeInsets.only(top: 120),
                    child: Center(child: Text('暂无待审核的入群申请')),
                  )
                else
                  for (final request in _requests) _requestCard(request),
              ],
            ),
          ),
  );

  Widget _requestCard(GroupJoinRequest request) {
    final requester = request.requester;
    final invitee = request.invitee;
    final requesterName = requester == null
        ? '群成员'
        : widget.controller.displayNameFor(requester);
    final inviteeName = invitee == null
        ? '待加入用户'
        : widget.controller.displayNameFor(invitee);
    final busy = _processingId == request.id;
    return Card(
      key: ValueKey('group-join-request-${request.id}'),
      margin: const EdgeInsets.only(bottom: 12),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                PersonAvatar(
                  name: inviteeName,
                  avatarUrl: invitee?.avatarUrl,
                  size: 48,
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        inviteeName,
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      const SizedBox(height: 2),
                      Text('$requesterName 邀请该好友加入'),
                    ],
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Text(
              '申请于 ${_displayTime(request.createdAt)} · ${_expiryText(request.expiresAt)}',
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 12),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                OutlinedButton(
                  key: ValueKey('group-join-reject-${request.id}'),
                  onPressed: busy || _processingId != null
                      ? null
                      : () => _respond(request, 'reject'),
                  child: const Text('拒绝'),
                ),
                const SizedBox(width: 10),
                FilledButton(
                  key: ValueKey('group-join-approve-${request.id}'),
                  onPressed: busy || _processingId != null
                      ? null
                      : () => _respond(request, 'approve'),
                  child: Text(busy ? '处理中…' : '同意并加入'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

String _displayTime(DateTime value) {
  final local = value.toLocal();
  return '${local.month}月${local.day}日 '
      '${local.hour.toString().padLeft(2, '0')}:'
      '${local.minute.toString().padLeft(2, '0')}';
}

String _expiryText(DateTime value) {
  final remaining = value.difference(DateTime.now());
  if (remaining.isNegative) return '已过期';
  if (remaining.inDays > 0) return '${remaining.inDays + 1} 天内有效';
  if (remaining.inHours > 0) return '${remaining.inHours + 1} 小时内有效';
  return '${remaining.inMinutes.clamp(1, 59)} 分钟内有效';
}
