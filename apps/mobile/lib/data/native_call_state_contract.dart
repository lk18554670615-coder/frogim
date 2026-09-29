import '../core/tenant_call_scope.dart';
import '../core/tenant_push.dart';

abstract interface class NativeCallState {
  bool get enabled;
  Future<void> replace(TenantCallScope? scope, {bool resetBindings = false});
  Future<void> bind(String provider, TenantPushBinding? binding);
}

class NoNativeCallState implements NativeCallState {
  @override
  bool get enabled => false;
  @override
  Future<void> replace(
    TenantCallScope? scope, {
    bool resetBindings = false,
  }) async {}
  @override
  Future<void> bind(String provider, TenantPushBinding? binding) async {}
}
