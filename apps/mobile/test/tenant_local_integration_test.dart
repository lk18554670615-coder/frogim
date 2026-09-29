import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/io_client.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/data/live_repository.dart';
import 'package:linli_im/data/platform_repository.dart';
import 'package:linli_im/data/secure_local_store.dart';
import 'package:linli_im/data/tenant_auth_client.dart';
import 'package:linli_im/core/tenant_password.dart';
import 'package:linli_im/core/client_upgrade.dart';
import 'package:linli_im/core/runtime_endpoints.dart';

import 'support/fake_wukong_gateway.dart';

class _LocalNetworkBinding extends AutomatedTestWidgetsFlutterBinding {
  @override
  bool get overrideHttpClient => false;
}

/// Real request reaches the local platform, but the first acceptance response
/// is deliberately lost. Recovery must not transmit either password again.
class _LosePasswordAcceptance extends http.BaseClient {
  _LosePasswordAcceptance(this.inner);
  final http.Client inner;
  bool lost = false;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final response = await inner.send(request);
    if (!lost && request.url.path == '/v2/auth/password-change') {
      lost = true;
      await response.stream.drain<void>();
      throw TimeoutException('Local test: acceptance lost');
    }
    return response;
  }

  @override
  void close() => inner.close();
}

void main() {
  _LocalNetworkBinding();
  final root = Platform.environment['TENANCY_LOCAL_TEST_ROOT'];
  final passwordTest =
      Platform.environment['TENANCY_LOCAL_PASSWORD_TEST'] == '1';
  test(
    'real TLS client upgrade queries platform for all four targets, never enterprise',
    () async {
      final trust = SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates('$root/browser-ca.pem');
      final client = IOClient(HttpClient(context: trust));
      try {
        for (final target in ['android', 'ios', 'web', 'macos']) {
          final decision = await ClientUpgradeService(
            client: client,
            apiBaseUrl: 'https://127.0.0.1:18443',
            platform: target,
            version: '1.0.12',
            installId: 'local-readonly-upgrade-check',
          ).check();
          expect(decision, isNotNull);
          expect(decision!.platform, target);
          expect(decision.currentVersion, '1.0.12');
        }
        await expectLater(
          ClientUpgradeService(
            client: client,
            apiBaseUrl: 'https://127.0.0.1:18444',
            platform: 'web',
            version: '1.0.12',
            installId: 'local-readonly-upgrade-check',
          ).check(),
          throwsA(
            isA<ClientUpgradeException>().having(
              (error) => error.message,
              'enterprise ownership rejected',
              contains('409'),
            ),
          ),
        );
      } finally {
        client.close();
      }
    },
    skip: root == null
        ? 'Set TENANCY_LOCAL_TEST_ROOT to the isolated local stack'
        : false,
  );
  test(
    'real local password acceptance lost, restart, reconcile, relogin same identity',
    () async {
      SharedPreferences.setMockInitialValues({});
      FlutterSecureStorage.setMockInitialValues({});
      final config =
          jsonDecode(await File('$root/credentials.json').readAsString())
              as Map;
      final trust = SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates('$root/browser-ca.pem');
      IOClient client() => IOClient(HttpClient(context: trust));
      PlatformImRepository repository({bool lose = false}) =>
          PlatformImRepository(
            auth: TenantAuthClient(
              platformBaseUrl: 'https://127.0.0.1:18443',
              clientPlatform: 'web',
              client: lose ? _LosePasswordAcceptance(client()) : client(),
            ),
            businessFactory: (context, namespace, refresh) => LiveImRepository(
              apiBaseUrl: context.httpBaseUrl.toString(),
              clientPlatform: 'web',
              client: client(),
              cacheNamespace: namespace,
              store: SecureLocalStore(namespace: namespace),
              externalSessionRefresh: refresh,
              wukongGateway: FakeWukongGateway(),
            ),
          );
      // Dedicated local fixture from -verify-admin-create, not the regular account.
      // Keep its existing random password unchanged; no password is printed.
      final secret = config['UserPassword'] as String;
      final first = repository(lose: true);
      late String id;
      try {
        await first.authPolicy();
        id = (await first.passwordLogin('19900000002', secret)).id;
        await first.changeLoginPassword(secret, secret);
        expect(
          first.passwordChangeProgress,
          TenantPasswordProgress.unconfirmed,
        );
        expect(first.live, isNull);
      } finally {
        await first.close();
      }
      final restored = repository();
      try {
        expect(await restored.restoreSession(), isFalse);
        for (var i = 0; i < 30; i++) {
          await restored.checkPasswordChange();
          if (restored.passwordChangeProgress ==
              TenantPasswordProgress.completed) {
            break;
          }
          await Future<void>.delayed(const Duration(milliseconds: 500));
        }
        expect(
          restored.passwordChangeProgress,
          TenantPasswordProgress.completed,
        );
        expect((await restored.passwordLogin('19900000002', secret)).id, id);
        expect(restored.passwordChangeProgress, isNull);
        await restored.logout();
      } finally {
        await restored.close();
      }
    },
    skip: root == null || !passwordTest
        ? 'Opt in with TENANCY_LOCAL_PASSWORD_TEST=1 for the dedicated loopback fixture'
        : false,
  );
  test(
    'real TLS platform/default enterprise login, direct profile, restore and logout',
    () async {
      SharedPreferences.setMockInitialValues({});
      FlutterSecureStorage.setMockInitialValues({});
      final config =
          jsonDecode(await File('$root/credentials.json').readAsString())
              as Map;
      final trust = SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates('$root/browser-ca.pem');
      // No badCertificateCallback or operating-system trust modification.
      IOClient client() => IOClient(HttpClient(context: trust));
      PlatformImRepository repository() => PlatformImRepository(
        auth: TenantAuthClient(
          platformBaseUrl: 'https://127.0.0.1:18443',
          clientPlatform: 'web',
          client: client(),
        ),
        businessFactory: (context, namespace, refresh) => LiveImRepository(
          apiBaseUrl: context.httpBaseUrl.toString(),
          clientPlatform: 'web',
          client: client(),
          cacheNamespace: namespace,
          store: SecureLocalStore(namespace: namespace),
          externalSessionRefresh: refresh,
          wukongGateway: FakeWukongGateway(),
        ),
      );
      final first = repository();
      final policy = await first.authPolicy();
      expect(policy.otpLoginEnabled, isFalse);
      final user = await first.passwordLogin(
        config['UserPhone'] as String,
        config['UserPassword'] as String,
      );
      expect(user.id, isNotEmpty);
      expect((await first.pushCapabilities()).providers, isEmpty);
      final notificationScope = RuntimeEndpoints.notificationContext;
      expect(notificationScope['authVersion'], isA<int>());
      expect(notificationScope['realmVersion'], isA<int>());
      expect(RuntimeEndpoints.acceptsNotification(notificationScope), isTrue);
      await expectLater(
        first.registerDevice(
          deviceId: 'no-web-push',
          platform: 'web',
          provider: 'webpush',
          pushToken: 'not-a-real-token',
          notificationsEnabled: true,
          previewEnabled: true,
          soundEnabled: true,
          vibrationEnabled: true,
        ),
        throwsA(isA<ImApiException>()),
      );
      expect((await first.profile()).id, user.id);
      await first.close();
      final second = repository();
      try {
        expect(await second.restoreSession(), isTrue);
        expect((await second.profile()).id, user.id);
        await second.logout();
        expect(second.currentUser, isNull);
        expect(
          RuntimeEndpoints.acceptsNotification(notificationScope),
          isFalse,
        );
      } finally {
        await second.close();
      }
    },
    skip: root == null
        ? 'Set TENANCY_LOCAL_TEST_ROOT to the isolated local stack; never use production credentials'
        : false,
  );
}
