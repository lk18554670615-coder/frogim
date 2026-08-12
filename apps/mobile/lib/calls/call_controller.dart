import 'dart:async';
import 'dart:math';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../core/models.dart';
import 'call_media_engine.dart';
import 'call_models.dart';
import 'call_repository.dart';
import 'system_call_service.dart';
import 'system_call_service_contract.dart';

typedef CallEngineFactory = CallMediaEngine Function();

class CallController extends ChangeNotifier {
  CallController({
    required this.repository,
    required this.currentUser,
    required this.findConversation,
    CallEngineFactory? engineFactory,
    SystemCallService? systemCallService,
  }) : _engineFactory = engineFactory ?? LiveKitCallMediaEngine.new,
       _systemCalls = systemCallService ?? createSystemCallService() {
    _events = repository.callEvents.listen(_onCallEvent);
    _systemActions = _systemCalls.actions.listen(_onSystemCallAction);
    unawaited(_systemCalls.initialize());
  }

  final CallRepository repository;
  final AppUser? Function() currentUser;
  final Conversation? Function(String id) findConversation;
  final CallEngineFactory _engineFactory;
  final SystemCallService _systemCalls;
  final Random _random = Random.secure();
  late final StreamSubscription<CallSignalEvent> _events;
  late final StreamSubscription<SystemCallAction> _systemActions;

  CallSession? session;
  CallPhase phase = CallPhase.idle;
  String? errorMessage;
  bool muted = false;
  bool speakerEnabled = true;
  bool cameraEnabled = true;
  bool screenShareEnabled = false;
  DateTime? connectedAt;
  Duration elapsed = Duration.zero;

  CallConfiguration? _configuration;
  CallMediaEngine? _engine;
  StreamSubscription<CallConnectionState>? _connections;
  StreamSubscription<void>? _mediaChanges;
  Timer? _inviteTimer;
  Timer? _durationTimer;
  Timer? _ringTimer;
  bool _answering = false;
  bool _joining = false;
  bool _failing = false;
  bool _cameraSuspendedByLifecycle = false;
  bool _disposed = false;
  Conversation? _draftConversation;
  CallMediaType? _draftMediaType;

  bool get isVisible => phase != CallPhase.idle;
  bool get isIncoming => phase == CallPhase.incoming;
  bool get isVideo =>
      (session?.mediaType ?? _draftMediaType) == CallMediaType.video;
  bool get supportsScreenShare =>
      _configuration?.supportsScreenShare == true && phase == CallPhase.active;
  dynamic get localVideoTrack => _engine?.localVideoTrack;
  List<CallRemoteVideo> get remoteVideos => _engine?.remoteVideos ?? const [];
  CallRemoteVideo? get primaryRemoteVideo => remoteVideos.firstOrNull;
  List<String> get activeSpeakerIds => _engine?.activeSpeakerIds ?? const [];
  int get participantCount => _engine?.participantCount ?? 1;
  Future<String?> voipPushToken() => _systemCalls.voipPushToken();
  Future<void> prepareSystemCallPermissions() =>
      _systemCalls.preparePermissions();
  Conversation? get conversation => session == null
      ? _draftConversation
      : findConversation(session!.conversationId) ?? _draftConversation;
  AppUser? get peer {
    if (conversation?.kind == ConversationKind.group) return null;
    final me = currentUser()?.id;
    return conversation?.members.where((member) => member.id != me).firstOrNull;
  }

