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
import '../core/models.dart';
import '../im/business_repository.dart';
import '../im/business_features.dart';
import '../im/local_conversation_cache.dart';
import '../im/message_content_registry.dart';
import '../im/message_mapper.dart';
import '../im/structured_event_text.dart';
import '../im/wukong_gateway.dart';
import 'im_repository.dart';
import 'secure_local_store.dart';

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
        CachedMessageRepository,
        PaginatedMessageRepository,
        CallRepository,
        BusinessFeatureRepository {
  LiveImRepository({
    http.Client? client,
    SecureLocalStore? store,
    String? apiBaseUrl,
    String? clientPlatform,
    BusinessRepository? businessRepository,
    WukongGateway? wukongGateway,
  }) : _client = client ?? http.Client(),
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
          client: _client,
        );
    _wukong = wukongGateway ?? createWukongGateway(dataSource: _business);
    _conversationCache = LocalConversationCache(_store);
  }

  final http.Client _client;
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
  String? _refreshToken;
  String? _userId;
  AppUser? _me;
  WukongSession? _imSession;
  final Map<String, WukongChannel> _conversationChannels = {};
  final Map<String, String> _channelConversations = {};
  final Map<String, _PendingWukongSend> _wukongSendsByClientMsgNo = {};
  final Map<int, _PendingWukongSend> _wukongSendsByClientSeq = {};
  final List<WukongSendResult> _earlyWukongSendResults = [];
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
    final storedImSession = stored['imSession'];
    if (storedImSession is Map<String, Object?>) {
      try {
        _imSession = WukongSession.fromJson(storedImSession);
      } on FormatException {
        _imSession = null;
      }
    }
    if (_token == null || _userId == null) return false;
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
  Future<AppUser> login(String phone, String code) async {
    final data = await _sendUnprotectedRequest('POST', '/v2/auth/login', {
      'phone': phone,
      'code': code,
      'name': '青蛙用户',
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
  }) async {
    final data = await _sendUnprotectedRequest('POST', '/v2/auth/register', {
      'phone': phone,
      'code': code,
      'password': password,
      'name': name,
    });
    return _acceptSession(data);
  }

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
    _token = data['accessToken'] as String?;
    _refreshToken = data['refreshToken'] as String?;
    final rawUser = data['user'] as Map<String, Object?>?;
    _userId = rawUser?['id'] as String?;
    if (_token == null || _userId == null || rawUser == null) {
      throw const FormatException('登录响应缺少必要凭据');
    }
    _imSession = _parseImSession(data['imSession']);
    if (_imSession case final session? when session.uid != _userId) {
      throw const FormatException('WuKongIM session user does not match login');
    }
    _closed = false;
    _handlingSessionReplacement = false;
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
    final user = _user(await _get('/v2/users/me'));
    _me = user;
    await _persistSession();
    return user;
  }

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
      _token = data['accessToken'] as String?;
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
          if (_wukongHasConnected && _reconnectReconciliationNeeded) {
            _scheduleReconnectReconciliation();
          }
          _wukongHasConnected = true;
        case WukongConnectionState.disconnected ||
            WukongConnectionState.networkUnavailable:
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
      _wukongEventSerial = _wukongEventSerial
          .then((_) => _handleWukongEvent(event))
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
      if (byNumber != null) return byNumber;
    }
    if (result.clientSeq > 0) {
      return _wukongSendsByClientSeq[result.clientSeq];
    }
    return null;
  }

  Future<void> _handleWukongSendResult(WukongSendResult result) async {
    if (_closed) return;
    final pending = _findWukongSend(result);
    if (pending == null) {
      _earlyWukongSendResults.add(result);
      if (_earlyWukongSendResults.length > 100) {
        _earlyWukongSendResults.removeRange(
          0,
          _earlyWukongSendResults.length - 100,
        );
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
    _removeWukongSend(pending);
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

  Future<ChatMessage> _trackWukongSend({
    required ChatMessage source,
    required WukongMessage sent,
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
    final earlyIndex = _earlyWukongSendResults.indexWhere(
      (result) =>
          (result.clientMsgNo.isNotEmpty &&
              result.clientMsgNo == sent.clientMsgNo) ||
          (result.clientSeq > 0 && result.clientSeq == sent.clientSeq),
    );
    final early = earlyIndex < 0
        ? tracked.earlyResult
        : _earlyWukongSendResults.removeAt(earlyIndex);
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
      _wukongSendsByClientMsgNo.remove(pending.message.clientMsgNo);
    }
    if (pending.message.clientSeq > 0) {
      _wukongSendsByClientSeq.remove(pending.message.clientSeq);
    }
  }

  Future<void> _handleWukongEvent(WukongGatewayEvent event) async {
    if (_closed) return;
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
    await _conversationCache.upsertMessage(uid, message);
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
        payload: {'message': mapped.toJson(), 'mentioned': mentioned},
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
    final existingURL = message.payload['url'] as String?;
    if (mediaId == null ||
        mediaId.isEmpty ||
        (existingURL != null && existingURL.isNotEmpty)) {
      return message;
    }
    try {
      final url = await _business.mediaUrl(mediaId);
      return message.copyWith(payload: {...message.payload, 'url': url});
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
      _events.add(ImEvent(type: type, payload: payload));
    }
  }

  @override
  Future<List<Conversation>> conversations() async {
    final data = await _get('/v2/channels/conversations');
    final metadata = (data['items'] as List<Object?>? ?? const [])
        .map((item) => _conversation(item! as Map<String, Object?>))
        .toList();
    for (final conversation in metadata) {
      _registerConversation(conversation);
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
      return metadata.map((conversation) {
        final channel = _conversationChannels[conversation.id];
        final item = channel == null ? null : byChannel[channel.key];
        return item == null
            ? conversation
            : _mergeWukongConversation(conversation, channel!, item);
      }).toList();
    } catch (_) {
      // Business metadata remains useful during a transient IM sync outage;
      // message history itself never falls back to the legacy message store.
      return metadata;
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

  Conversation _mergeWukongConversation(
    Conversation conversation,
    WukongChannel channel,
    Map<String, Object?> item,
  ) {
    final recents = item['recents'] as List<Object?>? ?? const [];
    String? subtitle;
    if (recents.isNotEmpty && recents.first is Map) {
      final recent = wukongObjectMap(recents.first);
      final normalized = <String, Object?>{
        ...recent,
        'channel_id': recent['channel_id'] ?? channel.id,
        'channel_type': recent['channel_type'] ?? channel.type,
      };
      final message = WukongMessage.fromSyncJson(normalized);
      subtitle = _messageMapper
          .toChatMessage(
            message,
            currentUserId: _userId ?? '',
            conversationId: conversation.id,
          )
          .text;
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
      unread: (item['unread'] as num?)?.toInt() ?? conversation.unread,
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
  Future<GroupProfile> groupProfile(String conversationId) async =>
      _groupProfile(await _get('/v2/channels/groups/$conversationId'));

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

  DateTime? _tryDate(Object? value) => tryParseLocalDateTime(value);

  @override
  Future<GroupProfile> updateGroupProfile(
    String conversationId, {
    String? name,
    String? avatarMediaId,
    String? joinPolicy,
    bool? allowMemberAddFriend,
    bool rotateQr = false,
  }) async => _groupProfile(
    await _sendRequest('PATCH', '/v2/channels/groups/$conversationId', {
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
      await persistMessages(conversationId, result);
      return result;
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
    return merged;
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
      durationSeconds: (body['duration'] as num?)?.toInt(),
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
    final outgoing = _messageMapper.toOutgoing(pending, channel: channel);
    final sent = await _wukong.send(outgoing);
    return _trackWukongSend(source: pending, sent: sent);
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
    onProgress?.call(0);
    final prepared = await _sendRequest('POST', '/v2/media/presign', {
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
    await _sendRequest('POST', '/v2/media/$mediaId/complete', {
      'checksum': checksum,
    });
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
    final remoteURL = await _business.bindMedia(mediaId, channel);
    final wireMessage = ChatMessage(
      id: pending.id,
      clientMessageId: pending.clientMessageId,
      conversationId: pending.conversationId,
      senderId: pending.senderId,
      senderName: pending.senderName,
      text: pending.text,
      sentAt: pending.sentAt,
      isMine: pending.isMine,
      status: pending.status,
      kind: upload.kind,
      mediaUrl: remoteURL,
      mediaId: mediaId,
      mediaWidth: upload.width,
      mediaHeight: upload.height,
      fileName: upload.fileName,
      mimeType: upload.mimeType,
      durationSeconds: upload.durationSeconds,
      replyToId: pending.replyToId,
      replyToText: pending.replyToText,
      replyToSeq: pending.replyToSeq,
      replyToSenderId: pending.replyToSenderId,
      replyToSenderName: pending.replyToSenderName,
    );
    final base = _messageMapper.toOutgoing(wireMessage, channel: channel);
    final outgoing = WukongOutgoingMessage(
      channel: base.channel,
      payload: {
        ...base.payload,
        'size': upload.bytes.length,
        'checksum': checksum,
      },
      clientMsgNo: base.clientMsgNo,
      expireSeconds: base.expireSeconds,
    );
    final sent = await _wukong.send(outgoing);
    return _trackWukongSend(
      source: wireMessage,
      sent: sent,
      decorate: (message) => message.copyWith(
        kind: upload.kind,
        mediaUrl: upload.localPath,
        mediaId: mediaId,
        fileName: upload.fileName,
        mimeType: upload.mimeType,
        durationSeconds: upload.durationSeconds,
        replyToId: pending.replyToId,
        replyToText: pending.replyToText,
        replyToSeq: pending.replyToSeq,
        replyToSenderId: pending.replyToSenderId,
        replyToSenderName: pending.replyToSenderName,
      ),
    );
  }

  @override
  Future<void> saveFavorite(ChatMessage message) async {
    if (!message.id.startsWith('local-')) {
      await _sendRequest(
        'PUT',
        '/v2/messages/favorites/${Uri.encodeComponent(message.id)}',
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
        '/v2/messages/favorites/${Uri.encodeComponent(message.id)}',
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
    if (_imSession != null) 'imSession': _imSession!.toJson(),
    if (_me != null) 'user': _storedUser(_me!),
  });

  Map<String, Object?> _storedUser(AppUser user) => {
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
    _token = null;
    _refreshToken = null;
    _userId = null;
    _me = null;
    _imSession = null;
    _conversationChannels.clear();
    _channelConversations.clear();
    _wukongSendsByClientMsgNo.clear();
    _wukongSendsByClientSeq.clear();
    _earlyWukongSendResults.clear();
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
    _closed = true;
    _reconnectReconciliationTimer?.cancel();
    _reconnectReconciliationTimer = null;
    await _disconnect();
    await _wukongConnectionSubscription?.cancel();
    await _wukongEventSubscription?.cancel();
    await _wukongEventSerial;
    await _wukongSendSubscription?.cancel();
    await _wukong.dispose();
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
        CachedMessageRepository,
        PaginatedMessageRepository,
        CallRepository,
        BusinessFeatureRepository {
  ResilientImRepository({this.live});

  factory ResilientImRepository.fromEnvironment() => ResilientImRepository(
    live: AppConfig.hasLiveBackend ? LiveImRepository() : null,
  );

  final ImRepository? live;

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
  Future<AppUser> login(String phone, String code) =>
      _active.login(phone, code);
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
