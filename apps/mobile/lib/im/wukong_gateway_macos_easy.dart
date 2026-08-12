import 'dart:async';
import 'dart:convert';

import 'package:wukong_easy_sdk/wukong_easy_sdk.dart' as easy;

import 'wukong_gateway_contract.dart';

/// macOS transport for the pinned WuKong Easy SDK. The Easy SDK intentionally
/// owns only the live JSON-RPC connection; durable history, extras, reminders
/// and encrypted local retention remain behind our data-source/cache boundary.
class MacOSWukongGateway implements WukongGateway {
  factory MacOSWukongGateway({WukongDataSource? dataSource}) =>
      MacOSWukongGateway._(dataSource);

  MacOSWukongGateway._(this._dataSource);

  final WukongDataSource? _dataSource;
  final easy.WuKongEasySDK _sdk = easy.WuKongEasySDK.getInstance();
  final _states = StreamController<WukongConnectionState>.broadcast();
  final _events = StreamController<WukongGatewayEvent>.broadcast();
  final _sendResults = StreamController<WukongSendResult>.broadcast();

  WukongConnectionState _state = WukongConnectionState.disconnected;
  WukongSession? _session;
  bool _disposed = false;

  @override
  Stream<WukongConnectionState> get connectionStates => _states.stream;

  @override
  Stream<WukongGatewayEvent> get events => _events.stream;

  @override
  Stream<WukongSendResult> get sendResults => _sendResults.stream;

  @override
  WukongConnectionState get connectionState => _state;

  @override
  WukongSession? get session => _session;

  @override
  Future<void> initialize(WukongSession session) async {
    _checkNotDisposed();
    session.validate();
    if (session.sdk != 'wukong_easy_sdk' || session.deviceFlag != 2) {
      throw const FormatException(
        'macOS requires a WuKong Easy SDK desktop session',
      );
    }
    if (_sdk.isInitialized) _sdk.dispose();
    _session = session;
    await _sdk.init(
      easy.WuKongConfig(
        serverUrl: session.wsUrl,
        uid: session.uid,
        token: session.token,
        deviceId: 'linli-macos-${session.uid}',
        deviceFlag: easy.WuKongDeviceFlag.pc,
      ),
    );
    _registerListeners();
  }

  void _registerListeners() {
    _sdk
      ..addEventListener<easy.ConnectResult>(
        easy.WuKongEvent.connect,
        _onConnect,
      )
      ..addEventListener<easy.DisconnectInfo>(
        easy.WuKongEvent.disconnect,
        _onDisconnect,
      )
      ..addEventListener<easy.ReconnectingInfo>(
        easy.WuKongEvent.reconnecting,
        _onReconnecting,
      )
      ..addEventListener<easy.WuKongError>(easy.WuKongEvent.error, _onError)
      ..addEventListener<easy.Message>(easy.WuKongEvent.message, _onMessage)
      ..addEventListener<easy.EventNotification>(
        easy.WuKongEvent.customEvent,
        _onCustomEvent,
      );
  }

  @override
  Future<void> connect() async {
    _checkNotDisposed();
    if (_session == null || !_sdk.isInitialized) {
      throw StateError('initialize must be called first');
    }
    if (_state == WukongConnectionState.connected) return;
    _setState(WukongConnectionState.connecting);
    try {
      await _sdk.connect().timeout(const Duration(seconds: 10));
      _setState(WukongConnectionState.connected);
      unawaited(_refreshBusinessState());
    } catch (_) {
      _setState(WukongConnectionState.disconnected);
      rethrow;
    }
  }

  @override
  Future<void> disconnect({bool logout = false}) async {
    if (_sdk.isInitialized) _sdk.disconnect();
    _setState(WukongConnectionState.disconnected);
  }

  @override
  Future<WukongMessage> send(WukongOutgoingMessage outgoing) async {
    _checkNotDisposed();
    if (_state != WukongConnectionState.connected) {
      throw StateError('WuKongIM is not connected');
    }
    final clientMsgNo = outgoing.clientMsgNo?.trim().isNotEmpty == true
        ? outgoing.clientMsgNo!.trim()
        : 'mac-${DateTime.now().microsecondsSinceEpoch.toRadixString(36)}';
    final result = await _sdk.send(
      channelId: outgoing.channel.id,
      channelType: easy.WuKongChannelType.fromValue(outgoing.channel.type),
      payload: outgoing.payload,
      clientMsgNo: clientMsgNo,
      header: {
        'noPersist': outgoing.noPersist,
        'redDot': outgoing.redDot,
        'syncOnce': outgoing.syncOnce,
      },
      topic: outgoing.topic,
      setting: outgoing.topic?.trim().isNotEmpty == true
          ? const <String, Object?>{'topic': true}
          : null,
    );
    final reasonCode = result.reasonCode.value;
    final mapped = WukongMessage(
      messageId: result.messageId,
      messageSeq: result.messageSeq,
      clientMsgNo: clientMsgNo,
      clientSeq: 0,
      fromUid: _session!.uid,
      channel: outgoing.channel,
      timestamp: DateTime.now().toUtc(),
      payload: outgoing.payload,
      state: reasonCode == 1
          ? WukongMessageState.sent
          : WukongMessageState.failed,
      reasonCode: reasonCode,
    );
    scheduleMicrotask(() {
      if (_disposed) return;
      _sendResults.add(
        WukongSendResult(
          clientMsgNo: clientMsgNo,
          messageId: result.messageId,
          messageSeq: result.messageSeq,
          reasonCode: reasonCode,
        ),
      );
    });
    return mapped;
  }

