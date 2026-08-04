import 'call_models.dart';

enum SystemCallActionType { accept, decline, end, timeout, mute }

class SystemCallAction {
  const SystemCallAction({
    required this.type,
    required this.serverCallId,
    required this.systemCallId,
    this.muted,
  });

  final SystemCallActionType type;
  final String serverCallId;
  final String systemCallId;
  final bool? muted;
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
  Future<String?> voipPushToken();
  Future<void> dispose();
}
