import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../widgets/linli_widgets.dart';

class SystemNotificationTile extends StatelessWidget {
  const SystemNotificationTile({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: controller,
    builder: (context, _) {
      final dark = Theme.of(context).brightness == Brightness.dark;
      final latest = controller.announcements.isEmpty
          ? null
          : controller.announcements.first;
      final error = controller.announcementsLoadError;
      final unread = controller.systemNotificationUnreadCount;
      final subtitle = error != null ? '通知暂时无法同步' : latest?.title ?? '暂无新通知';
      final time = latest?.publishedAt == null
          ? ''
          : _notificationDate(latest!.publishedAt!);
      return KeyedSubtree(
        key: error == null ? null : const Key('announcement-load-error'),
        child: Semantics(
          button: true,
          label: '系统通知${unread > 0 ? '，$unread 条未读' : ''}，$subtitle',
          child: Material(
            key: const Key('system-notification-surface'),
            color: dark ? LinliColors.darkPinnedSurface : LinliColors.brandMint,
            child: InkWell(
              key: const Key('system-notifications-entry'),
              overlayColor: WidgetStatePropertyAll(
                LinliColors.brandGreen.withValues(alpha: .12),
              ),
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) =>
                      SystemNotificationsScreen(controller: controller),
                ),
              ),
              child: ConstrainedBox(
                constraints: const BoxConstraints(minHeight: 74),
                child: Row(
                  children: [
                    Padding(
                      padding: const EdgeInsets.only(left: 16, right: 12),
                      child: Container(
                        width: 48,
                        height: 48,
                        decoration: BoxDecoration(
                          color: LinliColors.brandGreen,
                          borderRadius: BorderRadius.circular(14),
                        ),
                        alignment: Alignment.center,
                        child: const Icon(
                          CupertinoIcons.bell_fill,
                          size: 22,
                          color: Colors.white,
                        ),
                      ),
                    ),
                    Expanded(
                      child: Container(
                        key: const Key('system-notification-content'),
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
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: [
                            Row(
                              children: [
                                Expanded(
                                  child: Text(
                                    '系统通知',
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: Theme.of(
                                      context,
                                    ).textTheme.titleMedium,
                                  ),
                                ),
                                if (time.isNotEmpty) ...[
                                  const SizedBox(width: 8),
                                  Text(
                                    time,
                                    style: Theme.of(
                                      context,
                                    ).textTheme.labelSmall,
                                  ),
                                ],
                              ],
                            ),
                            const SizedBox(height: 5),
                            Row(
                              children: [
                                Expanded(
                                  child: Text(
                                    subtitle,
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: Theme.of(context)
                                        .textTheme
                                        .bodyMedium
                                        ?.copyWith(
                                          color: error == null
                                              ? LinliColors.preview
                                              : LinliColors.systemRed,
                                          fontSize: 14,
                                        ),
                                  ),
                                ),
                                if (unread > 0) ...[
                                  const SizedBox(width: 8),
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
                                      unread > 99 ? '99+' : '$unread',
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
    },
  );
}

class SystemNotificationsScreen extends StatelessWidget {
  const SystemNotificationsScreen({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: controller,
    builder: (context, _) {
      final announcements = controller.announcements;
      final error = controller.announcementsLoadError;
      return Scaffold(
        appBar: GlassAppBar(
          title: const Text('系统通知'),
          actions: [
            IconButton(
              key: const Key('refresh-system-notifications'),
              tooltip: '刷新系统通知',
              onPressed: controller.refreshAnnouncements,
              icon: const Icon(CupertinoIcons.refresh),
            ),
          ],
        ),
        body: RefreshIndicator(
          onRefresh: controller.refreshAnnouncements,
          color: LinliColors.navy,
          child: ListView.builder(
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.only(top: 8, bottom: 32),
            itemCount: announcements.isEmpty
                ? 1
                : announcements.length + (error == null ? 0 : 1),
            itemBuilder: (context, index) {
              if (error != null && index == 0) {
                return _NotificationLoadNotice(
                  message: error,
                  onRetry: controller.refreshAnnouncements,
                );
              }
              if (announcements.isEmpty) {
                return StatePanel(
                  icon: CupertinoIcons.bell,
                  title: error == null ? '暂无系统通知' : '通知加载失败',
                  body: error ?? '平台公告和服务提醒会显示在这里。',
                  actionLabel: error == null ? null : '重新加载',
                  onAction: error == null
                      ? null
                      : controller.refreshAnnouncements,
                );
              }
              final announcement =
                  announcements[index - (error == null ? 0 : 1)];
              return _SystemNotificationRow(
                controller: controller,
                announcement: announcement,
              );
            },
          ),
        ),
      );
    },
  );
}

class _NotificationLoadNotice extends StatelessWidget {
  const _NotificationLoadNotice({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
    child: Material(
      color: LinliColors.systemRed.withValues(alpha: .08),
      borderRadius: BorderRadius.circular(12),
      child: ListTile(
        minTileHeight: 52,
        leading: const Icon(
          CupertinoIcons.exclamationmark_circle,
          color: LinliColors.systemRed,
        ),
        title: Text(message, maxLines: 2, overflow: TextOverflow.ellipsis),
        trailing: TextButton(onPressed: onRetry, child: const Text('重试')),
      ),
    ),
  );
}

class _SystemNotificationRow extends StatelessWidget {
  const _SystemNotificationRow({
    required this.controller,
    required this.announcement,
  });

  final AppController controller;
  final AppAnnouncement announcement;

  @override
  Widget build(BuildContext context) => InkWell(
    onTap: () => Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => AnnouncementDetailsScreen(
          controller: controller,
          announcement: announcement,
        ),
      ),
    ),
    child: Padding(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 14),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 40,
            height: 40,
            decoration: BoxDecoration(
              color: Theme.of(context).colorScheme.surfaceContainerHighest,
              borderRadius: BorderRadius.circular(12),
            ),
            alignment: Alignment.center,
            child: Icon(
              announcement.pinned
                  ? CupertinoIcons.pin_fill
                  : CupertinoIcons.speaker_2_fill,
              size: 19,
              color: Theme.of(context).brightness == Brightness.dark
                  ? LinliColors.brandGreen
                  : LinliColors.navy,
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        announcement.title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.titleMedium
                            ?.copyWith(
                              fontWeight: announcement.unread
                                  ? FontWeight.w700
                                  : FontWeight.w500,
                            ),
                      ),
                    ),
                    if (announcement.publishedAt != null)
                      Text(
                        _notificationDate(announcement.publishedAt!),
                        style: Theme.of(context).textTheme.labelSmall,
                      ),
                  ],
                ),
                const SizedBox(height: 4),
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        announcement.content,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                          color: Theme.of(
                            context,
                          ).colorScheme.onSurface.withValues(alpha: .58),
                          height: 1.35,
                        ),
                      ),
                    ),
                    if (announcement.unread) ...[
                      const SizedBox(width: 10),
                      Container(
                        width: 8,
                        height: 8,
                        decoration: const BoxDecoration(
                          color: LinliColors.unread,
                          shape: BoxShape.circle,
                        ),
                      ),
                    ],
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    ),
  );
}

String _notificationDate(DateTime value) {
  final local = value.toLocal();
  final now = DateTime.now();
  if (local.year == now.year &&
      local.month == now.month &&
      local.day == now.day) {
    return '${local.hour.toString().padLeft(2, '0')}:${local.minute.toString().padLeft(2, '0')}';
  }
  if (local.year == now.year) return '${local.month}月${local.day}日';
  return '${local.year}/${local.month}/${local.day}';
}

class AnnouncementTicker extends StatelessWidget {
  const AnnouncementTicker({super.key, required this.controller});
  final AppController controller;

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: controller,
    builder: (context, _) {
      final announcements = controller.announcements;
      if (announcements.isEmpty) return const SizedBox.shrink();
      return SizedBox(
        key: const Key('announcement-ticker'),
        height: 48,
        child: PageView.builder(
          itemCount: announcements.length,
          itemBuilder: (context, index) {
            final announcement = announcements[index];
            return InkWell(
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => AnnouncementDetailsScreen(
                    controller: controller,
                    announcement: announcement,
                  ),
                ),
              ),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Row(
                  children: [
                    Icon(
                      announcement.pinned
                          ? CupertinoIcons.pin_fill
                          : CupertinoIcons.speaker_2_fill,
                      size: 17,
                      color: Theme.of(context).brightness == Brightness.dark
                          ? LinliColors.brandGreen
                          : LinliColors.navy,
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: _AnnouncementMarquee(
                        key: Key('announcement-scroll-${announcement.id}'),
                        text: '${announcement.title}  ${announcement.content}',
                        style: Theme.of(context).textTheme.bodyMedium,
                      ),
                    ),
                    if (announcement.unread) ...[
                      const SizedBox(width: 10),
                      Container(
                        width: 8,
                        height: 8,
                        decoration: const BoxDecoration(
                          color: LinliColors.unread,
                          shape: BoxShape.circle,
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            );
          },
        ),
      );
    },
  );
}

class _AnnouncementMarquee extends StatefulWidget {
  const _AnnouncementMarquee({super.key, required this.text, this.style});

  final String text;
  final TextStyle? style;

  @override
  State<_AnnouncementMarquee> createState() => _AnnouncementMarqueeState();
}

class _AnnouncementMarqueeState extends State<_AnnouncementMarquee> {
  final ScrollController _controller = ScrollController();
  Timer? _timer;
  bool _running = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _schedule());
  }

  @override
  void didUpdateWidget(covariant _AnnouncementMarquee oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.text != widget.text || oldWidget.style != widget.style) {
      _timer?.cancel();
      _running = false;
      WidgetsBinding.instance.addPostFrameCallback((_) => _schedule());
    }
  }

  void _schedule() {
    if (!mounted || !_controller.hasClients) return;
    if (_controller.position.maxScrollExtent <= 0 ||
        MediaQuery.disableAnimationsOf(context)) {
      return;
    }
    _timer = Timer(const Duration(milliseconds: 1200), _scrollForward);
  }

  Future<void> _scrollForward() async {
    if (!mounted || !_controller.hasClients || _running) return;
    _running = true;
    try {
      final distance = _controller.position.maxScrollExtent;
      if (distance <= 0) return;
      await _controller.animateTo(
        distance,
        duration: Duration(
          milliseconds: (distance * 28).clamp(2600, 9000).round(),
        ),
        curve: Curves.linear,
      );
    } finally {
      _running = false;
    }
    if (!mounted || !_controller.hasClients) return;
    _timer = Timer(const Duration(milliseconds: 900), () {
      if (!mounted || !_controller.hasClients) return;
      _controller.jumpTo(0);
      _schedule();
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ClipRect(
    child: SingleChildScrollView(
      controller: _controller,
      scrollDirection: Axis.horizontal,
      physics: const NeverScrollableScrollPhysics(),
      child: Text(
        widget.text,
        maxLines: 1,
        softWrap: false,
        style: widget.style,
      ),
    ),
  );
}

class AnnouncementDetailsScreen extends StatefulWidget {
  const AnnouncementDetailsScreen({
    super.key,
    required this.controller,
    required this.announcement,
  });
  final AppController controller;
  final AppAnnouncement announcement;

  @override
  State<AnnouncementDetailsScreen> createState() =>
      _AnnouncementDetailsScreenState();
}

class _AnnouncementDetailsScreenState extends State<AnnouncementDetailsScreen> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      widget.controller.markAnnouncementRead(widget.announcement);
    });
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: const GlassAppBar(title: Text('平台公告')),
    body: ListView(
      padding: const EdgeInsets.fromLTRB(20, 20, 20, 44),
      children: [
        Row(
          children: [
            if (widget.announcement.pinned) ...[
              const Icon(CupertinoIcons.pin_fill, size: 16),
              const SizedBox(width: 6),
              Text('置顶公告', style: Theme.of(context).textTheme.labelMedium),
            ],
          ],
        ),
        const SizedBox(height: 10),
        Text(
          widget.announcement.title,
          style: Theme.of(context).textTheme.headlineMedium,
        ),
        if (widget.announcement.publishedAt != null) ...[
          const SizedBox(height: 8),
          Text(
            _announcementDate(widget.announcement.publishedAt!),
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
        const SizedBox(height: 24),
        SelectableText(
          widget.announcement.content,
          style: Theme.of(context).textTheme.bodyLarge?.copyWith(height: 1.65),
        ),
      ],
    ),
  );
}

String _announcementDate(DateTime value) {
  final local = value.toLocal();
  return '${local.year}年${local.month}月${local.day}日 '
      '${local.hour.toString().padLeft(2, '0')}:'
      '${local.minute.toString().padLeft(2, '0')}';
}
