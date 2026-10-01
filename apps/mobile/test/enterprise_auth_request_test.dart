import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:linli_im/data/live_repository.dart';
import 'support/fake_wukong_gateway.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() {
    FlutterSecureStorage.setMockInitialValues({});
    SharedPreferences.setMockInitialValues({});
  });
  test(
    'platform login omits invitation and enterprise code while registration keeps optional wire compatibility',
    () async {
      final requests = <http.Request>[];
      final repo = LiveImRepository(
        platformBaseUrl: 'https://same.example/platform',
        apiBaseUrl: 'https://unused.example',
        clientPlatform: 'web',
        wukongGateway: FakeWukongGateway(),
        client: MockClient((r) async {
          requests.add(r);
          return http.Response(
            jsonEncode({
              'error': {'code': 'INVALID_ENTERPRISE_CODE'},
            }),
            400,
            headers: {'content-type': 'application/json'},
          );
        }),
      );
      await expectLater(
        repo.login('13800000008', '123456', inviteCode: 'PERSONAL'),
        throwsA(isA<ImApiException>()),
      );
      await expectLater(
        repo.passwordLogin('13800000008', 'LocalUser123!'),
        throwsA(isA<ImApiException>()),
      );
      for (final r in requests) {
        expect(jsonDecode(r.body).containsKey('inviteCode'), isFalse);
        expect(jsonDecode(r.body).containsKey('enterpriseCode'), isFalse);
        expect(r.url.path, startsWith('/platform/v2/auth/'));
      }
      for (final code in ['', ' b ']) {
        await expectLater(
          repo.register(
            phone: '13800000008',
            code: '123456',
            password: 'LocalUser123!',
            name: '测试',
            inviteCode: code,
          ),
          throwsA(isA<ImApiException>()),
        );
        final body = jsonDecode(requests.last.body) as Map;
        expect(requests.last.url.path, '/platform/v2/auth/register');
        expect(body['inviteCode'], code.isEmpty ? null : 'b');
      }
    },
  );
  test('enterprise and unavailable-default errors are localized', () async {
    for (final code in [
      'INVALID_ENTERPRISE_CODE',
      'DEFAULT_ENTERPRISE_UNAVAILABLE',
    ]) {
      final repo = LiveImRepository(
        platformBaseUrl: 'https://same.example/platform',
        apiBaseUrl: 'https://unused.example',
        clientPlatform: 'web',
        wukongGateway: FakeWukongGateway(),
        client: MockClient(
          (r) async => http.Response(
            jsonEncode({
              'error': {'code': code},
            }),
            code.startsWith('DEFAULT') ? 503 : 400,
            headers: {'content-type': 'application/json'},
          ),
        ),
      );
      await expectLater(
        repo.register(
          phone: '13800000008',
          code: '123456',
          password: 'LocalUser123!',
          name: '测试',
        ),
        throwsA(
          isA<ImApiException>().having(
            (e) => e.message,
            'message',
            contains(code.startsWith('DEFAULT') ? '默认企业暂不可用' : '企业码无效'),
          ),
        ),
      );
    }
  });
}
