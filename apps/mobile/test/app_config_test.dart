import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/app_config.dart';

void main() {
  void validate({
    String environment = 'production',
    String apiBaseUrl = 'https://chat.example.test',
    String wsUrl = 'wss://chat.example.test/v1/ws',
    bool enableDemo = false,
    int mediaMaxBytes = 100 * 1024 * 1024,
    bool getuiEnabled = false,
    String getuiAppId = '',
    String getuiAppKey = '',
    String getuiAppSecret = '',
    String termsUrl = 'https://legal.example.test/terms',
    String privacyUrl = 'https://legal.example.test/privacy',
  }) => AppConfig.validateConfiguration(
    environment: environment,
    apiBaseUrl: apiBaseUrl,
    wsUrl: wsUrl,
    enableDemo: enableDemo,
    mediaMaxBytes: mediaMaxBytes,
    getuiEnabled: getuiEnabled,
    getuiAppId: getuiAppId,
    getuiAppKey: getuiAppKey,
    getuiAppSecret: getuiAppSecret,
    termsUrl: termsUrl,
    privacyUrl: privacyUrl,
  );

  test('production accepts complete secure release configuration', () {
    expect(validate, returnsNormally);
  });

  test('production rejects demo mode and insecure endpoints', () {
    expect(() => validate(enableDemo: true), throwsStateError);
    expect(
      () => validate(apiBaseUrl: 'http://chat.example.test'),
      throwsStateError,
    );
    expect(
      () => validate(wsUrl: 'ws://chat.example.test/v1/ws'),
      throwsStateError,
    );
  });

  test('production rejects missing or insecure legal documents', () {
    expect(() => validate(termsUrl: ''), throwsStateError);
    expect(
      () => validate(privacyUrl: 'http://legal.example.test/privacy'),
      throwsStateError,
    );
  });

  test('release-like environments require both backend endpoints', () {
    expect(
      () => validate(
        environment: 'staging',
        apiBaseUrl: 'http://127.0.0.1:8080',
        wsUrl: '',
        termsUrl: '',
        privacyUrl: '',
      ),
      throwsStateError,
    );
  });

  test('unknown environments and invalid media limits are rejected', () {
    expect(() => validate(environment: 'prod'), throwsStateError);
    expect(() => validate(mediaMaxBytes: 0), throwsStateError);
  });
}
