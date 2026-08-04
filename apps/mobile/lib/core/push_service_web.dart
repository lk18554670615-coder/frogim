import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'dart:math';

import 'package:shared_preferences/shared_preferences.dart';
import 'package:web/web.dart' as web;

import 'app_controller.dart';
import 'browser_notification_permission.dart';
import 'models.dart';
import 'push_service_contract.dart';
import 'web_sync_logic.dart';

PlatformPushService createPlatformPushService() => _WebPushService();

class _WebPushService implements PlatformPushService {
  static const _channelName = 'linli-im-tabs-v1';
  static const _notificationPrefix = 'linli-im-notified-v1:';

  final _tabId =
      '${DateTime.now().microsecondsSinceEpoch}-${Random().nextInt(1 << 31)}';
  final Map<String, Set<String>> _claims = {};
  final Map<String, int> _lastSequences = {};
  web.BroadcastChannel? _channel;
  AppController? _controller;
  Timer? _refreshDebounce;
  bool _snapshotReady = false;
  bool _disposed = false;

  @override
  Future<void> initialize(AppController controller) async {
    _controller = controller;
    _cleanupNotificationClaims();
    try {
      final channel = web.BroadcastChannel(_channelName);
      channel.onmessage = ((web.Event raw) {
        final event = raw as web.MessageEvent;
        final data = event.data;
        if (data == null || !data.isA<JSString>()) return;
        _handleChannelMessage(jsonDecode((data as JSString).toDart));
      }).toJS;
      _channel = channel;
    } catch (_) {
      // Older embedded browsers still keep the current tab functional.
    }
  }

  void _cleanupNotificationClaims() {
    try {
      final storage = web.window.localStorage;
      final now = DateTime.now().millisecondsSinceEpoch;
      final expired = <String>[];
      for (var index = 0; index < storage.length; index++) {
        final key = storage.key(index);
        if (key == null || !key.startsWith(_notificationPrefix)) continue;
        final timestamp = int.tryParse(storage.getItem(key) ?? '');
        if (timestamp == null ||
            now - timestamp >= const Duration(hours: 24).inMilliseconds) {
          expired.add(key);
        }
      }
      for (final key in expired) {
        storage.removeItem(key);
      }
    } catch (_) {}
  }

  void _handleChannelMessage(Object? raw) {
    if (raw is! Map<String, dynamic>) return;
    final type = raw['type'] as String?;
    final key = raw['key'] as String?;
    if (type == 'claim' && key != null) {
      final tabId = raw['tabId'] as String?;
      if (tabId != null) _claims.putIfAbsent(key, () => {}).add(tabId);
      return;
    }
    if (type == 'shown' && key != null) {
      _claims.remove(key);
      return;
    }
    if (type == 'sync') {
      _refreshDebounce?.cancel();
      _refreshDebounce = Timer(const Duration(milliseconds: 180), () {
        final controller = _controller;
        if (!_disposed && controller?.authenticated == true) {
          unawaited(controller!.refresh());
        }
      });
    }
  }

  @override
  Future<void> sync(AppController controller) async {
    if (_disposed) return;
    if (!controller.authenticated) {
      _lastSequences.clear();
      _snapshotReady = false;
      return;
    }
    final current = {
      for (final conversation in controller.conversations)
        conversation.id: conversation.lastMessageSeq,
    };
    if (!_snapshotReady) {
      _lastSequences
        ..clear()
        ..addAll(current);
      _snapshotReady = true;
      return;
    }
    final changed = conversationSequencesChanged(_lastSequences, current);
    final advanced = <Conversation>[];
    for (final conversation in controller.conversations) {
      final previous = _lastSequences[conversation.id];
      if (previous != null &&
          conversation.lastMessageSeq > previous &&
          conversation.unread > 0 &&
          conversation.id != controller.activeConversationId) {
        advanced.add(conversation);
      }
    }
    _lastSequences
      ..clear()
      ..addAll(current);
    if (!changed) return;
    _post({'type': 'sync', 'tabId': _tabId});
    if (advanced.isEmpty) return;
    final hidden =
        web.document.visibilityState != 'visible' || !web.document.hasFocus();
    if (!hidden ||
        await browserNotificationPermission() !=
            BrowserNotificationPermission.granted) {
      return;
    }
    final preferences = await SharedPreferences.getInstance();
    if (!(preferences.getBool('settings.notification.enabled') ?? true)) {
      return;
    }
    final preview =
        preferences.getBool('settings.notification.preview') ?? true;
    final sound = preferences.getBool('settings.notification.sound') ?? true;
    for (final conversation in advanced) {
      try {
        await _showOnce(conversation, preview: preview, sound: sound);
      } catch (_) {
        // Storage and Notification APIs may be disabled by browser policy.
      }
    }
  }

  Future<void> _showOnce(
    Conversation conversation, {
    required bool preview,
    required bool sound,
  }) async {
    final userId = _controller?.currentUser?.id;
    if (userId == null) return;
    final key = '$userId:${conversation.id}:${conversation.lastMessageSeq}';
    final storageKey = '$_notificationPrefix$key';
    final previous = int.tryParse(
      web.window.localStorage.getItem(storageKey) ?? '',
    );
    final now = DateTime.now().millisecondsSinceEpoch;
    if (previous != null &&
        now - previous < const Duration(hours: 24).inMilliseconds) {
      return;
    }
    _claims.putIfAbsent(key, () => {}).add(_tabId);
    _post({'type': 'claim', 'key': key, 'tabId': _tabId});
    await Future<void>.delayed(const Duration(milliseconds: 90));
    final winner = notificationClaimWinner(_claims[key] ?? {_tabId});
    if (winner != _tabId) return;
    final claimed = int.tryParse(
      web.window.localStorage.getItem(storageKey) ?? '',
    );
    if (claimed != null &&
        now - claimed < const Duration(hours: 24).inMilliseconds) {
      return;
    }
    web.window.localStorage.setItem(storageKey, '$now');
    _post({'type': 'shown', 'key': key, 'tabId': _tabId});
    final notification = web.Notification(
      conversation.title,
      web.NotificationOptions(
        body: preview ? conversation.subtitle : '收到一条新消息',
        tag: 'conversation-${conversation.id}',
        icon: 'icons/Icon-192.png',
        silent: !sound,
      ),
    );
    notification.onclick = ((web.Event _) {
      web.window.focus();
      _controller?.handlePushPayload({'conversationId': conversation.id});
      notification.close();
    }).toJS;
  }

  void _post(Map<String, Object?> message) {
    try {
      _channel?.postMessage(jsonEncode(message).toJS);
    } catch (_) {}
  }

  @override
  Future<void> dispose() async {
    _disposed = true;
    _refreshDebounce?.cancel();
    _channel?.close();
    _channel = null;
    _controller = null;
  }
}
