import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

void main() {
  testWidgets('名片消息显示公开账号资料', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MessageBubble(
            message: ChatMessage(
              id: 'contact-1',
              conversationId: 'c1',
              senderId: 'friend-1',
              senderName: '邻居',
              text: '[名片] 林安',
              sentAt: DateTime(2026, 7, 31, 12),
              isMine: false,
              kind: MessageContentKind.contact,
              contactUserId: 'friend-1',
              contactName: '林安',
              contactHandle: 'linan',
            ),
          ),
        ),
      ),
    );

    expect(find.byKey(const Key('contact-message-contact-1')), findsOneWidget);
    expect(find.text('林安'), findsOneWidget);
    expect(find.text('@linan'), findsOneWidget);
    expect(find.text('联系人名片'), findsOneWidget);
    expect(find.textContaining('手机号'), findsNothing);
  });

  testWidgets('位置消息展示地点和地址并可查看坐标', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MessageBubble(
            message: ChatMessage(
              id: 'location-1',
              conversationId: 'c1',
              senderId: 'friend-1',
              senderName: '邻居',
              text: '[位置] 人民广场',
              sentAt: DateTime(2026, 7, 31, 12),
              isMine: false,
              kind: MessageContentKind.location,
              latitude: 31.2304,
              longitude: 121.4737,
              locationName: '人民广场',
              locationAddress: '上海市黄浦区人民大道',
            ),
          ),
        ),
      ),
    );

    expect(
      find.byKey(const Key('location-message-location-1')),
      findsOneWidget,
    );
    expect(find.text('人民广场'), findsOneWidget);
    expect(find.text('上海市黄浦区人民大道'), findsOneWidget);

    await tester.tap(find.byKey(const Key('location-message-location-1')));
    await tester.pumpAndSettle();
    expect(find.textContaining('31.230400, 121.473700'), findsOneWidget);
    expect(find.text('复制坐标'), findsOneWidget);
  });
}
