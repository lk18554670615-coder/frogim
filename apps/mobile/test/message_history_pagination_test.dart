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
    final beforeExtent = position.maxScrollExtent;
    position.jumpTo(position.minScrollExtent);
    await tester.pump();
    for (var attempt = 0; attempt < 20 && position.pixels == 0; attempt++) {
      await tester.pump(const Duration(milliseconds: 50));
    }

    expect(repository.historyRequestCount, 1);
    expect(controller.messagesFor('conversation-1'), hasLength(100));
    // The offset advances by exactly the height prepended above the viewport,
    // keeping the old first visible page in the same screen position.
    final prependedExtent = position.maxScrollExtent - beforeExtent;
    expect(prependedExtent, greaterThan(0));
    expect(position.pixels, moreOrLessEquals(prependedExtent, epsilon: 1));
    expect(tester.takeException(), isNull);
  });
}

class _HistoryRepository extends DemoImRepository
    implements PaginatedMessageRepository {
  _HistoryRepository({this.failHistory = false})
    : super(latency: Duration.zero);

  final bool failHistory;
  int? requestedBeforeSequence;
  int historyRequestCount = 0;

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    for (var sequence = 51; sequence <= 100; sequence++)
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
    if (failHistory) throw StateError('offline');
    return [
      for (var sequence = 1; sequence <= 51; sequence++)
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
    text: '消息 $sequence',
    sentAt: DateTime(2026, 8, 16, 10).add(Duration(seconds: sequence)),
    isMine: false,
    conversationSeq: sequence,
    status: MessageStatus.sent,
  );
}
