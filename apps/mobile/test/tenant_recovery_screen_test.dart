import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/app_theme.dart';
import 'package:linli_im/core/auth_validation.dart';
import 'package:linli_im/core/tenant_password.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/ui/screens/login_screen.dart';

import 'tenant_password_screen_test.dart' show PasswordFixture;

class RecoveryFixture extends PasswordFixture {
  bool enabled = true, smsFails = false, resetFails = false;
  int smsCalls = 0;
  Completer<void>? resetGate;
  TenantPasswordProgress result = TenantPasswordProgress.pending;
  @override
  Future<AuthPolicy> authPolicy() async => AuthPolicy(
    otpLoginEnabled: false,
    registrationEnabled: false,
    qrLoginEnabled: false,
    passwordResetEnabled: enabled,
  );
  @override
  Future<void> requestPasswordResetCode(String phone) async {
    smsCalls++;
    if (smsFails) {
      throw const ImApiException(
        statusCode: 503,
        code: 'PLATFORM_UNAVAILABLE',
        message: '短信服务暂不可用',
      );
    }
  }

  @override
  Future<void> resetPassword({
    required String phone,
    required String code,
    required String password,
  }) async {
    submits++;
    if (resetGate != null) await resetGate!.future;
    if (resetFails) {
      throw const ImApiException(
        statusCode: 401,
        code: 'PASSWORD_RECOVERY_REJECTED',
        message: '验证码不可用',
      );
    }
    passwordChangeProgress = result;
  }
}

