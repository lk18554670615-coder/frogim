import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/group_message_policy.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));
  final now = DateTime.now();
  final conversation = Conversation(
    id: 'c-linyu',
    title: '测试私聊',
    subtitle: '',
    updatedAt: now,
    kind: ConversationKind.direct,
  );
  ChatMessage message({required Duration age, bool mine = true}) => ChatMessage(
    id: '123456',
    clientMessageId: 'client-direct',
    conversationId: conversation.id,
    senderId: 'sender',
    senderName: '发送者',
    text: '私聊消息',
    sentAt: now.subtract(age),
    isMine: mine,
    status: MessageStatus.sent,
    conversationSeq: 1,
  );

  test('私聊使用独立时限，边界包含等于，不能撤回他人消息', () {
    for (final minutes in [1, 60, 1440, 10080]) {
      final policy = AuthPolicy(
        directRecallMinutes: minutes,
        groupRecallMinutes: 10,
        messageRecallMinutes: 2,
      );
      for (final delta in [-1, 0, 1]) {
        final age = Duration(minutes: minutes, microseconds: delta);
        for (final mine in [true, false]) {
          expect(
            canRecallChatMessage(
              message(age: age, mine: mine),
              conversation,
              policy,
              now: now,
            ),
            mine && delta <= 0,
          );
        }
      }
      expect(policy.messageMutationWindow, const Duration(minutes: 2));
      expect(policy.groupRecallWindow, const Duration(minutes: 10));
    }
    expect(
      canRecallChatMessage(
        message(age: const Duration(hours: 23)),
        conversation,
        const AuthPolicy(),
        now: now,
      ),
      isTrue,
    );
  });

  test('私聊仍拒绝不可撤回状态，撤回占位对收发双方可见', () {
    final original = message(age: const Duration(minutes: 5));
    for (final status in [
      MessageStatus.sending,
      MessageStatus.failed,
      MessageStatus.recalled,
      MessageStatus.expired,
    ]) {
      expect(
        canRecallChatMessage(
          original.copyWith(status: status),
          conversation,
          const AuthPolicy(),
          now: now,
        ),
        isFalse,
      );
    }
    for (final invalid in [
      original.copyWith(id: 'local-pending'),
      original.copyWith(id: ''),
      original.copyWith(kind: MessageContentKind.system),
      original.copyWith(expiresAt: now),
    ]) {
      expect(
        canRecallChatMessage(
          invalid,
          conversation,
          const AuthPolicy(),
          now: now,
        ),
        isFalse,
      );
    }
    for (final mine in [true, false]) {
      final recalled = message(
        age: const Duration(minutes: 5),
        mine: mine,
      ).copyWith(status: MessageStatus.recalled, text: '');
      expect(
        canPresentGroupMessage(recalled, conversation, roleTrusted: false),
        isTrue,
      );
      expect(messagePreviewText(recalled), '消息已撤回');
    }
  });

  test('发送端按新策略撤回，接收端处理 CMD，双方保留占位且旧缓存不恢复正文', () async {
    final bus = StreamController<ImEvent>.broadcast();
    final outgoing = message(age: const Duration(hours: 1));
    final senderRepo = _DirectRecallRepository(bus, outgoing);
    final receiverRepo = _DirectRecallRepository(
      bus,
      message(age: const Duration(hours: 1), mine: false),
    );
    final sender = AppController(senderRepo);
    final receiver = AppController(receiverRepo);
    addTearDown(() async {
      sender.dispose();
      receiver.dispose();
      await bus.close();
    });
    await sender.loginAsDemo();
    await receiver.loginAsDemo();
    await sender.loadMessages(conversation.id, force: true);
    await receiver.loadMessages(conversation.id, force: true);
    expect(sender.isMessageEditable(outgoing), isFalse);
    expect(sender.canRecallMessage(outgoing), isTrue);
    expect(
      receiver.canRecallMessage(receiver.messagesFor(conversation.id).single),
      isFalse,
    );
    expect(await sender.recallMessage(outgoing), isTrue);
    await Future<void>.delayed(Duration.zero);
    expect(senderRepo.recallCalls, 1);
    for (final controller in [sender, receiver]) {
      expect(
        controller.messagesFor(conversation.id).single.status,
        MessageStatus.recalled,
      );
      await controller.loadMessages(conversation.id, force: true);
      final recalled = controller.messagesFor(conversation.id).single;
      expect(recalled.status, MessageStatus.recalled);
      expect(recalled.text, '消息已撤回');
      expect(messagePreviewText(recalled), '消息已撤回');
      expect(await controller.recallMessage(recalled), isFalse);
    }
    expect(senderRepo.recallCalls, 1);
    expect(receiverRepo.recallCalls, 0);
  });
}

class _DirectRecallRepository extends DemoImRepository {
  _DirectRecallRepository(this.bus, this.message)
    : super(latency: Duration.zero);
  final StreamController<ImEvent> bus;
  final ChatMessage message;
  int recallCalls = 0;
  @override
  Stream<ImEvent> get events => bus.stream;
  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [message];
  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}
  @override
  Future<void> recallMessage(String messageId) async {
    recallCalls++;
    bus.add(
      ImEvent(
        type: ImEventType.messageRecalled,
        payload: {
          'conversationId': message.conversationId,
          'messageId': messageId,
        },
      ),
    );
  }
}
