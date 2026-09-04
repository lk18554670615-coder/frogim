import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/im_repository.dart';
import 'package:linli_im/data/message_deletion_cache.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/message_mapper.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

ChatMessage message({
  bool mine = false,
  MessageStatus status = MessageStatus.sent,
}) => ChatMessage(
  id: '123',
  clientMessageId: 'c123',
  conversationId: 'g',
  senderId: mine ? 'me' : 'peer',
  senderName: '成员',
  text: '删除测试',
  sentAt: DateTime(2020),
  isMine: mine,
  conversationSeq: 10,
  status: status,
);
Conversation conversation({
  bool group = true,
  String role = 'owner',
  bool business = false,
}) => Conversation(
  id: 'g',
  title: '删除测试',
  subtitle: '',
  updatedAt: DateTime.now(),
  kind: group ? ConversationKind.group : ConversationKind.direct,
  currentUserRole: role,
  channelType: business
      ? 5
      : group
      ? 2
      : 1,
);

class DeletionRepo extends DemoImRepository
    implements MessageDeletionRepository {
  DeletionRepo() : super(latency: Duration.zero);
  int calls = 0;
  bool fail = false;
  Completer<void>? pending;
  final deleted = <String>{};
  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    message().copyWith(text: '待删除正文'),
  ];
  @override
  Future<List<Conversation>> conversations() async => [
    conversation(group: false),
  ];
  @override
  bool isMessageDeleted(String id) => deleted.contains(id);
  @override
  Future<List<String>> deleteMessagesForEveryone(
    String cid,
    List<String> ids,
  ) async {
    calls++;
    if (pending != null) await pending!.future;
    if (fail) throw StateError('server rejected');
    deleted.addAll(ids);
    return ids;
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
  });
  test(
    'persistent tombstones survive restart, parallel writes and account changes',
    () async {
      final cache = MessageDeletionCache(SecureLocalStore());
      await Future.wait([
        cache.mark('a', ['1']),
        cache.mark('a', ['2']),
        cache.mark('b', ['3']),
      ]);
      expect(cache.contains('a', '3'), false);
      final restarted = MessageDeletionCache(SecureLocalStore());
      await restarted.load('a');
      expect(restarted.contains('a', '1'), true);
      expect(restarted.contains('a', '2'), true);
      expect(restarted.contains('b', '1'), false);
    },
  );
  test(
    'official mutual deletion maps independently of recall and persists',
    () {
      final wire = WukongMessage.fromSyncJson({
        'message_idstr': '123',
        'message_seq': 10,
        'channel_id': 'g',
        'channel_type': 2,
        'timestamp': 1700000000,
        'payload': {'type': 1, 'content': 'text'},
        'message_extra': {'is_mutual_deleted': 1},
      });
      final mapped = MessageMapper().toChatMessage(
        wire,
        currentUserId: 'me',
        conversationId: 'g',
      );
      expect(mapped.deletedForEveryone, true);
      expect(mapped.status, MessageStatus.sent);
      expect(ChatMessage.fromJson(mapped.toJson()).deletedForEveryone, true);
    },
  );
  test(
    'permission gate is independent from recall, sender and message age',
    () async {
      final repo = DeletionRepo();
      final c = AppController(repo);
      addTearDown(c.dispose);
      c.authenticated = true;
      c.currentUser = const AppUser(
        id: 'me',
        name: '我',
        handle: 'me',
        presence: '',
      );
      c.conversations = [conversation()];
      expect(c.canDeleteForEveryone(message()), false);
      c.currentUser = c.currentUser!.copyWith(
        canDeleteMessagesForEveryone: true,
      );
      for (final role in ['owner', 'admin', 'member']) {
        c.conversations = [conversation(role: role)];
        for (final mine in [true, false]) {
          expect(c.canDeleteForEveryone(message(mine: mine)), role != 'member');
        }
      }
      c.conversations = [conversation(group: false)];
      expect(c.canDeleteForEveryone(message()), true);
      expect(
        c.canDeleteForEveryone(message(status: MessageStatus.recalled)),
        true,
      );
      for (final status in [
        MessageStatus.sending,
        MessageStatus.failed,
        MessageStatus.expired,
      ]) {
        expect(c.canDeleteForEveryone(message(status: status)), false);
      }
      c.conversations = [conversation(business: true)];
      expect(c.canDeleteForEveryone(message()), false);
    },
  );
  test(
    'duplicate click, failure retention, and no resurrection after success',
    () async {
      final repo = DeletionRepo();
      final c = AppController(repo);
      addTearDown(c.dispose);
      c.authenticated = true;
      c.currentUser = const AppUser(
        id: 'me',
        name: '我',
        handle: 'me',
        presence: '',
        canDeleteMessagesForEveryone: true,
      );
      c.conversations = [conversation(group: false)];
      repo.fail = true;
      expect(await c.deleteForEveryone('g', [message()]), false);
      expect(c.isMessageDeleted('123'), false);
      repo.fail = false;
      repo.pending = Completer<void>();
      final pending = c.deleteForEveryone('g', [message()]);
      expect(await c.deleteForEveryone('g', [message()]), false);
      expect(repo.calls, 2);
      repo.pending!.complete();
      expect(await pending, true);
      expect(c.canDisplayMessage(message()), false);
      expect(c.canRecallMessage(message()), false);
    },
  );
  for (final allowed in [false, true]) {
    testWidgets(
      'message menu authorization $allowed and explicit confirmation',
      (tester) async {
        final repo = DeletionRepo();
        final c = AppController(repo);
        addTearDown(c.dispose);
        c.authenticated = true;
        c.currentUser = AppUser(
          id: 'me',
          name: '我',
          handle: 'me',
          presence: '',
          canDeleteMessagesForEveryone: allowed,
        );
        c.conversations = [conversation(group: false)];
        await tester.pumpWidget(
          MaterialApp(
            home: ChatScreen(
              controller: c,
              conversation: conversation(group: false),
            ),
          ),
        );
        await tester.pumpAndSettle();
        await tester.longPress(find.text('待删除正文'));
        await tester.pumpAndSettle();
        expect(find.text('为所有人删除'), allowed ? findsOneWidget : findsNothing);
        if (allowed) {
          await tester.tap(find.text('为所有人删除'));
          await tester.pumpAndSettle();
          expect(find.text('为所有人删除 1 条消息？'), findsOneWidget);
          expect(repo.calls, 0);
          await tester.tap(find.text('取消').last);
          await tester.pumpAndSettle();
          expect(repo.calls, 0);
          expect(find.text('待删除正文'), findsOneWidget);
        }
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
        await tester.pumpAndSettle();
      },
    );
  }
}
