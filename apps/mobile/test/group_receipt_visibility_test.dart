import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/group_message_policy.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

final now = DateTime.now();
Conversation conversation(String? role, {bool direct = false}) => Conversation(
  id: 'receipt-chat',
  title: direct ? '私聊测试' : '回执测试群',
  subtitle: '',
  updatedAt: now,
  kind: direct ? ConversationKind.direct : ConversationKind.group,
  channelType: direct ? 1 : 2,
  currentUserRole: role,
  unread: 3,
  members: [DemoImRepository.demoUser, DemoImRepository.people.first],
);

ChatMessage message(
  int seq, {
  MessageStatus status = MessageStatus.read,
  bool mine = true,
  bool voice = false,
}) => ChatMessage(
  id: 'receipt-$seq',
  clientMessageId: 'receipt-client-$seq',
  conversationId: 'receipt-chat',
  senderId: mine ? 'me' : 'u1',
  senderName: mine ? '我' : '林屿',
  text: voice ? '[语音]' : '回执消息 $seq',
  sentAt: now.add(Duration(seconds: seq)),
  isMine: mine,
  conversationSeq: seq,
  deliveredCount: 12,
  readCount: 7,
  status: status,
  kind: voice ? MessageContentKind.voice : MessageContentKind.text,
  durationSeconds: voice ? 4 : null,
);

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

  test('群回执只允许可信的群主和管理员，私聊不变，未知角色默认隐藏', () {
    for (final role in ['owner', 'admin', 'member', '', null]) {
      expect(
        canPresentMessageReceipts(conversation(role)),
        isGroupManager(role),
      );
      expect(
        canPresentMessageReceipts(conversation(role), roleTrusted: false),
        isFalse,
      );
      expect(
        canPresentMessageReceipts(
          conversation(role, direct: true),
          roleTrusted: false,
        ),
        isTrue,
      );
    }
    expect(canPresentMessageReceipts(null), isFalse);
  });

  for (final width in [390.0, 1280.0]) {
    for (final role in ['owner', 'admin', 'member', null]) {
      testWidgets('$width 群角色 $role 控制人数和已读状态，原始回执不清除', (tester) async {
        final c = AppController(_ReceiptRepository())
          ..conversations = [conversation(role)];
        addTearDown(c.dispose);
        final raw = message(1);
        await showPage(
          tester,
          Scaffold(
            body: MessageBubble(
              controller: c,
              message: raw,
              showGroupReceipt: true,
            ),
          ),
          width: width,
        );
        expect(
          find.byKey(const Key('group-receipt-summary')),
          isGroupManager(role) ? findsOneWidget : findsNothing,
        );
        expect(
          find.text('已送达 12 · 已读 7'),
          isGroupManager(role) ? findsOneWidget : findsNothing,
        );
        if (!isGroupManager(role)) {
          expect(find.text('已发送'), findsOneWidget);
          expect(find.textContaining('已读'), findsNothing);
          expect(find.textContaining('未读'), findsNothing);
        }
        expect(raw.status, MessageStatus.read);
        expect(raw.readCount, 7);
        expect(raw.deliveredCount, 12);
        expect(c.conversationUnreadCount, 3, reason: '自己的未读计数不受展示策略影响');
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets('普通成员保留发送中、已发送和失败重试，旧消息不显示单个已读标记', (tester) async {
    final c = AppController(_ReceiptRepository())
      ..conversations = [conversation('member')];
    addTearDown(c.dispose);
    var retries = 0;
    for (final status in [
      MessageStatus.sending,
      MessageStatus.sent,
      MessageStatus.delivered,
      MessageStatus.read,
      MessageStatus.failed,
    ]) {
      await showPage(
        tester,
        Scaffold(
          body: MessageBubble(
            controller: c,
            message: message(1, status: status),
            showGroupReceipt: false,
            onRetry: () => retries++,
          ),
        ),
      );
      expect(find.byKey(const Key('group-receipt-summary')), findsNothing);
      expect(find.textContaining('已读'), findsNothing);
      if (status == MessageStatus.sending) {
        expect(find.text('发送中'), findsOneWidget);
      } else if (status == MessageStatus.failed) {
        await tester.tap(find.byKey(const Key('failed-message-retry')));
        expect(retries, 1);
      } else {
        expect(find.text('已发送'), findsOneWidget);
      }
    }
  });

  testWidgets('私聊已读和送达保持原样，群主发送中不提前显示人数', (tester) async {
    final c = AppController(_ReceiptRepository())
      ..conversations = [conversation(null, direct: true)];
    addTearDown(c.dispose);
    for (final status in [MessageStatus.delivered, MessageStatus.read]) {
      await showPage(
        tester,
        Scaffold(
          body: MessageBubble(
            controller: c,
            message: message(1, status: status),
          ),
        ),
      );
      expect(
        find.text(status == MessageStatus.read ? '已读' : '已送达'),
        findsOneWidget,
      );
      expect(find.byKey(const Key('group-receipt-summary')), findsNothing);
    }
    c.conversations = [conversation('owner')];
    await showPage(
      tester,
      Scaffold(
        body: MessageBubble(
          controller: c,
          message: message(1, status: MessageStatus.sending),
          showGroupReceipt: true,
        ),
      ),
    );
    expect(find.text('发送中'), findsOneWidget);
    expect(find.byKey(const Key('group-receipt-summary')), findsNothing);
  });

  testWidgets('没有控制器授权的独立群气泡不显示回执', (tester) async {
    await showPage(
      tester,
      Scaffold(
        body: MessageBubble(message: message(1), showGroupReceipt: true),
      ),
    );
    expect(find.byKey(const Key('group-receipt-summary')), findsNothing);
    expect(find.text('已读'), findsNothing);
    expect(find.text('已发送'), findsOneWidget);
  });

  for (final role in ['owner', 'admin', 'member']) {
    testWidgets('真实聊天组件角色 $role 仅管理人员显示最新本人消息的回执', (tester) async {
      final repo = _ReceiptRepository()..role = role;
      final c = AppController(repo);
      addTearDown(c.dispose);
      await tester.runAsync(c.loginAsDemo);
      await showPage(
        tester,
        ChatScreen(controller: c, conversation: c.conversations.single),
      );
      expect(
        find.byKey(const Key('group-receipt-summary')),
        isGroupManager(role) ? findsOneWidget : findsNothing,
      );
      expect(
        find.text('已送达 12 · 已读 7'),
        isGroupManager(role) ? findsOneWidget : findsNothing,
      );
      if (role == 'member') {
        expect(find.textContaining('已读'), findsNothing);
        expect(find.text('已发送'), findsNWidgets(2));
      }
      expect(
        c
            .messagesFor('receipt-chat')
            .where((m) => m.isMine)
            .every((m) => m.readCount == 7),
        isTrue,
      );
    });
  }

  testWidgets('降级或角色重新同步立刻隐藏语音回执，不保留淡出动画，晋升后恢复', (tester) async {
    final repo = _ReceiptRepository()
      ..role = 'owner'
      ..voice = true;
    final c = AppController(repo);
    addTearDown(c.dispose);
    await tester.runAsync(c.loginAsDemo);
    await showPage(
      tester,
      ChatScreen(controller: c, conversation: c.conversations.single),
    );
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pumpAndSettle();
    expect(find.text('已送达 12 · 已读 7'), findsOneWidget);
    repo.role = 'member';
    repo.bus.add(
      const ImEvent(
        type: ImEventType.conversationChanged,
        payload: {'conversationId': 'receipt-chat'},
      ),
    );
    await tester.pump();
    expect(c.canDisplayMessageReceipts('receipt-chat'), isFalse);
    expect(
      find.textContaining('已读'),
      findsNothing,
      reason: '第一帧就移除旧语音回执，不等待动画或网络',
    );
    // The controller's subscriptions were created by loginAsDemo in runAsync,
    // so their debounced refresh uses the real async zone, not fake pump time.
    await tester.runAsync(() async {
      await Future<void>.delayed(const Duration(milliseconds: 250));
    });
    await tester.pumpAndSettle();
    expect(c.conversations.single.currentUserRole, 'member');
    expect(find.textContaining('已读'), findsNothing);
    repo.role = 'admin';
    repo.bus.add(
      const ImEvent(
        type: ImEventType.conversationChanged,
        payload: {'conversationId': 'receipt-chat'},
      ),
    );
    await tester.runAsync(() async {
      await Future<void>.delayed(const Duration(milliseconds: 250));
    });
    await tester.pumpAndSettle();
    expect(c.conversations.single.currentUserRole, 'admin');
    expect(c.canDisplayMessageReceipts('receipt-chat'), isTrue);
    expect(find.text('已送达 12 · 已读 7'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('普通成员收到读回执仍更新缓存，但不产生已读提示', (tester) async {
    final repo = _ReceiptRepository()..role = 'member';
    final c = AppController(repo);
    addTearDown(c.dispose);
    await tester.runAsync(c.loginAsDemo);
    await showPage(
      tester,
      ChatScreen(controller: c, conversation: c.conversations.single),
    );
    repo.bus.add(
      const ImEvent(
        type: ImEventType.messageRead,
        payload: {
          'conversationId': 'receipt-chat',
          'seq': 2,
          'deliveredCount': 20,
          'readCount': 15,
        },
      ),
    );
    await tester.pumpAndSettle();
    expect(
      c
          .messagesFor('receipt-chat')
          .where((m) => m.isMine)
          .every((m) => m.readCount == 15),
      isTrue,
    );
    expect(find.textContaining('已读'), findsNothing);
    expect(find.byKey(const Key('group-receipt-summary')), findsNothing);
    expect(find.text('已发送'), findsNWidgets(2));
  });
}

Future<void> showPage(
  WidgetTester tester,
  Widget child, {
  double width = 390,
}) async {
  tester.view.devicePixelRatio = 1;
  tester.view.physicalSize = Size(width, 844);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.view.resetPhysicalSize);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(width > 600 ? Brightness.dark : Brightness.light),
      home: child,
    ),
  );
  await tester.pumpAndSettle();
}

class _ReceiptRepository extends DemoImRepository {
  _ReceiptRepository() : super(latency: Duration.zero, store: _MemoryStore());
  String role = 'member';
  bool voice = false;
  final bus = StreamController<ImEvent>.broadcast(sync: true);
  @override
  Stream<ImEvent> get events => bus.stream;
  @override
  Future<List<Conversation>> conversations() async => [conversation(role)];
  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    message(1),
    message(2, voice: voice),
    message(3, mine: false),
  ];
  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}
  @override
  Future<void> close() async {
    await bus.close();
    await super.close();
  }
}

class _MemoryStore extends SecureLocalStore {
  final values = <String, Object>{};
  @override
  Future<void> writeJson(String key, Object value) async {
    values[key] = value;
  }

  @override
  Future<Object?> readJson(String key) async => values[key];
  @override
  Future<void> remove(String key) async {
    values.remove(key);
  }

  @override
  Future<void> clearAccountData() async {
    values.clear();
  }
}
