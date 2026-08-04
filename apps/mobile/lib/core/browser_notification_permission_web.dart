import 'dart:js_interop';

import 'package:web/web.dart' as web;

enum BrowserNotificationPermission { unsupported, prompt, granted, denied }

bool get browserNotificationsAvailable {
  try {
    return web.window.isSecureContext && web.Notification.permission.isNotEmpty;
  } catch (_) {
    return false;
  }
}

BrowserNotificationPermission _permission(String value) => switch (value) {
  'granted' => BrowserNotificationPermission.granted,
  'denied' => BrowserNotificationPermission.denied,
  _ => BrowserNotificationPermission.prompt,
};

Future<BrowserNotificationPermission> browserNotificationPermission() async =>
    browserNotificationsAvailable
    ? _permission(web.Notification.permission)
    : BrowserNotificationPermission.unsupported;

Future<BrowserNotificationPermission>
requestBrowserNotificationPermission() async {
  if (!browserNotificationsAvailable) {
    return BrowserNotificationPermission.unsupported;
  }
  final result = await web.Notification.requestPermission().toDart;
  return _permission(result.toDart);
}
