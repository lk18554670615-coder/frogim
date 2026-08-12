import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/calls/call_controller.dart';
import 'package:linli_im/calls/call_media_engine.dart';
import 'package:linli_im/calls/call_models.dart';
import 'package:linli_im/calls/call_repository.dart';
import 'package:linli_im/calls/call_screen.dart';
import 'package:linli_im/calls/system_call_service_contract.dart';
import 'package:linli_im/core/app_controller.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/data/demo_repository.dart';
import 'package:linli_im/ui/screens/chat_screen.dart';
import 'package:livekit_client/livekit_client.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test(
    'Android cold-start accept restores media without presenting incoming UI again',
    () async {
      final fixture = _Fixture(incoming: true);
      addTearDown(fixture.dispose);

      fixture.systemCalls.emit(
        SystemCallAction(
          type: SystemCallActionType.accept,
          serverCallId: fixture.repository.session.id,
          systemCallId: 'cold-start-system-call-id',
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));

      expect(fixture.systemCalls.incomingCount, 0);
      expect(fixture.repository.acceptCount, 1);
      expect(fixture.repository.joinCount, 1);
      expect(fixture.engine.connectCount, 1);
      expect(fixture.controller.phase, CallPhase.connecting);
      fixture.engine.connections.add(CallConnectionState.connected);
      await Future<void>.delayed(Duration.zero);
      expect(fixture.controller.phase, CallPhase.active);
    },
  );

  test(
    'Android process restore reconciles a still-ringing system call',
    () async {
      final fixture = _Fixture(incoming: true);
      addTearDown(fixture.dispose);

      fixture.systemCalls.emit(
        SystemCallAction(
          type: SystemCallActionType.restore,
          serverCallId: fixture.repository.session.id,
          systemCallId: 'restored-system-call-id',
        ),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));

      expect(fixture.controller.phase, CallPhase.incoming);
      expect(fixture.systemCalls.incomingCount, 0);
      expect(fixture.repository.acceptCount, 0);
      expect(fixture.repository.joinCount, 0);
      expect(fixture.systemCalls.endedCallIds, isEmpty);
    },
  );

  test('Android process restore clears a terminal stale system call', () async {
    final fixture = _Fixture(incoming: true);
    addTearDown(fixture.dispose);
    fixture.repository.markMissed();

    fixture.systemCalls.emit(
      SystemCallAction(
        type: SystemCallActionType.restore,
        serverCallId: fixture.repository.session.id,
        systemCallId: 'stale-system-call-id',
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 20));

    expect(fixture.controller.phase, CallPhase.idle);
    expect(
      fixture.systemCalls.endedCallIds,
      contains(fixture.repository.session.id),
    );
  });

  test('主叫在对方接受后获取短期凭证并加入 LiveKit', () async {
    final fixture = _Fixture();
    addTearDown(fixture.dispose);

    await fixture.controller.startCall(
      fixture.conversation,
      CallMediaType.video,
    );
    expect(fixture.controller.phase, CallPhase.outgoing);
    expect(fixture.engine.initialized, isTrue);
    expect(fixture.engine.connectedSession, isNull);

    fixture.repository.acceptRemotely();
    await Future<void>.delayed(const Duration(milliseconds: 10));
    expect(fixture.repository.joinCount, 1);
    expect(fixture.engine.connectedSession?.roomName, 'call-room');
    expect(fixture.controller.phase, CallPhase.connecting);

    fixture.engine.connections.add(CallConnectionState.connected);
    await Future<void>.delayed(Duration.zero);
    expect(fixture.controller.phase, CallPhase.active);
    fixture.repository.acceptRemotely();
    await Future<void>.delayed(Duration.zero);
    expect(fixture.repository.joinCount, 1);
    expect(fixture.controller.phase, CallPhase.active);

    await fixture.controller.toggleMute();
    await fixture.controller.toggleCamera();
    await fixture.controller.toggleSpeaker();
    await fixture.controller.toggleScreenShare();
    expect(fixture.engine.muted, isTrue);
    expect(fixture.engine.cameraEnabled, isFalse);
    expect(fixture.engine.speakerEnabled, isFalse);
    expect(fixture.engine.screenShareEnabled, isTrue);
  });

  test('群聊主叫只在首位成员接听后加入同一个 LiveKit 房间', () async {
    final repository = _FakeCallRepository(incoming: false);
    final engine = _FakeEngine();
    final systemCalls = _FakeSystemCallService();
    final group = Conversation(
      id: 'group-call-conversation',
      title: '邻里产品小组',
      subtitle: '',
      updatedAt: DateTime(2026, 8, 11),
      kind: ConversationKind.group,
      members: const [_Fixture.me, _Fixture.peer],
    );
    final controller = CallController(
      repository: repository,
      currentUser: () => _Fixture.me,
      findConversation: (_) => group,
      engineFactory: () => engine,
      systemCallService: systemCalls,
    );
    addTearDown(() {
      controller.dispose();
      repository.dispose();
    });

    await controller.startCall(group, CallMediaType.video);
    expect(controller.session?.isGroup, isTrue);
    expect(controller.phase, CallPhase.outgoing);
    repository.acceptRemotely();
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(repository.joinCount, 1);
    expect(engine.connectCount, 1);
    expect(systemCalls.outgoingCount, 0);
  });

  test('重复来电事件幂等，接听后只加入一次 LiveKit', () async {
    final fixture = _Fixture(incoming: true);
    addTearDown(fixture.dispose);
    final payload = {'call': fixture.repository.sessionJson};

    fixture.repository.emit('call.invited', payload);
    fixture.repository.emit('call.invited', payload);
    await Future<void>.delayed(Duration.zero);

    expect(fixture.controller.phase, CallPhase.incoming);
    await fixture.controller.accept();

    expect(fixture.repository.acceptCount, 1);
    expect(fixture.repository.joinCount, 1);
    expect(fixture.engine.connectCount, 1);
  });

  test('系统接听动作桥接到 LiveKit CallController', () async {
    final fixture = _Fixture(incoming: true);
    addTearDown(fixture.dispose);
    fixture.repository.emit('call.invited', {
      'call': fixture.repository.sessionJson,
    });
    await Future<void>.delayed(Duration.zero);

    expect(fixture.systemCalls.incomingCount, 1);
    fixture.systemCalls.emit(
      SystemCallAction(
        type: SystemCallActionType.accept,
        serverCallId: fixture.repository.session.id,
        systemCallId: 'system-call-id',
      ),
    );
    await Future<void>.delayed(const Duration(milliseconds: 20));

    expect(fixture.repository.acceptCount, 1);
    expect(fixture.engine.connectCount, 1);
  });

  test('浏览器拒绝媒体权限时保留可操作的明确失败状态', () async {
    final fixture = _Fixture(
      engineError: PlatformException(
        code: 'NotAllowedError',
        message: 'Permission denied',
      ),
    );
    addTearDown(fixture.dispose);

    await expectLater(
      fixture.controller.startCall(fixture.conversation, CallMediaType.video),
      throwsA(isA<PlatformException>()),
    );

    expect(fixture.controller.phase, CallPhase.failed);
    expect(fixture.controller.isVideo, isTrue);
    expect(fixture.controller.conversation?.id, fixture.conversation.id);
    expect(fixture.controller.errorMessage, contains('摄像头和麦克风权限'));
    expect(fixture.controller.errorMessage, contains('浏览器或系统设置'));

    await fixture.controller.dismissFailure();
    expect(fixture.controller.phase, CallPhase.idle);
  });

  test('没有采集设备时给出连接设备指引', () {
    final message = readableCallMediaError(
      Exception('NotFoundError: Requested device not found'),
      mediaType: CallMediaType.audio,
      fallback: '无法发起通话',
    );
    expect(message, '未检测到可用的麦克风，请连接设备后重试');
  });

  test('LiveKit 连接失败时结束服务端通话并显示媒体诊断', () async {
    final fixture = _Fixture();
    addTearDown(fixture.dispose);
    await fixture.controller.startCall(
      fixture.conversation,
      CallMediaType.audio,
    );
    fixture.repository.acceptRemotely();
    await Future<void>.delayed(const Duration(milliseconds: 10));

    fixture.engine.connections.add(CallConnectionState.failed);
    await Future<void>.delayed(const Duration(milliseconds: 30));

    expect(fixture.controller.phase, CallPhase.failed);
    expect(fixture.controller.errorMessage, contains('LiveKit'));
    expect(fixture.repository.lastHangupReason, 'media_failed');
  });

  testWidgets('来电全屏展示联系人并提供拒绝和接听操作', (tester) async {
    tester.view.physicalSize = const Size(1024, 768);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final fixture = _Fixture(incoming: true);
    addTearDown(fixture.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: CallUiHost(
          controller: fixture.controller,
          child: const Scaffold(body: Text('消息首页')),
        ),
      ),
    );

    fixture.repository.emit('call.invited', {
      'call': fixture.repository.sessionJson,
    });
    await tester.pump();

    expect(find.byKey(const Key('call-screen')), findsOneWidget);
    expect(find.text('林笙'), findsWidgets);
    expect(find.byKey(const Key('reject-call')), findsOneWidget);
    expect(find.byKey(const Key('accept-call')), findsOneWidget);
    expect(
      tester.getSize(find.byKey(const Key('accept-call'))).height,
      greaterThanOrEqualTo(44),
    );
    await tester.tap(find.byKey(const Key('reject-call')));
    await tester.pump(const Duration(milliseconds: 700));
  });

  testWidgets('通话失败面板适配小屏深色和 200% 字体', (tester) async {
    tester.view.physicalSize = const Size(320, 568);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final fixture = _Fixture(
      engineError: PlatformException(
        code: 'NotAllowedError',
        message: 'Permission denied',
      ),
    );
    addTearDown(fixture.dispose);
    await tester.pumpWidget(
      MediaQuery(
        data: const MediaQueryData(textScaler: TextScaler.linear(2)),
        child: MaterialApp(
          theme: ThemeData.dark(),
          home: CallUiHost(
            controller: fixture.controller,
            child: const Scaffold(body: Text('消息首页')),
          ),
        ),
      ),
    );

    Object? startError;
    await tester.runAsync(() async {
      try {
        await fixture.controller.startCall(
          fixture.conversation,
          CallMediaType.video,
        );
      } catch (error) {
        startError = error;
      }
    });
    expect(startError, isA<PlatformException>());
    await tester.pump();

    expect(find.byKey(const Key('call-failure-message')), findsOneWidget);
    expect(find.textContaining('摄像头和麦克风权限'), findsOneWidget);
    final close = find.byKey(const Key('close-call-failure'));
    expect(tester.getSize(close).height, greaterThanOrEqualTo(44));
    expect(tester.takeException(), isNull);

    await tester.tap(close);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('call-screen')), findsNothing);
  });

  testWidgets('单聊和普通群聊都显示语音视频入口', (tester) async {
    final appController = AppController(DemoImRepository());
    addTearDown(appController.dispose);
    final direct = Conversation(
      id: 'conversation-1',
      title: '林笙',
      subtitle: '',
      updatedAt: DateTime(2026, 7, 31),
      kind: ConversationKind.direct,
      members: const [_Fixture.me, _Fixture.peer],
    );
    await tester.pumpWidget(
      MaterialApp(
        home: ChatScreen(controller: appController, conversation: direct),
      ),
    );
    await tester.pump();
    expect(find.byKey(const Key('start-audio-call')), findsOneWidget);
    expect(find.byKey(const Key('start-video-call')), findsOneWidget);

    final group = Conversation(
      id: 'group-1',
      title: '邻里产品小组',
      subtitle: '',
      updatedAt: DateTime(2026, 7, 31),
      kind: ConversationKind.group,
      members: const [_Fixture.me, _Fixture.peer],
    );
    await tester.pumpWidget(
      MaterialApp(
        home: ChatScreen(controller: appController, conversation: group),
      ),
    );
    await tester.pump();
    expect(find.byKey(const Key('start-audio-call')), findsOneWidget);
    expect(find.byKey(const Key('start-video-call')), findsOneWidget);
  });
}

