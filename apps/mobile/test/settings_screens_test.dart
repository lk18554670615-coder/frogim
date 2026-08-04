import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/settings_preferences.dart';
import 'package:linli_im/ui/screens/settings_screens.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('设置中心覆盖账号、通知、隐私、通用、存储与支持入口', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    await _pump(
      tester,
      SettingsScreen(controller: controller, onToggleTheme: () {}),
    );

    expect(find.text('账号与安全'), findsOneWidget);
    expect(find.text('我的收藏'), findsOneWidget);
    expect(find.text('登录设备'), findsOneWidget);
    expect(find.text('消息通知'), findsOneWidget);
    expect(find.text('隐私与安全'), findsOneWidget);
    expect(find.text('通用'), findsOneWidget);
    expect(find.text('存储空间'), findsOneWidget);
    expect(find.text('帮助与反馈'), findsOneWidget);
    expect(find.text('关于邻里通讯'), findsOneWidget);

    await tester.tap(find.byKey(const Key('settings-notifications')));
    await _settle(tester);
    expect(find.text('通知偏好'), findsOneWidget);
    expect(find.text('系统通知权限'), findsOneWidget);
  });

  testWidgets('通知偏好写入本机且通用外观操作生效', (tester) async {
    await _pump(tester, const NotificationSettingsScreen());
    await _settle(tester);
    await tester.tap(find.byKey(const Key('notification-enabled-switch')));
    await _settle(tester);

    final preferences = await SharedPreferences.getInstance();
    expect(
      preferences.getBool(LocalSettingsStore.notificationEnabled),
      isFalse,
    );

    var toggled = false;
    await _pump(
      tester,
      GeneralSettingsScreen(onToggleTheme: () => toggled = true),
    );
    await _settle(tester);
    await tester.tap(find.byKey(const Key('general-toggle-theme')));
    expect(toggled, isTrue);
  });

  testWidgets('隐私页清除持久化的最近搜索', (tester) async {
    final preferences = await SharedPreferences.getInstance();
    await preferences.setStringList(LocalSettingsStore.recentSearches, [
      '林屿',
      '产品小组',
    ]);

    await _pump(tester, const PrivacyScreen());
    await tester.tap(find.byKey(const Key('privacy-clear-search-history')));
    await _settle(tester);

    expect(
      preferences.getStringList(LocalSettingsStore.recentSearches),
      isNull,
    );
    expect(find.text('最近搜索已从本机清除'), findsOneWidget);
  });

  testWidgets('存储页经二次确认清除所有会话本机消息', (tester) async {
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);
    final conversation = controller.conversations.first;
    await tester.runAsync(() => controller.loadMessages(conversation.id));
    expect(controller.messagesFor(conversation.id), isNotEmpty);

    await _pump(tester, StorageScreen(controller: controller));
    await tester.tap(find.byKey(const Key('storage-clear-local-messages')));
    await _settle(tester);
    expect(find.text('清除本机消息缓存？'), findsOneWidget);
    await tester.tap(find.text('清除本机缓存'));
    await _settle(tester);
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 120)),
    );
    await tester.pump();

    for (final item in controller.conversations) {
      expect(controller.messagesFor(item.id), isEmpty);
    }
  });

  testWidgets('帮助反馈明确保存草稿而不伪装提交', (tester) async {
    final appController = (await tester.runAsync(_controller))!;
    addTearDown(appController.dispose);
    await _pump(tester, HelpFeedbackScreen(controller: appController));
    await _settle(tester);
    await tester.enterText(
      find.byKey(const Key('feedback-field')),
      '在弱网下发送图片时希望展示更清晰的重试原因。',
    );
    await tester.pump();
    expect(find.byKey(const Key('submit-feedback')), findsOneWidget);
    expect(find.byKey(const Key('save-feedback-draft')), findsOneWidget);
    expect(
      tester
          .widget<FilledButton>(find.byKey(const Key('submit-feedback')))
          .onPressed,
      isNotNull,
    );
  });

  testWidgets('设置中心支持深色与 200% 字体且无布局异常', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = (await tester.runAsync(_controller))!;
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MediaQuery(
        data: const MediaQueryData(textScaler: TextScaler.linear(2)),
        child: MaterialApp(
          theme: buildLinliTheme(Brightness.dark),
          home: SettingsScreen(controller: controller, onToggleTheme: () {}),
        ),
      ),
    );
    await _settle(tester);

    expect(find.text('账号与安全'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

Future<AppController> _controller() async {
  final repository = DemoImRepository(latency: Duration.zero);
  final controller = AppController(repository);
  controller.authenticated = true;
  controller.currentUser = DemoImRepository.demoUser;
  controller.connected = true;
  controller.conversations = await repository.conversations();
  controller.contacts = await repository.contacts();
  return controller;
}

Future<void> _pump(WidgetTester tester, Widget home) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    MaterialApp(theme: buildLinliTheme(Brightness.light), home: home),
  );
  await _settle(tester);
}

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 6; i++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}