  Future<void> startCall(
    Conversation conversation,
    CallMediaType mediaType,
  ) async {
    if (phase != CallPhase.idle) throw StateError('当前已有通话进行中');
    final me = currentUser();
    final callee = conversation.kind == ConversationKind.direct
        ? conversation.members
              .where((member) => member.id != me?.id)
              .firstOrNull
        : null;
    if (me == null ||
        (conversation.kind == ConversationKind.direct && callee == null)) {
      throw StateError('无法识别通话成员');
    }
    final callId = _newCallId();
    try {
      _draftConversation = conversation;
      _draftMediaType = mediaType;
      errorMessage = null;
      phase = CallPhase.connecting;
      notifyListeners();
      final configuration = await _loadConfiguration();
      final memberCount = conversation.memberCount > 0
          ? conversation.memberCount
          : conversation.members.length;
      if (memberCount < 2 || memberCount > configuration.maxParticipants) {
        throw StateError('群通话仅支持 2–${configuration.maxParticipants} 人');
      }
      await _prepareEngine(configuration, mediaType);
      session = await repository.inviteCall(
        callId: callId,
        conversationId: conversation.id,
        calleeUserId: callee?.id,
        mediaType: mediaType,
      );
      phase = CallPhase.outgoing;
      _startInviteDeadline(session!.expiresAt);
      notifyListeners();
      if (callee != null) {
        unawaited(
          _systemCalls.showOutgoing(
            session: session!,
            calleeName: callee.name,
            calleeHandle: callee.handle,
            avatarUrl: callee.avatarUrl,
          ),
        );
      }
    } catch (error) {
      await _fail(_readableError(error, '无法发起通话'));
      rethrow;
    }
  }

  Future<void> accept() async {
    if (phase != CallPhase.incoming || session == null || _answering) return;
    _answering = true;
    _stopRinging();
    phase = CallPhase.connecting;
    notifyListeners();
    try {
      final configuration = await _loadConfiguration();
      await _prepareEngine(configuration, session!.mediaType);
      session = await repository.acceptCall(session!.id);
      _inviteTimer?.cancel();
      await _joinMedia();
    } catch (error) {
      await _endAcceptedCallAfterMediaFailure();
      await _fail(_readableError(error, '接听失败，请稍后重试'));
    } finally {
      _answering = false;
    }
  }

  Future<void> reject() async {
    final active = session;
    if (active == null || phase != CallPhase.incoming) return;
    _stopRinging();
    try {
      await repository.rejectCall(active.id, reason: 'declined');
    } finally {
      await _finish('已拒绝');
    }
  }

  Future<void> end() async {
    final active = session;
    if (active == null) return;
    try {
      if (phase == CallPhase.outgoing || active.status == 'invited') {
        await repository.cancelCall(active.id, reason: 'cancelled_by_caller');
      } else if (phase == CallPhase.incoming) {
        await repository.rejectCall(active.id, reason: 'declined');
      } else {
        await repository.hangupCall(active.id, reason: 'completed');
      }
    } catch (_) {
      // 本地媒体必须立即释放；服务端会通过状态查询或房间清理最终收敛。
    } finally {
      await _finish('通话结束');
    }
  }

  Future<void> dismissFailure() async {
    if (phase == CallPhase.failed) await _reset();
  }

  Future<void> toggleMute() async {
    muted = !muted;
    await _engine?.setMuted(muted);
    final callId = session?.id;
    if (callId != null) unawaited(_systemCalls.setMuted(callId, muted));
    notifyListeners();
  }

  Future<void> setMuted(bool value) async {
    if (muted == value) return;
    muted = value;
    await _engine?.setMuted(value);
    notifyListeners();
  }

  Future<void> toggleSpeaker() async {
    speakerEnabled = !speakerEnabled;
    await _engine?.setSpeakerEnabled(speakerEnabled);
    notifyListeners();
  }

  Future<void> toggleCamera() async {
    if (!isVideo) return;
    cameraEnabled = !cameraEnabled;
    await _engine?.setCameraEnabled(cameraEnabled);
    notifyListeners();
  }

  Future<void> toggleScreenShare() async {
    if (!supportsScreenShare) return;
    final target = !screenShareEnabled;
    try {
      await _engine?.setScreenShareEnabled(target);
      screenShareEnabled = target;
      notifyListeners();
    } catch (error) {
      errorMessage = _readableError(error, '无法共享屏幕');
      notifyListeners();
    }
  }

  Future<void> switchCamera() => _engine?.switchCamera() ?? Future.value();

