import 'dart:typed_data';

DateTime parseLocalDateTime(String value) => DateTime.parse(value).toLocal();

DateTime? tryParseLocalDateTime(Object? value) {
  final text = value?.toString() ?? '';
  if (text.isEmpty) return null;
  return DateTime.tryParse(text)?.toLocal();
}

enum MessageStatus { sending, sent, delivered, read, failed, recalled, expired }

enum MessageContentKind {
  text,
  image,
  voice,
  video,
  file,
  reply,
  contact,
  location,
  chatHistory,
  sticker,
  momentShare,
  liveEvent,
  system,
  screenshotNotice,
  unsupported,
}

enum ConversationKind { direct, group }

enum ImEventType {
  groupHistoryChanged,
  sessionExpired,
  messageCreated,
  messageChanged,
  messageRecalled,
  messageDelivered,
  messageRead,
  messageExpired,
  conversationChanged,
  friendChanged,
  groupInvitationChanged,
  announcementChanged,
  scheduledChanged,
  typing,
  unknown,
}

class MessageMention {
  const MessageMention({required this.userId, required this.name});

  final String userId;
  final String name;

  bool get isEveryone => userId == 'all';

  Map<String, Object?> toJson() => {'userId': userId, 'name': name};

  factory MessageMention.fromJson(Map<String, Object?> json) => MessageMention(
    userId: json['userId']! as String,
    name: json['name'] as String? ?? '',
  );
}

class MessageReaction {
  const MessageReaction({
    required this.emoji,
    required this.count,
    required this.reactedByMe,
    this.userIds = const [],
  });

  final String emoji;
  final int count;
  final bool reactedByMe;
  final List<String> userIds;

  Map<String, Object?> toJson() => {
    'emoji': emoji,
    'count': count,
    'reactedByMe': reactedByMe,
    'userIds': userIds,
  };

  factory MessageReaction.fromJson(Map<String, Object?> json) =>
      MessageReaction(
        emoji: json['emoji']! as String,
        count: (json['count'] as num?)?.toInt() ?? 0,
        reactedByMe: json['reactedByMe'] as bool? ?? false,
        userIds: (json['userIds'] as List<Object?>? ?? const [])
            .whereType<String>()
            .toList(),
      );
}

class ChatHistoryEntry {
  const ChatHistoryEntry({
    required this.sourceMessageId,
    required this.senderId,
    required this.summary,
    required this.createdAt,
    this.type = 'text',
  });

  final String sourceMessageId;
  final String senderId;
  final String summary;
  final DateTime createdAt;
  final String type;

  Map<String, Object?> toJson() => {
    'sourceMessageId': sourceMessageId,
    'senderId': senderId,
    'summary': summary,
    'createdAt': createdAt.toUtc().toIso8601String(),
    'type': type,
  };

  factory ChatHistoryEntry.fromJson(Map<String, Object?> json) {
    final rawCreatedAt = json['createdAt'];
    final createdAt = rawCreatedAt is String
        ? DateTime.tryParse(rawCreatedAt)
        : rawCreatedAt is num
        ? DateTime.fromMillisecondsSinceEpoch(rawCreatedAt.toInt(), isUtc: true)
        : null;
    return ChatHistoryEntry(
      sourceMessageId: json['sourceMessageId'] as String? ?? '',
      senderId: json['senderId'] as String? ?? '',
      summary: json['summary'] as String? ?? '[消息]',
      createdAt:
          (createdAt ?? DateTime.fromMillisecondsSinceEpoch(0, isUtc: true))
              .toLocal(),
      type: json['type'] as String? ?? 'text',
    );
  }
}

List<ChatHistoryEntry> chatHistoryEntriesFrom(Object? value) {
  if (value is! List) return const [];
  return value
      .whereType<Map>()
      .map(
        (entry) => ChatHistoryEntry.fromJson(Map<String, Object?>.from(entry)),
      )
      .toList(growable: false);
}

