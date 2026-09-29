/// Trusted routing information returned by the platform authentication service.
/// This is not a user-editable server preference.
class TenantContext {
  const TenantContext({
    required this.tenantId,
    required this.displayName,
    required this.httpBaseUrl,
    required this.assignmentVersion,
    required this.configVersion,
  });

  final String tenantId;
  final String displayName;
  final Uri httpBaseUrl;
  final int assignmentVersion;
  final int configVersion;

  static final _identifier = RegExp(r'^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$');

  factory TenantContext.fromJson(Map<String, Object?> json) {
    final id = json['tenantId'];
    final name = json['displayName'];
    final rawUrl = json['httpBaseUrl'];
    final assignment = json['assignmentVersion'];
    final config = json['configVersion'];
    if (id is! String ||
        !_identifier.hasMatch(id) ||
        name is! String ||
        name.trim().isEmpty ||
        rawUrl is! String ||
        assignment is! int ||
        assignment < 1 ||
        config is! int ||
        config < 1) {
      throw const FormatException('企业登录上下文不完整');
    }
    final url = trustedBaseUrl(rawUrl);
    return TenantContext(
      tenantId: id,
      displayName: name,
      httpBaseUrl: url,
      assignmentVersion: assignment,
      configVersion: config,
    );
  }

  static Uri trustedBaseUrl(String value) {
    final uri = Uri.tryParse(value);
    if (uri == null ||
        value.trim() != value ||
        uri.scheme != 'https' ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        (uri.path.isNotEmpty && uri.path != '/')) {
      throw const FormatException('企业服务地址必须是可信 HTTPS 入口');
    }
    return uri.replace(path: '');
  }

  String cacheNamespace(String localUserId) {
    if (!_identifier.hasMatch(localUserId)) {
      throw const FormatException('企业本地用户标识无效');
    }
    return 'tenant.$tenantId.user.$localUserId.binding.$assignmentVersion';
  }

  bool sameAssignment(TenantContext other) =>
      tenantId == other.tenantId &&
      assignmentVersion == other.assignmentVersion &&
      httpBaseUrl == other.httpBaseUrl;

  Map<String, Object?> toJson() => {
    'tenantId': tenantId,
    'displayName': displayName,
    'httpBaseUrl': httpBaseUrl.toString(),
    'assignmentVersion': assignmentVersion,
    'configVersion': configVersion,
  };
}

/// Each asynchronous operation captures this fence before starting. Changing
/// account/enterprise invalidates old replies; it never redirects or replays
/// an old business mutation against the new enterprise.
class TenantEpoch {
  int _value = 0;
  int capture() => _value;
  void invalidate() => _value++;
  void check(int captured) {
    if (captured != _value) throw StateError('企业会话已变更，请重新操作');
  }
}
