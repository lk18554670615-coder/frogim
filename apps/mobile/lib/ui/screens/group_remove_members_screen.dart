import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../widgets/linli_widgets.dart';

/// Owner/admin shortcut used by the minus tile in group chat information.
/// The server remains authoritative for every removal and role transition.
class GroupRemoveMembersScreen extends StatefulWidget {
  const GroupRemoveMembersScreen({
    super.key,
    required this.controller,
    required this.conversationId,
  });

  final AppController controller;
  final String conversationId;

  @override
  State<GroupRemoveMembersScreen> createState() =>
      _GroupRemoveMembersScreenState();
}

class _GroupRemoveMembersScreenState extends State<GroupRemoveMembersScreen> {
  late final String? _accountId;
  List<GroupMember> _members = const [];
  final Set<String> _selected = {};
  final Map<String, String> _failures = {};
  String _query = '';
  String? _error;
  bool _loading = true;
  bool _busy = false;

  bool get _sameAccount =>
      mounted &&
      widget.controller.authenticated &&
      widget.controller.currentUser?.id == _accountId;

  GroupMember? get _me =>
      _members.where((member) => member.user.id == _accountId).firstOrNull;

  bool get _canManage => _me?.isOwner == true || _me?.isAdmin == true;

  bool _canRemove(GroupMember member) {
    if (member.user.id == _accountId || !_canManage) return false;
    if (_me?.isOwner == true) return true;
    return !member.isOwner && !member.isAdmin;
  }

  String _roleLabel(GroupMember member) => switch (member.role) {
    'owner' => '群主',
    'admin' => '管理员',
    _ => '群成员',
  };

  String _unavailableReason(GroupMember member) {
    if (member.user.id == _accountId) return '当前账号';
    if (!_canManage) return '没有成员移除权限';
    if (member.isOwner) return '群主不可移除';
    if (member.isAdmin) return '管理员只能由群主移除';
    return '';
  }

  @override
  void initState() {
    super.initState();
    _accountId = widget.controller.currentUser?.id;
    unawaited(_load());
  }

  Future<bool> _load() async {
    final members = await widget.controller.loadGroupMembers(
      widget.conversationId,
      force: true,
    );
    if (!_sameAccount) return false;
    setState(() {
      _loading = false;
      if (members == null) {
        _error = widget.controller.error ?? '群成员加载失败，请重试';
        return;
      }
      _members = members;
      _selected.retainAll({
        for (final member in members)
          if (_canRemove(member)) member.user.id,
      });
      _error = !_canManage ? '只有群主和管理员可以移除成员' : null;
    });
    return members != null && _canManage;
  }