class AppUser {
  const AppUser({
    required this.id,
    required this.name,
    required this.handle,
    required this.presence,
    this.phone,
    this.signature,
    this.gender = 'unspecified',
    this.avatarMediaId,
    this.avatarUrl,
    this.isOnline = false,
    this.remark = '',
    this.tags = const [],
    this.handleChangeCount = 0,
    this.handleChangesRemaining = 0,
    this.allowSearchByHandle = true,
    this.allowSearchByPhone = false,
  });

  final String id;
  final String name;
  final String handle;
  final String presence;
  final String? phone;
  final String? signature;
  final String gender;
  final String? avatarMediaId;
  final String? avatarUrl;
  final bool isOnline;
  final String remark;
  final List<String> tags;

  /// Local presentation only. Keep [name] as the public nickname on the wire.
  String get displayName => remark.trim().isEmpty ? name : remark.trim();
  final int handleChangeCount;
  final int handleChangesRemaining;
  final bool allowSearchByHandle;
  final bool allowSearchByPhone;

  AppUser copyWith({
    String? name,
    String? handle,
    String? presence,
    String? phone,
    String? signature,
    String? gender,
    String? avatarMediaId,
    String? avatarUrl,
    bool? isOnline,
    String? remark,
    List<String>? tags,
    int? handleChangeCount,
    int? handleChangesRemaining,
    bool? allowSearchByHandle,
    bool? allowSearchByPhone,
  }) => AppUser(
    id: id,
    name: name ?? this.name,
    handle: handle ?? this.handle,
    presence: presence ?? this.presence,
    phone: phone ?? this.phone,
    signature: signature ?? this.signature,
    gender: gender ?? this.gender,
    avatarMediaId: avatarMediaId ?? this.avatarMediaId,
    avatarUrl: avatarUrl ?? this.avatarUrl,
    isOnline: isOnline ?? this.isOnline,
    remark: remark ?? this.remark,
    tags: tags ?? this.tags,
    handleChangeCount: handleChangeCount ?? this.handleChangeCount,
    handleChangesRemaining:
        handleChangesRemaining ?? this.handleChangesRemaining,
    allowSearchByHandle: allowSearchByHandle ?? this.allowSearchByHandle,
    allowSearchByPhone: allowSearchByPhone ?? this.allowSearchByPhone,
  );
}

class QrLoginTicket {
  const QrLoginTicket({
    required this.id,
    required this.qrPayload,
    required this.pollToken,
    required this.expiresAt,
  });

  final String id;
  final String qrPayload;
  final String pollToken;
  final DateTime expiresAt;

  bool get expired => !DateTime.now().isBefore(expiresAt);
}

class QrLoginRequest {
  const QrLoginRequest({
    required this.id,
    required this.clientPlatform,
    required this.clientName,
    required this.expiresAt,
  });

  final String id;
  final String clientPlatform;
  final String clientName;
  final DateTime expiresAt;
}

class RobotMenu {
  const RobotMenu({
    required this.robotId,
    required this.command,
    required this.remark,
    this.type = 'command',
  });

  final String robotId;
  final String command;
  final String remark;
  final String type;

  String get label => remark.trim().isEmpty ? command : remark;
}

class RobotProfile {
  const RobotProfile({
    required this.id,
    required this.name,
    required this.username,
    required this.placeholder,
    required this.version,
    required this.menus,
    this.inlineOn = false,
  });

  final String id;
  final String name;
  final String username;
  final String placeholder;
  final int version;
  final bool inlineOn;
  final List<RobotMenu> menus;
}

class UserDevice {
  const UserDevice({
    required this.id,
    required this.platform,
    required this.provider,
    required this.updatedAt,
  });

  final String id;
  final String platform;
  final String provider;
  final DateTime updatedAt;
}

class ImDeviceSession {
  const ImDeviceSession({
    required this.deviceFlag,
    required this.deviceLevel,
    required this.connectionCount,
    required this.updatedAt,
  });

