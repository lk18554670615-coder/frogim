import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';

const forwardTestUser = AppUser(
  id: 'forward-test-user',
  name: '转发测试',
  handle: 'forward-test',
  presence: '在线',
);

Conversation forwardConversation(
  int index, {
  String? title,
  bool archived = false,
}) => Conversation(
  id: 'target-$index',
  title: title ?? '测试会话 $index',
  subtitle: '测试对象',
  updatedAt: DateTime(2026, 9, 2),
  kind: index.isEven ? ConversationKind.direct : ConversationKind.group,
  archived: archived,
);

ChatMessage forwardSource(
  int index, {
  MessageContentKind kind = MessageContentKind.text,
}) => ChatMessage(
  id: 'source-$index',
  clientMessageId: 'source-client-$index',
  conversationId: 'source-conversation',
  senderId: 'forward-test-user',
  senderName: '转发测试',
  text: '转发测试消息 $index',
  sentAt: DateTime(2026, 9, 2, 12, index),
  isMine: true,
  kind: kind,
  status: MessageStatus.sent,
);

class ForwardRequest {
  ForwardRequest(this.targetId, this.sourceIds, this.mode, this.batchId);
  final String targetId;
  final List<String> sourceIds;
  final String mode;
  final String batchId;
}

class RecordingForwardRepository extends DemoImRepository {
  RecordingForwardRepository() : super(latency: Duration.zero);
  final requests = <ForwardRequest>[];
  final persisted = <String, List<ChatMessage>>{};
  Future<List<ChatMessage>> Function(ForwardRequest request)? onForward;
  Future<void> Function()? onLogout;
  bool failPersistence = false;
  int active = 0;
  int maximumActive = 0;
  @override
  AppUser? get currentUser => forwardTestUser;

  List<ChatMessage> responseFor(ForwardRequest request) => [
    for (
      var index = 0;
      index < (request.mode == 'merged' ? 1 : request.sourceIds.length);
      index++
    )
      ChatMessage(
        id: 'forward-${request.batchId}-$index',
        clientMessageId: 'forward-client-${request.batchId}-$index',
        conversationId: request.targetId,
        senderId: forwardTestUser.id,
        senderName: forwardTestUser.name,
        text: '转发测试消息 $index',
        sentAt: DateTime(2026, 9, 2),
        isMine: true,
        status: MessageStatus.sent,
        kind: request.mode == 'merged'
            ? MessageContentKind.chatHistory
            : MessageContentKind.text,
      ),
  ];

  @override
  Future<List<ChatMessage>> forwardMessages(
    String targetConversationId,
    List<String> sourceMessageIds, {
    required String mode,
    required String clientBatchId,
  }) async {
    final request = ForwardRequest(
      targetConversationId,
      List.of(sourceMessageIds),
      mode,
      clientBatchId,
    );
    requests.add(request);
    active++;
    if (active > maximumActive) maximumActive = active;
    try {
      return onForward == null
          ? responseFor(request)
          : await onForward!(request);
    } finally {
      active--;
    }
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [];

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {
    if (failPersistence) throw StateError('cache unavailable');
    persisted[conversationId] = List.of(messages);
  }

  @override
  Future<void> logout() async {
    await onLogout?.call();
  }
}

AppController forwardTestController(
  RecordingForwardRepository repository, {
  int count = 5,
}) => AppController(repository)
  ..currentUser = forwardTestUser
  ..authenticated = true
  ..initializing = false
  ..conversations = List.generate(count, forwardConversation);
