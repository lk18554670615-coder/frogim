import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/home_screen.dart';
import 'package:linli_im/ui/screens/login_screen.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('1440 桌面登录使用独立窗体而不是放大手机表单', (tester) async {
    _setViewport(tester, const Size(1440, 1000));
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: LoginScreen(controller: controller),
      ),
    );
    await _pumpUi(tester);

    expect(find.byKey(const Key('desktop-login-shell')), findsOneWidget);
    expect(find.textContaining('让重要的沟通'), findsOneWidget);
    expect(find.byKey(const Key('phone-field')), findsOneWidget);
    expect(find.byKey(const Key('login-button')), findsOneWidget);
    final card = tester.getRect(find.byKey(const Key('desktop-login-card')));
    final policy = tester.getRect(
      find.byKey(const Key('login-policy-consent')),
    );
    expect(card.bottom, greaterThanOrEqualTo(policy.bottom));
    expect(tester.takeException(), isNull);
  });

  testWidgets('1440 工作台提供账号栏、会话列、聊天区和可收起资料栏', (tester) async {
    final controller = await _pumpDesktopHome(
      tester,
      size: const Size(1440, 1000),
    );
    addTearDown(controller.dispose);

    expect(find.byKey(const Key('desktop-home-workspace')), findsOneWidget);
    expect(
      tester.getSize(find.byKey(const Key('home-navigation-rail'))).width,
      72,
    );
    expect(
      tester
          .getSize(find.byKey(const Key('desktop-conversation-column')))
          .width,
      304,
    );
    expect(
      tester.getSize(find.byKey(const Key('desktop-chat-details-panel'))).width,
      320,
    );
    expect(find.byKey(const Key('message-input')), findsOneWidget);

    await tester.tap(find.byKey(const Key('desktop-close-details')));
    await _pumpUi(tester);
    expect(find.byKey(const Key('desktop-chat-details-panel')), findsNothing);
    await tester.tap(find.byKey(const Key('chat-more-button')));
    await _pumpUi(tester);
    expect(find.byKey(const Key('desktop-chat-details-panel')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('1280 联系人工作区保持约 320 像素主列表并可返回消息', (tester) async {
    final controller = await _pumpDesktopHome(
      tester,
      size: const Size(1280, 900),
    );
    addTearDown(controller.dispose);

    await tester.tap(find.byKey(const Key('desktop-nav-contacts')));
    await _pumpUi(tester);
    expect(find.byKey(const Key('desktop-contacts-workspace')), findsOneWidget);
    expect(
      tester.getSize(find.byKey(const Key('desktop-contact-column'))).width,
      320,
    );
    expect(find.byKey(const Key('desktop-directory-overview')), findsOneWidget);

    await tester.sendKeyDownEvent(LogicalKeyboardKey.controlLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.digit1);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.controlLeft);
    await _pumpUi(tester);
    expect(
      find.byKey(const Key('wide-conversation-workspace')),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('桌面探索与我的页面使用工作台网格而不是放大手机列表', (tester) async {
    final controller = await _pumpDesktopHome(
      tester,
      size: const Size(1280, 900),
    );
    addTearDown(controller.dispose);

    await tester.tap(find.byKey(const Key('desktop-nav-discover')));
    await _pumpUi(tester);
    expect(find.byKey(const Key('desktop-tools-workspace')), findsOneWidget);
    expect(find.text('全局搜索'), findsOneWidget);
    expect(find.text('创建群聊'), findsOneWidget);

    await tester.tap(find.byKey(const Key('desktop-nav-profile')));
    await _pumpUi(tester);
    expect(find.byKey(const Key('desktop-account-workspace')), findsOneWidget);
    expect(find.text('账号与安全'), findsOneWidget);
    expect(find.text('帮助与反馈'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('桌面会话支持鼠标右键菜单且消息保留多选转发入口', (tester) async {
    final controller = await _pumpDesktopHome(
      tester,
      size: const Size(1440, 1000),
    );
    addTearDown(controller.dispose);

    final conversation = find.byKey(
      const ValueKey('conversation-slidable-c-team'),
    );
    expect(conversation, findsOneWidget);
    await tester.tap(conversation, buttons: kSecondaryMouseButton);
    await _pumpUi(tester);
    expect(find.text('取消置顶'), findsOneWidget);
    expect(find.text('消息免打扰'), findsWidgets);
    expect(find.text('归档会话'), findsOneWidget);
    await tester.tapAt(const Offset(1200, 800));
    await _pumpUi(tester);
  });

  testWidgets('1280 深色 200% 字体仍可操作且没有溢出', (tester) async {
    _setViewport(tester, const Size(1280, 900));
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.dark),
        home: MediaQuery(
          data: const MediaQueryData(
            size: Size(1280, 900),
            textScaler: TextScaler.linear(2),
            platformBrightness: Brightness.dark,
          ),
          child: HomeScreen(controller: controller, onToggleTheme: () {}),
        ),
      ),
    );
    await _pumpUi(tester);

    expect(find.byKey(const Key('desktop-home-workspace')), findsOneWidget);
    expect(find.byKey(const Key('message-input')), findsOneWidget);
    expect(find.byKey(const Key('desktop-global-search')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('1023 窄宽度回退移动端导航和页面逻辑', (tester) async {
    _setViewport(tester, const Size(1023, 800));
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    await tester.runAsync(controller.loginAsDemo);
    addTearDown(controller.dispose);

    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: HomeScreen(controller: controller, onToggleTheme: () {}),
      ),
    );
    await _pumpUi(tester);

    expect(find.byKey(const Key('desktop-home-workspace')), findsNothing);
    expect(find.byKey(const Key('home-tab-0')), findsOneWidget);
    expect(find.byKey(const Key('home-navigation-rail')), findsNothing);
    expect(tester.takeException(), isNull);
  });
}

Future<AppController> _pumpDesktopHome(
  WidgetTester tester, {
  required Size size,
}) async {
  _setViewport(tester, size);
  final controller = AppController(DemoImRepository(latency: Duration.zero));
  await tester.runAsync(controller.loginAsDemo);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: HomeScreen(controller: controller, onToggleTheme: () {}),
    ),
  );
  await _pumpUi(tester);
  return controller;
}

Future<void> _pumpUi(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 600));
}

void _setViewport(WidgetTester tester, Size size) {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}