  final int deviceFlag;
  final int deviceLevel;
  final int connectionCount;
  final DateTime updatedAt;

  bool get isOnline => connectionCount > 0;
}

class UserSearchCapabilities {
  const UserSearchCapabilities({
    required this.allowSearchByHandle,
    required this.allowSearchByPhone,
    this.canUpdatePrivacySettings = false,
  });

  final bool allowSearchByHandle;
  final bool allowSearchByPhone;
  final bool canUpdatePrivacySettings;
}

class MessageEditRevision {
  const MessageEditRevision({
    required this.messageId,
    required this.version,
    required this.editorId,
    required this.body,
    required this.editedAt,
  });

  final String messageId;
  final int version;
  final String editorId;
  final Map<String, Object?> body;
  final DateTime editedAt;

  String get text => body['text']?.toString() ?? '';
  bool get isOriginal => version == 0;
}

class ChatMessage {
  const ChatMessage({
    required this.id,
    required this.conversationId,
    required this.senderId,
    required this.senderName,
    required this.text,
    required this.sentAt,
    required this.isMine,
    String? clientMessageId,
    this.conversationSeq = 0,
    this.status = MessageStatus.sent,
    this.sendError,
    this.kind = MessageContentKind.text,
    this.mediaUrl,
    this.mediaId,
    this.mediaWidth,
    this.mediaHeight,
    this.stickerId,
    this.momentId,
    this.event,
    this.robotId,
    this.eventData = const {},
    this.chatHistoryEntries = const [],
    this.fileName,
    this.mimeType,
    this.durationSeconds,
    this.replyToId,
    this.replyToText,
    this.replyToSeq = 0,
    this.replyToSenderId,
    this.replyToSenderName,
    this.contactUserId,
    this.contactName,
    this.contactHandle,
    this.contactAvatarUrl,
    this.latitude,
    this.longitude,
    this.locationName,
    this.locationAddress,
    this.mentions = const [],
    this.reactions = const [],
    this.editedAt,
    this.isPinned = false,
    this.pinnedAt,
    this.pinnedBy,
    this.expiresAt,
    this.deliveredCount = 0,
    this.readCount = 0,
    this.linkPreview,
  }) : clientMessageId = clientMessageId ?? id;

  final String id;
  final String clientMessageId;
  final String conversationId;
  final String senderId;
  final String senderName;
  final String text;
  final DateTime sentAt;
  final bool isMine;
  final int conversationSeq;
  final MessageStatus status;

  /// Local sending feedback; never included in the WuKongIM message payload.
  final String? sendError;
  final MessageContentKind kind;
  final String? mediaUrl;
  final String? mediaId;
  final int? mediaWidth;
  final int? mediaHeight;
  final String? stickerId;
  final String? momentId;
  final String? event;
  final String? robotId;
  final Map<String, Object?> eventData;
  final List<ChatHistoryEntry> chatHistoryEntries;
  final String? fileName;
  final String? mimeType;
  final int? durationSeconds;
  final String? replyToId;
  final String? replyToText;
  final int replyToSeq;
  final String? replyToSenderId;
  final String? replyToSenderName;
  final String? contactUserId;
  final String? contactName;
  final String? contactHandle;
  final String? contactAvatarUrl;
  final double? latitude;
  final double? longitude;
  final String? locationName;
  final String? locationAddress;
  final List<MessageMention> mentions;
  final List<MessageReaction> reactions;
  final DateTime? editedAt;
  final bool isPinned;
  final DateTime? pinnedAt;
  final String? pinnedBy;
  final DateTime? expiresAt;
  final int deliveredCount;
  final int readCount;
  final LinkPreview? linkPreview;

  /// 客户端重试和服务端回执替换消息 ID 时保持不变的界面身份。
  String get stableIdentity => clientMessageId.isEmpty ? id : clientMessageId;

