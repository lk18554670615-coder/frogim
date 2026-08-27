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

  testWidgets('图片转发先选会话再确认，支持搜索且不会点行即发送', (tester) async {
    final fixture = await _openPreviewWithController(tester);
    addTearDown(fixture.controller.dispose);
    final target = fixture.controller.conversations.firstWhere(
      (conversation) => conversation.id != fixture.sourceConversation.id,
    );

    await tester.tap(find.byKey(const Key('forward-message-image-preview')));
    await tester.pumpAndSettle();

    expect(find.text('选择转发对象'), findsOneWidget);
    expect(find.text('选择一个会话，确认后发送'), findsOneWidget);
    final confirm = find.byKey(const Key('forward-conversation-confirm'));
    expect(tester.widget<FilledButton>(confirm).onPressed, isNull);

    await tester.enterText(
      find.byKey(const Key('forward-conversation-search')),
      target.title,
    );
    tester.view.viewInsets = const FakeViewPadding(bottom: 280);
    await tester.pump(const Duration(milliseconds: 220));
    await tester.pumpAndSettle();
    final targetRow = find.byKey(Key('forward-conversation-${target.id}'));
    expect(targetRow, findsOneWidget);
    expect(tester.getRect(confirm).bottom, lessThanOrEqualTo(812 - 280));

    await tester.tap(targetRow);
    await tester.pumpAndSettle();

    expect(find.text('选择转发对象'), findsOneWidget);
    expect(find.byKey(const Key('message-image-preview')), findsOneWidget);
    expect(tester.widget<FilledButton>(confirm).onPressed, isNotNull);

    await tester.tap(confirm);
    await tester.pumpAndSettle();

    expect(find.text('选择转发对象'), findsNothing);
    expect(find.text('已转发到 ${target.title}'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('没有最近会话时转发面板显示明确空状态', (tester) async {
    final fixture = await _openPreviewWithController(tester);
    addTearDown(fixture.controller.dispose);
    fixture.controller.conversations.clear();

    await tester.tap(find.byKey(const Key('forward-message-image-preview')));
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('forward-conversation-empty')), findsOneWidget);
    expect(find.text('暂无可转发会话'), findsOneWidget);
    expect(
      tester
          .widget<FilledButton>(
            find.byKey(const Key('forward-conversation-confirm')),
          )
          .onPressed,
      isNull,
    );
    expect(tester.takeException(), isNull);
  });
}

Future<({AppController controller, Conversation sourceConversation})>
_openPreviewWithController(WidgetTester tester) async {
  tester.view.physicalSize = const Size(375, 812);
  tester.view.devicePixelRatio = 1;
  tester.view.resetViewInsets();
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.view.resetViewInsets);

  final controller = AppController(DemoImRepository(latency: Duration.zero));
  await tester.runAsync(controller.loginAsDemo);
  final conversation = controller.conversations.firstWhere(
    (item) => item.kind == ConversationKind.direct,
  );
  await tester.runAsync(() => controller.loadMessages(conversation.id));
  final image = controller
      .messagesFor(conversation.id)
      .firstWhere((message) => message.kind == MessageContentKind.image);

  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: Scaffold(
        body: MessageBubble(message: image, controller: controller),
      ),
    ),
  );
  await tester.pumpAndSettle();
  await tester.tap(find.byKey(Key('message-image-${image.clientMessageId}')));
  await tester.pumpAndSettle();
  expect(find.byKey(const Key('message-image-preview')), findsOneWidget);

  return (controller: controller, sourceConversation: conversation);
}