  void handleAppLifecycle(AppLifecycleState state) {
    if (!isVideo || _engine == null) return;
    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.inactive ||
        state == AppLifecycleState.hidden) {
      if (cameraEnabled) {
        _cameraSuspendedByLifecycle = true;
        unawaited(_engine!.setCameraEnabled(false));
      }
    } else if (state == AppLifecycleState.resumed &&
        _cameraSuspendedByLifecycle &&
        cameraEnabled) {
      _cameraSuspendedByLifecycle = false;
      unawaited(_engine!.setCameraEnabled(true));
    }
  }

  Future<void> handlePushPayload(Map<String, dynamic> payload) async {
    final type = (payload['eventType'] ?? payload['type'])?.toString();
    if (type != 'call.invited' && type != 'call.invite') return;
    final callId = (payload['callId'] ?? payload['call_id'])?.toString();
    if (callId == null || callId.isEmpty || session?.id == callId) return;
    try {
      await _showIncoming(await repository.getCall(callId));
    } catch (_) {}
  }

  void _onCallEvent(CallSignalEvent event) {
    unawaited(_handleCallEvent(event));
  }

  void _onSystemCallAction(SystemCallAction action) {
    unawaited(_handleSystemCallAction(action));
  }

  Future<void> _handleSystemCallAction(SystemCallAction action) async {
    if (_disposed) return;
    if (session?.id != action.serverCallId) {
      if (phase != CallPhase.idle) return;
      final deadline = DateTime.now().add(const Duration(seconds: 12));
      while (currentUser() == null && DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 150));
      }
      if (currentUser() == null) return;
      try {
        await _showIncoming(await repository.getCall(action.serverCallId));
      } catch (_) {
        await _systemCalls.end(action.serverCallId);
        return;
      }
    }
    if (session?.id != action.serverCallId) return;
    switch (action.type) {
      case SystemCallActionType.accept:
        await accept();
      case SystemCallActionType.decline:
        await reject();
      case SystemCallActionType.end:
        if (phase != CallPhase.ended && phase != CallPhase.idle) await end();
      case SystemCallActionType.timeout:
        final active = session;
        if (active == null) return;
        try {
          await repository.rejectCall(active.id, reason: 'timeout');
        } catch (_) {}
        await _finish('无人接听');
      case SystemCallActionType.mute:
        await setMuted(action.muted ?? false);
    }
  }

  Future<void> _handleCallEvent(CallSignalEvent event) async {
    final callMap = event.payload['call'];
    final parsedCall = callMap is Map<String, Object?>
        ? CallSession.fromJson(callMap)
        : null;
    final eventCallId = event.payload['callId'] as String? ?? parsedCall?.id;
    switch (event.type) {
      case 'call.invite' || 'call.invited':
        if (parsedCall != null) await _showIncoming(parsedCall);
      case 'call.accepted':
        if (session?.id != eventCallId) return;
        final wasActive = phase == CallPhase.active;
        session = parsedCall ?? await repository.getCall(eventCallId!);
        final currentUserId = currentUser()?.id;
        if (currentUserId == null || !session!.hasJoined(currentUserId)) {
          notifyListeners();
          return;
        }
        if (wasActive) {
          notifyListeners();
          return;
        }
        phase = CallPhase.connecting;
        _inviteTimer?.cancel();
        notifyListeners();
        try {
          await _joinMedia();
        } catch (error) {
          await _endAcceptedCallAfterMediaFailure();
          await _fail(_readableError(error, '无法加入通话'));
        }
      case 'call.rejected':
        if (session?.id == eventCallId) await _finish('对方已拒绝');
      case 'call.participant_declined' || 'call.participant_left':
        if (session?.id == eventCallId && parsedCall != null) {
          session = parsedCall;
          notifyListeners();
        }
      case 'call.cancelled':
        if (session?.id == eventCallId) await _finish('对方已取消');
      case 'call.timeout':
        if (session?.id == eventCallId) await _finish('无人接听');
      case 'call.ended' || 'call.end':
        if (session?.id == eventCallId) await _finish('通话结束');
    }
  }

  Future<void> _showIncoming(CallSession incoming) async {
    final currentUserId = currentUser()?.id;
    if (incoming.isTerminal ||
        currentUserId == null ||
        incoming.callerId == currentUserId ||
        !incoming.includes(currentUserId)) {
      return;
    }
    if (session?.id == incoming.id && phase != CallPhase.idle) return;
    if (phase != CallPhase.idle && session?.id != incoming.id) {
      try {
        await repository.rejectCall(incoming.id, reason: 'busy');
      } catch (_) {}
      return;
    }
    session = incoming;
    phase = CallPhase.incoming;
    errorMessage = null;
    _startInviteDeadline(incoming.expiresAt);
    final managedBySystem = await _systemCalls.showIncoming(
      session: incoming,
      callerName: peer?.name ?? conversation?.title ?? '联系人',
      callerHandle: peer?.handle,
      avatarUrl: peer?.avatarUrl ?? conversation?.avatarUrl,
    );
    if (!managedBySystem) _startRinging();
    notifyListeners();
  }

  Future<CallConfiguration> _loadConfiguration() async {
    final existing = _configuration;
    if (existing != null) return existing;
    final loaded = await repository.callConfiguration();
    _configuration = loaded;
    return loaded;
  }

  Future<void> _prepareEngine(
    CallConfiguration configuration,
    CallMediaType mediaType,
  ) async {
    if (_engine != null) return;
    final engine = _engineFactory();
    _engine = engine;
    _connections = engine.connectionChanges.listen(_onConnectionChanged);
    _mediaChanges = engine.mediaChanges.listen((_) {
      screenShareEnabled = engine.screenShareEnabled;
      if (!_disposed) notifyListeners();
    });
    await engine.initialize(configuration: configuration, mediaType: mediaType);
    await engine.setSpeakerEnabled(speakerEnabled);
  }

  Future<void> _joinMedia() async {
    if (_joining || phase == CallPhase.active) return;
    final active = session;
    if (active == null || active.status != 'accepted') {
      throw StateError('通话尚未接通');
    }
    _joining = true;
    try {
      final configuration = await _loadConfiguration();
      await _prepareEngine(configuration, active.mediaType);
      final mediaSession = await repository.joinCall(active.id);
      await _engine!.connect(mediaSession);
    } finally {
      _joining = false;
    }
  }

  void _onConnectionChanged(CallConnectionState value) {
    switch (value) {
      case CallConnectionState.connected:
        connectedAt ??= DateTime.now();
        phase = CallPhase.active;
        _startDurationTimer();
        final callId = session?.id;
        if (callId != null) unawaited(_systemCalls.setConnected(callId));
      case CallConnectionState.reconnecting:
        if (phase == CallPhase.active) phase = CallPhase.connecting;
      case CallConnectionState.disconnected || CallConnectionState.failed:
        if (!_disposed && phase != CallPhase.ended && phase != CallPhase.idle) {
          unawaited(_connectionFailed('LiveKit 媒体连接已中断，请重新发起通话'));
        }
      case CallConnectionState.closed || CallConnectionState.connecting:
        break;
    }
    if (!_disposed) notifyListeners();
  }

  Future<void> _connectionFailed(String message) async {
    if (_failing) return;
    _failing = true;
    final active = session;
    if (active != null && active.status == 'accepted') {
      try {
        await repository.hangupCall(active.id, reason: 'media_failed');
      } catch (_) {}
    }
    await _fail(message);
    _failing = false;
  }

  Future<void> _endAcceptedCallAfterMediaFailure() async {
    final active = session;
    if (active?.status != 'accepted') return;
    try {
      await repository.hangupCall(active!.id, reason: 'media_failed');
    } catch (_) {}
  }

  void _startInviteDeadline(DateTime expiresAt) {
    _inviteTimer?.cancel();
    final remaining = expiresAt.difference(DateTime.now());
    _inviteTimer = Timer(
      remaining.isNegative ? Duration.zero : remaining,
      () => unawaited(_finish('无人接听')),
    );
  }

  void _startDurationTimer() {
    _durationTimer?.cancel();
    _durationTimer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (connectedAt == null) return;
      elapsed = DateTime.now().difference(connectedAt!);
      if (!_disposed) notifyListeners();
    });
  }

  void _startRinging() {
    _ringTimer?.cancel();
    void ring() {
      if (phase != CallPhase.incoming) return;
      SystemSound.play(SystemSoundType.alert);
      HapticFeedback.heavyImpact();
    }

    ring();
    _ringTimer = Timer.periodic(const Duration(seconds: 2), (_) => ring());
  }

  void _stopRinging() {
    _ringTimer?.cancel();
    _ringTimer = null;
  }

  Future<void> _fail(String message) async {
    final callId = session?.id;
    if (callId != null) unawaited(_systemCalls.end(callId));
    errorMessage = message;
    phase = CallPhase.failed;
    if (!_disposed) notifyListeners();
    await _releaseEngine();
  }

  Future<void> _finish(String message) async {
    if (phase == CallPhase.idle) return;
    final callId = session?.id;
    if (callId != null) unawaited(_systemCalls.end(callId));
    _stopRinging();
    errorMessage = message;
    phase = CallPhase.ended;
    if (!_disposed) notifyListeners();
    await Future<void>.delayed(const Duration(milliseconds: 650));
    await _reset();
  }

  Future<void> _reset() async {
    _inviteTimer?.cancel();
    _durationTimer?.cancel();
    _stopRinging();
    await _releaseEngine();
    session = null;
    phase = CallPhase.idle;
    errorMessage = null;
    muted = false;
    speakerEnabled = true;
    cameraEnabled = true;
    screenShareEnabled = false;
    connectedAt = null;
    elapsed = Duration.zero;
    _answering = false;
    _joining = false;
    _failing = false;
    _cameraSuspendedByLifecycle = false;
    _draftConversation = null;
    _draftMediaType = null;
    if (!_disposed) notifyListeners();
  }

  Future<void> _releaseEngine() async {
    await _connections?.cancel();
    await _mediaChanges?.cancel();
    _connections = null;
    _mediaChanges = null;
    final engine = _engine;
    _engine = null;
    if (engine != null) await engine.dispose();
  }

  String _newCallId() {
    final now = DateTime.now().microsecondsSinceEpoch.toRadixString(36);
    final random = List.generate(
      12,
      (_) => _random.nextInt(36).toRadixString(36),
    ).join();
    return 'call-$now-$random';
  }

  String _readableError(Object error, String fallback) =>
      readableCallMediaError(
        error,
        mediaType: isVideo ? CallMediaType.video : CallMediaType.audio,
        fallback: fallback,
      );

  @override
  void dispose() {
    _disposed = true;
    unawaited(_events.cancel());
    unawaited(_systemActions.cancel());
    unawaited(_systemCalls.dispose());
    unawaited(_reset());
    super.dispose();
  }
}

