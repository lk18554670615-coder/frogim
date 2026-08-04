enum CallMediaType { audio, video }

enum CallPhase { idle, incoming, outgoing, connecting, active, ended, failed }

class CallSession {
  const CallSession({
    required this.id,
    required this.conversationId,
    required this.callerId,
    required this.calleeId,
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
  final String callerId;
  final String calleeId;
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

  factory CallSession.fromJson(Map<String, Object?> json) => CallSession(
    id: json['id']! as String,
    conversationId: json['conversationId']! as String,
    callerId: json['callerId']! as String,
    calleeId: json['calleeId']! as String,
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
}

class CallIceServer {
  const CallIceServer({required this.urls, this.username, this.credential});

  final List<String> urls;
  final String? username;
  final String? credential;

  Map<String, Object> toRtcMap() => {
    'urls': urls,
    'username': ?username,
    'credential': ?credential,
  };

  factory CallIceServer.fromJson(Map<String, Object?> json) {
    final rawUrls = json['urls'];
    return CallIceServer(
      urls: rawUrls is List
          ? rawUrls.map((value) => value.toString()).toList()
          : rawUrls == null
          ? const []
          : [rawUrls.toString()],
      username: json['username'] as String?,
      credential: json['credential'] as String?,
    );
  }
}

class CallConfiguration {
  const CallConfiguration({
    required this.iceServers,
    required this.inviteTimeout,
  });

  final List<CallIceServer> iceServers;
  final Duration inviteTimeout;

  factory CallConfiguration.fromJson(Map<String, Object?> json) =>
      CallConfiguration(
        iceServers: (json['iceServers'] as List<Object?>? ?? const [])
            .whereType<Map<String, Object?>>()
            .map(CallIceServer.fromJson)
            .where((server) => server.urls.isNotEmpty)
            .toList(),
        inviteTimeout: Duration(
          seconds: (json['inviteTimeoutSeconds'] as num?)?.toInt() ?? 30,
        ),
      );
}

class CallSignalEvent {
  const CallSignalEvent({required this.type, required this.payload});

  final String type;
  final Map<String, Object?> payload;
}
