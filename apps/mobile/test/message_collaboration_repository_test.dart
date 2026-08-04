import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('文本发送携带结构化 mentions 且不降级为纯显示文本', () async {
    http.Request? sentRequest;
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v1/auth/login') return _loginResponse();
        if (request.url.path == '/v1/conversations/group-1/messages') {
          sentRequest = request;
          return _jsonResponse({
            'data': {
              'message': _message(
                id: 'message-1',
                conversationId: 'group-1',
                body: {
                  'text': '@所有人 @林安 请查看公告',
                  'mentions': ['friend-1'],
                  'mentionAll': true,
                },
              ),
            },
          });
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final message = await repository.send(
      ChatMessage(
        id: 'local-1',
        clientMessageId: 'client-1',
        conversationId: 'group-1',
        senderId: 'user-1',
        senderName: '测试用户',
        text: '@所有人 @林安 请查看公告',
        sentAt: DateTime.now(),
        isMine: true,
        mentions: const [
          MessageMention(userId: 'all', name: '所有人'),
          MessageMention(userId: 'friend-1', name: '林安'),
        ],
      ),
    );

    final payload = jsonDecode(sentRequest!.body) as Map<String, Object?>;
    expect(payload['type'], 'text');
    expect(payload['body'], {
      'text': '@所有人 @林安 请查看公告',
      'mentions': ['friend-1'],
      'mentionAll': true,
    });
    expect(message.mentions.first.isEveryone, isTrue);
    expect(message.mentions.last.userId, 'friend-1');
    await repository.close();
  });

  test('编辑和回应以服务端返回消息作为唯一成功状态', () async {
    final requests = <http.Request>[];
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v1/auth/login') return _loginResponse();
        requests.add(request);
        if (request.url.path == '/v1/messages/message-1') {
          return _jsonResponse({
            'data': {
              'message': _message(
                id: 'message-1',
                body: {'text': '修改后的消息'},
                editedAt: '2026-08-01T08:30:00Z',
              ),
            },
          });
        }
        if (request.url.pathSegments.contains('reactions')) {
          return _jsonResponse({
            'data': {
              'message': _message(
                id: 'message-1',
                body: {'text': '修改后的消息'},
                reactions: [
                  {
                    'emoji': '👍',
                    'count': request.method == 'PUT' ? 2 : 1,
                    'reactedByMe': request.method == 'PUT',
                    'userIds': request.method == 'PUT'
                        ? ['friend-1', 'user-1']
                        : ['friend-1'],
                  },
                ],
              ),
            },
          });
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final edited = await repository.editMessage('message-1', '修改后的消息');
    final added = await repository.setMessageReaction(
      'message-1',
      '👍',
      active: true,
    );
    final removed = await repository.setMessageReaction(
      'message-1',
      '👍',
      active: false,
    );

    expect(requests[0].method, 'PATCH');
    expect(jsonDecode(requests[0].body), {'text': '修改后的消息'});
    expect(edited.editedAt, DateTime.parse('2026-08-01T08:30:00Z'));
    expect(requests[1].method, 'PUT');
    expect(requests[2].method, 'DELETE');
    expect(added.reactions.single.reactedByMe, isTrue);
    expect(added.reactions.single.count, 2);
    expect(removed.reactions.single.reactedByMe, isFalse);
    await repository.close();
  });

  test('群消息置顶列表与会话搜索使用独立真实接口', () async {
    final paths = <String>[];
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v1/auth/login') return _loginResponse();
        paths.add('${request.method} ${request.url.path}');
        if (request.url.path == '/v1/conversations/group-1/pinned-messages' &&
            request.method == 'GET') {
          return _jsonResponse({
            'data': {
              'items': [
                _message(
                  id: 'pinned-1',
                  conversationId: 'group-1',
                  body: {'text': '群规'},
                  pinnedAt: '2026-08-01T08:00:00Z',
                ),
              ],
            },
          });
        }
        if (request.url.path ==
            '/v1/conversations/group-1/pinned-messages/pinned-1') {
          return http.Response('', 204);
        }
        if (request.url.path == '/v1/conversations/group-1/messages/search') {
          expect(request.url.queryParameters['q'], '群规');
          expect(request.url.queryParameters['limit'], '20');
          return _jsonResponse({
            'data': {
              'items': [
                _message(
                  id: 'pinned-1',
                  conversationId: 'group-1',
                  body: {'text': '群规'},
                ),
              ],
            },
          });
        }
        return http.Response('{}', 404);
      }),
    );
    await repository.login('13800138000', '123456');

    final pinned = await repository.pinnedMessages('group-1');
    await repository.setMessagePinned('group-1', 'pinned-1', pinned: false);
    final results = await repository.searchMessages('group-1', '群规', limit: 20);

    expect(pinned.single.isPinned, isTrue);
    expect(results.single.id, 'pinned-1');
    expect(paths, [
      'GET /v1/conversations/group-1/pinned-messages',
      'DELETE /v1/conversations/group-1/pinned-messages/pinned-1',
      'GET /v1/conversations/group-1/messages/search',
    ]);
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
    'user': {
      'id': 'user-1',
      'name': '测试用户',
      'handle': 'tester',
      'phone': '13800138000',
    },
  },
});

http.Response _jsonResponse(Map<String, Object?> body, [int status = 200]) =>
    http.Response(
      jsonEncode(body),
      status,
      headers: {'content-type': 'application/json'},
    );

Map<String, Object?> _message({
  required String id,
  String conversationId = 'c1',
  required Map<String, Object?> body,
  String? editedAt,
  String? pinnedAt,
  List<Map<String, Object?>> reactions = const [],
}) => {
  'id': id,
  'clientMsgId': 'client-$id',
  'conversationId': conversationId,
  'senderId': 'user-1',
  'senderName': '测试用户',
  'type': 'text',
  'body': body,
  'createdAt': '2026-08-01T08:00:00Z',
  'conversationSeq': 1,
  'editedAt': ?editedAt,
  'pinnedAt': ?pinnedAt,
  if (reactions.isNotEmpty) 'reactions': reactions,
};
