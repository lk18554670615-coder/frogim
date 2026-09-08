import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

const _bundleId = 'top.hongjinghuanqiu.app';

String _source(String path) => File(path).readAsStringSync();

void main() {
  test(
    'Android identity matches application components and Kotlin packages',
    () {
      final gradle = _source('android/app/build.gradle.kts');
      expect(gradle, contains('namespace = "$_bundleId"'));
      expect(gradle, contains('applicationId = "$_bundleId"'));
      final manifest = _source('android/app/src/main/AndroidManifest.xml');
      for (final name in [
        'LinliApplication',
        'MainActivity',
        'LinliCallIntentService',
        'LinliScreenShareService',
      ]) {
        expect(manifest, contains('android:name=".$name"'));
        expect(
          _source(
            'android/app/src/main/kotlin/top/hongjinghuanqiu/app/$name.kt',
          ),
          startsWith('package $_bundleId'),
        );
      }
    },
  );

  test('all iOS configurations and CI select the new application identity', () {
    final project = _source('ios/Runner.xcodeproj/project.pbxproj');
    final identifiers = RegExp(
      r'PRODUCT_BUNDLE_IDENTIFIER = ([^;]+);',
    ).allMatches(project).map((match) => match[1]).toList();
    expect(identifiers.where((id) => id == _bundleId), hasLength(3));
    expect(
      identifiers.where((id) => id == '$_bundleId.RunnerTests'),
      hasLength(3),
    );
    expect(identifiers, hasLength(6));
    final profiles = RegExp(r'PROVISIONING_PROFILE_SPECIFIER = ([^;]+);')
        .allMatches(project)
        .map((match) => match[1]!.replaceAll('"', '').trim())
        .where((name) => name.isNotEmpty);
    expect(profiles, orderedEquals([_bundleId, _bundleId]));
    expect(
      _source('../../.github/workflows/ios-build.yml'),
      contains('IOS_BUNDLE_ID: $_bundleId'),
    );
  });

  test(
    'Dart and native method channels keep matching after package rename',
    () {
      const android = 'android/app/src/main/kotlin/top/hongjinghuanqiu/app';
      final swift = _source('ios/Runner/AppDelegate.swift');
      for (final entry in {
        'message_feedback': (
          'lib/core/message_feedback.dart',
          'LinliMessageFeedback.kt',
        ),
        'screenshot': ('lib/core/screenshot_detection.dart', 'MainActivity.kt'),
        'system_calls': (
          'lib/calls/system_call_service_native.dart',
          'MainActivity.kt',
        ),
        'screen_share': ('lib/calls/call_media_engine.dart', 'MainActivity.kt'),
      }.entries) {
        final channel = '$_bundleId/${entry.key}';
        expect(_source(entry.value.$1), contains(channel));
        expect(_source('$android/${entry.value.$2}'), contains(channel));
        if (entry.key == 'message_feedback' || entry.key == 'screenshot') {
          expect(swift, contains(channel));
        }
      }
    },
  );
}
