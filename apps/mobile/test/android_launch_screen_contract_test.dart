import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('Android launch colors match light and dark page backgrounds', () {
    expect(
      File('android/app/src/main/res/values/colors.xml').readAsStringSync(),
      contains('<color name="launch_background">#F2F5F8</color>'),
    );
    expect(
      File(
        'android/app/src/main/res/values-night/colors.xml',
      ).readAsStringSync(),
      contains('<color name="launch_background">#0F1923</color>'),
    );
  });

  test('Android 原生启动层跟随亮暗背景且品牌只由 Flutter 启动页绘制', () {
    for (final path in [
      'android/app/src/main/res/drawable/launch_background.xml',
      'android/app/src/main/res/drawable-v21/launch_background.xml',
    ]) {
      final source = File(path).readAsStringSync();
      expect(source, contains('@color/launch_background'));
      expect(source, isNot(contains('@drawable/splash_logo')));
      expect(source, isNot(contains('<bitmap')));
    }

    for (final path in [
      'android/app/src/main/res/values-v31/styles.xml',
      'android/app/src/main/res/values-night-v31/styles.xml',
    ]) {
      final source = File(path).readAsStringSync();
      expect(source, contains('@android:color/transparent'));
      expect(source, isNot(contains('@drawable/splash_logo_android12')));
    }
  });
}
