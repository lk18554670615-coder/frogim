import 'app_config.dart';
import 'tenant_context.dart';

/// For presentation helpers that cannot own a repository. Requests themselves
/// always capture their immutable repository, never read this mutable pointer.
abstract final class RuntimeEndpoints {
  static Object? _owner;
  static TenantContext? _tenant;
  static String? _namespace;
  static String? get sessionNamespace => _namespace;
  static String? _localUserId;
  static String? _callSessionId;
  static int? _authVersion, _realmVersion;
  static DateTime? _expiresAt;
  static Map<String, Object?> get notificationContext => {
    if (_tenant != null) 'tenantId': _tenant!.tenantId,
    if (_tenant != null) 'assignmentVersion': _tenant!.assignmentVersion,
    if (_localUserId != null) 'localUserId': _localUserId,
    if (_callSessionId != null) 'callSessionId': _callSessionId,
    if (_authVersion != null) 'authVersion': _authVersion,
    if (_realmVersion != null) 'realmVersion': _realmVersion,
    if (_expiresAt != null) 'expiresAt': _expiresAt!.toIso8601String(),
  };
  static bool acceptsNotification(Map<String, Object?> data) {
    final expiry = data['expiresAt'];
    final parsed = expiry is String ? DateTime.tryParse(expiry) : null;
    final now = DateTime.now().toUtc();
    return _tenant != null &&
        _localUserId != null &&
        (_authVersion ?? 0) > 0 &&
        (_realmVersion ?? 0) > 0 &&
        _expiresAt != null &&
        now.isBefore(_expiresAt!) &&
        parsed != null &&
        now.isBefore(parsed) &&
        data['tenantId'] == _tenant!.tenantId &&
        data['assignmentVersion'] == _tenant!.assignmentVersion &&
        data['localUserId'] == _localUserId &&
        data['authVersion'] == _authVersion &&
        data['realmVersion'] == _realmVersion;
  }

  static String? scopedCacheKey(String? key) =>
      key == null || _namespace == null ? key : '$_namespace.$key';
  static String get businessBaseUrl =>
      _tenant?.httpBaseUrl.toString() ??
      (AppConfig.usesPlatformAuthentication ? '' : AppConfig.apiBaseUrl);
  static String get platformBaseUrl => AppConfig.usesPlatformAuthentication
      ? AppConfig.platformAuthUrl
      : AppConfig.apiBaseUrl;
  static void attach(
    Object owner,
    TenantContext tenant, {
    String? namespace,
    String? localUserId,
    int? authVersion,
    int? realmVersion,
    DateTime? expiresAt,
    String? callSessionId,
  }) {
    _owner = owner;
    _tenant = tenant;
    _namespace = namespace;
    _localUserId = localUserId;
    _authVersion = authVersion;
    _realmVersion = realmVersion;
    _expiresAt = expiresAt;
    _callSessionId = callSessionId;
  }

  static void clear(Object owner) {
    if (identical(_owner, owner)) {
      _owner = null;
      _tenant = null;
      _namespace = null;
      _localUserId = null;
      _authVersion = null;
      _realmVersion = null;
      _expiresAt = null;
      _callSessionId = null;
    }
  }
}
