import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/im/message_content_registry.dart';

void main() {
  test('registry covers built-in and planned custom content types', () {
    final registry = MessageContentRegistry.standard();
    expect(registry.registeredTypes.toSet(), {
      1,
      2,
      3,
      4,
      5,
      6,
      7,
      8,
      1001,
      1002,
      1003,
      1004,
      1005,
      1006,
      1007,
      1008,
    });
  });

  test('custom content requires schemaVersion 1', () {
    final registry = MessageContentRegistry.standard();
    expect(
      () => registry.validate(const {
        'type': WukongContentType.callEvent,
        'digest': '通话结束',
      }),
      throwsFormatException,
    );
    expect(
      registry.validate(const {
        'type': WukongContentType.callEvent,
        'schemaVersion': 1,
        'event': 'call.ended',
        'digest': '通话结束',
      })['schemaVersion'],
      1,
    );
  });

  test('media content must reference uploaded media', () {
    final registry = MessageContentRegistry.standard();
    expect(() => registry.validate(const {'type': 2}), throwsFormatException);
    expect(
      registry.validate(const {'type': 2, 'mediaId': 'media_1'}),
      containsPair('mediaId', 'media_1'),
    );
  });

  test('every built-in and custom codec validates a real wire shape', () {
    final registry = MessageContentRegistry.standard();
    final payloads = <int, Map<String, Object?>>{
      1: {'type': 1, 'content': 'hello'},
      2: {'type': 2, 'mediaId': 'image-1'},
      3: {'type': 3, 'mediaId': 'gif-1'},
      4: {'type': 4, 'mediaId': 'voice-1'},
      5: {'type': 5, 'mediaId': 'video-1'},
      6: {'type': 6, 'latitude': 31.2, 'longitude': 121.4},
      7: {'type': 7, 'userId': 'usr_b'},
      8: {'type': 8, 'mediaId': 'file-1'},
      1001: {
        'type': 1001,
        'schemaVersion': 1,
        'digest': '[聊天记录]',
        'entries': [
          {'sourceMessageId': 'message-1'},
        ],
      },
      1002: {
        'type': 1002,
        'schemaVersion': 1,
        'event': 'group.member.joined',
        'digest': '[系统消息]',
      },
      1003: {'type': 1003, 'schemaVersion': 1, 'stickerId': 'sticker-1'},
      1004: {'type': 1004, 'schemaVersion': 1, 'momentId': 'moment-1'},
      1005: {
        'type': 1005,
        'schemaVersion': 1,
        'event': 'call.ended',
        'digest': '[通话]',
      },
      1006: {
        'type': 1006,
        'schemaVersion': 1,
        'event': 'live.like',
        'digest': '[直播互动]',
      },
      1007: {
        'type': 1007,
        'schemaVersion': 1,
        'event': 'support.session.ended',
        'digest': '[客服消息]',
      },
      1008: {
        'type': 1008,
        'schemaVersion': 1,
        'event': 'screenshot.taken',
        'digest': '[截屏提示]',
      },
    };

    for (final entry in payloads.entries) {
      final validated = registry.validate(entry.value);
      expect(validated['type'], entry.key, reason: 'content type ${entry.key}');
      expect(
        registry.digest(validated),
        isNotEmpty,
        reason: 'content type ${entry.key}',
      );
    }
  });

  test('live events use the product protocol whitelist', () {
    final registry = MessageContentRegistry.standard();
    for (final event in const {'live.like', 'live.applause', 'live.follow'}) {
      expect(
        registry.validate({
          'type': WukongContentType.liveEvent,
          'schemaVersion': 1,
          'event': event,
        }),
        containsPair('event', event),
      );
    }
    for (final event in const {
      'live.unknown',
      'live.reaction',
      ' live.like ',
    }) {
      expect(
        () => registry.validate({
          'type': WukongContentType.liveEvent,
          'schemaVersion': 1,
          'event': event,
        }),
        throwsFormatException,
      );
    }
  });
}
