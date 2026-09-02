import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/models.dart';
import 'package:linli_im/im/message_content_registry.dart';
import 'package:linli_im/im/message_mapper.dart';
import 'package:linli_im/im/wukong_gateway_contract.dart';

void main() {
  final mapper = MessageMapper();

  test('失败回执有明确提示，重试不把本地错误文案发送给其他成员', () {
    final failed = mapper.toChatMessage(
      WukongMessage(
        messageId: '',
        messageSeq: 0,
        clientMsgNo: 'muted-client',
        clientSeq: 1,
        fromUid: 'usr_a',
        channel: const WukongChannel(id: 'group_1', type: 2),
        timestamp: DateTime.utc(2026, 9, 2),
        payload: const {'type': 1, 'content': '待发送'},
        state: WukongMessageState.failed,
        reasonCode: 25,
      ),
      currentUserId: 'usr_a',
      conversationId: 'conversation-1',
    );
    expect(failed.status, MessageStatus.failed);
    expect(failed.sendError, '当前会话已禁言，无法发送消息');
    expect(ChatMessage.fromJson(failed.toJson()).sendError, failed.sendError);
    expect(failed.copyWith(status: MessageStatus.sending).sendError, isNull);
    final outgoing = mapper.toOutgoing(
      failed,
      channel: const WukongChannel(id: 'group_1', type: 2),
    );
    expect(outgoing.payload, {'type': 1, 'content': '待发送'});
  });

  test('maps text reply and mentions to the WuKong wire body', () {
    final pending = ChatMessage(
      id: 'local-1',
      clientMessageId: 'client-1',
      conversationId: 'conversation-1',
      senderId: 'usr_a',
      senderName: 'Alice',
      text: 'hello @Bob',
      sentAt: DateTime.utc(2026, 8, 11),
      isMine: true,
      kind: MessageContentKind.reply,
      replyToId: 'server-1',
      replyToText: '原消息',
      replyToSeq: 42,
      replyToSenderId: 'usr_b',
      replyToSenderName: 'Bob',
      mentions: const [
        MessageMention(userId: 'usr_b', name: 'Bob'),
        MessageMention(userId: 'all', name: '所有人'),
      ],
    );

    final outgoing = mapper.toOutgoing(
      pending,
      channel: const WukongChannel(id: 'group_1', type: 2),
    );

    expect(outgoing.clientMsgNo, 'client-1');
    expect(outgoing.payload['type'], WukongContentType.text);
    expect(
      wukongObjectMap(outgoing.payload['reply'])['message_id'],
      'server-1',
    );
    expect(wukongObjectMap(outgoing.payload['reply']), {
      'message_id': 'server-1',
      'message_seq': 42,
      'from_uid': 'usr_b',
      'from_name': 'Bob',
      'content': '原消息',
    });
    expect(wukongObjectMap(outgoing.payload['mention'])['all'], 1);

    final restored = mapper.toChatMessage(
      WukongMessage(
        messageId: 'message-reply',
        messageSeq: 43,
        clientMsgNo: 'client-1',
        clientSeq: 1,
        fromUid: 'usr_a',
        channel: const WukongChannel(id: 'group_1', type: 2),
        timestamp: DateTime.utc(2026, 8, 11),
        payload: outgoing.payload,
        state: WukongMessageState.sent,
      ),
      currentUserId: 'usr_a',
      conversationId: 'conversation-1',
    );
    expect(restored.replyToSeq, 42);
    expect(restored.kind, MessageContentKind.reply);
    expect(restored.replyToText, '原消息');
    expect(restored.replyToSenderId, 'usr_b');
    expect(restored.replyToSenderName, 'Bob');
    expect(ChatMessage.fromJson(restored.toJson()).replyToText, '原消息');
  });

  test(
    'robot command keeps TangSeng-compatible target and entity metadata',
    () {
      final outgoing = mapper.toOutgoing(
        ChatMessage(
          id: 'local-robot',
          clientMessageId: 'client-robot',
          conversationId: 'conversation-robot',
          senderId: 'usr_a',
          senderName: 'Alice',
          text: '查询订单',
          sentAt: DateTime.utc(2026, 8, 16),
          isMine: true,
          robotId: 'robot_support',
        ),
        channel: const WukongChannel(id: 'group_support', type: 2),
      );

      expect(outgoing.payload['robot_id'], 'robot_support');
      final entities = outgoing.payload['entities']! as List<Object?>;
      expect(entities, hasLength(1));
      expect(wukongObjectMap(entities.single)['type'], 'bot_command');
      expect(wukongObjectMap(entities.single)['length'], 4);

      final restored = mapper.toChatMessage(
        WukongMessage(
          messageId: 'robot-message',
          messageSeq: 1,
          clientMsgNo: 'client-robot',
          clientSeq: 1,
          fromUid: 'usr_a',
          channel: const WukongChannel(id: 'group_support', type: 2),
          timestamp: DateTime.utc(2026, 8, 16),
          payload: outgoing.payload,
          state: WukongMessageState.sent,
        ),
        currentUserId: 'usr_a',
        conversationId: 'conversation-robot',
      );
      expect(restored.robotId, 'robot_support');
      expect(ChatMessage.fromJson(restored.toJson()).robotId, 'robot_support');
    },
  );

  test('maps received WuKong media and protocol status into UI model', () {
    final received = WukongMessage(
      messageId: '101',
      messageSeq: 9,
      clientMsgNo: 'client-9',
      clientSeq: 0,
      fromUid: 'usr_b',
      channel: const WukongChannel(id: 'usr_b', type: 1),
      timestamp: DateTime.utc(2026, 8, 11),
      payload: const {
        'type': WukongContentType.image,
        'url': 'https://media.example/image.jpg',
        'mediaId': 'media_9',
        'width': 1440,
        'height': 1920,
      },
      state: WukongMessageState.sent,
      reasonCode: 1,
    );

    final mapped = mapper.toChatMessage(
      received,
      currentUserId: 'usr_a',
      conversationId: 'conversation-9',
      senderName: 'Bob',
    );

    expect(mapped.id, '101');
    expect(mapped.kind, MessageContentKind.image);
    expect(mapped.mediaId, 'media_9');
    expect(mapped.mediaWidth, 1440);
    expect(mapped.mediaHeight, 1920);
    expect(mapped.status, MessageStatus.sent);
    expect(mapped.isMine, isFalse);

    final outgoing = mapper.toOutgoing(
      mapped,
      channel: const WukongChannel(id: 'usr_b', type: 1),
    );
    expect(outgoing.payload['width'], 1440);
    expect(outgoing.payload['height'], 1920);
    final restored = ChatMessage.fromJson(mapped.toJson());
    expect(restored.mediaWidth, 1440);
    expect(restored.mediaHeight, 1920);
  });

  test('normalizes WuKong and cached UTC timestamps for local display', () {
    final timestamp = DateTime.utc(2026, 9, 1, 5, 20);
    final mapped = mapper.toChatMessage(
      WukongMessage(
        messageId: 'local-time-message',
        messageSeq: 1,
        clientMsgNo: 'local-time-client',
        clientSeq: 1,
        fromUid: 'usr_b',
        channel: const WukongChannel(id: 'usr_b', type: 1),
        timestamp: timestamp,
        payload: const {'type': WukongContentType.text, 'content': 'time'},
        state: WukongMessageState.sent,
      ),
      currentUserId: 'usr_a',
      conversationId: 'conversation-time',
    );

    expect(mapped.sentAt, timestamp.toLocal());
    expect(mapped.sentAt.isUtc, isFalse);
    final restored = ChatMessage.fromJson({
      ...mapped.toJson(),
      'sentAt': timestamp.toIso8601String(),
      'editedAt': timestamp.toIso8601String(),
    });
    expect(restored.sentAt, timestamp.toLocal());
    expect(restored.editedAt, timestamp.toLocal());
    expect(restored.sentAt.isUtc, isFalse);
  });

  test(
    'preserves merged history snapshot entries from WuKong content 1001',
    () {
      final mapped = mapper.toChatMessage(
        WukongMessage(
          messageId: 'history-1',
          messageSeq: 12,
          clientMsgNo: 'history-client-1',
          clientSeq: 0,
          fromUid: 'usr_b',
          channel: const WukongChannel(id: 'usr_b', type: 1),
          timestamp: DateTime.utc(2026, 8, 13),
          payload: const {
            'type': WukongContentType.mergedHistory,
            'schemaVersion': 1,
            'digest': '[聊天记录]',
            'entries': [
              {
                'sourceMessageId': 'source-1',
                'senderId': 'usr_a',
                'summary': '第一条消息',
                'createdAt': '2026-08-13T01:02:03Z',
                'type': 'text',
              },
              {
                'sourceMessageId': 'source-2',
                'senderId': 'usr_b',
                'summary': '[图片]',
                'createdAt': 1786583045000,
                'type': 'image',
              },
            ],
          },
          state: WukongMessageState.sent,
        ),
        currentUserId: 'usr_a',
        conversationId: 'conversation-1',
        senderName: 'Bob',
      );

      expect(mapped.kind, MessageContentKind.chatHistory);
      expect(mapped.chatHistoryEntries, hasLength(2));
      expect(mapped.chatHistoryEntries.first.sourceMessageId, 'source-1');
      expect(mapped.chatHistoryEntries.last.type, 'image');

      final restored = ChatMessage.fromJson(mapped.toJson());
      expect(restored.chatHistoryEntries, hasLength(2));
      expect(restored.chatHistoryEntries.first.summary, '第一条消息');
    },
  );

  test('encodes GIF media with the pinned built-in content type 3', () {
    final outgoing = mapper.toOutgoing(
      ChatMessage(
        id: 'local-gif',
        clientMessageId: 'client-gif',
        conversationId: 'conversation-1',
        senderId: 'usr_a',
        senderName: 'Alice',
        text: '[动图]',
        sentAt: DateTime.utc(2026, 8, 12),
        isMine: true,
        kind: MessageContentKind.image,
        mediaId: 'media-gif-1',
        mimeType: 'image/gif',
      ),
      channel: const WukongChannel(id: 'usr_b', type: 1),
    );

    expect(outgoing.payload['type'], WukongContentType.gif);
    expect(outgoing.payload['mediaId'], 'media-gif-1');
  });

  test('keeps absolute expiry portable across every WuKong SDK', () {
    final expiresAt = DateTime.now().toUtc().add(const Duration(minutes: 5));
    final outgoing = mapper.toOutgoing(
      ChatMessage(
        id: 'local-expiring',
        clientMessageId: 'client-expiring',
        conversationId: 'conversation-1',
        senderId: 'usr_a',
        senderName: 'Alice',
        text: 'temporary',
        sentAt: DateTime.now().toUtc(),
        isMine: true,
        expiresAt: expiresAt,
      ),
      channel: const WukongChannel(id: 'usr_b', type: 1),
    );

    expect(DateTime.parse(outgoing.payload['expiresAt']! as String), expiresAt);
    expect(outgoing.expireSeconds, inInclusiveRange(298, 300));

    final active = mapper.toChatMessage(
      WukongMessage(
        messageId: 'expiry-active',
        messageSeq: 1,
        clientMsgNo: 'client-active',
        clientSeq: 0,
        fromUid: 'usr_a',
        channel: const WukongChannel(id: 'usr_b', type: 1),
        timestamp: DateTime.now().toUtc(),
        payload: outgoing.payload,
        state: WukongMessageState.sent,
      ),
      currentUserId: 'usr_b',
      conversationId: 'conversation-1',
    );
    expect(active.expiresAt, expiresAt.toLocal());
    expect(active.status, MessageStatus.sent);

    final expired = mapper.toChatMessage(
      WukongMessage(
        messageId: 'expiry-past',
        messageSeq: 2,
        clientMsgNo: 'client-past',
        clientSeq: 0,
        fromUid: 'usr_a',
        channel: const WukongChannel(id: 'usr_b', type: 1),
        timestamp: DateTime.now().toUtc().subtract(const Duration(minutes: 2)),
        payload: {
          'type': WukongContentType.text,
          'content': 'already expired',
          'expiresAt': DateTime.now()
              .toUtc()
              .subtract(const Duration(minutes: 1))
              .toIso8601String(),
        },
        state: WukongMessageState.sent,
      ),
      currentUserId: 'usr_b',
      conversationId: 'conversation-1',
    );
    expect(expired.status, MessageStatus.expired);
    expect(expired.expiresAt, isNotNull);
  });

  test('maps business extensions from synchronized WuKong payload', () {
    final received = WukongMessage(
      messageId: '102',
      messageSeq: 10,
      clientMsgNo: 'client-10',
      clientSeq: 0,
      fromUid: 'usr_b',
      channel: const WukongChannel(id: 'usr_b', type: 1),
      timestamp: DateTime.utc(2026, 8, 11),
      payload: const {
        'type': WukongContentType.text,
        'content': 'edited',
        'recalledAt': '2026-08-11T01:02:03Z',
        'editedAt': '2026-08-11T01:01:00Z',
        'reactions': [
          {'emoji': '👍', 'count': 2, 'reactedByMe': true},
        ],
        'isPinned': true,
        'pinnedBy': 'usr_admin',
        'pinnedAt': '2026-08-11T01:00:00Z',
      },
      state: WukongMessageState.sent,
    );

    final mapped = mapper.toChatMessage(
      received,
      currentUserId: 'usr_a',
      conversationId: 'conversation-10',
    );
    expect(mapped.status, MessageStatus.recalled);
    expect(mapped.text, 'edited');
    expect(mapped.editedAt, isNotNull);
    expect(mapped.reactions.single.count, 2);
    expect(mapped.reactions.single.reactedByMe, isTrue);
    expect(mapped.isPinned, isTrue);
    expect(mapped.pinnedBy, 'usr_admin');
  });

  test(
    'maps durable sticker and moment references to custom content types',
    () {
      final now = DateTime.utc(2026, 8, 11);
      final sticker = ChatMessage(
        id: 'local-sticker',
        clientMessageId: 'client-sticker',
        conversationId: 'conversation-1',
        senderId: 'usr_a',
        senderName: 'Alice',
        text: '[表情] Cat',
        sentAt: now,
        isMine: true,
        kind: MessageContentKind.sticker,
        stickerId: 'sticker-1',
        mediaId: 'media-1',
        mediaUrl: 'https://cdn.example/media-1',
        mimeType: 'image/webp',
      );
      final outgoing = mapper.toOutgoing(
        sticker,
        channel: const WukongChannel(id: 'group-1', type: 2),
      );
      expect(outgoing.payload['type'], WukongContentType.storeSticker);
      expect(outgoing.payload['schemaVersion'], 1);
      expect(outgoing.payload['stickerId'], 'sticker-1');

      final received = mapper.toChatMessage(
        WukongMessage(
          messageId: '200',
          messageSeq: 1,
          clientMsgNo: 'moment-client',
          clientSeq: 0,
          fromUid: 'usr_b',
          channel: const WukongChannel(id: 'group-1', type: 2),
          timestamp: now,
          payload: const {
            'type': WukongContentType.momentShare,
            'schemaVersion': 1,
            'momentId': 'moment-1',
            'content': '[朋友圈] hello',
          },
          state: WukongMessageState.sent,
        ),
        currentUserId: 'usr_a',
        conversationId: 'conversation-1',
      );
      expect(received.kind, MessageContentKind.momentShare);
      expect(received.momentId, 'moment-1');
    },
  );

  test('rejects custom messages without durable references', () {
    final invalid = ChatMessage(
      id: 'local-invalid',
      conversationId: 'conversation-1',
      senderId: 'usr_a',
      senderName: 'Alice',
      text: '[表情]',
      sentAt: DateTime.utc(2026, 8, 11),
      isMine: true,
      kind: MessageContentKind.sticker,
    );
    expect(
      () => mapper.toOutgoing(
        invalid,
        channel: const WukongChannel(id: 'group-1', type: 2),
      ),
      throwsFormatException,
    );
  });

  test('keeps screenshot notices distinct and schema-versioned', () {
    final outgoing = mapper.toOutgoing(
      ChatMessage(
        id: 'local-screenshot',
        clientMessageId: 'client-screenshot',
        conversationId: 'conversation-1',
        senderId: 'usr_a',
        senderName: 'Alice',
        text: 'Alice 截取了聊天界面',
        sentAt: DateTime.utc(2026, 8, 11),
        isMine: true,
        kind: MessageContentKind.screenshotNotice,
      ),
      channel: const WukongChannel(id: 'usr_b', type: 1),
    );
    expect(outgoing.payload['type'], WukongContentType.screenshotNotice);
    expect(outgoing.payload['schemaVersion'], 1);
    expect(outgoing.payload['event'], 'screenshot.taken');

    final received = mapper.toChatMessage(
      WukongMessage(
        messageId: 'screenshot-1',
        messageSeq: 3,
        clientMsgNo: 'client-screenshot',
        clientSeq: 0,
        fromUid: 'usr_a',
        channel: const WukongChannel(id: 'usr_b', type: 1),
        timestamp: DateTime.utc(2026, 8, 11),
        payload: outgoing.payload,
        state: WukongMessageState.sent,
      ),
      currentUserId: 'usr_b',
      conversationId: 'conversation-1',
    );
    expect(received.kind, MessageContentKind.screenshotNotice);
    expect(received.text, '对方截取了聊天界面');
  });

  test('maps structured live interaction events without flattening data', () {
    final outgoing = mapper.toOutgoing(
      ChatMessage(
        id: 'local-live',
        clientMessageId: 'client-live',
        conversationId: 'live-conversation',
        senderId: 'usr_a',
        senderName: 'Alice',
        text: '❤️ 点赞了直播',
        sentAt: DateTime.utc(2026, 8, 12),
        isMine: true,
        kind: MessageContentKind.liveEvent,
        event: 'live.like',
        eventData: const {'count': 1},
      ),
      channel: const WukongChannel(id: 'live-1', type: 9),
    );

    expect(outgoing.payload['type'], WukongContentType.liveEvent);
    expect(outgoing.payload['schemaVersion'], 1);
    expect(outgoing.payload['event'], 'live.like');
    expect(wukongObjectMap(outgoing.payload['data'])['count'], 1);

    final received = mapper.toChatMessage(
      WukongMessage(
        messageId: 'live-message-1',
        messageSeq: 8,
        clientMsgNo: 'client-live',
        clientSeq: 0,
        fromUid: 'usr_a',
        channel: const WukongChannel(id: 'live-1', type: 9),
        timestamp: DateTime.utc(2026, 8, 12),
        payload: outgoing.payload,
        state: WukongMessageState.sent,
      ),
      currentUserId: 'usr_b',
      conversationId: 'live-conversation',
      senderName: 'Alice',
    );
    expect(received.kind, MessageContentKind.liveEvent);
    expect(received.event, 'live.like');
    expect(received.eventData['count'], 1);
    expect(received.text, '❤️ 点赞了直播');
  });

  test('maps support events to friendly status text', () {
    final expected = <String, String>{
      'support.session.queued': '已进入客服队列，请稍候',
      'support.session.assigned': '客服已接入会话',
      'support.session.transferred': '客服会话已转接',
      'support.session.ended': '客服会话已结束',
      'support.session.rated': '已提交客服评价',
    };
    for (final entry in expected.entries) {
      final received = mapper.toChatMessage(
        WukongMessage(
          messageId: 'support-${entry.key}',
          messageSeq: 1,
          clientMsgNo: 'client-${entry.key}',
          clientSeq: 0,
          fromUid: '____system',
          channel: const WukongChannel(id: 'support-1', type: 10),
          timestamp: DateTime.utc(2026, 8, 13),
          payload: {
            'type': WukongContentType.supportEvent,
            'schemaVersion': 1,
            'event': entry.key,
          },
          state: WukongMessageState.sent,
        ),
        currentUserId: 'usr_a',
        conversationId: 'support-1:10',
      );
      expect(received.kind, MessageContentKind.system);
      expect(received.event, entry.key);
      expect(received.text, entry.value);
    }
  });

  test('maps persisted call events to durable friendly status text', () {
    final received = mapper.toChatMessage(
      WukongMessage(
        messageId: 'call-message-1',
        messageSeq: 9,
        clientMsgNo: 'call-record-1',
        clientSeq: 0,
        fromUid: 'usr_a',
        channel: const WukongChannel(id: 'usr_b', type: 1),
        timestamp: DateTime.utc(2026, 8, 13),
        payload: const {
          'type': WukongContentType.callEvent,
          'schemaVersion': 1,
          'event': 'call.ended',
          'mediaType': 'video',
          'status': 'ended',
          'durationSeconds': 65,
          'data': {'callId': 'call-1'},
        },
        state: WukongMessageState.sent,
      ),
      currentUserId: 'usr_b',
      conversationId: 'conversation-1',
      senderName: 'Alice',
    );

    expect(received.kind, MessageContentKind.system);
    expect(received.event, 'call.ended');
    expect(received.eventData['callId'], 'call-1');
    expect(received.text, '视频通话已结束 · 01:05');
  });
}
