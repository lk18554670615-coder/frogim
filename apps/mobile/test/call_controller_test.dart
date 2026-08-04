import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
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

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('拨号、Offer/Answer/ICE 与控制状态形成完整闭环', () async {
    final fixture = _Fixture();
    addTearDown(fixture.dispose);

    await fixture.controller.startCall(
      fixture.conversation,
      CallMediaType.video,
    );
    expect(fixture.controller.phase, CallPhase.outgoing);
    expect(fixture.repository.signals.last.type, 'call.offer');
    expect(fixture.repository.signals.last.payload['sdp'], 'local-offer');

    fixture.engine.candidates.add({
      'candidate': 'candidate:local',
      'sdpMid': '0',
      'sdpMLineIndex': 0,
    });
    await Future<void>.delayed(Duration.zero);
    expect(fixture.repository.signals.last.type, 'call.ice');

    fixture.repository.emit('call.answer', {
      'callId': fixture.repository.session.id,
      'sdp': 'remote-answer',
      'type': 'answer',
    });
    await Future<void>.delayed(Duration.zero);
    expect(fixture.engine.remoteDescriptions.single.$1, 'remote-answer');

    fixture.engine.connections.add(CallConnectionState.connected);
    await Future<void>.delayed(Duration.zero);
    expect(fixture.controller.phase, CallPhase.active);

    await fixture.controller.toggleMute();
    await fixture.controller.toggleCamera();
    await fixture.controller.toggleSpeaker();
    expect(fixture.engine.muted, isTrue);
    expect(fixture.engine.cameraEnabled, isFalse);
    expect(fixture.engine.speakerEnabled, isFalse);
  });

  test('重复来电事件幂等，接听后发送 Answer', () async {
    final fixture = _Fixture(incoming: true);
    addTearDown(fixture.dispose);
    final payload = {'call': fixture.repository.sessionJson};

    fixture.repository.emit('call.invite', payload);
    fixture.repository.emit('call.invite', payload);
    fixture.repository.emit('call.offer', {
      'callId': fixture.repository.session.id,
      'sdp': 'remote-offer',
      'type': 'offer',
    });
    await Future<void>.delayed(Duration.zero);

    expect(fixture.controller.phase, CallPhase.incoming);
    await fixture.controller.accept();

    expect(fixture.repository.acceptCount, 1);
    expect(fixture.repository.signals.last.type, 'call.answer');
    expect(fixture.repository.signals.last.payload['sdp'], 'local-answer');
  });

  test('系统接听动作桥接到现有 CallController', () async {
    final fixture = _Fixture(incoming: true);
    addTearDown(fixture.dispose);
    fixture.repository.emit('call.invite', {
      'call': fixture.repository.sessionJson,
    });
    fixture.repository.emit('call.offer', {
      'callId': fixture.repository.session.id,
      'sdp': 'remote-offer',
      'type': 'offer',
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
    expect(fixture.repository.signals.last.type, 'call.answer');
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

  test('ICE 失败且服务端未提供 TURN 时显示配置级诊断', () async {
    final fixture = _Fixture();
    addTearDown(fixture.dispose);
    await fixture.controller.startCall(
      fixture.conversation,
      CallMediaType.audio,
    );

    fixture.engine.connections.add(CallConnectionState.failed);
    await Future<void>.delayed(const Duration(milliseconds: 20));

    expect(fixture.controller.phase, CallPhase.failed);
    expect(fixture.controller.errorMessage, contains('TURN 中继'));
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

    fixture.repository.emit('call.invite', {
      'call': fixture.repository.sessionJson,
    });
    await tester.pump();

    expect(find.byKey(const Key('call-screen')), findsOneWidget);
    expect(find.text('林屿'), findsWidgets);
    expect(find.byKey(const Key('reject-call')), findsOneWidget);
    expect(find.byKey(const Key('accept-call')), findsOneWidget);
    expect(
      tester.getSize(find.byKey(const Key('accept-call'))).height,
      greaterThanOrEqualTo(44),
    );
    await tester.tap(find.byKey(const Key('reject-call')));
    await tester.pump(const Duration(milliseconds: 700));
  });

  testWidgets('通话失败面板适配小屏深色和 200% 字体且按钮不小于 44pt', (tester) async {
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

    await expectLater(
      fixture.controller.startCall(fixture.conversation, CallMediaType.video),
      throwsA(isA<PlatformException>()),
    );
    await tester.pump();

    expect(find.byKey(const Key('call-failure-message')), findsOneWidget);
    expect(find.textContaining('摄像头和麦克风权限'), findsOneWidget);
    final close = find.byKey(const Key('close-call-failure'));
    expect(tester.getSize(close).height, greaterThanOrEqualTo(44));
    expect(tester.takeException(), isNull);

    await tester.tap(close);
    await tester.pump();
    expect(find.byKey(const Key('call-screen')), findsNothing);
  });

  testWidgets('单聊显示语音视频入口，群聊不显示', (tester) async {
    final appController = AppController(DemoImRepository());
    addTearDown(appController.dispose);
    final direct = Conversation(
      id: 'conversation-1',
      title: '林屿',
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
    expect(find.byKey(const Key('start-audio-call')), findsNothing);
    expect(find.byKey(const Key('start-video-call')), findsNothing);
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
    name: '林屿',
    handle: 'linyu',
    presence: '',
  );
  final conversation = Conversation(
    id: 'conversation-1',
    title: '林屿',
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
  Future<void> dispose() => actionController.close();
}

class _SignalRecord {
  const _SignalRecord(this.type, this.payload);
  final String type;
  final Map<String, Object?> payload;
}

class _FakeCallRepository implements CallRepository {
  _FakeCallRepository({required bool incoming})
    : session = _session(incoming: incoming);

  final events = StreamController<CallSignalEvent>.broadcast();
  final List<_SignalRecord> signals = [];
  CallSession session;
  int acceptCount = 0;

  Map<String, Object?> get sessionJson => {
    'id': session.id,
    'conversationId': session.conversationId,
    'callerId': session.callerId,
    'calleeId': session.calleeId,
    'mediaType': session.mediaType.name,
    'status': session.status,
    'invitedAt': session.invitedAt.toUtc().toIso8601String(),
    'expiresAt': session.expiresAt.toUtc().toIso8601String(),
  };

  @override
  Stream<CallSignalEvent> get callEvents => events.stream;

  void emit(String type, Map<String, Object?> payload) =>
      events.add(CallSignalEvent(type: type, payload: payload));

  @override
  Future<CallConfiguration> callConfiguration() async =>
      const CallConfiguration(
        iceServers: [],
        inviteTimeout: Duration(seconds: 30),
      );

  @override
  Future<CallSession> inviteCall({
    required String callId,
    required String conversationId,
    required String calleeUserId,
    required CallMediaType mediaType,
  }) async => session = CallSession(
    id: callId,
    conversationId: conversationId,
    callerId: 'me',
    calleeId: calleeUserId,
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
  }) async => session;

  @override
  Future<void> sendCallSignal(
    String type,
    Map<String, Object?> payload,
  ) async => signals.add(_SignalRecord(type, payload));

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
}

class _FakeEngine implements CallMediaEngine {
  _FakeEngine({this.initializeError});

  final Object? initializeError;
  final connections = StreamController<CallConnectionState>.broadcast();
  final candidates = StreamController<Map<String, Object?>>.broadcast();
  final remoteDescriptions = <(String, String)>[];
  bool muted = false;
  bool cameraEnabled = true;
  bool speakerEnabled = true;

  @override
  Stream<CallConnectionState> get connectionChanges => connections.stream;
  @override
  Stream<Map<String, Object?>> get localCandidates => candidates.stream;
  @override
  RTCVideoRenderer? get localRenderer => null;
  @override
  RTCVideoRenderer? get remoteRenderer => null;

  @override
  Future<void> initialize({
    required CallConfiguration configuration,
    required CallMediaType mediaType,
  }) async {
    if (initializeError case final error?) throw error;
  }

  @override
  Future<Map<String, String>> createOffer() async => {
    'sdp': 'local-offer',
    'type': 'offer',
  };
  @override
  Future<Map<String, String>> createAnswer() async => {
    'sdp': 'local-answer',
    'type': 'answer',
  };
  @override
  Future<void> setRemoteDescription({
    required String sdp,
    required String type,
  }) async => remoteDescriptions.add((sdp, type));
  @override
  Future<void> addRemoteCandidate(Map<String, Object?> candidate) async {}
  @override
  Future<void> setMuted(bool value) async => muted = value;
  @override
  Future<void> setCameraEnabled(bool value) async => cameraEnabled = value;
  @override
  Future<void> setSpeakerEnabled(bool value) async => speakerEnabled = value;
  @override
  Future<void> switchCamera() async {}
  @override
  Future<void> dispose() async {
    await connections.close();
    await candidates.close();
  }
}
