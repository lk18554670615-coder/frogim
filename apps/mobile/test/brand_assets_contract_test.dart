import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('page logos use a blue badge instead of white on transparent', () {
    for (final path in [
      'lib/main.dart',
      'lib/ui/screens/login_screen.dart',
      'lib/ui/screens/qr_tools_screen.dart',
    ]) {
      final source = File(path).readAsStringSync();
      expect(source, contains('assets/brand/qingwaguagua-badge.png'));
      expect(source, isNot(contains('qingwaguagua-mark-transparent.png')));
    }
  });

  test('icon regeneration keeps page colors and a separate monochrome mask', () {
    final config = File('pubspec.yaml').readAsStringSync();
    expect(config, contains('adaptive_icon_background: "#1976B9"'));
    expect(
      config,
      contains(
        'adaptive_icon_monochrome: assets/brand/qingwaguagua-adaptive-monochrome.png',
      ),
    );
    expect(config, contains('background_color: "#F2F5F8"'));
    expect(config, contains('theme_color: "#FFFFFF"'));
    expect(config, isNot(contains('#123B32')));
    final manifest =
        jsonDecode(File('web/manifest.json').readAsStringSync())
            as Map<String, dynamic>;
    expect(manifest['background_color'], '#F2F5F8');
    expect(manifest['theme_color'], '#FFFFFF');
    expect(
      File('android/app/src/main/res/values/colors.xml').readAsStringSync(),
      contains('<color name="ic_launcher_background">#1976B9</color>'),
    );
  });

  test('all logo sources and platform catalogs resolve locally', () {
    for (final name in [
      'badge',
      'icon',
      'avatar',
      'mark-transparent',
      'adaptive-foreground',
      'adaptive-monochrome',
    ]) {
      expect(File('assets/brand/qingwaguagua-$name.png').existsSync(), isTrue);
    }
    for (final platform in ['ios', 'macos']) {
      final catalog = '$platform/Runner/Assets.xcassets/AppIcon.appiconset';
      final contents =
          jsonDecode(File('$catalog/Contents.json').readAsStringSync())
              as Map<String, dynamic>;
      for (final item in contents['images'] as List) {
        if (item['filename'] != null) {
          expect(File('$catalog/${item['filename']}').existsSync(), isTrue);
        }
      }
    }
  });
}
