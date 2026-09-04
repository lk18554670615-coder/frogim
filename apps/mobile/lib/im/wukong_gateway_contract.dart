import 'dart:async';

enum WukongConnectionState {
  disconnected,
  connecting,
  connected,
  syncing,
  kicked,
  networkUnavailable,
}

enum WukongMessageState { sending, sent, failed }

enum WukongGatewayEventKind {
  inserted,
  received,
  refreshed,
  command,
  conversationChanged,
  messageEvent,
  custom,
}

class WukongSession {
  const WukongSession({
    required this.uid,
    required this.token,
    required this.deviceFlag,
    required this.deviceLevel,
    required this.tcpUrl,
    required this.wsUrl,
    required this.sdk,
    required this.issuedAt,
  });

  final String uid;
  final String token;
  final int deviceFlag;
  final int deviceLevel;
  final String tcpUrl;
  final String wsUrl;
  final String sdk;
  final DateTime issuedAt;

  factory WukongSession.fromJson(Map<String, Object?> json) {
    final session = WukongSession(
      uid: json['uid'] as String? ?? '',
      token: json['token'] as String? ?? '',
      deviceFlag: (json['deviceFlag'] as num?)?.toInt() ?? -1,
      deviceLevel: (json['deviceLevel'] as num?)?.toInt() ?? -1,
      tcpUrl: json['tcpUrl'] as String? ?? '',
      wsUrl: json['wsUrl'] as String? ?? '',
      sdk: json['sdk'] as String? ?? '',
      issuedAt:
          DateTime.tryParse(json['issuedAt'] as String? ?? '')?.toUtc() ??
          DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
    );
    session.validate();
    return session;
  }

  Map<String, Object?> toJson() => {
    'uid': uid,
    'token': token,
    'deviceFlag': deviceFlag,
    'deviceLevel': deviceLevel,
    'tcpUrl': tcpUrl,
    'wsUrl': wsUrl,
    'sdk': sdk,
    'issuedAt': issuedAt.toUtc().toIso8601String(),
  };

  String get tcpAddress {
    final uri = Uri.tryParse(tcpUrl);
    if (uri != null && uri.scheme == 'tcp' && uri.host.isNotEmpty) {
      return '${uri.host}:${uri.port}';
    }
    return tcpUrl;
  }

  void validate() {
    if (uid.trim().isEmpty || token.trim().isEmpty) {
      throw const FormatException('WuKongIM session requires uid and token');
    }
    if (deviceFlag < 0 || deviceLevel < 0) {
      throw const FormatException('WuKongIM session has invalid device data');
    }
    final wsUri = Uri.tryParse(wsUrl);
    if (wsUri == null ||
        !const {'ws', 'wss'}.contains(wsUri.scheme) ||
        wsUri.host.isEmpty) {
      throw const FormatException('WuKongIM session has an invalid WS URL');
    }
    if (tcpAddress.isEmpty || !tcpAddress.contains(':')) {
      throw const FormatException('WuKongIM session has an invalid TCP URL');
    }
  }
}

class WukongChannel {
  const WukongChannel({required this.id, required this.type});

  final String id;
  final int type;

  String get key => '$id@$type';
}

class WukongOutgoingMessage {
  const WukongOutgoingMessage({
    required this.channel,
    required this.payload,
    this.clientMsgNo,
    this.noPersist = false,
    this.redDot = true,
    this.syncOnce = false,
    this.topic,
    this.expireSeconds = 0,
  });

  final WukongChannel channel;
  final Map<String, Object?> payload;
  final String? clientMsgNo;
  final bool noPersist;
  final bool redDot;
  final bool syncOnce;
  final String? topic;
  final int expireSeconds;
}

