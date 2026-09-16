import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/gestures.dart';
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

  test('历史分页按序列向前合并、去重并在到达开头后停止', () async {
    final repository = _HistoryRepository();
    final controller = AppController(repository);

    await controller.loadMessages('conversation-1');
    expect(controller.messageHistoryHasMore('conversation-1'), isTrue);
    expect(controller.messagesFor('conversation-1').first.conversationSeq, 51);

    final loaded = await controller.loadOlderMessages('conversation-1');

    expect(loaded, isTrue);
    expect(repository.requestedBeforeSequence, 51);
    final messages = controller.messagesFor('conversation-1');
    expect(messages, hasLength(100));
    expect(messages.first.conversationSeq, 1);
    expect(messages.last.conversationSeq, 100);
    expect(
      messages.where((message) => message.id == 'message-51'),
      hasLength(1),
    );
    expect(controller.messageHistoryHasMore('conversation-1'), isFalse);

    final repeated = await controller.loadOlderMessages('conversation-1');
    expect(repeated, isFalse);
    expect(repository.historyRequestCount, 1);
  });

  test('历史分页失败保留当前消息并提供独立重试状态', () async {
    final repository = _HistoryRepository(failHistory: true);
    final controller = AppController(repository);
    await controller.loadMessages('conversation-1');

    final loaded = await controller.loadOlderMessages('conversation-1');

    expect(loaded, isFalse);
    expect(controller.messagesFor('conversation-1'), hasLength(50));
    expect(
      controller.messageHistoryErrors['conversation-1'],
      contains('较早的消息加载失败'),
    );
    expect(controller.messageErrors['conversation-1'], isNull);
    expect(controller.messageHistoryHasMore('conversation-1'), isTrue);
  });

  testWidgets('聊天页滑到顶部自动取更早消息且保持当前阅读锚点', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repository = _HistoryRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.loadMessages('conversation-1');
    final conversation = Conversation(
      id: 'conversation-1',
      title: '历史分页测试',
      subtitle: '消息 100',
      updatedAt: DateTime(2026, 8, 16, 10, 2),
      kind: ConversationKind.direct,
      members: const [
        AppUser(id: 'user-1', name: '测试用户', handle: 'tester', presence: '在线'),
      ],
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: AnimatedBuilder(
          animation: controller,
          builder: (_, _) =>
              ChatScreen(controller: controller, conversation: conversation),
        ),
      ),
    );
    await tester.pumpAndSettle();

    final messageList = find.byKey(const Key('message-list'));
    final scrollable = find.descendant(
      of: messageList,
      matching: find.byType(Scrollable),
    );
    final position = tester.state<ScrollableState>(scrollable).position;
    final beforeStart = position.minScrollExtent;
    await _scroll(tester, position.minScrollExtent - position.pixels);
    await tester.pump();
    for (var attempt = 0; attempt < 20 && position.pixels == 0; attempt++) {
      await tester.pump(const Duration(milliseconds: 50));
    }

    expect(repository.historyRequestCount, 1);
    expect(controller.messagesFor('conversation-1'), hasLength(100));
    // History grows above the stable center; reading coordinates do not move.
    expect(position.minScrollExtent, lessThan(beforeStart));
    expect(position.pixels, moreOrLessEquals(beforeStart, epsilon: 1));
    expect(tester.takeException(), isNull);
  });

  for (final variableHeight in [false, true]) {
    testWidgets('等待历史期间继续滚轮滚动保留新位置 variableHeight=$variableHeight', (
      tester,
    ) async {
      final repository = _HistoryRepository(variableHeight: variableHeight);
      repository.historyGate = Completer<void>();
      final controller = AppController(repository);
      addTearDown(controller.dispose);
      await _mountChat(tester, controller);
      final position = _position(tester);
      await _scroll(tester, position.minScrollExtent - position.pixels);
      expect(find.byKey(const Key('older-messages-loading')), findsOneWidget);
      await _scroll(tester, 270);
      final before = position.pixels;
      final anchor = find.textContaining('消息 55');
      final anchorY = tester.getTopLeft(anchor).dy;
      repository.historyGate!.complete();
      for (var i = 0; i < 6; i++) {
        await tester.pump(const Duration(milliseconds: 50));
      }
      expect(repository.historyRequestCount, 1);
      expect(position.pixels, closeTo(before, 1));
      expect(tester.getTopLeft(anchor).dy, closeTo(anchorY, 1));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('滚轮触发分页失败后可重试并连续加载到历史开头', (tester) async {
    final repository = _HistoryRepository(
      firstSequence: 101,
      failHistory: true,
    );
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await _mountChat(tester, controller);
    final position = _position(tester);
    await _scroll(tester, position.minScrollExtent - position.pixels);
    await tester.pump();
    expect(find.text('较早消息加载失败，点此重试'), findsOneWidget);
    expect(controller.messagesFor('conversation-1'), hasLength(50));
    repository.failHistory = false;
    await tester.tap(find.text('较早消息加载失败，点此重试'));
    await tester.pumpAndSettle();
    expect(controller.messagesFor('conversation-1'), hasLength(100));
    await _scroll(tester, position.minScrollExtent - position.pixels);
    await tester.pumpAndSettle();
    expect(controller.messagesFor('conversation-1'), hasLength(150));
    expect(controller.messageHistoryHasMore('conversation-1'), isFalse);
    expect(repository.historyRequestCount, 3);
    expect(tester.takeException(), isNull);
  });

  testWidgets('点击回复预览自动加载较早历史并定位到原消息', (tester) async {
    final repository = _HistoryRepository(replyTargetSequence: 10);
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await _mountChat(tester, controller);

    final replyPreview = find.byKey(
      const Key('message-reply-preview-client-100'),
    );
    expect(replyPreview, findsOneWidget);
    expect(repository.historyRequestCount, 0);

    await tester.tap(replyPreview);
    await tester.pumpAndSettle();

    expect(repository.historyRequestCount, 1);
    expect(controller.messagesFor('conversation-1').first.conversationSeq, 1);
    expect(find.text('消息 10'), findsOneWidget);
    final targetCenter = tester.getCenter(find.text('消息 10'));
    expect(
      targetCenter.dy,
      inInclusiveRange(0, tester.view.physicalSize.height),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('原消息已加载时点击回复预览直接定位且不请求历史', (tester) async {
    final repository = _HistoryRepository(
      firstSequence: 1,
      replyTargetSequence: 10,
    );
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await _mountChat(tester, controller);

    await tester.tap(find.byKey(const Key('message-reply-preview-client-50')));
    await tester.pumpAndSettle();

    expect(repository.historyRequestCount, 0);
    expect(find.text('消息 10'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('只有引用快照而没有原消息 ID 时不提供无效跳转', (tester) async {
    final message = ChatMessage(
      id: 'snapshot-reply',
      conversationId: 'other-group',
      senderId: 'user-1',
      senderName: '测试用户',
      text: '转发后的回复正文',
      sentAt: DateTime(2026, 9, 16),
      isMine: false,
      kind: MessageContentKind.reply,
      replyToText: '来自另一个群的引用快照',
    );
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: MessageBubble(message: message)),
      ),
    );

    final preview = tester.widget<InkWell>(
      find.byKey(const Key('message-reply-preview-snapshot-reply')),
    );
    expect(preview.onTap, isNull);
  });
}

Future<void> _mountChat(WidgetTester tester, AppController controller) async {
  tester.view.physicalSize = const Size(1100, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: ChatScreen(
        controller: controller,
        conversation: Conversation(
          id: 'conversation-1',
          title: '历史测试',
          subtitle: '',
          updatedAt: DateTime(2026, 8, 16),
          kind: ConversationKind.direct,
          members: const [
            AppUser(
              id: 'user-1',
              name: '测试用户',
              handle: 'tester',
              presence: '在线',
            ),
          ],
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

ScrollPosition _position(WidgetTester tester) => tester
    .state<ScrollableState>(
      find.descendant(
        of: find.byKey(const Key('message-list')),
        matching: find.byType(Scrollable),
      ),
    )
    .position;

Future<void> _scroll(WidgetTester tester, double dy) async {
  await tester.sendEventToBinding(
    PointerScrollEvent(
      position: tester.getCenter(find.byKey(const Key('message-list'))),
      scrollDelta: Offset(0, dy),
    ),
  );
  await tester.pump();
}

class _HistoryRepository extends DemoImRepository
    implements PaginatedMessageRepository {
  _HistoryRepository({
    this.failHistory = false,
    this.firstSequence = 51,
    this.variableHeight = false,
    this.replyTargetSequence,
  }) : super(latency: Duration.zero);

  bool failHistory;
  final int firstSequence;
  final bool variableHeight;
  final int? replyTargetSequence;
  Completer<void>? historyGate;
  int? requestedBeforeSequence;
  int historyRequestCount = 0;

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    for (
      var sequence = firstSequence;
      sequence < firstSequence + 50;
      sequence++
    )
      _message(conversationId, sequence),
  ];

  @override
  Future<List<ChatMessage>> olderMessages(
    String conversationId, {
    required int beforeSequence,
    int limit = 50,
  }) async {
    historyRequestCount += 1;
    requestedBeforeSequence = beforeSequence;
    await historyGate?.future;
    if (failHistory) throw StateError('offline');
    return [
      for (
        var sequence = math.max(1, beforeSequence - limit);
        sequence <= beforeSequence;
        sequence++
      )
        _message(conversationId, sequence),
    ];
  }

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}

  ChatMessage _message(String conversationId, int sequence) => ChatMessage(
    id: 'message-$sequence',
    clientMessageId: 'client-$sequence',
    conversationId: conversationId,
    senderId: 'user-1',
    senderName: '测试用户',
    text: replyTargetSequence != null && sequence == firstSequence + 49
        ? '回复历史消息'
        : variableHeight
        ? '消息 $sequence${'\n多行正文' * (sequence % 4)}'
        : '消息 $sequence',
    sentAt: DateTime(2026, 8, 16, 10).add(Duration(seconds: sequence)),
    isMine: false,
    conversationSeq: sequence,
    status: MessageStatus.sent,
    kind: replyTargetSequence != null && sequence == firstSequence + 49
        ? MessageContentKind.reply
        : MessageContentKind.text,
    replyToId: replyTargetSequence != null && sequence == firstSequence + 49
        ? 'message-$replyTargetSequence'
        : null,
    replyToText: replyTargetSequence != null && sequence == firstSequence + 49
        ? '消息 $replyTargetSequence'
        : null,
    replyToSeq: replyTargetSequence != null && sequence == firstSequence + 49
        ? replyTargetSequence!
        : 0,
  );
}
