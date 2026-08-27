typedef WukongContentValidator = void Function(Map<String, Object?> payload);
typedef WukongContentDigest = String Function(Map<String, Object?> payload);

abstract final class WukongContentType {
  static const text = 1;
  static const image = 2;
  static const gif = 3;
  static const voice = 4;
  static const video = 5;
  static const location = 6;
  static const card = 7;
  static const file = 8;

  static const mergedHistory = 1001;
  static const systemEvent = 1002;
  static const storeSticker = 1003;
  static const momentShare = 1004;
  static const callEvent = 1005;
  static const liveEvent = 1006;
  static const supportEvent = 1007;
  static const screenshotNotice = 1008;

  static const custom = {
    mergedHistory,
    systemEvent,
    storeSticker,
    momentShare,
    callEvent,
    liveEvent,
    supportEvent,
    screenshotNotice,
  };
}

class MessageContentCodec {
  const MessageContentCodec({
    required this.type,
    required this.name,
    required this.validate,
    required this.digest,
  });

  final int type;
  final String name;
  final WukongContentValidator validate;
  final WukongContentDigest digest;
}

class MessageContentRegistry {
  MessageContentRegistry._(this._codecs);

  factory MessageContentRegistry.standard() {
    final registry = MessageContentRegistry._({});
    registry
      ..register(_textCodec())
      ..register(_mediaCodec(WukongContentType.image, 'image', '[图片]'))
      ..register(_mediaCodec(WukongContentType.gif, 'gif', '[动图]'))
      ..register(_mediaCodec(WukongContentType.voice, 'voice', '[语音]'))
      ..register(_mediaCodec(WukongContentType.video, 'video', '[视频]'))
      ..register(_locationCodec())
      ..register(_cardCodec())
      ..register(_mediaCodec(WukongContentType.file, 'file', '[文件]'));
    for (final entry in const {
      WukongContentType.mergedHistory: ('merged_history', '[聊天记录]'),
      WukongContentType.systemEvent: ('system_event', '[系统消息]'),
      WukongContentType.storeSticker: ('store_sticker', '[表情]'),
      WukongContentType.momentShare: ('moment_share', '[朋友圈]'),
      WukongContentType.callEvent: ('call_event', '[通话]'),
      WukongContentType.liveEvent: ('live_event', '[直播互动]'),
      WukongContentType.supportEvent: ('support_event', '[客服消息]'),
      WukongContentType.screenshotNotice: ('screenshot_notice', '[截屏提示]'),
    }.entries) {
      registry.register(
        _customCodec(entry.key, entry.value.$1, entry.value.$2),
      );
    }
    registry
      ..register(
        _listCustomCodec(
          WukongContentType.mergedHistory,
          'merged_history',
          'entries',
          '[聊天记录]',
        ),
        replace: true,
      )
      ..register(
        _referenceCustomCodec(
          WukongContentType.storeSticker,
          'store_sticker',
          'stickerId',
          '[表情]',
        ),
        replace: true,
      )
      ..register(
        _eventCustomCodec(
          WukongContentType.systemEvent,
          'system_event',
          '[系统消息]',
        ),
        replace: true,
      )
      ..register(
        _eventCustomCodec(
          WukongContentType.callEvent,
          'call_event',
          '[通话]',
          requiredPrefix: 'call.',
        ),
        replace: true,
      )
      ..register(
        _eventCustomCodec(
          WukongContentType.liveEvent,
          'live_event',
          '[直播互动]',
          allowedEvents: const {'live.like', 'live.applause', 'live.follow'},
        ),
        replace: true,
      )
      ..register(
        _eventCustomCodec(
          WukongContentType.supportEvent,
          'support_event',
          '[客服消息]',
          requiredPrefix: 'support.',
        ),
        replace: true,
      )
      ..register(
        _eventCustomCodec(
          WukongContentType.screenshotNotice,
          'screenshot_notice',
          '[截屏提示]',
          exactEvent: 'screenshot.taken',
        ),
        replace: true,
      )
      ..register(
        _referenceCustomCodec(
          WukongContentType.momentShare,
          'moment_share',
          'momentId',
          '[朋友圈]',
        ),
        replace: true,
      );
    return registry;
  }

  final Map<int, MessageContentCodec> _codecs;

  Iterable<int> get registeredTypes => _codecs.keys;

  void register(MessageContentCodec codec, {bool replace = false}) {
    if (!replace && _codecs.containsKey(codec.type)) {
      throw StateError(
        'message content type ${codec.type} is already registered',
      );
    }
    _codecs[codec.type] = codec;
  }

  MessageContentCodec? codec(int type) => _codecs[type];

