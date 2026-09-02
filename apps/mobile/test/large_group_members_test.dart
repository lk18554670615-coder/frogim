import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/relationship_screens.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  const record = MethodChannel('com.llfbandit.record/messages');
  setUpAll(() => messenger.setMockMethodCallHandler(record, (_) async => null));
  tearDownAll(() => messenger.setMockMethodCallHandler(record, null));
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('成员请求期间遇到连接/成员失效通知，自动重取一次而不是把旧资料交给选择页', () async {
    final repo = _Repository();
    final controller = AppController(repo);
    await controller.loginAsDemo();
    addTearDown(() async {
      controller.dispose();
      await repo.updates.close();
    });
    final pending = Completer<List<GroupMember>>();
    repo.blockedRead = pending;
    final before = repo.memberCalls;
    final operation = controller.loadGroupMembers('c-team');
    final stale = List.of(repo.members);
    repo.updates.add(
      const ImEvent(
        type: ImEventType.conversationChanged,
        payload: {'conversationId': 'c-team', 'groupSendPolicyChanged': true},
      ),
    );
    await Future<void>.delayed(Duration.zero);
    repo.members.add(_member(11));
    pending.complete(stale);
    expect((await operation)!.length, 12);
    expect(repo.memberCalls - before, 2);
    expect(controller.cachedGroupMembers('c-team')!.length, 12);
  });

  for (final width in [390.0, 1280.0]) {
    testWidgets('$width：11人群的预览外非好友显示昵称头像，可打开资料及 @', (tester) async {
      final repo = _Repository();
      final controller = await _open(tester, repo, width: width);
      expect(controller.conversations.single.members.length, 8);
      expect(controller.conversations.single.memberCount, 11);
      expect(controller.cachedGroupMembers('c-team')!.length, 11);
      for (final i in [8, 9, 10]) {
        final bubble = tester
            .widgetList<MessageBubble>(find.byType(MessageBubble))
            .firstWhere((b) => b.message.senderId == 'g$i');
        expect(bubble.senderName, '群友$i');
        expect(bubble.avatarUrl, 'assets/avatars/an-ran.png');
        expect(bubble.onAvatarTap, isNotNull);
      }
      expect(find.text('对方'), findsNothing);
      await tester.tap(find.byKey(const Key('message-avatar-msg10')));
      await tester.pumpAndSettle();
      final profile = tester.widget<FriendProfileScreen>(
        find.byType(FriendProfileScreen),
      );
      expect(profile.user.id, 'g10');
      expect(profile.user.name, '群友10');
      expect(find.byKey(const Key('friend-profile-handle')), findsNothing);
      await tester.pageBack();
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('mention-member-button')));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const Key('mention-member-search')),
        '群友10',
      );
      await tester.pump();
      expect(find.byKey(const Key('mention-member-g10')), findsOneWidget);
      await tester.tap(find.byKey(const Key('mention-member-g10')));
      await tester.pumpAndSettle();
      expect(find.textContaining('@群友10'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    });

    testWidgets('$width：查看全部11位成员直接打开完整列表，搜索末尾成员并查看资料', (tester) async {
      final repo = _Repository();
      await _open(tester, repo, width: width, info: true);
      expect(find.text('查看全部 11 位成员'), findsOneWidget);
      expect(find.byKey(const Key('chat-info-member-g8')), findsOneWidget);
      expect(
        find.byKey(const Key('chat-info-member-g10')),
        findsNothing,
        reason: '顶部保留简洁预览',
      );
      await tester.tap(find.byKey(const Key('chat-info-all-members')));
      await tester.pumpAndSettle();
      expect(find.text('群成员 · 11'), findsOneWidget);
      await tester.enterText(
        find.byKey(const Key('group-member-search')),
        '群友10',
      );
      await tester.pump();
      await tester.tap(find.byKey(const Key('group-member-g10')));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<FriendProfileScreen>(find.byType(FriendProfileScreen))
            .user
            .id,
        'g10',
      );
      expect(find.byKey(const Key('friend-profile-handle')), findsNothing);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    });
  }

  testWidgets('成员资料加载失败后点击头像重试，不永久禁用；返回后普通刷新不覆盖完整缓存', (tester) async {
    final repo = _Repository()..fail = true;
    final controller = await _open(tester, repo);
    expect(find.text('对方'), findsNothing);
    await tester.tap(find.byKey(const Key('message-avatar-msg10')));
    await tester.pumpAndSettle();
    expect(find.text('暂时无法获取该成员资料，请稍后重试'), findsOneWidget);
    repo.fail = false;
    await tester.tap(find.byKey(const Key('message-avatar-msg10')));
    await tester.pumpAndSettle();
    expect(find.byType(FriendProfileScreen), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle();
    await tester.runAsync(() => controller.refresh());
    await tester.pumpAndSettle();
    expect(controller.conversations.single.members.length, 8);
    expect(controller.cachedGroupMembers('c-team')!.length, 11);
    expect(find.text('群友10'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('新成员消息早于CMD时补查昵称；成员变更替换快照并保留好友备注优先', (tester) async {
    final repo = _Repository();
    final controller = await _open(tester, repo);
    final late = _member(11);
    repo.members.add(late);
    repo.chat.add(_message(11));
    await tester.runAsync(() async {
      repo.updates.add(
        ImEvent(
          type: ImEventType.messageCreated,
          payload: {'message': _message(11).toJson()},
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));
    });
    await tester.pumpAndSettle();
    expect(
      controller.messageSenderFor(controller.conversations.single, 'g11')?.name,
      '群友11',
    );
    expect(find.text('群友11'), findsOneWidget);
    controller.contacts = [late.user.copyWith(remark: '好友备注')];
    controller.notifyListeners();
    await tester.pump();
    expect(find.text('好友备注'), findsOneWidget);
    repo.members.removeWhere((m) => m.user.id == 'g9');
    await tester.runAsync(() async {
      repo.updates.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: {'conversationId': 'c-team', 'groupSendPolicyChanged': true},
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));
    });
    await tester.pumpAndSettle();
    expect(controller.groupMemberFor('c-team', 'g9'), isNull);
    expect(controller.groupMemberFor('c-team', 'g11'), isNotNull);
    await tester.runAsync(controller.logout);
    expect(controller.cachedGroupMembers('c-team'), isNull);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('聊天信息加载失败保留预览和全部入口，并可原地重试', (tester) async {
    final repo = _Repository()..fail = true;
    final controller = await _open(tester, repo, info: true);
    expect(find.text('查看全部 11 位成员'), findsOneWidget);
    expect(find.byKey(const Key('chat-info-members-retry')), findsOneWidget);
    repo.fail = false;
    await tester.tap(find.byKey(const Key('chat-info-members-retry')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('chat-info-members-retry')), findsNothing);
    expect(controller.cachedGroupMembers('c-team')!.length, 11);
    await tester.pumpWidget(const SizedBox());
  });
}

GroupMember _member(int i) => GroupMember(
  user: i == 0
      ? DemoImRepository.demoUser
      : AppUser(
          id: 'g$i',
          name: '群友$i',
          handle: 'handle$i',
          presence: '',
          avatarUrl: 'assets/avatars/an-ran.png',
        ),
  role: i == 1 ? 'owner' : 'member',
  joinedAt: DateTime(2026),
);
ChatMessage _message(int i) => ChatMessage(
  id: 'msg$i',
  clientMessageId: 'msg$i',
  conversationId: 'c-team',
  senderId: 'g$i',
  senderName: '',
  text: '来自第${i + 1}位成员的消息',
  sentAt: DateTime.now(),
  isMine: false,
  conversationSeq: i + 1,
  status: MessageStatus.sent,
);

Future<AppController> _open(
  WidgetTester tester,
  _Repository repo, {
  double width = 390,
  bool info = false,
}) async {
  final controller = AppController(repo);
  await tester.runAsync(controller.loginAsDemo);
  addTearDown(() async {
    controller.dispose();
    await repo.updates.close();
  });
  tester.view.physicalSize = Size(width, 1100);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final group = controller.conversations.single;
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(width > 600 ? Brightness.dark : Brightness.light),
      home: info
          ? ChatInfoScreen(
              controller: controller,
              conversation: group,
              onSearch: () {},
              onClearLocal: () async {},
              onBlock: () async {},
              onScheduledMessages: () {},
            )
          : ChatScreen(controller: controller, conversation: group),
    ),
  );
  await tester.pumpAndSettle();
  return controller;
}

class _Repository extends DemoImRepository {
  _Repository() : super(latency: Duration.zero, store: _Store());
  final updates = StreamController<ImEvent>.broadcast();
  final members = List.generate(11, _member);
  final chat = [
    for (final i in [8, 9, 10]) _message(i),
  ];
  bool fail = false;
  Completer<List<GroupMember>>? blockedRead;
  int memberCalls = 0;
  @override
  Stream<ImEvent> get events => updates.stream;
  @override
  Future<List<Conversation>> conversations() async => [
    (await super.conversations())
        .firstWhere((c) => c.id == 'c-team')
        .copyWith(
          members: members.take(8).map((m) => m.user).toList(),
          memberCount: members.length,
          currentUserRole: 'member',
        ),
  ];
  @override
  Future<List<GroupMember>> groupMembers(String id) async {
    memberCalls++;
    final blocked = blockedRead;
    if (blocked != null) {
      blockedRead = null;
      return blocked.future;
    }
    if (fail) throw const FormatException('成员资料暂不可用');
    return List.of(members);
  }

  @override
  Future<GroupProfile> groupProfile(String id) async => GroupProfile(
    conversationId: id,
    ownerId: 'g1',
    name: '11人测试群',
    announcement: '',
    announcementVersion: 0,
    joinPolicy: 'invite',
    allowMemberAddFriend: true,
    updatedAt: DateTime(2026),
  );
  @override
  Future<List<ChatMessage>> messages(String id) async => List.of(chat);
}

class _Store extends SecureLocalStore {
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
}
