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

  testWidgets('没有通知时不占用消息列表首行', (tester) async {
    final controller = await _pumpConversations(
      tester,
      announcements: const [],
    );
    addTearDown(controller.dispose);

    expect(find.byKey(const Key('system-notifications-entry')), findsNothing);
    expect(find.byKey(const Key('system-notification-section')), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('系统通知支持左滑删除，后续新通知会重新出现', (tester) async {
    final controller = await _pumpConversations(
      tester,
      announcements: [
        AppAnnouncement(
          id: 'deletable-notice',
          title: '可删除通知',
          content: '删除仅影响当前用户。',
          status: 'published',
          pinned: false,
          publishedAt: DateTime.now(),
        ),
      ],
    );
    addTearDown(controller.dispose);

    final entry = find.byKey(const Key('system-notifications-entry'));
    await tester.drag(entry, const Offset(-260, 0));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('system-notification-delete')));
    await tester.pumpAndSettle();
    expect(find.text('删除系统通知？'), findsOneWidget);
    await tester.tap(find.text('删除').last);
    await tester.pumpAndSettle();
    expect(entry, findsNothing);
    expect(controller.announcements, isEmpty);

    controller.announcements = [
      AppAnnouncement(
        id: 'new-notice',
        title: '后续新通知',
        content: '新公告重新显示系统通知入口。',
        status: 'published',
        pinned: false,
        publishedAt: DateTime.now(),
      ),
    ];
    controller.notifyListeners();
    await tester.pumpAndSettle();
    expect(entry, findsOneWidget);
    expect(find.text('后续新通知'), findsOneWidget);
  });

  testWidgets('会话首行优先展示最新未读而不是较早的置顶通知', (tester) async {
    final now = DateTime.now();
    final controller = await _pumpConversations(
      tester,
      announcements: [
        AppAnnouncement(
          id: 'older-pinned',
          title: '较早置顶通知',
          content: '置顶通知仍应在通知列表顶部。',
          status: 'published',
          pinned: true,
          publishedAt: now.subtract(const Duration(minutes: 10)),
        ),
        AppAnnouncement(
          id: 'latest-unread',
          title: '最新未读通知',
          content: '会话首行应预览这一条。',
          status: 'published',
          pinned: false,
          publishedAt: now,
        ),
      ],
    );
    addTearDown(controller.dispose);

    final entry = find.byKey(const Key('system-notifications-entry'));
    expect(
      find.descendant(of: entry, matching: find.text('最新未读通知')),
      findsOneWidget,
    );
    expect(
      find.descendant(of: entry, matching: find.text('较早置顶通知')),
      findsNothing,
    );
    expect(
      find.bySemanticsLabel(RegExp(r'系统通知.*2 条未读.*最新未读通知')),
      findsOneWidget,
    );

    await tester.tap(entry);
    await tester.pumpAndSettle();
    final rows = find.byType(InkWell);
    expect(find.text('较早置顶通知'), findsOneWidget);
    expect(find.text('最新未读通知'), findsOneWidget);
    expect(rows, findsWidgets);
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
