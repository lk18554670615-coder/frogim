import 'dart:async';

import 'package:flutter/cupertino.dart';
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

  testWidgets('首次进入先展示本地消息并在服务器刷新后稳定停在最底部', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final repository = _CacheThenNetworkRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: _conversation),
      ),
    );
    await tester.pump();
    await _pumpFrames(tester, 20);

    expect(controller.messagesFor(_conversation.id), hasLength(40));
    expect(find.textContaining('缓存消息 40'), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsNothing);
    expect(repository.networkRequests, 1);
    final position = _messagePosition(tester);
    expect(
      position.pixels,
      moreOrLessEquals(position.maxScrollExtent, epsilon: 1),
    );

    repository.completeNetwork(messageCount: 42);
    await tester.pump();
    await _pumpFrames(tester, 24);

    expect(find.textContaining('服务器消息 42'), findsOneWidget);
    expect(
      position.pixels,
      moreOrLessEquals(position.maxScrollExtent, epsilon: 1),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('用户主动上滑后服务器刷新不会强行把阅读位置拉回底部', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final repository = _CacheThenNetworkRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: _conversation),
      ),
    );
    await tester.pump();
    await _pumpFrames(tester, 20);

    final list = find.byKey(const Key('message-list'));
    final position = _messagePosition(tester);
    await tester.drag(list, const Offset(0, 320));
    await tester.pump();
    final readingOffset = position.pixels;
    expect(readingOffset, lessThan(position.maxScrollExtent));

    repository.completeNetwork(messageCount: 44);
    await tester.pump();
    await _pumpFrames(tester, 24);

    expect(position.pixels, lessThan(position.maxScrollExtent - 100));
    expect(position.pixels, moreOrLessEquals(readingOffset, epsilon: 2));
    expect(tester.takeException(), isNull);
  });

  testWidgets('实时新消息在底部自动跟随且阅读历史时不抢滚动位置', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final repository = _RealtimeMessageRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: _conversation),
      ),
    );
    await tester.pump();
    await _pumpFrames(tester, 20);

    final list = find.byKey(const Key('message-list'));
    final position = _messagePosition(tester);
    repository.emitIncoming(sequence: 41, text: '实时到达的最新消息');
    await tester.pump();
    await _pumpFrames(tester, 12);

    expect(find.text('实时到达的最新消息'), findsOneWidget);
    expect(
      position.pixels,
      moreOrLessEquals(position.maxScrollExtent, epsilon: 1),
    );

    await tester.drag(list, const Offset(0, 320));
    await tester.pump();
    final readingOffset = position.pixels;
    repository.emitIncoming(sequence: 42, text: '阅读历史时收到的消息');
    await tester.pump();
    await _pumpFrames(tester, 12);

    expect(position.pixels, moreOrLessEquals(readingOffset, epsilon: 2));
    expect(position.pixels, lessThan(position.maxScrollExtent - 100));
    expect(tester.takeException(), isNull);
  });

  testWidgets('从第二个聊天返回后恢复前一个会话的活跃状态', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: _conversation),
      ),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(controller.activeConversationId, _conversation.id);

    final navigator = Navigator.of(tester.element(find.byType(ChatScreen)));
    navigator.push<void>(
      chatScreenRoute(
        tester.element(find.byType(ChatScreen)),
        controller: controller,
        conversation: _conversationB,
      ),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 400));
    expect(controller.activeConversationId, _conversationB.id);

    navigator.pop();
    await _pumpFrames(tester, 10);
    expect(controller.activeConversationId, _conversation.id);
    expect(tester.takeException(), isNull);
  });

  testWidgets('会话转场期间复用同一次加载且返回保留上一页状态', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final repository = _TransitionRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    var homeState = 7;

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: StatefulBuilder(
          builder: (context, setState) => Scaffold(
            body: Column(
              children: [
                Text('主页状态 $homeState', key: const Key('home-state')),
                FilledButton(
                  key: const Key('open-chat-route'),
                  onPressed: () {
                    setState(() => homeState += 1);
                    Navigator.of(context).push(
                      chatScreenRoute(
                        context,
                        controller: controller,
                        conversation: _conversation,
                      ),
                    );
                  },
                  child: const Text('打开会话'),
                ),
              ],
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.byKey(const Key('open-chat-route')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 90));
    expect(find.byType(ChatScreen), findsOneWidget);
    expect(repository.networkRequests, 1);

    repository.completeNetwork();
    await tester.pump(const Duration(milliseconds: 180));
    await _pumpFrames(tester, 4);
    expect(find.text('转场缓存消息'), findsOneWidget);
    expect(repository.networkRequests, 1);

    Navigator.of(tester.element(find.byType(ChatScreen))).pop();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 220));
    expect(find.byType(ChatScreen), findsNothing);
    expect(find.text('主页状态 8'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('重新进入已有会话会保留现有消息并在后台校准最新记录', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final repository = _ReopenRefreshRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.loadMessages(_conversation.id);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Builder(
          builder: (context) => Scaffold(
            body: FilledButton(
              key: const Key('reopen-chat-route'),
              onPressed: () => Navigator.of(context).push(
                chatScreenRoute(
                  context,
                  controller: controller,
                  conversation: _conversation,
                ),
              ),
              child: const Text('重新进入'),
            ),
          ),
        ),
      ),
    );

    expect(repository.networkRequests, 1);
    await tester.tap(find.byKey(const Key('reopen-chat-route')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 90));

    expect(find.textContaining('上次已显示的消息'), findsOneWidget);
    expect(find.byType(CupertinoActivityIndicator), findsNothing);
    expect(repository.networkRequests, 2);

    repository.completeRefresh();
    await tester.pump();
    await _pumpFrames(tester, 8);

    expect(find.text('后台同步到的新消息'), findsOneWidget);
    expect(repository.networkRequests, 2);
    expect(tester.takeException(), isNull);
  });

  test('冷启动时缓存读取与服务器同步并行开始', () async {
    final repository = _SlowCacheRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);

    final loading = controller.loadMessages(_conversation.id, force: true);
    await Future<void>.delayed(Duration.zero);

    expect(repository.networkRequests, 1);
    expect(repository.cacheRequests, 1);

    repository.completeCache();
    await Future<void>.delayed(Duration.zero);
    expect(controller.messagesFor(_conversation.id).single.text, '冷启动缓存消息');

    repository.completeNetwork();
    await loading;
    expect(controller.messagesFor(_conversation.id).single.text, '服务器最新消息');
  });

  test('会话列表加载后预热最近聊天且点入时复用同一次本地读取', () async {
    final repository = _PrewarmCacheRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);

    await controller.loginAsDemo();
    await Future<void>.delayed(Duration.zero);

    expect(repository.cacheRequests, 1);
    final loading = controller.loadMessages(_conversation.id, force: true);
    await Future<void>.delayed(Duration.zero);
    expect(repository.cacheRequests, 1);

    repository.completeCache();
    await loading;

    expect(controller.messagesFor(_conversation.id).last.text, '服务器校准后的最新消息');
  });
}

