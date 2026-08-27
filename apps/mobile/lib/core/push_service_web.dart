import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'dart:math';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import 'package:web/web.dart' as web;

import 'app_config.dart';
import 'app_controller.dart';
import 'browser_notification_permission.dart';
import 'models.dart';
import 'push_service_contract.dart';
import 'web_sync_logic.dart';

PlatformPushService createPlatformPushService() => _WebPushService();

@JS('JSON.stringify')
external JSString _jsonStringify(JSAny? value);

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
  bool _backgroundSyncing = false;
  String? _backgroundRegistrationFingerprint;
  String? _vapidPublicKey;
  DateTime? _webPushConfigCheckedAt;
  web.ServiceWorkerRegistration? _pushRegistration;

  @override
  Future<void> initialize(AppController controller) async {
    _controller = controller;
    _cleanupNotificationClaims();
    _listenForServiceWorkerNavigation(controller);
    final initialConversationId = Uri.base.queryParameters['conversationId'];
    if (initialConversationId != null && initialConversationId.isNotEmpty) {
      controller.handlePushPayload({'conversationId': initialConversationId});
    }
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
      _backgroundRegistrationFingerprint = null;
      return;
    }
    await _syncBackgroundPush(controller);
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

  void _listenForServiceWorkerNavigation(AppController controller) {
    try {
      final serviceWorkers = web.window.navigator.serviceWorker;
      serviceWorkers.onmessage = ((web.Event raw) {
        final event = raw as web.MessageEvent;
        if (event.data == null) return;
        try {
          final decoded = jsonDecode(_jsonStringify(event.data).toDart);
          if (decoded is! Map) return;
          final message = decoded.map((key, value) => MapEntry('$key', value));
          if (message['type'] != 'linli.webpush.open') return;
          final payload = message['payload'];
          if (payload is Map) {
            controller.handlePushPayload(
              payload.map((key, value) => MapEntry('$key', value)),
            );
          }
        } catch (_) {}
      }).toJS;
    } catch (_) {
      // Service workers are absent in unsupported or insecure browsers.
    }
  }

  Future<void> _syncBackgroundPush(AppController controller) async {
    if (_backgroundSyncing || _disposed || !controller.authenticated) return;
    if (await browserNotificationPermission() !=
        BrowserNotificationPermission.granted) {
      return;
    }
    _backgroundSyncing = true;
    try {
      final publicKey = await _loadVapidPublicKey();
      if (publicKey == null || publicKey.isEmpty) return;
      final keyBytes = _decodeBase64Url(publicKey);
      if (keyBytes.length != 65) return;
      final serviceWorkers = web.window.navigator.serviceWorker;
      final registration = _pushRegistration ??= await serviceWorkers
          .register(
            'linli_push_worker.js'.toJS,
            web.RegistrationOptions(
              scope: 'push-scope/',
              updateViaCache: 'none',
            ),
          )
          .toDart;
      var subscription = await registration.pushManager
          .getSubscription()
          .toDart;
      if (subscription != null &&
          !_sameApplicationServerKey(subscription, keyBytes)) {
        await subscription.unsubscribe().toDart;
        subscription = null;
      }
      subscription ??= await registration.pushManager
          .subscribe(
            web.PushSubscriptionOptionsInit(
              userVisibleOnly: true,
              applicationServerKey: keyBytes.toJS,
            ),
          )
          .toDart;
      final serialized = _jsonStringify(subscription.toJSON()).toDart;
      if (serialized.isEmpty) return;
      final preferences = await SharedPreferences.getInstance();
      final notificationsEnabled =
          preferences.getBool('settings.notification.enabled') ?? true;
      final previewEnabled =
          preferences.getBool('settings.notification.preview') ?? true;
      final soundEnabled =
          preferences.getBool('settings.notification.sound') ?? true;
      final vibrationEnabled =
          preferences.getBool('settings.notification.vibration') ?? true;
      final userId = controller.currentUser?.id;
      if (userId == null) return;
      final fingerprint = [
        userId,
        serialized,
        notificationsEnabled,
        previewEnabled,
        soundEnabled,
        vibrationEnabled,
      ].join('|');
      if (_backgroundRegistrationFingerprint == fingerprint) return;
      final endpoint = subscription.endpoint;
      final digest = sha256.convert(utf8.encode(endpoint)).toString();
      await controller.registerWebPushDevice(
        deviceId: 'webpush-${digest.substring(0, 24)}',
        subscription: serialized,
        notificationsEnabled: notificationsEnabled,
        previewEnabled: previewEnabled,
        soundEnabled: soundEnabled,
        vibrationEnabled: vibrationEnabled,
      );
      _backgroundRegistrationFingerprint = fingerprint;
    } catch (_) {
      // Permission, browser policy, or a temporary API failure is retried on
      // the next controller refresh without affecting live IM delivery.
    } finally {
      _backgroundSyncing = false;
    }
  }

  Future<String?> _loadVapidPublicKey() async {
    final checkedAt = _webPushConfigCheckedAt;
    if (checkedAt != null &&
        DateTime.now().difference(checkedAt) < const Duration(minutes: 5)) {
      return _vapidPublicKey;
    }
    final base = AppConfig.apiBaseUrl.trim();
    if (base.isEmpty) return null;
    final response = await http
        .get(
          Uri.parse(base).resolve('/v2/config/web-push'),
          headers: const {'Accept': 'application/json'},
        )
        .timeout(const Duration(seconds: 8));
    if (response.statusCode < 200 || response.statusCode >= 300) return null;
    final decoded = jsonDecode(response.body);
    if (decoded is! Map || decoded['enabled'] != true) {
      _vapidPublicKey = null;
      _webPushConfigCheckedAt = DateTime.now();
      return null;
    }
    final key = decoded['publicKey']?.toString().trim() ?? '';
    _vapidPublicKey = key;
    _webPushConfigCheckedAt = DateTime.now();
    return key;
  }

  Uint8List _decodeBase64Url(String value) {
    final normalized = value.padRight((value.length + 3) ~/ 4 * 4, '=');
    return Uint8List.fromList(base64Url.decode(normalized));
  }

  bool _sameApplicationServerKey(
    web.PushSubscription subscription,
    Uint8List expected,
  ) {
    final buffer = subscription.options.applicationServerKey;
    if (buffer == null) return false;
    final current = Uint8List.view(buffer.toDart);
    if (current.length != expected.length) return false;
    for (var index = 0; index < current.length; index++) {
      if (current[index] != expected[index]) return false;
    }
    return true;
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
