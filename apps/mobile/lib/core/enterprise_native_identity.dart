import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'app_config.dart';

/// Persists only push routing identifiers, never credentials, for background call filtering.
Future<void> syncEnterpriseNativeIdentity({bool clear = false}) async {
  if (kIsWeb ||
      (defaultTargetPlatform != TargetPlatform.android &&
          defaultTargetPlatform != TargetPlatform.iOS)) {
    return;
  }
  try {
    await const MethodChannel(
      'top.hongjinghuanqiu.app/enterprise_identity',
    ).invokeMethod<void>('set', {
      'enabled': AppConfig.platformBaseUrl.isNotEmpty,
      'tenantId': clear ? '' : AppConfig.activeTenantId,
      'userId': clear ? '' : AppConfig.activeUserId,
      'epoch': clear ? 0 : AppConfig.activeEnterpriseEpoch,
    });
  } on MissingPluginException {
    // Host-side unit tests have no native engine.
  }
}