class WukongMessage {
  const WukongMessage({
    required this.messageId,
    required this.messageSeq,
    required this.clientMsgNo,
    required this.clientSeq,
    required this.fromUid,
    required this.channel,
    required this.timestamp,
    required this.payload,
    required this.state,
    this.reasonCode = 0,
    this.streamNo = '',
    this.streamSeq = 0,
    this.streamFlag = 0,
    this.streamEventSeq = 0,
    this.isStreaming = false,
    this.streamCompleted = false,
    this.streamContentInitialized = false,
  });

  final String messageId;
  final int messageSeq;
  final String clientMsgNo;
  final int clientSeq;
  final String fromUid;
  final WukongChannel channel;
  final DateTime timestamp;
  final Map<String, Object?> payload;
  final WukongMessageState state;
  final int reasonCode;
  final String streamNo;
  final int streamSeq;
  final int streamFlag;
  final int streamEventSeq;
  final bool isStreaming;
  final bool streamCompleted;
  final bool streamContentInitialized;

  int get contentType => (payload['type'] as num?)?.toInt() ?? -1;

  WukongMessage copyWith({
    String? messageId,
    int? messageSeq,
    Map<String, Object?>? payload,
    WukongMessageState? state,
    int? reasonCode,
    int? streamEventSeq,
    bool? isStreaming,
    bool? streamCompleted,
    bool? streamContentInitialized,
  }) => WukongMessage(
    messageId: messageId ?? this.messageId,
    messageSeq: messageSeq ?? this.messageSeq,
    clientMsgNo: clientMsgNo,
    clientSeq: clientSeq,
    fromUid: fromUid,
    channel: channel,
    timestamp: timestamp,
    payload: payload ?? this.payload,
    state: state ?? this.state,
    reasonCode: reasonCode ?? this.reasonCode,
    streamNo: streamNo,
    streamSeq: streamSeq,
    streamFlag: streamFlag,
    streamEventSeq: streamEventSeq ?? this.streamEventSeq,
    isStreaming: isStreaming ?? this.isStreaming,
    streamCompleted: streamCompleted ?? this.streamCompleted,
    streamContentInitialized:
        streamContentInitialized ?? this.streamContentInitialized,
  );

  Map<String, Object?> toJson() => {
    'messageId': messageId,
    'messageSeq': messageSeq,
    'clientMsgNo': clientMsgNo,
    'clientSeq': clientSeq,
    'fromUid': fromUid,
    'channelId': channel.id,
    'channelType': channel.type,
    'timestamp': timestamp.toUtc().toIso8601String(),
    'payload': payload,
    'state': state.name,
    'reasonCode': reasonCode,
    'streamNo': streamNo,
    'streamSeq': streamSeq,
    'streamFlag': streamFlag,
    'streamEventSeq': streamEventSeq,
    'isStreaming': isStreaming,
    'streamCompleted': streamCompleted,
    'streamContentInitialized': streamContentInitialized,
  };

  factory WukongMessage.fromJson(Map<String, Object?> json) => WukongMessage(
    messageId: json['messageId'] as String? ?? '',
    messageSeq: (json['messageSeq'] as num?)?.toInt() ?? 0,
    clientMsgNo: json['clientMsgNo'] as String? ?? '',
    clientSeq: (json['clientSeq'] as num?)?.toInt() ?? 0,
    fromUid: json['fromUid'] as String? ?? '',
    channel: WukongChannel(
      id: json['channelId'] as String? ?? '',
      type: (json['channelType'] as num?)?.toInt() ?? 0,
    ),
    timestamp:
        DateTime.tryParse(json['timestamp'] as String? ?? '')?.toUtc() ??
        DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
    payload: _objectMap(json['payload']),
    state: WukongMessageState.values.firstWhere(
      (value) => value.name == json['state'],
      orElse: () => WukongMessageState.sent,
    ),
    reasonCode: (json['reasonCode'] as num?)?.toInt() ?? 0,
    streamNo: json['streamNo'] as String? ?? '',
    streamSeq: (json['streamSeq'] as num?)?.toInt() ?? 0,
    streamFlag: (json['streamFlag'] as num?)?.toInt() ?? 0,
    streamEventSeq: (json['streamEventSeq'] as num?)?.toInt() ?? 0,
    isStreaming: json['isStreaming'] as bool? ?? false,
    streamCompleted: json['streamCompleted'] as bool? ?? false,
    streamContentInitialized:
        json['streamContentInitialized'] as bool? ?? false,
  );