  @override
  Future<void> markRead(WukongChannel channel) async {
    final source = _dataSource;
    if (source == null) return;
    final reminders = await source.syncReminders(version: 0, limit: 500);
    final ids = reminders
        .where(
          (item) =>
              item['channel_id'] == channel.id &&
              (item['channel_type'] as num?)?.toInt() == channel.type &&
              item['done'] != true,
        )
        .map((item) => (item['reminder_id'] as num?)?.toInt())
        .whereType<int>()
        .toList();
    if (ids.isNotEmpty) await source.doneReminders(ids);
  }

  void _onConnect(easy.ConnectResult result) {
    if (_disposed) return;
    if (result.reasonCode == 1) {
      _setState(WukongConnectionState.connected);
      unawaited(_refreshBusinessState());
    }
  }

  void _onDisconnect(easy.DisconnectInfo info) {
    if (_disposed) return;
    _setState(
      info.code == 12
          ? WukongConnectionState.kicked
          : WukongConnectionState.disconnected,
    );
  }

  void _onReconnecting(easy.ReconnectingInfo info) {
    if (!_disposed) _setState(WukongConnectionState.connecting);
  }

  void _onError(easy.WuKongError error) {
    if (!_disposed && _state != WukongConnectionState.connecting) {
      _setState(WukongConnectionState.networkUnavailable);
    }
  }

  void _onMessage(easy.Message message) {
    if (_disposed) return;
    final mapped = mapWukongEasyMessage(
      message,
      sessionUid: _session?.uid ?? '',
    );
    if (mapped.contentType == 99) {
      _events.add(
        WukongGatewayEvent(
          kind: WukongGatewayEventKind.command,
          data: mapped.payload,
        ),
      );
      unawaited(_refreshBusinessState());
      return;
    }
    _events.add(
      WukongGatewayEvent(
        kind: WukongGatewayEventKind.received,
        message: mapped,
        channel: mapped.channel,
      ),
    );
  }

  void _onCustomEvent(easy.EventNotification event) {
    if (_disposed) return;
    _events.add(
      WukongGatewayEvent(
        kind: WukongGatewayEventKind.messageEvent,
        data: {
          'id': event.id,
          'type': event.type,
          'timestamp': event.timestamp,
          'data': decodeWukongEasyPayload(event.data),
        },
      ),
    );
  }

  Future<void> _refreshBusinessState() async {
    final source = _dataSource;
    if (source == null || _disposed) return;
    try {
      await source.syncConversations(
        version: 0,
        lastMsgSeqs: '',
        messageCount: 1,
      );
      await source.syncReminders(version: 0, limit: 500);
      if (!_disposed) {
        _events.add(
          const WukongGatewayEvent(
            kind: WukongGatewayEventKind.conversationChanged,
          ),
        );
      }
    } catch (_) {
      // The live socket remains usable. Repository requests will retry the
      // authoritative business sync on the next refresh or reconnect.
    }
  }

  void _setState(WukongConnectionState value) {
    if (_disposed || _state == value) return;
    _state = value;
    _states.add(value);
  }

  void _checkNotDisposed() {
    if (_disposed) throw StateError('WuKong gateway is disposed');
  }

  @override
  Future<void> dispose() async {
    if (_disposed) return;
    if (_sdk.isInitialized) _sdk.dispose();
    _disposed = true;
    await _states.close();
    await _events.close();
    await _sendResults.close();
  }
}

WukongMessage mapWukongEasyMessage(
  easy.Message message, {
  required String sessionUid,
}) {
  var channelId = message.channelId;
  if (message.channelType.value == 1 && channelId == sessionUid) {
    channelId = message.fromUid;
  }
  return WukongMessage(
    messageId: message.messageId,
    messageSeq: message.messageSeq,
    clientMsgNo: message.clientMsgNo ?? '',
    clientSeq: 0,
    fromUid: message.fromUid,
    channel: WukongChannel(id: channelId, type: message.channelType.value),
    timestamp: _easyTimestamp(message.timestamp),
    payload: decodeWukongEasyPayload(message.payload),
    state: WukongMessageState.sent,
    reasonCode: 1,
    streamNo: message.streamNo ?? message.streamId ?? '',
    streamFlag: message.streamFlag ?? 0,
  );
}

Map<String, Object?> decodeWukongEasyPayload(Object? value) {
  Object? decoded = value;
  if (decoded is String) {
    try {
      decoded = utf8.decode(base64Decode(decoded));
    } catch (_) {
      // Event data is a plain JSON string while message payload is Base64.
    }
    if (decoded is String) {
      try {
        decoded = jsonDecode(decoded);
      } catch (_) {
        return {'value': decoded};
      }
    }
  }
  return wukongObjectMap(decoded);
}

DateTime _easyTimestamp(int value) => DateTime.fromMillisecondsSinceEpoch(
  value < 1000000000000 ? value * 1000 : value,
  isUtc: true,
);
