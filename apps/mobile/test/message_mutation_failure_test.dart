import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';

void main() {
  test('单条消息本地持久化失败时不从当前会话消失', () async {
    final controller = AppController(_FailingMessageMutationRepository());
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    await controller.loadMessages('c-linyu', force: true);
    final message = controller.messagesFor('c-linyu').first;
    final before = controller.messagesFor('c-linyu').length;

    final deleted = await controller.deleteMessage(message);

    expect(deleted, isFalse);
    expect(controller.messagesFor('c-linyu'), hasLength(before));
    expect(
      controller
          .messagesFor('c-linyu')
          .any((item) => item.clientMessageId == message.clientMessageId),
      isTrue,
    );
    expect(controller.error, '消息删除失败，本机记录未修改');
  });

  test('批量删除持久化失败时保留全部消息和可重试状态', () async {
    final controller = AppController(_FailingMessageMutationRepository());
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    await controller.loadMessages('c-linyu', force: true);
    final before = controller.messagesFor('c-linyu');
    final ids = before.take(2).map((item) => item.clientMessageId).toSet();

    final deleted = await controller.deleteMessages('c-linyu', ids);

    expect(deleted, isFalse);
    expect(controller.messagesFor('c-linyu'), hasLength(before.length));
    expect(
      controller.messagesFor('c-linyu').map((item) => item.clientMessageId),
      containsAll(ids),
    );
  });

  test('服务端撤回失败时保留原消息状态并返回失败', () async {
    final controller = AppController(_FailingMessageMutationRepository());
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    await controller.loadMessages('c-linyu', force: true);
    final message = controller
        .messagesFor('c-linyu')
        .firstWhere((item) => item.isMine && !item.id.startsWith('local-'));

    final recalled = await controller.recallMessage(message);

    expect(recalled, isFalse);
    expect(
      controller
          .messagesFor('c-linyu')
          .firstWhere((item) => item.clientMessageId == message.clientMessageId)
          .status,
      message.status,
    );
    expect(controller.error, '撤回失败，可能已超过可撤回时间');
  });
}

class _FailingMessageMutationRepository extends DemoImRepository {
  _FailingMessageMutationRepository() : super(latency: Duration.zero);

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    ChatMessage(
      id: 'server-incoming',
      conversationId: conversationId,
      senderId: 'u1',
      senderName: '林屿',
      text: '需要保留的消息',
      sentAt: DateTime(2026, 8, 16, 10),
      isMine: false,
      status: MessageStatus.read,
    ),
    ChatMessage(
      id: 'server-outgoing',
      conversationId: conversationId,
      senderId: 'me',
      senderName: '我',
      text: '需要撤回的消息',
      sentAt: DateTime(2026, 8, 16, 10, 1),
      isMine: true,
      status: MessageStatus.sent,
    ),
  ];

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) => Future.error(StateError('internal-persistence-token'));

  @override
  Future<void> recallMessage(String messageId) =>
      Future.error(StateError('internal-recall-token'));
}
