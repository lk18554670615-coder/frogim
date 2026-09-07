import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/models.dart';
import '../../core/user_identity.dart';
import '../widgets/linli_widgets.dart';

/// Shared by the member-grid + and the existing group-members entry.
class GroupInviteMembersScreen extends StatefulWidget {
  const GroupInviteMembersScreen({
    super.key,
    required this.controller,
    required this.conversationId,
  });
  final AppController controller;
  final String conversationId;

  @override
  State<GroupInviteMembersScreen> createState() =>
      _GroupInviteMembersScreenState();
}

class _GroupInviteMembersScreenState extends State<GroupInviteMembersScreen> {
  late final String? _accountId;
  List<GroupMember> _members = [];
  GroupProfile? _profile;
  final _selected = <String>{};
  final _completed = <String>{};
  final _failures = <String, String>{};
  final _resultLabels = <String, String>{};
  String _query = '';
  String? _error;
  bool _loading = true;
  bool _busy = false;

  bool get _sameAccount =>
      mounted &&
      widget.controller.authenticated &&
      widget.controller.currentUser?.id == _accountId;
  GroupMember? get _me =>
      _members.where((m) => m.user.id == _accountId).firstOrNull;
  bool get _manager => _me?.isOwner == true || _me?.isAdmin == true;
  bool get _directAdd => _profile?.canDirectInvite == true;
  bool get _requiresApproval => _profile?.canSubmitJoinRequest == true;
  bool get _canInvite => _directAdd || _requiresApproval;
  Set<String> get _existing => {for (final m in _members) m.user.id};

  @override
  void initState() {
    super.initState();
    _accountId = widget.controller.currentUser?.id;
    _load();
  }

  Future<bool> _load() async {
    final results = await Future.wait<Object?>([
      widget.controller.loadGroupProfile(widget.conversationId),
      widget.controller.loadGroupMembers(widget.conversationId),
    ]);
    final profile = results[0] as GroupProfile?;
    final members = results[1] as List<GroupMember>?;
    if (!_sameAccount) return false;
    setState(() {
      _loading = false;
      if (members == null || profile == null) {
        _error = widget.controller.error ?? '群成员加载失败，请重试';
      } else {
        _profile = profile;
        _members = members;
        _error = _me == null
            ? '你已不在本群，无法邀请成员'
            : _canInvite
            ? null
            : '当前入群方式不允许普通成员邀请好友';
        _selected.removeAll(_existing);
      }
    });
    return members != null && profile != null && _me != null && _canInvite;
  }

