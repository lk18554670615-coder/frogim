import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../widgets/linli_widgets.dart';

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
                          ? LinliColors.yellow
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
