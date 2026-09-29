import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';

import '../calls/call_models.dart';
import '../core/app_config.dart';
import '../core/auth_validation.dart';
import '../core/models.dart';
import '../core/runtime_endpoints.dart';
import '../core/tenant_context.dart';
import '../core/tenant_password.dart';
import '../core/tenant_push.dart';
import 'live_repository.dart';
import 'secure_local_store.dart';
import 'session_coordination.dart';
import 'native_call_state.dart';
import '../core/tenant_call_scope.dart';
import 'tenant_auth_client.dart';
import 'tenant_auth_repository.dart';

typedef TenantRepositoryFactory =
    LiveImRepository Function(
      TenantContext context,
      String namespace,
      Future<Map<String, Object?>> Function() refresh,
    );

/// Stable facade for controllers. Every business delegate owns an immutable
/// tenant address and storage scope. The platform never proxies business calls.
class PlatformImRepository extends ResilientImRepository
    implements
        TenantAuthenticationRepository,
        TenantPasswordRepository,
        TenantPushRepository {
  PlatformImRepository({
    required TenantAuthClient auth,
    SecureLocalStore? platformStore,
    SessionCoordination? coordination,
    NativeCallState? nativeCallState,
    TenantRepositoryFactory? businessFactory,
  }) : _auth = auth,
       _nativeCalls = nativeCallState ?? createNativeCallState(),
       _coordination =
           coordination ?? createSessionCoordination('${auth.platformBaseUrl}'),
       _store =
           platformStore ??
           SecureLocalStore(
             namespace: 'platform.${auth.platformBaseUrl}',
             requirePersistentKey: true,
             tabLocalKeys: const {'pending', 'password-task', 'tab-session'},
           ),
       _factory =
           businessFactory ??
           ((context, namespace, refresh) => LiveImRepository(
             apiBaseUrl: context.httpBaseUrl.toString(),
             clientPlatform: auth.clientPlatform,
             store: SecureLocalStore(
               namespace: namespace,
               tabLocalSession: kIsWeb,
             ),
             cacheNamespace: namespace,
             externalSessionRefresh: refresh,
           )) {
    _sharedChanges = _coordination.changes.listen(
      (_) => unawaited(_checkSharedSession()),
    );
  }

  factory PlatformImRepository.fromEnvironment() => PlatformImRepository(
    auth: TenantAuthClient(
      platformBaseUrl: AppConfig.platformAuthUrl,
      clientPlatform: kIsWeb
          ? 'web'
          : switch (defaultTargetPlatform) {
              TargetPlatform.android => 'android',
              TargetPlatform.iOS => 'ios',
              _ => 'macos',
            },
    ),
  );

  final TenantAuthClient _auth;
  final NativeCallState _nativeCalls;
  Future<void> _nativeRevocation = Future.value();
  final SecureLocalStore _store;
  final SessionCoordination _coordination;
  StreamSubscription<void>? _sharedChanges;
  bool _checkingShared = false;
  Future<void>? _expiryCleanup;
  final TenantRepositoryFactory _factory;
  LiveImRepository? _runtime;
  _SessionRecord? _record;
  TenantRegistrationPending? _pending;
  TenantPasswordTask? _passwordTask;
  ({String phone, TenantPasswordTask task})? _recoveryChallenge;
  bool _passwordRecoveryEnabled = false;
  bool _passwordChangeEnabled = false, _changingPassword = false;
  final _fence = TenantEpoch();
  bool _busy = false, _closed = false, _loggingOut = false;
  Timer? _expiry;
  Future<void> _lifecycle = Future<void>.value();
  Future<void> _credentials = Future<void>.value();
  final Map<String, String> _pushBindings = {};
  TenantPushCapabilities? _pushCapabilities;
  DateTime? _pushCapabilitiesAt;
  final _events = StreamController<ImEvent>.broadcast();
  final _connections = StreamController<bool>.broadcast();
  final _calls = StreamController<CallSignalEvent>.broadcast();
  final List<StreamSubscription<dynamic>> _subscriptions = [];

  @override
  LiveImRepository? get live => _runtime;
  @override
  Stream<ImEvent> get events => _events.stream;
  @override
  Stream<bool> get connectionChanges => _connections.stream;
  @override
  Stream<CallSignalEvent> get callEvents => _calls.stream;
  @override
  bool get hasPendingRegistration => _pending != null;
  @override
  bool get supportsPasswordChange => _passwordChangeEnabled;
  @override
  TenantPasswordProgress? get passwordChangeProgress => _passwordTask == null
      ? null
      : _passwordTask!.expired
      ? TenantPasswordProgress.expired
      : _passwordTask!.progress;

  Future<T> _serialized<T>(Future<T> Function() work) {
    final next = _lifecycle.then((_) => work());
    _lifecycle = next.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return next;
  }

  // Platform refresh rotates the credential used for device binding. Serialize
  // those RPCs, not just their local writes. Separate from the lifecycle queue:
  // restoring a business repository can itself need a refresh.
  Future<T> _withCredentials<T>(Future<T> Function() work) {
    final next = _credentials.then(
      (_) => _coordination.run(() async {
        await _adoptSharedCredential();
        return work();
      }),
    );
    _credentials = next.then<void>(
      (_) {},
      onError: (Object _, StackTrace _) {},
    );
    return next;
  }

  Future<_SessionRecord?> _readSharedRecord() async {
    final raw = await _store.readJson('session');
    return raw is Map<String, Object?> ? _SessionRecord.fromJson(raw) : null;
  }

  Future<void> _persistRecord(_SessionRecord record) async {
    await _store.writeJson('session', record.toJson());
    if (_coordination.enabled) {
      await _store.writeJson('tab-session', record.toJson());
    }
    _coordination.changed();
  }

  // Called only under the cross-tab lock. A rotated token may be adopted, but
  // another tab's later business expiry must never extend this tab's JWT life.
  Future<void> _adoptSharedCredential() async {
    final record = _record;
    if (!_coordination.enabled || record == null) return;
    final epoch = _fence.capture();
    final shared = await _readSharedRecord();
    _check(epoch);
    if (shared == null || !record.sameLogin(shared)) {
      _expire(_fence.capture(), sharedChanged: true);
      throw StateError('另一个标签页已退出或切换账号，请重新登录');
    }
    _record = record.withRefreshToken(shared.platformRefreshToken);
  }

  Future<void> _checkSharedSession() async {
    if (_closed || _busy || _loggingOut || _record == null || _checkingShared) {
      return;
    }
    _checkingShared = true;
    final epoch = _fence.capture();
    try {
      await _coordination.run(_adoptSharedCredential);
    } catch (_) {
      if (!_closed && _record != null && epoch == _fence.capture()) {
        _expire(epoch, sharedChanged: true);
      }
    } finally {
      _checkingShared = false;
    }
  }

  static String _newLoginGeneration() {
    final random = Random.secure();
    return base64Url
        .encode(List<int>.generate(32, (_) => random.nextInt(256)))
        .replaceAll('=', '');
  }

  // Expiry only removes the credential it observed; explicit user logout may
  // remove its own login's latest rotation. Never erase a different login.
  Future<String?> _removeSharedSession(
    _SessionRecord? observed, {
    required bool explicit,
  }) async {
    if (!_coordination.enabled) {
      await _store.remove('session');
      return observed?.platformRefreshToken;
    }
    final shared = await _readSharedRecord();
    if (observed == null ||
        shared == null ||
        !observed.sameLogin(shared) ||
        (!explicit &&
            observed.platformRefreshToken != shared.platformRefreshToken)) {
      return null;
    }
    await _store.remove('session');
    _coordination.changed();
    return shared.platformRefreshToken;
  }

  @override
  Future<TenantPushCapabilities> pushCapabilities() async {
    final epoch = _fence.capture();
    _check(epoch);
    if (_pushCapabilities != null &&
        _pushCapabilitiesAt != null &&
        DateTime.now().difference(_pushCapabilitiesAt!) <
            const Duration(minutes: 1)) {
      return _pushCapabilities!;
    }
    final result = await _auth.pushCapabilities();
    _check(epoch);
    _pushCapabilities = result;
    _pushCapabilitiesAt = DateTime.now();
    return result;
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
  }) async {
    await _registerPushDevice(
      deviceId: deviceId,
      platform: platform,
      provider: provider,
      pushToken: pushToken,
      notificationsEnabled: notificationsEnabled,
      previewEnabled: previewEnabled,
      soundEnabled: soundEnabled,
      vibrationEnabled: vibrationEnabled,
    );
  }

  @override
  Future<TenantPushBinding> registerBrowserPush({
    required String deviceId,
    required String subscription,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  }) async => TenantPushBinding(
    await _registerPushDevice(
      deviceId: deviceId,
      platform: 'web',
      provider: 'webpush',
      pushToken: subscription,
      notificationsEnabled: notificationsEnabled,
      previewEnabled: previewEnabled,
      soundEnabled: soundEnabled,
      vibrationEnabled: vibrationEnabled,
    ),
  );

  Future<Map<String, Object?>> _registerPushDevice({
    required String deviceId,
    required String platform,
    required String provider,
    required String pushToken,
    required bool notificationsEnabled,
    required bool previewEnabled,
    required bool soundEnabled,
    required bool vibrationEnabled,
  }) {
    final epoch = _fence.capture();
    final runtime = _runtime;
    if (runtime == null ||
        _loggingOut ||
        _busy ||
        _closed ||
        platform != _auth.clientPlatform ||
        !(provider == 'getui' && const {'android', 'ios'}.contains(platform) ||
            provider == 'getui_voip' && platform == 'ios' ||
            provider == 'webpush' && platform == 'web')) {
      return Future.error(StateError('当前会话不能登记此推送设备'));
    }
    return _withCredentials(() async {
      _check(epoch);
      if (_loggingOut || !identical(runtime, _runtime) || _record == null) {
        throw StateError('企业会话已变更');
      }
      final config = await pushCapabilities();
      _check(epoch);
      if (!config.providers.contains(provider)) {
        throw const ImApiException(
          statusCode: 503,
          code: 'PLATFORM_PUSH_UNAVAILABLE',
          message: '平台未启用此推送方式',
        );
      }
      final record = _record!;
      if (record.authVersion < 1 ||
          record.realmVersion < 1 ||
          !DateTime.now().toUtc().isBefore(record.expiresAt)) {
        throw StateError('请先续期平台会话再登记推送设备');
      }
      try {
        final result = await _auth.bindPushDevice(record.platformRefreshToken, {
          'deviceId': deviceId,
          'platform': platform,
          'provider': provider,
          'pushToken': pushToken,
          'notificationsEnabled': notificationsEnabled,
          'previewEnabled': previewEnabled,
          'soundEnabled': soundEnabled,
          'vibrationEnabled': vibrationEnabled,
        });
        _check(epoch);
        _pushBindings[deviceId] = provider;
        final bound = <String, Object?>{
          'deviceId': deviceId,
          'pushBindingId': result['id'],
          'pushBindingRevision': result['revision'],
          'leaseExpiresAt': result['leaseExpiresAt'],
          'tenantId': record.context.tenantId,
          'localUserId': record.localUserId,
          'assignmentVersion': record.context.assignmentVersion,
          'authVersion': record.authVersion,
          'realmVersion': record.realmVersion,
        };
        if (_nativeCalls.enabled && provider != 'webpush') {
          await _nativeCalls.bind(
            provider,
            notificationsEnabled ? TenantPushBinding(bound) : null,
          );
          _check(epoch);
        }
        return bound;
      } on TenantAuthException catch (error) {
        throw _error(error);
      }
    });
  }

  @override
  Future<void> removeUserDevice(String deviceId) {
    final epoch = _fence.capture();
    return _withCredentials(() async {
      _check(epoch);
      final record = _record;
      final provider = _pushBindings[deviceId];
      if (record == null || provider == null || _loggingOut) return;
      await _auth.unbindPushDevice(
        record.platformRefreshToken,
        deviceId,
        provider,
      );
      _check(epoch);
      _pushBindings.remove(deviceId);
      if (_nativeCalls.enabled && provider != 'webpush') {
        await _nativeCalls.bind(provider, null);
        _check(epoch);
      }
    });
  }

  void _check(int epoch) {
    _fence.check(epoch);
    if (_closed) throw StateError('认证连接已关闭');
  }

  ImApiException _error(TenantAuthException error) => ImApiException(
    statusCode: error.statusCode,
    code: error.code,
    message: error.message,
  );

  @override
  Future<AuthPolicy> authPolicy() async {
    _passwordChangeEnabled = false;
    _passwordRecoveryEnabled = false;
    final data = await _auth.policy();
    _passwordChangeEnabled = data['passwordChangeEnabled'] == true;
    _passwordRecoveryEnabled = data['passwordResetEnabled'] == true;
    return AuthPolicy.fromJson({
      ...data,
      // No fallback to tenant-local QR/password reset authentication.
      'otpLoginEnabled': data['otpLoginEnabled'] == true,
      'registrationEnabled': data['registrationEnabled'] == true,
      'qrLoginEnabled': false,
      'passwordResetEnabled': _passwordRecoveryEnabled,
    });
  }

  @override
  Future<String?> requestCode(String phone) async {
    try {
      await _auth.requestCode(phone);
      return null;
    } on TenantAuthException catch (error) {
      throw _error(error);
    }
  }

  // Recovery uses a separate platform SMS challenge, never a login OTP or the
  // enterprise-local reset endpoints. Before submit it lives only in memory.
  @override
  Future<void> requestPasswordResetCode(String phone) async {
    if (!_passwordRecoveryEnabled) {
      throw const ImApiException(
        statusCode: 409,
        code: 'PLATFORM_PASSWORD_RESET_UNAVAILABLE',
        message: '平台短信找回密码未启用，请联系企业管理员重置',
      );
    }
    if (_busy ||
        _loggingOut ||
        _runtime != null ||
        (_passwordTask != null &&
            !_passwordTask!.expired &&
            _passwordTask!.progress != TenantPasswordProgress.completed)) {
      throw StateError('请先查询原密码任务，或确认后移除本机提示');
    }
    final epoch = _fence.capture();
    _check(epoch);
    _busy = true;
    _recoveryChallenge = null;
    final task = TenantPasswordTask.recovery();
    try {
      await _auth.requestRecoveryCode(phone.trim(), task);
      _check(epoch);
      _recoveryChallenge = (phone: phone.trim(), task: task);
    } on TenantAuthException catch (error) {
      throw _error(error);
    } finally {
      _busy = false;
    }
  }

  @override
  Future<void> resetPassword({
    required String phone,
    required String code,
    required String password,
  }) async {
    if (!_passwordRecoveryEnabled) {
      throw const ImApiException(
        statusCode: 409,
        code: 'PLATFORM_PASSWORD_RESET_UNAVAILABLE',
        message: '平台短信找回密码未启用，请联系企业管理员重置',
      );
    }
    final challenge = _recoveryChallenge;
    if (_busy ||
        _loggingOut ||
        _runtime != null ||
        challenge == null ||
        challenge.phone != phone.trim() ||
        DateTime.now().toUtc().difference(challenge.task.createdAt) >=
            const Duration(minutes: 10)) {
      throw StateError('请为当前手机号重新获取找回密码验证码');
    }
    if (!RegExp(r'^\d{6}$').hasMatch(code.trim()) ||
        password.runes.length < 8 ||
        utf8.encode(password).length > 72) {
      throw StateError('请输入 6 位验证码；密码至少 8 个字符且最多 72 字节');
    }
    final epoch = _fence.capture();
    _check(epoch);
    _busy = true;
    final task = challenge.task;
    try {
      await _submitPasswordTask(
        task,
        epoch,
        () => _auth.recoverPassword(task, code.trim(), password),
      );
      _recoveryChallenge = null;
    } finally {
      _busy = false;
    }
  }

  @override
  Future<void> changeLoginPassword(String current, String next) async {
    final record = _record;
    if (!_passwordChangeEnabled || record == null || _busy || _loggingOut) {
      throw StateError('当前暂不能修改密码，请等待或重新登录');
    }
    if (current.isEmpty ||
        utf8.encode(current).length > 72 ||
        next.runes.length < 8 ||
        utf8.encode(next).length > 72) {
      throw StateError('请填写当前密码；新密码至少 8 个字符且最多 72 字节');
    }
    final epoch = _fence.capture();
    _check(epoch);
    _busy = true;
    _changingPassword = true;
    _expiry?.cancel();
    try {
      await _withCredentials(() async {
        _check(epoch);
        final latest = _record;
        if (latest == null || !record.sameIdentity(latest)) {
          throw StateError('企业会话已变更');
        }
        final task = TenantPasswordTask.create(latest.platformRefreshToken);
        await _submitPasswordTask(
          task,
          epoch,
          () => _auth.changePassword(task, current, next),
        );
      });
      // Stop business traffic for accepted OR uncertain requests. A late result
      // cannot log out a different account/session.
      if (!_closed && epoch == _fence.capture()) await logout();
    } finally {
      _busy = false;
      _changingPassword = false;
      if (!_closed && epoch == _fence.capture() && _record != null) {
        _scheduleExpiry(epoch);
      }
    }
  }

  Future<void> _submitPasswordTask(
    TenantPasswordTask task,
    int epoch,
    Future<TenantPasswordResult> Function() submit,
  ) async {
    // Persist before the side effect. Storage failure means no request is sent.
    await _serialized(() async {
      _check(epoch);
      await _store.writeJson('password-task', task.toJson());
      _check(epoch);
      _passwordTask = task;
    });
    try {
      final result = await submit();
      _check(epoch);
      await _serialized(() async {
        _check(epoch);
        if (!identical(_passwordTask, task)) return;
        final updated = task.update(result);
        await _store.writeJson('password-task', updated.toJson());
        _check(epoch);
        _passwordTask = updated;
      });
    } on TenantAuthException catch (error) {
      // Known API rejection is final only for this fresh attempt. Network,
      // malformed/redirected responses and timeouts remain unconfirmed.
      if ({400, 401, 403, 404, 409, 429}.contains(error.statusCode)) {
        await _serialized(() async {
          if (!identical(_passwordTask, task)) return;
          await _store.remove('password-task');
          _passwordTask = null;
        });
        throw _error(error);
      }
    } on ImApiException {
      rethrow;
    } catch (_) {
      // The original request may have committed. Keep the encrypted query
      // capability, never store/replay the passwords or claim cancellation.
    }
  }

  @override
  Future<void> checkPasswordChange() async {
    final task = _passwordTask;
    if (task == null ||
        task.expired ||
        _busy ||
        _runtime != null ||
        _loggingOut) {
      return;
    }
    final epoch = _fence.capture();
    _busy = true;
    try {
      final result = await _auth.passwordChangeStatus(task);
      await _serialized(() async {
        _check(epoch);
        if (!identical(_passwordTask, task)) return;
        final updated = task.update(result);
        await _store.writeJson('password-task', updated.toJson());
        _check(epoch);
        _passwordTask = updated;
      });
    } on TenantAuthException catch (error) {
      throw _error(error);
    } finally {
      _busy = false;
    }
  }

  @override
  Future<void> dismissPasswordChange() => _serialized(() async {
    if (_busy || _runtime != null) throw StateError('请等待当前操作完成');
    await _store.remove('password-task');
    _passwordTask = null;
  });

  Future<AppUser> _authenticate(
    Future<TenantBusinessSession> Function() work,
  ) async {
    if (_busy || _loggingOut || _runtime != null) {
      throw StateError('请等待当前操作完成或先退出登录');
    }
    final epoch = _fence.capture();
    _check(epoch);
    _busy = true;
    try {
      return await _coordination.run(() async {
        _check(epoch);
        TenantBusinessSession? session;
        var installed = false;
        try {
          session = await work();
          _check(epoch);
          final user = await _serialized(() => _install(session!, epoch));
          installed = true;
          return user;
        } on TenantRegistrationPending catch (pending) {
          await _serialized(() async {
            _check(epoch);
            _pending = pending;
            await _store.writeJson('pending', {
              'jobId': pending.jobId,
              'pollToken': pending.pollToken,
            });
          });
          throw const ImApiException(
            statusCode: 202,
            code: 'REGISTRATION_PENDING',
            message: '企业账号正在开通，可点击“检查开户进度”继续；无需重新获取验证码',
          );
        } on TenantAuthException catch (error) {
          throw _error(error);
        } finally {
          if (!installed && session != null) {
            try {
              await _auth.discardSession(session.platformRefreshToken);
            } catch (_) {}
          }
        }
      });
    } finally {
      _busy = false;
    }
  }

  @override
  Future<AppUser> passwordLogin(String phone, String password) =>
      _authenticate(() => _auth.passwordLogin(phone, password));
  @override
  Future<AppUser> login(String phone, String code, {String inviteCode = ''}) =>
      tenantLogin(phone, code, inviteCode: inviteCode);
  @override
  Future<AppUser> tenantLogin(
    String phone,
    String code, {
    String enterpriseCode = '',
    String inviteCode = '',
  }) => _authenticate(
    () => _auth.otpLogin(
      phone: phone,
      code: code,
      enterpriseCode: enterpriseCode,
      inviteCode: inviteCode,
    ),
  );
  @override
  Future<AppUser> register({
    required String phone,
    required String code,
    required String password,
    required String name,
    String inviteCode = '',
  }) => tenantRegister(
    phone: phone,
    code: code,
    password: password,
    name: name,
    inviteCode: inviteCode,
  );
  @override
  Future<AppUser> tenantRegister({
    required String phone,
    required String code,
    required String password,
    required String name,
    String enterpriseCode = '',
    String inviteCode = '',
  }) => _authenticate(
    () => _auth.register(
      phone: phone,
      code: code,
      password: password,
      name: name,
      enterpriseCode: enterpriseCode,
      inviteCode: inviteCode,
    ),
  );

  @override
  Future<AppUser> resumeRegistration() => _authenticate(() async {
    final pending = _pending;
    if (pending == null) throw StateError('没有待完成的开户任务');
    final status = await _auth.registrationStatus(pending);
    if (!status.completed) {
      throw ImApiException(
        statusCode: 202,
        code: 'REGISTRATION_PENDING',
        message: status.needsAttention
            ? '开户需要管理员处理（${status.errorCode}），处理后可再次检查进度'
            : '企业账号仍在开通，请稍后检查进度',
      );
    }
    return _auth.completeRegistration(pending);
  });

  @override
  Future<void> dismissPendingRegistration() => _serialized(() async {
    if (_busy) throw StateError('请等待当前操作完成');
    _pending = null;
    await _store.remove('pending');
  });

  Future<AppUser> _install(
    TenantBusinessSession session,
    int epoch, {
    String? generation,
  }) async {
    _check(epoch);
    final record = _SessionRecord.fromSession(
      session,
      generation: generation ?? _newLoginGeneration(),
    );
    await _detach();
    _check(epoch);
    final runtime = _factory(
      session.context,
      session.cacheNamespace,
      () => _refresh(record, epoch),
    );
    _runtime = runtime;
    _record = record;
    try {
      final user = await runtime.acceptPlatformSession(session.businessSession);
      _check(epoch);
      await _persistRecord(record);
      _check(epoch);
      _pending = null;
      _recoveryChallenge = null;
      await _store.remove('pending');
      _check(epoch);
      // A successful new login owns the current screen; never surface an old
      // account's pending task or apply its delayed status response here.
      await _store.remove('password-task');
      _passwordTask = null;
      _check(epoch);
      await _attach(runtime, epoch, resetNativeBindings: true);
      return user;
    } catch (_) {
      _invalidateNativeCalls();
      await _detach();
      await _store.remove('session');
      rethrow;
    }
  }

  Future<void> _attach(
    LiveImRepository runtime,
    int epoch, {
    bool resetNativeBindings = false,
  }) async {
    await _updatePresentationScope(
      epoch,
      resetNativeBindings: resetNativeBindings,
    );
    _check(epoch);
    bool current() =>
        !_closed && identical(runtime, _runtime) && epoch == _fence.capture();
    _subscriptions.add(
      runtime.events.listen((event) {
        if (!current()) return;
        if (event.type == ImEventType.sessionExpired) {
          _expire(epoch);
        } else {
          _events.add(event);
        }
      }),
    );
    _subscriptions.add(
      runtime.connectionChanges.listen((connected) {
        if (current()) _connections.add(connected);
      }),
    );
    _subscriptions.add(
      runtime.callEvents.listen((event) {
        if (current()) _calls.add(event);
      }),
    );
    _scheduleExpiry(epoch);
  }

  Future<void> _updatePresentationScope(
    int epoch, {
    bool resetNativeBindings = false,
  }) async {
    _check(epoch);
    final record = _record!;
    await _nativeCalls.replace(
      TenantCallScope({
        'tenantId': record.context.tenantId,
        'localUserId': record.localUserId,
        'assignmentVersion': record.context.assignmentVersion,
        'authVersion': record.authVersion,
        'realmVersion': record.realmVersion,
        'expiresAt': record.expiresAt.toIso8601String(),
        'callSessionId': record.generation,
      }),
      resetBindings: resetNativeBindings,
    );
    _check(epoch);
    RuntimeEndpoints.attach(
      this,
      _record!.context,
      namespace: _record!.context.cacheNamespace(_record!.localUserId),
      localUserId: _record!.localUserId,
      authVersion: _record!.authVersion,
      realmVersion: _record!.realmVersion,
      expiresAt: _record!.expiresAt,
      callSessionId: _record!.generation,
    );
  }

  void _scheduleExpiry(int epoch) {
    _expiry?.cancel();
    final remaining =
        _record!.expiresAt.difference(DateTime.now().toUtc()) -
        const Duration(seconds: 30);
    _expiry = Timer(remaining.isNegative ? Duration.zero : remaining, () async {
      final record = _record;
      if (record == null || epoch != _fence.capture()) return;
      final renewed = await _runtime?.renewPlatformSession() ?? false;
      if (epoch != _fence.capture() || renewed) return;
      // A failed platform renewal cannot extend the cached business lifetime.
      final untilExpiry = record.expiresAt.difference(DateTime.now().toUtc());
      _expiry = Timer(
        untilExpiry.isNegative ? Duration.zero : untilExpiry,
        () => _expire(epoch),
      );
    });
  }

  Future<Map<String, Object?>> _refresh(_SessionRecord original, int epoch) =>
      _withCredentials(() async {
        _check(epoch);
        if (_changingPassword) {
          throw const ImApiException(
            statusCode: 503,
            code: 'PASSWORD_CHANGE_PENDING',
            message: '正在修改密码，暂不续期',
          );
        }
        final record = _record;
        if (record == null || !record.sameIdentity(original)) {
          throw StateError('企业会话已变更');
        }
        TenantBusinessSession? session;
        var installed = false;
        try {
          session = await _auth.refresh(
            record.platformRefreshToken,
            record.context,
          );
          _check(epoch);
          if (session.localUserId != record.localUserId) {
            throw const TenantAuthException(
              'TENANT_ASSIGNMENT_CHANGED',
              '企业身份已变更，请重新登录',
              statusCode: 401,
            );
          }
          final updated = _SessionRecord.fromSession(
            session,
            generation: record.generation,
          );
          // Logout/expiry drain this queue before removing credentials. Do not
          // acquire the lifecycle queue here (restore may already hold it).
          await _persistRecord(updated);
          _check(epoch);
          _record = updated;
          installed = true;
          await _updatePresentationScope(epoch);
          _check(epoch);
          _scheduleExpiry(epoch);
          return session.businessSession;
        } on TenantAuthException catch (error) {
          throw _error(error);
        } finally {
          if (!installed && session != null) {
            try {
              await _auth.discardSession(session.platformRefreshToken);
            } catch (_) {}
          }
        }
      });

  @override
  Future<bool> restoreSession() async {
    if (_busy || _closed || _loggingOut || _runtime != null) return false;
    final epoch = _fence.capture();
    _busy = true;
    try {
      _SessionRecord? cached;
      var installedShared = false;
      await _coordination.run(() async {
        try {
          final pending = await _store.readJson('pending');
          _check(epoch);
          if (pending is Map<String, Object?> &&
              pending['jobId'] is String &&
              pending['pollToken'] is String) {
            _pending = TenantRegistrationPending(
              pending['jobId']! as String,
              pending['pollToken']! as String,
            );
          }
          final passwordTask = await _store.readJson('password-task');
          _check(epoch);
          if (passwordTask is Map<String, Object?>) {
            try {
              _passwordTask = TenantPasswordTask.fromJson(passwordTask);
            } on FormatException {
              await _store.remove('password-task');
            }
            if (_passwordTask != null) {
              // A crash between submit and acceptance must not restore stale login.
              // The task belongs to this tab. A different tab may since have
              // logged into a different (or the same) account; never erase it.
              if (!_coordination.enabled) await _store.remove('session');
              return;
            }
          }
          final raw = await _store.readJson('session');
          _check(epoch);
          if (raw is! Map<String, Object?>) return;
          var record = _SessionRecord.fromJson(raw);
          if (record.generation == null) {
            record = record.withGeneration(_newLoginGeneration());
            await _store.writeJson('session', record.toJson());
            _coordination.changed();
          }
          try {
            final session = await _auth.refresh(
              record.platformRefreshToken,
              record.context,
            );
            var installed = false;
            try {
              _check(epoch);
              if (session.localUserId != record.localUserId) {
                throw const TenantAuthException(
                  'TENANT_ASSIGNMENT_CHANGED',
                  '企业身份已变更，请重新登录',
                  statusCode: 401,
                );
              }
              await _serialized(
                () => _install(session, epoch, generation: record.generation),
              );
              installed = true;
              installedShared = true;
              return;
            } finally {
              if (!installed) {
                try {
                  await _auth.discardSession(session.platformRefreshToken);
                } catch (_) {}
              }
            }
          } on TenantAuthException catch (error) {
            if (error.statusCode < 500) rethrow;
          } on TimeoutException {
            /* valid cached sessions only */
          }
          if (_coordination.enabled) {
            final tab = await _store.readJson('tab-session');
            if (tab is! Map<String, Object?>) return;
            final own = _SessionRecord.fromJson(tab);
            if (!own.sameLogin(record)) return;
            cached = own.withRefreshToken(record.platformRefreshToken);
          } else {
            cached = record;
          }
        } on TenantAuthException catch (error) {
          await _serialized(() async {
            await _runtime?.clearTenantCredentials();
            await _detach();
            await _store.remove('session');
            _coordination.changed();
          });
          throw _error(error);
        }
      });
      if (installedShared) return true;
      final record = cached;
      if (record == null) return false;
      // Network errors other than a known temporary platform failure fail closed.
      if (!DateTime.now().toUtc().isBefore(record.expiresAt)) return false;
      return await _serialized(() async {
        _check(epoch);
        final runtime = _factory(
          record.context,
          record.context.cacheNamespace(record.localUserId),
          () => _refresh(record, epoch),
        );
        _runtime = runtime;
        _record = record;
        final restored = await runtime.restoreSession();
        _check(epoch);
        if (!restored || runtime.currentUser?.id != record.localUserId) {
          await _detach();
          return false;
        }
        // A crash can persist platform metadata before the matching business
        // token. Do not extend that cached token using a peer's later expiry.
        final expiry = runtime.tenantCredentialExpiry(
          authVersion: _record!.authVersion,
          realmVersion: _record!.realmVersion,
        );
        if (expiry == null || !DateTime.now().toUtc().isBefore(expiry)) {
          await _clearLocalCredentials();
          return false;
        }
        if (expiry.isBefore(_record!.expiresAt)) {
          _record = _record!.withExpiry(expiry);
        }
        await _coordination.run(() async {
          _check(epoch);
          await _adoptSharedCredential();
          await _attach(runtime, epoch);
        });
        return true;
      });
    } finally {
      _busy = false;
    }
  }

  void _expire(int epoch, {bool sharedChanged = false}) {
    if (_closed || epoch != _fence.capture()) return;
    final observed = _record;
    _invalidateNativeCalls();
    _fence.invalidate();
    _auth.epoch.invalidate();
    _runtime?.invalidateTenantSession();
    RuntimeEndpoints.clear(this);
    _expiry?.cancel();
    _events.add(const ImEvent(type: ImEventType.sessionExpired, payload: {}));
    _expiryCleanup = _credentials.then((_) async {
      try {
        await _coordination.run(() async {
          final refresh = sharedChanged
              ? null
              : await _removeSharedSession(observed, explicit: false);
          if (refresh != null) {
            try {
              await _auth.discardSession(refresh);
            } catch (_) {}
          }
        });
      } finally {
        // Local shutdown cannot depend on a working cross-tab lock or store.
        // Do not delete shared authority if its current owner is unconfirmed.
        await _serialized(_clearLocalCredentials);
      }
    });
    unawaited(
      _expiryCleanup!.catchError((Object _) {
        // Already fenced synchronously. An unavailable shared store must not be
        // replaced with an unconditional delete of another tab's credentials.
      }),
    );
  }

  Future<void> _clearLocalCredentials({bool revoke = false}) async {
    try {
      try {
        await _nativeRevocation;
      } finally {
        if (_coordination.enabled) await _store.remove('tab-session');
      }
    } finally {
      try {
        if (revoke) {
          await _runtime?.revokeTenantCredentials();
        } else {
          await _runtime?.clearTenantCredentials();
        }
      } finally {
        await _detach();
      }
    }
  }

  void _invalidateNativeCalls() {
    _nativeRevocation = _nativeCalls.replace(null);
    unawaited(_nativeRevocation.catchError((Object _) {}));
  }

  Future<void> _detach() async {
    _expiry?.cancel();
    RuntimeEndpoints.clear(this);
    final runtime = _runtime;
    _runtime = null;
    _record = null;
    _pushBindings.clear();
    _pushCapabilities = null;
    _pushCapabilitiesAt = null;
    runtime?.invalidateTenantSession();
    for (final subscription in _subscriptions) {
      await subscription.cancel();
    }
    _subscriptions.clear();
    await runtime?.close();
  }

  @override
  Future<void> logout() async {
    if (_loggingOut) return;
    _loggingOut = true;
    _recoveryChallenge = null;
    final observed = _record;
    _invalidateNativeCalls();
    _fence.invalidate();
    _auth.epoch.invalidate();
    _runtime?.invalidateTenantSession();
    RuntimeEndpoints.clear(this);
    try {
      await _credentials;
      try {
        await _coordination.run(() async {
          final refresh = await _removeSharedSession(observed, explicit: true);
          if (refresh != null) {
            try {
              await _auth.logout(refresh);
            } catch (_) {
              /* never restore local credentials */
            }
          }
        });
      } finally {
        await _serialized(() => _clearLocalCredentials(revoke: true));
      }
    } finally {
      _loggingOut = false;
    }
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _fence.invalidate();
    _auth.epoch.invalidate();
    _runtime?.invalidateTenantSession();
    await _sharedChanges?.cancel();
    await _credentials;
    try {
      await _expiryCleanup;
    } catch (_) {}
    await _serialized(_detach);
    _coordination.close();
    _auth.close();
    await _events.close();
    await _connections.close();
    await _calls.close();
  }
}

