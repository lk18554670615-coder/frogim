import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('Android 14 屏幕共享固定选择当前完整屏幕而不是历史应用任务', () {
    final pubspec = File('pubspec.yaml').readAsStringSync();
    expect(
      pubspec,
      contains('path: ../../third_party/flutter_webrtc-1.4.0-full-display'),
    );

    final source = File(
      '../../third_party/flutter_webrtc-1.4.0-full-display/'
      'android/src/main/java/com/cloudwebrtc/webrtc/GetUserMediaImpl.java',
    ).readAsStringSync();
    expect(
      source,
      contains('MediaProjectionConfig.createConfigForDefaultDisplay()'),
    );
    expect(source, contains('Build.VERSION_CODES.UPSIDE_DOWN_CAKE'));
  });
}
