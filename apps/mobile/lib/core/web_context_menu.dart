import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

Future<void> configureWebContextMenu() async {
  if (!kIsWeb) return;
  // Keep Flutter's message actions and text editing menus, not the browser's
  // overlapping native menu. Do not swallow secondary-pointer gestures.
  await BrowserContextMenu.disableContextMenu();
}
