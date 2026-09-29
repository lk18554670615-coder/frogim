import 'dart:convert';

import 'package:http/http.dart' as http;

import '../core/tenant_context.dart';
import '../core/tenant_password.dart';
import '../core/tenant_push.dart';

class TenantAuthException implements Exception {
  const TenantAuthException(this.code, this.message, {this.statusCode = 503});
  final String code;
  final String message;
  final int statusCode;
  @override
  String toString() => message;
}

class TenantBusinessSession {
  const TenantBusinessSession({
    required this.context,
    required this.platformRefreshToken,
    required this.localUserId,
    required this.businessSession,
  });
  final TenantContext context;
  final String platformRefreshToken;
  final String localUserId;
  final Map<String, Object?> businessSession;
  String get cacheNamespace => context.cacheNamespace(localUserId);
}

class TenantRegistrationPending implements Exception {
  const TenantRegistrationPending(this.jobId, this.pollToken);
  final String jobId;
  final String pollToken;
  @override
  String toString() => '企业账号正在开通，请等待完成后登录';
}

class TenantRegistrationStatus {
  const TenantRegistrationStatus(this.status, this.errorCode);
  final String status;
  final String errorCode;
  bool get completed => status == 'completed';
  bool get needsAttention => status == 'blocked';
}

/// Transport for the new authentication boundary. Business requests do not go
/// through this object or the platform; a repository is constructed with the
/// returned immutable tenant address and namespace after a successful exchange.
class TenantAuthClient {
  TenantAuthClient({
    required String platformBaseUrl,
    required this.clientPlatform,
    http.Client? client,
  }) : platformBaseUrl = TenantContext.trustedBaseUrl(platformBaseUrl),
       _client = client ?? http.Client() {
    if (!const {'android', 'ios', 'web', 'macos'}.contains(clientPlatform)) {
      throw ArgumentError.value(clientPlatform, 'clientPlatform');
    }
  }

  final Uri platformBaseUrl;
  final String clientPlatform;
  final http.Client _client;
  final epoch = TenantEpoch();

  Future<Map<String, Object?>> policy() =>
      _request(platformBaseUrl, '/v2/config/auth', method: 'GET');

  Future<TenantPushCapabilities> pushCapabilities() async =>
      TenantPushCapabilities.fromJson(
        await _request(platformBaseUrl, '/v2/config/push', method: 'GET'),
      );

  Future<Map<String, Object?>> bindPushDevice(
    String refreshToken,
    Map<String, Object?> device,
  ) async {
    final result = await _request(
      platformBaseUrl,
      '/v2/push/devices',
      body: {'refreshToken': refreshToken, 'device': device},
    );
    if (result['id'] is! String ||
        (result['id'] as String).isEmpty ||
        result['revision'] is! int ||
        (result['revision'] as int) < 1) {
      throw const FormatException('平台设备登记结果未确认');
    }
    return result;
  }

  Future<void> unbindPushDevice(
    String refreshToken,
    String deviceId,
    String provider,
  ) async {
    final result = await _request(
      platformBaseUrl,
      '/v2/push/devices/unbind',
      body: {
        'refreshToken': refreshToken,
        'deviceId': deviceId,
        'provider': provider,
      },
    );
    if (result['ok'] != true) throw const FormatException('平台设备移除结果未确认');
  }

  Future<void> requestCode(String phone) async {
    await _request(platformBaseUrl, '/v2/auth/code', body: {'phone': phone});
  }

  Future<bool> validateEnterpriseCode(String code) async {
    final result = await _request(
      platformBaseUrl,
      '/v2/auth/enterprise-codes/validate',
      body: {'code': code.trim()},
    );
    return result['valid'] == true;
  }

  Future<TenantRegistrationStatus> registrationStatus(
    TenantRegistrationPending pending,
  ) async {
    _checkPending(pending);
    final result = await _request(
      platformBaseUrl,
      '/v2/auth/registration-jobs/${pending.jobId}',
      method: 'GET',
      bearer: pending.pollToken,
    );
    final status = result['status'];
    if (!const {
      'prepare_target',
      'activate',
      'completed',
      'blocked',
    }.contains(status)) {
      throw const FormatException('企业开户任务状态不正确');
    }
    return TenantRegistrationStatus(
      status as String,
      result['errorCode'] as String? ?? '',
    );
  }

  Future<TenantBusinessSession> completeRegistration(
    TenantRegistrationPending pending,
  ) {
    _checkPending(pending);
    return _authenticate(
      '/v2/auth/registration-jobs/${pending.jobId}/complete',
      {},
      bearer: pending.pollToken,
    );
  }

