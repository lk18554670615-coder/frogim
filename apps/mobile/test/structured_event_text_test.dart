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
}
