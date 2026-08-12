import 'dart:async';
import 'dart:io';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/main.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/group_management_screens.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('开发登录进入消息首页', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      LinliApp(
        repository: DemoImRepository(latency: const Duration(milliseconds: 1)),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('登录邻里通讯'), findsOneWidget);
    await tester.tap(find.byKey(const Key('demo-login-button')));
    await tester.pumpAndSettle();
    expect(find.text('消息'), findsWidgets);
    expect(find.text('邻里产品小组'), findsOneWidget);
  });

  testWidgets('会话列表显示未读和演示连接状态', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: ConversationsTab(controller: controller)),
      ),
    );
    expect(find.text('邻里产品小组'), findsOneWidget);
    expect(find.text('林屿'), findsOneWidget);
    expect(find.text('3'), findsOneWidget);

    final header = tester.getRect(find.byKey(const Key('messages-header')));
    expect(header.height, lessThanOrEqualTo(160));
    expect(find.byKey(const Key('messages-signal-accent')), findsNothing);
    expect(find.text('置顶'), findsOneWidget);
    expect(
      find.byKey(const ValueKey('conversation-pinned-indicator-c-team')),
      findsOneWidget,
    );
    expect(
      tester.getSize(find.byKey(const ValueKey('conversation-avatar-c-team'))),
      const Size.square(48),
    );
    final conversationTitle = tester.widget<Text>(
      find.byKey(const ValueKey('conversation-title-c-team')),
    );
    expect(conversationTitle.style?.fontSize, 16);

    await tester.tap(find.text('单聊'));
    await tester.pumpAndSettle();
    expect(find.text('林屿'), findsOneWidget);
    expect(find.text('邻里产品小组'), findsNothing);

    await tester.tap(find.text('群聊'));
    await tester.pumpAndSettle();
    expect(find.text('邻里产品小组'), findsOneWidget);
    expect(find.text('林屿'), findsNothing);

    expect(find.text('未读 4'), findsOneWidget);
    expect(find.text('@我 2'), findsOneWidget);
    expect(
      tester
          .getSize(find.byKey(const Key('unread-conversation-filter')))
          .height,
      greaterThanOrEqualTo(44),
    );
    await tester.tap(find.byKey(const Key('unread-conversation-filter')));
    await tester.pumpAndSettle();
    expect(find.text('邻里产品小组'), findsOneWidget);
    expect(find.text('林屿'), findsOneWidget);
    expect(find.text('设计评审'), findsNothing);

    await tester.tap(find.byKey(const Key('mentioned-conversation-filter')));
    await tester.pumpAndSettle();
    expect(find.text('邻里产品小组'), findsOneWidget);
    expect(find.text('林屿'), findsNothing);
  });

  testWidgets('服务端未返回提醒计数时隐藏 @我筛选', (tester) async {
    final controller = AppController(NoMentionRepository());
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: ConversationsTab(controller: controller)),
      ),
    );
    expect(controller.supportsMentionUnread, isFalse);
    expect(
      find.byKey(const Key('mentioned-conversation-filter')),
      findsNothing,
    );
  });

  testWidgets('宽屏使用会话与聊天双栏而不是放大手机页面', (tester) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: HomeScreen(controller: controller, onToggleTheme: () {}),
      ),
    );
    await tester.pumpAndSettle();

    expect(
      find.byKey(const Key('wide-conversation-workspace')),
      findsOneWidget,
    );
    expect(find.byKey(const ValueKey('wide-chat-c-team')), findsOneWidget);
    expect(find.byKey(const Key('desktop-chat-details-panel')), findsOneWidget);
    await tester.tap(
      find.byKey(const ValueKey('conversation-slidable-c-linyu')),
    );
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('wide-chat-c-linyu')), findsOneWidget);
    expect(
      find.byKey(const Key('wide-conversation-workspace')),
      findsOneWidget,
    );
  });

  testWidgets('会话左滑支持置顶、已读、免打扰和安全删除', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: HomeScreen(controller: controller, onToggleTheme: () {}),
      ),
    );
    await tester.pumpAndSettle();

    final firstTabIcon = tester.widget<Icon>(
      find.descendant(
        of: find.byKey(const Key('home-tab-icon-0')),
        matching: find.byType(Icon),
      ),
    );
    expect(firstTabIcon.size, 24);

    await tester.drag(
      find.byKey(const ValueKey('conversation-slidable-c-team')),
      const Offset(-340, 0),
    );
    await tester.pumpAndSettle();
    expect(
      find.byKey(const ValueKey('conversation-pin-c-team')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('conversation-unread-c-team')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('conversation-mute-c-team')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('conversation-delete-c-team')),
      findsOneWidget,
    );
    await tester.tap(find.byKey(const ValueKey('conversation-pin-c-team')));
    await tester.pumpAndSettle();
    expect(
      find.byKey(const ValueKey('conversation-pinned-indicator-c-team')),
      findsNothing,
    );

    await tester.drag(
      find.byKey(const ValueKey('conversation-slidable-c-linyu')),
      const Offset(-340, 0),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('conversation-delete-c-linyu')));
    await tester.pumpAndSettle();
    expect(find.text('删除“林屿”会话？'), findsOneWidget);
    expect(find.textContaining('不会退出群聊'), findsOneWidget);
    await tester.tap(find.text('删除会话'));
    await tester.pumpAndSettle();
    expect(find.text('林屿'), findsNothing);
  });

  testWidgets('Web 和桌面宽度使用导航栏并限制内容宽度', (tester) async {
    tester.view.physicalSize = const Size(1200, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: HomeScreen(controller: controller, onToggleTheme: () {}),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('home-navigation-rail')), findsOneWidget);
    expect(find.byKey(const Key('home-tab-0')), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('桌面快捷键可搜索并关闭附件面板', (tester) async {
    tester.view.physicalSize = const Size(1100, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: HomeScreen(controller: controller, onToggleTheme: () {}),
      ),
    );
    await tester.pumpAndSettle();
    await tester.sendKeyDownEvent(LogicalKeyboardKey.controlLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.keyK);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.controlLeft);
    await tester.pumpAndSettle();
    expect(find.text('搜索'), findsWidgets);

    await tester.pageBack();
    await tester.pumpAndSettle();
    await tester.tap(
      find.byKey(const ValueKey('conversation-slidable-c-linyu')),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('send-button')));
    await tester.pumpAndSettle();
    expect(find.text('相册'), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.text('相册'), findsNothing);

    await tester.sendKeyDownEvent(LogicalKeyboardKey.controlLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.keyF);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.controlLeft);
    await tester.pumpAndSettle();
    expect(
      find.byKey(const Key('conversation-message-search')),
      findsOneWidget,
    );
  });

  testWidgets('媒体上传展示真实进度并在完成后移除', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repository = ControlledMediaRepository();
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final conversation = controller.conversations.firstWhere(
      (item) => item.id == 'c-linyu',
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: conversation),
      ),
    );
    await tester.pumpAndSettle();
    final send = controller.sendMedia(
      conversation.id,
      MediaUpload(
        bytes: Uint8List.fromList(List<int>.filled(128, 1)),
        fileName: '设计稿.pdf',
        mimeType: 'application/pdf',
        kind: MessageContentKind.file,
      ),
    );
    await tester.runAsync(() async {
      while (repository.pending == null) {
        await Future<void>.delayed(Duration.zero);
      }
    });
    repository.reportProgress(.5);
    await tester.pump();
    final message = controller.messagesFor(conversation.id).last;
    final progress = tester.widget<LinearProgressIndicator>(
      find.byKey(Key('media-upload-progress-${message.clientMessageId}')),
    );
    expect(progress.value, .5);
    repository.complete();
    await send;
    await tester.pumpAndSettle();
    expect(
      find.byKey(Key('media-upload-progress-${message.clientMessageId}')),
      findsNothing,
    );
  });

  testWidgets('四个一级页面使用一致的深色标题区和圆角内容面', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: HomeScreen(controller: controller, onToggleTheme: () {}),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('messages-header')), findsOneWidget);

    for (final entry in const [(1, '联系人'), (2, '探索'), (3, '我的')]) {
      await tester.tap(find.byKey(Key('home-tab-${entry.$1}')));
      await tester.pumpAndSettle();
      final header = find.byKey(ValueKey('top-level-header-${entry.$2}'));
      expect(header, findsOneWidget);
      expect(tester.getSize(header).height, 66);
      expect(
        find.byKey(ValueKey('top-level-content-${entry.$2}')),
        findsOneWidget,
      );
    }
  });

  test('发送消息经过 sending 到 sent 状态', () async {
    final repository = ControlledSendRepository();
    final controller = AppController(repository);
    await controller.loginAsDemo();
    await controller.loadMessages('c-linyu');

    final future = controller.sendMessage('c-linyu', '状态机测试');
    expect(
      controller.messagesFor('c-linyu').last.status,
      MessageStatus.sending,
    );
    while (repository.pending == null) {
      await Future<void>.delayed(Duration.zero);
    }
    repository.complete();
    await future;
    expect(controller.messagesFor('c-linyu').last.status, MessageStatus.sent);
    controller.dispose();
  });

  test('应用重启后失败媒体可从本地文件恢复并重试', () async {
    final directory = await Directory.systemTemp.createTemp('nexa-media-test-');
    final file = File('${directory.path}/retry.png');
    await file.writeAsBytes([1, 2, 3, 4]);
    final failed = ChatMessage(
      id: 'local-retry-media',
      clientMessageId: 'retry-media',
      conversationId: 'c-retry',
      senderId: 'me',
      senderName: '我',
      text: '[图片]',
      sentAt: DateTime.now(),
      isMine: true,
      kind: MessageContentKind.image,
      mediaUrl: file.path,
      fileName: 'retry.png',
      mimeType: 'image/png',
      status: MessageStatus.failed,
    );
    final repository = RestartMediaRepository(failed);
    final controller = AppController(repository);
    await controller.loginAsDemo();
    await controller.loadMessages('c-retry');

    await controller.retryMessage(controller.messagesFor('c-retry').single);

    expect(repository.upload?.bytes, [1, 2, 3, 4]);
    expect(controller.messagesFor('c-retry').single.status, MessageStatus.sent);
    controller.dispose();
    await directory.delete(recursive: true);
  });

  testWidgets('群聊页显示成员、发送者和输入区', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.first;

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: group),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('邻里产品小组'), findsOneWidget);
    expect(find.text('4 位成员'), findsOneWidget);
    expect(find.byKey(const Key('message-input')), findsOneWidget);
    expect(find.byKey(const Key('send-button')), findsOneWidget);
    expect(find.text('安然'), findsWidgets);
    expect(find.byKey(const Key('chat-more-button')), findsOneWidget);

    await tester.tap(find.byKey(const Key('send-button')));
    await tester.pumpAndSettle();
    expect(find.text('相册'), findsOneWidget);
    expect(find.text('拍摄'), findsOneWidget);
    expect(find.text('文件'), findsOneWidget);
    expect(find.text('联系人'), findsNothing);

    await tester.tap(find.byKey(const Key('chat-more-button')));
    await tester.pumpAndSettle();
    expect(find.text('聊天信息'), findsOneWidget);
    expect(find.byKey(const Key('chat-info-list')), findsOneWidget);
    expect(find.text('查找聊天内容'), findsOneWidget);
    expect(find.text('清空本地记录'), findsOneWidget);
    expect(find.text('群聊资料与管理'), findsOneWidget);
    expect(find.text('加入黑名单'), findsNothing);

    await tester.pageBack();
    await tester.pumpAndSettle();
    await tester.longPress(find.text('太好了，我已经把新的动效稿上传了。'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('message-context-menu')), findsOneWidget);
    expect(find.text('回复'), findsOneWidget);
    expect(find.text('复制'), findsOneWidget);
    expect(find.text('转发'), findsOneWidget);
    expect(find.text('收藏'), findsWidgets);
    expect(find.text('多选'), findsOneWidget);
    expect(find.text('举报'), findsOneWidget);
    expect(find.text('删除本机记录'), findsOneWidget);

    await tester.tapAt(const Offset(2, 400));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('message-context-menu')), findsNothing);

    await tester.longPress(find.text('太好了，我已经把新的动效稿上传了。'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('多选'));
    await tester.pumpAndSettle();
    expect(find.text('已选择 1 条'), findsOneWidget);
    expect(find.text('删除'), findsOneWidget);
  });

  testWidgets('直播频道显示并发送结构化直播互动', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final live = Conversation(
      id: 'live-channel-1',
      channelId: 'live-channel-1',
      channelType: 9,
      title: '社区直播间',
      subtitle: '正在直播',
      updatedAt: DateTime.now(),
      kind: ConversationKind.group,
      members: controller.contacts.take(2).toList(),
    );
    controller.conversations.add(live);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: live),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('send-button')));
    await tester.pumpAndSettle();
    expect(find.text('直播互动'), findsOneWidget);

    await tester.tap(find.text('直播互动'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('live-event-live.like')));
    await tester.pumpAndSettle();

    final sent = controller.messagesFor(live.id).last;
    expect(sent.kind, MessageContentKind.liveEvent);
    expect(sent.event, 'live.like');
    expect(
      find.byKey(Key('live-event-${sent.clientMessageId}')),
      findsOneWidget,
    );
  });

  test('普通会话不能发送直播互动', () async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await controller.loginAsDemo();
    addTearDown(controller.dispose);

    expect(
      () => controller.sendLiveEvent(
        'c-linyu',
        event: 'live.like',
        label: '❤️ 点赞了直播',
      ),
      throwsFormatException,
    );
  });

  testWidgets('群主可从群资料页修改群头像', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.firstWhere(
      (conversation) => conversation.kind == ConversationKind.group,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: GroupManagementScreen(
          controller: controller,
          conversation: group,
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('group-avatar-button')), findsOneWidget);
    expect(find.byIcon(Icons.camera_alt), findsNothing);
    expect(find.byIcon(CupertinoIcons.camera_fill), findsOneWidget);
  });

  testWidgets('单聊聊天信息直接展示联系人资料与会话设置', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final direct = controller.conversations.firstWhere(
      (conversation) => conversation.kind == ConversationKind.direct,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: direct),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('chat-more-button')));
    await tester.pumpAndSettle();

    expect(find.text('聊天信息'), findsOneWidget);
    expect(find.text('联系人'), findsOneWidget);
    expect(find.textContaining('邻里号：'), findsOneWidget);
    expect(find.text('联系人资料'), findsNothing);
    expect(find.text('置顶聊天'), findsOneWidget);
    expect(find.text('消息免打扰'), findsOneWidget);
    expect(find.text('加入黑名单'), findsOneWidget);
    expect(find.text('群聊资料'), findsNothing);
  });

  testWidgets('消息上下文菜单支持深色与 200% 字体', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.first;

    await tester.pumpWidget(
      MediaQuery(
        data: const MediaQueryData(textScaler: TextScaler.linear(2)),
        child: MaterialApp(
          theme: buildLinliTheme(Brightness.dark),
          home: ChatScreen(controller: controller, conversation: group),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.longPress(find.text('太好了，我已经把新的动效稿上传了。'));
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('message-context-menu')), findsOneWidget);
    expect(find.text('回复'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('消息首页支持深色与 200% 动态字体且无布局异常', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MediaQuery(
        data: const MediaQueryData(textScaler: TextScaler.linear(2)),
        child: MaterialApp(
          theme: buildLinliTheme(Brightness.dark),
          home: HomeScreen(controller: controller, onToggleTheme: () {}),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('messages-plus-menu')), findsOneWidget);
    expect(find.text('全部'), findsOneWidget);
    expect(find.byKey(const Key('messages-signal-accent')), findsNothing);
    expect(tester.takeException(), isNull);
  });

  test('浅色和深色主题保持品牌色与可读对比', () {
    final light = buildLinliTheme(Brightness.light);
    final dark = buildLinliTheme(Brightness.dark);
    expect(light.colorScheme.primary, LinliColors.yellow);
    expect(light.scaffoldBackgroundColor, LinliColors.background);
    expect(light.textTheme.headlineLarge?.fontSize, 32);
    expect(light.textTheme.titleLarge?.fontSize, 17);
    expect(light.textTheme.titleMedium?.fontSize, 16);
    expect(light.textTheme.titleSmall?.fontSize, 15);
    expect(light.textTheme.bodyMedium?.fontSize, 15);
    expect(light.textTheme.labelSmall?.fontSize, 12);
    expect(dark.brightness, Brightness.dark);
    expect(dark.colorScheme.surface, isNot(light.colorScheme.surface));
    expect(dark.colorScheme.onSurface, isNot(light.colorScheme.onSurface));
  });
}

class ControlledSendRepository extends DemoImRepository {
  ControlledSendRepository() : super(latency: Duration.zero);
  final completer = Completer<ChatMessage>();
  ChatMessage? pending;

  @override
  Future<ChatMessage> send(ChatMessage message) {
    pending = message;
    return completer.future;
  }

  void complete() =>
      completer.complete(pending!.copyWith(status: MessageStatus.sent));
}

class RestartMediaRepository extends DemoImRepository {
  RestartMediaRepository(this.failed) : super(latency: Duration.zero);

  final ChatMessage failed;
  MediaUpload? upload;

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [failed];

  @override
  Future<ChatMessage> sendMedia(
    ChatMessage pending,
    MediaUpload mediaUpload, {
    void Function(double progress)? onProgress,
  }) async {
    upload = mediaUpload;
    onProgress?.call(1);
    return pending.copyWith(id: 'sent-media', status: MessageStatus.sent);
  }
}

class ControlledMediaRepository extends DemoImRepository {
  ControlledMediaRepository() : super(latency: Duration.zero);

  final completer = Completer<ChatMessage>();
  ChatMessage? pending;
  void Function(double progress)? progress;

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {}

  @override
  Future<ChatMessage> sendMedia(
    ChatMessage message,
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  }) {
    pending = message;
    progress = onProgress;
    return completer.future;
  }

  void reportProgress(double value) => progress?.call(value);

  void complete() => completer.complete(
    pending!.copyWith(
      id: 'uploaded-media',
      mediaId: 'media-1',
      mediaUrl: 'https://cdn.example.com/media-1',
      status: MessageStatus.sent,
    ),
  );
}

class NoMentionRepository extends DemoImRepository {
  NoMentionRepository() : super(latency: Duration.zero);

  @override
  Future<List<Conversation>> conversations() async => [
    Conversation(
      id: 'c-no-mention-contract',
      title: '未升级服务端',
      subtitle: '正常会话仍可使用',
      updatedAt: DateTime.now(),
      kind: ConversationKind.direct,
      unread: 1,
    ),
  ];
}
