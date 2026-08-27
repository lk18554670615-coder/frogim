import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/screens/people_screens.dart';

void main() {
  testWidgets('核心附属数据加载失败不会伪装成真实空数据', (tester) async {
    final repository = _FailingCoreListsRepository();
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    expect(controller.contactsLoadError, '联系人加载失败，请稍后重试');
    expect(controller.friendRequestsLoadError, '好友申请加载失败，请稍后重试');
    expect(controller.groupInvitationsLoadError, '群聊邀请加载失败，请稍后重试');
    expect(controller.announcementsLoadError, '平台公告加载失败，请稍后重试');

    await tester.pumpWidget(
      MaterialApp(home: FriendRequestsScreen(controller: controller)),
    );
    await tester.pump();
    expect(find.text('好友申请暂时无法加载'), findsOneWidget);
    expect(find.text('暂无好友申请'), findsNothing);

    await tester.pumpWidget(
      MaterialApp(home: GroupInvitationsScreen(controller: controller)),
    );
    await tester.pump();
    expect(find.text('群聊邀请暂时无法加载'), findsOneWidget);
    expect(find.text('暂无群聊邀请'), findsNothing);

    await tester.pumpWidget(
      MaterialApp(home: ContactsTab(controller: controller)),
    );
    await tester.pump();
    expect(find.text('联系人暂时无法加载'), findsOneWidget);
    expect(find.text('还没有联系人'), findsNothing);

    await tester.pumpWidget(
      MaterialApp(
        home: HomeScreen(controller: controller, onToggleTheme: () {}),
      ),
    );
    await tester.pump();
    expect(find.byKey(const Key('announcement-load-error')), findsOneWidget);
  });

  testWidgets('好友申请失败后可重新加载并恢复真实列表', (tester) async {
    final repository = _FailingCoreListsRepository();
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    repository.failRequests = false;
    await tester.pumpWidget(
      MaterialApp(home: FriendRequestsScreen(controller: controller)),
    );
    await tester.pump();
    await tester.tap(find.text('重新加载'));
    await tester.pumpAndSettle();

    expect(controller.friendRequestsLoadError, isNull);
    expect(find.text('好友申请暂时无法加载'), findsNothing);
    expect(controller.requests, isNotEmpty);
  });
}

class _FailingCoreListsRepository extends DemoImRepository {
  _FailingCoreListsRepository() : super(latency: Duration.zero);

  bool failRequests = true;

  @override
  Future<List<AppUser>> contacts() =>
      Future.error(StateError('internal-contacts-token'));

  @override
  Future<List<FriendRequest>> friendRequests() => failRequests
      ? Future.error(StateError('internal-requests-token'))
      : super.friendRequests();

  @override
  Future<List<GroupInvitation>> groupInvitations() =>
      Future.error(StateError('internal-invitations-token'));

  @override
  Future<List<AppAnnouncement>> announcements() =>
      Future.error(StateError('internal-announcements-token'));
}
