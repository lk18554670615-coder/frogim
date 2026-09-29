import 'dart:async';
import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/runtime_endpoints.dart';
import 'package:linli_im/core/tenant_push.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/platform_repository.dart';
import 'package:linli_im/data/native_call_state_contract.dart';
import 'package:linli_im/core/tenant_call_scope.dart';
import 'package:linli_im/data/secure_local_store.dart';

import 'platform_repository_test.dart' show Fixture, jsonResponse;

class PushFixture extends Fixture {
  bool enabled = true;
  bool browser = false;
  bool voip = false;
  String? lease = DateTime.now()
      .add(const Duration(days: 1))
      .toUtc()
      .toIso8601String();
  final publicKey = base64Url
      .encode([4, ...List.filled(64, 1)])
      .replaceAll('=', '');
  int bindStatus = 201;
  Completer<void>? bindStarted, finishBind, refreshStarted, finishRefresh;
  String nextRefresh = 'n' * 43;

  @override
  Future<http.Response> authRequest(http.Request request) async {
    if (request.url.path == '/v2/auth/refresh' && finishRefresh != null) {
      requests.add(request);
      refreshStarted?.complete();
      await finishRefresh!.future;
      refresh = nextRefresh;
      return jsonResponse({
        'tenantContext': context,
        'sessionTicket': 't' * 43,
        'refreshToken': refresh,
      });
    }
    if (request.url.path == '/v2/config/push') {
      requests.add(request);
      return jsonResponse({
        'enabled': enabled,
        'providers': enabled
            ? [browser ? 'webpush' : 'getui', if (voip) 'getui_voip']
            : [],
        'webPushEnabled': enabled && browser,
        if (enabled && browser) 'webPushPublicKey': publicKey,
      });
    }
    if (request.url.path == '/v2/push/devices') {
      requests.add(request);
      bindStarted?.complete();
      if (finishBind != null) await finishBind!.future;
      return jsonResponse(
        bindStatus == 201
            ? {'id': 'binding', 'revision': 1, 'leaseExpiresAt': lease}
            : {
                'error': {
                  'code': 'UNAVAILABLE',
                  'message': 'private device details',
                },
              },
        bindStatus,
      );
    }
    if (request.url.path == '/v2/push/devices/unbind') {
      requests.add(request);
      return jsonResponse({'ok': true});
    }
    return super.authRequest(request);
  }

  List<http.Request> at(String path) => requests
      .where(
        (r) =>
            r.url.path == path &&
            (path != '/v2/auth/logout' || r.url.host == 'platform.example'),
      )
      .toList();
}

Future<void> bind(
  PlatformImRepository repository, {
  String id = 'getui-android-device',
  String platform = 'android',
  String provider = 'getui',
}) => repository.registerDevice(
  deviceId: id,
  platform: platform,
  provider: provider,
  pushToken: 'test-sdk-cid-0123456789',
  notificationsEnabled: true,
  previewEnabled: false,
  soundEnabled: true,
  vibrationEnabled: false,
);

class NativeBindingRecorder extends NoNativeCallState {
  final updates = <Map<String, Object?>?>[];
  TenantCallScope? scope;
  @override
  bool get enabled => true;
  @override
  Future<void> replace(
    TenantCallScope? scope, {
    bool resetBindings = false,
  }) async {
    this.scope = scope;
    if (scope == null) updates.add(null);
  }

