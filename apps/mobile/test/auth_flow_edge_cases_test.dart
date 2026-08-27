import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/login_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('登录页验证码状态不会串到新打开的注册页', (tester) async {
    final repository = _CountingAuthRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.initialize();
    await _pump(tester, LoginScreen(controller: controller));

    await tester.enterText(find.byType(TextFormField).first, '13800138000');
    await tester.tap(find.byKey(const Key('request-code-button')));
    await _settle(tester);
    expect(find.text('重新获取'), findsOneWidget);
    expect(repository.loginCodeRequests, 1);

    await tester.tap(find.byKey(const Key('open-register')));
    await _settle(tester);
    expect(find.text('创建账号'), findsOneWidget);
    expect(find.text('获取验证码').hitTestable(), findsOneWidget);
    expect(find.text('重新获取').hitTestable(), findsNothing);
    expect(find.text('验证码已发送，5 分钟内有效'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('修改手机号后登录、注册和重置页均恢复获取验证码', (tester) async {
    final controller = AppController(_CountingAuthRepository());
    addTearDown(controller.dispose);
    await controller.initialize();

    await _pump(tester, LoginScreen(controller: controller));
    await _requestThenChangePhone(tester, expectSentNotice: false);

    await _pump(tester, RegisterScreen(controller: controller));
    await _requestThenChangePhone(tester, expectSentNotice: true);

    await _pump(tester, ResetPasswordScreen(controller: controller));
    await _requestThenChangePhone(tester, expectSentNotice: true);

    expect(tester.takeException(), isNull);
  });

  test('字母手机号和 33 位手机号在 controller 层不会发出任何请求', () async {
    final repository = _CountingAuthRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.initialize();

    for (final invalidPhone in [
      'phoneABC',
      '138001380001380013800138001380013',
    ]) {
      expect(await controller.requestCode(invalidPhone), isFalse);
      expect(controller.error, '请输入有效手机号');

      expect(await controller.requestResetCode(invalidPhone), isFalse);
      expect(controller.error, '请输入有效手机号');

      expect(
        await controller.registerAccount(
          phone: invalidPhone,
          code: '123456',
          password: 'StrongPass123!',
          name: '测试用户',
        ),
        isFalse,
      );
      expect(
        await controller.resetPassword(
          phone: invalidPhone,
          code: '123456',
          password: 'StrongPass123!',
        ),
        isFalse,
      );
    }

    expect(repository.loginCodeRequests, 0);
    expect(repository.resetCodeRequests, 0);
    expect(repository.registerRequests, 0);
    expect(repository.resetRequests, 0);
  });

  testWidgets('认证页 UI 拒绝字母和 33 位手机号', (tester) async {
    final repository = _CountingAuthRepository();
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.initialize();
    await _pump(tester, LoginScreen(controller: controller));

    final phone = find.byType(TextFormField).first;
    await tester.enterText(phone, 'phoneABC');
    await tester.tap(find.byKey(const Key('request-code-button')));
    await tester.pump();
    expect(find.text('请输入有效手机号'), findsOneWidget);
    expect(repository.loginCodeRequests, 0);

    await tester.enterText(phone, '138001380001380013800138001380013');
    await tester.tap(find.byKey(const Key('request-code-button')));
    await tester.pump();
    expect(find.text('请输入有效手机号'), findsOneWidget);
    expect(repository.loginCodeRequests, 0);
    expect(tester.takeException(), isNull);
  });

  testWidgets('360×640 且键盘弹出时注册和重置按钮可滚动到可见区', (tester) async {
    final controller = AppController(_CountingAuthRepository());
    addTearDown(controller.dispose);
    await controller.initialize();

    await _pumpWithKeyboard(tester, RegisterScreen(controller: controller));
    await _expectScrollableButtonVisible(tester, const Key('register-submit'));

    await _pumpWithKeyboard(
      tester,
      ResetPasswordScreen(controller: controller),
    );
    await _expectScrollableButtonVisible(tester, const Key('reset-submit'));

    expect(tester.takeException(), isNull);
  });
}

Future<void> _requestThenChangePhone(
  WidgetTester tester, {
  required bool expectSentNotice,
}) async {
  final phone = find.byType(TextFormField).first;
  await tester.enterText(phone, '13800138000');
  await tester.tap(find.text('获取验证码'));
  await _settle(tester);
  expect(find.text('重新获取'), findsOneWidget);
  expect(
    find.text('验证码已发送，5 分钟内有效'),
    expectSentNotice ? findsOneWidget : findsNothing,
  );

  await tester.enterText(phone, '13900139000');
  await tester.pump();
  expect(find.text('获取验证码'), findsOneWidget);
  expect(find.text('重新获取'), findsNothing);
  expect(find.text('验证码已发送，5 分钟内有效'), findsNothing);
}

Future<void> _expectScrollableButtonVisible(
  WidgetTester tester,
  Key buttonKey,
) async {
  final button = find.byKey(buttonKey);
  await tester.scrollUntilVisible(
    button,
    180,
    scrollable: find.byType(Scrollable).first,
  );
  await tester.pump();

  final rect = tester.getRect(button);
  expect(rect.top, greaterThanOrEqualTo(48));
  expect(rect.bottom, lessThanOrEqualTo(640 - 280));
  expect(button.hitTestable(), findsOneWidget);
}

Future<void> _pump(WidgetTester tester, Widget home) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  tester.view.resetViewInsets();
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.view.resetViewInsets);
  await tester.pumpWidget(const SizedBox.shrink());
  await tester.pump();
  await tester.pumpWidget(
    MaterialApp(theme: buildLinliTheme(Brightness.light), home: home),
  );
  await _settle(tester);
}

Future<void> _pumpWithKeyboard(WidgetTester tester, Widget home) async {
  tester.view.physicalSize = const Size(360, 640);
  tester.view.devicePixelRatio = 1;
  tester.view.viewInsets = const FakeViewPadding(bottom: 280);
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.view.resetViewInsets);
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

class _CountingAuthRepository extends DemoImRepository {
  _CountingAuthRepository() : super(latency: Duration.zero);

  int loginCodeRequests = 0;
  int resetCodeRequests = 0;
  int registerRequests = 0;
  int resetRequests = 0;

  @override
  Future<String?> requestCode(String phone) async {
    loginCodeRequests += 1;
    return super.requestCode(phone);
  }

  @override
  Future<void> requestPasswordResetCode(String phone) async {
    resetCodeRequests += 1;
  }

  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
  }) async {
    registerRequests += 1;
    return super.register(
      phone: phone,
      code: code,
      password: password,
      name: name,
    );
  }

  @override
  Future<void> resetPassword({
    required String phone,
    required String code,
    required String password,
  }) async {
    resetRequests += 1;
    return super.resetPassword(phone: phone, code: code, password: password);
  }
}
