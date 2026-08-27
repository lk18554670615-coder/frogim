import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:package_info_plus/package_info_plus.dart';

import 'app_config.dart';
import 'client_device.dart';

class ClientUpgradeDecision {
  const ClientUpgradeDecision({
    required this.platform,
    required this.currentVersion,
    required this.minimumVersion,
    required this.latestVersion,
    required this.updateAvailable,
    required this.forceUpdate,
    required this.rolloutEligible,
    required this.rolloutPercentage,
    required this.releaseNotes,
    required this.downloadUrl,
    this.publishedAt,
  });

  factory ClientUpgradeDecision.fromJson(Map<String, Object?> json) =>
      ClientUpgradeDecision(
        platform: json['platform'] as String? ?? '',
        currentVersion: json['currentVersion'] as String? ?? '',
        minimumVersion: json['minimumVersion'] as String? ?? '',
        latestVersion: json['latestVersion'] as String? ?? '',
        updateAvailable: json['updateAvailable'] as bool? ?? false,
        forceUpdate: json['forceUpdate'] as bool? ?? false,
        rolloutEligible: json['rolloutEligible'] as bool? ?? false,
        rolloutPercentage: (json['rolloutPercentage'] as num?)?.toInt() ?? 100,
        releaseNotes: json['releaseNotes'] as String? ?? '',
        downloadUrl: json['downloadUrl'] as String? ?? '',
        publishedAt: DateTime.tryParse(json['publishedAt'] as String? ?? ''),
      );

  final String platform;
  final String currentVersion;
  final String minimumVersion;
  final String latestVersion;
  final bool updateAvailable;
  final bool forceUpdate;
  final bool rolloutEligible;
  final int rolloutPercentage;
  final String releaseNotes;
  final String downloadUrl;
  final DateTime? publishedAt;

  String get policyKey =>
      '$platform:$currentVersion:$latestVersion:$forceUpdate:$publishedAt';
}

class ClientUpgradeException implements Exception {
  const ClientUpgradeException(this.message);
  final String message;

  @override
  String toString() => message;
}

class ClientUpgradeService {
  ClientUpgradeService({
    http.Client? client,
    String? apiBaseUrl,
    this.platform,
    this.version,
    this.installId,
  }) : _client = client ?? http.Client(),
       _apiBaseUrl = apiBaseUrl ?? AppConfig.apiBaseUrl;

  final http.Client _client;
  final String _apiBaseUrl;
  final String? platform;
  final String? version;
  final String? installId;

  Future<ClientUpgradeDecision?> check() async {
    final base = _apiBaseUrl.trim();
    if (base.isEmpty) return null;
    final clientPlatform = platform ?? _runtimePlatform();
    final clientVersion =
        version ?? (await PackageInfo.fromPlatform()).version.trim();
    final stableInstallId =
        installId ?? await ClientInstallationIdentity.getOrCreate();
    final uri = Uri.parse(base)
        .resolve('/v2/config/version')
        .replace(
          queryParameters: {
            'platform': clientPlatform,
            'version': clientVersion,
            'installId': stableInstallId,
          },
        );
    final response = await _client
        .get(uri, headers: const {'Accept': 'application/json'})
        .timeout(const Duration(seconds: 8));
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw ClientUpgradeException('版本检查失败（${response.statusCode}）');
    }
    final decoded = jsonDecode(response.body);
    if (decoded is! Map<String, Object?>) {
      throw const ClientUpgradeException('版本检查响应无效');
    }
    final raw = decoded['data'] ?? decoded;
    if (raw is! Map<String, Object?>) {
      throw const ClientUpgradeException('版本检查数据无效');
    }
    final decision = ClientUpgradeDecision.fromJson(raw);
    if (decision.platform != clientPlatform ||
        decision.currentVersion != clientVersion) {
      throw const ClientUpgradeException('版本检查响应与当前客户端不匹配');
    }
    return decision;
  }

  static String _runtimePlatform() {
    if (kIsWeb) return 'web';
    return switch (defaultTargetPlatform) {
      TargetPlatform.android => 'android',
      TargetPlatform.iOS => 'ios',
      TargetPlatform.macOS => 'macos',
      TargetPlatform.windows ||
      TargetPlatform.linux ||
      TargetPlatform.fuchsia => 'macos',
    };
  }
}
