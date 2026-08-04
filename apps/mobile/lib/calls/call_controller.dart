import 'dart:async';
import 'dart:math';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

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
  }) : _engineFactory = engineFactory ?? WebRtcCallMediaEngine.new,
       _systemCalls = systemCallService ?? createSystemCallService() {
    _events = repository.callEvents.listen(_onSignalEvent);
    _systemActions = _systemCalls.actions.listen(_onSystemCallAction);
    unawaited(_systemCalls.initialize());
  }

  final CallRepository repository;
  final AppUser? Function() currentUser;
  final Conversation? Function(String id) findConversation;
  final CallEngineFactory _engineFactory;
  final SystemCallService _systemCalls;
  late final StreamSubscription<CallSignalEvent> _events;
  late final StreamSubscription<SystemCallAction> _systemActions;
  final Random _random = Random.secure();

  CallSession? session;
  CallPhase phase = CallPhase.idle;
  String? errorMessage;
  bool muted = false;
  bool speakerEnabled = true;
  bool cameraEnabled = true;
  DateTime? connectedAt;
  Duration elapsed = Duration.zero;

  CallMediaEngine? _engine;
  StreamSubscription<CallConnectionState>? _connections;
  StreamSubscription<Map<String, Object?>>? _candidates;
  Timer? _inviteTimer;
  Timer? _durationTimer;
  Timer? _disconnectTimer;
  Timer? _ringTimer;
  Map<String, Object?>? _pendingOffer;
  Map<String, Object?>? _lastOffer;
  final List<Map<String, Object?>> _pendingRemoteCandidates = [];
  bool _answering = false;
  bool _cameraSuspendedByLifecycle = false;
  bool _hasTurnServer = false;
  bool _disposed = false;
  Conversation? _draftConversation;
  CallMediaType? _draftMediaType;

  bool get isVisible => phase != CallPhase.idle;
  bool get isIncoming => phase == CallPhase.incoming;
  bool get isVideo =>
      (session?.mediaType ?? _draftMediaType) == CallMediaType.video;
  RTCVideoRenderer? get localRenderer => _engine?.localRenderer;
  RTCVideoRenderer? get remoteRenderer => _engine?.remoteRenderer;
  Future<String?> voipPushToken() => _systemCalls.voipPushToken();
  Future<void> prepareSystemCallPermissions() =>
      _systemCalls.preparePermissions();
  Conversation? get conversation => session == null
      ? _draftConversation
      : findConversation(session!.conversationId) ?? _draftConversation;
  AppUser? get peer {
    final me = currentUser()?.id;
    return conversation?.members.where((member) => member.id != me).firstOrNull;
  }

  Future<void> startCall(
    Conversation conversation,
    CallMediaType mediaType,
  ) async {
    if (conversation.kind != ConversationKind.direct) {
      throw StateError('群聊暂不支持音视频通话');
    }
    if (phase != CallPhase.idle) throw StateError('当前已有通话进行中');
    final me = currentUser();
    final callee = conversation.members
        .where((member) => member.id != me?.id)
        .firstOrNull;
    if (me == null || callee == null) throw StateError('无法识别通话联系人');
    final callId = _newCallId();
    try {
      _draftConversation = conversation;
      _draftMediaType = mediaType;
      errorMessage = null;
      phase = CallPhase.connecting;
      notifyListeners();
      final configuration = await repository.callConfiguration();
      await _prepareEngine(configuration, mediaType);
      session = await repository.inviteCall(
        callId: callId,
        conversationId: conversation.id,
        calleeUserId: callee.id,
        mediaType: mediaType,
      );
      phase = CallPhase.outgoing;
      _startInviteDeadline(session!.expiresAt);
      notifyListeners();
      unawaited(
        _systemCalls.showOutgoing(
          session: session!,
          calleeName: callee.name,
          calleeHandle: callee.handle,
          avatarUrl: callee.avatarUrl,
        ),
      );
      final offer = await _engine!.createOffer();
      _lastOffer = {
        'callId': callId,
        'conversationId': conversation.id,
        'calleeUserId': callee.id,
        'mediaType': mediaType.name,
        ...offer,
      };
      await repository.sendCallSignal('call.offer', _lastOffer!);
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
      final configuration = await repository.callConfiguration();
      await _prepareEngine(configuration, session!.mediaType);
      session = await repository.acceptCall(session!.id);
      final offer = await _waitForOffer();
      await _engine!.setRemoteDescription(
        sdp: offer['sdp']! as String,
        type: offer['type'] as String? ?? 'offer',
      );
      await _flushRemoteCandidates();
      final answer = await _engine!.createAnswer();
      await repository.sendCallSignal('call.answer', {
        'callId': session!.id,
        'conversationId': session!.conversationId,
        ...answer,
      });
    } catch (error) {
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
        await repository.sendCallSignal('call.end', {
          'callId': active.id,
          'conversationId': active.conversationId,
          'reason': 'completed',
        });
      }
    } catch (_) {
      // 本地必须立即结束；服务端会按超时或对端事件收敛状态。
    } finally {
      await _finish('通话结束');
    }
  }

  Future<void> dismissFailure() async {
    if (phase != CallPhase.failed) return;
    await _reset();
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
      final incoming = await repository.getCall(callId);
      await _showIncoming(incoming);
    } catch (_) {}
  }

  void _onSignalEvent(CallSignalEvent event) {
    unawaited(_handleSignalEvent(event));
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
        final restored = await repository.getCall(action.serverCallId);
        await _showIncoming(restored);
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

  Future<void> _handleSignalEvent(CallSignalEvent event) async {
    final callMap = event.payload['call'];
    final parsedCall = callMap is Map<String, Object?>
        ? CallSession.fromJson(callMap)
        : null;
    final eventCallId = event.payload['callId'] as String? ?? parsedCall?.id;
    switch (event.type) {
      case 'call.invite' || 'call.invited':
        if (parsedCall != null) await _showIncoming(parsedCall);
      case 'call.offer':
        if (eventCallId == null) return;
        if (session == null && parsedCall != null) {
          await _showIncoming(parsedCall);
        }
        if (session?.id != eventCallId) return;
        _pendingOffer = event.payload;
        notifyListeners();
      case 'call.answer':
        if (session?.id != eventCallId || _engine == null) return;
        final sdp = event.payload['sdp'] as String?;
        if (sdp == null) return;
        await _engine!.setRemoteDescription(
          sdp: sdp,
          type: event.payload['type'] as String? ?? 'answer',
        );
        await _flushRemoteCandidates();
        phase = CallPhase.connecting;
        notifyListeners();
      case 'call.ice':
        if (session?.id != eventCallId) return;
        final candidate = event.payload['candidate'];
        final candidateMap = candidate is Map<String, Object?>
            ? candidate
            : event.payload;
        if (_engine == null) {
          _pendingRemoteCandidates.add(candidateMap);
        } else {
          await _engine!.addRemoteCandidate(candidateMap);
        }
      case 'call.accepted':
        if (session?.id == parsedCall?.id) {
          session = parsedCall;
          phase = CallPhase.connecting;
          _inviteTimer?.cancel();
          notifyListeners();
          final offer = _lastOffer;
          if (offer != null) {
            unawaited(repository.sendCallSignal('call.offer', offer));
          }
        }
      case 'call.rejected':
        if (session?.id == parsedCall?.id) await _finish('对方已拒绝');
      case 'call.cancelled':
        if (session?.id == parsedCall?.id) await _finish('对方已取消');
      case 'call.timeout':
        if (session?.id == parsedCall?.id) await _finish('无人接听');
      case 'call.ended' || 'call.end':
        if (session?.id == eventCallId) await _finish('通话结束');
    }
  }

  Future<void> _showIncoming(CallSession incoming) async {
    if (incoming.isTerminal || incoming.calleeId != currentUser()?.id) return;
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
      callerName: peer?.name ?? conversation?.title ?? '邻里联系人',
      callerHandle: peer?.handle,
      avatarUrl: peer?.avatarUrl ?? conversation?.avatarUrl,
    );
    if (!managedBySystem) _startRinging();
    notifyListeners();
  }

  Future<void> _prepareEngine(
    CallConfiguration configuration,
    CallMediaType mediaType,
  ) async {
    if (_engine != null) return;
    _hasTurnServer = configuration.iceServers.any(
      (server) => server.urls.any((url) {
        final normalized = url.toLowerCase();
        return normalized.startsWith('turn:') ||
            normalized.startsWith('turns:');
      }),
    );
    final engine = _engineFactory();
    _engine = engine;
    await engine.initialize(configuration: configuration, mediaType: mediaType);
    await engine.setSpeakerEnabled(speakerEnabled);
    _connections = engine.connectionChanges.listen(_onConnectionChanged);
    _candidates = engine.localCandidates.listen((candidate) {
      final active = session;
      if (active == null) return;
      unawaited(
        repository.sendCallSignal('call.ice', {
          'callId': active.id,
          'conversationId': active.conversationId,
          'candidate': candidate,
        }),
      );
    });
  }

  void _onConnectionChanged(CallConnectionState value) {
    switch (value) {
      case CallConnectionState.connected:
        _disconnectTimer?.cancel();
        connectedAt ??= DateTime.now();
        phase = CallPhase.active;
        _startDurationTimer();
        final callId = session?.id;
        if (callId != null) unawaited(_systemCalls.setConnected(callId));
      case CallConnectionState.disconnected:
        _disconnectTimer?.cancel();
        _disconnectTimer = Timer(const Duration(seconds: 10), () {
          if (phase != CallPhase.active) return;
          unawaited(_connectionFailed('网络中断，通话已结束'));
        });
      case CallConnectionState.failed:
        unawaited(
          _connectionFailed(
            _hasTurnServer
                ? '媒体连接失败，请检查网络后重新呼叫'
                : '无法建立媒体连接，服务端尚未提供 TURN 中继，请联系管理员',
          ),
        );
      case CallConnectionState.closed:
      case CallConnectionState.connecting:
        break;
    }
    notifyListeners();
  }

  Future<void> _connectionFailed(String message) async {
    final active = session;
    if (active != null) {
      try {
        await repository.hangupCall(active.id, reason: 'ice_failed');
      } catch (_) {}
    }
    await _fail(message);
  }

  Future<Map<String, Object?>> _waitForOffer() async {
    final deadline = DateTime.now().add(const Duration(seconds: 6));
    while (_pendingOffer == null && DateTime.now().isBefore(deadline)) {
      await Future<void>.delayed(const Duration(milliseconds: 100));
    }
    final offer = _pendingOffer;
    if (offer == null || offer['sdp'] is! String) {
      throw StateError('未收到对方通话信令');
    }
    return offer;
  }

  Future<void> _flushRemoteCandidates() async {
    final engine = _engine;
    if (engine == null) return;
    for (final candidate in _pendingRemoteCandidates) {
      await engine.addRemoteCandidate(candidate);
    }
    _pendingRemoteCandidates.clear();
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
      notifyListeners();
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
    notifyListeners();
    await _releaseEngine();
    if (!_disposed && phase == CallPhase.failed) notifyListeners();
  }

  Future<void> _finish(String message) async {
    if (phase == CallPhase.idle) return;
    final callId = session?.id;
    if (callId != null) unawaited(_systemCalls.end(callId));
    _stopRinging();
    errorMessage = message;
    phase = CallPhase.ended;
    notifyListeners();
    await Future<void>.delayed(const Duration(milliseconds: 650));
    await _reset();
  }

  Future<void> _reset() async {
    _inviteTimer?.cancel();
    _durationTimer?.cancel();
    _disconnectTimer?.cancel();
    _stopRinging();
    await _releaseEngine();
    session = null;
    phase = CallPhase.idle;
    errorMessage = null;
    muted = false;
    speakerEnabled = true;
    cameraEnabled = true;
    connectedAt = null;
    elapsed = Duration.zero;
    _pendingOffer = null;
    _lastOffer = null;
    _pendingRemoteCandidates.clear();
    _answering = false;
    _cameraSuspendedByLifecycle = false;
    _hasTurnServer = false;
    _draftConversation = null;
    _draftMediaType = null;
    if (!_disposed) notifyListeners();
  }

  Future<void> _releaseEngine() async {
    await _connections?.cancel();
    await _candidates?.cancel();
    _connections = null;
    _candidates = null;
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

  String _readableError(Object error, String fallback) {
    return readableCallMediaError(
      error,
      mediaType: isVideo ? CallMediaType.video : CallMediaType.audio,
      fallback: fallback,
    );
  }

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
