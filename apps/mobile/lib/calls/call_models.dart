enum CallMediaType { audio, video }

enum CallPhase { idle, incoming, outgoing, connecting, active, ended, failed }

class CallSession {
  const CallSession({
    required this.id,
    required this.conversationId,
    this.kind = 'direct',
    required this.callerId,
    required this.calleeId,
    this.participantIds = const [],
    this.joinedUserIds = const [],
    this.declinedUserIds = const [],
    this.leftUserIds = const [],
    required this.mediaType,
    required this.status,
    required this.invitedAt,
    required this.expiresAt,
    this.endReason,
    this.endedBy,
    this.acceptedAt,
    this.endedAt,
    this.durationSeconds = 0,
  });

  final String id;
  final String conversationId;
  final String kind;
  final String callerId;
  final String calleeId;
  final List<String> participantIds;
  final List<String> joinedUserIds;
  final List<String> declinedUserIds;
  final List<String> leftUserIds;
  final CallMediaType mediaType;
  final String status;
  final String? endReason;
  final String? endedBy;
  final DateTime invitedAt;
  final DateTime expiresAt;
  final DateTime? acceptedAt;
  final DateTime? endedAt;
  final int durationSeconds;

  bool get isTerminal =>
      const {'rejected', 'cancelled', 'missed', 'ended'}.contains(status);
  bool get isGroup => kind == 'group';
  bool includes(String userId) => participantIds.isEmpty
      ? userId == callerId || userId == calleeId
      : participantIds.contains(userId);
  bool hasJoined(String userId) =>
      (joinedUserIds.contains(userId) ||
          (joinedUserIds.isEmpty &&
              status == 'accepted' &&
              includes(userId))) &&
      !declinedUserIds.contains(userId) &&
      !leftUserIds.contains(userId);

  factory CallSession.fromJson(Map<String, Object?> json) => CallSession(
    id: json['id']! as String,
    conversationId: json['conversationId']! as String,
    kind: json['kind'] as String? ?? 'direct',
    callerId: json['callerId']! as String,
    calleeId: json['calleeId'] as String? ?? '',
    participantIds: _strings(
      json['participantIds'],
      fallback: [
        json['callerId']! as String,
        if ((json['calleeId'] as String? ?? '').isNotEmpty)
          json['calleeId']! as String,
      ],
    ),
    joinedUserIds: _strings(
      json['joinedUserIds'],
      fallback: [json['callerId']! as String],
    ),
    declinedUserIds: _strings(json['declinedUserIds']),
    leftUserIds: _strings(json['leftUserIds']),
    mediaType: json['mediaType'] == 'video'
        ? CallMediaType.video
        : CallMediaType.audio,
    status: json['status'] as String? ?? 'invited',
    endReason: json['endReason'] as String?,
    endedBy: json['endedBy'] as String?,
    invitedAt: DateTime.parse(json['invitedAt']! as String).toLocal(),
    expiresAt: DateTime.parse(json['expiresAt']! as String).toLocal(),
    acceptedAt: _date(json['acceptedAt']),
    endedAt: _date(json['endedAt']),
    durationSeconds: (json['durationSeconds'] as num?)?.toInt() ?? 0,
  );

  static DateTime? _date(Object? value) =>
      value is String ? DateTime.tryParse(value)?.toLocal() : null;

  static List<String> _strings(
    Object? value, {
    List<String> fallback = const [],
  }) => value is List
      ? value.whereType<String>().toList(growable: false)
      : List<String>.unmodifiable(fallback);
}

class CallConfiguration {
  const CallConfiguration({
    required this.provider,
    required this.url,
    required this.inviteTimeout,
    required this.tokenTtl,
    required this.maxParticipants,
    required this.supportsScreenShare,
  });

  final String provider;
  final String url;
  final Duration inviteTimeout;
  final Duration tokenTtl;
  final int maxParticipants;
  final bool supportsScreenShare;

  factory CallConfiguration.fromJson(Map<String, Object?> json) {
    final provider = json['provider']?.toString() ?? '';
    final url = json['url']?.toString() ?? '';
    if (provider != 'livekit' ||
        (!url.startsWith('ws://') && !url.startsWith('wss://'))) {
      throw const FormatException('服务端未提供有效的 LiveKit 配置');
    }
    return CallConfiguration(
      provider: provider,
      url: url,
      inviteTimeout: Duration(
        seconds: (json['inviteTimeoutSeconds'] as num?)?.toInt() ?? 30,
      ),
      tokenTtl: Duration(
        seconds: (json['tokenTtlSeconds'] as num?)?.toInt() ?? 300,
      ),
      maxParticipants: (json['maxParticipants'] as num?)?.toInt() ?? 9,
      supportsScreenShare: json['supportsScreenShare'] as bool? ?? false,
    );
  }
}

class CallMediaSession {
  const CallMediaSession({
    required this.url,
    required this.roomName,
    required this.token,
    required this.expiresAt,
  });

  final String url;
  final String roomName;
  final String token;
  final DateTime expiresAt;

  factory CallMediaSession.fromJson(Map<String, Object?> json) {
    final url = json['url']?.toString() ?? '';
    final roomName = json['roomName']?.toString() ?? '';
    final token = json['token']?.toString() ?? '';
    final expiresAt = DateTime.tryParse(json['expiresAt']?.toString() ?? '');
    if ((!url.startsWith('ws://') && !url.startsWith('wss://')) ||
        roomName.isEmpty ||
        token.isEmpty ||
        expiresAt == null) {
      throw const FormatException('LiveKit 入会凭证无效');
    }
    return CallMediaSession(
      url: url,
      roomName: roomName,
      token: token,
      expiresAt: expiresAt.toLocal(),
    );
  }
}

class CallSignalEvent {
  const CallSignalEvent({required this.type, required this.payload});

  final String type;
  final Map<String, Object?> payload;
}
