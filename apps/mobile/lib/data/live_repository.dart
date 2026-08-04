import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;
import 'package:web_socket_channel/web_socket_channel.dart';

import '../calls/call_models.dart';
import '../calls/call_repository.dart';
import '../core/app_config.dart';
import '../core/models.dart';
import 'demo_repository.dart';
import 'im_repository.dart';
import 'secure_local_store.dart';

class ImApiException implements Exception {
  const ImApiException({
    required this.statusCode,
    required this.code,
    required this.message,
  });

  final int statusCode;
  final String code;
  final String message;

  @override
  String toString() => message;
}

class LiveImRepository implements ImRepository, CallRepository {
  LiveImRepository({
    http.Client? client,
    SecureLocalStore? store,
    String? apiBaseUrl,
    String? wsUrl,
  }) : _client = client ?? http.Client(),
       _store = store ?? SecureLocalStore(),
       _apiBaseUrl = apiBaseUrl ?? AppConfig.apiBaseUrl,
       _wsUrl = wsUrl ?? AppConfig.wsUrl;

  final http.Client _client;
  final SecureLocalStore _store;
  final String _apiBaseUrl;
  final String _wsUrl;
  final _connection = StreamController<bool>.broadcast();
  final _events = StreamController<ImEvent>.broadcast();
  final _callEvents = StreamController<CallSignalEvent>.broadcast();
  WebSocketChannel? _channel;
  StreamSubscription<Object?>? _socketSubscription;
  Timer? _retryTimer;
  final Map<String, List<Timer>> _pendingCallSignalRetries = {};
  final Map<String, DateTime> _receivedCallSignals = {};
  String? _token;
  String? _refreshToken;
  String? _userId;
  AppUser? _me;
  int _syncCursor = 0;
  int _retry = 0;
  bool _closed = false;
  bool _connecting = false;
  Future<bool>? _refreshInFlight;
  Future<void>? _authReconnectInFlight;

  @override
  bool get isDemo => false;

  @override
  bool get supportsDemo => false;

  @override
  AppUser? get currentUser => _me;

  @override
  Stream<bool> get connectionChanges => _connection.stream;

  @override
  Stream<ImEvent> get events => _events.stream;

  @override
  Stream<CallSignalEvent> get callEvents => _callEvents.stream;

  Map<String, String> get _headers => {
    'content-type': 'application/json',
    if (_token != null) 'authorization': 'Bearer $_token',
  };

  Uri _uri(String path) => Uri.parse('$_apiBaseUrl$path');

  Future<Map<String, Object?>> _get(String path) => _request('GET', path);

  Future<Map<String, Object?>> _sendRequest(
    String method,
    String path, [
    Object? body,
  ]) => _request(method, path, body);

  Future<Map<String, Object?>> _sendUnprotectedRequest(
    String method,
    String path, [
    Object? body,
  ]) => _request(method, path, body, false);

  Future<Map<String, Object?>> _request(
    String method,
    String path, [
    Object? body,
    bool isProtected = true,
  ]) async {
    final encodedBody = body == null ? null : jsonEncode(body);
    var response = await _rawRequest(method, path, encodedBody);
    if (isProtected && response.statusCode == 401) {
      final refreshed = await _refreshAccessToken();
      if (refreshed) {
        response = await _rawRequest(method, path, encodedBody);
      }
    }
    return _decode(response);
  }

