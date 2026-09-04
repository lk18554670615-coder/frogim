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
  final _selected = <String>{};
  final _completed = <String>{};
  final _failures = <String, String>{};
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
  bool get _directAdd => _me?.isOwner == true || _me?.isAdmin == true;
  Set<String> get _existing => {for (final m in _members) m.user.id};

  @override
  void initState() {
    super.initState();
    _accountId = widget.controller.currentUser?.id;
    _load();
  }

  Future<bool> _load() async {
    final members = await widget.controller.loadGroupMembers(
      widget.conversationId,
    );
    if (!_sameAccount) return false;
    setState(() {
      _loading = false;
      if (members == null) {
        _error = widget.controller.error ?? '群成员加载失败，请重试';
      } else {
        _members = members;
        _error = _me == null ? '你已不在本群，无法邀请成员' : null;
        _selected.removeAll(_existing);
      }
    });
    return members != null && _me != null;
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
      if (directAdd) {
        final success = await widget.controller.addGroupMembers(
          widget.conversationId,
          targets,
        );
        if (!_sameAccount) return;
        if (success) {
          _completed.addAll(targets.map((u) => u.id));
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
          final success = await widget.controller.inviteGroupMember(
            widget.conversationId,
            target,
          );
          if (!_sameAccount) return;
          if (success) {
            _completed.add(target.id);
            _selected.remove(target.id);
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
                  : '已向 ${targets.length} 位好友发送邀请',
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
      final canChoose = !_loading && !_busy && _me != null && _sameAccount;
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
                                  ? '勾选好友后点击完成，直接添加到群聊。'
                                  : '勾选好友后点击完成，对方接受邀请后加入。',
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
                                        ? '已完成'
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