  @override
  Future<void> bind(String provider, TenantPushBinding? binding) async {
    if (binding != null && scope == null) throw StateError('stale native bind');
    updates.add(binding?.context);
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
  });

  test(
    'iOS uses separate CID VoIP generation and unbinds only that capability',
    () async {
      final f = PushFixture()
        ..voip = true
        ..clientPlatform = 'ios';
      final native = NativeBindingRecorder();
      final repo = f.repository(nativeCalls: native);
      addTearDown(repo.close);
      await repo.passwordLogin('19900000001', 'synthetic-password');
      await bind(repo, id: 'ordinary', platform: 'ios');
      await bind(
        repo,
        id: 'getui-voip-generation-one',
        platform: 'ios',
        provider: 'getui_voip',
      );
      expect(native.updates.last!['deviceId'], 'getui-voip-generation-one');
      final requests = f
          .at('/v2/push/devices')
          .map((r) => jsonDecode(r.body)['device'] as Map)
          .toList();
      expect(requests.map((d) => d['provider']), ['getui', 'getui_voip']);
      expect(requests.last['pushToken'], 'test-sdk-cid-0123456789');
      await repo.removeUserDevice('getui-voip-generation-one');
      final removed = jsonDecode(f.at('/v2/push/devices/unbind').single.body);
      expect(removed['provider'], 'getui_voip');
      expect(removed['deviceId'], 'getui-voip-generation-one');
      await repo.removeUserDevice('ordinary');
      expect(
        jsonDecode(f.at('/v2/push/devices/unbind').last.body)['provider'],
        'getui',
      );
    },
  );

  test(
    'iOS cannot register unavailable VoIP or the retired direct APNs provider',
    () async {
      final f = PushFixture()..clientPlatform = 'ios';
      final repo = f.repository();
      addTearDown(repo.close);
      await repo.passwordLogin('19900000001', 'synthetic-password');
      await expectLater(
        bind(repo, platform: 'ios', provider: 'getui_voip'),
        throwsA(isA<ImApiException>()),
      );
      await expectLater(
        bind(repo, platform: 'ios', provider: 'apns_voip'),
        throwsStateError,
      );
      expect(f.at('/v2/push/devices'), isEmpty);
    },
  );

  test(
    'native registration persists the platform-issued binding and clears on unbind',
    () async {
      final f = PushFixture();
      final native = NativeBindingRecorder();
      final repo = f.repository(nativeCalls: native);
      addTearDown(repo.close);
      await repo.passwordLogin('19900000001', 'synthetic-password');
      await bind(repo);
      expect(native.updates.single, {
        'deviceId': 'getui-android-device',
        'pushBindingId': 'binding',
        'pushBindingRevision': 1,
        'leaseExpiresAt': f.lease,
        'tenantId': 'a',
        'localUserId': 'local',
        'assignmentVersion': 1,
        'authVersion': 1,
        'realmVersion': 1,
      });
      await repo.removeUserDevice('getui-android-device');
      expect(native.updates.last, isNull);
    },
  );

  test(
    'late platform bind after logout cannot repopulate native identity',
    () async {
      final f = PushFixture()
        ..bindStarted = Completer<void>()
        ..finishBind = Completer<void>();
      final native = NativeBindingRecorder();
      final repo = f.repository(nativeCalls: native);
      addTearDown(repo.close);
      await repo.passwordLogin('19900000001', 'synthetic-password');
      final result = expectLater(bind(repo), throwsStateError);
      await f.bindStarted!.future;
      final logout = repo.logout();
      expect(native.scope, isNull);
      f.finishBind!.complete();
      await Future.wait([result, logout]);
      expect(native.updates.whereType<Map<String, Object?>>(), isEmpty);
    },
  );

  test('native binding rejects absent or expired platform lease', () async {
    final f = PushFixture();
    final native = NativeBindingRecorder();
    final repo = f.repository(nativeCalls: native);
    addTearDown(repo.close);
    await repo.passwordLogin('19900000001', 'synthetic-password');
    for (final lease in [null, 'invalid', '2000-01-01T00:00:00Z']) {
      f.lease = lease;
      await expectLater(bind(repo), throwsFormatException);
    }
    expect(native.updates, isEmpty);
  });

  Future<PlatformImRepository> login(PushFixture f) async {
    final repository = f.repository();
    addTearDown(repository.close);
    await repository.passwordLogin('19900000001', 'test-password-only');
    return repository;
  }

  Future<TenantPushBinding> bindBrowser(PlatformImRepository repo) =>
      repo.registerBrowserPush(
        deviceId: 'browser-test',
        subscription: '{"endpoint":"test-only"}',
        notificationsEnabled: true,
        previewEnabled: false,
        soundEnabled: true,
        vibrationEnabled: false,
      );

  test(
    'browser binding uses platform credentials and returns identity plus platform lease',
    () async {
      final f = PushFixture()
        ..browser = true
        ..clientPlatform = 'web';
      final repo = await login(f);
      final binding = await bindBrowser(repo);
      expect(binding.context, {
        'deviceId': 'browser-test',
        'tenantId': f.tenant,
        'localUserId': f.uid,
        'assignmentVersion': f.binding,
        'authVersion': 1,
        'realmVersion': 1,
        'pushBindingId': 'binding',
        'pushBindingRevision': 1,
        'leaseExpiresAt': f.lease,
      });
      final sent = f.at('/v2/push/devices').single;
      final body = jsonDecode(sent.body);
      expect(sent.url.host, 'platform.example');
      expect(sent.followRedirects, false);
      expect(body['refreshToken'], f.refresh);
      expect(body['device']['provider'], 'webpush');
      expect(body['device']['platform'], 'web');
      expect(body['device']['previewEnabled'], false);
      expect(f.at('/v2/users/me/devices'), isEmpty);
      await repo.removeUserDevice('browser-test');
      expect(
        jsonDecode(f.at('/v2/push/devices/unbind').single.body)['provider'],
        'webpush',
      );
    },
  );

  test(
    'browser lease must be supplied and unexpired, never substituted with a local deadline',
    () async {
      final f = PushFixture()
        ..browser = true
        ..clientPlatform = 'web';
      final repo = await login(f);
      for (final lease in [null, 'invalid', '2000-01-01T00:00:00Z']) {
        f.lease = lease;
        await expectLater(bindBrowser(repo), throwsFormatException);
      }
    },
  );

  test(
    'browser bind is serialized with refresh and fenced by logout',
    () async {
      final f = PushFixture()
        ..browser = true
        ..clientPlatform = 'web'
        ..refreshStarted = Completer<void>()
        ..finishRefresh = Completer<void>();
      final repo = await login(f);
      final refresh = repo.live!.renewPlatformSession();
      await f.refreshStarted!.future;
      final pending = bindBrowser(repo);
      await Future<void>.delayed(Duration.zero);
      expect(f.at('/v2/push/devices'), isEmpty);
      f.finishRefresh!.complete();
      expect(await refresh, true);
      await pending;
      expect(
        jsonDecode(f.at('/v2/push/devices').single.body)['refreshToken'],
        f.nextRefresh,
      );
      f.bindStarted = Completer<void>();
      f.finishBind = Completer<void>();
      final late = expectLater(bindBrowser(repo), throwsStateError);
      await f.bindStarted!.future;
      final logout = repo.logout();
      f.finishBind!.complete();
      await Future.wait([late, logout]);
      expect(RuntimeEndpoints.notificationContext, isEmpty);
    },
  );

  test(
    'browser capability rejects incomplete keys and binding rejects ambiguous identity',
    () {
      final key = PushFixture().publicKey;
      final valid = <String, Object?>{
        'enabled': true,
        'providers': ['webpush'],
        'webPushEnabled': true,
        'webPushPublicKey': key,
      };
      expect(TenantPushCapabilities.fromJson(valid).webPushPublicKey, key);
      for (final bad in [null, '', 'invalid']) {
        expect(
          () => TenantPushCapabilities.fromJson({
            ...valid,
            'webPushPublicKey': bad,
          }),
          throwsFormatException,
        );
      }
      expect(
        () => TenantPushCapabilities.fromJson({
          ...valid,
          'webPushEnabled': false,
        }),
        throwsFormatException,
      );
      final context = <String, Object?>{
        'tenantId': 'a',
        'localUserId': 'u',
        'assignmentVersion': 1,
        'authVersion': 1,
        'realmVersion': 1,
        'pushBindingId': 'binding',
        'pushBindingRevision': 1,
        'leaseExpiresAt': DateTime.now()
            .toUtc()
            .add(const Duration(hours: 1))
            .toIso8601String(),
      };
      expect(TenantPushBinding(context).context, context);
      for (final field in context.keys) {
        expect(
          () => TenantPushBinding({...context}..remove(field)),
          throwsFormatException,
        );
      }
      expect(
        () => TenantPushBinding({
          ...context,
          'pushBindingRevision': 9007199254740992,
        }),
        throwsFormatException,
      );
      expect(
        () => TenantPushBinding({...context, 'tenantId': '../other'}),
        throwsFormatException,
      );
    },
  );

  test(
    'platform only registration, preferences, unbind and cached public policy',
    () async {
      final f = PushFixture();
      final repo = await login(f);
      await bind(repo);
      await bind(repo);
      expect(f.at('/v2/config/push'), hasLength(1));
      final request = f.at('/v2/push/devices').first;
      expect(request.url.host, 'platform.example');
      expect(request.headers['authorization'], isNull);
      expect(request.followRedirects, isFalse);
      final body = jsonDecode(request.body) as Map;
      expect(body.keys, unorderedEquals(['refreshToken', 'device']));
      expect(body['refreshToken'], f.refresh);
      expect(body['device']['previewEnabled'], false);
      expect(body['device']['vibrationEnabled'], false);
      expect(f.at('/v2/users/me/devices'), isEmpty);
      await repo.removeUserDevice('getui-android-device');
      final unbind = f.at('/v2/push/devices/unbind').single;
      expect(unbind.url.host, 'platform.example');
      expect(jsonDecode(unbind.body), {
        'refreshToken': f.refresh,
        'deviceId': 'getui-android-device',
        'provider': 'getui',
      });
      await repo.removeUserDevice('unknown');
      expect(f.at('/v2/push/devices/unbind'), hasLength(1));
    },
  );

  test(
    'disabled platform and unavailable configuration never use enterprise fallback',
    () async {
      final f = PushFixture()..enabled = false;
      final repo = await login(f);
      await expectLater(bind(repo), throwsA(isA<ImApiException>()));
      expect(f.at('/v2/push/devices'), isEmpty);
      expect(f.at('/v2/users/me/devices'), isEmpty);
    },
  );

  test(
    'unsupported provider or another client platform rejected before network',
    () async {
      final f = PushFixture();
      final repo = await login(f);
      for (final platform in ['ios', 'macos', 'web']) {
        await expectLater(bind(repo, platform: platform), throwsStateError);
      }
      for (final provider in ['webpush', 'getui_voip', 'webhook']) {
        await expectLater(bind(repo, provider: provider), throwsStateError);
      }
      expect(f.at('/v2/config/push'), isEmpty);
      expect(f.at('/v2/push/devices'), isEmpty);
    },
  );

  test('binding waits for rotation and uses new platform credential', () async {
    final f = PushFixture()
      ..refreshStarted = Completer<void>()
      ..finishRefresh = Completer<void>();
    final repo = await login(f);
    final renewed = repo.live!.renewPlatformSession();
    await f.refreshStarted!.future;
    final binding = bind(repo);
    await Future<void>.delayed(Duration.zero);
    expect(f.at('/v2/push/devices'), isEmpty);
    f.finishRefresh!.complete();
    expect(await renewed, isTrue);
    await binding;
    expect(
      jsonDecode(f.at('/v2/push/devices').single.body)['refreshToken'],
      f.nextRefresh,
    );
    await repo.logout();
    expect(
      jsonDecode(f.at('/v2/auth/logout').single.body)['refreshToken'],
      f.nextRefresh,
    );
    expect(await f.metadata.readJson('session'), isNull);
  });

  test(
    'rotation waits for binding; no concurrent use of rotating token',
    () async {
      final f = PushFixture()
        ..bindStarted = Completer<void>()
        ..finishBind = Completer<void>();
      final repo = await login(f);
      final binding = bind(repo);
      await f.bindStarted!.future;
      final renewed = repo.live!.renewPlatformSession();
      await Future<void>.delayed(Duration.zero);
      expect(f.at('/v2/auth/refresh'), isEmpty);
      f.finishBind!.complete();
      await binding;
      expect(await renewed, isTrue);
      expect(f.at('/v2/auth/refresh'), hasLength(1));
    },
  );

  test(
    'logout fences queued and in-flight bindings before clearing account',
    () async {
      final f = PushFixture()
        ..bindStarted = Completer<void>()
        ..finishBind = Completer<void>();
      final repo = await login(f);
      final first = expectLater(bind(repo), throwsStateError);
      await f.bindStarted!.future;
      final queued = expectLater(bind(repo, id: 'second'), throwsStateError);
      final logout = repo.logout();
      expect(RuntimeEndpoints.notificationContext, isEmpty);
      f.finishBind!.complete();
      await Future.wait([first, queued, logout]);
      expect(f.at('/v2/push/devices'), hasLength(1));
      expect(f.at('/v2/auth/logout'), hasLength(1));
      expect(repo.currentUser, isNull);
      expect(await f.metadata.readJson('session'), isNull);
    },
  );

  test(
    'logout during refresh discards issued token without exchanging a late ticket',
    () async {
      final f = PushFixture()
        ..refreshStarted = Completer<void>()
        ..finishRefresh = Completer<void>();
      final repo = await login(f);
      final oldToken = f.refresh;
      final renewed = repo.live!.renewPlatformSession();
      await f.refreshStarted!.future;
      final logout = repo.logout();
      f.finishRefresh!.complete();
      expect(await renewed, isFalse);
      await logout;
      expect(f.at('/v2/auth/tenant-session'), hasLength(1));
      expect(
        f.at('/v2/auth/logout').map((r) => jsonDecode(r.body)['refreshToken']),
        unorderedEquals([oldToken, f.nextRefresh]),
      );
      expect(await f.metadata.readJson('session'), isNull);
    },
  );

  test(
    'temporary registration failure can retry without storing private provider error',
    () async {
      final f = PushFixture()..bindStatus = 503;
      final repo = await login(f);
      await expectLater(
        bind(repo),
        throwsA(
          isA<ImApiException>().having(
            (e) => e.toString(),
            'safe error',
            isNot(contains('private device')),
          ),
        ),
      );
      f.bindStatus = 201;
      await bind(repo);
      expect(f.at('/v2/push/devices'), hasLength(2));
      expect(
        (await f.metadata.readJson('session')).toString(),
        isNot(contains('test-sdk-cid')),
      );
    },
  );

  test(
    'same local user across tenants binds only using new account session',
    () async {
      final f = PushFixture();
      final repo = await login(f);
      await bind(repo);
      await repo.logout();
      f.tenant = 'b';
      f.binding = 3;
      f.refresh = 'b' * 43;
      await repo.passwordLogin('19900000001', 'test-password-only');
      await bind(repo);
      expect(
        jsonDecode(f.at('/v2/push/devices').last.body)['refreshToken'],
        'b' * 43,
      );
      expect(RuntimeEndpoints.notificationContext['tenantId'], 'b');
      expect(RuntimeEndpoints.notificationContext['assignmentVersion'], 3);
    },
  );

  test(
    'controller rejects a callback captured before logout even for same user ID',
    () async {
      final f = PushFixture();
      final repo = await login(f);
      final controller = AppController(repo)
        ..authenticated = true
        ..currentUser = repo.currentUser;
      addTearDown(controller.dispose);
      final old = controller.pushSession;
      await controller.logout();
      await repo.passwordLogin('19900000001', 'test-password-only');
      controller.authenticated = true;
      controller.currentUser = repo.currentUser;
      await expectLater(
        controller.registerPushDevice(
          deviceId: 'device',
          platform: 'android',
          cid: 'test-sdk-cid-0123456789',
          notificationsEnabled: true,
          previewEnabled: true,
          soundEnabled: true,
          vibrationEnabled: true,
          expectedPushSession: old,
        ),
        throwsStateError,
      );
      expect(f.at('/v2/push/devices'), isEmpty);
    },
  );

  test(
    'notification needs full current versions and unexpired lifetime',
    () async {
      final f = PushFixture();
      await login(f);
      final data = {
        ...RuntimeEndpoints.notificationContext,
        'conversationId': 'test',
      };
      expect(RuntimeEndpoints.acceptsNotification(data), isTrue);
      for (final key in [
        'tenantId',
        'localUserId',
        'assignmentVersion',
        'authVersion',
        'realmVersion',
        'expiresAt',
      ]) {
        expect(
          RuntimeEndpoints.acceptsNotification({...data}..remove(key)),
          isFalse,
          reason: key,
        );
      }
      for (final key in ['assignmentVersion', 'authVersion', 'realmVersion']) {
        expect(
          RuntimeEndpoints.acceptsNotification({...data, key: 2}),
          isFalse,
        );
        expect(
          RuntimeEndpoints.acceptsNotification({...data, key: '1'}),
          isFalse,
        );
      }
      expect(
        RuntimeEndpoints.acceptsNotification({
          ...data,
          'expiresAt': '2020-01-01T00:00:00Z',
        }),
        isFalse,
      );
      expect(
        RuntimeEndpoints.acceptsNotification({...data, 'expiresAt': 'invalid'}),
        isFalse,
      );
    },
  );

  test(
    'push policy ignores unknown providers and rejects malformed responses',
    () {
      expect(
        TenantPushCapabilities.fromJson({
          'enabled': true,
          'providers': ['future-provider', 'getui'],
        }).providers,
        {'getui'},
      );
      expect(
        TenantPushCapabilities.fromJson({
          'enabled': false,
          'providers': ['getui'],
        }).providers,
        isEmpty,
      );
      for (final data in <Map<String, Object?>>[
        {},
        {'enabled': 'true', 'providers': []},
        {
          'enabled': true,
          'providers': [7],
        },
      ]) {
        expect(
          () => TenantPushCapabilities.fromJson(data),
          throwsFormatException,
        );
      }
    },
  );

  test(
    'cached restore can refresh inside lifecycle without queue deadlock',
    () async {
      final f = PushFixture();
      final first = await login(f);
      await first.close();
      final businessStore = SecureLocalStore(namespace: f.scopes.single);
      final cached = Map<String, Object?>.from(
        await businessStore.readJson('session') as Map,
      );
      cached.remove('user');
      await businessStore.writeJson('session', cached);
      f.refreshStatus = 503;
      var profileCalls = 0;
      f.businessOverride = (request) async {
        if (request.url.path == '/v2/users/me' && profileCalls++ == 0) {
          f.refreshStatus = 200;
          return jsonResponse({
            'error': {'code': 'UNAUTHENTICATED'},
          }, 401);
        }
        return jsonResponse({'data': f.user});
      };
      final restored = f.repository();
      addTearDown(restored.close);
      expect(
        await restored.restoreSession().timeout(const Duration(seconds: 3)),
        isTrue,
      );
      expect(f.at('/v2/auth/refresh'), hasLength(2));
      expect(restored.currentUser?.id, f.uid);
    },
  );

  test(
    'password change waits for rotation and uses the renewed credential',
    () async {
      final f = PushFixture()
        ..refreshStarted = Completer<void>()
        ..finishRefresh = Completer<void>();
      final repo = await login(f);
      await repo.authPolicy();
      final renewed = repo.live!.renewPlatformSession();
      await f.refreshStarted!.future;
      f.authOverride = (request) async {
        if (request.url.path == '/v2/auth/password-change') {
          expect(request.headers['authorization'], 'Bearer ${f.nextRefresh}');
          return jsonResponse({
            'requestId': jsonDecode(request.body)['requestId'],
            'jobId': 'job_test',
            'status': 'pending',
          }, 202);
        }
        if (request.url.path == '/v2/auth/logout') return jsonResponse({});
        return jsonResponse({'data': f.businessSession});
      };
      final change = repo.changeLoginPassword('old-password', 'new-password');
      await Future<void>.delayed(Duration.zero);
      expect(f.at('/v2/auth/password-change'), isEmpty);
      f.finishRefresh!.complete();
      expect(await renewed, isTrue);
      await change;
      expect(f.at('/v2/auth/password-change'), hasLength(1));
      expect(repo.currentUser, isNull);
    },
  );

  test(
    'registration redirect is not followed or retried at enterprise',
    () async {
      final f = PushFixture()..bindStatus = 302;
      final repo = await login(f);
      await expectLater(
        bind(repo),
        throwsA(
          isA<ImApiException>().having(
            (e) => e.code,
            'code',
            'AUTH_REDIRECT_REJECTED',
          ),
        ),
      );
      expect(f.at('/v2/push/devices'), hasLength(1));
      expect(f.at('/v2/push/devices').single.followRedirects, isFalse);
      expect(f.at('/v2/users/me/devices'), isEmpty);
    },
  );
}
