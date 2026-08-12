import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/local_conversation_cache.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('encrypted cache upserts by client id then server id', () async {
    SharedPreferences.setMockInitialValues({});
    final cache = LocalConversationCache(SecureLocalStore());
    const channel = WukongChannel(id: 'usr_b', type: 1);
    final pending = WukongMessage(
      messageId: '',
      messageSeq: 0,
      clientMsgNo: 'client-1',
      clientSeq: 1,
      fromUid: 'usr_a',
      channel: channel,
      timestamp: DateTime.utc(2026, 8, 11),
      payload: const {'type': 1, 'content': 'hello'},
      state: WukongMessageState.sending,
    );
    await cache.upsertMessage('usr_a', pending);
    await cache.upsertMessage(
      'usr_a',
      WukongMessage(
        messageId: '99',
        messageSeq: 3,
        clientMsgNo: 'client-1',
        clientSeq: 1,
        fromUid: 'usr_a',
        channel: channel,
        timestamp: DateTime.utc(2026, 8, 11),
        payload: const {'type': 1, 'content': 'hello'},
        state: WukongMessageState.sent,
        reasonCode: 1,
      ),
    );

    final messages = await cache.readMessages('usr_a', channel);
    expect(messages, hasLength(1));
    expect(messages.single.messageId, '99');
    expect(messages.single.state, WukongMessageState.sent);
  });

  test(
    'remote sync merges with pending and cached history instead of replacing it',
    () async {
      SharedPreferences.setMockInitialValues({});
      final cache = LocalConversationCache(SecureLocalStore());
      const channel = WukongChannel(id: 'usr_b', type: 1);
      final old = _message(
        channel: channel,
        messageId: 'old-1',
        clientMsgNo: 'old-client',
        messageSeq: 1,
        timestamp: DateTime.utc(2026, 8, 11, 8),
        content: 'cached history',
      );
      final pending = _message(
        channel: channel,
        messageId: '',
        clientMsgNo: 'pending-client',
        messageSeq: 0,
        timestamp: DateTime.utc(2026, 8, 11, 10),
        content: 'pending',
        state: WukongMessageState.sending,
      );
      await cache.writeMessages('usr_a', channel, [old, pending]);

      final merged = await cache.mergeMessages('usr_a', channel, [
        _message(
          channel: channel,
          messageId: 'server-2',
          clientMsgNo: 'server-client',
          messageSeq: 2,
          timestamp: DateTime.utc(2026, 8, 11, 9),
          content: 'remote',
        ),
      ]);

      expect(merged.map((message) => message.clientMsgNo), [
        'old-client',
        'server-client',
        'pending-client',
      ]);
      expect(merged.last.state, WukongMessageState.sending);
    },
  );

  test(
    'remote authoritative row replaces matching pending client number',
    () async {
      SharedPreferences.setMockInitialValues({});
      final cache = LocalConversationCache(SecureLocalStore());
      const channel = WukongChannel(id: 'usr_b', type: 1);
      await cache.upsertMessage(
        'usr_a',
        _message(
          channel: channel,
          messageId: '',
          clientMsgNo: 'same-client',
          messageSeq: 0,
          timestamp: DateTime.utc(2026, 8, 11, 10),
          content: 'pending',
          state: WukongMessageState.sending,
        ),
      );

      final merged = await cache.mergeMessages('usr_a', channel, [
        _message(
          channel: channel,
          messageId: 'authoritative-9',
          clientMsgNo: 'same-client',
          messageSeq: 9,
          timestamp: DateTime.utc(2026, 8, 11, 10),
          content: 'confirmed',
        ),
      ]);

      expect(merged, hasLength(1));
      expect(merged.single.messageId, 'authoritative-9');
      expect(merged.single.state, WukongMessageState.sent);
    },
  );
}

WukongMessage _message({
  required WukongChannel channel,
  required String messageId,
  required String clientMsgNo,
  required int messageSeq,
  required DateTime timestamp,
  required String content,
  WukongMessageState state = WukongMessageState.sent,
}) => WukongMessage(
  messageId: messageId,
  messageSeq: messageSeq,
  clientMsgNo: clientMsgNo,
  clientSeq: messageSeq,
  fromUid: 'usr_a',
  channel: channel,
  timestamp: timestamp,
  payload: {'type': 1, 'content': content},
  state: state,
  reasonCode: state == WukongMessageState.sent ? 1 : 0,
);
