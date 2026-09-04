/// Fixed media URLs contain no credential. Native clients use a media-scoped
/// header; Web players use an HttpOnly cookie. Never authenticate external URLs.
class MediaAccess {
  Object? _owner;
  Uri? _base;
  String? _userId, _token;
  bool get enabled => _token?.isNotEmpty == true && _base != null;

  void configure({
    required Object owner,
    required String apiBaseUrl,
    required String userId,
    required String token,
  }) {
    _owner = owner;
    _base = Uri.parse(apiBaseUrl);
    _userId = userId;
    _token = token;
  }

  void clear(Object owner) {
    if (!identical(owner, _owner)) return;
    _owner = null;
    _base = null;
    _userId = null;
    _token = null;
  }

  String? url(String? mediaId, {bool cover = false}) {
    if (!enabled ||
        mediaId == null ||
        !RegExp(r'^[A-Za-z0-9_-]{1,128}$').hasMatch(mediaId)) {
      return null;
    }
    return _base!
        .resolve(
          '/v2/media/${Uri.encodeComponent(mediaId)}/${cover ? 'cover' : 'content'}',
        )
        .replace(queryParameters: {'viewer': _userId!})
        .toString();
  }

  String? source(String? mediaId, String? fallback) {
    // Preserve local previews and retry files before a completed upload.
    if (fallback != null &&
        fallback.isNotEmpty &&
        !fallback.startsWith('http://') &&
        !fallback.startsWith('https://') &&
        !fallback.startsWith('/v2/media/')) {
      return fallback;
    }
    return url(mediaId) ?? fallback;
  }

  bool owns(String source) {
    final uri = Uri.tryParse(source);
    final base = _base;
    if (!enabled ||
        uri == null ||
        base == null ||
        !uri.hasAuthority ||
        !const {'http', 'https'}.contains(uri.scheme) ||
        uri.origin != base.origin ||
        uri.userInfo.isNotEmpty ||
        uri.queryParameters['viewer'] != _userId) {
      return false;
    }
    return RegExp(r'^/v2/media/[^/]+/(content|cover)$').hasMatch(uri.path);
  }

  Map<String, String> headersFor(String source) =>
      owns(source) ? {'Authorization': 'Media $_token'} : const {};
}

final mediaAccess = MediaAccess();
