import 'app_config.dart';

/// Only the authenticated directory may choose service destinations. Restoring
/// a session must preserve the full configuration and both versions.
class EnterpriseConnection {
  EnterpriseConnection._({
    required this.tenantId,
    required this.userId,
    required this.assignmentVersion,
    required this.configVersion,
    required this.ticket,
    required this.apiBaseUrl,
    required this.imWsUrl,
    required this.imTcpUrl,
    required this.callSignalUrl,
    required this.mediaBaseUrl,
  });

  factory EnterpriseConnection.fromGrant(Map<String, Object?> grant) {
    try {
      final tenant = grant['tenant'] as Map;
      final user = grant['user'] as Map;
      final services = tenant['services'] as Map;
      final connection = EnterpriseConnection._(
        tenantId: tenant['id'] as String,
        userId: user['id'] as String,
        assignmentVersion: (user['assignmentVersion'] as num).toInt(),
        configVersion: (tenant['version'] as num).toInt(),
        ticket: grant['ticket'] as String,
        apiBaseUrl: _service(services['apiBaseUrl'], 'http'),
        imWsUrl: _service(services['imWsUrl'], 'ws'),
        imTcpUrl: _service(services['imTcpUrl'], 'tcp'),
        callSignalUrl: _service(services['callSignalUrl'], 'ws'),
        mediaBaseUrl: _service(services['mediaBaseUrl'], 'http'),
      );
      if (!RegExp(r'^[a-zA-Z0-9_-]{1,64}$').hasMatch(connection.tenantId) ||
          connection.userId.isEmpty ||
          connection.ticket.isEmpty ||
          connection.assignmentVersion < 1 ||
          connection.configVersion < 1) {
        throw const FormatException('企业连接配置无效');
      }
      return connection;
    } on FormatException {
      rethrow;
    } catch (_) {
      throw const FormatException('企业连接配置不完整');
    }
  }

  static String _service(Object? value, String kind) {
    if (value is! String) throw const FormatException('企业服务地址缺失');
    final uri = Uri.tryParse(value);
    if (uri == null ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        uri.pathSegments.any((part) => part == '.' || part == '..')) {
      throw const FormatException('企业服务地址无效');
    }
    final local =
        AppConfig.environment == 'development' &&
        const {'localhost', '127.0.0.1', '10.0.2.2'}.contains(uri.host);
    final valid = switch (kind) {
      'ws' => uri.scheme == 'wss' || (local && uri.scheme == 'ws'),
      'tcp' =>
        const {'tcp', 'tls'}.contains(uri.scheme) &&
            uri.hasPort &&
            (uri.path.isEmpty || uri.path == '/'),
      _ => uri.scheme == 'https' || (local && uri.scheme == 'http'),
    };
    if (!valid) throw const FormatException('企业服务协议无效');
    return value.replaceFirst(RegExp(r'/$'), '');
  }

  final String tenantId,
      userId,
      ticket,
      apiBaseUrl,
      imWsUrl,
      imTcpUrl,
      callSignalUrl,
      mediaBaseUrl;
  final int assignmentVersion, configVersion;

  bool sameServices(EnterpriseConnection other) =>
      apiBaseUrl == other.apiBaseUrl &&
      imWsUrl == other.imWsUrl &&
      imTcpUrl == other.imTcpUrl &&
      callSignalUrl == other.callSignalUrl &&
      mediaBaseUrl == other.mediaBaseUrl;

  /// Consumed login tickets are never persisted or reused after a restart.
  Map<String, Object?> toStoredGrant() => {
    'ticket': 'stored',
    'user': {'id': userId, 'assignmentVersion': assignmentVersion},
    'tenant': {
      'id': tenantId,
      'version': configVersion,
      'services': {
        'apiBaseUrl': apiBaseUrl,
        'imWsUrl': imWsUrl,
        'imTcpUrl': imTcpUrl,
        'callSignalUrl': callSignalUrl,
        'mediaBaseUrl': mediaBaseUrl,
      },
    },
  };
}
