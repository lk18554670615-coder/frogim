import 'dart:async';
import 'dart:io';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/main.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:linli_im/ui/screens/group_management_screens.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/screens/moments_screen.dart';
import 'package:linli_im/ui/screens/relationship_screens.dart';
import 'package:linli_im/ui/screens/settings_screens.dart';
import 'package:linli_im/ui/widgets/voice_composer_widgets.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('未配置服务地址时显示真实错误登录页而不是白屏', (tester) async {
    await tester.pumpWidget(LinliApp(repository: ResilientImRepository()));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 700));

    expect(tester.takeException(), isNull);
    expect(find.text('登录青蛙呱呱'), findsOneWidget);
    expect(find.text('客户端尚未配置服务地址'), findsOneWidget);
  });

  testWidgets('会话恢复期间启动页保持完整品牌而不是空白转圈', (tester) async {
    await tester.pumpWidget(LinliApp(repository: _PendingRestoreRepository()));
    await tester.pump();

    expect(find.bySemanticsLabel('青蛙呱呱正在启动'), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    expect(find.text('正在启动，请稍候…'), findsOneWidget);
    expect(
      find.byWidgetPredicate(
        (widget) =>
            widget is Image &&
            widget.image is AssetImage &&
            widget.width == 160 &&
            widget.height == 160 &&
            (widget.image as AssetImage).assetName ==
                'assets/brand/qingwaguagua-mark-transparent.png',
      ),
      findsOneWidget,
    );
  });

  testWidgets('未登录启动不等待远程账号策略即可进入登录页', (tester) async {
    final repository = _PendingPolicyRepository();
    addTearDown(repository.completePolicy);

    await tester.pumpWidget(LinliApp(repository: repository));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 700));

    expect(find.text('登录青蛙呱呱'), findsOneWidget);
    expect(find.byKey(const Key('open-register')), findsOneWidget);
    expect(find.byKey(const Key('auth-policy-status-notice')), findsNothing);
    expect(find.bySemanticsLabel('青蛙呱呱正在启动'), findsNothing);
    expect(repository.policyRequests, 1);

    // Resolve the deliberately pending request so its timeout timer is
    // cancelled before the widget-test binding verifies pending timers.
    repository.completePolicy();
    await tester.pump();
  });

  testWidgets('已登录冷启动先进入消息壳层而不等待远程会话列表', (tester) async {
    final repository = _PendingRestoredCoreRepository();
    addTearDown(repository.completeCore);

    await tester.pumpWidget(LinliApp(repository: repository));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 700));

    expect(find.bySemanticsLabel('青蛙呱呱正在启动'), findsNothing);
    expect(find.text('消息'), findsWidgets);
    expect(find.byType(CupertinoActivityIndicator), findsWidgets);

    repository.completeCore();
    await tester.pumpAndSettle();
    expect(find.text('林屿'), findsWidgets);
  });

  testWidgets('首次验证码登录成功后立即进入消息壳层并后台同步会话', (tester) async {
    final repository = _PendingFreshLoginCoreRepository();
    addTearDown(repository.completeCore);

    await tester.pumpWidget(LinliApp(repository: repository));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField).first, '13800138000');
    await tester.enterText(find.byKey(const Key('code-field')), '123456');
    await tester.tap(find.byKey(const Key('policy-consent-checkbox')));
    await tester.tap(find.byKey(const Key('login-button')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 50));

    expect(find.text('登录青蛙呱呱'), findsNothing);
    expect(find.text('消息'), findsWidgets);
    expect(find.byType(CupertinoActivityIndicator), findsWidgets);

    repository.completeCore();
    await tester.pumpAndSettle();
    expect(find.text('林屿'), findsWidgets);
  });

  testWidgets('缓存会话被服务端撤销后退出旧壳层并要求重新登录', (tester) async {
    final repository = _ExpiredRestoredSessionRepository();
    addTearDown(repository.completeCore);

    await tester.pumpWidget(LinliApp(repository: repository));
    await tester.pump();
    for (var i = 0; i < 8; i++) {
      await tester.pump(const Duration(milliseconds: 100));
    }

    expect(find.text('登录青蛙呱呱'), findsOneWidget);
    expect(find.text('登录状态已失效，请重新登录'), findsOneWidget);
    expect(find.text('林屿'), findsNothing);

    repository.completeCore();
    await tester.pump();
  });

  testWidgets('运行中凭据失效时清空子页导航栈并回到登录页', (tester) async {
    final repository = _RuntimeExpiredSessionRepository();

    await tester.pumpWidget(LinliApp(repository: repository));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 700));
    await tester.enterText(find.byType(TextFormField).first, '13800138000');
    await tester.enterText(find.byKey(const Key('code-field')), '123456');
    await tester.tap(find.byKey(const Key('policy-consent-checkbox')));
    await tester.tap(find.byKey(const Key('login-button')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    await tester.tap(find.byKey(const Key('home-tab-3')));
    await tester.pump();
    await tester.tap(find.byKey(const Key('profile-settings')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    expect(find.byType(SettingsScreen), findsOneWidget);

    repository.expire();
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));
    await tester.pump(const Duration(milliseconds: 500));

    expect(find.text('登录青蛙呱呱'), findsOneWidget);
    expect(find.text('登录状态已失效，请重新登录'), findsOneWidget);
    expect(find.byType(SettingsScreen), findsNothing);
  });

  testWidgets('真实登录表单进入消息首页且不展示演示入口', (tester) async {
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
    expect(find.text('登录青蛙呱呱'), findsOneWidget);
    expect(find.text('预览演示环境'), findsNothing);
    expect(find.textContaining('开发环境'), findsNothing);
    await tester.enterText(find.byType(TextFormField).first, '13800138000');
    await tester.enterText(find.byKey(const Key('code-field')), '123456');
    await tester.tap(find.byKey(const Key('policy-consent-checkbox')));
    await tester.tap(find.byKey(const Key('login-button')));
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
    expect(
      tester.getSize(find.byKey(const Key('messages-plus-menu'))),
      const Size.square(44),
    );
    expect(
      tester.getSize(find.byKey(const Key('messages-plus-icon'))),
      const Size.square(28),
    );
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
    expect(
      find.byKey(const Key('system-notification-section')),
      findsOneWidget,
    );
    expect(
      find.byKey(const Key('system-notification-surface')),
      findsOneWidget,
    );
    final avatarRect = tester.getRect(
      find.byKey(const ValueKey('conversation-avatar-c-team')),
    );
    final conversationContentFinder = find.byKey(
      const ValueKey('conversation-content-c-team'),
    );
    final conversationContentRect = tester.getRect(conversationContentFinder);
    expect(conversationContentRect.left, greaterThan(avatarRect.right));
    final conversationContent = tester.widget<Container>(
      conversationContentFinder,
    );
    final conversationDecoration =
        conversationContent.decoration! as BoxDecoration;
    expect(conversationDecoration.border?.bottom.width, .75);
    expect(
      conversationDecoration.border?.bottom.color,
      const Color(0xFFD7E0DB),
    );
    final conversationTitle = tester.widget<Text>(
      find.byKey(const ValueKey('conversation-title-c-team')),
    );
    expect(conversationTitle.style?.fontSize, 16);

    await tester.tap(find.text('单聊'));
    await tester.pumpAndSettle();
    expect(find.text('林屿'), findsOneWidget);
    expect(find.text('邻里产品小组'), findsNothing);
    expect(find.byKey(const Key('system-notification-section')), findsNothing);

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

  testWidgets('消息服务离线时首页和聊天页明确阻止发送并可重试', (tester) async {
    final repository = OfflineConnectionRepository();
    final controller = AppController(repository);
    await tester.runAsync(() async {
      await controller.loginAsDemo();
      // The offline event invalidates the in-flight group-role snapshot.
      // Await its real-timer refresh before switching back to the fake clock.
      final deadline = DateTime.now().add(const Duration(seconds: 2));
      while (controller.conversations.isEmpty &&
          DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 10));
      }
      expect(controller.conversations, isNotEmpty);
    });
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: ConversationsTab(controller: controller)),
      ),
    );
    await tester.pump();
    expect(
      find.byKey(const Key('messaging-connection-banner')),
      findsOneWidget,
    );
    expect(find.text('消息服务未连接，发送暂不可用'), findsOneWidget);

    await tester.tap(find.byKey(const Key('retry-messaging-connection')));
    await tester.pumpAndSettle();
    expect(repository.connectCalls, greaterThanOrEqualTo(2));

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(
          controller: controller,
          conversation: controller.conversations.first,
        ),
      ),
    );
    await tester.pump();
    expect(
      find.byKey(const Key('messaging-connection-banner')),
      findsOneWidget,
    );
    expect(find.byType(ChatComposer), findsNothing);
    await tester.pump(const Duration(milliseconds: 200));
  });

  testWidgets('通讯录只展示服务端标记为已保存的群聊并可进入会话', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: ContactsTab(controller: controller)),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('contacts-saved-groups')));
    await tester.pumpAndSettle();
    expect(find.text('保存的群聊'), findsWidgets);
    expect(find.byKey(const Key('saved-groups-list')), findsOneWidget);
    expect(find.text('邻里产品小组'), findsOneWidget);

    await tester.tap(find.text('邻里产品小组'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('message-input')), findsOneWidget);
  });

  testWidgets('陌生人资料先显示添加好友，申请后进入等待状态而不是直接发消息', (tester) async {
    final repository = RelationshipStateRepository();
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: FriendProfileScreen(
          controller: controller,
          user: RelationshipStateRepository.stranger,
          requestSource: 'qr',
          requestSourceId: RelationshipStateRepository.stranger.id,
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('添加好友'), findsOneWidget);
    expect(find.text('发消息'), findsNothing);
    await tester.tap(find.byKey(const Key('friend-primary-action')));
    await tester.pumpAndSettle();
    expect(find.text('添加 扫码用户'), findsOneWidget);
    expect(
      tester
          .widget<TextField>(find.byKey(const Key('friend-verification-input')))
          .controller
          ?.text,
      '你好，我是许言',
    );

    await tester.tap(find.text('发送好友申请'));
    await tester.pumpAndSettle();
    expect(repository.lastSource, 'qr');
    expect(find.text('等待对方通过'), findsOneWidget);
    expect(find.text('好友申请已发送'), findsOneWidget);
  });

  testWidgets('同意好友申请后红点清除且消息页立即出现单聊', (tester) async {
    final repository = RelationshipStateRepository(incoming: true);
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    expect(controller.pendingIncomingFriendRequestCount, 1);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: FriendProfileScreen(
          controller: controller,
          user: RelationshipStateRepository.stranger,
          requestSource: 'qr',
          requestSourceId: RelationshipStateRepository.stranger.id,
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('同意添加'), findsOneWidget);
    await tester.tap(find.byKey(const Key('friend-primary-action')));
    await tester.pumpAndSettle();
    expect(controller.pendingIncomingFriendRequestCount, 0);
    expect(find.text('发消息'), findsOneWidget);
    expect(find.text('已添加 扫码用户 为好友'), findsOneWidget);
    expect(
      controller.conversations.any(
        (conversation) =>
            conversation.kind == ConversationKind.direct &&
            conversation.members.any(
              (member) => member.id == RelationshipStateRepository.stranger.id,
            ),
      ),
      isTrue,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ConversationsTab(controller: controller),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('扫码用户'), findsOneWidget);
    expect(find.text('你们已是好友，开始聊天吧'), findsOneWidget);
  });

  testWidgets('好友申请已发送但列表刷新失败时仍保持等待状态', (tester) async {
    final repository = RelationshipStateRepository(failRefreshAfterSend: true);
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: FriendProfileScreen(
          controller: controller,
          user: RelationshipStateRepository.stranger,
          requestSource: 'qr',
          requestSourceId: RelationshipStateRepository.stranger.id,
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('friend-primary-action')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('发送好友申请'));
    await tester.pumpAndSettle();

    expect(find.text('好友申请已发送'), findsOneWidget);
    expect(find.text('等待对方通过'), findsOneWidget);
    expect(find.text('添加好友'), findsNothing);
  });

  testWidgets('群资料页可将群聊移出并重新保存到通讯录', (tester) async {
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

    expect(group.saved, isTrue);
    expect(find.byKey(const Key('group-save-to-contacts')), findsOneWidget);
    await tester.tap(find.byType(CupertinoSwitch).first);
    await tester.pumpAndSettle();
    expect(
      controller.conversations.firstWhere((item) => item.id == group.id).saved,
      isFalse,
    );

    await tester.tap(find.byType(CupertinoSwitch).first);
    await tester.pumpAndSettle();
    expect(
      controller.conversations.firstWhere((item) => item.id == group.id).saved,
      isTrue,
    );
  });

  testWidgets('群历史开关默认关闭，取消不保存，确认后可切换并保持样式', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repository = DemoImRepository(latency: Duration.zero);
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.firstWhere(
      (c) => c.kind == ConversationKind.group,
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
    final row = find.byKey(const Key('group-history-visibility'));
    await tester.scrollUntilVisible(row, 250);
    final toggle = find.descendant(
      of: row,
      matching: find.byType(CupertinoSwitch),
    );
    expect(tester.widget<CupertinoSwitch>(toggle).value, isFalse);
    await tester.tap(toggle);
    await tester.pumpAndSettle();
    expect(find.text('开放入群前历史？'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(tester.widget<CupertinoSwitch>(toggle).value, isFalse);
    await tester.tap(toggle);
    await tester.pumpAndSettle();
    await tester.tap(find.text('确认修改'));
    await tester.pumpAndSettle();
    expect(tester.widget<CupertinoSwitch>(toggle).value, isTrue);
    expect(
      (await tester.runAsync(
        () => repository.groupProfile(group.id),
      ))!.historyVisibleToNewMembers,
      isTrue,
    );
    await tester.runAsync(
      () => repository.setGroupAnnouncement(group.id, '公告更新不改变历史策略'),
    );
    expect(
      (await tester.runAsync(
        () => repository.groupProfile(group.id),
      ))!.historyVisibleToNewMembers,
      isTrue,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('群举报使用服务端接受的 group 类型', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final repository = ReportCaptureRepository();
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.firstWhere(
      (conversation) => conversation.kind == ConversationKind.group,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatInfoScreen(
          controller: controller,
          conversation: group,
          onSearch: () {},
          onClearLocal: () async {},
          onBlock: () async {},
          onScheduledMessages: () {},
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.text('举报会话'),
      300,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.ensureVisible(find.text('举报会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('举报会话'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('其他'));
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, '提交举报'));
    await tester.pumpAndSettle();

    expect(repository.lastTargetType, 'group');
    expect(repository.lastTargetId, group.id);
    expect(find.text('举报已提交，我们会尽快审核'), findsOneWidget);
  });

  testWidgets('举报失败时留在页面并显示失败提示', (tester) async {
    final repository = ReportCaptureRepository(fail: true);
    final controller = AppController(repository);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ReportScreen(
          controller: controller,
          target: '测试群',
          targetId: 'group-test',
          targetType: 'group',
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('其他'));
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, '提交举报'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));

    expect(find.textContaining('举报 测试群'), findsOneWidget);
    expect(repository.lastTargetType, 'group');
    expect(find.byType(SnackBar), findsOneWidget);
    final snackBar = tester.widget<SnackBar>(find.byType(SnackBar));
    expect((snackBar.content as Text).data, contains('举报提交失败'));
  });

  testWidgets('群资料更新后聊天页实时刷新且解散后自动返回', (tester) async {
    const recordChannel = MethodChannel('com.llfbandit.record/messages');
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      recordChannel,
      (_) async => null,
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        recordChannel,
        null,
      ),
    );
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.firstWhere(
      (conversation) => conversation.kind == ConversationKind.group,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Builder(
          builder: (context) => Scaffold(
            body: FilledButton(
              key: const Key('open-live-group-chat'),
              onPressed: () => Navigator.of(context).push(
                chatScreenRoute(
                  context,
                  controller: controller,
                  conversation: group,
                ),
              ),
              child: const Text('打开测试群'),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.byKey(const Key('open-live-group-chat')));
    await tester.pumpAndSettle();

    await tester.runAsync(
      () => controller.updateGroupProfile(group.id, name: '实时更新群名'),
    );
    await tester.pumpAndSettle();
    expect(find.text('实时更新群名'), findsOneWidget);

    await tester.runAsync(() => controller.disbandGroup(group.id, '测试解散'));
    await tester.pumpAndSettle();
    expect(find.byType(ChatScreen), findsNothing);
    expect(find.byKey(const Key('open-live-group-chat')), findsOneWidget);
  });

  testWidgets('三个长昵称的群成员使用三列友好布局', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final members = <AppUser>[
      const AppUser(
        id: 'layout-a',
        name: 'AndroidTestA0828',
        handle: '',
        presence: '',
      ),
      const AppUser(
        id: 'layout-b',
        name: 'AndroidTestB0828',
        handle: '',
        presence: '',
      ),
      const AppUser(
        id: 'layout-c',
        name: 'AndroidTestC0828',
        handle: '',
        presence: '',
      ),
    ];
    final conversation = Conversation(
      id: 'layout-group',
      title: '布局测试群',
      subtitle: '',
      updatedAt: DateTime(2026, 8, 29),
      kind: ConversationKind.group,
      memberCount: members.length,
      members: members,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatInfoScreen(
          controller: controller,
          conversation: conversation,
          onSearch: () {},
          onClearLocal: () async {},
          onBlock: () async {},
          onScheduledMessages: () {},
        ),
      ),
    );
    await tester.pumpAndSettle();

    for (final member in members) {
      expect(
        tester
            .getSize(find.byKey(ValueKey('chat-info-member-${member.id}')))
            .width,
        greaterThan(95),
      );
    }
    expect(tester.takeException(), isNull);
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

  testWidgets(
    'voice enqueue animates the current chat once and ACK does not replay',
    (tester) async {
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
      Widget chat(Key key) => MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(
          key: key,
          controller: controller,
          conversation: conversation,
        ),
      );
      await tester.pumpWidget(chat(const ValueKey('initial')));
      await tester.pumpAndSettle();
      final composer = tester.widget<ChatComposer>(find.byType(ChatComposer));
      await tester.runAsync(
        () async => composer.onVoiceReady(
          MediaUpload(
            bytes: Uint8List.fromList([1, 2]),
            fileName: 'voice.m4a',
            mimeType: 'audio/mp4',
            kind: MessageContentKind.voice,
            durationSeconds: 2,
          ),
        ),
      );
      final message = controller.messagesFor(conversation.id).last;
      final entrance = find.byKey(
        ValueKey('voice-entrance-${message.clientMessageId}'),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 40));
      final opacity = find
          .descendant(of: entrance, matching: find.byType(Opacity))
          .first;
      expect(tester.widget<Opacity>(opacity).opacity, lessThan(1));
      await tester.pump(const Duration(milliseconds: 220));
      expect(tester.widget<Opacity>(opacity).opacity, 1);
      repository.reportProgress(1);
      await tester.pump();
      expect(
        controller.messagesFor(conversation.id).last.status,
        MessageStatus.sending,
      );
      repository.complete();
      await tester.pumpAndSettle();
      expect(
        controller.messagesFor(conversation.id).last.status,
        MessageStatus.sent,
      );
      expect(tester.widget<Opacity>(opacity).opacity, 1);
      expect(tester.widget<VoiceMessageEntrance>(entrance).animate, isFalse);
      await tester.pumpWidget(chat(const ValueKey('reopened')));
      await tester.pumpAndSettle();
      expect(tester.widget<VoiceMessageEntrance>(entrance).animate, isFalse);
      expect(tester.widget<Opacity>(opacity).opacity, 1);
    },
  );

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
      expect(tester.getSize(header).height, 60);
      expect(
        find.byKey(ValueKey('top-level-content-${entry.$2}')),
        findsOneWidget,
      );
      if (entry.$1 == 2) {
        expect(find.text('朋友圈'), findsOneWidget);
        expect(find.text('表情商店'), findsNothing);
        expect(find.text('社区与频道'), findsNothing);
        expect(find.text('在线客服'), findsNothing);

        await tester.tap(find.text('朋友圈'));
        await tester.pumpAndSettle();
        expect(find.byType(MomentsScreen), findsOneWidget);
        await tester.pageBack();
        await tester.pumpAndSettle();
      }
    }

    expect(find.byKey(const Key('profile-favorites')), findsOneWidget);
    expect(find.byKey(const Key('profile-devices')), findsOneWidget);
    expect(find.byKey(const Key('profile-settings')), findsOneWidget);
    expect(find.byKey(const Key('profile-help')), findsOneWidget);
  });

  testWidgets('底部菜单按下不透明闪烁且重复点击不触发多余切换', (tester) async {
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

    for (var i = 0; i < 4; i++) {
      final button = tester.widget<CupertinoButton>(
        find.byKey(Key('home-tab-$i')),
      );
      expect(button.pressedOpacity, 1);
    }

    final contactGesture = await tester.startGesture(
      tester.getCenter(find.byKey(const Key('home-tab-1'))),
    );
    await tester.pump(const Duration(milliseconds: 150));
    expect(find.byKey(const Key('messages-header')), findsOneWidget);

    await contactGesture.up();
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('top-level-header-联系人')), findsOneWidget);

    await tester.tap(find.byKey(const Key('home-tab-1')));
    await tester.pump();
    expect(find.byKey(const ValueKey('top-level-header-联系人')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('我的页常用入口可触达真实功能且点击区域不小于 44 点', (tester) async {
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
    await tester.tap(find.byKey(const Key('home-tab-3')));
    await tester.pumpAndSettle();

    for (final key in const [
      Key('profile-favorites'),
      Key('profile-devices'),
      Key('profile-settings'),
      Key('profile-help'),
    ]) {
      expect(tester.getSize(find.byKey(key)).height, greaterThanOrEqualTo(44));
    }

    await tester.tap(find.byKey(const Key('profile-favorites')));
    await tester.pumpAndSettle();
    expect(find.byType(FavoritesScreen), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('profile-devices')));
    await tester.pumpAndSettle();
    expect(find.byType(DevicesScreen), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('profile-settings')));
    await tester.pumpAndSettle();
    expect(find.byType(SettingsScreen), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('profile-help')));
    await tester.pumpAndSettle();
    expect(find.byType(HelpFeedbackScreen), findsOneWidget);
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

  test('发送回执替换服务端消息 ID 时保持消息行身份稳定', () {
    final pending = ChatMessage(
      id: 'local-client-1',
      clientMessageId: 'client-1',
      conversationId: 'c-linyu',
      senderId: 'me',
      senderName: '我',
      text: '无闪烁发送',
      sentAt: DateTime(2026, 8, 28),
      isMine: true,
      status: MessageStatus.sending,
    );
    final acknowledged = pending.copyWith(
      id: 'server-stable-row',
      status: MessageStatus.sent,
    );

    expect(acknowledged.id, isNot(pending.id));
    expect(acknowledged.stableIdentity, pending.stableIdentity);
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
    String? copiedText;
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          copiedText =
              (call.arguments as Map<Object?, Object?>)['text'] as String?;
        }
        return null;
      },
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        null,
      ),
    );
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
    expect(find.text('选择文字'), findsOneWidget);
    expect(find.text('转发'), findsOneWidget);
    expect(find.text('收藏'), findsWidgets);
    expect(find.text('多选'), findsOneWidget);
    expect(find.text('举报'), findsOneWidget);
    expect(find.text('删除本机记录'), findsOneWidget);

    await tester.tap(find.text('选择文字'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('message-context-menu')), findsNothing);
    expect(find.byKey(const Key('selectable-message-text')), findsOneWidget);
    expect(find.text('长按文字后拖动选区，可复制其中一部分。'), findsOneWidget);

    await tester.tap(find.byKey(const Key('copy-all-message-text')));
    await tester.pumpAndSettle();
    expect(copiedText, '太好了，我已经把新的动效稿上传了。');
    expect(find.byKey(const Key('selectable-message-text')), findsNothing);

    await tester.longPress(find.text('太好了，我已经把新的动效稿上传了。'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('多选'));
    await tester.pumpAndSettle();
    expect(find.text('已选择 1 条'), findsOneWidget);
    expect(find.text('删除'), findsOneWidget);
  });

  testWidgets('群成员提醒插入后光标稳定停在昵称末尾', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.firstWhere(
      (conversation) => conversation.kind == ConversationKind.group,
    );
    final member = group.members.firstWhere(
      (user) => user.id != controller.currentUser?.id,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: group),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('mention-member-button')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(Key('mention-member-${member.id}')));
    await tester.pumpAndSettle();

    final input = tester.widget<TextField>(
      find.byKey(const Key('message-input')),
    );
    expect(input.controller!.text, '@${member.name} ');
    expect(input.controller!.selection.isCollapsed, isTrue);
    expect(
      input.controller!.selection.baseOffset,
      input.controller!.text.length,
    );
  });

  testWidgets('聊天内容搜索结果会返回会话并定位目标消息', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
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
        home: ChatScreen(controller: controller, conversation: group),
      ),
    );
    await tester.pumpAndSettle();
    final target = controller
        .messagesFor(group.id)
        .firstWhere((message) => message.text.contains('动效稿'));
    await tester.tap(find.byKey(const Key('chat-more-button')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('查找聊天内容'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const Key('conversation-message-search')),
      '动效稿',
    );
    await tester.pump(const Duration(milliseconds: 350));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(Key('message-search-result-${target.id}')));
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('chat-info-list')), findsNothing);
    expect(find.byKey(const Key('message-list')), findsOneWidget);
    expect(find.text(target.text), findsOneWidget);
  });

  test('搜索结果与缓存 ID 表示不同时使用已加载消息定位', () async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    final group = controller.conversations.firstWhere(
      (conversation) => conversation.kind == ConversationKind.group,
    );
    await controller.loadMessages(group.id);
    final cached = controller.messagesFor(group.id).first;
    final searchResult = ChatMessage(
      id: 'search-${cached.id}',
      clientMessageId: cached.clientMessageId,
      conversationId: cached.conversationId,
      senderId: cached.senderId,
      senderName: cached.senderName,
      text: cached.text,
      sentAt: cached.sentAt,
      isMine: cached.isMine,
    );

    final canonical = await controller.revealSearchResult(searchResult);

    expect(canonical.id, cached.id);
    expect(controller.messagesFor(group.id), contains(same(cached)));
  });

  test('媒体发送因连接中断失败后会在重连时自动恢复一次', () async {
    final repository = ReconnectingMediaRepository();
    final controller = AppController(repository);
    await controller.loginAsDemo();
    addTearDown(controller.dispose);
    await controller.loadMessages('c-linyu');

    final first = await controller.sendMedia(
      'c-linyu',
      MediaUpload(
        bytes: Uint8List.fromList([1, 2, 3, 4]),
        fileName: 'network-retry.bin',
        mimeType: 'application/octet-stream',
        kind: MessageContentKind.file,
      ),
    );
    expect(first.status, MessageStatus.failed);
    expect(repository.sendAttempts, 1);

    repository.restoreConnection();
    for (
      var attempt = 0;
      attempt < 20 && repository.sendAttempts < 2;
      attempt++
    ) {
      await Future<void>.delayed(const Duration(milliseconds: 10));
    }
    expect(repository.sendAttempts, 2);
    expect(controller.messagesFor('c-linyu').last.status, MessageStatus.sent);
  });

  testWidgets('图片消息保持原比例并支持全屏缩放预览', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final message = ChatMessage(
      id: 'image-preview-message',
      conversationId: 'image-preview-conversation',
      senderId: 'me',
      senderName: '我',
      text: '[图片]',
      sentAt: DateTime(2026, 8, 16, 10, 20),
      isMine: true,
      kind: MessageContentKind.image,
      mediaUrl: 'assets/brand/qingwaguagua-mark-transparent.png',
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: MessageBubble(message: message)),
      ),
    );
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 80)),
    );
    await tester.pumpAndSettle();

    final thumbnail = tester.widget<Image>(
      find.byKey(const Key('message-image-render-image-preview-message')),
    );
    expect(thumbnail.fit, BoxFit.contain);
    expect(thumbnail.width, 220);
    expect(thumbnail.height, 180);
    final thumbnailRawImage = find.descendant(
      of: find.byKey(const Key('message-image-render-image-preview-message')),
      matching: find.byType(RawImage),
    );
    expect(
      tester.renderObject<RenderImage>(thumbnailRawImage).image,
      isNotNull,
    );

    await tester.tap(
      find.byKey(const Key('message-image-image-preview-message')),
    );
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('message-image-preview')), findsOneWidget);
    final preview = tester.widget<Image>(
      find.byKey(const Key('message-image-preview-render')),
    );
    expect(preview.fit, BoxFit.contain);
    final viewer = tester.widget<InteractiveViewer>(
      find.byKey(const Key('message-image-interactive-viewer')),
    );
    expect(viewer.minScale, 1);
    expect(viewer.maxScale, 5);
    final previewCenter = tester.getCenter(
      find.byKey(const Key('message-image-preview-render')),
    );
    await tester.tapAt(previewCenter);
    await tester.pump(const Duration(milliseconds: 60));
    await tester.tapAt(previewCenter);
    await tester.pump(const Duration(milliseconds: 400));
    expect(find.byKey(const Key('message-image-preview')), findsOneWidget);
    expect(
      viewer.transformationController!.value.getMaxScaleOnAxis(),
      greaterThan(1),
    );

    await tester.tapAt(previewCenter);
    // onTap and onDoubleTap share a gesture arena, so a lone tap is resolved
    // only after the platform double-tap timeout has elapsed.
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('message-image-preview')), findsNothing);
  });

  testWidgets('首次加载图片时预留稳定高度避免消息列表跳动', (tester) async {
    PaintingBinding.instance.imageCache
      ..clear()
      ..clearLiveImages();
    final message = ChatMessage(
      id: 'image-loading-placeholder',
      conversationId: 'image-loading-conversation',
      senderId: 'peer',
      senderName: '林屿',
      text: '[图片]',
      sentAt: DateTime(2026, 8, 16, 10, 20),
      isMine: false,
      kind: MessageContentKind.image,
      mediaUrl: 'assets/brand/qingwaguagua-mark-transparent.png',
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: MessageBubble(message: message)),
      ),
    );

    final placeholder = find.byKey(
      const Key('message-image-placeholder-image-loading-placeholder'),
    );
    expect(placeholder, findsOneWidget);
    expect(tester.getSize(placeholder), const Size(220, 180));

    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 80)),
    );
    await tester.pump();
    expect(placeholder, findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('图片消息按原始比例预留空间且不等待网络解码', (tester) async {
    PaintingBinding.instance.imageCache
      ..clear()
      ..clearLiveImages();
    final message = ChatMessage(
      id: 'portrait-image-slot',
      conversationId: 'image-loading-conversation',
      senderId: 'peer',
      senderName: '林屿',
      text: '[图片]',
      sentAt: DateTime(2026, 8, 16, 10, 20),
      isMine: false,
      kind: MessageContentKind.image,
      mediaUrl: 'assets/brand/qingwaguagua-mark-transparent.png',
      mediaWidth: 600,
      mediaHeight: 800,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: MessageBubble(message: message)),
      ),
    );

    final placeholder = find.byKey(
      const Key('message-image-placeholder-portrait-image-slot'),
    );
    expect(placeholder, findsOneWidget);
    expect(tester.getSize(placeholder), const Size(210, 280));
    expect(
      tester.getSize(
        find.byKey(const Key('message-image-portrait-image-slot')),
      ),
      const Size(210, 280),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('图片预览提供连续浏览、编辑、转发和保存入口', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final conversation = controller.conversations.firstWhere(
      (item) => item.kind == ConversationKind.direct,
    );
    await tester.runAsync(() => controller.loadMessages(conversation.id));
    await tester.runAsync(
      () => controller.sendMedia(
        conversation.id,
        MediaUpload(
          bytes: Uint8List.fromList(const [1, 2, 3]),
          fileName: 'second.png',
          mimeType: 'image/png',
          kind: MessageContentKind.image,
          localPath: 'assets/brand/qingwaguagua-mark-transparent.png',
        ),
      ),
    );
    final images = controller
        .messagesFor(conversation.id)
        .where((message) => message.kind == MessageContentKind.image)
        .toList();
    expect(images, hasLength(2));

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(
          body: MessageBubble(message: images.first, controller: controller),
        ),
      ),
    );
    await tester.pumpAndSettle();
    final previewTrigger = tester.widget<InkWell>(
      find.byKey(Key('message-image-${images.first.clientMessageId}')),
    );
    previewTrigger.onTap!();
    await tester.pumpAndSettle();

    expect(find.text('1 / 2'), findsOneWidget);
    expect(find.byKey(const Key('edit-message-image-preview')), findsOneWidget);
    expect(
      find.byKey(const Key('forward-message-image-preview')),
      findsOneWidget,
    );
    expect(find.byKey(const Key('save-message-image-preview')), findsOneWidget);

    await tester.tap(find.byKey(const Key('more-message-image-preview')));
    await tester.pumpAndSettle();
    expect(find.text('编辑'), findsWidgets);
    expect(find.text('转发'), findsWidgets);
    expect(find.text('保存原图'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();

    await tester.drag(
      find.byKey(const Key('message-image-interactive-viewer')),
      const Offset(-260, 0),
    );
    await tester.pumpAndSettle();
    expect(find.text('2 / 2'), findsOneWidget);
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
    expect(find.text('我 ❤️ 点赞了直播'), findsOneWidget);
  });

  testWidgets('合并聊天记录显示摘要并可查看完整快照', (tester) async {
    tester.view.physicalSize = const Size(375, 812);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(ChatHistoryRepository());
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final conversation = controller.conversations.first;

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: conversation),
      ),
    );
    await tester.pumpAndSettle();

    final card = find.byKey(const Key('chat-history-history-client-1'));
    expect(card, findsOneWidget);
    expect(find.text('共 2 条消息'), findsOneWidget);
    await tester.tap(card);
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('chat-history-detail-list')), findsOneWidget);
    expect(find.text('聊天记录'), findsWidgets);
    expect(
      find.byKey(const Key('chat-history-entry-source-1')),
      findsOneWidget,
    );
    expect(
      find.byKey(const Key('chat-history-entry-source-2')),
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

  test('演示仓库合并转发保留结构化聊天记录类型', () async {
    final repository = DemoImRepository(latency: Duration.zero);
    addTearDown(repository.close);
    await repository.enterDemo();
    final conversations = await repository.conversations();
    final source = await repository.messages(conversations.first.id);

    final forwarded = await repository.forwardMessages(
      conversations.first.id,
      source.take(2).map((message) => message.id).toList(),
      mode: 'merged',
      clientBatchId: 'demo-merged-contract',
    );

    expect(forwarded.single.kind, MessageContentKind.chatHistory);
    expect(forwarded.single.chatHistoryEntries, hasLength(2));
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
    expect(find.textContaining('呱呱号：'), findsOneWidget);
    expect(find.text('联系人资料'), findsNothing);
    expect(find.text('置顶聊天'), findsOneWidget);
    expect(find.text('消息免打扰'), findsOneWidget);
    expect(find.text('加入黑名单'), findsOneWidget);
    expect(find.text('群聊资料'), findsNothing);
  });

  testWidgets('单聊成员自己在前时头像资料与黑名单仍指向真实对方', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final original = controller.conversations.firstWhere(
      (conversation) => conversation.kind == ConversationKind.direct,
    );
    final me = controller.currentUser!;
    final peer = original.members
        .where((member) => member.id != me.id)
        .firstOrNull!;
    final direct = original.copyWith(
      channelId: peer.id,
      title: peer.name,
      members: [me, peer],
    );

    expect(direct.directPeerFor(me.id)?.id, peer.id);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: direct),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('chat-peer-avatar-button')));
    await tester.pumpAndSettle();
    expect(find.text('个人资料'), findsOneWidget);
    expect(find.text(peer.name), findsWidgets);
    expect(find.text(me.name), findsNothing);
    await tester.pageBack();
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('chat-more-button')));
    await tester.pumpAndSettle();
    expect(find.text(peer.name), findsOneWidget);
    expect(find.text(me.name), findsNothing);

    await tester.tap(find.byKey(const Key('chat-info-contact-profile')));
    await tester.pumpAndSettle();
    expect(find.text('个人资料'), findsOneWidget);
    expect(find.text(peer.name), findsWidgets);
    await tester.pageBack();
    await tester.pumpAndSettle();

    await tester.scrollUntilVisible(
      find.text('加入黑名单'),
      300,
      scrollable: find.descendant(
        of: find.byKey(const Key('chat-info-list')),
        matching: find.byType(Scrollable),
      ),
    );
    await tester.tap(find.text('加入黑名单'));
    await tester.pumpAndSettle();
    expect(find.text('将 ${peer.name} 加入黑名单？'), findsOneWidget);
    expect(find.text('将 ${me.name} 加入黑名单？'), findsNothing);
  });

  testWidgets('单聊消息发送者名称为空时头像使用成员名称且可以打开资料', (tester) async {
    var opened = false;
    final message = ChatMessage(
      id: 'message-avatar-test',
      clientMessageId: 'message-avatar-client',
      conversationId: 'direct-avatar',
      senderId: 'peer-1',
      senderName: '',
      text: '头像兜底测试',
      sentAt: DateTime(2026, 8, 28, 10),
      isMine: false,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(
          body: MessageBubble(
            message: message,
            senderName: '林安',
            onAvatarTap: () => opened = true,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('林'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('查看林安资料')), findsOneWidget);
    await tester.tap(
      find.byKey(const Key('message-avatar-message-avatar-client')),
    );
    await tester.pump();
    expect(opened, isTrue);
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

  testWidgets('已编辑消息可以查看服务端版本记录', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero))
      ..authPolicy = const AuthPolicy(messageRecallMinutes: 1440);
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);
    final group = controller.conversations.first;
    await tester.runAsync(() => controller.loadMessages(group.id));
    final original = controller
        .messagesFor(group.id)
        .firstWhere(
          (message) =>
              message.isMine && message.kind == MessageContentKind.reply,
        );
    expect(
      await tester.runAsync(() => controller.editMessage(original, '二次编辑后的内容')),
      isTrue,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: ChatScreen(controller: controller, conversation: group),
      ),
    );
    await tester.pumpAndSettle();
    await tester.longPress(find.text('二次编辑后的内容'));
    await tester.pumpAndSettle();
    expect(find.text('编辑记录'), findsOneWidget);

    await tester.tap(find.text('编辑记录'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('message-edit-history-list')), findsOneWidget);
    expect(find.text('原始内容'), findsOneWidget);
    expect(find.text('很不错，刚把新版本的体验走了一遍。'), findsOneWidget);
    expect(find.text('第 1 次编辑'), findsOneWidget);
    expect(find.text('二次编辑后的内容'), findsWidgets);
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
    expect(light.colorScheme.primary, LinliColors.brandGreen);
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

class _PendingRestoreRepository extends DemoImRepository {
  _PendingRestoreRepository() : super(latency: Duration.zero);

  @override
  Future<bool> restoreSession() => Completer<bool>().future;
}

class _PendingPolicyRepository extends DemoImRepository {
  _PendingPolicyRepository() : super(latency: Duration.zero);

  final Completer<AuthPolicy> _policy = Completer<AuthPolicy>();
  int policyRequests = 0;

  @override
  Future<AuthPolicy> authPolicy() {
    policyRequests += 1;
    return _policy.future;
  }

  void completePolicy() {
    if (!_policy.isCompleted) _policy.complete(const AuthPolicy());
  }
}

class _PendingRestoredCoreRepository extends DemoImRepository {
  _PendingRestoredCoreRepository() : super(latency: Duration.zero);

  final Completer<List<Conversation>> _core = Completer<List<Conversation>>();

  @override
  Future<bool> restoreSession() async => true;

  @override
  Future<List<Conversation>> conversations() => _core.future;

  void completeCore() {
    if (!_core.isCompleted) _core.complete(super.conversations());
  }
}

class _PendingFreshLoginCoreRepository extends DemoImRepository {
  _PendingFreshLoginCoreRepository() : super(latency: Duration.zero);

  final Completer<List<Conversation>> _core = Completer<List<Conversation>>();

  @override
  Future<List<Conversation>> conversations() => _core.future;

  void completeCore() {
    if (!_core.isCompleted) _core.complete(super.conversations());
  }
}

class _ExpiredRestoredSessionRepository extends DemoImRepository {
  _ExpiredRestoredSessionRepository() : super(latency: Duration.zero);

  final Completer<List<Conversation>> _core = Completer<List<Conversation>>();
  AppUser? _sessionUser = DemoImRepository.demoUser;

  @override
  AppUser? get currentUser => _sessionUser;

  @override
  Future<bool> restoreSession() async => true;

  @override
  Future<AppUser> profile() async {
    _sessionUser = null;
    throw StateError('refresh token revoked');
  }

  @override
  Future<List<Conversation>> conversations() => _core.future;

  void completeCore() {
    if (!_core.isCompleted) _core.complete(const <Conversation>[]);
  }
}

class _RuntimeExpiredSessionRepository extends DemoImRepository {
  _RuntimeExpiredSessionRepository() : super(latency: Duration.zero);

  final StreamController<ImEvent> _runtimeEvents =
      StreamController<ImEvent>.broadcast();

  @override
  Stream<ImEvent> get events => _runtimeEvents.stream;

  void expire() => _runtimeEvents.add(
    const ImEvent(type: ImEventType.sessionExpired, payload: {}),
  );

  @override
  Future<void> close() async {
    await _runtimeEvents.close();
    await super.close();
  }
}

class OfflineConnectionRepository extends DemoImRepository {
  OfflineConnectionRepository() : super(latency: Duration.zero);

  final StreamController<bool> _connection = StreamController<bool>.broadcast();
  int connectCalls = 0;

  @override
  Stream<bool> get connectionChanges => _connection.stream;

  @override
  Future<void> connect() async {
    connectCalls++;
    _connection.add(false);
  }

  @override
  Future<void> close() async {
    await _connection.close();
    await super.close();
  }
}

class RelationshipStateRepository extends DemoImRepository {
  RelationshipStateRepository({
    bool incoming = false,
    this.failRefreshAfterSend = false,
  }) : _requests = incoming
           ? [
               FriendRequest(
                 id: 'friend-request-incoming',
                 user: stranger,
                 note: '你好，想加你为好友',
                 outgoing: false,
                 source: 'qr',
                 createdAt: DateTime(2026, 8, 15, 10),
               ),
             ]
           : [],
       super(latency: Duration.zero);

  static const stranger = AppUser(
    id: 'scan-user-1',
    name: '扫码用户',
    handle: 'scan_user_1',
    presence: '通过二维码认识',
  );

  final List<FriendRequest> _requests;
  final bool failRefreshAfterSend;
  Conversation? _acceptedConversation;
  bool accepted = false;
  bool sent = false;
  String? lastSource;

  @override
  Future<List<AppUser>> contacts() async => accepted ? [stranger] : [];

  @override
  Future<List<Conversation>> conversations() async {
    final items = await super.conversations();
    final conversation = _acceptedConversation;
    if (conversation == null) return items;
    return [
      conversation,
      ...items.where(
        (item) =>
            item.id != conversation.id &&
            !item.members.any((member) => member.id == stranger.id),
      ),
    ];
  }

  @override
  Future<Conversation> createDirect(AppUser user) async =>
      _acceptedConversation ??= Conversation(
        id: 'new-friend-conversation',
        title: user.name,
        subtitle: '你们已是好友，开始聊天吧',
        updatedAt: DateTime(2026, 8, 16, 10),
        kind: ConversationKind.direct,
        channelId: user.id,
        channelType: 1,
        members: [user],
      );

  @override
  Future<List<FriendRequest>> friendRequests() async {
    if (failRefreshAfterSend && sent) {
      throw StateError('friend request refresh unavailable');
    }
    return List.of(_requests);
  }

  @override
  Future<void> sendFriendRequest(
    String userId,
    String note, {
    String source = 'search',
    String? sourceId,
  }) async {
    lastSource = source;
    sent = true;
    _requests.add(
      FriendRequest(
        id: 'friend-request-outgoing',
        user: stranger,
        note: note,
        outgoing: true,
        source: source,
        sourceId: sourceId,
        createdAt: DateTime(2026, 8, 15, 10),
      ),
    );
  }

  @override
  Future<void> acceptFriendRequest(String requestId) async {
    final index = _requests.indexWhere((request) => request.id == requestId);
    if (index < 0) throw StateError('friend request not found');
    _requests[index] = _requests[index].copyWith(status: 'accepted');
    accepted = true;
  }
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

  void complete({String? id}) =>
      completer.complete(pending!.copyWith(id: id, status: MessageStatus.sent));
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

class ReconnectingMediaRepository extends DemoImRepository {
  ReconnectingMediaRepository() : super(latency: Duration.zero);

  final StreamController<bool> _connection = StreamController<bool>.broadcast();
  int sendAttempts = 0;

  @override
  Stream<bool> get connectionChanges => _connection.stream;

  @override
  Future<void> connect() async => _connection.add(true);

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
  }) async {
    sendAttempts++;
    if (sendAttempts == 1) {
      _connection.add(false);
      await Future<void>.delayed(Duration.zero);
      throw const SocketException('Software caused connection abort');
    }
    onProgress?.call(1);
    return message.copyWith(
      id: 'auto-retried-media',
      mediaId: 'media-auto-retry',
      mediaUrl: 'https://cdn.example.com/media-auto-retry',
      status: MessageStatus.sent,
    );
  }

  void restoreConnection() => _connection.add(true);

  @override
  Future<void> close() async {
    await _connection.close();
    await super.close();
  }
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

class ChatHistoryRepository extends DemoImRepository {
  ChatHistoryRepository() : super(latency: Duration.zero);

  @override
  Future<List<ChatMessage>> messages(String conversationId) async => [
    ChatMessage(
      id: 'history-1',
      clientMessageId: 'history-client-1',
      conversationId: conversationId,
      senderId: 'u1',
      senderName: '林屿',
      text: '聊天记录\n第一条消息\n[图片]',
      sentAt: DateTime(2026, 8, 13, 10),
      isMine: false,
      kind: MessageContentKind.chatHistory,
      chatHistoryEntries: [
        ChatHistoryEntry(
          sourceMessageId: 'source-1',
          senderId: 'u1',
          summary: '第一条消息',
          createdAt: DateTime(2026, 8, 13, 9, 58),
        ),
        ChatHistoryEntry(
          sourceMessageId: 'source-2',
          senderId: currentUser?.id ?? 'demo-user',
          summary: '[图片]',
          createdAt: DateTime(2026, 8, 13, 9, 59),
          type: 'image',
        ),
      ],
    ),
  ];
}

class ReportCaptureRepository extends DemoImRepository {
  ReportCaptureRepository({this.fail = false}) : super(latency: Duration.zero);

  final bool fail;
  String? lastTargetType;
  String? lastTargetId;

  @override
  Future<void> report({
    required String targetType,
    required String targetId,
    required String reason,
    String details = '',
  }) async {
    lastTargetType = targetType;
    lastTargetId = targetId;
    if (fail) throw StateError('forced report failure');
  }
}
