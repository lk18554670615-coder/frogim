import 'dart:async';
import 'package:flutter/services.dart';
import '../core/app_config.dart';
import '../core/runtime_endpoints.dart';
import '../core/tenant_call_scope.dart';
import 'call_models.dart';
import 'system_call_identity.dart';
import 'system_call_service_contract.dart';

/// iOS owns one early-created CallKit provider. The third-party Android
/// integration is deliberately not used for PushKit UUID/account bookkeeping.
class IosSystemCallService implements SystemCallService {
  IosSystemCallService({MethodChannel? channel, bool? managed})
    : _channel =
          channel ??
          const MethodChannel('top.hongjinghuanqiu.app/system_calls'),
      _managed = managed ?? AppConfig.usesPlatformAuthentication;
  final MethodChannel _channel;
  final bool _managed;
  final _actions = StreamController<SystemCallAction>.broadcast();
  final _calls = <String, ({String serverId, TenantCallScope? scope})>{};
  final _seen = <String>{};
  static IosSystemCallService? _owner;
  bool _disposed = false;
  Future<void>? _ready;
  @override
  Stream<SystemCallAction> get actions => _actions.stream;
  bool _current(TenantCallScope? scope, {bool requireFresh = true}) {
    final current = TenantCallScope.parse(RuntimeEndpoints.notificationContext);
    if (!_managed && current == null && scope == null) return true;
    return !_disposed &&
        current != null &&
        current.validNow &&
        scope != null &&
        (!requireFresh || scope.validNow) &&
        current.sameSession(scope);
  }

  @override
  Future<void> initialize() => _ready ??= _initialize();
  Future<void> _initialize() async {
    _owner = this;
    _channel.setMethodCallHandler((call) async {
      if (!_disposed &&
          call.method == 'systemCallAction' &&
          call.arguments is Map) {
        _emit(call.arguments as Map);
      }
    });
    try {
      final list = await _channel.invokeListMethod<Object?>(
        'drainLaunchActions',
      );
      for (final raw in list ?? const []) {
        if (raw is Map) _emit(raw);
      }
    } on MissingPluginException {
      // Native tests/unsupported host. Never fall back to an unscoped provider.
    } on PlatformException {
      // A failed restore is not authorization to accept a legacy call.
    }
  }

  void _emit(Map<Object?, Object?> raw) {
    if (_disposed) return;
    final action = systemCallActionFromMap(raw);
    if (action == null) return;
    _calls[action.systemCallId] = (
      serverId: action.serverCallId,
      scope: action.tenantScope,
    );
    final key = '${action.systemCallId}|${action.type.name}|${action.muted}';
    if (action.type != SystemCallActionType.mute && !_seen.add(key)) return;
    if (_seen.length > 64) _seen.remove(_seen.first);
    _actions.add(
      action,
    ); // CallController rechecks after cold-start authentication.
  }

  Future<bool> _show(
    CallSession session,
    bool outgoing,
    String name,
    String? handle,
  ) async {
    final scope = TenantCallScope.parse(RuntimeEndpoints.notificationContext);
    await initialize();
    if (_disposed || !_current(scope)) return false;
    final id = systemCallIdFor(session.id, scope: scope);
    _calls[id] = (serverId: session.id, scope: scope);
    try {
      final accepted = await _channel
          .invokeMethod<bool>(outgoing ? 'showOutgoing' : 'showIncoming', {
            'id': id,
            'serverCallId': session.id,
            'mediaType': session.mediaType.name,
            'displayName': name,
            'handle': ?handle,
            'expiresAt': session.expiresAt.toUtc().toIso8601String(),
            if (scope != null) 'tenantScope': scope.data,
          });
      if (accepted != true || _disposed || !_current(scope)) {
        await dismiss(id);
        return false;
      }
      return true;
    } on MissingPluginException {
      _calls.remove(id);
      return false;
    } on PlatformException {
      await dismiss(id);
      return false;
    }
  }

  @override
  Future<bool> showIncoming({
    required CallSession session,
    required String callerName,
    String? callerHandle,
    String? avatarUrl,
  }) => _show(session, false, callerName, callerHandle);
  @override
  Future<void> showOutgoing({
    required CallSession session,
    required String calleeName,
    String? calleeHandle,
    String? avatarUrl,
  }) async {
    await _show(session, true, calleeName, calleeHandle);
  }

  String? _id(String serverId) => _calls.entries
      // An established call survives a same-login credential renewal. Fresh
      // current authority is still required; pending native actions are never
      // renewed here and are separately checked by CallController.
      .where(
        (e) =>
            e.value.serverId == serverId &&
            _current(e.value.scope, requireFresh: false),
      )
      .map((e) => e.key)
      .firstOrNull;
  @override
  Future<void> setConnected(String serverCallId) async {
    final id = _id(serverCallId);
    if (id != null) {
      await _channel.invokeMethod<bool>('callConnected', {'id': id});
    }
  }

  @override
  Future<void> setMuted(String serverCallId, bool muted) async {
    final id = _id(serverCallId);
    if (id != null) {
      await _channel.invokeMethod<bool>('muteCall', {'id': id, 'muted': muted});
    }
  }

  @override
  Future<void> end(String serverCallId) async {
    final id = _id(serverCallId);
    if (id != null) await dismiss(id);
  }

  @override
  Future<void> dismiss(String systemCallId) async {
    _calls.remove(systemCallId);
    try {
      await _channel.invokeMethod<bool>('endCall', {'id': systemCallId});
    } on MissingPluginException {
      /* No native call to end in unsupported hosts. */
    } on PlatformException {
      // Native identity updates also clean their exact obsolete UUIDs.
    }
  }

  @override
  Future<void> preparePermissions() async {}
  @override
  Future<String?> voipPushToken() async {
    try {
      final token = await _channel.invokeMethod<String>('voipToken');
      return token == null || token.isEmpty ? null : token;
    } on MissingPluginException {
      return null;
    } on PlatformException {
      return null;
    }
  }

  @override
  Future<void> dispose() async {
    _disposed = true;
    if (identical(_owner, this)) {
      _channel.setMethodCallHandler(null);
      _owner = null;
    }
    for (final id in _calls.keys.toList()) {
      await dismiss(id);
    }
    await _actions.close();
  }
}
