import 'call_models.dart';

/// 音视频通话的业务控制接口；媒体协商与实时传输由 LiveKit SDK 负责。
abstract interface class CallRepository {
  Stream<CallSignalEvent> get callEvents;

  Future<CallConfiguration> callConfiguration();
  Future<CallSession> inviteCall({
    required String callId,
    required String conversationId,
    String? calleeUserId,
    required CallMediaType mediaType,
  });
  Future<CallSession> getCall(String callId);
  Future<CallSession> acceptCall(String callId);
  Future<CallSession> rejectCall(String callId, {String reason = ''});
  Future<CallSession> cancelCall(String callId, {String reason = ''});
  Future<CallSession> hangupCall(String callId, {String reason = 'completed'});
  Future<CallMediaSession> joinCall(String callId);
}
