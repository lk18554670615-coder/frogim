import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/forward_batch.dart';
import '../../core/models.dart';
import 'linli_widgets.dart';

Future<ForwardBatchSummary?> showForwardConversations(
  BuildContext context, {
  required AppController controller,
  required List<ChatMessage> messages,
  required String mode,
}) {
  final validation = validateForwardMessages(messages);
  if (validation != null) {
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(validation)));
    return Future.value();
  }
  return showModalBottomSheet<ForwardBatchSummary>(
    context: context,
    isScrollControlled: true,
    useSafeArea: true,
    // The explicit cancel/finish actions and PopScope own dismissal. A drag
    // must never remove an operation while its requests are still in flight.
    isDismissible: false,
    enableDrag: false,
    showDragHandle: true,
    builder: (context) => AnimatedPadding(
      duration: MediaQuery.disableAnimationsOf(context)
          ? Duration.zero
          : const Duration(milliseconds: 180),
      curve: Curves.easeOut,
      padding: EdgeInsets.only(bottom: MediaQuery.viewInsetsOf(context).bottom),
      child: ForwardConversationSheet(
        controller: controller,
        conversations: List.unmodifiable(controller.conversations),
        messages: List.unmodifiable(messages),
        mode: mode,
      ),
    ),
  );
}

class ForwardConversationSheet extends StatefulWidget {
  const ForwardConversationSheet({
    super.key,
    required this.controller,
    required this.conversations,
    required this.messages,
    required this.mode,
  });

  final AppController controller;
  final List<Conversation> conversations;
  final List<ChatMessage> messages;
  final String mode;

  @override
  State<ForwardConversationSheet> createState() =>
      _ForwardConversationSheetState();
}

class _ForwardConversationSheetState extends State<ForwardConversationSheet> {
  final searchController = TextEditingController();
  final selectedIds = <String>{};
  late final List<Conversation> conversations;
  late final String? sessionUserId;
  ForwardBatchTask? task;
  String query = '';
  bool sessionEnded = false;

  @override
  void initState() {
    super.initState();
    conversations = List.unmodifiable(
      {
        for (final item in widget.conversations)
          if (!item.archived) item.id: item,
      }.values,
    );
    sessionUserId = widget.controller.currentUser?.id;
    widget.controller.addListener(_sessionChanged);
  }

  void _sessionChanged() {
    if (!mounted || sessionEnded) return;
    if (!widget.controller.authenticated ||
        widget.controller.currentUser?.id != sessionUserId) {
      setState(() => sessionEnded = true);
      task?.invalidateSession();
    }
  }

  @override
  void dispose() {
    widget.controller.removeListener(_sessionChanged);
    task?.removeListener(_taskChanged);
    task?.dispose();
    searchController.dispose();
    super.dispose();
  }

  void _taskChanged() {
    if (mounted) setState(() {});
  }

