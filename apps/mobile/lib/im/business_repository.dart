import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;

import '../core/models.dart';
import 'business_features.dart';
import 'wukong_gateway_contract.dart';

class BusinessApiException implements Exception {
  const BusinessApiException(this.statusCode, this.code, this.message);

  final int statusCode;
  final String code;
  final String message;

  @override
  String toString() => message;
}

class BusinessRepository
    implements WukongDataSource, BusinessFeatureRepository {
  factory BusinessRepository({
    required String apiBaseUrl,
    required String platform,
    required String? Function() accessToken,
    Future<bool> Function()? refreshAccessToken,
    http.Client? client,
  }) => BusinessRepository._(
    apiBaseUrl,
    platform,
    accessToken,
    refreshAccessToken,
    client ?? http.Client(),
  );

  BusinessRepository._(
    this._apiBaseUrl,
    this._platform,
    this._accessToken,
    this._refreshAccessToken,
    this._client,
  );

  final String _apiBaseUrl;
  final String _platform;
  final String? Function() _accessToken;
  final Future<bool> Function()? _refreshAccessToken;
  final http.Client _client;

  Future<WukongSession> issueImSession() async {
    final data = await request('POST', '/v2/auth/im-session', {
      'platform': _platform,
    });
    final raw = data['imSession'];
    if (raw is! Map<String, Object?>) {
      throw const FormatException('business API did not return imSession');
    }
    return WukongSession.fromJson(raw);
  }

  Future<String> bindMedia(String mediaId, WukongChannel channel) async {
    final encoded = Uri.encodeComponent(mediaId);
    final data = await request('POST', '/v2/media/$encoded/bind', {
      'channelId': channel.id,
      'channelType': channel.type,
    });
    final url = data['url'] as String?;
    if (url == null || !Uri.parse(url).hasScheme) {
      throw const FormatException('media binding did not return a URL');
    }
    return url;
  }

  Future<String> mediaUrl(String mediaId) async {
    final encoded = Uri.encodeComponent(mediaId);
    final data = await request('GET', '/v2/media/$encoded/url');
    final url = data['url'] as String?;
    if (url == null || !Uri.parse(url).hasScheme) {
      throw const FormatException('media URL response is invalid');
    }
    return url;
  }

  Future<Map<String, Object?>> startMessageStream({
    required String conversationId,
    required String clientMsgNo,
    String initialText = '',
    int expireSeconds = 0,
  }) => request(
    'POST',
    '/v2/messages/conversations/${Uri.encodeComponent(conversationId)}/streams',
    {
      'clientMsgNo': clientMsgNo,
      'initialText': initialText,
      'expireSeconds': expireSeconds,
    },
  );

  Future<Map<String, Object?>> appendMessageStreamEvent({
    required String conversationId,
    required String clientMsgNo,
    required String eventId,
    required String eventType,
    String eventKey = 'main',
    Map<String, Object?> payload = const {},
  }) => request(
    'POST',
    '/v2/messages/conversations/${Uri.encodeComponent(conversationId)}/streams/${Uri.encodeComponent(clientMsgNo)}/events',
    {
      'eventId': eventId,
      'eventType': eventType,
      if (eventType != 'stream.finish') 'eventKey': eventKey,
      if (payload.isNotEmpty) 'payload': payload,
    },
  );

  Future<Map<String, Object?>> syncMessageStreamEvents({
    required String conversationId,
    required String clientMsgNo,
    int fromMsgEventSeq = 0,
    String eventKey = '',
    int limit = 200,
  }) {
    final path = Uri(
      path:
          '/v2/messages/conversations/${Uri.encodeComponent(conversationId)}/streams/${Uri.encodeComponent(clientMsgNo)}/events',
      queryParameters: {
        'fromMsgEventSeq': '$fromMsgEventSeq',
        'limit': '$limit',
        if (eventKey.isNotEmpty) 'eventKey': eventKey,
      },
    ).toString();
    return request('GET', path);
  }

  @override
  Future<List<BusinessChannelSummary>> businessChannels({
    int channelType = 0,
    String category = '',
    String parentId = '',
    int limit = 100,
  }) async {
    final query = <String, String>{
      if (channelType > 0) 'channelType': '$channelType',
      if (category.isNotEmpty) 'category': category,
      if (parentId.isNotEmpty) 'parentId': parentId,
      'limit': '$limit',
    };
    final path = Uri(
      path: '/v2/channels/business',
      queryParameters: query,
    ).toString();
    final data = await request('GET', path);
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => BusinessChannelSummary.fromJson(wukongObjectMap(item)))
        .toList();
  }

  @override
  Future<BusinessChannelSummary> createBusinessChannel({
    required int channelType,
    required String name,
    String parentId = '',
    String description = '',
    String visibility = 'public',
    String joinPolicy = 'open',
    String postingPolicy = 'members',
    int slowModeSeconds = 0,
  }) async {
    final data = await request('POST', '/v2/channels/business', {
      'channelType': channelType,
      'name': name,
      'parentId': parentId,
      'description': description,
      'visibility': visibility,
      'joinPolicy': joinPolicy,
      'postingPolicy': postingPolicy,
      'slowModeSeconds': slowModeSeconds,
      'metadata': <String, Object?>{},
    });
    return BusinessChannelSummary.fromJson(wukongObjectMap(data['item']));
  }

  @override
  Future<BusinessChannelSummary> businessChannel(
    String channelId,
    int channelType,
  ) async {
    final data = await request(
      'GET',
      '/v2/channels/business/${Uri.encodeComponent(channelId)}?channelType=$channelType',
    );
    return BusinessChannelSummary.fromJson(wukongObjectMap(data['item']));
  }

  @override
  Future<BusinessChannelSummary> updateBusinessChannel(
    String channelId,
    int channelType, {
    String? name,
    String? description,
    String? visibility,
    String? joinPolicy,
    String? postingPolicy,
    int? slowModeSeconds,
    bool? sendBan,
    bool? allowStranger,
  }) async {
    final data = await request(
      'PATCH',
      '/v2/channels/business/${Uri.encodeComponent(channelId)}?channelType=$channelType',
      {
        'name': ?name,
        'description': ?description,
        'visibility': ?visibility,
        'joinPolicy': ?joinPolicy,
        'postingPolicy': ?postingPolicy,
        'slowModeSeconds': ?slowModeSeconds,
        'sendBan': ?sendBan,
        'allowStranger': ?allowStranger,
      },
    );
    return BusinessChannelSummary.fromJson(wukongObjectMap(data['item']));
  }

  @override
  Future<List<BusinessChannelMemberSummary>> businessChannelMembers(
    String channelId,
    int channelType,
  ) async {
    final data = await request(
      'GET',
      '/v2/channels/business/${Uri.encodeComponent(channelId)}/members?channelType=$channelType&limit=200',
    );
    return (data['items'] as List<Object?>? ?? const [])
        .map(
          (item) =>
              BusinessChannelMemberSummary.fromJson(wukongObjectMap(item)),
        )
        .toList();
  }

  @override
  Future<void> addBusinessChannelMember(
    String channelId,
    int channelType,
    String userId, {
    DateTime? expiresAt,
  }) => request(
    'PUT',
    '/v2/channels/business/${Uri.encodeComponent(channelId)}/members/${Uri.encodeComponent(userId)}?channelType=$channelType',
    {'expiresAt': ?expiresAt?.toUtc().toIso8601String()},
  );

  @override
  Future<void> removeBusinessChannelMember(
    String channelId,
    int channelType,
    String userId,
  ) => request(
    'DELETE',
    '/v2/channels/business/${Uri.encodeComponent(channelId)}/members/${Uri.encodeComponent(userId)}?channelType=$channelType',
  );

  @override
  Future<void> updateBusinessChannelMember(
    String channelId,
    int channelType,
    String userId, {
    String? role,
    DateTime? mutedUntil,
    bool clearMute = false,
    DateTime? expiresAt,
    bool clearExpiry = false,
  }) => request(
    'PATCH',
    '/v2/channels/business/${Uri.encodeComponent(channelId)}/members/${Uri.encodeComponent(userId)}?channelType=$channelType',
    {
      'role': ?role,
      'mutedUntil': ?mutedUntil?.toUtc().toIso8601String(),
      if (clearMute) 'clearMute': true,
      'expiresAt': ?expiresAt?.toUtc().toIso8601String(),
      if (clearExpiry) 'clearExpiry': true,
    },
  );

  @override
  Future<void> setBusinessChannelAccess(
    String channelId,
    int channelType,
    String userId,
    String accessType,
    bool enabled, {
    String reason = '',
  }) => request(
    enabled ? 'PUT' : 'DELETE',
    '/v2/channels/business/${Uri.encodeComponent(channelId)}/access/${Uri.encodeComponent(accessType)}/${Uri.encodeComponent(userId)}?channelType=$channelType',
    enabled ? {'reason': reason} : null,
  );

  @override
  Future<List<BusinessChannelAccessSummary>> businessChannelAccess(
    String channelId,
    int channelType, {
    String accessType = '',
  }) async {
    final path = Uri(
      path: '/v2/channels/business/${Uri.encodeComponent(channelId)}/access',
      queryParameters: {
        'channelType': '$channelType',
        if (accessType.isNotEmpty) 'accessType': accessType,
        'limit': '200',
      },
    ).toString();
    final data = await request('GET', path);
    return (data['items'] as List<Object?>? ?? const [])
        .map(
          (item) =>
              BusinessChannelAccessSummary.fromJson(wukongObjectMap(item)),
        )
        .toList();
  }

  @override
  Future<void> subscribeBusinessChannel(
    String channelId,
    int channelType, {
    DateTime? expiresAt,
  }) => request(
    'POST',
    '/v2/channels/business/${Uri.encodeComponent(channelId)}/subscribe?channelType=$channelType',
    {'expiresAt': ?expiresAt?.toUtc().toIso8601String()},
  );

  @override
  Future<void> unsubscribeBusinessChannel(
    String channelId,
    int channelType,
  ) => request(
    'DELETE',
    '/v2/channels/business/${Uri.encodeComponent(channelId)}/subscription?channelType=$channelType',
  );

  @override
  Future<List<SupportSkillGroupSummary>> supportSkillGroups() async {
    final data = await request('GET', '/v2/support/skills');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => SupportSkillGroupSummary.fromJson(wukongObjectMap(item)))
        .toList();
  }

  @override
  Future<List<SupportAgentSummary>> supportAgents({
    String skillGroupId = '',
  }) async {
    final path = Uri(
      path: '/v2/support/agents',
      queryParameters: {
        if (skillGroupId.isNotEmpty) 'skillGroupId': skillGroupId,
      },
    ).toString();
    final data = await request('GET', path);
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => SupportAgentSummary.fromJson(wukongObjectMap(item)))
        .toList();
  }

  @override
  Future<SupportAgentSummary> setSupportAgentStatus(String status) async {
    final data = await request('PUT', '/v2/support/agent/status', {
      'status': status,
    });
    return SupportAgentSummary.fromJson(wukongObjectMap(data['agent']));
  }

  @override
  Future<List<SupportSessionSummary>> supportSessions({
    String status = '',
    String skillGroupId = '',
  }) async {
    final path = Uri(
      path: '/v2/support/sessions',
      queryParameters: {
        if (status.isNotEmpty) 'status': status,
        if (skillGroupId.isNotEmpty) 'skillGroupId': skillGroupId,
        'limit': '200',
      },
    ).toString();
    final data = await request('GET', path);
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => SupportSessionSummary.fromJson(wukongObjectMap(item)))
        .toList();
  }

  @override
  Future<SupportSessionSummary> createSupportSession({
    required String skillGroupId,
    String subject = '',
    int channelType = 10,
  }) async {
    final data = await request('POST', '/v2/support/sessions', {
      'skillGroupId': skillGroupId,
      'subject': subject,
      'channelType': channelType,
      'metadata': <String, Object?>{},
    });
    return SupportSessionSummary.fromJson(wukongObjectMap(data['item']));
  }

  Future<SupportSessionSummary> _supportAction(
    String sessionId,
    String action, [
    Object? body,
  ]) async {
    final data = await request(
      'POST',
      '/v2/support/sessions/${Uri.encodeComponent(sessionId)}/$action',
      body ?? const <String, Object?>{},
    );
    return SupportSessionSummary.fromJson(wukongObjectMap(data['item']));
  }

  @override
  Future<SupportSessionSummary> claimSupportSession(String sessionId) =>
      _supportAction(sessionId, 'claim');

  @override
  Future<SupportSessionSummary> transferSupportSession(
    String sessionId,
    String targetAgentId,
  ) => _supportAction(sessionId, 'transfer', {'targetAgentId': targetAgentId});

  @override
  Future<SupportSessionSummary> endSupportSession(String sessionId) =>
      _supportAction(sessionId, 'end');

  @override
  Future<SupportSessionSummary> rateSupportSession(
    String sessionId,
    int rating,
    String comment,
  ) => _supportAction(sessionId, 'rating', {
    'rating': rating,
    'comment': comment,
  });

  @override
  Future<BusinessMedia> uploadBusinessMedia(
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  }) async {
    onProgress?.call(0);
    final prepared = await request('POST', '/v2/media/presign', {
      'mime': upload.mimeType,
      'fileName': upload.fileName,
      'size': upload.bytes.length,
    });
    final uploadUrl = prepared['uploadUrl'] as String?;
    final mediaId = prepared['mediaId'] as String?;
    if (uploadUrl == null || mediaId == null) {
      throw const FormatException('media upload response is incomplete');
    }
    final uploadHeaders = <String, String>{};
    if (prepared['headers'] case final Map rawHeaders) {
      for (final entry in rawHeaders.entries) {
        uploadHeaders[entry.key.toString()] = entry.value.toString();
      }
    }
    final stream = http.StreamedRequest('PUT', Uri.parse(uploadUrl));
    stream.headers.addAll(uploadHeaders);
    stream.contentLength = upload.bytes.length;
    final responseFuture = _client
        .send(stream)
        .timeout(const Duration(seconds: 45));
    const chunkSize = 64 * 1024;
    for (var start = 0; start < upload.bytes.length; start += chunkSize) {
      final end = min(start + chunkSize, upload.bytes.length);
      stream.sink.add(upload.bytes.sublist(start, end));
      onProgress?.call(end / upload.bytes.length);
      await Future<void>.delayed(Duration.zero);
    }
    await stream.sink.close();
    final response = await responseFuture;
    await response.stream.drain<void>();
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw BusinessApiException(
        response.statusCode,
        'MEDIA_UPLOAD_FAILED',
        'media upload failed',
      );
    }
    await request(
      'POST',
      '/v2/media/${Uri.encodeComponent(mediaId)}/complete',
      {'checksum': sha256.convert(upload.bytes).toString()},
    );
    return BusinessMedia(
      id: mediaId,
      mime: upload.mimeType,
      url: await mediaUrl(mediaId),
    );
  }

  @override
  Future<MomentPage> moments({
    String authorId = '',
    String cursor = '',
    int limit = 20,
  }) async {
    final path = Uri(
      path: '/v2/moments',
      queryParameters: {
        if (authorId.isNotEmpty) 'authorId': authorId,
        if (cursor.isNotEmpty) 'cursor': cursor,
        'limit': '$limit',
      },
    ).toString();
    final data = await request('GET', path);
    final items = await Future.wait(
      (data['items'] as List<Object?>? ?? const []).map(_momentFromResponse),
    );
    return MomentPage(
      items: items,
      nextCursor: data['nextCursor'] as String? ?? '',
    );
  }

  @override
  Future<MomentSummary> createMoment({
    required String content,
    required String mediaKind,
    required List<String> mediaIds,
    required String visibility,
    List<String> visibleUserIds = const [],
    Map<String, Object?> location = const {},
  }) async {
    final data = await request('POST', '/v2/moments', {
      'content': content,
      'mediaKind': mediaKind,
      'mediaIds': mediaIds,
      'visibility': visibility,
      'visibleUserIds': visibleUserIds,
      'location': location,
    });
    return _momentFromResponse(data['item']);
  }

  @override
  Future<MomentSummary> setMomentLike(String momentId, bool active) async {
    final data = await request(
      active ? 'PUT' : 'DELETE',
      '/v2/moments/${Uri.encodeComponent(momentId)}/like',
    );
    return _momentFromResponse(data['item']);
  }

  @override
  Future<MomentCommentSummary> createMomentComment(
    String momentId,
    String content, {
    String parentId = '',
  }) async {
    final data = await request(
      'POST',
      '/v2/moments/${Uri.encodeComponent(momentId)}/comments',
      {'content': content, 'parentId': parentId},
    );
    return MomentCommentSummary.fromJson(wukongObjectMap(data['item']));
  }

  @override
  Future<void> deleteMoment(String momentId) => request(
    'DELETE',
    '/v2/moments/${Uri.encodeComponent(momentId)}',
  ).then((_) {});

  @override
  Future<void> deleteMomentComment(
    String momentId,
    String commentId,
  ) => request(
    'DELETE',
    '/v2/moments/${Uri.encodeComponent(momentId)}/comments/${Uri.encodeComponent(commentId)}',
  ).then((_) {});

  @override
  Future<List<MomentReminderSummary>> momentReminders({int limit = 100}) async {
    final data = await request('GET', '/v2/moments/reminders?limit=$limit');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => MomentReminderSummary.fromJson(wukongObjectMap(item)))
        .toList();
  }

  @override
  Future<void> markMomentRemindersRead(List<int> reminderIds) => request(
    'POST',
    '/v2/moments/reminders/read',
    {'reminderIds': reminderIds},
  ).then((_) {});

  @override
  Future<List<StickerCategorySummary>> stickerCategories() async {
    final data = await request('GET', '/v2/stickers/categories');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => StickerCategorySummary.fromJson(wukongObjectMap(item)))
        .toList();
  }

  @override
  Future<List<StickerPackSummary>> stickerPacks({
    String categoryId = '',
  }) async {
    final path = Uri(
      path: '/v2/stickers/packs',
      queryParameters: {if (categoryId.isNotEmpty) 'categoryId': categoryId},
    ).toString();
    final data = await request('GET', path);
    return Future.wait(
      (data['items'] as List<Object?>? ?? const []).map(
        _stickerPackFromResponse,
      ),
    );
  }

  @override
  Future<StickerPackSummary> stickerPack(String packId) async {
    final data = await request(
      'GET',
      '/v2/stickers/packs/${Uri.encodeComponent(packId)}',
    );
    return _stickerPackFromResponse(data['item']);
  }

  @override
  Future<void> setStickerPackFavorite(String packId, bool active) => request(
    active ? 'PUT' : 'DELETE',
    '/v2/stickers/packs/${Uri.encodeComponent(packId)}/favorite',
  ).then((_) {});

  @override
  Future<void> setStickerFavorite(String stickerId, bool active) => request(
    active ? 'PUT' : 'DELETE',
    '/v2/stickers/${Uri.encodeComponent(stickerId)}/favorite',
  ).then((_) {});

  @override
  Future<void> recordStickerUse(String stickerId) => request(
    'POST',
    '/v2/stickers/${Uri.encodeComponent(stickerId)}/used',
  ).then((_) {});

  @override
  Future<List<StickerItemSummary>> recentStickers({int limit = 50}) =>
      _stickerItems('/v2/stickers/recent?limit=$limit');

  @override
  Future<List<StickerItemSummary>> favoriteStickers({int limit = 50}) =>
      _stickerItems('/v2/stickers/favorites?limit=$limit');

  Future<List<StickerItemSummary>> _stickerItems(String path) async {
    final data = await request('GET', path);
    return Future.wait(
      (data['items'] as List<Object?>? ?? const []).map(_stickerFromResponse),
    );
  }

  Future<MomentSummary> _momentFromResponse(Object? value) async {
    final raw = wukongObjectMap(value);
    final media = raw['media'] as List<Object?>? ?? const [];
    raw['media'] = await Future.wait(
      media.map((item) async {
        final mapped = wukongObjectMap(item);
        final id = mapped['id'] as String? ?? '';
        if (id.isNotEmpty) mapped['url'] = await mediaUrl(id);
        return mapped;
      }),
    );
    return MomentSummary.fromJson(raw);
  }

  Future<StickerItemSummary> _stickerFromResponse(Object? value) async {
    final raw = wukongObjectMap(value);
    final mediaId = raw['mediaId'] as String? ?? '';
    if (mediaId.isNotEmpty) raw['url'] = await mediaUrl(mediaId);
    return StickerItemSummary.fromJson(raw);
  }

  Future<StickerPackSummary> _stickerPackFromResponse(Object? value) async {
    final raw = wukongObjectMap(value);
    final coverMediaId = raw['coverMediaId'] as String? ?? '';
    if (coverMediaId.isNotEmpty) {
      raw['coverUrl'] = await mediaUrl(coverMediaId);
    }
    raw['items'] = await Future.wait(
      (raw['items'] as List<Object?>? ?? const []).map((item) async {
        final mapped = wukongObjectMap(item);
        final mediaId = mapped['mediaId'] as String? ?? '';
        if (mediaId.isNotEmpty) mapped['url'] = await mediaUrl(mediaId);
        return mapped;
      }),
    );
    return StickerPackSummary.fromJson(raw);
  }

  @override
  Future<Map<String, Object?>> channelInfo(WukongChannel channel) async {
    final data = await request('POST', '/v2/im/datasource/channel', {
      'channelId': channel.id,
      'channelType': channel.type,
    });
    final item = data['item'];
    if (item is! Map) {
      throw const FormatException('channel datasource did not return an item');
    }
    return wukongObjectMap(item);
  }

  @override
  Future<List<Map<String, Object?>>> syncChannelMembers({
    required WukongChannel channel,
    required int version,
    required int limit,
  }) async {
    final data = await request('POST', '/v2/im/datasource/members', {
      'channelId': channel.id,
      'channelType': channel.type,
      'version': version,
      'limit': limit,
    });
    return (data['items'] as List<Object?>? ?? const [])
        .map(wukongObjectMap)
        .toList();
  }

  @override
  Future<List<Map<String, Object?>>> syncMessageExtras({
    required WukongChannel channel,
    required int version,
    required int limit,
  }) async {
    final data = await request('POST', '/v2/im/datasource/message-extras', {
      'channelId': channel.id,
      'channelType': channel.type,
      'version': version,
      'limit': limit,
    });
    return (data['items'] as List<Object?>? ?? const [])
        .map(wukongObjectMap)
        .toList();
  }

  @override
  Future<List<Map<String, Object?>>> syncReminders({
    required int version,
    required int limit,
  }) async {
    final data = await request('POST', '/v2/im/datasource/reminders', {
      'version': version,
      'limit': limit,
    });
    return (data['items'] as List<Object?>? ?? const [])
        .map(wukongObjectMap)
        .toList();
  }

  @override
  Future<void> doneReminders(List<int> reminderIds) async {
    await request('POST', '/v2/im/datasource/reminders/done', {
      'reminderIds': reminderIds,
    });
  }

  @override
  Future<List<Map<String, Object?>>> syncConversations({
    required int version,
    required String lastMsgSeqs,
    required int messageCount,
  }) async {
    final data = await request('POST', '/v2/im/datasource/conversations', {
      'version': version,
      'lastMsgSeqs': lastMsgSeqs,
      'msgCount': messageCount,
      'onlyUnread': false,
      'page': 0,
      'pageSize': 500,
    });
    return (data['items'] as List<Object?>? ?? const [])
        .whereType<Map<String, Object?>>()
        .toList();
  }

  @override
  Future<Map<String, Object?>> syncMessages({
    required WukongChannel channel,
    required int startMessageSeq,
    required int endMessageSeq,
    required int limit,
    required int pullMode,
  }) => request('POST', '/v2/im/datasource/messages', {
    'channelId': channel.id,
    'channelType': channel.type,
    'startMessageSeq': startMessageSeq,
    'endMessageSeq': endMessageSeq,
    'limit': limit,
    'pullMode': pullMode,
    'eventSummaryMode': 'full',
  });

  Future<Map<String, Object?>> request(
    String method,
    String path, [
    Object? body,
  ]) async {
    var response = await _send(method, path, body);
    if (response.statusCode == 401 && _refreshAccessToken != null) {
      final refreshed = await _refreshAccessToken();
      if (refreshed) response = await _send(method, path, body);
    }
    return _decode(response);
  }

  Future<http.Response> _send(String method, String path, Object? body) async {
    final token = _accessToken();
    final request = http.Request(method, Uri.parse('$_apiBaseUrl$path'));
    request.headers.addAll({
      'accept': 'application/json',
      'content-type': 'application/json',
      'x-client-platform': _platform,
      if (token != null && token.isNotEmpty) 'authorization': 'Bearer $token',
    });
    if (body != null) request.body = jsonEncode(body);
    final streamed = await _client
        .send(request)
        .timeout(const Duration(seconds: 10));
    return http.Response.fromStream(streamed);
  }

  Map<String, Object?> _decode(http.Response response) {
    if (response.statusCode == 204 || response.body.trim().isEmpty) {
      return const {};
    }
    final decoded = jsonDecode(response.body);
    if (response.statusCode < 200 || response.statusCode >= 300) {
      final error = decoded is Map<String, Object?>
          ? wukongObjectMap(decoded['error'])
          : const <String, Object?>{};
      throw BusinessApiException(
        response.statusCode,
        error['code'] as String? ?? 'HTTP_${response.statusCode}',
        error['message'] as String? ?? '业务服务暂时不可用',
      );
    }
    if (decoded is! Map<String, Object?>) {
      throw const FormatException('business API returned invalid JSON');
    }
    return decoded['data'] is Map<String, Object?>
        ? decoded['data']! as Map<String, Object?>
        : decoded;
  }

  void close() => _client.close();
}
