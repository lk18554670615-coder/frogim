import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import 'linli_widgets.dart';

Future<void> showGroupMessageReceiptDetails(
  BuildContext context, {
  required AppController controller,
  required String conversationId,
  required String messageId,
}) {
  final width = MediaQuery.sizeOf(context).width;
  final desktop = useLinliDesktopLayout(width) && width >= 720;
  final content = _GroupMessageReceiptPanel(
    controller: controller,
    conversationId: conversationId,
    messageId: messageId,
    desktop: desktop,
  );
  if (!desktop) {
    return showModalBottomSheet<void>(
      context: context,
      useSafeArea: true,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (_) => FractionallySizedBox(heightFactor: .78, child: content),
    );
  }
  return showGeneralDialog<void>(
    context: context,
    barrierDismissible: true,
    barrierLabel: '关闭阅读详情',
    barrierColor: Colors.black.withValues(alpha: .28),
    transitionDuration: const Duration(milliseconds: 180),
    pageBuilder: (dialogContext, _, _) => SafeArea(
      child: Align(
        alignment: Alignment.centerRight,
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Material(
            color: Theme.of(dialogContext).colorScheme.surface,
            elevation: 16,
            borderRadius: BorderRadius.circular(20),
            clipBehavior: Clip.antiAlias,
            child: SizedBox(
              key: const Key('group-receipt-desktop-panel'),
              width: 390,
              height: MediaQuery.sizeOf(dialogContext).height - 40,
              child: content,
            ),
          ),
        ),
      ),
    ),
    transitionBuilder: (_, animation, _, child) => SlideTransition(
      position: Tween<Offset>(
        begin: const Offset(.08, 0),
        end: Offset.zero,
      ).animate(CurvedAnimation(parent: animation, curve: Curves.easeOutCubic)),
      child: FadeTransition(opacity: animation, child: child),
    ),
  );
}

class _GroupMessageReceiptPanel extends StatefulWidget {
  const _GroupMessageReceiptPanel({
    required this.controller,
    required this.conversationId,
    required this.messageId,
    required this.desktop,
  });

  final AppController controller;
  final String conversationId;
  final String messageId;
  final bool desktop;

  @override
  State<_GroupMessageReceiptPanel> createState() =>
      _GroupMessageReceiptPanelState();
}