Future<void> _pumpFrames(WidgetTester tester, int count) async {
  for (var index = 0; index < count; index++) {
    await tester.pump(const Duration(milliseconds: 50));
  }
}

ScrollPosition _messagePosition(WidgetTester tester) {
  final scrollable = find.descendant(
    of: find.byKey(const Key('message-list')),
    matching: find.byType(Scrollable),
  );
  return tester.state<ScrollableState>(scrollable).position;
}

final _conversation = Conversation(
  id: 'cache-first-conversation',
  title: '缓存优先测试',
  subtitle: '最新消息',
  updatedAt: DateTime(2026, 8, 16, 10),
  kind: ConversationKind.direct,
  members: [AppUser(id: 'peer', name: '林屿', handle: 'linyu', presence: '在线')],
);

final _conversationB = Conversation(
  id: 'cache-first-conversation-b',
  title: '第二个会话',
  subtitle: '另一条最新消息',
  updatedAt: DateTime(2026, 8, 16, 10, 1),
  kind: ConversationKind.direct,
  members: const [
    AppUser(id: 'peer-b', name: '许言', handle: 'xuyan', presence: '在线'),
  ],
);

class _CacheThenNetworkRepository extends DemoImRepository
    implements CachedMessageRepository {
  _CacheThenNetworkRepository() : super(latency: Duration.zero);

  final Completer<List<ChatMessage>> _network = Completer<List<ChatMessage>>();
  int networkRequests = 0;

  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) async =>
      _page(conversationId, count: 40, label: '缓存消息');

  @override
  Future<List<ChatMessage>> messages(String conversationId) {
    networkRequests += 1;
    return _network.future;
  }

  void completeNetwork({required int messageCount}) => _network.complete(
    _page(_conversation.id, count: messageCount, label: '服务器消息'),
  );

  List<ChatMessage> _page(
    String conversationId, {
    required int count,
    required String label,
  }) => [
    for (var sequence = 1; sequence <= count; sequence++)
      ChatMessage(
        id: 'message-$sequence',
        clientMessageId: 'client-$sequence',
        conversationId: conversationId,
        senderId: 'peer',
        senderName: '林屿',
        text: '$label $sequence：这是一条用于验证首次进入聊天定位的消息。',
        sentAt: DateTime(2026, 8, 16, 10).add(Duration(seconds: sequence)),
        isMine: false,
        conversationSeq: sequence,
        status: MessageStatus.sent,
      ),
  ];
}

