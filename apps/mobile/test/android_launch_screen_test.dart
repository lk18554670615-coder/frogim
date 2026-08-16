import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('Android 冷启动窗口不重复绘制 Flutter 品牌启动页', () {
    final legacy = File(
      'android/app/src/main/res/drawable/launch_background.xml',
    ).readAsStringSync();
    final legacyV21 = File(
      'android/app/src/main/res/drawable-v21/launch_background.xml',
    ).readAsStringSync();
    final android12 = File(
      'android/app/src/main/res/values-v31/styles.xml',
    ).readAsStringSync();
    final android12Night = File(
      'android/app/src/main/res/values-night-v31/styles.xml',
    ).readAsStringSync();

    expect(legacy, isNot(contains('@drawable/splash_logo')));
    expect(legacyV21, isNot(contains('@drawable/splash_logo')));
    expect(android12, isNot(contains('@drawable/splash_logo_android12')));
    expect(android12Night, isNot(contains('@drawable/splash_logo_android12')));
    expect(android12, contains('@android:color/transparent'));
    expect(android12Night, contains('@android:color/transparent'));
  });
}