  Future<http.Response> _rawRequest(
    String method,
    String path,
    String? encodedBody,
  ) async {
    final request = http.Request(method, _uri(path));
    request.headers.addAll(_headers);
    if (encodedBody != null) request.body = encodedBody;
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
          ? decoded['error'] as Map<String, Object?>?
          : null;
      throw ImApiException(
        statusCode: response.statusCode,
        code: error?['code'] as String? ?? 'HTTP_${response.statusCode}',
        message: error?['message'] as String? ?? '服务暂时不可用，请稍后重试',
      );
    }
    if (decoded is! Map<String, Object?>) {
      throw const FormatException('服务端响应格式无效');
    }
    return decoded['data'] is Map<String, Object?>
        ? decoded['data']! as Map<String, Object?>
        : decoded;
  }

  @override
  Future<bool> restoreSession() async {
    final stored = await _store.readJson('session');
    if (stored is! Map<String, Object?>) return false;
    _token = stored['accessToken'] as String?;
    _refreshToken = stored['refreshToken'] as String?;
    _userId = stored['userId'] as String?;
    _syncCursor = (stored['syncCursor'] as num?)?.toInt() ?? 0;
    if (_token == null || _userId == null) return false;
    try {
      _me = _user(await _get('/v1/me'));
      return true;
    } on ImApiException catch (_) {
      await _clearSession();
      return false;
    } catch (_) {
      return false;
    }
  }

  @override
  Future<String?> requestCode(String phone) async {
    final data = await _sendUnprotectedRequest('POST', '/v1/auth/code', {
      'phone': phone,
    });
    return data['devCode'] as String?;
  }

  @override
  Future<void> enterDemo() => throw const ImApiException(
    statusCode: 400,
    code: 'DEMO_DISABLED',
    message: '当前构建未启用演示模式',
  );

  @override
  Future<AppUser> login(String phone, String code) async {
    final data = await _sendUnprotectedRequest('POST', '/v1/auth/login', {
      'phone': phone,
      'code': code,
      'name': '邻里用户',
    });
    return _acceptSession(data);
  }

  @override
  Future<AppUser> passwordLogin(String phone, String password) async {
    final data = await _sendUnprotectedRequest(
      'POST',
      '/v1/auth/password-login',
      {'phone': phone, 'password': password},
    );
    return _acceptSession(data);
  }

  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
  }) async {
    final data = await _sendUnprotectedRequest('POST', '/v1/auth/register', {
      'phone': phone,
      'code': code,
      'password': password,
      'name': name,
    });
    return _acceptSession(data);
  }

  @override
  Future<void> requestPasswordResetCode(String phone) =>
      _sendUnprotectedRequest('POST', '/v1/auth/password/reset-code', {
        'phone': phone,
      }).then((_) {});

  @override
  Future<void> resetPassword({
    required String phone,
    required String code,
    required String password,
  }) => _sendUnprotectedRequest('POST', '/v1/auth/password/reset', {
    'phone': phone,
    'code': code,
    'password': password,
  }).then((_) {});

  Future<AppUser> _acceptSession(Map<String, Object?> data) async {
    _token = data['accessToken'] as String?;
    _refreshToken = data['refreshToken'] as String?;
    final rawUser = data['user'] as Map<String, Object?>?;
    _userId = rawUser?['id'] as String?;
    if (_token == null || _userId == null || rawUser == null) {
      throw const FormatException('登录响应缺少必要凭据');
    }
    _closed = false;
    _syncCursor = 0;
    await _persistSession();
    return _me = _user(rawUser);
  }

  @override
  Future<AppUser> profile() async => _me = _user(await _get('/v1/users/me'));

  @override
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? avatarMediaId,
  }) async {
    final payload = <String, Object?>{
      'name': ?name,
      'handle': ?handle,
      'signature': ?signature,
      'avatarMediaId': ?avatarMediaId,
    };
    return _me = _user(await _sendRequest('PATCH', '/v1/users/me', payload));
  }

  @override
  Future<String> uploadAvatar(MediaUpload upload) async {
    final prepared = await _sendRequest('POST', '/v1/media/presign', {
      'mime': upload.mimeType,
      'fileName': upload.fileName,
      'size': upload.bytes.length,
    });
    final uploadUrl = prepared['uploadUrl'] as String?;
    final mediaId = prepared['mediaId'] as String?;
    if (uploadUrl == null || mediaId == null) {
      throw const FormatException('头像上传响应缺少地址或媒体编号');
    }
    final headers = <String, String>{};
    if (prepared['headers'] case final Map<String, Object?> rawHeaders) {
      for (final entry in rawHeaders.entries) {
        headers[entry.key] = entry.value.toString();
      }
    }
    final response = await _client
        .put(Uri.parse(uploadUrl), headers: headers, body: upload.bytes)
        .timeout(const Duration(seconds: 45));
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw ImApiException(
        statusCode: response.statusCode,
        code: 'AVATAR_UPLOAD_FAILED',
        message: '头像上传失败，请重试',
      );
    }
    final checksum = sha256.convert(upload.bytes).toString();
    await _sendRequest('POST', '/v1/media/$mediaId/complete', {
      'checksum': checksum,
    });
    return mediaId;
  }

  @override
  Future<void> requestPhoneChangeCode(String phone) => _sendRequest(
    'POST',
    '/v1/users/me/phone/code',
    {'phone': phone},
  ).then((_) {});

  @override
  Future<AppUser> updatePhone(String phone, String code) async => _me = _user(
    await _sendRequest('PATCH', '/v1/users/me/phone', {
      'phone': phone,
      'code': code,
    }),
  );

  @override
  Future<void> requestAccountDeletionCode() => _sendRequest(
    'POST',
    '/v1/users/me/deletion/code',
    const <String, Object?>{},
  ).then((_) {});

  @override
  Future<void> deleteAccount(String code) async {
    await _sendRequest('DELETE', '/v1/users/me', {'code': code.trim()});
    await _disconnect();
    await _clearSession();
    await _store.clearAccountData();
    _closed = false;
  }

  @override
  Future<List<UserDevice>> userDevices() async {
    final data = await _get('/v1/users/me/devices');
    return (data['items'] as List<Object?>? ?? const []).map((raw) {
      final item = raw! as Map<String, Object?>;
      return UserDevice(
        id: item['id']! as String,
        platform: item['platform'] as String? ?? 'unknown',
        provider: item['provider'] as String? ?? '',
        updatedAt:
            DateTime.tryParse(item['updatedAt'] as String? ?? '') ??
            DateTime.fromMillisecondsSinceEpoch(0),
      );
    }).toList();
  }

  @override
  Future<void> registerDevice({
    required String deviceId,
    required String platform,
    required String provider,
    required String pushToken,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  }) => _sendRequest('POST', '/v1/devices', {
    'deviceId': deviceId,
    'platform': platform,
    'provider': provider,
    'pushToken': pushToken,
    'notificationsEnabled': notificationsEnabled,
    'previewEnabled': previewEnabled,
    'soundEnabled': soundEnabled,
    'vibrationEnabled': vibrationEnabled,
  }).then((_) {});

  @override
  Future<void> removeUserDevice(String deviceId) => _sendRequest(
    'DELETE',
    '/v1/users/me/devices/${Uri.encodeComponent(deviceId)}',
  ).then((_) {});

  @override
  Future<List<ChatMessage>> favorites() async {
    final data = await _get('/v1/users/me/favorites?limit=100');
    return (data['items'] as List<Object?>? ?? const []).map((raw) {
      final item = raw! as Map<String, Object?>;
      return _message(item, item['conversationId'] as String? ?? '');
    }).toList();
  }

  @override
  Future<void> submitFeedback({
    required String category,
    required String content,
    String contact = '',
  }) => _sendRequest('POST', '/v1/feedback', {
    'category': category,
    'content': content,
    'contact': contact,
  }).then((_) {});

  @override
  Future<List<AppAnnouncement>> announcements() async {
    final data = await _get('/v1/announcements');
    final items = (data['items'] as List<Object?>? ?? const []).map((raw) {
      final item = raw! as Map<String, Object?>;
      return AppAnnouncement(
        id: item['id']! as String,
        title: item['title'] as String? ?? '平台公告',
        content: item['content'] as String? ?? '',
        status: item['status'] as String? ?? 'published',
        pinned: item['pinned'] as bool? ?? false,
        publishedAt: _tryDate(item['publishedAt']),
        readAt: _tryDate(item['readAt']),
      );
    }).toList();
    items.sort((a, b) {
      if (a.pinned != b.pinned) return a.pinned ? -1 : 1;
      return (b.publishedAt ?? DateTime(1970)).compareTo(
        a.publishedAt ?? DateTime(1970),
      );
    });
    return items;
  }

  @override
  Future<void> markAnnouncementRead(String announcementId) => _sendRequest(
    'POST',
    '/v1/announcements/$announcementId/read',
  ).then((_) {});

  Future<bool> _refreshAccessToken() async {
    final inFlight = _refreshInFlight;
    if (inFlight != null) return inFlight;
    final refresh = _performRefresh();
    _refreshInFlight = refresh;
    try {
      return await refresh;
    } finally {
      if (identical(_refreshInFlight, refresh)) _refreshInFlight = null;
    }
  }

  Future<bool> _performRefresh() async {
    final refresh = _refreshToken;
    if (refresh == null) return false;
    try {
      final data = await _sendUnprotectedRequest('POST', '/v1/auth/refresh', {
        'refreshToken': refresh,
      });
      _token = data['accessToken'] as String?;
      _refreshToken = data['refreshToken'] as String? ?? refresh;
      await _persistSession();
      return _token != null;
    } catch (_) {
      return false;
    }
  }

  @override
  Future<void> logout() async {
    final refreshToken = _refreshToken;
    if (refreshToken != null) {
      try {
        await _sendUnprotectedRequest('POST', '/v1/auth/logout', {
          'refreshToken': refreshToken,
        });
      } catch (_) {
        // Local logout must always complete, including while offline.
      }
    }
    await _disconnect();
    await _clearSession();
    await _store.clearAccountData();
    _closed = false;
  }

  @override
  Future<void> connect() async {
    if (_closed || _connecting || _token == null) return;
    _connecting = true;
    await _socketSubscription?.cancel();
    await _channel?.sink.close();
    try {
      final admission = await _sendRequest('POST', '/v1/ws/ticket');
      final rawTicket = admission['ticket'] as String?;
      if (rawTicket == null || rawTicket.isEmpty) {
        throw const FormatException('实时连接凭据无效');
      }
      final separator = _wsUrl.contains('?') ? '&' : '?';
      final ticket = Uri.encodeQueryComponent(rawTicket);
      _channel = WebSocketChannel.connect(
        Uri.parse('$_wsUrl${separator}ticket=$ticket'),
      );
      await _channel!.ready.timeout(const Duration(seconds: 6));
      _retry = 0;
      _connection.add(true);
      _socketSubscription = _channel!.stream.listen(
        _handleSocketEvent,
        onError: (Object error) => _handleSocketFailure(error.toString()),
        onDone: () => _handleSocketFailure(
          '${_channel?.closeCode ?? ''} ${_channel?.closeReason ?? ''}',
        ),
        cancelOnError: true,
      );
      unawaited(syncNow());
    } catch (_) {
      _scheduleReconnect();
      rethrow;
    } finally {
      _connecting = false;
    }
  }

  void _handleSocketEvent(Object? event) {
    try {
      final envelope = jsonDecode(event! as String) as Map<String, Object?>;
      final type = envelope['type'] as String? ?? '';
      final error = envelope['error'] as Map<String, Object?>?;
      final code = (error?['code'] ?? envelope['code'])?.toString() ?? '';
      if (_isAuthenticationFailure('$type $code')) {
        unawaited(_refreshAndReconnect());
        return;
      }
      if (type == 'session.ready') {
        unawaited(syncNow());
        return;
      }
      final payload = envelope['payload'] as Map<String, Object?>? ?? const {};
      if (type == 'call.signal.ack') {
        final signalId = payload['signalId'] as String?;
        if (signalId != null) {
          for (final timer
              in _pendingCallSignalRetries.remove(signalId) ??
                  const <Timer>[]) {
            timer.cancel();
          }
        }
        return;
      }
      if (type.endsWith('.ack') || type == 'pong') return;
      if (type.startsWith('call.')) {
        final signalId = payload['signalId'] as String?;
        if (signalId != null &&
            const {
              'call.offer',
              'call.answer',
              'call.ice',
              'call.end',
            }.contains(type)) {
          _acknowledgeCallSignal(payload);
          final now = DateTime.now();
          _receivedCallSignals.removeWhere(
            (_, receivedAt) =>
                now.difference(receivedAt) > const Duration(minutes: 2),
          );
          if (_receivedCallSignals.containsKey(signalId)) return;
          _receivedCallSignals[signalId] = now;
        }
        _callEvents.add(CallSignalEvent(type: type, payload: payload));
        return;
      }
      _emitEvent(type, payload);
    } catch (_) {
      unawaited(syncNow());
    }
  }

  void _handleSocketFailure(String details) {
    if (_isAuthenticationFailure(details)) {
      unawaited(_refreshAndReconnect());
    } else {
      _scheduleReconnect();
    }
  }

  bool _isAuthenticationFailure(String value) {
    final normalized = value.toUpperCase();
    return normalized.contains('UNAUTHORIZED') ||
        normalized.contains('TOKEN_EXPIRED') ||
        normalized.contains('AUTH_FAILED') ||
        normalized.contains('AUTHENTICATION') ||
        normalized.contains(' 4001') ||
        normalized.startsWith('4001') ||
        normalized.contains(' 1008') ||
        normalized.startsWith('1008');
  }

  Future<void> _refreshAndReconnect() {
    final inFlight = _authReconnectInFlight;
    if (inFlight != null) return inFlight;
    final operation = _performAuthReconnect();
    _authReconnectInFlight = operation;
    operation.whenComplete(() {
      if (identical(_authReconnectInFlight, operation)) {
        _authReconnectInFlight = null;
      }
    });
    return operation;
  }

  Future<void> _performAuthReconnect() async {
    _connection.add(false);
    await _socketSubscription?.cancel();
    _socketSubscription = null;
    await _channel?.sink.close();
    _channel = null;
    if (await _refreshAccessToken()) {
      _retry = 0;
      await connect();
    } else {
      await _clearSession();
    }
  }

  void _scheduleReconnect() {
    if (_closed || _token == null) return;
    _connection.add(false);
    _retryTimer?.cancel();
    final seconds = 1 << _retry.clamp(0, 5);
    _retry++;
    _retryTimer = Timer(Duration(seconds: seconds), () {
      connect().catchError((_) {});
    });
  }

  @override
  Future<void> syncNow() async {
    if (_token == null) return;
    var hasMore = true;
    while (hasMore) {
      final data = await _get('/v1/sync?after=$_syncCursor&limit=200');
      final items = data['events'] as List<Object?>? ?? const [];
      for (final raw in items) {
        final event = raw! as Map<String, Object?>;
        _emitEvent(event['type'] as String? ?? '', event);
      }
      final next = (data['cursor'] as num?)?.toInt() ?? _syncCursor;
      _syncCursor = next > _syncCursor ? next : _syncCursor;
      hasMore = data['hasMore'] as bool? ?? false;
      await _persistSession();
    }
  }

  void _emitEvent(String rawType, Map<String, Object?> wrapper) {
    final nested = wrapper['payload'];
    final payload = nested is Map<String, Object?> ? nested : wrapper;
    final seq = (wrapper['userSyncSeq'] as num?)?.toInt() ?? 0;
    if (seq > _syncCursor) _syncCursor = seq;
    if (rawType.startsWith('call.')) {
      _callEvents.add(CallSignalEvent(type: rawType, payload: payload));
      return;
    }
    final type = switch (rawType) {
      'message.created' => ImEventType.messageCreated,
      'message.edited' ||
      'message.reaction_added' ||
      'message.reaction_removed' ||
      'message.pinned' ||
      'message.unpinned' => ImEventType.messageChanged,
      'message.recalled' => ImEventType.messageRecalled,
      'message.delivered' => ImEventType.messageDelivered,
      'message.read' || 'conversation.read' => ImEventType.messageRead,
      'message.expired' => ImEventType.messageExpired,
      'conversation.created' ||
      'group.created' ||
      'group.member_added' => ImEventType.conversationChanged,
      'friend.request' ||
      'friend.accepted' ||
      'block.updated' => ImEventType.friendChanged,
      'group.invite' ||
      'group.invite.updated' => ImEventType.groupInvitationChanged,
      'announcement.published' ||
      'announcement.updated' ||
      'announcement.withdrawn' => ImEventType.announcementChanged,
      'typing' => ImEventType.typing,
      _ => ImEventType.unknown,
    };
    if (type != ImEventType.unknown) {
      _events.add(ImEvent(type: type, payload: payload, userSyncSeq: seq));
    }
  }

  @override
  Future<List<Conversation>> conversations() async {
    final data = await _get('/v1/conversations');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => _conversation(item! as Map<String, Object?>))
        .toList();
  }

  Conversation _conversation(
    Map<String, Object?> item, {
    String? titleOverride,
    List<AppUser>? members,
  }) {
    final raw = item['conversation'] is Map<String, Object?>
        ? item['conversation']! as Map<String, Object?>
        : item;
    final last = item['lastMessage'] as Map<String, Object?>?;
    final membership = item['membership'] as Map<String, Object?>?;
    final kind = raw['type'] == 'group'
        ? ConversationKind.group
        : ConversationKind.direct;
    final resolvedMembers =
        members ??
        (item['members'] as List<Object?>? ?? const []).map((entry) {
          final member = entry! as Map<String, Object?>;
          final normalized = <String, Object?>{
            ...member,
            'id': member['id'] ?? member['userId'],
          };
          return _user(normalized);
        }).toList();
    final peer = kind == ConversationKind.direct
        ? resolvedMembers.where((member) => member.id != _userId).firstOrNull
        : null;
    final rawTitle = (raw['title'] as String?)?.trim();
    final title =
        titleOverride ??
        (rawTitle == null || rawTitle.isEmpty
            ? (kind == ConversationKind.group ? '未命名群聊' : peer?.name ?? '私密会话')
            : rawTitle);
    final preview = _messageText(last);
    return Conversation(
      id: raw['id']! as String,
      title: title,
      subtitle: preview.isEmpty ? '打开会话查看消息' : preview,
      updatedAt: DateTime.parse(raw['updatedAt']! as String),
      kind: kind,
      avatarUrl: raw['avatarUrl'] as String? ?? peer?.avatarUrl,
      unread: (item['unreadCount'] as num?)?.toInt() ?? 0,
      muted:
          membership?['notificationsMuted'] as bool? ??
          membership?['mutedUntil'] != null,
      pinned: membership?['pinned'] as bool? ?? false,
      archived: membership?['archived'] as bool? ?? false,
      lastMessageSeq:
          (raw['lastMessageSeq'] as num?)?.toInt() ??
          (last?['conversationSeq'] as num?)?.toInt() ??
          0,
      lastReadSeq: (membership?['lastReadSeq'] as num?)?.toInt() ?? 0,
      mentionUnreadCount:
          (item['mentionUnreadCount'] as num?)?.toInt() ??
          (membership?['mentionUnreadCount'] as num?)?.toInt(),
      members: resolvedMembers,
    );
  }

  @override
  Future<List<AppUser>> contacts() async {
    final data = await _get('/v1/friends');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => _user(item! as Map<String, Object?>))
        .toList();
  }

  @override
  Future<List<AppUser>> searchUsers(
    String query, {
    String by = 'handle',
  }) async {
    final encoded = Uri.encodeQueryComponent(query.trim());
    final encodedBy = Uri.encodeQueryComponent(by);
    final data = await _get('/v1/users/search?q=$encoded&by=$encodedBy');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => _user(item! as Map<String, Object?>))
        .where((user) => user.id != _userId)
        .toList();
  }

  @override
  Future<UserSearchCapabilities> searchCapabilities() async {
    final data = await _get('/v1/users/search/capabilities');
    return UserSearchCapabilities(
      allowSearchByHandle: data['allowSearchByHandle'] as bool? ?? false,
      allowSearchByPhone: data['allowSearchByPhone'] as bool? ?? false,
    );
  }

  AppUser _user(Map<String, Object?> item) => AppUser(
    id: item['id']! as String,
    name: item['name'] as String? ?? item['id']! as String,
    handle:
        item['handle'] as String? ??
        item['phone'] as String? ??
        item['id']! as String,
    presence:
        item['signature'] as String? ?? item['presence'] as String? ?? '邻里通讯用户',
    phone: item['phone'] as String?,
    signature: item['signature'] as String?,
    avatarMediaId: item['avatarMediaId'] as String?,
    avatarUrl: item['avatarUrl'] as String?,
    isOnline: item['online'] as bool? ?? false,
    remark: item['remark'] as String? ?? '',
    tags: (item['tags'] as List<Object?>? ?? const [])
        .whereType<String>()
        .toList(),
    handleChangeCount: (item['handleChangeCount'] as num?)?.toInt() ?? 0,
    handleChangesRemaining:
        (item['handleChangesRemaining'] as num?)?.toInt() ?? 0,
    allowSearchByHandle: item['allowSearchByHandle'] as bool? ?? true,
    allowSearchByPhone: item['allowSearchByPhone'] as bool? ?? false,
  );

  @override
  Future<List<FriendRequest>> friendRequests() async {
    final data = await _get('/v1/friend-requests');
    final rawItems = data['items'] as List<Object?>? ?? const [];
    final knownUsers = <String, AppUser>{
      for (final user in await contacts()) user.id: user,
    };
    final me = currentUser;
    if (me != null) knownUsers[me.id] = me;
    return rawItems.map((raw) {
      final item = raw! as Map<String, Object?>;
      final from = item['fromUserId']! as String;
      final outgoing = from == _userId;
      final peer = outgoing ? item['toUserId']! as String : from;
      return FriendRequest(
        id: item['id']! as String,
        user:
            knownUsers[peer] ??
            AppUser(
              id: peer,
              name: '用户 ${peer.length > 8 ? peer.substring(0, 8) : peer}',
              handle: peer,
              presence: outgoing ? '已发送申请' : '等待验证',
            ),
        note: item['message'] as String? ?? '',
        outgoing: outgoing,
        status: item['status'] as String? ?? 'pending',
        source: item['source'] as String? ?? 'search',
        sourceId: item['sourceId'] as String?,
        createdAt: _tryDate(item['createdAt']),
        expiresAt: _tryDate(item['expiresAt']),
        updatedAt: _tryDate(item['updatedAt']),
        resolvedAt: _tryDate(item['resolvedAt']),
      );
    }).toList();
  }

  @override
  Future<void> sendFriendRequest(
    String userId,
    String note, {
    String source = 'search',
    String? sourceId,
  }) => _sendRequest('POST', '/v1/friend-requests', {
    'userId': userId,
    'message': note,
    'source': source,
    'sourceId': ?sourceId,
  }).then((_) {});

  @override
  Future<void> acceptFriendRequest(String requestId) => _sendRequest(
    'POST',
    '/v1/friend-requests/$requestId/accept',
  ).then((_) {});

  @override
  Future<void> rejectFriendRequest(String requestId) => _sendRequest(
    'POST',
    '/v1/friend-requests/$requestId/reject',
  ).then((_) {});

  @override
  Future<void> cancelFriendRequest(String requestId) => _sendRequest(
    'POST',
    '/v1/friend-requests/$requestId/cancel',
  ).then((_) {});

  @override
  Future<void> updateFriendMetadata(
    String userId, {
    required String remark,
    required List<String> tags,
  }) => _sendRequest('PATCH', '/v1/friends/$userId', {
    'remark': remark,
    'tags': tags,
  }).then((_) {});

  @override
  Future<void> deleteFriend(String userId) =>
      _sendRequest('DELETE', '/v1/friends/$userId').then((_) {});

  @override
  Future<void> blockUser(String userId, bool blocked) => _sendRequest(
    'PUT',
    '/v1/users/$userId/block',
    {'blocked': blocked},
  ).then((_) {});

  @override
  Future<List<AppUser>> blockedUsers() async {
    final data = await _get('/v1/users/me/blocks');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => _user(item! as Map<String, Object?>))
        .toList();
  }

  @override
  Future<void> report({
    required String targetType,
    required String targetId,
    required String reason,
    String details = '',
  }) => _sendRequest('POST', '/v1/reports', {
    'targetType': targetType,
    'targetId': targetId,
    'reason': reason,
    'details': details,
  }).then((_) {});

  @override
  Future<Conversation> createDirect(AppUser user) async {
    final data = await _sendRequest('POST', '/v1/conversations/direct', {
      'userId': user.id,
    });
    return _conversation(data, titleOverride: user.name, members: [user]);
  }

  @override
  Future<Conversation> createGroup(String name, List<AppUser> members) async {
    final data = await _sendRequest('POST', '/v1/groups', {
      'name': name,
      'memberIds': members.map((user) => user.id).toList(),
    });
    return _conversation(data, titleOverride: name, members: members);
  }

  @override
  Future<GroupProfile> groupProfile(String conversationId) async =>
      _groupProfile(await _get('/v1/groups/$conversationId'));

  GroupProfile _groupProfile(Map<String, Object?> item) => GroupProfile(
    conversationId: item['conversationId']! as String,
    ownerId: item['ownerId']! as String,
    name: item['name'] as String? ?? '未命名群聊',
    avatarUrl: item['avatarUrl'] as String?,
    announcement: item['announcement'] as String? ?? '',
    announcementVersion: (item['announcementVersion'] as num?)?.toInt() ?? 0,
    announcementReadAt: _tryDate(item['announcementReadAt']),
    joinPolicy: item['joinPolicy'] as String? ?? 'invite',
    allowMemberAddFriend: item['allowMemberAddFriend'] as bool? ?? true,
    allMutedUntil: _tryDate(item['allMutedUntil']),
    qrToken: item['qrToken'] as String?,
    qrExpiresAt: _tryDate(item['qrExpiresAt']),
    dissolvedAt: _tryDate(item['dissolvedAt']),
    updatedAt: _tryDate(item['updatedAt']) ?? DateTime.now(),
  );

  DateTime? _tryDate(Object? value) =>
      value is String ? DateTime.tryParse(value) : null;

  @override
  Future<GroupProfile> updateGroupProfile(
    String conversationId, {
    String? name,
    String? avatarMediaId,
    String? joinPolicy,
    bool? allowMemberAddFriend,
    bool rotateQr = false,
  }) async => _groupProfile(
    await _sendRequest('PATCH', '/v1/groups/$conversationId', {
      'name': ?name,
      'avatarMediaId': ?avatarMediaId,
      'joinPolicy': ?joinPolicy,
      'allowMemberAddFriend': ?allowMemberAddFriend,
      if (rotateQr) 'rotateQr': true,
    }),
  );

  @override
  Future<GroupProfile> setGroupAnnouncement(
    String conversationId,
    String content,
  ) async => _groupProfile(
    await _sendRequest('PUT', '/v1/groups/$conversationId/announcement', {
      'content': content,
    }),
  );

  @override
  Future<void> markGroupAnnouncementRead(String conversationId) => _sendRequest(
    'POST',
    '/v1/groups/$conversationId/announcement/read',
  ).then((_) {});

  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async {
    final data = await _get('/v1/groups/$conversationId/members');
    final known = <String, AppUser>{
      for (final user in await contacts()) user.id: user,
    };
    final me = currentUser;
    if (me != null) known[me.id] = me;
    return (data['items'] as List<Object?>? ?? const []).map((raw) {
      final item = raw! as Map<String, Object?>;
      final id = item['userId']! as String;
      return GroupMember(
        user: AppUser(
          id: id,
          name:
              item['name'] as String? ??
              known[id]?.name ??
              '成员 ${id.length > 8 ? id.substring(0, 8) : id}',
          handle: item['handle'] as String? ?? known[id]?.handle ?? id,
          presence: '群成员',
          avatarUrl: item['avatarUrl'] as String? ?? known[id]?.avatarUrl,
        ),
        role: item['role'] as String? ?? 'member',
        joinedAt: _tryDate(item['joinedAt']) ?? DateTime.now(),
        mutedUntil: _tryDate(item['mutedUntil']),
        groupNickname: item['groupNickname'] as String? ?? '',
      );
    }).toList();
  }

  @override
  Future<void> addGroupMembers(String conversationId, List<String> userIds) =>
      _sendRequest('POST', '/v1/groups/$conversationId/members', {
        'userIds': userIds,
      }).then((_) {});

  @override
  Future<void> inviteGroupMember(String conversationId, String userId) =>
      _sendRequest('POST', '/v1/groups/$conversationId/invites', {
        'userId': userId,
      }).then((_) {});

  @override
  Future<List<GroupInvitation>> groupInvitations() async {
    final data = await _get('/v1/group-invites?limit=100');
    return (data['items'] as List<Object?>? ?? const []).map((raw) {
      final item = raw! as Map<String, Object?>;
      final invite = item['invite']! as Map<String, Object?>;
      final inviter = item['inviter']! as Map<String, Object?>;
      return GroupInvitation(
        id: invite['id']! as String,
        conversationId: invite['conversationId']! as String,
        groupName: item['groupName'] as String? ?? '群聊',
        inviter: _user(inviter),
        status: invite['status'] as String? ?? 'pending',
        outgoing: item['outgoing'] as bool? ?? false,
        createdAt: _tryDate(invite['createdAt']) ?? DateTime.now(),
        expiresAt:
            _tryDate(invite['expiresAt']) ??
            DateTime.now().add(const Duration(hours: 24)),
        updatedAt: _tryDate(invite['updatedAt']),
      );
    }).toList();
  }

  @override
  Future<void> respondGroupInvitation(String invitationId, String action) =>
      _sendRequest(
        'POST',
        '/v1/group-invites/$invitationId/$action',
      ).then((_) {});

  @override
  Future<void> joinGroupByQr(String token) =>
      _sendRequest('POST', '/v1/groups/join/qr', {'token': token}).then((_) {});

  @override
  Future<void> removeGroupMember(String conversationId, String userId) =>
      _sendRequest(
        'DELETE',
        '/v1/groups/$conversationId/members/$userId',
      ).then((_) {});

  @override
  Future<void> setGroupRole(
    String conversationId,
    String userId,
    String role,
  ) => _sendRequest('PUT', '/v1/groups/$conversationId/members/$userId/role', {
    'role': role,
  }).then((_) {});

  @override
  Future<void> transferGroupOwner(String conversationId, String userId) =>
      _sendRequest('POST', '/v1/groups/$conversationId/owner/transfer', {
        'userId': userId,
      }).then((_) {});

  @override
  Future<void> setGroupNickname(String conversationId, String nickname) =>
      _sendRequest('PATCH', '/v1/groups/$conversationId/nickname', {
        'nickname': nickname,
      }).then((_) {});

  @override
  Future<GroupProfile> setGroupAllMuted(
    String conversationId,
    bool muted,
  ) async => _groupProfile(
    await _sendRequest('PUT', '/v1/groups/$conversationId/mute-all', {
      'until': muted
          ? DateTime.now()
                .add(const Duration(days: 3650))
                .toUtc()
                .toIso8601String()
          : DateTime.now().toUtc().toIso8601String(),
    }),
  );

  @override
  Future<void> leaveGroup(String conversationId) =>
      _sendRequest('POST', '/v1/groups/$conversationId/leave').then((_) {});

  @override
  Future<void> disbandGroup(String conversationId, String reason) =>
      _sendRequest('POST', '/v1/groups/$conversationId/disband', {
        'reason': reason,
      }).then((_) {});

  @override
  Future<List<ChatMessage>> messages(String conversationId) async {
    try {
      final data = await _get(
        '/v1/conversations/$conversationId/messages?limit=50',
      );
      final parsed = (data['items'] as List<Object?>? ?? const [])
          .map(
            (item) => _message(item! as Map<String, Object?>, conversationId),
          )
          .toList();
      final byId = {for (final message in parsed) message.id: message};
      final result = parsed
          .map(
            (message) => message.replyToId == null
                ? message
                : message.copyWith(
                    replyToText: byId[message.replyToId]?.text ?? '原消息暂不可见',
                  ),
          )
          .toList();
      await persistMessages(conversationId, result);
      return result;
    } catch (_) {
      final cached = await _store.readJson('messages.$conversationId');
      if (cached is List<Object?> && cached.isNotEmpty) {
        return cached
            .map((item) => ChatMessage.fromJson(item! as Map<String, Object?>))
            .toList();
      }
      rethrow;
    }
  }

  ChatMessage _message(Map<String, Object?> item, String conversationId) {
    final body = item['body'] as Map<String, Object?>? ?? const {};
    final kindName =
        item['type'] as String? ??
        item['messageType'] as String? ??
        body['type'] as String? ??
        item['contentType'] as String?;
    final replyToId =
        item['replyToId'] as String? ?? body['replyToId'] as String?;
    final kind = switch (kindName) {
      'image' => MessageContentKind.image,
      'voice' || 'audio' => MessageContentKind.voice,
      'video' => MessageContentKind.video,
      'file' => MessageContentKind.file,
      'reply' => MessageContentKind.reply,
      'contact' => MessageContentKind.contact,
      'location' => MessageContentKind.location,
      'chat_history' => MessageContentKind.chatHistory,
      'system' => MessageContentKind.system,
      null || 'text' =>
        replyToId == null || replyToId.isEmpty
            ? MessageContentKind.text
            : MessageContentKind.reply,
      _ => MessageContentKind.unsupported,
    };
    final previewRaw = item['linkPreview'] is Map<String, Object?>
        ? item['linkPreview']! as Map<String, Object?>
        : body['linkPreview'] is Map<String, Object?>
        ? body['linkPreview']! as Map<String, Object?>
        : null;
    final rawStatus = item['status'] as String?;
    return ChatMessage(
      id: item['id']! as String,
      clientMessageId: item['clientMsgId'] as String?,
      conversationId: item['conversationId'] as String? ?? conversationId,
      senderId: item['senderId']! as String,
      senderName: item['senderName'] as String? ?? '联系人',
      text: _messageText(item),
      kind: kind,
      mediaUrl:
          body['url'] as String? ??
          body['downloadUrl'] as String? ??
          body['localPath'] as String?,
      mediaId: body['mediaId'] as String?,
      fileName: body['fileName'] as String?,
      mimeType: body['mime'] as String? ?? body['mimeType'] as String?,
      durationSeconds: (body['duration'] as num?)?.toInt(),
      replyToId: replyToId?.isEmpty == true ? null : replyToId,
      replyToText: body['replyToText'] as String?,
      contactUserId: body['userId'] as String?,
      contactName: body['name'] as String?,
      contactHandle: body['handle'] as String?,
      contactAvatarUrl: body['avatarUrl'] as String?,
      latitude: (body['latitude'] as num?)?.toDouble(),
      longitude: (body['longitude'] as num?)?.toDouble(),
      locationName: body['name'] as String?,
      locationAddress: body['address'] as String?,
      mentions: _messageMentions(item, body),
      reactions: _messageReactions(item, body),
      editedAt: _tryDate(item['editedAt'] ?? body['editedAt']),
      isPinned:
          item['isPinned'] as bool? ??
          body['isPinned'] as bool? ??
          item['pinnedAt'] != null,
      pinnedAt: _tryDate(item['pinnedAt'] ?? body['pinnedAt']),
      pinnedBy: item['pinnedBy'] as String? ?? body['pinnedBy'] as String?,
      expiresAt: _tryDate(item['expiresAt'] ?? body['expiresAt']),
      deliveredCount:
          (item['deliveredCount'] as num?)?.toInt() ??
          (body['deliveredCount'] as num?)?.toInt() ??
          0,
      readCount:
          (item['readCount'] as num?)?.toInt() ??
          (body['readCount'] as num?)?.toInt() ??
          0,
      linkPreview: previewRaw == null ? null : LinkPreview.fromJson(previewRaw),
      sentAt: DateTime.parse((item['createdAt'] ?? item['sentAt'])! as String),
      isMine: item['senderId'] == _userId,
      conversationSeq: (item['conversationSeq'] as num?)?.toInt() ?? 0,
      status: item['expiredAt'] != null || rawStatus == 'expired'
          ? MessageStatus.expired
          : item['recalledAt'] != null || rawStatus == 'recalled'
          ? MessageStatus.recalled
          : rawStatus == 'read'
          ? MessageStatus.read
          : rawStatus == 'delivered'
          ? MessageStatus.delivered
          : MessageStatus.sent,
    );
  }

  List<MessageMention> _messageMentions(
    Map<String, Object?> item,
    Map<String, Object?> body,
  ) {
    final raw =
        item['mentions'] as List<Object?>? ??
        body['mentions'] as List<Object?>? ??
        const [];
    final mentions = raw
        .map<MessageMention?>((entry) {
          if (entry is String && entry.isNotEmpty) {
            return MessageMention(userId: entry, name: entry);
          }
          if (entry is Map<String, Object?> && entry['userId'] is String) {
            return MessageMention.fromJson(entry);
          }
          return null;
        })
        .whereType<MessageMention>()
        .toList();
    final mentionAll =
        item['mentionAll'] as bool? ?? body['mentionAll'] as bool? ?? false;
    if (mentionAll && !mentions.any((mention) => mention.isEveryone)) {
      mentions.insert(0, const MessageMention(userId: 'all', name: '所有人'));
    }
    return mentions;
  }

  List<MessageReaction> _messageReactions(
    Map<String, Object?> item,
    Map<String, Object?> body,
  ) =>
      (item['reactions'] as List<Object?>? ??
              body['reactions'] as List<Object?>? ??
              const [])
          .whereType<Map<String, Object?>>()
          .where((entry) => entry['emoji'] is String)
          .map(MessageReaction.fromJson)
          .where((reaction) => reaction.count > 0)
          .toList();

  String _messageText(Map<String, Object?>? item) {
    if (item == null) return '';
    final body = item['body'] as Map<String, Object?>?;
    final text = body?['text'] as String? ?? item['text'] as String?;
    if (text != null && text.isNotEmpty) return text;
    final type = item['type'] as String? ?? body?['type'] as String?;
    return switch (type) {
      'image' => '[图片]',
      'video' => '[视频]',
      'file' => '[文件] ${body?['fileName'] as String? ?? ''}'.trim(),
      'audio' || 'voice' => '[语音]',
      'contact' => '[名片] ${body?['name'] as String? ?? ''}'.trim(),
      'location' => '[位置] ${body?['name'] as String? ?? ''}'.trim(),
      'chat_history' => _chatHistorySummary(body),
      null || 'text' => '',
      _ => '[当前版本暂不支持此消息]',
    };
  }

  String _chatHistorySummary(Map<String, Object?>? body) {
    final entries = body?['entries'];
    if (entries is! List<Object?> || entries.isEmpty) return '[聊天记录]';
    final previews = entries
        .whereType<Map<String, Object?>>()
        .map((entry) => entry['summary']?.toString().trim() ?? '')
        .where((summary) => summary.isNotEmpty)
        .take(3)
        .toList();
    return previews.isEmpty ? '[聊天记录]' : '聊天记录\n${previews.join('\n')}';
  }

  @override
  Future<ChatMessage> send(ChatMessage pending) async {
    final type = switch (pending.kind) {
      MessageContentKind.contact => 'contact',
      MessageContentKind.location => 'location',
      _ => 'text',
    };
    final body = switch (pending.kind) {
      MessageContentKind.contact => <String, Object?>{
        'userId': pending.contactUserId,
        'name': pending.contactName,
        'handle': pending.contactHandle,
        'avatarUrl': ?pending.contactAvatarUrl,
      },
      MessageContentKind.location => <String, Object?>{
        'latitude': pending.latitude,
        'longitude': pending.longitude,
        'name': pending.locationName,
        'address': pending.locationAddress ?? '',
      },
      _ => <String, Object?>{
        'text': pending.text,
        if (pending.mentions.any((mention) => !mention.isEveryone))
          'mentions': pending.mentions
              .where((mention) => !mention.isEveryone)
              .map((mention) => mention.userId)
              .toList(),
        if (pending.mentions.any((mention) => mention.isEveryone))
          'mentionAll': true,
      },
    };
    final data = await _sendRequest(
      'POST',
      '/v1/conversations/${pending.conversationId}/messages',
      {
        'clientMsgId': pending.clientMessageId,
        'type': type,
        'body': body,
        'replyToId': pending.replyToId ?? '',
        if (pending.expiresAt != null)
          'expiresInSeconds': max(
            1,
            pending.expiresAt!.difference(DateTime.now()).inSeconds,
          ),
      },
    );
    final sent = _message(
      data['message']! as Map<String, Object?>,
      pending.conversationId,
    );
    return sent.copyWith(
      replyToId: pending.replyToId,
      replyToText: pending.replyToText,
      mentions: pending.mentions,
      expiresAt: sent.expiresAt ?? pending.expiresAt,
    );
  }

  @override
  Future<ChatMessage> editMessage(String messageId, String text) async {
    final data = await _sendRequest('PATCH', '/v1/messages/$messageId', {
      'text': text,
    });
    final raw = data['message'] as Map<String, Object?>? ?? data;
    return _message(raw, raw['conversationId'] as String? ?? '');
  }

  @override
  Future<ChatMessage> setMessageReaction(
    String messageId,
    String emoji, {
    required bool active,
  }) async {
    final encoded = Uri.encodeComponent(emoji);
    final data = await _sendRequest(
      active ? 'PUT' : 'DELETE',
      '/v1/messages/$messageId/reactions/$encoded',
    );
    final raw = data['message'] as Map<String, Object?>? ?? data;
    return _message(raw, raw['conversationId'] as String? ?? '');
  }

  @override
  Future<List<ChatMessage>> pinnedMessages(String conversationId) async {
    final data = await _get(
      '/v1/conversations/$conversationId/pinned-messages',
    );
    return (data['items'] as List<Object?>? ?? const [])
        .whereType<Map<String, Object?>>()
        .map((item) => _message(item, conversationId).copyWith(isPinned: true))
        .toList();
  }

  @override
  Future<void> setMessagePinned(
    String conversationId,
    String messageId, {
    required bool pinned,
  }) => _sendRequest(
    pinned ? 'PUT' : 'DELETE',
    '/v1/conversations/$conversationId/pinned-messages/$messageId',
  ).then((_) {});

  @override
  Future<List<ChatMessage>> searchMessages(
    String conversationId,
    String query, {
    int limit = 50,
  }) async {
    final encoded = Uri.encodeQueryComponent(query.trim());
    final data = await _get(
      '/v1/conversations/$conversationId/messages/search?q=$encoded&limit=$limit',
    );
    return (data['items'] as List<Object?>? ?? const [])
        .whereType<Map<String, Object?>>()
        .map((item) => _message(item, conversationId))
        .toList();
  }

  @override
  Future<List<ChatMessage>> forwardMessages(
    String targetConversationId,
    List<String> sourceMessageIds, {
    required String mode,
    required String clientBatchId,
  }) async {
    final data = await _sendRequest(
      'POST',
      '/v1/conversations/$targetConversationId/forward',
      {
        'sourceMessageIds': sourceMessageIds,
        'mode': mode,
        'clientBatchId': clientBatchId,
      },
    );
    return (data['messages'] as List<Object?>? ?? const [])
        .map(
          (raw) => _message(raw! as Map<String, Object?>, targetConversationId),
        )
        .toList();
  }

  @override
  Future<ChatMessage> sendMedia(
    ChatMessage pending,
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  }) async {
    onProgress?.call(0);
    final prepared = await _sendRequest('POST', '/v1/media/presign', {
      'mime': upload.mimeType,
      'fileName': upload.fileName,
      'size': upload.bytes.length,
    });
    final uploadUrl = prepared['uploadUrl'] as String?;
    final mediaId = prepared['mediaId'] as String?;
    if (uploadUrl == null || mediaId == null) {
      throw const FormatException('媒体上传响应缺少地址或媒体编号');
    }
    final uploadHeaders = <String, String>{};
    final rawHeaders = prepared['headers'];
    if (rawHeaders is Map<String, Object?>) {
      for (final entry in rawHeaders.entries) {
        uploadHeaders[entry.key] = entry.value.toString();
      }
    }
    final request = http.StreamedRequest('PUT', Uri.parse(uploadUrl));
    request.headers.addAll(uploadHeaders);
    request.contentLength = upload.bytes.length;
    final responseFuture = _client
        .send(request)
        .timeout(const Duration(seconds: 45));
    const chunkSize = 64 * 1024;
    for (var start = 0; start < upload.bytes.length; start += chunkSize) {
      final end = min(start + chunkSize, upload.bytes.length);
      request.sink.add(upload.bytes.sublist(start, end));
      onProgress?.call(end / upload.bytes.length);
      await Future<void>.delayed(Duration.zero);
    }
    await request.sink.close();
    final uploadResponse = await responseFuture;
    await uploadResponse.stream.drain<void>();
    if (uploadResponse.statusCode < 200 || uploadResponse.statusCode >= 300) {
      throw ImApiException(
        statusCode: uploadResponse.statusCode,
        code: 'MEDIA_UPLOAD_FAILED',
        message: '文件上传失败，请重试',
      );
    }
    final checksum = sha256.convert(upload.bytes).toString();
    await _sendRequest('POST', '/v1/media/$mediaId/complete', {
      'checksum': checksum,
    });
    final type = switch (upload.kind) {
      MessageContentKind.image => 'image',
      MessageContentKind.voice => 'audio',
      MessageContentKind.video => 'video',
      _ => 'file',
    };
    final data = await _sendRequest(
      'POST',
      '/v1/conversations/${pending.conversationId}/messages',
      {
        'clientMsgId': pending.clientMessageId,
        'type': type,
        'body': {
          'type': type,
          'mediaId': mediaId,
          'mime': upload.mimeType,
          'fileName': upload.fileName,
          'size': upload.bytes.length,
          'checksum': checksum,
          if (upload.durationSeconds != null)
            'duration': upload.durationSeconds,
        },
        'replyToId': pending.replyToId ?? '',
      },
    );
    return _message(
      data['message']! as Map<String, Object?>,
      pending.conversationId,
    ).copyWith(
      kind: upload.kind,
      mediaUrl: upload.localPath,
      mediaId: mediaId,
      fileName: upload.fileName,
      mimeType: upload.mimeType,
      durationSeconds: upload.durationSeconds,
      replyToId: pending.replyToId,
      replyToText: pending.replyToText,
    );
  }

  @override
  Future<void> saveFavorite(ChatMessage message) async {
    if (!message.id.startsWith('local-')) {
      await _sendRequest(
        'PUT',
        '/v1/users/me/favorites/${Uri.encodeComponent(message.id)}',
      );
    }
    final stored = await _store.readJson('favorites');
    final favorites = stored is List<Object?>
        ? stored.whereType<Map<String, Object?>>().toList()
        : <Map<String, Object?>>[];
    favorites.removeWhere(
      (item) => item['clientMessageId'] == message.clientMessageId,
    );
    favorites.insert(0, message.toJson());
    await _store.writeJson('favorites', favorites.take(500).toList());
  }

  @override
  Future<void> removeFavorite(ChatMessage message) async {
    if (!message.id.startsWith('local-')) {
      await _sendRequest(
        'DELETE',
        '/v1/users/me/favorites/${Uri.encodeComponent(message.id)}',
      );
    }
    final stored = await _store.readJson('favorites');
    final favorites = stored is List<Object?>
        ? stored.whereType<Map<String, Object?>>().toList()
        : <Map<String, Object?>>[];
    favorites.removeWhere(
      (item) =>
          item['id'] == message.id ||
          item['clientMessageId'] == message.clientMessageId,
    );
    await _store.writeJson('favorites', favorites);
  }

  @override
  Future<void> markRead(String conversationId, int sequence) => _sendRequest(
    'PUT',
    '/v1/conversations/$conversationId/read',
    {'seq': sequence},
  ).then((_) {});

  @override
  Future<void> markDelivered(String conversationId, int sequence) =>
      _sendRequest('PUT', '/v1/conversations/$conversationId/delivered', {
        'seq': sequence,
      }).then((_) {});

  @override
  Future<void> updateConversationPreferences(
    String conversationId, {
    bool? pinned,
    bool? notificationsMuted,
    bool? manualUnread,
    bool? archived,
  }) {
    final payload = <String, Object?>{};
    if (pinned != null) payload['pinned'] = pinned;
    if (notificationsMuted != null) {
      payload['notificationsMuted'] = notificationsMuted;
    }
    if (manualUnread != null) payload['manualUnread'] = manualUnread;
    if (archived != null) payload['archived'] = archived;
    return _sendRequest(
      'PATCH',
      '/v1/conversations/$conversationId/preferences',
      payload,
    ).then((_) {});
  }

  @override
  Future<List<ScheduledMessage>> scheduledMessages(
    String conversationId,
  ) async {
    final data = await _get('/v1/scheduled-messages?status=pending&limit=200');
    return (data['items'] as List<Object?>? ?? const [])
        .whereType<Map<String, Object?>>()
        .map(ScheduledMessage.fromJson)
        .where((message) => message.conversationId == conversationId)
        .toList();
  }

  @override
  Future<ScheduledMessage> scheduleMessage(
    String conversationId,
    String text,
    DateTime scheduledAt, {
    String? replyToId,
    int? expiresInSeconds,
  }) async {
    final data = await _sendRequest('POST', '/v1/scheduled-messages', {
      'conversationId': conversationId,
      'clientMsgId':
          'scheduled-${DateTime.now().microsecondsSinceEpoch.toRadixString(36)}',
      'type': 'text',
      'body': {'text': text},
      'scheduledAt': scheduledAt.toUtc().toIso8601String(),
      'replyToId': ?replyToId,
      'expiresInSeconds': ?expiresInSeconds,
    });
    final raw = data['scheduledMessage'] is Map<String, Object?>
        ? data['scheduledMessage']! as Map<String, Object?>
        : data;
    return ScheduledMessage.fromJson(raw);
  }

  @override
  Future<void> cancelScheduledMessage(String scheduledMessageId) =>
      _sendRequest(
        'DELETE',
        '/v1/scheduled-messages/$scheduledMessageId',
      ).then((_) {});

  @override
  Future<LinkPreview?> linkPreview(String url) async {
    final data = await _sendRequest('POST', '/v1/link-preview', {'url': url});
    final raw = data['preview'] is Map<String, Object?>
        ? data['preview']! as Map<String, Object?>
        : data;
    final preview = LinkPreview.fromJson(raw);
    return preview.url.isEmpty ? null : preview;
  }

  @override
  Future<void> hideConversation(String conversationId) =>
      _sendRequest('DELETE', '/v1/conversations/$conversationId').then((_) {});

  @override
  Future<void> recallMessage(String messageId) =>
      _sendRequest('POST', '/v1/messages/$messageId/recall').then((_) {});

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) => _store.writeJson(
    'messages.$conversationId',
    messages.map((message) => message.toJson()).toList(),
  );

  @override
  Future<String> readDraft(String conversationId) async =>
      (await _store.readJson('draft.$conversationId') as String?) ?? '';

  @override
  Future<void> saveDraft(String conversationId, String text) async {
    final value = text.trimRight();
    if (value.isEmpty) {
      await _store.remove('draft.$conversationId');
    } else {
      await _store.writeJson('draft.$conversationId', value);
    }
  }

  Future<void> _persistSession() => _store.writeJson('session', {
    'accessToken': _token,
    'refreshToken': _refreshToken,
    'userId': _userId,
    'syncCursor': _syncCursor,
  });

  Future<void> _clearSession() async {
    _token = null;
    _refreshToken = null;
    _userId = null;
    _me = null;
    _syncCursor = 0;
    await _store.remove('session');
  }

  Future<void> _disconnect() async {
    _retryTimer?.cancel();
    for (final timers in _pendingCallSignalRetries.values) {
      for (final timer in timers) {
        timer.cancel();
      }
    }
    _pendingCallSignalRetries.clear();
    _receivedCallSignals.clear();
    await _socketSubscription?.cancel();
    _socketSubscription = null;
    await _channel?.sink.close();
    _channel = null;
    _connection.add(false);
  }

  @override
  Future<void> close() async {
    _closed = true;
    await _disconnect();
    _client.close();
    await _connection.close();
    await _events.close();
    await _callEvents.close();
  }

  @override
  Future<CallConfiguration> callConfiguration() async =>
      CallConfiguration.fromJson(await _get('/v1/calls/config'));

  @override
  Future<CallSession> inviteCall({
    required String callId,
    required String conversationId,
    required String calleeUserId,
    required CallMediaType mediaType,
  }) async {
    final data = await _sendRequest('POST', '/v1/calls/invite', {
      'callId': callId,
      'conversationId': conversationId,
      'calleeUserId': calleeUserId,
      'mediaType': mediaType.name,
    });
    return _callFromResponse(data);
  }

  @override
  Future<CallSession> getCall(String callId) async =>
      _callFromResponse(await _get('/v1/calls/$callId'));

  @override
  Future<CallSession> acceptCall(String callId) =>
      _callAction(callId, 'accept');

  @override
  Future<CallSession> rejectCall(String callId, {String reason = ''}) =>
      _callAction(callId, 'reject', reason);

  @override
  Future<CallSession> cancelCall(String callId, {String reason = ''}) =>
      _callAction(callId, 'cancel', reason);

  @override
  Future<CallSession> hangupCall(
    String callId, {
    String reason = 'completed',
  }) => _callAction(callId, 'hangup', reason);

  Future<CallSession> _callAction(
    String callId,
    String action, [
    String reason = '',
  ]) async => _callFromResponse(
    await _sendRequest('POST', '/v1/calls/$callId/$action', {
      if (reason.isNotEmpty) 'reason': reason,
    }),
  );

  CallSession _callFromResponse(Map<String, Object?> data) {
    final raw = data['call'];
    if (raw is! Map<String, Object?>) {
      throw const FormatException('通话响应缺少会话信息');
    }
    return CallSession.fromJson(raw);
  }

  @override
  Future<void> sendCallSignal(String type, Map<String, Object?> payload) async {
    if (!const {
      'call.offer',
      'call.answer',
      'call.ice',
      'call.end',
    }.contains(type)) {
      throw ArgumentError.value(type, 'type', '不支持的通话信令');
    }
    if (_channel == null) await connect();
    final channel = _channel;
    if (channel == null) {
      throw const ImApiException(
        statusCode: 503,
        code: 'REALTIME_UNAVAILABLE',
        message: '实时连接不可用，请稍后重试',
      );
    }
    final signalId =
        payload['signalId'] as String? ??
        'sig-${DateTime.now().microsecondsSinceEpoch.toRadixString(36)}';
    final safePayload = <String, Object?>{...payload, 'signalId': signalId};
    _sendCallEnvelope(type, safePayload);
    final retries = <Timer>[];
    for (final delay in const [
      Duration(milliseconds: 350),
      Duration(seconds: 1),
      Duration(seconds: 2),
      Duration(seconds: 4),
    ]) {
      retries.add(
        Timer(delay, () {
          if (!_pendingCallSignalRetries.containsKey(signalId)) return;
          _sendCallEnvelope(type, safePayload);
        }),
      );
    }
    retries.add(
      Timer(const Duration(seconds: 8), () {
        _pendingCallSignalRetries.remove(signalId);
      }),
    );
    for (final timer
        in _pendingCallSignalRetries.remove(signalId) ?? const <Timer>[]) {
      timer.cancel();
    }
    _pendingCallSignalRetries[signalId] = retries;
  }

  void _sendCallEnvelope(String type, Map<String, Object?> payload) {
    _channel?.sink.add(
      jsonEncode({
        'version': 1,
        'requestId': 'call-${DateTime.now().microsecondsSinceEpoch}',
        'type': type,
        'payload': payload,
      }),
    );
  }

  void _acknowledgeCallSignal(Map<String, Object?> payload) {
    final signalId = payload['signalId'] as String?;
    final callId = payload['callId'] as String?;
    final conversationId = payload['conversationId'] as String?;
    if (signalId == null || callId == null || conversationId == null) return;
    _sendCallEnvelope('call.signal.received', {
      'signalId': signalId,
      'callId': callId,
      'conversationId': conversationId,
    });
  }
}

