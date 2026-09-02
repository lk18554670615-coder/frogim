import 'dart:async';

import 'package:flutter/cupertino.dart';
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

  for (final delta in [-1.0, -24.0, -180.0]) {
    testWidgets('桌面首次进入时滚轮上翻 $delta px 取消贴底及延迟刷新', (tester) async {
      final repository = _CacheThenNetworkRepository();
      final controller = AppController(repository);
      addTearDown(controller.dispose);
      await _pumpDesktopChat(tester, controller);
      final position = _messagePosition(tester);
      final bottom = position.pixels;
      await _wheel(tester, delta);
      final reading = position.pixels;
      expect(reading, closeTo(bottom + delta, .1));
      await _pumpFrames(tester, 24);
      expect(position.pixels, closeTo(reading, .1));
      repository.completeNetwork(messageCount: 44);
      await _pumpFrames(tester, 24);
      expect(position.pixels, closeTo(reading, 1));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('连续滚轮阅读历史、窗口变大不贴底，主动回到底部恢复实时跟随', (tester) async {
    final repository = _RealtimeMessageRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);
    await _pumpDesktopChat(tester, controller);
    final position = _messagePosition(tester);
    for (var i = 0; i < 5; i++) {
      await _wheel(tester, -24);
    }
    final reading = position.pixels;
    repository.emitIncoming(sequence: 41, text: '滚轮阅读时的新消息');
    await _pumpFrames(tester, 10);
    expect(position.pixels, closeTo(reading, 1));
    tester.view.physicalSize = const Size(1280, 960);
    await _pumpFrames(tester, 10);
    expect(position.pixels, closeTo(reading, 1));
    repository.emitIncoming(sequence: 42, text: '尺寸变化后仍不贴底');
    await _pumpFrames(tester, 10);
    expect(position.pixels, closeTo(reading, 1));

    // Stop 3px short: layout/realtime updates must not restore following.
    await _wheel(tester, position.extentAfter - 3);
    final almostBottom = position.pixels;
    repository.emitIncoming(sequence: 43, text: '尚未回到底部');
    await _pumpFrames(tester, 10);
    expect(position.pixels, closeTo(almostBottom, 1));
    await _wheel(tester, position.extentAfter - 1);
    await _pumpFrames(tester, 10);
    repository.emitIncoming(sequence: 44, text: '回到底部后自动跟随');
    await _pumpFrames(tester, 10);
    expect(position.extentAfter, closeTo(0, 1));
    expect(find.text('回到底部后自动跟随'), findsOneWidget);
  });

  testWidgets('真实触控板 pan/zoom 上翻后不被刷新抢位置', (tester) async {
    final repository = _CacheThenNetworkRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await _pumpDesktopChat(tester, controller);
    final position = _messagePosition(tester);
    final before = position.pixels;
    final center = tester.getCenter(find.byKey(const Key('message-list')));
    final gesture = await tester.createGesture(
      kind: PointerDeviceKind.trackpad,
    );
    await gesture.panZoomStart(center);
    await gesture.panZoomUpdate(center, pan: const Offset(0, 80));
    await tester.pump(const Duration(milliseconds: 100));
    await gesture.panZoomUpdate(center, pan: const Offset(0, 160));
    await tester.pump(const Duration(milliseconds: 100));
    await gesture.panZoomEnd();
    await _pumpFrames(tester, 10);
    final reading = position.pixels;
    expect(reading, lessThan(before - 50));
    repository.completeNetwork(messageCount: 44);
    await _pumpFrames(tester, 20);
    expect(position.pixels, closeTo(reading, 1));
  });

  for (final brightness in Brightness.values) {
    testWidgets('桌面窄聊天列 $brightness 只有一个滚动条且鼠标可拖动滑块', (tester) async {
      final repository = _CacheThenNetworkRepository();
      final controller = AppController(repository);
      addTearDown(controller.dispose);
      await _pumpDesktopChat(tester, controller, brightness: brightness);
      final bar = find.byKey(const Key('message-scrollbar'));
      expect(
        find.byWidgetPredicate((widget) => widget is RawScrollbar),
        findsOneWidget,
      );
      final scrollbar = tester.widget<RawScrollbar>(bar);
      expect(scrollbar.interactive, isTrue);
      expect(scrollbar.thumbVisibility, isTrue);
      final position = _messagePosition(tester);
      expect(scrollbar.controller!.position, same(position));
      final list = find.byKey(const Key('message-list'));
      final behavior = ScrollConfiguration.of(tester.element(list));
      expect(behavior.dragDevices, isNot(contains(PointerDeviceKind.mouse)));
      final bottom = position.pixels;
      // Left drag inside the message panel must NOT scroll the list.
      await tester.drag(
        list,
        const Offset(0, 150),
        kind: PointerDeviceKind.mouse,
      );
      await _pumpFrames(tester, 5);
      expect(position.pixels, closeTo(bottom, 1));

      final rect = tester.getRect(bar);
      final mouse = await tester.startGesture(
        Offset(rect.right - 4, rect.bottom - 16),
        kind: PointerDeviceKind.mouse,
      );
      await tester.pump(const Duration(milliseconds: 100));
      await mouse.moveBy(const Offset(0, -100));
      await tester.pump(const Duration(milliseconds: 100));
      await mouse.up();
      await _pumpFrames(tester, 8);
      final reading = position.pixels;
      expect(reading, lessThan(bottom - 100));
      repository.completeNetwork(messageCount: 44);
      await _pumpFrames(tester, 20);
      expect(position.pixels, closeTo(reading, 1));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('本人发送恢复跟随，程序化 jumpTo 不冒充用户回到底部', (tester) async {
    final repository = _RealtimeMessageRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);
    await _pumpDesktopChat(tester, controller);
    final position = _messagePosition(tester);
    await _wheel(tester, -180);
    position.jumpTo(position.maxScrollExtent);
    final reading = position.pixels;
    repository.emitIncoming(sequence: 41, text: '程序化定位后不恢复跟随');
    await _pumpFrames(tester, 12);
    expect(position.pixels, closeTo(reading, 1));
    await tester.enterText(find.byKey(const Key('message-input')), '主动发送');
    await tester.pump();
    await tester.tap(find.byKey(const Key('send-button')));
    await _pumpFrames(tester, 12);
    expect(position.extentAfter, closeTo(0, 1));
    repository.emitIncoming(sequence: 42, text: '本人发送后继续跟随');
    await _pumpFrames(tester, 12);
    expect(position.extentAfter, closeTo(0, 1));
  });

  testWidgets('点击滚动条轨道翻页也是主动滚动，不被初始贴底抢回', (tester) async {
    final repository = _CacheThenNetworkRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await _pumpDesktopChat(tester, controller);
    final position = _messagePosition(tester);
    final before = position.pixels;
    final rect = tester.getRect(find.byKey(const Key('message-scrollbar')));
    await tester.tapAt(
      Offset(rect.right - 3, rect.center.dy),
      kind: PointerDeviceKind.mouse,
    );
    await _pumpFrames(tester, 10);
    final reading = position.pixels;
    expect(reading, lessThan(before - 100));
    repository.completeNetwork(messageCount: 44);
    await _pumpFrames(tester, 20);
    expect(position.pixels, closeTo(reading, 1));
  });

  testWidgets('未完成的初始搜索定位不会覆盖用户滚轮定位，销毁清理待执行回调', (tester) async {
    final repository = _CacheThenNetworkRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await _pumpDesktopChat(tester, controller, initialMessageId: 'message-10');
    final position = _messagePosition(tester);
    await _wheel(tester, 160);
    final reading = position.pixels;
    repository.completeNetwork(messageCount: 44);
    await _pumpFrames(tester, 20);
    expect(position.pixels, closeTo(reading, 1));
    await tester.pumpWidget(const SizedBox.shrink());
    await _pumpFrames(tester, 40);
    expect(tester.takeException(), isNull);
  });

  testWidgets('图片尺寸补齐不抢历史位置；回到底部后尺寸变化仍能跟随', (tester) async {
    final repository = _RealtimeMessageRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);
    await _pumpDesktopChat(tester, controller);
    final position = _messagePosition(tester);
    final image = controller
        .messagesFor(_conversation.id)
        .last
        .copyWith(
          kind: MessageContentKind.image,
          mediaUrl: 'assets/brand/qingwaguagua-mark-transparent.png',
          mediaWidth: 400,
          mediaHeight: 200,
        );
    repository.updateMessage(image);
    await _pumpFrames(tester, 12);
    expect(position.extentAfter, closeTo(0, 1));
    await _wheel(tester, -24);
    final reading = position.pixels;
    final beforeSize = tester.getSize(
      find.byKey(Key('message-image-${image.clientMessageId}')),
    );
    repository.updateMessage(image.copyWith(mediaHeight: 800));
    await _pumpFrames(tester, 12);
    expect(
      tester
          .getSize(find.byKey(Key('message-image-${image.clientMessageId}')))
          .height,
      greaterThan(beforeSize.height),
    );
    expect(position.pixels, closeTo(reading, 1));
    await _wheel(tester, 10000);
    repository.updateMessage(image.copyWith(mediaHeight: 500));
    await _pumpFrames(tester, 12);
    expect(position.extentAfter, closeTo(0, 1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('窄 macOS 窗口大字体仍有桌面滚动条，短消息没有可滚动范围', (tester) async {
    tester.view.physicalSize = const Size(720, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repository = _TransitionRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(
          Brightness.dark,
        ).copyWith(platform: TargetPlatform.macOS),
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(
            context,
          ).copyWith(textScaler: const TextScaler.linear(1.5)),
          child: child!,
        ),
        home: ChatScreen(controller: controller, conversation: _conversation),
      ),
    );
    repository.completeNetwork();
    await _pumpFrames(tester, 20);
    expect(find.byKey(const Key('message-scrollbar')), findsOneWidget);
    final position = _messagePosition(tester);
    expect(position.maxScrollExtent - position.minScrollExtent, closeTo(0, 1));
    expect(tester.takeException(), isNull);
  });

  testWidgets('同一聊天组件切换会话后忽略旧加载与待执行定位', (tester) async {
    final firstRepository = _CacheThenNetworkRepository();
    final first = AppController(firstRepository);
    final secondRepository = _CacheThenNetworkRepository();
    final second = AppController(secondRepository);
    addTearDown(first.dispose);
    addTearDown(second.dispose);
    await _pumpDesktopChat(tester, first);
    await _wheel(tester, -200);
    // Same widget key/position, new controller: exercises didUpdateWidget,
    // in addition to the keyed chat replacement used by HomeScreen.
    await _pumpDesktopChat(tester, second);
    final position = _messagePosition(tester);
    expect(position.extentAfter, closeTo(0, 1));
    await _wheel(tester, -24);
    final reading = position.pixels;
    firstRepository.completeNetwork(messageCount: 70);
    secondRepository.completeNetwork(messageCount: 44);
    await _pumpFrames(tester, 25);
    expect(position.pixels, closeTo(reading, 1));
    expect(tester.takeException(), isNull);
  });

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

Future<void> _pumpDesktopChat(
  WidgetTester tester,
  AppController controller, {
  Brightness brightness = Brightness.light,
  String? initialMessageId,
}) async {
  tester.view.physicalSize = const Size(1280, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    MaterialApp(
      // An Android platform with a wide overall window also covers Web layout
      // detection: the embedded chat is only 520px wide, not a mobile page.
      theme: buildLinliTheme(
        brightness,
      ).copyWith(platform: TargetPlatform.android),
      home: Row(
        children: [
          const Expanded(child: SizedBox()),
          SizedBox(
            width: 520,
            child: ChatScreen(
              controller: controller,
              conversation: _conversation,
              initialMessageId: initialMessageId,
            ),
          ),
        ],
      ),
    ),
  );
  await tester.pump();
  await _pumpFrames(tester, 8);
}

Future<void> _wheel(WidgetTester tester, double dy) async {
  await tester.sendEventToBinding(
    PointerScrollEvent(
      kind: PointerDeviceKind.mouse,
      position: tester.getCenter(find.byKey(const Key('message-list'))),
      scrollDelta: Offset(0, dy),
    ),
  );
  await tester.pump();
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

  void updateMessage(ChatMessage message) => _eventController.add(
    ImEvent(
      type: ImEventType.messageChanged,
      payload: {'message': message.toJson()},
    ),
  );

  @override
  Future<void> saveDraft(String conversationId, String text) async {}

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}

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
