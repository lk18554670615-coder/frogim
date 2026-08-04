import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

void main() {
  testWidgets('消息气泡展示编辑状态和聚合回应并支持撤销入口', (tester) async {
    String? selectedEmoji;
    var addTapped = false;
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(
          body: MessageBubble(
            message: ChatMessage(
              id: 'message-1',
              conversationId: 'c1',
              senderId: 'friend-1',
              senderName: '林安',
              text: '今晚开会',
              sentAt: DateTime(2026, 8, 1, 8),
              isMine: false,
              editedAt: DateTime(2026, 8, 1, 8, 5),
              reactions: const [
                MessageReaction(
                  emoji: '👍',
                  count: 3,
                  reactedByMe: true,
                  userIds: ['friend-1', 'friend-2', 'me'],
                ),
              ],
            ),
            onReactionTap: (emoji) => selectedEmoji = emoji,
            onAddReaction: () => addTapped = true,
          ),
        ),
      ),
    );

    expect(find.byKey(const Key('edited-label-message-1')), findsOneWidget);
    expect(find.text('👍 3'), findsOneWidget);
    await tester.tap(find.byKey(const Key('reaction-👍')));
    expect(selectedEmoji, '👍');
    await tester.tap(find.byKey(const Key('add-message-reaction')));
    expect(addTapped, isTrue);
  });

  testWidgets('群聊输入区提供至少 44 点的成员提醒入口', (tester) async {
    final controller = TextEditingController();
    addTearDown(controller.dispose);
    var mentionTapped = false;
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.dark),
        home: Scaffold(
          body: ChatComposer(
            controller: controller,
            onSend: () {},
            onToggleAttachments: () {},
            onToggleEmoji: () {},
            onAttachment: (_) {},
            onVoiceReady: (_) {},
            onCancelReply: () {},
            onMention: () => mentionTapped = true,
          ),
        ),
      ),
    );

    final button = find.byKey(const Key('mention-member-button'));
    expect(button, findsOneWidget);
    expect(tester.getSize(button).width, greaterThanOrEqualTo(44));
    expect(tester.getSize(button).height, greaterThanOrEqualTo(44));
    await tester.tap(button);
    expect(mentionTapped, isTrue);
  });

  testWidgets('mentions 和回应在深色 200% 字体下不溢出', (tester) async {
    tester.view.physicalSize = const Size(320, 568);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.dark),
        home: MediaQuery(
          data: const MediaQueryData(textScaler: TextScaler.linear(2)),
          child: Scaffold(
            body: MessageBubble(
              message: ChatMessage(
                id: 'message-large',
                conversationId: 'group-1',
                senderId: 'friend-1',
                senderName: '林安',
                text: '@所有人 请查看今天更新的群公告和会议安排',
                sentAt: DateTime(2026, 8, 1, 8),
                isMine: false,
                mentions: const [MessageMention(userId: 'all', name: '所有人')],
                reactions: const [
                  MessageReaction(emoji: '🎉', count: 12, reactedByMe: false),
                ],
              ),
            ),
          ),
        ),
      ),
    );

    expect(tester.takeException(), isNull);
    expect(find.textContaining('@所有人'), findsOneWidget);
    expect(find.text('🎉 12'), findsOneWidget);
  });
}
