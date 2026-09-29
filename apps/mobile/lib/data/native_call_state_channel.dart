import 'dart:async';
import 'package:flutter/services.dart';
import '../core/tenant_call_scope.dart';
import '../core/tenant_push.dart';
import 'native_call_state_contract.dart';

/// Carries no API/IM/provider credentials. Native code reads the atomically
/// persisted identity and binding leases even with no running Flutter engine.
class ChannelNativeCallState implements NativeCallState {
  ChannelNativeCallState({MethodChannel? channel})
    : _channel =
          channel ??
          const MethodChannel('top.hongjinghuanqiu.app/system_calls');
  final MethodChannel _channel;
  TenantCallScope? _scope;
  final _bindings = <String, Map<String, Object?>>{};
  Future<void> _tail = Future.value();
  Future<String>? _control;
  int _sequence = 0;
  @override
  bool get enabled => true;
  @override
  Future<void> replace(TenantCallScope? scope, {bool resetBindings = false}) {
    if (scope == null ||
        _scope == null ||
        !scope.sameSession(_scope!) ||
        resetBindings) {
      _bindings.clear();
    }
    _scope = scope;
    _bindings.removeWhere(
      (_, binding) => !DateTime.now().toUtc().isBefore(
        DateTime.parse(binding['leaseExpiresAt']! as String),
      ),
    );
    return _persist();
  }

  @override
  Future<void> bind(String provider, TenantPushBinding? binding) {
    if (!const {'getui', 'getui_voip'}.contains(provider)) {
      throw StateError('原生推送来源不支持');
    }
    if (binding == null) {
      _bindings.remove(provider);
    } else {
      final scope = _scope;
      final candidate = TenantCallScope.parse({
        ...binding.context,
        'expiresAt': binding.context['leaseExpiresAt'],
      });
      if (scope == null ||
          !scope.validNow ||
          candidate == null ||
          !scope.sameIdentity(candidate)) {
        throw StateError('原生推送身份已失效');
      }
      _bindings[provider] = {
        for (final key in const [
          'deviceId',
          'tenantId',
          'localUserId',
          'assignmentVersion',
          'authVersion',
          'realmVersion',
          'pushBindingId',
          'pushBindingRevision',
          'leaseExpiresAt',
        ])
          key: binding.context[key],
        'provider': provider,
      };
    }
    return _persist();
  }

  Future<void> _persist() {
    final data = <String, Object?>{
      'sequence': ++_sequence,
      'scope': _scope?.data,
      'bindings': _bindings.values.toList(growable: false),
    };
    final next = _tail.then((_) async {
      final control = await (_control ??= _open());
      final accepted = await _channel
          .invokeMethod<bool>('setTenantCallState', {
            ...data,
            'control': control,
          })
          .timeout(const Duration(seconds: 5));
      if (accepted != true) throw StateError('无法确认原生来电身份状态');
    });
    _tail = next.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return next;
  }

  Future<String> _open() async {
    try {
      final token = await _channel
          .invokeMethod<String>('beginTenantCallControl')
          .timeout(const Duration(seconds: 5));
      if (token == null || token.isEmpty) throw StateError('原生来电控制未确认');
      return token;
    } catch (_) {
      _control = null;
      rethrow;
    }
  }
}