  void _checkPending(TenantRegistrationPending pending) {
    if (!RegExp(r'^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$').hasMatch(pending.jobId) ||
        !RegExp(r'^[a-zA-Z0-9_-]{43}$').hasMatch(pending.pollToken)) {
      throw const FormatException('企业开户任务凭据不正确');
    }
  }

  Future<void> logout(String platformRefreshToken) async {
    epoch.invalidate();
    await discardSession(platformRefreshToken);
  }

  /// Dispose an issued-but-never-installed session without invalidating a newer
  /// login. This capability is sent only to the fixed platform, never redirected.
  Future<void> discardSession(String platformRefreshToken) async {
    if (!RegExp(r'^[a-zA-Z0-9_-]{43}$').hasMatch(platformRefreshToken)) return;
    await _request(
      platformBaseUrl,
      '/v2/auth/logout',
      body: {'refreshToken': platformRefreshToken},
      checkEpoch: false,
    );
  }

  Future<TenantPasswordResult> changePassword(
    TenantPasswordTask task,
    String current,
    String next,
  ) async {
    final response = await _request(
      platformBaseUrl,
      '/v2/auth/password-change',
      bearer: task.token,
      body: {
        'requestId': task.requestId,
        'currentPassword': current,
        'newPassword': next,
      },
    );
    return TenantPasswordResult.fromJson(response, task.requestId);
  }

  Future<TenantPasswordResult> passwordChangeStatus(
    TenantPasswordTask task,
  ) async {
    final response = await _request(
      platformBaseUrl,
      task.recovery
          ? '/v2/auth/password-reset/status'
          : '/v2/auth/password-change/status',
      bearer: task.token,
      body: {'requestId': task.requestId},
    );
    return TenantPasswordResult.fromJson(response, task.requestId);
  }

  Future<void> requestRecoveryCode(
    String phone,
    TenantPasswordTask task,
  ) async {
    if (!task.recovery) throw const FormatException('验证码用途不正确');
    final response = await _request(
      platformBaseUrl,
      '/v2/auth/password-reset/code',
      body: {
        'phone': phone,
        'requestId': task.requestId,
        'queryToken': task.token,
      },
    );
    if (response['ok'] != true || response['requestId'] != task.requestId) {
      throw const FormatException('验证码发送结果未确认，请重新申请');
    }
  }

  Future<TenantPasswordResult> recoverPassword(
    TenantPasswordTask task,
    String code,
    String next,
  ) async {
    if (!task.recovery) throw const FormatException('验证码用途不正确');
    final response = await _request(
      platformBaseUrl,
      '/v2/auth/password-reset',
      bearer: task.token,
      body: {
        'requestId': task.requestId,
        'code': code,
        'newPassword': next,
        'confirmed': true,
      },
    );
    return TenantPasswordResult.fromJson(response, task.requestId);
  }

  Future<TenantBusinessSession> passwordLogin(String phone, String password) =>
      _authenticate('/v2/auth/password-login', {
        'phone': phone,
        'password': password,
      });

  Future<TenantBusinessSession> otpLogin({
    required String phone,
    required String code,
    String enterpriseCode = '',
    String inviteCode = '',
  }) => _authenticate('/v2/auth/login', {
    'phone': phone,
    'code': code,
    'enterpriseCode': enterpriseCode,
    'inviteCode': inviteCode,
  });

  Future<TenantBusinessSession> register({
    required String phone,
    required String code,
    required String password,
    required String name,
    String enterpriseCode = '',
    String inviteCode = '',
  }) => _authenticate('/v2/auth/register', {
    'phone': phone,
    'code': code,
    'password': password,
    'name': name,
    'enterpriseCode': enterpriseCode,
    'inviteCode': inviteCode,
  });

  Future<TenantBusinessSession> refresh(
    String platformRefreshToken,
    TenantContext previous,
  ) => _authenticate('/v2/auth/refresh', {
    'refreshToken': platformRefreshToken,
  }, previous: previous);