class _Fixture {
  _Fixture({bool incoming = false, Object? engineError}) {
    repository = _FakeCallRepository(incoming: incoming);
    engine = _FakeEngine(initializeError: engineError);
    systemCalls = _FakeSystemCallService();
    controller = CallController(
      repository: repository,
      currentUser: () => me,
      findConversation: (_) => conversation,
      engineFactory: () => engine,
      systemCallService: systemCalls,
    );
  }

  static const me = AppUser(
    id: 'me',
    name: '许言',
    handle: 'xuyan',
    presence: '',
  );
  static const peer = AppUser(
    id: 'peer',
    name: '林笙',
    handle: 'linyu',
    presence: '',
  );
  final conversation = Conversation(
    id: 'conversation-1',
    title: '林笙',
    subtitle: '',
    updatedAt: DateTime(2026, 7, 31),
    kind: ConversationKind.direct,
    members: const [me, peer],
  );
  late final _FakeCallRepository repository;
  late final _FakeEngine engine;
  late final _FakeSystemCallService systemCalls;
  late final CallController controller;
  void dispose() {
    controller.dispose();
    repository.dispose();
  }
}

class _FakeSystemCallService implements SystemCallService {
  final actionController = StreamController<SystemCallAction>.broadcast();
  int incomingCount = 0;
  int outgoingCount = 0;
  final endedCallIds = <String>[];

