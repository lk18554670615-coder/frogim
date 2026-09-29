import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/tenant_auth_repository.dart';
import 'package:linli_im/ui/screens/login_screen.dart';

class _TenantAuthFixture extends DemoImRepository
    implements TenantAuthenticationRepository {
  _TenantAuthFixture() : super(latency: Duration.zero);
  String? submittedEnterprise, submittedPersonal;
  bool unavailable = false;
  int checks = 0;
  @override
  bool hasPendingRegistration = false;
  @override
  Future<AuthPolicy> authPolicy() async {
    if (unavailable) throw StateError('unavailable');
    return const AuthPolicy(
      otpLoginEnabled: true,
      qrLoginEnabled: false,
      passwordResetEnabled: false,
    );
  }

  @override
  Future<void> dismissPendingRegistration() async {
    hasPendingRegistration = false;
  }

  @override
  Future<AppUser> resumeRegistration() async {
    checks++;
    throw const ImApiException(
      statusCode: 202,
      code: 'REGISTRATION_PENDING',
      message: '仍在开通',
    );
  }

  @override
  Future<AppUser> tenantLogin(
    String phone,
    String code, {
    String enterpriseCode = '',
    String inviteCode = '',
  }) async {
    submittedEnterprise = enterpriseCode;
    submittedPersonal = inviteCode;
    throw const ImApiException(
      statusCode: 401,
      code: 'INVALID_CREDENTIALS',
      message: '测试登录失败',
    );
  }

  @override
  Future<AppUser> tenantRegister({
    required String phone,
    required String code,
    required String password,
    required String name,
    String enterpriseCode = '',
    String inviteCode = '',
  }) async {
    submittedEnterprise = enterpriseCode;
    submittedPersonal = inviteCode;
    hasPendingRegistration = true;
    throw const ImApiException(
      statusCode: 202,
      code: 'REGISTRATION_PENDING',
      message: '企业账号正在开通',
    );
  }

  @override
  Future<bool> validateInviteCode(String code) => throw StateError(
    'must not call legacy tenant validator before assignment',
  );
}

Future<void> pumpPage(
  WidgetTester tester,
  AppController controller, {
  bool register = false,
}) async {
  tester.view.physicalSize = const Size(360, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(Brightness.light),
      home: register
          ? RegisterScreen(controller: controller)
          : LoginScreen(controller: controller),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets(
    'managed policy failure exposes password only, never legacy registration/QR/reset',
    (tester) async {
      final repository = _TenantAuthFixture()..unavailable = true;
      final controller = AppController(repository);
      addTearDown(controller.dispose);
      await controller.refreshAuthPolicy();
      await pumpPage(tester, controller);
      expect(find.byKey(const Key('password-field')), findsOneWidget);
      expect(find.byKey(const Key('code-field')), findsNothing);
      expect(find.byKey(const Key('open-register')), findsNothing);
      expect(find.byKey(const Key('forgot-password')), findsNothing);
      expect(find.byKey(const Key('login-mode-control')), findsNothing);
      expect(controller.authenticated, isFalse);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'OTP login sends distinct enterprise and personal invite codes on narrow screens',
    (tester) async {
      final repository = _TenantAuthFixture();
      final controller = AppController(repository);
      addTearDown(controller.dispose);
      await controller.refreshAuthPolicy();
      await pumpPage(tester, controller);
      await tester.enterText(find.byType(TextFormField).first, '19900000001');
      await tester.enterText(find.byKey(const Key('code-field')), '987654');
      await tester.enterText(
        find.byKey(const Key('enterprise-code')),
        ' ENTERPRISE ',
      );
      await tester.enterText(
        find.byKey(const Key('login-invite-code')),
        ' PERSONAL ',
      );
      expect(find.byTooltip('校验邀请码'), findsNothing);
      await tester.ensureVisible(
        find.byKey(const Key('policy-consent-checkbox')),
      );
      await tester.tap(find.byKey(const Key('policy-consent-checkbox')));
      await tester.ensureVisible(find.byKey(const Key('login-button')));
      await tester.tap(find.byKey(const Key('login-button')));
      await tester.pumpAndSettle();
      expect(repository.submittedEnterprise, 'ENTERPRISE');
      expect(repository.submittedPersonal, 'PERSONAL');
      expect(controller.authenticated, isFalse);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'registration submits to target policy and pending UI resumes without a new code',
    (tester) async {
      final repository = _TenantAuthFixture();
      final controller = AppController(repository);
      addTearDown(controller.dispose);
      await controller.refreshAuthPolicy();
      await pumpPage(tester, controller, register: true);
      await tester.enterText(find.byType(TextFormField).first, '19900000001');
      await tester.enterText(find.byKey(const Key('auth-code')), '987654');
      await tester.enterText(find.byKey(const Key('register-name')), '新成员');
      await tester.ensureVisible(find.byKey(const Key('enterprise-code')));
      await tester.enterText(
        find.byKey(const Key('enterprise-code')),
        'ENTERPRISE',
      );
      await tester.enterText(
        find.byKey(const Key('register-invite-code')),
        'PERSONAL',
      );
      await tester.ensureVisible(find.byKey(const Key('register-password')));
      await tester.enterText(
        find.byKey(const Key('register-password')),
        'password123',
      );
      await tester.enterText(
        find.byKey(const Key('register-confirm-password')),
        'password123',
      );
      await tester.ensureVisible(
        find.byKey(const Key('policy-consent-checkbox')),
      );
      await tester.tap(find.byKey(const Key('policy-consent-checkbox')));
      await tester.ensureVisible(find.byKey(const Key('register-submit')));
      await tester.tap(find.byKey(const Key('register-submit')));
      await tester.pumpAndSettle();
      expect(repository.submittedEnterprise, 'ENTERPRISE');
      expect(repository.submittedPersonal, 'PERSONAL');
      expect(controller.authenticated, isFalse);
      await tester.ensureVisible(find.byKey(const Key('resume-registration')));
      await tester.tap(find.byKey(const Key('resume-registration')));
      await tester.pumpAndSettle();
      expect(repository.checks, 1);
      expect(controller.authenticated, isFalse);
      expect(tester.takeException(), isNull);
    },
  );
}
