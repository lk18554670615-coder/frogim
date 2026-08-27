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
  final audioEventChannels = <EventChannel>[];

  setUpAll(() {
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(
      const MethodChannel('com.llfbandit.record/messages'),
      (_) async => null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers.global'),
      (_) async => null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers'),
      (call) async {
        if (call.method == 'create') {
          final arguments = call.arguments! as Map<Object?, Object?>;
          final channel = EventChannel(
            'xyz.luan/audioplayers/events/${arguments['playerId']! as String}',
          );
          audioEventChannels.add(channel);
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
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(
      const MethodChannel('com.llfbandit.record/messages'),
      null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers.global'),
      null,
    );
    messenger.setMockMethodCallHandler(
      const MethodChannel('xyz.luan/audioplayers'),
      null,
    );
    for (final channel in audioEventChannels) {
      messenger.setMockStreamHandler(channel, null);
    }
    messenger.setMockStreamHandler(
      const EventChannel('xyz.luan/audioplayers.global/events'),
      null,
    );
  });

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('机器人会话展示真实菜单并发送目标明确的命令', (tester) async {
    final repository = _RobotMenuRepository();
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(
          controller: controller,
          conversation: _RobotMenuRepository.conversation,
        ),
      ),
    );
    await _pumpUi(tester);

    expect(find.byKey(const Key('robot-command-bar')), findsOneWidget);
    expect(find.text('请选择服务'), findsOneWidget);
    expect(find.text('订单查询'), findsNothing);

    await tester.tap(find.byKey(const Key('robot-menu-toggle')));
    await _pumpUi(tester);
    expect(find.text('订单查询'), findsOneWidget);

    await tester.tap(find.text('订单查询'));
    await _pumpUi(tester);
    await tester.runAsync(() async {
      for (var index = 0; index < 20 && repository.sent == null; index++) {
        await Future<void>.delayed(const Duration(milliseconds: 10));
      }
    });
    await tester.pump();
    final sentMessages = controller.messagesFor(
      _RobotMenuRepository.conversation.id,
    );
    expect(sentMessages, isNotEmpty);
    expect(sentMessages.last.robotId, 'robot-support');
    expect(repository.sent?.text, '查询订单');
    expect(repository.sent?.robotId, 'robot-support');
    expect(find.text('查询订单'), findsWidgets);
    expect(tester.takeException(), isNull);
  });
}

Future<void> _pumpUi(WidgetTester tester) async {
  for (var index = 0; index < 8; index++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}

class _RobotMenuRepository extends DemoImRepository {
  _RobotMenuRepository() : super(latency: Duration.zero);

  static final conversation = Conversation(
    id: 'conversation-robot',
    title: '服务助手',
    subtitle: '请选择服务',
    updatedAt: DateTime(2026, 8, 16, 15),
    kind: ConversationKind.direct,
    channelId: 'robot-support',
    channelType: 1,
    members: const [
      AppUser(
        id: 'robot-support',
        name: '服务助手',
        handle: 'support_bot',
        presence: '在线',
      ),
    ],
  );

  ChatMessage? sent;

  @override
  Future<List<Conversation>> conversations() async => [conversation];

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => const [];

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}

  @override
  Future<List<RobotProfile>> robotProfiles(
    String conversationId,
  ) async => const [
    RobotProfile(
      id: 'robot-support',
      name: '服务助手',
      username: 'support_bot',
      placeholder: '请选择服务',
      version: 1,
      menus: [
        RobotMenu(robotId: 'robot-support', command: '查询订单', remark: '订单查询'),
        RobotMenu(robotId: 'robot-support', command: '联系客服', remark: '人工服务'),
      ],
    ),
  ];

  @override
  Future<ChatMessage> send(ChatMessage pending) async {
    sent = pending;
    return pending.copyWith(
      id: 'server-robot-command',
      status: MessageStatus.sent,
    );
  }
}
