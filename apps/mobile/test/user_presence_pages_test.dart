import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/core/user_presence.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/group_management_screens.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/screens/relationship_screens.dart';
import 'package:linli_im/ui/widgets/user_presence.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));
  for (final width in [390.0, 1280.0]) {
    testWidgets('$width 好友列表、群列表、管理员页、资料页共享真实状态且保持备注和禁言', (tester) async {
      final repo = _PageRepo();
      final c = AppController(repo);
      await tester.runAsync(c.loginAsDemo);
      tester.view.physicalSize = Size(width, 1100);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(c.dispose);
      Future<void> show(Widget page) async {
        await tester.pumpWidget(
          MaterialApp(
            theme: buildLinliTheme(
              width > 600 ? Brightness.dark : Brightness.light,
            ),
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(
                context,
              ).copyWith(textScaler: TextScaler.linear(1.8)),
              child: child!,
            ),
            home: page,
          ),
        );
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
      }

      await show(ContactsTab(controller: c, desktopMode: width > 600));
      for (final (id, status) in [
        ('u2', UserPresenceStatus.offline),
        ('u1', UserPresenceStatus.online),
        ('u3', UserPresenceStatus.unknown),
      ]) {
        final row = find.byKey(Key('contact-$id'));
        final scrollable = find
            .descendant(
              of: find.byType(ContactsTab),
              matching: find.byType(Scrollable),
            )
            .first;
        // Start at the top, then scroll the lazy list until this row exists.
        tester.state<ScrollableState>(scrollable).position.jumpTo(0);
        await tester.pump();
        await tester.scrollUntilVisible(row, 180, scrollable: scrollable);
        await tester.pumpAndSettle();
        expect(
          tester
              .widget<PresenceLabel>(
                find.descendant(of: row, matching: find.byType(PresenceLabel)),
              )
              .status,
          status,
        );
        if (id == 'u1') expect(find.text('我的搭档'), findsOneWidget);
      }
      final group = (await tester.runAsync(() => repo.groupProfile('c-team')))!;
      final members = [
        GroupMember(
          user: DemoImRepository.demoUser,
          role: 'owner',
          joinedAt: DateTime(2026),
        ),
        GroupMember(
          user: DemoImRepository.people.first,
          role: 'admin',
          joinedAt: DateTime(2026),
          mutedUntil: DateTime.now().add(const Duration(hours: 1)),
        ),
      ];
      for (final admin in [false, true]) {
        await show(
          GroupMembersManagementScreen(
            key: ValueKey(admin),
            controller: c,
            conversationId: 'c-team',
            profile: group,
            initialMembers: members,
            administratorMode: admin,
          ),
        );
        expect(find.text('我的搭档'), findsOneWidget);
        expect(find.textContaining('已禁言'), findsOneWidget);
        final row = find.byKey(const ValueKey('group-member-u1'));
        expect(
          tester
              .widget<PresenceLabel>(
                find.descendant(of: row, matching: find.byType(PresenceLabel)),
              )
              .status,
          UserPresenceStatus.online,
        );
        expect(repo.contexts.last, 'c-team');
      }
      await show(
        FriendProfileScreen(
          controller: c,
          user: DemoImRepository.people.first,
          presenceGroupId: 'c-team',
        ),
      );
      // The title and the existing editable remark row both show it.
      expect(find.text('我的搭档'), findsNWidgets(2));
      expect(repo.contexts.last, 'c-team');
      expect(
        tester.widget<PresenceLabel>(find.byType(PresenceLabel)).status,
        UserPresenceStatus.online,
      );
      repo.directHidden = true;
      await show(
        FriendProfileScreen(controller: c, user: DemoImRepository.people.first),
      );
      expect(repo.contexts.last, isNull);
      expect(
        tester.widget<PresenceLabel>(find.byType(PresenceLabel)).status,
        UserPresenceStatus.hidden,
      );
      await tester.pumpWidget(const SizedBox());
      await repo.close();
    });
  }

  testWidgets('单聊正在输入优先，结束后恢复状态；普通会话刷新不重置在线状态', (tester) async {
    final repo = _PageRepo();
    final c = AppController(repo);
    await tester.runAsync(c.loginAsDemo);
    addTearDown(c.dispose);
    final conversation = c.conversations.firstWhere((v) => v.id == 'c-linyu');
    await tester.pumpWidget(
      MaterialApp(
        home: ChatScreen(controller: c, conversation: conversation),
      ),
    );
    await tester.pumpAndSettle();
    final label = find.byKey(const Key('chat-presence-c-linyu'));
    expect(
      tester.widget<PresenceLabel>(label).status,
      UserPresenceStatus.online,
    );
    repo.emit(
      const ImEvent(
        type: ImEventType.typing,
        payload: {'conversationId': 'c-linyu', 'userId': 'u1', 'typing': true},
      ),
    );
    await tester.pump();
    await tester.pump();
    expect(c.typingLabelFor('c-linyu'), '正在输入…');
    expect(find.text('正在输入…'), findsOneWidget);
    repo.emit(
      const ImEvent(
        type: ImEventType.typing,
        payload: {'conversationId': 'c-linyu', 'userId': 'u1', 'typing': false},
      ),
    );
    await tester.pump();
    await tester.pump();
    expect(
      tester.widget<PresenceLabel>(label).status,
      UserPresenceStatus.online,
    );
    final calls = repo.contexts.length;
    repo.emit(
      const ImEvent(
        type: ImEventType.conversationChanged,
        payload: {'conversationId': 'c-linyu'},
      ),
    );
    await tester.pump();
    expect(repo.contexts.length, calls);
    c.presence.setForeground(false);
    expect(c.presence.status('u1'), UserPresenceStatus.hidden);
    await tester.pumpWidget(const SizedBox());
    await repo.close();
  });
}

class _PageRepo extends DemoImRepository {
  _PageRepo() : super(latency: Duration.zero);
  final contexts = <String?>[];
  final _events = StreamController<ImEvent>.broadcast();
  bool directHidden = false;
  @override
  Stream<ImEvent> get events => _events.stream;
  void emit(ImEvent event) => _events.add(event);
  @override
  Future<List<AppUser>> contacts() async => [
    for (final user in DemoImRepository.people)
      user.copyWith(remark: user.id == 'u1' ? '我的搭档' : ''),
  ];
  @override
  Future<List<UserPresenceSnapshot>> userPresence(
    List<String> ids, {
    String? groupId,
  }) async {
    contexts.add(groupId);
    return [
      for (final id in ids)
        UserPresenceSnapshot(
          id,
          directHidden && groupId == null
              ? UserPresenceStatus.hidden
              : switch (id) {
                  'u2' => UserPresenceStatus.offline,
                  'u3' => UserPresenceStatus.unknown,
                  _ => UserPresenceStatus.online,
                },
        ),
    ];
  }

  @override
  Future<void> close() async {
    await _events.close();
    await super.close();
  }
}