  @override
  Stream<SystemCallAction> get actions => actionController.stream;
  void emit(SystemCallAction action) => actionController.add(action);
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
  }) async {
    incomingCount++;
    return true;
  }

  @override
  Future<void> showOutgoing({
    required CallSession session,
    required String calleeName,
    String? calleeHandle,
    String? avatarUrl,
  }) async {
    outgoingCount++;
  }

  @override
  Future<void> setConnected(String serverCallId) async {}
  @override
  Future<void> setMuted(String serverCallId, bool muted) async {}
  @override
  Future<void> end(String serverCallId) async => endedCallIds.add(serverCallId);
  @override
  Future<String?> voipPushToken() async => null;
  @override
  Future<void> dispose() => actionController.close();
}

class _FakeCallRepository implements CallRepository {
  _FakeCallRepository({required bool incoming})
    : session = _session(incoming: incoming);

  final events = StreamController<CallSignalEvent>.broadcast();
  CallSession session;
  int acceptCount = 0;
  int joinCount = 0;
  String? lastHangupReason;

  Map<String, Object?> get sessionJson => {
    'id': session.id,
    'conversationId': session.conversationId,
    'kind': session.kind,
    'callerId': session.callerId,
    'calleeId': session.calleeId,
    'participantIds': session.participantIds,
    'joinedUserIds': session.joinedUserIds,
    'declinedUserIds': session.declinedUserIds,
    'leftUserIds': session.leftUserIds,
    'mediaType': session.mediaType.name,
    'status': session.status,
    'invitedAt': session.invitedAt.toUtc().toIso8601String(),
    'expiresAt': session.expiresAt.toUtc().toIso8601String(),
    if (session.acceptedAt != null)
      'acceptedAt': session.acceptedAt!.toUtc().toIso8601String(),
  };

