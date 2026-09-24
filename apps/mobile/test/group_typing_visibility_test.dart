import 'dart:async';

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
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  const record = MethodChannel('com.llfbandit.record/messages');
  setUpAll(() => messenger.setMockMethodCallHandler(record, (_) async => null));
  tearDownAll(() => messenger.setMockMethodCallHandler(record, null));
  setUp(() => SharedPreferences.setMockInitialValues({}));

  Future<AppController> login(
    WidgetTester tester,
    _TypingRepository repo,
  ) async {
    final controller = AppController(repo);
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);
    await tester.runAsync(controller.refreshProfile);
    return controller;
  }

  Future<void> refreshed(WidgetTester tester) async {
    // Event subscriptions originate in runAsync; their debounce runs in real time.
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 300)),
    );
    await tester.pump();
  }

  for (final width in [390.0, 1280.0]) {
    for (final (role, internal, allowed) in [
      ('owner', false, true),
      ('admin', false, true),
      ('member', true, true),
      ('member', false, false),
      (null, true, false),
    ]) {
      testWidgets('$width 群输入提示 role=$role internal=$internal', (tester) async {
        final repo = _TypingRepository()
          ..role = role
          ..internal = internal;
        final c = await login(tester, repo);
        tester.view.physicalSize = Size(width, 844);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        await tester.pumpWidget(
          MaterialApp(
            theme: buildLinliTheme(
              width > 600 ? Brightness.dark : Brightness.light,
            ),
            home: ChatScreen(
              controller: c,
              conversation: c.conversations.firstWhere(
                (item) => item.id == 'typing-group',
              ),
            ),
          ),
        );
        await tester.pumpAndSettle();
        repo.typing();
        await tester.pump();
        expect(c.canViewTyping('typing-group'), allowed);
        expect(find.text('林屿 正在输入…'), allowed ? findsOneWidget : findsNothing);
        expect(find.text('8 位成员'), allowed ? findsNothing : findsOneWidget);
        repo.typing(userId: 'u2');
        await tester.pump();
        expect(
          find.textContaining('等正在输入…'),
          allowed ? findsOneWidget : findsNothing,
        );
        repo.typing(active: false);
        repo.typing(userId: 'u2', active: false);
        await tester.pump();
        expect(find.textContaining('正在输入'), findsNothing);
        expect(find.text('8 位成员'), findsOneWidget);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
      });
    }
  }

  testWidgets('普通成员仍上报输入，单聊提示不受内部权限影响，无权事件不缓存', (tester) async {
    final repo = _TypingRepository();
    final c = await login(tester, repo);
    c.updateTyping('typing-group', true);
    c.updateTyping('typing-group', true);
    c.updateTyping('typing-group', false);
    expect(repo.sentTyping, [true, false]);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNull);
    // Receiving an unauthorized hint must not keep it for a later grant.
    c.currentUser = c.currentUser!.copyWith(isInternalUser: true);
    expect(c.typingLabelFor('typing-group'), isNull);
    c.currentUser = c.currentUser!.copyWith(isInternalUser: false);
    repo.typing(cid: 'typing-direct');
    expect(c.typingLabelFor('typing-direct'), '正在输入…');
    repo.typing(cid: 'typing-direct', active: false);
    expect(c.typingLabelFor('typing-direct'), isNull);
    repo.typing(cid: 'unknown');
    repo.typing(userId: 'me');
    expect(c.typingLabelFor('unknown'), isNull);
    expect(c.typingLabelFor('typing-group'), isNull);
  });

  testWidgets('撤权立即清理、迟到事件不复活，管理员保留独立授权', (tester) async {
    final repo = _TypingRepository()..internal = true;
    final c = await login(tester, repo);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNotNull);
    repo.permission(false);
    expect(c.typingLabelFor('typing-group'), isNull);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNull);
    repo.permission(true);
    expect(c.typingLabelFor('typing-group'), isNull);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNotNull);
    repo.role = 'admin';
    repo.groupChanged();
    expect(c.typingLabelFor('typing-group'), isNull);
    await refreshed(tester);
    repo.permission(false);
    expect(c.canViewTyping('typing-group'), isTrue);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNotNull);
    // A profile refresh without a push event applies the same invalidation.
    repo.role = 'member';
    repo.groupChanged();
    await refreshed(tester);
    repo.permission(true);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNotNull);
    repo.internal = false;
    await tester.runAsync(c.refreshProfile);
    expect(c.typingLabelFor('typing-group'), isNull);
  });

  testWidgets('升降级、群主转让、退群和角色刷新失败均清掉旧提示', (tester) async {
    final repo = _TypingRepository()..role = 'owner';
    final c = await login(tester, repo);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNotNull);
    repo.role = 'member';
    repo.groupChanged();
    repo.typing(); // Late event while the role is untrusted.
    expect(c.canViewTyping('typing-group'), isFalse);
    expect(c.typingLabelFor('typing-group'), isNull);
    await refreshed(tester);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNull);
    for (final role in ['admin', 'owner']) {
      repo.role = role;
      repo.groupChanged();
      expect(c.canViewTyping('typing-group'), isFalse);
      await refreshed(tester);
      expect(c.typingLabelFor('typing-group'), isNull);
      repo.typing();
      expect(c.typingLabelFor('typing-group'), isNotNull);
    }
    repo.failRoles = true;
    repo.groupChanged();
    await refreshed(tester);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNull);
    repo.failRoles = false;
    repo.left = true;
    repo.groupChanged();
    await refreshed(tester);
    repo.typing();
    expect(c.canViewTyping('typing-group'), isFalse);
    expect(c.typingLabelFor('typing-group'), isNull);
  });

  testWidgets('提示过期，登出和切换账号不保留旧缓存', (tester) async {
    final repo = _TypingRepository()..internal = true;
    final c = await login(tester, repo);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNotNull);
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 6100)),
    );
    expect(c.typingLabelFor('typing-group'), isNull);
    repo.typing();
    await tester.runAsync(c.logout);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNull);
    repo.internal = false;
    await tester.runAsync(c.loginAsDemo);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNull);
    repo.permission(true);
    expect(c.typingLabelFor('typing-group'), isNull);
    repo.typing();
    expect(c.typingLabelFor('typing-group'), isNotNull);
  });
}