class _RealtimeMessageRepository extends DemoImRepository
    implements CachedMessageRepository {
  _RealtimeMessageRepository() : super(latency: Duration.zero);

  final StreamController<ImEvent> _eventController =
      StreamController<ImEvent>.broadcast();

  @override
  Stream<ImEvent> get events => _eventController.stream;

  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) async =>
      _page(conversationId);

  @override
  Future<List<ChatMessage>> messages(String conversationId) async =>
      _page(conversationId);

  void emitIncoming({required int sequence, required String text}) {
    final message = ChatMessage(
      id: 'realtime-$sequence',
      clientMessageId: 'realtime-client-$sequence',
      conversationId: _conversation.id,
      senderId: 'peer',
      senderName: '林屿',
      text: text,
      sentAt: DateTime(2026, 8, 16, 10).add(Duration(seconds: sequence)),
      isMine: false,
      conversationSeq: sequence,
      status: MessageStatus.sent,
    );
    _eventController.add(
      ImEvent(
        type: ImEventType.messageCreated,
        payload: {'message': message.toJson()},
      ),
    );
  }

  List<ChatMessage> _page(String conversationId) => [
    for (var sequence = 1; sequence <= 40; sequence++)
      ChatMessage(
        id: 'realtime-base-$sequence',
        clientMessageId: 'realtime-base-client-$sequence',
        conversationId: conversationId,
        senderId: 'peer',
        senderName: '林屿',
        text: '实时消息基础记录 $sequence',
        sentAt: DateTime(2026, 8, 16, 10).add(Duration(seconds: sequence)),
        isMine: false,
        conversationSeq: sequence,
        status: MessageStatus.sent,
      ),
  ];

  @override
  Future<void> close() async {
    await _eventController.close();
    await super.close();
  }
}

class _TransitionRepository extends DemoImRepository
    implements CachedMessageRepository {
  _TransitionRepository() : super(latency: Duration.zero);

  final Completer<List<ChatMessage>> _network = Completer<List<ChatMessage>>();
  int networkRequests = 0;

  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) async => [
    ChatMessage(
      id: 'transition-cache',
      conversationId: conversationId,
      senderId: 'peer',
      senderName: '林屿',
      text: '转场缓存消息',
      sentAt: DateTime(2026, 8, 16, 10),
      isMine: false,
      conversationSeq: 1,
    ),
  ];

  @override
  Future<List<ChatMessage>> messages(String conversationId) {
    networkRequests += 1;
    return _network.future;
  }

  void completeNetwork() => _network.complete([
    ChatMessage(
      id: 'transition-cache',
      conversationId: _conversation.id,
      senderId: 'peer',
      senderName: '林屿',
      text: '转场缓存消息',
      sentAt: DateTime(2026, 8, 16, 10),
      isMine: false,
      conversationSeq: 1,
    ),
  ]);
}

class _ReopenRefreshRepository extends DemoImRepository
    implements CachedMessageRepository {
  _ReopenRefreshRepository() : super(latency: Duration.zero);

  final Completer<List<ChatMessage>> _refresh = Completer<List<ChatMessage>>();
  int networkRequests = 0;

  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) async =>
      const [];

  @override
  Future<List<ChatMessage>> messages(String conversationId) {
    networkRequests += 1;
    if (networkRequests == 1) {
      return Future.value([
        _message(conversationId, sequence: 1, text: '上次已显示的消息'),
      ]);
    }
    return _refresh.future;
  }

  void completeRefresh() => _refresh.complete([
    _message(_conversation.id, sequence: 1, text: '上次已显示的消息'),
    _message(_conversation.id, sequence: 2, text: '后台同步到的新消息'),
  ]);
}

class _SlowCacheRepository extends DemoImRepository
    implements CachedMessageRepository {
  _SlowCacheRepository() : super(latency: Duration.zero);

  final Completer<List<ChatMessage>> _cache = Completer<List<ChatMessage>>();
  final Completer<List<ChatMessage>> _network = Completer<List<ChatMessage>>();
  int cacheRequests = 0;
  int networkRequests = 0;

  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) {
    cacheRequests += 1;
    return _cache.future;
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) {
    networkRequests += 1;
    return _network.future;
  }

  void completeCache() => _cache.complete([
    _message(_conversation.id, sequence: 1, text: '冷启动缓存消息'),
  ]);

  void completeNetwork() => _network.complete([
    _message(_conversation.id, sequence: 1, text: '服务器最新消息'),
  ]);
}

class _PrewarmCacheRepository extends DemoImRepository
    implements CachedMessageRepository {
  _PrewarmCacheRepository() : super(latency: Duration.zero);

  final Completer<List<ChatMessage>> _cache = Completer<List<ChatMessage>>();
  int cacheRequests = 0;

  @override
  Future<List<Conversation>> conversations() async => [_conversation];

  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) {
    cacheRequests += 1;
    return _cache.future;
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    _message(conversationId, sequence: 1, text: '预热缓存消息'),
    _message(conversationId, sequence: 2, text: '服务器校准后的最新消息'),
  ];

  void completeCache() => _cache.complete([
    _message(_conversation.id, sequence: 1, text: '预热缓存消息'),
  ]);
}

ChatMessage _message(
  String conversationId, {
  required int sequence,
  required String text,
}) => ChatMessage(
  id: 'test-message-$sequence',
  clientMessageId: 'test-client-$sequence',
  conversationId: conversationId,
  senderId: 'peer',
  senderName: '林屿',
  text: text,
  sentAt: DateTime(2026, 8, 16, 10).add(Duration(seconds: sequence)),
  isMine: false,
  conversationSeq: sequence,
  status: MessageStatus.sent,
);
