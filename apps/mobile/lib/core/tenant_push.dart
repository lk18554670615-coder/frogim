import 'dart:convert';

/// Platform policy contains capabilities only, never provider credentials.
class TenantPushCapabilities {
  const TenantPushCapabilities(this.providers, {this.webPushPublicKey});
  final Set<String> providers;
  final String? webPushPublicKey;

  factory TenantPushCapabilities.fromJson(Map<String, Object?> json) {
    final raw = json['providers'];
    if (json['enabled'] is! bool ||
        raw is! List ||
        raw.any((value) => value is! String)) {
      throw const FormatException('平台推送配置不完整');
    }
    String? publicKey;
    if (json['enabled'] == true && raw.contains('webpush')) {
      final key = json['webPushPublicKey'];
      if (json['webPushEnabled'] != true ||
          key is! String ||
          base64Url.decode(base64Url.normalize(key)).length != 65) {
        throw const FormatException('平台 Web Push 配置不完整');
      }
      publicKey = key;
    }
    return TenantPushCapabilities(
      Set.unmodifiable(
        json['enabled'] == true
            ? raw.whereType<String>().where(
                const {'getui', 'getui_voip', 'webpush'}.contains,
              )
            : const <String>[],
      ),
      webPushPublicKey: publicKey,
    );
  }
}

abstract interface class TenantPushRepository {
  Future<TenantPushCapabilities> pushCapabilities();
  Future<TenantPushBinding> registerBrowserPush({
    required String deviceId,
    required String subscription,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  });
}

class TenantPushBinding {
  TenantPushBinding(Map<String, Object?> value)
    : context = Map.unmodifiable(value) {
    final lease = value['leaseExpiresAt'];
    bool validId(Object? id) =>
        id is String &&
        RegExp(r'^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$').hasMatch(id);
    bool validVersion(Object? version) =>
        version is int && version > 0 && version <= 9007199254740991;
    if (!validId(value['pushBindingId']) ||
        !validId(value['tenantId']) ||
        !validId(value['localUserId']) ||
        !validVersion(value['pushBindingRevision']) ||
        !validVersion(value['assignmentVersion']) ||
        !validVersion(value['authVersion']) ||
        !validVersion(value['realmVersion']) ||
        lease is! String ||
        DateTime.tryParse(lease) == null ||
        !DateTime.now().toUtc().isBefore(DateTime.parse(lease))) {
      throw const FormatException('平台推送绑定有效期未确认');
    }
  }
  final Map<String, Object?> context;
}
