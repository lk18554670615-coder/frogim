import 'dart:convert';
import 'dart:math';

import 'package:device_info_plus/device_info_plus.dart';
import 'package:flutter/foundation.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

class ClientInstallationIdentity {
  static const storageKey = 'linli_im.install_id.v1';

  static Future<String> getOrCreate() async {
    final preferences = await SharedPreferences.getInstance();
    final existing = preferences.getString(storageKey);
    if (existing != null && existing.length >= 8) return existing;
    final random = Random.secure();
    final bytes = List<int>.generate(24, (_) => random.nextInt(256));
    final created = base64UrlEncode(bytes).replaceAll('=', '');
    await preferences.setString(storageKey, created);
    return created;
  }
}

class ClientDeviceReport {
  const ClientDeviceReport({
    required this.installationId,
    required this.platform,
    required this.deviceName,
    required this.deviceModel,
    required this.osVersion,
    required this.appVersion,
  });

  final String installationId;
  final String platform;
  final String deviceName;
  final String deviceModel;
  final String osVersion;
  final String appVersion;
}

class ClientDeviceReporter {
  static Future<ClientDeviceReport> collect({
    DeviceInfoPlugin? deviceInfo,
    PackageInfo? packageInfo,
    String? installationId,
    String? platformOverride,
    Future<Map<String, String>> Function(String platform)? deviceDetails,
  }) async {
    final plugin = deviceInfo ?? DeviceInfoPlugin();
    final package = packageInfo ?? await PackageInfo.fromPlatform();
    final id = installationId ?? await ClientInstallationIdentity.getOrCreate();
    final platform = platformOverride ?? _runtimePlatform();
    final details = await (deviceDetails == null
        ? _pluginDetails(plugin, platform)
        : deviceDetails(platform));
    return ClientDeviceReport(
      installationId: id,
      platform: platform,
      deviceName: details['deviceName'] ?? '未提供',
      deviceModel: details['deviceModel'] ?? '未提供',
      osVersion: details['osVersion'] ?? '未提供',
      appVersion: package.version,
    );
  }

  static String _runtimePlatform() {
    if (kIsWeb) return 'web';
    return switch (defaultTargetPlatform) {
      TargetPlatform.android => 'android',
      TargetPlatform.iOS => 'ios',
      TargetPlatform.macOS => 'macos',
      TargetPlatform.windows ||
      TargetPlatform.linux ||
      TargetPlatform.fuchsia => throw UnsupportedError('当前客户端平台不在正式发布矩阵中'),
    };
  }

  static Future<Map<String, String>> _pluginDetails(
    DeviceInfoPlugin plugin,
    String platform,
  ) async {
    if (platform == 'web') {
      final info = await plugin.webBrowserInfo;
      return {
        'deviceName': info.browserName.name,
        'deviceModel': info.platform ?? 'Web 浏览器',
        'osVersion': info.userAgent ?? '未知',
      };
    }
    switch (platform) {
      case 'android':
        final info = await plugin.androidInfo;
        return {
          'deviceName': '${info.brand} ${info.model}'.trim(),
          'deviceModel': info.device,
          'osVersion':
              'Android ${info.version.release} (API ${info.version.sdkInt})',
        };
      case 'ios':
        final info = await plugin.iosInfo;
        return {
          'deviceName': info.name,
          'deviceModel': '${info.model} (${info.utsname.machine})',
          'osVersion': '${info.systemName} ${info.systemVersion}',
        };
      case 'macos':
        final info = await plugin.macOsInfo;
        return {
          'deviceName': info.computerName,
          'deviceModel': info.model,
          'osVersion': info.osRelease,
        };
      default:
        throw UnsupportedError('当前客户端平台不在正式发布矩阵中');
    }
  }
}
