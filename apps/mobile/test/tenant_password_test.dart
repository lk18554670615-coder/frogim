import 'dart:async';
import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/tenant_password.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';

import 'platform_repository_test.dart' show Fixture, jsonResponse;

class FailingStore extends SecureLocalStore {
  FailingStore() : super(namespace: 'test-platform');
  bool reject = false;
  @override
  Future<void> writeJson(String key, Object value) async {
    if (reject && key == 'password-task') {
      throw StateError('storage unavailable');
    }
    await super.writeJson(key, value);
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
  });

  test(
    'strict platform key absence preserves encrypted recovery record',
    () async {
      final store = SecureLocalStore(
        namespace: 'strict-platform',
        requirePersistentKey: true,
      );
      await store.writeJson('password-task', {'opaque': 'test'});
      final prefs = await SharedPreferences.getInstance();
      final saved = {for (final k in prefs.getKeys()) k: prefs.get(k)};
      FlutterSecureStorage.setMockInitialValues({});
      final restored = SecureLocalStore(
        namespace: 'strict-platform',
        requirePersistentKey: true,
      );
      await expectLater(restored.readJson('password-task'), throwsStateError);
      expect({for (final k in prefs.getKeys()) k: prefs.get(k)}, saved);
    },
  );

  test(
    'accepted change uses platform only, persists no passwords and ends business session',
    () async {
      final f = Fixture();
      final repository = f.repository();
      addTearDown(repository.close);
      await repository.authPolicy();
      await repository.passwordLogin('19900000001', 'OriginalPassword123!');
      f.authOverride = (r) async {
        if (r.url.path == '/v2/auth/logout') return jsonResponse({});
        expect(r.url.host, 'platform.example');
        expect(r.url.path, '/v2/auth/password-change');
        expect(r.headers['authorization'], 'Bearer ${f.refresh}');
        final data = jsonDecode(r.body) as Map;
        final task = await f.metadata.readJson('password-task') as Map;
        expect(task['requestId'], data['requestId']);
        expect(data['currentPassword'], 'OriginalPassword123!');
        expect(data['newPassword'], 'NextPassword123!');
        return jsonResponse({
          'jobId': 'job_1',
          'requestId': data['requestId'],
          'status': 'pending',
        }, 202);
      };
      await repository.changeLoginPassword(
        'OriginalPassword123!',
        'NextPassword123!',
      );
      expect(repository.live, isNull);
      expect(f.gateways.single.disposed, isTrue);
      expect(repository.passwordChangeProgress, TenantPasswordProgress.pending);
      expect(await f.metadata.readJson('session'), isNull);
      final task = await f.metadata.readJson('password-task') as Map;
      expect(task.keys.toSet(), {
        'requestId',
        'token',
        'createdAt',
        'jobId',
        'status',
      });
      expect(jsonEncode(task), isNot(contains('Password123!')));
      final prefs = await SharedPreferences.getInstance();
      for (final key in prefs.getKeys()) {
        final value = prefs.get(key).toString();
        expect(value, isNot(contains('NextPassword123!')));
        expect(value, isNot(contains(f.refresh)));
      }
    },
  );

  test('definite rejection retains login and removes only new task', () async {
    final f = Fixture();
    final repository = f.repository();
    addTearDown(repository.close);
    await repository.authPolicy();
    await repository.passwordLogin('19900000001', 'OriginalPassword123!');
    f.authOverride = (_) async => jsonResponse({
      'error': {'code': 'INVALID_CREDENTIALS'},
    }, 401);
    await expectLater(
      repository.changeLoginPassword('wrong', 'NextPassword123!'),
      throwsA(isA<ImApiException>()),
    );
    expect(repository.live, isNotNull);
    expect(repository.passwordChangeProgress, isNull);
    expect(await f.metadata.readJson('password-task'), isNull);
    expect(await f.metadata.readJson('session'), isNotNull);
  });

  for (final mode in ['timeout', 'malformed', 'unavailable', 'redirect']) {
    test(
      '$mode remains unconfirmed across restart; query never replays passwords',
      () async {
        final f = Fixture();
        final repository = f.repository();
        await repository.authPolicy();
        await repository.passwordLogin('19900000001', 'OriginalPassword123!');
        f.authOverride = (r) async {
          if (r.url.path == '/v2/auth/logout') return jsonResponse({});
          return switch (mode) {
            'timeout' => throw TimeoutException('transport'),
            'malformed' => http.Response('not JSON', 202),
            'redirect' => http.Response(
              '',
              302,
              headers: {'location': 'https://foreign.example'},
            ),
            _ => jsonResponse({
              'error': {'code': 'UNAVAILABLE'},
            }, 503),
          };
        };
        await repository.changeLoginPassword(
          'OriginalPassword123!',
          'NextPassword123!',
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
        expect(
          restored.passwordChangeProgress,
          TenantPasswordProgress.unconfirmed,
        );
        expect(f.requests, isEmpty);
        f.authOverride = (r) async {
          expect(r.url.host, 'platform.example');
          expect(r.url.path, '/v2/auth/password-change/status');
          final body = jsonDecode(r.body) as Map;
          expect(body.keys, ['requestId']);
          return jsonResponse({
            'requestId': body['requestId'],
            'jobId': 'job_1',
            'status': 'completed',
          });
        };
        await restored.checkPasswordChange();
        expect(
          restored.passwordChangeProgress,
          TenantPasswordProgress.completed,
        );
        expect(restored.live, isNull);
        expect(f.requests.length, 1);
      },
    );
  }

  test(
    'persistent store failure prevents request, retains usable original login',
    () async {
      final store = FailingStore();
      final f = Fixture(metadata: store);
      final repository = f.repository();
      addTearDown(repository.close);
      await repository.authPolicy();
      await repository.passwordLogin('19900000001', 'OriginalPassword123!');
      f.requests.clear();
      store.reject = true;
      await expectLater(
        repository.changeLoginPassword(
          'OriginalPassword123!',
          'NextPassword123!',
        ),
        throwsStateError,
      );
      expect(f.requests, isEmpty);
      expect(repository.live, isNotNull);
      expect(repository.passwordChangeProgress, isNull);
    },
  );

  test(
    'double submit is refused; logout fences a delayed acceptance',
    () async {
      final f = Fixture();
      final repository = f.repository();
      addTearDown(repository.close);
      await repository.authPolicy();
      await repository.passwordLogin('19900000001', 'OriginalPassword123!');
      final started = Completer<void>();
      final result = Completer<http.Response>();
      String? request;
      f.authOverride = (r) async {
        if (r.url.path == '/v2/auth/logout') return jsonResponse({});
        request = (jsonDecode(r.body) as Map)['requestId'] as String;
        started.complete();
        return result.future;
      };
      final first = repository.changeLoginPassword(
        'OriginalPassword123!',
        'NextPassword123!',
      );
      await started.future;
      await expectLater(
        repository.changeLoginPassword(
          'OriginalPassword123!',
          'NextPassword123!',
        ),
        throwsStateError,
      );
      final logout = repository.logout();
      result.complete(
        jsonResponse({
          'requestId': request,
          'jobId': 'job_1',
          'status': 'pending',
        }, 202),
      );
      await first;
      await logout;
      expect(repository.live, isNull);
      expect(
        repository.passwordChangeProgress,
        TenantPasswordProgress.unconfirmed,
      );
      f.authOverride = null;
      f.uid = 'different';
      f.tenant = 'b';
      await repository.passwordLogin('19900000002', 'OtherPassword123!');
      expect(repository.passwordChangeProgress, isNull);
      expect(await f.metadata.readJson('password-task'), isNull);
      expect((await repository.profile()).id, 'different');
    },
  );

  test(
    'pending restore blocks stale session; expired status makes no network request',
    () async {
      final f = Fixture();
      await f.metadata.writeJson(
        'password-task',
        TenantPasswordTask(
          requestId: 'pwd_1',
          token: 'r' * 43,
          createdAt: DateTime.now().toUtc().subtract(const Duration(hours: 25)),
        ).toJson(),
      );
      final repository = f.repository();
      addTearDown(repository.close);
      expect(await repository.restoreSession(), isFalse);
      expect(repository.passwordChangeProgress, TenantPasswordProgress.expired);
      await repository.checkPasswordChange();
      expect(f.requests, isEmpty);
      await repository.dismissPasswordChange();
      expect(repository.passwordChangeProgress, isNull);
    },
  );

  test(
    'managed reset cannot fall through to legacy business endpoints',
    () async {
      final f = Fixture();
      final repository = f.repository();
      addTearDown(repository.close);
      await expectLater(
        repository.requestPasswordResetCode('19900000001'),
        throwsA(isA<ImApiException>()),
      );
      await expectLater(
        repository.resetPassword(
          phone: '19900000001',
          code: '123456',
          password: 'NextPassword123!',
        ),
        throwsA(isA<ImApiException>()),
      );
      expect(f.requests, isEmpty);
    },
  );

  test(
    'late status response after logout cannot overwrite task or resurrect a session',
    () async {
      final f = Fixture();
      final task = TenantPasswordTask.create('r' * 43);
      await f.metadata.writeJson('password-task', task.toJson());
      final repository = f.repository();
      addTearDown(repository.close);
      expect(await repository.restoreSession(), isFalse);
      final started = Completer<void>(), response = Completer<http.Response>();
      f.authOverride = (r) async {
        started.complete();
        return response.future;
      };
      final pending = repository.checkPasswordChange();
      final rejected = expectLater(pending, throwsStateError);
      await started.future;
      await repository.logout();
      response.complete(
        jsonResponse({
          'requestId': task.requestId,
          'jobId': 'job_late',
          'status': 'completed',
        }),
      );
      await rejected;
      expect(
        repository.passwordChangeProgress,
        TenantPasswordProgress.unconfirmed,
      );
      expect(repository.live, isNull);
      expect((await f.metadata.readJson('password-task') as Map)['jobId'], '');
    },
  );

  test('task decoding rejects foreign IDs, bad states and rollback', () {
    expect(
      () => TenantPasswordResult.fromJson({
        'requestId': 'other',
        'jobId': 'job_1',
        'status': 'pending',
      }, 'pwd_1'),
      throwsFormatException,
    );
    expect(
      () => TenantPasswordResult.fromJson({
        'requestId': 'pwd_1',
        'status': 'failed',
      }, 'pwd_1'),
      throwsFormatException,
    );
    final task = TenantPasswordTask(
      requestId: 'pwd_1',
      token: 'r' * 43,
      createdAt: DateTime.now(),
      jobId: 'job_1',
      progress: TenantPasswordProgress.completed,
    );
    expect(
      () => task.update(
        const TenantPasswordResult(
          'pwd_1',
          'job_1',
          TenantPasswordProgress.pending,
        ),
      ),
      throwsFormatException,
    );
    expect(
      () => task.update(
        const TenantPasswordResult(
          'pwd_1',
          'job_2',
          TenantPasswordProgress.completed,
        ),
      ),
      throwsFormatException,
    );
  });
}