/// Chooses Live or Demo once, explicitly. It never turns a failed production
/// request into a fake successful Demo request.
class ResilientImRepository implements ImRepository, CallRepository {
  ResilientImRepository({this.live, ImRepository? demo})
    : _demo = demo ?? DemoImRepository();

  factory ResilientImRepository.fromEnvironment() => ResilientImRepository(
    live: AppConfig.hasLiveBackend ? LiveImRepository() : null,
  );

  final ImRepository? live;
  final ImRepository _demo;
  bool _explicitDemo = false;

  ImRepository get _active => _explicitDemo || live == null ? _demo : live!;

  @override
  bool get isDemo => _active.isDemo;
  @override
  bool get supportsDemo => AppConfig.allowsDemo;
  @override
  AppUser? get currentUser => _active.currentUser;
  @override
  Stream<bool> get connectionChanges => _active.connectionChanges;
  @override
  Stream<ImEvent> get events => _active.events;

  CallRepository get _activeCalls {
    final active = _active;
    if (active is CallRepository) return active as CallRepository;
    throw const ImApiException(
      statusCode: 501,
      code: 'CALLS_UNAVAILABLE',
      message: '当前模式不支持音视频通话',
    );
  }

  @override
  Stream<CallSignalEvent> get callEvents => _active is CallRepository
      ? (_active as CallRepository).callEvents
      : const Stream<CallSignalEvent>.empty();

