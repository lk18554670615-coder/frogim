import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:linli_im/core/client_upgrade.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/main.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test(
    'version check sends exact platform/version and a stable install id',
    () async {
      final requests = <http.Request>[];
      final client = MockClient((request) async {
        requests.add(request);
        return http.Response(
          jsonEncode({
            'data': {
              'platform': 'android',
              'currentVersion': '1.0.0',
              'minimumVersion': '1.1.0',
              'latestVersion': '1.3.0',
              'updateAvailable': true,
              'forceUpdate': true,
              'rolloutEligible': true,
              'rolloutPercentage': 20,
              'releaseNotes': '稳定性更新',
              'downloadUrl': 'https://downloads.example.com/app.apk',
              'publishedAt': '2026-08-11T00:00:00Z',
            },
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      });
      for (var index = 0; index < 2; index++) {
        final result = await ClientUpgradeService(
          client: client,
          apiBaseUrl: 'https://api.example.com/base/',
          platform: 'android',
          version: '1.0.0',
        ).check();
        expect(result?.forceUpdate, isTrue);
        expect(result?.latestVersion, '1.3.0');
      }
      expect(requests, hasLength(2));
      expect(requests.first.url.path, '/v2/config/version');
      expect(requests.first.url.queryParameters['platform'], 'android');
      expect(requests.first.url.queryParameters['version'], '1.0.0');
      final installId = requests.first.url.queryParameters['installId'];
      expect(installId, isNotNull);
      expect(installId!.length, greaterThanOrEqualTo(8));
      expect(requests.last.url.queryParameters['installId'], installId);
    },
  );

  test('empty backend skips platform plugins and network', () async {
    var called = false;
    final result = await ClientUpgradeService(
      client: MockClient((_) async {
        called = true;
        return http.Response('{}', 200);
      }),
      apiBaseUrl: '',
    ).check();
    expect(result, isNull);
    expect(called, isFalse);
  });

  testWidgets('forced update policy replaces login and home routes', (
    tester,
  ) async {
    const decision = ClientUpgradeDecision(
      platform: 'android',
      currentVersion: '1.0.0',
      minimumVersion: '2.0.0',
      latestVersion: '2.1.0',
      updateAvailable: true,
      forceUpdate: true,
      rolloutEligible: true,
      rolloutPercentage: 100,
      releaseNotes: '必须更新才能继续连接服务。',
      downloadUrl: '',
    );
    await tester.pumpWidget(
      LinliApp(
        repository: DemoImRepository(latency: Duration.zero),
        upgradeService: _FakeUpgradeService(decision),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('需要更新后继续使用'), findsOneWidget);
    expect(find.textContaining('最低支持版本 2.0.0'), findsOneWidget);
    expect(find.text('立即更新'), findsOneWidget);
    expect(find.text('手机号登录'), findsNothing);
  });
}

class _FakeUpgradeService extends ClientUpgradeService {
  _FakeUpgradeService(this.decision) : super(apiBaseUrl: '');
  final ClientUpgradeDecision decision;

  @override
  Future<ClientUpgradeDecision?> check() async => decision;
}
