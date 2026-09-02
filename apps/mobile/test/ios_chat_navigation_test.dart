import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  final audioEvents = <EventChannel>[];
  setUpAll(() {
    for (final name in [
      'com.llfbandit.record/messages',
      'xyz.luan/audioplayers.global',
    ]) {
      messenger.setMockMethodCallHandler(
        MethodChannel(name),
        (_) async => null,
      );
    }
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers'),
      (call) async {
        if (call.method == 'create') {
          final args = call.arguments as Map<Object?, Object?>;
          final channel = EventChannel(
            'xyz.luan/audioplayers/events/${args['playerId']}',
          );
          audioEvents.add(channel);
          messenger.setMockStreamHandler(
            channel,
            MockStreamHandler.inline(onListen: (_, _) {}),
          );
        }
        return null;
      },
    );
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      MockStreamHandler.inline(onListen: (_, _) {}),
    );
  });
  tearDownAll(() {
    for (final name in [
      'com.llfbandit.record/messages',
      'xyz.luan/audioplayers.global',
      'xyz.luan/audioplayers',
    ]) {
      messenger.setMockMethodCallHandler(MethodChannel(name), null);
    }
    for (final channel in [
      ...audioEvents,
      const EventChannel('xyz.luan/audioplayers.global/events'),
    ]) {
      messenger.setMockStreamHandler(channel, null);
    }
  });
  setUp(() => SharedPreferences.setMockInitialValues({}));

  for (final conversationId in ['c-linyu', 'c-team']) {
    testWidgets(
      'iOS $conversationId follows an edge swipe and returns to the previous page',
      (tester) async {
        final harness = await _openChat(tester, conversationId: conversationId);
        final route = ModalRoute.of(tester.element(find.byType(ChatScreen)))!;
        expect(route, isA<CupertinoPageRoute<void>>());
        expect(route.popGestureEnabled, isTrue);
        expect(harness.controller.activeConversationId, conversationId);

        final gesture = await tester.startGesture(const Offset(3, 350));
        await gesture.moveBy(const Offset(25, 0));
        await tester.pump(const Duration(milliseconds: 80));
        await gesture.moveBy(const Offset(220, 0));
        await tester.pump(const Duration(milliseconds: 80));
        expect(harness.navigator.currentState!.userGestureInProgress, isTrue);
        expect(tester.getTopLeft(find.byType(ChatScreen)).dx, greaterThan(100));
        await gesture.up();
        await tester.pumpAndSettle();

        expect(find.byType(ChatScreen), findsNothing);
        expect(find.text('会话列表状态 8'), findsOneWidget);
        expect(harness.navigator.currentState!.canPop(), isFalse);
        expect(harness.controller.activeConversationId, isNull);
        expect(tester.takeException(), isNull);
      },
    );
  }

  testWidgets('iOS canceled swipe keeps chat, draft and active conversation', (
    tester,
  ) async {
    final harness = await _openChat(tester);
    await tester.enterText(find.byKey(const Key('message-input')), '未发送的草稿');
    await tester.pump(const Duration(milliseconds: 400));
    final gesture = await tester.startGesture(const Offset(3, 320));
    await gesture.moveBy(const Offset(25, 0));
    await tester.pump(const Duration(milliseconds: 100));
    await gesture.moveBy(const Offset(55, 0));
    await tester.pump(const Duration(milliseconds: 400));
    expect(harness.navigator.currentState!.userGestureInProgress, isTrue);
    await gesture.up();
    await tester.pumpAndSettle();

    expect(find.byType(ChatScreen), findsOneWidget);
    expect(tester.getTopLeft(find.byType(ChatScreen)).dx, closeTo(0, .01));
    expect(find.text('未发送的草稿'), findsOneWidget);
    expect(harness.controller.activeConversationId, 'c-linyu');
    expect(harness.navigator.currentState!.userGestureInProgress, isFalse);

    await tester.dragFrom(const Offset(3, 320), const Offset(330, 0));
    await tester.pumpAndSettle();
    expect(find.byType(ChatScreen), findsNothing);
    await tester.tap(find.byKey(const Key('open-chat')));
    await tester.pumpAndSettle();
    expect(find.text('未发送的草稿'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'iOS swiping in message content does not trigger edge navigation',
    (tester) async {
      final harness = await _openChat(tester);
      await tester.dragFrom(const Offset(120, 350), const Offset(220, 0));
      await tester.pumpAndSettle();
      expect(find.byType(ChatScreen), findsOneWidget);
      expect(harness.navigator.currentState!.userGestureInProgress, isFalse);
      expect(tester.getTopLeft(find.byType(ChatScreen)).dx, closeTo(0, .01));
      await tester.tap(find.byType(BackButton));
      await tester.pumpAndSettle();
      expect(find.byType(ChatScreen), findsNothing);
    },
  );

  testWidgets(
    'iOS reduced motion keeps edge navigation without page displacement',
    (tester) async {
      final harness = await _openChat(tester, reduceMotion: true);
      final route =
          ModalRoute.of(tester.element(find.byType(ChatScreen)))!
              as PageRoute<void>;
      expect(route.transitionDuration, Duration.zero);
      final gesture = await tester.startGesture(const Offset(3, 350));
      await gesture.moveBy(const Offset(25, 0));
      await tester.pump();
      await gesture.moveBy(const Offset(230, 0));
      await tester.pump();
      expect(harness.navigator.currentState!.userGestureInProgress, isTrue);
      expect(tester.getTopLeft(find.byType(ChatScreen)).dx, closeTo(0, .01));
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.byType(ChatScreen), findsNothing);
      expect(find.text('会话列表状态 8'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  for (final platform in [
    TargetPlatform.android,
    TargetPlatform.macOS,
    TargetPlatform.windows,
  ]) {
    testWidgets('$platform keeps the existing chat transition', (tester) async {
      await _openChat(tester, platform: platform);
      final route = ModalRoute.of(tester.element(find.byType(ChatScreen)))!;
      expect(route, isA<PageRouteBuilder<void>>());
      expect(
        (route as PageRoute<void>).transitionDuration,
        const Duration(milliseconds: 180),
      );
      await tester.dragFrom(const Offset(3, 350), const Offset(330, 0));
      await tester.pumpAndSettle();
      expect(find.byType(ChatScreen), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }
}

Future<({AppController controller, GlobalKey<NavigatorState> navigator})>
_openChat(
  WidgetTester tester, {
  String conversationId = 'c-linyu',
  TargetPlatform platform = TargetPlatform.iOS,
  bool reduceMotion = false,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final controller = AppController(_NavigationRepository());
  await tester.runAsync(controller.loginAsDemo);
  addTearDown(controller.dispose);
  final Conversation conversation = controller.conversations.firstWhere(
    (item) => item.id == conversationId,
  );
  final navigator = GlobalKey<NavigatorState>();
  var state = 7;
  await tester.pumpWidget(
    MaterialApp(
      navigatorKey: navigator,
      theme: buildLinliTheme(Brightness.light).copyWith(platform: platform),
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(context).copyWith(disableAnimations: reduceMotion),
        child: child!,
      ),
      home: StatefulBuilder(
        builder: (context, setState) => Scaffold(
          body: Column(
            children: [
              Text('会话列表状态 $state'),
              FilledButton(
                key: const Key('open-chat'),
                onPressed: () {
                  setState(() => state++);
                  Navigator.of(context).push(
                    chatScreenRoute<void>(
                      context,
                      controller: controller,
                      conversation: conversation,
                    ),
                  );
                },
                child: const Text('打开聊天'),
              ),
            ],
          ),
        ),
      ),
    ),
  );
  await tester.tap(find.byKey(const Key('open-chat')));
  await tester.pumpAndSettle();
  return (controller: controller, navigator: navigator);
}

class _NavigationRepository extends DemoImRepository {
  _NavigationRepository() : super(latency: Duration.zero);
  final drafts = <String, String>{};

  @override
  Future<void> saveDraft(String conversationId, String text) async {
    drafts[conversationId] = text;
  }

  @override
  Future<String> readDraft(String conversationId) async =>
      drafts[conversationId] ?? '';
}