  Map<String, Object?> validate(Map<String, Object?> source) {
    final payload = Map<String, Object?>.from(source);
    final type = (payload['type'] as num?)?.toInt();
    if (type == null) {
      throw const FormatException('message payload requires an integer type');
    }
    final codec = _codecs[type];
    if (codec == null) {
      throw FormatException('message content type $type is not registered');
    }
    payload['type'] = type;
    codec.validate(payload);
    return payload;
  }

  String digest(Map<String, Object?> payload) {
    final type = (payload['type'] as num?)?.toInt() ?? -1;
    return _codecs[type]?.digest(payload) ?? '[不支持的消息]';
  }

  static MessageContentCodec _textCodec() => MessageContentCodec(
    type: WukongContentType.text,
    name: 'text',
    validate: (payload) => _requireString(payload, 'content'),
    digest: (payload) => payload['content'] as String? ?? '',
  );

  static MessageContentCodec _mediaCodec(
    int type,
    String name,
    String digest,
  ) => MessageContentCodec(
    type: type,
    name: name,
    validate: (payload) {
      if ((payload['url'] as String? ?? '').isEmpty &&
          (payload['mediaId'] as String? ?? '').isEmpty) {
        throw FormatException('$name message requires url or mediaId');
      }
    },
    digest: (_) => digest,
  );

  static MessageContentCodec _locationCodec() => MessageContentCodec(
    type: WukongContentType.location,
    name: 'location',
    validate: (payload) {
      if (payload['latitude'] is! num || payload['longitude'] is! num) {
        throw const FormatException('location requires latitude and longitude');
      }
    },
    digest: (payload) =>
        '[位置] ${payload['name'] as String? ?? payload['address'] as String? ?? ''}',
  );

  static MessageContentCodec _cardCodec() => MessageContentCodec(
    type: WukongContentType.card,
    name: 'card',
    validate: (payload) => _requireString(payload, 'userId'),
    digest: (payload) => '[名片] ${payload['name'] as String? ?? ''}',
  );

  static MessageContentCodec _customCodec(
    int type,
    String name,
    String fallbackDigest,
  ) => MessageContentCodec(
    type: type,
    name: name,
    validate: (payload) {
      if ((payload['schemaVersion'] as num?)?.toInt() != 1) {
        throw FormatException('$name requires schemaVersion: 1');
      }
    },
    digest: (payload) => payload['digest'] as String? ?? fallbackDigest,
  );

  static void _requireString(Map<String, Object?> payload, String key) {
    if ((payload[key] as String? ?? '').trim().isEmpty) {
      throw FormatException('message payload requires $key');
    }
  }

  static MessageContentCodec _referenceCustomCodec(
    int type,
    String name,
    String referenceKey,
    String fallbackDigest,
  ) => MessageContentCodec(
    type: type,
    name: name,
    validate: (payload) {
      if ((payload['schemaVersion'] as num?)?.toInt() != 1) {
        throw FormatException('$name requires schemaVersion: 1');
      }
      _requireString(payload, referenceKey);
    },
    digest: (payload) => payload['digest'] as String? ?? fallbackDigest,
  );

  static MessageContentCodec _listCustomCodec(
    int type,
    String name,
    String listKey,
    String fallbackDigest,
  ) => MessageContentCodec(
    type: type,
    name: name,
    validate: (payload) {
      _requireSchemaVersion(payload, name);
      final entries = payload[listKey];
      if (entries is! List<Object?> ||
          entries.isEmpty ||
          entries.length > 100) {
        throw FormatException('$name requires 1–100 $listKey');
      }
    },
    digest: (payload) => payload['digest'] as String? ?? fallbackDigest,
  );

  static MessageContentCodec _eventCustomCodec(
    int type,
    String name,
    String fallbackDigest, {
    String? requiredPrefix,
    String? exactEvent,
    Set<String>? allowedEvents,
  }) => MessageContentCodec(
    type: type,
    name: name,
    validate: (payload) {
      _requireSchemaVersion(payload, name);
      final event = payload['event'];
      if (event is! String ||
          event.trim().isEmpty ||
          event.length > 128 ||
          (requiredPrefix != null && !event.startsWith(requiredPrefix)) ||
          (exactEvent != null && event != exactEvent) ||
          (allowedEvents != null && !allowedEvents.contains(event))) {
        throw FormatException('$name requires a valid event');
      }
    },
    digest: (payload) => payload['digest'] as String? ?? fallbackDigest,
  );

  static void _requireSchemaVersion(Map<String, Object?> payload, String name) {
    if ((payload['schemaVersion'] as num?)?.toInt() != 1) {
      throw FormatException('$name requires schemaVersion: 1');
    }
  }
}
