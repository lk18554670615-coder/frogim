import 'dart:async';
import 'dart:convert';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/core/runtime_endpoints.dart';
import 'package:linli_im/core/tenant_password.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/data/session_coordination.dart';
import 'platform_repository_test.dart' show Fixture, jsonResponse;

class Hub {
  Future<void> tail = Future.value();
  final peers = <Peer>[];
  Peer peer() {
    final p = Peer(this);
    peers.add(p);
    return p;
  }

  void signal() {
    for (final p in [...peers]) {
      p.events.add(null);
    }
  }
}

class Peer implements SessionCoordination {
  Peer(this.hub);
  final Hub hub;
  final events = StreamController<void>.broadcast();
  bool unavailable = false;
  @override
  bool get enabled => true;
  @override
  Stream<void> get changes => events.stream;
  @override
  Future<T> run<T>(Future<T> Function() work) {
    final next = hub.tail.then((_) {
      if (unavailable) throw StateError('synthetic lock failure');
      return work();
    });
    hub.tail = next.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return next;
  }

  // Explicit signaling lets tests model delayed storage events and frozen tabs.
  @override
  void changed() {}
  @override
  void close() {
    hub.peers.remove(this);
    unawaited(events.close());
  }
}

class RotatingPlatform {
  String valid = 'r' * 43;
  int sequence = 0, refreshes = 0, rejected = 0, binds = 0;
  int tabSequence = 0;
  bool offline = false;
  Completer<void>? entered, release;
  final revoked = <String>[];
  String issue() => valid = (sequence++).toString().padLeft(43, 'r');
  Future<http.Response> request(Fixture f, http.Request r) async {
    if (r.url.host != 'platform.example') {
      return jsonResponse({'data': f.businessSession});
    }
    final data = r.body.isEmpty
        ? <String, dynamic>{}
        : jsonDecode(r.body) as Map<String, dynamic>;
    if (r.url.path == '/v2/auth/logout') {
      revoked.add(data['refreshToken'] as String);
      return jsonResponse({});
    }
    if (r.url.path == '/v2/config/push') {
      return jsonResponse({
        'enabled': true,
        'providers': ['getui'],
      });
    }
    if (r.url.path == '/v2/push/devices') {
      if (data['refreshToken'] != valid) {
        rejected++;
        return jsonResponse({
          'error': {'code': 'INVALID_CREDENTIALS'},
        }, 401);
      }
      binds++;
      return jsonResponse({
        'id': 'binding',
        'revision': 1,
        'leaseExpiresAt': DateTime.now()
            .add(const Duration(minutes: 10))
            .toIso8601String(),
      });
    }
    if (r.url.path == '/v2/auth/refresh') {
      if (offline) {
        return jsonResponse({
          'error': {'code': 'PLATFORM_UNAVAILABLE'},
        }, 503);
      }
      if (data['refreshToken'] != valid) {
        rejected++;
        return jsonResponse({
          'error': {'code': 'INVALID_CREDENTIALS'},
        }, 401);
      }
      refreshes++;
      entered?.complete();
      entered = null;
      final waiting = release;
      release = null;
      if (waiting != null) await waiting.future;
    }
    issue();
    return jsonResponse({
      'tenantContext': f.context,
      'sessionTicket': 't' * 43,
      'refreshToken': valid,
    });
  }

  Fixture fixture({String uid = 'local'}) {
    final f = Fixture(metadata: TabMetadata())..uid = uid;
    final tab = tabSequence++;
    f.businessStoreFactory = (namespace) =>
        SecureLocalStore(namespace: '$namespace.tab.$tab');
    f.authOverride = (r) => request(f, r);
    return f;
  }
}

// VM equivalent of one shared platform record plus private sessionStorage.
class TabMetadata extends SecureLocalStore {
  TabMetadata() : super(namespace: 'shared-platform');
  final local = <String, Object>{};
  bool fail = false;
  static const privateKeys = {'tab-session', 'password-task', 'pending'};
  @override
  Future<Object?> readJson(String key) async {
    if (fail) throw StateError('synthetic storage unavailable');
    return privateKeys.contains(key) ? local[key] : await super.readJson(key);
  }