class _GroupMessageReceiptPanelState extends State<_GroupMessageReceiptPanel> {
  final _tabs = <String, _ReceiptTabState>{
    'read': _ReceiptTabState(),
    'unread': _ReceiptTabState(),
  };
  String _status = 'read';
  int _readCount = 0;
  int _unreadCount = 0;
  int _requestEpoch = 0;

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_handleControllerChange);
    unawaited(_load(refresh: true));
  }

  @override
  void dispose() {
    _requestEpoch++;
    widget.controller.removeListener(_handleControllerChange);
    super.dispose();
  }

  void _handleControllerChange() {
    if (!widget.controller.canDisplayMessageReceipts(widget.conversationId) &&
        mounted) {
      Navigator.of(context).maybePop();
    }
  }

  Future<void> _load({bool refresh = false}) async {
    final tab = _tabs[_status]!;
    if (tab.loading) return;
    final epoch = ++_requestEpoch;
    setState(() {
      tab.loading = true;
      tab.error = null;
      if (refresh) {
        tab.items = const [];
        tab.nextCursor = '';
        tab.loaded = false;
      }
    });
    try {
      final page = await widget.controller.groupMessageReceipts(
        widget.conversationId,
        widget.messageId,
        status: _status,
        cursor: refresh ? '' : tab.nextCursor,
      );
      if (!mounted || epoch != _requestEpoch) return;
      setState(() {
        _readCount = page.readCount;
        _unreadCount = page.unreadCount;
        tab.items = refresh ? page.items : [...tab.items, ...page.items];
        tab.nextCursor = page.nextCursor;
        tab.loaded = true;
      });
    } catch (_) {
      if (!mounted || epoch != _requestEpoch) return;
      setState(() => tab.error = '阅读详情加载失败，请稍后重试');
    } finally {
      if (mounted && tab.loading) {
        setState(() => tab.loading = false);
      }
    }
  }

  void _selectStatus(String status) {
    if (_status == status) return;
    setState(() => _status = status);
    if (!_tabs[status]!.loaded) unawaited(_load(refresh: true));
  }

  @override
  Widget build(BuildContext context) {
    final tab = _tabs[_status]!;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: EdgeInsets.fromLTRB(20, widget.desktop ? 18 : 0, 12, 12),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '阅读详情',
                      key: const Key('group-receipt-title'),
                      style: Theme.of(context).textTheme.titleLarge?.copyWith(
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(height: 3),
                    Text(
                      '仅群主和管理员可见',
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: context.linli.secondaryText,
                      ),
                    ),
                  ],
                ),
              ),
              IconButton(
                key: const Key('refresh-group-receipts'),
                tooltip: '刷新',
                onPressed: tab.loading ? null : () => _load(refresh: true),
                icon: const Icon(CupertinoIcons.refresh),
              ),
              if (widget.desktop)
                IconButton(
                  tooltip: '关闭',
                  onPressed: () => Navigator.pop(context),
                  icon: const Icon(CupertinoIcons.xmark),
                ),
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 20),
          child: Row(
            children: [
              Expanded(
                child: _ReceiptTab(
                  key: const Key('group-receipt-read-tab'),
                  selected: _status == 'read',
                  label: '已读 $_readCount',
                  onTap: () => _selectStatus('read'),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: _ReceiptTab(
                  key: const Key('group-receipt-unread-tab'),
                  selected: _status == 'unread',
                  label: '未读 $_unreadCount',
                  onTap: () => _selectStatus('unread'),
                ),
              ),
            ],
          ),
        ),
        const SizedBox(height: 10),
        Divider(height: 1, color: Theme.of(context).colorScheme.outline),
        Expanded(child: _buildBody(tab)),
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 8, 20, 14),
          child: Text(
            '统计不包含消息发送者和消息发送后才加入的成员',
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.labelSmall?.copyWith(
              color: context.linli.secondaryText,
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildBody(_ReceiptTabState tab) {
    if (tab.loading && !tab.loaded) {
      return const Center(child: CupertinoActivityIndicator());
    }
    if (tab.error != null && tab.items.isEmpty) {
      return _ReceiptError(message: tab.error!, onRetry: _load);
    }
    if (tab.items.isEmpty) {
      return Center(
        child: Text(
          _status == 'read' ? '暂无成员已读' : '全部成员已读',
          key: const Key('group-receipt-empty'),
          style: TextStyle(color: context.linli.secondaryText),
        ),
      );
    }
    return ListView.separated(
      key: Key('group-receipt-$_status-list'),
      padding: const EdgeInsets.fromLTRB(12, 6, 12, 10),
      itemCount:
          tab.items.length +
          (tab.nextCursor.isNotEmpty || tab.error != null ? 1 : 0),
      separatorBuilder: (_, _) => const SizedBox(height: 2),
      itemBuilder: (context, index) {
        if (index == tab.items.length) {
          return Padding(
            padding: const EdgeInsets.all(8),
            child: Center(
              child: tab.error != null
                  ? TextButton(onPressed: _load, child: const Text('加载失败，重试'))
                  : TextButton(
                      key: const Key('load-more-group-receipts'),
                      onPressed: tab.loading ? null : _load,
                      child: tab.loading
                          ? const CupertinoActivityIndicator(radius: 8)
                          : const Text('加载更多'),
                    ),
            ),
          );
        }
        return _ReceiptMemberTile(member: tab.items[index]);
      },
    );
  }
}

class _ReceiptTabState {
  List<GroupMessageReceiptMember> items = const [];
  String nextCursor = '';
  String? error;
  bool loading = false;
  bool loaded = false;
}

class _ReceiptTab extends StatelessWidget {
  const _ReceiptTab({
    super.key,
    required this.selected,
    required this.label,
    required this.onTap,
  });

  final bool selected;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Material(
    color: selected
        ? context.linli.selected
        : Theme.of(context).colorScheme.surfaceContainerHighest,
    borderRadius: BorderRadius.circular(10),
    child: InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(10),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 10),
        child: Text(
          label,
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.labelLarge?.copyWith(
            color: selected
                ? context.linli.primary
                : context.linli.secondaryText,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
          ),
        ),
      ),
    ),
  );
}

class _ReceiptMemberTile extends StatelessWidget {
  const _ReceiptMemberTile({required this.member});

  final GroupMessageReceiptMember member;

  @override
  Widget build(BuildContext context) {
    final role = switch (member.role) {
      'owner' => '群主',
      'admin' => '管理员',
      _ => '群成员',
    };
    final stateColor = member.read
        ? LinliColors.systemGreen
        : context.linli.secondaryText;
    return ListTile(
      key: Key('group-receipt-member-${member.userId}'),
      leading: PersonAvatar(
        name: member.displayName.isEmpty ? member.name : member.displayName,
        avatarUrl: member.avatarUrl,
        size: 42,
      ),
      title: Text(
        member.displayName.isEmpty ? member.name : member.displayName,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(role),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 7,
            height: 7,
            decoration: BoxDecoration(
              color: stateColor,
              shape: BoxShape.circle,
            ),
          ),
          const SizedBox(width: 6),
          Text(
            member.read ? '已读' : '未读',
            style: Theme.of(context).textTheme.labelMedium?.copyWith(
              color: stateColor,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
  }
}

class _ReceiptError extends StatelessWidget {
  const _ReceiptError({required this.message, required this.onRetry});

  final String message;
  final Future<void> Function({bool refresh}) onRetry;

  @override
  Widget build(BuildContext context) => Center(
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          CupertinoIcons.exclamationmark_triangle,
          color: context.linli.primary,
        ),
        const SizedBox(height: 10),
        Text(message),
        const SizedBox(height: 8),
        FilledButton(
          onPressed: () => onRetry(refresh: true),
          child: const Text('重新加载'),
        ),
      ],
    ),
  );
}