  ChatMessage copyWith({
    String? id,
    String? clientMessageId,
    String? text,
    MessageStatus? status,
    String? sendError,
    int? conversationSeq,
    MessageContentKind? kind,
    String? mediaUrl,
    String? mediaId,
    int? mediaWidth,
    int? mediaHeight,
    String? stickerId,
    String? momentId,
    String? event,
    String? robotId,
    Map<String, Object?>? eventData,
    List<ChatHistoryEntry>? chatHistoryEntries,
    String? fileName,
    String? mimeType,
    int? durationSeconds,
    String? replyToId,
    String? replyToText,
    int? replyToSeq,
    String? replyToSenderId,
    String? replyToSenderName,
    String? contactUserId,
    String? contactName,
    String? contactHandle,
    String? contactAvatarUrl,
    double? latitude,
    double? longitude,
    String? locationName,
    String? locationAddress,
    List<MessageMention>? mentions,
    List<MessageReaction>? reactions,
    DateTime? editedAt,
    bool? isPinned,
    DateTime? pinnedAt,
    String? pinnedBy,
    DateTime? expiresAt,
    int? deliveredCount,
    int? readCount,
    LinkPreview? linkPreview,
  }) => ChatMessage(
    id: id ?? this.id,
    clientMessageId: clientMessageId ?? this.clientMessageId,
    conversationId: conversationId,
    senderId: senderId,
    senderName: senderName,
    text: text ?? this.text,
    sentAt: sentAt,
    isMine: isMine,
    conversationSeq: conversationSeq ?? this.conversationSeq,
    status: status ?? this.status,
    sendError: (status ?? this.status) == MessageStatus.failed
        ? sendError ?? this.sendError
        : null,
    kind: kind ?? this.kind,
    mediaUrl: mediaUrl ?? this.mediaUrl,
    mediaId: mediaId ?? this.mediaId,
    mediaWidth: mediaWidth ?? this.mediaWidth,
    mediaHeight: mediaHeight ?? this.mediaHeight,
    stickerId: stickerId ?? this.stickerId,
    momentId: momentId ?? this.momentId,
    event: event ?? this.event,
    robotId: robotId ?? this.robotId,
    eventData: eventData ?? this.eventData,
    chatHistoryEntries: chatHistoryEntries ?? this.chatHistoryEntries,
    fileName: fileName ?? this.fileName,
    mimeType: mimeType ?? this.mimeType,
    durationSeconds: durationSeconds ?? this.durationSeconds,
    replyToId: replyToId ?? this.replyToId,
    replyToText: replyToText ?? this.replyToText,
    replyToSeq: replyToSeq ?? this.replyToSeq,
    replyToSenderId: replyToSenderId ?? this.replyToSenderId,
    replyToSenderName: replyToSenderName ?? this.replyToSenderName,
    contactUserId: contactUserId ?? this.contactUserId,
    contactName: contactName ?? this.contactName,
    contactHandle: contactHandle ?? this.contactHandle,
    contactAvatarUrl: contactAvatarUrl ?? this.contactAvatarUrl,
    latitude: latitude ?? this.latitude,
    longitude: longitude ?? this.longitude,
    locationName: locationName ?? this.locationName,
    locationAddress: locationAddress ?? this.locationAddress,
    mentions: mentions ?? this.mentions,
    reactions: reactions ?? this.reactions,
    editedAt: editedAt ?? this.editedAt,
    isPinned: isPinned ?? this.isPinned,
    pinnedAt: pinnedAt ?? this.pinnedAt,
    pinnedBy: pinnedBy ?? this.pinnedBy,
    expiresAt: expiresAt ?? this.expiresAt,
    deliveredCount: deliveredCount ?? this.deliveredCount,
    readCount: readCount ?? this.readCount,
    linkPreview: linkPreview ?? this.linkPreview,
  );