  @override
  Future<CallConfiguration> callConfiguration() =>
      _activeCalls.callConfiguration();

  @override
  Future<CallSession> inviteCall({
    required String callId,
    required String conversationId,
    required String calleeUserId,
    required CallMediaType mediaType,
  }) => _activeCalls.inviteCall(
    callId: callId,
    conversationId: conversationId,
    calleeUserId: calleeUserId,
    mediaType: mediaType,
  );

  @override
  Future<CallSession> getCall(String callId) => _activeCalls.getCall(callId);
  @override
  Future<CallSession> acceptCall(String callId) =>
      _activeCalls.acceptCall(callId);
  @override
  Future<CallSession> rejectCall(String callId, {String reason = ''}) =>
      _activeCalls.rejectCall(callId, reason: reason);
  @override
  Future<CallSession> cancelCall(String callId, {String reason = ''}) =>
      _activeCalls.cancelCall(callId, reason: reason);
  @override
  Future<CallSession> hangupCall(
    String callId, {
    String reason = 'completed',
  }) => _activeCalls.hangupCall(callId, reason: reason);
  @override
  Future<void> sendCallSignal(String type, Map<String, Object?> payload) =>
      _activeCalls.sendCallSignal(type, payload);

  @override
  Future<void> enterDemo() async {
    if (!supportsDemo) {
      throw const ImApiException(
        statusCode: 403,
        code: 'DEMO_DISABLED',
        message: '当前构建未启用演示模式',
      );
    }
    _explicitDemo = true;
    await _demo.enterDemo();
  }