String readableCallMediaError(
  Object error, {
  required CallMediaType mediaType,
  required String fallback,
}) {
  final value = error.toString().toLowerCase();
  final devices = mediaType == CallMediaType.video ? '摄像头和麦克风' : '麦克风';
  if (value.contains('notallowed') ||
      value.contains('permissiondenied') ||
      value.contains('permission denied') ||
      value.contains('permission')) {
    return '未获得$devices权限，请在浏览器或系统设置中允许后重试';
  }
  if (value.contains('notfounderror') ||
      value.contains('devicesnotfound') ||
      value.contains('no capture device') ||
      value.contains('no device')) {
    return '未检测到可用的$devices，请连接设备后重试';
  }
  if (value.contains('notreadableerror') ||
      value.contains('trackstarterror') ||
      value.contains('device in use') ||
      value.contains('could not start video source')) {
    return '$devices可能正被其他应用占用，请关闭占用程序后重试';
  }
  if (value.contains('overconstrained') ||
      value.contains('constraintnotsatisfied')) {
    return '当前$devices不支持所需采集规格，请更换设备后重试';
  }
  if (value.contains('secure context') ||
      value.contains('getusermedia is not allowed') ||
      value.contains('insecure')) {
    return '浏览器仅允许在 HTTPS 或 localhost 中使用音视频通话';
  }
  return fallback;
}
