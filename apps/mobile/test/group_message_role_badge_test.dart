import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

void main() {
  testWidgets('群主和管理员显示身份标签，普通及未知角色隐藏', (tester) async {
    for (final entry in <String?, String?>{
      'owner': '群主',
      'admin': '管理员',
      'member': null,
      null: null,
    }.entries) {
      await _pumpBubble(tester, senderRole: entry.key);
      expect(
        find.byKey(const Key('message-sender-role-message-1')),
        entry.value == null ? findsNothing : findsOneWidget,
      );
      if (entry.value != null) {
        expect(
          find.descendant(
            of: find.byKey(const Key('message-sender-role-message-1')),
            matching: find.text(entry.value!),
          ),
          findsOneWidget,
        );
      }
      expect(tester.takeException(), isNull);
    }
  });

  testWidgets('自己的消息、非群发送者行和系统消息不显示身份标签', (tester) async {
    await _pumpBubble(tester, senderRole: 'owner', mine: true);
    expect(
      find.byKey(const Key('message-sender-role-message-1')),
      findsNothing,
    );

    await _pumpBubble(tester, senderRole: 'owner', showSender: false);
    expect(
      find.byKey(const Key('message-sender-role-message-1')),
      findsNothing,
    );

    await _pumpBubble(
      tester,
      senderRole: 'owner',
      kind: MessageContentKind.system,
    );
    expect(
      find.byKey(const Key('message-sender-role-message-1')),
      findsNothing,
    );
  });

  testWidgets('身份加入消息无障碍描述且长昵称在窄屏不溢出', (tester) async {
    final semantics = tester.ensureSemantics();
    tester.view.physicalSize = const Size(260, 600);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await _pumpBubble(
      tester,
      senderRole: 'admin',
      senderName: '这是一个用于验证窄屏省略行为的非常非常长的群成员昵称',
      brightness: Brightness.dark,
    );

    expect(
      find.bySemanticsLabel('这是一个用于验证窄屏省略行为的非常非常长的群成员昵称，管理员：群消息'),
      findsOneWidget,
    );
    expect(find.text('管理员'), findsOneWidget);
    expect(tester.takeException(), isNull);
    semantics.dispose();
  });
}

Future<void> _pumpBubble(
  WidgetTester tester, {
  String? senderRole,
  String senderName = '林安',
  bool mine = false,
  bool showSender = true,
  MessageContentKind kind = MessageContentKind.text,
  Brightness brightness = Brightness.light,
}) => tester.pumpWidget(
  MaterialApp(
    theme: buildLinliTheme(brightness),
    home: Scaffold(
      body: MessageBubble(
        message: ChatMessage(
          id: 'message-1',
          clientMessageId: 'message-1',
          conversationId: 'group-1',
          senderId: mine ? 'me' : 'member-1',
          senderName: senderName,
          text: '群消息',
          sentAt: DateTime(2026),
          isMine: mine,
          kind: kind,
        ),
        senderName: senderName,
        senderRole: senderRole,
        showSender: showSender,
      ),
    ),
  ),
);
