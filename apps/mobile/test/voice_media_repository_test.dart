import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('voice upload uses audio protocol and preserves duration', () async {
    http.Request? messageRequest;
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') {
        return _json({
          'data': {
            'accessToken': 'access',
            'refreshToken': 'refresh',
            'user': {'id': 'me', 'name': '我', 'handle': 'me'},
          },
        });
      }
      if (request.url.path == '/v1/media/presign') {
        return _json({
          'data': {
            'uploadUrl': 'https://upload.example.com/voice.m4a',
            'mediaId': 'media-voice',
            'headers': <String, Object?>{},
          },
        });
      }
      if (request.url.host == 'upload.example.com') {
        return http.Response('', 200);
      }
      if (request.url.path == '/v1/media/media-voice/complete') {
        return _json({'data': <String, Object?>{}});
      }
      if (request.url.path == '/v1/conversations/c1/messages') {
        messageRequest = request;
        return _json({
          'data': {
            'message': {
              'id': 'message-1',
              'clientMsgId': 'client-1',
              'conversationId': 'c1',
              'senderId': 'me',
              'senderName': '我',
              'type': 'audio',
              'createdAt': '2026-07-31T12:00:00.000Z',
              'body': {
                'type': 'audio',
                'mediaId': 'media-voice',
                'duration': 9,
                'mime': 'audio/mp4',
              },
            },
          },
        });
      }
      return http.Response('{}', 404);
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example.com',
      wsUrl: 'wss://api.example.com/ws',
    );
    addTearDown(repository.close);
    await repository.login('13800138000', '123456');

    final sent = await repository.sendMedia(
      ChatMessage(
        id: 'local-client-1',
        clientMessageId: 'client-1',
        conversationId: 'c1',
        senderId: 'me',
        senderName: '我',
        text: '[语音]',
        sentAt: DateTime.now(),
        isMine: true,
        kind: MessageContentKind.voice,
      ),
      MediaUpload(
        bytes: Uint8List.fromList([1, 2, 3]),
        fileName: 'voice.m4a',
        mimeType: 'audio/mp4',
        kind: MessageContentKind.voice,
        localPath: '/tmp/voice.m4a',
        durationSeconds: 9,
      ),
    );

    final body = jsonDecode(messageRequest!.body) as Map<String, Object?>;
    expect(body['type'], 'audio');
    expect((body['body'] as Map<String, Object?>)['duration'], 9);
    expect(sent.kind, MessageContentKind.voice);
    expect(sent.durationSeconds, 9);
  });
}

http.Response _json(Map<String, Object?> body) => http.Response(
  jsonEncode(body),
  200,
  headers: {'content-type': 'application/json'},
);
