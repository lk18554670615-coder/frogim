import 'call_models.dart';
import 'system_call_service_contract.dart';

SystemCallService createSystemCallService() => const _StubSystemCallService();

class _StubSystemCallService implements SystemCallService {
  const _StubSystemCallService();

  @override
  Stream<SystemCallAction> get actions => const Stream.empty();
  @override
  Future<void> initialize() async {}
  @override
  Future<void> preparePermissions() async {}
  @override
  Future<bool> showIncoming({
    required CallSession session,
    required String callerName,
    String? callerHandle,
    String? avatarUrl,
  }) async => false;
  @override
  Future<void> showOutgoing({
    required CallSession session,
    required String calleeName,
    String? calleeHandle,
    String? avatarUrl,
  }) async {}
  @override
  Future<void> setConnected(String serverCallId) async {}
  @override
  Future<void> setMuted(String serverCallId, bool muted) async {}
  @override
  Future<void> end(String serverCallId) async {}
  @override
  Future<String?> voipPushToken() async => null;
  @override
  Future<void> dispose() async {}
}
