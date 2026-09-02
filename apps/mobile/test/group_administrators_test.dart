import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/ui/screens/group_management_screens.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('设置请求晚于会话销毁返回时不再刷新已销毁的控制器', () async {
    final repository = _RoleRepository()..pendingChange = Completer<void>();
    final controller = AppController(repository);
    await controller.loginAsDemo();
    final result = controller.setGroupRole(
      'c-team',
      DemoImRepository.people.first,
      'admin',
    );
    controller.dispose();
    repository.pendingChange!.complete();
    expect(await result, isFalse);
  });

  for (final width in [390.0, 1280.0]) {
    testWidgets('$width 群资料可直接设置和取消管理员，保留群成员身份', (tester) async {
      final repository = _RoleRepository();
      await _open(tester, repository, overview: true, width: width);
      final entry = find.byKey(const Key('group-administrators-entry'));
      await tester.ensureVisible(entry);
      await tester.tap(entry);
      await tester.pumpAndSettle();
      expect(find.text('群管理员 · 1'), findsOneWidget);
      expect(find.byKey(const Key('add-group-members')), findsNothing);
      expect(find.byKey(const Key('group-admin-switch-me')), findsNothing);

      final toggle = find.byKey(const Key('group-admin-switch-u1'));
      expect(tester.widget<CupertinoSwitch>(toggle).value, isFalse);
      await tester.tap(toggle);
      await tester.pump();
      expect(repository.changes, isEmpty);
      await tester.tap(find.byKey(const Key('confirm-group-administrator')));
      await tester.pumpAndSettle();
      expect(repository.changes, [('u1', 'admin')]);
      expect(tester.widget<CupertinoSwitch>(toggle).value, isTrue);
      expect(find.text('群管理员 · 2'), findsOneWidget);
      expect(find.text('已将“林屿”设为管理员'), findsOneWidget);

      await tester.tap(toggle);
      await tester.pump();
      expect(find.text('取消管理员'), findsOneWidget);
      await tester.tap(find.byKey(const Key('confirm-group-administrator')));
      await tester.pumpAndSettle();
      expect(repository.changes, [('u1', 'admin'), ('u1', 'member')]);
      expect(repository.roles['u1'], 'member');
      expect(repository.roles.length, 4);
      expect(tester.widget<CupertinoSwitch>(toggle).value, isFalse);
      expect(find.text('群管理员 · 1'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pageBack();
      await tester.pumpAndSettle();
      expect(find.text('1 位管理员 · 仅群主可设置'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  testWidgets('取消确认不提交，接口失败不假装设置成功且能重试', (tester) async {
    final repository = _RoleRepository()..fail = true;
    await _open(tester, repository);
    final toggle = find.byKey(const Key('group-admin-switch-u1'));
    await tester.tap(toggle);
    await tester.pump();
    await tester.tap(find.widgetWithText(CupertinoDialogAction, '取消'));
    await tester.pumpAndSettle();
    expect(repository.changes, isEmpty);

    await tester.tap(toggle);
    await tester.pump();
    await tester.tap(find.byKey(const Key('confirm-group-administrator')));
    await tester.pumpAndSettle();
    expect(tester.widget<CupertinoSwitch>(toggle).value, isFalse);
    expect(find.text('当前无权设置群管理员'), findsOneWidget);

    repository.fail = false;
    await tester.tap(toggle);
    await tester.pump();
    await tester.tap(find.byKey(const Key('confirm-group-administrator')));
    await tester.pumpAndSettle();
    expect(tester.widget<CupertinoSwitch>(toggle).value, isTrue);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('提交期间开关禁用，不会重复设置', (tester) async {
    final repository = _RoleRepository()..pendingChange = Completer<void>();
    await _open(tester, repository);
    final toggle = find.byKey(const Key('group-admin-switch-u1'));
    await tester.tap(toggle);
    await tester.pump();
    await tester.tap(find.byKey(const Key('confirm-group-administrator')));
    await tester.pump(const Duration(milliseconds: 300));
    expect(repository.changes, hasLength(1));
    expect(tester.widget<CupertinoSwitch>(toggle).onChanged, isNull);
    await tester.tap(toggle);
    await tester.pump();
    expect(repository.changes, hasLength(1));
    repository.pendingChange!.complete();
    await tester.pumpAndSettle();
    expect(tester.widget<CupertinoSwitch>(toggle).value, isTrue);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  for (final role in ['admin', 'member']) {
    testWidgets('$role 只能查看群主和管理员，不能任命或取消', (tester) async {
      final repository = _RoleRepository(currentRole: role);
      await _open(tester, repository);
      expect(find.byType(CupertinoSwitch), findsNothing);
      expect(find.byTooltip('管理成员'), findsNothing);
      expect(find.byKey(const Key('group-member-u1')), findsNothing);
      expect(find.byKey(const Key('group-member-u2')), findsOneWidget);
      expect(find.byKey(const Key('group-member-u3')), findsOneWidget);
      expect(repository.changes, isEmpty);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  testWidgets('支持昵称/备注搜索、空结果和大字体，权限变更后更新可操作项', (tester) async {
    final repository = _RoleRepository();
    final controller = await _open(
      tester,
      repository,
      width: 390,
      textScale: 2,
    );
    final search = find.byKey(const Key('group-member-search'));
    await tester.enterText(search, 'linyu');
    await tester.pump();
    expect(find.byKey(const Key('group-member-u1')), findsOneWidget);
    expect(find.byKey(const Key('group-member-u2')), findsNothing);
    await tester.enterText(search, '不存在的名字');
    await tester.pump();
    expect(find.text('没有匹配的群成员'), findsOneWidget);
    await tester.enterText(search, '');
    await tester.pump();
    expect(tester.takeException(), isNull);

    repository.roles['me'] = 'member';
    repository.roles['u3'] = 'owner';
    controller.groupSendPolicyRevision++;
    controller.notifyListeners();
    await tester.pump(const Duration(milliseconds: 200));
    await tester.pumpAndSettle();
    expect(find.byType(CupertinoSwitch), findsNothing);
    expect(find.byKey(const Key('group-member-u1')), findsNothing);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('原群成员菜单的设为管理员复用相同确认和反馈', (tester) async {
    final repository = _RoleRepository();
    await _open(tester, repository, administratorMode: false);
    await tester.tap(
      find.descendant(
        of: find.byKey(const Key('group-member-u1')),
        matching: find.byTooltip('管理成员'),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('设为管理员'));
    await tester.pumpAndSettle();
    expect(repository.changes, isEmpty);
    await tester.tap(find.byKey(const Key('confirm-group-administrator')));
    await tester.pumpAndSettle();
    expect(repository.roles['u1'], 'admin');
    expect(find.text('已将“林屿”设为管理员'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  });
}

Future<AppController> _open(
  WidgetTester tester,
  _RoleRepository repository, {
  bool overview = false,
  bool administratorMode = true,
  double width = 390,
  double textScale = 1,
}) async {
  final controller = AppController(repository);
  await tester.runAsync(controller.loginAsDemo);
  addTearDown(controller.dispose);
  tester.view.physicalSize = Size(width, 1100);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final conversation = controller.conversations.firstWhere(
    (c) => c.id == 'c-team',
  );
  final profile = await repository.groupProfile('c-team');
  final members = await repository.groupMembers('c-team');
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(width > 600 ? Brightness.dark : Brightness.light),
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: TextScaler.linear(textScale)),
        child: child!,
      ),
      home: overview
          ? GroupManagementScreen(
              controller: controller,
              conversation: conversation,
            )
          : GroupMembersManagementScreen(
              controller: controller,
              conversationId: 'c-team',
              profile: profile,
              initialMembers: members,
              administratorMode: administratorMode,
            ),
    ),
  );
  await tester.pumpAndSettle();
  return controller;
}

class _RoleRepository extends DemoImRepository {
  _RoleRepository({String currentRole = 'owner'})
    : super(latency: Duration.zero, store: _MemoryStore()) {
    roles['me'] = currentRole;
    if (currentRole != 'owner') roles['u3'] = 'owner';
  }
  final roles = {'me': 'owner', 'u1': 'member', 'u2': 'admin', 'u3': 'member'};
  final changes = <(String, String)>[];
  bool fail = false;
  Completer<void>? pendingChange;

  @override
  Future<GroupProfile> groupProfile(String conversationId) async =>
      GroupProfile(
        conversationId: conversationId,
        ownerId: roles.entries
            .firstWhere((entry) => entry.value == 'owner')
            .key,
        name: '管理员设置测试群',
        announcement: '',
        announcementVersion: 0,
        joinPolicy: 'invite',
        allowMemberAddFriend: true,
        updatedAt: DateTime.now(),
      );

  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async => [
    for (final user in [
      DemoImRepository.demoUser,
      ...DemoImRepository.people.take(3),
    ])
      GroupMember(user: user, role: roles[user.id]!, joinedAt: DateTime(2026)),
  ];

  @override
  Future<void> setGroupRole(
    String conversationId,
    String userId,
    String role,
  ) async {
    changes.add((userId, role));
    if (fail) throw const FormatException('当前无权设置群管理员');
    await pendingChange?.future;
    roles[userId] = role;
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
