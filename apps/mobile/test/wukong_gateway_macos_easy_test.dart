import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/im/wukong_gateway_macos_easy.dart';
import 'package:wukong_easy_sdk/wukong_easy_sdk.dart' as easy;

void main() {
  test('decodes the pinned Easy SDK Base64 message body', () {
    final payload = base64Encode(
      utf8.encode(
        jsonEncode({
          'type': 1,
          'content': 'hello',
          'expiresAt': '2026-08-11T00:05:00Z',
        }),
      ),
    );

    expect(decodeWukongEasyPayload(payload), {
      'type': 1,
      'content': 'hello',
      'expiresAt': '2026-08-11T00:05:00Z',
    });
  });

  test('normalizes Easy SDK seconds and self-addressed personal channel', () {
    final message = easy.Message(
      header: const easy.MessageHeader(noPersist: false, redDot: true),
      messageId: '9223372036854775807',
      messageSeq: 7,
      timestamp: 1786406400,
      channelId: 'usr_me',
      channelType: easy.WuKongChannelType.person,
      fromUid: 'usr_peer',
      payload: base64Encode(
        utf8.encode(jsonEncode({'type': 1, 'content': 'hello'})),
      ),
      clientMsgNo: 'client-7',
    );

    final mapped = mapWukongEasyMessage(message, sessionUid: 'usr_me');
    expect(mapped.channel.id, 'usr_peer');
    expect(mapped.channel.type, 1);
    expect(mapped.timestamp, DateTime.utc(2026, 8, 11));
    expect(mapped.payload['content'], 'hello');
  });

  test('decodes WuKong CMD payload without exposing SDK types upstream', () {
    final payload = base64Encode(
      utf8.encode(
        jsonEncode({
          'type': 99,
          'cmd': 'message.expired',
          'param': {
            'schemaVersion': 1,
            'event': 'message.expired',
            'payload': {'messageId': '42'},
          },
        }),
      ),
    );
    final mapped = mapWukongEasyMessage(
      easy.Message(
        header: const easy.MessageHeader(noPersist: true, syncOnce: true),
        messageId: '1',
        messageSeq: 1,
        timestamp: 1786406400,
        channelId: 'usr_me',
        channelType: easy.WuKongChannelType.person,
        fromUid: 'system',
        payload: payload,
      ),
      sessionUid: 'usr_me',
    );
    expect(mapped.contentType, 99);
    expect(mapped.payload['cmd'], 'message.expired');
  });
}
