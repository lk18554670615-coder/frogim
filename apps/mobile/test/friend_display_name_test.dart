import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/group_management_screens.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/screens/people_screens.dart';
import 'package:linli_im/ui/screens/relationship_screens.dart';
import 'package:linli_im/ui/widgets/forward_conversation_sheet.dart';
import 'package:shared_preferences/shared_preferences.dart';

const privateRemark = '项目搭档（仅我可见）';
const publicPeer = AppUser(
  id: 'u1',
  name: '林屿',
  handle: 'linyu',
  presence: '在线',
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('备注只影响显示，规范空白并保留原昵称', () {
    final noted = publicPeer.copyWith(remark: '  $privateRemark  ');
    expect(noted.displayName, privateRemark);
    expect(noted.name, publicPeer.name);
    expect(noted.copyWith(remark: ' \n ').displayName, publicPeer.name);
  });

  test('实时联系人优先；清空、删除好友后不使用旧对象里的备注', () async {
    final controller = AppController(_RemarkRepository())
      ..contacts = [publicPeer.copyWith(remark: privateRemark)];
    addTearDown(controller.dispose);
    final stale = controller.contacts.single;
    expect(controller.displayNameFor(publicPeer), privateRemark);
    expect(controller.displayNameForId('u1', fallback: '旧昵称'), privateRemark);
    expect(
      await controller.updateFriendMetadata(stale, remark: '', tags: []),
      isTrue,
    );
    expect(controller.displayNameFor(stale), publicPeer.name);
    expect(
      controller.displayNameFor(publicPeer, groupNickname: '群内昵称'),
      '群内昵称',
    );
    await controller.updateFriendMetadata(
      stale,
      remark: privateRemark,
      tags: [],
    );
    expect(
      controller.displayNameFor(publicPeer, groupNickname: '群内昵称'),
      privateRemark,
    );
    await controller.deleteFriend(stale);
    expect(controller.displayNameFor(stale), publicPeer.name);
    expect(controller.displayNameForId('u1', fallback: '未知发送者'), '未知发送者');
  });

  test('单聊按对端 ID 显示备注，群名及原始会话对象不改写', () {
    final controller = AppController(_RemarkRepository())
      ..currentUser = DemoImRepository.demoUser
      ..contacts = [publicPeer.copyWith(remark: privateRemark)];
    addTearDown(controller.dispose);
    final direct = Conversation(
      id: 'direct',
      title: '旧会话名称',
      subtitle: '',
      updatedAt: DateTime(2026),
      kind: ConversationKind.direct,
      channelId: 'u1',
      members: [DemoImRepository.demoUser, publicPeer],
    );
    expect(controller.displayConversationName(direct), privateRemark);
    expect(
      controller.displayConversationName(direct.copyWith(members: [])),
      privateRemark,
    );
    expect(direct.title, '旧会话名称');
    expect(direct.members.last.name, publicPeer.name);
    final group = Conversation(
      id: 'group',
      title: '群名不变',
      subtitle: '',
      updatedAt: DateTime(2026),
      kind: ConversationKind.group,
      channelId: 'u1',
      members: [publicPeer],
    );
    expect(controller.displayConversationName(group), '群名不变');
  });

  testWidgets('联系人按备注分组，修改和清空后立即更新', (tester) async {
    final controller = await fixture(tester);
    await changeRemark(controller, 'Alice备注');
    await page(tester, ContactsTab(controller: controller));
    expect(find.text('Alice备注'), findsOneWidget);
    await changeRemark(controller, 'Zulu备注');
    await tester.pumpAndSettle();
    expect(find.text('Alice备注'), findsNothing);
    expect(find.byKey(const Key('contact-group-Z')), findsOneWidget);
    await changeRemark(controller, '');
    await tester.pumpAndSettle();
    expect(find.text('Zulu备注'), findsNothing);
    expect(find.text(publicPeer.name), findsOneWidget);
    expect(find.byKey(const Key('contact-group-L')), findsOneWidget);
  });

  for (final desktop in [false, true]) {
    testWidgets('会话列表备注更新（desktop=$desktop）', (tester) async {
      final controller = await fixture(tester);
      await changeRemark(controller, privateRemark);
      await page(
        tester,
        ConversationsTab(controller: controller, desktopMode: desktop),
        desktop: desktop,
      );
      final direct = directConversation(controller);
      final title = find.byKey(Key('conversation-title-${direct.id}'));
      expect(tester.widget<Text>(title).data, privateRemark);
      await changeRemark(controller, '新备注');
      await tester.pumpAndSettle();
      expect(tester.widget<Text>(title).data, '新备注');
      await changeRemark(controller, '');
      await tester.pumpAndSettle();
      expect(tester.widget<Text>(title).data, publicPeer.name);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('聊天标题不依赖会话重新加载就能更新备注', (tester) async {
    final controller = await fixture(tester);
    final direct = directConversation(controller);
    await changeRemark(controller, privateRemark);
    await page(
      tester,
      ChatScreen(controller: controller, conversation: direct),
    );
    expect(
      find.descendant(
        of: find.byType(AppBar),
        matching: find.text(privateRemark),
      ),
      findsOneWidget,
    );
    await changeRemark(controller, '新备注');
    await tester.pumpAndSettle();
    expect(
      find.descendant(of: find.byType(AppBar), matching: find.text('新备注')),
      findsOneWidget,
    );
    await changeRemark(controller, '');
    await tester.pumpAndSettle();
    expect(
      find.descendant(
        of: find.byType(AppBar),
        matching: find.text(publicPeer.name),
      ),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('个人资料显示备注并保留公开昵称', (tester) async {
    final controller = await fixture(tester);
    await changeRemark(controller, privateRemark);
    await page(
      tester,
      FriendProfileScreen(controller: controller, user: publicPeer),
    );
    expect(find.text(privateRemark), findsWidgets);
    expect(find.text('昵称：${publicPeer.name}'), findsOneWidget);
    await changeRemark(controller, '');
    await tester.pumpAndSettle();
    expect(find.text(privateRemark), findsNothing);
    expect(find.text(publicPeer.name), findsOneWidget);
    expect(find.text('昵称：${publicPeer.name}'), findsNothing);
  });

  testWidgets('本地搜索支持备注和公开昵称，结果主名称为备注', (tester) async {
    final controller = await fixture(tester);
    await changeRemark(controller, privateRemark);
    await page(tester, SearchScreen(controller: controller));
    for (final query in [privateRemark, publicPeer.name, publicPeer.handle]) {
      await tester.enterText(find.byType(TextField).first, query);
      await tester.pumpAndSettle(const Duration(milliseconds: 400));
      expect(find.text(privateRemark), findsWidgets);
    }
    expect(tester.takeException(), isNull);
  });

  testWidgets('群成员优先备注，清空恢复群昵称，搜索同时支持两者', (tester) async {
    final controller = await fixture(tester);
    final group = controller.conversations.firstWhere(
      (item) => item.id == 'c-team',
    );
    final profile = await tester.runAsync(
      () => controller.loadGroupProfile(group.id),
    );
    final members = await tester.runAsync(
      () => controller.loadGroupMembers(group.id),
    );
    final users = members!
        .map(
          (member) => GroupMember(
            user: member.user,
            role: member.role,
            joinedAt: member.joinedAt,
            groupNickname: member.user.id == 'u1'
                ? '群内昵称'
                : member.groupNickname,
          ),
        )
        .toList();
    await changeRemark(controller, privateRemark);
    await page(
      tester,
      GroupMembersManagementScreen(
        controller: controller,
        conversationId: group.id,
        profile: profile!,
        initialMembers: users,
      ),
    );
    await tester.enterText(
      find.byKey(const Key('group-member-search')),
      privateRemark,
    );
    await tester.pumpAndSettle();
    expect(find.widgetWithText(ListTile, privateRemark), findsOneWidget);
    await tester.enterText(
      find.byKey(const Key('group-member-search')),
      '群内昵称',
    );
    await tester.pumpAndSettle();
    expect(find.text(privateRemark), findsOneWidget);
    await changeRemark(controller, '');
    await tester.pumpAndSettle();
    expect(find.widgetWithText(ListTile, '群内昵称'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('群消息发送者与 @ 选择显示备注，但插入与发送使用公开昵称', (tester) async {
    final controller = await fixture(tester);
    final group = controller.conversations.firstWhere(
      (item) => item.id == 'c-team',
    );
    await changeRemark(controller, privateRemark);
    await page(tester, ChatScreen(controller: controller, conversation: group));
    final raw = controller
        .messagesFor(group.id)
        .firstWhere((item) => item.senderId == 'u1');
    expect(raw.senderName, publicPeer.name);
    expect(find.text(privateRemark), findsWidgets);
    await tester.tap(find.byKey(const Key('mention-member-button')));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const Key('mention-member-search')),
      privateRemark,
    );
    await tester.pumpAndSettle();
    final member = find.byKey(const Key('mention-member-u1'));
    expect(
      find.descendant(of: member, matching: find.text(privateRemark)),
      findsOneWidget,
    );
    await tester.tap(member);
    await tester.pumpAndSettle();
    final input = tester
        .widget<TextField>(find.byKey(const Key('message-input')))
        .controller!;
    expect(input.text, '@${publicPeer.name} ');
    final composer = tester.widget<ChatComposer>(find.byType(ChatComposer));
    await tester.runAsync(() async => composer.onSend());
    await tester.pumpAndSettle();
    final sent = (controller.repository as _RemarkRepository).sent.last;
    expect(sent.mentions.single.name, publicPeer.name);
    expect(sent.mentions.single.userId, publicPeer.id);
    expect(jsonEncode(sent.toJson()), isNot(contains(privateRemark)));
    expect(raw.senderName, publicPeer.name);
  });

  testWidgets('创建群聊选人保留备注且不改写联系人公开资料', (tester) async {
    final controller = await fixture(tester);
    await changeRemark(controller, privateRemark);
    await page(tester, CreateGroupScreen(controller: controller));
    await tester.enterText(
      find.byKey(const Key('create-group-search')),
      privateRemark,
    );
    await tester.pumpAndSettle();
    final row = find.byKey(const Key('create-group-contact-u1'));
    expect(
      find.descendant(of: row, matching: find.text(privateRemark)),
      findsOneWidget,
    );
    await tester.tap(row);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('create-group-selected-u1')), findsOneWidget);
    expect(
      controller.contacts.firstWhere((item) => item.id == 'u1').name,
      publicPeer.name,
    );
  });

  testWidgets('转发面板按备注搜索、保持目标 ID，清空备注实时回退', (tester) async {
    final controller = await fixture(tester);
    final direct = directConversation(controller);
    await tester.runAsync(() => controller.loadMessages(direct.id));
    final source = controller.messagesFor(direct.id).first;
    await changeRemark(controller, privateRemark);
    await page(
      tester,
      ForwardConversationSheet(
        controller: controller,
        conversations: [direct],
        messages: [source],
        mode: 'separate',
      ),
    );
    await tester.enterText(
      find.byKey(const Key('forward-conversation-search')),
      privateRemark,
    );
    await tester.pumpAndSettle();
    final row = find.byKey(Key('forward-conversation-${direct.id}'));
    expect(
      find.descendant(of: row, matching: find.text(privateRemark)),
      findsOneWidget,
    );
    await tester.tap(row);
    await tester.pumpAndSettle();
    expect(find.text('转发（1）'), findsOneWidget);
    await tester.enterText(
      find.byKey(const Key('forward-conversation-search')),
      '',
    );
    await changeRemark(controller, '');
    await tester.pumpAndSettle();
    expect(
      find.descendant(of: row, matching: find.text(publicPeer.name)),
      findsOneWidget,
    );
    expect(find.text('转发（1）'), findsOneWidget);
    expect(jsonEncode(source.toJson()), isNot(contains(privateRemark)));
  });

  testWidgets('发送名片和引用不包含私人备注', (tester) async {
    final controller = await fixture(tester);
    final direct = directConversation(controller);
    await tester.runAsync(() => controller.loadMessages(direct.id));
    final original = controller
        .messagesFor(direct.id)
        .firstWhere((item) => item.senderId == 'u1');
    await changeRemark(controller, privateRemark);
    final noted = controller.contacts.firstWhere((item) => item.id == 'u1');
    await tester.runAsync(
      () => controller.sendContact(direct.id, noted, replyTo: original),
    );
    final card = (controller.repository as _RemarkRepository).sent.last;
    expect(card.contactName, publicPeer.name);
    expect(card.contactUserId, publicPeer.id);
    expect(card.replyToSenderName, original.senderName);
    expect(jsonEncode(card.toJson()), isNot(contains(privateRemark)));
  });

  testWidgets('群聊天信息页成员备注立即刷新', (tester) async {
    final controller = await fixture(tester);
    final group = controller.conversations.firstWhere(
      (item) => item.id == 'c-team',
    );
    await changeRemark(controller, privateRemark);
    await page(
      tester,
      ChatInfoScreen(
        controller: controller,
        conversation: group,
        onSearch: () {},
        onClearLocal: () async {},
        onBlock: () async {},
        onScheduledMessages: () {},
      ),
    );
    final row = find.byKey(const ValueKey('chat-info-member-u1'));
    expect(
      find.descendant(of: row, matching: find.text(privateRemark)),
      findsOneWidget,
    );
    await changeRemark(controller, '新群成员备注');
    await tester.pumpAndSettle();
    expect(
      find.descendant(of: row, matching: find.text('新群成员备注')),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('长备注在窄屏深色200%字体下不溢出，消息内容保持原样', (tester) async {
    final controller = await fixture(tester);
    await changeRemark(controller, privateRemark * 5);
    final message = ChatMessage(
      id: 'remark-layout',
      conversationId: 'c-team',
      senderId: 'u1',
      senderName: publicPeer.name,
      text: '原始消息内容',
      sentAt: DateTime.now(),
      isMine: false,
    );
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(320, 844);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.view.resetPhysicalSize);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.dark),
        home: MediaQuery(
          data: const MediaQueryData(
            size: Size(320, 844),
            textScaler: TextScaler.linear(2),
          ),
          child: Scaffold(
            body: MessageBubble(
              message: message,
              controller: controller,
              showSender: true,
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text(privateRemark * 5), findsOneWidget);
    expect(find.text('原始消息内容'), findsOneWidget);
    expect(tester.takeException(), isNull);
    expect(message.senderName, publicPeer.name);
  });
}

Future<AppController> fixture(WidgetTester tester) async {
  final controller = AppController(_RemarkRepository());
  addTearDown(controller.dispose);
  await tester.runAsync(controller.loginAsDemo);
  return controller;
}

Future<void> changeRemark(AppController controller, String remark) async {
  expect(
    await controller.updateFriendMetadata(publicPeer, remark: remark, tags: []),
    isTrue,
  );
}

Conversation directConversation(AppController controller) =>
    controller.conversations.firstWhere(
      (item) =>
          item.kind == ConversationKind.direct &&
          item.members.any((user) => user.id == 'u1'),
    );

Future<void> page(
  WidgetTester tester,
  Widget child, {
  bool desktop = false,
}) async {
  tester.view.devicePixelRatio = 1;
  tester.view.physicalSize = desktop
      ? const Size(1280, 900)
      : const Size(390, 844);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.view.resetPhysicalSize);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(desktop ? Brightness.dark : Brightness.light),
      home: Scaffold(body: child),
    ),
  );
  await tester.pumpAndSettle();
}

class _RemarkRepository extends DemoImRepository {
  _RemarkRepository() : super(latency: Duration.zero, store: _MemoryStore());
  final remarks = <String, String>{};
  final sent = <ChatMessage>[];

  @override
  Future<List<AppUser>> contacts() async => [
    for (final user in DemoImRepository.people)
      user.copyWith(remark: remarks[user.id] ?? ''),
  ];

  @override
  Future<void> updateFriendMetadata(
    String userId, {
    required String remark,
    required List<String> tags,
  }) async {
    remarks[userId] = remark;
  }

  @override
  Future<ChatMessage> send(ChatMessage pending) {
    sent.add(pending);
    return super.send(pending);
  }
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

  @override
  Future<void> clearAccountData() async {
    values.clear();
  }
}