class _TypingRepository extends DemoImRepository {
  _TypingRepository() : super(latency: Duration.zero);
  final bus = StreamController<ImEvent>.broadcast(sync: true);
  bool internal = false;
  String? role = 'member';
  bool left = false;
  bool failRoles = false;
  final sentTyping = <bool>[];

  @override
  Stream<ImEvent> get events => bus.stream;
  @override
  Future<AppUser> profile() async =>
      DemoImRepository.demoUser.copyWith(isInternalUser: internal);
  @override
  Future<List<Conversation>> conversations() async {
    if (failRoles) throw StateError('Group role unavailable');
    return [
      if (!left)
        Conversation(
          id: 'typing-group',
          title: '输入提示测试群',
          subtitle: '',
          updatedAt: DateTime(2026),
          kind: ConversationKind.group,
          channelType: 2,
          memberCount: 8,
          currentUserRole: role,
          members: DemoImRepository.people.take(2).toList(),
        ),
      Conversation(
        id: 'typing-direct',
        title: '林屿',
        subtitle: '',
        updatedAt: DateTime(2026),
        kind: ConversationKind.direct,
        channelType: 1,
        members: [DemoImRepository.people.first],
      ),
    ];
  }

  void typing({
    String cid = 'typing-group',
    String userId = 'u1',
    bool active = true,
  }) => bus.add(
    ImEvent(
      type: ImEventType.typing,
      payload: {'conversationId': cid, 'userId': userId, 'typing': active},
    ),
  );

  void permission(bool allowed) {
    internal = allowed;
    bus.add(
      ImEvent(
        type: ImEventType.messagePermissionsChanged,
        payload: {'isInternalUser': allowed},
      ),
    );
  }

  void groupChanged() => bus.add(
    const ImEvent(
      type: ImEventType.conversationChanged,
      payload: {'conversationId': 'typing-group'},
    ),
  );

  @override
  Future<void> setTyping(String conversationId, bool typing) async {
    sentTyping.add(typing);
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [];

  @override
  Future<void> close() async {
    await bus.close();
    await super.close();
  }
}
