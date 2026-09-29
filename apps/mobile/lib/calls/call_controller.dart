import 'dart:async';
import 'dart:math';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../core/models.dart';
import '../core/app_config.dart';
import '../core/runtime_endpoints.dart';
import '../core/tenant_call_scope.dart';
import '../core/user_identity.dart';
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
    bool? requiresTenantScope,
    this.preparationTimeout = const Duration(seconds: 30),
  }) : _requiresTenantScope =
           requiresTenantScope ?? AppConfig.usesPlatformAuthentication,
       _engineFactory = engineFactory ?? LiveKitCallMediaEngine.new,
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
  final bool _requiresTenantScope;
  final Duration preparationTimeout;
  _CallAttempt? _attempt;
  int _generation = 0;
  bool preparingMedia = false;
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
  bool _changingMute = false;
  bool _changingSpeaker = false;
  bool _changingCamera = false;
  bool _changingScreenShare = false;
  bool _switchingCameraLens = false;
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
    final attempt = _beginAttempt();
    try {
      _draftConversation = conversation;
      _draftMediaType = mediaType;
      errorMessage = null;
      phase = CallPhase.connecting;
      notifyListeners();
      final configuration = await _loadConfiguration(attempt);
      final memberCount = conversation.memberCount > 0
          ? conversation.memberCount
          : conversation.members.length;
      if (memberCount < 2 || memberCount > configuration.maxParticipants) {
        throw StateError('群通话仅支持 2–${configuration.maxParticipants} 人');
      }
      await _prepareEngine(configuration, mediaType, attempt);
      final invited = await _waitFor(
        repository
            .inviteCall(
              callId: callId,
              conversationId: conversation.id,
              calleeUserId: callee?.id,
              mediaType: mediaType,
            )
            .then((call) async {
              // The HTTP request may finish after cancellation. Close only that
              // invitation, and never use a replacement account's credentials.
              if (!_current(attempt) && _sameIdentity(attempt)) {
                try {
                  await repository.cancelCall(
                    call.id,
                    reason: 'cancelled_by_caller',
                  );
                } catch (_) {}
              }
              return call;
            }),
        attempt,
      );
      session = invited;
      phase = CallPhase.outgoing;
      _startInviteDeadline(session!.expiresAt);
      notifyListeners();
      if (callee != null) {
        unawaited(
          _systemCalls.showOutgoing(
            session: session!,
            calleeName: callee.name,
            calleeHandle: publicUserHandle(callee.handle),
            avatarUrl: callee.avatarUrl,
          ),
        );
      }
    } on _CallCancelled {
      if (identical(_attempt, attempt)) await _reset();
      return;
    } catch (error) {
      if (!_current(attempt)) return;
      await _fail(_readableError(error, '无法发起通话'));
      rethrow;
    }
  }

  Future<void> accept() async {
    if (phase != CallPhase.incoming || session == null || _answering) return;
    _answering = true;
    final attempt = _attempt!;
    final incoming = session!;
    _stopRinging();
    phase = CallPhase.connecting;
    notifyListeners();
    try {
      final configuration = await _loadConfiguration(attempt);
      await _prepareEngine(configuration, incoming.mediaType, attempt);
      session = await _waitFor(
        repository.acceptCall(incoming.id).then((call) async {
          if (!_current(attempt) && _sameIdentity(attempt)) {
            try {
              await repository.hangupCall(
                call.id,
                reason: 'cancelled_while_accepting',
              );
            } catch (_) {}
          }
          return call;
        }),
        attempt,
      );
      _inviteTimer?.cancel();
      await _joinMedia();
    } on _CallCancelled {
      if (identical(_attempt, attempt)) await _reset();
      return;
    } catch (error) {
      if (!_current(attempt)) return;
      await _endAcceptedCallAfterMediaFailure();
      if (!_current(attempt)) return;
      await _fail(_readableError(error, '接听失败，请稍后重试'));
    } finally {
      if (identical(_attempt, attempt)) _answering = false;
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
    if (phase == CallPhase.idle) return;
    final finishing = _finish('通话结束');
    try {
      if (active != null) {
        final ending = active.status == 'invited'
            ? active.callerId == currentUser()?.id
                  ? repository.cancelCall(
                      active.id,
                      reason: 'cancelled_by_caller',
                    )
                  : repository.rejectCall(active.id, reason: 'declined')
            : repository.hangupCall(active.id, reason: 'completed');
        await ending.timeout(const Duration(seconds: 5));
      }
    } catch (_) {
      // 本地媒体必须立即释放；服务端会通过状态查询或房间清理最终收敛。
    } finally {
      await finishing;
    }
  }

  Future<void> dismissFailure() async {
    if (phase == CallPhase.failed) await _reset();
  }

  Future<void> toggleMute() async {
    if (_changingMute) return;
    final previous = muted;
    final target = !previous;
    _changingMute = true;
    muted = target;
    notifyListeners();
    try {
      await _engine?.setMuted(target);
      final callId = session?.id;
      if (callId != null) unawaited(_systemCalls.setMuted(callId, target));
      errorMessage = null;
    } catch (error) {
      muted = previous;
      errorMessage = _readableError(error, '麦克风状态切换失败，请重试');
    } finally {
      _changingMute = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> setMuted(bool value) async {
    if (muted == value) return;
    if (_changingMute) return;
    final previous = muted;
    _changingMute = true;
    muted = value;
    notifyListeners();
    try {
      await _engine?.setMuted(value);
      errorMessage = null;
    } catch (error) {
      muted = previous;
      errorMessage = _readableError(error, '麦克风状态切换失败，请重试');
    } finally {
      _changingMute = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> toggleSpeaker() async {
    if (_changingSpeaker) return;
    final previous = speakerEnabled;
    final target = !previous;
    _changingSpeaker = true;
    speakerEnabled = target;
    notifyListeners();
    try {
      await _engine?.setSpeakerEnabled(target);
      errorMessage = null;
    } catch (error) {
      speakerEnabled = previous;
      errorMessage = _readableError(error, '扬声器切换失败，请重试');
    } finally {
      _changingSpeaker = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> toggleCamera() async {
    if (!isVideo || _changingCamera) return;
    final previous = cameraEnabled;
    final target = !previous;
    _changingCamera = true;
    cameraEnabled = target;
    notifyListeners();
    try {
      await _engine?.setCameraEnabled(target);
      errorMessage = null;
    } catch (error) {
      cameraEnabled = previous;
      errorMessage = _readableError(error, '摄像头状态切换失败，请重试');
    } finally {
      _changingCamera = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> toggleScreenShare() async {
    if (!supportsScreenShare || _changingScreenShare) return;
    final target = !screenShareEnabled;
    _changingScreenShare = true;
    try {
      await _engine?.setScreenShareEnabled(target);
      screenShareEnabled = target;
      errorMessage = null;
    } catch (error) {
      errorMessage = _readableError(error, '无法共享屏幕');
    } finally {
      _changingScreenShare = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> switchCamera() async {
    if (!isVideo || _switchingCameraLens) return;
    _switchingCameraLens = true;
    try {
      await _engine?.switchCamera();
      errorMessage = null;
    } catch (error) {
      errorMessage = _readableError(error, '摄像头切换失败，请重试');
    } finally {
      _switchingCameraLens = false;
      if (!_disposed) notifyListeners();
    }
  }

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
    bool allowed() {
      final current = TenantCallScope.parse(
        RuntimeEndpoints.notificationContext,
      );
      if (_requiresTenantScope ||
          current != null ||
          action.tenantScope != null) {
        return current != null &&
            current.validNow &&
            action.tenantScope != null &&
            action.tenantScope!.validNow &&
            current.sameSession(action.tenantScope!);
      }
      return true;
    }

    if (currentUser() != null && !allowed()) {
      await _systemCalls.dismiss(action.systemCallId);
      return;
    }
    if (session?.id != action.serverCallId) {
      if (phase != CallPhase.idle) return;
      final deadline = DateTime.now().add(const Duration(seconds: 12));
      while (currentUser() == null && DateTime.now().isBefore(deadline)) {
        await Future<void>.delayed(const Duration(milliseconds: 150));
        if (_disposed) return;
      }
      if (currentUser() == null || !allowed()) {
        await _systemCalls.dismiss(action.systemCallId);
        return;
      }
      try {
        final call = await repository.getCall(action.serverCallId);
        if (_disposed || !allowed()) {
          await _systemCalls.dismiss(action.systemCallId);
          return;
        }
        await _showIncoming(call, presentSystemUi: false);
      } catch (_) {
        await _systemCalls.dismiss(action.systemCallId);
        return;
      }
    }
    if (!allowed()) {
      await _systemCalls.dismiss(action.systemCallId);
      return;
    }
    if (session?.id != action.serverCallId) {
      // Android Telecom can retain a ringing self-managed call while the app
      // process is frozen. If the business call has since expired or ended,
      // remove that stale native call before it blocks the next invitation.
      await _systemCalls.dismiss(action.systemCallId);
      return;
    }
    switch (action.type) {
      case SystemCallActionType.restore:
        // `_showIncoming` above has restored the in-app deadline and state.
        // The existing native call remains responsible for ringing/UI.
        return;
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
    // JS dartify() returns nested Map<Object?, Object?> values. Normalise the
    // call object at this boundary instead of silently dropping Web invites.
    final parsedCall = callMap is Map
        ? CallSession.fromJson(Map<String, Object?>.from(callMap))
        : null;
    final eventCallId = event.payload['callId'] as String? ?? parsedCall?.id;
    switch (event.type) {
      case 'call.invite' || 'call.invited':
        if (parsedCall != null) await _showIncoming(parsedCall);
      case 'call.accepted':
        if (session?.id != eventCallId) return;
        final attempt = _attempt;
        if (attempt == null || !_current(attempt) || _answering) return;
        final wasActive = phase == CallPhase.active;
        final accepted = parsedCall ?? await repository.getCall(eventCallId!);
        if (!_current(attempt)) return;
        session = accepted;
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
        } on _CallCancelled {
          return;
        } catch (error) {
          if (!_current(attempt)) return;
          await _endAcceptedCallAfterMediaFailure();
          if (!_current(attempt)) return;
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

  Future<void> _showIncoming(
    CallSession incoming, {
    bool presentSystemUi = true,
  }) async {
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
    final attempt = _beginAttempt();
    session = incoming;
    phase = CallPhase.incoming;
    errorMessage = null;
    _startInviteDeadline(incoming.expiresAt);
    if (!presentSystemUi) {
      notifyListeners();
      return;
    }
    final managedBySystem = await _systemCalls.showIncoming(
      session: incoming,
      callerName: peer?.name ?? conversation?.title ?? '联系人',
      callerHandle: publicUserHandle(peer?.handle),
      avatarUrl: peer?.avatarUrl ?? conversation?.avatarUrl,
    );
    if (!_current(attempt)) return;
    if (!managedBySystem) _startRinging();
    notifyListeners();
  }

  Future<CallConfiguration> _loadConfiguration(_CallAttempt attempt) async {
    final existing = _configuration;
    if (existing != null) return existing;
    final loaded = await _waitFor(repository.callConfiguration(), attempt);
    _configuration = loaded;
    return loaded;
  }

  Future<void> _prepareEngine(
    CallConfiguration configuration,
    CallMediaType mediaType,
    _CallAttempt attempt,
  ) async {
    _checkAttempt(attempt);
    if (_engine != null) return;
    final engine = _engineFactory();
    _engine = engine;
    _connections = engine.connectionChanges.listen((value) {
      if (_current(attempt) && identical(_engine, engine)) {
        _onConnectionChanged(value);
      }
    });
    _mediaChanges = engine.mediaChanges.listen((_) {
      if (!_current(attempt) || !identical(_engine, engine)) return;
      screenShareEnabled = engine.screenShareEnabled;
      if (!_disposed) notifyListeners();
    });
    preparingMedia = true;
    notifyListeners();
    try {
      await _waitFor(
        engine.initialize(configuration: configuration, mediaType: mediaType),
        attempt,
      );
      await _waitFor(engine.setSpeakerEnabled(speakerEnabled), attempt);
    } finally {
      if (_current(attempt)) {
        preparingMedia = false;
        notifyListeners();
      }
    }
  }

  Future<void> _joinMedia() async {
    if (_joining || phase == CallPhase.active) return;
    final active = session;
    if (active == null || active.status != 'accepted') {
      throw StateError('通话尚未接通');
    }
    _joining = true;
    final attempt = _attempt!;
    try {
      final configuration = await _loadConfiguration(attempt);
      await _prepareEngine(configuration, active.mediaType, attempt);
      final mediaSession = await _waitFor(
        repository.joinCall(active.id),
        attempt,
      );
      await _waitFor(_engine!.connect(mediaSession), attempt);
    } finally {
      if (identical(_attempt, attempt)) _joining = false;
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
    final attempt = _attempt;
    if (active != null && active.status == 'accepted') {
      try {
        await repository.hangupCall(active.id, reason: 'media_failed');
      } catch (_) {}
    }
    if (attempt != null && _current(attempt)) {
      await _fail(message);
      _failing = false;
    }
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
    _invalidateAttempt();
    final callId = session?.id;
    if (callId != null) unawaited(_systemCalls.end(callId));
    errorMessage = message;
    phase = CallPhase.failed;
    if (!_disposed) notifyListeners();
    await _releaseEngine();
  }

  Future<void> _finish(String message) async {
    if (phase == CallPhase.idle) return;
    final generation = _invalidateAttempt();
    final callId = session?.id;
    if (callId != null) unawaited(_systemCalls.end(callId));
    _stopRinging();
    errorMessage = message;
    phase = CallPhase.ended;
    if (!_disposed) notifyListeners();
    final release = _releaseEngine();
    await Future<void>.delayed(const Duration(milliseconds: 650));
    await release;
    if (generation == _generation) await _reset();
  }

  Future<void> _reset() async {
    _invalidateAttempt();
    _inviteTimer?.cancel();
    _durationTimer?.cancel();
    _stopRinging();
    await _releaseEngine();
    session = null;
    phase = CallPhase.idle;
    errorMessage = null;
    _configuration = null;
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
    final connections = _connections;
    final mediaChanges = _mediaChanges;
    _connections = null;
    _mediaChanges = null;
    final engine = _engine;
    _engine = null;
    await connections?.cancel();
    await mediaChanges?.cancel();
    if (engine != null) {
      try {
        await engine.dispose().timeout(const Duration(seconds: 3));
      } catch (_) {}
    }
  }

  _CallAttempt _beginAttempt() {
    _invalidateAttempt();
    return _attempt = _CallAttempt(
      currentUser()?.id,
      TenantCallScope.parse(RuntimeEndpoints.notificationContext),
    );
  }

  int _invalidateAttempt() {
    final attempt = _attempt;
    _attempt = null;
    if (attempt != null && !attempt.cancelled.isCompleted) {
      attempt.cancelled.complete();
    }
    preparingMedia = false;
    return ++_generation;
  }

  bool _sameIdentity(_CallAttempt attempt) {
    if (currentUser()?.id != attempt.userId) return false;
    final scope = TenantCallScope.parse(RuntimeEndpoints.notificationContext);
    if (!_requiresTenantScope && scope == null && attempt.scope == null) {
      return true;
    }
    return scope != null &&
        scope.validNow &&
        attempt.scope != null &&
        scope.sameSession(attempt.scope!);
  }

  bool _current(_CallAttempt attempt) =>
      !_disposed && identical(_attempt, attempt) && _sameIdentity(attempt);

  void _checkAttempt(_CallAttempt attempt) {
    if (!_current(attempt)) throw const _CallCancelled();
  }

  Future<T> _waitFor<T>(Future<T> work, _CallAttempt attempt) async {
    final result = await Future.any<T>([
      work,
      attempt.cancelled.future.then<T>((_) => throw const _CallCancelled()),
    ]).timeout(preparationTimeout);
    _checkAttempt(attempt);
    return result;
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

class _CallAttempt {
  _CallAttempt(this.userId, this.scope);
  final String? userId;
  final TenantCallScope? scope;
  final cancelled = Completer<void>();
}

class _CallCancelled implements Exception {
  const _CallCancelled();
}

String readableCallMediaError(
  Object error, {
  required CallMediaType mediaType,
  required String fallback,
}) {
  final value = error.toString().toLowerCase();
  final devices = mediaType == CallMediaType.video ? '摄像头和麦克风' : '麦克风';
  if (error is TimeoutException) {
    return '通话准备超时，请检查$devices权限和网络后重试';
  }
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
