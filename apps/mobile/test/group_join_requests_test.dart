import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/ui/screens/group_join_requests_screen.dart';
import 'package:linli_im/ui/screens/group_management_screens.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('群主从群资料查看待审核申请并同意直接加入', (tester) async {
    final repository = _JoinReviewRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await tester.runAsync(controller.loginAsDemo);
    final conversation = controller.conversations.firstWhere(
      (item) => item.id == 'c-team',
    );

    await tester.pumpWidget(
      MaterialApp(
        home: GroupManagementScreen(
          controller: controller,
          conversation: conversation,
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.drag(
      find.byKey(const Key('group-management-list')),
      const Offset(0, -520),
    );
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('group-join-requests-entry')), findsOneWidget);
    expect(find.byKey(const Key('group-join-request-badge')), findsOneWidget);
    expect(find.text('1 条待处理申请'), findsOneWidget);

    await tester.tap(find.byKey(const Key('group-join-requests-entry')));
    await tester.pumpAndSettle();
    expect(find.byType(GroupJoinRequestsScreen), findsOneWidget);
    expect(find.text('安然'), findsOneWidget);
    expect(find.textContaining('邀请该好友加入'), findsOneWidget);

    await tester.tap(find.byKey(const Key('group-join-approve-request-1')));
    await tester.pumpAndSettle();
    await tester.tap(
      find.byKey(const Key('group-join-request-confirm-approve')),
    );
    await tester.pumpAndSettle();

    expect(repository.actions, ['approve']);
    expect(find.text('暂无待审核的入群申请'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

class _JoinReviewRepository extends DemoImRepository {
  _JoinReviewRepository()
    : super(latency: Duration.zero, store: _ReviewMemoryStore());

  final actions = <String>[];
  late GroupJoinRequest request = GroupJoinRequest(
    id: 'request-1',
    conversationId: 'c-team',
    requesterId: 'u1',
    inviteeId: 'u2',
    status: 'pending',
    createdAt: DateTime.now().subtract(const Duration(hours: 1)),
    expiresAt: DateTime.now().add(const Duration(days: 7)),
    updatedAt: DateTime.now(),
    requester: DemoImRepository.people.first,
    invitee: DemoImRepository.people[1],
  );

  @override
  Future<GroupProfile> groupProfile(String conversationId) async {
    final base = await super.groupProfile(conversationId);
    return GroupProfile(
      conversationId: base.conversationId,
      ownerId: DemoImRepository.demoUser.id,
      name: base.name,
      avatarUrl: base.avatarUrl,
      announcement: base.announcement,
      announcementVersion: base.announcementVersion,
      joinPolicy: 'member_approval',
      joinPolicyVersion: 2,
      canDirectInvite: true,
      canReviewJoinRequests: true,
      pendingJoinRequestCount: request.pending ? 1 : 0,
      allowMemberAddFriend: true,
      updatedAt: DateTime.now(),
    );
  }

  @override
  Future<List<GroupJoinRequest>> groupJoinRequests(
    String conversationId, {
    String status = 'pending',
  }) async => request.pending ? [request] : const [];

  @override
  Future<GroupJoinRequest> respondGroupJoinRequest(
    String conversationId,
    String requestId,
    String action,
  ) async {
    actions.add(action);
    request = GroupJoinRequest(
      id: request.id,
      conversationId: request.conversationId,
      requesterId: request.requesterId,
      inviteeId: request.inviteeId,
      status: action == 'approve' ? 'approved' : 'rejected',
      createdAt: request.createdAt,
      expiresAt: request.expiresAt,
      updatedAt: DateTime.now(),
      requester: request.requester,
      invitee: request.invitee,
    );
    return request;
  }
}

class _ReviewMemoryStore extends SecureLocalStore {
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
