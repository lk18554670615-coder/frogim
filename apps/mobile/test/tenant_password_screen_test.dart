import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/core/tenant_password.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/tenant_auth_repository.dart';
import 'package:linli_im/ui/screens/login_screen.dart';
import 'package:linli_im/ui/screens/settings_screens.dart';
import 'package:linli_im/ui/screens/tenant_password_screen.dart';

class PasswordFixture extends DemoImRepository
    implements TenantPasswordRepository, TenantAuthenticationRepository {
  PasswordFixture() : super(latency: Duration.zero);
  @override
  bool supportsPasswordChange = true;
  @override
  TenantPasswordProgress? passwordChangeProgress;
  bool reject = false, unavailable = false;
  int submits = 0, queries = 0, logouts = 0;
  Completer<void>? gate;
  @override
  Future<AuthPolicy> authPolicy() async {
    if (unavailable) {
      supportsPasswordChange = false;
      throw StateError('unavailable');
    }
    return const AuthPolicy(
      otpLoginEnabled: false,
      registrationEnabled: false,
      qrLoginEnabled: false,
      passwordResetEnabled: false,
    );
  }

  @override
  Future<void> changeLoginPassword(String current, String next) async {
    submits++;
    if (reject) {
      throw const ImApiException(
        statusCode: 401,
        code: 'INVALID_CREDENTIALS',
        message: '当前密码不正确',
      );
    }
    if (gate != null) await gate!.future;
    passwordChangeProgress = TenantPasswordProgress.pending;
  }

  @override
  Future<void> checkPasswordChange() async {
    queries++;
    passwordChangeProgress = TenantPasswordProgress.completed;
  }

  @override
  Future<void> dismissPasswordChange() async {
    passwordChangeProgress = null;
  }

  @override
  Future<void> logout() async {
    logouts++;
  }

  @override
  bool get hasPendingRegistration => false;
  @override
  Future<void> dismissPendingRegistration() async {}
  @override
  Future<AppUser> resumeRegistration() => throw StateError('not used');
  @override
  Future<AppUser> tenantLogin(
    String phone,
    String code, {
    String enterpriseCode = '',
    String inviteCode = '',
  }) => throw StateError('not used');
  @override
  Future<AppUser> tenantRegister({
    required String phone,
    required String code,
    required String password,
    required String name,
    String enterpriseCode = '',
    String inviteCode = '',
  }) => throw StateError('not used');
}

Future<AppController> form(
  WidgetTester tester,
  PasswordFixture repository, {
  bool dark = false,
  double width = 360,
  double scale = 1,
}) async {
  tester.view.physicalSize = Size(width, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final controller = AppController(repository)..authenticated = true;
  controller.currentUser = const AppUser(
    id: 'u1',
    name: '测试用户',
    phone: '19900000001',
    handle: 'test',
    presence: 'hidden',
  );
  addTearDown(controller.dispose);
  await tester.pumpWidget(
    MaterialApp(
      theme: buildLinliTheme(dark ? Brightness.dark : Brightness.light),
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: TextScaler.linear(scale)),
        child: child!,
      ),
      home: ChangePasswordScreen(controller: controller),
    ),
  );
  await tester.pumpAndSettle();
  return controller;
}

