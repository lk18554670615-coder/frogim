import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/login_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('登录页提供验证码与密码两种方式且拒绝空值', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    expect(find.text('验证码登录'), findsWidgets);
    expect(find.text('密码登录'), findsOneWidget);
    expect(find.byKey(const Key('open-register')), findsOneWidget);

    await tester.tap(find.byKey(const Key('login-button')));
    await tester.pump();
    expect(find.text('请输入有效手机号'), findsOneWidget);
    expect(find.text('请输入验证码'), findsOneWidget);
    expect(controller.authenticated, isFalse);

    await tester.tap(find.text('密码登录').hitTestable());
    await _settle(tester);
    expect(find.byKey(const Key('password-field')), findsOneWidget);
    expect(find.byKey(const Key('forgot-password')), findsOneWidget);
  });

  testWidgets('开发环境一键填充固定验证码 123456', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    await tester.tap(find.byKey(const Key('dev-fill-code')));
    await tester.pump();
    final field = tester.widget<TextFormField>(
      find.byKey(const Key('code-field')),
    );
    expect(field.controller?.text, '123456');
  });

  testWidgets('登录前必须主动勾选用户协议和隐私政策', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    await tester.enterText(find.byKey(const Key('phone-field')), '13800138000');
    await tester.enterText(find.byKey(const Key('code-field')), '123456');
    await tester.tap(find.byKey(const Key('login-button')));
    await _settle(tester);
    expect(find.text('请先阅读并同意用户协议和隐私政策'), findsOneWidget);
    expect(controller.authenticated, isFalse);

    await tester.tap(find.byKey(const Key('policy-consent-checkbox')));
    await tester.tap(find.byKey(const Key('login-button')));
    await _settle(tester);
    expect(controller.authenticated, isTrue);
  });

  testWidgets('小屏登录页协议说明与两个链接保持同一行', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    final labelCenter = tester.getCenter(find.text('我已阅读并同意'));
    final termsCenter = tester.getCenter(find.text('用户协议'));
    final privacyCenter = tester.getCenter(find.text('隐私政策'));

    expect((labelCenter.dy - termsCenter.dy).abs(), lessThan(2));
    expect((termsCenter.dy - privacyCenter.dy).abs(), lessThan(2));
  });

  testWidgets('注册与重置密码页面包含完整真实字段', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    await tester.tap(find.byKey(const Key('open-register')));
    await _settle(tester);
    expect(find.text('注册账号'), findsOneWidget);
    expect(find.byKey(const Key('register-name')), findsOneWidget);
    expect(find.byKey(const Key('register-password')), findsOneWidget);
    expect(find.byKey(const Key('register-dev-fill-code')), findsOneWidget);

    await _pump(tester, LoginScreen(controller: controller));
    await tester.tap(find.text('密码登录').hitTestable());
    await _settle(tester);
    await tester.tap(find.byKey(const Key('forgot-password')));
    await _settle(tester);
    expect(find.text('重置密码'), findsWidgets);
    expect(find.byKey(const Key('reset-password')), findsOneWidget);
    expect(find.byKey(const Key('reset-dev-fill-code')), findsOneWidget);
  });

  testWidgets('验证码不是固定值时演示仓库拒绝登录', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);

    await tester.runAsync(() => controller.login('13800138000', '999999'));
    expect(controller.authenticated, isFalse);
    expect(controller.error, isNotNull);

    await tester.runAsync(() => controller.login('13800138000', '123456'));
    expect(controller.authenticated, isTrue);
  });
}

Future<void> _pump(WidgetTester tester, Widget home) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(const SizedBox.shrink());
  await tester.pump();
  await tester.pumpWidget(
    MaterialApp(theme: buildLinliTheme(Brightness.light), home: home),
  );
  await _settle(tester);
}

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 5; i++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}
