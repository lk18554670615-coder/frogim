import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('Android 原生启动层保持纯白且品牌只由 Flutter 启动页绘制', () {
    for (final path in [
      'android/app/src/main/res/drawable/launch_background.xml',
      'android/app/src/main/res/drawable-v21/launch_background.xml',
    ]) {
      final source = File(path).readAsStringSync();
      expect(source, contains('@android:color/white'));
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
