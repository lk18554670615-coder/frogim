/// Platform paths are operator-configured. Enterprise and control URLs keep
/// their root-only contract; sharing an origin never shares credentials.
Uri trustedPlatformUrl(String value) {
  final uri = Uri.tryParse(value);
  if (uri == null ||
      value.trim() != value ||
      uri.scheme != 'https' ||
      uri.host.isEmpty ||
      uri.userInfo.isNotEmpty ||
      uri.hasQuery ||
      uri.hasFragment ||
      !const {'', '/', '/platform'}.contains(uri.path) ||
      uri.toString() != value ||
      (uri.path == '/platform' && !value.endsWith('/platform'))) {
    throw const FormatException('平台地址必须是 HTTPS 根地址或 /platform 入口');
  }
  return uri.path == '/' ? uri.replace(path: '') : uri;
}

/// Uri.resolve('/v2/...') discards /platform. Keep the configured prefix while
/// rejecting a route that could redirect a capability to another endpoint.
Uri serviceEndpoint(Uri base, String route) {
  final rawPath = Uri.decodeComponent(route.split('?').first);
  if (rawPath.split('/').any((s) => s == '..' || s == '.')) {
    throw const FormatException('无效的服务路径');
  }
  final relative = Uri.parse(route);
  if (!route.startsWith('/') ||
      route.startsWith('//') ||
      relative.hasScheme ||
      relative.hasAuthority ||
      relative.hasFragment ||
      route.contains('\\') ||
      relative.pathSegments.any((s) => s == '..' || s == '.')) {
    throw const FormatException('无效的服务路径');
  }
  final prefix = base.path == '/platform' ? '/platform' : '';
  return base.replace(
    path: '$prefix${relative.path}',
    query: relative.hasQuery ? relative.query : null,
  );
}
