import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;

import '../calls/call_models.dart';
import '../calls/call_repository.dart';
import '../core/app_config.dart';
import '../core/auth_validation.dart';
import '../core/group_message_policy.dart';
import '../core/models.dart';
import '../core/media_access.dart';
import '../core/session_http_client.dart';
import '../core/peer_login_info.dart';
import '../core/user_presence.dart';
import '../im/business_repository.dart';
import '../im/business_features.dart';
import '../im/local_conversation_cache.dart';
import '../im/history_access.dart';
import '../im/message_content_registry.dart';
import '../im/message_mapper.dart';
import '../im/structured_event_text.dart';
import '../im/wukong_gateway.dart';
import 'im_repository.dart';
import 'secure_local_store.dart';
import 'message_deletion_cache.dart';

String _runtimeClientPlatform() {
  if (kIsWeb) return 'web';
  return switch (defaultTargetPlatform) {
    TargetPlatform.android => 'android',
    TargetPlatform.iOS => 'ios',
    TargetPlatform.macOS => 'macos',
    // The production matrix intentionally excludes Windows and Linux.  A
    // deterministic value keeps host-side Flutter tests injectable; the real
    // gateway still rejects unsupported desktop runtimes on initialize.
    TargetPlatform.windows ||
    TargetPlatform.linux ||
    TargetPlatform.fuchsia => 'macos',
  };
}

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

