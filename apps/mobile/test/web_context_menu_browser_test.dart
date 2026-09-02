@TestOn('browser')
library;

import 'dart:ui_web' as ui_web;

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:linli_im/core/web_context_menu.dart';
import 'package:web/web.dart' as web;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('Web 启动配置阻止真实 DOM 原生右键菜单', () async {
    // The Flutter test runner ignores engine platform messages by default.
    // Enable real channel handling here to verify the DOM, not just a mock.
    final environment = ui_web.TestEnvironment.instance;
    ui_web.TestEnvironment.setUp(
      ui_web.TestEnvironment(
        forceTestFonts: environment.forceTestFonts,
        disableFontFallbacks: environment.disableFontFallbacks,
        keepSemanticsDisabledOnUpdate:
            environment.keepSemanticsDisabledOnUpdate,
        defaultToTestUrlStrategy: environment.defaultToTestUrlStrategy,
      ),
    );
    addTearDown(() => ui_web.TestEnvironment.setUp(environment));
    addTearDown(
      () => BrowserContextMenu.enableContextMenu().timeout(
        const Duration(seconds: 5),
      ),
    );
    await configureWebContextMenu().timeout(const Duration(seconds: 5));
    expect(BrowserContextMenu.enabled, isFalse);

    final view = web.document.querySelector('flutter-view');
    expect(view, isNotNull);
    final event = web.MouseEvent(
      'contextmenu',
      web.MouseEventInit(bubbles: true, cancelable: true, button: 2),
    );
    view!.dispatchEvent(event);
    expect(event.defaultPrevented, isTrue);
  });
}
