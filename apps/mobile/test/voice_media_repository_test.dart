import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/fake_wukong_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('voice upload uses audio protocol and preserves duration', () async {
    final gateway = FakeWukongGateway();
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') {
        return _json({
          'data': {
            'accessToken': 'access',
            'refreshToken': 'refresh',
            'user': {'id': 'me', 'name': '我', 'handle': 'me'},
            'imSession': {
              'uid': 'me',
              'token': 'wk1_test',
              'deviceFlag': 2,
              'deviceLevel': 1,
              'tcpUrl': 'tcp://im.example.com:5100',
              'wsUrl': 'wss://im.example.com/ws',
              'sdk': 'wukongimfluttersdk',
              'issuedAt': '2026-08-11T00:00:00Z',
            },
          },
        });
      }
      if (request.url.path == '/v2/media/presign') {
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
      if (request.url.path == '/v2/media/media-voice/complete') {
        return _json({'data': <String, Object?>{}});
      }
      if (request.url.path == '/v2/channels/conversations') {
        return _json({
          'data': {
            'items': [
              {
                'conversation': {
                  'id': 'c1',
                  'type': 'direct',
                  'title': 'Friend',
                  'updatedAt': '2026-08-11T00:00:00Z',
                },
                'members': [
                  {'id': 'me', 'name': '我'},
                  {'id': 'friend', 'name': 'Friend'},
                ],
              },
            ],
          },
        });
      }
      if (request.url.path == '/v2/im/datasource/conversations') {
        return _json({
          'data': {'items': <Object?>[]},
        });
      }
      if (request.url.path == '/v2/media/media-voice/bind') {
        return _json({
          'data': {
            'mediaId': 'media-voice',
            'url': 'https://cdn.example.com/media-voice',
          },
        });
      }
      return http.Response('{}', 404);
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example.com',
      wukongGateway: gateway,
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

    final body = gateway.sentMessages.single.payload;
    expect(body['type'], 4);
    expect(body['duration'], 9);
    expect(sent.kind, MessageContentKind.voice);
    expect(sent.durationSeconds, 9);
  });
}

http.Response _json(Map<String, Object?> body) => http.Response(
  jsonEncode(body),
  200,
  headers: {'content-type': 'application/json'},
);
