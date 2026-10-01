import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_config.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/login_screen.dart';

class _Repository extends DemoImRepository {
  _Repository(this.policy) : super(latency: Duration.zero);
  final AuthPolicy policy;
  int registrations = 0, validations = 0;
  bool validCode = false;
  String? loginInvite;
  @override
  Future<AuthPolicy> authPolicy() async => policy;
  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
    String inviteCode = '',
  }) async {
    registrations++;
    throw const FormatException('测试注册请求已到达');
  }

  @override
  Future<bool> validateInviteCode(String value) async {
    validations++;
    return validCode;
  }

  @override
  Future<AppUser> login(
    String phone,
    String code, {
    String inviteCode = '',
  }) async {
    loginInvite = inviteCode;
    throw const FormatException('测试登录请求已到达');
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));
  testWidgets(
    'configured platform hides personal invitation before policy load',
    (tester) async {
      final c = AppController(_Repository(const AuthPolicy()));
      addTearDown(c.dispose);
      expect(c.authPolicy.platformMode, isFalse);
      expect(c.platformAuthentication, isTrue);
      await tester.pumpWidget(MaterialApp(home: LoginScreen(controller: c)));
      await tester.pump();
      expect(find.byKey(const Key('login-invite-code')), findsNothing);
      expect(find.byTooltip('扫描邀请码'), findsNothing);
    },
    skip: AppConfig.platformBaseUrl.isEmpty,
  );
  test(
    'platform policy parses existing mode without changing personal invitation policy',
    () {
      final p = AuthPolicy.fromJson({
        'platformMode': true,
        'inviteRegistrationMode': 'required',
      });
      expect(p.platformMode, isTrue);
      expect(p.invitationRequired, isTrue);
      expect(AuthPolicy.fromJson({}).platformMode, isFalse);
    },
  );
  test(
    'platform controller permits empty enterprise code and strips login invitation',
    () async {
      final repo = _Repository(
        const AuthPolicy(
          platformMode: true,
          inviteRegistrationMode: 'required',
        ),
      );
      final c = AppController(repo);
      addTearDown(c.dispose);
      await c.refreshAuthPolicy();
      await c.registerAccount(
        phone: '13800000008',
        code: '123456',
        password: 'LocalUser123!',
        name: '测试',
      );
      expect(repo.registrations, 1);
      await c.login('13800000008', '123456', inviteCode: 'PERSONAL');
      expect(repo.loginInvite, '');
    },
  );
  for (final width in [390.0, 1280.0]) {
    testWidgets(
      'platform login and optional enterprise registration at width $width',
      (tester) async {
        final c = AppController(
          _Repository(
            const AuthPolicy(
              platformMode: true,
              inviteRegistrationMode: 'required',
            ),
          ),
        );
        addTearDown(c.dispose);
        await c.refreshAuthPolicy();
        tester.view.physicalSize = Size(width, 900);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        await tester.pumpWidget(
          MaterialApp(
            theme: buildLinliTheme(Brightness.light),
            home: LoginScreen(controller: c),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.byKey(const Key('login-invite-code')), findsNothing);
        expect(find.byTooltip('扫描邀请码'), findsNothing);
        await tester.tap(find.text(width < 1024 ? '密码登录' : '密码').hitTestable());
        await tester.pumpAndSettle();
        expect(find.byKey(const Key('login-invite-code')), findsNothing);
        await tester.pumpWidget(
          MaterialApp(
            theme: buildLinliTheme(Brightness.light),
            home: RegisterScreen(controller: c),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text('企业码（选填）'), findsOneWidget);
        expect(find.text('填写后加入对应企业，留空加入默认企业'), findsOneWidget);
        expect(find.byKey(const Key('register-invite-code')), findsNothing);
        expect(find.byTooltip('校验邀请码'), findsNothing);
        expect(find.byTooltip('扫描邀请码'), findsNothing);
      },
    );
  }
  testWidgets(
    'enterprise input remains available when personal invitations disabled and validates on submit',
    (tester) async {
      final repo = _Repository(
        const AuthPolicy(
          platformMode: true,
          inviteRegistrationMode: 'disabled',
        ),
      );
      final c = AppController(repo);
      addTearDown(c.dispose);
      await c.refreshAuthPolicy();
      tester.view.physicalSize = const Size(390, 1100);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        MaterialApp(
          theme: buildLinliTheme(Brightness.light),
          home: RegisterScreen(controller: c),
        ),
      );
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextFormField).first, '13800000008');
      await tester.enterText(find.byKey(const Key('auth-code')), '123456');
      await tester.enterText(find.byKey(const Key('register-name')), '测试');
      await tester.enterText(
        find.byKey(const Key('register-enterprise-code')),
        'BAD',
      );
      await tester.enterText(
        find.byKey(const Key('register-password')),
        'LocalUser123!',
      );
      await tester.enterText(
        find.byKey(const Key('register-confirm-password')),
        'LocalUser123!',
      );
      await tester.tap(find.byKey(const Key('policy-consent-checkbox')));
      await tester.pump();
      await tester.ensureVisible(find.byKey(const Key('register-submit')));
      await tester.tap(find.byKey(const Key('register-submit')));
      await tester.pumpAndSettle();
      expect(repo.validations, 1);
      expect(repo.registrations, 0);
      expect(find.text('请确认企业码及企业是否允许注册'), findsOneWidget);
    },
  );
}