  @override
  Future<bool> restoreSession() => _active.restoreSession();
  @override
  Future<String?> requestCode(String phone) => _active.requestCode(phone);
  @override
  Future<AppUser> login(String phone, String code) =>
      _active.login(phone, code);
  @override
  Future<AppUser> passwordLogin(String phone, String password) =>
      _active.passwordLogin(phone, password);
  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
  }) => _active.register(
    phone: phone,
    code: code,
    password: password,
    name: name,
  );
  @override
  Future<void> requestPasswordResetCode(String phone) =>
      _active.requestPasswordResetCode(phone);
  @override
  Future<void> resetPassword({
    required String phone,
    required String code,
    required String password,
  }) => _active.resetPassword(phone: phone, code: code, password: password);
  @override
  Future<void> logout() => _active.logout();
  @override
  Future<AppUser> profile() => _active.profile();
  @override
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? avatarMediaId,
  }) => _active.updateProfile(
    name: name,
    handle: handle,
    signature: signature,
    avatarMediaId: avatarMediaId,
  );
  @override
  Future<String> uploadAvatar(MediaUpload upload) =>
      _active.uploadAvatar(upload);
  @override
  Future<void> requestPhoneChangeCode(String phone) =>
      _active.requestPhoneChangeCode(phone);
  @override
  Future<AppUser> updatePhone(String phone, String code) =>
      _active.updatePhone(phone, code);
  @override
  Future<void> requestAccountDeletionCode() =>
      _active.requestAccountDeletionCode();
  @override
  Future<void> deleteAccount(String code) => _active.deleteAccount(code);
  @override
  Future<List<UserDevice>> userDevices() => _active.userDevices();
  @override
  Future<void> registerDevice({
    required String deviceId,
    required String platform,
    required String provider,
    required String pushToken,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  }) => _active.registerDevice(
    deviceId: deviceId,
    platform: platform,
    provider: provider,
    pushToken: pushToken,
    notificationsEnabled: notificationsEnabled,
    previewEnabled: previewEnabled,
    soundEnabled: soundEnabled,
    vibrationEnabled: vibrationEnabled,
  );
  @override
  Future<void> removeUserDevice(String deviceId) =>
      _active.removeUserDevice(deviceId);
  @override
  Future<List<ChatMessage>> favorites() => _active.favorites();
  @override
  Future<void> submitFeedback({
    required String category,
    required String content,
    String contact = '',
  }) => _active.submitFeedback(
    category: category,
    content: content,
    contact: contact,
  );
  @override
  Future<List<AppAnnouncement>> announcements() => _active.announcements();
  @override
  Future<void> markAnnouncementRead(String announcementId) =>
      _active.markAnnouncementRead(announcementId);
  @override
  Future<void> connect() => _active.connect();
  @override
  Future<void> syncNow() => _active.syncNow();
  @override
  Future<List<Conversation>> conversations() => _active.conversations();
  @override
  Future<List<AppUser>> contacts() => _active.contacts();
  @override
  Future<List<AppUser>> searchUsers(String query, {String by = 'handle'}) =>
      _active.searchUsers(query, by: by);
  @override
  Future<UserSearchCapabilities> searchCapabilities() =>
      _active.searchCapabilities();
  @override
  Future<List<FriendRequest>> friendRequests() => _active.friendRequests();
  @override
  Future<void> sendFriendRequest(
    String userId,
    String note, {
    String source = 'search',
    String? sourceId,
  }) => _active.sendFriendRequest(
    userId,
    note,
    source: source,
    sourceId: sourceId,
  );
  @override
  Future<void> acceptFriendRequest(String requestId) =>
      _active.acceptFriendRequest(requestId);
  @override
  Future<void> rejectFriendRequest(String requestId) =>
      _active.rejectFriendRequest(requestId);
  @override
  Future<void> cancelFriendRequest(String requestId) =>
      _active.cancelFriendRequest(requestId);
  @override
  Future<void> updateFriendMetadata(
    String userId, {
    required String remark,
    required List<String> tags,
  }) => _active.updateFriendMetadata(userId, remark: remark, tags: tags);
  @override
  Future<void> deleteFriend(String userId) => _active.deleteFriend(userId);
  @override
  Future<void> blockUser(String userId, bool blocked) =>
      _active.blockUser(userId, blocked);
  @override
  Future<List<AppUser>> blockedUsers() => _active.blockedUsers();
  @override
  Future<void> report({
    required String targetType,
    required String targetId,
    required String reason,
    String details = '',
  }) => _active.report(
    targetType: targetType,
    targetId: targetId,
    reason: reason,
    details: details,
  );
  @override
  Future<Conversation> createDirect(AppUser user) => _active.createDirect(user);
  @override
  Future<Conversation> createGroup(String name, List<AppUser> members) =>
      _active.createGroup(name, members);
  @override
  Future<GroupProfile> groupProfile(String conversationId) =>
      _active.groupProfile(conversationId);
  @override
  Future<GroupProfile> updateGroupProfile(
    String conversationId, {
    String? name,
    String? avatarMediaId,
    String? joinPolicy,
    bool? allowMemberAddFriend,
    bool rotateQr = false,
  }) => _active.updateGroupProfile(
    conversationId,
    name: name,
    avatarMediaId: avatarMediaId,
    joinPolicy: joinPolicy,
    allowMemberAddFriend: allowMemberAddFriend,
    rotateQr: rotateQr,
  );
  @override
  Future<GroupProfile> setGroupAnnouncement(
    String conversationId,
    String content,
  ) => _active.setGroupAnnouncement(conversationId, content);
  @override
  Future<void> markGroupAnnouncementRead(String conversationId) =>
      _active.markGroupAnnouncementRead(conversationId);
  @override
  Future<List<GroupMember>> groupMembers(String conversationId) =>
      _active.groupMembers(conversationId);
  @override
  Future<void> addGroupMembers(String conversationId, List<String> userIds) =>
      _active.addGroupMembers(conversationId, userIds);
  @override
  Future<void> inviteGroupMember(String conversationId, String userId) =>
      _active.inviteGroupMember(conversationId, userId);
  @override
  Future<List<GroupInvitation>> groupInvitations() =>
      _active.groupInvitations();
  @override
  Future<void> respondGroupInvitation(String invitationId, String action) =>
      _active.respondGroupInvitation(invitationId, action);
  @override
  Future<void> joinGroupByQr(String token) => _active.joinGroupByQr(token);
  @override
  Future<void> removeGroupMember(String conversationId, String userId) =>
      _active.removeGroupMember(conversationId, userId);
  @override
  Future<void> setGroupRole(
    String conversationId,
    String userId,
    String role,
  ) => _active.setGroupRole(conversationId, userId, role);
  @override
  Future<void> transferGroupOwner(String conversationId, String userId) =>
      _active.transferGroupOwner(conversationId, userId);
  @override
  Future<void> setGroupNickname(String conversationId, String nickname) =>
      _active.setGroupNickname(conversationId, nickname);
  @override
  Future<GroupProfile> setGroupAllMuted(String conversationId, bool muted) =>
      _active.setGroupAllMuted(conversationId, muted);
  @override
  Future<void> leaveGroup(String conversationId) =>
      _active.leaveGroup(conversationId);
  @override
  Future<void> disbandGroup(String conversationId, String reason) =>
      _active.disbandGroup(conversationId, reason);
  @override
  Future<List<ChatMessage>> messages(String conversationId) =>
      _active.messages(conversationId);
  @override
  Future<ChatMessage> send(ChatMessage pending) => _active.send(pending);
  @override
  Future<ChatMessage> editMessage(String messageId, String text) =>
      _active.editMessage(messageId, text);
  @override
  Future<ChatMessage> setMessageReaction(
    String messageId,
    String emoji, {
    required bool active,
  }) => _active.setMessageReaction(messageId, emoji, active: active);
  @override
  Future<List<ChatMessage>> pinnedMessages(String conversationId) =>
      _active.pinnedMessages(conversationId);
  @override
  Future<void> setMessagePinned(
    String conversationId,
    String messageId, {
    required bool pinned,
  }) => _active.setMessagePinned(conversationId, messageId, pinned: pinned);
  @override
  Future<List<ChatMessage>> searchMessages(
    String conversationId,
    String query, {
    int limit = 50,
  }) => _active.searchMessages(conversationId, query, limit: limit);
  @override
  Future<List<ChatMessage>> forwardMessages(
    String targetConversationId,
    List<String> sourceMessageIds, {
    required String mode,
    required String clientBatchId,
  }) => _active.forwardMessages(
    targetConversationId,
    sourceMessageIds,
    mode: mode,
    clientBatchId: clientBatchId,
  );
  @override
  Future<ChatMessage> sendMedia(
    ChatMessage pending,
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  }) => _active.sendMedia(pending, upload, onProgress: onProgress);
  @override
  Future<void> saveFavorite(ChatMessage message) =>
      _active.saveFavorite(message);
  @override
  Future<void> removeFavorite(ChatMessage message) =>
      _active.removeFavorite(message);
  @override
  Future<void> markRead(String conversationId, int sequence) =>
      _active.markRead(conversationId, sequence);
  @override
  Future<void> markDelivered(String conversationId, int sequence) =>
      _active.markDelivered(conversationId, sequence);
  @override
  Future<void> updateConversationPreferences(
    String conversationId, {
    bool? pinned,
    bool? notificationsMuted,
    bool? manualUnread,
    bool? archived,
  }) => _active.updateConversationPreferences(
    conversationId,
    pinned: pinned,
    notificationsMuted: notificationsMuted,
    manualUnread: manualUnread,
    archived: archived,
  );
  @override
  Future<List<ScheduledMessage>> scheduledMessages(String conversationId) =>
      _active.scheduledMessages(conversationId);
  @override
  Future<ScheduledMessage> scheduleMessage(
    String conversationId,
    String text,
    DateTime scheduledAt, {
    String? replyToId,
    int? expiresInSeconds,
  }) => _active.scheduleMessage(
    conversationId,
    text,
    scheduledAt,
    replyToId: replyToId,
    expiresInSeconds: expiresInSeconds,
  );
  @override
  Future<void> cancelScheduledMessage(String scheduledMessageId) =>
      _active.cancelScheduledMessage(scheduledMessageId);
  @override
  Future<LinkPreview?> linkPreview(String url) => _active.linkPreview(url);
  @override
  Future<void> hideConversation(String conversationId) =>
      _active.hideConversation(conversationId);
  @override
  Future<void> recallMessage(String messageId) =>
      _active.recallMessage(messageId);
  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) => _active.persistMessages(conversationId, messages);
  @override
  Future<String> readDraft(String conversationId) =>
      _active.readDraft(conversationId);
  @override
  Future<void> saveDraft(String conversationId, String text) =>
      _active.saveDraft(conversationId, text);
  @override
  Future<void> close() async {
    await live?.close();
    await _demo.close();
  }
}