class _SessionRecord {
  const _SessionRecord(
    this.context,
    this.localUserId,
    this.platformRefreshToken,
    this.expiresAt,
    this.authVersion,
    this.realmVersion, {
    this.generation,
  });
  final TenantContext context;
  final String localUserId, platformRefreshToken;
  final DateTime expiresAt;
  final int authVersion, realmVersion;
  final String? generation;
  factory _SessionRecord.fromSession(
    TenantBusinessSession session, {
    String? generation,
  }) {
    final parts = (session.businessSession['accessToken']! as String).split(
      '.',
    );
    if (parts.length != 3) throw const FormatException('业务会话缺少有效期');
    final claims =
        jsonDecode(utf8.decode(base64Url.decode(base64Url.normalize(parts[1]))))
            as Map;
    final exp = claims['exp'];
    if (exp is! int) throw const FormatException('业务会话有效期不正确');
    final expiry = DateTime.fromMillisecondsSinceEpoch(exp * 1000, isUtc: true);
    if (!DateTime.now().toUtc().isBefore(expiry)) {
      throw const FormatException('业务会话已过期');
    }
    return _SessionRecord(
      session.context,
      session.localUserId,
      session.platformRefreshToken,
      expiry,
      claims['ver'] is int ? claims['ver'] as int : 0,
      claims['realm'] is int ? claims['realm'] as int : 0,
      generation: generation,
    );
  }
  factory _SessionRecord.fromJson(Map<String, Object?> json) {
    final context = TenantContext.fromJson(
      json['context'] as Map<String, Object?>,
    );
    final uid = json['localUserId']! as String;
    context.cacheNamespace(uid);
    final token = json['platformRefreshToken']! as String;
    if (!RegExp(r'^[a-zA-Z0-9_-]{43}$').hasMatch(token)) {
      throw const FormatException('平台会话无效');
    }
    final generation = json['generation'];
    if (generation != null &&
        (generation is! String ||
            !RegExp(r'^[a-zA-Z0-9_-]{43}$').hasMatch(generation))) {
      throw const FormatException('平台登录代次不正确');
    }
    return _SessionRecord(
      context,
      uid,
      token,
      DateTime.parse(json['expiresAt']! as String).toUtc(),
      json['authVersion'] is int ? json['authVersion'] as int : 0,
      json['realmVersion'] is int ? json['realmVersion'] as int : 0,
      generation: generation as String?,
    );
  }
  bool sameIdentity(_SessionRecord other) =>
      localUserId == other.localUserId && context.sameAssignment(other.context);
  bool sameLogin(_SessionRecord other) =>
      generation != null &&
      generation == other.generation &&
      sameIdentity(other) &&
      authVersion == other.authVersion &&
      realmVersion == other.realmVersion;
  _SessionRecord withGeneration(String value) => _SessionRecord(
    context,
    localUserId,
    platformRefreshToken,
    expiresAt,
    authVersion,
    realmVersion,
    generation: value,
  );
  _SessionRecord withRefreshToken(String value) => _SessionRecord(
    context,
    localUserId,
    value,
    expiresAt,
    authVersion,
    realmVersion,
    generation: generation,
  );
  _SessionRecord withExpiry(DateTime value) => _SessionRecord(
    context,
    localUserId,
    platformRefreshToken,
    value,
    authVersion,
    realmVersion,
    generation: generation,
  );
  Map<String, Object?> toJson() => {
    'context': context.toJson(),
    'localUserId': localUserId,
    'platformRefreshToken': platformRefreshToken,
    'expiresAt': expiresAt.toIso8601String(),
    'authVersion': authVersion,
    'realmVersion': realmVersion,
    if (generation != null) 'generation': generation,
  };
}
