import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('头像先经 presign/PUT/complete 再以 mediaId 更新资料', () async {
    final requests = <String>[];
    final client = MockClient((request) async {
      requests.add('${request.method} ${request.url.path}');
      if (request.url.path == '/v2/auth/login') {
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
      if (request.url.path == '/v2/media/presign') {
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
      if (request.url.path == '/v2/media/media-avatar-1/complete') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        expect(body['checksum'], isNotEmpty);
        return _json({});
      }
      if (request.url.path == '/v2/users/me' && request.method == 'PATCH') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        expect(body['avatarMediaId'], 'media-avatar-1');
        expect(body['name'], '新昵称');
        expect(body['handle'], 'new_name');
        expect(body['gender'], 'female');
        return _json({
          'id': 'me',
          'phone': '13800138000',
          'name': body['name'],
          'handle': body['handle'],
          'signature': body['signature'],
          'gender': body['gender'],
          'avatarMediaId': body['avatarMediaId'],
          'avatarUrl': '/v2/media/media-avatar-1',
        });
      }
      return http.Response('not found', 404);
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example',
    );
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    controller.currentUser = await repository.login('13800138000', '123456');

    final success = await controller.saveProfile(
      name: '新昵称',
      handle: 'new_name',
      signature: '新的个性签名',
      gender: 'female',
      avatar: MediaUpload(
        bytes: Uint8List.fromList([1, 2, 3]),
        fileName: 'avatar.png',
        mimeType: 'image/png',
        kind: MessageContentKind.image,
      ),
    );

    expect(success, isTrue);
    expect(controller.currentUser?.avatarMediaId, 'media-avatar-1');
    expect(controller.currentUser?.gender, 'female');
    expect(
      requests,
      containsAllInOrder([
        'POST /v2/auth/login',
        'POST /v2/media/presign',
        'PUT /avatar',
        'POST /v2/media/media-avatar-1/complete',
        'PATCH /v2/users/me',
      ]),
    );
  });

  test('资料绑定失败后重试复用已上传头像，不产生重复媒体', () async {
    final repository = _RetryProfileRepository();
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser;
    addTearDown(controller.dispose);
    final avatar = MediaUpload(
      bytes: Uint8List.fromList([1, 2, 3]),
      fileName: 'avatar.png',
      mimeType: 'image/png',
      kind: MessageContentKind.image,
    );

    final first = await controller.saveProfile(
      name: '新昵称',
      handle: 'new_name',
      signature: '新的个性签名',
      gender: 'female',
      avatar: avatar,
    );
    final second = await controller.saveProfile(
      name: '新昵称',
      handle: 'new_name',
      signature: '新的个性签名',
      gender: 'female',
      avatar: avatar,
    );

    expect(first, isFalse);
    expect(second, isTrue);
    expect(repository.uploadAvatarCalls, 1);
    expect(repository.updateProfileCalls, 2);
    expect(repository.lastAvatarMediaId, 'media-profile-avatar-1');
  });

  test('资料绑定失败后改选另一张头像会重新上传', () async {
    final repository = _RetryProfileRepository(failuresBeforeSuccess: 2);
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser;
    addTearDown(controller.dispose);

    Future<bool> save(List<int> bytes) => controller.saveProfile(
      name: '新昵称',
      handle: 'new_name',
      signature: '',
      gender: 'unspecified',
      avatar: MediaUpload(
        bytes: Uint8List.fromList(bytes),
        fileName: 'avatar.png',
        mimeType: 'image/png',
        kind: MessageContentKind.image,
      ),
    );

    expect(await save([1, 2, 3]), isFalse);
    expect(await save([4, 5, 6]), isFalse);
    expect(repository.uploadAvatarCalls, 2);
    expect(repository.updateProfileCalls, 2);
  });
}

class _RetryProfileRepository extends DemoImRepository {
  _RetryProfileRepository({this.failuresBeforeSuccess = 1})
    : super(latency: Duration.zero);

  final int failuresBeforeSuccess;
  int uploadAvatarCalls = 0;
  int updateProfileCalls = 0;
  String? lastAvatarMediaId;

  @override
  Future<String> uploadAvatar(MediaUpload upload) async {
    uploadAvatarCalls += 1;
    return 'media-profile-avatar-$uploadAvatarCalls';
  }

  @override
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? gender,
    String? avatarMediaId,
    bool? allowSearchByHandle,
    bool? allowSearchByPhone,
  }) async {
    updateProfileCalls += 1;
    lastAvatarMediaId = avatarMediaId;
    if (updateProfileCalls <= failuresBeforeSuccess) {
      throw Exception('temporary profile update failure');
    }
    return super.updateProfile(
      name: name,
      handle: handle,
      signature: signature,
      gender: gender,
      avatarMediaId: avatarMediaId,
      allowSearchByHandle: allowSearchByHandle,
      allowSearchByPhone: allowSearchByPhone,
    );
  }
}

http.Response _json(Map<String, Object?> value) => http.Response(
  jsonEncode(value),
  200,
  headers: {'content-type': 'application/json'},
);
