import 'dart:convert';

import '../proto/packet.dart';

class WKEvent {
  WKEvent(EventPacket packet)
      : id = packet.id,
        type = packet.type,
        timestamp = packet.timestamp,
        data = List<int>.unmodifiable(packet.data),
        dataJson = _decodeData(packet.data);

  final String id;
  final String type;
  final int timestamp;
  final List<int> data;
  final Map<String, dynamic> dataJson;

  static Map<String, dynamic> _decodeData(List<int> bytes) {
    final decoded = jsonDecode(utf8.decode(bytes));
    if (decoded is! Map) {
      throw const FormatException('WuKongIM event data must be a JSON object');
    }
    return decoded.map((key, value) => MapEntry(key.toString(), value));
  }
}

class WKEventManager {
  WKEventManager._();

  static final WKEventManager shared = WKEventManager._();
  final Map<String, void Function(WKEvent)> _listeners = {};

  void addEventListener(String key, void Function(WKEvent) listener) {
    _listeners[key] = listener;
  }

  void removeEventListener(String key) {
    _listeners.remove(key);
  }

  void notifyEventListeners(WKEvent event) {
    for (final listener in List<void Function(WKEvent)>.of(_listeners.values)) {
      listener(event);
    }
  }
}
