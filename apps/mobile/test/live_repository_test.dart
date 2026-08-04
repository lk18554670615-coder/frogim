import 'dart:convert';
import 'dart:io';
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

  test('退出登录向服务端撤销 refresh token 后清理本地会话', () async {
    final requests = <http.Request>[];
    final client = MockClient((request) async {
      requests.add(request);
      if (request.url.path == '/v1/auth/login') {
        return http.Response(
          jsonEncode({
            'data': {
              'accessToken': 'access-token',
              'refreshToken': 'refresh-token',
              'user': {'id': 'user-1', 'name': '测试用户', 'handle': 'tester'},
            },
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      }
      if (request.url.path == '/v1/auth/logout') {
        return http.Response('', 204);
      }
      return http.Response('{}', 404);
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example.com',
      wsUrl: 'wss://api.example.com/ws',
    );

    await repository.login('13800138000', '123456');
    await repository.logout();

    final logoutRequest = requests.singleWhere(
      (request) => request.url.path == '/v1/auth/logout',
    );
    expect(
      jsonDecode(logoutRequest.body),
      containsPair('refreshToken', 'refresh-token'),
    );
    expect(await repository.restoreSession(), isFalse);
    await repository.close();
  });

  test('服务端撤销失败时仍完成本地退出', () async {
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') {
        return http.Response(
          jsonEncode({
            'data': {
              'accessToken': 'access-token',
              'refreshToken': 'refresh-token',
              'user': {'id': 'user-1', 'name': '测试用户', 'handle': 'tester'},
            },
          }),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        );
      }
      return http.Response(
        jsonEncode({
          'error': {'code': 'OFFLINE', 'message': 'offline'},
        }),
        503,
        headers: {'content-type': 'application/json; charset=utf-8'},
      );
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example.com',
      wsUrl: 'wss://api.example.com/ws',
    );

    await repository.login('13800138000', '123456');
    await repository.logout();

    expect(await repository.restoreSession(), isFalse);
    await repository.close();
  });

  test('受保护请求遇到 401 时只刷新一次并安全重放原请求', () async {
    final protectedRequests = <http.Request>[];
    var refreshCount = 0;
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') return _loginResponse();
      if (request.url.path == '/v1/auth/refresh') {
        refreshCount++;
        return _jsonResponse({
          'data': {
            'accessToken': 'new-access-token',
            'refreshToken': 'new-refresh-token',
          },
        });
      }
      if (request.url.path == '/v1/friend-requests') {
        protectedRequests.add(request);
        if (protectedRequests.length == 1) return _unauthorized();
        return http.Response('', 204);
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client);
    await repository.login('13800138000', '123456');

    await repository.sendFriendRequest('friend-1', '你好');

    expect(refreshCount, 1);
    expect(protectedRequests, hasLength(2));
    expect(
      protectedRequests.map((request) => request.body).toSet(),
      hasLength(1),
    );
    expect(
      protectedRequests.first.headers['authorization'],
      'Bearer access-token',
    );
    expect(
      protectedRequests.last.headers['authorization'],
      'Bearer new-access-token',
    );
    await repository.close();
  });

  test('并发 401 共用同一个刷新请求', () async {
    var refreshCount = 0;
    var protectedCount = 0;
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') return _loginResponse();
      if (request.url.path == '/v1/auth/refresh') {
        refreshCount++;
        await Future<void>.delayed(const Duration(milliseconds: 20));
        return _jsonResponse({
          'data': {
            'accessToken': 'new-access-token',
            'refreshToken': 'refresh-token',
          },
        });
      }
      if (request.url.path == '/v1/conversations' ||
          request.url.path == '/v1/friends') {
        protectedCount++;
        if (request.headers['authorization'] == 'Bearer access-token') {
          return _unauthorized();
        }
        return _jsonResponse({
          'data': {'items': <Object?>[]},
        });
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client);
    await repository.login('13800138000', '123456');

    await Future.wait([repository.conversations(), repository.contacts()]);

    expect(refreshCount, 1);
    expect(protectedCount, 4);
    await repository.close();
  });

  test('刷新凭据失败时 fail-closed 且不重放受保护请求', () async {
    var protectedCount = 0;
    var refreshCount = 0;
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') return _loginResponse();
      if (request.url.path == '/v1/auth/refresh') {
        refreshCount++;
        return http.Response(
          jsonEncode({
            'error': {'code': 'INVALID_REFRESH', 'message': 'invalid'},
          }),
          401,
        );
      }
      if (request.url.path == '/v1/conversations') {
        protectedCount++;
        return _unauthorized();
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client);
    await repository.login('13800138000', '123456');

    await expectLater(
      repository.conversations(),
      throwsA(isA<ImApiException>()),
    );

    expect(refreshCount, 1);
    expect(protectedCount, 1);
    await repository.close();
  });

  test('引用消息发送 replyToId 且历史刷新后恢复引用正文', () async {
    http.Request? sendRequest;
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') return _loginResponse();
      if (request.url.path == '/v1/conversations/c1/messages' &&
          request.method == 'POST') {
        sendRequest = request;
        return _jsonResponse({
          'data': {
            'message': _messageJson(
              id: 'reply-1',
              text: '收到',
              replyToId: 'origin-1',
            ),
          },
        });
      }
      if (request.url.path == '/v1/conversations/c1/messages') {
        return _jsonResponse({
          'data': {
            'items': [
              _messageJson(id: 'origin-1', text: '原始消息'),
              _messageJson(id: 'reply-1', text: '收到', replyToId: 'origin-1'),
            ],
          },
        });
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client);
    await repository.login('13800138000', '123456');
    final pending = ChatMessage(
      id: 'local-client-1',
      clientMessageId: 'client-1',
      conversationId: 'c1',
      senderId: 'user-1',
      senderName: '测试用户',
      text: '收到',
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.reply,
      replyToId: 'origin-1',
      replyToText: '原始消息',
    );

    final sent = await repository.send(pending);
    final history = await repository.messages('c1');

    expect(jsonDecode(sendRequest!.body)['replyToId'], 'origin-1');
    expect(sent.replyToId, 'origin-1');
    expect(sent.replyToText, '原始消息');
    expect(history.last.replyToId, 'origin-1');
    expect(history.last.replyToText, '原始消息');
    await repository.close();
  });

  test('会话草稿加密保存、恢复并在清空后删除', () async {
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') return _loginResponse();
      return http.Response('{}', 404);
    });
    final repository = _repository(client);
    await repository.login('13800138000', '123456');

    await repository.saveDraft('conversation-1', '尚未发送的私密内容');
    expect(await repository.readDraft('conversation-1'), '尚未发送的私密内容');
    final preferences = await SharedPreferences.getInstance();
    final encrypted = preferences.getString(
      'nexachat.secure.v1.draft.conversation-1',
    );
    expect(encrypted, isNotNull);
    expect(encrypted, isNot(contains('尚未发送的私密内容')));

    await repository.saveDraft('conversation-1', '');
    expect(await repository.readDraft('conversation-1'), isEmpty);
    await repository.close();
  });

  test('收藏服务端消息后同步到服务端并保留本地副本', () async {
    final favoriteRequests = <http.Request>[];
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') return _loginResponse();
      if (request.url.path == '/v1/users/me/favorites/message-1') {
        favoriteRequests.add(request);
        return http.Response('', 204);
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client);
    await repository.login('13800138000', '123456');
    final message = ChatMessage(
      id: 'message-1',
      clientMessageId: 'client-message-1',
      conversationId: 'conversation-1',
      senderId: 'user-1',
      senderName: '测试用户',
      text: '值得收藏的消息',
      sentAt: DateTime.now(),
      isMine: true,
    );

    await repository.saveFavorite(message);

    expect(favoriteRequests.single.method, 'PUT');
    final cached = await SharedPreferences.getInstance();
    final encrypted = cached.getString('nexachat.secure.v1.favorites');
    expect(encrypted, isNotNull);
    expect(encrypted, isNot(contains('值得收藏的消息')));

    await repository.removeFavorite(message);

    expect(favoriteRequests.map((request) => request.method), [
      'PUT',
      'DELETE',
    ]);
    expect(
      cached.getString('nexachat.secure.v1.favorites'),
      isNot(contains('值得收藏的消息')),
    );
    await repository.close();
  });

  test('媒体发送依次完成预签名、PUT、确认与消息落库', () async {
    final paths = <String>[];
    final client = MockClient((request) async {
      paths.add('${request.method} ${request.url.path}');
      if (request.url.path == '/v1/auth/login') return _loginResponse();
      if (request.url.path == '/v1/media/presign') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        expect(body['fileName'], 'photo.png');
        expect(body['size'], 4);
        return _jsonResponse({
          'data': {
            'mediaId': 'media-1',
            'uploadUrl': 'https://upload.example.com/object-1',
            'headers': {'content-type': 'image/png'},
          },
        });
      }
      if (request.url.host == 'upload.example.com') {
        expect(request.bodyBytes, Uint8List.fromList([1, 2, 3, 4]));
        return http.Response('', 200);
      }
      if (request.url.path == '/v1/media/media-1/complete') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        expect((body['checksum'] as String).length, 64);
        return _jsonResponse({'data': <String, Object?>{}});
      }
      if (request.url.path == '/v1/conversations/c1/messages') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        expect(body['type'], 'image');
        expect((body['body'] as Map<String, Object?>)['mediaId'], 'media-1');
        return _jsonResponse({
          'data': {
            'message': _messageJson(
              id: 'image-1',
              text: '',
              type: 'image',
              body: {
                'type': 'image',
                'mediaId': 'media-1',
                'fileName': 'photo.png',
                'mime': 'image/png',
              },
            ),
          },
        });
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client);
    await repository.login('13800138000', '123456');
    final pending = ChatMessage(
      id: 'local-media-client',
      clientMessageId: 'media-client',
      conversationId: 'c1',
      senderId: 'user-1',
      senderName: '测试用户',
      text: '[图片]',
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.image,
    );

    final progress = <double>[];
    final sent = await repository.sendMedia(
      pending,
      MediaUpload(
        bytes: Uint8List.fromList([1, 2, 3, 4]),
        fileName: 'photo.png',
        mimeType: 'image/png',
        kind: MessageContentKind.image,
        localPath: '/tmp/photo.png',
      ),
      onProgress: progress.add,
    );

    expect(paths.skip(1), [
      'POST /v1/media/presign',
      'PUT /object-1',
      'POST /v1/media/media-1/complete',
      'POST /v1/conversations/c1/messages',
    ]);
    expect(sent.mediaId, 'media-1');
    expect(sent.mediaUrl, '/tmp/photo.png');
    expect(progress.first, 0);
    expect(progress.last, 1);
    await repository.close();
  });

  test('WebSocket token 失效后刷新凭据并自动重连', () async {
    var socketConnections = 0;
    var refreshCount = 0;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final serverSubscription = server.listen((request) async {
      final socket = await WebSocketTransformer.upgrade(request);
      socketConnections++;
      if (socketConnections == 1) {
        await Future<void>.delayed(const Duration(milliseconds: 10));
        socket.add(
          jsonEncode({
            'type': 'error',
            'error': {'code': 'TOKEN_EXPIRED'},
          }),
        );
      } else {
        socket.add(jsonEncode({'type': 'session.ready'}));
      }
    });
    final client = MockClient((request) async {
      if (request.url.path == '/v1/auth/login') return _loginResponse();
      if (request.url.path == '/v1/ws/ticket') {
        return _jsonResponse({
          'data': {'ticket': 'ws-ticket-${request.headers['authorization']}'},
        }, 201);
      }
      if (request.url.path == '/v1/auth/refresh') {
        refreshCount++;
        return _jsonResponse({
          'data': {
            'accessToken': 'new-access-token',
            'refreshToken': 'refresh-token',
          },
        });
      }
      if (request.url.path == '/v1/sync') {
        return _jsonResponse({
          'data': {'events': <Object?>[], 'cursor': 0, 'hasMore': false},
        });
      }
      return http.Response('{}', 404);
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example.com',
      wsUrl: 'ws://127.0.0.1:${server.port}/ws',
    );
    await repository.login('13800138000', '123456');

    await repository.connect();
    final deadline = DateTime.now().add(const Duration(seconds: 2));
    while (socketConnections < 2 && DateTime.now().isBefore(deadline)) {
      await Future<void>.delayed(const Duration(milliseconds: 10));
    }

    expect(refreshCount, 1);
    expect(socketConnections, 2);
    await repository.close();
    await serverSubscription.cancel();
    await server.close(force: true);
  });

  test('群资料更新只提交受控的头像媒体 ID', () async {
    late Map<String, Object?> requestBody;
    final client = MockClient((request) async {
      expect(request.method, 'PATCH');
      expect(request.url.path, '/v1/groups/group-1');
      requestBody = (jsonDecode(request.body) as Map).cast<String, Object?>();
      return _jsonResponse({
        'data': {
          'conversationId': 'group-1',
          'ownerId': 'user-1',
          'name': '邻里群',
          'avatarUrl': '/v1/avatars/media-group?expires=1&signature=signed',
          'joinPolicy': 'invite',
          'allowMemberAddFriend': true,
          'updatedAt': '2026-07-31T12:00:00Z',
        },
      });
    });
    final repository = _repository(client);

    final profile = await repository.updateGroupProfile(
      'group-1',
      avatarMediaId: 'media-group',
    );

    expect(requestBody, {'avatarMediaId': 'media-group'});
    expect(profile.avatarUrl, contains('/v1/avatars/media-group'));
    await repository.close();
  });
}