Future<void> fill(WidgetTester tester) async {
  await tester.enterText(
    find.byKey(const Key('tenant-password-current')),
    'OriginalPassword123!',
  );
  await tester.enterText(
    find.byKey(const Key('tenant-password-new')),
    'NextPassword123!',
  );
  await tester.enterText(
    find.byKey(const Key('tenant-password-confirmation')),
    'NextPassword123!',
  );
  await tester.ensureVisible(find.byKey(const Key('tenant-password-submit')));
  await tester.tap(find.byKey(const Key('tenant-password-submit')));
  await tester.pumpAndSettle();
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));
  for (final width in [360.0, 1280.0]) {
    testWidgets(
      'managed form replaces SMS reset at $width, supports dark large text',
      (tester) async {
        await form(
          tester,
          PasswordFixture(),
          width: width,
          dark: true,
          scale: 2,
        );
        expect(find.byType(TenantChangePasswordScreen), findsOneWidget);
        expect(find.byKey(const Key('password-change-code')), findsNothing);
        expect(
          find.byKey(const Key('tenant-password-current')),
          findsOneWidget,
        );
        await tester.ensureVisible(
          find.byKey(const Key('tenant-password-submit')),
        );
        expect(tester.takeException(), isNull);
      },
    );
  }
  testWidgets(
    'requires confirmation; cancel sends nothing; rejection preserves draft',
    (tester) async {
      final source = PasswordFixture()..reject = true;
      final controller = await form(tester, source);
      await fill(tester);
      expect(source.submits, 0);
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(source.submits, 0);
      await tester.tap(find.byKey(const Key('tenant-password-submit')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('tenant-password-confirm')));
      await tester.pumpAndSettle();
      expect(source.submits, 1);
      expect(source.logouts, 0);
      expect(controller.authenticated, isTrue);
      expect(find.text('当前密码不正确'), findsOneWidget);
      expect(
        tester
            .widget<TextFormField>(find.byKey(const Key('tenant-password-new')))
            .controller!
            .text,
        'NextPassword123!',
      );
    },
  );
  testWidgets(
    'pending request disables resubmit and accepted task leaves authenticated shell',
    (tester) async {
      final source = PasswordFixture()..gate = Completer<void>();
      final controller = await form(tester, source);
      await fill(tester);
      await tester.tap(find.byKey(const Key('tenant-password-confirm')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));
      expect(source.submits, 1);
      expect(
        tester
            .widget<FilledButton>(
              find.byKey(const Key('tenant-password-submit')),
            )
            .onPressed,
        isNull,
      );
      source.gate!.complete();
      await tester.pumpAndSettle();
      expect(source.logouts, 1);
      expect(controller.authenticated, isFalse);
      expect(controller.tenantPasswordProgress, TenantPasswordProgress.pending);
      expect(controller.loading, isFalse);
      expect(tester.takeException(), isNull);
    },
  );
  testWidgets(
    'unknown task appears on login; manual query completes without login; dismissal confirms',
    (tester) async {
      final source = PasswordFixture()
        ..passwordChangeProgress = TenantPasswordProgress.unconfirmed;
      final controller = AppController(source);
      addTearDown(controller.dispose);
      await controller.refreshAuthPolicy();
      await tester.pumpWidget(
        MaterialApp(
          theme: buildLinliTheme(Brightness.light),
          home: LoginScreen(controller: controller),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('密码修改结果未确认'), findsOneWidget);
      expect(source.queries, 0);
      await tester.ensureVisible(
        find.byKey(const Key('tenant-password-check')),
      );
      await tester.tap(find.byKey(const Key('tenant-password-check')));
      await tester.pumpAndSettle();
      expect(find.text('密码已修改'), findsOneWidget);
      expect(source.queries, 1);
      expect(controller.authenticated, isFalse);
      await tester.ensureVisible(
        find.byKey(const Key('tenant-password-dismiss')),
      );
      await tester.tap(find.byKey(const Key('tenant-password-dismiss')));
      await tester.pumpAndSettle();
      expect(source.passwordChangeProgress, isNotNull);
      await tester.tap(
        find.byKey(const Key('tenant-password-dismiss-confirm')),
      );
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('tenant-password-notice')), findsNothing);
    },
  );
  testWidgets('unavailable policy never exposes enabled submit', (
    tester,
  ) async {
    await form(tester, PasswordFixture()..unavailable = true);
    expect(
      tester
          .widget<FilledButton>(find.byKey(const Key('tenant-password-submit')))
          .onPressed,
      isNull,
    );
    expect(find.text('刷新认证配置'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