  Future<void> _submit() async {
    if (_busy || _loading || _selected.isEmpty || !_sameAccount) return;
    final requested = Set<String>.of(_selected);
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      // Roles may change while the picker is open. Re-read before confirming
      // and silently drop targets the current actor can no longer manage.
      if (!await _load() || !mounted || !_sameAccount) return;
      final targets = _members
          .where(
            (member) =>
                requested.contains(member.user.id) && _canRemove(member),
          )
          .toList();
      if (targets.isEmpty) {
        setState(() => _error = '所选成员身份已变化，请重新选择');
        return;
      }
      final confirmed = await showCupertinoDialog<bool>(
        context: context,
        builder: (dialogContext) => CupertinoAlertDialog(
          title: Text('移出 ${targets.length} 位成员？'),
          content: const Text('移出后，对方将无法继续查看群内新消息；如需重新加入，必须再次邀请。'),
          actions: [
            CupertinoDialogAction(
              onPressed: () => Navigator.pop(dialogContext, false),
              child: const Text('取消'),
            ),
            CupertinoDialogAction(
              key: const Key('confirm-group-remove-members'),
              isDestructiveAction: true,
              onPressed: () => Navigator.pop(dialogContext, true),
              child: const Text('确认移出'),
            ),
          ],
        ),
      );
      if (!mounted || confirmed != true || !_sameAccount) return;

      _failures.clear();
      var completed = 0;
      for (final target in targets) {
        if (!mounted || !_sameAccount) return;
        final success = await widget.controller.removeGroupMember(
          widget.conversationId,
          target.user,
        );
        if (!_sameAccount) return;
        if (success) {
          completed++;
          _selected.remove(target.user.id);
        } else {
          _failures[target.user.id] = widget.controller.error ?? '移除失败，请重试';
        }
        if (mounted) setState(() {});
      }
      if (!mounted || !_sameAccount) return;
      if (_failures.isEmpty) {
        setState(() => _busy = false);
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('已移出 $completed 位群成员')));
        Navigator.of(context).pop(true);
        return;
      }

      final latest = await widget.controller.loadGroupMembers(
        widget.conversationId,
        force: true,
      );
      if (!_sameAccount) return;
      setState(() {
        if (latest != null) _members = latest;
        _selected.retainAll(_failures.keys.toSet());
        _error = '已移出 $completed 人，${_failures.length} 人失败；可重试未完成成员';
      });
    } finally {
      if (mounted && _busy) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final normalized = _query.trim().toLowerCase();
    final visible = _members.where((member) {
      final name = widget.controller.displayNameFor(
        member.user,
        groupNickname: member.groupNickname,
      );
      return normalized.isEmpty ||
          '$name ${member.user.name} ${member.groupNickname}'
              .toLowerCase()
              .contains(normalized);
    }).toList();
    final selectableVisible = {
      for (final member in visible)
        if (_canRemove(member)) member.user.id,
    };
    final allVisibleSelected =
        selectableVisible.isNotEmpty &&
        selectableVisible.every(_selected.contains);
    final canChoose = !_loading && !_busy && _canManage && _sameAccount;

    return PopScope(
      canPop: !_busy,
      child: Scaffold(
        appBar: GlassAppBar(
          title: const Text('移除群成员'),
          actions: [
            TextButton(
              key: const Key('group-remove-confirm'),
              onPressed: canChoose && _selected.isNotEmpty ? _submit : null,
              child: Text(_busy ? '处理中…' : '移除 ${_selected.length}'),
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
                    padding: const EdgeInsets.fromLTRB(16, 12, 16, 6),
                    child: CupertinoSearchTextField(
                      key: const Key('group-remove-search'),
                      enabled: !_busy,
                      placeholder: '搜索群成员',
                      onChanged: (value) => setState(() => _query = value),
                    ),
                  ),
                  Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: Row(
                      children: [
                        Expanded(
                          child: Text(
                            '已选 ${_selected.length} 位成员',
                            key: const Key('group-remove-selection-count'),
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ),
                        TextButton(
                          key: const Key('group-remove-select-all'),
                          onPressed: canChoose && selectableVisible.isNotEmpty
                              ? () => setState(() {
                                  if (allVisibleSelected) {
                                    _selected.removeAll(selectableVisible);
                                  } else {
                                    _selected.addAll(selectableVisible);
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
                      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
                      child: Text(
                        _error!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                  if (!_loading && _members.isEmpty)
                    Expanded(
                      child: StatePanel(
                        icon: CupertinoIcons.person_2,
                        title: '没有可移除的成员',
                        body: _error ?? '群成员列表为空。',
                        actionLabel: '重新加载',
                        onAction: () {
                          setState(() => _loading = true);
                          unawaited(_load());
                        },
                      ),
                    )
                  else
                    Expanded(
                      child: visible.isEmpty
                          ? const Center(child: Text('没有匹配的群成员'))
                          : ListView.builder(
                              itemCount: visible.length,
                              itemBuilder: (context, index) {
                                final member = visible[index];
                                final enabled = canChoose && _canRemove(member);
                                final failure = _failures[member.user.id];
                                final unavailable = _unavailableReason(member);
                                return CheckboxListTile(
                                  key: ValueKey(
                                    'group-remove-user-${member.user.id}',
                                  ),
                                  value: _selected.contains(member.user.id),
                                  onChanged: enabled
                                      ? (value) => setState(() {
                                          if (value == true) {
                                            _selected.add(member.user.id);
                                          } else {
                                            _selected.remove(member.user.id);
                                          }
                                        })
                                      : null,
                                  secondary: PersonAvatar(
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
                                  subtitle: Text(
                                    failure ??
                                        [
                                          _roleLabel(member),
                                          if (unavailable.isNotEmpty)
                                            unavailable,
                                        ].join(' · '),
                                    style: failure == null
                                        ? null
                                        : const TextStyle(
                                            color: LinliColors.systemRed,
                                          ),
                                  ),
                                );
                              },
                            ),
                    ),
                ],
              ),
      ),
    );
  }
}
