import 'call_models.dart';

/// 音视频通话所需的短期信令接口。SDP/ICE 只经 WebSocket 转发，不落本地或服务端数据库。
abstract interface class CallRepository {
  Stream<CallSignalEvent> get callEvents;

  Future<CallConfiguration> callConfiguration();
  Future<CallSession> inviteCall({
    required String callId,
    required String conversationId,
    required String calleeUserId,
    required CallMediaType mediaType,
  });
  Future<CallSession> getCall(String callId);
  Future<CallSession> acceptCall(String callId);
  Future<CallSession> rejectCall(String callId, {String reason = ''});
  Future<CallSession> cancelCall(String callId, {String reason = ''});
  Future<CallSession> hangupCall(String callId, {String reason = 'completed'});
  Future<void> sendCallSignal(String type, Map<String, Object?> payload);
}
