import '../data/secure_local_store.dart';
import 'wukong_gateway_contract.dart';

class LocalConversationCache {
  LocalConversationCache(this._store, {this.isVisible, this.sanitize});

  final SecureLocalStore _store;
  final bool Function(WukongMessage)? isVisible;
  final WukongMessage Function(WukongMessage)? sanitize;
  final Map<String, Object> _recalls = {};

  WukongMessage _withRecall(String uid, WukongMessage message) {
    message = sanitize?.call(message) ?? message;
    final key = '$uid:${message.messageId}';
    final stamp = message.payload['recalledAt'];
    if (stamp != null) _recalls[key] = stamp;
    final recalled = _recalls[key];
    return recalled == null
        ? message
        : message.copyWith(
            payload: {...message.payload, 'recalledAt': recalled},
          );
  }

  Future<void> markRecalled(
    String uid,
    WukongChannel channel,
    String id,
  ) async {
    final key = '$uid:$id';
    if (_recalls.containsKey(key)) return;
    _recalls[key] = DateTime.now().toUtc().toIso8601String();
    final messages = await readMessages(uid, channel);
    await writeMessages(uid, channel, messages);
  }

  Future<List<WukongMessage>> readMessages(
    String uid,
    WukongChannel channel,
  ) async {
    final raw = await _store.readJson(_messageKey(uid, channel));
    if (raw is! List<Object?>) return <WukongMessage>[];
    return raw
        .whereType<Map<String, Object?>>()
        .map(WukongMessage.fromJson)
        .map((message) => _withRecall(uid, message))
        .where((message) => isVisible?.call(message) ?? true)
        .toList();
  }

  Future<void> writeMessages(
    String uid,
    WukongChannel channel,
    Iterable<WukongMessage> messages,
  ) => _store.writeJson(
    _messageKey(uid, channel),
    messages
        .map((message) => _withRecall(uid, message))
        .where((message) => isVisible?.call(message) ?? true)
        .map((message) => message.toJson())
        .toList(),
  );

  Future<List<WukongMessage>> mergeMessages(
    String uid,
    WukongChannel channel,
    Iterable<WukongMessage> authoritative,
  ) async {
    final merged = await readMessages(uid, channel);
    for (final raw in authoritative) {
      final message = _withRecall(uid, raw);
      final index = merged.indexWhere(
        (item) =>
            (message.messageId.isNotEmpty &&
                item.messageId == message.messageId) ||
            (message.clientMsgNo.isNotEmpty &&
                item.clientMsgNo == message.clientMsgNo),
      );
      if (index < 0) {
        merged.add(message);
      } else {
        merged[index] = message;
      }
    }
    _sortMessages(merged);
    final retained = merged.length <= 1000
        ? merged
        : merged.sublist(merged.length - 1000);
    await writeMessages(uid, channel, retained);
    return retained
        .where((message) => isVisible?.call(message) ?? true)
        .toList();
  }

  Future<void> upsertMessage(String uid, WukongMessage message) async {
    final messages = await readMessages(uid, message.channel);
    message = _withRecall(uid, message);
    final index = messages.indexWhere(
      (item) =>
          (message.messageId.isNotEmpty &&
              item.messageId == message.messageId) ||
          (message.clientMsgNo.isNotEmpty &&
              item.clientMsgNo == message.clientMsgNo),
    );
    if (index < 0) {
      messages.add(message);
    } else {
      messages[index] = message;
    }
    _sortMessages(messages);
    final retained = messages.length <= 1000
        ? messages
        : messages.sublist(messages.length - 1000);
    await writeMessages(uid, message.channel, retained);
  }

  Future<WukongMessage?> findMessage(
    String uid,
    WukongChannel channel,
    String clientMsgNo,
  ) async {
    if (clientMsgNo.isEmpty) return null;
    final messages = await readMessages(uid, channel);
    for (final message in messages.reversed) {
      if (message.clientMsgNo == clientMsgNo) return message;
    }
    return null;
  }

  Future<void> removeChannel(String uid, WukongChannel channel) =>
      _store.remove(_messageKey(uid, channel));

  String _messageKey(String uid, WukongChannel channel) =>
      'wukong.$uid.messages.${channel.key}';

  void _sortMessages(List<WukongMessage> messages) {
    messages.sort((a, b) {
      if (a.messageSeq > 0 && b.messageSeq > 0) {
        final bySeq = a.messageSeq.compareTo(b.messageSeq);
        if (bySeq != 0) return bySeq;
      }
      return a.timestamp.compareTo(b.timestamp);
    });
  }
}
