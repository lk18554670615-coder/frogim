import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';

void main() {
  test('ImSession validates fixed Web SDK session and round-trips', () {
    final session = WukongSession.fromJson({
      'uid': 'usr_web',
      'token': 'wk1_token',
      'deviceFlag': 1,
      'deviceLevel': 1,
      'tcpUrl': 'tcp://im.example:5100',
      'wsUrl': 'wss://im.example/im',
      'sdk': 'wukongimjssdk',
      'issuedAt': '2026-08-11T00:00:00Z',
    });

    expect(session.tcpAddress, 'im.example:5100');
    expect(WukongSession.fromJson(session.toJson()).toJson(), session.toJson());
  });

  test('ImSession rejects invalid transport data', () {
    expect(
      () => WukongSession.fromJson({
        'uid': 'usr_web',
        'token': 'token',
        'deviceFlag': 1,
        'deviceLevel': 1,
        'tcpUrl': 'tcp://im:5100',
        'wsUrl': 'https://not-a-websocket.example/im',
        'sdk': 'wukongimjssdk',
        'issuedAt': '2026-08-11T00:00:00Z',
      }),
      throwsFormatException,
    );
  });

  test('macOS ImSession selects the pinned desktop Easy SDK', () {
    final session = WukongSession.fromJson({
      'uid': 'usr_macos',
      'token': 'wk1_token',
      'deviceFlag': 2,
      'deviceLevel': 1,
      'tcpUrl': 'tcp://im.example:5100',
      'wsUrl': 'wss://im.example/ws',
      'sdk': 'wukong_easy_sdk',
      'issuedAt': '2026-08-11T00:00:00Z',
    });
    expect(session.deviceFlag, 2);
    expect(session.sdk, 'wukong_easy_sdk');
  });

  test('WukongMessage cache representation is lossless', () {
    final message = WukongMessage(
      messageId: '42',
      messageSeq: 8,
      clientMsgNo: 'client-42',
      clientSeq: 2,
      fromUid: 'usr_a',
      channel: const WukongChannel(id: 'group_1', type: 2),
      timestamp: DateTime.utc(2026, 8, 11),
      payload: const {'type': 1002, 'schemaVersion': 1, 'digest': 'joined'},
      state: WukongMessageState.sent,
      reasonCode: 1,
      streamContentInitialized: true,
    );

    expect(WukongMessage.fromJson(message.toJson()).toJson(), message.toJson());
  });

  test('message sync prefers lossless id and normalizes seconds', () {
    final message = WukongMessage.fromSyncJson({
      'message_id': 9223372036854775807,
      'message_idstr': '9223372036854775807',
      'message_seq': 7,
      'client_msg_no': 'client-7',
      'client_seq': 3,
      'from_uid': 'usr_a',
      'channel_id': 'group_a',
      'channel_type': 2,
      'timestamp': 1786406400,
      'payload': {'type': 1, 'content': 'hello'},
    });

    expect(message.messageId, '9223372036854775807');
    expect(message.messageSeq, 7);
    expect(message.timestamp, DateTime.utc(2026, 8, 11));
    expect(message.payload['content'], 'hello');
  });

  test('message sync projects pinned stream event snapshot', () {
    final message = WukongMessage.fromSyncJson({
      'message_idstr': '88',
      'message_seq': 3,
      'client_msg_no': 'stream-1',
      'from_uid': 'usr_a',
      'channel_id': 'usr_b',
      'channel_type': 1,
      'timestamp': 1786406400,
      'setting': 2,
      'payload': {'type': 1, 'content': 'starting'},
      'event_meta': {
        'completed': true,
        'last_msg_event_seq': 6,
        'open_event_count': 0,
        'events': [
          {
            'event_key': 'main',
            'status': 'closed',
            'snapshot': {'kind': 'text', 'text': 'complete text'},
          },
        ],
      },
    });

    expect(message.payload['content'], 'complete text');
    expect(message.streamEventSeq, 6);
    expect(message.isStreaming, isFalse);
    expect(message.streamCompleted, isTrue);
    expect(message.streamContentInitialized, isTrue);
  });
}
