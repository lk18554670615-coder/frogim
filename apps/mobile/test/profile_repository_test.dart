import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('头像先经 presign/PUT/complete 再以 mediaId 更新资料', () async {
    final requests = <String>[];
    final client = MockClient((request) async {
      requests.add('${request.method} ${request.url.path}');
      if (request.url.path == '/v1/auth/login') {
        return _json({
          'accessToken': 'access',
          'refreshToken': 'refresh',
          'user': {
            'id': 'me',
            'phone': '13800138000',
            'name': '旧昵称',
            'handle': 'old_name',
            'signature': '',
          },
        });
      }
      if (request.url.path == '/v1/media/presign') {
        return _json({
          'uploadUrl': 'https://upload.example/avatar',
          'mediaId': 'media-avatar-1',
          'headers': {'content-type': 'image/png'},
        });
      }
      if (request.url.host == 'upload.example') {
        expect(request.bodyBytes, Uint8List.fromList([1, 2, 3]));
        return http.Response('', 200);
      }
      if (request.url.path == '/v1/media/media-avatar-1/complete') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        expect(body['checksum'], isNotEmpty);
        return _json({});
      }
      if (request.url.path == '/v1/users/me' && request.method == 'PATCH') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        expect(body['avatarMediaId'], 'media-avatar-1');
        expect(body['name'], '新昵称');
        expect(body['handle'], 'new_name');
        return _json({
          'id': 'me',
          'phone': '13800138000',
          'name': body['name'],
          'handle': body['handle'],
          'signature': body['signature'],
          'avatarMediaId': body['avatarMediaId'],
          'avatarUrl': '/v1/media/media-avatar-1',
        });
      }
      return http.Response('not found', 404);
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example',
      wsUrl: 'wss://api.example/v1/ws',
    );
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    controller.currentUser = await repository.login('13800138000', '123456');

    final success = await controller.saveProfile(
      name: '新昵称',
      handle: 'new_name',
      signature: '新的个性签名',
      avatar: MediaUpload(
        bytes: Uint8List.fromList([1, 2, 3]),
        fileName: 'avatar.png',
        mimeType: 'image/png',
        kind: MessageContentKind.image,
      ),
    );

    expect(success, isTrue);
    expect(controller.currentUser?.avatarMediaId, 'media-avatar-1');
    expect(
      requests,
      containsAllInOrder([
        'POST /v1/auth/login',
        'POST /v1/media/presign',
        'PUT /avatar',
        'POST /v1/media/media-avatar-1/complete',
        'PATCH /v1/users/me',
      ]),
    );
  });
}

http.Response _json(Map<String, Object?> value) => http.Response(
  jsonEncode(value),
  200,
  headers: {'content-type': 'application/json'},
);
