import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/platform_endpoint.dart';
import 'package:linli_im/core/tenant_context.dart';
import 'package:linli_im/data/tenant_auth_client.dart';

void main() {
  test(
    'shared origin preserves platform prefix without relaxing tenant URLs',
    () {
      final base = trustedPlatformUrl('https://example.test/platform');
      expect(
        serviceEndpoint(base, '/v2/config/version?platform=ios').toString(),
        'https://example.test/platform/v2/config/version?platform=ios',
      );
      expect(
        () => TenantContext.trustedBaseUrl(base.toString()),
        throwsFormatException,
      );
      for (final value in [
        'https://example.test/platform/',
        'https://example.test/other',
        'https://example.test/platform?x=1',
        'https://evil@example.test/platform',
      ]) {
        expect(() => trustedPlatformUrl(value), throwsFormatException);
      }
      for (final route in [
        '//evil.test/v2/auth',
        '/v2/../auth',
        'https://evil.test',
      ]) {
        expect(() => serviceEndpoint(base, route), throwsFormatException);
      }
    },
  );
  test('same-origin registration capability stays on platform path', () async {
    final requests = <http.Request>[];
    final client = TenantAuthClient(
      platformBaseUrl: 'https://example.test/platform',
      clientPlatform: 'ios',
      client: MockClient((request) async {
        requests.add(request);
        if (request.url.path.startsWith('/platform/v2/')) {
          expect(request.headers['authorization'], 'Bearer ${'p' * 43}');
          return http.Response(
            jsonEncode({
              'tenantContext': {
                'tenantId': 'default',
                'displayName': 'Default',
                'httpBaseUrl': 'https://example.test',
                'assignmentVersion': 1,
                'configVersion': 1,
              },
              'sessionTicket': 't' * 43,
              'refreshToken': 'r' * 43,
            }),
            200,
          );
        }
        expect(request.url.path, '/v2/auth/tenant-session');
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
    final result = await client.completeRegistration(
      TenantRegistrationPending('job', 'p' * 43),
    );
    expect(result.localUserId, 'local');
    expect(requests.length, 2);
    client.close();
  });
}
