import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

void main() {
  testWidgets('头像右键优先触发成员快捷操作而不是消息菜单', (tester) async {
    Offset? avatarAnchor;
    Offset? messageAnchor;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MessageBubble(
            message: _message(),
            senderName: '群成员',
            showSender: true,
            onAvatarTap: () {},
            onAvatarSecondaryTap: (anchor) => avatarAnchor = anchor,
            onLongPress: (anchor) => messageAnchor = anchor,
          ),
        ),
      ),
    );

    await tester.tap(
      find.byKey(const Key('message-avatar-message-1')),
      buttons: kSecondaryMouseButton,
    );
    await tester.pump();

    expect(avatarAnchor, isNotNull);
    expect(messageAnchor, isNull);
  });
}

ChatMessage _message() => ChatMessage(
  id: 'message-1',
  conversationId: 'group-1',
  senderId: 'member-1',
  senderName: '群成员',
  text: '群消息',
  sentAt: DateTime(2026),
  isMine: false,
);
