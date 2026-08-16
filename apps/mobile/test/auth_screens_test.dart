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

  testWidgets('进入登录页会清理上一个认证流程遗留的错误', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    controller.error = 'account already exists';
    addTearDown(controller.dispose);

    await _pump(tester, LoginScreen(controller: controller));

    expect(controller.error, isNull);
    expect(find.byKey(const Key('login-error')), findsNothing);
    expect(find.text('请输入有效手机号'), findsNothing);
  });

  testWidgets('认证页面不展示演示登录或固定验证码入口', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    expect(find.byKey(const Key('dev-fill-code')), findsNothing);
    expect(find.byKey(const Key('demo-login-button')), findsNothing);
    expect(find.textContaining('开发环境'), findsNothing);
    expect(find.textContaining('演示'), findsNothing);
  });

  testWidgets('登录前必须主动勾选用户协议和隐私政策', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    await tester.enterText(find.byType(TextFormField).first, '13800138000');
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

  testWidgets('小屏登录页协议同一行且关键入口不少于 44 点', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    final labelCenter = tester.getCenter(find.text('我已阅读并同意'));
    final termsCenter = tester.getCenter(find.text('用户协议'));
    final privacyCenter = tester.getCenter(find.text('隐私政策'));
    final termsButton = find.ancestor(
      of: find.text('用户协议'),
      matching: find.byType(TextButton),
    );
    final privacyButton = find.ancestor(
      of: find.text('隐私政策'),
      matching: find.byType(TextButton),
    );

    expect((labelCenter.dy - termsCenter.dy).abs(), lessThan(2));
    expect((termsCenter.dy - privacyCenter.dy).abs(), lessThan(2));
    expect(
      tester.getTopLeft(find.byKey(const Key('login-policy-consent'))).dy,
      lessThan(tester.getTopLeft(find.byKey(const Key('login-button'))).dy),
    );
    expect(
      tester.getSize(find.byKey(const Key('login-mode-control'))).height,
      greaterThanOrEqualTo(44),
    );
    expect(
      tester.getSize(find.byKey(const Key('policy-consent-checkbox'))).height,
      greaterThanOrEqualTo(44),
    );
    expect(tester.getSize(termsButton).height, greaterThanOrEqualTo(44));
    expect(tester.getSize(privacyButton).height, greaterThanOrEqualTo(44));
  });

  testWidgets('320 像素窄屏登录内容不会越过左右边界', (tester) async {
    tester.view.physicalSize = const Size(320, 700);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final controller = AppController(DemoImRepository(latency: Duration.zero))
      ..authPolicyLoaded = true
      ..authPolicyAvailable = false;
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildLinliTheme(Brightness.light),
        home: LoginScreen(controller: controller),
      ),
    );
    await _settle(tester);

    for (final key in const [
      Key('login-mode-control'),
      Key('code-field'),
      Key('login-policy-consent'),
      Key('login-button'),
      Key('open-register'),
    ]) {
      final rect = tester.getRect(find.byKey(key));
      expect(rect.left, greaterThanOrEqualTo(0), reason: '$key left edge');
      expect(rect.right, lessThanOrEqualTo(320), reason: '$key right edge');
    }
    expect(tester.takeException(), isNull);
  });

  testWidgets('注册与重置密码页面包含完整真实字段', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, LoginScreen(controller: controller));

    await tester.tap(find.byKey(const Key('open-register')));
    await _settle(tester);
    expect(find.text('创建账号'), findsOneWidget);
    expect(find.bySemanticsLabel('青蛙呱呱'), findsOneWidget);
    expect(find.byKey(const Key('register-name')), findsOneWidget);
    expect(find.byKey(const Key('register-password')), findsOneWidget);
    expect(find.byKey(const Key('register-dev-fill-code')), findsNothing);

    await _pump(tester, LoginScreen(controller: controller));
    await tester.tap(find.text('密码登录').hitTestable());
    await _settle(tester);
    await tester.tap(find.byKey(const Key('forgot-password')));
    await _settle(tester);
    expect(find.text('找回密码'), findsOneWidget);
    expect(find.bySemanticsLabel('青蛙呱呱'), findsOneWidget);
    expect(find.byKey(const Key('reset-password')), findsOneWidget);
    expect(find.byKey(const Key('reset-dev-fill-code')), findsNothing);
  });

  testWidgets('注册和重置密码只校验当前验证码请求所需的手机号', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, RegisterScreen(controller: controller));

    await tester.tap(find.text('获取验证码'));
    await _settle(tester);
    expect(find.text('请输入有效手机号'), findsOneWidget);
    expect(find.text('请输入验证码'), findsNothing);
    expect(find.text('密码至少 8 个字符'), findsNothing);

    await _pump(tester, ResetPasswordScreen(controller: controller));
    await tester.tap(find.text('获取验证码'));
    await _settle(tester);
    expect(find.text('请输入有效手机号'), findsOneWidget);
    expect(find.text('请输入验证码'), findsNothing);
    expect(find.text('密码至少 8 个字符'), findsNothing);
  });

  testWidgets('注册页不会因填写手机号就校验所有空白字段', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, RegisterScreen(controller: controller));

    await tester.enterText(find.byType(TextFormField).first, '13800138000');
    await tester.pump();

    expect(find.text('请输入验证码'), findsNothing);
    expect(find.text('请输入昵称'), findsNothing);
    expect(find.text('密码至少 8 个字符'), findsNothing);
    expect(find.text('请再次输入登录密码'), findsNothing);

    await tester.scrollUntilVisible(
      find.byKey(const Key('register-password')),
      160,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.tap(find.byKey(const Key('register-password')));
    await tester.pump();
    expect(find.text('密码至少 8 个字符'), findsNothing);

    await tester.tap(find.byKey(const Key('register-confirm-password')));
    await tester.pump();
    expect(find.text('密码至少 8 个字符'), findsOneWidget);
    expect(find.text('请再次输入登录密码'), findsNothing);
  });

  testWidgets('注册与重置密码提供密码可见性和清晰的返回入口', (tester) async {
    final controller = AppController(DemoImRepository(latency: Duration.zero));
    addTearDown(controller.dispose);
    await _pump(tester, RegisterScreen(controller: controller));

    expect(find.byTooltip('显示密码'), findsNWidgets(2));
    await tester.scrollUntilVisible(
      find.text('已有账号，返回登录'),
      160,
      scrollable: find.byType(Scrollable).first,
    );
    expect(find.text('已有账号，返回登录'), findsOneWidget);
    await tester.tap(find.byTooltip('显示密码').first);
    await tester.pump();
    expect(find.byTooltip('隐藏密码'), findsOneWidget);

    await _pump(tester, ResetPasswordScreen(controller: controller));
    expect(find.byTooltip('显示密码'), findsOneWidget);
    expect(find.text('返回登录'), findsOneWidget);
    expect(find.text('重置后，其他设备上的登录会话将自动失效。'), findsOneWidget);
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

  test('认证依赖不可用时展示中文可恢复提示', () async {
    final controller = AppController(_UnavailableAuthRepository());
    addTearDown(controller.dispose);

    await controller.requestCode('13800138000');

    expect(controller.error, '即时通讯服务暂时不可用，请稍后重试');
  });
}

class _UnavailableAuthRepository extends DemoImRepository {
  _UnavailableAuthRepository() : super(latency: Duration.zero);

  @override
  Future<String?> requestCode(String phone) async {
    throw Exception('instant messaging service is temporarily unavailable');
  }
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