  /// Decodes the exact snake_case message object returned by the pinned
  /// WuKongIM `/channel/messagesync` and `/conversation/sync` APIs.
  ///
  /// WuKongIM exposes the 64-bit message id as `message_idstr` for runtimes
  /// (notably JavaScript) that cannot represent every uint64 exactly.  Always
  /// prefer that field and only fall back to the numeric representation.
  factory WukongMessage.fromSyncJson(Map<String, Object?> json) {
    final rawTimestamp = json['timestamp'] ?? json['time'];
    final timestamp = switch (rawTimestamp) {
      final num value => DateTime.fromMillisecondsSinceEpoch(
        value.toInt() < 1000000000000 ? value.toInt() * 1000 : value.toInt(),
        isUtc: true,
      ),
      final String value =>
        DateTime.tryParse(value)?.toUtc() ??
            DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
      _ => DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
    };
    final messageId =
        json['message_idstr']?.toString() ??
        json['messageId']?.toString() ??
        json['message_id']?.toString() ??
        '';
    final projection = _streamProjection(json, _objectMap(json['payload']));
    return WukongMessage(
      messageId: messageId,
      messageSeq:
          (json['message_seq'] as num?)?.toInt() ??
          (json['messageSeq'] as num?)?.toInt() ??
          0,
      clientMsgNo:
          json['client_msg_no'] as String? ??
          json['clientMsgNo'] as String? ??
          '',
      clientSeq:
          (json['client_seq'] as num?)?.toInt() ??
          (json['clientSeq'] as num?)?.toInt() ??
          0,
      fromUid: json['from_uid'] as String? ?? json['fromUid'] as String? ?? '',
      channel: WukongChannel(
        id: json['channel_id'] as String? ?? json['channelId'] as String? ?? '',
        type:
            (json['channel_type'] as num?)?.toInt() ??
            (json['channelType'] as num?)?.toInt() ??
            0,
      ),
      timestamp: timestamp,
      payload: {
        ...projection.payload,
        if (json['is_mutual_deleted'] == 1 || _objectMap(json['message_extra'])['is_mutual_deleted'] == 1) 'is_mutual_deleted': 1,
      },
      state: WukongMessageState.sent,
      reasonCode:
          (json['reason_code'] as num?)?.toInt() ??
          (json['reasonCode'] as num?)?.toInt() ??
          1,
      streamNo:
          json['stream_no'] as String? ?? json['streamNo'] as String? ?? '',
      streamSeq:
          (json['stream_seq'] as num?)?.toInt() ??
          (json['streamSeq'] as num?)?.toInt() ??
          0,
      streamFlag:
          (json['stream_flag'] as num?)?.toInt() ??
          (json['streamFlag'] as num?)?.toInt() ??
          0,
      streamEventSeq: projection.eventSequence,
      isStreaming: projection.streaming,
      streamCompleted: projection.completed,
      streamContentInitialized: projection.contentInitialized,
    );
  }
}

class _StreamProjection {
  const _StreamProjection({
    required this.payload,
    required this.eventSequence,
    required this.streaming,
    required this.completed,
    required this.contentInitialized,
  });

  final Map<String, Object?> payload;
  final int eventSequence;
  final bool streaming;
  final bool completed;
  final bool contentInitialized;
}