  Future<void> _submit() async {
    if (_busy || _loading || _selected.isEmpty || !_sameAccount) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      // Re-read membership before submission, not the truncated avatar preview.
      // Existing members, stale roles and rejoining are checked by the server.
      if (!await _load() || !_sameAccount) return;
      final targets = widget.controller.contacts
          .where(
            (u) =>
                _selected.contains(u.id) &&
                !_completed.contains(u.id) &&
                u.id != _accountId,
          )
          .toList();
      if (targets.isEmpty) {
        setState(() => _error = '所选好友已在群内或已不在通讯录，请重新选择');
        return;
      }
      if (_directAdd && targets.length > 500) {
        setState(() => _error = '一次最多添加 500 人，请分批选择');
        return;
      }
      _failures.clear();
      final directAdd = _directAdd;
      if (_manager) {
        final success = await widget.controller.addGroupMembers(
          widget.conversationId,
          targets,
        );
        if (!_sameAccount) return;
        if (success) {
          _completed.addAll(targets.map((u) => u.id));
          for (final target in targets) {
            _resultLabels[target.id] = '已加入';
          }
          _selected.removeAll(_completed);
        } else {
          for (final target in targets) {
            _failures[target.id] = widget.controller.error ?? '添加失败，请重试';
          }
        }
      } else {
        // Keep per-user errors independent; successful invites are never
        // submitted again when retrying the remaining selection.
        for (final target in targets) {
          if (!_sameAccount) return;
          final outcome = await widget.controller.inviteGroupMemberWithOutcome(
            widget.conversationId,
            target,
          );
          if (!_sameAccount) return;
          if (outcome != null) {
            _completed.add(target.id);
            _selected.remove(target.id);
            _resultLabels[target.id] = outcome.alreadyInGroup
                ? '已在群内'
                : outcome.action == 'added'
                ? '已加入'
                : outcome.duplicate
                ? '已有待审核'
                : '已提交审核';
          } else {
            _failures[target.id] = widget.controller.error ?? '邀请发送失败，请重试';
          }
          setState(() {});
        }
      }
      if (!_sameAccount) return;
      if (_failures.isEmpty) {
        if (!mounted) return;
        // A server success is final; a later list refresh must not resend it.
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              directAdd
                  ? '已添加 ${targets.length} 位群成员'
                  : '已提交 ${targets.length} 条入群审核申请',
            ),
          ),
        );
        setState(() => _busy = false);
        Navigator.of(context).pop(true);
      } else {
        setState(
          () => _error =
              '已完成 ${_completed.length} 人，${_failures.length} 人失败；可重试未完成的选择',
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) {
      final existing = _existing;
      final contacts = widget.controller.contacts
          .where((u) => u.id != _accountId)
          .toList();
      final query = _query.trim().toLowerCase();
      final visible = contacts
          .where(
            (u) =>
                '${widget.controller.displayNameFor(u)} ${u.name} ${u.handle}'
                    .toLowerCase()
                    .contains(query),
          )
          .toList();
      final selectableVisibleIds = {
        for (final user in visible)
          if (!existing.contains(user.id) && !_completed.contains(user.id))
            user.id,
      };
      final selectedVisibleCount = selectableVisibleIds
          .where(_selected.contains)
          .length;
      final allVisibleSelected =
          selectableVisibleIds.isNotEmpty &&
          selectedVisibleCount == selectableVisibleIds.length;
      final canChoose =
          !_loading && !_busy && _me != null && _sameAccount && _canInvite;
      return PopScope(
        canPop: !_busy,
        child: Scaffold(
          appBar: GlassAppBar(
            title: const Text('选择联系人'),
            actions: [
              TextButton(
                key: const Key('group-invite-confirm'),
                onPressed: canChoose && _selected.isNotEmpty ? _submit : null,
                child: Text(_busy ? '处理中…' : '完成 ${_selected.length}'),
              ),
            ],
          ),
          body: !_sameAccount
              ? const Center(child: Text('登录状态已变化，请返回重试'))
              : Column(
                  children: [
                    if (_loading || _busy)
                      const LinearProgressIndicator(minHeight: 2),
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
                      child: CupertinoSearchTextField(
                        key: const Key('group-invite-search'),
                        enabled: !_busy,
                        placeholder: '搜索昵称、备注或呱呱号',
                        onChanged: (value) => setState(() => _query = value),
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 16,
                        vertical: 8,
                      ),
                      child: Row(
                        crossAxisAlignment: CrossAxisAlignment.center,
                        children: [
                          Expanded(
                            child: Text(
                              _loading
                                  ? '正在确认群成员…'
                                  : _directAdd
                                  ? '勾选好友后点击完成，好友将直接加入群聊。'
                                  : '勾选好友后点击完成，提交给群主或管理员审核。',
                            ),
                          ),
                          const SizedBox(width: 12),
                          Text(
                            '已选 ${_selected.length}',
                            key: const Key('group-invite-selection-count'),
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                          TextButton(
                            key: const Key('group-invite-select-all'),
                            onPressed:
                                canChoose && selectableVisibleIds.isNotEmpty
                                ? () => setState(() {
                                    if (allVisibleSelected) {
                                      _selected.removeAll(selectableVisibleIds);
                                    } else {
                                      _selected.addAll(selectableVisibleIds);
                                    }
                                  })
                                : null,
                            child: Text(allVisibleSelected ? '取消全选' : '全选'),
                          ),
                        ],
                      ),
                    ),
                    if (_error != null)
                      Padding(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 16,
                          vertical: 8,
                        ),
                        child: Text(
                          _error!,
                          style: TextStyle(
                            color: Theme.of(context).colorScheme.error,
                          ),
                        ),
                      ),
                    if (!_loading && _me == null)
                      TextButton(
                        onPressed: _busy
                            ? null
                            : () {
                                setState(() => _loading = true);
                                _load();
                              },
                        child: const Text('重新加载'),
                      ),
                    Expanded(
                      child: visible.isEmpty
                          ? Center(
                              child: Text(
                                contacts.isEmpty
                                    ? '没有可邀请的好友，请先添加好友'
                                    : '没有匹配的联系人',
                              ),
                            )
                          : ListView.builder(
                              itemCount: visible.length,
                              itemBuilder: (context, index) {
                                final user = visible[index];
                                final joined = existing.contains(user.id);
                                final done = _completed.contains(user.id);
                                return CheckboxListTile(
                                  key: ValueKey('group-invite-user-${user.id}'),
                                  value:
                                      joined ||
                                      done ||
                                      _selected.contains(user.id),
                                  onChanged: !canChoose || joined || done
                                      ? null
                                      : (value) => setState(() {
                                          if (value == true) {
                                            _selected.add(user.id);
                                          } else {
                                            _selected.remove(user.id);
                                          }
                                        }),
                                  secondary: PersonAvatar(
                                    name: widget.controller.displayNameFor(
                                      user,
                                    ),
                                    avatarUrl: user.avatarUrl,
                                  ),
                                  title: Text(
                                    widget.controller.displayNameFor(user),
                                  ),
                                  subtitle: Text(
                                    joined
                                        ? '已在群内'
                                        : done
                                        ? _resultLabels[user.id] ?? '已完成'
                                        : _failures[user.id] ??
                                              publicUserHandleLabel(
                                                user.handle,
                                              ),
                                  ),
                                  isThreeLine: false,
                                );
                              },
                            ),
                    ),
                  ],
                ),
        ),
      );
    },
  );
}