  Future<TenantBusinessSession> _authenticate(
    String route,
    Map<String, Object?> body, {
    TenantContext? previous,
    String? bearer,
  }) async {
    final captured = epoch.capture();
    final data = await _request(
      platformBaseUrl,
      route,
      body: body,
      bearer: bearer,
      // A cancelled response can still contain a newly issued credential. Read
      // it only for revocation; never install it into another session.
      checkEpoch: false,
    );
    if (data['status'] == 'pending' &&
        data['jobId'] is String &&
        data['pollToken'] is String) {
      epoch.check(captured);
      throw TenantRegistrationPending(
        data['jobId'] as String,
        data['pollToken'] as String,
      );
    }
    final raw = data['tenantContext'];
    final ticket = data['sessionTicket'];
    final refreshToken = data['refreshToken'];
    if (refreshToken is! String ||
        !RegExp(r'^[a-zA-Z0-9_-]{43}$').hasMatch(refreshToken)) {
      throw const FormatException('统一认证返回的数据不完整');
    }
    try {
      epoch.check(captured);
      if (raw is! Map<String, Object?> ||
          ticket is! String ||
          !RegExp(r'^[a-zA-Z0-9_-]{43}$').hasMatch(ticket)) {
        throw const FormatException('统一认证返回的数据不完整');
      }
      final context = TenantContext.fromJson(raw);
      if (previous != null && !previous.sameAssignment(context)) {
        epoch.invalidate();
        throw const TenantAuthException(
          'TENANT_ASSIGNMENT_CHANGED',
          '企业归属已变更，请重新登录',
          statusCode: 401,
        );
      }
      // Only the one-use ticket crosses this boundary, never a password, OTP,
      // platform refresh token or credentials from a previous enterprise.
      final session = await _request(
        context.httpBaseUrl,
        '/v2/auth/tenant-session',
        body: {'sessionTicket': ticket},
      );
      epoch.check(captured);
      final user = session['user'];
      if (user is! Map<String, Object?> ||
          user['id'] is! String ||
          session['accessToken'] is! String ||
          session['imSession'] is! Map) {
        throw const FormatException('企业业务会话不完整');
      }
      final localUserId = user['id'] as String;
      context.cacheNamespace(localUserId);
      if ((session['imSession'] as Map)['uid'] != localUserId) {
        throw const FormatException('企业 IM 身份不匹配');
      }
      return TenantBusinessSession(
        context: context,
        platformRefreshToken: refreshToken,
        localUserId: localUserId,
        businessSession: Map.unmodifiable(session),
      );
    } catch (_) {
      try {
        await discardSession(refreshToken);
      } catch (_) {
        // Offline cleanup is best effort. Never turn a cancelled login into a
        // valid local session or log the token/remote response.
      }
      rethrow;
    }
  }

  Future<Map<String, Object?>> _request(
    Uri base,
    String route, {
    Map<String, Object?>? body,
    String method = 'POST',
    String? bearer,
    bool checkEpoch = true,
  }) async {
    final captured = epoch.capture();
    // A poll capability is only ever attached to the fixed platform endpoint.
    if (bearer != null && base != platformBaseUrl) {
      throw const FormatException('开户凭据不能发送给企业服务');
    }
    final request = http.Request(method, base.resolve(route))
      ..followRedirects = false
      ..headers.addAll({
        'Content-Type': 'application/json',
        'X-Client-Platform': clientPlatform,
        if (bearer != null) 'Authorization': 'Bearer $bearer',
      })
      ..body = body == null ? '' : jsonEncode(body);
    final response = await http.Response.fromStream(
      await _client.send(request).timeout(const Duration(seconds: 15)),
    ).timeout(const Duration(seconds: 15));
    if (checkEpoch) epoch.check(captured);
    if (response.statusCode >= 300 && response.statusCode < 400) {
      throw const TenantAuthException(
        'AUTH_REDIRECT_REJECTED',
        '认证地址发生跳转，请联系管理员',
      );
    }
    final data = jsonDecode(utf8.decode(response.bodyBytes));
    if (data is! Map<String, Object?>) throw const FormatException('认证响应格式不正确');
    if (response.statusCode < 200 || response.statusCode >= 300) {
      final error = data['error'];
      final code = error is Map ? error['code'] : null;
      throw TenantAuthException(
        code is String ? code : 'PLATFORM_UNAVAILABLE',
        switch (code) {
          'PASSWORD_RECOVERY_REJECTED' => '验证码不可用或账号暂不可重置，请重新获取验证码或联系管理员',
          'TENANT_PASSWORD_POLICY_REJECTED' => '新密码不符合所属企业的密码长度要求',
          'REQUEST_CHANGED' => '请求内容已变化，请先查询原密码任务',
          'INVALID_ARGUMENT' => '请求参数不正确，请检查后重试',
          'RATE_LIMITED' => '操作过于频繁，请稍后重试',
          _ =>
            response.statusCode == 401
                ? '当前密码或登录凭据不可用，请检查或重新登录'
                : '认证服务暂不可用，请稍后重试',
        },
        statusCode: response.statusCode,
      );
    }
    // Enterprise APIs use the existing {data: ...} response envelope, while
    // the new platform authentication service returns its object directly.
    final payload = data['data'];
    return payload is Map<String, Object?> ? payload : data;
  }

  void close() {
    epoch.invalidate();
    _client.close();
  }
}
