import 'dart:collection';

import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'models.dart';

class MessageFeedbackPreferences {
  const MessageFeedbackPreferences({
    this.enabled = true,
    this.sound = true,
    this.vibration = true,
  });
  final bool enabled;
  final bool sound;
  final bool vibration;

  static Future<MessageFeedbackPreferences> load() async {
    final prefs = await SharedPreferences.getInstance();
    return MessageFeedbackPreferences(
      enabled: prefs.getBool('settings.notification.enabled') ?? true,
      sound: prefs.getBool('settings.notification.sound') ?? true,
      vibration: prefs.getBool('settings.notification.vibration') ?? true,
    );
  }
}

typedef MessageFeedbackPlayer =
    Future<void> Function({required bool sound, required bool vibration});

/// Foreground feedback only. Background/offline notification presentation is
/// still owned by Getui/APNs, not a second locally generated notification.
class MessageFeedback {
  MessageFeedback({
    Future<MessageFeedbackPreferences> Function()? preferences,
    MessageFeedbackPlayer? play,
    DateTime Function()? now,
  }) : _preferences = preferences ?? MessageFeedbackPreferences.load,
       _play = play ?? _playNative,
       _now = now ?? DateTime.now;

  static const _channel = MethodChannel('com.fd.kuailiao/message_feedback');
  static const webSoundAsset = 'sounds/message.wav';
  static AudioPlayer? _webPlayer;
  final Future<MessageFeedbackPreferences> Function() _preferences;
  final MessageFeedbackPlayer _play;
  final DateTime Function() _now;
  final LinkedHashSet<String> _seen = LinkedHashSet();
  String? _account;
  int _epoch = 0;
  bool _foreground = true;
  bool _disposed = false;
  DateTime? _lastAlert;

  void setAccount(String? account) {
    if (_account == account) return;
    _account = account;
    _epoch++;
    _seen.clear();
    _lastAlert = null;
  }

  void setForeground(bool foreground) {
    if (_foreground == foreground) return;
    _foreground = foreground;
    _epoch++;
  }

  Future<void> received(
    ChatMessage message, {
    required bool Function() eligible,
  }) async {
    if (_disposed || _account == null) return;
    final key =
        '${message.conversationId}:${message.id}:${message.clientMessageId}';
    if (!_seen.add(key)) return;
    if (_seen.length > 1000) _seen.remove(_seen.first);
    if (!_foreground || !eligible()) return;
    final epoch = _epoch;
    try {
      final prefs = await _preferences();
      if (_disposed ||
          epoch != _epoch ||
          !_foreground ||
          !eligible() ||
          !prefs.enabled ||
          (!prefs.sound && !prefs.vibration)) {
        return;
      }
      final now = _now();
      if (_lastAlert != null &&
          now.difference(_lastAlert!) < const Duration(milliseconds: 900)) {
        return;
      }
      // Reserve before invoking the platform: concurrent arrivals cannot all
      // pass the cooldown while a previous platform call is still in flight.
      _lastAlert = now;
      await _play(sound: prefs.sound, vibration: prefs.vibration);
    } catch (error) {
      // Notification failure must never interrupt IM processing or persistence.
      if (kDebugMode) {
        debugPrint('[message-feedback] unavailable: ${error.runtimeType}');
      }
    }
  }

  static Future<void> _playNative({
    required bool sound,
    required bool vibration,
  }) async {
    if (kIsWeb) {
      if (!sound) return;
      final player = _webPlayer ??= AudioPlayer(playerId: 'message-feedback');
      await player.stop();
      await player.play(
        AssetSource(webSoundAsset),
        volume: .82,
        mode: PlayerMode.lowLatency,
      );
      return;
    }
    if (!{
      TargetPlatform.android,
      TargetPlatform.iOS,
    }.contains(defaultTargetPlatform)) {
      return;
    }
    await _channel.invokeMethod<void>('play', {
      'sound': sound,
      'vibration': vibration,
    });
  }

  void dispose() {
    _disposed = true;
    _epoch++;
    _seen.clear();
  }
}
