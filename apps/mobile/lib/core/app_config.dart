import 'package:flutter/foundation.dart';

abstract final class AppConfig {
  static const environment = String.fromEnvironment(
    'APP_ENV',
    defaultValue: kReleaseMode ? 'production' : 'development',
  );
  static const apiBaseUrl = String.fromEnvironment(
    'API_BASE_URL',
    defaultValue: '',
  );
  static const enableDemo = bool.fromEnvironment(
    'ENABLE_DEMO',
    defaultValue: false,
  );
  static const mediaMaxBytes = int.fromEnvironment(
    'MEDIA_MAX_BYTES',
    defaultValue: 100 * 1024 * 1024,
  );
  static const getuiEnabled = bool.fromEnvironment(
    'GETUI_ENABLED',
    defaultValue: false,
  );
  static const getuiAppId = String.fromEnvironment('GETUI_APP_ID');
  static const getuiAppKey = String.fromEnvironment('GETUI_APP_KEY');
  static const getuiAppSecret = String.fromEnvironment('GETUI_APP_SECRET');
  static const termsUrl = String.fromEnvironment('TERMS_URL');
  static const privacyUrl = String.fromEnvironment('PRIVACY_URL');

  static bool get hasLiveBackend => apiBaseUrl.trim().isNotEmpty;

  static bool get isReleaseLike =>
      environment == 'production' || environment == 'staging';

  static bool get allowsDemo =>
      !isReleaseLike && (enableDemo || !hasLiveBackend);

  static void validate() => validateConfiguration(
    environment: environment,
    apiBaseUrl: apiBaseUrl,
    enableDemo: enableDemo,
    mediaMaxBytes: mediaMaxBytes,
    getuiEnabled: getuiEnabled,
    getuiAppId: getuiAppId,
    getuiAppKey: getuiAppKey,
    getuiAppSecret: getuiAppSecret,
    termsUrl: termsUrl,
    privacyUrl: privacyUrl,
  );

  static void validateConfiguration({
    required String environment,
    required String apiBaseUrl,
    required bool enableDemo,
    required int mediaMaxBytes,
    required bool getuiEnabled,
    required String getuiAppId,
    required String getuiAppKey,
    required String getuiAppSecret,
    required String termsUrl,
    required String privacyUrl,
  }) {
    const supportedEnvironments = {'development', 'staging', 'production'};
    if (!supportedEnvironments.contains(environment)) {
      throw StateError('Unsupported APP_ENV: $environment');
    }

    final hasApi = apiBaseUrl.trim().isNotEmpty;
    if (hasApi && !_validUrl(apiBaseUrl, const {'http', 'https'})) {
      throw StateError('API_BASE_URL must be a valid HTTP(S) URL');
    }

    final releaseLike = environment == 'production' || environment == 'staging';
    if (releaseLike && !hasApi) {
      throw StateError('$environment builds require API_BASE_URL');
    }
    if (releaseLike && enableDemo) {
      throw StateError('Demo mode is forbidden in $environment builds');
    }
    if (environment == 'production' &&
        !_validUrl(apiBaseUrl, const {'https'})) {
      throw StateError('Production requires an HTTPS API URL');
    }
    if (environment == 'production' &&
        (!_validUrl(termsUrl, const {'https'}) ||
            !_validUrl(privacyUrl, const {'https'}))) {
      throw StateError('Production requires HTTPS TERMS_URL and PRIVACY_URL');
    }
    if (environment != 'production') {
      for (final entry in {
        'TERMS_URL': termsUrl,
        'PRIVACY_URL': privacyUrl,
      }.entries) {
        if (entry.value.trim().isNotEmpty &&
            !_validUrl(entry.value, const {'http', 'https'})) {
          throw StateError('${entry.key} must be a valid HTTP(S) URL');
        }
      }
    }
    if (mediaMaxBytes <= 0) {
      throw StateError('MEDIA_MAX_BYTES must be greater than zero');
    }
    if (releaseLike &&
        getuiEnabled &&
        (getuiAppId.isEmpty || getuiAppKey.isEmpty || getuiAppSecret.isEmpty)) {
      throw StateError(
        'Getui builds require GETUI_APP_ID, GETUI_APP_KEY and GETUI_APP_SECRET',
      );
    }
  }

  static bool _validUrl(String value, Set<String> schemes) {
    final uri = Uri.tryParse(value.trim());
    return uri != null &&
        schemes.contains(uri.scheme.toLowerCase()) &&
        uri.host.isNotEmpty &&
        uri.userInfo.isEmpty &&
        !uri.hasFragment;
  }
}
