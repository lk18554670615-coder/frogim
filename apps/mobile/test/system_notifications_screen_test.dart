import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/announcement_screens.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('消息页系统通知入口打开完整公告列表并同步已读', (tester) async {
    final repository = DemoImRepository(latency: Duration.zero);
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..announcements = [
        AppAnnouncement(
          id: 'notice-1',
          title: '社区沟通公约',
          content: '请保护个人隐私，友好交流。遇到违规内容可在资料页提交举报。',
          status: 'published',
          pinned: true,
          publishedAt: DateTime(2026, 8, 16, 9),
        ),
      ];
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: Scaffold(body: SystemNotificationTile(controller: controller)),
      ),
    );
    await _settle(tester);

    expect(find.byKey(const Key('system-notifications-entry')), findsOneWidget);
    expect(find.text('系统通知'), findsOneWidget);
    expect(find.text('社区沟通公约'), findsOneWidget);
    expect(controller.systemNotificationUnreadCount, 1);

    await tester.tap(find.byKey(const Key('system-notifications-entry')));
    await _settle(tester);
    expect(find.byType(SystemNotificationsScreen), findsOneWidget);
    expect(find.text('社区沟通公约'), findsWidgets);

    await tester.tap(find.text('社区沟通公约').last);
    await _settle(tester);
    expect(find.text('平台公告'), findsOneWidget);
    expect(find.textContaining('请保护个人隐私'), findsOneWidget);
    expect(controller.systemNotificationUnreadCount, 0);
  });

  testWidgets('没有真实公告时系统通知页展示明确空态', (tester) async {
    final repository = DemoImRepository(latency: Duration.zero);
    final controller = AppController(repository)
      ..authenticated = true
      ..currentUser = DemoImRepository.demoUser
      ..announcements = [];
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: SystemNotificationsScreen(controller: controller),
      ),
    );
    await _settle(tester);

    expect(find.text('暂无系统通知'), findsOneWidget);
    expect(find.text('平台公告和服务提醒会显示在这里。'), findsOneWidget);
  });
}

Future<void> _settle(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 600));
}