  @override
  Stream<CallSignalEvent> get callEvents => events.stream;

  void emit(String type, Map<String, Object?> payload) =>
      events.add(CallSignalEvent(type: type, payload: payload));

  void acceptRemotely() {
    session = _accepted(session);
    emit('call.accepted', {'call': sessionJson, 'callId': session.id});
  }

  void markMissed() {
    final source = session;
    session = CallSession(
      id: source.id,
      conversationId: source.conversationId,
      kind: source.kind,
      callerId: source.callerId,
      calleeId: source.calleeId,
      participantIds: source.participantIds,
      joinedUserIds: source.joinedUserIds,
      declinedUserIds: source.declinedUserIds,
      leftUserIds: source.leftUserIds,
      mediaType: source.mediaType,
      status: 'missed',
      invitedAt: source.invitedAt,
      expiresAt: source.expiresAt,
      endReason: 'timeout',
      endedAt: DateTime.now(),
    );
  }

  @override
  Future<CallConfiguration> callConfiguration() async =>
      const CallConfiguration(
        provider: 'livekit',
        url: 'wss://livekit.example.test/rtc',
        inviteTimeout: Duration(seconds: 30),
        tokenTtl: Duration(minutes: 5),
        maxParticipants: 9,
        supportsScreenShare: true,
      );

