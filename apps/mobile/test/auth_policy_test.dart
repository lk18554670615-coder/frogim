import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/ui/screens/login_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('AuthPolicy 解析公开策略并将不安全数值收敛到客户端边界', () {
    final policy = AuthPolicy.fromJson({
      'registrationEnabled': false,
      'passwordMinLength': 12,
      'passwordMaxBytes': 72,
      'messageRecallMinutes': 5,
      'directRecallMinutes': 60,
      'groupRecallMinutes': 10080,
      'inviteRegistrationMode': 'required',
    });

    expect(policy.registrationEnabled, isFalse);
    expect(policy.passwordMinLength, 12);
    expect(policy.passwordMaxBytes, 72);
    expect(policy.messageRecallMinutes, 5);
    expect(policy.messageMutationWindow, const Duration(minutes: 5));
    expect(policy.directRecallWindow, const Duration(minutes: 60));
    expect(policy.groupRecallWindow, const Duration(days: 7));
    expect(policy.invitationRequired, isTrue);
    expect(policy.invitationEnabled, isTrue);

    final constrained = AuthPolicy.fromJson({
      'registrationEnabled': 'false',
      'passwordMinLength': 99,
      'passwordMaxBytes': 120,
      'messageRecallMinutes': 9999,
      'directRecallMinutes': 10081,
    });
    expect(constrained.registrationEnabled, isTrue);
    expect(constrained.passwordMinLength, 16);
    expect(constrained.passwordMaxBytes, 72);
    expect(constrained.messageRecallMinutes, 1440);
    expect(constrained.directRecallMinutes, 10080);
    expect(
      AuthPolicy.fromJson({'directRecallMinutes': 0}).directRecallMinutes,
      1,
    );

    final fallback = AuthPolicy.fromJson(const {});
    expect(fallback.registrationEnabled, isTrue);
    expect(fallback.passwordMinLength, 8);
    expect(fallback.passwordMaxBytes, 72);
    expect(fallback.messageRecallMinutes, 2);
    expect(fallback.directRecallWindow, const Duration(hours: 24));
    expect(fallback.inviteRegistrationMode, 'optional');
    expect(
      AuthPolicy.fromJson({
        'inviteRegistrationMode': 'disabled',
      }).invitationEnabled,
      isFalse,
    );
    expect(
      AuthPolicy.fromJson({
        'inviteRegistrationMode': 'unexpected',
      }).inviteRegistrationMode,
      'optional',
    );
  });

  test('邀请码二维码只接受注册深链并规范为大写', () {
    expect(
      inviteCodeFromQrPayload('qingwaguagua://register?invite=abc-2026'),
      'ABC-2026',
    );
    expect(inviteCodeFromQrPayload('  invite_88  '), 'INVITE_88');
    expect(inviteCodeFromQrPayload('https://example.com/?invite=LEAK'), isNull);
    expect(inviteCodeFromQrPayload('qingwaguagua://register'), isNull);
  });

  test('controller 在动态编辑时限后隐藏编辑能力并返回明确提示', () async {
    final controller = AppController(DemoImRepository(latency: Duration.zero))
      ..authPolicy = const AuthPolicy(messageRecallMinutes: 1);
    addTearDown(controller.dispose);
    final expired = ChatMessage(
      id: 'message-expired-edit',
      conversationId: 'conversation-1',
      senderId: 'me',
      senderName: '我',
      text: '旧消息',
      sentAt: DateTime.now().subtract(const Duration(minutes: 2)),
      isMine: true,
    );

    expect(controller.isMessageEditable(expired), isFalse);
    expect(await controller.editMessage(expired, '不应提交'), isFalse);
    expect(controller.error, '消息已超过 1 分钟编辑时限');
  });

  test('LiveRepository 以未登录 GET 请求解析 /v2/config/auth', () async {
    late http.Request captured;
    final repository = LiveImRepository(
      apiBaseUrl: 'https://api.example.com',
      client: MockClient((request) async {
        captured = request;
        return http.Response(
          jsonEncode({
            'data': {
              'registrationEnabled': false,
              'passwordMinLength': 14,
              'passwordMaxBytes': 72,
              'messageRecallMinutes': 7,
              'directRecallMinutes': 60,
            },
          }),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        );
      }),
    );
    addTearDown(repository.close);

    final policy = await repository.authPolicy();

    expect(captured.method, 'GET');
    expect(captured.url.path, '/v2/config/auth');
    expect(captured.headers.containsKey('authorization'), isFalse);
    expect(policy.registrationEnabled, isFalse);
    expect(policy.passwordMinLength, 14);
    expect(policy.passwordMaxBytes, 72);
    expect(policy.messageRecallMinutes, 7);
    expect(policy.directRecallMinutes, 60);
  });

  test('LiveRepository 不将缺失的认证策略静默降级为默认真实配置', () async {
    final repository = LiveImRepository(
      apiBaseUrl: 'https://api.example.com',
      client: MockClient(
        (_) async => http.Response(
          '404 page not found',
          404,
          headers: {'content-type': 'text/plain; charset=utf-8'},
        ),
      ),
    );
    addTearDown(repository.close);

    await expectLater(repository.authPolicy(), throwsA(isA<FormatException>()));
  });

  test('controller 初始化动态认证策略且失败时使用兼容规则', () async {
    final dynamicRepository = _PolicyRepository(
      policy: const AuthPolicy(
        registrationEnabled: false,
        passwordMinLength: 12,
        passwordMaxBytes: 72,
      ),
    );
    final dynamicController = AppController(dynamicRepository);
    addTearDown(dynamicController.dispose);
    await dynamicController.initialize();
    await _waitForPolicy(dynamicController);

    expect(dynamicRepository.policyRequestCount, 1);
    expect(dynamicController.authPolicy.registrationEnabled, isFalse);
    expect(dynamicController.authPolicy.passwordMinLength, 12);
    expect(dynamicController.authPolicy.passwordMaxBytes, 72);
    expect(dynamicController.authPolicyLoaded, isTrue);
    expect(dynamicController.authPolicyAvailable, isTrue);
    expect(dynamicController.authPolicyLoadError, isNull);
    expect(dynamicController.error, isNull);
    expect(dynamicController.initializing, isFalse);

    final unavailableRepository = _PolicyRepository(policyUnavailable: true);
    final fallbackController = AppController(unavailableRepository);
    addTearDown(fallbackController.dispose);
    await fallbackController.initialize();
    await _waitForPolicy(fallbackController);

    expect(unavailableRepository.policyRequestCount, 1);
    expect(fallbackController.authPolicy.registrationEnabled, isTrue);
    expect(fallbackController.authPolicy.passwordMinLength, 8);
    expect(fallbackController.authPolicy.passwordMaxBytes, 72);
    expect(fallbackController.authPolicyLoaded, isTrue);
    expect(fallbackController.authPolicyAvailable, isFalse);
    expect(fallbackController.authPolicyLoadError, '认证策略接口暂不可用，已使用兼容规则');
    expect(fallbackController.error, isNull);
    expect(fallbackController.initializing, isFalse);
  });

  test('controller 在本地拒绝关闭注册与超过 bcrypt 72 字节的密码', () async {
    final closedRepository = _PolicyRepository(
      policy: const AuthPolicy(registrationEnabled: false),
    );
    final closedController = AppController(closedRepository);
    addTearDown(closedController.dispose);
    await closedController.initialize();
    await _waitForPolicy(closedController);

    final closedResult = await closedController.registerAccount(
      phone: '13800138000',
      code: '123456',
      password: 'StrongPass123!',
      name: '测试用户',
    );
    expect(closedResult, isFalse);
    expect(closedController.error, '当前暂未开放新账号注册');
    expect(closedRepository.registerCallCount, 0);

    final openRepository = _PolicyRepository();
    final openController = AppController(openRepository);
    addTearDown(openController.dispose);
    await openController.initialize();
    await _waitForPolicy(openController);
    final overBcryptLimit = List.filled(25, '界').join();
    expect(utf8.encode(overBcryptLimit), hasLength(75));

    final overlongResult = await openController.registerAccount(
      phone: '13800138000',
      code: '123456',
      password: overBcryptLimit,
      name: '测试用户',
    );
    expect(overlongResult, isFalse);
    expect(openController.error, '密码过长，请缩短后重试');
    expect(openRepository.registerCallCount, 0);
  });

  testWidgets('登录页根据 registrationEnabled 隐藏注册入口', (tester) async {
    final controller = AppController(
      _PolicyRepository(policy: const AuthPolicy(registrationEnabled: false)),
    );
    addTearDown(controller.dispose);
    await controller.initialize();
    await _waitForPolicy(controller);

    await _pump(tester, LoginScreen(controller: controller));

    expect(find.byKey(const Key('open-register')), findsNothing);
    expect(find.text('当前暂未开放新账号注册'), findsOneWidget);
  });

  testWidgets('认证策略不可用时保留原有注册入口且不打扰用户', (tester) async {
    final repository = _PolicyRepository(policyUnavailable: true);
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.initialize();
    await _waitForPolicy(controller);

    await _pump(tester, LoginScreen(controller: controller));

    expect(find.byKey(const Key('login-button')), findsOneWidget);
    expect(find.byKey(const Key('open-register')), findsOneWidget);
    expect(find.byKey(const Key('auth-policy-status-notice')), findsNothing);
    expect(find.textContaining('注册暂不可用'), findsNothing);
  });

  testWidgets('认证策略不可用时注册与重置页使用兼容规则', (tester) async {
    final repository = _PolicyRepository(policyUnavailable: true);
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.initialize();
    await _waitForPolicy(controller);

    await _pump(tester, RegisterScreen(controller: controller));
    expect(find.byKey(const Key('register-submit')), findsOneWidget);
    expect(find.textContaining('至少 8'), findsOneWidget);

    await _pump(tester, ResetPasswordScreen(controller: controller));
    expect(find.byKey(const Key('reset-submit')), findsOneWidget);
    expect(find.textContaining('至少 8'), findsOneWidget);

    repository.registerFailure = true;
    final registerResult = await controller.registerAccount(
      phone: '13800138000',
      code: '123456',
      password: 'StrongPass123!',
      name: '测试用户',
    );
    expect(registerResult, isFalse);
    expect(repository.registerCallCount, 1);
    expect(controller.error, '测试注册接口失败');

    final resetResult = await controller.resetPassword(
      phone: '13800138000',
      code: '123456',
      password: 'StrongPass123!',
    );
    expect(resetResult, isTrue);
  });

  testWidgets('注册页防止绕过登录页直达提交', (tester) async {
    final repository = _PolicyRepository(
      policy: const AuthPolicy(registrationEnabled: false),
    );
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.initialize();
    await _waitForPolicy(controller);

    await _pump(tester, RegisterScreen(controller: controller));

    expect(find.text('当前暂未开放新账号注册'), findsOneWidget);
    expect(
      find.byWidgetPredicate(
        (widget) =>
            widget is FilledButton &&
            widget.key == const Key('register-submit') &&
            widget.onPressed != null,
      ),
      findsNothing,
    );
    expect(repository.registerCallCount, 0);
  });

  testWidgets('注册页按邀请码策略显示必填、选填或隐藏', (tester) async {
    for (final mode in ['required', 'optional', 'disabled']) {
      final controller = AppController(
        _PolicyRepository(policy: AuthPolicy(inviteRegistrationMode: mode)),
      );
      addTearDown(controller.dispose);
      await controller.initialize();
      await _waitForPolicy(controller);
      await _pump(tester, RegisterScreen(controller: controller));

      final field = find.byKey(const Key('register-invite-code'));
      if (mode == 'disabled') {
        expect(field, findsNothing);
        continue;
      }
      expect(field, findsOneWidget);
      expect(find.text(mode == 'required' ? '邀请码' : '邀请码（选填）'), findsOneWidget);
    }
  });

  testWidgets('验证码登录可校验邀请码且不泄露邀请人身份', (tester) async {
    final controller = AppController(_PolicyRepository());
    addTearDown(controller.dispose);
    await controller.initialize();
    await _waitForPolicy(controller);
    await _pump(tester, LoginScreen(controller: controller));

    await tester.enterText(
      find.byKey(const Key('login-invite-code')),
      'TESTCODE',
    );
    await tester.tap(find.byTooltip('校验邀请码'));
    await tester.pumpAndSettle();
    expect(find.text('邀请码有效'), findsOneWidget);
    expect(find.textContaining('邀请人'), findsNothing);
  });

  test('必填模式在客户端提交前阻止空邀请码', () async {
    final repository = _PolicyRepository(
      policy: const AuthPolicy(inviteRegistrationMode: 'required'),
    );
    final controller = AppController(repository);
    addTearDown(controller.dispose);
    await controller.initialize();
    await _waitForPolicy(controller);

    final result = await controller.registerAccount(
      phone: '13800138000',
      code: '123456',
      password: 'StrongPass123!',
      name: '测试用户',
    );
    expect(result, isFalse);
    expect(controller.error, '创建新账号需要填写邀请码');
    expect(repository.registerCallCount, 0);
  });

  testWidgets('注册和重置页展示动态最小长度并执行 UTF-8 字节上限', (tester) async {
    final controller = AppController(
      _PolicyRepository(policy: const AuthPolicy(passwordMinLength: 12)),
    );
    addTearDown(controller.dispose);
    await controller.initialize();
    await _waitForPolicy(controller);

    await _pump(tester, RegisterScreen(controller: controller));
    expect(find.textContaining('至少 12'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.byKey(const Key('register-password')),
      160,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.enterText(
      find.byKey(const Key('register-password')),
      'ElevenChars',
    );
    await tester.tap(find.byKey(const Key('register-confirm-password')));
    await tester.pump();
    expect(find.textContaining('密码至少 12'), findsOneWidget);

    await tester.enterText(
      find.byKey(const Key('register-password')),
      List.filled(25, '界').join(),
    );
    await tester.tap(find.byKey(const Key('register-confirm-password')));
    await tester.pump();
    expect(find.text('密码过长，请缩短后重试'), findsOneWidget);

    await _pump(tester, ResetPasswordScreen(controller: controller));
    expect(find.textContaining('至少 12'), findsOneWidget);
    await tester.enterText(
      find.byKey(const Key('reset-password')),
      'ElevenChars',
    );
    await tester.tap(find.byKey(const Key('reset-submit')));
    await tester.pump();
    expect(find.textContaining('密码至少 12'), findsOneWidget);
  });
}

class _PolicyRepository extends DemoImRepository {
  _PolicyRepository({
    this.policy = const AuthPolicy(),
    this.policyUnavailable = false,
  }) : super(latency: Duration.zero);

  final AuthPolicy policy;
  bool policyUnavailable;
  bool registerFailure = false;
  int policyRequestCount = 0;
  int registerCallCount = 0;

  @override
  Future<AuthPolicy> authPolicy() async {
    policyRequestCount += 1;
    if (policyUnavailable) throw Exception('auth policy unavailable');
    return policy;
  }

  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
    String inviteCode = '',
  }) async {
    registerCallCount += 1;
    if (registerFailure) throw const FormatException('测试注册接口失败');
    return super.register(
      phone: phone,
      code: code,
      password: password,
      name: name,
      inviteCode: inviteCode,
    );
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
  for (var i = 0; i < 5; i++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}

Future<void> _waitForPolicy(AppController controller) async {
  for (
    var attempt = 0;
    attempt < 20 && !controller.authPolicyLoaded;
    attempt++
  ) {
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
  expect(controller.authPolicyLoaded, isTrue);
}
