import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/announcement_screens.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('系统通知作为会话首行紧凑展示并可打开通知中心', (tester) async {
    final publishedAt = DateTime.now();
    final controller = await _pumpConversations(
      tester,
      announcements: [
        AppAnnouncement(
          id: 'system-notice-unread',
          title: '服务升级已完成',
          content: '即时消息服务已恢复稳定运行。',
          status: 'published',
          pinned: true,
          publishedAt: publishedAt,
        ),
      ],
    );
    addTearDown(controller.dispose);

    final section = find.byKey(const Key('system-notification-section'));
    final entry = find.byKey(const Key('system-notifications-entry'));
    final firstConversation = find.byKey(const ValueKey('c-team'));
    expect(section, findsOneWidget);
    expect(entry, findsOneWidget);
    expect(firstConversation, findsOneWidget);

    final entryRect = tester.getRect(entry);
    final sectionRect = tester.getRect(section);
    final conversationRect = tester.getRect(firstConversation);
    expect(entryRect.height, 74);
    expect(sectionRect.height, lessThanOrEqualTo(74));
    expect(entryRect.left, lessThanOrEqualTo(.5));
    expect(entryRect.right, greaterThanOrEqualTo(374.5));
    expect(sectionRect.top, lessThan(conversationRect.top));
    expect(sectionRect.bottom, lessThanOrEqualTo(conversationRect.top));

    expect(find.text('系统通知'), findsOneWidget);
    expect(find.text('服务升级已完成'), findsOneWidget);
    expect(
      find.descendant(of: entry, matching: find.text('1')),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: entry,
        matching: find.text(_notificationTime(publishedAt)),
      ),
      findsOneWidget,
    );
    expect(
      find.bySemanticsLabel(RegExp(r'系统通知.*1 条未读.*服务升级已完成')),
      findsOneWidget,
    );

    await tester.tap(entry);
    await tester.pumpAndSettle();
    expect(find.byType(SystemNotificationsScreen), findsOneWidget);
    expect(find.text('系统通知'), findsOneWidget);
    expect(find.text('服务升级已完成'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('没有通知时会话首行给出清晰空状态并可进入空通知中心', (tester) async {
    final controller = await _pumpConversations(
      tester,
      announcements: const [],
    );
    addTearDown(controller.dispose);

    final entry = find.byKey(const Key('system-notifications-entry'));
    expect(find.text('暂无新通知'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp(r'系统通知.*暂无新通知')), findsOneWidget);
    expect(tester.getSize(entry).height, 74);

    await tester.tap(entry);
    await tester.pumpAndSettle();
    expect(find.byType(SystemNotificationsScreen), findsOneWidget);
    expect(find.text('暂无系统通知'), findsOneWidget);
    expect(find.text('平台公告和服务提醒会显示在这里。'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

Future<AppController> _pumpConversations(
  WidgetTester tester, {
  required List<AppAnnouncement> announcements,
}) async {
  tester.view.physicalSize = const Size(375, 812);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);

  final controller = AppController(DemoImRepository(latency: Duration.zero));
  await tester.runAsync(controller.loginAsDemo);
  controller.announcements = announcements;

  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: Scaffold(body: ConversationsTab(controller: controller)),
    ),
  );
  await tester.pumpAndSettle();
  return controller;
}

String _notificationTime(DateTime value) {
  final local = value.toLocal();
  return '${local.hour.toString().padLeft(2, '0')}:'
      '${local.minute.toString().padLeft(2, '0')}';
}
