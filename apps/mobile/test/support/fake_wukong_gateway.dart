import 'dart:async';

import 'package:linli_im/im/wukong_gateway_contract.dart';

class FakeWukongGateway implements WukongGateway {
  final _states = StreamController<WukongConnectionState>.broadcast();
  final _events = StreamController<WukongGatewayEvent>.broadcast();
  final _results = StreamController<WukongSendResult>.broadcast();
  final sentMessages = <WukongOutgoingMessage>[];
  final readChannels = <WukongChannel>[];

  WukongConnectionState _state = WukongConnectionState.disconnected;
  WukongSession? _session;
  int _sequence = 0;
  bool disposed = false;
  bool autoAcknowledge = true;
  bool generateClientMsgNo = false;
  WukongMessageState initialSendState = WukongMessageState.sent;
  int initializeCount = 0;
  int logoutDisconnectCount = 0;

  @override
  Stream<WukongConnectionState> get connectionStates => _states.stream;

  @override
  Stream<WukongGatewayEvent> get events => _events.stream;

  @override
  Stream<WukongSendResult> get sendResults => _results.stream;

  @override
  WukongConnectionState get connectionState => _state;

  @override
  WukongSession? get session => _session;

  @override
  Future<void> initialize(WukongSession session) async {
    initializeCount += 1;
    _session = session;
    _setState(WukongConnectionState.disconnected);
  }

  @override
  Future<void> connect() async {
    if (_session == null) throw StateError('initialize must be called first');
    _setState(WukongConnectionState.connecting);
    _setState(WukongConnectionState.connected);
  }

  @override
  Future<void> disconnect({bool logout = false}) async {
    if (logout) {
      logoutDisconnectCount += 1;
      _session = null;
    }
    _setState(WukongConnectionState.disconnected);
  }

  @override
  Future<WukongMessage> send(WukongOutgoingMessage outgoing) async {
    if (_state != WukongConnectionState.connected) {
      throw StateError('fake gateway is disconnected');
    }
    sentMessages.add(outgoing);
    final sequence = ++_sequence;
    final message = WukongMessage(
      messageId: 'wk-message-$sequence',
      messageSeq: sequence,
      clientMsgNo: generateClientMsgNo
          ? 'wk-client-$sequence'
          : outgoing.clientMsgNo ?? 'wk-client-$sequence',
      clientSeq: sequence,
      fromUid: _session!.uid,
      channel: outgoing.channel,
      timestamp: DateTime.utc(2026, 8, 11, 0, 0, sequence),
      payload: outgoing.payload,
      state: initialSendState,
      reasonCode: initialSendState == WukongMessageState.sent ? 1 : 0,
    );
    _events.add(
      WukongGatewayEvent(
        kind: WukongGatewayEventKind.inserted,
        message: message,
        channel: outgoing.channel,
      ),
    );
    if (autoAcknowledge) {
      _results.add(
        WukongSendResult(
          clientSeq: message.clientSeq,
          clientMsgNo: message.clientMsgNo,
          messageId: message.messageId,
          messageSeq: message.messageSeq,
          reasonCode: 1,
        ),
      );
    }
    return message;
  }

  @override
  Future<void> markRead(WukongChannel channel) async {
    readChannels.add(channel);
  }

  void emitSendResult({
    required String clientMsgNo,
    required int clientSeq,
    required String messageId,
    required int messageSeq,
    required int reasonCode,
  }) {
    _results.add(
      WukongSendResult(
        clientSeq: clientSeq,
        clientMsgNo: clientMsgNo,
        messageId: messageId,
        messageSeq: messageSeq,
        reasonCode: reasonCode,
      ),
    );
  }

  void emit(WukongGatewayEvent event) => _events.add(event);

  void setConnectionState(WukongConnectionState state) => _setState(state);

  void _setState(WukongConnectionState state) {
    _state = state;
    _states.add(state);
  }

  @override
  Future<void> dispose() async {
    disposed = true;
    await _states.close();
    await _events.close();
    await _results.close();
  }
}