  @override
  Future<CallSession> inviteCall({
    required String callId,
    required String conversationId,
    String? calleeUserId,
    required CallMediaType mediaType,
  }) async => session = CallSession(
    id: callId,
    conversationId: conversationId,
    kind: calleeUserId == null ? 'group' : 'direct',
    callerId: 'me',
    calleeId: calleeUserId ?? '',
    participantIds: calleeUserId == null
        ? const ['me', 'peer']
        : ['me', calleeUserId],
    joinedUserIds: const ['me'],
    mediaType: mediaType,
    status: 'invited',
    invitedAt: DateTime.now(),
    expiresAt: DateTime.now().add(const Duration(seconds: 30)),
  );

  @override
  Future<CallSession> getCall(String callId) async => session;

  @override
  Future<CallSession> acceptCall(String callId) async {
    acceptCount++;
    session = _accepted(session);
    return session;
  }

  @override
  Future<CallSession> rejectCall(String callId, {String reason = ''}) async =>
      session;

  @override
  Future<CallSession> cancelCall(String callId, {String reason = ''}) async =>
      session;

  @override
  Future<CallSession> hangupCall(
    String callId, {
    String reason = 'completed',
  }) async {
    lastHangupReason = reason;
    return session;
  }

  @override
  Future<CallMediaSession> joinCall(String callId) async {
    joinCount++;
    return CallMediaSession(
      url: 'wss://livekit.example.test/rtc',
      roomName: 'call-room',
      token: 'short-lived-token',
      expiresAt: DateTime.now().add(const Duration(minutes: 5)),
    );
  }

  void dispose() => events.close();

  static CallSession _session({required bool incoming}) {
    final now = DateTime.now();
    return CallSession(
      id: 'call-test',
      conversationId: 'conversation-1',
      callerId: incoming ? 'peer' : 'me',
      calleeId: incoming ? 'me' : 'peer',
      mediaType: CallMediaType.video,
      status: 'invited',
      invitedAt: now,
      expiresAt: now.add(const Duration(seconds: 30)),
    );
  }

  static CallSession _accepted(CallSession source) => CallSession(
    id: source.id,
    conversationId: source.conversationId,
    kind: source.kind,
    callerId: source.callerId,
    calleeId: source.calleeId,
    participantIds: source.participantIds,
    joinedUserIds: {
      ...source.joinedUserIds,
      if (source.isGroup) 'peer',
    }.toList(),
    declinedUserIds: source.declinedUserIds,
    leftUserIds: source.leftUserIds,
    mediaType: source.mediaType,
    status: 'accepted',
    invitedAt: source.invitedAt,
    expiresAt: source.expiresAt,
    acceptedAt: DateTime.now(),
  );
}

class _FakeEngine implements CallMediaEngine {
  _FakeEngine({this.initializeError});

  final Object? initializeError;
  final connections = StreamController<CallConnectionState>.broadcast();
  final media = StreamController<void>.broadcast();
  bool initialized = false;
  bool muted = false;
  bool cameraEnabled = true;
  bool speakerEnabled = true;
  @override
  bool screenShareEnabled = false;
  int connectCount = 0;
  CallMediaSession? connectedSession;

  @override
  Stream<CallConnectionState> get connectionChanges => connections.stream;
  @override
  Stream<void> get mediaChanges => media.stream;
  @override
  VideoTrack? get localVideoTrack => null;
  @override
  List<CallRemoteVideo> get remoteVideos => const [];
  @override
  List<String> get activeSpeakerIds => const [];
  @override
  int get participantCount => connectedSession == null ? 1 : 2;

  @override
  Future<void> initialize({
    required CallConfiguration configuration,
    required CallMediaType mediaType,
  }) async {
    if (initializeError case final error?) throw error;
    initialized = true;
  }

  @override
  Future<void> connect(CallMediaSession session) async {
    connectCount++;
    connectedSession = session;
  }

  @override
  Future<void> setMuted(bool value) async => muted = value;
  @override
  Future<void> setCameraEnabled(bool value) async => cameraEnabled = value;
  @override
  Future<void> setSpeakerEnabled(bool value) async => speakerEnabled = value;
  @override
  Future<void> setScreenShareEnabled(bool value) async {
    screenShareEnabled = value;
    media.add(null);
  }

  @override
  Future<void> switchCamera() async {}
  @override
  Future<void> dispose() async {
    await connections.close();
    await media.close();
  }
}
