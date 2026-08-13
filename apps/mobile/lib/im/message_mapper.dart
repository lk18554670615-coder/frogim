import '../core/models.dart';
import 'message_content_registry.dart';
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
        'message_seq': 0,
        'from_uid': '',
        'from_name': '',
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
    final kind = _kind(type);
    final reply = payload['reply'] is Map
        ? wukongObjectMap(payload['reply'])
        : const <String, Object?>{};
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
      sentAt: message.timestamp,
      isMine: message.fromUid == currentUserId,
      conversationSeq: message.messageSeq,
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
      stickerId: payload['stickerId'] as String?,
      momentId: payload['momentId'] as String?,
      event: payload['event'] as String?,
      eventData: payload['data'] is Map
          ? wukongObjectMap(payload['data'])
          : const {},
      fileName: payload['fileName'] as String?,
      mimeType: payload['mime'] as String? ?? payload['mimeType'] as String?,
      durationSeconds: (payload['duration'] as num?)?.toInt(),
      replyToId: reply['message_id'] as String?,
      replyToText: reply['content'] as String?,
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
    MessageContentKind.text ||
    MessageContentKind.reply => {'content': message.text},
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
        'url': url,
      if (message.mediaId != null) 'mediaId': message.mediaId,
      if (message.fileName != null) 'fileName': message.fileName,
      if (message.mimeType != null) 'mime': message.mimeType,
      if (message.durationSeconds != null) 'duration': message.durationSeconds,
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
    MessageContentKind.chatHistory ||
    MessageContentKind.system ||
    MessageContentKind.screenshotNotice => {
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
    if ((payload['type'] as num?)?.toInt() == WukongContentType.supportEvent) {
      return _supportEventText(payload);
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

  String _supportEventText(Map<String, Object?> payload) {
    final digest = (payload['digest'] as String? ?? '').trim();
    if (digest.isNotEmpty && digest != '[客服消息]') return digest;
    return switch (payload['event']) {
      'support.session.queued' => '已进入客服队列，请稍候',
      'support.session.assigned' => '客服已接入会话',
      'support.session.transferred' => '客服会话已转接',
      'support.session.ended' => '客服会话已结束',
      'support.session.rated' => '已提交客服评价',
      'support.session.updated' => '客服会话状态已更新',
      _ => '[客服消息]',
    };
  }
}
