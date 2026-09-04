import '../core/models.dart';
import 'message_content_registry.dart';
import 'structured_event_text.dart';
import 'wukong_gateway_contract.dart';

class MessageMapper {
  MessageMapper({MessageContentRegistry? registry})
    : registry = registry ?? MessageContentRegistry.standard();

  final MessageContentRegistry registry;

  WukongOutgoingMessage toOutgoing(
    ChatMessage message, {
    required WukongChannel channel,
  }) {
    final expiresAt = message.expiresAt?.toUtc();
    final payload = <String, Object?>{
      'type': _contentType(message),
      ..._body(message),
      if (expiresAt != null) 'expiresAt': expiresAt.toIso8601String(),
    };
    if (WukongContentType.custom.contains(payload['type'])) {
      payload['schemaVersion'] = 1;
    }
    if (message.replyToId case final replyId?) {
      payload['reply'] = <String, Object?>{
        'message_id': replyId,
        'message_seq': message.replyToSeq,
        'from_uid': message.replyToSenderId ?? '',
        'from_name': message.replyToSenderName ?? '',
        'content': ?message.replyToText,
      };
    }
    if (message.mentions.isNotEmpty) {
      payload['mention'] = <String, Object?>{
        if (message.mentions.any((mention) => mention.isEveryone)) 'all': 1,
        'uids': message.mentions
            .where((mention) => !mention.isEveryone)
            .map((mention) => mention.userId)
            .toList(),
      };
    }
    return WukongOutgoingMessage(
      channel: channel,
      payload: registry.validate(payload),
      clientMsgNo: message.clientMessageId,
      expireSeconds: expiresAt == null
          ? 0
          : expiresAt
                .difference(DateTime.now().toUtc())
                .inSeconds
                .clamp(0, 1 << 31)
                .toInt(),
    );
  }

  ChatMessage toChatMessage(
    WukongMessage message, {
    required String currentUserId,
    required String conversationId,
    String senderName = '',
  }) {
    final payload = message.payload;
    final type = message.contentType;
    final reply = payload['reply'] is Map
        ? wukongObjectMap(payload['reply'])
        : const <String, Object?>{};
    final mappedKind = _kind(type);
    final kind = mappedKind == MessageContentKind.text && reply.isNotEmpty
        ? MessageContentKind.reply
        : mappedKind;
    final mention = payload['mention'] is Map
        ? wukongObjectMap(payload['mention'])
        : const <String, Object?>{};
    final mentionIds = (mention['uids'] as List<Object?>? ?? const [])
        .whereType<String>();
    final recalledAt = _date(payload['recalledAt']);
    final expiresAt = _date(payload['expiresAt']);
    final expired = expiresAt != null && !expiresAt.isAfter(DateTime.now());
    return ChatMessage(
      id: message.messageId.isEmpty ? message.clientMsgNo : message.messageId,
      clientMessageId: message.clientMsgNo,
      conversationId: conversationId,
      senderId: message.fromUid,
      senderName: senderName,
      text: _displayText(
        payload,
        kind,
        isMine: message.fromUid == currentUserId,
        senderName: senderName,
      ),
      sentAt: message.timestamp.toLocal(),
      isMine: message.fromUid == currentUserId,
      conversationSeq: message.messageSeq,
      sendError: message.state == WukongMessageState.failed
          ? wukongSendFailureMessage(message.reasonCode)
          : null,
      status: expired
          ? MessageStatus.expired
          : recalledAt != null
          ? MessageStatus.recalled
          : switch (message.state) {
              WukongMessageState.sending => MessageStatus.sending,
              WukongMessageState.sent => MessageStatus.sent,
              WukongMessageState.failed => MessageStatus.failed,
            },
      kind: kind,
      mediaUrl: payload['url'] as String?,
      mediaId: payload['mediaId'] as String?,
      mediaWidth: (payload['width'] as num?)?.toInt(),
      mediaHeight: (payload['height'] as num?)?.toInt(),
      coverMediaId: payload['coverMediaId'] as String?,
      coverUrl: payload['cover'] as String?,
      stickerId: payload['stickerId'] as String?,
      momentId: payload['momentId'] as String?,
      event: payload['event'] as String?,
      robotId: payload['robot_id'] as String? ?? payload['robotId'] as String?,
      eventData: payload['data'] is Map
          ? wukongObjectMap(payload['data'])
          : const {},
      chatHistoryEntries: chatHistoryEntriesFrom(payload['entries']),
      fileName: payload['fileName'] as String?,
      mimeType: payload['mime'] as String? ?? payload['mimeType'] as String?,
      durationSeconds: ((payload['second'] ?? payload['duration']) as num?)
          ?.toInt(),
      replyToId: reply['message_id'] as String?,
      replyToText: reply['content'] as String?,
      replyToSeq: (reply['message_seq'] as num?)?.toInt() ?? 0,
      replyToSenderId: reply['from_uid'] as String?,
      replyToSenderName: reply['from_name'] as String?,
      contactUserId: payload['userId'] as String?,
      contactName: payload['name'] as String?,
      contactHandle: payload['handle'] as String?,
      contactAvatarUrl: payload['avatarUrl'] as String?,
      latitude: (payload['latitude'] as num?)?.toDouble(),
      longitude: (payload['longitude'] as num?)?.toDouble(),
      locationName: payload['name'] as String?,
      locationAddress: payload['address'] as String?,
      mentions: [
        if ((mention['all'] as num?)?.toInt() == 1)
          const MessageMention(userId: 'all', name: '所有人'),
        ...mentionIds.map((uid) => MessageMention(userId: uid, name: '')),
      ],
      reactions: (payload['reactions'] as List<Object?>? ?? const [])
          .whereType<Map>()
          .map(
            (reaction) => MessageReaction.fromJson(wukongObjectMap(reaction)),
          )
          .toList(),
      editedAt: _date(payload['editedAt']),
      isPinned: payload['isPinned'] as bool? ?? false,
      deletedForEveryone:
          payload['deletedForEveryoneAt'] != null ||
          payload['is_mutual_deleted'] == 1,
      pinnedAt: _date(payload['pinnedAt']),
      pinnedBy: payload['pinnedBy'] as String?,
      expiresAt: expiresAt,
    );
  }