  Map<String, Object?> toJson() => {
    'id': id,
    'clientMessageId': clientMessageId,
    'conversationId': conversationId,
    'senderId': senderId,
    'senderName': senderName,
    'text': text,
    'sentAt': sentAt.toIso8601String(),
    'isMine': isMine,
    'conversationSeq': conversationSeq,
    'status': status.name,
    'sendError': sendError,
    'kind': kind.name,
    'mediaUrl': mediaUrl,
    'mediaId': mediaId,
    'mediaWidth': mediaWidth,
    'mediaHeight': mediaHeight,
    'stickerId': stickerId,
    'momentId': momentId,
    'event': event,
    'robotId': robotId,
    'eventData': eventData,
    'chatHistoryEntries': chatHistoryEntries
        .map((entry) => entry.toJson())
        .toList(),
    'fileName': fileName,
    'mimeType': mimeType,
    'durationSeconds': durationSeconds,
    'replyToId': replyToId,
    'replyToText': replyToText,
    'replyToSeq': replyToSeq,
    'replyToSenderId': replyToSenderId,
    'replyToSenderName': replyToSenderName,
    'contactUserId': contactUserId,
    'contactName': contactName,
    'contactHandle': contactHandle,
    'contactAvatarUrl': contactAvatarUrl,
    'latitude': latitude,
    'longitude': longitude,
    'locationName': locationName,
    'locationAddress': locationAddress,
    'mentions': mentions.map((mention) => mention.toJson()).toList(),
    'reactions': reactions.map((reaction) => reaction.toJson()).toList(),
    'editedAt': editedAt?.toIso8601String(),
    'isPinned': isPinned,
    'pinnedAt': pinnedAt?.toIso8601String(),
    'pinnedBy': pinnedBy,
    'expiresAt': expiresAt?.toIso8601String(),
    'deliveredCount': deliveredCount,
    'readCount': readCount,
    'linkPreview': linkPreview?.toJson(),
  };

  factory ChatMessage.fromJson(Map<String, Object?> json) => ChatMessage(
    id: json['id']! as String,
    clientMessageId:
        json['clientMessageId'] as String? ?? json['id']! as String,
    conversationId: json['conversationId']! as String,
    senderId: json['senderId']! as String,
    senderName: json['senderName']! as String,
    text: json['text']! as String,
    sentAt: parseLocalDateTime(json['sentAt']! as String),
    isMine: json['isMine']! as bool,
    conversationSeq: (json['conversationSeq'] as num?)?.toInt() ?? 0,
    status: MessageStatus.values.byName(json['status']! as String),
    sendError: json['sendError'] as String?,
    kind: MessageContentKind.values.byName(
      json['kind'] as String? ?? MessageContentKind.text.name,
    ),
    mediaUrl: json['mediaUrl'] as String?,
    mediaId: json['mediaId'] as String?,
    mediaWidth: (json['mediaWidth'] as num?)?.toInt(),
    mediaHeight: (json['mediaHeight'] as num?)?.toInt(),
    stickerId: json['stickerId'] as String?,
    momentId: json['momentId'] as String?,
    event: json['event'] as String?,
    robotId: json['robotId'] as String?,
    eventData: json['eventData'] is Map
        ? Map<String, Object?>.from(json['eventData']! as Map)
        : const {},
    chatHistoryEntries: chatHistoryEntriesFrom(json['chatHistoryEntries']),
    fileName: json['fileName'] as String?,
    mimeType: json['mimeType'] as String?,
    durationSeconds: (json['durationSeconds'] as num?)?.toInt(),
    replyToId: json['replyToId'] as String?,
    replyToText: json['replyToText'] as String?,
    replyToSeq: (json['replyToSeq'] as num?)?.toInt() ?? 0,
    replyToSenderId: json['replyToSenderId'] as String?,
    replyToSenderName: json['replyToSenderName'] as String?,
    contactUserId: json['contactUserId'] as String?,
    contactName: json['contactName'] as String?,
    contactHandle: json['contactHandle'] as String?,
    contactAvatarUrl: json['contactAvatarUrl'] as String?,
    latitude: (json['latitude'] as num?)?.toDouble(),
    longitude: (json['longitude'] as num?)?.toDouble(),
    locationName: json['locationName'] as String?,
    locationAddress: json['locationAddress'] as String?,
    mentions: (json['mentions'] as List<Object?>? ?? const [])
        .whereType<Map<String, Object?>>()
        .map(MessageMention.fromJson)
        .toList(),
    reactions: (json['reactions'] as List<Object?>? ?? const [])
        .whereType<Map<String, Object?>>()
        .map(MessageReaction.fromJson)
        .toList(),
    editedAt: tryParseLocalDateTime(json['editedAt']),
    isPinned: json['isPinned'] as bool? ?? false,
    pinnedAt: tryParseLocalDateTime(json['pinnedAt']),
    pinnedBy: json['pinnedBy'] as String?,
    expiresAt: tryParseLocalDateTime(json['expiresAt']),
    deliveredCount: (json['deliveredCount'] as num?)?.toInt() ?? 0,
    readCount: (json['readCount'] as num?)?.toInt() ?? 0,
    linkPreview: json['linkPreview'] is Map<String, Object?>
        ? LinkPreview.fromJson(json['linkPreview']! as Map<String, Object?>)
        : null,
  );
}

