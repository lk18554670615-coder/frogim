// Local-only QA entry point. Never imported by lib/main.dart and never talks to
// a business/IM server. Run with flutter run -d web-server -t tool/chat_scroll_preview.dart.
import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/im_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';

// Keep the handle alive for the lifetime of this standalone QA application.
SemanticsHandle? _semantics;

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  _semantics ??= SemanticsBinding.instance.ensureSemantics();
  final repository = ScrollPreviewRepository();
  final controller = AppController(repository);
  await controller.loginAsDemo();
  runApp(ScrollPreview(controller: controller, repository: repository));
}

final previewConversation = Conversation(
  id: 'local-scroll-preview',
  title: '历史滚动测试（仅本地）',
  subtitle: '',
  updatedAt: DateTime(2026, 9, 2, 12),
  kind: ConversationKind.direct,
  members: const [
    AppUser(
      id: 'scroll-peer',
      name: '滚动测试用户',
      handle: 'scroll-test',
      presence: '在线',
    ),
  ],
);

class ScrollPreview extends StatefulWidget {
  const ScrollPreview({
    super.key,
    required this.controller,
    required this.repository,
  });
  final AppController controller;
  final ScrollPreviewRepository repository;
  @override
  State<ScrollPreview> createState() => _ScrollPreviewState();
}

class _ScrollPreviewState extends State<ScrollPreview> {
  final metrics = ValueNotifier('等待消息布局');
  bool dark = false;
  bool narrow = false;
  int chatGeneration = 0;

  bool _metrics(ScrollMetrics m) {
    if (m.axis != Axis.vertical) return false;
    final value =
        '偏移 ${m.pixels.toStringAsFixed(1)} / 底部余量 ${m.extentAfter.toStringAsFixed(1)}';
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) metrics.value = value;
    });
    return false;
  }

  @override
  void dispose() {
    metrics.dispose();
    widget.controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    theme: buildLinliTheme(dark ? Brightness.dark : Brightness.light),
    home: Scaffold(
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(8),
            child: Wrap(
              spacing: 12,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                const Text('本地合成数据，不连接服务器'),
                FilledButton(
                  onPressed: widget.repository.incoming,
                  child: const Text('收到新消息'),
                ),
                OutlinedButton(
                  onPressed: () {
                    widget.repository.delayRefresh = true;
                    unawaited(
                      widget.controller.loadMessages(
                        previewConversation.id,
                        force: true,
                      ),
                    );
                  },
                  child: const Text('延迟刷新（3秒）'),
                ),
                OutlinedButton(
                  onPressed: () => setState(() => dark = !dark),
                  child: const Text('切换深浅色'),
                ),
                OutlinedButton(
                  onPressed: () => setState(() => narrow = !narrow),
                  child: const Text('切换窄聊天列'),
                ),
                OutlinedButton(
                  onPressed: () => setState(() => chatGeneration++),
                  child: const Text('重新进入会话'),
                ),
                ValueListenableBuilder(
                  valueListenable: metrics,
                  builder: (_, value, _) => Text(value),
                ),
              ],
            ),
          ),
          Expanded(
            child: Row(
              children: [
                const SizedBox(
                  width: 304,
                  child: Align(
                    alignment: Alignment.topCenter,
                    child: Padding(
                      padding: EdgeInsets.all(20),
                      child: Text('会话列\n\n历史滚动测试'),
                    ),
                  ),
                ),
                Expanded(
                  child: Align(
                    alignment: Alignment.centerRight,
                    child: SizedBox(
                      width: narrow ? 480 : double.infinity,
                      child: NotificationListener<ScrollMetricsNotification>(
                        onNotification: (n) => _metrics(n.metrics),
                        child: NotificationListener<ScrollNotification>(
                          onNotification: (n) => _metrics(n.metrics),
                          child: ChatScreen(
                            key: ValueKey(chatGeneration),
                            controller: widget.controller,
                            conversation: previewConversation,
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    ),
  );
}

class ScrollPreviewRepository extends DemoImRepository
    implements CachedMessageRepository, PaginatedMessageRepository {
  ScrollPreviewRepository() : super(latency: Duration.zero);
  final _events = StreamController<ImEvent>.broadcast();
  int latest = 100;
  bool delayRefresh = false;
  @override
  Stream<ImEvent> get events => _events.stream;
  @override
  Future<List<Conversation>> conversations() async => [previewConversation];
  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) async =>
      _page(51, latest);
  @override
  Future<List<ChatMessage>> messages(String conversationId) async {
    if (delayRefresh) {
      delayRefresh = false;
      await Future<void>.delayed(const Duration(seconds: 3));
      latest++;
    }
    return _page(51, latest);
  }

  @override
  Future<List<ChatMessage>> olderMessages(
    String conversationId, {
    required int beforeSequence,
    int limit = 50,
  }) async {
    await Future<void>.delayed(const Duration(seconds: 1));
    return _page(math.max(1, beforeSequence - limit), beforeSequence - 1);
  }

  void incoming() {
    latest++;
    _events.add(
      ImEvent(
        type: ImEventType.messageCreated,
        payload: {'message': _message(latest).toJson()},
      ),
    );
  }

  List<ChatMessage> _page(int first, int last) => [
    for (var i = first; i <= last; i++) _message(i),
  ];
  ChatMessage _message(int i) => ChatMessage(
    id: 'scroll-message-$i',
    clientMessageId: 'scroll-client-$i',
    conversationId: previewConversation.id,
    senderId: 'scroll-peer',
    senderName: '滚动测试用户',
    text: '测试消息 $i：滚轮上翻后应停留在这里。${'\n长短消息混排验证' * (i % 3)}',
    sentAt: DateTime(2026, 9, 2, 12).add(Duration(seconds: i)),
    isMine: false,
    conversationSeq: i,
    status: MessageStatus.sent,
  );
  @override
  Future<String> readDraft(String conversationId) async => '';
  @override
  Future<void> saveDraft(String conversationId, String text) async {}
  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}
  @override
  Future<void> close() async {
    await _events.close();
    await super.close();
  }
}
