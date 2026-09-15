import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('长按他人群消息在光标处插入结构化 @ 并保留回复', (tester) async {
    final controller = await _pumpGroupChat(tester);
    final group = _group(controller);
    final target = _targetMessage(controller, group.id);

    await tester.longPress(find.text(target.text));
    await tester.pumpAndSettle();
    await tester.tap(find.text('回复'));
    await tester.pumpAndSettle();

    final inputFinder = find.byKey(const Key('message-input'));
    await tester.enterText(inputFinder, '前后');
    final input = tester.widget<TextField>(inputFinder);
    input.controller!.selection = const TextSelection.collapsed(offset: 1);

    await tester.longPress(find.text(target.text));
    await tester.pumpAndSettle();
    expect(find.text('@TA'), findsOneWidget);
    await tester.tap(find.text('@TA'));
    await tester.pumpAndSettle();

    final expected = '前@${target.senderName} 后';
    expect(input.controller!.text, expected);
    expect(
      input.controller!.selection,
      TextSelection.collapsed(offset: expected.length - 1),
    );
    expect(tester.widget<TextField>(inputFinder).focusNode!.hasFocus, isTrue);
    expect(find.textContaining('回复 ${target.senderName}：'), findsOneWidget);

    await tester.tap(find.byKey(const Key('send-button')));
    await tester.pumpAndSettle();
    final sent = controller
        .messagesFor(group.id)
        .lastWhere((message) => message.isMine && message.text == expected);
    expect(sent.replyToId, target.id);
    expect(sent.mentions, hasLength(1));
    expect(sent.mentions.single.userId, target.senderId);
    expect(sent.mentions.single.name, target.senderName);
    await _disposeChat(tester, controller);
  });

  testWidgets('PC 右键他人群消息可 @ 且自动从语音切回文字输入', (tester) async {
    final controller = await _pumpGroupChat(tester, desktop: true);
    final target = _targetMessage(controller, _group(controller).id);

    await tester.tap(find.byTooltip('切换到语音'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('message-input')), findsNothing);

    await tester.tap(find.text(target.text), buttons: kSecondaryMouseButton);
    await tester.pumpAndSettle();
    await tester.tap(find.text('@TA'));
    await tester.pumpAndSettle();

    final inputFinder = find.byKey(const Key('message-input'));
    expect(inputFinder, findsOneWidget);
    final input = tester.widget<TextField>(inputFinder);
    expect(input.controller!.text, '@${target.senderName} ');
    expect(input.focusNode!.hasFocus, isTrue);
    await _disposeChat(tester, controller);
  });

  testWidgets('自己的群消息不显示快捷 @', (tester) async {
    final controller = await _pumpGroupChat(tester);
    final group = _group(controller);
    final mine = controller
        .messagesFor(group.id)
        .firstWhere((message) => message.isMine && message.text.isNotEmpty);
    await tester.longPress(find.text(mine.text));
    await tester.pumpAndSettle();
    expect(find.text('@TA'), findsNothing);
    await _disposeChat(tester, controller);
  });

  testWidgets('已退出成员的旧群消息不显示快捷 @', (tester) async {
    final controller = await _pumpGroupChat(
      tester,
      repository: _MissingSenderRepository(),
    );
    final group = _group(controller);
    final target = _targetMessage(controller, group.id);
    await tester.longPress(find.text(target.text));
    await tester.pumpAndSettle();
    expect(find.text('@TA'), findsNothing);
    await _disposeChat(tester, controller);
  });

  testWidgets('单聊消息不显示快捷 @', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    final direct = controller.conversations.firstWhere(
      (conversation) => conversation.kind == ConversationKind.direct,
    );
    await _pumpChat(tester, controller, direct);
    final directMessage = controller
        .messagesFor(direct.id)
        .firstWhere((message) => !message.isMine && message.text.isNotEmpty);
    await tester.longPress(find.text(directMessage.text).first);
    await tester.pumpAndSettle();
    expect(find.text('@TA'), findsNothing);
    await _disposeChat(tester, controller);
  });
}

Future<void> _disposeChat(WidgetTester tester, AppController controller) async {
  controller.dispose();
  await tester.pumpWidget(const SizedBox.shrink());
  await tester.pump();
}

Future<AppController> _pumpGroupChat(
  WidgetTester tester, {
  bool desktop = false,
  DemoImRepository? repository,
}) async {
  final controller = AppController(
    repository ?? DemoImRepository(latency: Duration.zero),
  );
  await tester.runAsync(controller.loginAsDemo);
  await _pumpChat(tester, controller, _group(controller), desktop: desktop);
  return controller;
}

Future<void> _pumpChat(
  WidgetTester tester,
  AppController controller,
  Conversation conversation, {
  bool desktop = false,
}) async {
  tester.view.devicePixelRatio = 1;
  tester.view.physicalSize = desktop
      ? const Size(1280, 900)
      : const Size(390, 844);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.view.resetPhysicalSize);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: ChatScreen(controller: controller, conversation: conversation),
    ),
  );
  await tester.pumpAndSettle();
}

Conversation _group(AppController controller) => controller.conversations
    .firstWhere((conversation) => conversation.kind == ConversationKind.group);

ChatMessage _targetMessage(AppController controller, String conversationId) =>
    controller
        .messagesFor(conversationId)
        .firstWhere(
          (message) =>
              !message.isMine &&
              message.senderId == 'u2' &&
              message.kind == MessageContentKind.text,
        );

class _MissingSenderRepository extends DemoImRepository {
  _MissingSenderRepository() : super(latency: Duration.zero);

  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async =>
      (await super.groupMembers(
        conversationId,
      )).where((member) => member.user.id != 'u2').toList();
}
