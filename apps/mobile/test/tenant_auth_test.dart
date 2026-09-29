import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/tenant_context.dart';
import 'package:linli_im/data/tenant_auth_client.dart';

Map<String, Object?> contextJson({String tenant = 'a', int version = 1}) => {
  'tenantId': tenant,
  'displayName': '企业 $tenant',
  'httpBaseUrl': 'https://$tenant.example',
  'assignmentVersion': version,
  'configVersion': 1,
};

void main() {
  test(
    'registration polling and completion never leak poll token to enterprise',
    () async {
      final pending = TenantRegistrationPending('job_123', 'p' * 43);
      final requests = <http.Request>[];
      final client = TenantAuthClient(
        platformBaseUrl: 'https://platform.example',
        clientPlatform: 'web',
        client: MockClient((request) async {
          requests.add(request);
          if (request.url.host == 'platform.example') {
            expect(
              request.headers['authorization'],
              'Bearer ${pending.pollToken}',
            );
            expect(request.url.query, isEmpty);
            if (request.method == 'GET') {
              return http.Response(
                '{"status":"completed","errorCode":""}',
                200,
              );
            }
            expect(
              request.url.path,
              '/v2/auth/registration-jobs/job_123/complete',
            );
            return http.Response(
              jsonEncode({
                'tenantContext': contextJson(),
                'sessionTicket': 't' * 43,
                'refreshToken': 'r' * 43,
              }),
              200,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }
          expect(request.headers.containsKey('authorization'), isFalse);
          expect(jsonDecode(request.body), {'sessionTicket': 't' * 43});
          return http.Response(
            jsonEncode({
              'user': {'id': 'local'},
              'accessToken': 'business',
              'imSession': {'uid': 'local'},
            }),
            200,
          );
        }),
      );
      expect((await client.registrationStatus(pending)).completed, isTrue);
      final result = await client.completeRegistration(pending);
      expect(result.localUserId, 'local');
      expect(requests.map((r) => r.url.host), [
        'platform.example',
        'platform.example',
        'a.example',
      ]);
      client.close();
    },
  );
  test(
    'blocked task is actionable and malformed job IDs never reach network',
    () async {
      var calls = 0;
      final client = TenantAuthClient(
        platformBaseUrl: 'https://platform.example',
        clientPlatform: 'android',
        client: MockClient((request) async {
          calls++;
          return http.Response(
            '{"status":"blocked","errorCode":"INVITE_REQUIRED"}',
            200,
          );
        }),
      );
      final state = await client.registrationStatus(
        TenantRegistrationPending('job_ok', 'p' * 43),
      );
      expect(state.needsAttention, isTrue);
      expect(state.errorCode, 'INVITE_REQUIRED');
      await expectLater(
        client.registrationStatus(
          TenantRegistrationPending('../auth/login', 'p' * 43),
        ),
        throwsFormatException,
      );
      expect(calls, 1);
      client.close();
    },
  );
  test('platform outage does not fall back to any enterprise', () async {
    final requests = <Uri>[];
    final client = TenantAuthClient(
      platformBaseUrl: 'https://platform.example',
      clientPlatform: 'android',
      client: MockClient((request) async {
        requests.add(request.url);
        return http.Response('{"error":{"code":"PLATFORM_UNAVAILABLE"}}', 503);
      }),
    );
    await expectLater(
      client.passwordLogin('13812345678', 'password'),
      throwsA(isA<TenantAuthException>()),
    );
    expect(requests.map((uri) => uri.host), ['platform.example']);
    client.close();
  });

  test('pending registration is never treated as a successful login', () async {
    var calls = 0;
    final client = TenantAuthClient(
      platformBaseUrl: 'https://platform.example',
      clientPlatform: 'web',
      client: MockClient((request) async {
        calls++;
        return http.Response(
          '{"status":"pending","jobId":"job","pollToken":"opaque"}',
          202,
        );
      }),
    );
    await expectLater(
      client.register(
        phone: '13812345678',
        code: 'provider-code',
        password: 'password',
        name: 'user',
        enterpriseCode: 'ENTERPRISE',
        inviteCode: 'PERSONAL',
      ),
      throwsA(isA<TenantRegistrationPending>()),
    );
    expect(calls, 1);
    client.close();
  });
  test('tenant address validation and cache generation boundaries', () {
    final a = TenantContext.fromJson(contextJson());
    final b = TenantContext.fromJson(contextJson(tenant: 'b'));
    final moved = TenantContext.fromJson(contextJson(version: 3));
    expect(a.cacheNamespace('same_user'), isNot(b.cacheNamespace('same_user')));
    expect(
      a.cacheNamespace('same_user'),
      isNot(moved.cacheNamespace('same_user')),
    );
    for (final url in [
      'http://a.example',
      'https://a.example?b=1',
      'https://user@a.example',
      'https://a.example/path',
      '//a.example',
    ]) {
      expect(
        () => TenantContext.fromJson({...contextJson(), 'httpBaseUrl': url}),
        throwsFormatException,
      );
    }
  });
  test(
    'password goes only to platform and ticket only to enterprise',
    () async {
      final hosts = <String>[];
      final client = TenantAuthClient(
        platformBaseUrl: 'https://platform.example',
        clientPlatform: 'web',
        client: MockClient((request) async {
          hosts.add(request.url.host);
          expect(request.followRedirects, isFalse);
          expect(request.headers.containsKey('authorization'), isFalse);
          if (request.url.host == 'platform.example') {
            expect(jsonDecode(request.body)['password'], 'secret-password');
            return http.Response(
              jsonEncode({
                'tenantContext': contextJson(),
                'sessionTicket': 't' * 43,
                'refreshToken': 'r' * 43,
              }),
              200,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }
          expect(request.url.path, '/v2/auth/tenant-session');
          expect(jsonDecode(request.body), {'sessionTicket': 't' * 43});
          return http.Response(
            jsonEncode({
              'user': {'id': 'local'},
              'accessToken': 'business',
              'imSession': {'uid': 'local'},
            }),
            200,
            headers: {'content-type': 'application/json; charset=utf-8'},
          );
        }),
      );
      final session = await client.passwordLogin(
        '13812345678',
        'secret-password',
      );
      expect(hosts, ['platform.example', 'a.example']);
      expect(session.cacheNamespace, 'tenant.a.user.local.binding.1');
      client.close();
    },
  );
  test('late auth response cannot exchange a ticket after logout', () async {
    final response = Completer<http.Response>();
    var calls = 0;
    final client = TenantAuthClient(
      platformBaseUrl: 'https://platform.example',
      clientPlatform: 'ios',
      client: MockClient((request) {
        calls++;
        return response.future;
      }),
    );
    final future = client.passwordLogin('13812345678', 'password');
    final expected = expectLater(future, throwsStateError);
    client.epoch.invalidate();
    response.complete(
      http.Response(
        jsonEncode({
          'tenantContext': contextJson(),
          'sessionTicket': 't' * 43,
          'refreshToken': 'r' * 43,
        }),
        200,
        headers: {'content-type': 'application/json; charset=utf-8'},
      ),
    );
    await expected;
    expect(calls, 2); // Login plus revocation; never an enterprise exchange.
    client.close();
  });
  test('refresh never follows new tenant assignment', () async {
    var calls = 0;
    final client = TenantAuthClient(
      platformBaseUrl: 'https://platform.example',
      clientPlatform: 'web',
      client: MockClient((request) async {
        calls++;
        return http.Response(
          jsonEncode({
            'tenantContext': contextJson(tenant: 'b', version: 2),
            'sessionTicket': 't' * 43,
            'refreshToken': 'r' * 43,
          }),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        );
      }),
    );
    await expectLater(
      client.refresh('r' * 43, TenantContext.fromJson(contextJson())),
      throwsA(isA<TenantAuthException>()),
    );
    expect(calls, 2); // Changed assignment's unused session is revoked.
    client.close();
  });
}
