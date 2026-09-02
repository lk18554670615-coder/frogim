import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/user_presence.dart';
export '../../core/user_presence.dart' show UserPresenceStatus;

/// Only mounted, current-route, enabled-panel consumers hold a watch. Lazy
/// list rows naturally release their subscriptions when scrolled out of cache.
class UserPresence extends StatefulWidget {
  const UserPresence({
    super.key,
    required this.controller,
    required this.userId,
    this.groupId,
    required this.builder,
  });
  final AppController controller;
  final String userId;
  final String? groupId;
  final Widget Function(BuildContext, UserPresenceStatus) builder;
  @override
  State<UserPresence> createState() => _UserPresenceState();
}

class _UserPresenceState extends State<UserPresence> {
  VoidCallback? _release;
  bool _active = false;

  @override
  void initState() {
    super.initState();
    widget.controller.presence.addListener(_changed);
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _active =
        TickerMode.valuesOf(context).enabled &&
        (ModalRoute.isCurrentOf(context) ?? true);
    _subscribe();
  }

  void _subscribe() {
    if (!_active || widget.userId.isEmpty) {
      _release?.call();
      _release = null;
      return;
    }
    _release ??= widget.controller.presence.watch(
      widget.userId,
      groupId: widget.groupId,
    );
  }

  @override
  void didUpdateWidget(UserPresence oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller ||
        oldWidget.userId != widget.userId ||
        oldWidget.groupId != widget.groupId) {
      _release?.call();
      _release = null;
      oldWidget.controller.presence.removeListener(_changed);
      widget.controller.presence.addListener(_changed);
      _subscribe();
    }
  }

  @override
  void dispose() {
    _release?.call();
    widget.controller.presence.removeListener(_changed);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.builder(
    context,
    _active
        ? widget.controller.presence.status(
            widget.userId,
            groupId: widget.groupId,
          )
        : UserPresenceStatus.hidden,
  );
}

class PresenceLabel extends StatelessWidget {
  const PresenceLabel(this.status, {super.key});
  final UserPresenceStatus status;
  @override
  Widget build(BuildContext context) {
    if (status == UserPresenceStatus.hidden) return const SizedBox.shrink();
    final color = status == UserPresenceStatus.online
        ? LinliColors.systemGreen
        : Theme.of(context).colorScheme.onSurfaceVariant;
    final label = switch (status) {
      UserPresenceStatus.online => '在线',
      UserPresenceStatus.offline => '离线',
      _ => '状态未知',
    };
    return Text.rich(
      TextSpan(
        children: [
          if (status != UserPresenceStatus.unknown)
            const TextSpan(text: '● ', style: TextStyle(fontSize: 9)),
          TextSpan(text: label),
        ],
      ),
      semanticsLabel: label,
      style: Theme.of(context).textTheme.labelSmall?.copyWith(color: color),
    );
  }
}
