import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/fake_wukong_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('页面销毁时保存草稿不在锁定的 widget 树内同步通知根组件', () async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    var notificationCount = 0;
    controller.addListener(() => notificationCount += 1);

    await controller.saveDraft('conversation-1', '待发送内容', notify: false);

    expect(notificationCount, 0);
    expect(controller.draftFor('conversation-1'), '待发送内容');
    controller.dispose();
  });

  test('LiveRepository 严格使用归档、送达、定时、过期和链接预览契约', () async {
    final requests = <http.Request>[];
    final gateway = FakeWukongGateway();
    final scheduledAt = DateTime(2026, 8, 2, 9);
    final client = MockClient((request) async {
      requests.add(request);
      if (request.url.path == '/v2/auth/login') {
        return _json({
          'data': {
            'accessToken': 'access-token',
            'refreshToken': 'refresh-token',
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
      if (request.url.path == '/v2/channels/conversations/c1/preferences' ||
          request.url.path == '/v2/channels/conversations/c1/delivered' ||
          (request.url.path == '/v2/messages/scheduled/scheduled-1' &&
              request.method == 'DELETE')) {
        return http.Response('', 204);
      }
      if (request.url.path == '/v2/messages/scheduled' &&
          request.method == 'GET') {
        return _json({
          'data': {
            'items': [_scheduledJson(scheduledAt)],
          },
        });
      }
      if (request.url.path == '/v2/messages/scheduled' &&
          request.method == 'POST') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        return _json({
          'data': {
            'scheduledMessage': {
              ..._scheduledJson(DateTime.parse(body['scheduledAt']! as String)),
              'body': body['body'],
              'expiresInSeconds': body['expiresInSeconds'],
            },
          },
        });
      }
      if (request.url.path == '/v2/messages/scheduled/scheduled-1' &&
          request.method == 'PATCH') {
        final body = jsonDecode(request.body) as Map<String, Object?>;
        return _json({
          'data': {
            'scheduledMessage': {
              ..._scheduledJson(DateTime.parse(body['scheduledAt']! as String)),
              'body': body['body'],
              'expiresInSeconds': body['expiresInSeconds'],
            },
          },
        });
      }
      if (request.url.path == '/v2/link-preview') {
        return _json({
          'data': {
            'url': 'https://example.com/a',
            'title': '服务端标题',
            'description': '服务端摘要',
            'siteName': 'Example',
            'imageUrl': 'https://example.com/cover.png',
          },
        });
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
      return http.Response('{}', 404);
    });
    final repository = LiveImRepository(
      client: client,
      apiBaseUrl: 'https://api.example.com',
      wukongGateway: gateway,
    );

    await repository.login('13800138000', '123456');
    await repository.updateConversationPreferences('c1', archived: true);
    await repository.markDelivered('c1', 42);
    final scheduled = await repository.scheduledMessages('c1');
    final created = await repository.scheduleMessage(
      'c1',
      '稍后提醒我',
      scheduledAt,
      expiresInSeconds: 3600,
    );
    final updatedAt = scheduledAt.add(const Duration(hours: 1));
    final updated = await repository.updateScheduledMessage(
      created.id,
      text: '修改后的提醒',
      scheduledAt: updatedAt,
      expiresInSeconds: 3600,
    );
    await repository.cancelScheduledMessage(created.id);
    final preview = await repository.linkPreview('https://example.com/a');
    await repository.send(
      ChatMessage(
        id: 'local-client-1',
        clientMessageId: 'client-1',
        conversationId: 'c1',
        senderId: 'me',
        senderName: '我',
        text: '限时消息',
        sentAt: DateTime.now(),
        isMine: true,
        expiresAt: DateTime.now().add(const Duration(hours: 1)),
      ),
    );

    expect(scheduled.single.id, 'scheduled-1');
    expect(updated.text, '修改后的提醒');
    expect(updated.scheduledAt, updatedAt);
    expect(preview?.title, '服务端标题');
    final archiveRequest = requests.singleWhere(
      (request) => request.url.path.endsWith('/preferences'),
    );
    expect(jsonDecode(archiveRequest.body), {'archived': true});
    final deliveredRequest = requests.singleWhere(
      (request) => request.url.path.endsWith('/delivered'),
    );
    expect(deliveredRequest.method, 'PUT');
    expect(jsonDecode(deliveredRequest.body), {'seq': 42});
    final createRequest = requests.singleWhere(
      (request) =>
          request.url.path == '/v2/messages/scheduled' &&
          request.method == 'POST',
    );
    expect(
      jsonDecode(createRequest.body),
      containsPair('expiresInSeconds', 3600),
    );
    expect(
      jsonDecode(createRequest.body),
      containsPair('conversationId', 'c1'),
    );
    final updateRequest = requests.singleWhere(
      (request) =>
          request.url.path == '/v2/messages/scheduled/scheduled-1' &&
          request.method == 'PATCH',
    );
    expect(jsonDecode(updateRequest.body), {
      'body': {'text': '修改后的提醒'},
      'scheduledAt': updatedAt.toUtc().toIso8601String(),
      'expiresInSeconds': 3600,
    });
    final listRequest = requests.singleWhere(
      (request) =>
          request.url.path == '/v2/messages/scheduled' &&
          request.method == 'GET',
    );
    expect(listRequest.url.queryParameters['status'], 'pending');
    expect(listRequest.url.queryParameters['limit'], '200');
    expect(
      requests.any(
        (request) =>
            request.method == 'DELETE' &&
            request.url.path == '/v2/messages/scheduled/scheduled-1',
      ),
      isTrue,
    );
    expect(gateway.sentMessages.single.expireSeconds, greaterThan(3500));
    expect(
      requests,
      isNot(
        contains(
          isA<http.Request>().having(
            (request) => request.url.path,
            'path',
            '/v2/messages/conversations/c1/send',
          ),
        ),
      ),
    );
    await repository.close();
  });

  test('恢复和连续收消息只批量上报递增的最高送达序列并消费回执与过期事件', () async {
    final repository = _ReceiptRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.loginAsDemo();
    await controller.loadMessages('c-linyu', force: true);
    await Future<void>.delayed(const Duration(milliseconds: 220));
    expect(repository.deliveredSequences, [3]);

    repository.emitMessage(sequence: 5, id: 'incoming-5', senderId: 'u1');
    repository.emitMessage(sequence: 4, id: 'incoming-4', senderId: 'u1');
    await Future<void>.delayed(const Duration(milliseconds: 220));
    expect(repository.deliveredSequences, [3, 5]);

    repository.emitMessage(sequence: 6, id: 'mine-6', senderId: 'me');
    final mappedSending = ChatMessage(
      id: 'wk-client-7',
      clientMessageId: 'wk-client-7',
      conversationId: 'c-linyu',
      senderId: controller.currentUser!.id,
      senderName: controller.currentUser!.name,
      text: 'WuKong sending event',
      sentAt: DateTime.utc(2026, 8, 1, 10, 1),
      isMine: true,
      status: MessageStatus.sending,
    );
    repository.emit(
      ImEvent(
        type: ImEventType.messageCreated,
        payload: {'message': mappedSending.toJson()},
      ),
    );
    final mappedFailed = ChatMessage(
      id: 'local-client-failed',
      clientMessageId: 'client-failed',
      conversationId: 'c-linyu',
      senderId: controller.currentUser!.id,
      senderName: controller.currentUser!.name,
      text: '未发送成功',
      sentAt: DateTime.utc(2026, 8, 1, 10, 2),
      isMine: true,
      status: MessageStatus.failed,
    );
    repository.emit(
      ImEvent(
        type: ImEventType.messageCreated,
        payload: {'message': mappedFailed.toJson()},
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(
      controller
          .messagesFor('c-linyu')
          .singleWhere((message) => message.id == 'wk-client-7')
          .status,
      MessageStatus.sending,
    );
    repository.emit(
      const ImEvent(
        type: ImEventType.messageDelivered,
        payload: {'conversationId': 'c-linyu', 'seq': 6, 'deliveredCount': 1},
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(
      controller
          .messagesFor('c-linyu')
          .singleWhere((message) => message.id == 'mine-6')
          .status,
      MessageStatus.delivered,
    );
    expect(
      controller
          .messagesFor('c-linyu')
          .singleWhere((message) => message.id == 'wk-client-7')
          .status,
      MessageStatus.sending,
      reason: '没有服务端序号的本地消息不得被其他消息的回执改写',
    );
    expect(
      controller
          .messagesFor('c-linyu')
          .singleWhere((message) => message.id == 'local-client-failed')
          .status,
      MessageStatus.failed,
    );

    repository.emit(
      const ImEvent(
        type: ImEventType.messageExpired,
        payload: {'messageId': 'mine-6'},
      ),
    );
    await Future<void>.delayed(Duration.zero);
    final expired = controller
        .messagesFor('c-linyu')
        .singleWhere((message) => message.id == 'mine-6');
    expect(expired.status, MessageStatus.expired);
    expect(expired.text, isEmpty);
  });

  test('正在输入状态去重发送、接收展示并在消息到达时清除', () async {
    final repository = _ReceiptRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.loginAsDemo();

    controller.updateTyping('c-linyu', true);
    controller.updateTyping('c-linyu', true);
    await Future<void>.delayed(Duration.zero);
    expect(repository.typingStates, [true]);
    controller.updateTyping('c-linyu', false);
    await Future<void>.delayed(Duration.zero);
    expect(repository.typingStates, [true, false]);

    repository.emit(
      const ImEvent(
        type: ImEventType.typing,
        payload: {'conversationId': 'c-linyu', 'userId': 'u1', 'typing': true},
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(controller.typingLabelFor('c-linyu'), '正在输入…');

    repository.emitMessage(sequence: 8, id: 'typing-finished', senderId: 'u1');
    await Future<void>.delayed(Duration.zero);
    expect(controller.typingLabelFor('c-linyu'), isNull);
  });

  test(
    'bursty SDK conversation invalidations are coalesced without sync loops',
    () async {
      final repository = _ConversationRefreshRepository();
      final controller = AppController(repository);
      addTearDown(controller.dispose);
      await controller.loginAsDemo();
      repository.conversationLoads = 0;
      repository.syncCalls = 0;

      for (var index = 0; index < 25; index++) {
        repository.emit(
          const ImEvent(
            type: ImEventType.conversationChanged,
            payload: <String, Object?>{},
          ),
        );
      }
      await Future<void>.delayed(const Duration(milliseconds: 300));

      expect(repository.conversationLoads, 1);
      expect(repository.syncCalls, 0);
    },
  );

  testWidgets('已归档入口和左滑归档恢复适配小屏深色 200% 字体', (tester) async {
    tester.view.physicalSize = const Size(320, 568);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MediaQuery(
        data: const MediaQueryData(textScaler: TextScaler.linear(2)),
        child: MaterialApp(
          theme: buildLinliTheme(Brightness.dark),
          home: Scaffold(
            body: AnimatedBuilder(
              animation: controller,
              builder: (_, _) => ConversationsTab(controller: controller),
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    final archivedFilter = find.byKey(
      const Key('archived-conversation-filter'),
    );
    expect(tester.getSize(archivedFilter).height, greaterThanOrEqualTo(44));
    await tester.drag(
      find.byKey(const ValueKey('conversation-slidable-c-linyu')),
      const Offset(-300, 0),
    );
    await tester.pumpAndSettle();
    final archiveAction = find.byKey(
      const ValueKey('conversation-archive-c-linyu'),
    );
    expect(archiveAction, findsOneWidget);
    await tester.runAsync(
      () => controller.toggleConversationArchived('c-linyu'),
    );
    await tester.pumpAndSettle();
    expect(find.text('林屿'), findsNothing);

    await tester.drag(
      find.byKey(const Key('messages-filter-control')),
      const Offset(-500, 0),
    );
    await tester.pumpAndSettle();
    await tester.tap(archivedFilter);
    await tester.pumpAndSettle();
    expect(find.text('林屿'), findsOneWidget);
    await tester.drag(
      find.byKey(const ValueKey('conversation-slidable-c-linyu')),
      const Offset(-300, 0),
    );
    await tester.pumpAndSettle();
    expect(find.text('恢复'), findsOneWidget);
    await tester.runAsync(
      () => controller.toggleConversationArchived('c-linyu'),
    );
    await tester.pumpAndSettle();
    expect(find.text('林屿'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('链接卡、到期提示、过期系统态和群回执摘要不伪造且不刷屏', (tester) async {
    tester.view.physicalSize = const Size(320, 568);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final now = DateTime(2026, 8, 1, 10);
    await tester.pumpWidget(
      MediaQuery(
        data: const MediaQueryData(textScaler: TextScaler.linear(2)),
        child: MaterialApp(
          theme: buildLinliTheme(Brightness.dark),
          home: Scaffold(
            body: ListView(
              children: [
                MessageBubble(
                  message: ChatMessage(
                    id: 'preview',
                    conversationId: 'c1',
                    senderId: 'me',
                    senderName: '我',
                    text: 'https://example.com/a',
                    sentAt: now,
                    isMine: true,
                    expiresAt: now.add(const Duration(hours: 1)),
                    deliveredCount: 8,
                    readCount: 5,
                    linkPreview: const LinkPreview(
                      url: 'https://example.com/a',
                      title: '服务端标题',
                      description: '服务端摘要',
                      siteName: 'Example',
                    ),
                  ),
                  showGroupReceipt: true,
                ),
                MessageBubble(
                  message: ChatMessage(
                    id: 'plain',
                    conversationId: 'c1',
                    senderId: 'u1',
                    senderName: '林屿',
                    text: 'https://failed.example',
                    sentAt: now,
                    isMine: false,
                  ),
                ),
                MessageBubble(
                  message: ChatMessage(
                    id: 'expired',
                    conversationId: 'c1',
                    senderId: 'u1',
                    senderName: '林屿',
                    text: '',
                    sentAt: now,
                    isMine: false,
                    status: MessageStatus.expired,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );

    expect(find.byKey(const Key('server-link-preview')), findsOneWidget);
    expect(find.text('服务端标题'), findsOneWidget);
    expect(find.text('https://failed.example'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.text('消息已过期'),
      160,
      scrollable: find.byType(Scrollable).first,
    );
    expect(find.text('消息已过期'), findsOneWidget);
    expect(find.byKey(const Key('group-receipt-summary')), findsOneWidget);
    expect(find.text('已送达 8 · 已读 5'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('定时消息列表提供空态和离线重试错误且点击目标不小于 44pt', (tester) async {
    final repository = _ScheduledErrorRepository();
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final conversation = controller.conversations.firstWhere(
      (item) => item.kind == ConversationKind.direct,
    );
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.dark),
        home: ChatScreen(controller: controller, conversation: conversation),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('chat-more-button')));
    await tester.pumpAndSettle();
    final scheduledButton = find.byKey(const Key('scheduled-messages-button'));
    expect(tester.getSize(scheduledButton).height, greaterThanOrEqualTo(44));
    await tester.tap(scheduledButton);
    await tester.pumpAndSettle();

    expect(find.text('定时消息加载失败'), findsOneWidget);
    expect(find.textContaining('网络不可用'), findsOneWidget);
    expect(find.text('重试'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('长按发送键选择服务端定时发送并可在列表取消', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final conversation = controller.conversations.firstWhere(
      (item) => item.kind == ConversationKind.direct,
    );
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: conversation),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('chat-more-button')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('scheduled-messages-button')));
    await tester.pumpAndSettle();
    expect(find.text('没有待发送消息'), findsOneWidget);
    Navigator.of(tester.element(find.text('没有待发送消息'))).pop();
    await tester.pumpAndSettle();
    await tester.pageBack();
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('message-input')), '晚上提醒我提交评审');
    await tester.pump();
    expect(
      tester
          .widget<InkResponse>(find.byKey(const Key('send-button')))
          .onLongPress,
      isNotNull,
    );
    await tester.longPress(find.byKey(const Key('send-button')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('schedule-message-action')), findsOneWidget);
    await tester.tap(find.byKey(const Key('schedule-message-action')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('schedule-date-picker')), findsOneWidget);
    await tester.tap(find.byKey(const Key('confirm-schedule-time')));
    await tester.pumpAndSettle();
    expect(find.text('已交由服务器定时发送'), findsOneWidget);
    expect(
      tester
          .widget<TextField>(find.byKey(const Key('message-input')))
          .controller
          ?.text,
      isEmpty,
    );

    await tester.tap(find.byKey(const Key('chat-more-button')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('scheduled-messages-button')));
    await tester.pumpAndSettle();
    expect(find.text('晚上提醒我提交评审'), findsOneWidget);
    final item = controller.scheduledMessagesFor(conversation.id).single;
    final edit = find.byKey(Key('edit-scheduled-${item.id}'));
    expect(tester.getSize(edit).height, greaterThanOrEqualTo(44));
    await tester.tap(edit);
    await tester.pumpAndSettle();
    expect(find.text('修改定时消息'), findsOneWidget);
    await tester.enterText(
      find.byKey(Key('edit-scheduled-text-${item.id}')),
      '修改后的定时提醒',
    );
    tester.testTextInput.hide();
    await tester.pump(const Duration(milliseconds: 200));
    await tester.tap(find.byKey(Key('confirm-edit-scheduled-${item.id}')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 800));
    expect(
      controller.scheduledMessagesFor(conversation.id).single.text,
      '修改后的定时提醒',
    );
    expect(find.text('修改后的定时提醒'), findsOneWidget);
    final cancel = find.byKey(Key('cancel-scheduled-${item.id}'));
    expect(tester.getSize(cancel).height, greaterThanOrEqualTo(44));
  });
}

class _ReceiptRepository extends DemoImRepository {
  _ReceiptRepository() : super(latency: Duration.zero);

  final eventController = StreamController<ImEvent>.broadcast();
  final deliveredSequences = <int>[];
  final typingStates = <bool>[];

  @override
  Stream<ImEvent> get events => eventController.stream;

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    ChatMessage(
      id: 'restored-3',
      conversationId: conversationId,
      senderId: 'u1',
      senderName: '林屿',
      text: '恢复消息',
      sentAt: DateTime(2026, 8, 1),
      isMine: false,
      conversationSeq: 3,
    ),
  ];

  @override
  Future<void> markDelivered(String conversationId, int sequence) async {
    deliveredSequences.add(sequence);
  }

  @override
  Future<void> setTyping(String conversationId, bool typing) async {
    typingStates.add(typing);
  }

  void emit(ImEvent event) => eventController.add(event);

  void emitMessage({
    required int sequence,
    required String id,
    required String senderId,
  }) => emit(
    ImEvent(
      type: ImEventType.messageCreated,
      payload: {
        'message': {
          'id': id,
          'clientMsgId': 'client-$id',
          'conversationId': 'c-linyu',
          'senderId': senderId,
          'type': 'text',
          'body': {'text': '消息 $sequence'},
          'createdAt': '2026-08-01T10:00:00Z',
          'conversationSeq': sequence,
        },
      },
    ),
  );

  @override
  Future<void> close() async {
    await eventController.close();
    await super.close();
  }
}

class _ConversationRefreshRepository extends DemoImRepository {
  _ConversationRefreshRepository() : super(latency: Duration.zero);

  final eventController = StreamController<ImEvent>.broadcast();
  int conversationLoads = 0;
  int syncCalls = 0;

  @override
  Stream<ImEvent> get events => eventController.stream;

  @override
  Future<List<Conversation>> conversations() async {
    conversationLoads++;
    return super.conversations();
  }

  @override
  Future<void> syncNow() async {
    syncCalls++;
    await super.syncNow();
  }

  void emit(ImEvent event) => eventController.add(event);

  @override
  Future<void> close() async {
    await eventController.close();
    await super.close();
  }
}

class _ScheduledErrorRepository extends DemoImRepository {
  _ScheduledErrorRepository() : super(latency: Duration.zero);

  @override
  Future<List<ScheduledMessage>> scheduledMessages(String conversationId) =>
      throw Exception('网络不可用');
}

http.Response _json(Map<String, Object?> value) => http.Response(
  jsonEncode(value),
  200,
  headers: {'content-type': 'application/json'},
);

Map<String, Object?> _scheduledJson(DateTime at) => {
  'id': 'scheduled-1',
  'conversationId': 'c1',
  'body': {'text': '稍后提醒我'},
  'scheduledAt': at.toUtc().toIso8601String(),
  'status': 'scheduled',
};
