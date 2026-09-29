import 'dart:async';
import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:wukongimfluttersdk/common/options.dart';

import 'package:linli_im/core/runtime_endpoints.dart';
import 'package:linli_im/core/tenant_context.dart';
import 'package:linli_im/core/tenant_call_scope.dart';
import 'package:linli_im/core/tenant_push.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/platform_repository.dart';
import 'package:linli_im/data/native_call_state_contract.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/data/session_coordination.dart';
import 'package:linli_im/data/tenant_auth_client.dart';
import 'package:linli_im/im/business_repository.dart';

import 'support/fake_wukong_gateway.dart';

http.Response jsonResponse(Object body, [int code = 200]) => http.Response(
  jsonEncode(body),
  code,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

class Fixture {
  Fixture({SecureLocalStore? metadata})
    : metadata = metadata ?? SecureLocalStore(namespace: 'test-platform');
  String tenant = 'a', uid = 'local', refresh = 'r' * 43;
  int binding = 1, accessRevision = 0;
  DateTime tokenExpiry = DateTime.now().add(const Duration(hours: 1));
  int refreshStatus = 200;
  bool recoveryEnabled = false;
  String clientPlatform = 'android';
  final requests = <http.Request>[];
  final scopes = <String>[];
  final gateways = <FakeWukongGateway>[];
  final runtimes = <LiveImRepository>[];
  final SecureLocalStore metadata;
  SecureLocalStore Function(String namespace)? businessStoreFactory;
  Future<http.Response> Function(http.Request)? authOverride, businessOverride;

  Map<String, Object?> get context => {
    'tenantId': tenant,
    'displayName': '企业$tenant',
    'httpBaseUrl': 'https://$tenant.example',
    'assignmentVersion': binding,
    'configVersion': 1,
  };
  Map<String, Object?> get user => {
    'id': uid,
    'name': '测试用户',
    'phone': '19900000001',
  };
  String get accessToken =>
      'header.${base64Url.encode(utf8.encode(jsonEncode({'exp': tokenExpiry.millisecondsSinceEpoch ~/ 1000, 'revision': accessRevision, 'ver': 1, 'realm': 1}))).replaceAll('=', '')}.signature';
  Map<String, Object?> get businessSession => {
    'user': user,
    'accessToken': accessToken,
    'refreshToken': 'enterprise-refresh',
    'imSession': {
      'uid': uid,
      'token': 'wk1_test',
      'deviceFlag': 0,
      'deviceLevel': 1,
      'tcpUrl': 'tcp://$tenant.example:5100',
      'wsUrl': 'wss://$tenant.example/im',
      'sdk': 'wukongimfluttersdk',
      'issuedAt': DateTime.now().toUtc().toIso8601String(),
    },
  };

  Future<http.Response> authRequest(http.Request request) async {
    requests.add(request);
    if (authOverride != null) return authOverride!(request);
    if (request.url.host == 'platform.example') {
      if (request.url.path == '/v2/config/auth') {
        return jsonResponse({
          'otpLoginEnabled': false,
          'registrationEnabled': false,
          'passwordChangeEnabled': true,
          'passwordResetEnabled': recoveryEnabled,
        });
      }
      if (request.url.path == '/v2/auth/logout') return jsonResponse({});
      if (request.url.path == '/v2/auth/refresh' && refreshStatus != 200) {
        return jsonResponse({
          'error': {
            'code': refreshStatus == 401
                ? 'INVALID_CREDENTIALS'
                : 'PLATFORM_UNAVAILABLE',
          },
        }, refreshStatus);
      }
      return jsonResponse({
        'tenantContext': context,
        'sessionTicket': 't' * 43,
        'refreshToken': refresh,
      });
    }
    expect(request.url.path, '/v2/auth/tenant-session');
    expect(request.headers['authorization'], isNull);
    expect(jsonDecode(request.body), {'sessionTicket': 't' * 43});
    // Real enterprise API envelope, unlike platform authentication responses.
    return jsonResponse({'data': businessSession});
  }

  PlatformImRepository repository({
    SessionCoordination? coordination,
    NativeCallState? nativeCalls,
  }) => PlatformImRepository(
    coordination: coordination,
    nativeCallState: nativeCalls,
    auth: TenantAuthClient(
      platformBaseUrl: 'https://platform.example',
      clientPlatform: clientPlatform,
      client: MockClient(authRequest),
    ),
    platformStore: metadata,
    businessFactory: (context, namespace, refresh) {
      scopes.add(namespace);
      final gateway = FakeWukongGateway();
      gateways.add(gateway);
      final runtime = LiveImRepository(
        apiBaseUrl: context.httpBaseUrl.toString(),
        clientPlatform: clientPlatform,
        cacheNamespace: namespace,
        store:
            businessStoreFactory?.call(namespace) ??
            SecureLocalStore(namespace: namespace),
        externalSessionRefresh: refresh,
        wukongGateway: gateway,
        client: MockClient((request) async {
          requests.add(request);
          if (businessOverride != null) return businessOverride!(request);
          if (request.url.path == '/v2/users/me') {
            return jsonResponse({'data': user});
          }
          return jsonResponse({'data': {}});
        }),
      );
      runtimes.add(runtime);
      return runtime;
    },
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
  });

  test(
    'native login generation survives restart but not logout and new login',
    () async {
      final f = Fixture();
      final native = _RecordingNativeCalls();
      var repository = f.repository(nativeCalls: native);
      await repository.passwordLogin('19900000001', 'synthetic-password');
      final first = native.scopes.last!;
      expect(first.data['callSessionId'], matches(r'^[A-Za-z0-9_-]{43}$'));
      expect(first.data['tenantId'], 'a');
      expect(first.data['localUserId'], 'local');
      await repository.close();
      repository = f.repository(nativeCalls: native);
      addTearDown(repository.close);
      expect(await repository.restoreSession(), isTrue);
      expect(native.scopes.last!.sameSession(first), isTrue);
      await repository.logout();
      expect(native.scopes.last, isNull);
      expect(RuntimeEndpoints.notificationContext, isEmpty);
      await repository.passwordLogin('19900000001', 'synthetic-password');
      expect(native.scopes.last!.sameSession(first), isFalse);
      expect(native.scopes.last!.sameIdentity(first), isTrue);
    },
  );

  test('unconfirmed native identity never exposes business runtime', () async {
    final f = Fixture();
    final native = _RecordingNativeCalls()..failActive = true;
    final repository = f.repository(nativeCalls: native);
    addTearDown(repository.close);
    await expectLater(
      repository.passwordLogin('19900000001', 'synthetic-password'),
      throwsA(isA<Exception>()),
    );
    expect(native.scopes.last, isNull);
    expect(RuntimeEndpoints.notificationContext, isEmpty);
    expect(repository.currentUser, isNull);
  });

  test(
    'platform login exchanges real envelope and business requests go directly to enterprise',
    () async {
      final f = Fixture();
      final repository = f.repository();
      addTearDown(repository.close);
      final policy = await repository.authPolicy();
      expect(policy.otpLoginEnabled, isFalse);
      expect(policy.qrLoginEnabled, isFalse);
      expect(policy.registrationEnabled, isFalse);
      expect(policy.passwordResetEnabled, isFalse);
      expect(
        (await repository.passwordLogin(
          '19900000001',
          'not-a-real-password',
        )).id,
        f.uid,
      );
      expect((await repository.profile()).id, f.uid);
      expect(f.requests.map((r) => r.url.host), [
        'platform.example',
        'platform.example',
        'a.example',
        'a.example',
      ]);
      expect(f.scopes.single, 'tenant.a.user.local.binding.1');
      expect(RuntimeEndpoints.businessBaseUrl, 'https://a.example');
      expect(
        f.requests.last.headers['authorization'],
        'Bearer ${f.accessToken}',
      );
      await repository.saveDraft('same-conversation', 'A企业草稿');
      expect(await repository.readDraft('same-conversation'), 'A企业草稿');
    },
  );

  test(
    'logout then same local ID in another enterprise cannot read previous draft',
    () async {
      final f = Fixture();
      final repository = f.repository();
      addTearDown(repository.close);
      await repository.passwordLogin('19900000001', 'password');
      await repository.saveDraft('same-conversation', 'private A');
      await repository.logout();
      expect(f.gateways.single.disposed, isTrue);
      expect(
        await SecureLocalStore(namespace: f.scopes.single).readJson('session'),
        isNull,
      );
      f.tenant = 'b';
      f.binding = 2;
      await repository.passwordLogin('19900000001', 'password');
      expect(await repository.readDraft('same-conversation'), isEmpty);
      expect(f.scopes.last, 'tenant.b.user.local.binding.2');
      expect(RuntimeEndpoints.businessBaseUrl, 'https://b.example');
      expect(
        f.requests
            .where((r) => r.url.path == '/v2/auth/logout')
            .map((r) => r.url.host),
        ['platform.example', 'a.example'],
      );
    },
  );

  test('API 401 renews on platform, not enterprise /auth/refresh', () async {
    final f = Fixture();
    final repository = f.repository();
    addTearDown(repository.close);
    await repository.passwordLogin('19900000001', 'password');
    var attempt = 0;
    f.businessOverride = (request) async {
      if (attempt++ == 0) {
        f.accessRevision++;
        return jsonResponse({
          'error': {'code': 'UNAUTHORIZED'},
        }, 401);
      }
      return jsonResponse({'data': f.user});
    };
    expect((await repository.profile()).id, f.uid);
    final refreshes = f.requests.where((r) => r.url.path == '/v2/auth/refresh');
    expect(refreshes.length, 1);
    expect(refreshes.single.url.host, 'platform.example');
    expect(jsonDecode(refreshes.single.body), {'refreshToken': f.refresh});
    expect(attempt, 2);
  });

  test(
    'assignment change on refresh emits expiry and never contacts the new enterprise',
    () async {
      final f = Fixture();
      final repository = f.repository();
      addTearDown(repository.close);
      await repository.passwordLogin('19900000001', 'password');
      final expired = repository.events.firstWhere(
        (e) => e.type == ImEventType.sessionExpired,
      );
      f.tenant = 'b';
      f.binding++;
      f.businessOverride = (_) async => jsonResponse({
        'error': {'code': 'UNAUTHORIZED'},
      }, 401);
      await expectLater(
        repository.profile(),
        throwsA(anyOf(isA<StateError>(), isA<ImApiException>())),
      );
      await expired;
      expect(f.requests.any((r) => r.url.host == 'b.example'), isFalse);
      expect(RuntimeEndpoints.businessBaseUrl, isNot('https://a.example'));
    },
  );

  test('late business response after logout is fenced', () async {
    final f = Fixture();
    final repository = f.repository();
    addTearDown(repository.close);
    await repository.passwordLogin('19900000001', 'password');
    final started = Completer<void>();
    final response = Completer<http.Response>();
    f.businessOverride = (_) {
      started.complete();
      return response.future;
    };
    final result = expectLater(repository.profile(), throwsStateError);
    await started.future;
    await repository.logout();
    response.complete(jsonResponse({'data': f.user}));
    await result;
    expect(repository.currentUser, isNull);
  });

  test('late authentication after logout cannot resurrect a session', () async {
    final f = Fixture();
    final repository = f.repository();
    addTearDown(repository.close);
    final response = Completer<http.Response>();
    final started = Completer<void>();
    f.authOverride = (_) {
      started.complete();
      return response.future;
    };
    final result = expectLater(
      repository.passwordLogin('19900000001', 'password'),
      throwsStateError,
    );
    await started.future;
    await repository.logout();
    response.complete(
      jsonResponse({
        'tenantContext': f.context,
        'sessionTicket': 't' * 43,
        'refreshToken': f.refresh,
      }),
    );
    await result;
    expect(f.scopes, isEmpty);
    expect(await f.metadata.readJson('session'), isNull);
  });

  test(
    'restart renews saved scope; unavailable platform only allows unexpired cached session',
    () async {
      final f = Fixture();
      final first = f.repository();
      await first.passwordLogin('19900000001', 'password');
      await first.close();
      final second = f.repository();
      f.refreshStatus = 503;
      expect(await second.restoreSession(), isTrue);
      expect(second.currentUser?.id, f.uid);
      await second.close();
      final raw = await f.metadata.readJson('session') as Map<String, Object?>;
      raw['expiresAt'] = DateTime.now()
          .subtract(const Duration(minutes: 1))
          .toUtc()
          .toIso8601String();
      await f.metadata.writeJson('session', raw);
      final third = f.repository();
      addTearDown(third.close);
      expect(await third.restoreSession(), isFalse);
      expect(third.currentUser, isNull);
    },
  );

  test(
    '401 on restore does not fall back to otherwise valid cached credentials',
    () async {
      final f = Fixture();
      final first = f.repository();
      await first.passwordLogin('19900000001', 'password');
      await first.close();
      f.refreshStatus = 401;
      final second = f.repository();
      addTearDown(second.close);
      await expectLater(
        second.restoreSession(),
        throwsA(isA<ImApiException>()),
      );
      expect(second.currentUser, isNull);
      expect(await f.metadata.readJson('session'), isNull);
    },
  );

  test(
    'pending registration persists and completes without reusing OTP or exposing poll capability',
    () async {
      final f = Fixture();
      var repository = f.repository();
      f.authOverride = (_) async => jsonResponse({
        'status': 'pending',
        'jobId': 'job_1',
        'pollToken': 'p' * 43,
      }, 202);
      await expectLater(
        repository.tenantRegister(
          phone: '19900000001',
          code: 'real-otp',
          password: 'password',
          name: 'name',
          enterpriseCode: 'ENTERPRISE',
          inviteCode: 'PERSONAL',
        ),
        throwsA(isA<ImApiException>()),
      );
      expect(repository.hasPendingRegistration, isTrue);
      expect(
        jsonDecode(f.requests.single.body)['enterpriseCode'],
        'ENTERPRISE',
      );
      expect(jsonDecode(f.requests.single.body)['inviteCode'], 'PERSONAL');
      await repository.close();
      repository = f.repository();
      addTearDown(repository.close);
      expect(await repository.restoreSession(), isFalse);
      expect(repository.hasPendingRegistration, isTrue);
      f.authOverride = (request) async {
        if (request.url.path.endsWith('job_1')) {
          return jsonResponse({'status': 'completed'});
        }
        if (request.url.path.endsWith('/complete')) {
          return jsonResponse({
            'tenantContext': f.context,
            'sessionTicket': 't' * 43,
            'refreshToken': f.refresh,
          });
        }
        return jsonResponse({'data': f.businessSession});
      };
      expect((await repository.resumeRegistration()).id, f.uid);
      expect(repository.hasPendingRegistration, isFalse);
      expect(
        f.requests
            .where((r) => r.url.host == 'a.example')
            .single
            .headers['authorization'],
        isNull,
      );
      expect(
        f.requests
            .where((r) => r.headers['authorization'] == 'Bearer ${'p' * 43}')
            .every(
              (r) => r.url.host == 'platform.example' && r.url.query.isEmpty,
            ),
        isTrue,
      );
    },
  );

  test(
    'business data source fences stale replies independently of UI',
    () async {
      var epoch = 1;
      final response = Completer<http.Response>();
      final started = Completer<void>();
      final business = BusinessRepository(
        apiBaseUrl: 'https://a.example',
        platform: 'android',
        accessToken: () => 'a-token',
        sessionEpoch: () => epoch,
        sessionActive: () => true,
        client: MockClient((r) {
          started.complete();
          return response.future;
        }),
      );
      final result = expectLater(
        business.request('GET', '/test'),
        throwsStateError,
      );
      await started.future;
      epoch++;
      response.complete(jsonResponse({'data': {}}));
      await result;
    },
  );

  test(
    'native DB names include binding scope while wire UID stays unchanged',
    () {
      final a = Options.newDefault('same', 'token')
        ..databaseNamespace = 'tenant.a.user.same.binding.1';
      final b = Options.newDefault('same', 'token')
        ..databaseNamespace = 'tenant.b.user.same.binding.1';
      final again = Options.newDefault('same', 'token')
        ..databaseNamespace = 'tenant.a.user.same.binding.2';
      expect(
        {a.databaseIdentity, b.databaseIdentity, again.databaseIdentity}.length,
        3,
      );
      expect(a.uid, 'same');
      expect(a.databaseIdentity, matches(r'^tenant_[a-f0-9]{64}$'));
      expect(Options.newDefault('legacy', 'token').databaseIdentity, 'legacy');
    },
  );

  test(
    'notifications require the exact current tenant user and assignment',
    () {
      final owner = Object();
      final context = TenantContext.fromJson(Fixture().context);
      RuntimeEndpoints.attach(
        owner,
        context,
        namespace: context.cacheNamespace('local'),
        localUserId: 'local',
        authVersion: 1,
        realmVersion: 1,
        expiresAt: DateTime.now().toUtc().add(const Duration(hours: 1)),
      );
      final payload = {
        ...RuntimeEndpoints.notificationContext,
        'conversationId': 'c1',
      };
      expect(RuntimeEndpoints.acceptsNotification(payload), isTrue);
      expect(
        RuntimeEndpoints.acceptsNotification({...payload, 'tenantId': 'b'}),
        isFalse,
      );
      expect(
        RuntimeEndpoints.acceptsNotification({
          ...payload,
          'assignmentVersion': 2,
        }),
        isFalse,
      );
      expect(
        RuntimeEndpoints.acceptsNotification({
          ...payload,
          'localUserId': 'other',
        }),
        isFalse,
      );
      expect(
        RuntimeEndpoints.acceptsNotification({'conversationId': 'c1'}),
        isFalse,
      );
      RuntimeEndpoints.clear(Object());
      expect(RuntimeEndpoints.acceptsNotification(payload), isTrue);
      RuntimeEndpoints.clear(owner);
      expect(RuntimeEndpoints.acceptsNotification(payload), isFalse);
    },
  );
}

class _RecordingNativeCalls implements NativeCallState {
  final scopes = <TenantCallScope?>[];
  bool failActive = false;
  @override
  bool get enabled => true;
  @override
  Future<void> replace(
    TenantCallScope? scope, {
    bool resetBindings = false,
  }) async {
    scopes.add(scope);
    if (failActive && scope != null) {
      throw const FormatException('native state unavailable');
    }
  }

  @override
  Future<void> bind(String provider, TenantPushBinding? binding) async {}
}
