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
import 'package:linli_im/ui/screens/group_management_screens.dart';
import 'package:linli_im/ui/screens/group_invite_members_screen.dart';
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

  for (final role in ['owner', 'admin', 'member']) {
    for (final width in [390.0, 1280.0]) {
      testWidgets('$role / $width 群成员头像打开资料时按当前角色显示呱呱号', (tester) async {
        final repo = _Repository(role);
        await _open(tester, repo, width: width);
        await tester.tap(find.byKey(const Key('chat-info-member-u1')));
        await tester.pumpAndSettle();
        expect(find.byType(FriendProfileScreen), findsOneWidget);
        expect(
          find.text('@linyu'),
          role == 'member' ? findsNothing : findsNWidgets(2),
        );
        expect(
          find.byKey(const Key('friend-profile-handle')),
          role == 'member' ? findsNothing : findsOneWidget,
        );
        expect(find.text('发消息'), findsOneWidget, reason: '好友关系不改变群内隐藏规则');
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
      });
    }

    testWidgets('$role 群成员列表和 @ 面板不绕过呱呱号限制', (tester) async {
      final repo = _Repository(role);
      final controller = await _open(tester, repo, screen: 'members');
      await tester.enterText(
        find.byKey(const Key('group-member-search')),
        'linyu',
      );
      await tester.pump();
      expect(
        find.byKey(const Key('group-member-u1')),
        role == 'member' ? findsNothing : findsOneWidget,
      );
      await tester.enterText(
        find.byKey(const Key('group-member-search')),
        '林屿',
      );
      await tester.pump();
      await tester.tap(find.byKey(const Key('group-member-u1')));
      await tester.pumpAndSettle();
      expect(
        find.text('@linyu'),
        role == 'member' ? findsNothing : findsNWidgets(2),
      );
      await tester.pumpWidget(const SizedBox());
      await tester.pumpWidget(
        MaterialApp(
          theme: buildLinliTheme(Brightness.light),
          home: ChatScreen(
            controller: controller,
            conversation: controller.conversations.firstWhere(
              (c) => c.id == 'c-team',
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('mention-member-button')));
      await tester.pumpAndSettle();
      expect(
        find.text('@linyu'),
        role == 'member' ? findsNothing : findsOneWidget,
      );
      await tester.enterText(
        find.byKey(const Key('mention-member-search')),
        'linyu',
      );
      await tester.pump();
      expect(
        find.byKey(const Key('mention-member-u1')),
        role == 'member' ? findsNothing : findsOneWidget,
      );
      await tester.pumpWidget(const SizedBox());
    });
  }

  testWidgets('降级或角色同步期间立即隐藏，通讯录查看仍保留呱呱号', (tester) async {
    final repo = _Repository('admin');
    final controller = await _open(tester, repo);
    await tester.tap(find.byKey(const Key('chat-info-member-u1')));
    await tester.pumpAndSettle();
    expect(find.text('@linyu'), findsNWidgets(2));
    repo.role = 'member';
    await tester.runAsync(() async {
      repo.updates.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: {'conversationId': 'c-team', 'groupSendPolicyChanged': true},
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 5));
    });
    await tester.pump();
    expect(find.text('@linyu'), findsNothing);
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 180)),
    );
    await tester.pumpAndSettle();
    expect(find.text('@linyu'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    await tester.pumpWidget(
      MaterialApp(
        home: FriendProfileScreen(
          controller: controller,
          user: DemoImRepository.people.first,
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('@linyu'), findsNWidgets(2));
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('缺失群上下文不展示呱呱号', (tester) async {
    final repo = _Repository('owner');
    final controller = await _open(tester, repo);
    await tester.pumpWidget(const SizedBox());
    await tester.pumpWidget(
      MaterialApp(
        home: FriendProfileScreen(
          controller: controller,
          user: DemoImRepository.people.first,
          requestSource: 'group',
          requestSourceId: 'missing',
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('@linyu'), findsNothing);
    expect(controller.canViewGroupMemberHandle(null), isFalse);
    await tester.pumpWidget(const SizedBox());
  });

  for (final role in ['owner', 'admin', 'member']) {
    for (final width in [390.0, 1280.0]) {
      testWidgets('$role / $width 点击加号直接多选完成，搜索保留选择', (tester) async {
        final repo = _Repository(role);
        await _open(
          tester,
          repo,
          width: width,
          textScale: width > 600 ? 1 : 1.8,
        );
        await tester.tap(find.byKey(const Key('chat-info-invite-members')));
        await tester.pumpAndSettle();
        expect(find.byType(GroupInviteMembersScreen), findsOneWidget);
        expect(find.byType(GroupManagementScreen), findsNothing);
        expect(find.byType(GroupMembersManagementScreen), findsNothing);
        expect(
          tester
              .widget<CheckboxListTile>(
                find.byKey(const Key('group-invite-user-u1')),
              )
              .onChanged,
          isNull,
        );
        await tester.enterText(
          find.byKey(const Key('group-invite-search')),
          'anran',
        );
        await tester.pump();
        await tester.tap(find.byKey(const Key('group-invite-user-u2')));
        await tester.pump();
        await tester.enterText(
          find.byKey(const Key('group-invite-search')),
          '周末',
        );
        await tester.pump();
        await tester.tap(find.byKey(const Key('group-invite-user-u3')));
        await tester.pump();
        expect(find.text('完成 2'), findsOneWidget);
        expect(repo.added, isEmpty);
        expect(repo.invited, isEmpty);
        await tester.tap(find.byKey(const Key('group-invite-confirm')));
        await tester.pumpAndSettle();
        if (role == 'member') {
          expect(repo.invited, ['u2', 'u3']);
          expect(repo.added, isEmpty);
        } else {
          expect(repo.added, [
            ['u2', 'u3'],
          ]);
          expect(repo.invited, isEmpty);
        }
        expect(find.byType(GroupInviteMembersScreen), findsNothing);
        expect(find.byType(ChatInfoScreen), findsOneWidget);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
      });
    }
  }

  testWidgets('邀请成员支持全选当前搜索结果，搜索外选择保持不变', (tester) async {
    final repo = _Repository('owner');
    await _open(tester, repo);
    await tester.tap(find.byKey(const Key('chat-info-invite-members')));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('group-invite-select-all')));
    await tester.pump();
    expect(find.text('已选 5'), findsOneWidget);
    expect(find.text('完成 5'), findsOneWidget);
    expect(find.text('取消全选'), findsOneWidget);

    await tester.tap(find.byKey(const Key('group-invite-select-all')));
    await tester.pump();
    expect(find.text('已选 0'), findsOneWidget);

    await tester.enterText(
      find.byKey(const Key('group-invite-search')),
      'anran',
    );
    await tester.pump();
    await tester.tap(find.byKey(const Key('group-invite-select-all')));
    await tester.pump();
    expect(find.text('已选 1'), findsOneWidget);

    await tester.enterText(find.byKey(const Key('group-invite-search')), '周末');
    await tester.pump();
    expect(find.text('全选'), findsOneWidget);
    await tester.tap(find.byKey(const Key('group-invite-select-all')));
    await tester.pump();
    expect(find.text('已选 2'), findsOneWidget);
    expect(find.text('取消全选'), findsOneWidget);

    await tester.tap(find.byKey(const Key('group-invite-select-all')));
    await tester.pump();
    expect(find.text('已选 1'), findsOneWidget);
    expect(find.text('完成 1'), findsOneWidget);

    await tester.tap(find.byKey(const Key('group-invite-confirm')));
    await tester.pumpAndSettle();
    expect(repo.added, [
      ['u2'],
    ]);
    expect(find.byType(GroupInviteMembersScreen), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('原成员列表添加入口复用新选择页；取消不发送', (tester) async {
    final repo = _Repository('member');
    await _open(tester, repo, screen: 'members');
    await tester.tap(find.byKey(const Key('add-group-members')));
    await tester.pumpAndSettle();
    expect(find.byType(GroupInviteMembersScreen), findsOneWidget);
    await tester.tap(find.byKey(const Key('group-invite-user-u2')));
    await tester.pageBack();
    await tester.pumpAndSettle();
    expect(repo.invited, isEmpty);
    expect(repo.added, isEmpty);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('部分邀请失败只重试失败者，成功者不重复邀请', (tester) async {
    final repo = _Repository('member')..failInvite.add('u3');
    await _open(tester, repo);
    await tester.tap(find.byKey(const Key('chat-info-invite-members')));
    await tester.pumpAndSettle();
    for (final id in ['u2', 'u3']) {
      await tester.tap(find.byKey(Key('group-invite-user-$id')));
      await tester.pump();
    }
    await tester.tap(find.byKey(const Key('group-invite-confirm')));
    await tester.pumpAndSettle();
    expect(repo.invited, ['u2', 'u3']);
    expect(find.textContaining('已完成 1 人，1 人失败'), findsOneWidget);
    expect(find.text('完成 1'), findsOneWidget);
    expect(
      tester
          .widget<CheckboxListTile>(
            find.byKey(const Key('group-invite-user-u2')),
          )
          .onChanged,
      isNull,
    );
    repo.failInvite.clear();
    await tester.tap(find.byKey(const Key('group-invite-confirm')));
    await tester.pumpAndSettle();
    expect(repo.invited, ['u2', 'u3', 'u3']);
    expect(find.byType(GroupInviteMembersScreen), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('提交重查群成员，忽略选择期间已入群的人；重复点击不重复添加', (tester) async {
    final repo = _Repository('owner')..pendingAdd = Completer<void>();
    await _open(tester, repo);
    await tester.tap(find.byKey(const Key('chat-info-invite-members')));
    await tester.pumpAndSettle();
    for (final id in ['u2', 'u3']) {
      await tester.tap(find.byKey(Key('group-invite-user-$id')));
      await tester.pump();
    }
    repo.joined.add('u2');
    await tester.tap(find.byKey(const Key('group-invite-confirm')));
    await tester.pump();
    expect(repo.added, [
      ['u3'],
    ]);
    expect(
      tester
          .widget<TextButton>(find.byKey(const Key('group-invite-confirm')))
          .onPressed,
      isNull,
    );
    await tester.tap(find.byKey(const Key('group-invite-confirm')));
    await tester.pump();
    expect(repo.added, hasLength(1));
    repo.pendingAdd!.complete();
    await tester.pumpAndSettle();
    expect(find.byType(GroupInviteMembersScreen), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('成员信息失败可重试，退出登录后停止后续邀请', (tester) async {
    final repo = _Repository('member')..failRead = true;
    final controller = await _open(tester, repo);
    await tester.tap(find.byKey(const Key('chat-info-invite-members')));
    await tester.pumpAndSettle();
    expect(find.text('重新加载'), findsOneWidget);
    expect(
      tester
          .widget<TextButton>(find.byKey(const Key('group-invite-confirm')))
          .onPressed,
      isNull,
    );
    repo.failRead = false;
    await tester.tap(find.text('重新加载'));
    await tester.pumpAndSettle();
    for (final id in ['u2', 'u3']) {
      await tester.tap(find.byKey(Key('group-invite-user-$id')));
      await tester.pump();
    }
    repo.pendingInvite = Completer<void>();
    await tester.tap(find.byKey(const Key('group-invite-confirm')));
    await tester.pump();
    expect(repo.invited, ['u2']);
    controller.authenticated = false;
    controller.currentUser = null;
    controller.notifyListeners();
    repo.pendingInvite!.complete();
    await tester.pumpAndSettle();
    expect(repo.invited, ['u2']);
    expect(find.text('登录状态已变化，请返回重试'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });
}

Future<AppController> _open(
  WidgetTester tester,
  _Repository repo, {
  String screen = 'info',
  double width = 390,
  double textScale = 1,
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
  final group = controller.conversations.firstWhere((c) => c.id == 'c-team');
  final initialMembers = screen == 'members'
      ? await repo.groupMembers(group.id)
      : <GroupMember>[];
  final profile = await repo.groupProfile(group.id);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(width > 600 ? Brightness.dark : Brightness.light),
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: TextScaler.linear(textScale)),
        child: child!,
      ),
      home: screen == 'members'
          ? GroupMembersManagementScreen(
              controller: controller,
              conversationId: group.id,
              profile: profile,
              initialMembers: initialMembers,
            )
          : ChatInfoScreen(
              controller: controller,
              conversation: group,
              onSearch: () {},
              onClearLocal: () async {},
              onBlock: () async {},
              onScheduledMessages: () {},
            ),
    ),
  );
  await tester.pumpAndSettle();
  return controller;
}

class _Repository extends DemoImRepository {
  _Repository(this.role) : super(latency: Duration.zero, store: _MemoryStore());
  String role;
  final joined = <String>{'u1'};
  final added = <List<String>>[];
  final invited = <String>[];
  final failInvite = <String>{};
  final updates = StreamController<ImEvent>.broadcast();
  Completer<void>? pendingAdd;
  Completer<void>? pendingInvite;
  bool failRead = false;
  @override
  Future<GroupProfile> groupProfile(String conversationId) async =>
      GroupProfile(
        conversationId: conversationId,
        ownerId: role == 'owner' ? 'me' : 'u1',
        name: '群成员测试群',
        announcement: '',
        announcementVersion: 0,
        joinPolicy: 'invite',
        allowMemberAddFriend: true,
        updatedAt: DateTime(2026),
      );
  @override
  Stream<ImEvent> get events => updates.stream;
  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async {
    if (failRead) throw const FormatException('群成员加载失败');
    return [
      GroupMember(
        user: DemoImRepository.demoUser,
        role: role,
        joinedAt: DateTime(2026),
      ),
      for (final user in DemoImRepository.people.where(
        (u) => joined.contains(u.id),
      ))
        GroupMember(
          user: user,
          role: role == 'owner' ? 'member' : 'owner',
          joinedAt: DateTime(2026),
        ),
    ];
  }

  @override
  Future<List<Conversation>> conversations() async => [
    for (final c in await super.conversations())
      c.id == 'c-team'
          ? c.copyWith(
              currentUserRole: role,
              members: [
                DemoImRepository.demoUser,
                ...DemoImRepository.people.where((u) => joined.contains(u.id)),
              ],
            )
          : c,
  ];
  @override
  Future<void> addGroupMembers(
    String conversationId,
    List<String> userIds,
  ) async {
    added.add(List.of(userIds));
    await pendingAdd?.future;
    joined.addAll(userIds);
  }

  @override
  Future<void> inviteGroupMember(String conversationId, String userId) async {
    invited.add(userId);
    await pendingInvite?.future;
    if (failInvite.contains(userId)) throw const FormatException('邀请失败，请稍后再试');
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
}