class LinkPreview {
  const LinkPreview({
    required this.url,
    required this.title,
    required this.description,
    required this.siteName,
    this.imageUrl,
  });

  final String url;
  final String title;
  final String description;
  final String siteName;
  final String? imageUrl;

  Map<String, Object?> toJson() => {
    'url': url,
    'title': title,
    'description': description,
    'siteName': siteName,
    'imageUrl': imageUrl,
  };

  factory LinkPreview.fromJson(Map<String, Object?> json) => LinkPreview(
    url: json['url'] as String? ?? '',
    title: json['title'] as String? ?? '',
    description: json['description'] as String? ?? '',
    siteName: json['siteName'] as String? ?? '',
    imageUrl: json['imageUrl'] as String?,
  );
}

class ScheduledMessage {
  const ScheduledMessage({
    required this.id,
    required this.conversationId,
    required this.text,
    required this.scheduledAt,
    required this.status,
    this.expiresInSeconds,
    this.errorMessage,
  });

  final String id;
  final String conversationId;
  final String text;
  final DateTime scheduledAt;
  final String status;
  final int? expiresInSeconds;
  final String? errorMessage;

  bool get canRetry => status == 'failed';

  factory ScheduledMessage.fromJson(Map<String, Object?> json) {
    final body = json['body'] as Map<String, Object?>? ?? const {};
    return ScheduledMessage(
      id: json['id']! as String,
      conversationId: json['conversationId']! as String,
      text: body['text'] as String? ?? json['text'] as String? ?? '',
      scheduledAt: DateTime.parse(json['scheduledAt']! as String).toLocal(),
      status: json['status'] as String? ?? 'scheduled',
      expiresInSeconds: (json['expiresInSeconds'] as num?)?.toInt(),
      errorMessage: json['errorMessage'] as String?,
    );
  }
}

class MediaUpload {
  const MediaUpload({
    required this.bytes,
    required this.fileName,
    required this.mimeType,
    required this.kind,
    this.localPath,
    this.durationSeconds,
    this.width,
    this.height,
  });

  final Uint8List bytes;
  final String fileName;
  final String mimeType;
  final MessageContentKind kind;
  final String? localPath;
  final int? durationSeconds;
  final int? width;
  final int? height;

  MediaUpload copyWith({int? width, int? height}) => MediaUpload(
    bytes: bytes,
    fileName: fileName,
    mimeType: mimeType,
    kind: kind,
    localPath: localPath,
    durationSeconds: durationSeconds,
    width: width ?? this.width,
    height: height ?? this.height,
  );
}

