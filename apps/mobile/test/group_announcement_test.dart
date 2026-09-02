import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/group_message_policy.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/message_content_registry.dart';
import 'package:linli_im/im/message_mapper.dart';
import 'package:linli_im/im/structured_event_text.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/group_management_screens.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  const recordChannel = MethodChannel('com.llfbandit.record/messages');
  setUpAll(
    () => messenger.setMockMethodCallHandler(recordChannel, (_) async => null),
  );
  tearDownAll(() => messenger.setMockMethodCallHandler(recordChannel, null));
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('旧版通用系统消息按公告事件渲染，所有角色均可见', () {
    const payload = <String, Object?>{
      'type': 1002,
      'schemaVersion': 1,
      'event': groupAnnouncementUpdatedEvent,
      'digest': '[群系统消息]',
      'data': {},
    };
    final message = MessageMapper().toChatMessage(
      WukongMessage(
        messageId: 'notice',
        messageSeq: 1,
        clientMsgNo: 'notice',
        clientSeq: 0,
        fromUid: 'u1',
        channel: const WukongChannel(id: 'c-team', type: 2),
        timestamp: DateTime(2026),
        payload: payload,
        state: WukongMessageState.sent,
      ),
      currentUserId: 'me',
      conversationId: 'c-team',
    );
    expect(message.text, groupAnnouncementUpdatedText);
    expect(
      MessageContentRegistry.standard().digest(payload),
      groupAnnouncementUpdatedText,
    );
    for (final role in ['owner', 'admin', 'member', null]) {
      final group = Conversation(
        id: 'c-team',
        title: '群',
        subtitle: '',
        updatedAt: DateTime(2026),
        kind: ConversationKind.group,
        currentUserRole: role,
      );
      expect(
        canPresentGroupMessage(message, group, roleTrusted: false),
        isTrue,
      );
    }
    expect(
      messagePreviewText(message.copyWith(text: '[群系统消息]')),
      groupAnnouncementUpdatedText,
    );
  });

  for (final role in ['owner', 'admin', 'member']) {
    for (final width in [390.0, 1280.0]) {
      testWidgets('$role / $width 聊天顶部及旧公告提示可查看全文', (tester) async {
        final repository = _Announcements(role);
        final controller = await _open(tester, repository, width: width);
        final banner = find.byKey(const Key('chat-group-announcement'));
        expect(banner, findsOneWidget);
        expect(find.text('群公告：第一版公告\n请按时参加会议'), findsOneWidget);
        await tester.tap(banner);
        await tester.pumpAndSettle();
        expect(find.byType(SelectableText), findsOneWidget);
        expect(find.text('第一版公告\n请按时参加会议'), findsOneWidget);
        expect(
          find.text('编辑'),
          role == 'member' ? findsNothing : findsOneWidget,
        );
        expect(repository.reads, greaterThan(0));
        await tester.pageBack();
        await tester.pumpAndSettle();
        final notice = find.byKey(
          const Key('group-announcement-notice-notice'),
        );
        await tester.ensureVisible(notice);
        await tester.tap(notice);
        await tester.pumpAndSettle();
        expect(find.byType(GroupAnnouncementScreen), findsOneWidget);
        expect(find.text('[群系统消息]'), findsNothing);
        expect(controller.error, isNull);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
      });
    }
  }

  testWidgets('普通成员已打开的公告页收到 CMD 后刷新，清空公告也同步', (tester) async {
    final repository = _Announcements('member');
    final controller = await _open(tester, repository);
    await tester.tap(find.byKey(const Key('chat-group-announcement')));
    await tester.pumpAndSettle();
    final revision = controller.groupSendPolicyRevision;
    await _publish(tester, repository, '第二版公告');
    await tester.pump();
    await tester.pumpAndSettle(const Duration(milliseconds: 200));
    expect(controller.groupSendPolicyRevision, greaterThan(revision));
    expect(
      find.text('第二版公告'),
      findsOneWidget,
      reason:
          'profile reads: ${repository.profileReads}, route: ${find.byType(GroupAnnouncementScreen).evaluate().length}',
    );
    expect(find.text('第一版公告\n请按时参加会议'), findsNothing);
    await _publish(tester, repository, '');
    await tester.pump();
    await tester.pumpAndSettle(const Duration(milliseconds: 200));
    expect(find.text('暂无群公告'), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('chat-group-announcement')), findsNothing);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('初始快照过期会重新获取，错误可重试，不清空已有正文', (tester) async {
    final repository = _Announcements('member');
    final controller = await _open(tester, repository);
    repository.content = '服务端最新公告'; // No CMD: simulates an offline update.
    await tester.tap(find.byKey(const Key('chat-group-announcement')));
    await tester.pumpAndSettle();
    expect(find.text('服务端最新公告'), findsOneWidget);
    repository.fail = true;
    await tester.tap(find.byKey(const Key('refresh-group-announcement')));
    await tester.pumpAndSettle();
    expect(find.text('服务端最新公告'), findsOneWidget);
    expect(find.text('群公告加载失败'), findsOneWidget);
    repository.fail = false;
    repository.content = '重试后公告';
    await tester.tap(find.byKey(const Key('refresh-group-announcement')));
    await tester.pumpAndSettle();
    expect(find.text('重试后公告'), findsOneWidget);
    expect(find.text('群公告加载失败'), findsNothing);
    expect(controller.currentUser, isNotNull);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('编辑草稿不被远端刷新覆盖，发布后聊天立即显示新公告', (tester) async {
    final repository = _Announcements('owner');
    await _open(tester, repository);
    await tester.tap(find.byKey(const Key('chat-group-announcement')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('编辑'));
    await tester.pumpAndSettle();
    final input = find.byKey(const Key('group-announcement-input'));
    await tester.enterText(input, '我的编辑草稿');
    await _publish(tester, repository, '他人更新');
    await tester.pump();
    await tester.pumpAndSettle(const Duration(milliseconds: 200));
    expect(tester.widget<TextField>(input).controller!.text, '我的编辑草稿');
    await tester.tap(find.text('发布'));
    await tester.pumpAndSettle();
    expect(find.text('我的编辑草稿'), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle();
    expect(find.text('群公告：我的编辑草稿'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  });
}

Future<void> _publish(
  WidgetTester tester,
  _Announcements repository,
  String content,
) async {
  // loginAsDemo subscribes in runAsync, so the stream listener's debounce is
  // a real timer. Pumping only the test clock does not run that callback.
  await tester.runAsync(() async {
    repository.publish(content);
    await Future<void>.delayed(const Duration(milliseconds: 220));
  });
}

Future<AppController> _open(
  WidgetTester tester,
  _Announcements repository, {
  double width = 390,
}) async {
  final controller = AppController(repository);
  await tester.runAsync(controller.loginAsDemo);
  addTearDown(controller.dispose);
  addTearDown(repository.updates.close);
  tester.view.physicalSize = Size(width, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(width > 600 ? Brightness.dark : Brightness.light),
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: const TextScaler.linear(1.3)),
        child: child!,
      ),
      home: ChatScreen(
        controller: controller,
        conversation: controller.conversations.firstWhere(
          (item) => item.id == 'c-team',
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return controller;
}

class _Announcements extends DemoImRepository {
  _Announcements(this.role)
    : super(latency: Duration.zero, store: _MemoryStore());
  final String role;
  String content = '第一版公告\n请按时参加会议';
  int version = 1;
  int reads = 0;
  bool fail = false;
  final profileReads = <String>[];
  final updates = StreamController<ImEvent>.broadcast();
  @override
  Stream<ImEvent> get events => updates.stream;
  void publish(String value) {
    content = value;
    version++;
    updates.add(
      const ImEvent(
        type: ImEventType.conversationChanged,
        payload: {'conversationId': 'c-team', 'groupSendPolicyChanged': true},
      ),
    );
  }

  @override
  Future<List<Conversation>> conversations() async => [
    for (final item in await super.conversations())
      item.id == 'c-team' ? item.copyWith(currentUserRole: role) : item,
  ];
  @override
  Future<GroupProfile> groupProfile(String conversationId) async {
    profileReads.add(content);
    if (fail) throw const FormatException('群公告加载失败');
    return GroupProfile(
      conversationId: conversationId,
      ownerId: role == 'owner' ? 'me' : 'u1',
      name: '测试群',
      announcement: content,
      announcementVersion: version,
      joinPolicy: 'invite',
      allowMemberAddFriend: true,
      updatedAt: DateTime(2026),
    );
  }

  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async => [
    GroupMember(
      user: DemoImRepository.demoUser,
      role: role,
      joinedAt: DateTime(2026),
    ),
  ];
  @override
  Future<void> markGroupAnnouncementRead(String conversationId) async {
    reads++;
  }

  @override
  Future<GroupProfile> setGroupAnnouncement(
    String conversationId,
    String content,
  ) async {
    this.content = content;
    version++;
    return groupProfile(conversationId);
  }

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    ChatMessage(
      id: 'notice',
      clientMessageId: 'notice',
      conversationId: conversationId,
      senderId: 'u1',
      senderName: '群主',
      text: '[群系统消息]',
      sentAt: DateTime(2026),
      isMine: false,
      kind: MessageContentKind.system,
      event: groupAnnouncementUpdatedEvent,
      conversationSeq: 1,
    ),
  ];
}

class _MemoryStore extends SecureLocalStore {
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
