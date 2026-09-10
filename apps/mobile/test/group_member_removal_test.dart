import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/group_remove_members_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  for (final role in ['owner', 'admin']) {
    testWidgets('$role 群资料显示成员移除快捷入口', (tester) async {
      final fixture = await _openInfo(tester, _RemovalRepository(role));
      expect(find.byKey(const Key('chat-info-remove-members')), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
      fixture.dispose();
    });
  }

  testWidgets('普通成员不显示成员移除快捷入口', (tester) async {
    final fixture = await _openInfo(tester, _RemovalRepository('member'));
    expect(find.byKey(const Key('chat-info-remove-members')), findsNothing);
    await tester.pumpWidget(const SizedBox.shrink());
    fixture.dispose();
  });

  testWidgets('群主可从减号入口多选并确认移出成员', (tester) async {
    final repository = _RemovalRepository('owner');
    final controller = await _openInfo(tester, repository);
    await tester.tap(find.byKey(const Key('chat-info-remove-members')));
    await tester.pumpAndSettle();
    expect(find.byType(GroupRemoveMembersScreen), findsOneWidget);

    for (final id in ['u2', 'u3']) {
      await tester.tap(find.byKey(Key('group-remove-user-$id')));
      await tester.pump();
    }
    expect(find.text('移除 2'), findsOneWidget);
    await tester.tap(find.byKey(const Key('group-remove-confirm')));
    await tester.pump();
    expect(repository.removed, isEmpty, reason: '二次确认前不能请求服务端');
    await tester.tap(find.byKey(const Key('confirm-group-remove-members')));
    await tester.pumpAndSettle();

    expect(repository.removed, ['u2', 'u3']);
    expect(find.byType(GroupRemoveMembersScreen), findsNothing);
    await tester.pumpWidget(const SizedBox.shrink());
    controller.dispose();
  });

  testWidgets('管理员不能移除群主及其他管理员，全选仅选择普通成员', (tester) async {
    final repository = _RemovalRepository('admin');
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    await tester.pumpWidget(
      MaterialApp(
        home: GroupRemoveMembersScreen(
          controller: controller,
          conversationId: 'c-team',
        ),
      ),
    );
    await tester.pumpAndSettle();

    for (final id in ['me', 'u1', 'u2']) {
      expect(
        tester
            .widget<CheckboxListTile>(find.byKey(Key('group-remove-user-$id')))
            .onChanged,
        isNull,
      );
    }
    expect(
      tester
          .widget<CheckboxListTile>(
            find.byKey(const Key('group-remove-user-u3')),
          )
          .onChanged,
      isNotNull,
    );
    await tester.tap(find.byKey(const Key('group-remove-select-all')));
    await tester.pump();
    expect(find.text('已选 1 位成员'), findsOneWidget);

    await tester.pumpWidget(const SizedBox.shrink());
    controller.dispose();
  });

  testWidgets('成员列表刷新不会中断 Web 输入法组合态或夺走搜索焦点', (tester) async {
    final repository = _RemovalRepository('owner');
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    repository.delayNextMemberLoad = true;
    await tester.pumpWidget(
      MaterialApp(
        home: GroupRemoveMembersScreen(
          controller: controller,
          conversationId: 'c-team',
        ),
      ),
    );
    await tester.pump();

    final search = find.byKey(const Key('group-remove-search'));
    await tester.tap(search);
    await tester.showKeyboard(search);
    tester.testTextInput.updateEditingValue(
      const TextEditingValue(
        text: 'ping',
        selection: TextSelection.collapsed(offset: 4),
        composing: TextRange(start: 0, end: 4),
      ),
    );
    await tester.pump();

    EditableText editable() => tester.widget<EditableText>(
      find.descendant(of: search, matching: find.byType(EditableText)),
    );
    expect(editable().controller.text, 'ping');
    expect(
      editable().controller.value.composing,
      const TextRange(start: 0, end: 4),
    );
    expect(editable().focusNode.hasFocus, isTrue);

    await tester.pump(const Duration(milliseconds: 250));
    expect(editable().controller.text, 'ping');
    expect(
      editable().controller.value.composing,
      const TextRange(start: 0, end: 4),
    );
    expect(editable().focusNode.hasFocus, isTrue);

    tester.testTextInput.updateEditingValue(
      const TextEditingValue(
        text: '苹果',
        selection: TextSelection.collapsed(offset: 2),
      ),
    );
    await tester.pump();
    expect(editable().controller.text, '苹果');
    expect(editable().focusNode.hasFocus, isTrue);
    expect(tester.takeException(), isNull);

    await tester.pumpWidget(const SizedBox.shrink());
    controller.dispose();
  });
}

Future<AppController> _openInfo(
  WidgetTester tester,
  _RemovalRepository repository,
) async {
  final controller = AppController(repository);
  await tester.runAsync(controller.loginAsDemo);
  final conversation = controller.conversations.firstWhere(
    (item) => item.id == 'c-team',
  );
  await tester.pumpWidget(
    MaterialApp(
      home: ChatInfoScreen(
        controller: controller,
        conversation: conversation,
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

class _RemovalRepository extends DemoImRepository {
  _RemovalRepository(this.actorRole) : super(latency: Duration.zero) {
    roles.addAll(
      actorRole == 'owner'
          ? {'me': 'owner', 'u1': 'admin', 'u2': 'member', 'u3': 'member'}
          : actorRole == 'admin'
          ? {'me': 'admin', 'u1': 'owner', 'u2': 'admin', 'u3': 'member'}
          : {'me': 'member', 'u1': 'owner', 'u2': 'admin', 'u3': 'member'},
    );
  }

  final String actorRole;
  final Map<String, String> roles = {};
  final List<String> removed = [];
  bool delayNextMemberLoad = false;

  AppUser _user(String id) => id == 'me'
      ? DemoImRepository.demoUser
      : DemoImRepository.people.firstWhere((user) => user.id == id);

  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async {
    if (delayNextMemberLoad) {
      delayNextMemberLoad = false;
      await Future<void>.delayed(const Duration(milliseconds: 150));
    }
    return [
      for (final entry in roles.entries)
        GroupMember(
          user: _user(entry.key),
          role: entry.value,
          joinedAt: DateTime(2026),
        ),
    ];
  }

  @override
  Future<List<Conversation>> conversations() async => [
    for (final conversation in await super.conversations())
      conversation.id == 'c-team'
          ? conversation.copyWith(
              currentUserRole: actorRole,
              memberCount: roles.length,
              members: [for (final id in roles.keys) _user(id)],
            )
          : conversation,
  ];

  @override
  Future<void> removeGroupMember(String conversationId, String userId) async {
    removed.add(userId);
    roles.remove(userId);
  }
}