_StreamProjection _streamProjection(
  Map<String, Object?> message,
  Map<String, Object?> source,
) {
  final payload = Map<String, Object?>.from(source);
  final meta = _objectMap(message['event_meta'] ?? message['eventMeta']);
  if (meta.isEmpty) {
    final setting = (message['setting'] as num?)?.toInt() ?? 0;
    return _StreamProjection(
      payload: payload,
      eventSequence: 0,
      streaming: setting & 2 != 0,
      completed: false,
      contentInitialized: false,
    );
  }
  final events = meta['events'] as List<Object?>? ?? const [];
  var contentInitialized = false;
  for (final raw in events) {
    final event = _objectMap(raw);
    if (event['event_key'] != 'main' && event['eventKey'] != 'main') continue;
    final snapshot = _objectMap(event['snapshot']);
    if (snapshot['kind'] == 'text' && snapshot['text'] is String) {
      payload['content'] = snapshot['text'];
      contentInitialized = true;
    }
    break;
  }
  return _StreamProjection(
    payload: payload,
    eventSequence:
        (meta['last_msg_event_seq'] as num?)?.toInt() ??
        (meta['lastMsgEventSeq'] as num?)?.toInt() ??
        0,
    streaming:
        ((meta['open_event_count'] as num?)?.toInt() ??
            (meta['openEventCount'] as num?)?.toInt() ??
            0) >
        0,
    completed: meta['completed'] == true,
    contentInitialized: contentInitialized,
  );
}

Map<String, Object?> projectWukongStreamPayload(Map<String, Object?> message) =>
    _streamProjection(message, _objectMap(message['payload'])).payload;

class WukongSendResult {
  const WukongSendResult({
    required this.clientMsgNo,
    required this.messageId,
    required this.messageSeq,
    required this.reasonCode,
    this.clientSeq = 0,
  });

  final String clientMsgNo;
  final String messageId;
  final int messageSeq;
  final int reasonCode;
  final int clientSeq;

  bool get accepted => reasonCode == 1;
}

class WukongGatewayEvent {
  const WukongGatewayEvent({
    required this.kind,
    this.message,
    this.channel,
    this.data = const {},
  });

  final WukongGatewayEventKind kind;
  final WukongMessage? message;
  final WukongChannel? channel;
  final Map<String, Object?> data;
}

abstract interface class WukongDataSource {
  Future<Map<String, Object?>> channelInfo(WukongChannel channel);

  Future<List<Map<String, Object?>>> syncChannelMembers({
    required WukongChannel channel,
    required int version,
    required int limit,
  });

  Future<List<Map<String, Object?>>> syncMessageExtras({
    required WukongChannel channel,
    required int version,
    required int limit,
  });

  Future<List<Map<String, Object?>>> syncReminders({
    required int version,
    required int limit,
  });

  Future<void> doneReminders(List<int> reminderIds);

  Future<List<Map<String, Object?>>> syncConversations({
    required int version,
    required String lastMsgSeqs,
    required int messageCount,
  });

  Future<Map<String, Object?>> syncMessages({
    required WukongChannel channel,
    required int startMessageSeq,
    required int endMessageSeq,
    required int limit,
    required int pullMode,
  });
}

abstract interface class WukongGateway {
  Stream<WukongConnectionState> get connectionStates;
  Stream<WukongGatewayEvent> get events;
  Stream<WukongSendResult> get sendResults;
  WukongConnectionState get connectionState;
  WukongSession? get session;

  Future<void> initialize(WukongSession session);
  Future<void> connect();
  Future<void> disconnect({bool logout = false});
  Future<WukongMessage> send(WukongOutgoingMessage message);
  Future<void> markRead(WukongChannel channel);
  Future<void> dispose();
}

Map<String, Object?> wukongObjectMap(Object? value) => _objectMap(value);

abstract interface class WukongDeletionCache {
  Future<void> markMessagesDeleted(List<String> messageIds);
}

Map<String, Object?> _objectMap(Object? value) {
  if (value is Map<String, Object?>) return value;
  if (value is Map) {
    return value.map((key, item) => MapEntry(key.toString(), item));
  }
  return const {};
}