  @override
  Future<void> writeJson(String key, Object value) async {
    if (privateKeys.contains(key)) {
      local[key] = value;
    } else {
      await super.writeJson(key, value);
    }
  }

  @override
  Future<void> remove(String key) async {
    if (privateKeys.contains(key)) {
      local.remove(key);
    } else {
      await super.remove(key);
    }
  }
}

Future<void> settle() async {
  for (var i = 0; i < 12; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
  });
  test(
    'two restored tabs and simultaneous renewal consume each rotation once',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final a = server.fixture(), b = server.fixture(), c = server.fixture();
      final ra = a.repository(coordination: hub.peer()),
          rb = b.repository(coordination: hub.peer()),
          rc = c.repository(coordination: hub.peer());
      addTearDown(ra.close);
      addTearDown(rb.close);
      addTearDown(rc.close);
      await ra.passwordLogin('19900000001', 'fixture');
      final generation =
          (await a.metadata.readJson('session') as Map)['generation'];
      expect(await Future.wait([rb.restoreSession(), rc.restoreSession()]), [
        true,
        true,
      ]);
      expect(
        await Future.wait([
          ra.live!.renewPlatformSession(),
          rb.live!.renewPlatformSession(),
          rc.live!.renewPlatformSession(),
        ]),
        [true, true, true],
      );
      expect(server.refreshes, 5);
      expect(server.rejected, 0);
      final current = await a.metadata.readJson('session') as Map;
      expect(current['platformRefreshToken'], server.valid);
      expect(current['generation'], generation);
    },
  );
  test(
    'device binding in a different tab waits for rotation and uses fresh token',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final a = server.fixture(), b = server.fixture();
      final ra = a.repository(coordination: hub.peer()),
          rb = b.repository(coordination: hub.peer());
      addTearDown(ra.close);
      addTearDown(rb.close);
      await ra.passwordLogin('19900000001', 'fixture');
      await rb.restoreSession();
      final entered = Completer<void>(), release = Completer<void>();
      server.entered = entered;
      server.release = release;
      final renewing = ra.live!.renewPlatformSession();
      await entered.future;
      final binding = rb.registerDevice(
        deviceId: 'synthetic',
        platform: 'android',
        provider: 'getui',
        pushToken: 'synthetic',
        notificationsEnabled: true,
        previewEnabled: true,
        soundEnabled: true,
        vibrationEnabled: true,
      );
      await settle();
      expect(server.binds, 0);
      release.complete();
      expect(await renewing, true);
      await binding;
      expect(server.binds, 1);
      expect(server.rejected, 0);
    },
  );
  for (final sameUser in [false, true]) {
    test(
      'stale logout cannot remove a newer ${sameUser ? "same-account" : "other-account"} login',
      () async {
        final hub = Hub(), server = RotatingPlatform();
        final a = server.fixture(),
            b = server.fixture(uid: sameUser ? 'local' : 'other');
        final ra = a.repository(coordination: hub.peer()),
            rb = b.repository(coordination: hub.peer());
        addTearDown(ra.close);
        addTearDown(rb.close);
        await ra.passwordLogin('19900000001', 'fixture');
        final old = await a.metadata.readJson('session') as Map;
        await rb.passwordLogin('19900000002', 'fixture');
        final fresh = await b.metadata.readJson('session') as Map;
        expect(old['generation'], isNot(fresh['generation']));
        await ra.logout();
        expect(await b.metadata.readJson('session'), fresh);
        expect(server.revoked, isNot(contains(fresh['platformRefreshToken'])));
        expect(await rb.live!.renewPlatformSession(), true);
      },
    );
  }
  test(
    'logout after another tab rotated revokes latest credential and notifies peers',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final a = server.fixture(), b = server.fixture();
      final ra = a.repository(coordination: hub.peer()),
          rb = b.repository(coordination: hub.peer());
      addTearDown(ra.close);
      addTearDown(rb.close);
      await ra.passwordLogin('19900000001', 'fixture');
      await rb.restoreSession();
      final latest = server.valid;
      final ended = Completer<void>();
      final subscription = rb.events.listen((e) {
        if (e.type == ImEventType.sessionExpired && !ended.isCompleted) {
          ended.complete();
        }
      });
      addTearDown(subscription.cancel);
      await ra.logout();
      hub.signal();
      await ended.future.timeout(const Duration(seconds: 3));
      await settle();
      expect(server.revoked, contains(latest));
      expect(await b.metadata.readJson('session'), isNull);
      expect(rb.live, isNull);
    },
  );
  test(
    'late rotation and logout cannot erase another tab login queued behind it',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final a = server.fixture(), b = server.fixture(uid: 'other');
      final ra = a.repository(coordination: hub.peer()),
          rb = b.repository(coordination: hub.peer());
      addTearDown(ra.close);
      addTearDown(rb.close);
      await ra.passwordLogin('19900000001', 'fixture');
      final entered = Completer<void>(), release = Completer<void>();
      server.entered = entered;
      server.release = release;
      final pending = ra.live!.renewPlatformSession();
      await entered.future;
      final login = rb.passwordLogin('19900000002', 'fixture');
      final logout = ra.logout();
      release.complete();
      expect(await pending, false);
      await login;
      await logout;
      final fresh = await b.metadata.readJson('session') as Map;
      expect(fresh['localUserId'], 'other');
      expect(fresh['platformRefreshToken'], server.valid);
      expect(server.revoked, isNot(contains(server.valid)));
    },
  );
  test(
    'refresh fails closed on foreign login even before advisory event arrives',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final a = server.fixture(), b = server.fixture(uid: 'other');
      final ra = a.repository(coordination: hub.peer()),
          rb = b.repository(coordination: hub.peer());
      addTearDown(ra.close);
      addTearDown(rb.close);
      await ra.passwordLogin('19900000001', 'fixture');
      await rb.passwordLogin('19900000002', 'fixture');
      final fresh = await b.metadata.readJson('session');
      expect(await ra.live!.renewPlatformSession(), false);
      await settle();
      expect(server.refreshes, 0);
      expect(await b.metadata.readJson('session'), fresh);
      expect(ra.live, isNull);
    },
  );
  test('unavailable browser lock still detaches local logout', () async {
    final hub = Hub(), server = RotatingPlatform();
    final fixture = server.fixture(), peer = hub.peer();
    final repository = fixture.repository(coordination: peer);
    addTearDown(repository.close);
    await repository.passwordLogin('19900000001', 'fixture');
    final shared = await fixture.metadata.readJson('session');
    peer.unavailable = true;
    await expectLater(repository.logout(), throwsStateError);
    expect(repository.live, isNull);
    expect(await fixture.metadata.readJson('session'), shared);
    expect(server.revoked, isEmpty);
  });
  test(
    'advisory lock failure fences and detaches without erasing shared state',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final fixture = server.fixture(), peer = hub.peer();
      final repository = fixture.repository(coordination: peer);
      addTearDown(repository.close);
      await repository.passwordLogin('19900000001', 'fixture');
      final shared = await fixture.metadata.readJson('session');
      peer.unavailable = true;
      hub.signal();
      await settle();
      expect(repository.live, isNull);
      expect(await fixture.metadata.readJson('session'), shared);
      expect(server.revoked, isEmpty);
    },
  );
  test(
    'cancelled login does not issue authentication after waiting for lock',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final fixture = server.fixture(), peer = hub.peer();
      final repository = fixture.repository(coordination: peer);
      addTearDown(repository.close);
      final held = Completer<void>();
      hub.tail = held.future;
      final login = repository.passwordLogin('19900000001', 'fixture');
      final rejected = expectLater(login, throwsStateError);
      final logout = repository.logout();
      held.complete();
      await rejected;
      await logout;
      expect(server.sequence, 0);
      expect(repository.live, isNull);
    },
  );
  test(
    'failed shared store cannot leave a local runtime connected on logout',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final f = server.fixture(),
          repository = f.repository(coordination: hub.peer());
      addTearDown(repository.close);
      await repository.passwordLogin('19900000001', 'fixture');
      final shared = await f.metadata.readJson('session');
      (f.metadata as TabMetadata).fail = true;
      await expectLater(repository.logout(), throwsStateError);
      expect(repository.live, isNull);
      expect(f.gateways.single.disposed, true);
      (f.metadata as TabMetadata).fail = false;
      expect(await f.metadata.readJson('session'), shared);
      expect(server.revoked, isEmpty);
    },
  );
  test(
    'old password task restore does not remove a newer shared login',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final a = server.fixture(), b = server.fixture(uid: 'other');
      final first = a.repository(coordination: hub.peer());
      await first.passwordLogin('19900000001', 'fixture');
      final oldToken = server.valid;
      await first.close();
      await a.metadata.writeJson(
        'password-task',
        TenantPasswordTask.create(oldToken).toJson(),
      );
      final rb = b.repository(coordination: hub.peer());
      addTearDown(rb.close);
      await rb.passwordLogin('19900000002', 'fixture');
      final fresh = await b.metadata.readJson('session');
      final restored = a.repository(coordination: hub.peer());
      addTearDown(restored.close);
      expect(await restored.restoreSession(), false);
      expect(restored.passwordChangeProgress, isNotNull);
      expect(await b.metadata.readJson('session'), fresh);
      expect(await rb.live!.renewPlatformSession(), true);
    },
  );
  test(
    'offline restore cannot adopt same-account foreign login from own old business cache',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final a = server.fixture(), b = server.fixture();
      final first = a.repository(coordination: hub.peer());
      await first.passwordLogin('19900000001', 'fixture');
      await first.close();
      final rb = b.repository(coordination: hub.peer());
      addTearDown(rb.close);
      await rb.passwordLogin('19900000001', 'fixture');
      server.offline = true;
      final restored = a.repository(coordination: hub.peer());
      addTearDown(restored.close);
      expect(await restored.restoreSession(), false);
      expect(restored.live, isNull);
      expect(await b.metadata.readJson('session'), isNotNull);
    },
  );
  test(
    'peer rotation cannot extend own offline business expiry, including interrupted persistence',
    () async {
      final hub = Hub(), server = RotatingPlatform();
      final a = server.fixture(), b = server.fixture();
      a.tokenExpiry = DateTime.now().add(const Duration(minutes: 15));
      final first = a.repository(coordination: hub.peer());
      await first.passwordLogin('19900000001', 'fixture');
      await first.close();
      final rb = b.repository(coordination: hub.peer());
      addTearDown(rb.close);
      await rb.restoreSession();
      // Simulate a crash after metadata, before persisting the newer business JWT.
      final partial = Map<String, Object?>.from(
        await a.metadata.readJson('tab-session') as Map,
      );
      partial['expiresAt'] = b.tokenExpiry.toUtc().toIso8601String();
      await a.metadata.writeJson('tab-session', partial);
      server.offline = true;
      final restored = a.repository(coordination: hub.peer());
      addTearDown(restored.close);
      expect(await restored.restoreSession(), true);
      final expiry = DateTime.parse(
        RuntimeEndpoints.notificationContext['expiresAt'] as String,
      );
      expect(
        expiry.millisecondsSinceEpoch ~/ 1000,
        a.tokenExpiry.millisecondsSinceEpoch ~/ 1000,
      );
      expect(expiry.isBefore(b.tokenExpiry), true);
    },
  );
}
