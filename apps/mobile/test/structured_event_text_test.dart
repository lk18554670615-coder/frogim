import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/im/structured_event_text.dart';

void main() {
  test('call event text handles terminal states and nested call data', () {
    expect(
      callEventDisplayText(const {
        'event': 'call.rejected',
        'call': {'mediaType': 'video'},
      }),
      '视频通话已拒绝',
    );
    expect(
      callEventDisplayText(const {
        'event': 'call.timeout',
        'data': {'mediaType': 'audio'},
      }),
      '语音通话未接通',
    );
    expect(
      callEventDisplayText(const {
        'status': 'ended',
        'mediaType': 'audio',
        'durationSeconds': 3661,
      }),
      '语音通话已结束 · 01:01:01',
    );
  });

  test('authoritative event digests override local fallback wording', () {
    expect(
      callEventDisplayText(const {
        'digest': '视频通话已结束 · 00:42',
        'event': 'call.ended',
      }),
      '视频通话已结束 · 00:42',
    );
    expect(
      supportEventDisplayText(const {'event': 'support.session.transferred'}),
      '客服会话已转接',
    );
  });

  test(
    'group system events describe their operation instead of a placeholder',
    () {
      expect(
        groupSystemEventDisplayText(const {
          'event': 'group.mute_all.updated',
          'digest': '[群系统消息]',
          'data': {'muted': true},
        }),
        '已开启全员禁言',
      );
      expect(
        groupSystemEventDisplayText(const {
          'event': 'group.message.pinned',
          'digest': '[系统消息]',
        }),
        '已置顶一条群消息',
      );
      expect(
        groupSystemEventDisplayText(const {
          'event': 'group.member.mute',
          'data': {'muted': false},
        }),
        '已解除一名群成员的禁言',
      );
      expect(
        groupSystemEventDisplayText(const {
          'event': 'group.members.added',
          'data': {
            'userIds': ['u1', 'u2', 'u3'],
          },
        }),
        '3 位成员已加入群聊',
      );
    },
  );

  test('explicit non-placeholder group digest remains authoritative', () {
    expect(
      groupSystemEventDisplayText(const {
        'event': 'group.profile.updated',
        'digest': '群名称已修改为「周会群」',
      }),
      '群名称已修改为「周会群」',
    );
  });
}
