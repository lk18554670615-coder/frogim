import 'package:flutter/foundation.dart';

import 'models.dart';

enum ForwardTargetStatus { pending, sending, succeeded, failed }

class ForwardSendResult {
  const ForwardSendResult.success(this.messages)
    : error = null,
      sessionExpired = false;
  const ForwardSendResult.failure(this.error, {this.sessionExpired = false})
    : messages = const [];

  final List<ChatMessage> messages;
  final String? error;
  final bool sessionExpired;
  bool get succeeded => error == null && messages.isNotEmpty;
}

class ForwardTarget {
  ForwardTarget(this.conversation, this.clientBatchId);

  final Conversation conversation;
  final String clientBatchId;
  ForwardTargetStatus _status = ForwardTargetStatus.pending;
  String? _error;
  ForwardTargetStatus get status => _status;
  String? get error => _error;
}

class ForwardBatchSummary {
  const ForwardBatchSummary({
    required this.total,
    required this.succeeded,
    required this.failed,
    required this.notSent,
  });

  final int total;
  final int succeeded;
  final int failed;
  final int notSent;
  bool get allSucceeded => total > 0 && succeeded == total;
}

/// One in-memory operation. Retrying it never replaces a target's idempotency ID.
class ForwardBatchTask extends ChangeNotifier {
  ForwardBatchTask({
    required List<Conversation> conversations,
    required String Function() createBatchId,
    required this.send,
    required this.canContinue,
    this.onDispose,
  }) : targets = List.unmodifiable([
         for (final conversation in {
           for (final item in conversations) item.id: item,
         }.values)
           ForwardTarget(conversation, createBatchId()),
       ]);

  static const concurrency = 3;
  final List<ForwardTarget> targets;
  final Future<ForwardSendResult> Function(String targetId, String batchId)
  send;
  final bool Function() canContinue;
  final VoidCallback? onDispose;
  bool _running = false;
  bool _stopRequested = false;
  bool _sessionExpired = false;
  bool _disposed = false;

  bool get running => _running;
  bool get stopRequested => _stopRequested;
  bool get sessionExpired => _sessionExpired;
  int _count(ForwardTargetStatus status) =>
      targets.where((target) => target.status == status).length;
  int get succeededCount => _count(ForwardTargetStatus.succeeded);
  int get failedCount => _count(ForwardTargetStatus.failed);
  int get notSentCount => _count(ForwardTargetStatus.pending);
  int get completedCount => succeededCount + failedCount;
  bool get allSucceeded =>
      targets.isNotEmpty && succeededCount == targets.length;
  bool get canRetry =>
      targets.isNotEmpty &&
      !_disposed &&
      !_running &&
      !_sessionExpired &&
      !allSucceeded;
  ForwardBatchSummary get summary => ForwardBatchSummary(
    total: targets.length,
    succeeded: succeededCount,
    failed: failedCount,
    notSent: notSentCount,
  );

  void stop() {
    if (_disposed || !_running || _stopRequested) return;
    _stopRequested = true;
    notifyListeners();
  }

  void invalidateSession() {
    if (_disposed) return;
    _sessionExpired = true;
    _stopRequested = true;
    notifyListeners();
  }

  Future<void> start() async {
    if (!canRetry) return;
    if (!canContinue()) {
      invalidateSession();
      return;
    }
    _running = true;
    _stopRequested = false;
    final remaining = targets
        .where((target) => target.status != ForwardTargetStatus.succeeded)
        .toList();
    for (final target in remaining) {
      target._status = ForwardTargetStatus.pending;
      target._error = null;
    }
    var next = 0;
    notifyListeners();

    Future<void> worker() async {
      while (!_disposed && !_stopRequested && next < remaining.length) {
        if (!canContinue()) {
          invalidateSession();
          return;
        }
        final target = remaining[next++];
        target._status = ForwardTargetStatus.sending;
        notifyListeners();
        // A listener may cancel or log out before this request starts.
        if (_disposed || _stopRequested || !canContinue()) {
          target._status = ForwardTargetStatus.pending;
          return;
        }
        ForwardSendResult result;
        try {
          result = await send(target.conversation.id, target.clientBatchId);
        } catch (_) {
          result = const ForwardSendResult.failure('转发失败，请稍后重试');
        }
        target._status = result.succeeded
            ? ForwardTargetStatus.succeeded
            : ForwardTargetStatus.failed;
        target._error = result.succeeded ? null : result.error ?? '转发失败，请稍后重试';
        if (_disposed) return;
        if (result.sessionExpired || !canContinue()) {
          invalidateSession();
        } else {
          notifyListeners();
        }
      }
    }

    try {
      await Future.wait([
        for (var index = 0; index < concurrency; index++) worker(),
      ]);
    } finally {
      _running = false;
      if (!_disposed) notifyListeners();
    }
  }

  @override
  void dispose() {
    if (_disposed) return;
    _stopRequested = true;
    _disposed = true;
    onDispose?.call();
    super.dispose();
  }
}

String? validateForwardMessages(List<ChatMessage> messages) {
  if (messages.isEmpty) return '请选择要转发的消息';
  if (messages.length > 100) return '每次最多转发 100 条消息，请减少选择后重试';
  if (messages.any(
    (message) =>
        message.id.startsWith('local-') ||
        message.status == MessageStatus.sending ||
        message.status == MessageStatus.failed ||
        message.status == MessageStatus.recalled ||
        message.status == MessageStatus.expired,
  )) {
    return '请选择已发送且未撤回、未过期的消息';
  }
  if (messages.map((message) => message.id).toSet().length != messages.length) {
    return '不能重复选择同一条消息';
  }
  return null;
}
