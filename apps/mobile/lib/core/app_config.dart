abstract final class AppConfig {
  static const environment = String.fromEnvironment(
    'APP_ENV',
    defaultValue: 'development',
  );
  static const apiBaseUrl = String.fromEnvironment(
    'API_BASE_URL',
    defaultValue: '',
  );
  static const wsUrl = String.fromEnvironment('WS_URL', defaultValue: '');
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

  static bool get hasLiveBackend =>
      apiBaseUrl.trim().isNotEmpty && wsUrl.trim().isNotEmpty;

  static bool get isReleaseLike =>
      environment == 'production' || environment == 'staging';

  static bool get allowsDemo =>
      !isReleaseLike && (enableDemo || !hasLiveBackend);

  static void validate() {
    if (isReleaseLike && !hasLiveBackend) {
      throw StateError('$environment builds require API_BASE_URL and WS_URL');
    }
    if (isReleaseLike && enableDemo) {
      throw StateError('Demo mode is forbidden in $environment builds');
    }
    if (environment == 'production' &&
        (!apiBaseUrl.startsWith('https://') || !wsUrl.startsWith('wss://'))) {
      throw StateError('Production requires HTTPS API and WSS realtime URLs');
    }
    if (isReleaseLike &&
        getuiEnabled &&
        (getuiAppId.isEmpty || getuiAppKey.isEmpty || getuiAppSecret.isEmpty)) {
      throw StateError(
        'Getui builds require GETUI_APP_ID, GETUI_APP_KEY and GETUI_APP_SECRET',
      );
    }
  }
}