  DateTime? _date(Object? value) => value is String
      ? DateTime.tryParse(value)?.toLocal()
      : value is num
      ? DateTime.fromMillisecondsSinceEpoch(
          value.toInt(),
          isUtc: true,
        ).toLocal()
      : null;

  int _contentType(ChatMessage message) => switch (message.kind) {
    MessageContentKind.text ||
    MessageContentKind.reply => WukongContentType.text,
    MessageContentKind.image
        when message.mimeType?.toLowerCase() == 'image/gif' =>
      WukongContentType.gif,
    MessageContentKind.image => WukongContentType.image,
    MessageContentKind.voice => WukongContentType.voice,
    MessageContentKind.video => WukongContentType.video,
    MessageContentKind.file => WukongContentType.file,
    MessageContentKind.contact => WukongContentType.card,
    MessageContentKind.location => WukongContentType.location,
    MessageContentKind.chatHistory => WukongContentType.mergedHistory,
    MessageContentKind.sticker => WukongContentType.storeSticker,
    MessageContentKind.momentShare => WukongContentType.momentShare,
    MessageContentKind.liveEvent => WukongContentType.liveEvent,
    MessageContentKind.system => WukongContentType.systemEvent,
    MessageContentKind.screenshotNotice => WukongContentType.screenshotNotice,
    MessageContentKind.unsupported => throw const FormatException(
      'unsupported message cannot be sent',
    ),
  };

  MessageContentKind _kind(int type) => switch (type) {
    WukongContentType.text => MessageContentKind.text,
    WukongContentType.image ||
    WukongContentType.gif => MessageContentKind.image,
    WukongContentType.voice => MessageContentKind.voice,
    WukongContentType.video => MessageContentKind.video,
    WukongContentType.file => MessageContentKind.file,
    WukongContentType.card => MessageContentKind.contact,
    WukongContentType.location => MessageContentKind.location,
    WukongContentType.mergedHistory => MessageContentKind.chatHistory,
    WukongContentType.storeSticker => MessageContentKind.sticker,
    WukongContentType.momentShare => MessageContentKind.momentShare,
    WukongContentType.liveEvent => MessageContentKind.liveEvent,
    WukongContentType.systemEvent ||
    WukongContentType.callEvent ||
    WukongContentType.supportEvent => MessageContentKind.system,
    WukongContentType.screenshotNotice => MessageContentKind.screenshotNotice,
    _ => MessageContentKind.unsupported,
  };

