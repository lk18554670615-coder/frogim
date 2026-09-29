import 'dart:async';
import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/tenant_password.dart';
import 'package:linli_im/data/live_repository.dart';

import 'platform_repository_test.dart' show Fixture, jsonResponse;
import 'tenant_password_test.dart' show FailingStore;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
  });
  test(
    'SMS recovery purpose and capability stay at platform; no secrets persisted',
    () async {
      final f = Fixture()..recoveryEnabled = true;
      final repository = f.repository();
      addTearDown(repository.close);
      expect((await repository.authPolicy()).passwordResetEnabled, isTrue);
      late String request, token;
      f.authOverride = (r) async {
        expect(r.url.host, 'platform.example');
        final body = jsonDecode(r.body) as Map;
        if (r.url.path == '/v2/auth/password-reset/code') {
          request = body['requestId'] as String;
          token = body['queryToken'] as String;
          expect(base64Url.decode(base64Url.normalize(token)).length, 32);
          expect(r.headers['authorization'], isNull);
          expect(body['phone'], '19900000001');
          return jsonResponse({'ok': true, 'requestId': request});
        }
        expect(r.url.path, '/v2/auth/password-reset');
        expect(r.headers['authorization'], 'Bearer $token');
        expect(body, {
          'requestId': request,
          'code': '123987',
          'newPassword': 'RecoveredPassword123!',
          'confirmed': true,
        });
        final persisted = await f.metadata.readJson('password-task') as Map;
        expect(persisted['recovery'], isTrue);
        expect(persisted.containsKey('code'), isFalse);
        expect(persisted.containsKey('password'), isFalse);
        return jsonResponse({
          'jobId': 'recovery_job',
          'requestId': request,
          'status': 'pending',
        }, 202);
      };
      await repository.requestPasswordResetCode('19900000001');
      expect(await f.metadata.readJson('password-task'), isNull);
      await repository.resetPassword(
        phone: '19900000001',
        code: '123987',
        password: 'RecoveredPassword123!',
      );
      expect(repository.passwordChangeProgress, TenantPasswordProgress.pending);
      expect(repository.live, isNull);
      final prefs = await SharedPreferences.getInstance();
      for (final k in prefs.getKeys()) {
        expect(prefs.get(k).toString(), isNot(contains(token)));
        expect(
          prefs.get(k).toString(),
          isNot(contains('RecoveredPassword123!')),
        );
      }
    },
  );
  test(
    'uncertain reset restores query route, never repeats SMS or OTP',
    () async {
      final f = Fixture()..recoveryEnabled = true;
      final repository = f.repository();
      await repository.authPolicy();
      late String id;
      f.authOverride = (r) async {
        if (r.url.path.endsWith('/code')) {
          id = (jsonDecode(r.body) as Map)['requestId'] as String;
          return jsonResponse({'ok': true, 'requestId': id});
        }
        throw TimeoutException('lost acceptance');
      };
      await repository.requestPasswordResetCode('19900000001');
      await repository.resetPassword(
        phone: '19900000001',
        code: '123987',
        password: 'RecoveredPassword123!',
      );
      expect(
        repository.passwordChangeProgress,
        TenantPasswordProgress.unconfirmed,
      );
      await repository.close();
      f.requests.clear();
      final restored = f.repository();
      addTearDown(restored.close);
      expect(await restored.restoreSession(), isFalse);
      expect(f.requests, isEmpty);
      f.authOverride = (r) async {
        expect(r.url.path, '/v2/auth/password-reset/status');
        expect(jsonDecode(r.body), {'requestId': id});
        return jsonResponse({
          'requestId': id,
          'jobId': 'recovery_job',
          'status': 'completed',
        });
      };
      await restored.checkPasswordChange();
      expect(restored.passwordChangeProgress, TenantPasswordProgress.completed);
      expect(f.requests.length, 1);
    },
  );
  test(
    'new phone, missing challenge and malformed code cannot submit',
    () async {
      final f = Fixture()..recoveryEnabled = true;
      final repository = f.repository();
      addTearDown(repository.close);
      await repository.authPolicy();
      await expectLater(
        repository.resetPassword(
          phone: '19900000001',
          code: '123987',
          password: 'RecoveredPassword123!',
        ),
        throwsStateError,
      );
      f.authOverride = (r) async => jsonResponse({
        'ok': true,
        'requestId': (jsonDecode(r.body) as Map)['requestId'],
      });
      await repository.requestPasswordResetCode('19900000001');
      final count = f.requests.length;
      await expectLater(
        repository.resetPassword(
          phone: '19900000002',
          code: '123987',
          password: 'RecoveredPassword123!',
        ),
        throwsStateError,
      );
      await expectLater(
        repository.resetPassword(
          phone: '19900000001',
          code: 'abc',
          password: 'RecoveredPassword123!',
        ),
        throwsStateError,
      );
      expect(f.requests.length, count);
    },
  );
  test('definite OTP rejection keeps challenge and no pending task', () async {
    final f = Fixture()..recoveryEnabled = true;
    final repository = f.repository();
    addTearDown(repository.close);
    await repository.authPolicy();
    f.authOverride = (r) async {
      if (r.url.path.endsWith('/code')) {
        return jsonResponse({
          'ok': true,
          'requestId': (jsonDecode(r.body) as Map)['requestId'],
        });
      }
      return jsonResponse({
        'error': {'code': 'PASSWORD_RECOVERY_REJECTED'},
      }, 401);
    };
    await repository.requestPasswordResetCode('19900000001');
    await expectLater(
      repository.resetPassword(
        phone: '19900000001',
        code: '123987',
        password: 'RecoveredPassword123!',
      ),
      throwsA(isA<ImApiException>()),
    );
    expect(repository.passwordChangeProgress, isNull);
    expect(await f.metadata.readJson('password-task'), isNull);
  });
  test('query store failure stops reset before side effect', () async {
    final store = FailingStore();
    final f = Fixture(metadata: store)..recoveryEnabled = true;
    final repository = f.repository();
    addTearDown(repository.close);
    await repository.authPolicy();
    f.authOverride = (r) async => jsonResponse({
      'ok': true,
      'requestId': (jsonDecode(r.body) as Map)['requestId'],
    });
    await repository.requestPasswordResetCode('19900000001');
    f.requests.clear();
    store.reject = true;
    await expectLater(
      repository.resetPassword(
        phone: '19900000001',
        code: '123987',
        password: 'RecoveredPassword123!',
      ),
      throwsStateError,
    );
    expect(f.requests, isEmpty);
  });
  test(
    'unconfirmed task cannot be overwritten by another SMS recovery',
    () async {
      final f = Fixture()..recoveryEnabled = true;
      final task = TenantPasswordTask.recovery();
      await f.metadata.writeJson('password-task', task.toJson());
      final repository = f.repository();
      addTearDown(repository.close);
      await repository.restoreSession();
      await repository.authPolicy();
      f.requests.clear();
      await expectLater(
        repository.requestPasswordResetCode('19900000002'),
        throwsStateError,
      );
      expect(f.requests, isEmpty);
    },
  );
}
