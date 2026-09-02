import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/group_message_policy.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/im_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/local_conversation_cache.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

final now = DateTime.now();
Conversation group(String? role) => Conversation(
  id: 'g',
  title: '群策略测试',
  subtitle: '',
  updatedAt: now,
  kind: ConversationKind.group,
  currentUserRole: role,
  channelType: 2,
);
ChatMessage msg(
  int seq, {
  String? event,
  bool mine = false,
  MessageStatus status = MessageStatus.sent,
  Duration age = Duration.zero,
}) => ChatMessage(
  id: 'm$seq',
  clientMessageId: 'c$seq',
  conversationId: 'g',
  senderId: mine ? 'me' : 'peer',
  senderName: '测试成员',
  text: event == null ? '正文 $seq' : '管理提示 $seq',
  sentAt: now.subtract(age),
  isMine: mine,
  conversationSeq: seq,
  status: status,
  kind: event == null ? MessageContentKind.text : MessageContentKind.system,
  event: event,
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));
  test(
    'group recall roles, independent window, exact boundary and invalid states',
    () {
      const policy = AuthPolicy();
      expect(policy.groupRecallMinutes, 1440);
      expect(
        AuthPolicy.fromJson({'groupRecallMinutes': 10081}).groupRecallMinutes,
        10080,
      );
      expect(
        AuthPolicy.fromJson({'groupRecallMinutes': 0}).groupRecallMinutes,
        1,
      );
      for (final role in ['owner', 'admin', 'member', null]) {
        for (final mine in [true, false]) {
          for (final age in [
            const Duration(hours: 1),
            const Duration(hours: 24),
            const Duration(hours: 24, microseconds: 1),
          ]) {
            expect(
              canRecallChatMessage(
                msg(1, mine: mine, age: age),
                group(role),
                policy,
                now: now,
              ),
              role != null &&
                  (mine || isGroupManager(role)) &&
                  age <= const Duration(hours: 24),
              reason: '$role $mine $age',
            );
          }
        }
      }
      for (final status in [
        MessageStatus.sending,
        MessageStatus.failed,
        MessageStatus.recalled,
        MessageStatus.expired,
      ]) {
        expect(
          canRecallChatMessage(
            msg(1, mine: true, status: status),
            group('owner'),
            policy,
            now: now,
          ),
          isFalse,
        );
      }
      expect(
        canRecallChatMessage(
          msg(1, mine: true, event: 'group.announcement.updated'),
          group('owner'),
          policy,
          now: now,
        ),
        isFalse,
      );
      expect(
        canRecallChatMessage(
          msg(1, mine: true),
          group('owner'),
          policy,
          now: now,
          roleTrusted: false,
        ),
        isFalse,
      );
      final direct = Conversation(
        id: 'g',
        title: '单聊',
        subtitle: '',
        updatedAt: now,
        kind: ConversationKind.direct,
      );
      expect(
        canRecallChatMessage(
          msg(1, mine: true, age: const Duration(minutes: 3)),
          direct,
          policy,
          now: now,
        ),
        isTrue,
      );
      expect(policy.messageMutationWindow, const Duration(minutes: 2));
    },
  );
  test(
    'all management events and recalls follow current role, not translated text',
    () {
      for (final role in ['owner', 'admin', 'member', null]) {
        for (final m in [
          ...groupManagementNoticeEvents.map((e) => msg(1, event: e)),
          msg(2, status: MessageStatus.recalled),
          msg(3, mine: true, status: MessageStatus.recalled),
        ]) {
          expect(canPresentGroupMessage(m, group(role)), isGroupManager(role));
          expect(
            canPresentGroupMessage(m, group(role), roleTrusted: false),
            isFalse,
          );
        }
        for (final event in [
          'group.announcement.updated',
          'group.profile.updated',
          'group.member.role',
          'group.mute.updated',
        ]) {
          expect(
            canPresentGroupMessage(msg(1, event: event), group(role)),
            isTrue,
          );
        }
        expect(
          canPresentGroupMessage(msg(1).copyWith(text: '消息已撤回'), group(role)),
          isTrue,
        );
      }
    },
  );
  test(
    'hidden-only pages continue, roles retain raw history, summary/search/quotes filtered',
    () async {
      final repo = PolicyRepository();
      final c = AppController(repo);
      addTearDown(c.dispose);
      c.conversations = [group('member')];
      await c.loadMessages('g');
      expect(repo.cursors, [6, 4]);
      expect(c.messagesFor('g').map((m) => m.id), ['m1']);
      expect(
        c
            .messagesFor('g')
            .where((m) => m.kind == MessageContentKind.screenshotNotice),
        isEmpty,
      );
      expect(c.messageHistoryHasMore('g'), isFalse);
      expect(c.conversations.single.subtitle, '正文 1');
      expect(c.shouldNotifyConversation('g', afterSequence: 1), isFalse);
      expect(await c.searchConversationMessages('g', '管理提示'), isEmpty);
      c.conversations = [group('admin')];
      expect(c.messagesFor('g'), hasLength(6));
      expect(
        c
            .messagesFor('g')
            .where((m) => m.kind == MessageContentKind.screenshotNotice),
        hasLength(1),
      );
      expect(await c.searchConversationMessages('g', '管理提示'), hasLength(5));
      expect(c.canRecallMessage(msg(1)), isTrue);
      expect(await c.recallMessage(msg(1)), isTrue);
      expect(c.messagesFor('g').first.status, MessageStatus.recalled);
      c.conversations = [group('member')];
      expect(c.messagesFor('g'), isEmpty);
      await c.loadMessages('g', force: true);
      expect(
        c.messagesFor('g'),
        isEmpty,
        reason: 'stale remote must not resurrect recalled body',
      );
    },
  );
  test('群截屏提示仅群主和管理员可见，包括本人截屏；私聊不变', () {
    final direct = Conversation(
      id: 'd',
      title: '私聊',
      subtitle: '',
      updatedAt: now,
      kind: ConversationKind.direct,
    );
    for (final mine in [true, false]) {
      final screenshot = msg(
        8,
        mine: mine,
      ).copyWith(kind: MessageContentKind.screenshotNotice);
      for (final role in ['owner', 'admin', 'member', null]) {
        expect(
          canPresentGroupMessage(screenshot, group(role)),
          isGroupManager(role),
        );
        expect(
          canPresentGroupMessage(screenshot, group(role), roleTrusted: false),
          isFalse,
        );
      }
      expect(canPresentGroupMessage(screenshot, null), isFalse);
      expect(
        canPresentGroupMessage(screenshot, direct, roleTrusted: false),
        isTrue,
      );
    }
    expect(
      canPresentGroupMessage(
        msg(9).copyWith(text: '我截取了聊天界面'),
        group('member'),
      ),
      isTrue,
    );
  });
  test(
    'CMD recall and demotion hide cached notices immediately; promotion restores',
    () async {
      final repo = PolicyRepository()..role = 'admin';
      final c = AppController(repo);
      addTearDown(c.dispose);
      await c.loginAsDemo();
      await c.loadMessages('g');
      expect(c.messagesFor('g'), isNotEmpty);
      repo.role = 'member';
      repo.bus.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: {'conversationId': 'g'},
        ),
      );
      await Future<void>.delayed(Duration.zero);
      expect(c.messagesFor('g').where(isGroupManagementNotice), isEmpty);
      await Future<void>.delayed(const Duration(milliseconds: 500));
      expect(c.conversations.single.currentUserRole, 'member');
      repo.role = 'owner';
      repo.bus.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: {'conversationId': 'g'},
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 500));
      expect(c.messagesFor('g').where(isGroupManagementNotice), isNotEmpty);
    },
  );
  test(
    'SDK encrypted cache retains recall after delayed ACK, history sync and reopen',
    () async {
      final store = SecureLocalStore();
      final cache = LocalConversationCache(store);
      const channel = WukongChannel(id: 'g', type: 2);
      final original = WukongMessage(
        messageId: '1',
        messageSeq: 1,
        clientMsgNo: 'c1',
        clientSeq: 1,
        fromUid: 'peer',
        channel: channel,
        timestamp: now,
        payload: const {'type': 1, 'content': 'old'},
        state: WukongMessageState.sent,
      );
      await cache.upsertMessage('me', original);
      await cache.markRecalled('me', channel, '1');
      await cache.upsertMessage('me', original);
      await cache.mergeMessages('me', channel, [original]);
      final reopened = LocalConversationCache(store);
      expect(
        (await reopened.readMessages(
          'me',
          channel,
        )).single.payload['recalledAt'],
        isNotNull,
      );
      await reopened.mergeMessages('me', channel, [original]);
      expect(
        (await reopened.readMessages(
          'me',
          channel,
        )).single.payload['recalledAt'],
        isNotNull,
      );
    },
  );
  test(
    'group role invalidation during login does not skip contacts bootstrap',
    () async {
      final repo = PolicyRepository()..invalidateFirstLoad = true;
      final c = AppController(repo);
      addTearDown(c.dispose);
      await c.loginAsDemo();
      expect(c.contacts, isNotEmpty);
    },
  );
  for (final width in [390.0, 1280.0]) {
    testWidgets(
      'chat $width member has no management rows; owner sees placeholders',
      (tester) async {
        tester.view.physicalSize = Size(width, 844);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final c = AppController(PolicyRepository())
          ..conversations = [group('member')];
        addTearDown(c.dispose);
        await tester.pumpWidget(
          MaterialApp(
            home: ChatScreen(controller: c, conversation: group('member')),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.textContaining('管理提示'), findsNothing);
        expect(find.text('正文 1'), findsOneWidget);
        expect(tester.takeException(), isNull);
        c.conversations = [group('owner')];
        await c.loadMessages('g', force: true);
        await tester.pumpAndSettle();
        expect(find.textContaining('管理提示'), findsWidgets);
        expect(tester.takeException(), isNull);
      },
    );
  }
}

