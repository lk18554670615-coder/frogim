import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

enum ScreenshotDetectionAvailability {
  supported,
  systemUnavailable,
  platformUnavailable,
}

/// Bridges only operating-system screenshot notifications. It never infers a
/// screenshot from focus, visibility or media-library changes.
class ScreenshotDetection {
  ScreenshotDetection._() {
    _channel.setMethodCallHandler(_handleMethodCall);
  }

  static final ScreenshotDetection instance = ScreenshotDetection._();
  static const _channel = MethodChannel('com.qingwaguagua.imapp/screenshot');

  final _events = StreamController<DateTime>.broadcast();
  bool _started = false;
  ScreenshotDetectionAvailability _availability =
      ScreenshotDetectionAvailability.platformUnavailable;

  Stream<DateTime> get events => _events.stream;
  ScreenshotDetectionAvailability get availability => _availability;

  bool get _isNativeCandidate =>
      !kIsWeb &&
      (defaultTargetPlatform == TargetPlatform.android ||
          defaultTargetPlatform == TargetPlatform.iOS);

  String get description => switch (_availability) {
    ScreenshotDetectionAvailability.supported => '系统检测到截屏后，会在当前会话发送一条真实截屏提示。',
    ScreenshotDetectionAvailability.systemUnavailable =>
      '当前 Android 系统版本不提供可靠的截屏回调；应用不会根据相册变化伪造提示。',
    ScreenshotDetectionAvailability.platformUnavailable =>
      '当前平台不提供可靠的截屏回调；应用不会伪造截屏事件。',
  };

  Future<ScreenshotDetectionAvailability> start() async {
    if (!_isNativeCandidate) {
      _availability = ScreenshotDetectionAvailability.platformUnavailable;
      return _availability;
    }
    try {
      final result = await _channel.invokeMapMethod<String, Object?>('start');
      _started = result?['supported'] == true;
      _availability = _started
          ? ScreenshotDetectionAvailability.supported
          : ScreenshotDetectionAvailability.systemUnavailable;
    } on MissingPluginException {
      _availability = ScreenshotDetectionAvailability.platformUnavailable;
    } on PlatformException {
      _availability = ScreenshotDetectionAvailability.systemUnavailable;
    }
    return _availability;
  }

  Future<void> stop() async {
    if (!_isNativeCandidate || !_started) return;
    _started = false;
    try {
      await _channel.invokeMethod<void>('stop');
    } on MissingPluginException {
      // A hot-restart can replace the native engine before this screen exits.
    } on PlatformException {
      // Detection is advisory; chat teardown must never be blocked by it.
    }
  }

  Future<void> _handleMethodCall(MethodCall call) async {
    if (call.method != 'detected' || !_started) return;
    final arguments = call.arguments;
    final raw = arguments is Map ? arguments['occurredAt'] : null;
    final occurredAt = raw is num
        ? DateTime.fromMillisecondsSinceEpoch(raw.toInt())
        : DateTime.tryParse(raw?.toString() ?? '') ?? DateTime.now();
    _events.add(occurredAt);
  }
}