Future<AppController> recoveryForm(
  WidgetTester tester,
  RecoveryFixture repository, {
  double width = 360,
  double scale = 1,
}) async {
  tester.view.physicalSize = Size(width, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final controller = AppController(repository);
  addTearDown(controller.dispose);
  await controller.refreshAuthPolicy();
  final navigator = GlobalKey<NavigatorState>();
  await tester.pumpWidget(
    MaterialApp(
      navigatorKey: navigator,
      theme: buildLinliTheme(Brightness.dark),
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: TextScaler.linear(scale)),
        child: child!,
      ),
      home: LoginScreen(controller: controller),
    ),
  );
  await tester.pumpAndSettle();
  unawaited(
    navigator.currentState!.push(
      MaterialPageRoute<void>(
        builder: (_) => ResetPasswordScreen(
          controller: controller,
          initialPhone: '19900000001',
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return controller;
}

Future<void> requestCode(WidgetTester tester) async {
  final request = find.text('获取验证码');
  await tester.ensureVisible(request);
  await tester.tap(request);
  await tester.pumpAndSettle();
}

Future<void> fillRecovery(
  WidgetTester tester, {
  String repeat = 'NewPassword123!',
}) async {
  await tester.enterText(find.byKey(const Key('auth-code')), '123456');
  await tester.enterText(
    find.byKey(const Key('reset-password')),
    'NewPassword123!',
  );
  await tester.enterText(
    find.byKey(const Key('tenant-recovery-password-confirmation')),
    repeat,
  );
  await tester.ensureVisible(find.byKey(const Key('reset-submit')));
  await tester.tap(find.byKey(const Key('reset-submit')));
  await tester.pumpAndSettle();
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));
  for (final width in [360.0, 1280.0]) {
    testWidgets(
      'SMS recovery $width dark 200% layout, accurate expiry and confirmation',
      (tester) async {
        final source = RecoveryFixture();
        await recoveryForm(tester, source, width: width, scale: 2);
        await requestCode(tester);
        expect(find.text('验证码已发送，10 分钟内有效'), findsOneWidget);
        expect(find.text('验证码已发送，5 分钟内有效'), findsNothing);
        await fillRecovery(tester);
        expect(
          find.byKey(const Key('tenant-recovery-confirm')),
          findsOneWidget,
        );
        expect(source.submits, 0);
        await tester.tap(find.text('取消'));
        await tester.pumpAndSettle();
        expect(source.submits, 0);
        expect(tester.takeException(), isNull);
      },
    );
  }
  for (final result in [
    TenantPasswordProgress.pending,
    TenantPasswordProgress.unconfirmed,
  ]) {
    testWidgets(
      '$result returns to login task notice, never claims reset completed',
      (tester) async {
        final source = RecoveryFixture()..result = result;
        final controller = await recoveryForm(tester, source);
        await requestCode(tester);
        await fillRecovery(tester);
        await tester.tap(find.byKey(const Key('tenant-recovery-confirm')));
        await tester.pumpAndSettle();
        expect(find.byType(ResetPasswordScreen), findsNothing);
        expect(find.byKey(const Key('tenant-password-notice')), findsOneWidget);
        expect(find.text('密码已修改'), findsNothing);
        expect(find.text('密码已重置，请使用新密码登录'), findsNothing);
        expect(controller.authenticated, isFalse);
        expect(source.queries, 0);
        expect(source.submits, 1);
      },
    );
  }
  testWidgets('disabled provider blocks SMS and submit', (tester) async {
    final source = RecoveryFixture()..enabled = false;
    await recoveryForm(tester, source);
    expect(
      tester
          .widget<FilledButton>(find.byKey(const Key('reset-submit')))
          .onPressed,
      isNull,
    );
    expect(
      tester
          .widget<TextButton>(find.widgetWithText(TextButton, '获取验证码'))
          .onPressed,
      isNull,
    );
    expect(find.text('平台尚未启用短信找回，请联系企业管理员重置。'), findsOneWidget);
    expect(source.smsCalls, 0);
  });
  testWidgets(
    'mismatching password and changed phone require correction before submit',
    (tester) async {
      final source = RecoveryFixture();
      await recoveryForm(tester, source);
      await requestCode(tester);
      await fillRecovery(tester, repeat: 'WrongRepeat123!');
      expect(find.text('两次输入的密码不一致'), findsOneWidget);
      expect(source.submits, 0);
      final phone = find.byWidgetPredicate(
        (w) => w is TextField && w.decoration?.labelText == '手机号',
      );
      await tester.enterText(phone, '19900000002');
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<FilledButton>(find.byKey(const Key('reset-submit')))
            .onPressed,
        isNull,
      );
    },
  );
  testWidgets(
    'failed SMS and rejected verification preserve password draft, no success notice',
    (tester) async {
      final source = RecoveryFixture()..resetFails = true;
      await recoveryForm(tester, source);
      await requestCode(tester);
      await fillRecovery(tester);
      await tester.tap(find.byKey(const Key('tenant-recovery-confirm')));
      await tester.pumpAndSettle();
      expect(find.text('验证码不可用'), findsOneWidget);
      expect(
        tester
            .widget<TextFormField>(find.byKey(const Key('reset-password')))
            .controller!
            .text,
        'NewPassword123!',
      );
      source.smsFails = true;
      await tester.ensureVisible(find.text('重新获取'));
      await tester.tap(find.text('重新获取'));
      await tester.pumpAndSettle();
      expect(find.text('短信服务暂不可用'), findsOneWidget);
      expect(find.text('验证码已发送，10 分钟内有效'), findsNothing);
      expect(
        tester
            .widget<FilledButton>(find.byKey(const Key('reset-submit')))
            .onPressed,
        isNull,
      );
    },
  );
  testWidgets(
    'in-flight reset cannot submit twice or navigate back prematurely',
    (tester) async {
      final source = RecoveryFixture()..resetGate = Completer<void>();
      await recoveryForm(tester, source);
      await requestCode(tester);
      await fillRecovery(tester);
      await tester.tap(find.byKey(const Key('tenant-recovery-confirm')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));
      expect(source.submits, 1);
      expect(
        tester
            .widget<FilledButton>(find.byKey(const Key('reset-submit')))
            .onPressed,
        isNull,
      );
      final context = tester.element(find.byType(ResetPasswordScreen));
      await Navigator.of(context).maybePop();
      await tester.pump();
      expect(find.byType(ResetPasswordScreen), findsOneWidget);
      source.resetGate!.complete();
      await tester.pumpAndSettle();
      expect(source.submits, 1);
      expect(find.byType(ResetPasswordScreen), findsNothing);
    },
  );
}