  Future<void> _send() async {
    if (sessionEnded || task?.running == true) return;
    if (task == null) {
      if (selectedIds.isEmpty) return;
      FocusScope.of(context).unfocus();
      final created = widget.controller.createForwardBatch(
        widget.messages,
        conversations.where((item) => selectedIds.contains(item.id)).toList(),
        mode: widget.mode,
      );
      created.addListener(_taskChanged);
      setState(() => task = created);
    }
    final batch = task!;
    await batch.start();
    if (!mounted) return;
    if (batch.allSucceeded) Navigator.pop(context, batch.summary);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final normalized = query.trim().toLowerCase();
    final filtered = conversations
        .where(
          (item) =>
              normalized.isEmpty ||
              item.title.toLowerCase().contains(normalized) ||
              item.subtitle.toLowerCase().contains(normalized),
        )
        .toList();
    final allSelected =
        filtered.isNotEmpty &&
        filtered.every((item) => selectedIds.contains(item.id));
    final batch = task;
    return PopScope(
      canPop: batch?.running != true,
      child: FractionallySizedBox(
        heightFactor: .78,
        child: Column(
          children: [
            Expanded(
              child: CustomScrollView(
                key: ValueKey(
                  batch == null
                      ? 'forward-selection-scroll'
                      : 'forward-results-scroll',
                ),
                keyboardDismissBehavior:
                    ScrollViewKeyboardDismissBehavior.onDrag,
                slivers: [
                  SliverToBoxAdapter(
                    child: Column(
                      children: [
                        Padding(
                          padding: const EdgeInsets.fromLTRB(18, 0, 18, 8),
                          child: Align(
                            alignment: Alignment.centerLeft,
                            child: Text(
                              batch == null ? '选择转发对象' : '转发进度',
                              style: theme.textTheme.titleLarge,
                            ),
                          ),
                        ),
                        if (batch == null) ...[
                          Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 18),
                            child: Align(
                              alignment: Alignment.centerLeft,
                              child: Text(
                                '可选择多个会话，确认后发送',
                                style: theme.textTheme.bodySmall,
                              ),
                            ),
                          ),
                          Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 16),
                            child: Wrap(
                              spacing: 12,
                              crossAxisAlignment: WrapCrossAlignment.center,
                              children: [
                                Text(
                                  '已选 ${selectedIds.length} 个 · 当前 ${filtered.length} 个',
                                  key: const Key('forward-selected-count'),
                                  style: theme.textTheme.bodySmall,
                                ),
                                TextButton(
                                  key: const Key(
                                    'forward-conversation-select-all',
                                  ),
                                  onPressed: filtered.isEmpty || sessionEnded
                                      ? null
                                      : () {
                                          setState(() {
                                            final ids = filtered.map(
                                              (item) => item.id,
                                            );
                                            if (allSelected) {
                                              selectedIds.removeAll(ids);
                                            } else {
                                              selectedIds.addAll(ids);
                                            }
                                          });
                                        },
                                  child: Text(allSelected ? '取消全选' : '全选'),
                                ),
                              ],
                            ),
                          ),
                          Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 16),
                            child: CupertinoSearchTextField(
                              key: const Key('forward-conversation-search'),
                              controller: searchController,
                              placeholder: '搜索会话',
                              onChanged: (value) =>
                                  setState(() => query = value),
                            ),
                          ),
                        ] else ...[
                          Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 18),
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  '已完成 ${batch.completedCount}/${batch.targets.length} 个会话',
                                  key: const Key('forward-progress-count'),
                                ),
                                const SizedBox(height: 6),
                                LinearProgressIndicator(
                                  value: batch.targets.isEmpty
                                      ? 0
                                      : batch.completedCount /
                                            batch.targets.length,
                                ),
                                const SizedBox(height: 6),
                                Text(
                                  '成功 ${batch.succeededCount} · 失败 ${batch.failedCount} · '
                                  '未发送 ${batch.notSentCount}',
                                  style: theme.textTheme.bodySmall,
                                ),
                                if (batch.stopRequested &&
                                    !batch.sessionExpired)
                                  Text(
                                    batch.running
                                        ? '正在停止，等待在途请求结束…'
                                        : '已停止后续发送，已发送消息不会撤回',
                                    style: theme.textTheme.bodySmall,
                                  ),
                              ],
                            ),
                          ),
                        ],
                        if (sessionEnded || batch?.sessionExpired == true)
                          Padding(
                            padding: const EdgeInsets.fromLTRB(18, 8, 18, 0),
                            child: Text(
                              '登录状态已失效，已停止后续发送，请重新登录',
                              style: theme.textTheme.bodySmall?.copyWith(
                                color: theme.colorScheme.error,
                              ),
                            ),
                          ),
                        const SizedBox(height: 8),
                      ],
                    ),
                  ),
                  batch == null
                      ? _selectionList(filtered, normalized, theme)
                      : _resultList(batch, theme),
                ],
              ),
            ),
            _footer(batch),
          ],
        ),
      ),
    );
  }

  Widget _selectionList(
    List<Conversation> filtered,
    String normalized,
    ThemeData theme,
  ) {
    if (filtered.isEmpty) {
      return SliverFillRemaining(
        hasScrollBody: false,
        child: Center(
          key: const Key('forward-conversation-empty'),
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  CupertinoIcons.chat_bubble_2,
                  size: 34,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                const SizedBox(height: 12),
                Text(
                  normalized.isEmpty ? '暂无可转发会话' : '没有匹配的会话',
                  style: theme.textTheme.titleMedium,
                ),
                const SizedBox(height: 4),
                Text(
                  normalized.isEmpty ? '有最近会话后，可以从这里转发消息。' : '换个会话名称试试。',
                  textAlign: TextAlign.center,
                  style: theme.textTheme.bodySmall,
                ),
              ],
            ),
          ),
        ),
      );
    }
    return SliverPadding(
      padding: const EdgeInsets.fromLTRB(8, 2, 8, 8),
      sliver: SliverList.separated(
        key: const Key('forward-conversation-list'),
        itemCount: filtered.length,
        separatorBuilder: (_, _) =>
            const Divider(height: 1, indent: 68, endIndent: 8),
        itemBuilder: (context, index) {
          final item = filtered[index];
          final selected = selectedIds.contains(item.id);
          return Semantics(
            checked: selected,
            button: true,
            label: '转发到 ${item.title}',
            child: ListTile(
              key: Key('forward-conversation-${item.id}'),
              minTileHeight: 64,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(12),
              ),
              selected: selected,
              selectedTileColor: theme.brightness == Brightness.dark
                  ? LinliColors.brandGreen.withValues(alpha: .10)
                  : LinliColors.brandMint,
              leading: PersonAvatar(
                name: item.title,
                size: 44,
                avatarUrl: item.avatarUrl,
              ),
              title: Text(
                item.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              subtitle: item.subtitle.trim().isEmpty
                  ? null
                  : Text(
                      item.subtitle,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
              trailing: ExcludeSemantics(
                child: Icon(
                  selected
                      ? CupertinoIcons.check_mark_circled_solid
                      : CupertinoIcons.circle,
                  color: selected
                      ? LinliColors.brandGreen
                      : theme.colorScheme.outlineVariant,
                ),
              ),
              onTap: sessionEnded
                  ? null
                  : () => setState(() {
                      if (!selectedIds.add(item.id)) {
                        selectedIds.remove(item.id);
                      }
                    }),
            ),
          );
        },
      ),
    );
  }

  Widget _resultList(ForwardBatchTask batch, ThemeData theme) => SliverPadding(
    padding: const EdgeInsets.symmetric(horizontal: 8),
    sliver: SliverList.builder(
      key: const Key('forward-results-list'),
      itemCount: batch.targets.length,
      itemBuilder: (context, index) {
        final target = batch.targets[index];
        final label = switch (target.status) {
          ForwardTargetStatus.pending =>
            batch.running && !batch.stopRequested ? '等待发送' : '未发送',
          ForwardTargetStatus.sending => '正在发送',
          ForwardTargetStatus.succeeded => '转发成功',
          ForwardTargetStatus.failed => target.error ?? '转发失败',
        };
        return ListTile(
          key: Key('forward-result-${target.conversation.id}'),
          leading: PersonAvatar(
            name: target.conversation.title,
            size: 40,
            avatarUrl: target.conversation.avatarUrl,
          ),
          title: Text(
            target.conversation.title,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
          subtitle: Text(
            label,
            style: TextStyle(
              color: target.status == ForwardTargetStatus.failed
                  ? theme.colorScheme.error
                  : null,
            ),
          ),
          trailing: target.status == ForwardTargetStatus.sending
              ? const SizedBox.square(
                  dimension: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : target.status == ForwardTargetStatus.succeeded
              ? const Icon(
                  CupertinoIcons.check_mark_circled,
                  color: LinliColors.brandGreen,
                )
              : null,
        );
      },
    ),
  );

  Widget _footer(ForwardBatchTask? batch) {
    final Widget primary;
    final Widget? secondary;
    if (batch?.running == true) {
      secondary = null;
      primary = OutlinedButton(
        key: const Key('forward-stop'),
        onPressed: batch!.stopRequested ? null : batch.stop,
        child: Text(batch.stopRequested ? '正在停止…' : '停止后续发送'),
      );
    } else if (batch != null) {
      secondary = OutlinedButton(
        key: const Key('forward-finish'),
        onPressed: () => Navigator.pop(context, batch.summary),
        child: const Text('完成'),
      );
      primary = FilledButton(
        key: const Key('forward-retry'),
        onPressed: sessionEnded || !batch.canRetry ? null : _send,
        child: Text('重试未完成（${batch.targets.length - batch.succeededCount}）'),
      );
    } else {
      secondary = OutlinedButton(
        key: const Key('forward-conversation-cancel'),
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      );
      primary = FilledButton(
        key: const Key('forward-conversation-confirm'),
        onPressed: selectedIds.isEmpty || sessionEnded ? null : _send,
        child: Text('转发（${selectedIds.length}）'),
      );
    }
    return DecoratedBox(
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surface,
        border: Border(
          top: BorderSide(color: Theme.of(context).colorScheme.outlineVariant),
        ),
      ),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 10, 16, 12),
          child: LayoutBuilder(
            builder: (context, constraints) {
              final stacked =
                  constraints.maxWidth < 300 ||
                  MediaQuery.textScalerOf(context).scale(14) > 20;
              if (stacked) {
                return Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    SizedBox(width: double.infinity, child: primary),
                    if (secondary != null)
                      SizedBox(width: double.infinity, child: secondary),
                  ],
                );
              }
              return Row(
                children: [
                  if (secondary != null) ...[
                    Expanded(child: secondary),
                    const SizedBox(width: 12),
                  ],
                  Expanded(child: primary),
                ],
              );
            },
          ),
        ),
      ),
    );
  }
}
