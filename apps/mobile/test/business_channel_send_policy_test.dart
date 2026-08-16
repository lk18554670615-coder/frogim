import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/im/business_features.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

void main() {
  test('客服会话使用客服状态而不是公开频道资料确认发言权限', () {
    expect(usesManagedBusinessChannelSendPolicy(_conversation(10)), isFalse);
    expect(usesManagedBusinessChannelSendPolicy(_conversation(3)), isFalse);
    for (final channelType in const [4, 5, 6, 9]) {
      expect(
        usesManagedBusinessChannelSendPolicy(_conversation(channelType)),
        isTrue,
      );
    }
  });

  test('客服结束事件会立即关闭输入区，其他频道不受影响', () {
    final messages = [
      ChatMessage(
        id: 'support-ended',
        conversationId: 'support_1',
        senderId: '____system',
        senderName: '系统',
        text: '客服会话已结束',
        sentAt: DateTime.utc(2026, 8, 13),
        isMine: false,
        kind: MessageContentKind.system,
        event: 'support.session.ended',
      ),
    ];
    expect(supportSessionSendRestriction(10, messages), '客服会话已结束，如需帮助请重新发起咨询。');
    expect(supportSessionSendRestriction(3, messages), isNotNull);
    expect(supportSessionSendRestriction(9, messages), isNull);
    expect(supportSessionSendRestriction(10, const []), isNull);
  });

  test('资讯频道的普通订阅者不会获得发布权限', () {
    expect(
      businessChannelSendRestriction(_channel()),
      '该资讯频道仅管理员可发布，你仍可浏览和接收更新。',
    );
    expect(businessChannelSendRestriction(_channel(role: 'moderator')), isNull);
  });

  test('封禁、解散、未加入和全员禁言均禁止发送', () {
    expect(businessChannelSendRestriction(_channel(disband: true)), isNotNull);
    expect(businessChannelSendRestriction(_channel(ban: true)), isNotNull);
    expect(
      businessChannelSendRestriction(_channel(subscribed: false)),
      isNotNull,
    );
    expect(businessChannelSendRestriction(_channel(sendBan: true)), isNotNull);
  });

  testWidgets('只读频道栏清楚说明原因并可重试', (tester) async {
    var retries = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Align(
            alignment: Alignment.bottomCenter,
            child: ChannelSendRestrictionBar(
              message: '该资讯频道仅管理员可发布',
              onRetry: () async => retries++,
            ),
          ),
        ),
      ),
    );

    expect(find.byKey(const Key('channel-send-restriction')), findsOneWidget);
    expect(find.text('该资讯频道仅管理员可发布'), findsOneWidget);
    expect(find.byType(TextField), findsNothing);
    await tester.tap(find.text('重试'));
    expect(retries, 1);
  });

  testWidgets('只读频道的历史失败消息不再诱导重试', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MessageBubble(
            message: ChatMessage(
              id: 'local-failed',
              conversationId: 'info_1',
              senderId: 'me',
              senderName: '我',
              text: '未发出',
              sentAt: DateTime.utc(2026, 8, 13),
              isMine: true,
              status: MessageStatus.failed,
            ),
          ),
        ),
      ),
    );

    expect(find.text('发送失败'), findsOneWidget);
    expect(find.text('发送失败，点此重试'), findsNothing);
  });

  testWidgets('失败消息重试入口具备可读语义和至少 44 点触控区域', (tester) async {
    var retries = 0;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MessageBubble(
            message: ChatMessage(
              id: 'retryable-failed',
              conversationId: 'direct_1',
              senderId: 'me',
              senderName: '我',
              text: '请重新发送',
              sentAt: DateTime.utc(2026, 8, 16),
              isMine: true,
              status: MessageStatus.failed,
            ),
            onRetry: () => retries++,
          ),
        ),
      ),
    );

    final target = find.byKey(const Key('failed-message-retry'));
    expect(target, findsOneWidget);
    expect(tester.getSize(target).height, greaterThanOrEqualTo(44));
    final semantics = tester.getSemantics(target).getSemanticsData();
    expect(semantics.label, '消息发送失败，重新发送');
    expect(semantics.hasAction(ui.SemanticsAction.tap), isTrue);
    await tester.tap(target);
    expect(retries, 1);
  });
}

Conversation _conversation(int channelType) => Conversation(
  id: 'channel_$channelType',
  title: '频道',
  subtitle: '',
  updatedAt: DateTime.utc(2026, 8, 13),
  kind: ConversationKind.group,
  channelId: 'channel_$channelType',
  channelType: channelType,
);

BusinessChannelSummary _channel({
  String role = 'member',
  bool subscribed = true,
  bool ban = false,
  bool disband = false,
  bool sendBan = false,
}) => BusinessChannelSummary(
  id: 'info_1',
  channelType: 6,
  category: 'info',
  name: '资讯',
  description: '',
  ownerId: 'owner',
  visibility: 'public',
  joinPolicy: 'open',
  postingPolicy: 'operators',
  memberCount: 2,
  subscribed: subscribed,
  role: role,
  ban: ban,
  disband: disband,
  sendBan: sendBan,
);
