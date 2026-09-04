import 'dart:convert';

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

  test('文本发送携带结构化 mentions 且不降级为纯显示文本', () async {
    final gateway = FakeWukongGateway();
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/channels/conversations') {
          return _jsonResponse({
            'data': {
              'items': [
                {
                  'conversation': {
                    'id': 'group-1',
                    'type': 'group',
                    'title': '群聊',
                    'updatedAt': '2026-08-11T00:00:00Z',
                  },
                  'members': <Object?>[],
                },
              ],
            },
          });
        }
        if (request.url.path == '/v2/im/datasource/conversations') {
          return _jsonResponse({
            'data': {'items': <Object?>[]},
          });
        }
        return http.Response('{}', 404);
      }),
      gateway: gateway,
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

    final payload = gateway.sentMessages.single.payload;
    expect(payload['type'], 1);
    expect(payload['content'], '@所有人 @林安 请查看公告');
    expect(payload['mention'], {
      'all': 1,
      'uids': ['friend-1'],
    });
    expect(message.mentions.first.isEveryone, isTrue);
    expect(message.mentions.last.userId, 'friend-1');
    await repository.close();
  });

  test('编辑、编辑记录和回应以服务端返回状态为准', () async {
    final requests = <http.Request>[];
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        requests.add(request);
        if (request.url.path == '/v2/messages/message-1') {
          final requestBody = jsonDecode(request.body) as Map<String, Object?>;
          return _jsonResponse({
            'data': {
              'message': _message(
                id: 'message-1',
                body: {'text': requestBody['text']},
                editedAt: '2026-08-01T08:30:00Z',
              ),
            },
          });
        }
        if (request.url.path == '/v2/messages/message-1/edits') {
          return _jsonResponse({
            'data': {
              'items': [
                {
                  'messageId': 'message-1',
                  'version': 0,
                  'editorId': 'user-1',
                  'body': {'text': '原始消息'},
                  'editedAt': '2026-08-01T08:00:00Z',
                },
                {
                  'messageId': 'message-1',
                  'version': 1,
                  'editorId': 'user-1',
                  'body': {'text': '修改后的消息'},
                  'editedAt': '2026-08-01T08:30:00Z',
                },
                {
                  'messageId': 'message-1',
                  'version': 2,
                  'editorId': 'user-1',
                  'body': {'text': '第二次修改'},
                  'editedAt': '2026-08-01T08:31:00Z',
                },
              ],
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
    final editedAgain = await repository.editMessage('message-1', '第二次修改');
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
    final history = await repository.messageEditHistory('message-1');

    expect(requests[0].method, 'PATCH');
    final firstEditBody = jsonDecode(requests[0].body) as Map<String, Object?>;
    final secondEditBody = jsonDecode(requests[1].body) as Map<String, Object?>;
    expect(firstEditBody['text'], '修改后的消息');
    expect(secondEditBody['text'], '第二次修改');
    expect(firstEditBody['editId'], isNotEmpty);
    expect(secondEditBody['editId'], isNotEmpty);
    expect(secondEditBody['editId'], isNot(firstEditBody['editId']));
    expect(edited.editedAt, DateTime.parse('2026-08-01T08:30:00Z').toLocal());
    expect(editedAgain.text, '第二次修改');
    expect(requests[2].method, 'PUT');
    expect(requests[3].method, 'DELETE');
    expect(added.reactions.single.reactedByMe, isTrue);
    expect(added.reactions.single.count, 2);
    expect(removed.reactions.single.reactedByMe, isFalse);
    expect(requests[4].method, 'GET');
    expect(requests[4].url.path, '/v2/messages/message-1/edits');
    expect(history.map((item) => item.version), [0, 1, 2]);
    expect(history.first.isOriginal, isTrue);
    expect(history.last.text, '第二次修改');
    expect(
      history.last.editedAt,
      DateTime.parse('2026-08-01T08:31:00Z').toLocal(),
    );
    await repository.close();
  });

  test('群消息置顶列表与会话搜索使用独立真实接口', () async {
    final paths = <String>[];
    final repository = _repository(
      MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        paths.add('${request.method} ${request.url.path}');
        if (request.url.path == '/v2/messages/pins' &&
            request.method == 'GET') {
          expect(request.url.queryParameters['conversationId'], 'group-1');
          return _jsonResponse({
            'data': {
              'items': [
                {
                  'conversationId': 'group-1',
                  'message': _message(
                    id: 'pinned-1',
                    conversationId: 'group-1',
                    body: {'text': '群规'},
                  ),
                  'pinnedBy': 'owner-1',
                  'pinnedAt': '2026-08-01T08:00:00Z',
                },
              ],
            },
          });
        }
        if (request.url.path == '/v2/messages/pins/pinned-1') {
          expect(request.url.queryParameters['conversationId'], 'group-1');
          return http.Response('', 204);
        }
        if (request.url.path == '/v2/messages/search') {
          expect(request.url.queryParameters['conversationId'], 'group-1');
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
    expect(pinned.single.text, '群规');
    expect(pinned.single.pinnedBy, 'owner-1');
    expect(
      pinned.single.pinnedAt,
      DateTime.parse('2026-08-01T08:00:00Z').toLocal(),
    );
    expect(results.single.id, 'pinned-1');
    expect(paths, [
      'GET /v2/messages/pins',
      'DELETE /v2/messages/pins/pinned-1',
      'GET /v2/messages/search',
    ]);
    await repository.close();
  });
}

LiveImRepository _repository(
  http.Client client, {
  FakeWukongGateway? gateway,
}) => LiveImRepository(
  client: client,
  apiBaseUrl: 'https://api.example.com',
  wukongGateway: gateway ?? FakeWukongGateway(),
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
    'imSession': {
      'uid': 'user-1',
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
