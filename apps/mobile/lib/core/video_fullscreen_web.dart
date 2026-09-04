import 'dart:js_interop';
import 'package:web/web.dart' as web;

Future<void> setVideoFullscreen(bool enabled) async {
  if (enabled) {
    await web.document.documentElement?.requestFullscreen().toDart;
  } else if (web.document.fullscreenElement != null) {
    await web.document.exitFullscreen().toDart;
  }
}