  Map<String, Object?> _body(ChatMessage message) => switch (message.kind) {
    MessageContentKind.text || MessageContentKind.reply => {
      'content': message.text,
      if (message.robotId case final robotId?) ...{
        'robot_id': robotId,
        'entities': [
          {
            'type': 'bot_command',
            'offset': 0,
            'length': message.text.runes.length,
          },
        ],
      },
    },
    MessageContentKind.image ||
    MessageContentKind.voice ||
    MessageContentKind.video ||
    MessageContentKind.file => {
      'content': message.text,
      // Never put a device-local path into the shared wire payload. Remote
      // media URLs are short-lived; `mediaId` remains the durable reference.
      if (message.mediaUrl case final url?
          when Uri.tryParse(url)?.hasScheme == true &&
              const {'http', 'https'}.contains(Uri.parse(url).scheme))
        'url': _wireMediaURL(url),
      if (message.mediaId != null) 'mediaId': message.mediaId,
      if (message.mediaWidth != null) 'width': message.mediaWidth,
      if (message.mediaHeight != null) 'height': message.mediaHeight,
      if (message.fileName != null) 'fileName': message.fileName,
      if (message.mimeType != null) 'mime': message.mimeType,
      if (message.durationSeconds != null) 'duration': message.durationSeconds,
      if (message.kind == MessageContentKind.video) ...{
        if (message.durationSeconds != null) 'second': message.durationSeconds,
        if (message.coverMediaId != null) 'coverMediaId': message.coverMediaId,
        if (message.coverUrl case final url?
            when const {'http', 'https'}.contains(Uri.tryParse(url)?.scheme))
          'cover': _wireMediaURL(url),
      },
    },
    MessageContentKind.contact => {
      'userId': message.contactUserId,
      'name': message.contactName ?? message.text,
      'handle': message.contactHandle,
      'avatarUrl': message.contactAvatarUrl,
    },
    MessageContentKind.location => {
      'latitude': message.latitude,
      'longitude': message.longitude,
      'name': message.locationName,
      'address': message.locationAddress,
    },
    MessageContentKind.chatHistory => {
      'content': message.text,
      'digest': message.text,
      'entries': message.chatHistoryEntries
          .map((entry) => entry.toJson())
          .toList(),
    },
    MessageContentKind.system || MessageContentKind.screenshotNotice => {
      'content': message.text,
      'digest': message.text,
      if (message.kind == MessageContentKind.screenshotNotice)
        'event': 'screenshot.taken',
    },
    MessageContentKind.sticker => {
      'stickerId': message.stickerId,
      'mediaId': message.mediaId,
      'url': message.mediaUrl,
      'mime': message.mimeType,
      'name': message.fileName,
      'content': message.text,
      'digest': message.text,
    },
    MessageContentKind.momentShare => {
      'momentId': message.momentId,
      'content': message.text,
      'digest': message.text,
    },
    MessageContentKind.liveEvent => {
      'event': message.event,
      'content': message.text,
      'digest': message.text,
      if (message.eventData.isNotEmpty) 'data': message.eventData,
    },
    MessageContentKind.unsupported => const {},
  };

  String _displayText(
    Map<String, Object?> payload,
    MessageContentKind kind, {
    required bool isMine,
    required String senderName,
  }) {
    final contentType = (payload['type'] as num?)?.toInt();
    if (contentType == WukongContentType.callEvent) {
      return callEventDisplayText(payload);
    }
    if (contentType == WukongContentType.supportEvent) {
      return supportEventDisplayText(payload);
    }
    if (contentType == WukongContentType.systemEvent &&
        isGroupSystemEvent(payload)) {
      return groupSystemEventDisplayText(payload);
    }
    return switch (kind) {
      MessageContentKind.text ||
      MessageContentKind.reply => payload['content'] as String? ?? '',
      MessageContentKind.contact => payload['name'] as String? ?? '[名片]',
      MessageContentKind.location => payload['name'] as String? ?? '[位置]',
      MessageContentKind.sticker => payload['digest'] as String? ?? '[表情]',
      MessageContentKind.momentShare =>
        payload['content'] as String? ?? '[朋友圈]',
      MessageContentKind.liveEvent =>
        payload['digest'] as String? ??
            payload['content'] as String? ??
            '[直播互动]',
      MessageContentKind.screenshotNotice =>
        isMine
            ? '你截取了聊天界面'
            : senderName.trim().isEmpty
            ? '对方截取了聊天界面'
            : '${senderName.trim()} 截取了聊天界面',
      _ => payload['content'] as String? ?? registry.digest(payload),
    };
  }
}

String _wireMediaURL(String url) {
  final uri = Uri.parse(url);
  if (!RegExp(r'^/v2/media/[^/]+/(content|cover)$').hasMatch(uri.path)) {
    return url;
  }
  // The viewer guard is local account state, not shared message metadata.
  return url.split('?').first.split('#').first;
}

/// WuKongIMGoProto v1.2.3 (server v2.2.5-20260422), also pinned in
/// server/internal/wukong/policy.go. Generic permission codes do not prove mute.
String wukongSendFailureMessage(int reasonCode) => switch (reasonCode) {
  3 => '你已不在该会话中，无法发送消息',
  4 => '消息被拒绝，当前账号在会话黑名单中',
  11 || 13 => '消息被拒绝，请确认发言权限或消息内容',
  19 => '会话已被封禁，无法发送消息',
  22 => '发送过于频繁，请稍后重试',
  24 => '会话已解散，无法发送消息',
  25 => '当前会话已禁言，无法发送消息',
  _ => '消息发送失败，请稍后重试',
};
