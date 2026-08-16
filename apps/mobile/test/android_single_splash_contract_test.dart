import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'Android native launch layer stays blank before Flutter branded splash',
    () {
      for (final path in [
        'android/app/src/main/res/drawable/launch_background.xml',
        'android/app/src/main/res/drawable-v21/launch_background.xml',
      ]) {
        final source = File(path).readAsStringSync();
        expect(source, contains('@android:color/white'));
        expect(source, isNot(contains('splash_logo')));
        expect(source, isNot(contains('<bitmap')));
      }

      for (final path in [
        'android/app/src/main/res/values-v31/styles.xml',
        'android/app/src/main/res/values-night-v31/styles.xml',
      ]) {
        final source = File(path).readAsStringSync();
        expect(
          source,
          contains(
            '<item name="android:windowSplashScreenAnimatedIcon">'
            '@android:color/transparent</item>',
          ),
        );
        expect(source, isNot(contains('splash_logo_android12')));
      }
    },
  );

  test('Flutter owns the only branded launch state with progress and copy', () {
    final source = File('lib/main.dart').readAsStringSync();
    expect(source, contains("class _LaunchScreen extends StatelessWidget"));
    expect(
      source,
      contains("'assets/brand/qingwaguagua-mark-transparent.png'"),
    );
    expect(source, contains('CircularProgressIndicator('));
    expect(source, contains("'正在启动，请稍候…'"));
  });
}
