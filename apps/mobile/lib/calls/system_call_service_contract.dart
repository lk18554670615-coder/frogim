import 'call_models.dart';
import '../core/tenant_call_scope.dart';

enum SystemCallActionType { restore, accept, decline, end, timeout, mute }

class SystemCallAction {
  const SystemCallAction({
    required this.type,
    required this.serverCallId,
    required this.systemCallId,
    this.muted,
    this.tenantScope,
  });

  final SystemCallActionType type;
  final String serverCallId;
  final String systemCallId;
  final bool? muted;
  final TenantCallScope? tenantScope;
}

abstract interface class SystemCallService {
  Stream<SystemCallAction> get actions;

  Future<void> initialize();
  Future<void> preparePermissions();

  /// 返回 true 表示系统已经接管响铃与来电界面。
  Future<bool> showIncoming({
    required CallSession session,
    required String callerName,
    String? callerHandle,
    String? avatarUrl,
  });

  Future<void> showOutgoing({
    required CallSession session,
    required String calleeName,
    String? calleeHandle,
    String? avatarUrl,
  });

  Future<void> setConnected(String serverCallId);
  Future<void> setMuted(String serverCallId, bool muted);
  Future<void> end(String serverCallId);

  /// Remove this exact native UI, never derive an ID using a newer account.
  Future<void> dismiss(String systemCallId);
  Future<String?> voipPushToken();
  Future<void> dispose();
}