class Conversation {
  const Conversation({
    required this.id,
    required this.title,
    required this.subtitle,
    required this.updatedAt,
    required this.kind,
    this.channelId,
    this.channelType = 0,
    this.avatarUrl,
    this.unread = 0,
    this.muted = false,
    this.pinned = false,
    this.saved = false,
    this.archived = false,
    this.lastMessageSeq = 0,
    this.lastReadSeq = 0,
    this.mentionUnreadCount,
    this.members = const [],
    this.currentUserRole,
    int? memberCount,
  }) : memberCount = memberCount ?? members.length;

  final String id;
  final String title;
  final String subtitle;
  final DateTime updatedAt;
  final ConversationKind kind;
  final String? channelId;
  final int channelType;
  final String? avatarUrl;
  final int unread;
  final bool muted;
  final bool pinned;
  final bool saved;
  final bool archived;
  final int lastMessageSeq;
  final int lastReadSeq;

  /// Exact unread mention count supplied by the conversation sync endpoint.
  /// Null means the active server does not support this capability yet.
  final int? mentionUnreadCount;
  final List<AppUser> members;
  final String? currentUserRole;
  final int memberCount;
  bool get isBusinessChannel => channelType > 2;
  bool get canMentionEveryone =>
      kind == ConversationKind.group &&
      (currentUserRole == 'owner' || currentUserRole == 'admin');

  /// Resolves the other participant in a direct conversation without relying
  /// on the server-provided member ordering. The direct channel id is the most
  /// authoritative peer identifier; older payloads can still fall back to
  /// excluding the authenticated user from the member list.
  AppUser? directPeerFor(String? currentUserId) {
    if (kind != ConversationKind.direct) return null;
    final explicitPeerId = channelId?.trim() ?? '';
    if (explicitPeerId.isNotEmpty) {
      for (final member in members) {
        if (member.id == explicitPeerId) return member;
      }
    }
    final ownId = currentUserId?.trim() ?? '';
    for (final member in members) {
      if (ownId.isEmpty || member.id != ownId) return member;
    }
    return null;
  }

  Conversation copyWith({
    String? title,
    String? avatarUrl,
    String? subtitle,
    DateTime? updatedAt,
    int? unread,
    bool? muted,
    bool? pinned,
    bool? saved,
    bool? archived,
    int? lastMessageSeq,
    int? lastReadSeq,
    int? mentionUnreadCount,
    List<AppUser>? members,
    String? currentUserRole,
    int? memberCount,
    String? channelId,
    int? channelType,
  }) => Conversation(
    id: id,
    title: title ?? this.title,
    subtitle: subtitle ?? this.subtitle,
    updatedAt: updatedAt ?? this.updatedAt,
    kind: kind,
    channelId: channelId ?? this.channelId,
    channelType: channelType ?? this.channelType,
    avatarUrl: avatarUrl ?? this.avatarUrl,
    unread: unread ?? this.unread,
    muted: muted ?? this.muted,
    pinned: pinned ?? this.pinned,
    saved: saved ?? this.saved,
    archived: archived ?? this.archived,
    lastMessageSeq: lastMessageSeq ?? this.lastMessageSeq,
    lastReadSeq: lastReadSeq ?? this.lastReadSeq,
    mentionUnreadCount: mentionUnreadCount ?? this.mentionUnreadCount,
    members: members ?? this.members,
    currentUserRole: currentUserRole ?? this.currentUserRole,
    memberCount: memberCount ?? this.memberCount,
  );
}

class FriendRequest {
  const FriendRequest({
    required this.id,
    required this.user,
    required this.note,
    this.outgoing = false,
    this.status = 'pending',
    this.source = 'search',
    this.sourceId,
    this.createdAt,
    this.expiresAt,
    this.updatedAt,
    this.resolvedAt,
  });

  final String id;
  final AppUser user;
  final String note;
  final bool outgoing;
  final String status;
  final String source;
  final String? sourceId;
  final DateTime? createdAt;
  final DateTime? expiresAt;
  final DateTime? updatedAt;
  final DateTime? resolvedAt;

