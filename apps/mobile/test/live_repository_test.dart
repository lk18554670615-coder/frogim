import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/fake_wukong_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('退出登录向服务端撤销 refresh token 后清理本地会话', () async {
    final requests = <http.Request>[];
    final client = MockClient((request) async {
      requests.add(request);
      if (request.url.path == '/v2/auth/login') {
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
      if (request.url.path == '/v2/auth/logout') {
        return http.Response('', 204);
      }
      return http.Response('{}', 404);
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example.com',
    );

    await repository.login('13800138000', '123456');
    await repository.logout();

    final logoutRequest = requests.singleWhere(
      (request) => request.url.path == '/v2/auth/logout',
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
      if (request.url.path == '/v2/auth/login') {
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
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/auth/refresh') {
        refreshCount++;
        return _jsonResponse({
          'data': {
            'accessToken': 'new-access-token',
            'refreshToken': 'new-refresh-token',
          },
        });
      }
      if (request.url.path == '/v2/contacts/requests') {
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
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/auth/refresh') {
        refreshCount++;
        await Future<void>.delayed(const Duration(milliseconds: 20));
        return _jsonResponse({
          'data': {
            'accessToken': 'new-access-token',
            'refreshToken': 'refresh-token',
          },
        });
      }
      if (request.url.path == '/v2/channels/conversations' ||
          request.url.path == '/v2/contacts/friends') {
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
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/auth/refresh') {
        refreshCount++;
        return http.Response(
          jsonEncode({
            'error': {'code': 'INVALID_REFRESH', 'message': 'invalid'},
          }),
          401,
        );
      }
      if (request.url.path == '/v2/channels/conversations') {
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

  test(
    'syncNow does not recursively invalidate the conversation list',
    () async {
      final gateway = FakeWukongGateway();
      final client = MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        return http.Response('{}', 404);
      });
      final repository = _repository(client, gateway: gateway);
      await repository.login('13800138000', '123456');
      await repository.connect();
      final events = <ImEvent>[];
      final subscription = repository.events.listen(events.add);

      await repository.syncNow();
      await Future<void>.delayed(const Duration(milliseconds: 20));

      expect(events, isEmpty);
      await subscription.cancel();
      await repository.close();
    },
  );

  test('引用消息发送 replyToId 且历史刷新后恢复引用正文', () async {
    final gateway = FakeWukongGateway();
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/channels/conversations') {
        return _conversationResponse();
      }
      if (request.url.path == '/v2/im/datasource/conversations') {
        return _jsonResponse({
          'data': {'items': <Object?>[]},
        });
      }
      if (request.url.path == '/v2/im/datasource/messages') {
        return _jsonResponse({
          'data': {
            'messages': [
              _syncedMessage(id: 'origin-1', sequence: 1, content: '原始消息'),
              _syncedMessage(
                id: 'reply-1',
                sequence: 2,
                content: '收到',
                replyToId: 'origin-1',
              ),
            ],
          },
        });
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client, gateway: gateway);
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

    expect(
      (gateway.sentMessages.single.payload['reply'] as Map)['message_id'],
      'origin-1',
    );
    expect(sent.replyToId, 'origin-1');
    expect(sent.replyToText, '原始消息');
    expect(history.last.replyToId, 'origin-1');
    expect(history.last.replyToText, '原始消息');
    await repository.close();
  });

  test('WuKong SDK 本机插入不重复上屏且 SENDACK 关联回页面 clientMessageId', () async {
    final gateway = FakeWukongGateway()
      ..autoAcknowledge = false
      ..generateClientMsgNo = true
      ..initialSendState = WukongMessageState.sending;
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/channels/conversations') {
        return _conversationResponse();
      }
      if (request.url.path == '/v2/im/datasource/conversations') {
        return _jsonResponse({
          'data': {'items': <Object?>[]},
        });
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client, gateway: gateway);
    final emitted = <ImEvent>[];
    final subscription = repository.events.listen(emitted.add);
    await repository.login('13800138000', '123456');
    final pending = ChatMessage(
      id: 'local-page-client',
      clientMessageId: 'page-client',
      conversationId: 'c1',
      senderId: 'user-1',
      senderName: '我',
      text: '等待 ACK',
      sentAt: DateTime.now(),
      isMine: true,
      status: MessageStatus.sending,
    );

    final sending = await repository.send(pending);
    await Future<void>.delayed(Duration.zero);

    expect(sending.clientMessageId, 'page-client');
    expect(sending.status, MessageStatus.sending);
    expect(
      emitted.where((event) => event.type == ImEventType.messageCreated),
      isEmpty,
    );

    final ackEvent = repository.events.firstWhere(
      (event) =>
          event.type == ImEventType.messageChanged &&
          event.payload['message'] != null,
    );
    gateway.emitSendResult(
      clientMsgNo: '',
      clientSeq: 1,
      messageId: 'authoritative-message-42',
      messageSeq: 42,
      reasonCode: 1,
    );
    final event = await ackEvent.timeout(const Duration(seconds: 1));
    final acknowledged = ChatMessage.fromJson(
      event.payload['message']! as Map<String, Object?>,
    );

    expect(acknowledged.id, 'authoritative-message-42');
    expect(acknowledged.clientMessageId, 'page-client');
    expect(acknowledged.conversationSeq, 42);
    expect(acknowledged.status, MessageStatus.sent);

    await subscription.cancel();
    await repository.close();
  });

  test('会话草稿加密保存、恢复并在清空后删除', () async {
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') return _loginResponse();
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
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/messages/favorites/message-1') {
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
    final gateway = FakeWukongGateway()
      ..autoAcknowledge = false
      ..initialSendState = WukongMessageState.sending;
    final client = MockClient((request) async {
      paths.add('${request.method} ${request.url.path}');
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/media/presign') {
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
      if (request.url.path == '/v2/media/media-1/complete') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        expect((body['checksum'] as String).length, 64);
        return _jsonResponse({'data': <String, Object?>{}});
      }
      if (request.url.path == '/v2/channels/conversations') {
        return _conversationResponse();
      }
      if (request.url.path == '/v2/im/datasource/conversations') {
        return _jsonResponse({
          'data': {'items': <Object?>[]},
        });
      }
      if (request.url.path == '/v2/media/media-1/bind') {
        expect(jsonDecode(request.body), {
          'channelId': 'user-2',
          'channelType': 1,
        });
        return _jsonResponse({
          'data': {
            'mediaId': 'media-1',
            'url': 'https://cdn.example.com/media-1',
          },
        });
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client, gateway: gateway);
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

    expect(
      paths.skip(1),
      containsAllInOrder([
        'POST /v2/media/presign',
        'PUT /object-1',
        'POST /v2/media/media-1/complete',
        'GET /v2/channels/conversations',
        'POST /v2/im/datasource/conversations',
        'POST /v2/media/media-1/bind',
      ]),
    );
    expect(paths, isNot(contains('POST /v2/messages/conversations/c1/send')));
    expect(gateway.sentMessages.single.payload['mediaId'], 'media-1');
    expect(gateway.sentMessages.single.payload['type'], 2);
    expect(sent.mediaId, 'media-1');
    expect(sent.mediaUrl, '/tmp/photo.png');
    expect(sent.status, MessageStatus.sending);
    expect(progress.first, 0);
    expect(progress.last, 1);

    final ackEvent = repository.events.firstWhere(
      (event) =>
          event.type == ImEventType.messageChanged &&
          event.payload['message'] != null,
    );
    gateway.emitSendResult(
      clientMsgNo: '',
      clientSeq: 1,
      messageId: 'media-message-42',
      messageSeq: 42,
      reasonCode: 1,
    );
    final acknowledged = ChatMessage.fromJson(
      (await ackEvent.timeout(const Duration(seconds: 1))).payload['message']!
          as Map<String, Object?>,
    );
    expect(acknowledged.id, 'media-message-42');
    expect(acknowledged.clientMessageId, 'media-client');
    expect(acknowledged.status, MessageStatus.sent);
    expect(acknowledged.kind, MessageContentKind.image);
    expect(acknowledged.mediaId, 'media-1');
    expect(acknowledged.mimeType, 'image/png');
    await repository.close();
  });

  test('WuKong 网关连接和收消息映射到现有页面事件边界', () async {
    final gateway = FakeWukongGateway();
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/channels/conversations') {
        return _conversationResponse();
      }
      if (request.url.path == '/v2/im/datasource/conversations') {
        return _jsonResponse({
          'data': {'items': <Object?>[]},
        });
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client, gateway: gateway);
    await repository.login('13800138000', '123456');
    await repository.conversations();
    final connected = repository.connectionChanges.firstWhere((value) => value);
    await repository.connect();
    await connected;

    final received = repository.events.firstWhere(
      (event) => event.type == ImEventType.messageCreated,
    );
    gateway.emit(
      WukongGatewayEvent(
        kind: WukongGatewayEventKind.received,
        channel: const WukongChannel(id: 'user-2', type: 1),
        message: WukongMessage(
          messageId: 'wk-incoming-1',
          messageSeq: 9,
          clientMsgNo: 'incoming-client-1',
          clientSeq: 1,
          fromUid: 'user-2',
          channel: const WukongChannel(id: 'user-2', type: 1),
          timestamp: DateTime.utc(2026, 8, 11),
          payload: const {'type': 1, 'content': 'WuKong 收到'},
          state: WukongMessageState.sent,
          reasonCode: 1,
        ),
      ),
    );

    final event = await received;
    final message = ChatMessage.fromJson(
      event.payload['message']! as Map<String, Object?>,
    );
    expect(message.conversationId, 'c1');
    expect(message.text, 'WuKong 收到');
    expect(message.conversationSeq, 9);
    expect(message.isMine, isFalse);
    await repository.close();
  });

  test(
    'WuKong stream event is ordered behind its anchor and updates text',
    () async {
      final gateway = FakeWukongGateway();
      final client = MockClient((request) async {
        if (request.url.path == '/v2/auth/login') return _loginResponse();
        if (request.url.path == '/v2/channels/conversations') {
          return _conversationResponse();
        }
        if (request.url.path == '/v2/im/datasource/conversations') {
          return _jsonResponse({
            'data': {'items': <Object?>[]},
          });
        }
        return http.Response('{}', 404);
      });
      final repository = _repository(client, gateway: gateway);
      await repository.login('13800138000', '123456');
      await repository.conversations();
      await repository.connect();
      final changed = repository.events
          .where((event) => event.type == ImEventType.messageChanged)
          .take(4)
          .toList();

      gateway.emit(
        const WukongGatewayEvent(
          kind: WukongGatewayEventKind.messageEvent,
          data: {
            'id': 'stream-event-1',
            'type': 'stream.delta',
            'timestamp': 1786406400000,
            'data': {
              'client_msg_no': 'stream-client-1',
              'channel_id': 'user-1',
              'channel_type': 1,
              'from_uid': 'user-2',
              'message_id': 9001,
              'event_key': 'main',
              'msg_event_seq': 0,
              'stream_status': 'open',
              'payload': {'kind': 'text', 'delta': '实时内容'},
            },
          },
        ),
      );
      await Future<void>.delayed(Duration.zero);
      gateway.emit(
        const WukongGatewayEvent(
          kind: WukongGatewayEventKind.messageEvent,
          data: {
            'id': 'stream-event-2',
            'type': 'stream.delta',
            'timestamp': 1786406400001,
            'data': {
              'client_msg_no': 'stream-client-1',
              'channel_id': 'user-1',
              'channel_type': 1,
              'from_uid': 'user-2',
              'message_id': 9001,
              'event_key': 'main',
              'msg_event_seq': 0,
              'stream_status': 'open',
              'payload': {'kind': 'text', 'delta': '补充'},
            },
          },
        ),
      );
      await Future<void>.delayed(Duration.zero);
      gateway.emit(
        const WukongGatewayEvent(
          kind: WukongGatewayEventKind.messageEvent,
          data: {
            'id': 'stream-event-3',
            'type': 'stream.snapshot',
            'timestamp': 1786406400002,
            'data': {
              'client_msg_no': 'stream-client-1',
              'channel_id': 'user-1',
              'channel_type': 1,
              'from_uid': 'user-2',
              'message_id': 9001,
              'event_key': 'main',
              'msg_event_seq': 1,
              'stream_status': 'open',
              'payload': {'kind': 'text', 'text': '权威快照'},
            },
          },
        ),
      );
      await Future<void>.delayed(Duration.zero);
      gateway.emit(
        const WukongGatewayEvent(
          kind: WukongGatewayEventKind.messageEvent,
          data: {
            'id': 'stream-event-4',
            'type': 'stream.delta',
            'timestamp': 1786406400003,
            'data': {
              'client_msg_no': 'stream-client-1',
              'channel_id': 'user-1',
              'channel_type': 1,
              'from_uid': 'user-2',
              'message_id': 9001,
              'event_key': 'main',
              'msg_event_seq': 1,
              'stream_status': 'open',
              'payload': {'kind': 'text', 'delta': '后续'},
            },
          },
        ),
      );
      await Future<void>.delayed(Duration.zero);
      gateway.emit(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.received,
          message: WukongMessage(
            messageId: '9001',
            messageSeq: 9,
            clientMsgNo: 'stream-client-1',
            clientSeq: 0,
            fromUid: 'user-2',
            channel: const WukongChannel(id: 'user-2', type: 1),
            timestamp: DateTime.utc(2026, 8, 11),
            payload: const {'type': 1, 'content': '正在生成…'},
            state: WukongMessageState.sent,
            isStreaming: true,
          ),
        ),
      );

      final events = await changed;
      final messages = events
          .map(
            (event) => ChatMessage.fromJson(
              event.payload['message']! as Map<String, Object?>,
            ),
          )
          .toList();
      final message = messages.last;
      expect(message.id, '9001');
      expect(message.conversationId, 'c1');
      expect(messages.map((item) => item.text).toList(), [
        '实时内容',
        '实时内容补充',
        '权威快照',
        '权威快照后续',
      ]);
      await repository.close();
    },
  );

  test('WuKong CMD 类型 99 严格映射为业务通话类型 1005', () async {
    final gateway = FakeWukongGateway();
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      return http.Response('{}', 404);
    });
    final repository = _repository(client, gateway: gateway);
    await repository.login('13800138000', '123456');
    await repository.connect();
    final eventFuture = repository.callEvents.first;
    gateway.emit(
      const WukongGatewayEvent(
        kind: WukongGatewayEventKind.command,
        data: {
          'type': 99,
          'cmd': 'call.accepted',
          'param': {
            'schemaVersion': 1,
            'contentType': 1005,
            'event': 'call.accepted',
            'callId': 'call-1',
            'conversationId': 'c1',
          },
        },
      ),
    );
    final event = await eventFuture;
    expect(event.type, 'call.accepted');
    expect(event.payload['callId'], 'call-1');

    var invalidDelivered = false;
    final invalid = repository.callEvents.listen(
      (_) => invalidDelivered = true,
    );
    gateway.emit(
      const WukongGatewayEvent(
        kind: WukongGatewayEventKind.command,
        data: {
          'cmd': 'call.ended',
          'param': {'schemaVersion': 1, 'contentType': 1002},
        },
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(invalidDelivered, isFalse);
    await invalid.cancel();
    await repository.close();
  });

  test('WuKong 业务 CMD 校验版本并映射服务端真实事件名', () async {
    final gateway = FakeWukongGateway();
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      return http.Response('{}', 404);
    });
    final repository = _repository(client, gateway: gateway);
    await repository.login('13800138000', '123456');
    await repository.connect();
    final emitted = <ImEvent>[];
    final subscription = repository.events.listen(emitted.add);

    gateway.emit(
      const WukongGatewayEvent(
        kind: WukongGatewayEventKind.command,
        data: {
          'type': 99,
          'cmd': 'message.reaction.updated',
          'param': {
            'schemaVersion': 1,
            'event': 'message.reaction.updated',
            'payload': {'conversationId': 'c1', 'messageId': 'msg-1'},
          },
        },
      ),
    );
    gateway.emit(
      const WukongGatewayEvent(
        kind: WukongGatewayEventKind.command,
        data: {
          'cmd': 'scheduled.sent',
          'param': {
            'schemaVersion': 1,
            'event': 'scheduled.sent',
            'payload': {
              'scheduledMessage': {'conversationId': 'c1'},
            },
          },
        },
      ),
    );
    gateway.emit(
      const WukongGatewayEvent(
        kind: WukongGatewayEventKind.command,
        data: {
          'cmd': 'friend.removed',
          'param': {
            'schemaVersion': 2,
            'event': 'friend.removed',
            'payload': {'userId': 'friend-1'},
          },
        },
      ),
    );
    await Future<void>.delayed(Duration.zero);

    expect(emitted, hasLength(2));
    expect(emitted[0].type, ImEventType.messageChanged);
    expect(emitted[0].payload['messageId'], 'msg-1');
    expect(emitted[1].type, ImEventType.scheduledChanged);
    expect(
      emitted.where((event) => event.type == ImEventType.friendChanged),
      isEmpty,
    );
    await subscription.cancel();
    await repository.close();
  });

  test('群资料更新只提交受控的头像媒体 ID', () async {
    late Map<String, Object?> requestBody;
    final client = MockClient((request) async {
      expect(request.method, 'PATCH');
      expect(request.url.path, '/v2/channels/groups/group-1');
      requestBody = (jsonDecode(request.body) as Map).cast<String, Object?>();
      return _jsonResponse({
        'data': {
          'conversationId': 'group-1',
          'ownerId': 'user-1',
          'name': '邻里群',
          'avatarUrl': '/v2/avatars/media-group?expires=1&signature=signed',
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
    expect(profile.avatarUrl, contains('/v2/avatars/media-group'));
    await repository.close();
  });

  test('标记已读同时清除 WuKong SDK 本地会话红点', () async {
    final gateway = FakeWukongGateway();
    Map<String, Object?>? readBody;
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/channels/conversations') {
        return _conversationResponse();
      }
      if (request.url.path == '/v2/im/datasource/conversations') {
        return _jsonResponse({
          'data': {'items': <Object?>[]},
        });
      }
      if (request.url.path == '/v2/channels/conversations/c1/read') {
        readBody = (jsonDecode(request.body) as Map).cast<String, Object?>();
        return _jsonResponse({
          'data': {'seq': 9},
        });
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client, gateway: gateway);
    await repository.login('13800138000', '123456');
    await repository.conversations();

    await repository.markRead('c1', 9);

    expect(readBody, {'seq': 9});
    expect(gateway.readChannels, hasLength(1));
    expect(gateway.readChannels.single.id, 'user-2');
    expect(gateway.readChannels.single.type, 1);
    await repository.close();
  });

  test('正在输入状态使用受鉴权业务接口且显式发送开始和结束', () async {
    final requests = <http.Request>[];
    final client = MockClient((request) async {
      requests.add(request);
      return http.Response('', 204);
    });
    final repository = _repository(client);

    await repository.setTyping('conversation-1', true);
    await repository.setTyping('conversation-1', false);

    expect(
      requests.map((request) => '${request.method} ${request.url.path}'),
      everyElement('POST /v2/channels/conversations/conversation-1/typing'),
    );
    expect(
      requests
          .map((request) => jsonDecode(request.body) as Map<String, Object?>)
          .toList(),
      [
        {'typing': true},
        {'typing': false},
      ],
    );
    await repository.close();
  });

  test('客服与扩展频道保留服务端 WuKong channel type', () async {
    final gateway = FakeWukongGateway();
    final client = MockClient((request) async {
      if (request.url.path == '/v2/auth/login') return _loginResponse();
      if (request.url.path == '/v2/channels/conversations') {
        return _jsonResponse({
          'data': {
            'items': [
              {
                'conversation': {
                  'id': 'user-1',
                  'type': 'visitor',
                  'title': '在线客服 · 售后',
                  'updatedAt': '2026-08-11T00:00:00Z',
                },
                'members': [
                  {'id': 'user-1', 'name': '访客'},
                  {'id': 'agent-1', 'name': '客服'},
                ],
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
      if (request.url.path == '/v2/channels/conversations/user-1/read') {
        return http.Response('', 204);
      }
      return http.Response('{}', 404);
    });
    final repository = _repository(client, gateway: gateway);
    await repository.login('13800138000', '123456');

    final conversations = await repository.conversations();
    await repository.markRead('user-1', 3);

    expect(conversations.single.channelId, 'user-1');
    expect(conversations.single.channelType, 10);
    expect(conversations.single.isBusinessChannel, isTrue);
    expect(gateway.readChannels.single.id, 'user-1');
    expect(gateway.readChannels.single.type, 10);
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
    'user': {'id': 'user-1', 'name': '测试用户', 'phone': '13800138000'},
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

http.Response _conversationResponse() => _jsonResponse({
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
          {'id': 'user-1', 'name': 'Me'},
          {'id': 'user-2', 'name': 'Friend'},
        ],
      },
    ],
  },
});

Map<String, Object?> _syncedMessage({
  required String id,
  required int sequence,
  required String content,
  String? replyToId,
}) => {
  'message_idstr': id,
  'message_seq': sequence,
  'client_msg_no': 'client-$id',
  'client_seq': sequence,
  'from_uid': 'user-1',
  'channel_id': 'user-2',
  'channel_type': 1,
  'timestamp': 1786406400 + sequence,
  'payload': {
    'type': 1,
    'content': content,
    if (replyToId != null) 'reply': {'message_id': replyToId},
  },
};

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
