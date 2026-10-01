import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/core/enterprise_connection.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/core/app_config.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/im/business_repository.dart';
import 'support/fake_wukong_gateway.dart';

Map<String, Object?> grant(String tenant) => {
  'accessToken': 'directory-access',
  'refreshToken': 'directory-refresh',
  'enterprise': {
    'ticket': 'single-use',
    'user': {'id': 'u', 'assignmentVersion': 1},
    'tenant': {
      'id': tenant,
      'version': 1,
      'services': {
        'apiBaseUrl': 'https://same.example/$tenant',
        'imWsUrl': 'wss://same.example/$tenant/im',
        'imTcpUrl': 'tcp://same.example:5100',
        'callSignalUrl': 'wss://same.example/$tenant/livekit',
        'mediaBaseUrl': 'https://same.example/$tenant',
      },
    },
  },
};
http.Response json(Map<String, Object?> data) => http.Response(
  jsonEncode(data),
  200,
  headers: {'content-type': 'application/json'},
);
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    FlutterSecureStorage.setMockInitialValues({});
    SharedPreferences.setMockInitialValues({});
  });
  test(
    'same origin still separates platform and enterprise credentials and paths',
    () async {
      final requests = <http.Request>[];
      final client = MockClient((r) async {
        requests.add(r);
        if (r.url.path == '/platform/v2/auth/password-login') {
          return json(grant('a'));
        }
        if (r.url.path == '/a/v2/auth/enterprise-session') {
          return json({
            'accessToken': 'business-access',
            'refreshToken': 'business-refresh',
            'enterpriseEpoch': 1,
            'user': {'id': 'u', 'name': 'A资料', 'phone': '13800000001'},
          });
        }
        if (r.url.path == '/a/v2/users/me') {
          return json({'id': 'u', 'name': 'A资料', 'phone': '13800000001'});
        }
        return json({});
      });
      final repo = LiveImRepository(
        client: client,
        platformBaseUrl: 'https://same.example/platform',
        apiBaseUrl: 'https://unused.example',
        clientPlatform: 'web',
        wukongGateway: FakeWukongGateway(),
      );
      await repo.passwordLogin('13800000001', 'LocalUser123!');
      await repo.profile();
      expect(requests[0].headers['authorization'], isNull);
      expect(requests[1].headers['authorization'], isNull);
      expect(
        requests
            .firstWhere((r) => r.url.path == '/a/v2/users/me')
            .headers['authorization'],
        'Bearer business-access',
      );
      await repo.logout();
      expect(
        requests
            .firstWhere((r) => r.url.path == '/platform/v2/auth/logout')
            .headers['authorization'],
        'Bearer directory-access',
      );
      await repo.close();
    },
  );
  test(
    'renewal discovers B and cancels the old A request instead of replaying it',
    () async {
      final paths = <String>[];
      final events = <ImEventType>[];
      final switched = grant('b');
      ((switched['enterprise'] as Map)['user'] as Map)['assignmentVersion'] = 2;
      final repo = LiveImRepository(
        client: MockClient((r) async {
          paths.add(r.url.path);
          if (r.url.path == '/platform/v2/auth/password-login') {
            return json(grant('a'));
          }
          if (r.url.path == '/platform/v2/auth/refresh') return json(switched);
          if (r.url.path.endsWith('/auth/enterprise-session')) {
            return json({
              'accessToken': 'business-${r.url.path[1]}',
              'refreshToken': 'business-refresh',
              'enterpriseEpoch': 2,
              'user': {'id': 'u', 'name': r.url.path[1]},
            });
          }
          if (r.url.path == '/a/v2/users/me') {
            return http.Response('{"error":{"code":"UNAUTHENTICATED"}}', 401);
          }
          if (r.url.path == '/b/v2/users/me') {
            return json({'id': 'u', 'name': 'B资料'});
          }
          return json({});
        }),
        platformBaseUrl: 'https://same.example/platform',
        clientPlatform: 'web',
        wukongGateway: FakeWukongGateway(),
      );
      final subscription = repo.events.listen((e) => events.add(e.type));
      await repo.passwordLogin('13800000001', 'LocalUser123!');
      await expectLater(repo.profile(), throwsStateError);
      await Future<void>.delayed(Duration.zero);
      expect(
        events,
        containsAllInOrder([
          ImEventType.enterpriseSessionChanging,
          ImEventType.enterpriseSessionChanged,
        ]),
      );
      expect(paths.where((p) => p == '/b/v2/users/me'), isEmpty);
      expect(repo.currentUser?.name, 'b');
      expect((await repo.profile()).name, 'B资料');
      await subscription.cancel();
      await repo.close();
    },
  );

  test(
    'pending switch keeps directory identity for renewal after manual recovery',
    () async {
      var pending = true;
      var refreshes = 0;
      final switched = grant('b');
      ((switched['enterprise'] as Map)['user'] as Map)['assignmentVersion'] = 2;
      final repo = LiveImRepository(
        client: MockClient((r) async {
          if (r.url.path == '/platform/v2/auth/password-login') {
            return json(grant('a'));
          }
          if (r.url.path == '/platform/v2/auth/refresh') {
            refreshes++;
            return pending
                ? http.Response('{"error":{"code":"OPERATION_PENDING"}}', 409)
                : json(switched);
          }
          if (r.url.path.endsWith('/auth/enterprise-session')) {
            return json({
              'accessToken': 'business',
              'refreshToken': 'business-refresh',
              'user': {'id': 'u', 'name': r.url.path[1]},
            });
          }
          if (r.url.path == '/a/v2/users/me') {
            return http.Response('{"error":{"code":"UNAUTHENTICATED"}}', 401);
          }
          return json({});
        }),
        platformBaseUrl: 'https://same.example/platform',
        clientPlatform: 'web',
        wukongGateway: FakeWukongGateway(),
      );
      await repo.passwordLogin('13800000001', 'LocalUser123!');
      await expectLater(repo.profile(), throwsA(isA<ImApiException>()));
      expect(repo.currentUser?.name, 'a');
      pending = false;
      await expectLater(repo.profile(), throwsStateError);
      expect(refreshes, 2);
      expect(repo.currentUser?.name, 'b');
      await repo.close();
    },
  );

  test(
    'IM kick after admin switch discovers B without reclaiming an unchanged device session',
    () async {
      var changed = false;
      var exchangeCount = 0;
      final gateway = FakeWukongGateway();
      final completed = Completer<void>();
      final repo = LiveImRepository(
        client: MockClient((r) async {
          if (r.url.path == '/platform/v2/auth/password-login') {
            return json(grant('a'));
          }
          if (r.url.path == '/platform/v2/auth/refresh') {
            final result = grant(changed ? 'b' : 'a');
            ((result['enterprise'] as Map)['user']
                as Map)['assignmentVersion'] = changed
                ? 2
                : 1;
            return json(result);
          }
          if (r.url.path.endsWith('/auth/enterprise-session')) {
            exchangeCount++;
            final t = r.url.path[1];
            return json({
              'accessToken': 'business',
              'refreshToken': 'business-refresh',
              'user': {'id': 'u', 'name': t},
              'imSession': {
                'uid': 'u',
                'token': 'im-token',
                'deviceFlag': 1,
                'deviceLevel': 1,
                'tcpUrl': 'tcp://same.example:5100',
                'wsUrl': 'wss://same.example/$t/im',
                'sdk': 'wukong',
                'issuedAt': '2026-10-01T00:00:00Z',
              },
            });
          }
          return json({});
        }),
        platformBaseUrl: 'https://same.example/platform',
        clientPlatform: 'web',
        wukongGateway: gateway,
      );
      final subscription = repo.events.listen((e) {
        if (e.type == ImEventType.enterpriseSessionChanged ||
            e.type == ImEventType.sessionExpired) {
          if (!completed.isCompleted) completed.complete();
        }
      });
      await repo.passwordLogin('13800000001', 'LocalUser123!');
      await repo.connect();
      changed = true;
      gateway.setConnectionState(WukongConnectionState.kicked);
      await completed.future.timeout(const Duration(seconds: 3));
      expect(repo.currentUser?.name, 'b');
      expect(exchangeCount, 2);
      expect(gateway.logoutDisconnectCount, greaterThan(0));
      await subscription.cancel();
      final expired = Completer<void>();
      final expiration = repo.events.listen((e) {
        if (e.type == ImEventType.sessionExpired && !expired.isCompleted) {
          expired.complete();
        }
      });
      await repo.connect();
      gateway.setConnectionState(WukongConnectionState.kicked);
      await expired.future.timeout(const Duration(seconds: 3));
      expect(
        exchangeCount,
        2,
        reason: 'ordinary device kick must not reclaim the B session',
      );
      expect(repo.currentUser, isNull);
      await expiration.cancel();
      await repo.close();
    },
  );

  test(
    'same global user changing enterprise resets drafts and ignores late contacts and drafts',
    () async {
      final repo = _TransitionRepository();
      final controller = AppController(repo);
      await controller.passwordLogin('13800000001', 'StrongPass123!');
      await Future<void>.delayed(const Duration(milliseconds: 20));
      expect(controller.authenticated, isTrue);
      final initialGeneration = controller.authenticationGeneration;
      await controller.saveDraft('shared-id', 'A draft');
      final oldContacts = Completer<List<AppUser>>();
      final oldDraft = Completer<String>();
      repo.contactsGate = oldContacts;
      repo.draftGate = oldDraft;
      final contactsRead = controller.refreshContacts();
      final draftRead = controller.loadDraft('shared-id');
      final draftExpectation = expectLater(draftRead, throwsStateError);
      repo.transitionEvents.add(
        const ImEvent(type: ImEventType.enterpriseSessionChanging, payload: {}),
      );
      await Future<void>.delayed(Duration.zero);
      expect(controller.authenticated, isFalse);
      repo.contactsGate = null;
      repo.draftGate = null;
      repo.selectedUser = const AppUser(
        id: 'me',
        name: 'B资料',
        handle: 'b-user',
        presence: '',
      );
      AppConfig.activeTenantId = 'b';
      repo.transitionEvents.add(
        const ImEvent(
          type: ImEventType.enterpriseSessionChanged,
          payload: {'tenantId': 'b', 'userId': 'me'},
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));
      oldContacts.complete([
        const AppUser(id: 'old-a', name: '旧企业好友', handle: 'a', presence: ''),
      ]);
      oldDraft.complete('late A draft');
      await contactsRead;
      await draftExpectation;
      expect(controller.authenticated, isTrue);
      expect(controller.currentUser?.name, 'B资料');
      expect(
        controller.authenticationGeneration,
        greaterThan(initialGeneration),
      );
      expect(controller.contacts.any((u) => u.id == 'old-a'), isFalse);
      expect(controller.draftFor('shared-id'), isEmpty);
      controller.dispose();
      await repo.close();
      await repo.transitionEvents.close();
      AppConfig.activeTenantId = '';
    },
  );

  test('enterprise identifiers reject filesystem traversal', () {
    final bad = grant('../b');
    expect(
      () => EnterpriseConnection.fromGrant(
        bad['enterprise'] as Map<String, Object?>,
      ),
      throwsFormatException,
    );
  });
  test(
    'directory service configurations retain versions and reject incomplete or credential-bearing addresses',
    () {
      final good = Map<String, Object?>.from(grant('a')['enterprise'] as Map);
      final connection = EnterpriseConnection.fromGrant(good);
      final stored = connection.toStoredGrant();
      expect(stored['ticket'], isNot(connection.ticket));
      final restored = EnterpriseConnection.fromGrant(stored);
      expect(restored.assignmentVersion, 1);
      expect(restored.configVersion, 1);
      expect(restored.sameServices(connection), isTrue);
      for (final field in [
        'imWsUrl',
        'imTcpUrl',
        'callSignalUrl',
        'mediaBaseUrl',
      ]) {
        final missing = jsonDecode(jsonEncode(good)) as Map<String, Object?>;
        ((missing['tenant'] as Map)['services'] as Map).remove(field);
        expect(
          () => EnterpriseConnection.fromGrant(missing),
          throwsFormatException,
        );
      }
      final bad = jsonDecode(jsonEncode(good)) as Map<String, Object?>;
      ((bad['tenant'] as Map)['services'] as Map)['imWsUrl'] =
          'wss://password:secret@same.example/im';
      expect(() => EnterpriseConnection.fromGrant(bad), throwsFormatException);
    },
  );
  test('cache keeps distinct values for same user in A and B', () async {
    final store = SecureLocalStore();
    store.useIdentity('a:u');
    await store.writeJson('profile', {'name': 'A'});
    store.useIdentity('b:u');
    expect(await store.readJson('profile'), isNull);
    await store.writeJson('profile', {'name': 'B'});
    store.useIdentity('a:u');
    expect(await store.readJson('profile'), {'name': 'A'});
  });
  test(
    'late enterprise response cannot update a newly selected enterprise',
    () async {
      final pending = Completer<http.Response>();
      final repo = BusinessRepository(
        apiBaseUrl: 'https://a.example',
        platform: 'web',
        accessToken: () => 'a-token',
        client: MockClient((_) => pending.future),
      );
      final old = repo.request('GET', '/v2/users/me');
      final expectation = expectLater(old, throwsStateError);
      repo.useEnterprise('https://b.example');
      pending.complete(json({'name': 'old A'}));
      await expectation;
    },
  );
}

class _TransitionRepository extends DemoImRepository {
  _TransitionRepository() : super(latency: Duration.zero);
  final transitionEvents = StreamController<ImEvent>.broadcast();
  AppUser selectedUser = DemoImRepository.demoUser;
  Completer<List<AppUser>>? contactsGate;
  Completer<String>? draftGate;
  @override
  Stream<ImEvent> get events => transitionEvents.stream;
  @override
  AppUser? get currentUser => selectedUser;
  @override
  Future<List<AppUser>> contacts() => contactsGate?.future ?? Future.value([]);
  @override
  Future<String> readDraft(String conversationId) =>
      draftGate?.future ?? Future.value('');
}