class LiveImRepository
    implements
        ImRepository,
        MessageDeletionRepository,
        CachedMessageRepository,
        GroupHistoryRepository,
        PaginatedMessageRepository,
        CallRepository,
        BusinessFeatureRepository {
  LiveImRepository({
    http.Client? client,
    http.Client? uploadClient,
    SecureLocalStore? store,
    String? apiBaseUrl,
    String? clientPlatform,
    BusinessRepository? businessRepository,
    WukongGateway? wukongGateway,
  }) : _client = client ?? createSessionHttpClient(),
       _uploadClient = uploadClient ?? client ?? http.Client(),
       _store = store ?? SecureLocalStore(),
       _apiBaseUrl = apiBaseUrl ?? AppConfig.apiBaseUrl,
       _clientPlatform = clientPlatform ?? _runtimeClientPlatform() {
    _business =
        businessRepository ??
        BusinessRepository(
          apiBaseUrl: _apiBaseUrl,
          platform: _clientPlatform,
          accessToken: () => _token,
          refreshAccessToken: _refreshAccessToken,
          fixedMediaUrl: (id) =>
              _mediaToken == null ? null : mediaAccess.url(id),
          client: _client,
          uploadClient: _uploadClient,
        );
    _wukong = wukongGateway ?? createWukongGateway(dataSource: _business);
    _conversationCache = LocalConversationCache(
      _store,
      isVisible: _canReadWukongMessage,
      sanitize: _sanitizeDeletedWukongReply,
    );
  }

  final http.Client _client;

  /// Presigned object-storage requests must not carry the browser's API
  /// cookies. Besides avoiding credential leakage, this keeps Web uploads from
  /// requiring credentialed CORS support from the storage endpoint.
  final http.Client _uploadClient;
  late final _deletions = MessageDeletionCache(_store);
  final Map<String, Future<void>> _deletionSyncs = {};
  final Map<String, int> _deletionVersions = {};
  final Set<String> _deletionVerified = {};
  int _sessionEpoch = 0;
  @override
  bool isMessageDeleted(String id) => _deletions.contains(_userId, id);

  WukongMessage _sanitizeDeletedWukongReply(WukongMessage message) {
    final reply = message.payload['reply'];
    if (reply is Map && isMessageDeleted('${reply['message_id']}')) {
      return message.copyWith(payload: {...message.payload}..remove('reply'));
    }
    return message;
  }

  List<Map<String, Object?>> _purgeDeletedCache(String uid, List raw) => raw
      .whereType<Map>()
      .where((m) => !_deletions.contains(uid, '${m['id']}'))
      .map((m) {
        final clean = Map<String, Object?>.from(m);
        if (_deletions.contains(uid, '${clean['replyToId']}')) {
          for (final key
              in clean.keys.where((k) => k.startsWith('replyTo')).toList()) {
            clean.remove(key);
          }
        }
        return clean;
      })
      .toList();

  Future<void> _syncDeletions(String cid, WukongChannel channel) async {
    final uid = _userId;
    final epoch = _sessionEpoch;
    if (uid == null) return;
    final key = '$uid:$cid';
    if (_deletionSyncs[key] case final pending?) {
      await pending;
      return;
    }
    final task = () async {
      await _deletions.load(uid);
      var version = _deletionVersions[key] ?? 0;
      while (_userId == uid && epoch == _sessionEpoch) {
        final extras = await _business.syncMessageExtras(
          channel: channel,
          version: version,
          limit: 500,
        );
        if (_userId != uid || epoch != _sessionEpoch) return;
        final ids = extras
            .where((e) => e['is_mutual_deleted'] == 1)
            .map((e) => e['message_idstr'].toString())
            .toList();
        if (ids.isNotEmpty) await _applyDeletions(uid, cid, ids);
        if (_userId != uid || epoch != _sessionEpoch) return;
        var next = version;
        for (final e in extras) {
          next = max(next, (e['extra_version'] as num?)?.toInt() ?? 0);
        }
        _deletionVersions[key] = next;
        if (extras.length < 500 || next <= version) break;
        version = next;
      }
    }();
    _deletionSyncs[key] = task;
    try {
      await task;
      if (_userId == uid && epoch == _sessionEpoch) _deletionVerified.add(key);
    } finally {
      if (identical(_deletionSyncs[key], task)) _deletionSyncs.remove(key);
    }
  }

  Future<void> _applyDeletions(String uid, String cid, List<String> ids) async {
    final epoch = _sessionEpoch;
    bool currentSession() =>
        !_closed && _userId == uid && epoch == _sessionEpoch;
    final write = _deletions.mark(uid, ids);
    if (currentSession()) {
      _events.add(
        ImEvent(
          type: ImEventType.messagesDeleted,
          payload: {'conversationId': cid, 'messageIds': ids},
        ),
      );
    }
    await write;
    if (!currentSession()) return;
    final favorites = await _store.readJson('favorites');
    if (!currentSession()) return;
    if (favorites is List) {
      await _store.writeJson('favorites', _purgeDeletedCache(uid, favorites));
    }
    final channel = _conversationChannels[cid];
    if (channel != null) {
      await _conversationCache.writeMessages(
        uid,
        channel,
        await _conversationCache.readMessages(uid, channel),
      );
    }
    final raw = await _store.readJson('messages.$cid');
    if (!currentSession()) return;
    if (raw is List) {
      await _store.writeJson('messages.$cid', _purgeDeletedCache(uid, raw));
    }
    if (currentSession() && _wukong is WukongDeletionCache) {
      await (_wukong as WukongDeletionCache).markMessagesDeleted(ids);
    }
  }

  @override
  Future<List<String>> deleteMessagesForEveryone(
    String conversationId,
    List<String> messageIds,
  ) async {
    final uid = _userId;
    final epoch = _sessionEpoch;
    final result = await _sendRequest(
      'POST',
      '/v2/messages/delete-for-everyone',
      {
        'conversationId': conversationId,
        'messageIds': messageIds,
        'confirmed': true,
      },
    );
    final ids = (result['messageIds'] as List).cast<String>();
    if (uid != null && _userId == uid && epoch == _sessionEpoch && !_closed) {
      // A local write failure cannot turn a committed deletion into a failed send.
      try {
        await _applyDeletions(uid, conversationId, ids);
      } catch (_) {}
    }
    return ids;
  }

  @override
  Future<ChatMessage> refreshMessageMedia(ChatMessage message) async {
    final account = _userId;
    var id = message.mediaId;
    if (id == null || id.isEmpty) {
      final checkpoint = await _store.readJson(
        'media-upload-$account-${message.clientMessageId}-body',
      );
      if (checkpoint is Map<String, Object?> &&
          checkpoint['completed'] == true) {
        id = checkpoint['mediaId'] as String?;
      }
    }
    if (id == null || id.isEmpty) return message;
    if (account != _userId) throw const FormatException('登录账号已变化');
    final fixed = mediaAccess.url(id);
    if (_mediaToken != null && fixed != null) {
      return ChatMessage.fromJson({
        ...message.toJson(),
        'mediaId': id,
        'mediaUrl': fixed,
        if (message.kind == MessageContentKind.video)
          'coverUrl': mediaAccess.url(id, cover: true),
      });
    }
    final data = await _business.mediaInfo(id);
    if (account != _userId) throw const FormatException('登录账号已变化');
    final url = data['url'] as String?;
    if (url == null ||
        !const {'http', 'https'}.contains(Uri.tryParse(url)?.scheme)) {
      throw const FormatException('媒体地址暂不可用');
    }
    return ChatMessage.fromJson({
      ...message.toJson(),
      'mediaId': id,
      'mediaUrl': url,
      'coverMediaId': data['coverMediaId'],
      'coverUrl': data['cover'],
    });
  }

  final SecureLocalStore _store;
  final String _apiBaseUrl;
  final String _clientPlatform;
  late final BusinessRepository _business;
  late final WukongGateway _wukong;
  late final LocalConversationCache _conversationCache;
  final MessageMapper _messageMapper = MessageMapper();
  final _connection = StreamController<bool>.broadcast();
  final _events = StreamController<ImEvent>.broadcast();
  final _callEvents = StreamController<CallSignalEvent>.broadcast();
  StreamSubscription<WukongConnectionState>? _wukongConnectionSubscription;
  StreamSubscription<WukongGatewayEvent>? _wukongEventSubscription;
  StreamSubscription<WukongSendResult>? _wukongSendSubscription;
  String? _token;
  String? _mediaToken;

  void _acceptMediaSession(Object? value) {
    if (value is! String || value.isEmpty || _userId == null) return;
    _mediaToken = value;
    mediaAccess.configure(
      owner: this,
      apiBaseUrl: _apiBaseUrl,
      userId: _userId!,
      token: value,
    );
  }

  Future<void> _restoreMediaSession() async {
    final account = _userId;
    try {
      // Also repairs a missing Web cookie after browser storage cleanup. Only
      // runs once per restored session, never once per image or video.
      final result = await _request(
        'POST',
        '/v2/media/session',
      ).timeout(const Duration(seconds: 5));
      if (_userId != account) return;
      _acceptMediaSession(result['mediaAccessToken']);
      await _persistSession();
    } catch (_) {
      // Offline restoration remains available; an older server keeps its
      // existing signed-URL behavior until the server-first rollout finishes.
    }
  }

  String? _refreshToken;
  String? _userId;
  AppUser? _me;
  int _profileRevision = 0;
  WukongSession? _imSession;
  final Map<String, WukongChannel> _conversationChannels = {};
  final Map<String, GroupHistoryAccess> _groupHistory = {};
  final Map<String, String> _historyFingerprints = {};
  final Map<String, int> _historyRequiredVersions = {};
  final Map<String, GroupHistoryAccess> _latestHistoryAccess = {};

  bool _canReadWukongMessage(WukongMessage message) {
    if (message.payload['is_mutual_deleted'] == 1 ||
        isMessageDeleted(message.messageId) ||
        message.payload['deletedForEveryoneAt'] != null) {
      return false;
    }
    if (message.channel.type != 2) return true;
    if (message.messageSeq == 0 &&
        message.fromUid == _userId &&
        (message.state == WukongMessageState.sending ||
            message.state == WukongMessageState.failed)) {
      return true;
    }
    return _groupHistory[message.channel.id]?.allows(
          message.messageSeq,
          message.timestamp,
        ) ??
        false;
  }

  @override
  bool canReadCachedMessage(ChatMessage message) {
    if (message.conversationSeq > 0 &&
        !_deletionVerified.contains('$_userId:${message.conversationId}')) {
      return false;
    }
    if (message.deletedForEveryone || isMessageDeleted(message.id)) {
      return false;
    }
    final channel = _conversationChannels[message.conversationId];
    if (channel != null && channel.type != 2) return true;
    if (message.conversationSeq == 0 &&
        message.senderId == _userId &&
        (message.status == MessageStatus.sending ||
            message.status == MessageStatus.failed)) {
      return true;
    }
    return channel != null &&
        (_groupHistory[channel.id]?.allows(
              message.conversationSeq,
              message.sentAt,
            ) ??
            false);
  }

  void _notifyGroupHistory(String conversationId) {
    if (!_events.isClosed) {
      _events.add(
        ImEvent(
          type: ImEventType.groupHistoryChanged,
          payload: {'conversationId': conversationId},
        ),
      );
    }
  }

  void _distrustGroupHistories() {
    final ids = _groupHistory.keys.toList();
    _groupHistory.clear();
    _historyFingerprints.clear();
    for (final cid in ids) {
      _notifyGroupHistory(cid);
    }
  }

  Future<void> _applyGroupHistory(String conversationId, Object? raw) async {
    _conversationChannels[conversationId] = WukongChannel(
      id: conversationId,
      type: 2,
    );
    final access = GroupHistoryAccess.parse(raw);
    final previous = _groupHistory[conversationId];
    final latest = _latestHistoryAccess[conversationId];
    if (access != null) {
      if (access.version < (_historyRequiredVersions[conversationId] ?? 0)) {
        return;
      }
      if (latest?.afterSeq != null &&
          (access.afterSeq == null || access.afterSeq! < latest!.afterSeq!)) {
        return;
      }
      _historyRequiredVersions[conversationId] = access.version;
      _latestHistoryAccess[conversationId] = access;
    }
    if (access != null &&
        previous != null &&
        access.version < previous.version) {
      return;
    }
    final fingerprint = access?.fingerprint ?? 'untrusted';
    if (_historyFingerprints[conversationId] == fingerprint) return;
    _historyFingerprints[conversationId] = fingerprint;
    if (access == null) {
      _groupHistory.remove(conversationId);
    } else {
      _groupHistory[conversationId] = access;
    }
    // Notify synchronously before any disk work: old in-flight reads are gated by
    // the current policy too, so they cannot repaint a revoked message.
    _notifyGroupHistory(conversationId);
    final uid = _userId;
    if (uid == null) return;
    final channel = WukongChannel(id: conversationId, type: 2);
    try {
      final snapshot = await _readPageMessageSnapshot(conversationId);
      if (_userId != uid) return;
      await persistMessages(conversationId, snapshot);
      final favorites = await _store.readJson('favorites');
      if (_userId != uid) return;
      if (favorites is List) {
        final retained = favorites.whereType<Map>().where((raw) {
          if (raw['conversationId'] != conversationId) return true;
          return canReadCachedMessage(
            ChatMessage.fromJson(wukongObjectMap(raw)),
          );
        }).toList();
        await _store.writeJson('favorites', retained);
      }
      final cached = await _conversationCache.readMessages(uid, channel);
      if (_userId != uid) return;
      await _conversationCache.writeMessages(uid, channel, cached);
      final gateway = _wukong;
      if (gateway is WukongHistoryCache) {
        await (gateway as WukongHistoryCache).invalidateGroupHistory(
          conversationId,
          access,
        );
      }
    } catch (_) {
      _groupHistory.remove(conversationId);
      _historyFingerprints.remove(conversationId);
      _notifyGroupHistory(conversationId);
      rethrow;
    }
  }

  Future<void> _refreshGroupHistory(String conversationId) async {
    final uid = _userId;
    final raw = await _get('/v2/channels/groups/$conversationId');
    if (uid == _userId && uid != null) {
      await _applyGroupHistory(conversationId, raw['historyAccess']);
    }
  }

  final Map<String, String> _channelConversations = {};
  final Map<String, _PendingWukongSend> _wukongSendsByClientMsgNo = {};
  final Map<int, _PendingWukongSend> _wukongSendsByClientSeq = {};
  // Only buffer ACKs during an actual gateway dispatch. SDK refresh callbacks
  // after a completed attempt must not become the next retry's early result.
  final Set<List<WukongSendResult>> _dispatchResultBuffers = {};
  final Set<String> _seenWukongMessageEvents = {};
  Future<void> _wukongEventSerial = Future<void>.value();
  final Map<String, List<Map<String, Object?>>> _pendingWukongMessageEvents =
      {};
  int _mutationSequence = 0;
  bool _closed = false;
  Future<bool>? _refreshInFlight;
  Timer? _reconnectReconciliationTimer;
  bool _wukongHasConnected = false;
  bool _reconnectReconciliationNeeded = false;
  int _reconnectReconciliationAttempts = 0;
  bool _handlingSessionReplacement = false;

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
    'x-client-platform': _clientPlatform,
    if (_token != null) 'authorization': 'Bearer $_token',
  };

  Uri _uri(String path) => Uri.parse('$_apiBaseUrl$path');

  String _newMutationId(String prefix) {
    _mutationSequence = (_mutationSequence + 1) & 0x7fffffff;
    return '$prefix-${DateTime.now().microsecondsSinceEpoch.toRadixString(36)}-'
        '${_mutationSequence.toRadixString(36)}';
  }

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
    try {
      return _decode(response);
    } on ImApiException catch (error) {
      _logRequestFailure(
        method: method,
        path: path,
        statusCode: error.statusCode,
        code: error.code,
      );
      rethrow;
    } on FormatException {
      _logRequestFailure(
        method: method,
        path: path,
        statusCode: response.statusCode,
        code: 'INVALID_RESPONSE',
      );
      rethrow;
    }
  }

  void _logRequestFailure({
    required String method,
    required String path,
    required int statusCode,
    required String code,
  }) {
    if (!kDebugMode && !kProfileMode) return;
    debugPrint(
      '[im-api] request failed: method=$method '
      'route=${_safeDiagnosticRoute(path)} status=$statusCode code=$code',
    );
  }

  String _safeDiagnosticRoute(String path) {
    final segments = Uri.parse(path).pathSegments;
    if (segments.length < 2) return '/';
    final route = <String>[segments[0], segments[1]];
    if (segments.length >= 3 &&
        const {'auth', 'contacts'}.contains(segments[1])) {
      route.add(segments[2]);
    }
    return '/${route.join('/')}';
  }

  Future<http.Response> _rawRequest(
    String method,
    String path,
    String? encodedBody,
  ) async {
    final request = http.Request(method, _uri(path));
    request.headers.addAll(_headers);
    if (encodedBody != null) request.body = encodedBody;
    try {
      final streamed = await _client
          .send(request)
          .timeout(const Duration(seconds: 10));
      return http.Response.fromStream(streamed);
    } catch (error) {
      if (kDebugMode || kProfileMode) {
        debugPrint(
          '[im-api] transport failed: method=$method '
          'route=${_safeDiagnosticRoute(path)} type=${error.runtimeType}',
        );
      }
      rethrow;
    }
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
        message: _localizedApiErrorMessage(
          error?['code'] as String? ?? 'HTTP_${response.statusCode}',
          error?['message'] as String?,
        ),
      );
    }
    if (decoded is! Map<String, Object?>) {
      throw const FormatException('服务端响应格式无效');
    }
    return decoded['data'] is Map<String, Object?>
        ? decoded['data']! as Map<String, Object?>
        : decoded;
  }

  String _localizedApiErrorMessage(String code, String? serverMessage) {
    final normalizedCode = code.trim().toUpperCase();
    final localized = switch (normalizedCode) {
      'ACCOUNT_EXISTS' => '该手机号已注册，请直接登录',
      'INVALID_CREDENTIALS' => '手机号、密码或验证码不正确',
      'INVALID_CODE' => '验证码无效或已过期，请重新获取',
      'UNAUTHENTICATED' ||
      'INVALID_REFRESH' ||
      'REFRESH_REUSED' => '登录状态已失效，请重新登录',
      'SESSION_REPLACED' => '该账号已在同类型设备上重新登录，请再次登录',
      'DEVICE_TYPE_MISMATCH' => '当前登录凭据不属于此设备类型，请重新登录',
      'QR_LOGIN_NOT_FOUND' => '登录二维码无效，请在电脑端刷新后重试',
      'QR_LOGIN_EXPIRED' => '登录二维码已过期，请在电脑端刷新',
      'QR_LOGIN_USED' => '这个登录二维码已使用，请在电脑端重新获取',
      'QR_LOGIN_ACCOUNT_UNAVAILABLE' => '当前账号暂时无法用于扫码登录',
      'RATE_LIMITED' => '操作过于频繁，请稍后再试',
      'SMS_NOT_CONFIGURED' || 'SMS_UNAVAILABLE' => '短信验证码服务暂时不可用，请稍后重试',
      'MAINTENANCE' => '服务正在维护，请稍后再试',
      'IM_DISABLED' ||
      'IM_UNAVAILABLE' ||
      'WUKONG_UNAVAILABLE' ||
      'WUKONG_UPSTREAM_ERROR' ||
      'IM_SYNC_FAILED' => '通讯服务暂时不可用，请稍后重试',
      'LIVEKIT_UNAVAILABLE' || 'LIVEKIT_UPSTREAM_ERROR' => '音视频通话服务暂时不可用，请稍后重试',
      'MEDIA_UNAVAILABLE' || 'INVALID_MEDIA' => '媒体文件暂时不可用，请稍后重试',
      'DEVICE_STATE_UNAVAILABLE' => '设备状态暂时无法获取，请稍后重试',
      'GROUP_OWNERSHIP_REQUIRED' => '请先转让群主或解散所管理的群聊',
      'HANDLE_TAKEN' => '这个呱呱号已被使用，请换一个',
      'HANDLE_CHANGE_LIMIT' => '呱呱号修改次数已用完',
      'CONFIRMATION_REQUIRED' => '请完成二次确认后再继续',
      'INVITE_CODE_REQUIRED' => '创建新账号需要填写邀请码',
      'INVITE_CODE_INVALID' => '邀请码无效、已停用或已失效',
      'INVITE_CODE_DISABLED' => '邀请码功能当前未启用',
      'INVITE_CODE_STATUS_DISABLED' => '邀请码已被后台停用，请联系管理员',
      'INVITE_CODE_DUPLICATE' => '该邀请码已被使用或永久保留',
      'INVITE_CODE_CHANGE_USED' => '邀请码修改次数已用完',
      'FORBIDDEN' || 'FORBIDDEN_ORIGIN' => '你没有权限执行此操作',
      'NOT_FOUND' || 'CHANNEL_NOT_FOUND' => '请求的内容不存在或已被删除',
      'CONFLICT' => '当前状态已发生变化，请刷新后重试',
      'INVALID_ARGUMENT' ||
      'INVALID_REQUEST' ||
      'INVALID_PLATFORM' => '提交内容不完整或格式不正确',
      'DATASOURCE_UNAVAILABLE' || 'NOT_READY' || 'INTERNAL' => '服务暂时不可用，请稍后重试',
      _ => null,
    };
    if (localized != null) return localized;
    final message = serverMessage?.trim() ?? '';
    if (RegExp(r'[\u3400-\u9fff]').hasMatch(message)) return message;
    return '服务暂时不可用，请稍后重试';
  }

  @override
  Future<AuthPolicy> authPolicy() async => AuthPolicy.fromJson(
    await _sendUnprotectedRequest('GET', '/v2/config/auth'),
  );

  @override
  Future<bool> restoreSession() async {
    final stored = await _store.readJson('session');
    if (stored is! Map<String, Object?>) return false;
    _token = stored['accessToken'] as String?;
    _refreshToken = stored['refreshToken'] as String?;
    _userId = stored['userId'] as String?;
    _acceptMediaSession(stored['mediaAccessToken']);
    final storedImSession = stored['imSession'];
    if (storedImSession is Map<String, Object?>) {
      try {
        _imSession = WukongSession.fromJson(storedImSession);
      } on FormatException {
        _imSession = null;
      }
    }
    if (_token == null || _userId == null) return false;
    await _restoreMediaSession();
    final storedUser = stored['user'];
    if (storedUser is Map) {
      try {
        final cached = _user(
          storedUser.map((key, value) => MapEntry(key.toString(), value)),
        );
        if (cached.id == _userId) _me = cached;
      } catch (_) {
        // A damaged cached profile must not invalidate otherwise usable tokens.
      }
    }
    // The login response already bound this profile to the encrypted session.
    // Render it immediately; the controller refreshes /users/me in background.
    if (_me != null) return true;
    try {
      _me = _user(await _get('/v2/users/me'));
      await _persistSession();
      return true;
    } on ImApiException catch (error) {
      if (error.statusCode == 401 || error.statusCode == 403) {
        await _clearSession();
      }
      return false;
    } catch (_) {
      return false;
    }
  }

  @override
  Future<String?> requestCode(String phone) async {
    final data = await _sendUnprotectedRequest('POST', '/v2/auth/code', {
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
  Future<AppUser> login(
    String phone,
    String code, {
    String inviteCode = '',
  }) async {
    final data = await _sendUnprotectedRequest('POST', '/v2/auth/login', {
      'phone': phone,
      'code': code,
      'name': '青蛙用户',
      if (inviteCode.trim().isNotEmpty) 'inviteCode': inviteCode.trim(),
    });
    return _acceptSession(data);
  }

  @override
  Future<AppUser> passwordLogin(String phone, String password) async {
    final data = await _sendUnprotectedRequest(
      'POST',
      '/v2/auth/password-login',
      {'phone': phone, 'password': password},
    );
    return _acceptSession(data);
  }

  @override
  Future<QrLoginTicket> createQrLoginTicket({
    required String clientName,
  }) async {
    final data = await _sendUnprotectedRequest('POST', '/v2/auth/qr/create', {
      'clientName': clientName,
    });
    final id = data['id'] as String?;
    final qrPayload = data['qrPayload'] as String?;
    final pollToken = data['pollToken'] as String?;
    final expiresAt = DateTime.tryParse(data['expiresAt'] as String? ?? '');
    if (id == null ||
        id.isEmpty ||
        qrPayload == null ||
        qrPayload.isEmpty ||
        pollToken == null ||
        pollToken.isEmpty ||
        expiresAt == null) {
      throw const FormatException('扫码登录响应缺少必要信息');
    }
    return QrLoginTicket(
      id: id,
      qrPayload: qrPayload,
      pollToken: pollToken,
      expiresAt: expiresAt.toLocal(),
    );
  }

  @override
  Future<AppUser?> pollQrLoginTicket(QrLoginTicket ticket) async {
    final data = await _sendUnprotectedRequest('POST', '/v2/auth/qr/poll', {
      'id': ticket.id,
      'pollToken': ticket.pollToken,
    });
    if (data['status'] == 'pending') return null;
    return _acceptSession(data);
  }

  @override
  Future<QrLoginRequest> inspectQrLogin(String token) async {
    final data = await _sendRequest('POST', '/v2/auth/qr/inspect', {
      'token': token,
    });
    final id = data['id'] as String?;
    final platform = data['clientPlatform'] as String?;
    final name = data['clientName'] as String?;
    final expiresAt = DateTime.tryParse(data['expiresAt'] as String? ?? '');
    if (id == null ||
        id.isEmpty ||
        platform == null ||
        platform.isEmpty ||
        name == null ||
        name.isEmpty ||
        expiresAt == null) {
      throw const FormatException('登录确认信息不完整');
    }
    return QrLoginRequest(
      id: id,
      clientPlatform: platform,
      clientName: name,
      expiresAt: expiresAt.toLocal(),
    );
  }

  @override
  Future<void> confirmQrLogin(String token) => _sendRequest(
    'POST',
    '/v2/auth/qr/confirm',
    {'token': token},
  ).then((_) {});

  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
    String inviteCode = '',
  }) async {
    final data = await _sendUnprotectedRequest('POST', '/v2/auth/register', {
      'phone': phone,
      'code': code,
      'password': password,
      'name': name,
      if (inviteCode.trim().isNotEmpty) 'inviteCode': inviteCode.trim(),
    });
    return _acceptSession(data);
  }

  @override
  Future<bool> validateInviteCode(String code) async {
    final data = await _sendUnprotectedRequest(
      'POST',
      '/v2/auth/invite-codes/validate',
      {'code': code.trim()},
    );
    return data['valid'] == true;
  }

  @override
  Future<InviteCodeProfile> inviteCode() async =>
      InviteCodeProfile.fromJson(await _get('/v2/users/me/invite-code'));

  @override
  Future<InviteCodeProfile> changeInviteCode(String code) async =>
      InviteCodeProfile.fromJson(
        await _sendRequest('PUT', '/v2/users/me/invite-code', {
          'code': code.trim(),
          'confirmed': true,
        }),
      );

  @override
  Future<void> requestPasswordResetCode(String phone) =>
      _sendUnprotectedRequest('POST', '/v2/auth/password/reset-code', {
        'phone': phone,
      }).then((_) {});

  @override
  Future<void> resetPassword({
    required String phone,
    required String code,
    required String password,
  }) => _sendUnprotectedRequest('POST', '/v2/auth/password/reset', {
    'phone': phone,
    'code': code,
    'password': password,
  }).then((_) {});

  Future<AppUser> _acceptSession(Map<String, Object?> data) async {
    _sessionEpoch++;
    _deletionSyncs.clear();
    _deletionVersions.clear();
    _deletionVerified.clear();
    mediaAccess.clear(this);
    _mediaToken = null;
    _token = data['accessToken'] as String?;
    _refreshToken = data['refreshToken'] as String?;
    final rawUser = data['user'] as Map<String, Object?>?;
    _userId = rawUser?['id'] as String?;
    if (_token == null || _userId == null || rawUser == null) {
      throw const FormatException('登录响应缺少必要凭据');
    }
    _acceptMediaSession(data['mediaAccessToken']);
    _imSession = _parseImSession(data['imSession']);
    if (_imSession case final session? when session.uid != _userId) {
      throw const FormatException('WuKongIM session user does not match login');
    }
    _closed = false;
    _handlingSessionReplacement = false;
    _profileRevision++;
    _me = _user(rawUser);
    await _persistSession();
    return _me!;
  }

  WukongSession? _parseImSession(Object? raw) {
    if (raw is Map<String, Object?>) return WukongSession.fromJson(raw);
    if (raw is Map) {
      return WukongSession.fromJson(
        raw.map((key, value) => MapEntry(key.toString(), value)),
      );
    }
    return null;
  }

  @override
  Future<AppUser> profile() async {
    final revision = _profileRevision;
    final accountId = _userId;
    final user = _user(await _get('/v2/users/me'));
    if (_userId != accountId || user.id != accountId) {
      throw StateError('个人资料请求对应的登录账号已变更');
    }
    // A GET started before a successful edit must not overwrite that edit,
    // including the encrypted profile restored on the next app launch.
    if (revision != _profileRevision) {
      final current = _me;
      if (current == null) throw StateError('登录状态已变更');
      return current;
    }
    _me = user;
    await _persistSession();
    return user;
  }

  @override
  Future<PeerLoginInfo> peerLoginInfo(
    String conversationId,
  ) async => PeerLoginInfo.fromJson(
    await _get(
      '/v2/channels/conversations/${Uri.encodeComponent(conversationId)}/peer-login-info',
    ),
  );

  @override
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? gender,
    String? avatarMediaId,
    bool? allowSearchByHandle,
    bool? allowSearchByPhone,
  }) async {
    final payload = <String, Object?>{
      'name': ?name,
      'handle': ?handle,
      'signature': ?signature,
      'gender': ?gender,
      'avatarMediaId': ?avatarMediaId,
      'allowSearchByHandle': ?allowSearchByHandle,
      'allowSearchByPhone': ?allowSearchByPhone,
    };
    final user = _user(await _sendRequest('PATCH', '/v2/users/me', payload));
    _profileRevision++;
    _me = user;
    await _persistSession();
    return user;
  }

  @override
  Future<String> uploadAvatar(MediaUpload upload) async {
    final prepared = await _sendRequest('POST', '/v2/media/presign', {
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
    final response = await _uploadClient
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
    await _sendRequest('POST', '/v2/media/$mediaId/complete', {
      'checksum': checksum,
    });
    return mediaId;
  }

  @override
  Future<void> requestPhoneChangeCode(String phone) => _sendRequest(
    'POST',
    '/v2/users/me/phone/code',
    {'phone': phone},
  ).then((_) {});

  @override
  Future<AppUser> updatePhone(String phone, String code) async {
    final user = _user(
      await _sendRequest('PATCH', '/v2/users/me/phone', {
        'phone': phone,
        'code': code,
      }),
    );
    _profileRevision++;
    _me = user;
    await _persistSession();
    return user;
  }

  @override
  Future<void> requestAccountDeletionCode() => _sendRequest(
    'POST',
    '/v2/users/me/deletion/code',
    const <String, Object?>{},
  ).then((_) {});

  @override
  Future<void> deleteAccount(String code) async {
    await _sendRequest('DELETE', '/v2/users/me', {'code': code.trim()});
    await _disconnect();
    await _clearSession();
    await _store.clearAccountData();
    _closed = false;
  }

  @override
  Future<List<UserDevice>> userDevices() async {
    final data = await _get('/v2/users/me/devices');
    return (data['items'] as List<Object?>? ?? const []).map((raw) {
      final item = raw! as Map<String, Object?>;
      return UserDevice(
        id: item['id']! as String,
        platform: item['platform'] as String? ?? 'unknown',
        provider: item['provider'] as String? ?? '',
        updatedAt:
            tryParseLocalDateTime(item['updatedAt']) ??
            DateTime.fromMillisecondsSinceEpoch(0),
      );
    }).toList();
  }

  @override
  Future<List<ImDeviceSession>> imDeviceSessions() async {
    final data = await _get('/v2/users/me/im-devices');
    return (data['items'] as List<Object?>? ?? const []).map((raw) {
      final item = raw! as Map<String, Object?>;
      return ImDeviceSession(
        deviceFlag: (item['deviceFlag'] as num?)?.toInt() ?? 0,
        deviceLevel: (item['deviceLevel'] as num?)?.toInt() ?? 0,
        connectionCount: (item['connectionCount'] as num?)?.toInt() ?? 0,
        updatedAt: _managerDeviceUpdatedAt(item['updatedAt']),
      );
    }).toList();
  }

  DateTime _managerDeviceUpdatedAt(Object? value) {
    final timestamp = (value as num?)?.toInt() ?? 0;
    final milliseconds = switch (timestamp) {
      > 100000000000000000 => timestamp ~/ 1000000,
      > 100000000000000 => timestamp ~/ 1000,
      > 100000000000 => timestamp,
      _ => timestamp * 1000,
    };
    return DateTime.fromMillisecondsSinceEpoch(milliseconds);
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
  }) => _sendRequest('POST', '/v2/users/me/devices', {
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
  Future<void> registerClientDevice({
    required String installationId,
    required String platform,
    required String deviceName,
    required String deviceModel,
    required String osVersion,
    required String appVersion,
  }) => _sendRequest('PUT', '/v2/users/me/client-device', {
    'installationId': installationId,
    'platform': platform,
    'deviceName': deviceName,
    'deviceModel': deviceModel,
    'osVersion': osVersion,
    'appVersion': appVersion,
  }).then((_) {});

  @override
  Future<void> removeUserDevice(String deviceId) => _sendRequest(
    'DELETE',
    '/v2/users/me/devices/${Uri.encodeComponent(deviceId)}',
  ).then((_) {});

  @override
  Future<void> quitImDeviceSession(int deviceFlag) => _sendRequest(
    'DELETE',
    '/v2/users/me/im-devices/$deviceFlag',
  ).then((_) {});

  @override
  Future<List<ChatMessage>> favorites() async {
    final data = await _get('/v2/messages/favorites?limit=100');
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
  }) => _sendRequest('POST', '/v2/feedback', {
    'category': category,
    'content': content,
    'contact': contact,
  }).then((_) {});

  @override
  Future<void> reportClientDiagnostic({
    required String kind,
    required String name,
    required String fingerprint,
    required String platform,
    required String appVersion,
    int? durationMs,
  }) => _sendRequest('POST', '/v2/client-diagnostics', {
    'kind': kind,
    'name': name,
    'fingerprint': fingerprint,
    'platform': platform,
    'appVersion': appVersion,
    'durationMs': ?durationMs,
  }).then((_) {});

  @override
  Future<List<AppAnnouncement>> announcements() async {
    final data = await _get('/v2/announcements');
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
    '/v2/announcements/$announcementId/read',
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
      final data = await _sendUnprotectedRequest('POST', '/v2/auth/refresh', {
        'refreshToken': refresh,
      });
      if (_refreshToken != refresh) return false;
      _token = data['accessToken'] as String?;
      _acceptMediaSession(data['mediaAccessToken']);
      _refreshToken = data['refreshToken'] as String? ?? refresh;
      _imSession = _parseImSession(data['imSession']) ?? _imSession;
      await _persistSession();
      return _token != null;
    } on ImApiException catch (error) {
      if (error.statusCode == 401 || error.statusCode == 403) {
        final hadSession = _token != null || _refreshToken != null;
        await _disconnect();
        await _clearSession();
        if (hadSession && !_events.isClosed) {
          _events.add(
            const ImEvent(type: ImEventType.sessionExpired, payload: {}),
          );
        }
      }
      return false;
    } catch (_) {
      return false;
    }
  }

  @override
  Future<void> logout() async {
    final refreshToken = _refreshToken;
    if (refreshToken != null) {
      try {
        await _sendRequest('POST', '/v2/auth/logout', {
          'refreshToken': refreshToken,
        });
      } catch (_) {
        // Local logout must always complete, including while offline.
      }
    }
    await _disconnect(logout: true);
    await _clearSession();
    await _store.clearAccountData();
    _closed = false;
  }

  @override
  Future<void> connect() async {
    if (_closed || _token == null || _userId == null) return;
    _bindWukongGateway();
    final session = await _ensureImSession();
    final active = _wukong.session;
    if (active == null ||
        active.uid != session.uid ||
        active.token != session.token ||
        active.deviceFlag != session.deviceFlag ||
        active.tcpUrl != session.tcpUrl ||
        active.wsUrl != session.wsUrl) {
      await _wukong.initialize(session);
      // Policies may arrive before the native SDK opens its database. Purge it
      // again after initialization, without tombstones or server-side deletes.
      if (_wukong is WukongHistoryCache) {
        for (final cid in _groupHistory.keys.toList()) {
          if (_userId != session.uid) return;
          await (_wukong as WukongHistoryCache).invalidateGroupHistory(
            cid,
            _groupHistory[cid],
          );
        }
      }
    }
    if (kDebugMode || kProfileMode) {
      final tcp = Uri.tryParse(session.tcpUrl);
      final ws = Uri.tryParse(session.wsUrl);
      debugPrint(
        '[wukong] connecting: tcp=${tcp?.host}:${tcp?.port} '
        'ws=${ws?.scheme}://${ws?.host}:${ws?.hasPort == true ? ws?.port : ''}',
      );
    }
    try {
      await _wukong.connect();
    } catch (error) {
      if (kDebugMode || kProfileMode) {
        debugPrint('[wukong] connection failed: type=${error.runtimeType}');
      }
      rethrow;
    }
  }

  Future<WukongSession> _ensureImSession() async {
    if (_imSession case final current? when current.uid == _userId) {
      return current;
    }
    final issued = await _business.issueImSession();
    if (issued.uid != _userId) {
      throw const FormatException('WuKongIM session user does not match login');
    }
    _imSession = issued;
    await _persistSession();
    return issued;
  }

  void _bindWukongGateway() {
    _wukongConnectionSubscription ??= _wukong.connectionStates.listen((state) {
      if (_closed) return;
      final available =
          state == WukongConnectionState.connected ||
          state == WukongConnectionState.syncing;
      _connection.add(available);
      switch (state) {
        case WukongConnectionState.connected:
          _deletionVerified.clear();
          unawaited(conversations().catchError((Object _) => <Conversation>[]));
          if (_wukongHasConnected && _reconnectReconciliationNeeded) {
            _scheduleReconnectReconciliation();
          }
          _wukongHasConnected = true;
        case WukongConnectionState.disconnected ||
            WukongConnectionState.networkUnavailable:
          _distrustGroupHistories();
          _reconnectReconciliationTimer?.cancel();
          _reconnectReconciliationTimer = null;
          if (_wukongHasConnected) {
            _reconnectReconciliationNeeded = true;
          }
        case WukongConnectionState.kicked:
          _reconnectReconciliationTimer?.cancel();
          _reconnectReconciliationTimer = null;
          unawaited(_handleSameTypeSessionReplacement());
        case WukongConnectionState.connecting || WukongConnectionState.syncing:
          // A successful TCP connection is reported before the SDK's own
          // conversation sync. Wait for its final connected state so our
          // defensive gap scan cannot race the SDK database transaction.
          _reconnectReconciliationTimer?.cancel();
          _reconnectReconciliationTimer = null;
      }
    });
    _wukongEventSubscription ??= _wukong.events.listen((event) {
      final epoch = _sessionEpoch;
      _wukongEventSerial = _wukongEventSerial
          .then((_) async {
            if (!_closed && epoch == _sessionEpoch) {
              await _handleWukongEvent(event);
            }
          })
          .catchError((Object error, StackTrace stackTrace) {
            debugPrint('WuKong event handling failed: $error');
          });
    });
    _wukongSendSubscription ??= _wukong.sendResults.listen(
      (result) => unawaited(_handleWukongSendResult(result)),
    );
  }

  Future<void> _handleSameTypeSessionReplacement() async {
    if (_closed || _handlingSessionReplacement || _userId == null) return;
    _handlingSessionReplacement = true;
    final hadSession = _token != null || _refreshToken != null;
    // A same-device-type replacement is a full IM logout. The WuKong SDK has
    // already cleared its credentials, so the gateway session must be
    // invalidated as well before this account can log in again.
    await _disconnect(logout: true);
    await _clearSession();
    await _store.clearAccountData();
    if (hadSession && !_events.isClosed) {
      _events.add(
        const ImEvent(
          type: ImEventType.sessionExpired,
          payload: {'reason': 'same_device_type_replaced'},
        ),
      );
    }
  }

  void _scheduleReconnectReconciliation() {
    _reconnectReconciliationTimer?.cancel();
    _reconnectReconciliationTimer = Timer(
      const Duration(milliseconds: 250),
      () {
        _reconnectReconciliationTimer = null;
        if (_closed ||
            !_reconnectReconciliationNeeded ||
            _wukong.connectionState != WukongConnectionState.connected) {
          return;
        }
        _wukongEventSerial = _wukongEventSerial.then(
          (_) => _runReconnectReconciliation(),
        );
      },
    );
  }

  Future<void> _runReconnectReconciliation() async {
    if (_closed ||
        !_reconnectReconciliationNeeded ||
        _wukong.connectionState != WukongConnectionState.connected) {
      return;
    }
    try {
      await _reconcileMessagesAfterReconnect();
      _reconnectReconciliationNeeded = false;
      _reconnectReconciliationAttempts = 0;
    } catch (error) {
      _reconnectReconciliationAttempts++;
      if (kDebugMode || kProfileMode) {
        debugPrint(
          '[wukong] reconnect reconciliation failed: '
          'attempt=$_reconnectReconciliationAttempts '
          'type=${error.runtimeType}',
        );
      }
      if (_reconnectReconciliationAttempts < 3 &&
          !_closed &&
          _wukong.connectionState == WukongConnectionState.connected) {
        _scheduleReconnectReconciliation();
      }
    }
  }

  Future<void> _reconcileMessagesAfterReconnect() async {
    final uid = _userId;
    if (uid == null) return;
    if (_conversationChannels.isEmpty) {
      await conversations();
    }
    final channels = <String, WukongChannel>{
      for (final channel in _conversationChannels.values) channel.key: channel,
    };
    if (channels.isEmpty) return;

    final localSequences = <String, int>{};
    final syncKeys = <String>[];
    for (final channel in channels.values) {
      final cached = await _conversationCache.readMessages(uid, channel);
      final sequence = cached.fold<int>(
        0,
        (current, message) => max(current, message.messageSeq),
      );
      localSequences[channel.key] = sequence;
      syncKeys.add('${channel.id}:${channel.type}:$sequence');
    }

    final changed = await _business.syncConversations(
      version: 0,
      lastMsgSeqs: syncKeys.join('|'),
      messageCount: 200,
    );
    var restoredAny = false;
    for (final item in changed) {
      final channel = WukongChannel(
        id: (item['channel_id'] ?? item['channelId'] ?? '').toString(),
        type:
            (item['channel_type'] as num?)?.toInt() ??
            (item['channelType'] as num?)?.toInt() ??
            0,
      );
      if (channel.id.isEmpty || channel.type <= 0) continue;
      var localSequence = localSequences[channel.key] ?? 0;
      final remoteSequence =
          (item['last_msg_seq'] as num?)?.toInt() ??
          (item['lastMsgSeq'] as num?)?.toInt() ??
          0;
      if (remoteSequence <= localSequence) continue;

      var pages = 0;
      while (localSequence < remoteSequence && pages < 5) {
        pages++;
        final response = await _business.syncMessages(
          channel: channel,
          startMessageSeq: localSequence + 1,
          endMessageSeq: 0,
          limit: 200,
          pullMode: 1,
        );
        final messages =
            (response['messages'] as List<Object?>? ?? const [])
                .whereType<Map>()
                .map(
                  (raw) => WukongMessage.fromSyncJson({
                    ...wukongObjectMap(raw),
                    'channel_id': channel.id,
                    'channel_type': channel.type,
                  }),
                )
                .where(
                  (message) =>
                      message.messageSeq > localSequence &&
                      message.messageSeq <= remoteSequence,
                )
                .toList()
              ..sort(
                (left, right) => left.messageSeq.compareTo(right.messageSeq),
              );
        if (messages.isEmpty) break;
        for (final message in messages) {
          await _handleWukongEvent(
            WukongGatewayEvent(
              kind: WukongGatewayEventKind.received,
              message: message,
              channel: channel,
              data: const {'historySync': true},
            ),
          );
          localSequence = max(localSequence, message.messageSeq);
          restoredAny = true;
        }
        if ((response['more'] as num?)?.toInt() != 1) break;
      }
    }
    if (restoredAny && !_closed) {
      _events.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: <String, Object?>{},
        ),
      );
    }
  }

  _PendingWukongSend? _findWukongSend(WukongSendResult result) {
    if (result.clientMsgNo.isNotEmpty) {
      final byNumber = _wukongSendsByClientMsgNo[result.clientMsgNo];
      if (byNumber != null && _matchesWukongAttempt(byNumber.message, result)) {
        return byNumber;
      }
    }
    if (result.clientSeq > 0) {
      final bySequence = _wukongSendsByClientSeq[result.clientSeq];
      if (bySequence != null &&
          _matchesWukongAttempt(bySequence.message, result)) {
        return bySequence;
      }
    }
    return null;
  }

  bool _matchesWukongAttempt(WukongMessage message, WukongSendResult result) {
    if (result.clientSeq > 0 &&
        message.clientSeq > 0 &&
        result.clientSeq != message.clientSeq) {
      return false;
    }
    if (result.clientMsgNo.isNotEmpty &&
        message.clientMsgNo.isNotEmpty &&
        result.clientMsgNo != message.clientMsgNo) {
      return false;
    }
    return (result.clientSeq > 0 && result.clientSeq == message.clientSeq) ||
        (result.clientMsgNo.isNotEmpty &&
            result.clientMsgNo == message.clientMsgNo);
  }

  Future<void> _handleWukongSendResult(WukongSendResult result) async {
    if (_closed) return;
    final pending = _findWukongSend(result);
    if (pending == null) {
      for (final buffer in _dispatchResultBuffers) {
        buffer.add(result);
        if (buffer.length > 100) buffer.removeAt(0);
      }
      return;
    }
    if (!pending.exposedToCaller) {
      pending.earlyResult = result;
      return;
    }
    await _completeWukongSend(pending, result, emitEvent: true);
  }

  Future<ChatMessage> _completeWukongSend(
    _PendingWukongSend pending,
    WukongSendResult result, {
    required bool emitEvent,
  }) async {
    // Claim the result before the cache write yields to duplicate callbacks.
    _removeWukongSend(pending);
    final authoritative = pending.message.copyWith(
      messageId: result.messageId.isEmpty ? null : result.messageId,
      messageSeq: result.messageSeq <= 0 ? null : result.messageSeq,
      state: result.accepted
          ? WukongMessageState.sent
          : WukongMessageState.failed,
      reasonCode: result.reasonCode,
    );
    pending.message = authoritative;
    final uid = _userId;
    if (uid != null) {
      await _conversationCache.upsertMessage(uid, authoritative);
    }
    final mapped = _messageMapper
        .toChatMessage(
          authoritative,
          currentUserId: uid ?? authoritative.fromUid,
          conversationId: pending.conversationId,
        )
        .copyWith(
          clientMessageId: pending.appClientMessageId,
          replyToId: pending.source.replyToId,
          replyToText: pending.source.replyToText,
          replyToSeq: pending.source.replyToSeq,
          replyToSenderId: pending.source.replyToSenderId,
          replyToSenderName: pending.source.replyToSenderName,
          mentions: pending.source.mentions,
          expiresAt: pending.source.expiresAt,
        );
    if (emitEvent && !_closed) {
      _events.add(
        ImEvent(
          type: ImEventType.messageChanged,
          payload: {
            'message': mapped.toJson(),
            'reasonCode': result.reasonCode,
          },
        ),
      );
    }
    return mapped;
  }

  Future<ChatMessage> _dispatchWukongSend({
    required ChatMessage source,
    required WukongOutgoingMessage outgoing,
    ChatMessage Function(ChatMessage message)? decorate,
  }) async {
    final earlyResults = <WukongSendResult>[];
    _dispatchResultBuffers.add(earlyResults);
    try {
      final sent = await _wukong.send(outgoing);
      return await _trackWukongSend(
        source: source,
        sent: sent,
        decorate: decorate,
        earlyResults: earlyResults,
      );
    } finally {
      _dispatchResultBuffers.remove(earlyResults);
    }
  }

  Future<ChatMessage> _trackWukongSend({
    required ChatMessage source,
    required WukongMessage sent,
    required List<WukongSendResult> earlyResults,
    ChatMessage Function(ChatMessage message)? decorate,
  }) async {
    final uid = _userId;
    if (uid == null) {
      throw StateError('WuKongIM sent without an authenticated user');
    }
    await _conversationCache.upsertMessage(uid, sent);
    final tracked = _PendingWukongSend(
      conversationId: source.conversationId,
      appClientMessageId: source.clientMessageId,
      source: source,
      message: sent,
    );
    if (sent.clientMsgNo.isNotEmpty) {
      _wukongSendsByClientMsgNo[sent.clientMsgNo] = tracked;
    }
    if (sent.clientSeq > 0) {
      _wukongSendsByClientSeq[sent.clientSeq] = tracked;
    }
    final earlyIndex = earlyResults.indexWhere(
      (result) => _matchesWukongAttempt(sent, result),
    );
    final early = earlyIndex < 0
        ? tracked.earlyResult
        : earlyResults.removeAt(earlyIndex);
    ChatMessage mapped;
    if (early != null) {
      mapped = await _completeWukongSend(tracked, early, emitEvent: false);
    } else {
      tracked.exposedToCaller = true;
      mapped = _messageMapper
          .toChatMessage(
            sent,
            currentUserId: uid,
            conversationId: source.conversationId,
          )
          .copyWith(
            clientMessageId: source.clientMessageId,
            replyToId: source.replyToId,
            replyToText: source.replyToText,
            replyToSeq: source.replyToSeq,
            replyToSenderId: source.replyToSenderId,
            replyToSenderName: source.replyToSenderName,
            mentions: source.mentions,
            expiresAt: source.expiresAt,
          );
    }
    return decorate == null ? mapped : decorate(mapped);
  }

  void _removeWukongSend(_PendingWukongSend pending) {
    if (pending.message.clientMsgNo.isNotEmpty) {
      if (identical(
        _wukongSendsByClientMsgNo[pending.message.clientMsgNo],
        pending,
      )) {
        _wukongSendsByClientMsgNo.remove(pending.message.clientMsgNo);
      }
    }
    if (pending.message.clientSeq > 0) {
      if (identical(
        _wukongSendsByClientSeq[pending.message.clientSeq],
        pending,
      )) {
        _wukongSendsByClientSeq.remove(pending.message.clientSeq);
      }
    }
  }

  Future<void> _handleWukongEvent(WukongGatewayEvent event) async {
    if (_closed) return;
    final realtime =
        event.kind == WukongGatewayEventKind.received &&
        event.data['historySync'] != true &&
        _wukong.connectionState == WukongConnectionState.connected;
    if (event.kind == WukongGatewayEventKind.messageEvent) {
      await _handleWukongMessageEvent(event.data);
      return;
    }
    if (event.kind == WukongGatewayEventKind.command) {
      _handleWukongCommand(event.data);
      return;
    }
    if (event.kind == WukongGatewayEventKind.conversationChanged) {
      _events.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: <String, Object?>{},
        ),
      );
      return;
    }
    // The page already owns the optimistic outgoing row. SDK insert callbacks
    // are used internally to obtain its SDK client sequence; forwarding them
    // would create a second widget for the same physical message.
    if (event.kind == WukongGatewayEventKind.inserted) return;
    var message = event.message;
    if (message == null) return;
    if (message.contentType == 99) {
      _handleWukongCommand(message.payload);
      return;
    }
    if (message.contentType == WukongContentType.callEvent) {
      _handleWukongCallEvent(message.payload);
    }
    if (message.channel.type == 2 &&
        !_groupHistory.containsKey(message.channel.id)) {
      try {
        await _refreshGroupHistory(message.channel.id);
      } catch (_) {
        return;
      }
    }
    if (!_canReadWukongMessage(message)) {
      final gateway = _wukong;
      if (gateway is WukongHistoryCache) {
        await (gateway as WukongHistoryCache).invalidateGroupHistory(
          message.channel.id,
          _groupHistory[message.channel.id],
        );
      }
      return;
    }
    message = await _hydrateWukongMedia(message);
    final conversationId = await _conversationIdFor(message.channel);
    if (conversationId == null) {
      _events.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: <String, Object?>{},
        ),
      );
      return;
    }
    final uid = _userId;
    if (uid == null) return;
    if (!_canReadWukongMessage(message)) return;
    await _conversationCache.upsertMessage(uid, message);
    if (_userId != uid || !_canReadWukongMessage(message)) return;
    if (event.kind == WukongGatewayEventKind.refreshed) {
      final ownSend =
          _wukongSendsByClientMsgNo[message.clientMsgNo] ??
          (message.clientSeq > 0
              ? _wukongSendsByClientSeq[message.clientSeq]
              : null);
      if (ownSend != null) {
        await _drainPendingWukongMessageEvents(message.clientMsgNo);
        return;
      }
    }
    final mapped = _messageMapper.toChatMessage(
      message,
      currentUserId: uid,
      conversationId: conversationId,
    );
    if (event.kind == WukongGatewayEventKind.refreshed) {
      _events.add(
        ImEvent(
          type: ImEventType.messageChanged,
          payload: {'conversationId': conversationId, 'messageId': mapped.id},
        ),
      );
      await _drainPendingWukongMessageEvents(message.clientMsgNo);
      return;
    }
    final mention = wukongObjectMap(message.payload['mention']);
    final mentioned =
        (mention['all'] as num?)?.toInt() == 1 ||
        (mention['uids'] as List<Object?>? ?? const []).contains(uid);
    _events.add(
      ImEvent(
        type: ImEventType.messageCreated,
        payload: {
          'message': mapped.toJson(), 'mentioned': mentioned,
          // Suppress delayed/offline SDK deliveries as well as explicit history
          // pages. Do not produce a burst of tones after reconnecting.
          'realtime':
              realtime &&
              DateTime.now()
                      .toUtc()
                      .difference(message.timestamp.toUtc())
                      .abs() <=
                  const Duration(seconds: 30),
        },
      ),
    );
    await _drainPendingWukongMessageEvents(message.clientMsgNo);
  }

  Future<void> _handleWukongMessageEvent(Map<String, Object?> envelope) async {
    final eventID = envelope['id'] as String? ?? '';
    final eventType = envelope['type'] as String? ?? '';
    if (eventID.isEmpty || !_validWukongStreamEventType(eventType)) return;
    final deduplicationKey = '$eventType:$eventID';
    if (!_seenWukongMessageEvents.add(deduplicationKey)) return;
    if (_seenWukongMessageEvents.length > 2000) {
      _seenWukongMessageEvents.remove(_seenWukongMessageEvents.first);
    }
    final data = wukongObjectMap(envelope['data']);
    if (!await _applyWukongMessageEvent(eventType, data)) {
      final clientMsgNo = data['client_msg_no'] as String? ?? '';
      if (clientMsgNo.isEmpty) return;
      final pending = _pendingWukongMessageEvents.putIfAbsent(
        clientMsgNo,
        () => [],
      );
      if (pending.length < 100) {
        pending.add({'type': eventType, 'data': data});
      }
    }
  }

  Future<bool> _applyWukongMessageEvent(
    String eventType,
    Map<String, Object?> data,
  ) async {
    final uid = _userId;
    final clientMsgNo = data['client_msg_no'] as String? ?? '';
    final fromUid = data['from_uid'] as String? ?? '';
    var channelID = data['channel_id'] as String? ?? '';
    final channelType = (data['channel_type'] as num?)?.toInt() ?? 0;
    if (uid == null ||
        clientMsgNo.isEmpty ||
        fromUid.isEmpty ||
        channelID.isEmpty ||
        channelType <= 0) {
      return true;
    }
    if (channelType == 1 && channelID == uid) channelID = fromUid;
    final channel = WukongChannel(id: channelID, type: channelType);
    final current = await _conversationCache.findMessage(
      uid,
      channel,
      clientMsgNo,
    );
    if (current == null) return false;
    final eventSequence = (data['msg_event_seq'] as num?)?.toInt() ?? 0;
    // Cached deltas do not allocate a persisted event sequence. After a
    // snapshot they legitimately reuse that snapshot's sequence, so event ID
    // deduplication (above), not the persisted cursor, orders delta packets.
    if (eventType != 'stream.delta' &&
        eventSequence > 0 &&
        eventSequence <= current.streamEventSeq) {
      return true;
    }
    final eventKey = data['event_key'] as String? ?? 'main';
    final eventPayload = wukongObjectMap(data['payload']);
    final payload = Map<String, Object?>.from(current.payload);
    var streaming = current.isStreaming;
    var completed = current.streamCompleted;
    var contentInitialized = current.streamContentInitialized;
    switch (eventType) {
      case 'stream.delta':
        if (eventKey == 'main' &&
            eventPayload['kind'] == 'text' &&
            eventPayload['delta'] is String) {
          final delta = eventPayload['delta'] as String;
          // WuKong's anchor content is a placeholder. The authoritative event
          // snapshot contains only stream events, so the first delta replaces
          // the anchor and subsequent deltas append to it.
          payload['content'] = contentInitialized
              ? (payload['content'] as String? ?? '') + delta
              : delta;
          contentInitialized = true;
        }
        streaming = true;
      case 'stream.snapshot':
        if (eventKey == 'main' &&
            eventPayload['kind'] == 'text' &&
            eventPayload['text'] is String) {
          payload['content'] = eventPayload['text'];
          contentInitialized = true;
        }
        streaming = true;
      case 'stream.close':
        final snapshot = wukongObjectMap(eventPayload['snapshot']);
        if (eventKey == 'main' &&
            snapshot['kind'] == 'text' &&
            snapshot['text'] is String) {
          payload['content'] = snapshot['text'];
          contentInitialized = true;
        }
        streaming = false;
      case 'stream.error' || 'stream.cancel':
        streaming = false;
      case 'stream.finish':
        streaming = false;
        completed = true;
    }
    final updated = current.copyWith(
      payload: payload,
      streamEventSeq: eventSequence > current.streamEventSeq
          ? eventSequence
          : current.streamEventSeq,
      isStreaming: streaming,
      streamCompleted: completed,
      streamContentInitialized: contentInitialized,
    );
    await _conversationCache.upsertMessage(uid, updated);
    final conversationID = await _conversationIdFor(channel);
    if (conversationID == null) return true;
    final mapped = _messageMapper.toChatMessage(
      updated,
      currentUserId: uid,
      conversationId: conversationID,
    );
    _events.add(
      ImEvent(
        type: ImEventType.messageChanged,
        payload: {'message': mapped.toJson()},
      ),
    );
    return true;
  }

  Future<void> _drainPendingWukongMessageEvents(String clientMsgNo) async {
    if (clientMsgNo.isEmpty) return;
    final pending = _pendingWukongMessageEvents.remove(clientMsgNo);
    if (pending == null) return;
    for (final event in pending) {
      await _applyWukongMessageEvent(
        event['type'] as String? ?? '',
        wukongObjectMap(event['data']),
      );
    }
  }

  bool _validWukongStreamEventType(String value) => const {
    'stream.delta',
    'stream.snapshot',
    'stream.close',
    'stream.error',
    'stream.cancel',
    'stream.finish',
  }.contains(value);

  void _handleWukongCommand(Map<String, Object?> data) {
    final command =
        data['cmd'] as String? ??
        data['type'] as String? ??
        data['command'] as String? ??
        '';
    final payload = wukongObjectMap(
      data['param'] ?? data['data'] ?? data['payload'],
    );
    if (command.isNotEmpty) {
      if (command.startsWith('call.')) {
        _handleWukongCallEvent(payload, command: command);
        return;
      }
      if ((payload['schemaVersion'] as num?)?.toInt() != 1 ||
          payload['event'] != command) {
        return;
      }
      _emitEvent(command, payload);
    } else {
      _events.add(
        const ImEvent(
          type: ImEventType.conversationChanged,
          payload: <String, Object?>{},
        ),
      );
    }
  }

  void _handleWukongCallEvent(Map<String, Object?> payload, {String? command}) {
    if ((payload['schemaVersion'] as num?)?.toInt() != 1 ||
        (payload['contentType'] as num?)?.toInt() !=
            WukongContentType.callEvent) {
      return;
    }
    final event = command ?? payload['event'] as String? ?? '';
    if (!event.startsWith('call.') ||
        (payload['event'] is String && payload['event'] != event)) {
      return;
    }
    _callEvents.add(CallSignalEvent(type: event, payload: payload));
  }

  Future<WukongMessage> _hydrateWukongMedia(WukongMessage message) async {
    final mediaId = message.payload['mediaId'] as String?;
    if (_mediaToken != null && mediaId?.isNotEmpty == true) {
      return message.copyWith(
        payload: {
          ...message.payload,
          'url': mediaAccess.url(mediaId),
          if (message.payload['type'] == 5)
            'cover': mediaAccess.url(mediaId, cover: true),
        },
      );
    }
    final existingURL = message.payload['url'] as String?;
    if (mediaId == null ||
        mediaId.isEmpty ||
        (existingURL != null &&
            existingURL.isNotEmpty &&
            message.payload['type'] != 5)) {
      return message;
    }
    try {
      final data = await _business.mediaInfo(mediaId);
      return message.copyWith(
        payload: {
          ...message.payload,
          'url': data['url'],
          'cover': data['cover'],
          'coverMediaId': data['coverMediaId'],
        },
      );
    } catch (_) {
      return message;
    }
  }

  @override
  Future<void> syncNow() async {
    if (_token == null) return;
    if (_wukong.connectionState != WukongConnectionState.connected &&
        _wukong.connectionState != WukongConnectionState.syncing) {
      await connect();
    }
  }

  void _emitEvent(String rawType, Map<String, Object?> wrapper) {
    final nested = wrapper['payload'];
    final payload = nested is Map ? wukongObjectMap(nested) : wrapper;
    if (rawType == 'messages.deleted') {
      final uid = _userId;
      if (uid != null) {
        unawaited(
          _applyDeletions(
            uid,
            payload['conversationId'].toString(),
            (payload['messageIds'] as List? ?? const [])
                .map((id) => id.toString())
                .toList(),
          ).catchError((Object _) {}),
        );
      }
      return;
    }
    if (rawType == 'message.recalled') {
      final cid = payload['conversationId']?.toString();
      final mid = payload['messageId']?.toString();
      final channel = _conversationChannels[cid];
      if (_userId != null && channel != null && mid != null) {
        unawaited(
          _conversationCache
              .markRecalled(_userId!, channel, mid)
              .catchError((Object _) {}),
        );
      }
    }
    if (rawType == 'group.history.updated' ||
        (rawType == 'group.system' &&
            payload['event'] == 'group.history.updated')) {
      final cid = payload['conversationId']?.toString() ?? '';
      if (cid.isNotEmpty) {
        final details = wukongObjectMap(payload['data']);
        final version = (details['historyPolicyVersion'] as num?)?.toInt();
        if (version != null && version <= (_groupHistory[cid]?.version ?? 0)) {
          return;
        }
        if (version != null) {
          _historyRequiredVersions[cid] = max(
            version,
            _historyRequiredVersions[cid] ?? 0,
          );
        }
        _groupHistory.remove(cid);
        _historyFingerprints.remove(cid);
        _notifyGroupHistory(cid);
        unawaited(_refreshGroupHistory(cid).catchError((Object _) {}));
      }
    }
    if (rawType.startsWith('call.')) {
      _callEvents.add(CallSignalEvent(type: rawType, payload: payload));
      return;
    }
    final type = switch (rawType) {
      'message.created' => ImEventType.messageCreated,
      'message.edited' ||
      'message.reaction.updated' ||
      'message.reaction_added' ||
      'message.reaction_removed' ||
      'group.message.pinned' ||
      'group.message.unpinned' ||
      'message.pinned' ||
      'message.unpinned' => ImEventType.messageChanged,
      'message.recalled' => ImEventType.messageRecalled,
      'user.message_permissions.updated' =>
        ImEventType.messagePermissionsChanged,
      'message.delivered' => ImEventType.messageDelivered,
      'message.read' || 'conversation.read' => ImEventType.messageRead,
      'message.expired' => ImEventType.messageExpired,
      'conversation.created' ||
      'conversation.preferences.updated' ||
      'group.created' ||
      'group.system' ||
      'group.profile.updated' ||
      'group.announcement.updated' ||
      'group.member_added' ||
      'group.member.joined' ||
      'group.members.added' ||
      'group.members.updated' ||
      'group.disbanded' => ImEventType.conversationChanged,
      'friend.request' ||
      'friend.request.sent' ||
      'friend.request.updated' ||
      'friend.accepted' ||
      'friend.removed' ||
      'friend.metadata.updated' ||
      'friend.account_deleted' ||
      'block.updated' => ImEventType.friendChanged,
      'group.invite' ||
      'group.invite.updated' => ImEventType.groupInvitationChanged,
      'announcement.published' ||
      'announcement.updated' ||
      'announcement.withdrawn' => ImEventType.announcementChanged,
      String() when rawType.startsWith('scheduled.') =>
        ImEventType.scheduledChanged,
      'sync.reset_required' => ImEventType.conversationChanged,
      'typing' => ImEventType.typing,
      _ => ImEventType.unknown,
    };
    if (type != ImEventType.unknown) {
      _events.add(
        ImEvent(
          type: type,
          payload: {
            ...payload,
            if (type == ImEventType.conversationChanged &&
                rawType.startsWith('group.'))
              'groupSendPolicyChanged': true,
          },
        ),
      );
    }
  }

  @override
  Future<List<Conversation>> conversations() async {
    final sessionUserId = _userId;
    final data = await _get('/v2/channels/conversations');
    if (_userId != sessionUserId) return const [];
    final metadata = (data['items'] as List<Object?>? ?? const [])
        .map((item) => _conversation(item! as Map<String, Object?>))
        .toList();
    for (final conversation in metadata) {
      _registerConversation(conversation);
      final channel = _conversationChannels[conversation.id];
      if (channel != null) {
        await _store.writeJson('channel.${conversation.id}', {
          'id': channel.id,
          'type': channel.type,
        });
      }
    }
    for (final raw
        in (data['items'] as List<Object?>? ?? const []).whereType<Map>()) {
      final conversation = wukongObjectMap(raw['conversation']);
      final cid = conversation['id']?.toString() ?? '';
      if (_conversationChannels[cid]?.type == 2) {
        await _applyGroupHistory(cid, raw['historyAccess']);
      }
    }
    try {
      final synced = await _business.syncConversations(
        version: 0,
        lastMsgSeqs: '',
        messageCount: 1,
      );
      final byChannel = <String, Map<String, Object?>>{
        for (final item in synced)
          '${item['channel_id'] ?? item['channelId']}@${item['channel_type'] ?? item['channelType']}':
              item,
      };
      final result = <Conversation>[];
      for (final conversation in metadata) {
        final channel = _conversationChannels[conversation.id];
        if (channel != null) await _syncDeletions(conversation.id, channel);
        final item = channel == null ? null : byChannel[channel.key];
        result.add(
          item == null
              ? conversation.copyWith(subtitle: '打开会话查看消息')
              : await _mergeWukongConversation(conversation, channel!, item),
        );
      }
      return _userId == sessionUserId ? result : const [];
    } catch (_) {
      // Business metadata remains useful during a transient IM sync outage;
      // message history itself never falls back to the legacy message store.
      return metadata
          .map((item) => item.copyWith(subtitle: '打开会话查看消息'))
          .toList();
    }
  }

  void _registerConversation(Conversation conversation) {
    final explicitType = conversation.channelType;
    final channel = explicitType > 0
        ? WukongChannel(
            id:
                conversation.channelId ??
                (explicitType == 1
                    ? conversation.directPeerFor(_userId)?.id ?? ''
                    : conversation.id),
            type: explicitType,
          )
        : conversation.kind == ConversationKind.group
        ? WukongChannel(id: conversation.id, type: 2)
        : WukongChannel(
            id: conversation.directPeerFor(_userId)?.id ?? '',
            type: 1,
          );
    if (channel.id.isEmpty) return;
    _conversationChannels[conversation.id] = channel;
    _channelConversations[channel.key] = conversation.id;
  }

  Future<Conversation> _mergeWukongConversation(
    Conversation conversation,
    WukongChannel channel,
    Map<String, Object?> item,
  ) async {
    final recents = item['recents'] as List<Object?>? ?? const [];
    String? subtitle = isManagedGroup(conversation) ? '打开会话查看消息' : null;
    if (recents.isNotEmpty && recents.first is Map) {
      final recent = wukongObjectMap(recents.first);
      final normalized = <String, Object?>{
        ...recent,
        'channel_id': recent['channel_id'] ?? channel.id,
        'channel_type': recent['channel_type'] ?? channel.type,
      };
      final message = WukongMessage.fromSyncJson(normalized);
      final mapped = _messageMapper.toChatMessage(
        message,
        currentUserId: _userId ?? '',
        conversationId: conversation.id,
      );
      if (!_canReadWukongMessage(message)) {
        subtitle = '打开会话查看消息';
      } else if (canPresentGroupMessage(mapped, conversation)) {
        subtitle = messagePreviewText(mapped);
      } else {
        subtitle = await _previousVisibleGroupPreview(
          conversation,
          channel,
          message.messageSeq,
        );
      }
    }
    final timestamp = (item['timestamp'] as num?)?.toInt() ?? 0;
    final updatedAt = timestamp <= 0
        ? conversation.updatedAt
        : DateTime.fromMillisecondsSinceEpoch(
            timestamp < 1000000000000 ? timestamp * 1000 : timestamp,
          );
    return conversation.copyWith(
      subtitle: subtitle?.isNotEmpty == true ? subtitle : conversation.subtitle,
      updatedAt: updatedAt,
      unread: channel.type == 2 && !_groupHistory.containsKey(channel.id)
          ? 0
          : (item['unread'] as num?)?.toInt() ?? conversation.unread,
      lastMessageSeq:
          (item['last_msg_seq'] as num?)?.toInt() ??
          (item['lastMsgSeq'] as num?)?.toInt() ??
          conversation.lastMessageSeq,
      lastReadSeq:
          (item['readed_to_msg_seq'] as num?)?.toInt() ??
          (item['readedToMsgSeq'] as num?)?.toInt() ??
          conversation.lastReadSeq,
    );
  }

  Future<String> _previousVisibleGroupPreview(
    Conversation conversation,
    WukongChannel channel,
    int before,
  ) async {
    final uid = _userId;
    // Use raw sequence cursors, not the number of rendered items: any number
    // of adjacent hidden notices may precede the last visible message.
    try {
      while (uid != null && _userId == uid && before > 1) {
        final data = await _business.syncMessages(
          channel: channel,
          startMessageSeq: before - 1,
          endMessageSeq: 0,
          limit: 50,
          pullMode: 0,
        );
        final page =
            (data['messages'] as List<Object?>? ?? const [])
                .whereType<Map>()
                .map(
                  (raw) => WukongMessage.fromSyncJson({
                    ...wukongObjectMap(raw),
                    'channel_id': channel.id,
                    'channel_type': channel.type,
                  }),
                )
                .where(
                  (item) => item.messageSeq > 0 && item.messageSeq < before,
                )
                .toList()
              ..sort((a, b) => b.messageSeq.compareTo(a.messageSeq));
        if (_userId != uid || page.isEmpty) break;
        await _conversationCache.mergeMessages(uid, channel, page);
        for (final message in page) {
          final mapped = _messageMapper.toChatMessage(
            message,
            currentUserId: uid,
            conversationId: conversation.id,
          );
          if (_canReadWukongMessage(message) &&
              canPresentGroupMessage(mapped, conversation)) {
            return messagePreviewText(mapped);
          }
        }
        before = page.last.messageSeq;
        if (data['more'] == 0) break;
      }
    } catch (_) {
      // Never fall back to an unclassified server digest when history is down.
    }
    return '打开会话查看消息';
  }

  Future<WukongChannel?> _channelForConversation(String conversationId) async {
    var channel = _conversationChannels[conversationId];
    if (channel != null) return channel;
    await conversations();
    channel = _conversationChannels[conversationId];
    return channel;
  }

  Future<String?> _conversationIdFor(WukongChannel channel) async {
    final known = _channelConversations[channel.key];
    if (known != null) return known;
    if (channel.type == 2) {
      _channelConversations[channel.key] = channel.id;
      _conversationChannels[channel.id] = channel;
      return channel.id;
    }
    try {
      await conversations();
    } catch (_) {
      return null;
    }
    return _channelConversations[channel.key];
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
    final conversationType = raw['type'] as String? ?? '';
    final channelType = _channelTypeForConversationType(conversationType);
    final kind = conversationType == 'direct'
        ? ConversationKind.direct
        : ConversationKind.group;
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
      subtitle: preview.isEmpty
          ? kind == ConversationKind.direct
                ? '你们已是好友，开始聊天吧'
                : '打开会话查看消息'
          : preview,
      updatedAt: parseLocalDateTime(raw['updatedAt']! as String),
      kind: kind,
      channelId: kind == ConversationKind.direct
          ? peer?.id
          : raw['id']! as String,
      channelType: channelType,
      avatarUrl: raw['avatarUrl'] as String? ?? peer?.avatarUrl,
      unread: (item['unreadCount'] as num?)?.toInt() ?? 0,
      muted:
          membership?['notificationsMuted'] as bool? ??
          membership?['mutedUntil'] != null,
      pinned: membership?['pinned'] as bool? ?? false,
      saved: membership?['saved'] as bool? ?? false,
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
      currentUserRole: membership?['role'] as String?,
      memberCount:
          (item['memberCount'] as num?)?.toInt() ?? resolvedMembers.length,
    );
  }

  int _channelTypeForConversationType(String type) => switch (type) {
    'direct' => 1,
    'group' => 2,
    'customer_service' => 3,
    'community' => 4,
    'community_topic' => 5,
    'info' => 6,
    'live' => 9,
    'visitor' => 10,
    _ => 0,
  };

  @override
  Future<List<BusinessChannelSummary>> businessChannels({
    int channelType = 0,
    String category = '',
    String parentId = '',
    int limit = 100,
  }) => _business.businessChannels(
    channelType: channelType,
    category: category,
    parentId: parentId,
    limit: limit,
  );

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
  }) => _business.createBusinessChannel(
    channelType: channelType,
    name: name,
    parentId: parentId,
    description: description,
    visibility: visibility,
    joinPolicy: joinPolicy,
    postingPolicy: postingPolicy,
    slowModeSeconds: slowModeSeconds,
  );

  @override
  Future<BusinessChannelSummary> businessChannel(
    String channelId,
    int channelType,
  ) => _business.businessChannel(channelId, channelType);

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
  }) => _business.updateBusinessChannel(
    channelId,
    channelType,
    name: name,
    description: description,
    visibility: visibility,
    joinPolicy: joinPolicy,
    postingPolicy: postingPolicy,
    slowModeSeconds: slowModeSeconds,
    sendBan: sendBan,
    allowStranger: allowStranger,
  );

  @override
  Future<List<BusinessChannelMemberSummary>> businessChannelMembers(
    String channelId,
    int channelType,
  ) => _business.businessChannelMembers(channelId, channelType);

  @override
  Future<void> addBusinessChannelMember(
    String channelId,
    int channelType,
    String userId, {
    DateTime? expiresAt,
  }) => _business.addBusinessChannelMember(
    channelId,
    channelType,
    userId,
    expiresAt: expiresAt,
  );

  @override
  Future<void> removeBusinessChannelMember(
    String channelId,
    int channelType,
    String userId,
  ) => _business.removeBusinessChannelMember(channelId, channelType, userId);

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
  }) => _business.updateBusinessChannelMember(
    channelId,
    channelType,
    userId,
    role: role,
    mutedUntil: mutedUntil,
    clearMute: clearMute,
    expiresAt: expiresAt,
    clearExpiry: clearExpiry,
  );

  @override
  Future<void> setBusinessChannelAccess(
    String channelId,
    int channelType,
    String userId,
    String accessType,
    bool enabled, {
    String reason = '',
  }) => _business.setBusinessChannelAccess(
    channelId,
    channelType,
    userId,
    accessType,
    enabled,
    reason: reason,
  );

  @override
  Future<List<BusinessChannelAccessSummary>> businessChannelAccess(
    String channelId,
    int channelType, {
    String accessType = '',
  }) => _business.businessChannelAccess(
    channelId,
    channelType,
    accessType: accessType,
  );

  @override
  Future<void> subscribeBusinessChannel(
    String channelId,
    int channelType, {
    DateTime? expiresAt,
  }) => _business.subscribeBusinessChannel(
    channelId,
    channelType,
    expiresAt: expiresAt,
  );

  @override
  Future<void> unsubscribeBusinessChannel(String channelId, int channelType) =>
      _business.unsubscribeBusinessChannel(channelId, channelType);

  @override
  Future<List<SupportSkillGroupSummary>> supportSkillGroups() =>
      _business.supportSkillGroups();

  @override
  Future<List<SupportAgentSummary>> supportAgents({String skillGroupId = ''}) =>
      _business.supportAgents(skillGroupId: skillGroupId);

  @override
  Future<SupportAgentSummary> setSupportAgentStatus(String status) =>
      _business.setSupportAgentStatus(status);

  @override
  Future<List<SupportSessionSummary>> supportSessions({
    String status = '',
    String skillGroupId = '',
  }) => _business.supportSessions(status: status, skillGroupId: skillGroupId);

  @override
  Future<SupportSessionSummary> createSupportSession({
    required String skillGroupId,
    String subject = '',
    int channelType = 10,
  }) => _business.createSupportSession(
    skillGroupId: skillGroupId,
    subject: subject,
    channelType: channelType,
  );

  @override
  Future<SupportSessionSummary> claimSupportSession(String sessionId) =>
      _business.claimSupportSession(sessionId);

  @override
  Future<SupportSessionSummary> transferSupportSession(
    String sessionId,
    String targetAgentId,
  ) => _business.transferSupportSession(sessionId, targetAgentId);

  @override
  Future<SupportSessionSummary> endSupportSession(String sessionId) =>
      _business.endSupportSession(sessionId);

  @override
  Future<SupportSessionSummary> rateSupportSession(
    String sessionId,
    int rating,
    String comment,
  ) => _business.rateSupportSession(sessionId, rating, comment);

  @override
  Future<BusinessMedia> uploadBusinessMedia(
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  }) => _business.uploadBusinessMedia(upload, onProgress: onProgress);

  @override
  Future<MomentPage> moments({
    String authorId = '',
    String cursor = '',
    int limit = 20,
  }) => _business.moments(authorId: authorId, cursor: cursor, limit: limit);

  @override
  Future<MomentSummary> createMoment({
    required String content,
    required String mediaKind,
    required List<String> mediaIds,
    required String visibility,
    List<String> visibleUserIds = const [],
    Map<String, Object?> location = const {},
  }) => _business.createMoment(
    content: content,
    mediaKind: mediaKind,
    mediaIds: mediaIds,
    visibility: visibility,
    visibleUserIds: visibleUserIds,
    location: location,
  );

  @override
  Future<MomentSummary> setMomentLike(String momentId, bool active) =>
      _business.setMomentLike(momentId, active);

  @override
  Future<MomentCommentSummary> createMomentComment(
    String momentId,
    String content, {
    String parentId = '',
  }) => _business.createMomentComment(momentId, content, parentId: parentId);

  @override
  Future<void> deleteMoment(String momentId) =>
      _business.deleteMoment(momentId);

  @override
  Future<void> deleteMomentComment(String momentId, String commentId) =>
      _business.deleteMomentComment(momentId, commentId);

  @override
  Future<List<MomentReminderSummary>> momentReminders({int limit = 100}) =>
      _business.momentReminders(limit: limit);

  @override
  Future<void> markMomentRemindersRead(List<int> reminderIds) =>
      _business.markMomentRemindersRead(reminderIds);

  @override
  Future<List<StickerCategorySummary>> stickerCategories() =>
      _business.stickerCategories();

  @override
  Future<List<StickerPackSummary>> stickerPacks({String categoryId = ''}) =>
      _business.stickerPacks(categoryId: categoryId);

  @override
  Future<StickerPackSummary> stickerPack(String packId) =>
      _business.stickerPack(packId);

  @override
  Future<void> setStickerPackFavorite(String packId, bool active) =>
      _business.setStickerPackFavorite(packId, active);

  @override
  Future<void> setStickerFavorite(String stickerId, bool active) =>
      _business.setStickerFavorite(stickerId, active);

  @override
  Future<void> recordStickerUse(String stickerId) =>
      _business.recordStickerUse(stickerId);

  @override
  Future<List<StickerItemSummary>> recentStickers({int limit = 50}) =>
      _business.recentStickers(limit: limit);

  @override
  Future<List<StickerItemSummary>> favoriteStickers({int limit = 50}) =>
      _business.favoriteStickers(limit: limit);

  @override
  Future<List<AppUser>> contacts() async {
    final data = await _get('/v2/contacts/friends');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => _user(item! as Map<String, Object?>))
        .toList();
  }

  @override
  Future<List<UserPresenceSnapshot>> userPresence(
    List<String> userIds, {
    String? groupId,
  }) async {
    final data = await _sendRequest('POST', '/v2/users/presence', {
      'userIds': userIds,
      'groupId': ?groupId,
    });
    return (data['items'] as List)
        .map(
          (row) => UserPresenceSnapshot.fromJson(
            Map<String, Object?>.from(row as Map),
          ),
        )
        .toList();
  }

  @override
  Future<List<AppUser>> searchUsers(
    String query, {
    String by = 'handle',
  }) async {
    final encoded = Uri.encodeQueryComponent(query.trim());
    final encodedBy = Uri.encodeQueryComponent(by);
    final data = await _get('/v2/contacts/search?q=$encoded&by=$encodedBy');
    return (data['items'] as List<Object?>? ?? const [])
        .map((item) => _user(item! as Map<String, Object?>))
        .where((user) => user.id != _userId)
        .toList();
  }

  @override
  Future<UserSearchCapabilities> searchCapabilities() async {
    final data = await _get('/v2/contacts/search/capabilities');
    return UserSearchCapabilities(
      allowSearchByHandle: data['allowSearchByHandle'] as bool? ?? false,
      allowSearchByPhone: data['allowSearchByPhone'] as bool? ?? false,
      canUpdatePrivacySettings:
          data['canUpdatePrivacySettings'] as bool? ?? false,
    );
  }

  AppUser _user(Map<String, Object?> item) => AppUser(
    canDeleteMessagesForEveryone: item['canDeleteMessagesForEveryone'] == true,
    id: item['id']! as String,
    name: item['name'] as String? ?? item['id']! as String,
    handle:
        item['handle'] as String? ??
        item['phone'] as String? ??
        item['id']! as String,
    presence:
        item['signature'] as String? ?? item['presence'] as String? ?? '青蛙呱呱用户',
    phone: item['phone'] as String?,
    signature: item['signature'] as String?,
    gender: switch (item['gender']) {
      'male' => 'male',
      'female' => 'female',
      _ => 'unspecified',
    },
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
    final data = await _get('/v2/contacts/requests');
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
      final embeddedUser = switch (item['user']) {
        final Map<String, Object?> value => _user(value),
        final Map value => _user(
          value.map((key, value) => MapEntry(key.toString(), value)),
        ),
        _ => null,
      };
      return FriendRequest(
        id: item['id']! as String,
        user:
            embeddedUser ??
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
  }) => _sendRequest('POST', '/v2/contacts/requests', {
    'userId': userId,
    'message': note,
    'source': source,
    'sourceId': ?sourceId,
  }).then((_) {});

  @override
  Future<void> acceptFriendRequest(String requestId) => _sendRequest(
    'POST',
    '/v2/contacts/requests/$requestId/accept',
  ).then((_) {});

  @override
  Future<void> rejectFriendRequest(String requestId) => _sendRequest(
    'POST',
    '/v2/contacts/requests/$requestId/reject',
  ).then((_) {});

  @override
  Future<void> cancelFriendRequest(String requestId) => _sendRequest(
    'POST',
    '/v2/contacts/requests/$requestId/cancel',
  ).then((_) {});

  @override
  Future<void> updateFriendMetadata(
    String userId, {
    required String remark,
    required List<String> tags,
  }) => _sendRequest('PATCH', '/v2/contacts/friends/$userId', {
    'remark': remark,
    'tags': tags,
  }).then((_) {});

  @override
  Future<void> deleteFriend(String userId) =>
      _sendRequest('DELETE', '/v2/contacts/friends/$userId').then((_) {});

  @override
  Future<void> blockUser(String userId, bool blocked) => _sendRequest(
    'PUT',
    '/v2/contacts/blocks/$userId',
    {'blocked': blocked},
  ).then((_) {});

  @override
  Future<List<AppUser>> blockedUsers() async {
    final data = await _get('/v2/contacts/blocks');
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
  }) => _sendRequest('POST', '/v2/reports', {
    'targetType': targetType,
    'targetId': targetId,
    'reason': reason,
    'details': details,
  }).then((_) {});

  @override
  Future<Conversation> createDirect(AppUser user) async {
    final data = await _sendRequest('POST', '/v2/channels/direct', {
      'userId': user.id,
    });
    final conversation = _conversation(
      data,
      titleOverride: user.name,
      members: [user],
    );
    _registerConversation(conversation);
    return conversation;
  }

  @override
  Future<Conversation> createGroup(String name, List<AppUser> members) async {
    final data = await _sendRequest('POST', '/v2/channels/groups', {
      'name': name,
      'memberIds': members.map((user) => user.id).toList(),
    });
    final conversation = _conversation(
      data,
      titleOverride: name,
      members: members,
    );
    _registerConversation(conversation);
    return conversation;
  }

  @override
  Future<GroupProfile> groupProfile(String conversationId) async {
    final sessionUserId = _userId;
    final raw = await _get('/v2/channels/groups/$conversationId');
    if (_userId == sessionUserId) {
      await _applyGroupHistory(conversationId, raw['historyAccess']);
    }
    return _groupProfile(raw);
  }

  GroupProfile _groupProfile(Map<String, Object?> item) => GroupProfile(
    historyVisibleToNewMembers: item['historyVisibleToNewMembers'] == true,
    historyPolicyVersion: (item['historyPolicyVersion'] as num?)?.toInt() ?? 1,
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

  DateTime? _tryDate(Object? value) => tryParseLocalDateTime(value);

  @override
  Future<GroupProfile> updateGroupProfile(
    String conversationId, {
    String? name,
    String? avatarMediaId,
    String? joinPolicy,
    bool? allowMemberAddFriend,
    bool? historyVisibleToNewMembers,
    bool rotateQr = false,
  }) async {
    final sessionUserId = _userId;
    final raw =
        await _sendRequest('PATCH', '/v2/channels/groups/$conversationId', {
          'name': ?name,
          'avatarMediaId': ?avatarMediaId,
          'joinPolicy': ?joinPolicy,
          'allowMemberAddFriend': ?allowMemberAddFriend,
          'historyVisibleToNewMembers': ?historyVisibleToNewMembers,
          if (rotateQr) 'rotateQr': true,
        });
    // Avatar/name/profile mutations must not be reported as failed after the
    // server has committed them merely because an unrelated local history
    // cache cleanup fails. History-policy changes still require the strict,
    // fail-closed cache update.
    if (_userId == sessionUserId && historyVisibleToNewMembers != null) {
      await _applyGroupHistory(conversationId, raw['historyAccess']);
    }
    return _groupProfile(raw);
  }

  @override
  Future<GroupProfile> setGroupAnnouncement(
    String conversationId,
    String content,
  ) async => _groupProfile(
    await _sendRequest(
      'PUT',
      '/v2/channels/groups/$conversationId/announcement',
      {'content': content},
    ),
  );

  @override
  Future<void> markGroupAnnouncementRead(String conversationId) => _sendRequest(
    'POST',
    '/v2/channels/groups/$conversationId/announcement/read',
  ).then((_) {});

  @override
  Future<List<GroupMember>> groupMembers(String conversationId) async {
    final rows = <Object?>[];
    String cursor = '';
    do {
      final suffix = cursor.isEmpty
          ? '?limit=200'
          : '?limit=200&cursor=${Uri.encodeQueryComponent(cursor)}';
      final data = await _get(
        '/v2/channels/groups/$conversationId/members$suffix',
      );
      rows.addAll(data['items'] as List<Object?>? ?? const []);
      cursor = data['nextCursor'] as String? ?? '';
    } while (cursor.isNotEmpty);
    final known = <String, AppUser>{
      for (final user in await contacts()) user.id: user,
    };
    final me = currentUser;
    if (me != null) known[me.id] = me;
    return rows.map((raw) {
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
  Future<List<RobotProfile>> robotProfiles(String conversationId) async {
    final encoded = Uri.encodeComponent(conversationId);
    final data = await _get('/v2/robots/conversations/$encoded');
    return (data['items'] as List<Object?>? ?? const [])
        .whereType<Map<String, Object?>>()
        .where((item) => (item['status'] as num?)?.toInt() == 1)
        .map((item) {
          final robotId = item['robot_id'] as String? ?? '';
          final menus = (item['menus'] as List<Object?>? ?? const [])
              .whereType<Map<String, Object?>>()
              .map(
                (menu) => RobotMenu(
                  robotId: robotId,
                  command: menu['cmd'] as String? ?? '',
                  remark: menu['remark'] as String? ?? '',
                  type: menu['type'] as String? ?? 'command',
                ),
              )
              .where((menu) => menu.command.trim().isNotEmpty)
              .toList(growable: false);
          return RobotProfile(
            id: robotId,
            name: item['name'] as String? ?? '',
            username: item['username'] as String? ?? '',
            placeholder: item['placeholder'] as String? ?? '',
            version: (item['version'] as num?)?.toInt() ?? 0,
            inlineOn: (item['inline_on'] as num?)?.toInt() == 1,
            menus: menus,
          );
        })
        .where((item) => item.id.isNotEmpty && item.menus.isNotEmpty)
        .toList(growable: false);
  }

  @override
  Future<void> addGroupMembers(String conversationId, List<String> userIds) =>
      _sendRequest('POST', '/v2/channels/groups/$conversationId/members', {
        'userIds': userIds,
      }).then((_) {});

  @override
  Future<void> inviteGroupMember(String conversationId, String userId) =>
      _sendRequest('POST', '/v2/channels/groups/$conversationId/invites', {
        'userId': userId,
      }).then((_) {});

  @override
  Future<List<GroupInvitation>> groupInvitations() async {
    final data = await _get('/v2/channels/group-invitations?limit=100');
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
        '/v2/channels/group-invitations/$invitationId/$action',
      ).then((_) {});

  @override
  Future<void> joinGroupByQr(String token) => _sendRequest(
    'POST',
    '/v2/channels/groups/join/qr',
    {'token': token},
  ).then((_) {});

  @override
  Future<void> removeGroupMember(String conversationId, String userId) =>
      _sendRequest(
        'DELETE',
        '/v2/channels/groups/$conversationId/members/$userId',
      ).then((_) {});

  @override
  Future<void> setGroupRole(
    String conversationId,
    String userId,
    String role,
  ) => _sendRequest(
    'PUT',
    '/v2/channels/groups/$conversationId/members/$userId/role',
    {'role': role},
  ).then((_) {});

  @override
  Future<void> setGroupMemberMuted(
    String conversationId,
    String userId,
    DateTime? until,
  ) => _sendRequest(
    'PUT',
    '/v2/channels/groups/$conversationId/members/$userId/mute',
    {'until': until?.toUtc().toIso8601String()},
  ).then((_) {});

  @override
  Future<void> transferGroupOwner(String conversationId, String userId) =>
      _sendRequest(
        'POST',
        '/v2/channels/groups/$conversationId/owner/transfer',
        {'userId': userId},
      ).then((_) {});

  @override
  Future<void> setGroupNickname(String conversationId, String nickname) =>
      _sendRequest('PATCH', '/v2/channels/groups/$conversationId/nickname', {
        'nickname': nickname,
      }).then((_) {});

  @override
  Future<GroupProfile> setGroupAllMuted(
    String conversationId,
    bool muted,
  ) async => _groupProfile(
    await _sendRequest('PUT', '/v2/channels/groups/$conversationId/mute-all', {
      'until': muted
          ? DateTime.now()
                .add(const Duration(days: 3650))
                .toUtc()
                .toIso8601String()
          : DateTime.now().toUtc().toIso8601String(),
    }),
  );

  @override
  Future<void> leaveGroup(String conversationId) => _sendRequest(
    'POST',
    '/v2/channels/groups/$conversationId/leave',
  ).then((_) {});

  @override
  Future<void> disbandGroup(String conversationId, String reason) =>
      _sendRequest('POST', '/v2/channels/groups/$conversationId/disband', {
        'reason': reason,
      }).then((_) {});

  @override
  Future<List<ChatMessage>> messages(String conversationId) async {
    final channel = await _channelForConversation(conversationId);
    if (channel == null) {
      throw const ImApiException(
        statusCode: 404,
        code: 'IM_CHANNEL_NOT_FOUND',
        message: 'Conversation has no WuKongIM channel',
      );
    }
    final uid = _userId;
    if (uid == null) {
      throw const ImApiException(
        statusCode: 401,
        code: 'UNAUTHENTICATED',
        message: 'Login is required',
      );
    }
    try {
      await _syncDeletions(conversationId, channel);
      if (channel.type == 2 && !_groupHistory.containsKey(channel.id)) {
        await _refreshGroupHistory(conversationId);
      }
      final data = await _business.syncMessages(
        channel: channel,
        startMessageSeq: 0,
        endMessageSeq: 0,
        limit: 50,
        pullMode: 1,
      );
      final rawMessages = data['messages'] as List<Object?>? ?? const [];
      final synced =
          rawMessages.whereType<Map>().map((raw) {
            final normalized = <String, Object?>{
              ...wukongObjectMap(raw),
              'channel_id': raw['channel_id'] ?? raw['channelId'] ?? channel.id,
              'channel_type':
                  raw['channel_type'] ?? raw['channelType'] ?? channel.type,
            };
            return WukongMessage.fromSyncJson(normalized);
          }).toList()..sort((a, b) {
            final bySequence = a.messageSeq.compareTo(b.messageSeq);
            return bySequence != 0
                ? bySequence
                : a.timestamp.compareTo(b.timestamp);
          });
      final merged = await _conversationCache.mergeMessages(
        uid,
        channel,
        synced,
      );
      final parsed = merged
          .map(
            (message) => _messageMapper.toChatMessage(
              message,
              currentUserId: uid,
              conversationId: conversationId,
            ),
          )
          .toList();
      final byId = <String, ChatMessage>{
        for (final message in parsed) message.id: message,
      };
      final result = parsed
          .map(
            (message) => message.replyToId == null
                ? message
                : message.copyWith(
                    replyToText: byId[message.replyToId]?.text ?? '原消息暂不可见',
                  ),
          )
          .toList();
      if (channel.type == 2) {
        // An authoritative history page has no local failed/queued uploads.
        // Retain those from the page cache while resetting visible history.
        final local = await _readPageMessageSnapshot(conversationId);
        if (_userId != uid) return const [];
        for (final pending in local) {
          if (pending.conversationSeq == 0 &&
              pending.senderId == uid &&
              (pending.status == MessageStatus.sending ||
                  pending.status == MessageStatus.failed) &&
              !result.any(
                (m) =>
                    m.id == pending.id ||
                    m.stableIdentity == pending.stableIdentity,
              )) {
            result.add(pending);
          }
        }
      }
      await persistMessages(conversationId, result);
      return result.where(canReadCachedMessage).toList();
    } catch (_) {
      final cached = await _conversationCache.readMessages(uid, channel);
      if (cached.isNotEmpty) {
        return cached
            .map(
              (message) => _messageMapper.toChatMessage(
                message,
                currentUserId: uid,
                conversationId: conversationId,
              ),
            )
            .toList();
      }
      rethrow;
    }
  }

  @override
  Future<List<ChatMessage>> cachedMessages(String conversationId) async {
    if (!_conversationChannels.containsKey(conversationId)) {
      final raw = await _store.readJson('channel.$conversationId');
      if (raw is Map && raw['id'] is String && raw['type'] is num) {
        _conversationChannels[conversationId] = WukongChannel(
          id: raw['id'] as String,
          type: (raw['type'] as num).toInt(),
        );
      }
    }
    final deletionUid = _userId;
    final deletionChannel = _conversationChannels[conversationId];
    if (deletionUid != null && deletionChannel != null) {
      try {
        await _syncDeletions(conversationId, deletionChannel);
      } catch (_) {
        // Offline/failed validation may still return drafts and pending sends,
        // but never an unverified historical snapshot.
        _deletionVerified.remove('$deletionUid:$conversationId');
      }
    }
    // Both stores are encrypted local data. On a cold Android start either
    // read can wait for keystore initialisation, so start them together. A
    // damaged snapshot must not hide a healthy WuKong cache (or vice versa).
    final pageSnapshot = _readPageMessageSnapshot(conversationId);
    final uid = _userId;
    final channel = _conversationChannels[conversationId];
    final sdkSnapshot = uid == null || channel == null
        ? Future<List<ChatMessage>>.value(const [])
        : _readWukongMessageSnapshot(
            conversationId: conversationId,
            uid: uid,
            channel: channel,
          );
    final snapshots = await Future.wait([pageSnapshot, sdkSnapshot]);
    final decoded = snapshots[0];
    final sdkCached = snapshots[1];

    // The page snapshot may predate messages received while this chat was
    // closed. WuKongIM has already committed those events to its local cache,
    // so merge both stores instead of returning the first non-empty one. This
    // is what lets a conversation open on the latest message without waiting
    // for the remote history endpoint.
    final merged = List<ChatMessage>.of(decoded);
    for (final message in sdkCached) {
      final index = merged.indexWhere(
        (existing) =>
            existing.id == message.id ||
            existing.clientMessageId == message.clientMessageId,
      );
      if (index < 0) {
        merged.add(message);
        continue;
      }
      final pageMessage = merged[index];
      merged[index] = message.copyWith(
        status: pageMessage.status == MessageStatus.recalled
            ? MessageStatus.recalled
            : message.status,
        text: pageMessage.status == MessageStatus.recalled ? '' : message.text,
        replyToText: message.replyToText ?? pageMessage.replyToText,
        linkPreview: message.linkPreview ?? pageMessage.linkPreview,
        reactions: message.reactions.isEmpty
            ? pageMessage.reactions
            : message.reactions,
      );
    }
    merged.sort((left, right) {
      if (left.conversationSeq > 0 && right.conversationSeq > 0) {
        final bySequence = left.conversationSeq.compareTo(
          right.conversationSeq,
        );
        if (bySequence != 0) return bySequence;
      }
      final byTime = left.sentAt.compareTo(right.sentAt);
      return byTime != 0 ? byTime : left.id.compareTo(right.id);
    });
    return merged.where(canReadCachedMessage).toList();
  }

  Future<List<ChatMessage>> _readPageMessageSnapshot(
    String conversationId,
  ) async {
    Object? persisted;
    try {
      persisted = await _store.readJson('messages.$conversationId');
    } catch (_) {
      return const [];
    }
    final decoded = <ChatMessage>[];
    if (persisted is List<Object?> && persisted.isNotEmpty) {
      for (final item in persisted) {
        if (item is! Map) continue;
        try {
          decoded.add(
            ChatMessage.fromJson(
              item.map((key, value) => MapEntry(key.toString(), value)),
            ),
          );
        } catch (_) {
          // Ignore one damaged cache entry instead of blocking the chat page.
        }
      }
    }
    return decoded;
  }

  Future<List<ChatMessage>> _readWukongMessageSnapshot({
    required String conversationId,
    required String uid,
    required WukongChannel channel,
  }) async {
    try {
      return (await _conversationCache.readMessages(uid, channel))
          .map(
            (message) => _messageMapper.toChatMessage(
              message,
              currentUserId: uid,
              conversationId: conversationId,
            ),
          )
          .toList();
    } catch (_) {
      return const [];
    }
  }

  @override
  Future<List<ChatMessage>> olderMessages(
    String conversationId, {
    required int beforeSequence,
    int limit = 50,
  }) async {
    if (beforeSequence <= 1 || limit <= 0) return const [];
    final channel = await _channelForConversation(conversationId);
    if (channel == null) {
      throw const ImApiException(
        statusCode: 404,
        code: 'IM_CHANNEL_NOT_FOUND',
        message: 'Conversation has no WuKongIM channel',
      );
    }
    final uid = _userId;
    if (uid == null) {
      throw const ImApiException(
        statusCode: 401,
        code: 'UNAUTHENTICATED',
        message: 'Login is required',
      );
    }

    List<WukongMessage> synced = const [];
    try {
      final data = await _business.syncMessages(
        channel: channel,
        // WuKongIM pullMode=0 walks backward from the sequence immediately
        // preceding the oldest message already displayed by the client.
        startMessageSeq: max(0, beforeSequence - 1),
        endMessageSeq: 0,
        limit: min(limit, 100),
        pullMode: 0,
      );
      final rawMessages = data['messages'] as List<Object?>? ?? const [];
      synced =
          rawMessages
              .whereType<Map>()
              .map((raw) {
                final normalized = <String, Object?>{
                  ...wukongObjectMap(raw),
                  'channel_id':
                      raw['channel_id'] ?? raw['channelId'] ?? channel.id,
                  'channel_type':
                      raw['channel_type'] ?? raw['channelType'] ?? channel.type,
                };
                return WukongMessage.fromSyncJson(normalized);
              })
              .where(
                (message) =>
                    message.messageSeq > 0 &&
                    message.messageSeq < beforeSequence,
              )
              .toList()
            ..sort((a, b) {
              final bySequence = a.messageSeq.compareTo(b.messageSeq);
              return bySequence != 0
                  ? bySequence
                  : a.timestamp.compareTo(b.timestamp);
            });
      await _conversationCache.mergeMessages(uid, channel, synced);
    } catch (_) {
      final cached = await _conversationCache.readMessages(uid, channel);
      synced =
          cached
              .where(
                (message) =>
                    message.messageSeq > 0 &&
                    message.messageSeq < beforeSequence,
              )
              .toList()
            ..sort((a, b) => a.messageSeq.compareTo(b.messageSeq));
      if (synced.length > limit) {
        synced = synced.sublist(synced.length - limit);
      }
      if (synced.isEmpty) rethrow;
    }

    final current = await _conversationCache.readMessages(uid, channel);
    final replySources = <String, ChatMessage>{};
    final mappedCurrent = current.map(
      (message) => _messageMapper.toChatMessage(
        message,
        currentUserId: uid,
        conversationId: conversationId,
      ),
    );
    final mappedPage = synced.map(
      (message) => _messageMapper.toChatMessage(
        message,
        currentUserId: uid,
        conversationId: conversationId,
      ),
    );
    for (final message in [...mappedCurrent, ...mappedPage]) {
      replySources[message.id] = message;
    }
    final result = mappedPage
        .where(canReadCachedMessage)
        .map(
          (message) => message.replyToId == null
              ? message
              : message.copyWith(
                  replyToText:
                      replySources[message.replyToId]?.text ?? '原消息暂不可见',
                ),
        )
        .toList();
    return result;
  }

  ChatMessage _message(Map<String, Object?> item, String conversationId) {
    final body = item['body'] as Map<String, Object?>? ?? const {};
    final reply = body['reply'] is Map
        ? wukongObjectMap(body['reply'])
        : item['reply'] is Map
        ? wukongObjectMap(item['reply'])
        : const <String, Object?>{};
    final kindName =
        item['type'] as String? ??
        item['messageType'] as String? ??
        body['type'] as String? ??
        item['contentType'] as String?;
    final replyToId =
        item['replyToId'] as String? ??
        body['replyToId'] as String? ??
        reply['message_id'] as String? ??
        reply['messageId'] as String?;
    final kind = switch (kindName) {
      'image' => MessageContentKind.image,
      'voice' || 'audio' => MessageContentKind.voice,
      'video' => MessageContentKind.video,
      'file' => MessageContentKind.file,
      'reply' => MessageContentKind.reply,
      'contact' => MessageContentKind.contact,
      'location' => MessageContentKind.location,
      'chat_history' => MessageContentKind.chatHistory,
      'sticker' || 'store_sticker' => MessageContentKind.sticker,
      'moment' || 'moment_share' => MessageContentKind.momentShare,
      'live' || 'live_event' => MessageContentKind.liveEvent,
      'system' ||
      'call' ||
      'call_event' ||
      'support' ||
      'support_event' => MessageContentKind.system,
      'screenshot' ||
      'screenshot_notice' => MessageContentKind.screenshotNotice,
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
    final senderId = item['senderId']! as String;
    final senderName = item['senderName'] as String? ?? '联系人';
    final isMine = senderId == _userId;
    return ChatMessage(
      id: item['id']! as String,
      clientMessageId: item['clientMsgId'] as String?,
      conversationId: item['conversationId'] as String? ?? conversationId,
      senderId: senderId,
      senderName: senderName,
      text: kind == MessageContentKind.screenshotNotice
          ? isMine
                ? '你截取了聊天界面'
                : '$senderName 截取了聊天界面'
          : kindName == 'call' || kindName == 'call_event'
          ? callEventDisplayText(body)
          : kindName == 'support' || kindName == 'support_event'
          ? supportEventDisplayText(body)
          : _messageText(item),
      kind: kind,
      mediaUrl:
          body['url'] as String? ??
          body['downloadUrl'] as String? ??
          body['localPath'] as String?,
      mediaId: body['mediaId'] as String?,
      mediaWidth: (body['width'] as num?)?.toInt(),
      mediaHeight: (body['height'] as num?)?.toInt(),
      coverMediaId: body['coverMediaId'] as String?,
      coverUrl: body['cover'] as String?,
      stickerId: body['stickerId'] as String?,
      momentId: body['momentId'] as String?,
      event: body['event'] as String?,
      robotId:
          body['robot_id'] as String? ??
          body['robotId'] as String? ??
          item['robot_id'] as String? ??
          item['robotId'] as String?,
      eventData: body['data'] is Map
          ? Map<String, Object?>.from(body['data']! as Map)
          : const {},
      chatHistoryEntries: chatHistoryEntriesFrom(body['entries']),
      fileName: body['fileName'] as String?,
      mimeType: body['mime'] as String? ?? body['mimeType'] as String?,
      durationSeconds: ((body['second'] ?? body['duration']) as num?)?.toInt(),
      replyToId: replyToId?.isEmpty == true ? null : replyToId,
      replyToText:
          body['replyToText'] as String? ?? reply['content'] as String?,
      replyToSeq:
          (body['replyToSeq'] as num?)?.toInt() ??
          (reply['message_seq'] as num?)?.toInt() ??
          (reply['messageSeq'] as num?)?.toInt() ??
          0,
      replyToSenderId:
          body['replyToSenderId'] as String? ??
          reply['from_uid'] as String? ??
          reply['fromUid'] as String?,
      replyToSenderName:
          body['replyToSenderName'] as String? ??
          reply['from_name'] as String? ??
          reply['fromName'] as String?,
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
      sentAt: parseLocalDateTime(
        (item['createdAt'] ?? item['sentAt'])! as String,
      ),
      isMine: isMine,
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
    if (body != null && isGroupSystemEvent(body)) {
      return groupSystemEventDisplayText(body);
    }
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
      'sticker' || 'store_sticker' =>
        body?['digest'] as String? ?? body?['content'] as String? ?? '[表情]',
      'moment' || 'moment_share' =>
        body?['content'] as String? ?? body?['digest'] as String? ?? '[朋友圈]',
      'live' || 'live_event' =>
        body?['digest'] as String? ?? body?['content'] as String? ?? '[直播互动]',
      'system' =>
        body?['digest'] as String? ?? body?['content'] as String? ?? '[系统消息]',
      'call' || 'call_event' => callEventDisplayText(body ?? const {}),
      'support' || 'support_event' => supportEventDisplayText(body ?? const {}),
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
    final channel = await _channelForConversation(pending.conversationId);
    if (channel == null) {
      throw const ImApiException(
        statusCode: 404,
        code: 'IM_CHANNEL_NOT_FOUND',
        message: 'Conversation has no WuKongIM channel',
      );
    }
    if (_wukong.connectionState != WukongConnectionState.connected) {
      await connect();
    }
    var source = pending;
    if (pending.mediaId?.isNotEmpty == true &&
        {
          MessageContentKind.image,
          MessageContentKind.voice,
          MessageContentKind.video,
          MessageContentKind.file,
        }.contains(pending.kind)) {
      // A failed SENDACK does not invalidate a completed upload. Rebind for a
      // fresh authorised URL, even if the original local file no longer exists.
      final remoteURL = await _business.bindMedia(pending.mediaId!, channel);
      source = pending.copyWith(mediaUrl: remoteURL);
      if (pending.kind == MessageContentKind.video) {
        source = await refreshMessageMedia(source);
      }
    }
    final outgoing = _messageMapper.toOutgoing(source, channel: channel);
    return _dispatchWukongSend(source: source, outgoing: outgoing);
  }

  @override
  Future<ChatMessage> editMessage(String messageId, String text) async {
    final data = await _sendRequest('PATCH', '/v2/messages/$messageId', {
      'editId': _newMutationId('edit'),
      'text': text,
    });
    final raw = data['message'] as Map<String, Object?>? ?? data;
    return _message(raw, raw['conversationId'] as String? ?? '');
  }

  @override
  Future<List<MessageEditRevision>> messageEditHistory(String messageId) async {
    final data = await _get('/v2/messages/$messageId/edits');
    return (data['items'] as List<Object?>? ?? const [])
        .whereType<Map<String, Object?>>()
        .map((item) {
          final rawBody = item['body'];
          return MessageEditRevision(
            messageId: item['messageId'] as String? ?? messageId,
            version: (item['version'] as num?)?.toInt() ?? 0,
            editorId: item['editorId'] as String? ?? '',
            body: rawBody is Map
                ? Map<String, Object?>.from(rawBody)
                : const <String, Object?>{},
            editedAt:
                _tryDate(item['editedAt']) ??
                DateTime.fromMillisecondsSinceEpoch(0),
          );
        })
        .toList(growable: false);
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
      '/v2/messages/$messageId/reactions/$encoded',
    );
    final raw = data['message'] as Map<String, Object?>? ?? data;
    return _message(raw, raw['conversationId'] as String? ?? '');
  }

  @override
  Future<List<ChatMessage>> pinnedMessages(String conversationId) async {
    final encodedConversationId = Uri.encodeQueryComponent(conversationId);
    final data = await _get(
      '/v2/messages/pins?conversationId=$encodedConversationId',
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
  }) {
    final encodedConversationId = Uri.encodeQueryComponent(conversationId);
    final encodedMessageId = Uri.encodeComponent(messageId);
    return _sendRequest(
      pinned ? 'PUT' : 'DELETE',
      '/v2/messages/pins/$encodedMessageId?conversationId=$encodedConversationId',
    ).then((_) {});
  }

  @override
  Future<List<ChatMessage>> searchMessages(
    String conversationId,
    String query, {
    int limit = 50,
  }) async {
    final encoded = Uri.encodeQueryComponent(query.trim());
    final encodedConversationId = Uri.encodeQueryComponent(conversationId);
    final data = await _get(
      '/v2/messages/search?conversationId=$encodedConversationId&q=$encoded&limit=$limit',
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
    final data = await _sendRequest('POST', '/v2/messages/forward', {
      'targetConversationId': targetConversationId,
      'sourceMessageIds': sourceMessageIds,
      'mode': mode,
      'clientBatchId': clientBatchId,
    });
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
    final account = _userId;
    void checkAccount() {
      if (account != _userId || account != pending.senderId) {
        throw const FormatException('登录账号已变化，已停止发送');
      }
    }

    checkAccount();
    onProgress?.call(0);
    String? coverId;
    if (upload.kind == MessageContentKind.video && upload.coverBytes != null) {
      try {
        coverId = await _uploadMediaPart(
          pending.clientMessageId,
          'cover',
          MediaUpload(
            bytes: upload.coverBytes!,
            fileName: 'video-cover.jpg',
            mimeType: 'image/jpeg',
            kind: MessageContentKind.image,
          ),
        );
      } catch (error) {
        if (error is ImApiException && error.statusCode == 401) rethrow;
      }
    }
    checkAccount();
    final mediaId = await _uploadMediaPart(
      pending.clientMessageId,
      'body',
      upload,
      coverId: coverId,
      onProgress: onProgress,
    );
    checkAccount();
    final channel = await _channelForConversation(pending.conversationId);
    checkAccount();
    if (channel == null) throw const FormatException('会话不存在');
    final remoteURL = await _business.bindMedia(mediaId, channel);
    checkAccount();
    var wireMessage = pending.copyWith(
      kind: upload.kind,
      mediaUrl: remoteURL,
      mediaId: mediaId,
      mediaWidth: upload.width,
      mediaHeight: upload.height,
      coverMediaId: coverId,
      fileName: upload.fileName,
      mimeType: upload.mimeType,
      durationSeconds: upload.durationSeconds,
    );
    if (upload.kind == MessageContentKind.video) {
      wireMessage = await refreshMessageMedia(wireMessage);
    }
    checkAccount();
    // Persist durable IDs before SDK delivery. A later ACK timeout must not
    // turn a retry into another media upload.
    final cached = await cachedMessages(pending.conversationId);
    checkAccount();
    await persistMessages(pending.conversationId, [
      for (final item in cached)
        if (item.clientMessageId != pending.clientMessageId) item,
      wireMessage,
    ]);
    if (_wukong.connectionState != WukongConnectionState.connected) {
      await connect();
    }
    checkAccount();
    final base = _messageMapper.toOutgoing(wireMessage, channel: channel);
    return _dispatchWukongSend(
      source: wireMessage,
      outgoing: WukongOutgoingMessage(
        channel: base.channel,
        clientMsgNo: base.clientMsgNo,
        expireSeconds: base.expireSeconds,
        payload: {
          ...base.payload,
          'size': upload.bytes.length,
          'checksum': sha256.convert(upload.bytes).toString(),
        },
      ),
    );
  }

  Future<String> _uploadMediaPart(
    String clientId,
    String part,
    MediaUpload upload, {
    String? coverId,
    void Function(double)? onProgress,
  }) async {
    final account = _userId;
    final key = 'media-upload-$account-$clientId-$part';
    void checkAccount() {
      if (account != _userId) throw const FormatException('登录账号已变化，已停止发送');
    }

    final cached = await _store.readJson(key);
    checkAccount();
    var prepared = cached is Map<String, Object?>
        ? cached
        : <String, Object?>{};
    if (prepared['completed'] == true) return prepared['mediaId'] as String;
    final expiry = DateTime.tryParse(prepared['expiresAt']?.toString() ?? '');
    if (prepared.isEmpty ||
        (prepared['uploaded'] != true &&
            (expiry == null || expiry.isBefore(DateTime.now())))) {
      checkAccount();
      prepared = await _sendRequest('POST', '/v2/media/presign', {
        'mime': upload.mimeType,
        'fileName': upload.fileName,
        'size': upload.bytes.length,
      });
      checkAccount();
      await _store.writeJson(key, prepared);
    }
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
    if (prepared['uploaded'] != true) {
      checkAccount();
      final request = http.StreamedRequest('PUT', Uri.parse(uploadUrl));
      request.headers.addAll(uploadHeaders);
      request.contentLength = upload.bytes.length;
      final responseFuture = _uploadClient
          .send(request)
          .timeout(const Duration(seconds: 45));
      const chunkSize = 64 * 1024;
      for (var start = 0; start < upload.bytes.length; start += chunkSize) {
        final end = min(start + chunkSize, upload.bytes.length);
        request.sink.add(upload.bytes.sublist(start, end));
        onProgress?.call(.95 * end / upload.bytes.length);
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
      checkAccount();
      prepared = {...prepared, 'uploaded': true};
      await _store.writeJson(key, prepared);
    }
    final checksum = sha256.convert(upload.bytes).toString();
    checkAccount();
    await _sendRequest('POST', '/v2/media/$mediaId/complete', {
      'checksum': checksum,
      'coverMediaId': ?coverId,
    });
    checkAccount();
    await _store.writeJson(key, {...prepared, 'completed': true});
    onProgress?.call(1);
    return mediaId;
  }

  @override
  Future<void> saveFavorite(ChatMessage message) async {
    final uid = _userId;
    final epoch = _sessionEpoch;
    if (!message.id.startsWith('local-')) {
      await _sendRequest(
        'PUT',
        '/v2/messages/favorites/${Uri.encodeComponent(message.id)}',
      );
    }
    final stored = await _store.readJson('favorites');
    if (uid == null || uid != _userId || epoch != _sessionEpoch) return;
    final favorites = stored is List<Object?>
        ? stored.whereType<Map<String, Object?>>().toList()
        : <Map<String, Object?>>[];
    favorites.removeWhere(
      (item) => item['clientMessageId'] == message.clientMessageId,
    );
    favorites.insert(0, message.toJson());
    await _store.writeJson(
      'favorites',
      _purgeDeletedCache(uid, favorites.take(500).toList()),
    );
  }

  @override
  Future<void> removeFavorite(ChatMessage message) async {
    final uid = _userId;
    final epoch = _sessionEpoch;
    if (!message.id.startsWith('local-')) {
      await _sendRequest(
        'DELETE',
        '/v2/messages/favorites/${Uri.encodeComponent(message.id)}',
      );
    }
    final stored = await _store.readJson('favorites');
    if (uid == null || uid != _userId || epoch != _sessionEpoch) return;
    final favorites = stored is List<Object?>
        ? stored.whereType<Map<String, Object?>>().toList()
        : <Map<String, Object?>>[];
    favorites.removeWhere(
      (item) =>
          item['id'] == message.id ||
          item['clientMessageId'] == message.clientMessageId,
    );
    await _store.writeJson('favorites', _purgeDeletedCache(uid, favorites));
  }

  @override
  Future<void> markRead(String conversationId, int sequence) async {
    await _sendRequest(
      'PUT',
      '/v2/channels/conversations/$conversationId/read',
      {'seq': sequence},
    );
    final channel = await _channelForConversation(conversationId);
    if (channel != null) await _wukong.markRead(channel);
  }

  @override
  Future<void> markDelivered(String conversationId, int sequence) =>
      _sendRequest(
        'PUT',
        '/v2/channels/conversations/$conversationId/delivered',
        {'seq': sequence},
      ).then((_) {});

  @override
  Future<void> setTyping(String conversationId, bool typing) => _sendRequest(
    'POST',
    '/v2/channels/conversations/$conversationId/typing',
    {'typing': typing},
  ).then((_) {});

  @override
  Future<void> updateConversationPreferences(
    String conversationId, {
    bool? pinned,
    bool? saved,
    bool? notificationsMuted,
    bool? manualUnread,
    bool? archived,
  }) {
    final payload = <String, Object?>{};
    if (pinned != null) payload['pinned'] = pinned;
    if (saved != null) payload['saved'] = saved;
    if (notificationsMuted != null) {
      payload['notificationsMuted'] = notificationsMuted;
    }
    if (manualUnread != null) payload['manualUnread'] = manualUnread;
    if (archived != null) payload['archived'] = archived;
    return _sendRequest(
      'PATCH',
      '/v2/channels/conversations/$conversationId/preferences',
      payload,
    ).then((_) {});
  }

  @override
  Future<List<ScheduledMessage>> scheduledMessages(
    String conversationId,
  ) async {
    final data = await _get('/v2/messages/scheduled?status=pending&limit=200');
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
    final data = await _sendRequest('POST', '/v2/messages/scheduled', {
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
  Future<ScheduledMessage> updateScheduledMessage(
    String scheduledMessageId, {
    required String text,
    required DateTime scheduledAt,
    int? expiresInSeconds,
  }) async {
    final data = await _sendRequest(
      'PATCH',
      '/v2/messages/scheduled/${Uri.encodeComponent(scheduledMessageId)}',
      {
        'body': {'text': text},
        'scheduledAt': scheduledAt.toUtc().toIso8601String(),
        'expiresInSeconds': ?expiresInSeconds,
      },
    );
    final raw = data['scheduledMessage'] is Map<String, Object?>
        ? data['scheduledMessage']! as Map<String, Object?>
        : data;
    return ScheduledMessage.fromJson(raw);
  }

  @override
  Future<void> cancelScheduledMessage(String scheduledMessageId) =>
      _sendRequest(
        'DELETE',
        '/v2/messages/scheduled/${Uri.encodeComponent(scheduledMessageId)}',
      ).then((_) {});

  @override
  Future<LinkPreview?> linkPreview(String url) async {
    final data = await _sendRequest('POST', '/v2/link-preview', {'url': url});
    final raw = data['preview'] is Map<String, Object?>
        ? data['preview']! as Map<String, Object?>
        : data;
    final preview = LinkPreview.fromJson(raw);
    return preview.url.isEmpty ? null : preview;
  }

  @override
  Future<void> hideConversation(String conversationId) => _sendRequest(
    'DELETE',
    '/v2/channels/conversations/$conversationId',
  ).then((_) {});

  @override
  Future<void> recallMessage(String messageId) =>
      _sendRequest('POST', '/v2/messages/$messageId/recall').then((_) {});

  @override
  Future<void> persistMessages(
    String conversationId,
    List<ChatMessage> messages,
  ) async {
    final uid = _userId;
    final channel = _conversationChannels[conversationId];
    if (uid != null && channel != null) {
      for (final message in messages.where(
        (m) => m.status == MessageStatus.recalled,
      )) {
        await _conversationCache.markRecalled(uid, channel, message.id);
      }
    }
    if (_userId != uid) return;
    final encoded = messages
        .where(canReadCachedMessage)
        .map((message) => message.toJson())
        .toList();
    await _store.writeJson(
      'messages.$conversationId',
      uid == null ? encoded : _purgeDeletedCache(uid, encoded),
    );
  }

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
    if (_mediaToken != null) 'mediaAccessToken': _mediaToken,
    'refreshToken': _refreshToken,
    'userId': _userId,
    if (_imSession != null) 'imSession': _imSession!.toJson(),
    if (_me != null) 'user': _storedUser(_me!),
  });

  Map<String, Object?> _storedUser(AppUser user) => {
    'canDeleteMessagesForEveryone': user.canDeleteMessagesForEveryone,
    'id': user.id,
    'name': user.name,
    'handle': user.handle,
    'presence': user.presence,
    'phone': user.phone,
    'signature': user.signature,
    'gender': user.gender,
    'avatarMediaId': user.avatarMediaId,
    'avatarUrl': user.avatarUrl,
    'online': user.isOnline,
    'remark': user.remark,
    'tags': user.tags,
    'handleChangeCount': user.handleChangeCount,
    'handleChangesRemaining': user.handleChangesRemaining,
    'allowSearchByHandle': user.allowSearchByHandle,
    'allowSearchByPhone': user.allowSearchByPhone,
  };

  Future<void> _clearSession() async {
    _sessionEpoch++;
    _deletionSyncs.clear();
    _deletionVersions.clear();
    _deletionVerified.clear();
    mediaAccess.clear(this);
    _mediaToken = null;
    _profileRevision++;
    _distrustGroupHistories();
    _historyRequiredVersions.clear();
    _latestHistoryAccess.clear();
    _token = null;
    _refreshToken = null;
    _userId = null;
    _me = null;
    _imSession = null;
    _conversationChannels.clear();
    _channelConversations.clear();
    _wukongSendsByClientMsgNo.clear();
    _wukongSendsByClientSeq.clear();
    _dispatchResultBuffers.clear();
    _seenWukongMessageEvents.clear();
    _pendingWukongMessageEvents.clear();
    _reconnectReconciliationTimer?.cancel();
    _reconnectReconciliationTimer = null;
    _wukongHasConnected = false;
    _reconnectReconciliationNeeded = false;
    _reconnectReconciliationAttempts = 0;
    await _store.remove('session');
  }

  Future<void> _disconnect({bool logout = false}) async {
    try {
      await _wukong
          .disconnect(logout: logout)
          .timeout(const Duration(seconds: 2));
    } catch (_) {
      // SDK transports can already be gone while the local session is valid.
    }
    _connection.add(false);
  }

  @override
  Future<void> close() async {
    _sessionEpoch++;
    mediaAccess.clear(this);
    _closed = true;
    _reconnectReconciliationTimer?.cancel();
    _reconnectReconciliationTimer = null;
    await _disconnect();
    await _wukongConnectionSubscription?.cancel();
    await _wukongEventSubscription?.cancel();
    await _wukongEventSerial;
    await _wukongSendSubscription?.cancel();
    await _wukong.dispose();
    if (!identical(_uploadClient, _client)) _uploadClient.close();
    _client.close();
    await _connection.close();
    await _events.close();
    await _callEvents.close();
  }

  @override
  Future<CallConfiguration> callConfiguration() async =>
      CallConfiguration.fromJson(await _get('/v2/calls/config'));

  @override
  Future<CallSession> inviteCall({
    required String callId,
    required String conversationId,
    String? calleeUserId,
    required CallMediaType mediaType,
  }) async {
    final data = await _sendRequest('POST', '/v2/calls/invite', {
      'callId': callId,
      'conversationId': conversationId,
      if (calleeUserId != null && calleeUserId.isNotEmpty)
        'calleeUserId': calleeUserId,
      'mediaType': mediaType.name,
    });
    return _callFromResponse(data);
  }

  @override
  Future<CallSession> getCall(String callId) async =>
      _callFromResponse(await _get('/v2/calls/$callId'));

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
    await _sendRequest('POST', '/v2/calls/$callId/$action', {
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
  Future<CallMediaSession> joinCall(String callId) async {
    final data = await _sendRequest(
      'POST',
      '/v2/calls/$callId/token',
      const {},
    );
    final raw = data['session'];
    if (raw is! Map<String, Object?>) {
      throw const FormatException('通话响应缺少 LiveKit 入会凭证');
    }
    return CallMediaSession.fromJson(raw);
  }
}

class _PendingWukongSend {
  _PendingWukongSend({
    required this.conversationId,
    required this.appClientMessageId,
    required this.source,
    required this.message,
  });

  final String conversationId;
  final String appClientMessageId;
  final ChatMessage source;
  WukongMessage message;
  bool exposedToCaller = false;
  WukongSendResult? earlyResult;
}

/// Uses the configured live service only. Network failures are surfaced to the
/// user and are never converted into fake successful responses.
class ResilientImRepository
    implements
        ImRepository,
        MessageDeletionRepository,
        CachedMessageRepository,
        GroupHistoryRepository,
        PaginatedMessageRepository,
        CallRepository,
        BusinessFeatureRepository {
  ResilientImRepository({this.live});

  factory ResilientImRepository.fromEnvironment() => ResilientImRepository(
    live: AppConfig.hasLiveBackend ? LiveImRepository() : null,
  );

  final ImRepository? live;
  @override
  bool isMessageDeleted(String id) =>
      live is MessageDeletionRepository &&
      (live as MessageDeletionRepository).isMessageDeleted(id);
  @override
  Future<List<String>> deleteMessagesForEveryone(
    String cid,
    List<String> ids,
  ) => (_active as MessageDeletionRepository).deleteMessagesForEveryone(
    cid,
    ids,
  );
  @override
  bool canReadCachedMessage(ChatMessage message) =>
      live is! GroupHistoryRepository ||
      (live as GroupHistoryRepository).canReadCachedMessage(message);

  ImRepository get _active {
    if (live case final live?) return live;
    throw const ImApiException(
      statusCode: 503,
      code: 'CLIENT_NOT_CONFIGURED',
      message: '客户端尚未配置服务地址',
    );
  }

  @override
  bool get isDemo => live?.isDemo ?? false;
  @override
  bool get supportsDemo => false;
  @override
  AppUser? get currentUser => live?.currentUser;
  @override
  Stream<bool> get connectionChanges =>
      live?.connectionChanges ?? const Stream<bool>.empty();
  @override
  Stream<ImEvent> get events => live?.events ?? const Stream<ImEvent>.empty();

  CallRepository get _activeCalls {
    final active = _active;
    if (active is CallRepository) return active as CallRepository;
    throw const ImApiException(
      statusCode: 501,
      code: 'CALLS_UNAVAILABLE',
      message: '当前模式不支持音视频通话',
    );
  }

  BusinessFeatureRepository get _activeBusiness {
    final active = _active;
    if (active is BusinessFeatureRepository) {
      return active as BusinessFeatureRepository;
    }
    throw const ImApiException(
      statusCode: 501,
      code: 'BUSINESS_FEATURES_UNAVAILABLE',
      message: '当前模式不支持业务频道与客服功能',
    );
  }

  @override
  Future<List<BusinessChannelSummary>> businessChannels({
    int channelType = 0,
    String category = '',
    String parentId = '',
    int limit = 100,
  }) => _activeBusiness.businessChannels(
    channelType: channelType,
    category: category,
    parentId: parentId,
    limit: limit,
  );

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
  }) => _activeBusiness.createBusinessChannel(
    channelType: channelType,
    name: name,
    parentId: parentId,
    description: description,
    visibility: visibility,
    joinPolicy: joinPolicy,
    postingPolicy: postingPolicy,
    slowModeSeconds: slowModeSeconds,
  );

  @override
  Future<BusinessChannelSummary> businessChannel(
    String channelId,
    int channelType,
  ) => _activeBusiness.businessChannel(channelId, channelType);

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
  }) => _activeBusiness.updateBusinessChannel(
    channelId,
    channelType,
    name: name,
    description: description,
    visibility: visibility,
    joinPolicy: joinPolicy,
    postingPolicy: postingPolicy,
    slowModeSeconds: slowModeSeconds,
    sendBan: sendBan,
    allowStranger: allowStranger,
  );

  @override
  Future<List<BusinessChannelMemberSummary>> businessChannelMembers(
    String channelId,
    int channelType,
  ) => _activeBusiness.businessChannelMembers(channelId, channelType);

  @override
  Future<void> addBusinessChannelMember(
    String channelId,
    int channelType,
    String userId, {
    DateTime? expiresAt,
  }) => _activeBusiness.addBusinessChannelMember(
    channelId,
    channelType,
    userId,
    expiresAt: expiresAt,
  );

  @override
  Future<void> removeBusinessChannelMember(
    String channelId,
    int channelType,
    String userId,
  ) => _activeBusiness.removeBusinessChannelMember(
    channelId,
    channelType,
    userId,
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
  }) => _activeBusiness.updateBusinessChannelMember(
    channelId,
    channelType,
    userId,
    role: role,
    mutedUntil: mutedUntil,
    clearMute: clearMute,
    expiresAt: expiresAt,
    clearExpiry: clearExpiry,
  );

  @override
  Future<void> setBusinessChannelAccess(
    String channelId,
    int channelType,
    String userId,
    String accessType,
    bool enabled, {
    String reason = '',
  }) => _activeBusiness.setBusinessChannelAccess(
    channelId,
    channelType,
    userId,
    accessType,
    enabled,
    reason: reason,
  );

  @override
  Future<List<BusinessChannelAccessSummary>> businessChannelAccess(
    String channelId,
    int channelType, {
    String accessType = '',
  }) => _activeBusiness.businessChannelAccess(
    channelId,
    channelType,
    accessType: accessType,
  );

  @override
  Future<void> subscribeBusinessChannel(
    String channelId,
    int channelType, {
    DateTime? expiresAt,
  }) => _activeBusiness.subscribeBusinessChannel(
    channelId,
    channelType,
    expiresAt: expiresAt,
  );

  @override
  Future<void> unsubscribeBusinessChannel(String channelId, int channelType) =>
      _activeBusiness.unsubscribeBusinessChannel(channelId, channelType);

  @override
  Future<List<SupportSkillGroupSummary>> supportSkillGroups() =>
      _activeBusiness.supportSkillGroups();

  @override
  Future<List<SupportAgentSummary>> supportAgents({String skillGroupId = ''}) =>
      _activeBusiness.supportAgents(skillGroupId: skillGroupId);

  @override
  Future<SupportAgentSummary> setSupportAgentStatus(String status) =>
      _activeBusiness.setSupportAgentStatus(status);

  @override
  Future<List<SupportSessionSummary>> supportSessions({
    String status = '',
    String skillGroupId = '',
  }) => _activeBusiness.supportSessions(
    status: status,
    skillGroupId: skillGroupId,
  );

  @override
  Future<SupportSessionSummary> createSupportSession({
    required String skillGroupId,
    String subject = '',
    int channelType = 10,
  }) => _activeBusiness.createSupportSession(
    skillGroupId: skillGroupId,
    subject: subject,
    channelType: channelType,
  );

  @override
  Future<SupportSessionSummary> claimSupportSession(String sessionId) =>
      _activeBusiness.claimSupportSession(sessionId);

  @override
  Future<SupportSessionSummary> transferSupportSession(
    String sessionId,
    String targetAgentId,
  ) => _activeBusiness.transferSupportSession(sessionId, targetAgentId);

  @override
  Future<SupportSessionSummary> endSupportSession(String sessionId) =>
      _activeBusiness.endSupportSession(sessionId);

  @override
  Future<SupportSessionSummary> rateSupportSession(
    String sessionId,
    int rating,
    String comment,
  ) => _activeBusiness.rateSupportSession(sessionId, rating, comment);

  @override
  Future<BusinessMedia> uploadBusinessMedia(
    MediaUpload upload, {
    void Function(double progress)? onProgress,
  }) => _activeBusiness.uploadBusinessMedia(upload, onProgress: onProgress);

  @override
  Future<MomentPage> moments({
    String authorId = '',
    String cursor = '',
    int limit = 20,
  }) =>
      _activeBusiness.moments(authorId: authorId, cursor: cursor, limit: limit);

  @override
  Future<MomentSummary> createMoment({
    required String content,
    required String mediaKind,
    required List<String> mediaIds,
    required String visibility,
    List<String> visibleUserIds = const [],
    Map<String, Object?> location = const {},
  }) => _activeBusiness.createMoment(
    content: content,
    mediaKind: mediaKind,
    mediaIds: mediaIds,
    visibility: visibility,
    visibleUserIds: visibleUserIds,
    location: location,
  );

  @override
  Future<MomentSummary> setMomentLike(String momentId, bool active) =>
      _activeBusiness.setMomentLike(momentId, active);

  @override
  Future<MomentCommentSummary> createMomentComment(
    String momentId,
    String content, {
    String parentId = '',
  }) => _activeBusiness.createMomentComment(
    momentId,
    content,
    parentId: parentId,
  );

  @override
  Future<void> deleteMoment(String momentId) =>
      _activeBusiness.deleteMoment(momentId);

  @override
  Future<void> deleteMomentComment(String momentId, String commentId) =>
      _activeBusiness.deleteMomentComment(momentId, commentId);

  @override
  Future<List<MomentReminderSummary>> momentReminders({int limit = 100}) =>
      _activeBusiness.momentReminders(limit: limit);

  @override
  Future<void> markMomentRemindersRead(List<int> reminderIds) =>
      _activeBusiness.markMomentRemindersRead(reminderIds);

  @override
  Future<List<StickerCategorySummary>> stickerCategories() =>
      _activeBusiness.stickerCategories();

  @override
  Future<List<StickerPackSummary>> stickerPacks({String categoryId = ''}) =>
      _activeBusiness.stickerPacks(categoryId: categoryId);

  @override
  Future<StickerPackSummary> stickerPack(String packId) =>
      _activeBusiness.stickerPack(packId);

  @override
  Future<void> setStickerPackFavorite(String packId, bool active) =>
      _activeBusiness.setStickerPackFavorite(packId, active);

  @override
  Future<void> setStickerFavorite(String stickerId, bool active) =>
      _activeBusiness.setStickerFavorite(stickerId, active);

  @override
  Future<void> recordStickerUse(String stickerId) =>
      _activeBusiness.recordStickerUse(stickerId);

  @override
  Future<List<StickerItemSummary>> recentStickers({int limit = 50}) =>
      _activeBusiness.recentStickers(limit: limit);

  @override
  Future<List<StickerItemSummary>> favoriteStickers({int limit = 50}) =>
      _activeBusiness.favoriteStickers(limit: limit);

  @override
  Stream<CallSignalEvent> get callEvents {
    final active = live;
    return active is CallRepository
        ? (active as CallRepository).callEvents
        : const Stream<CallSignalEvent>.empty();
  }

  @override
  Future<CallConfiguration> callConfiguration() =>
      _activeCalls.callConfiguration();

  @override
  Future<CallSession> inviteCall({
    required String callId,
    required String conversationId,
    String? calleeUserId,
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
  Future<CallMediaSession> joinCall(String callId) =>
      _activeCalls.joinCall(callId);

  @override
  Future<void> enterDemo() async {
    throw const ImApiException(
      statusCode: 403,
      code: 'DEMO_DISABLED',
      message: '当前应用只连接真实服务',
    );
  }

  @override
  Future<AuthPolicy> authPolicy() => _active.authPolicy();
  @override
  Future<bool> restoreSession() => _active.restoreSession();
  @override
  Future<String?> requestCode(String phone) => _active.requestCode(phone);
  @override
  Future<AppUser> login(String phone, String code, {String inviteCode = ''}) =>
      _active.login(phone, code, inviteCode: inviteCode);
  @override
  Future<AppUser> passwordLogin(String phone, String password) =>
      _active.passwordLogin(phone, password);
  @override
  Future<QrLoginTicket> createQrLoginTicket({required String clientName}) =>
      _active.createQrLoginTicket(clientName: clientName);
  @override
  Future<AppUser?> pollQrLoginTicket(QrLoginTicket ticket) =>
      _active.pollQrLoginTicket(ticket);
  @override
  Future<QrLoginRequest> inspectQrLogin(String token) =>
      _active.inspectQrLogin(token);
  @override
  Future<void> confirmQrLogin(String token) => _active.confirmQrLogin(token);
  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
    String inviteCode = '',
  }) => _active.register(
    phone: phone,
    code: code,
    password: password,
    name: name,
    inviteCode: inviteCode,
  );
  @override
  Future<bool> validateInviteCode(String code) =>
      _active.validateInviteCode(code);
  @override
  Future<InviteCodeProfile> inviteCode() => _active.inviteCode();
  @override
  Future<InviteCodeProfile> changeInviteCode(String code) =>
      _active.changeInviteCode(code);
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
  Future<PeerLoginInfo> peerLoginInfo(String conversationId) =>
      _active.peerLoginInfo(conversationId);

  @override
  Future<ChatMessage> refreshMessageMedia(ChatMessage message) =>
      _active.refreshMessageMedia(message);
  @override
  Future<AppUser> updateProfile({
    String? name,
    String? handle,
    String? signature,
    String? gender,
    String? avatarMediaId,
    bool? allowSearchByHandle,
    bool? allowSearchByPhone,
  }) => _active.updateProfile(
    name: name,
    handle: handle,
    signature: signature,
    gender: gender,
    avatarMediaId: avatarMediaId,
    allowSearchByHandle: allowSearchByHandle,
    allowSearchByPhone: allowSearchByPhone,
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
  Future<List<ImDeviceSession>> imDeviceSessions() =>
      _active.imDeviceSessions();
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
  Future<void> registerClientDevice({
    required String installationId,
    required String platform,
    required String deviceName,
    required String deviceModel,
    required String osVersion,
    required String appVersion,
  }) => _active.registerClientDevice(
    installationId: installationId,
    platform: platform,
    deviceName: deviceName,
    deviceModel: deviceModel,
    osVersion: osVersion,
    appVersion: appVersion,
  );
  @override
  Future<void> removeUserDevice(String deviceId) =>
      _active.removeUserDevice(deviceId);
  @override
  Future<void> quitImDeviceSession(int deviceFlag) =>
      _active.quitImDeviceSession(deviceFlag);
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
  Future<void> reportClientDiagnostic({
    required String kind,
    required String name,
    required String fingerprint,
    required String platform,
    required String appVersion,
    int? durationMs,
  }) => _active.reportClientDiagnostic(
    kind: kind,
    name: name,
    fingerprint: fingerprint,
    platform: platform,
    appVersion: appVersion,
    durationMs: durationMs,
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
  Future<List<UserPresenceSnapshot>> userPresence(
    List<String> userIds, {
    String? groupId,
  }) => _active.userPresence(userIds, groupId: groupId);
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
    bool? historyVisibleToNewMembers,
    bool rotateQr = false,
  }) => _active.updateGroupProfile(
    conversationId,
    name: name,
    avatarMediaId: avatarMediaId,
    joinPolicy: joinPolicy,
    allowMemberAddFriend: allowMemberAddFriend,
    historyVisibleToNewMembers: historyVisibleToNewMembers,
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
  Future<List<RobotProfile>> robotProfiles(String conversationId) =>
      _active.robotProfiles(conversationId);
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
  Future<void> setGroupMemberMuted(
    String conversationId,
    String userId,
    DateTime? until,
  ) => _active.setGroupMemberMuted(conversationId, userId, until);
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
  Future<List<ChatMessage>> cachedMessages(String conversationId) {
    final active = _active;
    if (active case final CachedMessageRepository cached) {
      return cached.cachedMessages(conversationId);
    }
    return Future.value(const <ChatMessage>[]);
  }

  @override
  Future<List<ChatMessage>> olderMessages(
    String conversationId, {
    required int beforeSequence,
    int limit = 50,
  }) {
    final active = _active;
    if (active is! PaginatedMessageRepository) {
      throw const ImApiException(
        statusCode: 501,
        code: 'MESSAGE_HISTORY_UNAVAILABLE',
        message: '当前服务暂不支持加载更早的消息',
      );
    }
    final historyRepository = active as PaginatedMessageRepository;
    return historyRepository.olderMessages(
      conversationId,
      beforeSequence: beforeSequence,
      limit: limit,
    );
  }

  @override
  Future<ChatMessage> send(ChatMessage pending) => _active.send(pending);
  @override
  Future<ChatMessage> editMessage(String messageId, String text) =>
      _active.editMessage(messageId, text);
  @override
  Future<List<MessageEditRevision>> messageEditHistory(String messageId) =>
      _active.messageEditHistory(messageId);
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
  Future<void> setTyping(String conversationId, bool typing) =>
      _active.setTyping(conversationId, typing);
  @override
  Future<void> updateConversationPreferences(
    String conversationId, {
    bool? pinned,
    bool? saved,
    bool? notificationsMuted,
    bool? manualUnread,
    bool? archived,
  }) => _active.updateConversationPreferences(
    conversationId,
    pinned: pinned,
    saved: saved,
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
  Future<ScheduledMessage> updateScheduledMessage(
    String scheduledMessageId, {
    required String text,
    required DateTime scheduledAt,
    int? expiresInSeconds,
  }) => _active.updateScheduledMessage(
    scheduledMessageId,
    text: text,
    scheduledAt: scheduledAt,
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
  }
}
