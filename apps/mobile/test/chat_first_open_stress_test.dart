import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/im_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('首次进入大量可变高度消息时立即显示最新消息且后台刷新不跳走', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final repository = _LargeCacheRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: _conversation),
      ),
    );
    await tester.pump();
    for (var frame = 0; frame < 7; frame++) {
      await tester.pump(const Duration(milliseconds: 50));
    }

    expect(repository.cacheRequests, 1);
    expect(repository.networkRequests, 1);
    expect(find.text('最新缓存消息 240').hitTestable(), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsNothing);

    repository.completeNetwork();
    await tester.pump();
    for (var frame = 0; frame < 6; frame++) {
      await tester.pump(const Duration(milliseconds: 50));
    }

    expect(find.text('服务器新增消息 242').hitTestable(), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

final _conversation = Conversation(
  id: 'large-first-open',
  title: '首开压力测试',
  subtitle: '服务器新增消息 242',
  updatedAt: DateTime(2026, 8, 17, 9),
  kind: ConversationKind.direct,
  members: const [
    AppUser(id: 'peer', name: '林屿', handle: 'linyu', presence: '在线'),
  ],
);

class _LargeCacheRepository extends DemoImRepository
    implements CachedMessageRepository {
  _LargeCacheRepository() : super(latency: Duration.zero);

  final _network = Completer<List<ChatMessage>>();
  int cacheRequests = 0;
  int networkRequests = 0;

  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) async {
    cacheRequests += 1;
    return _messages(conversationId, 240, latestLabel: '最新缓存消息 240');
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) {
    networkRequests += 1;
    return _network.future;
  }

  void completeNetwork() => _network.complete([
    ..._messages(_conversation.id, 240, latestLabel: '最新缓存消息 240'),
    _message(_conversation.id, 241, '服务器新增消息 241'),
    _message(_conversation.id, 242, '服务器新增消息 242'),
  ]);

  List<ChatMessage> _messages(
    String conversationId,
    int count, {
    required String latestLabel,
  }) => [
    for (var sequence = 1; sequence <= count; sequence++)
      _message(
        conversationId,
        sequence,
        sequence == count
            ? latestLabel
            : sequence % 5 == 0
            ? '第 $sequence 条较长消息，用来模拟真实聊天里多行文字、链接摘要和不同气泡高度。'
                  '这一行继续增加高度，避免列表用单一固定尺寸就能算准终点。'
            : '消息 $sequence',
      ),
  ];

  ChatMessage _message(String conversationId, int sequence, String text) =>
      ChatMessage(
        id: 'message-$sequence',
        clientMessageId: 'client-$sequence',
        conversationId: conversationId,
        senderId: 'peer',
        senderName: '林屿',
        text: text,
        sentAt: DateTime(2026, 8, 17, 9).add(Duration(seconds: sequence)),
        isMine: false,
        conversationSeq: sequence,
        status: MessageStatus.sent,
      );
}