  FriendRequest copyWith({String? status, AppUser? user}) => FriendRequest(
    id: id,
    user: user ?? this.user,
    note: note,
    outgoing: outgoing,
    status: status ?? this.status,
    source: source,
    sourceId: sourceId,
    createdAt: createdAt,
    expiresAt: expiresAt,
    updatedAt: updatedAt,
    resolvedAt: resolvedAt,
  );
}

class GroupProfile {
  const GroupProfile({
    this.historyVisibleToNewMembers = false,
    this.historyPolicyVersion = 1,
    required this.conversationId,
    required this.ownerId,
    required this.name,
    required this.announcement,
    required this.announcementVersion,
    required this.joinPolicy,
    required this.allowMemberAddFriend,
    required this.updatedAt,
    this.avatarUrl,
    this.announcementReadAt,
    this.allMutedUntil,
    this.qrToken,
    this.qrExpiresAt,
    this.dissolvedAt,
  });

  final String conversationId;
  final bool historyVisibleToNewMembers;
  final int historyPolicyVersion;
  final String ownerId;
  final String name;
  final String? avatarUrl;
  final String announcement;
  final int announcementVersion;
  final DateTime? announcementReadAt;
  final String joinPolicy;
  final bool allowMemberAddFriend;
  final DateTime? allMutedUntil;
  final String? qrToken;
  final DateTime? qrExpiresAt;
  final DateTime? dissolvedAt;
  final DateTime updatedAt;

  bool get announcementUnread =>
      announcement.isNotEmpty && announcementReadAt == null;
  bool get allMuted =>
      allMutedUntil != null && allMutedUntil!.isAfter(DateTime.now());
}

class GroupMember {
  const GroupMember({
    required this.user,
    required this.role,
    required this.joinedAt,
    this.mutedUntil,
    this.groupNickname = '',
  });

  final AppUser user;
  final String role;
  final DateTime joinedAt;
  final DateTime? mutedUntil;
  final String groupNickname;

  bool get isOwner => role == 'owner';
  bool get isAdmin => role == 'admin';
  bool get isMuted => mutedUntil != null && mutedUntil!.isAfter(DateTime.now());
}

class GroupInvitation {
  const GroupInvitation({
    required this.id,
    required this.conversationId,
    required this.groupName,
    required this.inviter,
    required this.status,
    required this.createdAt,
    required this.expiresAt,
    this.outgoing = false,
    this.updatedAt,
  });

  final String id;
  final String conversationId;
  final String groupName;
  final AppUser inviter;
  final String status;
  final bool outgoing;
  final DateTime createdAt;
  final DateTime expiresAt;
  final DateTime? updatedAt;

  bool get pending => status == 'pending' && expiresAt.isAfter(DateTime.now());

  GroupInvitation copyWith({String? status}) => GroupInvitation(
    id: id,
    conversationId: conversationId,
    groupName: groupName,
    inviter: inviter,
    status: status ?? this.status,
    outgoing: outgoing,
    createdAt: createdAt,
    expiresAt: expiresAt,
    updatedAt: DateTime.now(),
  );
}

class AppNotice {
  const AppNotice({required this.title, required this.message});
  final String title;
  final String message;
}

class AppAnnouncement {
  const AppAnnouncement({
    required this.id,
    required this.title,
    required this.content,
    required this.status,
    required this.pinned,
    this.publishedAt,
    this.readAt,
  });

  final String id;
  final String title;
  final String content;
  final String status;
  final bool pinned;
  final DateTime? publishedAt;
  final DateTime? readAt;

  bool get unread => readAt == null;

  AppAnnouncement copyWith({DateTime? readAt}) => AppAnnouncement(
    id: id,
    title: title,
    content: content,
    status: status,
    pinned: pinned,
    publishedAt: publishedAt,
    readAt: readAt ?? this.readAt,
  );
}

class ImEvent {
  const ImEvent({
    required this.type,
    required this.payload,
    this.userSyncSeq = 0,
  });

  final ImEventType type;
  final Map<String, Object?> payload;
  final int userSyncSeq;
}