LiveImRepository _repository(http.Client client) => LiveImRepository(
  client: client,
  apiBaseUrl: 'https://api.example.com',
  wsUrl: 'wss://api.example.com/ws',
);

http.Response _loginResponse() => _jsonResponse({
  'data': {
    'accessToken': 'access-token',
    'refreshToken': 'refresh-token',
    'user': {'id': 'user-1', 'name': '测试用户', 'phone': '13800138000'},
  },
});

http.Response _unauthorized() => http.Response(
  jsonEncode({
    'error': {'code': 'UNAUTHORIZED', 'message': 'expired'},
  }),
  401,
  headers: {'content-type': 'application/json'},
);

http.Response _jsonResponse(Map<String, Object?> body, [int status = 200]) =>
    http.Response(
      jsonEncode(body),
      status,
      headers: {'content-type': 'application/json'},
    );

Map<String, Object?> _messageJson({
  required String id,
  required String text,
  String type = 'text',
  String? replyToId,
  Map<String, Object?>? body,
}) {
  final result = <String, Object?>{
    'id': id,
    'clientMsgId': 'client-$id',
    'conversationId': 'c1',
    'senderId': 'user-1',
    'type': type,
    'body': body ?? {'text': text},
    'createdAt': '2026-07-31T12:00:00Z',
    'conversationSeq': id == 'origin-1' ? 1 : 2,
  };
  if (replyToId != null) result['replyToId'] = replyToId;
  return result;
}