class PolicyRepository extends DemoImRepository
    implements PaginatedMessageRepository {
  PolicyRepository() : super(latency: Duration.zero);
  String role = 'member';
  bool invalidateFirstLoad = false;
  final bus = StreamController<ImEvent>.broadcast();
  final cursors = <int>[];
  final data = [
    msg(1),
    for (var i = 2; i <= 5; i++) msg(i, event: 'group.members.added'),
    msg(
      6,
      event: 'screenshot.taken',
    ).copyWith(kind: MessageContentKind.screenshotNotice),
  ];
  @override
  Stream<ImEvent> get events => bus.stream;
  @override
  Future<List<Conversation>> conversations() async {
    if (invalidateFirstLoad) {
      invalidateFirstLoad = false;
      bus.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: {'conversationId': 'g'},
        ),
      );
      await Future<void>.delayed(Duration.zero);
    }
    return [group(role)];
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    data.last,
  ];
  @override
  Future<List<ChatMessage>> olderMessages(
    String conversationId, {
    required int beforeSequence,
    int limit = 50,
  }) async {
    cursors.add(beforeSequence);
    return data
        .where(
          (m) =>
              m.conversationSeq < beforeSequence &&
              m.conversationSeq >=
                  beforeSequence - (beforeSequence == 4 ? 3 : 2),
        )
        .toList();
  }

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}
  @override
  Future<void> recallMessage(String id) async {}
  @override
  Future<List<ChatMessage>> searchMessages(
    String conversationId,
    String query, {
    int limit = 50,
  }) async => data.where((m) => m.text.contains(query)).take(limit).toList();
}
