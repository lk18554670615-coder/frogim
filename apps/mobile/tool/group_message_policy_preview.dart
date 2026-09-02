// Local-only visual QA. Synthetic data; no production API, IM or push access.
import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

SemanticsHandle? _semantics;
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  _semantics ??= SemanticsBinding.instance.ensureSemantics();
  final repo = GroupPolicyPreviewRepository();
  final controller = AppController(repo);
  await controller.loginAsDemo();
  runApp(GroupPolicyPreview(controller: controller, repository: repo));
}

class GroupPolicyPreview extends StatefulWidget {
  const GroupPolicyPreview({
    super.key,
    required this.controller,
    required this.repository,
  });
  final AppController controller;
  final GroupPolicyPreviewRepository repository;
  @override
  State<GroupPolicyPreview> createState() => _GroupPolicyPreviewState();
}

class _GroupPolicyPreviewState extends State<GroupPolicyPreview> {
  bool dark = false, large = false, narrow = false;
  @override
  void dispose() {
    widget.controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    theme: buildLinliTheme(dark ? Brightness.dark : Brightness.light),
    builder: (context, child) => MediaQuery(
      data: MediaQuery.of(
        context,
      ).copyWith(textScaler: TextScaler.linear(large ? 1.6 : 1)),
      child: child!,
    ),
    home: Scaffold(
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(8),
            child: Wrap(
              spacing: 8,
              children: [
                const Text('本地群策略测试：不连接服务器'),
                for (final entry in {
                  'member': '普通成员',
                  'admin': '管理员',
                  'owner': '群主',
                }.entries)
                  OutlinedButton(
                    onPressed: () => widget.repository.changeRole(entry.key),
                    child: Text(entry.value),
                  ),
                OutlinedButton(
                  onPressed: () => setState(() => dark = !dark),
                  child: const Text('深浅色'),
                ),
                OutlinedButton(
                  onPressed: () => setState(() => large = !large),
                  child: const Text('大字体'),
                ),
                OutlinedButton(
                  onPressed: () => setState(() => narrow = !narrow),
                  child: const Text('窄聊天列'),
                ),
              ],
            ),
          ),
          AnimatedBuilder(
            animation: widget.controller,
            builder: (context, _) => Text(
              '角色：${widget.controller.conversations.firstOrNull?.currentUserRole}；可见消息：${widget.controller.messagesFor('policy-preview').length}；摘要：${widget.controller.conversations.firstOrNull?.subtitle}',
            ),
          ),
          Expanded(
            child: Center(
              child: SizedBox(
                width: narrow ? 390 : 900,
                child: ChatScreen(
                  controller: widget.controller,
                  conversation: widget.repository.group,
                ),
              ),
            ),
          ),
        ],
      ),
    ),
  );
}

class GroupPolicyPreviewRepository extends DemoImRepository {
  GroupPolicyPreviewRepository() : super(latency: Duration.zero);
  String role = 'member';
  final bus = StreamController<ImEvent>.broadcast();
  Conversation get group => Conversation(
    id: 'policy-preview',
    title: '群管理显示测试',
    subtitle: '',
    updatedAt: DateTime.now(),
    kind: ConversationKind.group,
    currentUserRole: role,
    channelType: 2,
  );
  final rows = <ChatMessage>[
    for (var i = 1; i <= 4; i++)
      ChatMessage(
        id: 'm$i',
        clientMessageId: 'c$i',
        conversationId: 'policy-preview',
        senderId: 'peer',
        senderName: '测试成员',
        text: i == 1
            ? '这是一条普通成员的消息'
            : i == 2
            ? '邀请加入提示'
            : i == 3
            ? '旧消息'
            : '群公告：本周会议',
        sentAt: DateTime.now().subtract(Duration(minutes: 5 - i)),
        isMine: false,
        conversationSeq: i,
        status: i == 3 ? MessageStatus.recalled : MessageStatus.sent,
        kind: i == 2 || i == 4
            ? MessageContentKind.system
            : MessageContentKind.text,
        event: i == 2
            ? 'group.members.added'
            : i == 4
            ? 'group.announcement.updated'
            : null,
      ),
  ];
  void changeRole(String value) {
    role = value;
    bus.add(
      const ImEvent(
        type: ImEventType.conversationChanged,
        payload: {'conversationId': 'policy-preview'},
      ),
    );
  }

  @override
  Stream<ImEvent> get events => bus.stream;
  @override
  Future<List<Conversation>> conversations() async => [group];
  @override
  Future<List<ChatMessage>> messages(String conversationId) async =>
      List.of(rows);
  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}
  @override
  Future<void> recallMessage(String id) async {
    final index = rows.indexWhere((m) => m.id == id);
    rows[index] = rows[index].copyWith(
      text: '',
      status: MessageStatus.recalled,
    );
    bus.add(
      ImEvent(
        type: ImEventType.messageRecalled,
        payload: {'conversationId': 'policy-preview', 'messageId': id},
      ),
    );
  }
}
